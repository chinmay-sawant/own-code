package termui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/terminal"
)

func blankCell(r rune) terminal.Cell {
	return terminal.Cell{Rune: r, FG: terminal.DefaultColor, BG: terminal.DefaultColor}
}

func styledCell(r rune, fg, bg uint8) terminal.Cell {
	return terminal.Cell{Rune: r, FG: fg, BG: bg}
}

func textLine(text string) terminal.Line {
	line := make(terminal.Line, 0, len(text))
	for _, r := range text {
		line = append(line, blankCell(r))
	}

	return line
}

func trailingBlanks(line terminal.Line, count int) terminal.Line {
	padded := make(terminal.Line, 0, len(line)+count)
	padded = append(padded, line...)
	for i := 0; i < count; i++ {
		padded = append(padded, blankCell(' '))
	}

	return padded
}

func testSnapshot(lines []terminal.Line, rows int) terminal.Snapshot {
	cols := 80
	if len(lines) > 0 {
		cols = len(lines[0])
	}

	return terminal.Snapshot{
		Lines:   lines,
		Rows:    rows,
		Cols:    cols,
		Running: true,
	}
}

func rowText(row Row) string {
	var b strings.Builder
	for _, segment := range row.Segments {
		b.WriteString(segment.Text)
	}

	return b.String()
}

func rowClasses(row Row) []string {
	classes := make([]string, 0, len(row.Segments))
	for _, segment := range row.Segments {
		classes = append(classes, segment.Class)
	}

	return classes
}

func rowTexts(view View) []string {
	texts := make([]string, 0, len(view.Rows))
	for _, row := range view.Rows {
		texts = append(texts, rowText(row))
	}

	return texts
}

func TestRenderMergesSegments(t *testing.T) {
	line := terminal.Line{
		blankCell('a'), blankCell('a'),
		styledCell('b', 1, terminal.DefaultColor), styledCell('b', 1, terminal.DefaultColor),
		{Rune: 'c', FG: 1, BG: terminal.DefaultColor, Bold: true},
	}

	view := Render(testSnapshot([]terminal.Line{line}, 1), 1, 0)
	if len(view.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(view.Rows))
	}

	want := []Segment{
		{Text: "aa"},
		{Text: "bb", Class: "fg1"},
		{Text: "c", Class: "fg1 bold"},
	}
	got := view.Rows[0].Segments
	if len(got) != len(want) {
		t.Fatalf("segments = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestRenderColorsAndAttributes(t *testing.T) {
	tests := []struct {
		name  string
		cell  terminal.Cell
		class string
	}{
		{name: "default", cell: blankCell('x')},
		{name: "palette foreground and background", cell: styledCell('x', 1, 4), class: "fg1 bg4"},
		{name: "bright palette", cell: styledCell('x', 15, 8), class: "fg15 bg8"},
		{name: "attributes", cell: terminal.Cell{Rune: 'x', FG: terminal.DefaultColor, BG: terminal.DefaultColor, Bold: true, Dim: true, Italic: true, Underline: true}, class: "bold dim italic underline"},
		{name: "all classes", cell: terminal.Cell{Rune: 'x', FG: 2, BG: 4, Bold: true, Dim: true, Italic: true, Underline: true}, class: "fg2 bg4 bold dim italic underline"},
		{name: "out of palette foreground", cell: styledCell('x', terminal.DefaultColor+1, terminal.DefaultColor)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			view := Render(testSnapshot([]terminal.Line{{test.cell}}, 1), 1, 0)
			if got := view.Rows[0].Segments[0].Class; got != test.class {
				t.Fatalf("class = %q, want %q", got, test.class)
			}
		})
	}
}

func TestRenderReverse(t *testing.T) {
	tests := []struct {
		name  string
		cell  terminal.Cell
		class string
	}{
		{
			name:  "swaps foreground and background",
			cell:  terminal.Cell{Rune: 'x', FG: 1, BG: 4, Reverse: true},
			class: "fg4 bg1",
		},
		{
			name:  "default background becomes dark foreground",
			cell:  terminal.Cell{Rune: 'x', FG: 1, BG: terminal.DefaultColor, Reverse: true},
			class: "fg0 bg1",
		},
		{
			name:  "default foreground becomes light background",
			cell:  terminal.Cell{Rune: 'x', FG: terminal.DefaultColor, BG: 4, Reverse: true},
			class: "fg4 bg7",
		},
		{
			name:  "same index on both sides",
			cell:  terminal.Cell{Rune: 'x', FG: 2, BG: 2, Reverse: true},
			class: "fg2 bg2",
		},
		{
			name:  "both defaults get an inverse block",
			cell:  terminal.Cell{Rune: 'x', FG: terminal.DefaultColor, BG: terminal.DefaultColor, Reverse: true},
			class: "fg0 bg7",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			view := Render(testSnapshot([]terminal.Line{{test.cell}}, 1), 1, 0)
			if got := view.Rows[0].Segments[0].Class; got != test.class {
				t.Fatalf("class = %q, want %q", got, test.class)
			}
		})
	}
}

