package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/opencode"
	"github.com/chinmay-sawant/own-code/internal/termui"
	"github.com/chinmay-sawant/ownframe"
)

func displayContainsText(t *testing.T, a *App, needle string) bool {
	t.Helper()
	d := a.Page().Probe().Display()
	if d == nil {
		return false
	}
	for i := range d.Ops {
		if strings.Contains(d.Ops[i].Text, needle) {
			return true
		}
	}
	return false
}

func TestRetainedPatchHoverPath(t *testing.T) {
	a := newTestApp(t)
	defer a.Close()

	ctx := context.Background()
	if err := a.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	before := a.Page().Probe().Stats()
	genBefore := a.Page().Probe().Generation()

	if !a.patchHoverPath(ctx, "backend/server.go") {
		t.Fatal("patchHoverPath(empty -> non-empty) fell back instead of patching in place")
	}
	after := a.Page().Probe().Stats()
	if after.Parses != before.Parses {
		t.Fatalf("Parses incremented from %d to %d on hover patch", before.Parses, after.Parses)
	}
	if a.Page().Probe().Generation() <= genBefore {
		t.Fatal("Generation did not advance after hover patch")
	}
	if !displayContainsText(t, a, "backend/server.go") {
		t.Fatal("display does not contain patched hover path")
	}

	// Switch to a second path (non-empty -> non-empty).
	if !a.patchHoverPath(ctx, "web/app.ts") {
		t.Fatal("patchHoverPath(non-empty -> non-empty) fell back instead of patching in place")
	}
	if !displayContainsText(t, a, "web/app.ts") {
		t.Fatal("display does not contain updated hover path")
	}

	// Clear hover path (non-empty -> empty).
	if !a.patchHoverPath(ctx, "") {
		t.Fatal("patchHoverPath(non-empty -> empty) fell back instead of patching in place")
	}
	if a.Page().Probe().Stats().Parses != before.Parses {
		t.Fatalf("Parses changed during hover patches: got %d, want %d", a.Page().Probe().Stats().Parses, before.Parses)
	}
}

func TestRetainedPatchTerminalBody(t *testing.T) {
	a := newTestApp(t)
	defer a.Close()

	ctx := context.Background()
	a.termOpen = true
	a.termFocus = true
	a.refresh()
	a.view.Terminal = terminalView{
		Open:     true,
		Height:   termDefaultHeight,
		Focus:    true,
		Active:   1,
		Running:  true,
		Shell:    "bash",
		Sessions: []terminalTabRow{{ID: 1, Title: "bash", Running: true, Active: true}},
		Rows: []termui.Row{
			{Segments: []termui.Segment{{Class: "plain", Text: "$ echo hello"}}},
		},
	}
	a.page.SetData(&a.view)
	if err := a.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	before := a.Page().Probe().Stats()
	prev := a.view.Terminal
	next := prev
	next.Rows = []termui.Row{
		{Segments: []termui.Segment{{Class: "plain", Text: "$ echo hello"}}},
		{Segments: []termui.Segment{{Class: "plain", Text: "retained-terminal-chunk-42"}}},
	}
	a.view.Terminal = next

	if !a.patchTerminalBody(ctx, prev, next) {
		t.Fatal("patchTerminalBody fell back instead of patching #term-body in place")
	}
	after := a.Page().Probe().Stats()
	if after.Parses != before.Parses {
		t.Fatalf("Parses incremented from %d to %d on terminal body patch", before.Parses, after.Parses)
	}
	if !displayContainsText(t, a, "retained-terminal-chunk-42") {
		t.Fatal("display does not contain patched terminal output")
	}
}

