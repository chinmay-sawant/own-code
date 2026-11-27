package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

func placeCaretOn(t *testing.T, a *App, substr string) {
	t.Helper()

	enterEditAtFirstLine(t, a)

	text := a.edit.Text()
	idx := strings.Index(text, substr)
	if idx < 0 {
		t.Fatalf("%q not found in %q", substr, text)
	}

	a.editCaret = utf8.RuneCountInString(text[:idx])
	a.editAnchor = a.editCaret
	a.refresh()
}

func TestLSPGotoDefinitionCrossFileAndUTF16(t *testing.T) {
	mainSrc := "package a\nfunc run() { _ = \"😀\" + Target() }\n"
	targetSrc := "package a\n// header\nfunc Target() string { return \"ok\" }\n"

	app, fake, dir := lspTestApp(t, map[string]string{
		"main.go":   mainSrc,
		"target.go": targetSrc,
	})

	targetURI := lsp.URIFromPath(filepath.Join(dir, "target.go"))
	locJSON, _ := json.Marshal([]lsp.Location{{
		URI:   targetURI,
		Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 5}, End: lsp.Position{Line: 2, Character: 11}},
	}})
	fake.defResult = string(locJSON)

	app.openFile("main.go")
	pump(t, app, "didOpen", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for _, d := range fake.opens {
			if strings.HasSuffix(d.URI, "/main.go") {
				return true
			}
		}
		return false
	})

	placeCaretOn(t, app, "Target")
	if err := app.gotoDefinitionAtCaret(context.Background()); err != nil {
		t.Fatalf("gotoDefinitionAtCaret: %v", err)
	}

	pump(t, app, "jump to target.go", func() bool { return app.activeDocPath() == "target.go" })

	fake.mu.Lock()
	defs := append([]lsp.TextDocumentPositionParams(nil), fake.defs...)
	fake.mu.Unlock()

	if len(defs) != 1 {
		t.Fatalf("definition requests = %d, want 1", len(defs))
	}

	// "func run() { _ = \"😀\" + Target() }": rune col of Target is 23, UTF-16 col is 24.
	if defs[0].Position != (lsp.Position{Line: 1, Character: 24}) {
		t.Fatalf("definition pos = %+v, want {Line:1 Character:24}", defs[0].Position)
	}

	line, col := app.editCaretLineCol()
	if line != 2 || col != 5 {
		t.Fatalf("caret landed at line=%d col=%d, want line=2 col=5", line, col)
	}
}

func TestLSPGotoDefinitionFallsBackWhenEmpty(t *testing.T) {
	mainSrc := "package a\nfunc run() { Helper() }\n"
	helperSrc := "package a\nfunc Helper() {}\n"

	app, fake, _ := lspTestApp(t, map[string]string{
		"main.go":   mainSrc,
		"helper.go": helperSrc,
	})
	fake.defResult = "[]"

	app.openFile("main.go")
	pump(t, app, "didOpen", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for _, d := range fake.opens {
			if strings.HasSuffix(d.URI, "/main.go") {
				return true
			}
		}
		return false
	})

	placeCaretOn(t, app, "Helper")
	if err := app.gotoDefinitionAtCaret(context.Background()); err != nil {
		t.Fatalf("gotoDefinitionAtCaret: %v", err)
	}

	// Empty LSP reply must fall back to the local workspace symbol index.
	pump(t, app, "fallback jump to helper.go", func() bool { return app.activeDocPath() == "helper.go" })
}

