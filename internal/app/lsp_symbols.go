package app

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

// LSP symbols: documentSymbol feeds the Details outline and Ctrl+Shift+O
// (replacing the regex outline while a server answers for the file's current
// version); workspace/symbol feeds Ctrl+T. With no server, an unsupported
// request, or an empty answer, the regex outline and the local symbol index
// stay in charge.

const wsSymbolDebounce = 150 * time.Millisecond

type docSyms struct {
	version int
	asked   int
	ok      bool
	rows    []symbolRow
}

type symbolState struct {
	doc map[string]*docSyms
	off map[*lspServer]bool

	// Workspace symbols (Ctrl+T).
	ws        bool
	wsNext    int
	wsPending int
	wsFor     string // query wsRows answers
	wsRows    []workspaceSymbol
	wsSeen    string // last query observed on the tick
	wsSeenAt  time.Time
	wsAsked   string
}

// pollSymbols runs on the tick: it asks for the active document's symbols
// when its version moved and drives the Ctrl+T request. It reports a visible
// change.
func (m *lspManager) pollSymbols() bool {
	a, st := m.app, &m.ext.sym
	changed := false

	if path := a.activeDocPath(); path != "" {
		if ld := m.docs[path]; ld != nil && !ld.skipped && ld.server != nil && ld.pendingAt.IsZero() && !st.off[ld.server] {
			if st.doc == nil {
				st.doc = map[string]*docSyms{}
			}

			ds := st.doc[path]
			if ds == nil {
				ds = &docSyms{}
				st.doc[path] = ds
			}

			if ds.asked != ld.version {
				ds.asked = ld.version
				m.requestDocSymbols(ld, ds)
			}
		}
	}

	if m.workspaceSymbolsActive() {
		changed = m.pollWorkspaceSymbols()
	}

	return changed
}

func (m *lspManager) requestDocSymbols(ld *lspDoc, ds *docSyms) {
	srv, path, version := ld.server, ld.path, ld.version
	st := &m.ext.sym

	m.callServer(srv, ld.uri, func(ctx context.Context, c *lsp.Client, sent *lspSent) (any, error) {
		if sent == nil || sent.version != version {
			return nil, errLSPNotSynced
		}

		return c.DocumentSymbols(ctx, ld.uri)
	}, func(_ context.Context, res any, err error) bool {
		cur := m.docs[path]
		if err != nil {
			if cur != nil && cur.server == srv && !strings.Contains(err.Error(), errLSPNotSynced.Error()) {
				if st.off == nil {
					st.off = map[*lspServer]bool{}
				}

				st.off[srv] = true
			}

			return false
		}

		syms, _ := res.([]lsp.Symbol)
		if cur == nil || cur.server != srv || cur.version != version || len(syms) == 0 {
			return false
		}

		rows := make([]symbolRow, 0, len(syms))
		for _, s := range syms {
			rows = appendSymbol(rows, s.Name, lsp.SymbolKindName(s.Kind), s.Range.Start.Line, s.Depth)
		}

		ds.version, ds.ok, ds.rows = version, len(rows) > 0, rows

		if doc := m.app.docs[path]; doc != nil && len(rows) > 0 {
			doc.Outline = rows[:min(len(rows), maxOutlineRows)]
		}

		return true
	})
}

// docSymbolRows returns the server's symbols when they match the document's
// current version, else the regex outline.
func (a *App) docSymbolRows(doc *fileDoc, lines []string) []symbolRow {
	if m := a.lspm; m != nil {
		ld := m.docs[doc.Path]
		ds := m.ext.sym.doc[doc.Path]

		if ld != nil && ds != nil && ds.ok && ds.version == ld.version && ld.pendingAt.IsZero() {
			return ds.rows
		}
	}

	return outlineFull(doc.Lang, lines)
}

// ---- Ctrl+T ----

// workspaceSymbolsActive reports whether the symbol overlay is the
// workspace (Ctrl+T) one.
func (m *lspManager) workspaceSymbolsActive() bool {
	a := m.app

	return m.ext.sym.ws && a.paletteOpen && a.paletteMode == paletteModeSymbols && a.symbolScope == ""
}

