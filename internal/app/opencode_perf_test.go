package app

import (
	"context"
	"strings"
	"testing"
)

func BenchmarkOpenCodePanelRedrawWithAttachmentAndStream(b *testing.B) {
	a := newRunTestApp(&testing.T{})
	ctx := context.Background()
	a.Page().SetSize(1440, 900)
	a.openCode.panelOpen = true
	a.openCode.connected = true
	a.openCode.selectedSessionID = "s-bench"

	// Simulate an attached 32KB file in the composer + a user message that
	// already sent a 32KB file + a multi-turn assistant transcript.
	bigFile := strings.Repeat("func exampleLine(x int) int { return x*2 + 1 }\n", 650)
	chip := openCodeChip{
		ID: "c1", Kind: openCodeChipFile, Label: "large.go", Path: "internal/app/large.go",
		Detail: "lines 1-650", Version: "abc123def456", Text: bigFile, StartLine: 1, EndLine: 650,
	}
	a.openCode.composer.chips = []openCodeChip{chip}
	sentPrompt := "Please review this file" + renderOpenCodeContextBlock([]openCodeChip{chip})
	a.openCode.messages = []openCodeTranscriptMessage{
		{ID: "m1", Role: "user", Text: sentPrompt, Kind: "text"},
		{ID: "m2", Role: "assistant", Text: strings.Repeat("## Analysis\n- Checked function behavior and edge cases.\n```go\nfunc foo() { return }\n```\n", 12), Kind: "text"},
	}

	a.refresh()
	if err := a.Page().Redraw(ctx); err != nil {
		b.Fatal(err)
	}

	run := addRun(a, "s-bench", "t-bench", sentPrompt)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		run.appendDelta("token ")
		prev := a.view.OpenCodePanel
		next := a.openCodePanelView()
		a.view.OpenCodePanel = next
		if !a.patchOpenCodePanel(ctx, prev, next) {
			a.refresh()
			if err := a.Page().Redraw(ctx); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func TestOpenCodeAttachedFileDoesNotPrintFullContentsInUI(t *testing.T) {
	a := newRunTestApp(t)
	a.openCode.panelOpen = true
	a.openCode.connected = true
	a.openCode.selectedSessionID = "s-attach"

	secretBody := "UNIQUE_FULL_FILE_BODY_LINE_SHOULD_NOT_PRINT_IN_CHAT"
	chip := openCodeChip{
		ID: "c1", Kind: openCodeChipFile, Label: "large.go", Path: "internal/app/large.go",
		Detail: "lines 1-650", Version: "abc123def456", Text: secretBody, StartLine: 1, EndLine: 650,
	}
	a.openCode.composer.chips = []openCodeChip{chip}
	sentPrompt := "Please review this file" + renderOpenCodeContextBlock([]openCodeChip{chip})
	a.openCode.messages = []openCodeTranscriptMessage{
		{ID: "m1", Role: "user", Text: sentPrompt, Kind: "text"},
	}

	v := a.openCodePanelView()
	if strings.Contains(v.ComposerPreview, secretBody) {
		t.Fatalf("ComposerPreview leaked full file contents: %q", v.ComposerPreview)
	}
	if !strings.Contains(v.ComposerPreview, "internal/app/large.go") {
		t.Fatalf("ComposerPreview missing summary: %q", v.ComposerPreview)
	}
	if len(v.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(v.Messages))
	}
	if strings.Contains(string(v.Messages[0].Body), secretBody) || strings.Contains(v.Messages[0].Text, secretBody) {
		t.Fatalf("transcript user message printed full attached file contents: %+v", v.Messages[0])
	}
	if !strings.Contains(v.Messages[0].Text, "Please review this file") {
		t.Fatalf("user prompt text missing: %q", v.Messages[0].Text)
	}
	if !strings.Contains(v.Messages[0].Note, "internal/app/large.go") {
		t.Fatalf("expected attachment summary note, got %q", v.Messages[0].Note)
	}
}
