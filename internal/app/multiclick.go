package app

import (
	"time"
	"unicode"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// pressPointSlop is how many runes apart two presses may land and still count
// as one multi-click.
const pressPointSlop = 1

// pressDebounce merges duplicate deliveries of one physical press.
const pressDebounce = 40 * time.Millisecond

// pressCount returns 1, 2 or 3 for a press at offset: consecutive presses on
// the same spot inside doubleClickWindow count up, then wrap to 1.
func (a *App) pressCount(offset int) int {
	now := time.Now()
	near := offset-a.pressOffset <= pressPointSlop && a.pressOffset-offset <= pressPointSlop

	// The engine can deliver one physical press to onPress twice (drag claim
	// then press). Presses this close are the same click, not a double-click.
	if a.pressCountN > 0 && near && now.Sub(a.pressAt) < pressDebounce {
		return a.pressCountN
	}

	if a.pressCountN > 0 && near && now.Sub(a.pressAt) < doubleClickWindow {
		a.pressCountN++
		if a.pressCountN > 3 {
			a.pressCountN = 1
		}
	} else {
		a.pressCountN = 1
	}

	a.pressAt = now
	a.pressOffset = offset

	return a.pressCountN
}

type runeClass int

const (
	classSpace runeClass = iota
	classWord
	classOther
)

func classOfRune(r rune) runeClass {
	switch {
	case r == '\n' || r == '\r':
		return classSpace
	case isIdentRune(r):
		return classWord
	case unicode.IsSpace(r):
		return classSpace
	default:
		return classOther
	}
}

// wordRangeAt returns the rune span of the word under caret: an identifier,
// a run of spaces, or a run of punctuation, as VS Code's double-click does.
// A caret at a word's right edge picks the word on its left.
func wordRangeAt(runes []rune, caret int) (int, int) {
	if len(runes) == 0 {
		return 0, 0
	}

	caret = edit.ClampPos(caret, len(runes))

	at := caret
	if at >= len(runes) || runes[at] == '\n' || (classOfRune(runes[at]) != classWord && at > 0 && classOfRune(runes[at-1]) == classWord && runes[at-1] != '\n') {
		if at == 0 {
			return 0, 0
		}

		at--
	}

	if runes[at] == '\n' || runes[at] == '\r' {
		return caret, caret
	}

	class := classOfRune(runes[at])
	start, end := at, at+1

	for start > 0 && runes[start-1] != '\n' && classOfRune(runes[start-1]) == class {
		start--
	}

	for end < len(runes) && runes[end] != '\n' && classOfRune(runes[end]) == class {
		end++
	}

	return start, end
}

// lineRangeAt returns the span of the whole line holding caret, including its
// line break, so a triple-click selects the line the way VS Code does.
func lineRangeAt(runes []rune, caret int) (int, int) {
	caret = edit.ClampPos(caret, len(runes))

	start := caret
	for start > 0 && runes[start-1] != '\n' {
		start--
	}

	end := caret
	for end < len(runes) && runes[end] != '\n' {
		end++
	}

	if end < len(runes) {
		end++
	}

	return start, end
}

// selectByClicks widens the selection for a double (word) or triple (line)
// press at offset. It reports whether a multi-click selection was applied.
func (a *App) selectByClicks(text string, offset int) bool {
	switch a.pressCount(offset) {
	case 2:
		a.editAnchor, a.editCaret = wordRangeAt([]rune(text), offset)
		return true
	case 3:
		a.editAnchor, a.editCaret = lineRangeAt([]rune(text), offset)
		return true
	}

	return false
}
