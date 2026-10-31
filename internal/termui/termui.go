// Package termui turns a terminal snapshot into a small, pure view model and
// translates browser key names into the byte sequences a shell expects.
//
// The package has no state and does not touch the snapshot it is given, so the
// app can render a terminal without doing cell or scrollback math itself.
package termui

import (
	"strconv"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/terminal"
)

// Segment is a run of text that shares one terminal style.
type Segment struct {
	Text string
	// Class holds space-separated CSS classes such as "fg1 bg4 bold".
	Class string
}

// Row is one rendered terminal line.
type Row struct {
	Segments []Segment
	// Empty reports that the snapshot line has no non-default content. The
	// row still carries one space segment so a blank line keeps its height.
	Empty bool
}

// View is the renderable part of a terminal snapshot.
type View struct {
	Rows []Row
	// Scroll is the actual bottom-relative scrollback offset after clamping.
	Scroll int
	// MaxScroll is the largest usable scrollback offset for this snapshot.
	MaxScroll int
	// CursorRow is the absolute line index in Snapshot.Lines when the cursor
	// is rendered, or -1 when it is hidden, the shell is not running, or the
	// cursor line is outside the rendered window.
	CursorRow int
	// CursorCol is the snapshot column of the rendered cursor. It is zero
	// when CursorRow is -1.
	CursorCol int
}

// Render builds the visible rows and cursor position for a snapshot. A
// non-positive visibleRows falls back to Snapshot.Rows, and at least one row is
// assumed. Scroll is measured from the live tail: zero shows the last
// visibleRows lines and larger values move back through scrollback.
func Render(snapshot terminal.Snapshot, visibleRows, scroll int) View {
	if visibleRows <= 0 {
		visibleRows = snapshot.Rows
		if visibleRows <= 0 {
			visibleRows = 1
		}
	}

	total := len(snapshot.Lines)
	scroll, maxScroll := ClampScroll(scroll, total, visibleRows)

	start := total - visibleRows - scroll
	if start < 0 {
		start = 0
	}
	end := start + visibleRows
	if end > total {
		end = total
	}

	view := View{
		Rows:      make([]Row, 0, end-start),
		Scroll:    scroll,
		MaxScroll: maxScroll,
		CursorRow: -1,
	}

	cursorLine := -1
	if snapshot.CursorVisible && snapshot.Running &&
		snapshot.CursorLine >= start && snapshot.CursorLine < end {
		lineLen := len(snapshot.Lines[snapshot.CursorLine])
		if snapshot.CursorCol >= 0 && snapshot.CursorCol <= lineLen {
			cursorLine = snapshot.CursorLine
			view.CursorRow = snapshot.CursorLine
			view.CursorCol = snapshot.CursorCol
		}
	}

	for line := start; line < end; line++ {
		view.Rows = append(view.Rows, renderLine(
			snapshot.Lines[line],
			line == cursorLine,
			snapshot.CursorCol,
		))
	}

	return view
}

// renderLine converts one snapshot line to merged style segments. The cursor
// cell is kept in its own segment so only that cell gets the cursor class.
func renderLine(line terminal.Line, cursor bool, cursorCol int) Row {
	end := len(line)
	for end > 0 && isDefaultBlank(line[end-1]) {
		end--
	}
	empty := end == 0

	// A cursor at the end of a short line needs one cell past the slice. Valid
	// snapshots always have Cols cells, but tests and partial snapshots may not.
	cursorCell := cursor && cursorCol >= 0 && cursorCol <= len(line)
	if cursorCell && cursorCol >= end {
		// The cursor sits in cells trimmed from the visible text. Keep the
		// intervening spaces so the cursor still lines up with its column.
		end = cursorCol + 1
	}

	if end == 0 {
		return Row{Segments: []Segment{{Text: " "}}, Empty: true}
	}

	segments := make([]Segment, 0, 4)
	for i := 0; i < end; {
		style := styleOf(cellAt(line, i))
		atCursor := cursorCell && i == cursorCol

		j := i + 1
		for j < end {
			nextCursor := cursorCell && j == cursorCol
			if styleOf(cellAt(line, j)) != style || nextCursor != atCursor {
				break
			}
			j++
		}

		class := style.class()
		if atCursor {
			class = appendClass(class, "cursor")
		}
		segments = append(segments, Segment{Text: lineText(line, i, j), Class: class})
		i = j
	}

	return Row{Segments: segments, Empty: empty}
}

