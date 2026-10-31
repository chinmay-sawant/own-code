package terminal

import (
	"strings"
	"testing"
)

func newTestScreen(cols, rows int) *Screen {
	return NewScreen(cols, rows)
}

func feed(s *Screen, text string) {
	s.Feed([]byte(text))
}

func screenLine(snapshot Snapshot, line int) string {
	if line < 0 || line >= len(snapshot.Lines) {
		return ""
	}
	var b strings.Builder
	for _, cell := range snapshot.Lines[line] {
		if cell.Rune == 0 {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(cell.Rune)
	}
	return strings.TrimRight(b.String(), " ")
}

func screenCell(snapshot Snapshot, line, col int) Cell {
	if line < 0 || line >= len(snapshot.Lines) || col < 0 || col >= len(snapshot.Lines[line]) {
		return Cell{}
	}
	return snapshot.Lines[line][col]
}

func TestScreenSGR(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		fg        uint8
		bg        uint8
		bold      bool
		dim       bool
		italic    bool
		underline bool
		reverse   bool
	}{
		{name: "basic color", input: "\x1b[31;44mX", fg: 1, bg: 4},
		{name: "bright color", input: "\x1b[91;104mX", fg: 9, bg: 12},
		{name: "attributes", input: "\x1b[1;2;3;4;7mX", fg: DefaultColor, bg: DefaultColor, bold: true, dim: true, italic: true, underline: true, reverse: true},
		{name: "reset", input: "\x1b[1;31;44m\x1b[0mX", fg: DefaultColor, bg: DefaultColor},
		{name: "attributes off", input: "\x1b[1;2;3;4;7m\x1b[22;23;24;27mX", fg: DefaultColor, bg: DefaultColor},
		{name: "default foreground", input: "\x1b[31m\x1b[39mX", fg: DefaultColor, bg: DefaultColor},
		{name: "default background", input: "\x1b[44m\x1b[49mX", fg: DefaultColor, bg: DefaultColor},
		{name: "256 bright red", input: "\x1b[38;5;196mX", fg: 9, bg: DefaultColor},
		{name: "256 blue", input: "\x1b[38;5;21mX", fg: 4, bg: DefaultColor},
		{name: "256 background", input: "\x1b[48;5;196mX", fg: DefaultColor, bg: 9},
		{name: "truecolor green", input: "\x1b[38;2;0;255;0mX", fg: 10, bg: DefaultColor},
		{name: "truecolor background", input: "\x1b[48;2;0;0;255mX", fg: DefaultColor, bg: 4},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			screen := newTestScreen(10, 3)
			feed(screen, test.input)

			cell := screenCell(screen.Snapshot(), 0, 0)
			if cell.Rune != 'X' {
				t.Fatalf("rune = %q, want X", cell.Rune)
			}
			if cell.FG != test.fg || cell.BG != test.bg {
				t.Errorf("colors = (%d, %d), want (%d, %d)", cell.FG, cell.BG, test.fg, test.bg)
			}
			if cell.Bold != test.bold || cell.Dim != test.dim || cell.Italic != test.italic ||
				cell.Underline != test.underline || cell.Reverse != test.reverse {
				t.Errorf("attributes = %+v, want bold=%v dim=%v italic=%v underline=%v reverse=%v",
					cell, test.bold, test.dim, test.italic, test.underline, test.reverse)
			}
		})
	}
}

func TestScreenCursorMovement(t *testing.T) {
	tests := []struct {
		name string
		move string
		line int
		col  int
	}{
		{name: "cursor position", move: "\x1b[2;3H", line: 1, col: 2},
		{name: "cursor home", move: "\x1b[H", line: 0, col: 0},
		{name: "cursor up", move: "\x1b[2A", line: 2, col: 4},
		{name: "cursor down", move: "\x1b[3B", line: 7, col: 4},
		{name: "cursor forward", move: "\x1b[4C", line: 4, col: 8},
		{name: "cursor back clamps", move: "\x1b[9D", line: 4, col: 0},
		{name: "next line", move: "\x1b[2E", line: 6, col: 0},
		{name: "previous line", move: "\x1b[2F", line: 2, col: 0},
		{name: "column absolute", move: "\x1b[7G", line: 4, col: 6},
		{name: "vertical absolute", move: "\x1b[8d", line: 7, col: 4},
		{name: "row column", move: "\x1b[9;10f", line: 8, col: 9},
		{name: "clamps to screen", move: "\x1b[99;99H", line: 9, col: 19},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			screen := newTestScreen(20, 10)
			feed(screen, "\x1b[5;5H")
			feed(screen, test.move)

			snapshot := screen.Snapshot()
			if snapshot.CursorLine != test.line || snapshot.CursorCol != test.col {
				t.Fatalf("cursor = (%d, %d), want (%d, %d)",
					snapshot.CursorLine, snapshot.CursorCol, test.line, test.col)
			}
		})
	}
}

