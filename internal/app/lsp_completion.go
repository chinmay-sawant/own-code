package app

import (
	"context"
	"sort"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

// LSP completion: Ctrl+Space (and server trigger characters such as ".")
// request textDocument/completion for the caret; the reply opens the anchored
// popup list. Typed identifier characters filter the list client-side
// (popupMatch); anything else closes it. Tab/Enter accept via the popup.

const (
	// cmpCaretTag marks the popup as the completion list (popupSpec.Caret).
	cmpCaretTag = -0x434d50
	// cmpMaxItems caps the list handed to the popup.
	cmpMaxItems = 800
)

type completionState struct {
	nextID  int
	pending int
	path    string
	line    int // 0-based caret line at request time
	start   int // rune column where the filter prefix starts
	shown   bool
}

// completionAtCaret is Ctrl+Space / the palette command.
func (a *App) completionAtCaret(ctx context.Context) error {
	if !a.editing || a.edit == nil || a.mode != "explorer" {
		a.showToast("info", "Completion", "Press Ctrl+E to edit, then Ctrl+Space completes.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if !a.requestCompletion("") {
		a.showToast("info", "Completion", "No language server is running for this file.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	return nil
}

// lspTyped is called after typed text lands in the edit buffer. A trigger
// character requests completion; the open list re-filters on the next tick.
func (a *App) lspTyped(text string) {
	a.signatureTyped(text)

	m := a.lspm
	if m == nil || !m.enabled || a.edit == nil {
		return
	}

	r, size := utf8.DecodeRuneInString(text)
	if size != len(text) || isIdentRune(r) {
		return
	}

	ld := m.docs[a.edit.Path]
	if ld == nil || ld.server == nil {
		return
	}

	if !m.serverTriggers(ld.server, string(r)) {
		m.completionHide()

		return
	}

	a.requestCompletion(string(r))
}

// lspCommitBefore runs before typed text lands in the buffer: when the
// completion list is open and text is a commit character of the selected item
// (its own, else the server's allCommitCharacters), the item is accepted
// first and the character then types normally after it.
func (a *App) lspCommitBefore(text string) {
	m := a.lspm
	if m == nil || !m.ext.cmp.shown || !a.popup.open || a.popup.spec.Caret != cmpCaretTag || a.edit == nil {
		return
	}

	r, size := utf8.DecodeRuneInString(text)
	if size != len(text) || isIdentRune(r) || a.popup.index < 0 || a.popup.index >= len(a.popup.matches) {
		return
	}

	it, ok := a.popup.spec.Items[a.popup.matches[a.popup.index]].Data.(lsp.CompletionItem)
	if !ok {
		return
	}

	chars := it.CommitCharacters

	if len(chars) == 0 {
		if ld := m.docs[a.edit.Path]; ld != nil && ld.server != nil {
			ld.server.smu.Lock()
			c := ld.server.client
			ld.server.smu.Unlock()

			if c != nil {
				chars = c.CompletionCommitChars()
			}
		}
	}

	for _, ch := range chars {
		if ch == text {
			a.popupAccept()

			return
		}
	}
}

// serverTriggers reports whether trigger starts completion for srv.
func (m *lspManager) serverTriggers(srv *lspServer, trigger string) bool {
	srv.smu.Lock()
	c := srv.client
	srv.smu.Unlock()

	if c == nil {
		return trigger == "."
	}

	triggers := c.CompletionTriggers()
	if triggers == nil {
		return false
	}

	for _, t := range triggers {
		if t == trigger {
			return true
		}
	}

	return false
}

// requestCompletion sends textDocument/completion for the caret. trigger is
// "" for an explicit request. It reports false without a server.
func (a *App) requestCompletion(trigger string) bool {
	if a.edit == nil {
		return false
	}

	m, ld := a.syncedDoc(a.edit.Path)
	if m == nil {
		return false
	}

	text := a.edit.Text()
	line, col := offsetLineCol(text, a.editCaret)
	raw := []rune(lineTextAt(text, line))
	col = min(col, len(raw))
	start := col

	for start > 0 && isIdentRune(raw[start-1]) {
		start--
	}

	c := &m.ext.cmp
	c.nextID++
	id := c.nextID
	c.pending, c.path, c.line, c.start = id, ld.path, line, start

	m.callDoc(ld, line, col, func(ctx context.Context, cl *lsp.Client, uri string, pos lsp.Position) (any, error) {
		return cl.Completion(ctx, uri, pos, trigger)
	}, func(_ context.Context, res any, err error, _ bool) bool {
		items, _ := res.([]lsp.CompletionItem)

		return m.applyCompletion(id, items, err)
	})

	return true
}

// applyCompletion shows a reply unless a newer request or the caret moved on.
func (m *lspManager) applyCompletion(id int, items []lsp.CompletionItem, err error) bool {
	a, c := m.app, &m.ext.cmp
	if id != c.pending {
		return false
	}

	c.pending = 0

	if err != nil || len(items) == 0 || !a.editing || a.edit == nil || a.edit.Path != c.path || a.popup.open && !c.shown {
		return false
	}

	prefix, ok := a.completionPrefix()
	if !ok {
		return false
	}

	sort.SliceStable(items, func(i, j int) bool {
		return sortKey(items[i]) < sortKey(items[j])
	})

	if len(items) > cmpMaxItems {
		items = items[:cmpMaxItems]
	}

	rows := make([]popupItem, 0, len(items))
	for _, it := range items {
		filter := it.FilterText
		if filter == "" {
			filter = it.Label
		}

		rows = append(rows, popupItem{Label: it.Label, Detail: it.Detail, Kind: completionKind(it.Kind), Filter: filter, Data: it})
	}

	x, top, bottom := a.editCaretAnchor()

	a.showPopup(popupSpec{
		Label:   "Completions",
		AnchorX: x, AnchorTop: top, AnchorBottom: bottom,
		Width:     420,
		Caret:     cmpCaretTag,
		Items:     rows,
		Filter:    prefix,
		OnSelect:  func(item popupItem, _ int) { a.acceptCompletion(item) },
		OnDismiss: func(popupDismiss) { c.shown = false },
	})

	if len(a.popup.matches) == 0 {
		m.completionHide()

		return true
	}

	c.shown = true

	return true
}

func sortKey(it lsp.CompletionItem) string {
	if it.SortText != "" {
		return it.SortText
	}

	return it.Label
}

// completionHide closes the completion list when it is the open popup.
func (m *lspManager) completionHide() {
	a, c := m.app, &m.ext.cmp
	if c.shown && a.popup.open && a.popup.spec.Caret == cmpCaretTag {
		a.hidePopup(popupDismissCode)
	}

	c.shown = false
	c.pending = 0
}

// completionPrefix is the identifier text between the request start column
// and the caret, or ok=false when the caret left the completion span.
func (a *App) completionPrefix() (string, bool) {
	c := &a.lspm.ext.cmp
	if a.edit == nil {
		return "", false
	}

	text := a.edit.Text()
	line, col := offsetLineCol(text, a.editCaret)
	raw := []rune(lineTextAt(text, line))

	if line != c.line || col < c.start || col > len(raw) {
		return "", false
	}

	prefix := raw[c.start:col]
	for _, r := range prefix {
		if !isIdentRune(r) {
			return "", false
		}
	}

	return string(prefix), true
}

// completionPoll keeps the open list in step with typing. It reports whether
// the view changed.
func (m *lspManager) completionPoll() bool {
	a, c := m.app, &m.ext.cmp
	if !c.shown {
		return false
	}

	if !a.popup.open || a.popup.spec.Caret != cmpCaretTag {
		c.shown = false

		return false
	}

	prefix, ok := a.completionPrefix()
	if !ok || !a.editing || a.edit.Path != c.path {
		m.completionHide()

		return true
	}

	if prefix == a.popup.spec.Filter {
		return false
	}

	a.popupSetFilter(prefix)

	if len(a.popup.matches) == 0 {
		m.completionHide()
	}

	return true
}

// acceptCompletion applies the chosen item at the caret.
func (a *App) acceptCompletion(item popupItem) {
	it, ok := item.Data.(lsp.CompletionItem)
	if !ok || a.edit == nil || a.lspm == nil {
		return
	}

	c := &a.lspm.ext.cmp
	text := a.edit.Text()
	runes := []rune(text)
	caret := min(a.editCaret, len(runes))
	lineNo, _ := offsetLineCol(text, caret)

	// Default span: the typed prefix up to the caret.
	start, end := lineColOffset(runes, c.line, c.start), caret
	insert := it.InsertText

	if insert == "" {
		insert = it.Label
	}

	if it.TextEdit != nil {
		insert = it.TextEdit.NewText

		r := it.TextEdit.Range
		start = lspPosOffset(runes, r.Start)
		end = max(lspPosOffset(runes, r.End), caret)

		if lineNo != r.End.Line {
			end = lspPosOffset(runes, r.End)
		}
	}

	var stops []snippetStop

	if it.InsertTextFormat == lsp.InsertSnippet {
		insert, stops = parseSnippet(insert)
	}

	if len(it.AdditionalTextEdits) > 0 {
		a.applyCompletionWithExtras(text, start, end, insert, it.AdditionalTextEdits)

		if len(stops) > 0 {
			a.setCaretAfterCompletion(start + stops[0].start)
		}

		return
	}

	_ = a.replaceEditRange(context.Background(), text, start, end, insert)

	if len(stops) > 0 {
		a.beginSnippet(start, stops)
	}
}

func (a *App) setCaretAfterCompletion(pos int) {
	a.editCaret = lspClamp(pos, utf8.RuneCountInString(a.edit.Text()))
	a.editAnchor = a.editCaret
	a.refresh()
}

func lspClamp(pos, n int) int { return max(0, min(pos, n)) }

// applyCompletionWithExtras applies the main edit plus additionalTextEdits
// (typically an auto-import) as one buffer change.
func (a *App) applyCompletionWithExtras(text string, start, end int, insert string, extras []lsp.TextEdit) {
	runes := []rune(text)
	main := lsp.TextEdit{
		Range:   lsp.Range{Start: runeOffsetPos(runes, start), End: runeOffsetPos(runes, end)},
		NewText: insert,
	}

	next, err := lsp.ApplyEdits(text, append([]lsp.TextEdit{main}, extras...))
	if err != nil {
		_ = a.replaceEditRange(context.Background(), text, start, end, insert)

		return
	}

	// Caret: after the inserted text, shifted by extras that sit before it.
	delta := 0

	for _, e := range extras {
		if lspPosOffset(runes, e.Range.End) <= start {
			delta += utf8.RuneCountInString(e.NewText) - (lspPosOffset(runes, e.Range.End) - lspPosOffset(runes, e.Range.Start))
		}
	}

	caret := start + delta + utf8.RuneCountInString(insert)

	_ = a.replaceEditRange(context.Background(), text, 0, len(runes), next)

	a.editCaret = lspClamp(caret, utf8.RuneCountInString(next))
	a.editAnchor = a.editCaret

	if doc := a.docs[a.edit.Path]; doc != nil && doc.Syntax != nil {
		doc.Syntax.markDirty(0)
	}
}

// lineColOffset is the rune offset of (line, rune col).
func lineColOffset(runes []rune, line, col int) int {
	off, l := 0, 0

	for l < line && off < len(runes) {
		if runes[off] == '\n' {
			l++
		}

		off++
	}

	return min(off+col, len(runes))
}

// lspPosOffset converts an LSP position (UTF-16 column) to a rune offset.
func lspPosOffset(runes []rune, p lsp.Position) int {
	start := lineColOffset(runes, p.Line, 0)
	end := start

	for end < len(runes) && runes[end] != '\n' {
		end++
	}

	col := lsp.RuneColFromPosition(string(runes[start:end]), lsp.Position{Character: p.Character})

	return min(start+col, end)
}

// runeOffsetPos converts a rune offset to an LSP position.
func runeOffsetPos(runes []rune, off int) lsp.Position {
	off = min(off, len(runes))
	line, ls := 0, 0

	for i := 0; i < off; i++ {
		if runes[i] == '\n' {
			line++
			ls = i + 1
		}
	}

	return lsp.Position{Line: line, Character: lsp.UTF16Len(string(runes[ls:off]))}
}

// completionKind names an LSP CompletionItemKind for the popup badge.
func completionKind(k int) string {
	switch k {
	case 2, 3, 4:
		return "func"
	case 5, 10:
		return "field"
	case 6:
		return "var"
	case 7:
		return "class"
	case 8:
		return "interface"
	case 9:
		return "module"
	case 13:
		return "enum"
	case 14:
		return "keyword"
	case 15:
		return "snippet"
	case 21:
		return "const"
	case 22:
		return "struct"
	}

	return ""
}

// editCaretAnchor returns the caret's window-pixel anchor (x, row top, row
// bottom) in the active editor group for popup placement.
func (a *App) editCaretAnchor() (int, int, int) {
	winW, winH := a.page.Size()
	fallback := func() (int, int, int) { return winW / 3, winH / 2, winH/2 + a.zx(rowH) }

	group := a.activeGroup()
	doc := a.groupDoc(group)
	id := "code-body"

	if group == 1 {
		id = "code-body-2"
	}

	box, ok := findBoxID(a.page.Boxes(), id)
	if !ok || doc == nil || a.edit == nil {
		return fallback()
	}

	line, col := a.editDisplayLineCol()
	row, rowStart := line, 0

	if plan := a.rowPlan(doc, group); plan != nil {
		row = caretVisualRow(plan.Rows, line, col)
		if row >= 0 && row < len(plan.Rows) {
			rowStart = plan.Rows[row].Start
		}
	}

	textLeft := box.X + float64(a.zx(editTextLeft))
	if a.blameShows(doc) {
		textLeft += float64(a.zx(blameGutterW))
	}

	scrollX := 0
	if !a.wrap {
		scrollX = a.scrollXFor(group)
	}

	rh := a.zx(rowH)
	x := int(textLeft) - scrollX + int(float64(max(col-rowStart, 0))*a.zf(codeCellW))
	top := int(box.Y) + a.stickyTopPx(group) + row*rh - a.hoverScrollY(group)

	return x, top, top + rh
}
