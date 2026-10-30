package layout

import (
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
)

// Single-corner shorthands for the corner-shape wave
// (plans/v0.0.3/corner-shape/corner-shape-checklist.md, phase 1, 8 rows).
//
// Each shorthand sets one corner's radius piece and shape piece:
// corner-top-left, corner-top-right, corner-bottom-right,
// corner-bottom-left, corner-start-start, corner-start-end,
// corner-end-start, corner-end-end. Catalog grammar per row is
// <'border-top-left-radius'> || <'corner-top-left-shape'>: a radius piece,
// a shape piece, or both in any order. A missing piece resets to its
// initial value (radius 0, shape round), the way a border shorthand resets
// omitted parts. An invalid value leaves the previous declaration alone.
//
// Radius piece: calls the existing setCornerRadius setter on the existing
// BorderRadius* fields. There is no second radius store. Logical rows
// resolve to the same physical corner through the fixed left-to-right map
// (start-start is top-left), mirroring style_logical_border.go. Shape
// piece: parses the shared K contract through parseCornerShapeValue and
// stores through SetCornerShapeByName, the canonical store in
// style_corner_shape.go; an absent declaration reads as the initial round.
//
// Integration: applyCornerSingleShorthand is called from the border
// property group; it returns true only for these 8 names with a value that
// parses. layout.go is untouched.

// applyCornerSingleShorthand applies one single-corner shorthand
// (corner-top-left and its 7 siblings). It reports false when prop is not
// one of the 8 names or the value matches neither piece, so the
// declaration is ignored and the previous store is kept.
func applyCornerSingleShorthand(style *ResolvedStyle, prop, value string, fsize float64) bool {
	corner, owned := cornerSingleTarget(prop)
	if !owned || style == nil {
		return false
	}

	radiusText, keyword, hasRadius, hasShape := cornerSingleSplit(value, fsize)
	if !hasRadius && !hasShape {
		return false
	}

	destX, destY, ok := borderRadiusFields(style, corner)
	if !ok {
		return false
	}

	if hasRadius {
		setCornerRadius(style, destX, destY, radiusText, fsize)
	} else {
		setCornerRadius(style, destX, destY, "0", fsize)
	}

	if hasShape {
		cornerSingleStoreShape(style, corner, keyword)
	} else {
		cornerSingleResetShape(style, corner)
	}

	return true
}

// cornerSingleTarget maps a shorthand name onto its physical corner.
// Logical rows resolve through the same fixed left-to-right map as
// logicalRadiusCorner: start-start is top-left, start-end is top-right,
// end-start is bottom-left, end-end is bottom-right.
func cornerSingleTarget(prop string) (string, bool) {
	switch prop {
	case propCornerTopLeft, propCornerStartStart:
		return cornerNameTopLeft, true
	case propCornerTopRight, propCornerStartEnd:
		return cornerNameTopRight, true
	case propCornerBottomRight, propCornerEndEnd:
		return cornerNameBottomRight, true
	case propCornerBottomLeft, propCornerEndStart:
		return cornerNameBottomLeft, true
	default:
		return "", false
	}
}

// cornerSingleSplit partitions a shorthand value into its radius piece and
// shape piece. At most one token may parse as a shape value; two shapes is
// invalid. With no shape token the whole value must be a radius (a
// radius-only declaration resets the shape to round). With one shape token
// the remaining tokens are the radius piece; with none remaining it is a
// shape-only declaration (the radius resets to 0). Anything else leaves
// both has flags false so the caller keeps the previous declaration.
func cornerSingleSplit(value string, fsize float64) (string, string, bool, bool) {
	var tokens [cornerShapeQuadValues]string

	count := splitSpaceTokens(value, tokens[:])
	if count == 0 || count > len(tokens) {
		return "", "", false, false
	}

	shapeIdx, keyword, shapesOK := cornerSingleShapeIndex(tokens[:count])
	if !shapesOK {
		return "", "", false, false
	}

	if shapeIdx < 0 {
		return cornerSingleRadiusOnly(value, fsize)
	}

	return cornerSingleRadiusAndShape(tokens[:count], shapeIdx, keyword, fsize)
}

// cornerSingleShapeIndex finds the single shape token among tokens. ok is
// false when two tokens parse as shapes; shapeIdx is -1 when none do.
func cornerSingleShapeIndex(tokens []string) (int, string, bool) {
	shapeIdx := -1
	keyword := ""

	for idx := range tokens {
		_, shapeKeyword, ok := parseCornerShapeValue(tokens[idx])
		if !ok {
			continue
		}

		if shapeIdx >= 0 {
			return 0, "", false
		}

		shapeIdx = idx
		keyword = shapeKeyword
	}

	return shapeIdx, keyword, true
}

// cornerSingleRadiusOnly parses value as a radius-only declaration: the shape
// piece resets to round.
func cornerSingleRadiusOnly(value string, fsize float64) (string, string, bool, bool) {
	radiusText := strings.TrimSpace(value)
	if !cornerSingleRadiusOK(radiusText, fsize) {
		return "", "", false, false
	}

	return radiusText, "", true, false
}

// cornerSingleRadiusAndShape joins the non-shape tokens into the radius
// piece and returns it with the shape keyword. With no radius tokens left
// the declaration is shape-only and the radius resets to 0.
func cornerSingleRadiusAndShape(
	tokens []string, shapeIdx int, keyword string, fsize float64,
) (string, string, bool, bool) {
	rest := make([]string, 0, len(tokens)-1)

	for idx := range tokens {
		if idx != shapeIdx {
			rest = append(rest, tokens[idx])
		}
	}

	if len(rest) == 0 {
		return "", keyword, false, true
	}

	radiusText := strings.Join(rest, " ")

	if !cornerSingleRadiusOK(radiusText, fsize) {
		return "", "", false, false
	}

	return radiusText, keyword, true, true
}

// cornerSingleRadiusOK mirrors what setCornerRadius honors for the X token:
// a non-negative percent, or a non-negative length. The Y token keeps the
// setter's leniency (a bad Y is ignored, not fatal), so only X is checked.
func cornerSingleRadiusOK(radiusText string, fsize float64) bool {
	rxTok, _, _ := splitCornerRadiusTokens(radiusText)
	if rxTok == "" {
		return false
	}

	if percent, unit, parsed := css.ParseLength(rxTok); parsed && unit == "%" {
		return percent >= 0
	}

	radius, ok := lengthBox(rxTok, fsize, 0, cssDisplayNone)

	return ok && radius >= 0
}

// cornerSingleStoreShape records one corner's shape through the canonical
// store.
func cornerSingleStoreShape(style *ResolvedStyle, corner string, keyword string) {
	kValue, normalized, ok := parseCornerShapeValue(keyword)
	if !ok {
		return
	}

	SetCornerShapeByName(style, corner, kValue, normalized)
}

// cornerSingleResetShape returns one corner to the initial round.
func cornerSingleResetShape(style *ResolvedStyle, corner string) {
	SetCornerShapeByName(style, corner, CornerShapeRoundK, cornerShapeKeywordRound)
}

// cornerSingleShorthandValueAccepted mirrors applyCornerSingleShorthand for
// the cascade gate and the @supports probe: a radius piece, one shape piece,
// or both, each valid. The probe context uses font size 0; length parsing
// does not depend on it.
func cornerSingleShorthandValueAccepted(value string) bool {
	_, _, hasRadius, hasShape := cornerSingleSplit(value, 0)

	return hasRadius || hasShape
}
