package terminal

import (
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	// DefaultColor is the palette index that means "terminal default" for
	// Cell.FG and Cell.BG. Indices 0 through 15 are ANSI colors.
	DefaultColor uint8 = 16

	defaultCols       = 80
	defaultRows       = 24
	defaultScrollback = 1000
	maxDimension      = 4096
	maxCSILength      = 4096
)

// Cell is one character cell of a terminal line. Rune is a space for an
// untouched cell. FG and BG are ANSI palette indices; DefaultColor means the
// terminal default.
type Cell struct {
	Rune      rune
	FG        uint8
	BG        uint8
	Bold      bool
	Dim       bool
	Italic    bool
	Underline bool
	Reverse   bool
}

// Line is one row of cells. Every line in a Snapshot has exactly Cols cells.
type Line []Cell

// Snapshot is an immutable copy of the visible terminal state. Lines holds
// scrollback followed by the visible rows, and CursorLine is an absolute index
// into Lines. Trailing blank rows are kept, so CursorLine always addresses a
// row in Lines. Version changes whenever the visible state changes.
type Snapshot struct {
	Lines         []Line
	CursorLine    int
	CursorCol     int
	CursorVisible bool
	// AppCursor reports DECCKM: the shell wants SS3 arrow-key sequences.
	AppCursor bool
	// BracketedPaste reports DECSET 2004: pasted text should be wrapped in
	// the ESC[200~ ... ESC[201~ markers.
	BracketedPaste bool
	Version        uint64
	Title          string
	Running        bool
	Note           string
	Cols           int
	Rows           int
}

// Text returns the visible screen and scrollback as plain text. Trailing
// default cells are trimmed from each line, and lines are newline-separated.
func (s Snapshot) Text() string {
	var b strings.Builder

	for i, line := range s.Lines {
		end := len(line)
		for end > 0 && defaultBlankCell(line[end-1]) {
			end--
		}
		for _, cell := range line[:end] {
			if cell.Rune == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteRune(cell.Rune)
		}
		if i < len(s.Lines)-1 {
			b.WriteByte('\n')
		}
	}

	return b.String()
}

type parserState uint8

const (
	stateGround parserState = iota
	stateEscape
	stateCSI
	stateOSC
	stateOSCEscape
	stateCharset
)

type cellStyle struct {
	fg, bg    uint8
	bold      bool
	dim       bool
	italic    bool
	underline bool
	reverse   bool
}

func defaultStyle() cellStyle {
	return cellStyle{fg: DefaultColor, bg: DefaultColor}
}

// Screen is an ANSI terminal emulator with a scrollback buffer. It is safe for
// one goroutine to feed output while another takes snapshots.
type Screen struct {
	mu sync.Mutex

	cols, rows      int
	scrollbackLimit int
	buffer          [][]Cell
	scrollback      [][]Cell
	curX, curY      int
	savedX, savedY  int
	style           cellStyle
	savedStyle      cellStyle
	scrollTop       int
	scrollBottom    int
	cursorVisible   bool
	appCursor       bool
	bracketedPaste  bool
	wrapPending     bool
	version         uint64
	dirty           bool

	alt         bool
	primary     [][]Cell
	primaryX    int
	primaryY    int
	primaryWrap bool

	state   parserState
	csi     []byte
	pending []byte
}

// NewScreen returns a blank screen of the requested size. Non-positive
// dimensions default to 80x24; oversized dimensions are clamped.
func NewScreen(cols, rows int) *Screen {
	cols, rows = normalizeSize(cols, rows)
	s := &Screen{
		cols:            cols,
		rows:            rows,
		scrollbackLimit: defaultScrollback,
		buffer:          blankGrid(cols, rows),
		style:           defaultStyle(),
		savedStyle:      defaultStyle(),
		cursorVisible:   true,
	}
	s.resetScrollRegion()
	return s
}