// openWorkspaceSymbols shows the symbol overlay in workspace scope.
func (a *App) openWorkspaceSymbols(ctx context.Context) error {
	if a.settingsOpen || a.confirmOpen || a.terminalOwnsKeys() {
		return nil
	}

	if a.ws == nil {
		return nil
	}

	st := &a.lspManager().ext.sym
	fresh := !a.paletteOpen || a.paletteMode != paletteModeSymbols || a.symbolScope != "" || !st.ws

	st.ws = true
	a.paletteOpen = true
	a.paletteMode = paletteModeSymbols
	a.symbolScope = ""
	a.paletteIndex = 0

	if fresh {
		a.paletteQuery, a.symbolQuery = "", ""
		st.wsFor, st.wsRows, st.wsSeen, st.wsAsked = "", nil, "", ""
	}

	a.refresh()

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	return a.page.Form().Focus(ctx, "palette")
}

// pollWorkspaceSymbols debounces the typed query and asks the server.
func (m *lspManager) pollWorkspaceSymbols() bool {
	a, st := m.app, &m.ext.sym
	query := strings.TrimSpace(a.paletteQuery)

	if query != st.wsSeen {
		st.wsSeen, st.wsSeenAt = query, time.Now()
		a.page.WantFrameIn(wsSymbolDebounce)

		return false
	}

	if query == "" || query == st.wsAsked {
		return false
	}

	if wait := wsSymbolDebounce - time.Since(st.wsSeenAt); wait > 0 {
		a.page.WantFrameIn(wait)

		return false
	}

	srv, uri := m.anyServer()
	if srv == nil || st.off[srv] {
		st.wsAsked = query

		return false
	}

	st.wsAsked = query
	st.wsNext++
	id := st.wsNext
	st.wsPending = id

	m.callServer(srv, uri, func(ctx context.Context, c *lsp.Client, _ *lspSent) (any, error) {
		return c.WorkspaceSymbols(ctx, query)
	}, func(_ context.Context, res any, err error) bool {
		if id != st.wsPending {
			return false
		}

		st.wsPending = 0
		syms, _ := res.([]lsp.Symbol)

		if err != nil || len(syms) == 0 {
			return false
		}

		root := a.lspAbsPath(".")
		rows := make([]workspaceSymbol, 0, len(syms))

		for _, s := range syms {
			abs, perr := lsp.PathFromURI(s.URI)
			if perr != nil || !filepath.IsAbs(abs) {
				continue
			}

			path := abs
			if rel, rerr := filepath.Rel(root, abs); rerr == nil && rel != "." && filepath.IsLocal(rel) {
				path = filepath.ToSlash(rel)
			}

			rows = append(rows, workspaceSymbol{Name: s.Name, Kind: lsp.SymbolKindName(s.Kind), Path: path, Line: s.Range.Start.Line})
		}

		st.wsFor, st.wsRows = query, rows

		return true
	})

	return false
}

// workspaceSymbolRows is the Ctrl+T result list: the server's answer for the
// current query, else the local symbol index.
func (a *App) workspaceSymbolRows() []workspaceSymbol {
	if m := a.lspm; m != nil {
		st := &m.ext.sym
		if q := strings.TrimSpace(a.paletteQuery); q != "" && st.wsFor == q && len(st.wsRows) > 0 {
			return st.wsRows[:min(len(st.wsRows), paletteMaxRows)]
		}
	}

	return a.matchSymbols()
}

// lspSymbolPaletteRows overrides the symbol overlay rows for the rename
// prompt and the workspace scope. ok is false for the file-scoped overlay.
func (a *App) lspSymbolPaletteRows() ([]paletteRow, bool) {
	if a.renamePromptActive() {
		return a.renamePaletteRows(), true
	}

	if !a.paletteOpen || a.paletteMode != paletteModeSymbols || a.symbolScope != "" || a.lspm == nil || !a.lspm.ext.sym.ws {
		return nil, false
	}

	syms := a.workspaceSymbolRows()
	rows := make([]paletteRow, 0, len(syms))

	for _, s := range syms {
		lang := languageOf(s.Path)
		rows = append(rows, paletteRow{
			Path: s.Path, Name: s.Name, Dir: dirName(s.Path), Lang: lang,
			Badge: languageInfo(lang).Badge, Symbol: true, Kind: s.Kind, Line: s.Line, No: s.Line + 1,
		})
	}

	return rows, true
}

