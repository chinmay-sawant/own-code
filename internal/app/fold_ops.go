package app

import (
	"context"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// Fold commands. They act on the active document at the caret line (edit
// mode) or the top visible line, and keep the caret out of hidden lines.

// Palette command ids for the fold actions.
const (
	paletteCmdFold      = "fold"
	paletteCmdUnfold    = "unfold"
	paletteCmdFoldAll   = "fold-all"
	paletteCmdUnfoldAll = "unfold-all"
)

// foldCatalog lists the palette rows for folding.
func foldCatalog() []paletteRow {
	return []paletteRow{
		{Cmd: paletteCmdFold, Name: "Fold: Fold Region", Dir: "Collapse the region at the caret (Ctrl+Shift+[)"},
		{Cmd: paletteCmdUnfold, Name: "Fold: Unfold Region", Dir: "Expand the region at the caret (Ctrl+Shift+])"},
		{Cmd: paletteCmdFoldAll, Name: "Fold: Fold All", Dir: "Collapse every top-level region in the file"},
		{Cmd: paletteCmdUnfoldAll, Name: "Fold: Unfold All", Dir: "Expand every folded region in the file"},
	}
}

// foldTarget is the document and source line a fold command applies to.
func (a *App) foldTarget() (*fileDoc, int) {
	doc := a.activeDoc()
	if doc == nil || doc.Err != "" || doc.Binary || doc.Image != nil || len(doc.Lines) == 0 {
		return nil, 0
	}

	if a.editing && a.edit != nil && a.edit.Path == doc.Path && a.activeGroup() == 0 {
		line, _ := offsetLineCol(a.edit.Text(), a.editCaret)

		return doc, line
	}

	return doc, a.topSourceLine()
}

// runFoldCommand applies one fold command and repaints.
func (a *App) runFoldCommand(cmd string) bool {
	doc, line := a.foldTarget()
	if doc == nil {
		return false
	}

	var changed bool

	switch cmd {
	case paletteCmdFold:
		changed = doc.foldAround(line, true)
	case paletteCmdUnfold:
		changed = doc.foldAround(line, false)
	case paletteCmdFoldAll:
		changed = doc.foldAll()
	case paletteCmdUnfoldAll:
		changed = doc.unfoldAll()
	}

	if changed {
		a.afterFoldChange(doc)
	}

	return changed
}

// toggleFoldLine flips the fold that opens on one line (gutter chevron).
func (a *App) toggleFoldLine(group, line int) {
	doc := a.groupDoc(group)
	if doc == nil || !doc.toggleFold(line) {
		return
	}

	a.afterFoldChange(doc)
}

// afterFoldChange moves the caret out of hidden lines and repaints.
func (a *App) afterFoldChange(doc *fileDoc) {
	a.moveCaretOutOfFold(doc)
	a.viewportCols = 0
	a.scrollTo(a.scrollY)
	a.refresh()
}

// moveCaretOutOfFold relocates the caret (and selection anchor) to the end
// of the fold's first line when a fold now hides it.
func (a *App) moveCaretOutOfFold(doc *fileDoc) {
	if doc == nil || !a.editing || a.edit == nil || a.edit.Path != doc.Path {
		return
	}

	text := a.edit.Text()
	move := func(off int) int {
		line, _ := offsetLineCol(text, off)

		start := doc.foldStartFor(line)
		if start < 0 {
			return off
		}

		col := len([]rune(lineTextAt(text, start)))

		return edit.OffsetAtPoint(edit.SelectionLines(text), start, col)
	}

	moved := move(a.editCaret)
	if moved != a.editCaret {
		a.editCaret = moved
		a.editAnchor = moved

		return
	}

	a.editAnchor = move(a.editAnchor)
}

// foldKey handles the fold chords: Ctrl+Shift+[ folds, Ctrl+Shift+] unfolds.
func (a *App) foldKey(ctx context.Context, key string) (bool, error) {
	if !a.ctrl || !a.shift {
		return false, nil
	}

	switch normalizeKeyName(key) {
	case "[", "{", "bracketleft":
		a.runFoldCommand(paletteCmdFold)
	case "]", "}", "bracketright":
		a.runFoldCommand(paletteCmdUnfold)
	default:
		return false, nil
	}

	return true, a.page.Redraw(ctx)
}
