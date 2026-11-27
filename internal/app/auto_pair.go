package app

import (
	"context"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// Auto-closing brackets and quotes, and surround-selection. The planner is
// pure over the buffer runes; onType and onBackspace apply its result.

// pairClose maps an opener to its closer. Quotes close themselves.
var pairClose = map[rune]rune{'(': ')', '[': ']', '{': '}', '"': '"', '\'': '\'', '`': '`'}

func isQuoteRune(r rune) bool { return r == '"' || r == '\'' || r == '`' }

func isCloserRune(r rune) bool { return r == ')' || r == ']' || r == '}' }

// pairEdit is one typed-rune outcome: replace [start,end) with insert and
// select anchor..caret (offsets in the new text). skip means the typed
// closer just steps over an existing one.
type pairEdit struct {
	start, end    int
	insert        string
	anchor, caret int
	skip          bool
}

// planAutoPair decides how a typed rune interacts with brackets and quotes.
// ok is false when the rune should insert normally.
func planAutoPair(runes []rune, start, end int, typed rune) (pairEdit, bool) {
	closer, opener := pairClose[typed]
	next := rune(0)
	prev := rune(0)

	if end < len(runes) {
		next = runes[end]
	}

	if start > 0 {
		prev = runes[start-1]
	}

	// Surround the selection.
	if opener && start != end {
		inner := string(runes[start:end])

		return pairEdit{start: start, end: end, insert: string(typed) + inner + string(closer),
			anchor: start + 1, caret: end + 1}, true
	}

	if start != end {
		return pairEdit{}, false
	}

	// Typing a closer over an identical next rune steps past it.
	if next == typed && (isCloserRune(typed) || isQuoteRune(typed)) {
		return pairEdit{start: start, end: start, anchor: start + 1, caret: start + 1, skip: true}, true
	}

	if !opener {
		return pairEdit{}, false
	}

	// Only pair before whitespace, a closer, or the end of the line.
	if next != 0 && next != ' ' && next != '\t' && next != '\n' && !isCloserRune(next) && next != ',' && next != ';' {
		return pairEdit{}, false
	}

	// A quote after a word rune is an apostrophe or a closing quote.
	if isQuoteRune(typed) && (edit.IsWordRune(prev) || prev == typed) {
		return pairEdit{}, false
	}

	return pairEdit{start: start, end: start, insert: string(typed) + string(closer),
		anchor: start + 1, caret: start + 1}, true
}

// pairBackspaceSpan reports whether the caret sits between an empty pair,
// so Backspace removes both runes.
func pairBackspaceSpan(runes []rune, caret int) (int, int, bool) {
	if caret <= 0 || caret >= len(runes) {
		return 0, 0, false
	}

	if closer, ok := pairClose[runes[caret-1]]; ok && runes[caret] == closer {
		return caret - 1, caret + 1, true
	}

	return 0, 0, false
}

// autoPairType applies planAutoPair for one typed rune. It reports whether
// the rune was handled.
func (a *App) autoPairType(ctx context.Context, typed string) (bool, error) {
	rs := []rune(typed)
	if len(rs) != 1 || a.edit == nil {
		return false, nil
	}

	text, start, end := a.editSelection()
	runes := []rune(text)

	plan, ok := planAutoPair(runes, start, end, rs[0])
	if !ok {
		return false, nil
	}

	if plan.skip {
		a.editCaret, a.editAnchor = plan.caret, plan.anchor
		a.clearBracket()
		a.ensureEditCaretVisible()
		a.refresh()

		return true, a.page.Redraw(ctx)
	}

	if err := a.replaceEditRange(ctx, text, plan.start, plan.end, plan.insert); err != nil {
		return true, err
	}

	a.editAnchor, a.editCaret = plan.anchor, plan.caret
	a.ensureEditCaretVisible()
	a.refresh()

	return true, a.page.Redraw(ctx)
}
