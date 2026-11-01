package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

// twoHunkRepo builds a repo where notes.txt carries two separated edits, so
// the combined HEAD diff reports exactly two hunks.
func twoHunkRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.name", "Code View Test")
	runGit(t, dir, "config", "user.email", "code-view@example.com")

	lines := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve"}
	writeFile(t, dir, "notes.txt", strings.Join(lines, "\n")+"\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "base")

	lines[1] = "TWO"
	lines[10] = "ELEVEN"
	writeFile(t, dir, "notes.txt", strings.Join(lines, "\n")+"\n")

	return dir
}

func newTwoHunkApp(t *testing.T, dir string) *App {
	t.Helper()

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	app.ReloadChanges(ctx, false)
	app.selectDiff("notes.txt")

	if len(app.changes.Files) != 1 || len(app.changes.Files[0].Hunks) != 2 {
		t.Fatalf("fixture hunks = %+v, want 2 hunks in one file", app.changes.Files)
	}

	return app
}

func stagedDiff(t *testing.T, dir string) string {
	t.Helper()

	return runGit(t, dir, "diff", "--cached", "--unified=0", "--", "notes.txt")
}

func unstagedDiff(t *testing.T, dir string) string {
	t.Helper()

	return runGit(t, dir, "diff", "--unified=0", "--", "notes.txt")
}

func TestStageHunkStagesOneOfTwo(t *testing.T) {
	dir := twoHunkRepo(t)
	app := newTwoHunkApp(t, dir)
	ctx := context.Background()

	if err := app.stageHunk(ctx, 0); err != nil {
		t.Fatal(err)
	}

	staged := stagedDiff(t, dir)
	if !strings.Contains(staged, "TWO") || strings.Contains(staged, "ELEVEN") {
		t.Fatalf("staged = %q, want only the TWO hunk", staged)
	}

	unstaged := unstagedDiff(t, dir)
	if !strings.Contains(unstaged, "ELEVEN") || strings.Contains(unstaged, "TWO") {
		t.Fatalf("unstaged = %q, want only the ELEVEN hunk", unstaged)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "notes.txt")) //nolint:gosec // path is a t.TempDir fixture
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(raw), "TWO") || !strings.Contains(string(raw), "ELEVEN") {
		t.Fatalf("worktree lost an edit: %q", raw)
	}

	if len(app.changes.Staged) != 1 || len(app.changes.Unstaged) != 1 {
		t.Fatalf("staged = %d files, unstaged = %d files, want one each", len(app.changes.Staged), len(app.changes.Unstaged))
	}

	if app.toast.Kind != "success" || app.toast.Title != "Hunk staged" {
		t.Fatalf("toast = %+v, want staged success", app.toast)
	}
}

