package layout

import (
	"math"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
)

// Corner-shape store: the 34 corner* properties of
// plans/v0.0.3/corner-shape/corner-shape-checklist.md.
//
// Each corner stores a superellipse K number plus the keyword it was
// declared with, so the declared form can be tested. Keyword to K follows
// CSS Borders 4 (https://drafts.csswg.org/css-borders-4/#typedef-corner-shape-value):
// round is 1, squircle is 2, square is +Inf, bevel is 0, scoop is -1, and
// notch is -Inf. superellipse() takes a number, infinity, or -infinity.
//
// The initial shape is round on all four corners (see initialStyle in
// style.go). A corner whose keyword is empty never ran initialStyle (a
// hand-built ResolvedStyle); readers treat it as the initial round, not as
// bevel (K 0). A bad token rejects the declaration, so the previous shape
// stays, matching how applyRadiusLonghand treats bad radii.
//
// Logical names use the fixed horizontal left-to-right map from
// logicalRadiusCorner in style_logical_border.go: start-start is top-left,
// start-end is top-right, end-start is bottom-left, end-end is bottom-right.
// No writing-mode table in this wave.
//
// The pair longhands in style_corner_shape_pairs.go, the shorthands in
// style_corner_shape_shorthand.go, the side shorthands in
// style_corner_shape_side.go, and the single-corner shorthands in
// style_corner_shape_corners.go all funnel through parseCornerShapeValue and
// SetCornerShapeByName, the two entry points defined here.

// Property names owned by the corner-shape family. style_value_accept.go and
// every applier switch use these constants so a rename cannot drift.
const (
	propCornerShorthand = "corner"
	propCornerShape     = "corner-shape"

	propCornerTopLeftShape     = "corner-top-left-shape"
	propCornerTopRightShape    = "corner-top-right-shape"
	propCornerBottomRightShape = "corner-bottom-right-shape"
	propCornerBottomLeftShape  = "corner-bottom-left-shape"
	propCornerStartStartShape  = "corner-start-start-shape"
	propCornerStartEndShape    = "corner-start-end-shape"
	propCornerEndStartShape    = "corner-end-start-shape"
	propCornerEndEndShape      = "corner-end-end-shape"

	propCornerTopShape         = "corner-top-shape"
	propCornerBottomShape      = "corner-bottom-shape"
	propCornerLeftShape        = "corner-left-shape"
	propCornerRightShape       = "corner-right-shape"
	propCornerBlockStartShape  = "corner-block-start-shape"
	propCornerBlockEndShape    = "corner-block-end-shape"
	propCornerInlineStartShape = "corner-inline-start-shape"
	propCornerInlineEndShape   = "corner-inline-end-shape"

	propCornerTop         = "corner-top"
	propCornerRight       = "corner-right"
	propCornerBottom      = "corner-bottom"
	propCornerLeft        = "corner-left"
	propCornerBlockStart  = "corner-block-start"
	propCornerBlockEnd    = "corner-block-end"
	propCornerInlineStart = "corner-inline-start"
	propCornerInlineEnd   = "corner-inline-end"

	propCornerTopLeft     = "corner-top-left"
	propCornerTopRight    = "corner-top-right"
	propCornerBottomRight = "corner-bottom-right"
	propCornerBottomLeft  = "corner-bottom-left"
	propCornerStartStart  = "corner-start-start"
	propCornerStartEnd    = "corner-start-end"
	propCornerEndStart    = "corner-end-start"
	propCornerEndEnd      = "corner-end-end"
)

// Physical corner names used by the store and its readers.
const (
	cornerNameTopLeft     = "top-left"
	cornerNameTopRight    = "top-right"
	cornerNameBottomRight = "bottom-right"
	cornerNameBottomLeft  = "bottom-left"
)

