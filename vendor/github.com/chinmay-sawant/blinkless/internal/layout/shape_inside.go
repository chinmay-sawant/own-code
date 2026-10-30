package layout

// shape-inside lite (v0.0.3 shapes ledger). The outside hook (shape-outside,
// shape_exclusion.go) carves a hole out of the lines BESIDE a float. This
// file builds the inside hook: a contour that narrows the element's OWN line
// boxes. The two share the circle/ellipse/inset geometry; only the reference
// rectangle and the consumer differ.

// buildShapeInside resolves sty.ShapeInside (or, for outside-shape, the
// element's own ShapeOutside) against the element's own reference box.
// boxNode is the element's border box; contentX/contentW is the content box
// the caller lays lines into. Returns nil when there is no usable inside
// contour: auto, a rejected token, outside-shape without a shape-outside, a
// box without a definite content height (see below), or unstorable geometry.
//
// A non-nil contour always applies. intervalAt(y) is the span the element's
// lines may use at canvas y, already shrunk by shape-padding; a missing
// interval at a y means an empty span there (the caller keeps the line at its
// Y with width 0), never a full-width line.
//
// A definite content height is required because the inside contour is
// anchored to the content box, and during line layout an auto-height box has
// no final height yet. The lite rule keeps shape-inside a no-op on
// auto-height boxes instead of guessing a height; a measure pre-pass is the
// follow-up if this ever matters.
func (e *engine) buildShapeInside(
	sty ResolvedStyle, boxNode *box, contentX, contentW float64,
) *shapeExclusion {
	if boxNode == nil {
		return nil
	}

	source := sty.ShapeInside

	if sty.ShapeInsideOutside {
		source = sty.ShapeOutside
	}

	if source == "" || source == shapeOutsideNone || source == shapeInsideAuto {
		return nil
	}

	contentH, ok := e.definiteContentHeight(sty)
	if !ok || contentH <= 0 || contentW <= 0 {
		return nil
	}

	contentTop := boxNode.y + e.scalePt(sty.PaddingTop) +
		e.scalePt(borderLayoutWidth(&sty, sty.BorderTop))

	refX, refY, refW, refH := e.insideReferenceBox(sty, contentX, contentW, contentTop, contentH)

	shape := e.resolveInsideShape(sty, source, refX, refY, refW, refH)
	if shape == nil {
		return nil
	}

	shrinkInsideShape(shape, resolveShapePadding(sty, refW, refH, e.scale))

	return shape
}

// resolveInsideShape runs the circle/ellipse/inset math from
// shape_exclusion.go against the inside reference rectangle. margin stays 0;
// shape-padding shrinks the contour after it resolves, not through the
// outside-only shape-margin slot.
func (e *engine) resolveInsideShape(
	sty ResolvedStyle, source string, refX, refY, refW, refH float64,
) *shapeExclusion {
	if refW <= 0 || refH <= 0 {
		return nil
	}

	name, args, ok := insideShapeParts(source)
	if !ok {
		return nil
	}

	switch name {
	case listStyleCircle:
		return resolveCircleExclusion(args, refX, refY, refW, refH, 0, sty.FontSize, e.scale)
	case shapeEllipseKeyword:
		return resolveEllipseExclusion(args, refX, refY, refW, refH, 0, sty.FontSize, e.scale)
	case "inset":
		return resolveInsetExclusion(args, refX, refY, refW, refH, 0, sty.FontSize, e.scale)
	default:
		return nil
	}
}

// insideShapeParts splits a stored shape string into function name and args.
// ShapeInside is canonical (no box keyword); ShapeOutside may carry one, so a
// failed box split falls back to the whole string, mirroring
// buildShapeExclusion.
func insideShapeParts(raw string) (string, string, bool) {
	value := normalizeCSSValue(raw)

	shape, _, ok := splitShapeOutsideParts(value)
	if !ok {
		shape = value
	}

	return splitShapeFunction(shape)
}

