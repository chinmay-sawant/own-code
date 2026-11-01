package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/terminal"
	"github.com/chinmay-sawant/own-code/internal/termui"
	"github.com/chinmay-sawant/ownframe"
)

const (
	termRowH          = 16
	termCellW         = 7.2 // monospace advance at the 12 px terminal face, matching diffCellW
	termDefaultHeight = 240
	termMinHeight     = 120

	termResizeH  = 5
	termHeadH    = 30
	termFootH    = 18
	termNoteH    = 18
	termBodyPad  = 12
	termWidthPad = 20
	termTopGap   = 80
)

// Close stops every terminal shell. It is safe to call more than once and
// when New never ran.
func (a *App) Close() {
	if a == nil || a.term == nil {
		return
	}

	_ = a.term.CloseAll()
}

// closeTerminalSessions stops every shell and clears the panel's transient
// state. The panel stays open, empty, so the next explicit toggle or New
// session can start a shell in the new workspace.
func (a *App) closeTerminalSessions() {
	if a.term != nil {
		_ = a.term.CloseAll()
	}

	a.termScroll = 0
	a.termTotalLines = 0
	a.termVersion = 0
	a.termRunning = false
	a.termNote = ""
	a.termFocus = false
	a.termAppCursor = false
	a.termBracketedPaste = false
	a.termCols = 0
	a.termRows = 0
	a.termDrag = false
	a.termDragDy = 0
}

// terminalShell is the configured shell, falling back to the platform shell.
func (a *App) terminalShell() string {
	if shell := strings.TrimSpace(os.Getenv("OWN_CODE_SHELL")); shell != "" {
		return shell
	}

	return terminal.DefaultShell()
}

// terminalDir is the workspace root. The embedded sample has no directory on
// disk, so shell sessions start in the process working directory instead.
func (a *App) terminalDir() string {
	if a.ws != nil && !a.ws.Sample && strings.TrimSpace(a.ws.Root) != "" {
		return a.ws.Root
	}

	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	return dir
}

// termMaxHeight is the tallest panel the editor body can hold.
func (a *App) termMaxHeight() int {
	h := a.viewportH - a.zx(editorBodyTop) - a.zx(termTopGap)
	if h < a.zx(termMinHeight) {
		h = a.zx(termMinHeight)
	}

	return h
}

// termPanelHeight is the clamped panel height in page pixels.
func (a *App) termPanelHeight() int {
	base := a.termHeight
	if base <= 0 {
		base = termDefaultHeight
	}

	return max(a.zx(termMinHeight), min(a.zx(base), a.termMaxHeight()))
}

// termContentHeight is the output area after the resize strip, header,
// footer, optional note, and body padding.
func (a *App) termContentHeight() int {
	h := a.termPanelHeight() - a.zx(termResizeH) - a.zx(termHeadH) -
		a.zx(termFootH) - a.zx(termBodyPad)
	if a.termNote != "" {
		h -= a.zx(termNoteH)
	}

	return max(a.zx(termRowH), h)
}

// termVisibleRows is how many 16 px output rows fit in the panel.
func (a *App) termVisibleRows() int {
	return max(1, a.termContentHeight()/a.zx(termRowH))
}

// termColsForWidth is the fixed-width cell count for the editor column.
func (a *App) termColsForWidth() int {
	return max(20, int(float64(a.editorWidth()-termWidthPad)/a.zf(termCellW)))
}

// terminalMaxScroll is the largest bottom-relative scrollback offset.
func (a *App) terminalMaxScroll() int {
	return max(0, a.termTotalLines-a.termVisibleRows())
}

// requestTerminalFrame asks the window for a frame on shell output. It runs
// on the PTY reader goroutine, like the watcher loop's frame requests.
func (a *App) requestTerminalFrame() {
	if a.page != nil {
		a.page.WantFrameIn(0)
	}
}

// newTerminalManager creates the session manager and hooks shell output to
// frame requests, so typed echo and command output paint on the next frame
// instead of waiting for an unrelated repaint.
func (a *App) newTerminalManager() *terminal.Manager {
	m := terminal.NewManager()
	m.SetNotify(a.requestTerminalFrame)

	return m
}

