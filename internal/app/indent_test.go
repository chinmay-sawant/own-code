package app

import (
	"context"
	"reflect"
	"testing"
)

func TestIndentForNewline(t *testing.T) {
	cases := []struct {
		lang string
		line string
		want string
	}{
		{langGo, "func f() {", "    "},
		{langGo, "    if x {", "        "},
		{langGo, "    return x", "    "},
		{langGo, "", ""},
		{langJS, "  foo(bar, [", "      "},
		{langPython, "def f():", "    "},
		{langPython, "    if x:", "        "},
		{langPython, "    return x", "    "},
		{langShell, "deploy:", "    "},
		{langShell, "if [ x ]; then", ""},
		{langYAML, "server:", "    "},
		{langMarkdown, "- item", "- "},
		{langMarkdown, "  * nested", "  * "},
		{langMarkdown, "3. third", "4. "},
		{langMarkdown, "# heading", ""},
		{langMarkdown, "plain", ""},
		{langCSS, ".a {", "    "},
		{langJSON, `  "a": {`, "      "},
	}

	for _, tc := range cases {
		if got := indentForNewline(tc.lang, tc.line); got != tc.want {
			t.Errorf("indentForNewline(%s, %q) = %q, want %q", tc.lang, tc.line, got, tc.want)
		}
	}
}

func TestIndentLines(t *testing.T) {
	lines := []string{"a {", "", "  b"}

	if got, want := indentLines(lines, false), []string{"    a {", "", "      b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("indent = %q", got)
	}

	if got, want := indentLines([]string{"    a", "\tb", "  c", "d"}, true),
		[]string{"a", "b", "c", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("outdent = %q", got)
	}
}

func TestAutoIndentOnEnter(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	enterEditAtFirstLine(t, app)

	// Caret at the end of the "# Beacon" heading: Enter keeps the new
	// line unindented.
	text := app.edit.Text()
	first := lineTextAt(text, 0)
	app.editCaret = len([]rune(first))
	app.editAnchor = app.editCaret
	before := len(app.editUndo)

	if err := app.onSubmit(ctx); err != nil {
		t.Fatal(err)
	}

	if len(app.editUndo) != before+1 {
		t.Fatalf("enter recorded %d undo points, want 1", len(app.editUndo)-before)
	}

	if second := lineTextAt(app.edit.Text(), 1); second != "" {
		t.Fatalf("newline after %q indented to %q", first, second)
	}
}

func TestAutoIndentAfterBrace(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.openFile("backend/probe.go")
	enterEditAtFirstLine(t, app)

	app.edit.SetText("package main\n\nfunc f() {\n}\n")
	app.editCaret = len([]rune("package main\n\nfunc f() {"))
	app.editAnchor = app.editCaret
	app.commitEditBuffer()

	if err := app.onSubmit(ctx); err != nil {
		t.Fatal(err)
	}

	if got := lineTextAt(app.edit.Text(), 3); got != tabSpaces {
		t.Fatalf("newline after brace = %q, want %q", got, tabSpaces)
	}
}

func TestTabIndentOutdent(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	enterEditAtFirstLine(t, app)

	// Collapsed Tab inserts one level.
	app.edit.SetText("a\n")
	app.editCaret = 1
	app.editAnchor = 1

	if err := app.onKeyDown(ctx, "tab"); err != nil {
		t.Fatal(err)
	}

	if got := app.edit.Text(); got != "a    \n" {
		t.Fatalf("tab insert = %q", got)
	}

	// Multi-line Shift+Tab strips one level and keeps one undo point.
	app.edit.SetText("    a\n    b\n")
	n := len([]rune(app.edit.Text()))
	app.editAnchor = 0
	app.editCaret = n
	before := len(app.editUndo)

	app.shift = true
	if err := app.onKeyDown(ctx, "tab"); err != nil {
		t.Fatal(err)
	}
	app.shift = false

	if got := app.edit.Text(); got != "a\nb\n" {
		t.Fatalf("shift+tab = %q", got)
	}

	if len(app.editUndo) != before+1 {
		t.Fatalf("shift+tab recorded %d undo points, want 1", len(app.editUndo)-before)
	}
}

func TestTabRangeExcludesLastLineAtCol0(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	enterEditAtFirstLine(t, app)

	app.edit.SetText("a\nb\nc\n")
	// Select lines 0-1; the selection ends at the start of line 2.
	app.editAnchor = 0
	app.editCaret = len([]rune("a\nb\n"))

	if err := app.onKeyDown(ctx, "tab"); err != nil {
		t.Fatal(err)
	}

	if got := app.edit.Text(); got != "    a\n    b\nc\n" {
		t.Fatalf("range tab = %q", got)
	}
}
