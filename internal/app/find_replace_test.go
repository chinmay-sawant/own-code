package app

import (
	"context"
	"strings"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

// openReplaceBar opens path with the replace bar ready and the given
// find/replace text staged through the same binder path the UI uses. The
// file must already be open: opening a file folds any live edit buffer, so
// editing tests open and toggle-edit before calling this.
func openReplaceBar(t *testing.T, ctx context.Context, app *App, path, find, replace string) {
	t.Helper()

	if !app.editing {
		app.openFile(path)
	} else if app.activeDocPath() != path {
		t.Fatalf("editing %q, want %q open", app.activeDocPath(), path)
	}

	if err := app.openReplace(ctx); err != nil {
		t.Fatalf("open replace: %v", err)
	}

	if !app.View().ReplaceOpen {
		t.Fatal("replace bar did not open")
	}

	// The editing crumbs hide the find inputs, so focus only lands on the
	// find field outside edit mode.
	if !app.editing && app.Page().Form().FocusID() != "find" {
		t.Fatalf("focus=%q, want find", app.Page().Form().FocusID())
	}

	app.view.FindQuery = find
	if err := app.onChange(ctx, ownframe.Box{ID: "find"}); err != nil {
		t.Fatalf("find change: %v", err)
	}

	app.view.ReplaceQuery = replace
	if err := app.onChange(ctx, ownframe.Box{ID: "find-replace"}); err != nil {
		t.Fatalf("replace change: %v", err)
	}
}

func TestReplaceOneLiteral(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	openReplaceBar(t, ctx, app, "README.md", "Beacon", "Signal")

	doc := app.docs["README.md"]
	before := strings.Join(doc.Lines, "\n")
	if !strings.Contains(before, "Beacon") {
		t.Fatal("sample has no Beacon to replace")
	}

	total := len(app.findHits)
	if total == 0 {
		t.Fatal("no hits for Beacon")
	}

	if err := app.replaceOne(ctx); err != nil {
		t.Fatalf("replace one: %v", err)
	}

	after := strings.Join(app.docs["README.md"].Lines, "\n")
	if strings.Count(after, "Signal") != strings.Count(before, "Signal")+1 {
		t.Fatalf("one replacement missing:\n%s", after)
	}

	if !app.dirty["README.md"] {
		t.Error("document is not dirty after replace")
	}

	if len(app.findHits) != total-1 {
		t.Fatalf("hits = %d, want %d", len(app.findHits), total-1)
	}
}

func TestReplaceAllGroupedUndo(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.openFile("README.md")

	app.toggleEdit()
	openReplaceBar(t, ctx, app, "README.md", "Beacon", "Signal")

	before := app.edit.Text()
	undoDepth := len(app.editUndo)

	if len(app.findHits) < 2 {
		t.Fatalf("want several hits, got %d", len(app.findHits))
	}

	if err := app.replaceAllInFile(ctx); err != nil {
		t.Fatalf("replace all: %v", err)
	}

	if got := len(app.editUndo); got != undoDepth+1 {
		t.Fatalf("undo steps = %d, want exactly one more (had %d)", got, undoDepth)
	}

	if strings.Contains(app.edit.Text(), "Beacon") {
		t.Fatal("Beacon survives replace-all")
	}

	if err := app.onUndo(ctx); err != nil {
		t.Fatalf("undo: %v", err)
	}

	if app.edit.Text() != before {
		t.Fatal("one Ctrl+Z did not restore the buffer")
	}
}

func TestReplaceRegexGroups(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.openFile("README.md")

	app.toggleEdit()
	openReplaceBar(t, ctx, app, "README.md", `Bea(con)`, "Sig$1")

	app.replaceRegex = true
	app.reindexFind()
	if len(app.findHits) == 0 {
		t.Fatal("regex found no hits")
	}

	if err := app.replaceAllInFile(ctx); err != nil {
		t.Fatalf("replace all: %v", err)
	}

	if strings.Contains(app.edit.Text(), "Beacon") {
		t.Fatal("Beacon survives regex replace-all")
	}

	if !strings.Contains(app.edit.Text(), "Sigcon") {
		t.Fatalf("group expansion missing:\n%s", app.edit.Text())
	}
}

func TestReplaceWholeWord(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	openReplaceBar(t, ctx, app, "README.md", "the", "a")

	app.replaceWholeWord = true
	app.reindexFind()

	for _, hit := range app.findHits {
		line := []rune(app.docs["README.md"].Lines[hit.Line])
		if hit.Start > 0 && isWordChar(line[hit.Start-1]) {
			t.Fatalf("hit inside a word at line %d", hit.Line)
		}
		if hit.End < len(line) && isWordChar(line[hit.End]) {
			t.Fatalf("hit inside a word at line %d", hit.Line)
		}
	}
}

func TestReplaceInvalidRegex(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	openReplaceBar(t, ctx, app, "README.md", "(unclosed", "x")

	app.replaceRegex = true
	app.reindexFind()

	if app.findError == "" {
		t.Fatal("no inline error for invalid regex")
	}

	if app.replaceEnabled() {
		t.Fatal("replace enabled with an invalid pattern")
	}

	before := strings.Join(app.docs["README.md"].Lines, "\n")
	if err := app.replaceOne(ctx); err != nil {
		t.Fatalf("replace one: %v", err)
	}

	if after := strings.Join(app.docs["README.md"].Lines, "\n"); after != before {
		t.Fatal("invalid pattern changed the file")
	}
}

func TestReplaceTogglesSticky(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	openReplaceBar(t, ctx, app, "README.md", "Beacon", "Signal")

	app.findCase = true
	app.replaceRegex = true

	if err := app.closeFind(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	if !app.findCase || !app.replaceRegex {
		t.Fatalf("toggles reset: case=%v regex=%v", app.findCase, app.replaceRegex)
	}

	if app.findQuery != "" || app.replaceQuery != "" || len(app.findHits) != 0 {
		t.Fatal("queries or hits survived close")
	}

	if app.View().FindOpen || app.View().ReplaceOpen {
		t.Fatal("bar still open")
	}
}

func TestReplaceKeys(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.openFile("README.md")

	// Ctrl+H opens the replace bar.
	if err := app.onKeyDown(ctx, "control"); err != nil {
		t.Fatal(err)
	}

	if err := app.onKeyDown(ctx, "h"); err != nil {
		t.Fatal(err)
	}

	if err := app.onKeyUp(ctx, "control"); err != nil {
		t.Fatal(err)
	}

	if !app.View().ReplaceOpen {
		t.Fatal("Ctrl+H did not open the replace bar")
	}

	// Esc closes both bars.
	if err := app.onKeyDown(ctx, "escape"); err != nil {
		t.Fatal(err)
	}

	if app.View().FindOpen || app.View().ReplaceOpen {
		t.Fatal("Esc did not close the bars")
	}

	// Plain Ctrl+F still opens find without replace.
	if err := app.onKeyDown(ctx, "control"); err != nil {
		t.Fatal(err)
	}

	if err := app.onKeyDown(ctx, "f"); err != nil {
		t.Fatal(err)
	}

	if err := app.onKeyUp(ctx, "control"); err != nil {
		t.Fatal(err)
	}

	if !app.View().FindOpen || app.View().ReplaceOpen {
		t.Fatalf("Ctrl+F state: find=%v replace=%v", app.View().FindOpen, app.View().ReplaceOpen)
	}
}

func TestReplaceTabMapping(t *testing.T) {
	// probe.go indents with tabs. doc.Lines carry tab-expanded display
	// text, so the editing path must map the display hit back to raw
	// buffer offsets before splicing.
	app := newTestApp(t)
	ctx := context.Background()
	app.openFile("backend/probe.go")

	app.toggleEdit()
	openReplaceBar(t, ctx, app, "backend/probe.go", `"context"`, `"ctx2"`)

	undoDepth := len(app.editUndo)

	if err := app.replaceOne(ctx); err != nil {
		t.Fatalf("replace one: %v", err)
	}

	if got := len(app.editUndo); got != undoDepth+1 {
		t.Fatalf("undo steps = %d, want exactly one more", got)
	}

	text := app.edit.Text()
	if !strings.Contains(text, "\t\"ctx2\"") {
		t.Fatalf("tab-indented replacement missing:\n%s", text)
	}

	if strings.Count(text, "\"ctx2\"") != 1 {
		t.Fatalf("replaced %d times, want 1", strings.Count(text, "\"ctx2\""))
	}

	if err := app.onUndo(ctx); err != nil {
		t.Fatalf("undo: %v", err)
	}

	if !strings.Contains(app.edit.Text(), "\t\"context\"") {
		t.Fatal("undo did not restore the tab-indented line")
	}
}

func TestReplaceAltToggles(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.openFile("README.md")

	if err := app.openReplace(ctx); err != nil {
		t.Fatalf("open replace: %v", err)
	}

	for _, tc := range []struct {
		key   string
		check func() bool
	}{
		{"alt+r", func() bool { return app.replaceRegex }},
		{"alt+w", func() bool { return app.replaceWholeWord }},
		{"alt+c", func() bool { return app.findCase }},
	} {
		if err := app.toggleMatchKeys(ctx, tc.key); err != nil {
			t.Fatalf("%s: %v", tc.key, err)
		}

		if !tc.check() {
			t.Fatalf("%s did not flip the toggle", tc.key)
		}
	}

	if err := app.toggleMatchKeys(ctx, "alt+r"); err != nil {
		t.Fatal(err)
	}

	if app.replaceRegex {
		t.Fatal("alt+r did not flip back")
	}
}
