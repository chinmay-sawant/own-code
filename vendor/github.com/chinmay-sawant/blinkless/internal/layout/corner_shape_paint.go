package layout

import (
	"math"
	"slices"
	"strings"
)

// Corner-shape paint (phase 2 of plans/v0.0.3/corner-shape/corner-shape-checklist.md).
//
// K is the superellipse parameter per corner: round is 1, squircle is 2,
// square is +infinity, bevel is 0, scoop is -1, notch is -infinity.
// For K >= 0 the corner follows |u-1|^p + |v-1|^p = 1 with p = |K|+1 in the
// normalized corner box (p = 1 bevel line, p = 2 quarter ellipse, p = inf
// sharp outer corner). For K < 0 the same curve is reflected across the
// bevel line u+v = 1, so square reflects to notch and round reflects to a
// concave scoop. K = 0 is the fixed point (bevel). This keeps
// superellipse(1) == round, superellipse(0) == bevel, and
// superellipse(-1) == scoop by construction.
//
// Every curve here is flattened to line segments and emitted as an OpPath
// command list on opExtra (corner_path.go). Straight edges between corners
// stay straight; only the corner boxes carry sampled points.
//
// prependChrome calls cornerShapePaint with the used border radii chosen
// there. The returned fill ops slot where the radiused OpFillRect would go,
// and the border ops where the OpStrokeRect border would go; a false handled
// flag keeps the caller's existing emission for that layer.
//
// Limits of this first cut, all intentional per the phase 2 ledger:
//   - Differing side widths, colors, or styles stay on the existing
//     stroke-rect path (borderHandled stays false, caller keeps its ops).
//   - Dashed/dotted uniform borders paint as a SOLID path. Dash segments
//     never run through the new curve in this wave.
//   - border-image stays on the image painter and ignores corner shape.
//   - Overflow clip and background-clip stay on their existing boxes
//     (layout_chrome.go:468-496) and never follow a scooped outer path.
//     The fill path is rebuilt on the same background-clip box the
//     radiused fill uses.
//   - box-shadow keeps today's radii and is not handled here.

const (
	// cornerShapePaintSegments is the number of line segments one finite,
	// non-zero K corner is flattened into. Bevel, square, and notch ignore
	// it (1-2 straight segments).
	cornerShapePaintSegments = 8
	// cornerShapePaintEdgePoints is the number of straight edge endpoints
	// interleaved with the four corners in the traced outline.
	cornerShapePaintEdgePoints = 8
	// cornerShapePaintPointCap bounds the outline capacity hint: four
	// sampled corners plus the straight edge endpoints.
	cornerShapePaintPointCap = 4*cornerShapePaintSegments + cornerShapePaintEdgePoints
	// cornerShapeQuarterTurn is the angle span of one corner curve.
	cornerShapeQuarterTurn = math.Pi / 2
	// cornerShapeCircleExponent is the numerator of the sampled exponent
	// 2/p of the superellipse parametrization (p = 2 is the ellipse).
	cornerShapeCircleExponent = 2
)

// cornerShapePaintK reports the four corner K values in top-left,
// top-right, bottom-right, bottom-left order from the canonical store in
// style_corner_shape.go.
func cornerShapePaintK(sty ResolvedStyle) [4]float64 {
	return cornerShapeValues(&sty)
}

// cornerShapePaintIsRound reports whether every corner is round (K == 1).
// NaN reads as round so an unknown corner never diverts paint to a path.
func cornerShapePaintIsRound(shapeK [4]float64) bool {
	for _, kValue := range shapeK {
		if math.IsNaN(kValue) {
			continue
		}

		if kValue != CornerShapeRoundK {
			return false
		}
	}

	return true
}