func TestRenderTrimsTrailingBlanks(t *testing.T) {
	line := trailingBlanks(textLine("hi"), 3)
	view := Render(testSnapshot([]terminal.Line{line}, 1), 1, 0)
	row := view.Rows[0]

	if row.Empty {
		t.Fatal("row marked empty")
	}
	if got := rowText(row); got != "hi" {
		t.Errorf("text = %q, want %q", got, "hi")
	}
	if len(row.Segments) != 1 {
		t.Errorf("segments = %#v, want one", row.Segments)
	}
}

func TestRenderBlankLineKeepsOneCell(t *testing.T) {
	line := make(terminal.Line, 4)
	for i := range line {
		line[i] = blankCell(' ')
	}

	view := Render(testSnapshot([]terminal.Line{line}, 1), 1, 0)
	row := view.Rows[0]

	if !row.Empty {
		t.Fatal("row not marked empty")
	}
	if got := rowText(row); got != " " {
		t.Errorf("text = %q, want one space", got)
	}
	if len(row.Segments) != 1 || row.Segments[0].Class != "" {
		t.Errorf("segments = %#v, want one unstyled space", row.Segments)
	}
}

func TestRenderKeepsInteriorAndStyledSpaces(t *testing.T) {
	line := terminal.Line{
		blankCell('a'), blankCell(' '), blankCell(' '), blankCell('b'),
		blankCell(' '), styledCell(' ', terminal.DefaultColor, 4), blankCell(' '),
	}

	view := Render(testSnapshot([]terminal.Line{line}, 1), 1, 0)
	row := view.Rows[0]

	if got := rowText(row); got != "a  b  " {
		t.Fatalf("text = %q, want %q", got, "a  b  ")
	}
	if len(row.Segments) != 2 {
		t.Fatalf("segments = %#v, want two", row.Segments)
	}
	if row.Segments[0].Text != "a  b " || row.Segments[0].Class != "" {
		t.Errorf("first segment = %#v", row.Segments[0])
	}
	if row.Segments[1].Text != " " || row.Segments[1].Class != "bg4" {
		t.Errorf("styled space segment = %#v", row.Segments[1])
	}
}

func scrollLines(count int) []terminal.Line {
	lines := make([]terminal.Line, count)
	for i := range lines {
		lines[i] = textLine(fmt.Sprintf("L%d", i))
	}

	return lines
}

func TestRenderScrollbackWindows(t *testing.T) {
	snapshot := testSnapshot(scrollLines(10), 3)

	tests := []struct {
		name       string
		visible    int
		scroll     int
		wantText   []string
		wantScroll int
		wantMax    int
	}{
		{name: "live tail", visible: 3, scroll: 0, wantText: []string{"L7", "L8", "L9"}, wantScroll: 0, wantMax: 7},
		{name: "two lines back", visible: 3, scroll: 2, wantText: []string{"L5", "L6", "L7"}, wantScroll: 2, wantMax: 7},
		{name: "oldest window", visible: 3, scroll: 7, wantText: []string{"L0", "L1", "L2"}, wantScroll: 7, wantMax: 7},
		{name: "clamped", visible: 3, scroll: 99, wantText: []string{"L0", "L1", "L2"}, wantScroll: 7, wantMax: 7},
		{name: "negative clamped", visible: 3, scroll: -2, wantText: []string{"L7", "L8", "L9"}, wantScroll: 0, wantMax: 7},
		{name: "fallback rows", visible: 0, scroll: 0, wantText: []string{"L7", "L8", "L9"}, wantScroll: 0, wantMax: 7},
		{name: "more rows than lines", visible: 20, scroll: 0, wantText: []string{"L0", "L1", "L2", "L3", "L4", "L5", "L6", "L7", "L8", "L9"}, wantScroll: 0, wantMax: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			view := Render(snapshot, test.visible, test.scroll)

			if got := rowTexts(view); strings.Join(got, ",") != strings.Join(test.wantText, ",") {
				t.Fatalf("rows = %#v, want %#v", got, test.wantText)
			}
			if view.Scroll != test.wantScroll {
				t.Errorf("scroll = %d, want %d", view.Scroll, test.wantScroll)
			}
			if view.MaxScroll != test.wantMax {
				t.Errorf("max scroll = %d, want %d", view.MaxScroll, test.wantMax)
			}
		})
	}
}

