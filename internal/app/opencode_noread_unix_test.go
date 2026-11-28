//go:build unix

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Plan 05: paths that appear in streamed agent text or tool events never cause
// own-code to read those files. FIFOs in the workspace act as tripwires: any
// open-for-read of one blocks forever, so the handler would hang and fail.
// Also checks that no context chip or mention is created from the output.
func TestAgentOutputPathsNeverTriggerReads(t *testing.T) {
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	for _, name := range []string{"trap.txt", "secret.env"} {
		if err := syscall.Mkfifo(filepath.Join(repo, name), 0o600); err != nil {
			t.Skipf("mkfifo unavailable: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "real.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newProposalApp(t, repo)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.openCode.runs = map[string]*openCodeRun{"s1": {sessionID: "s1", turnID: "t1", cancel: cancel}}

	done := make(chan struct{})
	go func() {
		defer close(done)
		text := "See trap.txt, ./secret.env, " + filepath.Join(repo, "trap.txt") + ", ../../etc/passwd and `real.go`:\n```\n@trap.txt\n```\n"
		evs := []openCodeRunEvent{
			{kind: openCodeRunDelta, text: text},
			{kind: openCodeRunToolStart, toolID: "c1", toolName: "read_file"},
			{kind: openCodeRunToolDone, toolID: "c1", toolName: "read_file", text: "read trap.txt secret.env failed"},
		}
		for i := range evs {
			evs[i].sessionID, evs[i].turnID = "s1", "t1"
			a.handleOpenCodeRunEvent(&evs[i])
		}
		_ = a.openCodePanelView() // renders the live text and tool rows
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handling agent output opened a workspace file named in it")
	}
	v := a.openCodePanelView()
	if len(v.Chips) != 0 || len(v.InlineChips) != 0 || strings.Contains(v.ComposerPreview, "package x") {
		t.Fatalf("agent output attached context: chips=%v preview=%q", v.Chips, v.ComposerPreview)
	}
}
