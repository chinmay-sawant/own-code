package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOpenCodeProposalNameStatusRename(t *testing.T) {
	got, err := parseOpenCodeProposalNameStatus([]byte("R100\x00before.txt\x00after.txt\x00"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]openCodeProposalChange{
		"after.txt": {Kind: "renamed", RenamedFrom: "before.txt"},
	}
	if len(got) != len(want) || got["after.txt"] != want["after.txt"] {
		t.Fatalf("changes = %+v, want %+v", got, want)
	}
}

func TestParseOpenCodeProposalNameStatusRejectsUnsafeRename(t *testing.T) {
	cases := map[string][]byte{
		"missing destination": []byte("R100\x00before.txt\x00"),
		"outside source":      []byte("R100\x00../outside.txt\x00after.txt\x00"),
		"outside destination": []byte("R100\x00before.txt\x00../outside.txt\x00"),
		"duplicate endpoint":  []byte("R100\x00same.txt\x00same.txt\x00"),
		"source reused":       []byte("R100\x00before.txt\x00after.txt\x00D\x00before.txt\x00"),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseOpenCodeProposalNameStatus(input); err == nil {
				t.Fatal("parseOpenCodeProposalNameStatus() = nil, want refusal")
			}
		})
	}
}

func TestOpenCodeProposalSnapshotIncludesRename(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	const content = "first line\nsecond line\n"
	if err := os.WriteFile(filepath.Join(repo, "before.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", "before.txt")
	e2eGit(t, repo, "commit", "-q", "-m", "base")
	workspace, err := canonicalWorkspaceRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newOpenCodeWorktreeManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := manager.Create(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	e2eGit(t, worktree.Path(), "mv", "before.txt", "after.txt")

	snapshot, err := manager.snapshotProposal(ctx, worktree, worktree.baseCommit)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Files) != 1 {
		t.Fatalf("snapshot files = %+v, want one rename", snapshot.Files)
	}
	got := snapshot.Files[0]
	if got.Change != "renamed" || got.Path != "after.txt" || got.RenamedFrom != "before.txt" || got.SizeBytes != int64(len(content)) {
		t.Fatalf("rename snapshot = %+v", got)
	}
	if !strings.Contains(got.Diff, "rename from before.txt") || !strings.Contains(got.Diff, "rename to after.txt") {
		t.Fatalf("rename diff does not identify both paths: %q", got.Diff)
	}
	if _, err := os.Stat(filepath.Join(repo, "before.txt")); err != nil {
		t.Fatalf("snapshot modified checkout source: %v", err)
	}
}

func TestOpenCodeProposalSnapshotRejectsSymlinkRenameTarget(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	const content = "same bytes\n"
	if err := os.WriteFile(filepath.Join(repo, "before.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", "before.txt")
	e2eGit(t, repo, "commit", "-q", "-m", "base")
	workspace, err := canonicalWorkspaceRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newOpenCodeWorktreeManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := manager.Create(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	e2eGit(t, worktree.Path(), "mv", "before.txt", "after.txt")
	if err := os.Remove(filepath.Join(worktree.Path(), "after.txt")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(worktree.Path(), "after.txt")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	_, err = manager.snapshotProposal(ctx, worktree, worktree.baseCommit)
	if err == nil {
		t.Fatal("snapshot accepted rename target symlink")
	}
	if !errors.Is(err, errOpenCodeProposalUnsupported) {
		t.Fatalf("snapshot error = %v, want unsupported rename refusal", err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != content {
		t.Fatalf("outside target changed: %q (%v)", got, err)
	}
}
