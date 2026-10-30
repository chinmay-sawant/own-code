package layout

import "errors"

const (
	maxGridRows   = 1 << 16
	maxGridCells  = 1 << 20
	maxGridChecks = 1 << 22
)

var errGridExtentExceeded = errors.New("layout: grid placement exceeds row or cell budget")

// gridExtentWithinBudget runs before placement, whose occupancy checks visit
// every cell in a span. Negative starts represent automatic placement.
func gridExtentWithinBudget(row, col, rowSpan, colSpan, remaining int) bool {
	if rowSpan < 1 || rowSpan > maxGridRows || colSpan < 1 {
		return false
	}

	if colSpan > remaining/rowSpan {
		return false
	}

	if row >= 0 && row > maxGridRows-rowSpan {
		return false
	}

	return col < 0 || col <= maxGridCells-colSpan
}