// insideReferenceBox returns the canvas rectangle the ShapeInsideBox keyword
// names: the content box by default, expanded by padding, border, or margins.
// Auto margins read as 0 (their stored length) here. The expansion uses the
// same border widths as contentBox (layout_flow.go) so a border-image box
// measures the same in both places.
func (e *engine) insideReferenceBox(
	sty ResolvedStyle, contentX, contentW, contentTop, contentH float64,
) (float64, float64, float64, float64) {
	padL := e.scalePt(sty.PaddingLeft)
	padR := e.scalePt(sty.PaddingRight)
	padT := e.scalePt(sty.PaddingTop)
	padB := e.scalePt(sty.PaddingBottom)
	bordL := e.scalePt(borderLayoutWidth(&sty, sty.BorderLeft))
	bordR := e.scalePt(borderLayoutWidth(&sty, sty.BorderRight))
	bordT := e.scalePt(borderLayoutWidth(&sty, sty.BorderTop))
	bordB := e.scalePt(borderLayoutWidth(&sty, sty.BorderBottom))

	var left, right, top, bottom float64

	switch sty.ShapeInsideBox {
	case shapeBoxPadding:
		left, right, top, bottom = padL, padR, padT, padB
	case shapeBoxBorder:
		left, right, top, bottom = padL+bordL, padR+bordR, padT+bordT, padB+bordB
	case shapeBoxMargin:
		left = padL + bordL + e.scalePt(sty.MarginLeft)
		right = padR + bordR + e.scalePt(sty.MarginRight)
		top = padT + bordT + e.scalePt(sty.MarginTop)
		bottom = padB + bordB + e.scalePt(sty.MarginBottom)
	default: // missing keyword and content-box both name the content box
	}

	return contentX - left, contentTop - top, contentW + left + right, contentH + top + bottom
}

// resolveShapePadding resolves shape-padding the way resolveShapeMargin
// resolves shape-margin (shape_exclusion.go): a percentage against the
// reference diagonal sqrt(w^2+h^2)/sqrt(2), a length scaled to canvas points.
func resolveShapePadding(sty ResolvedStyle, refW, refH, scale float64) float64 {
	if sty.ShapePaddingPercent >= 0 {
		return sty.ShapePaddingPercent / 100 * shapeReferenceDiagonal(refW, refH)
	}

	return sty.ShapePadding * scale
}

// shrinkInsideShape insets the contour by pad: an ellipse loses the padding
// from both radii, an inset rectangle moves all four edges inward. A fully
// consumed shape stays non-nil and reports an empty interval at query time
// (the caller emits a width-0 line at that Y and never invents a page break).
func shrinkInsideShape(shape *shapeExclusion, pad float64) {
	if pad == 0 {
		return
	}

	switch shape.kind {
	case shapeKindNone:
		// A missing kind carries no geometry to inset; keep it as-is.
	case shapeKindEllipse:
		shape.rx -= pad
		shape.ry -= pad
	case shapeKindInset:
		shape.x0 += pad
		shape.x1 -= pad
		shape.y0 += pad
		shape.y1 -= pad
	}
}

// insideSpanAt returns the span the inside contour allows at canvas y.
// inside == nil keeps the full content width. A y outside the contour (or a
// contour consumed by shape-padding) yields a zero-width span at contentX.
// The span is not clamped to the content box: a padding/border/margin-box
// reference intentionally lets lines reach into that box.
func insideSpanAt(inside *shapeExclusion, contentX, contentW, y float64) (float64, float64) {
	if inside == nil {
		return contentX, contentW
	}

	left, right, ok := inside.intervalAt(y)
	if !ok || right <= left {
		return contentX, 0
	}

	return left, right - left
}

// intersectSpans returns the overlap of two horizontal spans. A disjoint pair
// collapses to a zero-width span at the later start.
func intersectSpans(x1, w1, x2, w2 float64) (float64, float64) {
	start := maxY(x1, x2)
	end := minY(x1+w1, x2+w2)

	if end < start {
		end = start
	}

	return start, end - start
}