// lspSubmitSymbol handles Enter for the rename prompt and workspace scope.
func (a *App) lspSubmitSymbol(ctx context.Context) (bool, error) {
	if a.renamePromptActive() {
		return true, a.submitRename(ctx)
	}

	rows, ok := a.lspSymbolPaletteRows()
	if !ok {
		return false, nil
	}

	if len(rows) == 0 {
		return true, nil
	}

	row := rows[max(0, min(a.paletteIndex, len(rows)-1))]

	if err := a.closePalette(ctx); err != nil {
		return true, err
	}

	return true, a.jumpToDefinition(ctx, row.Path, row.Line)
}

// ---- keys and commands ----

// lspKeyDown handles the LSP editing chords: Ctrl+Space completion, F2
// rename, Shift+F12 references, Ctrl+T workspace symbols.
func (a *App) lspKeyDown(ctx context.Context, key string) (bool, error) {
	switch {
	case a.ctrl && !a.shift && key == "space" && a.editing:
		return true, a.completionAtCaret(ctx)
	case a.ctrl && !a.shift && key == "t":
		return true, a.openWorkspaceSymbols(ctx)
	case a.ctrl && !a.shift && (key == "period" || key == ".") && a.editing && a.mode == "explorer":
		return true, a.codeActionsAtCaret(ctx)
	case !a.ctrl && key == "f2" && a.editing && a.mode == "explorer" && !a.shift:
		return true, a.renameSymbolAtCaret(ctx)
	case !a.ctrl && key == "f12" && a.shift && a.mode == "explorer":
		return true, a.findReferencesAtCaret(ctx)
	}

	return false, nil
}

const (
	paletteCmdLSPCompletion = "lsp-completion"
	paletteCmdLSPReferences = "lsp-references"
	paletteCmdLSPRename     = "lsp-rename"
	paletteCmdLSPWorkspace  = "lsp-workspace-symbols"
	paletteCmdLSPDocSymbols = "lsp-document-symbols"
)

// lspCommandCatalog lists the `>` commands of this file.
func lspCommandCatalog() []paletteRow {
	return []paletteRow{
		{Cmd: paletteCmdLSPCompletion, Name: "LSP: Trigger Completion", Dir: "Complete at the caret (Ctrl+Space)"},
		{Cmd: paletteCmdLSPReferences, Name: "LSP: Find References", Dir: "List references of the symbol at the caret (Shift+F12)"},
		{Cmd: paletteCmdLSPRename, Name: "LSP: Rename Symbol", Dir: "Rename the symbol at the caret in every file (F2)"},
		{Cmd: paletteCmdLSPCodeAction, Name: "LSP: Code Actions (Quick Fix)", Dir: "List fixes and refactors at the caret (Ctrl+.)"},
		{Cmd: paletteCmdLSPSignature, Name: "LSP: Signature Help", Dir: "Show the parameters of the call at the caret"},
		{Cmd: paletteCmdLSPWorkspace, Name: "LSP: Go to Symbol in Workspace", Dir: "Search symbols across the workspace (Ctrl+T)"},
		{Cmd: paletteCmdLSPDocSymbols, Name: "LSP: Go to Symbol in File", Dir: "Search symbols in this file (Ctrl+Shift+O)"},
	}
}

// runLSPPaletteCommand dispatches this file's palette commands.
func (a *App) runLSPPaletteCommand(ctx context.Context, id string) (bool, error) {
	id = strings.TrimSpace(id)

	closeIt := func() {
		a.paletteOpen = false
		a.paletteQuery = ""
		a.paletteIndex = 0
		a.paletteMode = paletteModeFiles
		a.symbolQuery, a.symbolScope = "", ""
	}

	switch id {
	case paletteCmdLSPRenameGo:
		return true, a.submitRename(ctx)
	case paletteCmdLSPCompletion:
		closeIt()

		return true, a.completionAtCaret(ctx)
	case paletteCmdLSPReferences:
		closeIt()

		return true, a.findReferencesAtCaret(ctx)
	case paletteCmdLSPRename:
		closeIt()

		return true, a.renameSymbolAtCaret(ctx)
	case paletteCmdLSPCodeAction:
		closeIt()

		return true, a.codeActionsAtCaret(ctx)
	case paletteCmdLSPSignature:
		closeIt()

		return true, a.signatureHelpAtCaret(ctx)
	case paletteCmdLSPWorkspace:
		closeIt()

		return true, a.openWorkspaceSymbols(ctx)
	case paletteCmdLSPDocSymbols:
		closeIt()

		return true, a.openSymbolPalette(ctx)
	}

	return false, nil
}
