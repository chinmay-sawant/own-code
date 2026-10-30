package layout

// Page-break keyword shared by style resolution. The keyword still lands on
// the box so later passes can read it.
const (
	pageBreakAvoid  = "avoid"
	pageBreakAlways = "always"
)

// layoutCoordEpsilon is the coordinate tolerance for same-band tests.
const layoutCoordEpsilon = 0.01

func opRadii(op *Op) [4]float64 {
	if op.RadiusTopLeft == 0 && op.RadiusTopRight == 0 && op.RadiusBottomRight == 0 && op.RadiusBottomLeft == 0 {
		return [4]float64{op.Radius, op.Radius, op.Radius, op.Radius}
	}

	return [4]float64{op.RadiusTopLeft, op.RadiusTopRight, op.RadiusBottomRight, op.RadiusBottomLeft}
}

func isDashedOrDottedStyle(style string) bool {
	return style == borderStyleDashed || style == borderStyleDotted
}

// Dash-segment length heuristic tuning: a dash segment is at most
// dashSegWidthFactor stroke widths long, floored at dashSegMinLen points, and
// gets dashSegSlack points of allowance.
const (
	dashSegWidthFactor = 3
	dashSegMinLen      = 0.5
	dashSegSlack       = 0.5
)

func looksLikeDashSegmentLength(segLen, strokeWidth float64) bool {
	if segLen <= 0 {
		return false
	}

	maxSeg := strokeWidth * dashSegWidthFactor
	if maxSeg < dashSegMinLen {
		maxSeg = dashSegMinLen
	}

	maxSeg += dashSegSlack
	if strokeWidth <= 0 {
		maxSeg = dashSegWidthFactor + dashSegSlack
	}

	return segLen <= maxSeg
}
