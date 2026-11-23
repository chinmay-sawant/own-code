package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

// LSP Find References and Rename Symbol.
//
// References reuse the workspace-search results panel: the hits are installed
// as searchHits so the panel's keyboard and click handling work unchanged.
// Rename asks for the new name in the symbol overlay, requests
// textDocument/rename, shows the WorkspaceEdit in the shared confirm dialog
// (files and counts), then applies it: the live edit buffer through the
// editor (one Ctrl+Z), other files on disk with the "Undo replace" journal.

const (
	// lspRenameScope marks the symbol overlay as the rename prompt
	// (symbolScope is reset by closePalette).
	lspRenameScope = "\x00rename"
	// confirmKindLSPRename is the confirm kind of a pending rename.
	confirmKindLSPRename = "lsp-rename"

	paletteCmdLSPRenameGo = "lsp-rename-go"
)

type renameTarget struct {
	path      string
	line, col int
	word      string
}

// renamePlan is a validated WorkspaceEdit ready to apply.
type renamePlan struct {
	word, name string
	files      []string
	oldText    map[string]string
	newText    map[string]string
	edits      int
}

type refsState struct {
	target  renameTarget
	plan    *renamePlan
	nextID  int
	pending int
}

// lspPrelude validates the caret context shared by references and rename.
func (a *App) lspPrelude(ctx context.Context, label string) (*fileDoc, string, bool, error) {
	toast := func(msg string) (*fileDoc, string, bool, error) {
		a.showToast("info", label, msg)
		a.refresh()

		return nil, "", false, a.page.Redraw(ctx)
	}

	if a.mode != "explorer" {
		return toast(label + " works in the file view. Return to Explorer.")
	}

	doc := a.activeDoc()
	if doc == nil || doc.Err != "" || doc.Binary {
		return toast("Open a text file first.")
	}

	word, ok := a.symbolAtCaret()
	if !ok || word == "" {
		return toast("Press Ctrl+E and place the caret on a symbol first.")
	}

	return doc, word, true, nil
}

// findReferencesAtCaret lists the references of the symbol at the caret.
func (a *App) findReferencesAtCaret(ctx context.Context) error {
	doc, word, ok, err := a.lspPrelude(ctx, "Find references")
	if !ok {
		return err
	}

	m, ld := a.syncedDoc(doc.Path)
	if m == nil {
		a.showToast("info", "Find references", "No language server is running for this file.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	line, col := a.editCaretLineCol()
	st := &m.ext.refs
	st.nextID++
	id := st.nextID
	st.pending = id

	m.callDoc(ld, line, col, func(ctx context.Context, c *lsp.Client, uri string, pos lsp.Position) (any, error) {
		return c.References(ctx, uri, pos, true)
	}, func(ctx context.Context, res any, err error, _ bool) bool {
		if id != st.pending {
			return false
		}

		st.pending = 0
		locs, _ := res.([]lsp.Location)

		if err != nil {
			a.showToast("error", "Find references", err.Error())

			return true
		}

		if len(locs) == 0 {
			a.showToast("info", "Find references", fmt.Sprintf("No references found for %q.", word))

			return true
		}

		a.showReferences(ctx, word, m.referenceHits(locs))

		return true
	})

	return nil
}

// referenceHits converts locations into search-panel hits sorted by file and
// line. Files outside the workspace keep their absolute path.
func (m *lspManager) referenceHits(locs []lsp.Location) []workspaceHit {
	a := m.app
	root := a.lspAbsPath(".")
	cache := map[string][]string{}
	hits := make([]workspaceHit, 0, len(locs))

	for _, loc := range locs {
		abs, err := lsp.PathFromURI(loc.URI)
		if err != nil || !filepath.IsAbs(abs) {
			continue
		}

		path := abs
		if rel, err := filepath.Rel(root, abs); err == nil && rel != "." && filepath.IsLocal(rel) {
			path = filepath.ToSlash(rel)
		}

		lines, ok := cache[path]
		if !ok {
			lines = a.lspFileLines(path, abs)
			cache[path] = lines
		}

		line := max(loc.Range.Start.Line, 0)
		raw := ""

		if line < len(lines) {
			raw = strings.TrimSuffix(lines[line], "\r")
		}

		startRune := lsp.RuneColFromPosition(raw, loc.Range.Start)
		endRune := startRune

		if loc.Range.End.Line == loc.Range.Start.Line {
			endRune = lsp.RuneColFromPosition(raw, loc.Range.End)
		}

		runes := []rune(raw)
		startRune = min(startRune, len(runes))
		endRune = max(startRune, min(endRune, len(runes)))

		start := utf8.RuneCountInString(expandTabs(string(runes[:startRune])))
		matchLen := utf8.RuneCountInString(expandTabs(string(runes[startRune:endRune])))

		disp := []rune(expandTabs(raw))
		start = min(start, len(disp))
		end := min(start+matchLen, len(disp))
		previewStart := max(0, start-(workspaceSearchTextRunes-min(end-start, workspaceSearchTextRunes))/2)
		previewStart = min(previewStart, max(0, len(disp)-workspaceSearchTextRunes))
		previewEnd := min(len(disp), previewStart+workspaceSearchTextRunes)
		end = min(end, previewEnd)

		hits = append(hits, workspaceHit{
			Path: path, Line: line + 1, Col: start + 1,
			Text:   string(disp[previewStart:previewEnd]),
			Before: string(disp[previewStart:start]),
			Match:  string(disp[start:end]),
			After:  string(disp[end:previewEnd]),
		})
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}

		if hits[i].Line != hits[j].Line {
			return hits[i].Line < hits[j].Line
		}

		return hits[i].Col < hits[j].Col
	})

	return hits
}

