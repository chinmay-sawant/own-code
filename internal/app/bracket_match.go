package app

import (
	"github.com/chinmay-sawant/own-code/internal/edit"
)

// Passive editor guides: the bracket pair around the caret is tinted while
// editing, and leading whitespace carries one indent guide per level. Both
// only add CSS classes to visible rows; they never change text or layout.

// bracketMatchMaxBytes bounds the passive matcher: larger buffers skip it so
// a frame never tokenizes a huge file.
const bracketMatchMaxBytes = 256 << 10

// bracketMatchCache remembers the last passive match so repeated frames at
// the same caret and text reuse it.
type bracketMatchCache struct {
	valid bool
	path  string
	caret int
	size  int
	line  string
	ok    bool
	// open and close are rune offsets of the pair.
	open, close int
}

// passiveBracketPair returns the bracket pair touching the caret.
func (a *App) passiveBracketPair() (int, int, bool) {
	if !a.editing || a.edit == nil {
		return 0, 0, false
	}

	text := a.edit.Text()
	if len(text) > bracketMatchMaxBytes {
		return 0, 0, false
	}

	caretLine, _ := offsetLineCol(text, a.editCaret)
	curLine := lineTextAt(text, caretLine)
	c := &a.bracketCache

	if c.valid && c.path == a.edit.Path && c.caret == a.editCaret && c.size == len(text) && c.line == curLine {
		return c.open, c.close, c.ok
	}

	*c = bracketMatchCache{valid: true, path: a.edit.Path, caret: a.editCaret, size: len(text), line: curLine}

	match, ok := matchBracket(text, a.editLang(), a.editCaret)
	if !ok {
		return 0, 0, false
	}

	runes := []rune(text)
	caret := edit.ClampPos(a.editCaret, len(runes))
	at := caret

	if !(at < len(runes) && isBracketRune(runes[at]) && bracketSkipAt(runes, at, match)) {
		at = caret - 1
	}

	c.open, c.close, c.ok = min(at, match), max(at, match), true

	return c.open, c.close, true
}

// bracketSkipAt reports whether the bracket at index at is the one whose
// partner is match: the rune under the caret wins when it pairs with match.
func bracketSkipAt(runes []rune, at, match int) bool {
	if match < 0 || match >= len(runes) {
		return false
	}

	o, cl, forward := bracketPair(runes[at])
	if forward {
		return match > at && runes[match] == cl
	}

	return match < at && runes[match] == o
}

// markBracketMatchRows tints both brackets of the pair at the caret.
func (a *App) markBracketMatchRows(rows []codeRow) {
	if len(rows) == 0 {
		return
	}

	open, close, ok := a.passiveBracketPair()
	if !ok {
		return
	}

	text := a.edit.Text()
	targets := [2]int{open, close}

	for _, off := range targets {
		line, col := offsetLineCol(text, off)
		from := displayColumn(text, line, col)
		to := displayColumn(text, line, col+1)

		for i := range rows {
			row := &rows[i]
			if row.Line != line || row.Segments == nil {
				continue
			}

			start := row.Start
			end := start + segmentRuneLen(row.Segments)

			f, t := max(from, start), min(to, end)
			if f < t {
				row.Segments = markRange(row.Segments, f-start, t-start, "bracket-match")
			}
		}
	}
}

// markIndentGuideRows tags one leading-space cell per indent level. Wrapped
// continuation rows and rows with no leading spaces are skipped.
func markIndentGuideRows(rows []codeRow, step int) {
	if step <= 0 {
		return
	}

	for i := range rows {
		row := &rows[i]
		if row.Start != 0 || len(row.Segments) == 0 {
			continue
		}

		lead := leadingSpaces(row.Segments)
		if lead == 0 {
			continue
		}

		segs := row.Segments
		for col := 0; col < lead; col += step {
			segs = markRange(segs, col, col+1, "indent-guide")
		}

		row.Segments = segs
	}
}

// leadingSpaces counts the run of spaces at the start of a row, ignoring
// rows that are only whitespace.
func leadingSpaces(segs []segment) int {
	n := 0

	for _, seg := range segs {
		for _, r := range seg.Text {
			if r == ' ' {
				n++

				continue
			}

			return n
		}
	}

	return 0
}

// decorateGuides applies the passive guides to the visible rows of the
// first editor group.
func (a *App) decorateGuides(doc *fileDoc, rows []codeRow, group int) {
	if doc == nil || len(rows) == 0 {
		return
	}

	if group == 0 {
		a.markBracketMatchRows(rows)
	}

	markIndentGuideRows(rows, a.indentSizeFor(doc.Path))
}