func TestScreenErase(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "erase to end of line",
			input: "abc\x1b[1;2H\x1b[K",
			want:  []string{"a", "", ""},
		},
		{
			name:  "erase to start of line",
			input: "abc\x1b[1;2H\x1b[1K",
			want:  []string{"  c", "", ""},
		},
		{
			name:  "erase whole line",
			input: "abc\x1b[2K",
			want:  []string{"", "", ""},
		},
		{
			name:  "erase display to end",
			input: "abc\r\ndef\r\nghi\x1b[2;2H\x1b[J",
			want:  []string{"abc", "d", ""},
		},
		{
			name:  "erase display to start",
			input: "abc\r\ndef\r\nghi\x1b[2;2H\x1b[1J",
			want:  []string{"", "  f", "ghi"},
		},
		{
			name:  "erase display all",
			input: "abc\r\ndef\x1b[2J",
			want:  []string{"", "", ""},
		},
		{
			name:  "erase chars",
			input: "abcdef\x1b[1;2H\x1b[2X",
			want:  []string{"a  def", "", ""},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			screen := newTestScreen(10, 3)
			feed(screen, test.input)

			snapshot := screen.Snapshot()
			for line, want := range test.want {
				if got := screenLine(snapshot, line); got != want {
					t.Errorf("line %d = %q, want %q", line, got, want)
				}
			}
		})
	}
}

func TestScreenCRLFBackspaceAndTab(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "crlf", input: "abc\r\ndef", want: []string{"abc", "def"}},
		{name: "carriage return overwrites", input: "abc\rX", want: []string{"Xbc"}},
		{name: "backspace", input: "abc\bX", want: []string{"abX"}},
		{name: "tab stop", input: "ab\tX", want: []string{"ab      X"}},
		{name: "tab clamps at last column", input: "12345678901234567\tX", want: []string{"12345678901234567  X"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			screen := newTestScreen(20, 3)
			feed(screen, test.input)

			snapshot := screen.Snapshot()
			for line, want := range test.want {
				if got := screenLine(snapshot, line); got != want {
					t.Errorf("line %d = %q, want %q", line, got, want)
				}
			}
		})
	}
}

func TestScreenUnicodeAndSplitReads(t *testing.T) {
	screen := newTestScreen(5, 3)

	// é split across reads, then a full sequence of wide runes.
	feed(screen, "h\xc3")
	feed(screen, "\xa9llo")
	feed(screen, "✓世界")

	snapshot := screen.Snapshot()
	if got := screenLine(snapshot, 0); got != "héllo" {
		t.Errorf("line 0 = %q, want %q", got, "héllo")
	}
	if got := screenLine(snapshot, 1); got != "✓世界" {
		t.Errorf("line 1 = %q, want %q", got, "✓世界")
	}
	if snapshot.CursorLine != 1 || snapshot.CursorCol != 3 {
		t.Errorf("cursor = (%d, %d), want (1, 3)", snapshot.CursorLine, snapshot.CursorCol)
	}

	split := newTestScreen(10, 2)
	feed(split, "caf\xc3")
	feed(split, "\xa9")
	if got := screenLine(split.Snapshot(), 0); got != "café" {
		t.Errorf("split rune line = %q, want %q", got, "café")
	}
}

