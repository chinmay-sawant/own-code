package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/edit"
	"github.com/chinmay-sawant/own-code/internal/lsp"
)

// LSP Go to Definition / Declaration / Type Definition: when the active file
// is synced with a language server, send the position request asynchronously
// through the server's op queue and jump when the reply arrives. For
// textDocument/definition, an empty or failed reply falls back to the local
// text-index path in goto_definition.go. Stale replies (tab switch, edit, or
// a newer request) are dropped silently.

const lspDefTimeout = 5 * time.Second

type defState struct {
	nextID   int
	pending  int
	path     string
	version  int
	word     string
	method   string
	label    string
	fallback bool
}

type resolvedDefTarget struct {
	path     string
	line     int
	utf16Col int
}

// gotoDeclarationAtCaret jumps to the declaration of the symbol at the caret
// via the active language server.
func (a *App) gotoDeclarationAtCaret(ctx context.Context) error {
	return a.gotoLSPLocationAtCaret(ctx, "textDocument/declaration", "Go to declaration")
}

// gotoTypeDefinitionAtCaret jumps to the type definition of the symbol at the
// caret via the active language server.
func (a *App) gotoTypeDefinitionAtCaret(ctx context.Context) error {
	return a.gotoLSPLocationAtCaret(ctx, "textDocument/typeDefinition", "Go to type definition")
}

