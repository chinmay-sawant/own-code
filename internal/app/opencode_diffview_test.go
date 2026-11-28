package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/agentreview"
	"github.com/chinmay-sawant/own-code/internal/agenttools"
)

const hunkBase = "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"
const hunkProposed = "A\nb\nc\nd\ne\nf\ng\nh\nI\nj\n"

func hunkRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	file := filepath.Join(repo, "f.txt")
	if err := os.WriteFile(file, []byte(hunkBase), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", ".")
	e2eGit(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init")
	return repo, file
}

func agentRows(a *App) []diffRow {
	var out []diffRow
	for _, r := range a.diffRows {
		if r.AgentHunk != "" {
			out = append(out, r)
		}
	}
	return out
}

func TestAgentHunkAppliesToOpenBufferThroughUndoAndDirty(t *testing.T) {
	repo, file := hunkRepo(t)
	a := newProposalApp(t, repo)
	a.addAgentProposal("s1", e2eProposal(t, repo, "f.txt", hunkProposed))
	fr := a.agentReviewRegistry().Get("s1", "f.txt")
	if fr == nil || len(fr.Hunks) != 2 {
		t.Fatalf("review: %+v", fr)
	}
	a.openFilePreview("f.txt")
	a.beginEditAtLine(0)
	if !a.editing || a.edit == nil {
		t.Fatal("editor did not open")
	}
	if err := a.reviewOpenCodeHunk(agentHunkAccept, "f.txt", fr.Hunks[1].ID); err != nil {
		t.Fatal(err)
	}
	if got := a.edit.Text(); got != strings.Replace(hunkBase, "i\n", "I\n", 1) {
		t.Fatalf("buffer after accept = %q", got)
	}
	if !a.edit.Dirty || !a.dirty[a.edit.Path] {
		t.Fatal("accept must mark the buffer dirty")
	}
	if disk, _ := os.ReadFile(file); string(disk) != hunkBase {
		t.Fatal("accept into an open buffer must not write the file")
	}
	if err := a.onUndo(a.openCode.lifeCtx); err != nil {
		t.Fatal(err)
	}
	if got := a.edit.Text(); got != hunkBase {
		t.Fatalf("undo did not restore the buffer: %q", got)
	}
	// State stays accepted (model) but revert on the restored buffer conflicts
	// cleanly instead of corrupting it.
	if err := a.reviewOpenCodeHunk(agentHunkRevert, "f.txt", fr.Hunks[1].ID); err != nil {
		t.Fatal(err)
	}
	if a.edit.Text() != hunkBase {
		t.Fatalf("failed revert changed the buffer: %q", a.edit.Text())
	}
	if !strings.Contains(a.openCode.panelStatus, "Refused") {
		t.Fatalf("status = %q", a.openCode.panelStatus)
	}
}

func TestAgentDiffViewUnifiedAndSplitControlsAndErrors(t *testing.T) {
	repo, file := hunkRepo(t)
	a := newProposalApp(t, repo)
	a.addAgentProposal("s1", e2eProposal(t, repo, "f.txt", hunkProposed))
	fr := a.agentReviewRegistry().Get("s1", "f.txt")
	for _, split := range []bool{false, true} {
		a.diffSplit = split
		a.openAgentDiff("f.txt")
		if a.diffSource != "agent" {
			t.Fatalf("split=%v source=%q", split, a.diffSource)
		}
		rows := agentRows(a)
		if len(rows) != 2 {
			t.Fatalf("split=%v hunk rows=%d", split, len(rows))
		}
		for _, r := range rows {
			if !strings.Contains(r.Hunk, "Agent s1") || !strings.Contains(r.Hunk, "f.txt") || r.AgentState != "pending" || !r.AgentAccept || !r.AgentReject || r.AgentRevert {
				t.Fatalf("split=%v bad pending row %+v", split, r)
			}
		}
	}
	if err := a.agentHunkAction("accept:" + fr.Hunks[0].ID); err != nil {
		t.Fatal(err)
	}
	rows := agentRows(a)
	if rows[0].AgentState != "accepted" || !rows[0].AgentRevert || rows[0].AgentAccept {
		t.Fatalf("accepted row %+v", rows[0])
	}
	if disk, _ := os.ReadFile(file); !strings.HasPrefix(string(disk), "A\n") {
		t.Fatalf("disk %q", disk)
	}
	if err := a.agentHunkAction("reject:" + fr.Hunks[1].ID); err != nil {
		t.Fatal(err)
	}
	if got := agentRows(a)[1]; got.AgentState != "rejected" || got.AgentAccept || got.AgentRevert {
		t.Fatalf("rejected row %+v", got)
	}
	// Conflict: the user rewrote the accepted line; revert must refuse and show why.
	if err := os.WriteFile(file, []byte(strings.Replace(string(mustRead(t, file)), "A\n", "user\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.agentHunkAction("revert:" + fr.Hunks[0].ID); err != nil {
		t.Fatal(err)
	}
	if note := agentRows(a)[0].AgentNote; !strings.Contains(note, "overlap") {
		t.Fatalf("conflict note = %q", note)
	}
	if disk := string(mustRead(t, file)); !strings.HasPrefix(disk, "user\n") {
		t.Fatalf("conflicting revert changed the file: %q", disk)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAgentHunksMultiSessionStagedAndBackgroundDirtyBuffer(t *testing.T) {
	repo, file := hunkRepo(t)
	a := newProposalApp(t, repo)
	a.addAgentProposal("s1", e2eProposal(t, repo, "f.txt", hunkProposed))
	a.addAgentProposal("s2", e2eProposal(t, repo, "f.txt", strings.Replace(hunkBase, "a\n", "Z\n", 1)))
	r1 := a.agentReviewRegistry().Get("s1", "f.txt")
	r2 := a.agentReviewRegistry().Get("s2", "f.txt")
	if r1 == nil || r2 == nil || len(r2.Hunks) != 1 {
		t.Fatal("both sessions must hold independent reviews")
	}
	// The user stages an unrelated edit first.
	if err := os.WriteFile(file, []byte(strings.Replace(hunkBase, "e\n", "e staged\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", "f.txt")
	staged := e2eOut(t, repo, "diff", "--cached")
	a.openCode.selectedSessionID = "s1"
	if err := a.reviewOpenCodeHunk(agentHunkAccept, "f.txt", r1.Hunks[0].ID); err != nil {
		t.Fatal(err)
	}
	// s2 targets the same line s1 already changed: conflict, s2 stays pending.
	a.openCode.selectedSessionID = "s2"
	if err := a.reviewOpenCodeHunk(agentHunkAccept, "f.txt", r2.Hunks[0].ID); err != nil {
		t.Fatal(err)
	}
	if r2.States[0] != agentreview.Pending || !strings.Contains(a.openCode.panelStatus, "overlap") {
		t.Fatalf("s2 state=%v status=%q", r2.States[0], a.openCode.panelStatus)
	}
	if r1.States[0] != agentreview.Accepted {
		t.Fatal("s1 state leaked")
	}
	if got := e2eOut(t, repo, "diff", "--cached"); got != staged {
		t.Fatal("hunk actions changed the index")
	}
	if disk := string(mustRead(t, file)); !strings.Contains(disk, "e staged") || !strings.HasPrefix(disk, "A\n") {
		t.Fatalf("disk %q", disk)
	}
	// A dirty buffer in a background tab blocks disk writes.
	a.openCode.selectedSessionID = "s1"
	a.dirty["f.txt"] = true
	before := string(mustRead(t, file))
	if err := a.reviewOpenCodeHunk(agentHunkAccept, "f.txt", r1.Hunks[1].ID); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, file)) != before || r1.States[1] != agentreview.Pending || !strings.Contains(a.openCode.panelStatus, "unsaved") {
		t.Fatalf("dirty background buffer not protected: %q", a.openCode.panelStatus)
	}
}

func e2eOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return runGit(t, dir, args...)
}

func TestAgentProposalClassificationAndBaselineAttribution(t *testing.T) {
	repo, file := hunkRepo(t)
	a := newProposalApp(t, repo)
	bin := agenttools.Proposal{Path: "blob.bin", NewContent: "a\x00b", NewHash: "h1"}
	a.addAgentProposal("s1", bin)
	if rows := a.agentProposalRows("s1"); len(rows) != 1 || !strings.Contains(rows[0].Note, "binary") {
		t.Fatalf("binary note: %+v", rows)
	}
	if a.agentReviewRegistry().Get("s1", "blob.bin") != nil {
		t.Fatal("binary must not get hunk review")
	}
	big := agenttools.Proposal{Path: "big.txt", NewContent: strings.Repeat("x\n", agentreview.MaxReviewBytes), NewHash: "h2"}
	a.addAgentProposal("s1", big)
	if n := a.agentProposalRows("s1")[1].Note; !strings.Contains(n, "size limit") {
		t.Fatalf("oversized note %q", n)
	}
	if !strings.Contains(unsupportedChangeNote(agentreview.Delete), "whole-file") || !strings.Contains(unsupportedChangeNote(agentreview.Rename), "whole-file") {
		t.Fatal("delete/rename must be reported as whole-file units")
	}
	// Baseline: the user's staged edit and an unsaved buffer predate the turn.
	if err := os.WriteFile(file, []byte(hunkBase+"user\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", "f.txt")
	base, err := agentreview.CaptureBaseline(a.openCode.lifeCtx, repo, []string{"other.txt"})
	if err != nil {
		t.Fatal(err)
	}
	a.openCode.agentBaseline = map[string]*agentreview.Baseline{"s1": base}
	if n := a.userOwnedNote("s1", "f.txt"); !strings.Contains(n, "staged") {
		t.Fatalf("staged attribution %q", n)
	}
	if n := a.userOwnedNote("s1", "other.txt"); !strings.Contains(n, "unsaved") {
		t.Fatalf("dirty attribution %q", n)
	}
	if a.userOwnedNote("s1", "clean.txt") != "" || a.userOwnedNote("s2", "f.txt") != "" {
		t.Fatal("attribution must be per path and per session")
	}
	a.dirty["sub/x.go"] = true
	if got := a.dirtyRelPaths(repo); len(got) != 1 || got[0] != "sub/x.go" {
		t.Fatalf("dirtyRelPaths %v", got)
	}
}
