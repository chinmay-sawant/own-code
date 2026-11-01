package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// paletteModeFiles is the Ctrl+P filename overlay; paletteModeSymbols is
// the Ctrl+Shift+O file-scoped symbol overlay; paletteModeRecent is the
// MRU overlay; paletteModeGoto is the Ctrl+G line-jump overlay. The modes
// share one input box and one palette overlay.
const (
	paletteModeFiles   = "files"
	paletteModeSymbols = "symbols"
	paletteModeRecent  = "recent"
	paletteModeGoto    = "goto"
)

// parseGoto parses a go-to query into a 1-based line and column. It accepts
// ":42", "42:7", "42", and ":42:7"; the column defaults to 1. Results clamp
// to [1,maxLine] x [1,maxCol] (floors of 1 apply when the file is empty).
// req is the requested line for the clamp toast; clamped reports whether
// either coordinate moved; ok is false when the query holds no digits.
func parseGoto(query string, maxLine, maxCol int) (line, col, req int, clamped, ok bool) {
	if maxLine < 1 {
		maxLine = 1
	}

	if maxCol < 1 {
		maxCol = 1
	}

	text := strings.TrimSpace(query)
	text = strings.TrimPrefix(text, ":")

	parts := strings.SplitN(text, ":", 2)
	if !isGotoNum(strings.TrimSpace(parts[0])) {
		return 0, 0, 0, false, false
	}

	req = atoiGoto(strings.TrimSpace(parts[0]))
	line = req
	col = 1

	if len(parts) == 2 {
		if c := strings.TrimSpace(parts[1]); c != "" {
			if !isGotoNum(c) {
				return 0, 0, 0, false, false
			}

			col = atoiGoto(c)
		}
	}

	if line < 1 {
		line, clamped = 1, true
	}

	if col < 1 {
		col, clamped = 1, true
	}

	if line > maxLine {
		line, clamped = maxLine, true
	}

	if col > maxCol {
		col, clamped = maxCol, true
	}

	return line, col, req, clamped, true
}

// isGotoNum reports whether s is a non-empty run of ASCII digits.
func isGotoNum(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

// atoiGoto parses a digit-only string.
func atoiGoto(s string) int {
	n := 0

	for _, r := range s {
		n = n*10 + int(r-'0')
	}

	return n
}

// gotoLine jumps to a 1-based line and column in the active file. It exits
// Markdown preview so the source caret is visible, enters edit mode when the
// buffer is closed, and otherwise moves the edit caret without a blanket
// scroll reset: only the caret's row is revealed.
func (a *App) gotoLine(ctx context.Context, line, col int) error {
	doc := a.activeDoc()
	if doc == nil || doc.Err != "" || doc.Binary {
		a.showToast("info", "Go to line", "Open a text file before jumping to a line.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.preview {
		a.preview = false
		a.showToast("info", "Preview closed", "Showing the source for the jump.")
	}

	var lines []string
	if a.editing && a.edit != nil && a.edit.Path == doc.Path {
		for _, sl := range edit.SelectionLines(a.edit.Text()) {
			lines = append(lines, sl.Text)
		}
	} else {
		lines = append([]string(nil), doc.Lines...)
	}

	if len(lines) == 0 {
		lines = []string{""}
	}

	if line < 1 {
		line = 1
	}

	if line > len(lines) {
		line = len(lines)
	}

	width := len([]rune(lines[line-1])) + 1
	if col < 1 {
		col = 1
	}

	if col > width {
		col = width
	}

	if a.editing && a.edit != nil && a.edit.Path == doc.Path {
		a.editCaret = edit.OffsetAtPoint(edit.SelectionLines(a.edit.Text()), line-1, col-1)
		a.editAnchor = a.editCaret
		a.clearBracket()
		a.ensureEditCaretVisible()
		a.refresh()

		return a.page.Redraw(ctx)
	}

	a.beginEditAt(line-1, displayColumn(strings.Join(doc.Lines, "\n"), line-1, col-1))
	a.refresh()

	return a.page.Redraw(ctx)
}

// openGotoPalette shows the palette overlay in go-to mode, prefilled with
// ":", from anywhere except settings, confirm, and the terminal.
func (a *App) openGotoPalette(ctx context.Context) error {
	if a.settingsOpen || a.confirmOpen || a.terminalOwnsKeys() {
		return nil
	}

	if a.activeDoc() == nil {
		a.showToast("info", "Go to line", "Open a text file before jumping to a line.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	// A repeat chord keeps the typed query; a fresh open prefills ":".
	fresh := !a.paletteOpen || a.paletteMode != paletteModeGoto

	a.paletteOpen = true
	a.paletteMode = paletteModeGoto
	a.paletteIndex = 0

	if fresh {
		a.paletteQuery = ":"
	}

	a.refresh()

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	return a.page.Form().Focus(ctx, "palette")
}

// submitGoto parses the palette query and jumps. A bad query keeps the
// overlay open with a hint; a clamped jump lands and toasts Line req/max.
func (a *App) submitGoto(ctx context.Context) error {
	doc := a.activeDoc()

	maxLine := 1
	if doc != nil {
		if a.editing && a.edit != nil && a.edit.Path == doc.Path {
			maxLine = len(edit.SelectionLines(a.edit.Text()))
		} else {
			maxLine = len(doc.Lines)
		}
	}

	if maxLine < 1 {
		maxLine = 1
	}

	line, col, req, clamped, ok := parseGoto(a.paletteQuery, maxLine, 1<<30)
	if !ok {
		a.showToast("info", "Go to line", "Type :<line>[:<col>], for example :42 or :42:7.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	// Clamp the column against the target line after the line is known.
	text := ""
	if a.editing && a.edit != nil && doc != nil && a.edit.Path == doc.Path {
		text = a.edit.Text()
	} else if doc != nil {
		text = strings.Join(doc.Lines, "\n")
	}

	raw := []rune(lineTextAt(text, line-1))
	if col > len(raw)+1 {
		col, clamped = len(raw)+1, true
	}

	if err := a.closePalette(ctx); err != nil {
		return err
	}

	if err := a.gotoLine(ctx, line, col); err != nil {
		return err
	}

	if clamped {
		a.showToast("info", "Go to line", fmt.Sprintf("Line %d/%d.", req, maxLine))
		a.refresh()

		return a.page.Redraw(ctx)
	}

	return nil
}

// isGotoQuery reports whether a files-mode query is really a ":line" jump.
func isGotoQuery(query string) bool {
	text := strings.TrimSpace(query)
	if !strings.HasPrefix(text, ":") {
		return false
	}

	rest := strings.TrimSpace(strings.TrimPrefix(text, ":"))
	if rest == "" {
		return false
	}

	parts := strings.SplitN(rest, ":", 2)

	return isGotoNum(strings.TrimSpace(parts[0]))
}

// gotoOverlayCapturesKeys reports whether keystrokes belong to the goto or
// symbol palette input instead of the edit buffer. Both overlays open while
// editing; the binder owns the field text, so buffer handlers stay out.
func (a *App) gotoOverlayCapturesKeys() bool {
	return a.paletteOpen && (a.paletteMode == paletteModeGoto || a.paletteMode == paletteModeSymbols)
}