func TestLSPGotoDeclarationAndTypeDefinition(t *testing.T) {
	mainSrc := "package a\nvar x MyType\n"
	typesSrc := "package a\ntype MyType struct{}\n"

	app, fake, dir := lspTestApp(t, map[string]string{
		"main.go":  mainSrc,
		"types.go": typesSrc,
	})

	typesURI := lsp.URIFromPath(filepath.Join(dir, "types.go"))
	linkJSON, _ := json.Marshal([]lsp.LocationLink{{
		TargetURI:            typesURI,
		TargetRange:          lsp.Range{Start: lsp.Position{Line: 1, Character: 0}, End: lsp.Position{Line: 1, Character: 19}},
		TargetSelectionRange: lsp.Range{Start: lsp.Position{Line: 1, Character: 5}, End: lsp.Position{Line: 1, Character: 11}},
	}})
	fake.defResult = string(linkJSON)

	app.openFile("main.go")
	pump(t, app, "didOpen", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for _, d := range fake.opens {
			if strings.HasSuffix(d.URI, "/main.go") {
				return true
			}
		}
		return false
	})

	placeCaretOn(t, app, "MyType")
	if err := app.gotoTypeDefinitionAtCaret(context.Background()); err != nil {
		t.Fatalf("gotoTypeDefinitionAtCaret: %v", err)
	}

	pump(t, app, "typeDefinition jump", func() bool { return app.activeDocPath() == "types.go" })

	fake.mu.Lock()
	methods := append([]string(nil), fake.defMethod...)
	fake.mu.Unlock()

	if len(methods) != 1 || methods[0] != "textDocument/typeDefinition" {
		t.Fatalf("methods = %v, want [textDocument/typeDefinition]", methods)
	}

	// Switch back and test declaration with empty result (no fallback, toast shown).
	app.openFile("main.go")
	fake.mu.Lock()
	fake.defResult = "null"
	fake.mu.Unlock()

	placeCaretOn(t, app, "MyType")
	if err := app.gotoDeclarationAtCaret(context.Background()); err != nil {
		t.Fatalf("gotoDeclarationAtCaret: %v", err)
	}

	pump(t, app, "declaration toast", func() bool { return strings.Contains(app.View().Toast.Message, "MyType") })
}

func TestLSPGotoDefinitionDropsStaleReplyOnTabSwitch(t *testing.T) {
	mainSrc := "package a\nfunc run() { Target() }\n"
	otherSrc := "package a\nfunc other() {}\n"
	targetSrc := "package a\nfunc Target() {}\n"

	app, fake, dir := lspTestApp(t, map[string]string{
		"main.go":   mainSrc,
		"other.go":  otherSrc,
		"target.go": targetSrc,
	})

	targetURI := lsp.URIFromPath(filepath.Join(dir, "target.go"))
	locJSON, _ := json.Marshal(lsp.Location{
		URI:   targetURI,
		Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 5}},
	})
	fake.defResult = string(locJSON)
	fake.defDelay = 80 * time.Millisecond

	app.openFile("main.go")
	pump(t, app, "didOpen", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for _, d := range fake.opens {
			if strings.HasSuffix(d.URI, "/main.go") {
				return true
			}
		}
		return false
	})

	placeCaretOn(t, app, "Target")
	if err := app.gotoDefinitionAtCaret(context.Background()); err != nil {
		t.Fatalf("gotoDefinitionAtCaret: %v", err)
	}

	// Switch tabs before the delayed reply returns.
	app.openFile("other.go")

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := app.tickWatch(context.Background()); err != nil {
			t.Fatal(err)
		}

		time.Sleep(10 * time.Millisecond)
	}

	if got := app.activeDocPath(); got != "other.go" {
		t.Fatalf("stale reply jumped away from other.go to %q", got)
	}
}

func TestLSPGotoDefinitionOpensExternalFile(t *testing.T) {
	extDir := t.TempDir()
	extPath := filepath.Join(extDir, "stdlib.go")
	if err := os.WriteFile(extPath, []byte("package stdlib\nfunc Println() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app, fake, _ := lspTestApp(t, map[string]string{
		"main.go": "package a\nfunc run() { Println() }\n",
	})

	locJSON, _ := json.Marshal(lsp.Location{
		URI:   lsp.URIFromPath(extPath),
		Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 5}},
	})
	fake.defResult = string(locJSON)

	app.openFile("main.go")
	pump(t, app, "didOpen", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for _, d := range fake.opens {
			if strings.HasSuffix(d.URI, "/main.go") {
				return true
			}
		}
		return false
	})

	placeCaretOn(t, app, "Println")
	if err := app.gotoDefinitionAtCaret(context.Background()); err != nil {
		t.Fatalf("gotoDefinitionAtCaret: %v", err)
	}

	pump(t, app, "external jump", func() bool { return app.activeDocPath() == extPath })
}
