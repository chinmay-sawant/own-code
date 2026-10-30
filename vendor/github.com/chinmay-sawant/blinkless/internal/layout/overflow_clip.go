//nolint:varnamelen // clip math uses compact x/y/w/h/op geometry names
package layout

import "math"

// clipRect is an axis-aligned padding-box clip in canvas points.
type clipRect struct {
	x, y, w, h float64
}

// overflowClipsPaint reports overflow values that clip descendant paint to
// the padding box. auto/scroll clip the same (no user scroll).
func overflowClipsPaint(overflow string) bool {
	switch overflow {
	case overflowHidden, overflowClip, overflowScroll, overflowAuto:
		return true
	}

	return false
}

func (r clipRect) empty() bool {
	return r.w <= 0 || r.h <= 0
}

func intersectClip(a, b clipRect) clipRect {
	x1 := math.Max(a.x, b.x)
	y1 := math.Max(a.y, b.y)
	x2 := math.Min(a.x+a.w, b.x+b.w)
	y2 := math.Min(a.y+a.h, b.y+b.h)

	if x2 <= x1 || y2 <= y1 {
		return clipRect{} //nolint:exhaustruct // empty intersection
	}

	return clipRect{x: x1, y: y1, w: x2 - x1, h: y2 - y1}
}

func (e *engine) paddingBoxRect(posX, posY, width, height float64, sty ResolvedStyle) clipRect {
	left := e.scalePt(sty.BorderLeft.Width)
	top := e.scalePt(sty.BorderTop.Width)
	right := e.scalePt(sty.BorderRight.Width)
	bottom := e.scalePt(sty.BorderBottom.Width)
	w := width - left - right
	h := height - top - bottom

	if w < 0 {
		w = 0
	}

	if h < 0 {
		h = 0
	}

	rect := clipRect{x: posX + left, y: posY + top, w: w, h: h}

	// Inflate for overflow-clip-margin (used by fixture-62 rows 27-37).
	// Values are in points already via parseAdvancedLength.
	if sty.OverflowClipMarginTop != 0 || sty.OverflowClipMarginRight != 0 ||
		sty.OverflowClipMarginBottom != 0 || sty.OverflowClipMarginLeft != 0 {
		rect.x -= sty.OverflowClipMarginLeft
		rect.y -= sty.OverflowClipMarginTop
		rect.w += sty.OverflowClipMarginLeft + sty.OverflowClipMarginRight
		rect.h += sty.OverflowClipMarginTop + sty.OverflowClipMarginBottom

		if rect.w < 0 {
			rect.w = 0
		}

		if rect.h < 0 {
			rect.h = 0
		}
	}

	return rect
}

func (e *engine) paddingBoxOf(boxNode *box) clipRect {
	if boxNode == nil || boxNode.style == nil {
		return clipRect{} //nolint:exhaustruct // missing box
	}

	return e.paddingBoxRect(boxNode.x, boxNode.y, boxNode.w, boxNode.height, *boxNode.style)
}

// applyOverflowClips clips descendant paint to padding boxes of overflow
// hidden|clip|auto|scroll ancestors. Own background/border/outline of the
// overflow box are kept. Call after finalizeChrome has merged deferred ops.
func (e *engine) applyOverflowClips(root *box) {
	if e == nil || root == nil {
		return
	}

	e.clipOverflowTree(root, nil)
}

const (
	unconstrainedClipOffset = -1e9
	unconstrainedClipSpan   = 2e9
	clipPointTolerance      = 0.01
	clipZeroLineEpsilon     = 1e-6
)

func (e *engine) computeBoxOverflowClip(boxNode *box, current *clipRect) *clipRect {
	if boxNode.style == nil {
		return current
	}

	clipX := clipsPaintAxis(boxNode.style, boxNode.style.OverflowX)
	clipY := clipsPaintAxis(boxNode.style, boxNode.style.OverflowY)

	if !clipX && !clipY {
		return current
	}

	// Read the clip through the scroll viewport so the rect stays offset-aware:
	// the viewport pairs the padding box with the offset the region carries
	// (see scroll.go and scroll_runtime.go). Production carries zero, so this
	// matches paddingBoxOf exactly and static paint is unchanged; tests carry
	// nonzero offsets and the clip translates with them. The zero-margin form
	// of scrollVisibleRect is used so the scroller clip itself carries no one
	// target's scroll-margin inset; per-target margin insets apply through
	// scrollVisibleRect for snap-target visibility.
	view := e.scrollRegionViewport(boxNode)
	pb := scrollOffsetAwarePort(view)

	if !clipX {
		pb.x = unconstrainedClipOffset
		pb.w = unconstrainedClipSpan
	}

	if !clipY {
		pb.y = unconstrainedClipOffset
		pb.h = unconstrainedClipSpan
	}

	if current != nil {
		merged := intersectClip(*current, pb)

		return &merged
	}

	return &pb
}

