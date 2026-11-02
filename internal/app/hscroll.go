package app

import (
	"math"
	"unicode/utf8"
)

const (
	hscrollStep      = 40 // pixels per keyboard step
	hscrollWheelStep = 30 // pixels per wheel tick
)

// scrollHorizontal shifts horizontal scroll offset by dx pixels, clamped between
// 0 and the maximum line width in the active document. In split mode it
// routes to the focused group; use scrollHorizontalFor to target a group.
func (a *App) scrollHorizontal(dx int) {
	a.scrollHorizontalFor(a.focusedScrollGroup(), dx)
}

// focusedScrollGroup is the group keyboard scrolling applies to: the focused
// group in split mode, group 0 otherwise.
func (a *App) focusedScrollGroup() int {
	if a.split && a.mode == "explorer" && a.focusGroup == 1 {
		return 1
	}

	return 0
}

// scrollHorizontalFor shifts one group's horizontal offset by dx pixels.
func (a *App) scrollHorizontalFor(group, dx int) {
	if group != 1 {
		group = 0
	}

	if a.wrap {
		if a.split {
			a.scrollXGrp[group] = 0
		} else {
			a.scrollX = 0
		}

		return
	}

	a.setScrollXFor(group, a.scrollXFor(group)+dx)
}

// scrollXFor returns the horizontal offset one group paints with.
func (a *App) scrollXFor(group int) int {
	if a.split && a.mode == "explorer" && (group == 0 || group == 1) {
		return a.scrollXGrp[group]
	}

	if a.wrap {
		return 0
	}

	return a.scrollX
}

// ScrollHorizontal is the exported form of scrollHorizontal.
func (a *App) ScrollHorizontal(dx int) {
	a.scrollHorizontal(dx)
}

// ScrollX returns the current horizontal scroll offset in pixels.
func (a *App) ScrollX() int {
	if a.wrap {
		return 0
	}

	return a.scrollXFor(a.focusedScrollGroup())
}

// SetScrollX sets the horizontal scroll offset in pixels, clamped to [0, maxLineWidth].
func (a *App) SetScrollX(x int) {
	a.setScrollXFor(a.focusedScrollGroup(), x)
}

// setScrollX sets the horizontal scroll offset in pixels, clamped to [0, maxLineWidth].
func (a *App) setScrollX(x int) {
	a.setScrollXFor(a.focusedScrollGroup(), x)
}

// setScrollXFor sets one group's horizontal offset, clamped to its own
// document width. Single-pane mode keeps the legacy shared scrollX.
func (a *App) setScrollXFor(group, x int) {
	if group != 1 {
		group = 0
	}

	if a.wrap {
		if a.split {
			a.scrollXGrp[group] = 0
		} else {
			a.scrollX = 0
		}

		return
	}

	maxW := a.maxLineWidthFor(group)
	if x < 0 {
		x = 0
	} else if x > maxW {
		x = maxW
	}

	if a.split && a.mode == "explorer" {
		a.scrollXGrp[group] = x
	} else {
		a.scrollX = x
	}

	a.refresh()
}

// maxLineWidth returns the maximum line width in pixels of the active document
// or diff rows.
func (a *App) maxLineWidth() int {
	return a.maxLineWidthFor(a.activeGroup())
}

// maxLineWidthFor returns the maximum line width in pixels of one group's
// document, or diff rows in changes mode.
func (a *App) maxLineWidthFor(group int) int {
	if a.mode == "changes" {
		rows := a.currentDiffRows()
		maxRunes := 0

		for _, row := range rows {
			leftRunes := 0
			for _, seg := range row.Left {
				leftRunes += utf8.RuneCountInString(seg.Text)
			}

			if leftRunes > maxRunes {
				maxRunes = leftRunes
			}

			rightRunes := 0
			for _, seg := range row.Right {
				rightRunes += utf8.RuneCountInString(seg.Text)
			}

			if rightRunes > maxRunes {
				maxRunes = rightRunes
			}

			if row.Hunk != "" {
				if h := utf8.RuneCountInString(row.Hunk); h > maxRunes {
					maxRunes = h
				}
			}
		}

		return int(math.Ceil(float64(maxRunes) * a.zf(diffCellW)))
	}

	doc := a.groupDoc(group)
	if doc == nil {
		return 0
	}

	maxRunes := 0

	for _, line := range doc.Lines {
		if r := utf8.RuneCountInString(line); r > maxRunes {
			maxRunes = r
		}
	}

	return int(math.Ceil(float64(maxRunes) * a.zf(codeCellW)))
}

// MaxLineWidth returns the maximum line width in pixels of the active document.
func (a *App) MaxLineWidth() int {
	return a.maxLineWidth()
}

// isOverEditor reports whether point (x, y) is inside the editor pane.
func (a *App) isOverEditor(x, y float64) bool {
	for _, id := range []string{"code-body", "editor-window", "code-body-2", "editor-window-2"} {
		if box, ok := findBoxID(a.page.Boxes(), id); ok {
			if x >= box.X && x <= box.X+box.W && y >= box.Y && y <= box.Y+box.H {
				return true
			}
		}
	}

	// Fallback when layout boxes are not present (e.g. headless tests).
	w := float64(defaultWidth)
	if a.page != nil {
		if pw, _ := a.page.Size(); pw > 0 {
			w = float64(pw)
		}
	}

	left := float64(a.windowControlLeftPx())

	return x >= left && x <= w
}

// hscrollWindow maps the pixel scrollX to a rune window and a sub-column
// remainder. Unwrapped rows lay out segs[col:col+cols] at -rem px, so the
// laid text always fits the cell and never trips the engine's overflow wrap.
// Click and caret math keeps using the full scrollX and stays unchanged:
// col*cellW+rem always equals scrollX.
func (a *App) hscrollWindow(cellW float64, cols int) (col, rem int) {
	return a.hscrollWindowFor(a.focusedScrollGroup(), cellW, cols)
}

// hscrollWindowFor is hscrollWindow for one editor group.
func (a *App) hscrollWindowFor(group int, cellW float64, cols int) (col, rem int) {
	if a.wrap || cellW <= 0 || cols <= 0 {
		return 0, 0
	}

	x := a.scrollXFor(group)
	col = int(float64(x) / cellW)
	rem = x - int(float64(col)*cellW)

	return col, rem
}
