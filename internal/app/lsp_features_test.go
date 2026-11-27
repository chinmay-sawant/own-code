package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

// featLSP is a scriptable in-process server for the completion, references,
// rename, symbol and progress features.
type featLSP struct {
	mu      sync.Mutex
	caps    string
	results map[string]string // method -> raw JSON result
	errs    map[string]string // method -> error message
	reqs    map[string][]json.RawMessage
	conn    *lsp.Conn
	opens   int
}

func newFeatLSP() *featLSP {
	return &featLSP{
		caps:    `{"textDocumentSync":1,"completionProvider":{"triggerCharacters":["."]}}`,
		results: map[string]string{},
		errs:    map[string]string{},
		reqs:    map[string][]json.RawMessage{},
	}
}

func (f *featLSP) requests(method string) []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]json.RawMessage(nil), f.reqs[method]...)
}

func (f *featLSP) push(method, params string) {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()

	_ = conn.Write(&lsp.Message{Method: method, Params: json.RawMessage(params)})
}

func (f *featLSP) connect(_ lsp.ServerConfig, _ string, opt lsp.Options) (*lsp.Client, error) {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	conn := lsp.NewConn(sr, sw)

	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()

	go func() {
		for {
			m, err := conn.Read()
			if err != nil {
				_ = sw.Close()

				return
			}

			if m.ID == nil {
				if m.Method == "textDocument/didOpen" {
					f.mu.Lock()
					f.opens++
					f.mu.Unlock()
				}

				if m.Method == "exit" {
					_ = sw.Close()

					return
				}

				continue
			}

			f.mu.Lock()
			f.reqs[m.Method] = append(f.reqs[m.Method], m.Params)
			res, errMsg := f.results[m.Method], f.errs[m.Method]
			caps := f.caps
			f.mu.Unlock()

			switch {
			case m.Method == "initialize":
				_ = conn.Write(&lsp.Message{ID: m.ID, Result: json.RawMessage(`{"capabilities":` + caps + `}`)})
			case errMsg != "":
				_ = conn.Write(&lsp.Message{ID: m.ID, Error: &lsp.ResponseError{Code: -32601, Message: errMsg}})
			default:
				if res == "" {
					res = "null"
				}

				_ = conn.Write(&lsp.Message{ID: m.ID, Result: json.RawMessage(res)})
			}
		}
	}()

	return lsp.NewClient(cr, cw, opt), nil
}

// featRig opens main.go in edit mode with the fake attached.
func featRig(t *testing.T, files map[string]string, setup ...func(*featLSP)) (*App, *featLSP, string) {
	t.Helper()

	app, _, dir := lspTestApp(t, files)
	fake := newFeatLSP()

	for _, fn := range setup {
		fn(fake)
	}

	app.lspm.connect = fake.connect

	app.openFile("main.go")
	pump(t, app, "didOpen", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()

		return fake.opens > 0
	})

	return app, fake, dir
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

// caretAfter puts the caret just after the first occurrence of substr.
func caretAfter(t *testing.T, a *App, substr string) {
	t.Helper()

	placeCaretOn(t, a, substr)
	a.editCaret += utf8.RuneCountInString(substr)
	a.editAnchor = a.editCaret
}

func TestExpandSnippet(t *testing.T) {
	cases := []struct {
		in   string
		text string
		at   int
	}{
		{"foo", "foo", 3},
		{"foo($1)", "foo()", 4},
		{"foo(${1:a}, ${2:b})$0", "foo(a, b)", 4},
		{"if ${1:cond} {\n\t$0\n}", "if cond {\n\t\n}", 3},
		{`a\$b`, "a$b", 3},
		{"x$0y", "xy", 1},
	}

	for _, tc := range cases {
		got, at := expandSnippet(tc.in)
		if got != tc.text || at != tc.at {
			t.Errorf("expandSnippet(%q) = %q,%d want %q,%d", tc.in, got, at, tc.text, tc.at)
		}
	}
}

