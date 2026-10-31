package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/terminal"
)

func TestTerminalModelSessionTabs(t *testing.T) {
	t.Parallel()

	sessions := []terminal.SessionInfo{
		{ID: 7, Title: "third", Running: true},
		{ID: 2, Title: "first"},
		{ID: 5, Title: "second", Running: true},
	}

	view := terminalViewState(false, false, -30, 5, sessions, terminal.Snapshot{}, false, 0, 0)

	if view.Height != 0 {
		t.Errorf("Height = %d, want 0", view.Height)
	}
	if view.Active != 5 {
		t.Errorf("Active = %d, want 5", view.Active)
	}

	want := []terminalTabRow{
		{ID: 2, Title: "first"},
		{ID: 5, Title: "second", Running: true, Active: true},
		{ID: 7, Title: "third", Running: true},
	}
	if !slices.Equal(view.Sessions, want) {
		t.Errorf("Sessions = %+v, want %+v", view.Sessions, want)
	}

	if got := []int{sessions[0].ID, sessions[1].ID, sessions[2].ID}; !slices.Equal(got, []int{7, 2, 5}) {
		t.Errorf("input sessions reordered: %v", got)
	}
}

func TestTerminalModelEmptySessions(t *testing.T) {
	t.Parallel()

	view := terminalViewState(false, false, 0, 9, nil, terminal.Snapshot{}, false, 0, 0)
	if len(view.Sessions) != 0 {
		t.Errorf("Sessions = %+v, want none", view.Sessions)
	}
	if view.Active != 9 {
		t.Errorf("Active = %d, want 9", view.Active)
	}
}

func TestTerminalModelRendersSnapshot(t *testing.T) {
	t.Parallel()

	snapshot := terminalTestSnapshot("alpha", "beta")
	snapshot.Running = false
	snapshot.Note = "from snapshot"

	sessions := []terminal.SessionInfo{
		{ID: 1, Title: "bash", Note: "exited"},
		{ID: 2, Title: "zsh", Running: true, Note: "from session"},
	}
	view := terminalViewState(true, true, 300, 2, sessions, snapshot, true, 24, 0)

	if !view.Open || !view.Focus {
		t.Errorf("Open, Focus = %v, %v, want true, true", view.Open, view.Focus)
	}
	if view.Height != 300 {
		t.Errorf("Height = %d, want 300", view.Height)
	}
	if !view.Running || view.Note != "from session" {
		t.Errorf("Running, Note = %v, %q, want true, %q", view.Running, view.Note, "from session")
	}
	if len(view.Rows) != 24 {
		t.Fatalf("len(Rows) = %d, want 24", len(view.Rows))
	}

	text := terminalRowsText(view.Rows)
	if !strings.Contains(text, "alpha") || !strings.Contains(text, "beta") {
		t.Errorf("rendered rows = %q, want alpha and beta", text)
	}

	if view.Scroll != 0 {
		t.Errorf("Scroll = %d, want 0", view.Scroll)
	}
	if len(view.Rows[0].Segments) == 0 {
		t.Errorf("first row has no segments")
	}
}

func TestTerminalModelSnapshotFallback(t *testing.T) {
	t.Parallel()

	snapshot := terminalTestSnapshot("alpha")
	snapshot.Running = true
	snapshot.Note = "from snapshot"

	view := terminalViewState(true, false, 100, 9, nil, snapshot, true, 24, 0)
	if !view.Running || view.Note != "from snapshot" {
		t.Errorf("Running, Note = %v, %q, want true, %q", view.Running, view.Note, "from snapshot")
	}
}

func TestTerminalModelNoSnapshot(t *testing.T) {
	t.Parallel()

	sessions := []terminal.SessionInfo{{ID: 1, Title: "bash", Note: "exited"}}
	snapshot := terminal.Snapshot{Running: true, Note: "from snapshot"}
	view := terminalViewState(true, true, 240, 1, sessions, snapshot, false, 10, 3)

	if !view.Open || !view.Focus || view.Height != 240 {
		t.Errorf("Open, Focus, Height = %v, %v, %d, want true, true, 240", view.Open, view.Focus, view.Height)
	}
	if view.Running || view.Note != "exited" {
		t.Errorf("Running, Note = %v, %q, want false, %q", view.Running, view.Note, "exited")
	}
	if view.Rows != nil {
		t.Errorf("Rows = %+v, want nil", view.Rows)
	}
	if view.Scroll != 0 || view.MaxScroll != 0 {
		t.Errorf("Scroll, MaxScroll = %d, %d, want 0, 0", view.Scroll, view.MaxScroll)
	}
	if len(view.Sessions) != 1 || !view.Sessions[0].Active {
		t.Errorf("Sessions = %+v, want one active tab", view.Sessions)
	}
}

func TestTerminalModelClosedDoesNotRender(t *testing.T) {
	t.Parallel()

	snapshot := terminalTestSnapshot("alpha")
	sessions := []terminal.SessionInfo{{ID: 1, Title: "bash", Running: true}}
	view := terminalViewState(false, false, 100, 1, sessions, snapshot, true, 24, 0)

	if view.Rows != nil || view.Scroll != 0 || view.MaxScroll != 0 {
		t.Errorf("closed view rendered rows: Rows=%v Scroll=%d MaxScroll=%d", view.Rows, view.Scroll, view.MaxScroll)
	}
	if !view.Running {
		t.Errorf("Running = false, want true from active session")
	}
}