// cornerShapePaint builds OpPath chrome for a non-round box.
//
// radii and radiiY are the used border radii from usedBorderRadiiXY
// (border_radius.go:189): each corner box is radii[i] by radiiY[i].
// shapeK holds the four corner K values in TL, TR, BR, BL order.
//
// It returns fillOps and borderOps plus two handled flags. fillHandled
// tells the caller to skip its radiused OpFillRect and append fillOps;
// borderHandled tells it to skip its stroke-rect border and append
// borderOps. A false flag means "keep existing emission" for that layer.
// When every corner is round it returns no ops and no handled flags: zero
// OpPath, today's OpFillRect and OpStrokeRect stay exactly as they are.
func cornerShapePaint(
	eng *engine, boxNode *box, sty ResolvedStyle,
	posX, posY, width, height float64,
	radii, radiiY [4]float64, shapeK [4]float64,
) ([]Op, []Op, bool, bool) {
	if eng == nil || cornerShapePaintIsRound(shapeK) {
		return nil, nil, false, false
	}

	if !(width > 0 && height > 0) {
		return nil, nil, false, false
	}

	cornerFillOps, cornerFillHandled := cornerShapePaintFill(
		eng, boxNode, sty, posX, posY, width, height, radii, radiiY, shapeK)

	cornerBorderOps, cornerBorderHandled := cornerShapePaintBorder(
		eng, sty, posX, posY, width, height, radii, radiiY, shapeK)

	return cornerFillOps, cornerBorderOps, cornerFillHandled, cornerBorderHandled
}

// cornerShapePaintFill emits the shaped background fill, replacing the
// radiused OpFillRect. fillHandled is true when the shaped box owns the
// fill layer even if the painted op list is empty.
func cornerShapePaintFill(
	eng *engine, boxNode *box, sty ResolvedStyle,
	posX, posY, width, height float64, radii, radiiY [4]float64, shapeK [4]float64,
) ([]Op, bool) {
	if sty.BGColor[3] > 0 && eng.backgroundPaintEnabled(&sty) {
		// Claimed even when the clipped box is empty: the default
		// radiused fill must not reappear for a shaped box.
		return cornerShapePaintColorFill(eng, sty, posX, posY, width, height, radii, radiiY, shapeK), true
	}

	if isColorSchemeCanvasRoot(boxNode) {
		fill, ok := canvasFillForRoot(&sty)
		if !ok {
			return nil, false
		}

		fillOp, painted := cornerShapePaintFillOp(
			fill[0], fill[1], fill[2], fill[3], posX, posY, width, height, radii, radiiY, shapeK)
		if !painted {
			return nil, false
		}

		fillOp.setCanvasBackground()

		return []Op{fillOp}, true
	}

	return nil, false
}

// cornerShapePaintColorFill builds the background-color fill path on the
// same background-clip box the radiused fill uses, or nil when that box is
// empty.
func cornerShapePaintColorFill(
	eng *engine, sty ResolvedStyle,
	posX, posY, width, height float64, radii, radiiY [4]float64, shapeK [4]float64,
) []Op {
	usedBG := usedBGForPaintActive(sty, eng.forcedColorsForPaint())

	fillX, fillY, fillW, fillH, ok := cornerShapePaintFillBox(sty, posX, posY, width, height)
	if !ok {
		return nil
	}

	fillOp, painted := cornerShapePaintFillOp(
		usedBG[0], usedBG[1], usedBG[2], usedBG[3], fillX, fillY, fillW, fillH, radii, radiiY, shapeK)
	if !painted {
		return nil
	}

	return []Op{fillOp}
}

// cornerShapePaintFillOp builds one fill path op for the given color and
// fill box. painted is false when the box or the traced outline is empty.
func cornerShapePaintFillOp(
	red, green, blue, alpha, posX, posY, width, height float64,
	radii, radiiY [4]float64, shapeK [4]float64,
) (Op, bool) {
	if !(width > 0 && height > 0) {
		return Op{}, false //nolint:exhaustruct // zero op on no-paint
	}

	points := cornerShapePaintOuterPath(posX, posY, width, height, radii, radiiY, shapeK)
	if len(points) == 0 {
		return Op{}, false //nolint:exhaustruct // zero op on no-paint
	}

	fillOp := Op{ //nolint:exhaustruct // intentional zero fields
		Kind: OpPath, X: posX, Y: posY, W: width, H: height,
		R: red, G: green, B: blue, Alpha: alpha,
	}
	fillOp.setCornerPath(cornerShapePathCommands(points), false)

	return fillOp, true
}