func TestClampScroll(t *testing.T) {
	tests := []struct {
		name    string
		scroll  int
		total   int
		visible int
		want    int
		wantMax int
	}{
		{name: "within range", scroll: 5, total: 10, visible: 3, want: 5, wantMax: 7},
		{name: "too large", scroll: 99, total: 10, visible: 3, want: 7, wantMax: 7},
		{name: "negative", scroll: -4, total: 10, visible: 3, want: 0, wantMax: 7},
		{name: "no history", scroll: 5, total: 2, visible: 3, want: 0, wantMax: 0},
		{name: "empty", scroll: 5, total: 0, visible: 0, want: 0, wantMax: 0},
		{name: "negative total", scroll: 5, total: -1, visible: 3, want: 0, wantMax: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scroll, maxScroll := ClampScroll(test.scroll, test.total, test.visible)
			if scroll != test.want || maxScroll != test.wantMax {
				t.Fatalf("got (%d, %d), want (%d, %d)", scroll, maxScroll, test.want, test.wantMax)
			}
		})
	}
}

func TestRenderCursorInsideLine(t *testing.T) {
	snapshot := testSnapshot([]terminal.Line{trailingBlanks(textLine("abc"), 2)}, 1)
	snapshot.CursorVisible = true
	snapshot.CursorLine = 0
	snapshot.CursorCol = 1

	view := Render(snapshot, 1, 0)
	if view.CursorRow != 0 || view.CursorCol != 1 {
		t.Fatalf("cursor = (%d, %d), want (0, 1)", view.CursorRow, view.CursorCol)
	}

	wantSegments := []Segment{
		{Text: "a"},
		{Text: "b", Class: "cursor"},
		{Text: "c"},
	}
	got := view.Rows[0].Segments
	if len(got) != len(wantSegments) {
		t.Fatalf("segments = %#v, want %#v", got, wantSegments)
	}
	for i := range wantSegments {
		if got[i] != wantSegments[i] {
			t.Errorf("segment %d = %#v, want %#v", i, got[i], wantSegments[i])
		}
	}
}

func TestRenderCursorAtEndOfText(t *testing.T) {
	snapshot := testSnapshot([]terminal.Line{trailingBlanks(textLine("abc"), 2)}, 1)
	snapshot.CursorVisible = true
	snapshot.CursorLine = 0
	snapshot.CursorCol = 3

	view := Render(snapshot, 1, 0)
	row := view.Rows[0]

	if got := rowText(row); got != "abc " {
		t.Fatalf("text = %q, want %q", got, "abc ")
	}
	if len(row.Segments) != 2 {
		t.Fatalf("segments = %#v, want text then cursor", row.Segments)
	}
	if row.Segments[1].Text != " " || row.Segments[1].Class != "cursor" {
		t.Errorf("cursor segment = %#v", row.Segments[1])
	}
}

func TestRenderCursorAtEndOfShortLine(t *testing.T) {
	snapshot := testSnapshot([]terminal.Line{textLine("abc")}, 1)
	snapshot.CursorVisible = true
	snapshot.CursorLine = 0
	snapshot.CursorCol = 3

	view := Render(snapshot, 1, 0)
	row := view.Rows[0]

	if got := rowText(row); got != "abc " {
		t.Fatalf("text = %q, want %q", got, "abc ")
	}
	last := row.Segments[len(row.Segments)-1]
	if last.Text != " " || last.Class != "cursor" {
		t.Errorf("last segment = %#v, want one cursor space", last)
	}
}

