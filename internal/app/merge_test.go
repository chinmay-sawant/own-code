package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/merge"
)

const mergeTestPath = "conflict.txt"

func requireGit(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// mergeConflictRepo builds a repository parked in the middle of a conflicted
// merge of branch "theirs" into branch "ours".
func mergeConflictRepo(t *testing.T, name, base, ours, theirs string) string {
	t.Helper()
	requireGit(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.name", "Code View Test")
	runGit(t, dir, "config", "user.email", "code-view@example.com")
	runGit(t, dir, "config", "merge.conflictStyle", "diff3")

	writeFile(t, dir, name, base)
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "base")

	runGit(t, dir, "checkout", "-q", "-b", "ours")
	writeFile(t, dir, name, ours)
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "ours")

	runGit(t, dir, "checkout", "-q", "main")
	runGit(t, dir, "checkout", "-q", "-b", "theirs")
	writeFile(t, dir, name, theirs)
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "theirs")

	runGit(t, dir, "checkout", "-q", "ours")
	mergeBranch(t, dir, "theirs")

	return dir
}

// mergeBranch starts a merge that the fixture expects to conflict.
func mergeBranch(t *testing.T, dir, branch string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "-C", dir, "merge", "--no-edit", branch) //nolint:gosec // test helper runs the fixed git binary on a temp repo
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_MERGE_AUTOEDIT=no")

	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("merge unexpectedly clean:\n%s", out)
	}
}

// addAddConflictRepo builds an add/add conflict on one new path.
func addAddConflictRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.name", "Code View Test")
	runGit(t, dir, "config", "user.email", "code-view@example.com")

	writeFile(t, dir, "README.md", "base\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "base")

	runGit(t, dir, "checkout", "-q", "-b", "ours")
	writeFile(t, dir, "added.txt", "ours\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "ours adds")

	runGit(t, dir, "checkout", "-q", "main")
	runGit(t, dir, "checkout", "-q", "-b", "theirs")
	writeFile(t, dir, "added.txt", "theirs\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "theirs adds")

	runGit(t, dir, "checkout", "-q", "ours")
	mergeBranch(t, dir, "theirs")

	return dir
}

func textConflictRepo(t *testing.T) string {
	t.Helper()

	base := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\n"
	ours := "ours one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nours nine\n"
	theirs := "theirs one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\ntheirs nine\n"

	return mergeConflictRepo(t, mergeTestPath, base, ours, theirs)
}

func newMergeTestApp(t *testing.T, dir string) *App {
	t.Helper()

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	app.ReloadChanges(context.Background(), false)

	return app
}

func TestLoadLiveChangesReportsMergeConflicts(t *testing.T) {
	dir := textConflictRepo(t)

	changes, err := loadLiveChanges(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(changes.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(changes.Conflicts))
	}

	file := changes.Conflicts[0]
	if file.Path != mergeTestPath || !file.Unmerged || file.ConflictCode != "UU" {
		t.Fatalf("conflict = %+v, want UU conflict.txt", file)
	}

	if file.Conflicts != 2 {
		t.Fatalf("marker blocks = %d, want 2", file.Conflicts)
	}

	for name, list := range map[string][]changedFile{
		"files":    changes.Files,
		"staged":   changes.Staged,
		"unstaged": changes.Unstaged,
	} {
		if findFile(list, mergeTestPath) != nil {
			t.Fatalf("%s still lists the unmerged path", name)
		}
	}
}

func TestMergeEditorAcceptsChoices(t *testing.T) {
	dir := textConflictRepo(t)
	app := newMergeTestApp(t, dir)
	ctx := context.Background()

	if len(app.changes.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(app.changes.Conflicts))
	}

	app.setMode(ctx, "changes")
	if view := app.View(); !view.Diff.HasConflicts || view.Diff.ConflictCount != 1 ||
		len(view.Diff.Conflicts) != 1 || view.Diff.Conflicts[0].Path != mergeTestPath {
		t.Fatalf("source control conflicts = %+v", view.Diff.Conflicts)
	}

	if rows, ok := app.scmContextMenu("merge-conflict-" + mergeTestPath); !ok || len(rows) < 3 {
		t.Fatalf("conflict context menu = %+v, ok %v", rows, ok)
	}

	app.openMergeConflict(ctx, mergeTestPath)
	if app.mode != "merge" || !app.mergeOpen || len(app.mergeConflicts) != 2 {
		t.Fatalf("merge state = mode %q open %v conflicts %d", app.mode, app.mergeOpen, len(app.mergeConflicts))
	}

	if len(app.View().Merge.Rows) == 0 {
		t.Fatal("merge editor has no rows")
	}

	if rows := app.ContextMenuFor("merge-c-0"); len(rows) != 4 {
		t.Fatalf("merge context menu = %+v", rows)
	}

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("merge redraw: %v", err)
	}

	if !strings.Contains(app.mergeResult, "<<<<<<<") {
		t.Fatalf("result has no markers:\n%s", app.mergeResult)
	}

	app.mergeAccept(merge.Ours, 0)
	if !strings.Contains(app.mergeResult, "ours one") || !strings.Contains(app.mergeResult, "<<<<<<<") {
		t.Fatalf("ours accept result:\n%s", app.mergeResult)
	}

	if len(app.mergeConflicts) != 1 || !app.mergeDirty {
		t.Fatalf("after ours: conflicts %d dirty %v", len(app.mergeConflicts), app.mergeDirty)
	}

	app.openMergeConflict(ctx, mergeTestPath)
	app.mergeAccept(merge.Theirs, 0)
	if !strings.Contains(app.mergeResult, "theirs one") || strings.Contains(app.mergeResult, "ours one") {
		t.Fatalf("theirs accept result:\n%s", app.mergeResult)
	}

	if len(app.mergeConflicts) != 1 {
		t.Fatalf("after theirs: conflicts %d", len(app.mergeConflicts))
	}

	app.openMergeConflict(ctx, mergeTestPath)
	app.mergeAccept(merge.Both, 0)
	if !strings.Contains(app.mergeResult, "ours one") || !strings.Contains(app.mergeResult, "theirs one") {
		t.Fatalf("both accept result:\n%s", app.mergeResult)
	}

	if len(app.mergeConflicts) != 1 {
		t.Fatalf("after both: conflicts %d", len(app.mergeConflicts))
	}

	app.openMergeConflict(ctx, mergeTestPath)
	for len(app.mergeConflicts) > 0 {
		app.mergeAccept(merge.Ours, 0)
	}

	if merge.HasMarkers(app.mergeResult) {
		t.Fatalf("resolved result still has markers:\n%s", app.mergeResult)
	}

	if !app.View().Merge.CanStage {
		t.Fatal("resolved result should be stageable")
	}
}