// lspFileLines returns the lines of path from the open buffer, the open
// document, or disk (bounded).
func (a *App) lspFileLines(path, abs string) []string {
	if a.edit != nil && a.edit.Path == path {
		return strings.Split(a.edit.Text(), "\n")
	}

	if doc := a.docs[path]; doc != nil && doc.Err == "" && !doc.Binary {
		return doc.Lines
	}

	info, err := os.Stat(abs)
	if err != nil || info.IsDir() || info.Size() > maxFileBytes {
		return nil
	}

	data, err := os.ReadFile(abs)
	if err != nil || isBinary(data) {
		return nil
	}

	return strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n")
}

// showReferences installs hits into the workspace search panel.
func (a *App) showReferences(ctx context.Context, word string, hits []workspaceHit) {
	a.cancelSearch()
	a.drainSearchResults()

	a.searchQuery = clampFieldLen(word)
	a.searchError = ""
	a.searchHits = hits
	a.searchIndex = 0
	a.searchScroll = historyScroll{}
	a.searchBusy = false
	a.searchTotal = len(hits)
	a.searchCapped = false
	a.searchTruncated = false
	a.replaceCheck = nil
	a.searchNote = fmt.Sprintf("%s to %q from the language server.", pluralWord(len(hits), "reference"), word)

	_ = a.openWorkspaceSearch(ctx)
}

func pluralWord(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", n, noun)
}

// ---- rename ----

