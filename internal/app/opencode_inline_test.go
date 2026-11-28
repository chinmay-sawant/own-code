package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agent"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

type inlineFake struct {
	reply string
	got   agent.Request
}

func (p *inlineFake) Complete(ctx context.Context, req agent.Request) (agent.Result, error) {
	return p.Stream(ctx, req, func(agent.Event) {})
}

func (p *inlineFake) Stream(ctx context.Context, req agent.Request, emit func(agent.Event)) (agent.Result, error) {
	p.got = req
	emit(agent.Event{Kind: agent.EventTextDelta, Text: p.reply})
	return agent.Result{Status: agent.StatusCompleted, Text: p.reply}, nil
}

// inlineEditorApp opens main.go in edit mode with the given selection.
func inlineEditorApp(t *testing.T, content string, from, to int, prov agent.Provider) *App {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "main.go", content)
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.openFile("main.go")
	a.toggleEdit()
	if !a.editing || a.edit == nil {
		t.Fatal("not editing")
	}
	a.editAnchor, a.editCaret = from, to
	a.openCode.connected = true
	a.openCode.testProvider = prov
	a.openCode.consoleModels = []opencode.ConsoleModel{{ID: "m", ProviderID: "p", Type: "chat", ToolsKnown: true, ToolsSupported: true,
		APIs: map[string]opencode.ConsoleModelAPI{"chat-completions": {BaseURL: "https://x.example/v1"}}}}
	a.settings.OpenCode = &opencodeSettings{ProviderID: "p", ModelID: "m"}
	return a
}

func inlineWait(t *testing.T, a *App, phase openCodeInlinePhase) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.receiveOpenCode(context.Background())
		if a.openCode.inline != nil && a.openCode.inline.phase == phase {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("phase never reached %s: %+v", phase, a.openCode.inline)
}

func TestInlineAcceptOneUndoableOperationAndNoMutationWhileRunning(t *testing.T) {
	ctx := context.Background()
	prov := &inlineFake{reply: "```go\nreturn 2\n```"}
	content := "func f() int {\n\treturn 1\n}\n"
	from := strings.Index(content, "return 1")
	a := inlineEditorApp(t, content, from, from+len("return 1"), prov)
	a.edit.SetText(content + "// dirty\n") // pre-existing unsaved edit
	content = a.edit.Text()
	a.dirty[a.edit.Path] = true
	a.editAnchor, a.editCaret = from, from+len("return 1")
	if err := a.beginOpenCodeInline(ctx); err != nil {
		t.Fatal(err)
	}
	in := a.openCode.inline
	if in == nil || in.phase != inlineDraft || in.snap.SelectionText != "return 1" || in.snap.Version != inlineDocVersion(content) || in.snap.WorkspaceRoot == "" {
		t.Fatalf("draft %+v", in)
	}
	in.instruction = "make it two"
	in.removed[openCodeInlineCtxLines] = true
	if err := a.openCodeInlineAction(ctx, "opencode-panel-inline-send"); err != nil {
		t.Fatal(err)
	}
	inlineWait(t, a, inlineReady)
	// Exact context: surrounding lines were removed; no tools advertised.
	user := prov.got.Messages[1].Content
	if strings.Contains(user, "Surrounding") || !strings.Contains(user, "return 1") || len(prov.got.Tools) != 0 {
		t.Fatalf("request %q tools=%v", user, prov.got.Tools)
	}
	if a.edit.Text() != content || a.editAnchor != from || a.editCaret != from+8 || len(a.editUndo) != 0 {
		t.Fatal("document state changed during request")
	}
	if err := a.openCodeInlineAction(ctx, "opencode-panel-inline-accept"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(content, "return 1", "return 2", 1)
	if a.edit.Text() != want || !a.dirty[a.edit.Path] || a.openCode.inline != nil {
		t.Fatalf("accept: %q", a.edit.Text())
	}
	if len(a.editUndo) != 1 {
		t.Fatalf("undo points = %d", len(a.editUndo))
	}
	if err := a.onUndo(ctx); err != nil {
		t.Fatal(err)
	}
	if a.edit.Text() != content {
		t.Fatalf("undo: %q", a.edit.Text())
	}
}

func TestInlineStaleRefusedAndRejectLeavesDocument(t *testing.T) {
	ctx := context.Background()
	prov := &inlineFake{reply: "```\nB\n```"}
	a := inlineEditorApp(t, "A\nz\n", 0, 1, prov)
	_ = a.beginOpenCodeInline(ctx)
	a.openCode.inline.instruction = "x"
	_ = a.openCodeInlineAction(ctx, "opencode-panel-inline-send")
	inlineWait(t, a, inlineReady)
	a.edit.SetText("A\nz\nTYPED\n") // user typed during the request
	before := a.edit.Text()
	_ = a.openCodeInlineAction(ctx, "opencode-panel-inline-accept")
	if a.edit.Text() != before || a.openCode.inline == nil || a.openCode.inline.phase != inlineStale || len(a.editUndo) != 0 {
		t.Fatalf("stale apply: %q %+v", a.edit.Text(), a.openCode.inline)
	}
	_ = a.openCodeInlineAction(ctx, "opencode-panel-inline-dismiss")
	if a.openCode.inline != nil || a.edit.Text() != before {
		t.Fatal("dismiss changed state")
	}
}

func TestInlineCaretInsertAndEndings(t *testing.T) {
	ctx := context.Background()
	a := inlineEditorApp(t, "ab\n", 1, 1, &inlineFake{reply: "```\nXY\n```"})
	_ = a.beginOpenCodeInline(ctx)
	a.openCode.inline.instruction = "insert"
	_ = a.openCodeInlineAction(ctx, "opencode-panel-inline-send")
	inlineWait(t, a, inlineReady)
	if !strings.Contains(inlinePromptText(a.openCode.inline.snap, "i", nil), "<caret/>") {
		t.Fatal("no caret marker")
	}
	_ = a.openCodeInlineAction(ctx, "opencode-panel-inline-accept")
	if a.edit.Text() != "aXYb\n" || len(a.editUndo) != 1 {
		t.Fatalf("insert %q undo=%d", a.edit.Text(), len(a.editUndo))
	}
	// CRLF and final newline are preserved from the original selection.
	if got := matchInlineEnds("a\r\nb\r\n", "x\ny"); got != "x\r\ny\r\n" {
		t.Fatalf("crlf %q", got)
	}
	if got := matchInlineEnds("a\nb", "x\ny\n"); got != "x\ny" {
		t.Fatalf("final newline %q", got)
	}
}

func TestInlinePatchRejectsEmptyAndNoChange(t *testing.T) {
	snap := agentContextSnapshot{RelativePath: "a.go", SelectionText: "x"}
	if _, err := buildInlinePatch(snap, "```\n\n```"); err == nil {
		t.Fatal("empty accepted")
	}
	if _, err := buildInlinePatch(snap, "```\nx\n```"); err == nil {
		t.Fatal("no-op accepted")
	}
	if got := extractInlineReplacement("plain"); got != "plain" {
		t.Fatal(got)
	}
}
