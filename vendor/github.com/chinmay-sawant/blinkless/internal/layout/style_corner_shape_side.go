package layout

import (
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
)

// Phase 1 side shorthands for corner shape. Ledger:
// plans/v0.0.3/corner-shape/corner-shape-checklist.md, "Shorthands that
// also set a radius".
//
// Owned names (9): corner-top, corner-right, corner-bottom, corner-left,
// corner-block-start, corner-block-end, corner-inline-start,
// corner-inline-end. Each grammar is a radius piece, a shape piece, or
// both in any order (<'border-*-radius'> || <'corner-*-shape'>). The
// ledger repeats the top row's grammar text for every side row; each side
// applies its own radius and shape pair, not the top pair.
//
// A missing piece resets that piece to its initial on the side's corners
// (0 radius, round shape), the same way a border shorthand resets omitted
// parts. An invalid token rejects the whole declaration and leaves the
// previous values in place; nothing is half applied.
//
// Corner pairs follow the Phase 1 pair rules: one shape value sets both
// corners, and two values run left then right on the top and bottom
// sides, top then bottom on the left and right sides. Logical sides use
// the fixed left-to-right map already hardcoded for border radii
// (style_logical_border.go): block-start is the top side, block-end the
// bottom side, inline-start the left side, inline-end the right side.
// This wave adds no writing-mode table.
//
// Shape storage is the canonical one in style_corner_shape.go: the
// per-corner fields on ResolvedStyle written through SetCornerShapeByName.
// A missing declaration reads as the initial round.
//
// Wiring: applyCornerSideShorthand is called from applyBorderGroup in
// style_properties.go next to applyCornerShapeLonghand.

// applyCornerSideShorthand owns the nine corner-<side> shorthands. It
// reports false for names it does not own. An owned name always reports
// true; an invalid value leaves the previous declaration in place.
func applyCornerSideShorthand(style *ResolvedStyle, prop, value string, fsize float64) bool {
	first, second, owned := cornerSideCorners(prop)
	if !owned {
		return false
	}

	radiusPiece, shapeToks := splitCornerSidePieces(value)

	shapeKs, shapeKeywords, shapesOK := cornerSideShapes(shapeToks)
	if !shapesOK {
		return true
	}

	if radiusPiece != "" && !validCornerSideRadiusPiece(radiusPiece, fsize) {
		return true
	}

	firstX, firstY, secondX, secondY, targetsOK := cornerSideRadiusTargets(style, prop)
	if !targetsOK {
		return true
	}

	if radiusPiece == "" {
		setCornerRadius(style, firstX, firstY, "0", fsize)
		setCornerRadius(style, secondX, secondY, "0", fsize)
	} else {
		setCornerRadius(style, firstX, firstY, radiusPiece, fsize)
		setCornerRadius(style, secondX, secondY, radiusPiece, fsize)
	}

	cornerSideApplyShapes(style, first, second, shapeKs, shapeKeywords)

	return true
}

// cornerSideShapes validates and parses the shape tokens of a side
// shorthand: none, one, or two, each a <corner-shape-value>. ok is false
// when a token is invalid or three or more tokens are present, so the
// whole declaration is rejected and the previous values stay.
func cornerSideShapes(shapeToks []string) ([]float64, []string, bool) {
	if len(shapeToks) > cornerShapePairValues {
		return nil, nil, false
	}

	kValues := make([]float64, 0, len(shapeToks))
	keywords := make([]string, 0, len(shapeToks))

	for _, token := range shapeToks {
		kValue, keyword, ok := parseCornerShapeValue(token)
		if !ok {
			return nil, nil, false
		}

		kValues = append(kValues, kValue)
		keywords = append(keywords, keyword)
	}

	return kValues, keywords, true
}

// cornerSideApplyShapes writes the parsed side shape pair: no tokens reset
// both corners to round, one value sets both, and two values set first
// then second.
func cornerSideApplyShapes(
	style *ResolvedStyle, first, second string, kValues []float64, keywords []string,
) {
	switch len(kValues) {
	case 0:
		SetCornerShapeByName(style, first, CornerShapeRoundK, cornerShapeKeywordRound)
		SetCornerShapeByName(style, second, CornerShapeRoundK, cornerShapeKeywordRound)
	case 1:
		SetCornerShapeByName(style, first, kValues[0], keywords[0])
		SetCornerShapeByName(style, second, kValues[0], keywords[0])
	default:
		SetCornerShapeByName(style, first, kValues[0], keywords[0])
		SetCornerShapeByName(style, second, kValues[1], keywords[1])
	}
}