// Feed parses terminal output and updates the screen. Incomplete escape
// sequences and UTF-8 runes are retained until the next call. Version advances
// at most once per call, and only when the visible state changed.
func (s *Screen) Feed(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.dirty = false
	defer func() {
		if s.dirty {
			s.version++
			s.dirty = false
		}
	}()

	if len(data) == 0 {
		return
	}

	if len(s.pending) > 0 {
		data = append(s.pending, data...)
		s.pending = nil
	}
	s.consume(data)
}

// Resize changes the visible size while preserving overlapping content.
// Content is not reflowed. On the primary screen, shrinking keeps the cursor
// visible: the rows above it are pushed into scrollback and the viewport
// follows the cursor down. The alternate screen is resized in place.
func (s *Screen) Resize(cols, rows int) {
	cols, rows = normalizeSize(cols, rows)

	s.mu.Lock()
	defer s.mu.Unlock()

	if cols == s.cols && rows == s.rows {
		return
	}

	oldRows := s.rows
	shift := 0
	if !s.alt && rows < oldRows && s.curY >= rows {
		shift = s.curY - (rows - 1)
	}

	if shift > 0 {
		for y := 0; y < shift; y++ {
			s.pushScrollback(resizeLine(s.buffer[y], cols))
		}
	}

	s.buffer = resizeGridShifted(s.buffer, cols, rows, shift)
	for i, line := range s.scrollback {
		s.scrollback[i] = resizeLine(line, cols)
	}
	if s.alt {
		s.primary = resizeGridShifted(s.primary, cols, rows, 0)
	}
	s.cols = cols
	s.rows = rows
	s.curX = clamp(s.curX, 0, cols-1)
	s.curY = clamp(s.curY-shift, 0, rows-1)
	s.savedX = clamp(s.savedX, 0, cols-1)
	s.savedY = clamp(s.savedY-shift, 0, rows-1)
	s.primaryX = clamp(s.primaryX, 0, cols-1)
	s.primaryY = clamp(s.primaryY, 0, rows-1)
	s.resetScrollRegion()
	s.version++
}

// Clear erases the visible screen and scrollback without touching the running
// shell. It is the explicit Clear action, not a terminal control sequence.
func (s *Screen) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for y := range s.buffer {
		s.buffer[y] = blankLine(s.cols)
	}
	s.scrollback = nil
	s.curX, s.curY = 0, 0
	s.wrapPending = false
	s.dirty = true
	s.version++
}

// Version returns the screen's change counter. It advances only when the
// visible state changes, so a caller can cheaply skip an unchanged Snapshot.
func (s *Screen) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.version
}

// Snapshot returns a deep copy of the current screen, including scrollback.
func (s *Screen) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	var lines []Line
	cursorLine := s.curY
	if s.alt {
		lines = cloneGrid(s.buffer)
	} else {
		lines = make([]Line, 0, len(s.scrollback)+s.rows)
		for _, row := range s.scrollback {
			lines = append(lines, cloneLine(row))
		}
		cursorLine = len(lines) + s.curY
		for _, row := range s.buffer {
			lines = append(lines, cloneLine(row))
		}
	}

	return Snapshot{
		Lines:          lines,
		CursorLine:     cursorLine,
		CursorCol:      s.curX,
		CursorVisible:  s.cursorVisible,
		AppCursor:      s.appCursor,
		BracketedPaste: s.bracketedPaste,
		Version:        s.version,
		Cols:           s.cols,
		Rows:           s.rows,
	}
}

func (s *Screen) consume(data []byte) {
	for i := 0; i < len(data); {
		switch s.state {
		case stateGround:
			c := data[i]
			switch {
			case c == 0x1b:
				s.state = stateEscape
				i++
			case c < 0x20 || c == 0x7f:
				s.control(c)
				i++
			case c < utf8.RuneSelf:
				s.printRune(rune(c))
				i++
			default:
				r, size := utf8.DecodeRune(data[i:])
				if r == utf8.RuneError && size == 1 {
					if !utf8.FullRune(data[i:]) {
						s.pending = append([]byte(nil), data[i:]...)
						return
					}
					i++
					continue
				}
				s.printRune(r)
				i += size
			}
		case stateEscape:
			s.escape(data[i])
			i++
		case stateCSI:
			i = s.csiByte(data, i)
		case stateOSC, stateOSCEscape:
			i = s.oscByte(data, i)
		case stateCharset:
			s.state = stateGround
			i++
		}
	}
}

