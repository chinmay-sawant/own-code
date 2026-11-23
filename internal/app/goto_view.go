package app

import (
	"strings"
	"unicode/utf8"
)

// Go to definition from the read-only token view. There is no caret outside
// edit mode, so Ctrl+D resolves the identifier under the mouse pointer using
// the same geometry as click-to-caret and hover.

// viewDefinitionTarget resolves the symbol under the pointer in the token
// view. line and col are 0-based raw rune coordinates in the document line.
func (a *App) viewDefinitionTarget() (word string, line, col int, ok bool) {
	doc := a.activeDoc()
	if doc == nil || doc.Err != "" || doc.Binary || doc.Image != nil {
		return "", 0, 0, false
	}

	if a.viewPointerHook != nil {
		line, col, ok = a.viewPointerHook()
	} else {
		line, col, ok = a.pointerLineCol()
	}

	if !ok {
		return "", 0, 0, false
	}

	word, ok = viewSymbolAt(doc, line, col)

	return word, line, col, ok
}

// viewSymbolAt returns the identifier covering a raw rune column of a
// document line, preferring the highlighter's definition-like token.
func viewSymbolAt(doc *fileDoc, line, col int) (string, bool) {
	if doc == nil || line < 0 || line >= len(doc.Lines) {
		return "", false
	}

	raw := strings.TrimSuffix(doc.Lines[line], "\r")

	if name, ok := gotoTokenSymbolAt(doc, raw, doc.Lines, doc.Tokens, line, col); ok {
		return name, true
	}

	runes := []rune(raw)
	if col < 0 || col >= len(runes) || !isIdentRune(runes[col]) {
		return "", false
	}

	if word := gotoDefinitionWordAt(raw, col); word != "" {
		return word, true
	}

	return "", false
}

// pointerLineCol maps the window pointer to a document line and raw column
// in the code body of either editor group.
func (a *App) pointerLineCol() (int, int, bool) {
	if a.page == nil {
		return 0, 0, false
	}

	x, y := a.page.Window().CursorPosition()

	for group := 0; group < 2; group++ {
		if group == 1 && !a.split {
			break
		}

		id := "code-body"
		if group == 1 {
			id = "code-body-2"
		}

		box, ok := findBoxID(a.page.Boxes(), id)
		if !ok || float64(x) < box.X || float64(x) > box.X+box.W || float64(y) < box.Y || float64(y) > box.Y+box.H {
			continue
		}

		return a.pointerLineColIn(group, box.X, box.Y, float64(x), float64(y))
	}

	return 0, 0, false
}

func (a *App) pointerLineColIn(group int, boxX, boxY, x, y float64) (int, int, bool) {
	doc := a.groupDoc(group)
	if doc == nil || (a.preview && doc.Lang == langMarkdown && a.activeGroup() == group) {
		return 0, 0, false
	}

	sticky := a.stickyTopPx(group)
	if y-boxY < float64(sticky) {
		return 0, 0, false
	}

	scrollY := a.hoverScrollY(group)
	cols := a.codeColsForGroup(group)
	visual := visualRowAt(int(y)-sticky, int(boxY), scrollY, a.zx(rowH))
	line, rowStart := visual, 0

	if a.wrap {
		plan := doc.wrapped(cols)
		if visual < 0 || visual >= len(plan.Rows) {
			return 0, 0, false
		}

		line, rowStart = sourceLineAt(doc, cols, visual), plan.Rows[visual].Start
	}

	if line < 0 || line >= len(doc.Lines) {
		return 0, 0, false
	}

	textLeft := boxX + float64(a.zx(editTextLeft))
	if a.blameShows(doc) {
		textLeft += float64(a.zx(blameGutterW))
	}

	scrollX := 0
	if !a.wrap {
		scrollX = a.scrollXFor(group)
	}

	if x < textLeft {
		return 0, 0, false
	}

	raw := strings.TrimSuffix(doc.Lines[line], "\r")
	displayCol := rowStart + int((x-textLeft+float64(scrollX))/a.zf(codeCellW))

	if displayCol >= utf8.RuneCountInString(expandTabs(raw)) {
		return 0, 0, false
	}

	return line, rawColumnAt(raw, displayCol), true
}