func (a *App) gotoLSPLocationAtCaret(ctx context.Context, method, label string) error {
	if a.mode != "explorer" {
		a.showToast("info", label, label+" jumps in the file view. Return to Explorer.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	doc := a.activeDoc()
	if doc == nil || doc.Err != "" || doc.Binary {
		a.showToast("info", label, "Open a text file first.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	word, ok := a.symbolAtCaret()
	if !ok || word == "" {
		a.showToast("info", label, "Place the caret on a symbol first.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	line, col := a.editCaretLineCol()
	if !a.requestLSPDefinition(doc.Path, word, line, col, method, label, false) {
		a.showToast("info", label, "No language server is running for this file.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	return nil
}

func (a *App) editCaretLineCol() (int, int) {
	if a.edit == nil {
		return 0, 0
	}

	text := a.edit.Text()
	caret := edit.ClampPos(a.editCaret, utf8.RuneCountInString(text))

	return offsetLineCol(text, caret)
}

// requestLSPDefinition enqueues an LSP definition-family request if path is
// synced with a running server. It returns false when no server can handle it.
func (a *App) requestLSPDefinition(path, word string, line, col int, method, label string, fallback bool) bool {
	m := a.lspm
	if m == nil || !m.enabled {
		return false
	}

	ld := m.docs[path]
	if ld == nil || ld.skipped || ld.server == nil {
		return false
	}

	// Flush any pending debounced edit for this file first so the server's
	// view matches the caret position (the server queue is FIFO).
	if !ld.pendingAt.IsZero() {
		ld.pendingAt = time.Time{}
		text, fromDisk := m.docText(path)
		if !fromDisk && len(text) > maxFileBytes {
			return false
		}

		ld.version++
		ld.server.enqueue(lspOp{
			kind: lspOpChange, uri: ld.uri, abs: ld.abs,
			version: ld.version, text: text, fromDisk: fromDisk,
		})
	}

	m.def.nextID++
	id := m.def.nextID
	m.def = defState{
		nextID:   id,
		pending:  id,
		path:     path,
		version:  ld.version,
		word:     word,
		method:   method,
		label:    label,
		fallback: fallback,
	}

	ld.server.enqueue(lspOp{
		kind:    lspOpDefinition,
		uri:     ld.uri,
		version: ld.version,
		reqID:   id,
		method:  method,
		line:    line,
		col:     col,
	})

	return true
}

// startDefinition runs on the server goroutine and sends the position request
// on a worker goroutine so edits keep flowing.
func (s *lspServer) startDefinition(c *lsp.Client, op lspOp) {
	s.smu.Lock()
	sent := s.sent[op.uri]
	s.smu.Unlock()

	reply := func(locs []lsp.Location, err error) {
		s.mgr.post(lspEvent{
			kind:    lspEvDefinition,
			server:  s,
			uri:     op.uri,
			version: op.version,
			reqID:   op.reqID,
			locs:    locs,
			err:     err,
		})
	}

	if sent == nil || sent.version != op.version || op.line < 0 || op.line >= len(sent.lines) {
		reply(nil, nil)

		return
	}

	pos := lsp.PositionFromRunes(sent.lines[op.line], op.line, op.col)

	s.mgr.wg.Add(1)

	go func() {
		defer s.mgr.wg.Done()

		ctx, cancel := context.WithTimeout(context.Background(), lspDefTimeout)
		defer cancel()

		var (
			locs []lsp.Location
			err  error
		)

		switch op.method {
		case "textDocument/declaration":
			locs, err = c.Declaration(ctx, op.uri, pos)
		case "textDocument/typeDefinition":
			locs, err = c.TypeDefinition(ctx, op.uri, pos)
		default:
			locs, err = c.Definition(ctx, op.uri, pos)
		}

		if err != nil {
			s.mgr.log.addf("[%s] %s: %v", s.cfg.Name, op.method, err)
		}

		reply(locs, err)
	}()
}

// applyDefinition handles a definition-family reply on the app loop.
func (m *lspManager) applyDefinition(ctx context.Context, ev lspEvent) bool {
	a, d := m.app, &m.def
	if ev.reqID == 0 || ev.reqID != d.pending {
		return false
	}

	req := *d
	d.pending = 0

	if a.mode != "explorer" {
		return false
	}

	cur := a.activeDoc()
	if cur == nil || cur.Path != req.path {
		return false
	}

	ld := m.docs[req.path]
	if ld == nil || ld.server != ev.server || ld.version != ev.version || !ld.pendingAt.IsZero() {
		return false
	}

	targets := m.resolveDefinitionTargets(ev.locs)
	if len(targets) == 0 {
		if req.fallback {
			_ = a.gotoDefinitionFallback(ctx, req.word)

			return true
		}

		a.showToast("info", req.label, fmt.Sprintf("No %s found for %q.", strings.ToLower(strings.TrimPrefix(req.label, "Go to ")), req.word))

		return true
	}

	best := targets[0]
	if len(targets) > 1 {
		a.showToast("info", req.label, fmt.Sprintf("%d locations for %q; jumped to %s.", len(targets), req.word, best.path))
	}

	if a.activeDocPath() != best.path {
		a.openFilePreview(best.path)
	}

	col := 1
	if targetDoc := a.activeDoc(); targetDoc != nil && best.line >= 0 && best.line < len(targetDoc.Lines) {
		col = lsp.RuneColFromPosition(targetDoc.Lines[best.line], lsp.Position{Character: best.utf16Col}) + 1
	} else if best.utf16Col > 0 {
		col = best.utf16Col + 1
	}

	_ = a.jumpToDefinitionCol(ctx, best.path, best.line, col)

	return true
}

func (m *lspManager) resolveDefinitionTargets(locs []lsp.Location) []resolvedDefTarget {
	if len(locs) == 0 || m.app.ws == nil {
		return nil
	}

	root := m.app.lspAbsPath(".")
	out := make([]resolvedDefTarget, 0, len(locs))

	for _, loc := range locs {
		abs, err := lsp.PathFromURI(loc.URI)
		if err != nil || !filepath.IsAbs(abs) {
			continue
		}

		path := abs
		if rel, err := filepath.Rel(root, abs); err == nil && rel != "." && filepath.IsLocal(rel) {
			path = filepath.ToSlash(rel)
		} else if info, err := os.Stat(abs); err != nil || info.IsDir() {
			continue
		}

		line := max(loc.Range.Start.Line, 0)
		col := max(loc.Range.Start.Character, 0)
		out = append(out, resolvedDefTarget{path: path, line: line, utf16Col: col})
	}

	return out
}