func (s *Screen) escape(c byte) {
	switch c {
	case '[':
		s.state = stateCSI
		s.csi = s.csi[:0]
	case ']', 'P', 'X', '^', '_':
		s.state = stateOSC
	case '(', ')', '*', '+', '-', '.', '/':
		s.state = stateCharset
	case '7':
		s.saveCursor()
		s.state = stateGround
	case '8':
		s.restoreCursor()
		s.state = stateGround
	case 'D':
		s.lineFeed()
		s.state = stateGround
	case 'M':
		s.reverseIndex()
		s.state = stateGround
	case 'E':
		s.lineFeed()
		s.curX = 0
		s.wrapPending = false
		s.dirty = true
		s.state = stateGround
	case 'c':
		s.reset()
		s.state = stateGround
	case 0x1b:
		// A new escape starts; stay in stateEscape.
	default:
		s.state = stateGround
	}
}

func (s *Screen) csiByte(data []byte, i int) int {
	c := data[i]
	switch {
	case c == 0x1b:
		s.state = stateEscape
		s.csi = s.csi[:0]
	case c >= 0x40 && c <= 0x7e:
		raw := string(s.csi)
		s.csi = s.csi[:0]
		s.state = stateGround
		s.handleCSI(raw, c)
	case c >= 0x20 && c <= 0x3f:
		if len(s.csi) < maxCSILength {
			s.csi = append(s.csi, c)
		}
	}
	return i + 1
}

func (s *Screen) oscByte(data []byte, i int) int {
	c := data[i]
	if s.state == stateOSCEscape {
		if c == '\\' {
			s.state = stateGround
		} else {
			s.state = stateOSC
		}
		return i + 1
	}

	switch c {
	case 0x07:
		s.state = stateGround
	case 0x1b:
		s.state = stateOSCEscape
	}
	return i + 1
}

func (s *Screen) control(c byte) {
	switch c {
	case '\b':
		if s.curX > 0 {
			s.curX--
			s.dirty = true
		}
		s.wrapPending = false
	case '\t':
		next := (s.curX/8 + 1) * 8
		if next >= s.cols {
			next = s.cols - 1
		}
		if next != s.curX {
			s.curX = next
			s.dirty = true
		}
		s.wrapPending = false
	case '\n', '\v', '\f':
		s.lineFeed()
	case '\r':
		if s.curX != 0 {
			s.curX = 0
			s.dirty = true
		}
		s.wrapPending = false
	case 0x07:
		// The bell has no visible effect.
	}
}

func (s *Screen) printRune(r rune) {
	if r == 0 {
		return
	}
	if s.wrapPending {
		s.lineFeed()
		s.curX = 0
		s.wrapPending = false
	}
	if s.curX >= s.cols {
		s.curX = 0
		s.lineFeed()
	}
	if s.curY < 0 || s.curY >= s.rows {
		return
	}

	s.buffer[s.curY][s.curX] = Cell{
		Rune:      r,
		FG:        s.style.fg,
		BG:        s.style.bg,
		Bold:      s.style.bold,
		Dim:       s.style.dim,
		Italic:    s.style.italic,
		Underline: s.style.underline,
		Reverse:   s.style.reverse,
	}
	if s.curX == s.cols-1 {
		s.wrapPending = true
	} else {
		s.curX++
	}
	s.dirty = true
}

func (s *Screen) lineFeed() {
	s.wrapPending = false
	if s.curY == s.scrollBottom {
		s.scrollUp(1)
		if s.curY < s.rows {
			s.dirty = true
		}
		return
	}
	if s.curY < s.rows-1 {
		s.curY++
		s.dirty = true
		return
	}
	s.scrollUp(1)
	s.dirty = true
}

func (s *Screen) reverseIndex() {
	s.wrapPending = false
	if s.curY == s.scrollTop {
		s.scrollDown(1)
		s.dirty = true
		return
	}
	if s.curY > 0 {
		s.curY--
		s.dirty = true
	}
}