// Shape keywords and the superellipse spellings of the canonical store.
const (
	cornerShapeKeywordRound    = "round"
	cornerShapeKeywordScoop    = "scoop"
	cornerShapeKeywordBevel    = "bevel"
	cornerShapeKeywordNotch    = "notch"
	cornerShapeKeywordSquare   = "square"
	cornerShapeKeywordSquircle = "squircle"

	cornerShapeSuperellipsePrefix          = "superellipse("
	cornerShapeSuperellipseInfinityKeyword = "superellipse(infinity)"
	cornerShapeSuperellipseNegInfKeyword   = "superellipse(-infinity)"
	cornerShapeInfinity                    = "infinity"
	cornerShapeNegInfinity                 = "-infinity"
)

// Numeric K values of the shape keywords. CornerShapeRoundK (1) lives in
// corner_path.go; square (+Inf) and notch (-Inf) are not constants.
const (
	cornerShapeSquircleK float64 = 2
	cornerShapeBevelK    float64 = 0
	cornerShapeScoopK    float64 = -1
)

// Value-count bounds shared by the 1-4 token producers.
const (
	cornerShapePairValues   = 2
	cornerShapeTripleValues = 3
	cornerShapeQuadValues   = 4
)

// parseCornerShapeValue parses one <corner-shape-value> into its K number and
// canonical keyword. ok is false for anything else, including lengths, so a
// radius token never passes as a shape.
func parseCornerShapeValue(raw string) (float64, string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, "", false
	}

	switch strings.ToLower(trimmed) {
	case cornerShapeKeywordRound:
		return CornerShapeRoundK, cornerShapeKeywordRound, true
	case cornerShapeKeywordSquircle:
		return cornerShapeSquircleK, cornerShapeKeywordSquircle, true
	case cornerShapeKeywordSquare:
		return math.Inf(1), cornerShapeKeywordSquare, true
	case cornerShapeKeywordBevel:
		return cornerShapeBevelK, cornerShapeKeywordBevel, true
	case cornerShapeKeywordScoop:
		return cornerShapeScoopK, cornerShapeKeywordScoop, true
	case cornerShapeKeywordNotch:
		return math.Inf(-1), cornerShapeKeywordNotch, true
	}

	return parseCornerShapeSuperellipse(trimmed)
}

// parseCornerShapeSuperellipse parses superellipse(<number> | infinity |
// -infinity). The function name match is case insensitive. A + sign before
// infinity is tolerated. NaN is rejected.
func parseCornerShapeSuperellipse(token string) (float64, string, bool) {
	lower := strings.ToLower(strings.TrimSpace(token))

	if !strings.HasPrefix(lower, cornerShapeSuperellipsePrefix) || !strings.HasSuffix(lower, ")") {
		return 0, "", false
	}

	inner := strings.TrimSpace(lower[len(cornerShapeSuperellipsePrefix) : len(lower)-1])
	if inner == "" || strings.ContainsAny(inner, ",()") {
		return 0, "", false
	}

	switch strings.TrimPrefix(inner, "+") {
	case cornerShapeInfinity:
		return math.Inf(1), cornerShapeSuperellipseInfinityKeyword, true
	case cornerShapeNegInfinity:
		return math.Inf(-1), cornerShapeSuperellipseNegInfKeyword, true
	}

	number, parsed := css.ParseNumber(inner)
	if !parsed || math.IsNaN(number) {
		return 0, "", false
	}

	return number, cornerShapeSuperellipsePrefix + inner + ")", true
}

// cornerShapeValueAccepted mirrors parseCornerShapeValue for the cascade gate
// (supportedDeclaration) and the @supports probe, so a bad token never wins
// the cascade over an earlier valid declaration.
func cornerShapeValueAccepted(value string) bool {
	_, _, ok := parseCornerShapeValue(value)

	return ok
}