func TestMergeStageRefusesMarkersThenSavesAndStages(t *testing.T) {
	dir := textConflictRepo(t)
	app := newMergeTestApp(t, dir)
	ctx := context.Background()

	before := runGit(t, dir, "rev-list", "--count", "HEAD")

	app.openMergeConflict(ctx, mergeTestPath)
	app.mergeAccept(merge.Ours, 0)

	if !merge.HasMarkers(app.mergeResult) {
		t.Fatal("one accepted conflict should leave the second unresolved")
	}

	app.stageMergeConflict(ctx)
	if !app.mergeOpen || app.mode != "merge" {
		t.Fatalf("marker stage should have been refused: mode %q open %v", app.mode, app.mergeOpen)
	}

	if !app.toast.Show || app.toast.Kind != "error" {
		t.Fatalf("toast = %+v, want an error", app.toast)
	}

	if out := runGit(t, dir, "ls-files", "-u", "--", mergeTestPath); out == "" {
		t.Fatal("conflict was staged despite remaining markers")
	}

	for len(app.mergeConflicts) > 0 {
		app.mergeAccept(merge.Theirs, 0)
	}

	if !app.saveMergeEditor(ctx) {
		t.Fatalf("save failed: %+v", app.toast)
	}

	if app.mergeDirty {
		t.Fatal("save should clear the dirty flag")
	}

	raw, err := os.ReadFile(filepath.Join(dir, mergeTestPath))
	if err != nil {
		t.Fatal(err)
	}

	text := string(raw)
	if merge.HasMarkers(text) || !strings.Contains(text, "ours one") || !strings.Contains(text, "theirs nine") {
		t.Fatalf("saved result:\n%s", text)
	}

	if out := runGit(t, dir, "ls-files", "-u", "--", mergeTestPath); out == "" {
		t.Fatalf("save cleared the unmerged index entries without an explicit stage")
	}

	app.stageMergeConflict(ctx)
	if app.mode != "changes" || app.mergeOpen {
		t.Fatalf("after stage: mode %q open %v", app.mode, app.mergeOpen)
	}

	if out := runGit(t, dir, "diff", "--cached", "--name-only"); !strings.Contains(out, mergeTestPath) {
		t.Fatalf("staged files = %q, want %s", out, mergeTestPath)
	}

	if out := runGit(t, dir, "ls-files", "-u", "--", mergeTestPath); out != "" {
		t.Fatalf("unmerged entries remain after stage:\n%s", out)
	}

	if after := runGit(t, dir, "rev-list", "--count", "HEAD"); after != before {
		t.Fatalf("commit count %s -> %s; staging must not commit", before, after)
	}
}