func TestRetainedPatchOpenCodePanel(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Close()

	ctx := context.Background()
	a.openCode.panelOpen = true
	a.openCode.connected = true
	a.openCode.selectedSessionID = "sess-1"
	a.openCode.panelStatus = "Streaming response..."
	a.openCode.operation = openCodeOperationPrompt
	a.openCode.token = 7
	a.openCode.sessions = []opencode.Session{{
		ID:       "sess-1",
		Title:    "Patch Session",
		Location: opencode.LocationRef{Directory: a.ws.Root},
	}}
	a.openCode.messages = []openCodeTranscriptMessage{{
		ID:   "msg-1",
		Role: "assistant",
		Text: "chunk 1",
	}}
	a.refresh()
	if err := a.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	before := a.Page().Probe().Stats()
	a.openCode.results <- openCodeReply{
		token:             7,
		operation:         openCodeOperationPrompt,
		inProgress:        true,
		selectedSessionID: "sess-1",
		panelStatus:       "Streaming chunk 2...",
		messages: []openCodeTranscriptMessage{{
			ID:   "msg-1",
			Role: "assistant",
			Text: "chunk 1 + streamed-token-99",
		}},
	}

	if !a.receiveOpenCode(ctx) {
		t.Fatal("receiveOpenCode returned false")
	}
	after := a.Page().Probe().Stats()
	if after.Parses != before.Parses {
		t.Fatalf("Parses incremented from %d to %d on OpenCode streaming patch", before.Parses, after.Parses)
	}
	if !displayContainsText(t, a, "Streaming chunk 2...") {
		t.Fatal("display does not contain patched OpenCode status")
	}
	if !displayContainsText(t, a, "streamed-token-99") {
		t.Fatal("display does not contain patched OpenCode transcript")
	}
}

func TestRetainedPatchFallbackOnRefused(t *testing.T) {
	a, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Close()

	ctx := context.Background()

	// Before the first Redraw there is no retained display, so PatchElement
	// refuses with ErrPatchRefused and patchHoverPath must fall back cleanly.
	text := "fallback.go"
	if err := a.Page().PatchElement(ctx, "statusbar", ownframe.ElementEdit{ID: "sb-hover-path", Text: &text}); !errors.Is(err, ownframe.ErrPatchRefused) {
		t.Fatalf("PatchElement before first Redraw err = %v, want ErrPatchRefused", err)
	}
	if a.patchHoverPath(ctx, "backend/fallback.go") {
		t.Fatal("expected patchHoverPath to fall back before initial Redraw")
	}
	if err := a.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	if !displayContainsText(t, a, "backend/fallback.go") {
		t.Fatal("fallback Redraw did not paint hover path")
	}

	// Patching a missing element ID returns ErrPatchRefused and leaves the
	// page able to recover via a normal full Redraw.
	if err := a.Page().PatchElement(ctx, "nonexistent-retained-id", ownframe.ElementEdit{Text: &text}); !errors.Is(err, ownframe.ErrPatchRefused) {
		t.Fatalf("PatchElement(missing) err = %v, want ErrPatchRefused", err)
	}
	if err := a.Page().Redraw(ctx); err != nil {
		t.Fatalf("Redraw after refused patch failed: %v", err)
	}
}

func newBenchApp(b *testing.B) *App {
	b.Helper()
	a, err := New("")
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	if err := a.Page().Redraw(context.Background()); err != nil {
		b.Fatalf("Redraw: %v", err)
	}
	return a
}

