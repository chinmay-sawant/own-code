package app

import (
	"context"
	"strings"
	"testing"
)

// assertNoWorkingTreeActions checks that a read-only diff view renders no
// commit, stage, unstage, or discard controls.
func assertNoWorkingTreeActions(t *testing.T, html string) {
	t.Helper()

	for _, id := range []string{
		"changes-commit",
		"changes-stage-all",
		"changes-unstage-all",
		"changes-discard-all",
		"changes-stage-selected",
		"changes-unstage-selected",
		"changes-discard-selected",
	} {
		if strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("read-only diff rendered %s", id)
		}
	}

	for _, prefix := range []string{`id="stage-`, `id="unstage-`, `id="discard-`} {
		if strings.Contains(html, prefix) {
			t.Errorf("read-only diff rendered a %s control", prefix)
		}
	}
}

// TestCommitDiffHidesWorkingTreeActions verifies that an expanded commit is a
// read-only snapshot: no working-tree buttons, and no working-tree context
// menu, while the working tree view still offers both.
func TestCommitDiffHidesWorkingTreeActions(t *testing.T) {
	app, hashes := newHistoryApp(t)
	ctx := context.Background()
	short := app.findCommit(hashes[0]).Short

	clickID(t, app, "commit-"+short)
	waitForCommitPatch(t, app)

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	if view := app.View(); view.Diff.Source != "commit" {
		t.Fatalf("source = %q, want commit", view.Diff.Source)
	}

	assertNoWorkingTreeActions(t, renderAppHTML(t, app))

	for _, id := range []string{"changes-list", "changes-head", "history-working"} {
		if rows := app.ContextMenuFor(id); len(rows) != 0 {
			t.Errorf("commit view context menu for %s = %+v, want none", id, rows)
		}
	}

	if rows := app.ContextMenuFor("commit-" + short); len(rows) != 2 {
		t.Errorf("commit row context menu = %+v, want copy actions", rows)
	}

	clickID(t, app, "history-working")

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	html := renderAppHTML(t, app)
	for _, id := range []string{"changes-commit", "changes-stage-all", "changes-discard-all"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("working tree missing %s", id)
		}
	}

	for _, prefix := range []string{`id="stage-`, `id="discard-`} {
		if !strings.Contains(html, prefix) {
			t.Errorf("working tree missing a %s control", prefix)
		}
	}
}

// TestBranchDiffHidesWorkingTreeActions verifies that comparing a branch is
// read-only too: the working-tree rows never leak into the context menu.
func TestBranchDiffHidesWorkingTreeActions(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.setMode(ctx, "changes")

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	clickID(t, app, "history-branch-changes")

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	view := app.View()
	if view.Diff.Source != "branch" || view.Diff.Path == "" {
		t.Fatalf("branch diff = %+v", view.Diff)
	}

	assertNoWorkingTreeActions(t, renderAppHTML(t, app))

	for _, id := range []string{"changes-list", "changes-head", "history-working", "change-" + view.Diff.Path} {
		if rows := app.ContextMenuFor(id); len(rows) != 0 {
			t.Errorf("branch view context menu for %s = %+v, want none", id, rows)
		}
	}
}
