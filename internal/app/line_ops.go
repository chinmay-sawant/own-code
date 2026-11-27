package app

import (
	"context"
	"strings"
	"unicode/utf8"
)

// Line operations for the edit buffer: move, duplicate, delete, and join.
// The planning functions are pure over the buffer text so they test without
// a window; the App methods apply one plan as a single undo point.

// Rebindable keymap action ids and their default chords.
const (
	actionMoveLineUp   = "move-line-up"
	actionMoveLineDown = "move-line-down"
	actionDuplicateLn  = "duplicate-line"
	actionDeleteLine   = "delete-line"
	actionJoinLines    = "join-lines"
)

// lineOpActions lists the actions in dispatch order.
var lineOpActions = []string{
	actionMoveLineUp, actionMoveLineDown, actionDuplicateLn, actionDeleteLine, actionJoinLines,
}

// lineEdit replaces old lines lo..hi with repl and places the selection.
// anchor and caret are rune offsets in the text after the replacement.
type lineEdit struct {
	lo, hi        int
	repl          []string
	anchor, caret int
}

// splitBufferLines splits buffer text into its real lines. The edit buffer
// always ends non-empty text with a newline; trailing reports that.
func splitBufferLines(text string) (lines []string, trailing bool) {
	if text == "" {
		return nil, false
	}

	lines = strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		return lines[:len(lines)-1], true
	}

	return lines, false
}

// lineOffset is the rune offset of (line, col) in lines joined by newlines.
func lineOffset(lines []string, line, col int) int {
	off := 0

	for i := 0; i < line && i < len(lines); i++ {
		off += utf8.RuneCountInString(lines[i]) + 1
	}

	return off + col
}

// selectedLineBlock returns the first and last line a selection touches. A
// selection that ends at column 0 of a later line excludes that line.
func selectedLineBlock(text string, start, end int) (int, int) {
	s, _ := offsetLineCol(text, start)
	e, endCol := offsetLineCol(text, end)

	if e > s && endCol == 0 {
		e--
	}

	return s, e
}

// planLineOp builds the edit for one line action. ok is false when the
// action does nothing (top line moved up, caret past the last line, ...).
func planLineOp(action, text string, start, end, anchor, caret int) (lineEdit, bool) {
	lines, _ := splitBufferLines(text)
	n := len(lines)
	s, e := selectedLineBlock(text, start, end)

	if n == 0 || s >= n {
		return lineEdit{}, false
	}

	e = min(e, n-1)
	aLine, aCol := offsetLineCol(text, anchor)
	cLine, cCol := offsetLineCol(text, caret)
	block := append([]string(nil), lines[s:e+1]...)

	var (
		le     lineEdit
		newSel func(res []string) (int, int)
	)

	shift := func(delta int) func(res []string) (int, int) {
		return func(res []string) (int, int) {
			full := spliceLines(lines, le.lo, le.hi, res)

			return lineOffset(full, aLine+delta, aCol), lineOffset(full, cLine+delta, cCol)
		}
	}

	switch action {
	case actionMoveLineUp:
		if s == 0 {
			return lineEdit{}, false
		}

		le = lineEdit{lo: s - 1, hi: e}
		le.repl = append(append([]string(nil), block...), lines[s-1])
		newSel = shift(-1)
	case actionMoveLineDown:
		if e >= n-1 {
			return lineEdit{}, false
		}

		le = lineEdit{lo: s, hi: e + 1}
		le.repl = append([]string{lines[e+1]}, block...)
		newSel = shift(1)
	case actionDuplicateLn:
		le = lineEdit{lo: s, hi: e}
		le.repl = append(append([]string(nil), block...), block...)
		newSel = shift(len(block))
	case actionDeleteLine:
		return planDeleteLines(lines, s, e, cCol), true
	case actionJoinLines:
		return planJoinLines(lines, s, e)
	default:
		return lineEdit{}, false
	}

	le.anchor, le.caret = newSel(le.repl)

	return le, true
}

// planDeleteLines removes lines s..e. The span grows to a neighbor so the
// line break goes with the text, and the caret lands on the line that
// slides into place, keeping its column when it fits.
func planDeleteLines(lines []string, s, e, col int) lineEdit {
	n := len(lines)
	le := lineEdit{lo: s, hi: e}

	switch {
	case e+1 < n:
		le.hi = e + 1
		le.repl = []string{lines[e+1]}
	case s > 0:
		le.lo = s - 1
		le.repl = []string{lines[s-1]}
	}

	full := spliceLines(lines, le.lo, le.hi, le.repl)
	target := min(s, len(full)-1)

	if target < 0 {
		return le
	}

	col = min(col, utf8.RuneCountInString(full[target]))
	le.anchor = lineOffset(full, target, col)
	le.caret = le.anchor

	return le
}

// planJoinLines joins lines s..e into one, or the caret line with the next
// when the selection sits on one line. Whitespace at each seam collapses to
// one space; an empty side adds none.
func planJoinLines(lines []string, s, e int) (lineEdit, bool) {
	if e == s {
		e = s + 1
	}

	if e >= len(lines) {
		return lineEdit{}, false
	}

	joined := lines[s]
	seam := 0

	for i := s + 1; i <= e; i++ {
		left := strings.TrimRight(joined, " \t")
		right := strings.TrimLeft(lines[i], " \t")

		if right == "" {
			joined = left
		} else if left == "" {
			joined = right
		} else {
			joined = left + " " + right
		}

		seam = utf8.RuneCountInString(left)
	}

	le := lineEdit{lo: s, hi: e, repl: []string{joined}}
	full := spliceLines(lines, s, e, le.repl)
	le.anchor = lineOffset(full, s, seam)
	le.caret = le.anchor

	return le, true
}

// spliceLines returns lines with lo..hi replaced by repl.
func spliceLines(lines []string, lo, hi int, repl []string) []string {
	out := make([]string, 0, len(lines)-(hi-lo+1)+len(repl))
	out = append(out, lines[:lo]...)
	out = append(out, repl...)

	return append(out, lines[hi+1:]...)
}

// runLineOp applies one line action to the edit buffer.
func (a *App) runLineOp(ctx context.Context, action string) error {
	if !a.editing || a.edit == nil {
		return nil
	}

	text, start, end := a.editSelection()
	plan, ok := planLineOp(action, text, start, end, a.editAnchor, a.editCaret)

	if !ok {
		return nil
	}

	lines, _ := splitBufferLines(text)
	spanStart := lineOffset(lines, plan.lo, 0)
	spanEnd := lineOffset(lines, plan.hi, utf8.RuneCountInString(lines[plan.hi]))

	if err := a.replaceEditRange(ctx, text, spanStart, spanEnd, strings.Join(plan.repl, "\n")); err != nil {
		return err
	}

	if doc := a.docs[a.edit.Path]; doc != nil && doc.Syntax != nil {
		for line := plan.lo; line <= plan.lo+max(len(plan.repl), plan.hi-plan.lo+1); line++ {
			doc.Syntax.markDirty(line)
		}
	}

	a.editAnchor, a.editCaret = plan.anchor, plan.caret
	a.ensureEditCaretVisible()
	a.refresh()

	return a.page.Redraw(ctx)
}

// lineOpKey fires a line action when key matches its (possibly rebound)
// chord. It reports whether the key was consumed.
func (a *App) lineOpKey(ctx context.Context, key string) (bool, error) {
	for _, action := range lineOpActions {
		if a.keymapMatches(action, key) {
			return true, a.runLineOp(ctx, action)
		}
	}

	return false, nil
}