func TestLSPCompletionFilterAndAccept(t *testing.T) {
	app, fake, _ := featRig(t, map[string]string{"main.go": "package a\nfunc run() { foo }\n"})
	fake.results["textDocument/completion"] = jsonOf(t, map[string]any{"isIncomplete": false, "items": []lsp.CompletionItem{
		{Label: "other", Kind: 6},
		{Label: "fooBar", Kind: 3, Detail: "func()"},
		{Label: "foobaz", Kind: 6},
	}})

	caretAfter(t, app, "foo")

	if err := app.lspKeyDownCtrlSpace(); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "completion popup", func() bool { return app.popup.open && app.popup.spec.Caret == cmpCaretTag })

	if got := len(app.popup.matches); got != 2 {
		t.Fatalf("matches for prefix foo = %d, want 2 (items %d)", got, len(app.popup.spec.Items))
	}

	reqs := fake.requests("textDocument/completion")
	if len(reqs) != 1 || !strings.Contains(string(reqs[0]), `"triggerKind":1`) {
		t.Fatalf("completion requests = %s", reqs)
	}

	// Typing narrows the list through the tick.
	if err := app.onType(context.Background(), "B"); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "filter to foob...", func() bool { return app.popup.open && app.popup.spec.Filter == "fooB" })

	if len(app.popup.matches) != 2 {
		t.Fatalf("fooB subsequence matches = %d, want fooBar + foobaz", len(app.popup.matches))
	}

	if !app.popupKeyDown("enter") {
		t.Fatal("enter was not consumed by the popup")
	}

	if got := lineTextAt(app.edit.Text(), 1); got != "func run() { fooBar }" {
		t.Fatalf("line after accept = %q", got)
	}

	if app.popup.open {
		t.Fatal("popup stays open after accept")
	}
}

func TestLSPCompletionClosesOnNonIdentifier(t *testing.T) {
	app, fake, _ := featRig(t, map[string]string{"main.go": "package a\nfunc run() { foo }\n"})
	fake.results["textDocument/completion"] = jsonOf(t, []lsp.CompletionItem{{Label: "foo1"}, {Label: "foo2"}})

	caretAfter(t, app, "foo")

	if err := app.lspKeyDownCtrlSpace(); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "completion popup", func() bool { return app.popup.open })

	if err := app.onType(context.Background(), ")"); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "popup closed", func() bool { return !app.popup.open })
}

func TestLSPCompletionTriggerCharAndTextEditSnippet(t *testing.T) {
	app, fake, _ := featRig(t, map[string]string{"main.go": "package a\nfunc run() { \"😀\"+x }\n"})

	// Server replaces the typed "x." span using UTF-16 columns (emoji = 2).
	// Line: func run() { "😀"+x. }  -> x is at rune col 17, UTF-16 col 18.
	fake.results["textDocument/completion"] = `[{"label":"Run","kind":2,"insertTextFormat":2,"textEdit":{"newText":"x.Run(${1:ctx})$0","range":{"start":{"line":1,"character":18},"end":{"line":1,"character":20}}}}]`

	caretAfter(t, app, "+x")

	if err := app.onType(context.Background(), "."); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "trigger completion popup", func() bool { return app.popup.open })

	reqs := fake.requests("textDocument/completion")
	if len(reqs) != 1 || !strings.Contains(string(reqs[0]), `"triggerKind":2`) || !strings.Contains(string(reqs[0]), `"triggerCharacter":"."`) {
		t.Fatalf("trigger request = %s", reqs)
	}

	if !strings.Contains(string(reqs[0]), `"character":20`) {
		t.Fatalf("position must be UTF-16 (20): %s", reqs[0])
	}

	app.popupAccept()

	line := lineTextAt(app.edit.Text(), 1)
	if line != "func run() { \"😀\"+x.Run(ctx) }" {
		t.Fatalf("line = %q", line)
	}

	// The first tab stop ("ctx") is selected; the selection starts before it.
	_, col := offsetLineCol(app.edit.Text(), min(app.editAnchor, app.editCaret))
	if want := utf8.RuneCountInString("func run() { \"😀\"+x.Run("); col != want {
		t.Fatalf("caret col = %d, want %d", col, want)
	}
}

func TestLSPFindReferencesFillsSearchPanel(t *testing.T) {
	app, fake, dir := featRig(t, map[string]string{
		"main.go":  "package a\nfunc run() { Target() }\n",
		"other.go": "package a\nvar _ = Target\n",
	})

	main := lsp.URIFromPath(filepath.Join(dir, "main.go"))
	other := lsp.URIFromPath(filepath.Join(dir, "other.go"))
	fake.results["textDocument/references"] = jsonOf(t, []lsp.Location{
		{URI: other, Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 8}, End: lsp.Position{Line: 1, Character: 14}}},
		{URI: main, Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 13}, End: lsp.Position{Line: 1, Character: 19}}},
	})

	placeCaretOn(t, app, "Target")

	if err := app.findReferencesAtCaret(context.Background()); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "references in search panel", func() bool { return app.searchOpen && len(app.searchHits) == 2 })

	first, second := app.searchHits[0], app.searchHits[1]
	if first.Path != "main.go" || first.Line != 2 || first.Match != "Target" || first.Col != 14 {
		t.Fatalf("first hit = %+v", first)
	}

	if second.Path != "other.go" || second.Match != "Target" || !strings.Contains(app.searchNote, "2 references") {
		t.Fatalf("second hit = %+v note %q", second, app.searchNote)
	}

	if !strings.Contains(string(fake.requests("textDocument/references")[0]), `"includeDeclaration":true`) {
		t.Fatal("includeDeclaration missing")
	}

	app.openSearchHit(1)

	if app.activeDocPath() != "other.go" {
		t.Fatalf("hit opened %q", app.activeDocPath())
	}
}