// cornerShapePaintBorder emits the shaped border stroke, replacing the
// rounded OpStrokeRect. borderHandled is false when the box keeps its
// existing border emission: a border image, or sides that differ in paint
// width, color, or style.
func cornerShapePaintBorder(
	eng *engine, sty ResolvedStyle,
	posX, posY, width, height float64, radii, radiiY [4]float64, shapeK [4]float64,
) ([]Op, bool) {
	if sty.BorderImageSource != "" {
		return nil, false
	}

	base, ok := cornerShapePaintUniformBorder(sty)
	if !ok {
		return nil, false
	}

	points := cornerShapePaintOuterPath(posX, posY, width, height, radii, radiiY, shapeK)
	if len(points) == 0 {
		return nil, false
	}

	borderOp := Op{ //nolint:exhaustruct // intentional zero fields
		Kind: OpPath, X: posX, Y: posY, W: width, H: height,
		R: base.Color[0], G: base.Color[1], B: base.Color[2],
		Width: eng.scalePt(borderPaint(base)),
	}
	borderOp.setCornerPath(cornerShapePathCommands(points), true)

	return []Op{borderOp}, true
}

// cornerShapePathCommands converts the flattened outer path into OpPath
// commands: one moveTo, a lineTo per remaining point, and a close.
func cornerShapePathCommands(points []DisplayPoint) []PathCmd {
	if len(points) == 0 {
		return nil
	}

	commands := make([]PathCmd, 0, len(points)+1)
	commands = append(commands, PathCmd{Verb: PathMoveTo, Pts: [3]DisplayPoint{points[0]}})

	for _, point := range points[1:] {
		commands = append(commands, PathCmd{Verb: PathLineTo, Pts: [3]DisplayPoint{point}})
	}

	commands = append(commands, PathCmd{Verb: PathClose, Pts: [3]DisplayPoint{}})

	return commands
}

// cornerShapePaintFillBox resolves the fill box for a shaped background.
// It mirrors prependChrome's background-clip handling: content-box and
// padding-box clip the fill, any other value (including the initial
// border-box) keeps the border box, and the clip never follows a scooped
// outer path. n is false when the box is empty.
func cornerShapePaintFillBox(
	sty ResolvedStyle, posX, posY, width, height float64,
) (float64, float64, float64, float64, bool) {
	bgClip := strings.ToLower(strings.TrimSpace(sty.BackgroundClip))
	if bgClip != shapeBoxContent && bgClip != shapeBoxPadding {
		if !(width > 0 && height > 0) {
			return 0, 0, 0, 0, false
		}

		return posX, posY, width, height, true
	}

	clipX, clipY, clipW, clipH := resolveBackgroundClipBox(sty, posX, posY, width, height)
	if !(clipW > 0 && clipH > 0) {
		return 0, 0, 0, 0, false
	}

	return clipX, clipY, clipW, clipH, true
}

// cornerShapePaintUniformBorder returns the single border side to paint
// when all four sides match in paint width, color, and style. Dashed and
// dotted styles count as matching here: the shaped path paints solid in
// this wave and never runs dash segments through the curve. Any mismatch,
// transparency, or missing side returns false so the caller stays on the
// existing stroke-rect path.
func cornerShapePaintUniformBorder(sty ResolvedStyle) (border, bool) {
	sides := [4]border{sty.BorderTop, sty.BorderRight, sty.BorderBottom, sty.BorderLeft}
	base := sides[0]

	if !cornerShapePaintBorderSideUsable(base) {
		return border{}, false //nolint:exhaustruct // zero border on no-match
	}

	for _, side := range sides[1:] {
		if !cornerShapePaintBorderSideUsable(side) ||
			side.Style != base.Style ||
			borderPaint(side) != borderPaint(base) ||
			side.Color != base.Color {
			return border{}, false //nolint:exhaustruct // zero border on no-match
		}
	}

	return base, true
}

// cornerShapePaintBorderSideUsable reports whether one border side paints.
func cornerShapePaintBorderSideUsable(side border) bool {
	return !side.Transparent && side.Style != cssDisplayNone && borderPaint(side) > 0
}

