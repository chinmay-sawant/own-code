package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGotoParse(t *testing.T) {
	cases := []struct {
		query       string
		maxLine     int
		maxCol      int
		line, col   int
		req         int
		clamped, ok bool
	}{
		{":42", 170, 80, 42, 1, 42, false, true},
		{"42:7", 170, 80, 42, 7, 42, false, true},
		{"42", 170, 80, 42, 1, 42, false, true},
		{":42:7", 170, 80, 42, 7, 42, false, true},
		{":1:1", 170, 80, 1, 1, 1, false, true},
		{":99999", 170, 80, 170, 1, 99999, true, true},
		{":3:999", 10, 5, 3, 5, 3, true, true},
		{":0", 10, 5, 1, 1, 0, true, true},
		{"", 10, 5, 0, 0, 0, false, false},
		{":", 10, 5, 0, 0, 0, false, false},
		{":abc", 10, 5, 0, 0, 0, false, false},
		{":12:xy", 10, 5, 0, 0, 0, false, false},
		{"probe", 10, 5, 0, 0, 0, false, false},
		{"  :7  ", 10, 5, 7, 1, 7, false, true},
	}

	for _, tc := range cases {
		line, col, req, clamped, ok := parseGoto(tc.query, tc.maxLine, tc.maxCol)
		if line != tc.line || col != tc.col || req != tc.req || clamped != tc.clamped || ok != tc.ok {
			t.Errorf("parseGoto(%q) = %d,%d,req %d,clamped %v,ok %v; want %d,%d,req %d,clamped %v,ok %v",
				tc.query, line, col, req, clamped, ok, tc.line, tc.col, tc.req, tc.clamped, tc.ok)
		}
	}
}

func TestGotoQueryAlias(t *testing.T) {
	for query, want := range map[string]bool{
		":42": true, ":42:7": true, ": 7": true, ":": false,
		"probe": false, "": false, "42": false,
	} {
		if got := isGotoQuery(query); got != want {
			t.Errorf("isGotoQuery(%q) = %v, want %v", query, got, want)
		}
	}
}

func TestGotoLineMovesCaret(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.openFile("backend/probe.go")
	enterEditAtFirstLine(t, app)

	if err := app.gotoLine(ctx, 100, 1); err != nil {
		t.Fatal(err)
	}

	line, _ := offsetLineCol(app.edit.Text(), app.editCaret)
	if line != 99 {
		t.Fatalf("caret line = %d, want 99", line)
	}

	if app.editAnchor != app.editCaret {
		t.Fatal("goto left a selection behind")
	}

	if app.scrollY == 0 {
		t.Fatal("goto to line 100 did not scroll")
	}
}

func TestGotoLineEntersEdit(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	if app.editing {
		t.Fatal("expected token view")
	}

	if err := app.gotoLine(ctx, 2, 1); err != nil {
		t.Fatal(err)
	}

	if !app.editing || app.edit == nil {
		t.Fatal("goto outside edit mode did not enter edit mode")
	}

	line, _ := offsetLineCol(app.edit.Text(), app.editCaret)
	if line != 1 {
		t.Fatalf("caret line = %d, want 1", line)
	}
}

func TestGotoLineEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), nil, 0o644); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	app.openFile("empty.go")

	if err := app.gotoLine(context.Background(), 1, 1); err != nil {
		t.Fatalf("gotoLine: %v", err)
	}

	if !app.editing || app.editCaret != 0 {
		t.Fatalf("editing = %v, caret = %d, want edit mode at offset 0", app.editing, app.editCaret)
	}
}

func TestGotoSubmitClampsWithToast(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	if err := app.openGotoPalette(ctx); err != nil {
		t.Fatal(err)
	}

	if app.paletteMode != paletteModeGoto || app.paletteQuery != ":" {
		t.Fatalf("goto palette = mode %q query %q", app.paletteMode, app.paletteQuery)
	}

	app.paletteQuery = ":99999"
	app.refresh()

	if err := app.onSubmit(ctx); err != nil {
		t.Fatal(err)
	}

	if app.paletteOpen {
		t.Fatal("goto submit left the palette open")
	}

	if app.View().Toast.Show == false || app.View().Toast.Title != "Go to line" {
		t.Fatalf("clamp toast = %+v", app.View().Toast)
	}
}

func TestGotoSubmitBadQueryKeepsPalette(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	if err := app.openGotoPalette(ctx); err != nil {
		t.Fatal(err)
	}

	app.paletteQuery = ":abc"
	app.refresh()

	if err := app.onSubmit(ctx); err != nil {
		t.Fatal(err)
	}

	if !app.paletteOpen {
		t.Fatal("bad goto query closed the palette")
	}
}

func TestGotoExitsPreview(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	if app.View().Lang != langMarkdown {
		t.Fatalf("initial language = %s", app.View().Lang)
	}

	app.togglePreview()

	if !app.View().Preview {
		t.Fatal("preview did not open")
	}

	if err := app.gotoLine(ctx, 2, 1); err != nil {
		t.Fatal(err)
	}

	if app.preview || app.View().Preview {
		t.Fatal("goto did not exit preview")
	}
}