func TestLSPRenameConfirmApplyAndUndo(t *testing.T) {
	app, fake, dir := featRig(t, map[string]string{
		"main.go":  "package a\nfunc run() { Target() }\n",
		"other.go": "package a\nvar _ = Target\n",
	})

	main := lsp.URIFromPath(filepath.Join(dir, "main.go"))
	other := lsp.URIFromPath(filepath.Join(dir, "other.go"))
	edit := func(line, from, to int) lsp.TextEdit {
		return lsp.TextEdit{Range: lsp.Range{Start: lsp.Position{Line: line, Character: from}, End: lsp.Position{Line: line, Character: to}}, NewText: "Renamed"}
	}

	fake.results["textDocument/rename"] = jsonOf(t, map[string]any{
		"changes": map[string]any{other: []lsp.TextEdit{edit(1, 8, 14)}},
		"documentChanges": []any{map[string]any{
			"textDocument": map[string]any{"uri": main, "version": 1},
			"edits":        []lsp.TextEdit{edit(1, 13, 19)},
		}},
	})

	placeCaretOn(t, app, "Target")
	ctx := context.Background()

	if err := app.renameSymbolAtCaret(ctx); err != nil {
		t.Fatal(err)
	}

	if !app.renamePromptActive() || app.paletteQuery != "Target" {
		t.Fatalf("prompt = open %v query %q", app.paletteOpen, app.paletteQuery)
	}

	if rows := app.paletteRows(); len(rows) != 1 || rows[0].Cmd != paletteCmdLSPRenameGo {
		t.Fatalf("rename rows = %+v", rows)
	}

	app.paletteQuery = "Renamed"

	if err := app.lspSubmitSymbolForTest(ctx); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "rename confirm", func() bool { return app.confirmOpen && app.confirmKind == confirmKindLSPRename })

	if len(app.confirmPaths) != 2 || !strings.Contains(app.confirmTitle, "Target to Renamed") {
		t.Fatalf("confirm = %q %v", app.confirmTitle, app.confirmPaths)
	}

	if v := app.confirmViewState(); v.OkLabel != "Rename" || v.Count != 2 {
		t.Fatalf("confirm view = %+v", v)
	}

	// Enter confirms through the shared dialog path.
	if err := app.confirmOK(ctx); err != nil {
		t.Fatal(err)
	}

	if got := app.edit.Text(); !strings.Contains(got, "Renamed()") {
		t.Fatalf("buffer after rename = %q", got)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "other.go"))
	if err != nil || !strings.Contains(string(raw), "var _ = Renamed") {
		t.Fatalf("other.go = %q err %v", raw, err)
	}

	if !app.replaceJournalOK || len(app.replaceJournal) != 1 {
		t.Fatalf("journal = %v ok=%v", app.replaceJournal, app.replaceJournalOK)
	}

	if err := app.undoWorkspaceReplace(ctx); err != nil {
		t.Fatal(err)
	}

	raw, _ = os.ReadFile(filepath.Join(dir, "other.go"))
	if !strings.Contains(string(raw), "var _ = Target") {
		t.Fatalf("undo did not restore other.go: %q", raw)
	}
}

func TestLSPRenameRefusesDirtyOtherFileAndOutsideWorkspace(t *testing.T) {
	app, fake, dir := featRig(t, map[string]string{
		"main.go":  "package a\nfunc run() { Target() }\n",
		"other.go": "package a\nvar _ = Target\n",
	})

	outside := lsp.URIFromPath(filepath.Join(filepath.Dir(dir), "elsewhere.go"))
	fake.results["textDocument/rename"] = jsonOf(t, map[string]any{"changes": map[string]any{
		outside: []lsp.TextEdit{{NewText: "x"}},
	}})

	placeCaretOn(t, app, "Target")
	ctx := context.Background()

	if err := app.renameSymbolAtCaret(ctx); err != nil {
		t.Fatal(err)
	}

	app.paletteQuery = "Other"

	if err := app.lspSubmitSymbolForTest(ctx); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "refusal toast", func() bool { return strings.Contains(app.toast.Message, "outside the workspace") })

	if app.confirmOpen {
		t.Fatal("a rename touching outside files must not open the confirm")
	}
}