func TestScreenWrapping(t *testing.T) {
	screen := newTestScreen(5, 3)
	feed(screen, "abcdef")

	snapshot := screen.Snapshot()
	if got := screenLine(snapshot, 0); got != "abcde" {
		t.Errorf("line 0 = %q, want %q", got, "abcde")
	}
	if got := screenLine(snapshot, 1); got != "f" {
		t.Errorf("line 1 = %q, want %q", got, "f")
	}
	if snapshot.CursorLine != 1 || snapshot.CursorCol != 1 {
		t.Errorf("cursor = (%d, %d), want (1, 1)", snapshot.CursorLine, snapshot.CursorCol)
	}

	// A line that exactly fills the width does not wrap until the next rune.
	exact := newTestScreen(5, 3)
	feed(exact, "abcde\r\nX")
	exactSnapshot := exact.Snapshot()
	if got := screenLine(exactSnapshot, 1); got != "X" {
		t.Errorf("after exact fill line 1 = %q, want X", got)
	}
	if got := screenLine(exactSnapshot, 0); got != "abcde" {
		t.Errorf("after exact fill line 0 = %q, want abcde", got)
	}
}

func TestScreenScrollback(t *testing.T) {
	screen := newTestScreen(10, 3)
	feed(screen, "one\r\ntwo\r\nthree\r\nfour")

	snapshot := screen.Snapshot()
	want := []string{"one", "two", "three", "four"}
	for line, wantLine := range want {
		if got := screenLine(snapshot, line); got != wantLine {
			t.Errorf("line %d = %q, want %q", line, got, wantLine)
		}
	}
	if snapshot.CursorLine != 3 {
		t.Errorf("cursor line = %d, want 3", snapshot.CursorLine)
	}

	feed(screen, "\r\nfive")
	snapshot = screen.Snapshot()
	if got := screenLine(snapshot, 4); got != "five" {
		t.Errorf("new last line = %q, want five", got)
	}
	if snapshot.CursorLine != 4 {
		t.Errorf("cursor line = %d, want 4", snapshot.CursorLine)
	}
}

func TestScreenScrollbackLimit(t *testing.T) {
	screen := newTestScreen(10, 2)
	screen.mu.Lock()
	screen.scrollbackLimit = 2
	screen.mu.Unlock()

	feed(screen, "a\r\nb\r\nc\r\nd\r\ne")

	snapshot := screen.Snapshot()
	want := []string{"b", "c", "d", "e"}
	if len(snapshot.Lines) != len(want) {
		t.Fatalf("line count = %d, want %d", len(snapshot.Lines), len(want))
	}
	for line, wantLine := range want {
		if got := screenLine(snapshot, line); got != wantLine {
			t.Errorf("line %d = %q, want %q", line, got, wantLine)
		}
	}
}

func TestScreenScrollRegion(t *testing.T) {
	screen := newTestScreen(5, 6)
	feed(screen, "a\r\nb\r\nc\r\nd\r\ne\r\nf")

	feed(screen, "\x1b[2;4r\x1b[4;1H\n")
	snapshot := screen.Snapshot()
	want := []string{"a", "c", "d", "", "e", "f"}
	for line, wantLine := range want {
		if got := screenLine(snapshot, line); got != wantLine {
			t.Errorf("after scroll up line %d = %q, want %q", line, got, wantLine)
		}
	}

	feed(screen, "\x1b[2;4r\x1b[2;1H\x1b[T")
	snapshot = screen.Snapshot()
	want = []string{"a", "", "c", "d", "e", "f"}
	for line, wantLine := range want {
		if got := screenLine(snapshot, line); got != wantLine {
			t.Errorf("after scroll down line %d = %q, want %q", line, got, wantLine)
		}
	}
}

