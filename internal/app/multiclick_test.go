package app

import (
	"testing"
	"time"
)

func TestWordRangeAt(t *testing.T) {
	r := []rune("func main() {\n\tfoo_bar(1)\n}")
	cases := []struct {
		caret      int
		start, end int
	}{
		{1, 0, 4},    // inside "func"
		{4, 0, 4},    // right edge of "func" picks the word on its left
		{5, 5, 9},    // "main"
		{9, 5, 9},    // boundary after "main" picks the word on its left
		{14, 14, 15}, // tab
		{18, 15, 22}, // foo_bar
	}
	for _, c := range cases {
		s, e := wordRangeAt(r, c.caret)
		if s != c.start || e != c.end {
			t.Fatalf("caret %d = %d..%d, want %d..%d", c.caret, s, e, c.start, c.end)
		}
	}
}

func TestLineRangeAt(t *testing.T) {
	r := []rune("ab\ncd\nef")
	if s, e := lineRangeAt(r, 4); s != 3 || e != 6 {
		t.Fatalf("middle line = %d..%d, want 3..6", s, e)
	}
	if s, e := lineRangeAt(r, 7); s != 6 || e != 8 {
		t.Fatalf("last line = %d..%d, want 6..8", s, e)
	}
}

func TestPressCountCycles(t *testing.T) {
	app := newTestApp(t)
	if n := app.pressCount(5); n != 1 {
		t.Fatalf("first press = %d, want 1", n)
	}
	if n := app.pressCount(5); n != 1 {
		t.Fatalf("duplicate delivery of one press = %d, want 1", n)
	}
	for i, want := range []int{2, 3, 1} {
		app.pressAt = app.pressAt.Add(-100 * time.Millisecond)
		if n := app.pressCount(5); n != want {
			t.Fatalf("press %d = %d, want %d", i+2, n, want)
		}
	}
	if n := app.pressCount(40); n != 1 {
		t.Fatalf("press elsewhere = %d, want 1", n)
	}
}

func TestEditorClickSelectsWordAndLine(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "alpha beta\n\nlast line\n")
	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	app.openFile("README.md")

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	body, ok := boxByID(app.Page().Boxes(), "code-body")
	if !ok {
		t.Fatal("missing editor body")
	}

	x, y := body.X+editTextLeft+2*codeCellW, body.Y+rowH/2
	press := func() {
		t.Helper()
		if claimed, err := app.Page().Testing().BeginDrag(ctx, x, y); err != nil || !claimed {
			t.Fatalf("begin drag = %v, %v", claimed, err)
		}
		if err := app.Page().Testing().EndDrag(ctx); err != nil {
			t.Fatal(err)
		}
		app.pressAt = app.pressAt.Add(-100 * time.Millisecond)
	}

	press()
	if app.editAnchor != app.editCaret {
		t.Fatalf("single click selected %d..%d, want a bare caret", app.editAnchor, app.editCaret)
	}

	press()
	if app.editAnchor == app.editCaret {
		t.Fatalf("double click should select the word: %d..%d count=%d text=%q", app.editAnchor, app.editCaret, app.pressCountN, string([]rune(app.edit.Text())[:5]))
	}

	press()
	text := []rune(app.edit.Text())
	if app.editAnchor != 0 || text[app.editCaret-1] != '\n' {
		t.Fatalf("triple click selected %d..%d, want the whole first line", app.editAnchor, app.editCaret)
	}
}
