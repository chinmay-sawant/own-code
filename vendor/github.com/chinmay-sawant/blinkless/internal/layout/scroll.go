package layout

import "strings"

// Scroll-margin snap offsets (CSS Scroll Snap 1,
// https://drafts.csswg.org/css-scroll-snap-1/#propdef-scroll-margin).
//
// Chrome 143.0.7499.40 uses scroll-margin as a scroll-snap offset: the margin
// edges of a snap target (its border box outset by these values) align to the
// scroll container's snapport. This engine has no JavaScript, no user scroll,
// and static print-style output (see documentation/deferred.md), so production
// carries the zero scroll offset and static paint clips to the padding box
// exactly as before. Tests carry nonzero offsets through setTestScrollOffset
// in scroll_runtime.go. The model below is the smallest honest step: the
// cascade stores real used values (points) for the shorthand and its four
// longhands, each scrollable region exposes one offset-aware viewport rect,
// and scroll-margin insets the snap/visibility rect through pure functions the
// tests assert in points.
//
// What is intentionally left out (follow-up, not a gap in this change): full
// snapping, which would choose a snap offset from scroll-snap-align and
// scroll-snap-type and then scroll the region to it. That needs a scrolling
// pass this engine does not have. The seam is scrollRegionViewport, consumed
// by computeBoxOverflowClip in overflow_clip.go: it now plugs the carried
// offset in there and every consumer below reads the translated rect with no
// further changes.
const (
	scrollMarginProp       = "scroll-margin"
	scrollMarginTopProp    = "scroll-margin-top"
	scrollMarginRightProp  = "scroll-margin-right"
	scrollMarginBottomProp = "scroll-margin-bottom"
	scrollMarginLeftProp   = "scroll-margin-left"

	// scrollMarginPairSides and scrollMarginTripleSides are the two- and
	// three-token shorthand arities (vertical/horizontal, top/horizontal/bottom).
	scrollMarginPairSides   = 2
	scrollMarginTripleSides = 3
	// scrollMarginMaxSides is the four-token shorthand arity (top/right/bottom/left).
	scrollMarginMaxSides = 4
)

// applyScrollProps owns the scroll-margin shorthand and its four physical
// longhands. It runs near the end of styleGroups (style_cascade.go) so no
// earlier group can claim these names; applySVGPresentationProps in
// style_paint_props.go already routes them to applyLeftoversProps, which
// declines them, so dispatch reaches this group. Negative lengths are invalid
// per CSS Scroll Snap 1: the declaration is dropped and the previous value
// wins. Reference: Chrome 143.0.7499.40.
func applyScrollProps(
	style *ResolvedStyle, prop, value string, fsize float64, _ *styleContext,
	_ *ResolvedStyle, _ bool,
) bool {
	switch prop {
	case scrollMarginTopProp:
		applyScrollMarginSide(&style.ScrollMarginTop, value, fsize)

		return true
	case scrollMarginRightProp:
		applyScrollMarginSide(&style.ScrollMarginRight, value, fsize)

		return true
	case scrollMarginBottomProp:
		applyScrollMarginSide(&style.ScrollMarginBottom, value, fsize)

		return true
	case scrollMarginLeftProp:
		applyScrollMarginSide(&style.ScrollMarginLeft, value, fsize)

		return true
	case scrollMarginProp:
		applyScrollMarginShorthand(style, value, fsize)

		return true
	default:
		return false
	}
}

// applyScrollMarginSide stores one validated scroll-margin longhand. An
// invalid value leaves the previous declaration intact.
func applyScrollMarginSide(slot *float64, value string, fsize float64) {
	if length, ok := parseScrollMarginLength(value, fsize); ok {
		*slot = length
	}
}

// applyScrollMarginShorthand expands 1-4 non-negative lengths onto the four
// physical sides, following the margin shorthand order (top, right, bottom,
// left). Any invalid or negative token voids the whole declaration, so
// nothing is written and the previous values win.
func applyScrollMarginShorthand(style *ResolvedStyle, value string, fsize float64) {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	if len(tokens) == 0 || len(tokens) > scrollMarginMaxSides {
		return
	}

	lengths := make([]float64, 0, scrollMarginMaxSides)

	for _, token := range tokens {
		length, ok := parseScrollMarginLength(token, fsize)
		if !ok {
			return
		}

		lengths = append(lengths, length)
	}

	top, right, bottom, left := lengths[0], lengths[0], lengths[0], lengths[0]

	switch len(lengths) {
	case scrollMarginPairSides:
		right, left = lengths[1], lengths[1]
	case scrollMarginTripleSides:
		right, left, bottom = lengths[1], lengths[1], lengths[2]
	case scrollMarginMaxSides:
		right, bottom, left = lengths[1], lengths[2], lengths[3]
	}

	style.ScrollMarginTop, style.ScrollMarginRight = top, right
	style.ScrollMarginBottom, style.ScrollMarginLeft = bottom, left
}