func BenchmarkHoverPathFullRedraw(b *testing.B) {
	a := newBenchApp(b)
	defer a.Close()
	ctx := context.Background()
	paths := []string{"backend/server.go", "web/app.ts"}
	b.ResetTimer()
	for i := range b.N {
		a.hoverPath = paths[i%len(paths)]
		a.refresh()
		if err := a.Page().Redraw(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHoverPathRetainedPatch(b *testing.B) {
	a := newBenchApp(b)
	defer a.Close()
	ctx := context.Background()
	paths := []string{"backend/server.go", "web/app.ts"}
	b.ResetTimer()
	for i := range b.N {
		if !a.patchHoverPath(ctx, paths[i%len(paths)]) {
			b.Fatal("hover patch fell back during benchmark")
		}
	}
}

func BenchmarkTerminalOutputFullRedraw(b *testing.B) {
	a := newBenchApp(b)
	defer a.Close()
	ctx := context.Background()
	a.termOpen = true
	a.termFocus = true
	a.refresh()
	base := terminalView{
		Open:     true,
		Height:   termDefaultHeight,
		Focus:    true,
		Active:   1,
		Running:  true,
		Shell:    "bash",
		Sessions: []terminalTabRow{{ID: 1, Title: "bash", Running: true, Active: true}},
		Rows:     make([]termui.Row, 14),
	}
	for i := range base.Rows {
		base.Rows[i] = termui.Row{Segments: []termui.Segment{{Class: "plain", Text: fmt.Sprintf("line %02d: ok", i)}}}
	}
	a.view.Terminal = base
	a.page.SetData(&a.view)
	if err := a.Page().Redraw(ctx); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := range b.N {
		base.Rows[13] = termui.Row{Segments: []termui.Segment{{Class: "plain", Text: fmt.Sprintf("build step %d", i)}}}
		a.view.Terminal = base
		a.page.SetData(&a.view)
		if err := a.Page().Redraw(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTerminalOutputRetainedPatch(b *testing.B) {
	a := newBenchApp(b)
	defer a.Close()
	ctx := context.Background()
	a.termOpen = true
	a.termFocus = true
	a.refresh()
	base := terminalView{
		Open:     true,
		Height:   termDefaultHeight,
		Focus:    true,
		Active:   1,
		Running:  true,
		Shell:    "bash",
		Sessions: []terminalTabRow{{ID: 1, Title: "bash", Running: true, Active: true}},
		Rows:     make([]termui.Row, 14),
	}
	for i := range base.Rows {
		base.Rows[i] = termui.Row{Segments: []termui.Segment{{Class: "plain", Text: fmt.Sprintf("line %02d: ok", i)}}}
	}
	a.view.Terminal = base
	a.page.SetData(&a.view)
	if err := a.Page().Redraw(ctx); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := range b.N {
		prev := a.view.Terminal
		next := prev
		rows := make([]termui.Row, len(prev.Rows))
		copy(rows, prev.Rows)
		rows[13] = termui.Row{Segments: []termui.Segment{{Class: "plain", Text: fmt.Sprintf("build step %d", i)}}}
		next.Rows = rows
		a.view.Terminal = next
		if !a.patchTerminalBody(ctx, prev, next) {
			b.Fatal("terminal body patch fell back during benchmark")
		}
	}
}

func BenchmarkOpenCodeStreamFullRedraw(b *testing.B) {
	a, err := New(b.TempDir())
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	defer a.Close()
	ctx := context.Background()
	a.openCode.panelOpen = true
	a.openCode.connected = true
	a.openCode.selectedSessionID = "sess-1"
	a.openCode.operation = openCodeOperationPrompt
	a.openCode.panelStatus = "Streaming..."
	a.openCode.sessions = []opencode.Session{{
		ID:       "sess-1",
		Title:    "Patch Session",
		Location: opencode.LocationRef{Directory: a.ws.Root},
	}}
	a.openCode.messages = []openCodeTranscriptMessage{{
		ID:   "msg-1",
		Role: "assistant",
		Text: "initial",
	}}
	a.refresh()
	if err := a.Page().Redraw(ctx); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := range b.N {
		a.openCode.panelStatus = fmt.Sprintf("Streaming token %d...", i)
		a.openCode.messages = []openCodeTranscriptMessage{{
			ID:   "msg-1",
			Role: "assistant",
			Text: fmt.Sprintf("streamed assistant output token %d", i),
		}}
		a.refresh()
		if err := a.Page().Redraw(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpenCodeStreamRetainedPatch(b *testing.B) {
	a, err := New(b.TempDir())
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	defer a.Close()
	ctx := context.Background()
	a.openCode.panelOpen = true
	a.openCode.connected = true
	a.openCode.selectedSessionID = "sess-1"
	a.openCode.operation = openCodeOperationPrompt
	a.openCode.panelStatus = "Streaming..."
	a.openCode.sessions = []opencode.Session{{
		ID:       "sess-1",
		Title:    "Patch Session",
		Location: opencode.LocationRef{Directory: a.ws.Root},
	}}
	a.openCode.messages = []openCodeTranscriptMessage{{
		ID:   "msg-1",
		Role: "assistant",
		Text: "initial",
	}}
	a.refresh()
	if err := a.Page().Redraw(ctx); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := range b.N {
		prev := a.view.OpenCodePanel
		a.openCode.panelStatus = fmt.Sprintf("Streaming token %d...", i)
		a.openCode.messages = []openCodeTranscriptMessage{{
			ID:   "msg-1",
			Role: "assistant",
			Text: fmt.Sprintf("streamed assistant output token %d", i),
		}}
		next := a.openCodePanelView()
		a.view.OpenCodePanel = next
		if !a.patchOpenCodePanel(ctx, prev, next) {
			b.Fatal("opencode panel patch fell back during benchmark")
		}
	}
}