func TestScreenInsertAndDelete(t *testing.T) {
	tests := []struct {
		name  string
		setup string
		edit  string
		want  []string
	}{
		{
			name:  "insert chars",
			setup: "abcd",
			edit:  "\x1b[1;2H\x1b[2@",
			want:  []string{"a  bcd", "", "", ""},
		},
		{
			name:  "delete chars",
			setup: "abcdef",
			edit:  "\x1b[1;2H\x1b[2P",
			want:  []string{"adef", "", "", ""},
		},
		{
			name:  "insert lines",
			setup: "a\r\nb\r\nc\r\nd",
			edit:  "\x1b[2;1H\x1b[L",
			want:  []string{"a", "", "b", "c"},
		},
		{
			name:  "delete lines",
			setup: "a\r\nb\r\nc\r\nd",
			edit:  "\x1b[2;1H\x1b[M",
			want:  []string{"a", "c", "d", ""},
		},
		{
			name:  "scroll up command",
			setup: "a\r\nb\r\nc",
			edit:  "\x1b[1;3r\x1b[1;1H\x1b[S",
			want:  []string{"b", "c", "", ""},
		},
		{
			name:  "scroll down command",
			setup: "a\r\nb\r\nc",
			edit:  "\x1b[T",
			want:  []string{"", "a", "b"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			screen := newTestScreen(8, 4)
			feed(screen, test.setup)
			feed(screen, test.edit)

			snapshot := screen.Snapshot()
			for line, wantLine := range test.want {
				if got := screenLine(snapshot, line); got != wantLine {
					t.Errorf("line %d = %q, want %q", line, got, wantLine)
				}
			}
		})
	}
}

func TestScreenOSCAndUnknownSequences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "osc bel", input: "\x1b]0;title\x07A", want: "A"},
		{name: "osc st", input: "\x1b]2;title\x1b\\B", want: "B"},
		{name: "dcs st", input: "\x1bPpayload\x1b\\C", want: "C"},
		{name: "unknown final", input: "\x1b[999zD", want: "D"},
		{name: "unknown sgr parameter", input: "\x1b[1;2;3;99mE", want: "E"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			screen := newTestScreen(10, 2)
			feed(screen, test.input)
			if got := screenLine(screen.Snapshot(), 0); got != test.want {
				t.Errorf("line = %q, want %q", got, test.want)
			}
		})
	}

	screen := newTestScreen(10, 2)
	feed(screen, "\x1b[3")
	feed(screen, "1mX")
	cell := screenCell(screen.Snapshot(), 0, 0)
	if cell.Rune != 'X' || cell.FG != 1 {
		t.Errorf("split csi cell = %+v, want red X", cell)
	}
}

func TestScreenAltScreenAndCursorVisibility(t *testing.T) {
	screen := newTestScreen(10, 3)
	feed(screen, "primary")
	feed(screen, "\x1b[?1049h")
	if got := screenLine(screen.Snapshot(), 0); got != "" {
		t.Fatalf("alt screen line 0 = %q, want blank", got)
	}

	feed(screen, "alternate")
	if got := screenLine(screen.Snapshot(), 0); got != "alternate" {
		t.Errorf("alt line = %q, want alternate", got)
	}

	feed(screen, "\x1b[?1049l")
	if got := screenLine(screen.Snapshot(), 0); got != "primary" {
		t.Errorf("restored line = %q, want primary", got)
	}

	if !screen.Snapshot().CursorVisible {
		t.Error("cursor should start visible")
	}
	feed(screen, "\x1b[?25l")
	if screen.Snapshot().CursorVisible {
		t.Error("cursor should be hidden")
	}
	feed(screen, "\x1b[?25h")
	if !screen.Snapshot().CursorVisible {
		t.Error("cursor should be visible again")
	}
}

func TestScreenVersion(t *testing.T) {
	screen := newTestScreen(10, 3)
	version := screen.Snapshot().Version

	screen.Feed(nil)
	if got := screen.Snapshot().Version; got != version {
		t.Fatalf("empty feed version = %d, want %d", got, version)
	}

	feed(screen, "\x1b]0;title\x07\x1b[999z")
	if got := screen.Snapshot().Version; got != version {
		t.Fatalf("invisible sequences version = %d, want %d", got, version)
	}

	feed(screen, "\x1b[31m")
	if got := screen.Snapshot().Version; got != version {
		t.Fatalf("style-only feed version = %d, want %d", got, version)
	}

	feed(screen, "hello")
	afterText := screen.Snapshot().Version
	if afterText <= version {
		t.Fatalf("text version = %d, want > %d", afterText, version)
	}

	feed(screen, "\x1b[2;2H")
	if got := screen.Snapshot().Version; got <= afterText {
		t.Fatalf("cursor move version = %d, want > %d", got, afterText)
	}
}

