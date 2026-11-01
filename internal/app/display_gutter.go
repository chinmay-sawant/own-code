package app

// Gutter visibility and relative line numbers. The number column stays in
// the layout when hidden (v1): the mark column and the 44 px number box keep
// their widths and only the number text hides, so the wrap budget in
// codeColsFor and the edit-surface hit offsets never fork. Preview (.pln)
// and diff (.dnum) gutters are separate surfaces and are unaffected.

// uiShowLineNumbers is the effective gutter flag. It defaults to true: a
// fresh app and an old session without the field both show numbers.
func (a *App) uiShowLineNumbers() bool {
	if !a.lineNumbersSet {
		return true
	}

	return a.showLineNumbers
}

// decorateLineNumbers fills the display number of every visible row.
// caretLine is the zero-based caret source line, or negative when the caret
// is not in this document (then every row keeps its absolute number).
// Continuation rows (No == 0) always stay blank. It runs over wrapped and
// unwrapped rows in both editor groups.
func decorateLineNumbers(rows []codeRow, caretLine int, relative bool) {
	for i := range rows {
		row := &rows[i]
		row.RelNo = 0

		if row.No <= 0 {
			continue
		}

		if !relative || caretLine < 0 {
			row.RelNo = row.No

			continue
		}

		if row.Line == caretLine {
			row.RelNo = row.No
		} else {
			if d := row.No - (caretLine + 1); d < 0 {
				row.RelNo = -d
			} else {
				row.RelNo = d
			}
		}
	}
}

// caretLineFor gutter-relative numbers: the caret source line when the live
// buffer is on this document, or -1 so rows keep absolute numbers.
func (a *App) caretLineFor(doc *fileDoc, group int) int {
	if doc == nil || !a.editing || a.edit == nil || a.edit.Path != doc.Path {
		return -1
	}

	line, _ := offsetLineCol(a.edit.Text(), a.editCaret)
	_ = group

	return line
}

// toggleLineNumbers flips the gutter and persists the session.
func (a *App) toggleLineNumbers() {
	if !a.lineNumbersSet {
		a.showLineNumbers = false
		a.lineNumbersSet = true
	} else {
		a.showLineNumbers = !a.showLineNumbers
	}

	a.refresh()
}

// toggleRelativeNumbers flips relative numbers. It only applies while the
// gutter is shown; the template gates the switch the same way.
func (a *App) toggleRelativeNumbers() {
	if !a.uiShowLineNumbers() {
		return
	}

	a.relativeNumbers = !a.relativeNumbers
	a.refresh()
}