func TestMergeEditResultUsesExistingEditor(t *testing.T) {
	dir := textConflictRepo(t)
	app := newMergeTestApp(t, dir)
	ctx := context.Background()

	app.openMergeConflict(ctx, mergeTestPath)
	app.mergeAccept(merge.Ours, 0)

	before, err := os.ReadFile(filepath.Join(dir, mergeTestPath))
	if err != nil {
		t.Fatal(err)
	}

	app.editMergeResult()
	if app.mode != "explorer" || !app.editing || app.edit == nil {
		t.Fatalf("edit result state = mode %q editing %v edit %v", app.mode, app.editing, app.edit)
	}

	if !app.dirty[mergeTestPath] {
		t.Fatal("the accepted result should be dirty against the file on disk")
	}

	if text := app.edit.Text(); !strings.Contains(text, "ours one") || !strings.Contains(text, "<<<<<<<") {
		t.Fatalf("edit buffer:\n%s", text)
	}

	raw, err := os.ReadFile(filepath.Join(dir, mergeTestPath))
	if err != nil {
		t.Fatal(err)
	}

	if string(raw) != string(before) {
		t.Fatal("edit result wrote to disk before Save")
	}

	app.saveEdit()

	saved, err := os.ReadFile(filepath.Join(dir, mergeTestPath))
	if err != nil {
		t.Fatal(err)
	}

	if merge.Parse(string(saved)).Count() != 1 {
		t.Fatalf("saved edit should keep the remaining conflict marker:\n%s", saved)
	}

	app.ReloadChanges(ctx, false)

	app.openMergeConflict(ctx, mergeTestPath)
	if !app.mergeOpen || len(app.mergeConflicts) != 1 {
		t.Fatalf("reopened merge = open %v conflicts %d", app.mergeOpen, len(app.mergeConflicts))
	}

	if !strings.Contains(app.mergeResult, "ours one") {
		t.Fatalf("reopened result did not re-parse the saved text:\n%s", app.mergeResult)
	}
}

func TestMergeSaveRefusesExternalChange(t *testing.T) {
	dir := textConflictRepo(t)
	app := newMergeTestApp(t, dir)
	ctx := context.Background()

	app.openMergeConflict(ctx, mergeTestPath)
	app.mergeAccept(merge.Ours, 0)

	full := filepath.Join(dir, mergeTestPath)
	if err := os.WriteFile(full, []byte("external rewrite\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if app.saveMergeEditor(ctx) {
		t.Fatal("save overwrote a file changed on disk")
	}

	raw, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}

	if string(raw) != "external rewrite\n" {
		t.Fatalf("external text changed:\n%s", raw)
	}

	if !app.toast.Show || app.toast.Kind != "error" {
		t.Fatalf("toast = %+v, want an error", app.toast)
	}
}