func TestStageHunkAtCursorAndHunkMenu(t *testing.T) {
	dir := twoHunkRepo(t)
	app := newTwoHunkApp(t, dir)
	ctx := context.Background()

	// The keyboard chord stages the hunk under the highlighted block, which
	// starts on the first hunk after selectDiff.
	app.shift = true
	if err := app.onKeyDown(ctx, "s"); err != nil {
		t.Fatal(err)
	}

	app.shift = false

	if staged := stagedDiff(t, dir); !strings.Contains(staged, "TWO") {
		t.Fatalf("Shift+S staged = %q, want the TWO hunk", staged)
	}

	// The hunk menu offers Stage next to Discard on a revert button.
	app.selectDiff("notes.txt")
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	box, ok := firstRevertLineBox(app)
	if !ok {
		t.Fatal("missing revert button for the remaining hunk")
	}

	rest := strings.TrimPrefix(box.ID, "revert-line-")
	rows := app.ContextMenuFor(box.ID)
	if len(rows) != 2 || rows[0].Label != "Stage Hunk" || rows[1].Label != "Discard Hunk" {
		t.Fatalf("hunk menu = %+v, want Stage + Discard rows", rows)
	}

	// Stage the remaining ELEVEN block: it is the last revert button in
	// layout order, since the first block is already staged.
	var last ownframe.Box

	for _, b := range app.Page().Boxes() {
		if strings.HasPrefix(b.ID, "revert-line-") {
			last = b
		}
	}

	if last.ID == "" {
		t.Fatal("missing revert button for the remaining hunk")
	}

	rest = strings.TrimPrefix(last.ID, "revert-line-")

	if err := app.MenuAction(ctx, "code-view:stage-hunk:"+rest); err != nil {
		t.Fatal(err)
	}

	if out := runGit(t, dir, "diff", "--cached", "--name-only"); !strings.Contains(out, "notes.txt") {
		t.Fatalf("cached names = %q, want notes.txt", out)
	}

	if unstaged := unstagedDiff(t, dir); strings.Contains(unstaged, "ELEVEN") || strings.Contains(unstaged, "TWO") {
		t.Fatalf("unstaged = %q, want a clean worktree-vs-index diff", unstaged)
	}

	// The Discard Hunk menu row reverts the last staged-test edit path: make
	// a fresh unstaged edit, discard it through the menu, and check disk.
	// TWELVE sits next to staged ELEVEN with no context between them, so
	// they form one block and revert together; staged TWO is untouched.
	writeFile(t, dir, "notes.txt", strings.Replace(diskText(t, dir, "notes.txt"), "twelve", "TWELVE", 1))
	app.ReloadChanges(ctx, false)
	app.selectDiff("notes.txt")

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	var discard ownframe.Box

	for _, b := range app.Page().Boxes() {
		if strings.HasPrefix(b.ID, "revert-line-") {
			discard = b
		}
	}

	if discard.ID == "" {
		t.Fatal("missing revert button for the TWELVE edit")
	}

	rest = strings.TrimPrefix(discard.ID, "revert-line-")

	if err := app.MenuAction(ctx, "code-view:revert-hunk:"+rest); err != nil {
		t.Fatal(err)
	}

	if got := diskText(t, dir, "notes.txt"); strings.Contains(got, "TWELVE") || strings.Contains(got, "ELEVEN") {
		t.Fatalf("menu discard kept the block: %q", got)
	}

	if got := diskText(t, dir, "notes.txt"); !strings.Contains(got, "TWO") {
		t.Fatalf("menu discard took the untouched hunk: %q", got)
	}

	if staged := stagedDiff(t, dir); !strings.Contains(staged, "TWO") || strings.Contains(staged, "ELEVEN") {
		t.Fatalf("staged = %q, want only TWO after the block revert", staged)
	}
}

func TestStageHunkNotes(t *testing.T) {
	ctx := context.Background()

	t.Run("binary", func(t *testing.T) {
		requireGit(t)

		dir := t.TempDir()
		runGit(t, dir, "init", "-q", "-b", "main")
		runGit(t, dir, "config", "user.name", "Code View Test")
		runGit(t, dir, "config", "user.email", "code-view@example.com")
		writeFile(t, dir, "blob.bin", "a\x00b\n")
		runGit(t, dir, "add", "-A")
		runGit(t, dir, "commit", "-q", "-m", "base")
		writeFile(t, dir, "blob.bin", "a\x00c\n")

		app, err := New(dir)
		if err != nil {
			t.Fatal(err)
		}

		app.ReloadChanges(ctx, false)
		app.selectDiff("blob.bin")

		if err := app.stageHunkAtCursor(ctx); err != nil {
			t.Fatal(err)
		}

		if app.toast.Kind != "info" || !strings.Contains(app.toast.Message, "whole") {
			t.Fatalf("toast = %+v, want the file-level note", app.toast)
		}

		if out := runGit(t, dir, "diff", "--cached", "--name-only"); out != "" {
			t.Fatalf("binary stage guessed: cached = %q", out)
		}
	})

	t.Run("untracked", func(t *testing.T) {
		requireGit(t)

		dir := t.TempDir()
		runGit(t, dir, "init", "-q", "-b", "main")
		runGit(t, dir, "config", "user.name", "Code View Test")
		runGit(t, dir, "config", "user.email", "code-view@example.com")
		writeFile(t, dir, "base.txt", "base\n")
		runGit(t, dir, "add", "-A")
		runGit(t, dir, "commit", "-q", "-m", "base")
		writeFile(t, dir, "new.txt", "fresh\n")

		app, err := New(dir)
		if err != nil {
			t.Fatal(err)
		}

		app.ReloadChanges(ctx, false)
		app.selectDiff("new.txt")

		if err := app.stageHunk(ctx, 0); err != nil {
			t.Fatal(err)
		}

		if app.toast.Kind != "info" || !strings.Contains(app.toast.Message, "whole") {
			t.Fatalf("toast = %+v, want the untracked note", app.toast)
		}

		if out := runGit(t, dir, "status", "--porcelain", "--", "new.txt"); !strings.HasPrefix(out, "??") {
			t.Fatalf("untracked file was staged: %q", out)
		}
	})

	t.Run("conflicted", func(t *testing.T) {
		dir := textConflictRepo(t)
		app := newMergeTestApp(t, dir)
		app.selectDiff(mergeTestPath)

		if err := app.stageHunkAtCursor(ctx); err != nil {
			t.Fatal(err)
		}

		if app.toast.Kind != "info" || !strings.Contains(app.toast.Title, "Conflict") {
			t.Fatalf("toast = %+v, want the conflict note", app.toast)
		}

		if out := runGit(t, dir, "ls-files", "-u", "--", mergeTestPath); out == "" {
			t.Fatal("conflict was resolved by a hunk stage")
		}
	})
}

