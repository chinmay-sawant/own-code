package app

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// matchBracket finds the bracket matching the one at the caret. It looks at
// the rune under the caret first and the rune before it second, walks with
// depth, and skips runes inside strings and comments (per the highlight
// scanner, whose block state carries across lines). Only () [] {} match;
// ok is false when neither neighbor is a bracket, the bracket sits in a
// string or comment, or no partner exists.
func matchBracket(text, lang string, caret int) (match int, ok bool) {
	runes := []rune(text)
	caret = edit.ClampPos(caret, len(runes))
	skip := bracketSkipSet(lang, text)

	at := -1

	if caret < len(runes) && isBracketRune(runes[caret]) && !skip[caret] {
		at = caret
	} else if caret > 0 && isBracketRune(runes[caret-1]) && !skip[caret-1] {
		at = caret - 1
	} else {
		return 0, false
	}

	open, close, forward := bracketPair(runes[at])
	if forward {
		depth := 0

		for i := at; i < len(runes); i++ {
			if skip[i] {
				continue
			}

			switch runes[i] {
			case open:
				depth++
			case close:
				depth--

				if depth == 0 {
					return i, true
				}
			}
		}

		return 0, false
	}

	depth := 0

	for i := at; i >= 0; i-- {
		if skip[i] {
			continue
		}

		switch runes[i] {
		case close:
			depth++
		case open:
			depth--

			if depth == 0 {
				return i, true
			}
		}
	}

	return 0, false
}

// isBracketRune reports whether r is a matchable bracket. Angle brackets
// are excluded: they collide with operators and generics.
func isBracketRune(r rune) bool {
	switch r {
	case '(', ')', '[', ']', '{', '}':
		return true
	}

	return false
}

// bracketPair returns the open/close pair for a bracket and the scan
// direction: forward from an opener, backward from a closer.
func bracketPair(r rune) (open, close rune, forward bool) {
	switch r {
	case '(':
		return '(', ')', true
	case '[':
		return '[', ']', true
	case '{':
		return '{', '}', true
	default:
		switch r {
		case ')':
			return '(', ')', false
		case ']':
			return '[', ']', false
		default:
			return '{', '}', false
		}
	}
}

// bracketSkipSet returns the rune offsets inside string and comment spans.
// The whole file tokenizes once per jump; the cache-free tokenize keeps one
// keystroke of latency for the files this viewer opens.
func bracketSkipSet(lang, text string) map[int]bool {
	lines := strings.Split(text, "\n")
	segs := tokenize(lang, lines)

	out := map[int]bool{}
	offset := 0

	for i, line := range lines {
		runes := []rune(line)
		pos := 0

		if i < len(segs) {
			for _, seg := range segs[i] {
				n := utf8.RuneCountInString(seg.Text)

				if seg.Class == classStr || seg.Class == classCom {
					for k := 0; k < n && pos+k < len(runes); k++ {
						out[offset+pos+k] = true
					}
				}

				pos += n
			}
		}

		offset += len(runes) + 1
	}

	return out
}

// jumpToBracket moves the caret to the matching bracket and flashes it. A
// bracket flash persists until the next caret move or edit, like the find
// highlight; there is no timer.
func (a *App) jumpToBracket(ctx context.Context, selectTo bool) error {
	if !a.editing || a.edit == nil {
		return nil
	}

	text := a.edit.Text()
	match, ok := matchBracket(text, a.editLang(), a.editCaret)
	if !ok {
		a.showToast("info", "Bracket match", "No matching bracket at the caret.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if !selectTo {
		a.editCaret = match
		a.editAnchor = match
	} else {
		a.editCaret = match
	}

	line, col := offsetLineCol(text, match)
	a.bracketOn = true
	a.bracketLine = line
	// The view expands tabs, so the flash stores display columns, not raw
	// rune columns.
	a.bracketFrom = displayColumn(text, line, col)
	a.bracketTo = displayColumn(text, line, col+1)
	a.ensureEditCaretVisible()
	a.refresh()

	return a.page.Redraw(ctx)
}

// editLang reports the language of the live edit buffer.
func (a *App) editLang() string {
	if a.edit == nil {
		return langPlain
	}

	if doc := a.docs[a.edit.Path]; doc != nil {
		return doc.Lang
	}

	return languageOf(a.edit.Path)
}

// clearBracket drops the bracket-match flash.
func (a *App) clearBracket() {
	a.bracketOn = false
	a.bracketLine = 0
	a.bracketFrom = 0
	a.bracketTo = 0
}

// markBracketRows adds the flash class to a copy of the visible rows,
// mirroring markFindRows. The copy keeps the shared wrap plan immutable.
func (a *App) markBracketRows(rows []codeRow) []codeRow {
	if !a.bracketOn || len(rows) == 0 {
		return rows
	}

	out := append([]codeRow(nil), rows...)

	for i := range out {
		row := &out[i]
		if row.Line != a.bracketLine || row.Segments == nil {
			continue
		}

		start := row.Start
		length := 0

		for _, seg := range row.Segments {
			length += utf8.RuneCountInString(seg.Text)
		}

		from := max(a.bracketFrom, start)
		to := min(a.bracketTo, start+length)

		if from < to {
			row.Segments = markRange(row.Segments, from-start, to-start, "bracket-flash")
		}
	}

	return out
}