func (s *Screen) handleCSI(raw string, final byte) {
	params, private := parseCSI(raw)

	switch final {
	case 'A':
		s.moveCursor(0, -countParam(params, 0))
	case 'B':
		s.moveCursor(0, countParam(params, 0))
	case 'C':
		s.moveCursor(countParam(params, 0), 0)
	case 'D':
		s.moveCursor(-countParam(params, 0), 0)
	case 'E':
		s.moveCursor(0, countParam(params, 0))
		s.curX = 0
		s.wrapPending = false
	case 'F':
		s.moveCursor(0, -countParam(params, 0))
		s.curX = 0
		s.wrapPending = false
	case 'G', '`':
		s.moveCursor(countParam(params, 0)-1-s.curX, 0)
	case 'H', 'f':
		row := countParam(params, 0) - 1
		col := countParam(params, 1) - 1
		s.moveCursor(col-s.curX, row-s.curY)
		s.wrapPending = false
	case 'd':
		row := countParam(params, 0) - 1
		s.moveCursor(0, row-s.curY)
		s.wrapPending = false
	case 'J':
		s.eraseDisplay(paramOr(params, 0, 0))
	case 'K':
		s.eraseLine(paramOr(params, 0, 0))
	case 'L':
		s.insertLines(countParam(params, 0))
	case 'M':
		s.deleteLines(countParam(params, 0))
	case 'P':
		s.deleteChars(countParam(params, 0))
	case '@':
		s.insertChars(countParam(params, 0))
	case 'X':
		s.eraseChars(countParam(params, 0))
	case 'S':
		s.scrollUp(countParam(params, 0))
		s.dirty = true
	case 'T':
		s.scrollDown(countParam(params, 0))
		s.dirty = true
	case 'r':
		s.setScrollRegion(params)
	case 's':
		s.saveCursor()
	case 'u':
		s.restoreCursor()
	case 'm':
		s.applySGR(params)
	case 'h':
		if private == '?' {
			s.setModes(params, true)
		}
	case 'l':
		if private == '?' {
			s.setModes(params, false)
		}
	}
}

func parseCSI(raw string) ([]int, byte) {
	var private byte
	if raw != "" {
		switch raw[0] {
		case '?', '>', '<', '=':
			private = raw[0]
			raw = raw[1:]
		}
	}
	if raw == "" {
		return nil, private
	}

	params := make([]int, 0, 4)
	for _, part := range strings.Split(raw, ";") {
		if part == "" {
			params = append(params, 0)
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			n = 0
		}
		params = append(params, n)
	}
	return params, private
}

func paramOr(params []int, i, def int) int {
	if i < len(params) {
		return params[i]
	}
	return def
}

func countParam(params []int, i int) int {
	n := paramOr(params, i, 1)
	if n == 0 {
		n = 1
	}
	return n
}

func (s *Screen) moveCursor(dx, dy int) {
	x := clamp(s.curX+dx, 0, s.cols-1)
	y := clamp(s.curY+dy, 0, s.rows-1)
	if x != s.curX || y != s.curY {
		s.curX, s.curY = x, y
		s.dirty = true
	}
	if dx != 0 || dy != 0 {
		s.wrapPending = false
	}
}

func (s *Screen) saveCursor() {
	s.savedX, s.savedY = s.curX, s.curY
	s.savedStyle = s.style
}

func (s *Screen) restoreCursor() {
	s.curX = clamp(s.savedX, 0, s.cols-1)
	s.curY = clamp(s.savedY, 0, s.rows-1)
	s.style = s.savedStyle
	s.wrapPending = false
	s.dirty = true
}

func (s *Screen) eraseDisplay(mode int) {
	switch mode {
	case 1:
		for y := 0; y < s.curY; y++ {
			s.buffer[y] = blankLine(s.cols)
		}
		s.blankRange(s.buffer[s.curY], 0, s.curX+1)
	case 2:
		for y := range s.buffer {
			s.buffer[y] = blankLine(s.cols)
		}
	case 3:
		s.scrollback = nil
	default:
		s.blankRange(s.buffer[s.curY], s.curX, s.cols)
		for y := s.curY + 1; y < s.rows; y++ {
			s.buffer[y] = blankLine(s.cols)
		}
	}
	s.dirty = true
}