// startTerminalSession opens one shell in the workspace root. Failures keep
// the panel open with a note instead of crashing or retrying on their own.
func (a *App) startTerminalSession(ctx context.Context) {
	a.termOpen = true
	_ = a.page.Form().Focus(ctx, "")

	if a.term == nil {
		a.term = a.newTerminalManager()
	}

	shell := a.terminalShell()
	a.termShell = shell

	_, err := a.term.Open(terminal.Config{
		Shell: shell,
		Dir:   a.terminalDir(),
		Cols:  a.termColsForWidth(),
		Rows:  a.termVisibleRows(),
	})
	if err != nil {
		a.termFocus = true
		a.termRunning = false
		a.termNote = fmt.Sprintf("Cannot start %s: %v", shell, err)
		a.showToast("error", "Terminal unavailable", a.termNote)
		a.refresh()

		return
	}

	a.termScroll = 0
	a.termTotalLines = 0
	a.termVersion = 0
	a.termRunning = true
	a.termNote = ""
	a.termFocus = true
	a.termAppCursor = false
	a.termBracketedPaste = false
	a.termCols = 0
	a.termRows = 0
	a.syncTerminalSize(ctx)
	a.refresh()
}

// toggleTerminal hides or shows the panel. Hiding leaves shells running.
func (a *App) toggleTerminal(ctx context.Context) {
	if a.termOpen {
		a.termOpen = false
		a.termFocus = false
		a.refresh()

		return
	}

	a.termOpen = true

	if a.term == nil {
		a.term = a.newTerminalManager()
	}

	if _, session := a.term.Active(); session == nil {
		a.startTerminalSession(ctx)

		return
	}

	a.termFocus = true
	a.termCols = 0
	a.termRows = 0
	_ = a.page.Form().Focus(ctx, "")
	a.syncTerminalSize(ctx)
	a.refresh()
}

// focusTerminal gives the keyboard back to the active shell.
func (a *App) focusTerminal(ctx context.Context) {
	a.termOpen = true
	a.termFocus = true
	a.refresh()

	_ = a.page.Form().Focus(ctx, "")
}

// pickTerminal switches the active session tab and resets scrollback to the
// live tail.
func (a *App) pickTerminal(ctx context.Context, id int) {
	if a.term == nil {
		return
	}

	a.term.SetActive(id)
	_ = a.page.Form().Focus(ctx, "")
	a.termFocus = true
	a.termScroll = 0
	a.termVersion = 0
	a.termTotalLines = 0
	a.termRunning = false
	a.termNote = ""
	a.termCols = 0
	a.termRows = 0
	a.syncTerminalSize(ctx)
	a.refresh()
}

// closeTerminal stops one session. The manager picks the next active id; the
// panel stays open with its empty state when no sessions remain.
func (a *App) closeTerminal(ctx context.Context, id int) {
	if a.term == nil {
		return
	}

	_ = a.term.Close(id)
	a.termScroll = 0
	a.termVersion = 0
	a.termTotalLines = 0
	a.termRunning = false
	a.termNote = ""
	a.termCols = 0
	a.termRows = 0
	a.syncTerminalSize(ctx)
	a.refresh()
}

// copyTerminal puts the visible output on the clipboard.
func (a *App) copyTerminal(ctx context.Context) error {
	text := terminalCopyText(a.view.Terminal)
	if strings.TrimSpace(text) == "" {
		return nil
	}

	ownframe.Write(text)
	a.showToast("success", "Terminal output copied", "The visible terminal text is on the clipboard.")
	a.refresh()

	return a.page.Redraw(ctx)
}

// clearTerminal erases the active screen and scrollback.
func (a *App) clearTerminal(ctx context.Context) {
	if a.term == nil {
		return
	}

	id, session := a.term.Active()
	if session != nil {
		_ = a.term.Clear(id)
	}

	a.termScroll = 0
	a.termTotalLines = 0
	a.refresh()
}

// scrollTerminal moves the output viewport and repaints.
func (a *App) scrollTerminal(ctx context.Context, delta int) error {
	a.termScroll = terminalScrollBy(a.termScroll, a.terminalMaxScroll(), delta)
	a.refresh()

	return a.page.Redraw(ctx)
}

// writeTerminal sends bytes only to a live active shell.
func (a *App) writeTerminal(data []byte) {
	if len(data) == 0 || a.term == nil {
		return
	}

	id, session := a.term.Active()
	if session == nil || !session.Info().Running {
		return
	}

	_ = a.term.Write(id, data)
}

// pasteTerminal sends clipboard text, using bracketed paste when the shell
// asked for it.
func (a *App) pasteTerminal(text string) {
	if text == "" {
		return
	}

	if a.termBracketedPaste {
		text = "\x1b[200~" + text + "\x1b[201~"
	}

	a.writeTerminal([]byte(text))
}

// pasteTerminalClipboard reads the OS clipboard and forwards it to the shell.
func (a *App) pasteTerminalClipboard() {
	text, err := ownframe.Read()
	if err != nil || text == "" {
		return
	}

	a.pasteTerminal(text)
}

