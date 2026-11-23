package app

import (
	"context"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

func TestParseSnippetStopsOrder(t *testing.T) {
	text, stops := parseSnippet("f(${2:b}, ${1:a})$0")
	if text != "f(b, a)" || len(stops) != 3 {
		t.Fatalf("text=%q stops=%v", text, stops)
	}

	if stops[0] != (snippetStop{5, 6}) || stops[1] != (snippetStop{2, 3}) || stops[2] != (snippetStop{7, 7}) {
		t.Fatalf("stops = %v", stops)
	}

	if text, stops = parseSnippet("x${1|one,two|}y"); text != "xoney" || stops[0] != (snippetStop{1, 4}) {
		t.Fatalf("choice: %q %v", text, stops)
	}
}

func TestLSPSnippetTabCyclesPlaceholders(t *testing.T) {
	app, fake, _ := featRig(t, map[string]string{"main.go": "package a\nfunc run() { fo }\n"})
	fake.results["textDocument/completion"] = `[{"label":"foo","insertTextFormat":2,"insertText":"foo(${1:alpha}, ${2:beta})$0"}]`

	caretAfter(t, app, "fo")

	if err := app.lspKeyDownCtrlSpace(); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "popup", func() bool { return app.popup.open })

	if !app.popupKeyDown("enter") {
		t.Fatal("enter not consumed")
	}

	if got := lineTextAt(app.edit.Text(), 1); got != "func run() { foo(alpha, beta) }" {
		t.Fatalf("line = %q", got)
	}

	sel := func() string {
		s, e := min(app.editAnchor, app.editCaret), max(app.editAnchor, app.editCaret)

		return string([]rune(app.edit.Text())[s:e])
	}

	if sel() != "alpha" {
		t.Fatalf("first stop selection = %q", sel())
	}

	// Typing over the placeholder then Tab lands on the next one.
	if err := app.onType(context.Background(), "XY"); err != nil {
		t.Fatal(err)
	}

	if !app.snippetTab() || sel() != "beta" {
		t.Fatalf("second stop selection = %q", sel())
	}

	if got := lineTextAt(app.edit.Text(), 1); got != "func run() { foo(XY, beta) }" {
		t.Fatalf("line = %q", got)
	}
}

func TestLSPCommitCharacterAcceptsSelectedItem(t *testing.T) {
	app, fake, _ := featRig(t, map[string]string{"main.go": "package a\nfunc run() { foo }\n"})
	fake.results["textDocument/completion"] = `[{"label":"fooBar","commitCharacters":["."]}]`

	caretAfter(t, app, "foo")

	if err := app.lspKeyDownCtrlSpace(); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "popup", func() bool { return app.popup.open && app.popup.spec.Caret == cmpCaretTag })

	app.lspCommitBefore(";")

	if !app.popup.open {
		t.Fatal("a non-commit character must not accept")
	}

	app.lspCommitBefore(".")

	if app.popup.open || lineTextAt(app.edit.Text(), 1) != "func run() { fooBar }" {
		t.Fatalf("commit char did not accept: open=%v line=%q", app.popup.open, lineTextAt(app.edit.Text(), 1))
	}
}

func TestLSPPrepareRenameRefusesAndPrefills(t *testing.T) {
	prep := func(f *featLSP) {
		f.caps = `{"textDocumentSync":1,"renameProvider":{"prepareProvider":true}}`
	}

	app, fake, _ := featRig(t, map[string]string{"main.go": "package a\nfunc run() { Target() }\n"}, prep)
	placeCaretOn(t, app, "Target")

	if err := app.renameSymbolAtCaret(context.Background()); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "refusal", func() bool { return strings.Contains(app.toast.Message, "cannot be renamed") })

	if app.renamePromptActive() || len(fake.requests("textDocument/prepareRename")) != 1 {
		t.Fatal("null prepareRename must not open the prompt")
	}

	fake.mu.Lock()
	fake.results["textDocument/prepareRename"] = `{"range":{"start":{"line":1,"character":13},"end":{"line":1,"character":19}},"placeholder":"Target"}`
	fake.mu.Unlock()

	if err := app.renameSymbolAtCaret(context.Background()); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "prompt", func() bool { return app.renamePromptActive() })

	if app.paletteQuery != "Target" {
		t.Fatalf("query = %q", app.paletteQuery)
	}
}

func TestLSPSignatureHelpOnParen(t *testing.T) {
	app, fake, _ := featRig(t, map[string]string{"main.go": "package a\nfunc run() { f }\n"}, func(f *featLSP) {
		f.caps = `{"textDocumentSync":1,"signatureHelpProvider":{"triggerCharacters":["(",","]}}`
		f.results["textDocument/signatureHelp"] = `{"signatures":[{"label":"f(a int, b string)","documentation":"Does f.","parameters":[{"label":"a int"},{"label":"b string"}]}],"activeParameter":1}`
	})

	caretAfter(t, app, "{ f")

	if err := app.onType(context.Background(), "("); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "signature card", func() bool { return app.popup.open && app.popup.spec.Caret == sigCaretTag })

	if c := app.popup.spec.Content; !strings.Contains(c, "f(a int, b string)") || !strings.Contains(c, "`b string`") || !strings.Contains(c, "Does f.") {
		t.Fatalf("content = %q", c)
	}

	if len(fake.requests("textDocument/signatureHelp")) != 1 {
		t.Fatal("expected one signatureHelp request")
	}

	app.lspm.signatureHide()

	if app.popup.open {
		t.Fatal("signatureHide must close the card")
	}
}

func TestLSPCodeActionsListAndStageEdit(t *testing.T) {
	app, fake, dir := featRig(t, map[string]string{"main.go": "package a\nfunc run() { foo }\n"}, func(f *featLSP) {
		f.caps = `{"textDocumentSync":1,"codeActionProvider":true}`
	})

	uri := lsp.URIFromPath(dir + "/main.go")
	fake.results["textDocument/codeAction"] = jsonOf(t, []map[string]any{
		{"title": "Add import", "kind": "quickfix", "isPreferred": true, "edit": map[string]any{
			"changes": map[string]any{uri: []lsp.TextEdit{{NewText: "import \"os\"\n", Range: lsp.Range{Start: lsp.Position{Line: 1}, End: lsp.Position{Line: 1}}}}},
		}},
		{"title": "Disabled", "disabled": map[string]any{"reason": "x"}},
	})

	caretAfter(t, app, "foo")

	if err := app.codeActionsAtCaret(context.Background()); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "actions popup", func() bool { return app.popup.open && app.popup.spec.Caret == codeActionCaretTag })

	if len(app.popup.spec.Items) != 1 {
		t.Fatalf("disabled actions must be hidden: %d items", len(app.popup.spec.Items))
	}

	if !app.popupKeyDown("enter") {
		t.Fatal("enter not consumed")
	}

	if !app.confirmOpen || !strings.Contains(app.confirmTitle, "Add import") {
		t.Fatalf("confirm = open %v title %q", app.confirmOpen, app.confirmTitle)
	}

	if err := app.applyLSPRename(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(app.edit.Text(), "import \"os\"") {
		t.Fatalf("edit not applied: %q", app.edit.Text())
	}
}

func TestWelcomeShowsCoreShortcuts(t *testing.T) {
	app := newTestApp(t)
	app.closeTab(0)

	for app.View().Active != "" {
		app.closeTab(0)
	}

	if err := app.Page().Redraw(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, ok := findBoxID(app.Page().Boxes(), "welcome-core-keys"); !ok {
		t.Fatal("welcome card must list the core shortcuts")
	}
}