// renameSymbolAtCaret opens the rename prompt (the symbol overlay prefilled
// with the current name).
func (a *App) renameSymbolAtCaret(ctx context.Context) error {
	doc, word, ok, err := a.lspPrelude(ctx, "Rename symbol")
	if !ok {
		return err
	}

	m, ld := a.syncedDoc(doc.Path)
	if m == nil {
		a.showToast("info", "Rename symbol", "No language server is running for this file.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.settingsOpen || a.confirmOpen || a.terminalOwnsKeys() {
		return nil
	}

	line, col := a.editCaretLineCol()
	target := renameTarget{path: ld.path, line: line, col: col, word: word}

	if !m.supportsPrepareRename(ld) {
		return a.openRenamePrompt(ctx, m, target)
	}

	// Ask textDocument/prepareRename first: a null answer means the caret is
	// not on something renamable; the placeholder pre-fills the name.
	st := &m.ext.refs
	st.nextID++
	id := st.nextID
	st.pending = id

	m.callDoc(ld, line, col, func(ctx context.Context, c *lsp.Client, uri string, pos lsp.Position) (any, error) {
		res, ok, err := c.PrepareRename(ctx, uri, pos)

		return prepareOutcome{res: res, ok: ok}, err
	}, func(ctx context.Context, res any, err error, fresh bool) bool {
		if id != st.pending {
			return false
		}

		st.pending = 0
		out, _ := res.(prepareOutcome)

		switch {
		case err == nil && !out.ok:
			a.showToast("info", "Rename symbol", fmt.Sprintf("%q cannot be renamed here.", word))
		case !fresh:
			a.showToast("info", "Rename symbol", "The file changed while renaming. Try again.")
		default:
			// On an error the server may simply not implement prepare: carry on.
			t := target
			if err == nil && out.res.Placeholder != "" {
				t.word = out.res.Placeholder
			}

			_ = a.openRenamePrompt(ctx, m, t)
		}

		return true
	})

	return nil
}

// prepareOutcome is the result of a prepareRename call.
type prepareOutcome struct {
	res lsp.PrepareRenameResult
	ok  bool
}

// supportsPrepareRename reports whether the document's server advertises
// renameProvider.prepareProvider.
func (m *lspManager) supportsPrepareRename(ld *lspDoc) bool {
	ld.server.smu.Lock()
	c := ld.server.client
	ld.server.smu.Unlock()

	return c != nil && c.SupportsPrepareRename()
}

// openRenamePrompt opens the symbol overlay as the rename prompt for t.
func (a *App) openRenamePrompt(ctx context.Context, m *lspManager, t renameTarget) error {
	m.ext.refs.target = t

	a.paletteOpen = true
	a.paletteMode = paletteModeSymbols
	a.symbolScope = lspRenameScope
	a.paletteIndex = 0
	a.paletteQuery = t.word
	a.symbolQuery = t.word
	a.refresh()

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	return a.page.Form().Focus(ctx, "palette")
}

// renamePromptActive reports whether the symbol overlay is the rename prompt.
func (a *App) renamePromptActive() bool {
	return a.paletteOpen && a.paletteMode == paletteModeSymbols && a.symbolScope == lspRenameScope
}

// renamePaletteRows is the single row shown while the rename prompt is open.
func (a *App) renamePaletteRows() []paletteRow {
	st := a.lspm
	if st == nil {
		return nil
	}

	name := strings.TrimSpace(a.paletteQuery)
	old := st.ext.refs.target.word

	switch {
	case name == "":
		return []paletteRow{{Cmd: paletteCmdLSPRenameGo, Name: "Type the new name for " + old, Dir: "Enter previews the changes"}}
	case name == old:
		return []paletteRow{{Cmd: paletteCmdLSPRenameGo, Name: "Rename " + old + " (name unchanged)", Dir: "Type a different name"}}
	}

	return []paletteRow{{Cmd: paletteCmdLSPRenameGo, Name: "Rename " + old + " to " + name, Dir: "Enter previews the changes in every file"}}
}

// submitRename sends textDocument/rename for the typed name.
func (a *App) submitRename(ctx context.Context) error {
	m := a.lspm
	if m == nil {
		return a.closePalette(ctx)
	}

	t := m.ext.refs.target
	name := strings.TrimSpace(a.paletteQuery)

	if name == "" || name == t.word {
		a.showToast("info", "Rename symbol", "Type a new name different from "+t.word+".")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if strings.ContainsAny(name, " \t\r\n") {
		a.showToast("info", "Rename symbol", "A symbol name cannot contain whitespace.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if err := a.closePalette(ctx); err != nil {
		return err
	}

	_, ld := a.syncedDoc(t.path)
	if ld == nil {
		a.showToast("info", "Rename symbol", "The language server is no longer available for this file.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	st := &m.ext.refs
	st.nextID++
	id := st.nextID
	st.pending = id

	m.callDoc(ld, t.line, t.col, func(ctx context.Context, c *lsp.Client, uri string, pos lsp.Position) (any, error) {
		return c.Rename(ctx, uri, pos, name)
	}, func(ctx context.Context, res any, err error, fresh bool) bool {
		if id != st.pending {
			return false
		}

		st.pending = 0
		we, _ := res.(*lsp.WorkspaceEdit)

		switch {
		case err != nil:
			a.showToast("error", "Rename symbol", err.Error())
		case we == nil:
			a.showToast("info", "Rename symbol", fmt.Sprintf("The server cannot rename %q here.", t.word))
		case !fresh:
			a.showToast("info", "Rename symbol", "The file changed while renaming. Try again.")
		default:
			a.stageRename(ctx, t.word, name, we)
		}

		return true
	})

	a.showToast("info", "Rename symbol", "Asking the language server...")
	a.refresh()

	return a.page.Redraw(ctx)
}

// stageRename validates a WorkspaceEdit and opens the confirm dialog.
func (a *App) stageRename(ctx context.Context, word, name string, we *lsp.WorkspaceEdit) {
	fail := func(msg string) {
		a.showToast("error", "Rename symbol", msg)
	}

	edits, unsupported := we.FileEdits()
	if unsupported > 0 {
		fail("The server also wants to create, rename, or delete files, which is not supported.")

		return
	}

	if len(edits) == 0 {
		a.showToast("info", "Rename symbol", "The server returned no changes.")

		return
	}

	root := a.lspAbsPath(".")
	plan := &renamePlan{word: word, name: name, oldText: map[string]string{}, newText: map[string]string{}}

	var dirty []string

	for uri, list := range edits {
		abs, err := lsp.PathFromURI(uri)
		if err != nil || !filepath.IsAbs(abs) {
			fail("Unsupported file location: " + uri)

			return
		}

		rel, err := filepath.Rel(root, abs)
		if err != nil || rel == "." || !filepath.IsLocal(rel) {
			fail("The rename touches files outside the workspace (" + abs + "); nothing was changed.")

			return
		}

		path := filepath.ToSlash(rel)
		editing := a.editing && a.edit != nil && a.edit.Path == path

		var old string

		switch {
		case editing:
			old = a.edit.Text()
		case a.isDirty(path):
			dirty = append(dirty, path)

			continue
		default:
			raw, err := a.ws.readFile(path)
			if err != nil || len(raw) > maxFileBytes || isBinary(raw) {
				fail("Cannot read " + path + " for the rename.")

				return
			}

			old = string(raw)
		}

		next, err := lsp.ApplyEdits(old, list)
		if err != nil {
			fail(path + ": " + err.Error())

			return
		}

		plan.files = append(plan.files, path)
		plan.oldText[path], plan.newText[path] = old, next
		plan.edits += len(list)
	}

	if len(dirty) > 0 {
		sort.Strings(dirty)
		fail("Save first, unsaved edits in: " + strings.Join(dirty, ", "))

		return
	}

	sort.Strings(plan.files)
	a.lspm.ext.refs.plan = plan

	a.confirmOpen = true
	a.confirmKind = confirmKindLSPRename
	a.confirmPaths = append([]string(nil), plan.files...)
	a.confirmTitle = fmt.Sprintf("Rename %s to %s?", word, name)
	a.confirmMsg = fmt.Sprintf("%s in %d files. Other files are written to disk (undo with Undo replace); the open buffer changes in place (Ctrl+Z).",
		pluralWord(plan.edits, "edit"), len(plan.files))
}

// applyLSPRename applies the staged rename after the confirm dialog.
func (a *App) applyLSPRename(ctx context.Context) error {
	var plan *renamePlan
	if a.lspm != nil {
		plan = a.lspm.ext.refs.plan
		a.lspm.ext.refs.plan = nil
	}

	a.closeConfirm()

	if plan == nil {
		return nil
	}

	var (
		journal []appliedFile
		written int
		failed  []string
	)

	for _, path := range plan.files {
		next := plan.newText[path]

		if a.editing && a.edit != nil && a.edit.Path == path {
			if a.edit.Text() != plan.oldText[path] {
				failed = append(failed, path)

				continue
			}

			caret := a.editCaret
			_ = a.replaceEditRange(ctx, plan.oldText[path], 0, utf8.RuneCountInString(plan.oldText[path]), next)
			a.editCaret = min(caret, utf8.RuneCountInString(next))
			a.editAnchor = a.editCaret

			if doc := a.docs[path]; doc != nil && doc.Syntax != nil {
				doc.Syntax.markDirty(0)
			}

			written++

			continue
		}

		if a.isDirty(path) {
			failed = append(failed, path)

			continue
		}

		if err := a.ws.writeFile(path, []byte(next)); err != nil {
			failed = append(failed, path)

			continue
		}

		journal = append(journal, appliedFile{Path: path, Old: []byte(plan.oldText[path]), New: []byte(next)})

		if _, open := a.docs[path]; open {
			a.docs[path] = a.ws.openDoc(path)
			a.lspTouch(path)
		}

		written++
	}

	if len(journal) > 0 {
		a.replaceJournal = journal
		a.replaceJournalOK = true
		a.replaceJournalMsg = ""
	}

	msg, title, done := fmt.Sprintf("%s to %s in %d files", plan.word, plan.name, written), "Rename symbol", "Renamed"
	if plan.word == "" {
		msg, title, done = fmt.Sprintf("Applied in %d files", written), "Code actions", "Applied"
	}

	if len(failed) > 0 {
		a.showToast("error", title, msg+"; skipped (changed meanwhile): "+strings.Join(failed, ", "))
	} else {
		a.showToast("success", done, msg)
	}

	a.refresh()

	return a.page.Redraw(ctx)
}
