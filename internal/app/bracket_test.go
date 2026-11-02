package app

import (
	"context"
	"testing"
)

func TestBracketMatch(t *testing.T) {
	text := "func f(a int, b []string) {\n\tm := map[string]int{\"k\": 1}\n}\n"

	// Caret on the opening paren of the parameter list.
	caret := 6
	match, ok := matchBracket(text, langGo, caret)
	if !ok {
		t.Fatal("no match for the parameter paren")
	}

	if match != 24 {
		t.Fatalf("paren match = %d, want 24", match)
	}

	// Caret just after the closing paren walks backward.
	if match, ok := matchBracket(text, langGo, 25); !ok || match != 6 {
		t.Fatalf("reverse paren match = %d,%v", match, ok)
	}

	// Caret on the opening brace.
	brace := 26
	if match, ok := matchBracket(text, langGo, brace); !ok || match != len([]rune(text))-2 {
		t.Fatalf("brace match = %d,%v", match, ok)
	}

	// A non-bracket caret has no match.
	if _, ok := matchBracket(text, langGo, 2); ok {
		t.Fatal("letter caret matched a bracket")
	}
}

func TestBracketMatchSkipsStrings(t *testing.T) {
	text := "s := \"(not a bracket)\"\nnext()\n"

	// Inside the string literal: no match.
	if _, ok := matchBracket(text, langGo, 6); ok {
		t.Fatal("string paren matched")
	}

	call := len([]rune("s := \"(not a bracket)\"\nnext"))
	match, ok := matchBracket(text, langGo, call)
	if !ok {
		t.Fatal("no match for the real call paren")
	}

	if match != call+1 {
		t.Fatalf("call match = %d, want %d", match, call+1)
	}
}

func TestBracketMatchSkipsComments(t *testing.T) {
	text := "// (ghost)\nreal()\n"

	if _, ok := matchBracket(text, langGo, 3); ok {
		t.Fatal("comment paren matched")
	}

	call := len([]rune("// (ghost)\nreal"))
	match, ok := matchBracket(text, langGo, call)
	if !ok || match != call+1 {
		t.Fatalf("code match = %d,%v", match, ok)
	}
}

func TestBracketJumpAndFlash(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	enterEditAtFirstLine(t, app)

	app.edit.SetText("func f(a int) {\n}\n")
	app.editCaret = 6
	app.editAnchor = 6

	if err := app.jumpToBracket(ctx, false); err != nil {
		t.Fatal(err)
	}

	if app.editCaret != 12 || app.editAnchor != 12 {
		t.Fatalf("jump caret = %d anchor = %d, want 12", app.editCaret, app.editAnchor)
	}

	if !app.bracketOn {
		t.Fatal("jump did not flash")
	}

	rows := app.markBracketRows([]codeRow{{Line: 0, Start: 0, Segments: []segment{{Text: "func f(a int) {", Class: "plain"}}}})
	if len(rows) != 1 {
		t.Fatal("flash marked no rows")
	}

	flashed := false

	for _, seg := range rows[0].Segments {
		if seg.Class == "plain bracket-flash" && seg.Text == ")" {
			flashed = true
		}
	}

	if !flashed {
		t.Fatalf("flash segments = %+v", rows[0].Segments)
	}
}

func TestBracketSelect(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	enterEditAtFirstLine(t, app)

	app.edit.SetText("func f(a int) {\n}\n")
	app.editCaret = 6
	app.editAnchor = 2

	if err := app.jumpToBracket(ctx, true); err != nil {
		t.Fatal(err)
	}

	if app.editCaret != 12 {
		t.Fatalf("select caret = %d, want 12", app.editCaret)
	}

	if app.editAnchor != 2 {
		t.Fatalf("select kept anchor = %d, want 2", app.editAnchor)
	}
}

func TestBracketUnmatchedToasts(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	enterEditAtFirstLine(t, app)

	app.edit.SetText("func f(a int {\n}\n")
	app.editCaret = 6
	app.editAnchor = 6

	if err := app.jumpToBracket(ctx, false); err != nil {
		t.Fatal(err)
	}

	if !app.View().Toast.Show {
		t.Fatal("unmatched bracket showed no toast")
	}
}