func TestTerminalModelScrollPropagation(t *testing.T) {
	t.Parallel()

	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "line-" + string(rune('a'+i))
	}
	snapshot := terminalManualSnapshot(lines...)

	view := terminalViewState(true, false, 100, 0, nil, snapshot, true, 4, 2)
	if view.Scroll != 2 {
		t.Errorf("Scroll = %d, want 2", view.Scroll)
	}
	if view.MaxScroll <= 0 {
		t.Errorf("MaxScroll = %d, want positive", view.MaxScroll)
	}
	if len(view.Rows) != 4 {
		t.Errorf("len(Rows) = %d, want 4", len(view.Rows))
	}

	clamped := terminalViewState(true, false, 100, 0, nil, snapshot, true, 4, 999)
	if clamped.Scroll != clamped.MaxScroll {
		t.Errorf("clamped Scroll = %d, want MaxScroll %d", clamped.Scroll, clamped.MaxScroll)
	}
}

func TestTerminalModelNonPositiveVisibleRows(t *testing.T) {
	t.Parallel()

	snapshot := terminalManualSnapshot("alpha", "beta", "gamma")

	for _, visible := range []int{0, -3} {
		view := terminalViewState(true, false, 100, 0, nil, snapshot, true, visible, 0)
		if len(view.Rows) != snapshot.Rows {
			t.Errorf("visible=%d: len(Rows) = %d, want fallback %d", visible, len(view.Rows), snapshot.Rows)
		}
		if view.Scroll != 0 || view.MaxScroll != 0 {
			t.Errorf("visible=%d: Scroll, MaxScroll = %d, %d, want 0, 0", visible, view.Scroll, view.MaxScroll)
		}
	}
}

func TestTerminalCopyText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rows []terminalRow
		want string
	}{
		{
			name: "joins segments and trims trailing blanks",
			rows: []terminalRow{
				{Segments: []segment{{Text: "hello", Class: "plain"}, {Text: " world", Class: "plain"}}},
				{Segments: []segment{{Text: "second", Class: "plain"}}},
				{Empty: true},
				{Segments: []segment{{Text: "   ", Class: "plain"}}},
			},
			want: "hello world\nsecond",
		},
		{
			name: "keeps leading blank rows",
			rows: []terminalRow{
				{Empty: true},
				{Segments: []segment{{Text: "text", Class: "plain"}}},
				{Empty: true},
			},
			want: "\ntext",
		},
		{
			name: "empty view",
			rows: nil,
			want: "",
		},
		{
			name: "only blank rows",
			rows: []terminalRow{{Empty: true}, {Segments: []segment{{Text: " ", Class: "plain"}}}},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := terminalCopyText(terminalView{Rows: tc.rows}); got != tc.want {
				t.Errorf("terminalCopyText() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTerminalScrollBy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		scroll    int
		maxScroll int
		delta     int
		want      int
	}{
		{name: "moves up", scroll: 0, maxScroll: 10, delta: 3, want: 3},
		{name: "clamps at bottom", scroll: 9, maxScroll: 10, delta: 5, want: 10},
		{name: "clamps at top", scroll: 2, maxScroll: 10, delta: -5, want: 0},
		{name: "clamps existing scroll", scroll: 20, maxScroll: 10, want: 10},
		{name: "negative max scroll", scroll: 5, maxScroll: -1, delta: -2, want: 0},
		{name: "zero range", scroll: 0, maxScroll: 0, delta: 4, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := terminalScrollBy(tc.scroll, tc.maxScroll, tc.delta); got != tc.want {
				t.Errorf("terminalScrollBy(%d, %d, %d) = %d, want %d", tc.scroll, tc.maxScroll, tc.delta, got, tc.want)
			}
		})
	}
}

func terminalTestSnapshot(lines ...string) terminal.Snapshot {
	screen := terminal.NewScreen(80, 24)
	screen.Feed([]byte(strings.Join(lines, "\r\n")))
	return screen.Snapshot()
}

func terminalManualSnapshot(lines ...string) terminal.Snapshot {
	snapshot := terminal.Snapshot{Cols: 80, Rows: len(lines), CursorLine: max(0, len(lines)-1)}
	for _, text := range lines {
		line := make(terminal.Line, 80)
		for i := range line {
			line[i] = terminal.Cell{Rune: ' ', FG: terminal.DefaultColor, BG: terminal.DefaultColor}
		}
		for i, r := range text {
			if i >= len(line) {
				break
			}
			line[i] = terminal.Cell{Rune: r, FG: terminal.DefaultColor, BG: terminal.DefaultColor}
		}
		snapshot.Lines = append(snapshot.Lines, line)
	}
	return snapshot
}

func terminalRowsText(rows []terminalRow) string {
	lines := make([]string, len(rows))
	for i, row := range rows {
		var b strings.Builder
		for _, part := range row.Segments {
			b.WriteString(part.Text)
		}
		lines[i] = b.String()
	}
	return strings.Join(lines, "\n")
}