// clipsPaintAxis reports whether one overflow axis clips paint: the axis
// longhand, the overflow shorthand, or contain: paint.
func clipsPaintAxis(style *ResolvedStyle, axis string) bool {
	return overflowClipsPaint(axis) || overflowClipsPaint(style.Overflow) || containsPaint(*style)
}

func (e *engine) clipOverflowTree(boxNode *box, clip *clipRect) {
	if boxNode == nil {
		return
	}

	// Record the ancestor clip on the box for hosts: transformed
	// descendants must intersect their paint with it at draw time.
	if clip == nil {
		boxNode.overflowClip = nil
	} else {
		kept := *clip
		boxNode.overflowClip = &kept
	}

	next := e.computeBoxOverflowClip(boxNode, clip)

	switch {
	case clip != nil:
		// Ancestor clip applies to this whole box, including its chrome.
		clipOpsRange(e.ops, boxNode.opStart, boxNode.opEnd, *clip)
	case next != nil:
		// This box established the clip: keep its chrome, clip descendants
		// and its own in-flow content.
		e.clipBoxContents(boxNode, *next)
	}

	for _, child := range boxNode.children {
		e.clipOverflowTree(child, next)
	}

	e.clipDeprecatedRect(boxNode)
}

func (e *engine) clipBoxContents(boxNode *box, clip clipRect) {
	if boxNode == nil || clip.empty() {
		return
	}

	for _, child := range boxNode.children {
		if child == nil {
			continue
		}

		clipOpsRange(e.ops, child.opStart, child.opEnd, clip)
	}

	e.clipOwnContentOps(e.ops, boxNode, clip)
}

func clipOpsRange(ops []Op, start, end int, clip clipRect) {
	if end < start || start < 0 || clip.empty() {
		return
	}

	for i := start; i <= end && i < len(ops); i++ {
		clipPaintOp(&ops[i], clip)
	}
}

func clipOpsSlice(ops []Op, clip clipRect) {
	if clip.empty() {
		return
	}

	for i := range ops {
		clipPaintOp(&ops[i], clip)
	}
}

func (e *engine) clipOwnContentOps(ops []Op, boxNode *box, clip clipRect) {
	if e == nil || boxNode == nil || boxNode.opEnd < boxNode.opStart || boxNode.opStart < 0 {
		return
	}

	for i := boxNode.opStart; i <= boxNode.opEnd && i < len(ops); i++ {
		if opInChildRange(boxNode, i) || opOwnedBy(&ops[i], boxNode, opOwnerClip) {
			continue
		}

		clipPaintOp(&ops[i], clip)
	}
}

func opInChildRange(boxNode *box, idx int) bool {
	if boxNode == nil {
		return false
	}

	for _, child := range boxNode.children {
		if child == nil || child.opEnd < child.opStart {
			continue
		}

		if idx >= child.opStart && idx <= child.opEnd {
			return true
		}
	}

	return false
}

func lineOnRectEdges(op *Op, x, y, w, h float64) bool {
	if op == nil {
		return false
	}

	if op.Kind == OpGridRun {
		onEdge := false

		op.forEachLine(func(line Op) {
			if !onEdge && lineOnRectEdges(&line, x, y, w, h) {
				onEdge = true
			}
		})

		return onEdge
	}

	if op.Kind != OpLine {
		return false
	}

	if math.Abs(op.H) <= clipPointTolerance && op.W > 0 {
		return horizontalOnRectEdges(op, x, y, w, h)
	}

	if math.Abs(op.W) <= clipPointTolerance && op.H > 0 {
		return verticalOnRectEdges(op, x, y, w, h)
	}

	return false
}

func horizontalOnRectEdges(op *Op, x, y, w, h float64) bool {
	if op == nil {
		return false
	}

	onTop := math.Abs(op.Y-y) <= clipPointTolerance
	onBot := math.Abs(op.Y-(y+h)) <= clipPointTolerance

	if !onTop && !onBot {
		return false
	}

	return op.X+op.W >= x-clipPointTolerance && op.X <= x+w+clipPointTolerance
}

