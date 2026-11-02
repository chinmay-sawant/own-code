package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

func TestCommentToggleLines(t *testing.T) {
	goStyle, _ := commentStyleFor(langGo)

	commented := toggleCommentLines([]string{
		"func f() {",
		"    x := 1",
		"",
		"    return x",
		"}",
	}, goStyle)

	want := []string{
		"// func f() {",
		"    // x := 1",
		"",
		"    // return x",
		"// }",
	}

	if !reflect.DeepEqual(commented, want) {
		t.Fatalf("commented = %q", commented)
	}

	// Re-toggle strips exactly what the first pass added.
	if plain := toggleCommentLines(commented, goStyle); !reflect.DeepEqual(plain, []string{
		"func f() {",
		"    x := 1",
		"",
		"    return x",
		"}",
	}) {
		t.Fatalf("uncommented = %q", plain)
	}
}

func TestCommentToggleMixedCommentsAll(t *testing.T) {
	pyStyle, _ := commentStyleFor(langPython)

	// One uncommented line forces the whole range to comment, including a
	// second level on the already-commented line.
	got := toggleCommentLines([]string{"# a = 1", "b = 2"}, pyStyle)
	if got[0] != "# # a = 1" || got[1] != "# b = 2" {
		t.Fatalf("mixed toggle = %q", got)
	}

	// Stripping one level round-trips the cycle.
	got = toggleCommentLines(got, pyStyle)
	if got[0] != "# a = 1" || got[1] != "b = 2" {
		t.Fatalf("strip toggle = %q", got)
	}
}

func TestCommentToggleBlockStyle(t *testing.T) {
	cssStyle, _ := commentStyleFor(langCSS)

	got := toggleCommentLines([]string{".a {", "  color: red;", "}"}, cssStyle)
	if got[0] != "/* .a { */" || got[1] != "  /* color: red; */" || got[2] != "/* } */" {
		t.Fatalf("css toggle = %q", got)
	}

	if plain := toggleCommentLines(got, cssStyle); plain[0] != ".a {" || plain[1] != "  color: red;" {
		t.Fatalf("css strip = %q", plain)
	}
}

func TestCommentStyles(t *testing.T) {
	if _, ok := commentStyleFor(langJSON); ok {
		t.Fatal("JSON has a comment style")
	}

	if _, ok := commentStyleFor(langPlain); ok {
		t.Fatal("plain text has a comment style")
	}

	for _, lang := range []string{langGo, langJS, langPython, langShell, langYAML, langCSS, langHTML, langMarkdown} {
		if _, ok := commentStyleFor(lang); !ok {
			t.Fatalf("%s has no comment style", lang)
		}
	}
}

func TestCommentToggleSingleUndo(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	enterEditAtFirstLine(t, app)

	style, ok := commentStyleFor(langMarkdown)
	if !ok {
		t.Fatal("no markdown style")
	}

	// Pure round-trip over the buffer's own lines.
	text, _, _ := app.editSelection()
	sel := edit.SelectionLines(text)
	lines := make([]string, len(sel))

	for i, l := range sel {
		lines[i] = l.Text
	}

	roundTrip := toggleCommentLines(toggleCommentLines(lines, style), style)
	if !reflect.DeepEqual(roundTrip, lines) {
		t.Fatal("comment toggle is not idempotent over the buffer lines")
	}

	// One gesture records exactly one undo point.
	before := len(app.editUndo)
	app.editAnchor = 0
	app.editCaret = len([]rune(app.edit.Text()))

	if err := app.toggleLineComment(ctx); err != nil {
		t.Fatal(err)
	}

	if len(app.editUndo) != before+1 {
		t.Fatalf("undo points grew by %d, want 1", len(app.editUndo)-before)
	}
}

func TestCommentToggleJSONToasts(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	enterEditAtFirstLine(t, app)

	app.edit.SetText("{\"a\": 1}\n")
	app.editCaret = 0
	app.editAnchor = 0

	// Force a JSON language for the buffer path.
	if doc := app.docs[app.edit.Path]; doc != nil {
		doc.Lang = langJSON
	}

	if err := app.toggleLineComment(ctx); err != nil {
		t.Fatal(err)
	}

	if !app.View().Toast.Show {
		t.Fatal("JSON toggle showed no toast")
	}

	if doc := app.docs[app.edit.Path]; doc != nil {
		doc.Lang = languageOf(app.edit.Path)
	}
}
