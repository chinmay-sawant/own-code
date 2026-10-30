package layout

import (
	"sort"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

// autoRowBuiltItem is one item the auto-row fast path built once.
type autoRowBuiltItem struct {
	b         *box
	style     *ResolvedStyle
	cellW     float64
	chromeIdx int
	chromeAt  int
}

// autoRowStretch is one deferred stretch request for an item shorter than its
// row.
type autoRowStretch struct {
	idx                 int
	target              float64
	chromeIdx, chromeAt int
}

// canBuildAutoRowGrid reports whether a multi-column grid can build each item
// once. The rows are auto-sized, each item occupies one cell, and the placed
// order is row-major fill order. Two-pass cases stay on the general path.
func canBuildAutoRowGrid(eng *engine, sty ResolvedStyle, placed []gridCell, cols []float64) bool {
	if len(cols) < 2 || len(placed) == 0 {
		return false
	}

	if strings.TrimSpace(sty.GridTemplateRows) != "" ||
		strings.TrimSpace(sty.GridTemplateAreas) != "" {
		return false
	}

	if autoRows := strings.TrimSpace(sty.GridAutoRows); autoRows != "" && !strings.EqualFold(autoRows, "auto") {
		return false
	}

	for _, page := range placed {
		if !autoRowCellEligible(eng, page, cols, sty.JustifyItems) {
			return false
		}
	}

	return autoRowPlacementSequential(placed)
}

// autoRowCellEligible reports whether one cell and its item keep the one-pass
// shape. Percentage heights, explicit placement, and stretch hazards fall
// back to the two-pass path.
func autoRowCellEligible(eng *engine, page gridCell, cols []float64, containerJustify string) bool {
	if !autoRowCellShape(page, cols) {
		return false
	}

	child := eng.stylePtr(page.n)
	if child == nil || autoRowExplicitPlacement(child) {
		return false
	}

	if autoRowPaintHazard(child) || autoRowHeightHazard(child) ||
		!gridJustifyEligible(child, containerJustify) || !autoRowInnerGridEligible(child) {
		return false
	}

	return !itemHasStretchHazard(eng, page.n)
}

// autoRowCellShape reports whether one placed cell occupies exactly one track
// inside the column list.
func autoRowCellShape(page gridCell, cols []float64) bool {
	return page.rowSpan == 1 && page.colSpan == 1 && page.col >= 0 && page.col < len(cols)
}

// autoRowExplicitPlacement reports whether the item names a grid line or area.
func autoRowExplicitPlacement(child *ResolvedStyle) bool {
	return child.GridRowStart != 0 || child.GridColumnStart != 0 ||
		child.GridRowEnd != 0 || child.GridColumnEnd != 0 || child.GridArea != ""
}

// autoRowPaintHazard reports styles that carry their own compositing or
// out-of-flow placement and cannot be stretched by chrome alone.
func autoRowPaintHazard(child *ResolvedStyle) bool {
	return autoRowOutOfFlowHazard(child) || autoRowCompositingHazard(child) ||
		autoRowLayoutHazard(child) || autoRowClipHazard(child)
}

// autoRowOutOfFlowHazard reports boxes placed outside the normal flow.
func autoRowOutOfFlowHazard(child *ResolvedStyle) bool {
	return child.Position == positionAbsolute || child.Position == positionFixed || child.Position == "sticky"
}

// autoRowCompositingHazard reports boxes that create their own paint group.
func autoRowCompositingHazard(child *ResolvedStyle) bool {
	return child.HasTransform || child.Isolation == unicodeBidiIsolate ||
		nonNormalBlendMode(child.MixBlendMode) != "" || maskGroupRequested(*child)
}

// autoRowLayoutHazard reports boxes whose used height depends on a second
// layout at the stretched size.
func autoRowLayoutHazard(child *ResolvedStyle) bool {
	return child.Display == displayFlex || child.Display == displayInlineFlex ||
		isMulticol(*child) || child.Display == displayTable || child.Display == displaySubgrid ||
		isVerticalWritingMode(child.WritingMode)
}

// autoRowClipHazard reports boxes that clip their own paint. A chrome-only
// stretch cannot re-clip the item's descendants safely.
func autoRowClipHazard(child *ResolvedStyle) bool {
	return clipsPaintAxis(child, child.OverflowX) || clipsPaintAxis(child, child.OverflowY)
}

// autoRowHeightHazard reports percentage heights that depend on a definite
// row height the one-pass path has not resolved yet.
func autoRowHeightHazard(child *ResolvedStyle) bool {
	return child.HeightPercent >= 0 || child.MinHeightPercent >= 0 || child.MaxHeightPercent >= 0
}

// gridJustifyEligible reports whether the item's inline justify leaves the
// item at the cell width.
func gridJustifyEligible(child *ResolvedStyle, containerJustify string) bool {
	justify := gridItemJustify(*child, containerJustify)

	return justify == "" || justify == fxStretch || justify == "auto"
}

// autoRowInnerGridEligible reports whether a nested grid can be built once
// inside the row. A nested explicit row template or align-content needs the
// measure pass.
func autoRowInnerGridEligible(child *ResolvedStyle) bool {
	if child.Display != displayGrid && child.Display != displayInlineGrid {
		return true
	}

	if strings.TrimSpace(child.GridTemplateRows) != "" {
		return false
	}

	align := strings.TrimSpace(child.AlignContent)

	return align == "" || align == "normal" || align == fxStretch
}

// autoRowPlacementSequential reports whether placed items fill rows in order,
// with no gaps and no repeated column.
func autoRowPlacementSequential(placed []gridCell) bool {
	row, col := -1, 0

	for _, page := range placed {
		if page.row != row {
			if row >= 0 && page.row != row+1 {
				return false
			}

			row, col = page.row, 0
		}

		if page.col != col {
			return false
		}

		col++
	}

	return true
}

// itemHasStretchHazard reports descendant styles whose used size depends on a
// definite item height. The chrome-only stretch cannot resolve those without
// laying the subtree out again.
func itemHasStretchHazard(eng *engine, node *html.Node) bool {
	hazard := false

	node.Walk(func(child *html.Node) {
		if hazard || child.Type != html.ElementNode || child == node {
			return
		}

		if styleHasStretchHazard(eng.stylePtr(child)) {
			hazard = true
		}
	})

	return hazard
}

// styleHasStretchHazard reports whether one style needs a real layout at the
// stretched height instead of a chrome-only stretch.
func styleHasStretchHazard(style *ResolvedStyle) bool {
	if style == nil {
		return false
	}

	if style.HeightPercent >= 0 || style.MinHeightPercent >= 0 || style.MaxHeightPercent >= 0 {
		return true
	}

	return (style.Position == positionAbsolute || style.Position == positionFixed) && !style.BottomAuto
}

// layoutAutoRowGrid builds each item once, then stretches or aligns it after
// the row height is known. The single-column fast path stays separate.
func (e *engine) layoutAutoRowGrid(
	boxNode *box, sty ResolvedStyle, placed []gridCell,
	cols []float64, columnGap, rowGap, contentX, contentW, curY, posY, contentH float64,
	contentStart int,
) *box {
	var noAreas gridTemplateAreasMap

	numRows := gridRowCount(placed, noAreas)
	rowItems := autoRowItemsByRow(placed, numRows)
	items := make([]autoRowBuiltItem, len(placed))
	rows := make([]float64, numRows)

	containerJustify := sty.JustifyItems
	if containerJustify == "" {
		containerJustify = fxStretch
	}

	containerAlign := sty.AlignItems
	if containerAlign == "" {
		containerAlign = fxStretch
	}

	rowTop := curY

	for row := range numRows {
		maxH := 0.0

		for _, idx := range rowItems[row] {
			item := e.buildAutoRowItem(&placed[idx], cols, columnGap, contentX, posY, rowTop, containerJustify)
			if item.b == nil {
				continue
			}

			items[idx] = item

			if item.b.height > maxH {
				maxH = item.b.height
			}
		}

		rows[row] = maxH
		e.applyAutoRowStretch(rowItems[row], items, containerAlign, maxH, posY, rowTop)
		rowTop += maxH + rowGap
	}

	e.appendAutoRowBoxes(boxNode, items)

	rowYs := gridRowTops(curY, rows, rowGap, contentH, sty.AlignContent)

	e.shiftAutoRowBoxes(placed, items, rowYs, posY)
	e.paintGridGapRules(sty, placed, contentX, contentW, cols, columnGap, rowYs, rows, posY)

	usedH := autoRowUsedHeight(curY, numRows, rowYs, rows)
	usedH = resolveGridUsedHeight(e, sty, usedH, contentH)
	boxNode.height = usedH
	e.prependChrome(contentStart, boxNode, sty, boxNode.x, posY, boxNode.w, boxNode.height)

	return boxNode
}

// autoRowItemsByRow groups placed indexes by their row.
func autoRowItemsByRow(placed []gridCell, numRows int) [][]int {
	rowItems := make([][]int, numRows)

	for index := range placed {
		rowItems[placed[index].row] = append(rowItems[placed[index].row], index)
	}

	return rowItems
}

// appendAutoRowBoxes appends the built items to the grid box in placement
// order, skipping items that produced no box.
func (e *engine) appendAutoRowBoxes(boxNode *box, items []autoRowBuiltItem) {
	for index := range items {
		if items[index].b != nil {
			boxNode.children = append(boxNode.children, items[index].b)
		}
	}
}

// shiftAutoRowBoxes moves every built item to its final row position.
func (e *engine) shiftAutoRowBoxes(placed []gridCell, items []autoRowBuiltItem, rowYs []float64, posY float64) {
	for index := range placed {
		itemBox := items[index].b
		if itemBox == nil {
			continue
		}

		deltaY := posY + rowYs[placed[index].row] - itemBox.y
		e.shiftBoxOps(itemBox, 0, deltaY)
		itemBox.y += deltaY
	}
}

// autoRowUsedHeight returns the content height the rows occupy, or the start
// position when the grid holds no rows.
func autoRowUsedHeight(curY float64, numRows int, rowYs, rows []float64) float64 {
	if numRows == 0 {
		return curY
	}

	return rowYs[numRows-1] + rows[numRows-1]
}

// buildAutoRowItem builds one item at its cell start and records the deferred
// chrome that a later stretch must replace. It returns a zero item when the
// build produced no box.
func (e *engine) buildAutoRowItem(
	page *gridCell, cols []float64, columnGap, contentX, posY, rowTop float64, containerJustify string,
) autoRowBuiltItem {
	cellW, cellX := gridCellExtent(cols, columnGap, contentX, *page)
	child := e.stylePtr(page.n)
	justify := gridItemJustify(*child, containerJustify)
	buildW := gridItemBuildWidth(e, page.n, *child, justify, cellW)

	oldMax := e.imgMaxW

	if cellW > 0 {
		e.imgMaxW = cellW
	}

	built := e.build(page.n, buildW, cellX, posY+rowTop)
	e.imgMaxW = oldMax

	if built == nil {
		var empty autoRowBuiltItem

		return empty
	}

	deltaX := cellX + gridAlignOffset(justify, cellW, built.w) - built.x
	e.shiftBoxOps(built, deltaX, 0)
	built.x += deltaX

	chromeIdx, chromeAt := -1, -1

	for i := len(e.deferredChrome) - 1; i >= 0; i-- {
		if e.deferredChrome[i].b == built {
			chromeIdx, chromeAt = i, e.deferredChrome[i].at

			break
		}
	}

	return autoRowBuiltItem{
		b: built, style: child, cellW: cellW, chromeIdx: chromeIdx, chromeAt: chromeAt,
	}
}

// applyAutoRowStretch stretches the items shorter than the row or aligns them
// when their align value does not stretch. Deferred chrome entries are removed
// in reverse index order before the replacement chrome is prepended.
func (e *engine) applyAutoRowStretch(
	rowItems []int, items []autoRowBuiltItem, containerAlign string, maxH, posY, rowTop float64,
) {
	stretches := make([]autoRowStretch, 0, len(rowItems))

	for _, idx := range rowItems {
		item := &items[idx]
		if item.b == nil {
			continue
		}

		align := gridItemAlign(*item.style, containerAlign)
		buildH := gridStretchBuildHeight(align, maxH, *item.style)

		if buildH > 0 {
			target := buildH
			if item.style.MaxHeight >= 0 {
				if maxHeight := e.scalePt(item.style.MaxHeight); maxHeight < target {
					target = maxHeight
				}
			}

			if target > item.b.height {
				stretches = append(stretches, autoRowStretch{
					idx: idx, target: target, chromeIdx: item.chromeIdx, chromeAt: item.chromeAt,
				})
			}

			continue
		}

		deltaY := posY + rowTop + gridAlignOffset(align, maxH, item.b.height) - item.b.y
		e.shiftBoxOps(item.b, 0, deltaY)
		item.b.y += deltaY
	}

	sort.Slice(stretches, func(i, j int) bool { return stretches[i].chromeIdx > stretches[j].chromeIdx })

	for _, stretch := range stretches {
		if stretch.chromeIdx < 0 {
			continue
		}

		idx := stretch.chromeIdx
		e.deferredChrome = append(e.deferredChrome[:idx], e.deferredChrome[idx+1:]...)
	}

	for _, stretch := range stretches {
		item := &items[stretch.idx]
		e.stretchGridItemToHeight(item.b, *item.style, stretch.target, stretch.chromeAt)
	}
}

// stretchGridItemToHeight extends an item's border box and regenerates its
// deferred background and border at the new height. The subtree stays put.
func (e *engine) stretchGridItemToHeight(itemBox *box, style ResolvedStyle, height float64, chromeAt int) {
	if height <= itemBox.height {
		return
	}

	itemBox.height = height

	if e.noEmit || chromeAt < 0 {
		return
	}

	scope := e.pushZ(style, itemBox.style)
	e.prependChrome(chromeAt, itemBox, style, itemBox.x, itemBox.y, itemBox.w, height)
	e.popZ(scope, nil)
}
