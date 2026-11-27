package app

import (
	"strings"
	"testing"
)

func TestIndentGuideRows(t *testing.T) {
	rows := []codeRow{
		{Line: 0, Segments: []segment{{Text: "func x() {", Class: "plain"}}},
		{Line: 1, Segments: []segment{{Text: "        ", Class: "plain"}, {Text: "y()", Class: "fn"}}},
	}

	markIndentGuideRows(rows, 4)

	if len(rows[0].Segments) != 1 {
		t.Fatalf("unindented row changed: %+v", rows[0].Segments)
	}

	guides := 0

	for _, seg := range rows[1].Segments {
		if strings.Contains(seg.Class, "indent-guide") {
			guides++

			if seg.Text != " " {
				t.Fatalf("guide cell = %q", seg.Text)
			}
		}
	}

	if guides != 2 {
		t.Fatalf("guides = %d, want 2", guides)
	}

	if got := joinSegs(rows[1].Segments); got != "        y()" {
		t.Fatalf("text changed: %q", got)
	}
}

func joinSegs(segs []segment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}

	return b.String()
}

func TestPassiveBracketMatchRows(t *testing.T) {
	app := newTestApp(t)
	app.openFile("backend/probe.go")
	app.toggleEdit()
	app.edit.SetText("package main\n\nfunc f() { g(1) }\n")
	// Caret just after the opening paren of g(.
	text := app.edit.Text()
	app.editCaret = strings.Index(text, "g(") + 2
	app.editAnchor = app.editCaret

	rows := []codeRow{{Line: 2, Segments: []segment{{Text: "func f() { g(1) }", Class: "plain"}}}}
	app.markBracketMatchRows(rows)

	marked := ""

	for _, seg := range rows[0].Segments {
		if strings.Contains(seg.Class, "bracket-match") {
			marked += seg.Text
		}
	}

	if marked != "()" {
		t.Fatalf("marked = %q, want ()", marked)
	}

	// Away from any bracket nothing is marked.
	app.editCaret = 0
	rows = []codeRow{{Line: 2, Segments: []segment{{Text: "func f() { g(1) }", Class: "plain"}}}}
	app.markBracketMatchRows(rows)

	if len(rows[0].Segments) != 1 {
		t.Fatalf("unexpected marks: %+v", rows[0].Segments)
	}
}