func (s *Screen) eraseLine(mode int) {
	line := s.buffer[s.curY]
	switch mode {
	case 1:
		s.blankRange(line, 0, s.curX+1)
	case 2:
		s.buffer[s.curY] = blankLine(s.cols)
	default:
		s.blankRange(line, s.curX, s.cols)
	}
	s.dirty = true
}

func (s *Screen) blankRange(line []Cell, from, to int) {
	from = clamp(from, 0, len(line))
	to = clamp(to, 0, len(line))
	for x := from; x < to; x++ {
		line[x] = blankCell()
	}
}

func (s *Screen) insertLines(n int) {
	if s.curY < s.scrollTop || s.curY > s.scrollBottom {
		return
	}
	n = clamp(n, 1, s.scrollBottom-s.curY+1)
	for y := s.scrollBottom; y >= s.curY+n; y-- {
		s.buffer[y] = s.buffer[y-n]
	}
	for y := s.curY; y < s.curY+n; y++ {
		s.buffer[y] = blankLine(s.cols)
	}
	s.dirty = true
}

func (s *Screen) deleteLines(n int) {
	if s.curY < s.scrollTop || s.curY > s.scrollBottom {
		return
	}
	n = clamp(n, 1, s.scrollBottom-s.curY+1)
	for y := s.curY; y <= s.scrollBottom-n; y++ {
		s.buffer[y] = s.buffer[y+n]
	}
	for y := s.scrollBottom - n + 1; y <= s.scrollBottom; y++ {
		s.buffer[y] = blankLine(s.cols)
	}
	s.dirty = true
}

func (s *Screen) insertChars(n int) {
	n = clamp(n, 1, s.cols-s.curX)
	line := s.buffer[s.curY]
	copy(line[s.curX+n:], line[s.curX:s.cols-n])
	s.blankRange(line, s.curX, s.curX+n)
	s.dirty = true
}

func (s *Screen) deleteChars(n int) {
	n = clamp(n, 1, s.cols-s.curX)
	line := s.buffer[s.curY]
	copy(line[s.curX:], line[s.curX+n:])
	s.blankRange(line, s.cols-n, s.cols)
	s.dirty = true
}

func (s *Screen) eraseChars(n int) {
	n = clamp(n, 1, s.cols-s.curX)
	s.blankRange(s.buffer[s.curY], s.curX, s.curX+n)
	s.dirty = true
}

func (s *Screen) scrollUp(n int) {
	n = clamp(n, 1, s.scrollBottom-s.scrollTop+1)
	for i := 0; i < n; i++ {
		if s.scrollTop == 0 && s.scrollBottom == s.rows-1 && !s.alt {
			s.pushScrollback(s.buffer[0])
		}
		copy(s.buffer[s.scrollTop:s.scrollBottom], s.buffer[s.scrollTop+1:s.scrollBottom+1])
		s.buffer[s.scrollBottom] = blankLine(s.cols)
	}
	s.dirty = true
}

func (s *Screen) scrollDown(n int) {
	n = clamp(n, 1, s.scrollBottom-s.scrollTop+1)
	for i := 0; i < n; i++ {
		copy(s.buffer[s.scrollTop+1:s.scrollBottom+1], s.buffer[s.scrollTop:s.scrollBottom])
		s.buffer[s.scrollTop] = blankLine(s.cols)
	}
	s.dirty = true
}

func (s *Screen) pushScrollback(line []Cell) {
	s.scrollback = append(s.scrollback, cloneLine(line))
	if len(s.scrollback) > s.scrollbackLimit {
		overflow := len(s.scrollback) - s.scrollbackLimit
		copy(s.scrollback, s.scrollback[overflow:])
		s.scrollback = s.scrollback[:s.scrollbackLimit]
	}
}