func verticalOnRectEdges(op *Op, x, y, w, h float64) bool {
	if op == nil {
		return false
	}

	onLeft := math.Abs(op.X-x) <= clipPointTolerance
	onRight := math.Abs(op.X-(x+w)) <= clipPointTolerance

	if !onLeft && !onRight {
		return false
	}

	return op.Y+op.H >= y-clipPointTolerance && op.Y <= y+h+clipPointTolerance
}

func clipPaintOp(op *Op, clip clipRect) {
	if op == nil || clip.empty() || op.Kind == opKindNoop {
		return
	}

	switch op.Kind {
	case OpFillRect, OpStrokeRect, OpImage, OpLinkURI:
		clipRectOp(op, clip)
	case OpPath:
		clipCornerPathOp(op, clip)
	case OpLine:
		clipLineOp(op, clip)
	case OpGridRun:
		clipGridRunOp(op, clip)
	case OpText, OpBullet:
		clipTextOp(op, clip)
	case OpUnknown, opKindNoop:
	}
}

// clipCornerPathOp keeps a corner-shape path only while its bounds overlap
// the clip. Point-level clipping inside a partial overlap is a later wave
// (documented in corner_shape_paint.go).
func clipCornerPathOp(op *Op, clip clipRect) {
	if !rectIntersects(op.X, op.Y, op.X+op.W, op.Y+op.H, clip) {
		DeactivateOp(op)
	}
}

// clipGridRunOp clips every segment of a batched grid run, dropping segments
// the clip removed entirely. The run is the union of its remaining segments.
func clipGridRunOp(op *Op, clip clipRect) {
	if op.Grid == nil {
		DeactivateOp(op)

		return
	}

	kept := op.Grid.Segs[:0]

	for idx := range op.Grid.Segs {
		line := op.Grid.asLine(op, idx)
		clipLineOp(&line, clip)

		if line.Kind == opKindNoop {
			continue
		}

		seg := op.Grid.Segs[idx]
		seg.X, seg.Y, seg.W, seg.H = line.X, line.Y, line.W, line.H
		kept = append(kept, seg)
	}

	if len(kept) == 0 {
		DeactivateOp(op)

		return
	}

	op.Grid.Segs = kept
	recomputeGridRunBounds(op)
}

func clipRectOp(op *Op, clip clipRect) {
	x1 := math.Max(op.X, clip.x)
	y1 := math.Max(op.Y, clip.y)
	x2 := math.Min(op.X+op.W, clip.x+clip.w)
	y2 := math.Min(op.Y+op.H, clip.y+clip.h)

	if x2-x1 <= clipPointTolerance || y2-y1 <= clipPointTolerance {
		DeactivateOp(op)

		return
	}

	op.X, op.Y, op.W, op.H = x1, y1, x2-x1, y2-y1
}

func clipLineOp(op *Op, clip clipRect) {
	x0, y0 := op.X, op.Y
	x1, y1 := op.X+op.W, op.Y+op.H

	if math.Abs(op.H) <= clipZeroLineEpsilon {
		clipHorizontalLine(op, clip, x0, x1, y0)

		return
	}

	if math.Abs(op.W) <= clipZeroLineEpsilon {
		clipVerticalLine(op, clip, y0, y1, x0)

		return
	}

	if !rectIntersects(x0, y0, x1, y1, clip) {
		DeactivateOp(op)
	}
}

func clipHorizontalLine(op *Op, clip clipRect, x0, x1, y0 float64) {
	if y0 < clip.y-clipPointTolerance || y0 > clip.y+clip.h+clipPointTolerance {
		DeactivateOp(op)

		return
	}

	left := math.Min(x0, x1)
	right := math.Max(x0, x1)
	nl := math.Max(left, clip.x)
	nr := math.Min(right, clip.x+clip.w)

	if nr-nl <= 0 {
		DeactivateOp(op)

		return
	}

	op.X, op.Y, op.W, op.H = nl, y0, nr-nl, 0
}

func clipVerticalLine(op *Op, clip clipRect, y0, y1, x0 float64) {
	if x0 < clip.x-clipPointTolerance || x0 > clip.x+clip.w+clipPointTolerance {
		DeactivateOp(op)

		return
	}

	top := math.Min(y0, y1)
	bot := math.Max(y0, y1)
	nt := math.Max(top, clip.y)
	nb := math.Min(bot, clip.y+clip.h)

	if nb-nt <= 0 {
		DeactivateOp(op)

		return
	}

	op.X, op.Y, op.W, op.H = x0, nt, 0, nb-nt
}

