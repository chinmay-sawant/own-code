package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agentstore"
)

var _ openCodeSessionAdmin = (*agentstore.Store)(nil)

func TestPanelGapsStreamedUTF8AndEmptyDeltas(t *testing.T) {
	r := &openCodeRun{}
	if r.appendDelta("") {
		t.Fatal("empty delta changed the run")
	}
	euro := []byte("€") // e2 82 ac
	if r.appendDelta(string(euro[:1])) || r.appendDelta(string(euro[1:2])) {
		t.Fatal("partial rune rendered early")
	}
	if !r.appendDelta(string(euro[2:])+"x") || r.text != "€x" {
		t.Fatalf("text = %q", r.text)
	}
	if !r.appendDelta("\xff") || !strings.Contains(r.text, "�") {
		t.Fatalf("invalid byte not replaced: %q", r.text)
	}
}

func TestPanelGapsMarkdownEscapes(t *testing.T) {
	out := string(renderOpenCodeMarkdown("# T\n<script>alert(1)</script> hi\n- item\n```go\nx := \"<b>\"\n"))
	if strings.Contains(out, "<script>") || strings.Contains(out, "<b>") {
		t.Fatalf("unescaped HTML: %s", out)
	}
	for _, want := range []string{`class="md-h"`, `class="md-li"`, `class="md-code"`, "&lt;script&gt;"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
}

func TestPanelGapsTranscriptScrollFollowsOnlyAtEnd(t *testing.T) {
	s := &openCodeTranscriptScroll{}
	s.measure(100, 300)
	if s.offset() != 200 || !s.atEnd() {
		t.Fatalf("not following: %d", s.offset())
	}
	s.by(-60)
	if s.atEnd() || s.offset() != 140 {
		t.Fatalf("not detached at 140: %d", s.offset())
	}
	s.measure(100, 500) // new output arrives while reading
	if s.offset() != 140 {
		t.Fatalf("reading position moved: %d", s.offset())
	}
	s.by(10000)
	if !s.atEnd() || s.offset() != 400 {
		t.Fatalf("did not reattach at end: %d", s.offset())
	}
	s.measure(100, 600)
	if s.offset() != 500 {
		t.Fatal("did not follow after reattach")
	}
}

func TestPanelGapsLiveToolRowsAndTurnSummary(t *testing.T) {
	a := newRunTestApp(t)
	a.openCode.selectedSessionID = "s1"
	run := addRun(a, "s1", "t1", "go")
	run.startedAt, run.provider, run.model = time.Now().Add(-2*time.Second), "prov", "mod"
	a.handleOpenCodeRunEvent(&openCodeRunEvent{sessionID: "s1", turnID: "t1", kind: openCodeRunDelta, text: "before"})
	a.handleOpenCodeRunEvent(&openCodeRunEvent{sessionID: "s1", turnID: "t1", kind: openCodeRunToolStart, toolID: "c1", toolName: "read_file"})
	vis := openCodeVisibleMessages(nil, run)
	if len(vis) != 4 || vis[2].Role != "tool" || !strings.Contains(vis[2].Text, "read_file [running]") {
		t.Fatalf("live tool row missing: %+v", vis)
	}
	if vis[3].Role != "turn" || !strings.Contains(vis[3].Text, "prov/mod · Running") {
		t.Fatalf("turn line = %q", vis[3].Text)
	}
	a.handleOpenCodeRunEvent(&openCodeRunEvent{sessionID: "s1", turnID: "t1", kind: openCodeRunToolDone, toolID: "c1", toolName: "read_file", text: "succeeded"})
	a.handleOpenCodeRunEvent(&openCodeRunEvent{sessionID: "s1", turnID: "t1", kind: openCodeRunDelta, text: "after"})
	vis = openCodeVisibleMessages(nil, run)
	if !strings.Contains(vis[2].Text, "[succeeded]") || vis[3].Text != "after" {
		t.Fatalf("ordering wrong: %+v", vis)
	}
	st := time.Now()
	line := openCodeTurnSummary(agentstore.Turn{ProviderID: "p", ModelID: "m", Status: agentstore.TurnFailed, StartedAt: st, EndedAt: st.Add(1500 * time.Millisecond)})
	if line != "p/m · Failed · 1.5s" {
		t.Fatalf("summary = %q", line)
	}
}

func TestPanelGapsOfflineNoticeAndWorkspaceSwitchConfirmation(t *testing.T) {
	a := newRunTestApp(t)
	a.openCode.connected = false
	if n := a.openCodeRequestNotice(); !strings.Contains(n, "Console credential") || !strings.Contains(n, "model") {
		t.Fatalf("notice = %q", n)
	}
	root := a.ws.Root
	a.openCode.allSessions = []agentstore.Session{{ID: "other", Title: "Elsewhere", WorkspaceRoot: t.TempDir()}}
	if rows := a.openCodeOtherWorkspaceSessions(); len(rows) != 1 {
		t.Fatalf("other rows = %+v", rows)
	}
	if err := a.requestOpenCodeWorkspaceSwitch("other"); err != nil {
		t.Fatal(err)
	}
	if a.openCode.pendingSwitch == nil || a.ws.Root != root || a.openCode.selectedSessionID != "" {
		t.Fatal("selecting another workspace's session switched silently")
	}
	a.openCode.panelOpen = true
	if v := a.openCodePanelView(); v.PendingSwitch == nil || !v.Offline || v.Notice == "" {
		t.Fatalf("view = %+v", v)
	}
	a.cancelOpenCodeWorkspaceSwitch()
	if a.openCode.pendingSwitch != nil || a.ws.Root != root {
		t.Fatal("cancel changed state")
	}
}

func TestPanelGapsRenameArchiveAndPanelRenders(t *testing.T) {
	a := newRunTestApp(t)
	ctx := context.Background()
	if err := a.ensureOpenCodeStore(); err != nil {
		t.Fatal(err)
	}
	root, err := canonicalWorkspaceRoot(a.ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.openCode.store.CreateSession(ctx, agentstore.Session{ID: "s-gap", WorkspaceRoot: root, Title: "Old", Agent: "opencode", Status: agentstore.SessionQueued}); err != nil {
		t.Fatal(err)
	}
	a.openCode.panelOpen = true
	if err := a.loadOpenCodeSessions(); err != nil {
		t.Fatal(err)
	}
	waitPanel := func(cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			a.receiveOpenCode(ctx)
			if cond() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for panel: status=%q", a.openCode.panelStatus)
	}
	waitPanel(func() bool { return len(a.openCodePanelView().Sessions) == 1 })
	a.startOpenCodeRename("s-gap")
	if err := a.page.Redraw(ctx); err != nil { // template renders the inline editor
		t.Fatal(err)
	}
	a.page.Form().SetFormValue("opencode-rename", "  New   title ")
	if err := a.saveOpenCodeRename("s-gap"); err != nil {
		t.Fatal(err)
	}
	waitPanel(func() bool { s := a.openCodePanelView().Sessions; return len(s) == 1 && s[0].Title == "New title" })
	if err := a.archiveOpenCodeSession("s-gap"); err != nil {
		t.Fatal(err)
	}
	waitPanel(func() bool { v := a.openCodePanelView(); return len(v.Sessions) == 0 && len(v.Archived) == 1 })
	if err := a.unarchiveOpenCodeSession("s-gap"); err != nil {
		t.Fatal(err)
	}
	waitPanel(func() bool { v := a.openCodePanelView(); return len(v.Sessions) == 1 && len(v.Archived) == 0 })
}
