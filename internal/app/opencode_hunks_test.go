package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHunkReviewAcceptRejectRevertOnDiskKeepsUserLines(t *testing.T) {
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	base := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"
	file := filepath.Join(repo, "f.txt")
	if err := os.WriteFile(file, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", ".")
	e2eGit(t, repo, "commit", "-qm", "init")
	a := newProposalApp(t, repo)
	p := e2eProposal(t, repo, "f.txt", "A\nb\nc\nd\ne\nf\ng\nh\nI\nj\n")
	a.addAgentProposal("s1", p)
	fr := a.agentReviewRegistry().Get("s1", "f.txt")
	if fr == nil || len(fr.Hunks) != 2 {
		t.Fatalf("review not recorded: %+v", fr)
	}
	// User edits a line next to the second hunk after the proposal was made.
	if err := os.WriteFile(file, []byte(strings.Replace(base, "h\n", "h user\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.reviewOpenCodeHunk(agentHunkAccept, "f.txt", fr.Hunks[1].ID); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	if string(got) != strings.Replace(strings.Replace(base, "h\n", "h user\n", 1), "i\n", "I\n", 1) {
		t.Fatalf("accept changed more than the hunk: %q", got)
	}
	if err := a.reviewOpenCodeHunk(agentHunkReject, "f.txt", fr.Hunks[0].ID); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(got) {
		t.Fatal("reject must not touch the file")
	}
	if err := a.reviewOpenCodeHunk(agentHunkRevert, "f.txt", fr.Hunks[1].ID); err != nil {
		t.Fatal(err)
	}
	rev, _ := os.ReadFile(file)
	if string(rev) != strings.Replace(base, "h\n", "h user\n", 1) {
		t.Fatalf("revert lost user line: %q", rev)
	}
	// Nothing was staged by review actions.
	e2eGit(t, repo, "diff", "--cached", "--quiet")
}