func TestStageAllSkipsUnresolvedConflicts(t *testing.T) {
	dir := textConflictRepo(t)
	app := newMergeTestApp(t, dir)
	ctx := context.Background()

	writeFile(t, dir, "ordinary.txt", "ordinary\n")
	app.ReloadChanges(ctx, false)

	if err := app.stageAll(ctx); err != nil {
		t.Fatal(err)
	}

	if out := runGit(t, dir, "ls-files", "-u", "--", mergeTestPath); out == "" {
		t.Fatal("Stage All resolved the conflict")
	}

	if out := runGit(t, dir, "diff", "--cached", "--name-only"); !strings.Contains(out, "ordinary.txt") {
		t.Fatalf("ordinary file was not staged:\n%s", out)
	}
}

func TestMergeFallbacksDoNotPanic(t *testing.T) {
	t.Run("binary", func(t *testing.T) {
		base := "base\x00payload\n"
		ours := "ours\x00payload\n"
		theirs := "theirs\x00payload\n"
		dir := mergeConflictRepo(t, "blob.bin", base, ours, theirs)

		changes, err := loadLiveChanges(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}

		if len(changes.Conflicts) != 1 || !changes.Conflicts[0].Binary {
			t.Fatalf("binary conflicts = %+v", changes.Conflicts)
		}

		app := newMergeTestApp(t, dir)
		app.openMergeConflict(context.Background(), "blob.bin")

		if !app.mergeOpen || !app.mergeUnavailable {
			t.Fatalf("binary merge = open %v unavailable %v", app.mergeOpen, app.mergeUnavailable)
		}

		if app.View().Merge.CanStage {
			t.Fatal("binary fallback must not offer Mark Resolved & Stage")
		}
	})

	t.Run("add add", func(t *testing.T) {
		dir := addAddConflictRepo(t)

		changes, err := loadLiveChanges(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}

		if len(changes.Conflicts) != 1 || changes.Conflicts[0].ConflictCode != "AA" {
			t.Fatalf("add/add conflicts = %+v", changes.Conflicts)
		}

		app := newMergeTestApp(t, dir)
		app.openMergeConflict(context.Background(), "added.txt")
		if !app.mergeOpen || app.mergeUnavailable {
			t.Fatalf("add/add merge = open %v unavailable %v", app.mergeOpen, app.mergeUnavailable)
		}

		if app.mergeBaseSource != "none" {
			t.Fatalf("add/add base source = %q, want none", app.mergeBaseSource)
		}

		app.toggleMergeCompare()
		if err := app.Page().Redraw(context.Background()); err != nil {
			t.Fatalf("add/add compare redraw: %v", err)
		}
	})

	t.Run("missing working file", func(t *testing.T) {
		dir := textConflictRepo(t)
		app := newMergeTestApp(t, dir)

		if err := os.Remove(filepath.Join(dir, mergeTestPath)); err != nil {
			t.Fatal(err)
		}

		app.openMergeConflict(context.Background(), mergeTestPath)
		if !app.mergeOpen {
			t.Fatal("missing-file conflict did not open")
		}

		if app.mergeNote == "" {
			t.Fatal("missing-file conflict should explain the fallback")
		}
	})
}

func TestLiveChangesWithoutConflictsKeepsOrdinaryDiff(t *testing.T) {
	requireGit(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.name", "Code View Test")
	runGit(t, dir, "config", "user.email", "code-view@example.com")
	writeFile(t, dir, "notes.txt", "one\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "base")
	writeFile(t, dir, "notes.txt", "one\ntwo\n")

	changes, err := loadLiveChanges(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(changes.Conflicts) != 0 {
		t.Fatalf("conflicts = %d, want 0", len(changes.Conflicts))
	}

	if findFile(changes.Files, "notes.txt") == nil {
		t.Fatal("ordinary modified file missing")
	}

	app := newMergeTestApp(t, dir)
	app.setMode(context.Background(), "changes")

	if app.View().Diff.HasConflicts || app.View().Diff.ConflictCount != 0 {
		t.Fatalf("view conflicts = %v/%d", app.View().Diff.HasConflicts, app.View().Diff.ConflictCount)
	}

	if app.View().Diff.Selected != "notes.txt" {
		t.Fatalf("selected diff = %q", app.View().Diff.Selected)
	}
}
