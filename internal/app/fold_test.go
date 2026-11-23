package app

import (
	"context"
	"os"
	"testing"
)

func TestComputeFoldRegions(t *testing.T) {
	lines := []string{
		"func a() {",      // 0
		"\tx := 1",        // 1 (tab -> 4)
		"",                // 2
		"\tif x {",        // 3
		"\t\ty()",         // 4
		"\t}",             // 5
		"}",               // 6
		"func b() {}",     // 7
		"type t struct {", // 8
		"    f int",       // 9
	}

	got := computeFoldRegions(lines)
	want := map[int]int{0: 5, 3: 4, 8: 9}

	if len(got) != len(want) {
		t.Fatalf("regions = %v, want %v", got, want)
	}

	for k, v := range want {
		if got[k] != v {
			t.Fatalf("regions = %v, want %v", got, want)
		}
	}
}

func testFoldDoc(t *testing.T, app *App) *fileDoc {
	t.Helper()

	app.openFile("tools/report.py")

	doc := app.activeDoc()
	if doc == nil || len(doc.foldRegions()) < 2 {
		t.Fatalf("test file needs fold regions, got %v", doc.foldRegions())
	}

	return doc
}

func firstRegion(doc *fileDoc) (int, int) {
	start, end := -1, -1

	for s, e := range doc.foldRegions() {
		if start < 0 || s < start {
			start, end = s, e
		}
	}

	return start, end
}

func TestFoldMappingUnwrapped(t *testing.T) {
	app := newTestApp(t)
	app.wrap = false
	doc := testFoldDoc(t, app)

	if app.rowPlan(doc, 0) != nil {
		t.Fatal("no folds and no wrap must be the identity mapping")
	}

	start, end := firstRegion(doc)
	hidden := end - start

	app.toggleFoldLine(0, start)

	plan := app.rowPlan(doc, 0)
	if plan == nil {
		t.Fatal("folded doc needs a row plan")
	}

	if got, want := len(plan.Rows), len(doc.Lines)-hidden; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}

	if got := app.rowOfLine(doc, 0, start); got != start {
		t.Fatalf("fold start row = %d, want %d", got, start)
	}

	if got := app.rowOfLine(doc, 0, end+1); got != start+1 {
		t.Fatalf("line after the fold sits on row %d, want %d", got, start+1)
	}

	if got := app.lineOfRow(doc, 0, start+1); got != end+1 {
		t.Fatalf("row after the fold = line %d, want %d", got, end+1)
	}

	if got := app.lineOfRow(doc, 0, start); got != start {
		t.Fatalf("fold row = line %d, want %d", got, start)
	}

	// Lines before the fold keep identity.
	if start > 0 && app.lineOfRow(doc, 0, start-1) != start-1 {
		t.Fatal("rows above the fold must map to themselves")
	}

	// The window shows the chevron and skips hidden lines.
	rows := app.View().Rows
	for _, row := range rows {
		if row.Line > start && row.Line <= end {
			t.Fatalf("hidden line %d painted", row.Line)
		}

		if row.Line == start && row.Fold != "closed" {
			t.Fatalf("fold start row Fold = %q, want closed", row.Fold)
		}
	}

	app.toggleFoldLine(0, start)

	if app.rowPlan(doc, 0) != nil {
		t.Fatal("unfolding everything restores the identity mapping")
	}
}

func TestFoldMappingWrapped(t *testing.T) {
	app := newTestApp(t)
	app.wrap = true
	doc := testFoldDoc(t, app)
	before := len(app.rowPlan(doc, 0).Rows)

	start, end := firstRegion(doc)
	app.toggleFoldLine(0, start)

	plan := app.rowPlan(doc, 0)
	if len(plan.Rows) >= before {
		t.Fatalf("wrapped rows %d, want fewer than %d", len(plan.Rows), before)
	}

	for _, row := range plan.Rows {
		if row.Line > start && row.Line <= end {
			t.Fatalf("hidden line %d in wrapped plan", row.Line)
		}
	}

	if got := plan.lineAt(plan.firstRow(end + 1)); got != end+1 {
		t.Fatalf("lineAt(firstRow(%d)) = %d", end+1, got)
	}
}