func TestRenderCursorPastVisibleText(t *testing.T) {
	snapshot := testSnapshot([]terminal.Line{trailingBlanks(textLine("ab"), 4)}, 1)
	snapshot.CursorVisible = true
	snapshot.CursorLine = 0
	snapshot.CursorCol = 4

	view := Render(snapshot, 1, 0)
	row := view.Rows[0]

	if got := rowText(row); got != "ab   " {
		t.Fatalf("text = %q, want %q", got, "ab   ")
	}
	last := row.Segments[len(row.Segments)-1]
	if last.Text != " " || last.Class != "cursor" {
		t.Errorf("last segment = %#v, want one cursor space", last)
	}
}

func TestRenderCursorOnBlankLine(t *testing.T) {
	line := make(terminal.Line, 4)
	for i := range line {
		line[i] = blankCell(' ')
	}

	snapshot := testSnapshot([]terminal.Line{line}, 1)
	snapshot.CursorVisible = true
	snapshot.CursorLine = 0
	snapshot.CursorCol = 2

	view := Render(snapshot, 1, 0)
	row := view.Rows[0]

	if !row.Empty {
		t.Error("row not marked empty")
	}
	if got := rowText(row); got != "   " {
		t.Errorf("text = %q, want three spaces", got)
	}
	last := row.Segments[len(row.Segments)-1]
	if last.Text != " " || last.Class != "cursor" {
		t.Errorf("last segment = %#v, want one cursor space", last)
	}
}

func TestRenderCursorOutsideLine(t *testing.T) {
	snapshot := testSnapshot([]terminal.Line{trailingBlanks(textLine("ab"), 4)}, 1)
	snapshot.CursorVisible = true
	snapshot.CursorLine = 0
	snapshot.CursorCol = 10

	view := Render(snapshot, 1, 0)
	row := view.Rows[0]

	if view.CursorRow != -1 {
		t.Fatalf("cursor row = %d, want -1 for an off-line cursor", view.CursorRow)
	}
	if got := rowText(row); got != "ab" {
		t.Fatalf("text = %q, want %q", got, "ab")
	}
	for _, segment := range row.Segments {
		if strings.Contains(segment.Class, "cursor") {
			t.Errorf("unexpected cursor class in %#v", row.Segments)
		}
	}
}

func TestRenderHiddenCursor(t *testing.T) {
	snapshot := testSnapshot([]terminal.Line{trailingBlanks(textLine("abc"), 2)}, 1)
	snapshot.CursorLine = 0
	snapshot.CursorCol = 1

	view := Render(snapshot, 1, 0)
	if view.CursorRow != -1 || view.CursorCol != 0 {
		t.Fatalf("cursor = (%d, %d), want (-1, 0)", view.CursorRow, view.CursorCol)
	}
	if got := rowText(view.Rows[0]); got != "abc" {
		t.Errorf("text = %q, want abc", got)
	}
	if len(view.Rows[0].Segments) != 1 {
		t.Errorf("segments = %#v, want merged text", view.Rows[0].Segments)
	}
}

func TestRenderCursorHiddenWhenNotRunning(t *testing.T) {
	snapshot := testSnapshot([]terminal.Line{trailingBlanks(textLine("abc"), 2)}, 1)
	snapshot.Running = false
	snapshot.CursorVisible = true
	snapshot.CursorLine = 0
	snapshot.CursorCol = 1

	view := Render(snapshot, 1, 0)
	if view.CursorRow != -1 {
		t.Fatalf("cursor row = %d, want -1", view.CursorRow)
	}
	for _, segment := range view.Rows[0].Segments {
		if strings.Contains(segment.Class, "cursor") {
			t.Errorf("unexpected cursor class in %#v", view.Rows[0].Segments)
		}
	}
}

func TestRenderCursorOutsideWindow(t *testing.T) {
	snapshot := testSnapshot(scrollLines(10), 3)
	snapshot.CursorVisible = true
	snapshot.CursorLine = 0
	snapshot.CursorCol = 0

	live := Render(snapshot, 3, 0)
	if live.CursorRow != -1 {
		t.Fatalf("live cursor row = %d, want -1", live.CursorRow)
	}

	scrolled := Render(snapshot, 3, 7)
	if scrolled.CursorRow != 0 {
		t.Fatalf("scrolled cursor row = %d, want 0", scrolled.CursorRow)
	}
	if !strings.Contains(scrolled.Rows[0].Segments[0].Class, "cursor") {
		t.Fatalf("cursor class missing from %#v", scrolled.Rows[0].Segments)
	}
}