func (s *Screen) setScrollRegion(params []int) {
	top := countParam(params, 0) - 1
	bottomParam := paramOr(params, 1, s.rows)
	if bottomParam == 0 {
		bottomParam = s.rows
	}
	bottom := bottomParam - 1
	if bottom < 0 || bottom >= s.rows {
		bottom = s.rows - 1
	}
	if top < 0 || top >= bottom {
		s.resetScrollRegion()
	} else {
		s.scrollTop, s.scrollBottom = top, bottom
	}
	s.curX, s.curY = 0, 0
	s.wrapPending = false
	s.dirty = true
}

func (s *Screen) resetScrollRegion() {
	s.scrollTop = 0
	s.scrollBottom = s.rows - 1
}

func (s *Screen) setModes(params []int, enabled bool) {
	for _, mode := range params {
		switch mode {
		case 25:
			if s.cursorVisible != enabled {
				s.cursorVisible = enabled
				s.dirty = true
			}
		case 1:
			if s.appCursor != enabled {
				s.appCursor = enabled
				s.dirty = true
			}
		case 2004:
			if s.bracketedPaste != enabled {
				s.bracketedPaste = enabled
				s.dirty = true
			}
		case 47, 1047, 1049:
			if enabled {
				s.enterAlt()
			} else {
				s.leaveAlt()
			}
		}
	}
}

func (s *Screen) enterAlt() {
	if s.alt {
		return
	}
	s.primary = s.buffer
	s.primaryX, s.primaryY = s.curX, s.curY
	s.primaryWrap = s.wrapPending
	s.buffer = blankGrid(s.cols, s.rows)
	s.curX, s.curY = 0, 0
	s.wrapPending = false
	s.alt = true
	s.resetScrollRegion()
	s.dirty = true
}

func (s *Screen) leaveAlt() {
	if !s.alt {
		return
	}
	s.buffer = s.primary
	s.primary = nil
	s.curX, s.curY = s.primaryX, s.primaryY
	s.wrapPending = s.primaryWrap
	s.alt = false
	s.resetScrollRegion()
	s.dirty = true
}

func (s *Screen) reset() {
	s.buffer = blankGrid(s.cols, s.rows)
	s.scrollback = nil
	s.curX, s.curY = 0, 0
	s.savedX, s.savedY = 0, 0
	s.style = defaultStyle()
	s.savedStyle = s.style
	s.cursorVisible = true
	s.wrapPending = false
	s.alt = false
	s.primary = nil
	s.resetScrollRegion()
	s.dirty = true
}

func (s *Screen) applySGR(params []int) {
	if len(params) == 0 {
		s.style = defaultStyle()
		return
	}

	for i := 0; i < len(params); i++ {
		p := params[i]
		switch {
		case p == 0:
			s.style = defaultStyle()
		case p == 1:
			s.style.bold = true
		case p == 2:
			s.style.dim = true
		case p == 3:
			s.style.italic = true
		case p == 4:
			s.style.underline = true
		case p == 7:
			s.style.reverse = true
		case p == 22:
			s.style.bold = false
			s.style.dim = false
		case p == 23:
			s.style.italic = false
		case p == 24:
			s.style.underline = false
		case p == 27:
			s.style.reverse = false
		case p >= 30 && p <= 37:
			s.style.fg = uint8(p - 30)
		case p == 38:
			i = s.applyExtendedColor(params, i, true)
		case p == 39:
			s.style.fg = DefaultColor
		case p >= 40 && p <= 47:
			s.style.bg = uint8(p - 40)
		case p == 48:
			i = s.applyExtendedColor(params, i, false)
		case p == 49:
			s.style.bg = DefaultColor
		case p >= 90 && p <= 97:
			s.style.fg = uint8(p - 90 + 8)
		case p >= 100 && p <= 107:
			s.style.bg = uint8(p - 100 + 8)
		}
	}
}

