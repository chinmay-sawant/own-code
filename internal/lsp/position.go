package lsp

import (
	"strings"
	"unicode/utf8"
)

// SplitLines splits text into lines without terminators, treating \n, \r\n
// and lone \r as line breaks (as LSP does). A trailing terminator yields a
// final empty line.
func SplitLines(text string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			lines = append(lines, text[start:i])
			start = i + 1
		case '\r':
			lines = append(lines, text[start:i])
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	return append(lines, text[start:])
}

// UTF16Len returns the number of UTF-16 code units in s.
func UTF16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16Width(r)
	}
	return n
}

func utf16Width(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// RuneColToUTF16 converts a rune index within line to a UTF-16 offset.
// Indexes past the end clamp to the line length; negative clamp to 0.
func RuneColToUTF16(line string, runeCol int) int {
	if runeCol <= 0 {
		return 0
	}
	n, i := 0, 0
	for _, r := range line {
		if i >= runeCol {
			break
		}
		n += utf16Width(r)
		i++
	}
	return n
}

// UTF16ToRuneCol converts a UTF-16 offset within line to a rune index.
// An offset landing inside a surrogate pair rounds down to the start of that
// rune. Offsets past the end clamp to the rune count.
func UTF16ToRuneCol(line string, u16 int) int {
	if u16 <= 0 {
		return 0
	}
	n, i := 0, 0
	for _, r := range line {
		w := utf16Width(r)
		if n+w > u16 {
			return i
		}
		n += w
		i++
	}
	return i
}

// PositionFromRunes builds an LSP Position from a zero-based line and rune
// column. line is the text of that line (no terminator).
func PositionFromRunes(line string, lineNo, runeCol int) Position {
	return Position{Line: lineNo, Character: RuneColToUTF16(line, runeCol)}
}

// RuneColFromPosition converts p.Character into a rune column for line.
func RuneColFromPosition(line string, p Position) int {
	return UTF16ToRuneCol(line, p.Character)
}

// PositionAtOffset converts a byte offset in text to an LSP Position.
// Offsets are clamped to [0, len(text)] and snapped back to a rune boundary.
func PositionAtOffset(text string, off int) Position {
	if off < 0 {
		off = 0
	}
	if off > len(text) {
		off = len(text)
	}
	for off > 0 && off < len(text) && !utf8.RuneStart(text[off]) {
		off--
	}
	line, start := 0, 0
	for i := 0; i < off; i++ {
		switch text[i] {
		case '\n':
			line++
			start = i + 1
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				if i+1 >= off {
					// offset between \r and \n: stay on this line.
					return Position{Line: line, Character: UTF16Len(text[start : i+1])}
				}
				continue // the \n will end the line
			}
			line++
			start = i + 1
		}
	}
	return Position{Line: line, Character: UTF16Len(text[start:off])}
}

// OffsetAtPosition converts an LSP Position to a byte offset in text. Lines
// past the end clamp to len(text); characters past the line end clamp to the
// line end (before its terminator).
func OffsetAtPosition(text string, p Position) int {
	line, start := 0, 0
	for line < p.Line {
		i := strings.IndexAny(text[start:], "\r\n")
		if i < 0 {
			return len(text)
		}
		start += i
		if text[start] == '\r' && start+1 < len(text) && text[start+1] == '\n' {
			start++
		}
		start++
		line++
	}
	end := len(text)
	if i := strings.IndexAny(text[start:], "\r\n"); i >= 0 {
		end = start + i
	}
	l := text[start:end]
	off, n := 0, 0
	for _, r := range l {
		w := utf16Width(r)
		if n+w > p.Character {
			break
		}
		n += w
		off += utf8.RuneLen(r)
	}
	// RuneLen of invalid runes is 1 for RuneError from decoding; recompute safely.
	return start + clampBytes(l, off)
}

func clampBytes(l string, off int) int {
	if off > len(l) {
		return len(l)
	}
	return off
}