func rectIntersects(x0, y0, x1, y1 float64, clip clipRect) bool {
	minX, maxX := math.Min(x0, x1), math.Max(x0, x1)
	minY, maxY := math.Min(y0, y1), math.Max(y0, y1)

	return maxX >= clip.x && minX <= clip.x+clip.w &&
		maxY >= clip.y && minY <= clip.y+clip.h
}

func clipTextOp(op *Op, clip clipRect) {
	left := op.X
	right := op.X

	if op.W > 0 {
		right = op.X + op.W
	}

	bottom := op.Y
	top := op.Y

	if op.H > 0 {
		top = op.Y - op.H
	} else if op.Size > 0 {
		top = op.Y - op.Size
	}

	if right < left {
		left, right = right, left
	}

	if bottom < top {
		top, bottom = bottom, top
	}

	outside := right <= clip.x || left >= clip.x+clip.w || bottom <= clip.y || top >= clip.y+clip.h
	if outside {
		DeactivateOp(op)
	}
}

// clipDeprecatedRect applies the deprecated CSS2 clip property to an
// absolutely positioned box. The rect() declaration is stored canonically on
// the style (style_leftovers.go) with lengths parsed by the clip-path length
// parser; here it resolves against the border box and cuts the box's ops,
// descendants included, per CSS 2.1 section 11.1.2. Static and relative boxes
// keep their paint: Chrome 143.0.7499.40 ignores clip there, as does
// clip:auto (stored as no Clip). Reference: Chrome 143.0.7499.40.
func (e *engine) clipDeprecatedRect(boxNode *box) {
	edges, ok := e.deprecatedClipEdges(boxNode)
	if !ok {
		return
	}

	rect, hasArea := e.deprecatedClipBounds(boxNode, edges)
	if !hasArea {
		e.deactivateBoxRange(boxNode)

		return
	}

	clipOpsRange(e.ops, boxNode.opStart, boxNode.opEnd, rect)
}

// deprecatedClipEdges validates the box and parses its stored clip rect:
// only absolutely positioned boxes with a well-formed range clip.
func (e *engine) deprecatedClipEdges(boxNode *box) (clipRectEdges, bool) {
	var empty clipRectEdges

	if e == nil || boxNode == nil || boxNode.style == nil || boxNode.style.Clip == "" {
		return empty, false
	}

	if boxNode.style.Position != positionAbsolute && boxNode.style.Position != positionFixed {
		return empty, false
	}

	if boxNode.opEnd < boxNode.opStart || boxNode.opStart < 0 {
		return empty, false
	}

	return parseDeprecatedClipRect(boxNode.style.Clip, boxNode.style.FontSize)
}

// deactivateBoxRange hides every op a box emitted.
func (e *engine) deactivateBoxRange(boxNode *box) {
	if e == nil || boxNode == nil {
		return
	}

	for i := boxNode.opStart; i <= boxNode.opEnd && i < len(e.ops); i++ {
		DeactivateOp(&e.ops[i])
	}
}

// deprecatedClipBounds resolves parsed clip edges against the border box in
// canvas points. rect() lengths measure from the border-box top-left origin;
// auto edges read as the matching border-box edge. hasArea=false reports an
// empty rect (nothing visible).
func (e *engine) deprecatedClipBounds(boxNode *box, edges clipRectEdges) (clipRect, bool) {
	bx, by, bw, bh := boxNode.x, boxNode.y, boxNode.w, boxNode.height
	left, top := bx, by
	right, bottom := bx+bw, by+bh

	if !edges.leftAuto {
		left = bx + resolveDeprecatedClipEdge(edges.left, bw, e)
	}

	if !edges.topAuto {
		top = by + resolveDeprecatedClipEdge(edges.top, bh, e)
	}

	if !edges.rightAuto {
		right = bx + resolveDeprecatedClipEdge(edges.right, bw, e)
	}

	if !edges.bottomAuto {
		bottom = by + resolveDeprecatedClipEdge(edges.bottom, bh, e)
	}

	rect := clipRect{x: left, y: top, w: right - left, h: bottom - top}
	if rect.w <= 0 || rect.h <= 0 {
		return clipRect{}, false //nolint:exhaustruct // empty rect hides the box
	}

	return rect, true
}

// resolveDeprecatedClipEdge resolves one stored clip edge to canvas points:
// percentages read against the border-box dimension (already canvas points),
// absolute lengths scale from style points.
func resolveDeprecatedClipEdge(length clipPathLength, base float64, e *engine) float64 {
	if length.percent {
		return length.value * base
	}

	if e == nil {
		return length.value
	}

	return e.scalePt(length.value)
}