func TestKeyBytesSpecialKeys(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{key: "enter", want: "\r"},
		{key: "tab", want: "\t"},
		{key: "escape", want: "\x1b"},
		{key: "backspace", want: "\x7f"},
		{key: "arrowup", want: "\x1b[A"},
		{key: "arrowdown", want: "\x1b[B"},
		{key: "arrowright", want: "\x1b[C"},
		{key: "arrowleft", want: "\x1b[D"},
		{key: "home", want: "\x1b[H"},
		{key: "end", want: "\x1b[F"},
		{key: "insert", want: "\x1b[2~"},
		{key: "delete", want: "\x1b[3~"},
		{key: "pageup", want: "\x1b[5~"},
		{key: "pagedown", want: "\x1b[6~"},
		{key: "f1", want: "\x1bOP"},
		{key: "f2", want: "\x1bOQ"},
		{key: "f3", want: "\x1bOR"},
		{key: "f4", want: "\x1bOS"},
		{key: "f5", want: "\x1b[15~"},
		{key: "f6", want: "\x1b[17~"},
		{key: "f7", want: "\x1b[18~"},
		{key: "f8", want: "\x1b[19~"},
		{key: "f9", want: "\x1b[20~"},
		{key: "f10", want: "\x1b[21~"},
		{key: "f11", want: "\x1b[23~"},
		{key: "f12", want: "\x1b[24~"},
	}

	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			got, ok := KeyBytes(test.key, false, false, false)
			if !ok || string(got) != test.want {
				t.Fatalf("KeyBytes(%q) = (%q, %v), want (%q, true)", test.key, got, ok, test.want)
			}
		})
	}
}

func TestKeyBytesModifiers(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		ctrl, shift bool
		alt         bool
		want        string
	}{
		{name: "shift arrow", key: "arrowup", shift: true, want: "\x1b[1;2A"},
		{name: "ctrl shift arrow", key: "arrowright", ctrl: true, shift: true, want: "\x1b[1;6C"},
		{name: "alt arrow", key: "arrowup", alt: true, want: "\x1b[1;3A"},
		{name: "ctrl alt arrow", key: "arrowup", ctrl: true, alt: true, want: "\x1b[1;7A"},
		{name: "all arrow", key: "arrowup", ctrl: true, shift: true, alt: true, want: "\x1b[1;8A"},
		{name: "shift home", key: "home", shift: true, want: "\x1b[1;2H"},
		{name: "ctrl end", key: "end", ctrl: true, want: "\x1b[1;5F"},
		{name: "shift insert", key: "insert", shift: true, want: "\x1b[2;2~"},
		{name: "alt delete", key: "delete", alt: true, want: "\x1b[3;3~"},
		{name: "shift f1", key: "f1", shift: true, want: "\x1b[1;2P"},
		{name: "ctrl f5", key: "f5", ctrl: true, want: "\x1b[15;5~"},
		{name: "alt f12", key: "f12", alt: true, want: "\x1b[24;3~"},
		{name: "alt letter", key: "a", alt: true, want: "\x1ba"},
		{name: "alt shifted letter", key: "a", shift: true, alt: true, want: "\x1bA"},
		{name: "alt space", key: "space", alt: true, want: "\x1b "},
		{name: "alt enter", key: "enter", alt: true, want: "\x1b\r"},
		{name: "alt escape", key: "escape", alt: true, want: "\x1b\x1b"},
		{name: "backtab", key: "tab", shift: true, want: "\x1b[Z"},
		{name: "ctrl alt letter", key: "a", ctrl: true, alt: true, want: "\x1b\x01"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := KeyBytes(test.key, test.ctrl, test.shift, test.alt)
			if !ok || string(got) != test.want {
				t.Fatalf("KeyBytes(%q, %v, %v, %v) = (%q, %v), want (%q, true)",
					test.key, test.ctrl, test.shift, test.alt, got, ok, test.want)
			}
		})
	}
}

func TestKeyBytesApplicationCursor(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{key: "arrowup", want: "\x1bOA"},
		{key: "arrowdown", want: "\x1bOB"},
		{key: "arrowright", want: "\x1bOC"},
		{key: "arrowleft", want: "\x1bOD"},
		{key: "home", want: "\x1bOH"},
		{key: "end", want: "\x1bOF"},
	}
	for _, test := range tests {
		got, ok := KeyBytesFor(test.key, false, false, false, true)
		if !ok || string(got) != test.want {
			t.Errorf("KeyBytesFor(%q, appCursor) = (%q, %v), want (%q, true)", test.key, got, ok, test.want)
		}
	}

	if got, ok := KeyBytesFor("arrowup", false, true, false, true); !ok || string(got) != "\x1b[1;2A" {
		t.Errorf("modified arrow in app mode = (%q, %v), want CSI form", got, ok)
	}
}

