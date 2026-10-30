package layout

import "math"

// Widows control for the multicol whole-box distribution loop
// (placeMulticolLine in multicol.go).
//
// Whole child boxes are assigned to columns without splitting. Each box is
// treated as one or more lines estimated from its measured height over its
// used line height, so a column break can keep at least the widows minimum
// lines together at the top of the next column. Reference: Chrome
// 143.0.7499.40 keeps the widows minimum lines together at a column break.

const (
	// multicolFragmentInitial is the CSS initial for orphans and widows.
	multicolFragmentInitial = 2
	// multicolHeightEpsilon clusters height comparisons against float noise.
	multicolHeightEpsilon = 1e-6
)

// multicolWidowsValue returns the used widows minimum for a multicol
// container style, falling back to the CSS initial.
func multicolWidowsValue(style ResolvedStyle) int {
	if style.Widows < 1 {
		return multicolFragmentInitial
	}

	return style.Widows
}

// multicolOrphansValue returns the used orphans minimum for a multicol
// container style, falling back to the CSS initial. It only guards the
// widows shift so an earlier break does not strand too few lines at the
// bottom of the current column.
func multicolOrphansValue(style ResolvedStyle) int {
	if style.Orphans < 1 {
		return multicolFragmentInitial
	}

	return style.Orphans
}

// multicolWidowsLineCount estimates the line count of one multicol item as
// round(height / line-height), at least one line for a non-empty box. Empty
// boxes hold no lines.
func multicolWidowsLineCount(eng *engine, item multicolItem) int {
	if item.h <= 0 {
		return 0
	}

	itemStyle := eng.stylePtr(item.n)

	var lineH float64
	if itemStyle != nil {
		lineH = eng.lineHeightOf(itemStyle) * eng.scale
	}

	if lineH <= 0 {
		return 1
	}

	count := int(math.Round(item.h / lineH))
	if count < 1 {
		return 1
	}

	return count
}

// multicolWidowsSuffix returns per-item line counts plus suffix sums for
// lines and heights. suffixLines[itemIdx] holds lines from itemIdx to the
// end, suffixLines[len] is 0; suffixHeights matches with measured heights.
func multicolWidowsSuffix(eng *engine, items []multicolItem) ([]int, []int, []float64) {
	counts := make([]int, len(items))
	suffixLines := make([]int, len(items)+1)
	suffixHeights := make([]float64, len(items)+1)

	for idx, item := range items {
		counts[idx] = multicolWidowsLineCount(eng, item)
	}

	for idx := len(items) - 1; idx >= 0; idx-- {
		suffixLines[idx] = suffixLines[idx+1] + counts[idx]
		suffixHeights[idx] = suffixHeights[idx+1] + items[idx].h
	}

	return counts, suffixLines, suffixHeights
}

// multicolWidowsAdvancePossible checks the structural guards for an early
// widows break: a real next column exists, the current column is non-empty,
// and the item is not the last one. A widows value of 1 never needs a move.
func multicolWidowsAdvancePossible(col, nCols int, colLines []int, itemIdx, nItems, widows int) bool {
	if widows <= 1 {
		return false
	}

	if col < 0 || col >= nCols-1 {
		return false
	}

	if col >= len(colLines) {
		return false
	}

	if itemIdx < 0 || itemIdx >= nItems || itemIdx == nItems-1 {
		return false
	}

	return colLines[col] > 0
}

// multicolWidowsShouldAdvance reports whether the item at itemIdx must start
// the next column to keep the widows minimum together there. The break moves
// earlier: when keeping the item in the current column would leave fewer
// than widows lines for the remaining columns while moving it there
// satisfies the minimum, advance now. In auto fill it only fires when the
// remaining items cannot all fit the current column, so an all-fit line never
// gains a spurious split. It holds back when the current column would drop
// below the orphans minimum.
func multicolWidowsShouldAdvance(
	col, nCols int,
	colLines []int,
	suffixLines []int,
	suffixHeights []float64,
	colHeights []float64,
	itemIdx, nItems, widows, orphans int,
	maxColH float64,
	balance bool,
) bool {
	if !multicolWidowsAdvancePossible(col, nCols, colLines, itemIdx, nItems, widows) {
		return false
	}

	if itemIdx+1 >= len(suffixLines) || itemIdx >= len(suffixHeights) || col >= len(colHeights) {
		return false
	}

	if suffixLines[itemIdx] < widows || suffixLines[itemIdx+1] >= widows {
		return false
	}

	if colLines[col] < orphans {
		return false
	}

	if !balance && colHeights[col]+suffixHeights[itemIdx] <= maxColH+multicolHeightEpsilon {
		return false
	}

	return true
}