// SetCornerShapeByName stores one parsed corner shape on a physical corner:
// top-left, top-right, bottom-right, or bottom-left. kValue is the
// superellipse K and keyword is the canonical declared form for tests. An
// unknown corner name stores nothing. The signature matches
// CornerShapePairSetter from style_corner_shape_pairs.go so the pair
// longhands wire to this store.
func SetCornerShapeByName(style *ResolvedStyle, corner string, kValue float64, keyword string) {
	switch corner {
	case cornerNameTopLeft:
		style.CornerShapeTopLeft = kValue
		style.CornerShapeTopLeftKeyword = keyword
	case cornerNameTopRight:
		style.CornerShapeTopRight = kValue
		style.CornerShapeTopRightKeyword = keyword
	case cornerNameBottomRight:
		style.CornerShapeBottomRight = kValue
		style.CornerShapeBottomRightKeyword = keyword
	case cornerNameBottomLeft:
		style.CornerShapeBottomLeft = kValue
		style.CornerShapeBottomLeftKeyword = keyword
	}
}

// cornerShapeLonghandCorner maps a single-corner shape longhand name
// (physical or logical on the fixed LTR map) to its physical corner.
func cornerShapeLonghandCorner(prop string) (string, bool) {
	switch prop {
	case propCornerTopLeftShape, propCornerStartStartShape:
		return cornerNameTopLeft, true
	case propCornerTopRightShape, propCornerStartEndShape:
		return cornerNameTopRight, true
	case propCornerBottomRightShape, propCornerEndEndShape:
		return cornerNameBottomRight, true
	case propCornerBottomLeftShape, propCornerEndStartShape:
		return cornerNameBottomLeft, true
	default:
		return "", false
	}
}

// applyCornerShapeLonghand stores one single-corner shape longhand. Physical
// names write that corner; logical names use the fixed map from
// logicalRadiusCorner. It reports true for an owned property even when the
// value is bad, which leaves the previous declaration.
func applyCornerShapeLonghand(style *ResolvedStyle, prop, value string) bool {
	corner, owned := cornerShapeLonghandCorner(prop)
	if !owned {
		return false
	}

	kValue, keyword, ok := parseCornerShapeValue(value)
	if !ok {
		return true
	}

	SetCornerShapeByName(style, corner, kValue, keyword)

	return true
}

// cornerShapeStored returns one stored corner shape K. An empty keyword means
// the store never ran, so the corner keeps the initial round.
func cornerShapeStored(kValue float64, keyword string) float64 {
	if keyword == "" {
		return CornerShapeRoundK
	}

	return kValue
}

// cornerShapeValues returns the four corner K values in top-left, top-right,
// bottom-right, bottom-left order, the order usedBorderRadiiXY uses. A
// corner with no declared keyword (hand-built style, no initialStyle) reads
// as the initial round.
func cornerShapeValues(style *ResolvedStyle) [4]float64 {
	if style == nil {
		return [4]float64{CornerShapeRoundK, CornerShapeRoundK, CornerShapeRoundK, CornerShapeRoundK}
	}

	topLeft := cornerShapeStored(style.CornerShapeTopLeft, style.CornerShapeTopLeftKeyword)
	topRight := cornerShapeStored(style.CornerShapeTopRight, style.CornerShapeTopRightKeyword)
	bottomRight := cornerShapeStored(style.CornerShapeBottomRight, style.CornerShapeBottomRightKeyword)
	bottomLeft := cornerShapeStored(style.CornerShapeBottomLeft, style.CornerShapeBottomLeftKeyword)

	return [4]float64{topLeft, topRight, bottomRight, bottomLeft}
}

// borderRadiusFields returns the X and Y border-radius fields for one
// physical corner name, or ok false for an unknown name.
func borderRadiusFields(style *ResolvedStyle, corner string) (*float64, *float64, bool) {
	switch corner {
	case cornerNameTopLeft:
		return &style.BorderRadiusTopLeft, &style.BorderRadiusTopLeftY, true
	case cornerNameTopRight:
		return &style.BorderRadiusTopRight, &style.BorderRadiusTopRightY, true
	case cornerNameBottomRight:
		return &style.BorderRadiusBottomRight, &style.BorderRadiusBottomRightY, true
	case cornerNameBottomLeft:
		return &style.BorderRadiusBottomLeft, &style.BorderRadiusBottomLeftY, true
	default:
		return nil, nil, false
	}
}