// applyExtendedColor consumes 38/48 parameters and returns the index of the
// last parameter it used. Malformed sequences consume only the prefix.
func (s *Screen) applyExtendedColor(params []int, i int, foreground bool) int {
	if i+1 >= len(params) {
		return i
	}

	switch params[i+1] {
	case 5:
		if i+2 >= len(params) {
			return i + 1
		}
		setColor(&s.style, foreground, color256To16(params[i+2]))
		return i + 2
	case 2:
		if i+4 >= len(params) {
			return len(params) - 1
		}
		setColor(&s.style, foreground, nearest16(
			clamp(params[i+2], 0, 255),
			clamp(params[i+3], 0, 255),
			clamp(params[i+4], 0, 255),
		))
		return i + 4
	}
	return i + 1
}

func setColor(style *cellStyle, foreground bool, color uint8) {
	if foreground {
		style.fg = color
		return
	}
	style.bg = color
}

func color256To16(n int) uint8 {
	if n < 0 {
		n = 0
	}
	if n > 255 {
		n = 255
	}
	if n < 16 {
		return uint8(n)
	}

	var r, g, b int
	if n < 232 {
		n -= 16
		level := [6]int{0, 95, 135, 175, 215, 255}
		r = level[n/36]
		g = level[(n%36)/6]
		b = level[n%6]
	} else {
		n -= 232
		r, g, b = 8+10*n, 8+10*n, 8+10*n
	}
	return nearest16(r, g, b)
}

func nearest16(r, g, b int) uint8 {
	palette := [16][3]int{
		{0, 0, 0},
		{205, 0, 0},
		{0, 205, 0},
		{205, 205, 0},
		{0, 0, 238},
		{205, 0, 205},
		{0, 205, 205},
		{229, 229, 229},
		{127, 127, 127},
		{255, 0, 0},
		{0, 255, 0},
		{255, 255, 0},
		{92, 92, 255},
		{255, 0, 255},
		{0, 255, 255},
		{255, 255, 255},
	}

	best := 0
	bestDistance := int(^uint(0) >> 1)
	for i, color := range palette {
		dr, dg, db := color[0]-r, color[1]-g, color[2]-b
		distance := dr*dr + dg*dg + db*db
		if distance < bestDistance {
			best, bestDistance = i, distance
		}
	}
	return uint8(best)
}

func normalizeSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = defaultCols
	}
	if rows <= 0 {
		rows = defaultRows
	}
	return clamp(cols, 1, maxDimension), clamp(rows, 1, maxDimension)
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// defaultBlankCell reports an untouched cell: no glyph and no styling.
func defaultBlankCell(cell Cell) bool {
	if cell.Rune != 0 && cell.Rune != ' ' {
		return false
	}

	return cell.FG == DefaultColor && cell.BG == DefaultColor &&
		!cell.Bold && !cell.Dim && !cell.Italic && !cell.Underline && !cell.Reverse
}

func blankCell() Cell {
	return Cell{Rune: ' ', FG: DefaultColor, BG: DefaultColor}
}

func blankLine(cols int) Line {
	line := make(Line, cols)
	for i := range line {
		line[i] = blankCell()
	}
	return line
}

func blankGrid(cols, rows int) [][]Cell {
	grid := make([][]Cell, rows)
	for y := range grid {
		grid[y] = blankLine(cols)
	}
	return grid
}

func cloneLine(line []Cell) Line {
	clone := make(Line, len(line))
	copy(clone, line)
	return clone
}

func cloneGrid(grid [][]Cell) []Line {
	lines := make([]Line, len(grid))
	for y, row := range grid {
		lines[y] = cloneLine(row)
	}
	return lines
}

func resizeGrid(grid [][]Cell, cols, rows int) [][]Cell {
	return resizeGridShifted(grid, cols, rows, 0)
}

// resizeGridShifted keeps rows starting at shift, then blanks the tail.
func resizeGridShifted(grid [][]Cell, cols, rows, shift int) [][]Cell {
	if shift < 0 {
		shift = 0
	}
	if shift > len(grid) {
		shift = len(grid)
	}

	resized := blankGrid(cols, rows)
	for y := 0; y < rows && shift+y < len(grid); y++ {
		resized[y] = resizeLine(grid[shift+y], cols)
	}
	return resized
}

func resizeLine(line []Cell, cols int) Line {
	resized := blankLine(cols)
	copy(resized, line[:min(len(line), cols)])
	return resized
}