// parseScrollMarginLength parses one non-negative scroll-margin length into
// points. It reports false for unparseable and negative inputs, so the caller
// keeps the previous declaration. Reference: Chrome 143.0.7499.40 rejects
// scroll-margin-top:-10px at parse time.
func parseScrollMarginLength(value string, fsize float64) (float64, bool) {
	length, ok := parseAdvancedLengthOK(strings.ToLower(strings.TrimSpace(value)), fsize)
	if !ok || length < 0 {
		return 0, false
	}

	return length, true
}

// ScrollOffset is the scrolled position of one scrollable overflow region in
// canvas points. Positive X scrolled the content left (the user scrolled
// right); positive Y scrolled the content up. Zero is the initial position,
// which is the only position this engine lays out.
type ScrollOffset struct {
	X float64
	Y float64
}

// ScrollMargins carries one snap target's used scroll-margin outsets in
// canvas points. Zero on every side is the CSS initial value.
type ScrollMargins struct {
	Top    float64
	Right  float64
	Bottom float64
	Left   float64
}

// scrollMarginsOf reads the used scroll margins off a resolved style. A nil
// style reads as all zero, matching the CSS initial values.
func scrollMarginsOf(style *ResolvedStyle) ScrollMargins {
	var margins ScrollMargins

	if style == nil {
		return margins
	}

	margins.Top = style.ScrollMarginTop
	margins.Right = style.ScrollMarginRight
	margins.Bottom = style.ScrollMarginBottom
	margins.Left = style.ScrollMarginLeft

	return margins
}

// ScrollViewport binds one scrollable region's padding-box port to the scroll
// offset the region currently carries. The port is in canvas points; the
// offset translates it into content coordinates for snap and visibility math.
type ScrollViewport struct {
	Port clipRect
	At   ScrollOffset
}

// scrollRegionViewport returns the snapport viewport for a scrollable region
// box: its padding box paired with the offset the region carries. Production
// carries zero (no test set one), so the port matches paddingBoxOf exactly
// and static paint is unchanged; tests carry nonzero offsets through
// setTestScrollOffset in scroll_runtime.go, and snap and visibility consumers
// read the translated rect with no further changes.
func (e *engine) scrollRegionViewport(boxNode *box) ScrollViewport {
	var view ScrollViewport

	if e != nil && boxNode != nil {
		view.Port = e.paddingBoxOf(boxNode)
		view.At = testScrollOffsetOf(boxNode)
	}

	return view
}

// scrollSnapRect outsets a snap target's border box by its scroll margins,
// producing the target's scroll snap area: the margin edges, not the border
// edges, align to the snapport. Reference: Chrome 143.0.7499.40.
func scrollSnapRect(target clipRect, margins ScrollMargins) clipRect {
	return clipRect{
		x: target.x - margins.Left,
		y: target.y - margins.Top,
		w: target.w + margins.Left + margins.Right,
		h: target.h + margins.Top + margins.Bottom,
	}
}

// scrollVisibleRect returns the used visibility rect for a snap target inside
// a scrollable region: the region's port translated by the scroll offset the
// region carries, then inset by the target's scroll margins. A target whose
// margins cover the whole port reads as empty (fully out of view), matching
// the empty-intersection convention in intersectClip. With the zero offset
// this engine always carries, the rect is the port minus the margins, so
// static paint (which clips to the padding box) is unchanged. Reference:
// Chrome 143.0.7499.40.
func scrollVisibleRect(view ScrollViewport, margins ScrollMargins) clipRect {
	rect := clipRect{
		x: view.Port.x + view.At.X + margins.Left,
		y: view.Port.y + view.At.Y + margins.Top,
		w: view.Port.w - margins.Left - margins.Right,
		h: view.Port.h - margins.Top - margins.Bottom,
	}

	if rect.w < 0 {
		rect.w = 0
	}

	if rect.h < 0 {
		rect.h = 0
	}

	return rect
}