// cellStyle is the effective style after applying reverse video.
type cellStyle struct {
	fg, bg                       uint8
	bold, dim, italic, underline bool
}

func styleOf(cell terminal.Cell) cellStyle {
	fg, bg := cell.FG, cell.BG
	if cell.Reverse {
		fg, bg = bg, fg
		// The default colors already carry the terminals' light-on-dark
		// convention. Reverse video maps them to concrete palette entries so
		// a default-colored reversed cell still paints an inverse block.
		if fg == terminal.DefaultColor {
			fg = 0
		}
		if bg == terminal.DefaultColor {
			bg = 7
		}
	}

	return cellStyle{
		fg:        fg,
		bg:        bg,
		bold:      cell.Bold,
		dim:       cell.Dim,
		italic:    cell.Italic,
		underline: cell.Underline,
	}
}

// class returns the CSS classes for the effective style. Palette entries
// outside 0..15 are treated as the terminal default.
func (s cellStyle) class() string {
	parts := make([]string, 0, 6)
	if s.fg < terminal.DefaultColor {
		parts = append(parts, "fg"+strconv.Itoa(int(s.fg)))
	}
	if s.bg < terminal.DefaultColor {
		parts = append(parts, "bg"+strconv.Itoa(int(s.bg)))
	}
	if s.bold {
		parts = append(parts, "bold")
	}
	if s.dim {
		parts = append(parts, "dim")
	}
	if s.italic {
		parts = append(parts, "italic")
	}
	if s.underline {
		parts = append(parts, "underline")
	}

	return strings.Join(parts, " ")
}

func appendClass(class, extra string) string {
	if class == "" {
		return extra
	}

	return class + " " + extra
}

func isDefaultBlank(cell terminal.Cell) bool {
	if cell.Rune != 0 && cell.Rune != ' ' {
		return false
	}

	return cell.FG == terminal.DefaultColor &&
		cell.BG == terminal.DefaultColor &&
		!cell.Bold && !cell.Dim && !cell.Italic && !cell.Underline && !cell.Reverse
}

func cellAt(line terminal.Line, index int) terminal.Cell {
	if index < len(line) {
		return line[index]
	}

	return terminal.Cell{Rune: ' ', FG: terminal.DefaultColor, BG: terminal.DefaultColor}
}