func TestCommitEmptyBlocked(t *testing.T) {
	dir := twoHunkRepo(t)
	app := newTwoHunkApp(t, dir)
	ctx := context.Background()

	if err := app.stageHunk(ctx, 0); err != nil {
		t.Fatal(err)
	}

	if err := app.openCommitDialog(ctx); err != nil {
		t.Fatal(err)
	}

	app.commitDialogMsg = "   "
	if err := app.submitCommitDialog(ctx); err != nil {
		t.Fatal(err)
	}

	if !app.commitDialogOpen {
		t.Fatal("dialog closed on an empty message")
	}

	if app.commitDialogErr == "" || !strings.Contains(app.View().CommitDialog.Error, "message") {
		t.Fatalf("dialog = %+v, want the inline empty-message error", app.View().CommitDialog)
	}

	if count := runGit(t, dir, "rev-list", "--count", "HEAD"); count != "1" {
		t.Fatalf("commit count = %s, want no new commit", count)
	}
}

func TestCommitCreatesCommit(t *testing.T) {
	dir := twoHunkRepo(t)
	app := newTwoHunkApp(t, dir)
	ctx := context.Background()

	if err := app.stageHunk(ctx, 0); err != nil {
		t.Fatal(err)
	}

	if err := app.openCommitDialog(ctx); err != nil {
		t.Fatal(err)
	}

	view := app.View()
	if !view.CommitDialog.Show || len(view.CommitDialog.Files) != 1 {
		t.Fatalf("dialog = %+v, want one staged file", view.CommitDialog)
	}

	if !strings.Contains(view.CommitDialog.Shortstat, "1 file changed") {
		t.Fatalf("shortstat = %q", view.CommitDialog.Shortstat)
	}

	app.commitDialogMsg = "Add feature\n\nBody line"
	app.refresh()

	if err := app.submitCommitDialog(ctx); err != nil {
		t.Fatal(err)
	}

	if app.commitDialogOpen || app.commitDialogMsg != "" {
		t.Fatalf("dialog open = %v draft = %q, want closed and cleared", app.commitDialogOpen, app.commitDialogMsg)
	}

	if subject := runGit(t, dir, "log", "-1", "--format=%s"); subject != "Add feature" {
		t.Fatalf("subject = %q", subject)
	}

	if count := runGit(t, dir, "rev-list", "--count", "HEAD"); count != "2" {
		t.Fatalf("commit count = %s, want exactly one new commit (no amend)", count)
	}

	if prev := runGit(t, dir, "log", "-1", "--format=%s", "HEAD~1"); prev != "base" {
		t.Fatalf("previous subject = %q, want the base commit untouched", prev)
	}

	if body := runGit(t, dir, "log", "-1", "--format=%b"); !strings.Contains(body, "Body line") {
		t.Fatalf("body = %q, want the body line", body)
	}

	if len(app.changes.Staged) != 0 {
		t.Fatalf("staged = %+v, want a refresh to clear the index", app.changes.Staged)
	}

	if app.toast.Kind != "success" || app.toast.Title != "Committed" {
		t.Fatalf("toast = %+v, want committed success", app.toast)
	}

	// The remaining unstaged hunk survives the commit.
	if unstaged := unstagedDiff(t, dir); !strings.Contains(unstaged, "ELEVEN") {
		t.Fatalf("unstaged = %q, want the ELEVEN hunk to survive", unstaged)
	}
}

func TestCommitFailureKeepsInput(t *testing.T) {
	dir := twoHunkRepo(t)
	app := newTwoHunkApp(t, dir)
	ctx := context.Background()

	if err := app.stageHunk(ctx, 0); err != nil {
		t.Fatal(err)
	}

	if err := app.openCommitDialog(ctx); err != nil {
		t.Fatal(err)
	}

	app.commitDialogMsg = "Will fail"
	app.refresh()

	// Empty the index behind the app's back so git commit fails.
	runGit(t, dir, "restore", "--staged", "--", ".")

	if err := app.submitCommitDialog(ctx); err != nil {
		t.Fatal(err)
	}

	if !app.commitDialogOpen {
		t.Fatal("dialog closed on a failed commit")
	}

	if app.commitDialogMsg != "Will fail" {
		t.Fatalf("draft = %q, want the message kept on failure", app.commitDialogMsg)
	}

	if app.View().CommitDialog.Error == "" {
		t.Fatal("no inline error after the failed commit")
	}

	if count := runGit(t, dir, "rev-list", "--count", "HEAD"); count != "1" {
		t.Fatalf("commit count = %s, want no new commit", count)
	}
}