// cornerShapePaintOuterPath traces the closed outer edge of the box with
// one shaped corner per corner box, clockwise from the top edge. Straight
// edges between corners stay straight. Radii fields on the emitted ops
// stay zero; the corner boxes live only in the returned points.
func cornerShapePaintOuterPath(
	minX, minY, width, height float64, radii, radiiY [4]float64, shapeK [4]float64,
) []DisplayPoint {
	if !(width > 0 && height > 0) {
		return nil
	}

	radiusX := radii
	radiusY := radiiY

	for idx := range radiusX {
		if !(radiusX[idx] > 0) {
			radiusX[idx] = 0
		}

		if radiusX[idx] > width {
			radiusX[idx] = width
		}

		if !(radiusY[idx] > 0) {
			radiusY[idx] = 0
		}

		if radiusY[idx] > height {
			radiusY[idx] = height
		}
	}

	points := make([]DisplayPoint, 0, cornerShapePaintPointCap)
	// Each corner emits its own edge endpoints, so the straight edges between
	// corners are the segments between one corner's end and the next corner's
	// start; the top edge is the closing segment. Top-right and bottom-left
	// run horizontal edge to vertical edge; bottom-right and top-left run the
	// other way. TL is emitted last, and the close returns to the first point
	// (the top edge start).
	points = appendCornerShapePaintPoints(points, minX+width, minY, -radiusX[1], radiusY[1], shapeK[1], false)
	points = appendCornerShapePaintPoints(points, minX+width, minY+height, -radiusX[2], -radiusY[2], shapeK[2], true)
	points = appendCornerShapePaintPoints(points, minX, minY+height, radiusX[3], -radiusY[3], shapeK[3], false)
	points = appendCornerShapePaintPoints(points, minX, minY, radiusX[0], radiusY[0], shapeK[0], true)

	return points
}

// appendCornerShapePaintPoints appends one corner's outline from its start
// edge point to its end edge point, both emitted exactly. outerX, outerY is
// the outer box corner and deltaX, deltaY is the signed inner offset of the
// curve's inner corner, so the two edge points are the horizontal-edge point
// (outerX+deltaX, outerY) and the vertical-edge point (outerX, outerY+deltaY).
// reverse selects the traversal direction, keeping the clockwise trace
// continuous.
func appendCornerShapePaintPoints(
	points []DisplayPoint, outerX, outerY, deltaX, deltaY, kValue float64, reverse bool,
) []DisplayPoint {
	horizontalEdge := DisplayPoint{X: outerX + deltaX, Y: outerY}
	verticalEdge := DisplayPoint{X: outerX, Y: outerY + deltaY}

	start, end := horizontalEdge, verticalEdge
	if reverse {
		start, end = verticalEdge, horizontalEdge
	}

	points = append(points, start)

	if deltaX == 0 || deltaY == 0 {
		if start != end {
			points = append(points, end)
		}

		return points
	}

	if math.IsNaN(kValue) {
		kValue = CornerShapeRoundK
	}

	switch {
	case math.IsInf(kValue, 1):
		// square: the sharp outer corner.
		points = append(points, DisplayPoint{X: outerX, Y: outerY})
	case math.IsInf(kValue, -1):
		// notch: the concave V through the inner corner.
		points = append(points, DisplayPoint{X: outerX + deltaX, Y: outerY + deltaY})
	case kValue == 0:
		// bevel: the straight chamfer; start to end is the whole outline.
	default:
		points = appendCornerShapePaintArc(points, outerX, outerY, deltaX, deltaY, kValue, reverse)
	}

	return append(points, end)
}

// appendCornerShapePaintArc samples the superellipse between the corner's
// edge points. Samples are generated from the horizontal-edge side toward the
// vertical-edge side and emitted in reverse when the traversal runs the other
// way; the endpoints themselves come from the caller, so only interior
// samples are appended.
func appendCornerShapePaintArc(
	points []DisplayPoint, outerX, outerY, deltaX, deltaY, kValue float64, reverse bool,
) []DisplayPoint {
	exponent := cornerShapeCircleExponent / (math.Abs(kValue) + 1)

	samples := make([]DisplayPoint, 0, cornerShapePaintSegments-1)

	for step := 1; step < cornerShapePaintSegments; step++ {
		angle := cornerShapeQuarterTurn * (1 - float64(step)/cornerShapePaintSegments)
		cosine, sine := math.Cos(angle), math.Sin(angle)
		curveX := 1 - math.Pow(math.Abs(cosine), exponent)
		curveY := 1 - math.Pow(math.Abs(sine), exponent)

		offsetX, offsetY := curveX, curveY
		if kValue < 0 {
			offsetX, offsetY = 1-curveY, 1-curveX
		}

		samples = append(samples, DisplayPoint{X: outerX + offsetX*deltaX, Y: outerY + offsetY*deltaY})
	}

	if reverse {
		slices.Reverse(samples)
	}

	return append(points, samples...)
}