// cornerSideCorners maps a side shorthand to its two physical corners in
// pair order: left then right on top and bottom sides, top then bottom on
// left and right sides. Logical sides use the fixed left-to-right map.
func cornerSideCorners(prop string) (string, string, bool) {
	switch prop {
	case propCornerTop, propCornerBlockStart:
		return cornerNameTopLeft, cornerNameTopRight, true
	case propCornerRight, propCornerInlineEnd:
		return cornerNameTopRight, cornerNameBottomRight, true
	case propCornerBottom, propCornerBlockEnd:
		return cornerNameBottomLeft, cornerNameBottomRight, true
	case propCornerLeft, propCornerInlineStart:
		return cornerNameTopLeft, cornerNameBottomLeft, true
	default:
		return "", "", false
	}
}

// cornerSideRadiusTargets maps a side shorthand to the two corner radius
// field pairs its radius piece writes, reusing the same fixed corner map
// as the shape pair.
func cornerSideRadiusTargets(
	style *ResolvedStyle, prop string,
) (*float64, *float64, *float64, *float64, bool) {
	first, second, ok := cornerSideCorners(prop)
	if !ok {
		return nil, nil, nil, nil, false
	}

	firstX, firstY, ok1 := borderRadiusFields(style, first)
	secondX, secondY, ok2 := borderRadiusFields(style, second)

	if !ok1 || !ok2 {
		return nil, nil, nil, nil, false
	}

	return firstX, firstY, secondX, secondY, true
}

// splitCornerSidePieces partitions a side shorthand value into its radius
// piece (rejoined with single spaces, original order kept) and its shape
// tokens. Every token lands in exactly one piece: shape-looking tokens go
// to the shape piece and everything else, including "/" and junk, stays
// in the radius piece so validation can reject it as a whole.
func splitCornerSidePieces(value string) (string, []string) {
	var radiusToks, shapeToks []string

	for start := 0; ; {
		token, next, ok := nextSpaceToken(value, start)
		if !ok {
			break
		}

		if isCornerSideShapeToken(token) {
			shapeToks = append(shapeToks, token)
		} else {
			radiusToks = append(radiusToks, token)
		}

		start = next
	}

	return strings.Join(radiusToks, " "), shapeToks
}

// isCornerSideShapeToken reports whether a token parses as a shape value.
// Radius tokens (lengths, percentages, "/") do not.
func isCornerSideShapeToken(token string) bool {
	_, _, ok := parseCornerShapeValue(token)

	return ok
}

// validCornerSideRadiusPiece mirrors what setCornerRadius accepts for one
// corner value: a non-negative percent, or a non-negative length. A bad
// second axis is dropped by the setter, so it does not fail validation;
// extra tokens past the second are dropped the same way.
func validCornerSideRadiusPiece(piece string, fsize float64) bool {
	rxTok, _, _ := splitCornerRadiusTokens(piece)
	if rxTok == "" {
		return false
	}

	if percent, unit, parsed := css.ParseLength(rxTok); parsed && unit == "%" {
		return percent >= 0
	}

	radius, ok := lengthBox(rxTok, fsize, 0, cssDisplayNone)

	return ok && radius >= 0
}

// cornerSideShorthandValueAccepted mirrors applyCornerSideShorthand for the
// cascade gate and the @supports probe: one or two shape tokens, at most one
// radius piece, and a valid radius when present. The probe context uses font
// size 0; length parsing does not depend on it.
func cornerSideShorthandValueAccepted(value string) bool {
	radiusPiece, shapeToks := splitCornerSidePieces(value)
	if len(shapeToks) > cornerShapePairValues {
		return false
	}

	for _, token := range shapeToks {
		if _, _, ok := parseCornerShapeValue(token); !ok {
			return false
		}
	}

	if radiusPiece != "" && !validCornerSideRadiusPiece(radiusPiece, 0) {
		return false
	}

	return radiusPiece != "" || len(shapeToks) > 0
}