func TestFoldAllUnfoldAllCommands(t *testing.T) {
	app := newTestApp(t)
	app.wrap = false
	doc := testFoldDoc(t, app)

	if !app.runFoldCommand(paletteCmdFoldAll) {
		t.Fatal("fold all changed nothing")
	}

	if len(doc.Folded) == 0 || doc.foldMask() == nil {
		t.Fatal("fold all left nothing folded")
	}

	if !app.runFoldCommand(paletteCmdUnfoldAll) || len(doc.Folded) != 0 {
		t.Fatal("unfold all did not clear folds")
	}
}

func TestFoldAtCaretCommands(t *testing.T) {
	app := newTestApp(t)
	app.wrap = false
	doc := testFoldDoc(t, app)
	start, end := firstRegion(doc)

	app.toggleEdit()
	app.editCaret = lineOffset(doc.Lines, start+1, 0)
	app.editAnchor = app.editCaret

	if !app.runFoldCommand(paletteCmdFold) {
		t.Fatal("fold at caret failed")
	}

	if !doc.foldedAt(start) {
		t.Fatalf("expected a fold at %d", start)
	}

	line, _ := offsetLineCol(app.edit.Text(), app.editCaret)
	if line != start {
		t.Fatalf("caret line = %d, want moved to fold start %d (end %d)", line, start, end)
	}

	if !app.runFoldCommand(paletteCmdUnfold) || doc.foldedAt(start) {
		t.Fatal("unfold at caret failed")
	}
}

func TestFoldRevealOnCaretMove(t *testing.T) {
	app := newTestApp(t)
	app.wrap = false
	doc := testFoldDoc(t, app)
	start, end := firstRegion(doc)

	app.toggleFoldLine(0, start)
	app.toggleEdit()
	app.editCaret = lineOffset(doc.Lines, end, 0)
	app.editAnchor = app.editCaret
	app.ensureEditCaretVisible()

	if doc.foldedAt(start) {
		t.Fatal("caret landing in a hidden line must unfold its region")
	}
}

func TestFoldStateSurvivesEdits(t *testing.T) {
	app := newTestApp(t)
	app.wrap = false
	doc := testFoldDoc(t, app)
	start, _ := firstRegion(doc)

	app.toggleFoldLine(0, start)

	// Same text, new slice: the fold stays.
	doc.Lines = append([]string(nil), doc.Lines...)
	doc.Wrap = nil

	if !doc.foldedAt(start) || doc.foldMask() == nil {
		t.Fatal("fold lost across an identical reload")
	}

	// A line inserted above shifts the fold down with it.
	doc.Lines = append([]string{"# new"}, doc.Lines...)
	doc.Wrap = nil

	if !doc.foldedAt(start+1) || doc.foldedAt(start) {
		t.Fatalf("fold did not shift: %v", doc.Folded)
	}

	// Editing the fold's first line drops it.
	doc.Lines = append([]string(nil), doc.Lines...)
	doc.Lines[start+1] += " # edited"
	doc.Wrap = nil

	if doc.foldMask() != nil || len(doc.Folded) != 0 {
		t.Fatalf("fold with changed header should reset: %v", doc.Folded)
	}
}

func TestScreenshotFolding(t *testing.T) {
	if os.Getenv("OWNFRAME_CODE_VIEW_SCREENSHOT") != "1" {
		t.Skip("set OWNFRAME_CODE_VIEW_SCREENSHOT=1 to capture the frames")
	}

	app := newTestApp(t)
	app.Page().SetSize(1400, 900)
	app.wrap = false
	doc := testFoldDoc(t, app)
	start, _ := firstRegion(doc)

	app.toggleFoldLine(0, start)

	if err := app.Page().Redraw(context.Background()); err != nil {
		t.Fatal(err)
	}

	writeShot(t, app, "code-folding")
}