// terminalOwnsKeys reports whether typed keys and clipboard chords belong to
// the terminal rather than the editor or a focused form field.
func (a *App) terminalOwnsKeys() bool {
	return a.termOpen && a.termFocus && !a.settingsOpen && !a.confirmOpen &&
		a.page != nil && a.page.Form().FocusID() == ""
}

// handleTerminalKey translates terminal-focused keys. It returns false only
// when another part of the app should own the key.
func (a *App) handleTerminalKey(ctx context.Context, key string) (bool, error) {
	if !a.terminalOwnsKeys() {
		return false, nil
	}

	// The window maps Ctrl+C/Ctrl+V/Ctrl+Shift+C/Ctrl+Shift+V to its copy
	// and paste chords after key handling. Let those chords reach onCopy and
	// onPaste instead of also writing a control byte to the shell.
	if a.ctrl && (key == "c" || key == "v") && (a.shift || key == "v") {
		return true, nil
	}

	// Backspace has its own window event; sending it here would delete twice.
	if key == "backspace" {
		return true, nil
	}

	if a.ctrl && !a.shift && (key == "backquote" || key == "grave" || key == "`") {
		a.toggleTerminal(ctx)

		return true, a.page.Redraw(ctx)
	}

	if a.shift && (key == "pageup" || key == "pagedown") {
		delta := a.termVisibleRows()
		if key == "pageup" {
			delta = -delta
		}

		return true, a.scrollTerminal(ctx, delta)
	}

	if seq, ok := termui.KeyBytesFor(key, a.ctrl, a.shift, false, a.termAppCursor); ok {
		a.writeTerminal(seq)

		return true, nil
	}

	// Printable text arrives through onType. Everything else is swallowed so
	// app shortcuts such as w, r, and n never fire while the shell types.
	return true, nil
}

// syncTerminalSize keeps the active PTY on the panel's current cell grid.
func (a *App) syncTerminalSize(_ context.Context) {
	if a.term == nil {
		return
	}

	id, session := a.term.Active()
	if session == nil {
		return
	}

	cols, rows := a.termColsForWidth(), a.termVisibleRows()
	if cols == a.termCols && rows == a.termRows {
		return
	}

	if err := a.term.Resize(id, cols, rows); err != nil {
		return
	}

	a.termCols, a.termRows = cols, rows
}

// pollTerminal copies the active screen when it changed. One refresh follows
// a change, so an idle shell costs only an Info read.
func (a *App) pollTerminal(ctx context.Context) {
	if !a.termOpen || a.term == nil {
		return
	}

	_, session := a.term.Active()
	if session == nil {
		sessions := a.term.Sessions()
		if len(sessions) == 0 {
			return
		}

		a.term.SetActive(sessions[0].ID)
		_, session = a.term.Active()
		if session == nil {
			return
		}
	}

	a.syncTerminalSize(ctx)

	info := session.Info()
	if info.Version == a.termVersion && info.Running == a.termRunning && info.Note == a.termNote {
		return
	}

	snapshot := session.SnapshotViewport(1, 0)

	total := snapshot.TotalLines
	if a.termScroll > 0 && total > a.termTotalLines {
		// New output lands at the bottom. Keep the reader on the same lines
		// instead of letting the viewport follow the tail.
		a.termScroll += total - a.termTotalLines
	}

	a.termVersion = info.Version
	a.termRunning = info.Running
	a.termNote = info.Note
	a.termTotalLines = total
	a.termAppCursor = snapshot.AppCursor
	a.termBracketedPaste = snapshot.BracketedPaste
	a.termScroll = terminalScrollBy(a.termScroll, max(0, total-a.termVisibleRows()), 0)
	a.refresh()
}

// terminalView builds the panel state for one frame.
func (a *App) terminalView() terminalView {
	sessions := []terminal.SessionInfo(nil)
	activeID := 0

	if a.term != nil {
		sessions = a.term.Sessions()
		activeID, _ = a.term.Active()
	}

	hasSnapshot := false

	var snapshot terminal.Snapshot

	if a.termOpen && a.term != nil && activeID != 0 {
		_, session := a.term.Active()
		if session != nil {
			snapshot = session.SnapshotViewport(a.termVisibleRows(), a.termScroll)
			hasSnapshot = true
		}
	}

	view := terminalViewState(
		a.termOpen,
		a.termFocus,
		a.termPanelHeight(),
		activeID,
		sessions,
		snapshot,
		hasSnapshot,
		a.termVisibleRows(),
		a.termScroll,
	)

	shell := a.termShell
	if strings.TrimSpace(shell) == "" {
		shell = a.terminalShell()
	}

	view.Shell = filepath.Base(shell)
	if a.termNote != "" && view.Note == "" {
		view.Note = a.termNote
	}

	return view
}