func TestScreenResize(t *testing.T) {
	screen := newTestScreen(20, 5)
	feed(screen, "abcdef")

	screen.Resize(3, 2)
	snapshot := screen.Snapshot()
	if snapshot.Cols != 3 || snapshot.Rows != 2 {
		t.Fatalf("size = %dx%d, want 3x2", snapshot.Cols, snapshot.Rows)
	}
	if len(snapshot.Lines) != 2 {
		t.Fatalf("line count = %d, want 2", len(snapshot.Lines))
	}
	if got := screenLine(snapshot, 0); got != "abc" {
		t.Errorf("line 0 = %q, want abc", got)
	}
	if snapshot.CursorLine != 0 || snapshot.CursorCol != 2 {
		t.Errorf("cursor = (%d, %d), want (0, 2)", snapshot.CursorLine, snapshot.CursorCol)
	}

	screen.Resize(5, 4)
	snapshot = screen.Snapshot()
	if len(snapshot.Lines) != 4 {
		t.Fatalf("grown line count = %d, want 4", len(snapshot.Lines))
	}
	if got := screenLine(snapshot, 3); got != "" {
		t.Errorf("new line = %q, want blank", got)
	}
}

func TestScreenResizeKeepsCursorVisible(t *testing.T) {
	screen := newTestScreen(10, 5)
	feed(screen, "1\r\n2\r\n3\r\n4\r\n5")

	screen.Resize(10, 3)
	snapshot := screen.Snapshot()

	if len(snapshot.Lines) != 5 {
		t.Fatalf("line count = %d, want 5 (2 scrollback + 3 screen)", len(snapshot.Lines))
	}
	if got := screenLine(snapshot, 0); got != "1" {
		t.Errorf("scrollback line 0 = %q, want 1", got)
	}
	if got := screenLine(snapshot, 2); got != "3" {
		t.Errorf("screen line 0 = %q, want 3", got)
	}
	if got := screenLine(snapshot, 4); got != "5" {
		t.Errorf("screen line 2 = %q, want 5", got)
	}
	if snapshot.CursorLine != 4 || snapshot.CursorCol != 1 {
		t.Errorf("cursor = (%d, %d), want (4, 1)", snapshot.CursorLine, snapshot.CursorCol)
	}
}

func TestScreenModeFlags(t *testing.T) {
	screen := newTestScreen(10, 3)
	feed(screen, "\x1b[?1h\x1b[?2004h")
	snapshot := screen.Snapshot()
	if !snapshot.AppCursor {
		t.Error("AppCursor = false, want true after DECCKM")
	}
	if !snapshot.BracketedPaste {
		t.Error("BracketedPaste = false, want true after DECSET 2004")
	}

	feed(screen, "\x1b[?1l\x1b[?2004l")
	snapshot = screen.Snapshot()
	if snapshot.AppCursor || snapshot.BracketedPaste {
		t.Errorf("modes = appCursor %v bracketedPaste %v, want both false", snapshot.AppCursor, snapshot.BracketedPaste)
	}
}

func TestSnapshotTextTrimsBlankCells(t *testing.T) {
	screen := newTestScreen(8, 2)
	feed(screen, "hi")
	if got := screen.Snapshot().Text(); got != "hi\n" {
		t.Fatalf("Text() = %q, want %q", got, "hi\n")
	}
}

func TestScreenClear(t *testing.T) {
	screen := newTestScreen(10, 2)
	feed(screen, "old\r\noutput")
	screen.Clear()
	snapshot := screen.Snapshot()
	for line := range snapshot.Lines {
		if got := screenLine(snapshot, line); got != "" {
			t.Errorf("line %d = %q, want blank", line, got)
		}
	}
}

func TestScreenResizeScrollback(t *testing.T) {
	screen := newTestScreen(10, 2)
	feed(screen, "a\r\nb\r\nc")

	screen.Resize(4, 2)
	snapshot := screen.Snapshot()
	if len(snapshot.Lines) != 3 {
		t.Fatalf("line count = %d, want 3", len(snapshot.Lines))
	}
	for line, cells := range snapshot.Lines {
		if len(cells) != 4 {
			t.Errorf("line %d width = %d, want 4", line, len(cells))
		}
	}
	if got := screenLine(snapshot, 0); got != "a" {
		t.Errorf("scrollback line = %q, want a", got)
	}
}
