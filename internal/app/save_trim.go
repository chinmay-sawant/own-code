package app

import "github.com/chinmay-sawant/own-code/internal/edit"

// trimEditForSave strips trailing whitespace from the edit buffer before a
// save, per the effective trim mode (the Settings choice, else the
// .editorconfig trim_trailing_whitespace key). The buffer always serializes
// with one final newline, so insert_final_newline=true is already the
// behavior. The caret keeps its line and column, clamped to the new line.
func (a *App) trimEditForSave(path string) {
	if a.edit == nil {
		return
	}

	mode := a.effectiveTrimMode(path)
	if mode == "" || trimSkippable(a.docs[path], len(a.edit.Text())) {
		return
	}

	next, changed := applyTrimForSave(mode, trimSavedLines(a.edit.Original), a.edit.Lines)
	if changed == 0 {
		return
	}

	text := a.edit.Text()
	aLine, aCol := offsetLineCol(text, a.editAnchor)
	cLine, cCol := offsetLineCol(text, a.editCaret)

	a.edit.SetText(edit.JoinLines(next))
	a.editAnchor = clampedLineOffset(next, aLine, aCol)
	a.editCaret = clampedLineOffset(next, cLine, cCol)
	a.commitEditBuffer()
}

// clampedLineOffset is lineOffset with the line and column held inside lines.
func clampedLineOffset(lines []string, line, col int) int {
	if len(lines) == 0 {
		return 0
	}

	line = min(line, len(lines)-1)
	col = min(col, len([]rune(lines[line])))

	return lineOffset(lines, line, col)
}