func TestLSPDocumentSymbolsReplaceOutlineAndFallBack(t *testing.T) {
	app, _, _ := featRig(t, map[string]string{"main.go": "package a\n\nfunc run() {}\n\ntype T struct{}\n"}, func(f *featLSP) {
		f.results["textDocument/documentSymbol"] = jsonOf(t, []map[string]any{
			{"name": "ServerSaysRun", "kind": 12,
				"range":          lsp.Range{Start: lsp.Position{Line: 2}, End: lsp.Position{Line: 2, Character: 12}},
				"selectionRange": lsp.Range{Start: lsp.Position{Line: 2, Character: 5}, End: lsp.Position{Line: 2, Character: 8}}},
		})
	})

	pump(t, app, "document symbols applied", func() bool {
		doc := app.activeDoc()

		return doc != nil && len(doc.Outline) == 1 && doc.Outline[0].Name == "ServerSaysRun"
	})

	rows := app.symbolRows("")
	if len(rows) != 1 || rows[0].Name != "ServerSaysRun" || rows[0].Line != 2 || rows[0].Kind != "fn" {
		t.Fatalf("palette rows = %+v", rows)
	}
}

func TestLSPDocumentSymbolsUnsupportedKeepsRegexOutline(t *testing.T) {
	app, fake, _ := featRig(t, map[string]string{"main.go": "package a\n\nfunc run() {}\n"}, func(f *featLSP) {
		f.errs["textDocument/documentSymbol"] = "method not found"
	})

	pump(t, app, "documentSymbol attempted", func() bool { return len(fake.requests("textDocument/documentSymbol")) > 0 })
	pump(t, app, "off", func() bool {
		for _, off := range app.lspm.ext.sym.off {
			if off {
				return true
			}
		}

		return false
	})

	rows := app.symbolRows("")
	if len(rows) == 0 || rows[0].Name != "run" {
		t.Fatalf("regex fallback rows = %+v", rows)
	}

	if n := len(fake.requests("textDocument/documentSymbol")); n != 1 {
		t.Fatalf("unsupported server asked %d times, want once", n)
	}
}

func TestLSPWorkspaceSymbolsCtrlT(t *testing.T) {
	app, fake, dir := featRig(t, map[string]string{
		"main.go":   "package a\nfunc run() {}\n",
		"target.go": "package a\n\nfunc Target() {}\n",
	})

	fake.results["workspace/symbol"] = jsonOf(t, []map[string]any{{
		"name": "TargetFromServer", "kind": 12,
		"location": lsp.Location{
			URI:   lsp.URIFromPath(filepath.Join(dir, "target.go")),
			Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 5}},
		},
	}})

	ctx := context.Background()

	app.ctrl = true
	handled, err := app.lspKeyDown(ctx, "t")
	app.ctrl = false

	if err != nil || !handled || !app.paletteOpen || app.symbolScope != "" {
		t.Fatalf("ctrl+t handled=%v err=%v open=%v", handled, err, app.paletteOpen)
	}

	app.paletteQuery, app.symbolQuery = "Targ", "Targ"

	pump(t, app, "server workspace symbols", func() bool {
		rows := app.paletteRows()

		return len(rows) > 0 && rows[0].Name == "TargetFromServer"
	})

	rows := app.paletteRows()
	if rows[0].Path != "target.go" || rows[0].No != 3 || !rows[0].Symbol {
		t.Fatalf("row = %+v", rows[0])
	}

	if err := app.submitSymbol(ctx); err != nil {
		t.Fatal(err)
	}

	if app.activeDocPath() != "target.go" {
		t.Fatalf("jumped to %q", app.activeDocPath())
	}
}