func TestCommitCancelKeepsInput(t *testing.T) {
	dir := twoHunkRepo(t)
	app := newTwoHunkApp(t, dir)
	ctx := context.Background()

	if err := app.stageHunk(ctx, 0); err != nil {
		t.Fatal(err)
	}

	if err := app.openCommitDialog(ctx); err != nil {
		t.Fatal(err)
	}

	app.commitDialogMsg = "draft subject"
	app.refresh()

	if err := app.onClick(ctx, ownframe.Box{ID: "commit-cancel", Action: "commit-cancel"}); err != nil {
		t.Fatal(err)
	}

	if app.commitDialogOpen {
		t.Fatal("cancel left the dialog open")
	}

	if app.commitDialogMsg != "draft subject" {
		t.Fatalf("draft = %q, want cancel to keep the input", app.commitDialogMsg)
	}

	if err := app.openCommitDialog(ctx); err != nil {
		t.Fatal(err)
	}

	if got := app.View().CommitDialog.Message; got != "draft subject" {
		t.Fatalf("reopened draft = %q, want the kept input", got)
	}

	if count := runGit(t, dir, "rev-list", "--count", "HEAD"); count != "1" {
		t.Fatalf("commit count = %s, want no new commit", count)
	}
}

func TestCommitDirtyGuard(t *testing.T) {
	requireGit(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.name", "Code View Test")
	runGit(t, dir, "config", "user.email", "code-view@example.com")
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "base")

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	writeFile(t, dir, "main.go", "package main\n\nfunc main() { println(1) }\n")
	app.ReloadChanges(ctx, false)

	if err := app.stageFile(ctx, "main.go"); err != nil {
		t.Fatal(err)
	}

	app.openFile("main.go")
	app.toggleEdit()

	if err := app.Page().Edit().Type(ctx, "x"); err != nil {
		t.Fatal(err)
	}

	if !app.isDirty("main.go") {
		t.Fatal("main.go is not dirty after typing")
	}

	if err := app.openCommitDialog(ctx); err != nil {
		t.Fatal(err)
	}

	app.commitDialogMsg = "Guarded commit"
	app.refresh()

	if err := app.submitCommitDialog(ctx); err != nil {
		t.Fatal(err)
	}

	if !app.confirmOpen || !unsavedKind(app.confirmKind) {
		t.Fatalf("dialog = open %v kind %q, want the unsaved guard", app.confirmOpen, app.confirmKind)
	}

	if count := runGit(t, dir, "rev-list", "--count", "HEAD"); count != "1" {
		t.Fatalf("commit count = %s, want the guard to hold the commit", count)
	}

	if err := app.confirmUnsavedSave(ctx); err != nil {
		t.Fatal(err)
	}

	if app.confirmOpen || app.commitDialogOpen {
		t.Fatal("dialogs stayed open after Save + commit")
	}

	if subject := runGit(t, dir, "log", "-1", "--format=%s"); subject != "Guarded commit" {
		t.Fatalf("subject = %q", subject)
	}
}

func TestCommitDialogNeedsStaged(t *testing.T) {
	dir := twoHunkRepo(t)
	app := newTwoHunkApp(t, dir)
	ctx := context.Background()

	if err := app.openCommitDialog(ctx); err != nil {
		t.Fatal(err)
	}

	if app.commitDialogOpen {
		t.Fatal("dialog opened with nothing staged")
	}

	if app.toast.Kind != "info" {
		t.Fatalf("toast = %+v, want the nothing-staged note", app.toast)
	}
}

func TestSplitCommitMessage(t *testing.T) {
	subject, body := splitCommitMessage("Add feature\n\nBody line\n")
	if subject != "Add feature" || body != "Body line" {
		t.Fatalf("split = %q / %q", subject, body)
	}

	subject, body = splitCommitMessage("  Solo subject  \n")
	if subject != "Solo subject" || body != "" {
		t.Fatalf("split = %q / %q", subject, body)
	}

	subject, body = splitCommitMessage("  spaced  \n  trimmed body  ")
	if subject != "spaced" || body != "trimmed body" {
		t.Fatalf("split = %q / %q", subject, body)
	}
}
