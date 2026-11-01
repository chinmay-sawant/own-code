package app

import (
	"sort"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/terminal"
	"github.com/chinmay-sawant/own-code/internal/termui"
)

// terminalTabRow is one session tab in the terminal panel.
type terminalTabRow struct {
	ID      int
	Title   string
	Running bool
	Active  bool
}

// terminalRow is one rendered terminal line.
type terminalRow struct {
	Segments []segment
	Empty    bool
}

// terminalView is the terminal panel state for one frame.
type terminalView struct {
	Open           bool
	Height         int
	Focus          bool
	Sessions       []terminalTabRow
	Active         int
	Rows           []terminalRow
	Scroll         int
	MaxScroll      int
	Running        bool
	Note           string
	Shell          string
	AppCursor      bool
	BracketedPaste bool
}

// terminalViewState assembles one terminal frame from manager/session state.
func terminalViewState(open, focus bool, height int, activeID int, sessions []terminal.SessionInfo, snapshot terminal.Snapshot, hasSnapshot bool, visibleRows, scroll int) terminalView {
	view := terminalView{
		Open:   open,
		Focus:  focus,
		Height: max(0, height),
		Active: activeID,
	}

	view.Sessions = make([]terminalTabRow, 0, len(sessions))
	for _, info := range sessions {
		view.Sessions = append(view.Sessions, terminalTabRow{
			ID:      info.ID,
			Title:   info.Title,
			Running: info.Running,
			Active:  info.ID == activeID,
		})
	}
	sort.Slice(view.Sessions, func(i, j int) bool {
		return view.Sessions[i].ID < view.Sessions[j].ID
	})

	view.Running = snapshot.Running
	view.Note = snapshot.Note
	view.AppCursor = snapshot.AppCursor
	view.BracketedPaste = snapshot.BracketedPaste
	for _, info := range sessions {
		if info.ID == activeID {
			view.Running = info.Running
			view.Note = info.Note
			break
		}
	}

	if !open || !hasSnapshot {
		return view
	}

	rendered := termui.Render(snapshot, visibleRows, scroll)
	view.Rows = make([]terminalRow, len(rendered.Rows))
	for i, row := range rendered.Rows {
		segments := make([]segment, len(row.Segments))
		for j, part := range row.Segments {
			segments[j] = segment{Text: part.Text, Class: part.Class, LinkPath: part.LinkPath, LinkLine: part.LinkLine, LinkCol: part.LinkCol}
		}
		view.Rows[i] = terminalRow{Segments: segments, Empty: row.Empty}
	}
	view.Scroll = rendered.Scroll
	view.MaxScroll = rendered.MaxScroll

	return view
}

// terminalCopyText joins the rendered rows into copyable plain text. Trailing
// blank rows are dropped so copying an unfilled screen does not add newlines.
func terminalCopyText(view terminalView) string {
	lines := make([]string, len(view.Rows))
	for i, row := range view.Rows {
		var b strings.Builder
		for _, part := range row.Segments {
			b.WriteString(part.Text)
		}
		lines[i] = b.String()
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// terminalScrollBy moves the terminal viewport by delta lines and clamps it to
// the current scrollable range.
func terminalScrollBy(scroll, maxScroll, delta int) int {
	if maxScroll < 0 {
		maxScroll = 0
	}
	return max(0, min(scroll+delta, maxScroll))
}