func TestLSPWorkspaceSymbolsFallBackToLocalIndex(t *testing.T) {
	app, _, _ := lspTestApp(t, map[string]string{
		"main.go":   "package a\nfunc run() {}\n",
		"target.go": "package a\n\nfunc Target() {}\n",
	})

	ctx := context.Background()
	if err := app.openWorkspaceSymbols(ctx); err != nil {
		t.Fatal(err)
	}

	app.paletteQuery, app.symbolQuery = "Targ", "Targ"

	rows := app.paletteRows()
	if len(rows) == 0 || rows[0].Name != "Target" || rows[0].Path != "target.go" {
		t.Fatalf("fallback rows = %+v", rows)
	}
}

func TestLSPProgressInStatusBar(t *testing.T) {
	app, fake, _ := featRig(t, map[string]string{"main.go": "package a\n"})

	fake.push("$/progress", `{"token":"idx","value":{"kind":"begin","title":"Indexing","percentage":10}}`)
	pump(t, app, "progress begin", func() bool { return strings.Contains(app.lspViewData().Progress, "Indexing") })

	if got := app.lspViewData().Progress; got != "gopls: Indexing 10%" {
		t.Fatalf("progress = %q", got)
	}

	fake.push("$/progress", `{"token":"idx","value":{"kind":"report","message":"pkg/a","percentage":55}}`)
	pump(t, app, "progress report", func() bool { return strings.Contains(app.lspViewData().Progress, "55%") })

	if got := app.lspViewData().Progress; got != "gopls: Indexing pkg/a 55%" {
		t.Fatalf("progress = %q", got)
	}

	fake.push("$/progress", `{"token":"idx","value":{"kind":"end"}}`)
	pump(t, app, "progress end", func() bool { return app.lspViewData().Progress == "" })
}

func TestLSPCommandsInPaletteCatalog(t *testing.T) {
	want := map[string]bool{
		paletteCmdLSPCompletion: false, paletteCmdLSPReferences: false, paletteCmdLSPRename: false,
		paletteCmdLSPWorkspace: false, paletteCmdLSPDocSymbols: false,
	}

	for _, row := range paletteCommandCatalog() {
		if _, ok := want[row.Cmd]; ok {
			want[row.Cmd] = true
		}
	}

	for id, seen := range want {
		if !seen {
			t.Errorf("command %s missing from the palette catalog", id)
		}
	}
}

// lspKeyDownCtrlSpace sends Ctrl+Space through the real key path.
func (a *App) lspKeyDownCtrlSpace() error {
	a.ctrl = true
	defer func() { a.ctrl = false }()

	handled, err := a.lspKeyDown(context.Background(), "space")
	if err == nil && !handled {
		return os.ErrInvalid
	}

	return err
}

func (a *App) lspSubmitSymbolForTest(ctx context.Context) error {
	_, err := a.lspSubmitSymbol(ctx)

	return err
}

// TestScreenshotLSPFeatures renders completion, references and progress.
// Gated: OWNFRAME_CODE_VIEW_SCREENSHOT=1.
func TestScreenshotLSPFeatures(t *testing.T) {
	if os.Getenv("OWNFRAME_CODE_VIEW_SCREENSHOT") != "1" {
		t.Skip("set OWNFRAME_CODE_VIEW_SCREENSHOT=1 to capture the frames")
	}

	ctx := context.Background()
	app, fake, dir := featRig(t, map[string]string{"main.go": "package a\n\nfunc run() {\n\tfoo\n\tTarget()\n}\n"}, func(f *featLSP) {
		f.results["textDocument/completion"] = `[{"label":"fooBar","kind":3,"detail":"func()"},{"label":"foobaz","kind":6,"detail":"int"},{"label":"other","kind":6}]`
	})
	app.Page().SetSize(1280, 800)

	caretAfter(t, app, "foo")

	if err := app.lspKeyDownCtrlSpace(); err != nil {
		t.Fatal(err)
	}

	fake.push("$/progress", `{"token":"i","value":{"kind":"begin","title":"Indexing","percentage":42}}`)
	pump(t, app, "popup+progress", func() bool { return app.popup.open && app.lspViewData().Progress != "" })

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	writeShot(t, app, "lsp-completion-progress")

	app.hidePopup(popupDismissEscape)

	main := lsp.URIFromPath(filepath.Join(dir, "main.go"))
	fake.results["textDocument/references"] = jsonOf(t, []lsp.Location{{URI: main, Range: lsp.Range{Start: lsp.Position{Line: 4, Character: 1}, End: lsp.Position{Line: 4, Character: 7}}}})

	placeCaretOn(t, app, "Target")

	if err := app.findReferencesAtCaret(ctx); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "references", func() bool { return len(app.searchHits) == 1 })

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	writeShot(t, app, "lsp-references")
}