func TestKeyBytesControl(t *testing.T) {
	tests := []struct {
		key  string
		want byte
	}{
		{key: "a", want: 0x01},
		{key: "z", want: 0x1a},
		{key: "A", want: 0x01},
		{key: "[", want: 0x1b},
		{key: "\\", want: 0x1c},
		{key: "]", want: 0x1d},
		{key: "^", want: 0x1e},
		{key: "_", want: 0x1f},
		{key: "space", want: 0x00},
		{key: " ", want: 0x00},
		{key: "?", want: 0x7f},
	}

	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			got, ok := KeyBytes(test.key, true, false, false)
			if !ok || len(got) != 1 || got[0] != test.want {
				t.Fatalf("KeyBytes(%q, ctrl) = (%q, %v), want %#x", test.key, got, ok, test.want)
			}
		})
	}
}

func TestKeyBytesReturnsFalse(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		ctrl, shift bool
		alt         bool
	}{
		{name: "plain letter", key: "a"},
		{name: "plain symbol", key: "!"},
		{name: "unknown key", key: "hyper"},
		{name: "unknown control", key: "1", ctrl: true},
		{name: "shift page up", key: "pageup", shift: true},
		{name: "shift page down", key: "pagedown", shift: true},
		{name: "ctrl shift page up", key: "pageup", ctrl: true, shift: true},
		{name: "alt shift page up", key: "pageup", shift: true, alt: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := KeyBytes(test.key, test.ctrl, test.shift, test.alt)
			if ok || got != nil {
				t.Fatalf("KeyBytes(%q, %v, %v, %v) = (%q, %v), want (nil, false)",
					test.key, test.ctrl, test.shift, test.alt, got, ok)
			}
		})
	}
}

func TestControlByte(t *testing.T) {
	tests := []struct {
		key  string
		want byte
		ok   bool
	}{
		{key: "a", want: 0x01, ok: true},
		{key: "Z", want: 0x1a, ok: true},
		{key: "[", want: 0x1b, ok: true},
		{key: "\\", want: 0x1c, ok: true},
		{key: "]", want: 0x1d, ok: true},
		{key: "^", want: 0x1e, ok: true},
		{key: "_", want: 0x1f, ok: true},
		{key: "space", want: 0x00, ok: true},
		{key: " ", want: 0x00, ok: true},
		{key: "?", want: 0x7f, ok: true},
		{key: "enter"},
		{key: "1"},
		{key: ""},
	}

	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			got, ok := ControlByte(test.key)
			if ok != test.ok || (ok && got != test.want) {
				t.Fatalf("ControlByte(%q) = (%#x, %v), want (%#x, %v)", test.key, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestRenderDoesNotMutateSnapshot(t *testing.T) {
	line := trailingBlanks(textLine("abc"), 2)
	line[1].Bold = true
	line[1].Reverse = true
	snapshot := testSnapshot([]terminal.Line{line}, 1)

	before := make(terminal.Line, len(line))
	copy(before, line)

	_ = Render(snapshot, 1, 0)

	for i := range line {
		if line[i] != before[i] {
			t.Fatalf("cell %d changed from %#v to %#v", i, before[i], line[i])
		}
	}
}

func TestRenderPartialViewport(t *testing.T) {
	s := terminal.NewScreen(20, 4)
	s.Feed([]byte(strings.Repeat("line\r\n", 30)))
	for _, scroll := range []int{0, 2, 25, 5000} {
		full := s.Snapshot()
		full.Running = true
		partial := s.SnapshotViewport(4, scroll)
		partial.Running = true
		want := Render(full, 4, scroll)
		got := Render(partial, 4, scroll)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("scroll %d: partial %v differs from full %v", scroll, got, want)
		}
	}
}

func TestNativeModifierKeyNames(t *testing.T) {
	for key, want := range map[string]string{
		"ctrl+arrowleft":  "\x1b[1;5D",
		"alt+arrowright":  "\x1b[1;3C",
		"ctrl+shift+home": "\x1b[1;6H",
	} {
		got, ok := KeyBytesFor(key, false, false, false, false)
		if !ok || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q", key, got, ok, want)
		}
	}
}
