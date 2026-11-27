package app

import (
	"context"
	"testing"
)

func TestPlanAutoPair(t *testing.T) {
	t.Parallel()

	r := func(s string) []rune { return []rune(s) }

	if e, ok := planAutoPair(r("x = "), 4, 4, '('); !ok || e.insert != "()" || e.caret != 5 {
		t.Fatalf("open at eol: %+v %v", e, ok)
	}

	if _, ok := planAutoPair(r("x = foo"), 4, 4, '('); ok {
		t.Fatal("paired before a word")
	}

	if e, ok := planAutoPair(r("()"), 1, 1, ')'); !ok || !e.skip || e.caret != 2 {
		t.Fatalf("closer skip: %+v %v", e, ok)
	}

	if _, ok := planAutoPair(r("don"), 3, 3, '\''); ok {
		t.Fatal("apostrophe after a word paired")
	}

	if e, ok := planAutoPair(r("say "), 4, 4, '"'); !ok || e.insert != `""` {
		t.Fatalf("quote: %+v %v", e, ok)
	}

	e, ok := planAutoPair(r("hello world"), 0, 5, '[')
	if !ok || e.insert != "[hello]" || e.anchor != 1 || e.caret != 6 {
		t.Fatalf("surround: %+v %v", e, ok)
	}

	if _, _, ok := pairBackspaceSpan(r("a()"), 2); !ok {
		t.Fatal("backspace inside pair not detected")
	}

	if _, _, ok := pairBackspaceSpan(r("(a)"), 1); ok {
		t.Fatal("backspace after opener with content paired")
	}
}

func TestAutoPairTypeAndBackspace(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	enterEditAtFirstLine(t, app)

	app.edit.SetText("\n")
	app.editCaret, app.editAnchor = 0, 0

	if err := app.onType(ctx, "{"); err != nil {
		t.Fatal(err)
	}

	if got := app.edit.Text(); got != "{}\n" || app.editCaret != 1 {
		t.Fatalf("text=%q caret=%d", got, app.editCaret)
	}

	if err := app.onType(ctx, "}"); err != nil {
		t.Fatal(err)
	}

	if got := app.edit.Text(); got != "{}\n" || app.editCaret != 2 {
		t.Fatalf("skip: text=%q caret=%d", got, app.editCaret)
	}

	app.editCaret, app.editAnchor = 1, 1
	if err := app.onBackspace(ctx); err != nil {
		t.Fatal(err)
	}

	if got := app.edit.Text(); got != "\n" {
		t.Fatalf("backspace pair: %q", got)
	}

	app.edit.SetText("word\n")
	app.editAnchor, app.editCaret = 0, 4

	if err := app.onType(ctx, `"`); err != nil {
		t.Fatal(err)
	}

	if got := app.edit.Text(); got != "\"word\"\n" || app.editAnchor != 1 || app.editCaret != 5 {
		t.Fatalf("surround: %q %d %d", got, app.editAnchor, app.editCaret)
	}
}