func lineText(line terminal.Line, start, end int) string {
	var b strings.Builder
	b.Grow(end - start)
	for i := start; i < end; i++ {
		cell := cellAt(line, i)
		if cell.Rune == 0 {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(cell.Rune)
	}

	return b.String()
}

// KeyBytes maps a browser key name plus modifiers to the bytes a shell
// expects. It handles named keys, ctrl combinations, and alt-prefixed single
// characters; plain printable text is left to the app's type handler and
// produces false. Shift+PageUp and Shift+PageDown also produce false so the
// caller can use them for scrollback.
func KeyBytes(key string, ctrl, shift, alt bool) ([]byte, bool) {
	return KeyBytesFor(key, ctrl, shift, alt, false)
}

// KeyBytesFor is KeyBytes with DECCKM application-cursor mode. When appCursor
// is true, unmodified arrows and Home/End use the SS3 forms the shell asked
// for; modified keys keep the CSI form.
func KeyBytesFor(key string, ctrl, shift, alt, appCursor bool) ([]byte, bool) {
	name := key
	if len(name) > 1 {
		name = strings.ToLower(name)
	}

	if shift && (name == "pageup" || name == "pagedown") {
		return nil, false
	}
	if shift && name == "tab" && !ctrl && !alt {
		return []byte("\x1b[Z"), true
	}

	if ctrl {
		if b, ok := ControlByte(key); ok {
			if alt {
				return []byte{0x1b, b}, true
			}
			return []byte{b}, true
		}
	}

	modified := ctrl || shift || alt
	modifier := 1
	if shift {
		modifier += 1
	}
	if alt {
		modifier += 2
	}
	if ctrl {
		modifier += 4
	}

	if seq, ok := cursorKey(name, modifier, modified, appCursor); ok {
		return seq, true
	}
	if seq, ok := functionKey(name, modifier, modified); ok {
		return seq, true
	}

	var seq []byte
	switch name {
	case "enter":
		seq = []byte("\r")
	case "tab":
		seq = []byte("\t")
	case "escape":
		seq = []byte("\x1b")
	case "backspace":
		seq = []byte("\x7f")
	}
	if seq != nil {
		if alt {
			seq = append([]byte{0x1b}, seq...)
		}
		return seq, true
	}

	if alt {
		if b, ok := altByte(key, shift); ok {
			return []byte{0x1b, b}, true
		}
	}

	return nil, false
}

// cursorKey maps arrows, home, end, and the tilde keys. Modified keys use the
// xterm CSI parameter form; unmodified keys use the short forms terminals send.
func cursorKey(name string, modifier int, modified, appCursor bool) ([]byte, bool) {
	finals := map[string]byte{
		"arrowup":    'A',
		"arrowdown":  'B',
		"arrowright": 'C',
		"arrowleft":  'D',
		"home":       'H',
		"end":        'F',
	}
	if final, ok := finals[name]; ok {
		if !modified {
			if appCursor {
				return []byte{0x1b, 'O', final}, true
			}
			return []byte{0x1b, '[', final}, true
		}
		return []byte("\x1b[1;" + strconv.Itoa(modifier) + string(final)), true
	}

	numbers := map[string]int{
		"insert":   2,
		"delete":   3,
		"pageup":   5,
		"pagedown": 6,
	}
	if number, ok := numbers[name]; ok {
		if !modified {
			return []byte("\x1b[" + strconv.Itoa(number) + "~"), true
		}
		return []byte("\x1b[" + strconv.Itoa(number) + ";" + strconv.Itoa(modifier) + "~"), true
	}

	return nil, false
}

// functionKey maps F1 through F12, using SS3 for the first four plain keys and
// CSI sequences for the rest and for every modified key.
func functionKey(name string, modifier int, modified bool) ([]byte, bool) {
	finals := map[string]byte{
		"f1": 'P',
		"f2": 'Q',
		"f3": 'R',
		"f4": 'S',
	}
	if final, ok := finals[name]; ok {
		if !modified {
			return []byte{0x1b, 'O', final}, true
		}
		return []byte("\x1b[1;" + strconv.Itoa(modifier) + string(final)), true
	}

	numbers := map[string]int{
		"f5":  15,
		"f6":  17,
		"f7":  18,
		"f8":  19,
		"f9":  20,
		"f10": 21,
		"f11": 23,
		"f12": 24,
	}
	if number, ok := numbers[name]; ok {
		if !modified {
			return []byte("\x1b[" + strconv.Itoa(number) + "~"), true
		}
		return []byte("\x1b[" + strconv.Itoa(number) + ";" + strconv.Itoa(modifier) + "~"), true
	}

	return nil, false
}

// altByte returns the byte for an alt-prefixed key. Only printable ASCII keys
// and the named space key qualify; shifted letters are uppercased. Other
// shifted symbols are sent as their unshifted key, matching the app's keydown
// names rather than a keyboard layout.
func altByte(key string, shift bool) (byte, bool) {
	if strings.EqualFold(key, "space") {
		return ' ', true
	}
	if len(key) != 1 {
		return 0, false
	}

	b := key[0]
	if b < 0x20 || b > 0x7e {
		return 0, false
	}
	if shift && b >= 'a' && b <= 'z' {
		b -= 'a' - 'A'
	}

	return b, true
}

// ControlByte maps a ctrl key combination to its control byte. Letters are
// case-insensitive; the symbol and space mappings follow the ASCII control
// table. It returns false for every other key.
func ControlByte(key string) (byte, bool) {
	if strings.EqualFold(key, "space") || key == " " {
		return 0x00, true
	}
	if len(key) != 1 {
		return 0, false
	}

	b := key[0]
	switch {
	case b >= 'a' && b <= 'z':
		return b - 'a' + 1, true
	case b >= 'A' && b <= 'Z':
		return b - 'A' + 1, true
	}

	switch b {
	case '[':
		return 0x1b, true
	case '\\':
		return 0x1c, true
	case ']':
		return 0x1d, true
	case '^':
		return 0x1e, true
	case '_':
		return 0x1f, true
	case '?':
		return 0x7f, true
	}

	return 0, false
}

// ClampScroll clamps a bottom-relative scrollback offset to the available
// history. It returns the clamped offset and the maximum offset for totalLines
// content lines with visibleRows on screen.
func ClampScroll(scroll, totalLines, visibleRows int) (int, int) {
	if totalLines < 0 {
		totalLines = 0
	}
	if visibleRows < 0 {
		visibleRows = 0
	}

	maxScroll := totalLines - visibleRows
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scroll < 0 {
		scroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}

	return scroll, maxScroll
}
