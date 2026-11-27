package app

import (
	"context"
	"strings"
	"testing"
)

func TestGotoDefinitionFromTokenView(t *testing.T) {
	app := newTestApp(t)
	app.openFile("backend/probe.go")

	if app.editing {
		t.Fatal("expected token view")
	}

	doc := app.activeDoc()

	line, col := -1, -1

	for i, l := range doc.Lines {
		if j := strings.Index(l, "once("); j >= 0 && strings.Contains(l, "p.once") {
			line, col = i, j+1

			break
		}
	}

	if line < 0 {
		t.Skip("no call site in fixture")
	}

	app.viewPointerHook = func() (int, int, bool) { return line, col, true }

	if err := app.gotoDefinitionAtCaret(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := app.activeDocPath(); got != "backend/probe.go" {
		t.Fatalf("path = %q", got)
	}

	got, _ := offsetLineCol(app.edit.Text(), app.editCaret)
	if got == line {
		t.Fatalf("caret did not move off the call line %d", line)
	}
}

func TestGotoDefinitionFromViewNoSymbol(t *testing.T) {
	app := newTestApp(t)
	app.openFile("backend/probe.go")
	app.viewPointerHook = func() (int, int, bool) { return 0, 0, false }

	if err := app.gotoDefinitionAtCaret(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !app.View().Toast.Show {
		t.Fatal("expected a toast when nothing is under the pointer")
	}
}

func TestViewSymbolAt(t *testing.T) {
	doc := &fileDoc{Lines: []string{"x := foo(1)"}}
	if w, ok := viewSymbolAt(doc, 0, 6); !ok || w != "foo" {
		t.Fatalf("got %q %v", w, ok)
	}

	if _, ok := viewSymbolAt(doc, 0, 3); ok {
		t.Fatal("space is not a symbol")
	}
}
