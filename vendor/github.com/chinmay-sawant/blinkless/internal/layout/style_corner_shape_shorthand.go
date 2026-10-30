package layout

import (
	"strings"
)

// Four-corner and top-level corner shorthands: checklist Phase 1 rows
// `corner-shape` and `corner`.
//
// `corner-shape` takes one to four <corner-shape-value> tokens. The four
// values shape the top-left, top-right, bottom-right, and bottom-left
// corners in that order, and the missing-value copy rules match border-radius:
// a missing bottom copies top, a missing right copies top, and a missing left
// copies right. Chrome 155 resolves `corner-shape: scoop bevel notch squircle`
// as top-left scoop, top-right bevel, bottom-right notch, bottom-left
// squircle, which is this expansion (the side language in css-borders-4
// section 3.8.2 names each side's value at the corner where the side starts:
// top at top-left, right at top-right, bottom at bottom-right, left at
// bottom-left).
//
// `corner` takes a radius piece, a shape piece, or both in any order
// (<'border-radius'> || <'corner-shape'>). It sets all four radii and all
// four shapes. A missing piece resets that piece to the initial value
// (zero radius, round shape), the same way a border shorthand resets
// omitted parts. The radius piece goes through the existing setter; no
// second radius store lives here.
//
// Shape parsing and storage are style_corner_shape.go's: parseCornerShapeValue
// maps round to 1, squircle to 2, square to +Inf, bevel to 0, scoop to -1,
// notch to -Inf, and superellipse() to its number (infinity/-infinity at
// the ends). SetCornerShapeByName writes one corner's K plus its keyword.
// This file only expands the value list and splits the `corner` pieces,
// then calls those.
//
// An invalid value owns its property name but stores nothing, so the
// previous declaration stays. Validation runs before any write: a bad
// token anywhere rejects the whole declaration, never half of it.
func applyCornerShapeShorthand(style *ResolvedStyle, prop, value string, fsize float64) bool {
	switch prop {
	case propCornerShape:
		applyCornerShapeSides(style, value)
	case propCornerShorthand:
		applyCornerTopLevel(style, value, fsize)
	default:
		return false
	}

	return true
}

// cornerShapeValue is one parsed shape value: the superellipse K plus the
// canonical keyword for the declared-form tests.
type cornerShapeValue struct {
	k       float64
	keyword string
}

// applyCornerShapeSides stores one `corner-shape` declaration. Anything
// but one to four valid shape tokens stores nothing.
func applyCornerShapeSides(style *ResolvedStyle, value string) {
	corners, ok := expandCornerShapeSides(value)
	if !ok {
		return
	}

	writeCornerShapeCorners(style, corners)
}

// writeCornerShapeCorners assigns the four corner values in top-left,
// top-right, bottom-right, bottom-left order.
func writeCornerShapeCorners(style *ResolvedStyle, corners [cornerShapeQuadValues]cornerShapeValue) {
	SetCornerShapeByName(style, cornerNameTopLeft, corners[0].k, corners[0].keyword)
	SetCornerShapeByName(style, cornerNameTopRight, corners[1].k, corners[1].keyword)
	SetCornerShapeByName(style, cornerNameBottomRight, corners[2].k, corners[2].keyword)
	SetCornerShapeByName(style, cornerNameBottomLeft, corners[3].k, corners[3].keyword)
}

// expandCornerShapeSides parses one `corner-shape` value into its four
// corner values in top-left, top-right, bottom-right, bottom-left order.
// Tokens split paren-aware (nextSpaceToken keeps superellipse() with inner
// spaces whole). ok is false for zero tokens, more than four, or any token
// that is not a shape value.
func expandCornerShapeSides(value string) ([cornerShapeQuadValues]cornerShapeValue, bool) {
	var toks []string

	for start := 0; ; {
		token, next, has := nextSpaceToken(value, start)
		if !has {
			break
		}

		toks = append(toks, token)
		start = next
	}

	return expandCornerShapeTokens(toks)
}

// expandCornerShapeTokens parses already-split shape tokens into the four
// corner values with the border-radius copy rules: one value sets all four
// corners, two values set top-left and bottom-right then top-right and
// bottom-left, three values set top-left, top-right and bottom-left, then
// bottom-right. ok is false unless one to four tokens are present and every
// token parses as a shape value.
func expandCornerShapeTokens(toks []string) ([cornerShapeQuadValues]cornerShapeValue, bool) {
	var empty [cornerShapeQuadValues]cornerShapeValue

	if len(toks) == 0 || len(toks) > cornerShapeQuadValues {
		return empty, false
	}

	parsed := make([]cornerShapeValue, 0, len(toks))

	for _, token := range toks {
		kValue, keyword, valid := parseCornerShapeValue(token)
		if !valid {
			return empty, false
		}

		parsed = append(parsed, cornerShapeValue{k: kValue, keyword: keyword})
	}

	corners := [cornerShapeQuadValues]cornerShapeValue{parsed[0], parsed[0], parsed[0], parsed[0]}

	if len(parsed) > 1 {
		corners[cornerShapePairValues-1] = parsed[1]
		corners[cornerShapeQuadValues-1] = parsed[1]
	}

	if len(parsed) > cornerShapePairValues {
		corners[cornerShapeTripleValues-1] = parsed[cornerShapePairValues]
	}

	if len(parsed) > cornerShapeTripleValues {
		corners[cornerShapeQuadValues-1] = parsed[cornerShapeTripleValues]
	}

	return corners, true
}

// applyCornerTopLevel stores one `corner` declaration: a radius piece, a
// shape piece of one to four tokens, or both. Either piece missing resets
// that piece to the initial value on all four corners. A token that is
// neither a shape value nor part of a valid radius rejects the whole
// declaration, as do more than four shape tokens.
func applyCornerTopLevel(style *ResolvedStyle, value string, fsize float64) {
	radiusPiece, shapeToks, ok := splitCornerTopLevelPieces(value)
	if !ok || len(shapeToks) > cornerShapeQuadValues {
		return
	}

	corners, shapesOK := expandCornerShapeTokens(shapeToks)
	if len(shapeToks) > 0 && !shapesOK {
		return
	}

	if radiusPiece != "" && !borderRadiusValueAccepted(radiusPiece) {
		return
	}

	if radiusPiece == "" {
		setBorderRadius(style, "0", fsize)
	} else {
		setBorderRadius(style, radiusPiece, fsize)
	}

	if len(shapeToks) == 0 {
		resetCornerShapeRound(style)
	} else {
		writeCornerShapeCorners(style, corners)
	}
}

// resetCornerShapeRound restores the initial shape on all four corners:
// round, K = 1.
func resetCornerShapeRound(style *ResolvedStyle) {
	SetCornerShapeByName(style, cornerNameTopLeft, CornerShapeRoundK, cornerShapeKeywordRound)
	SetCornerShapeByName(style, cornerNameTopRight, CornerShapeRoundK, cornerShapeKeywordRound)
	SetCornerShapeByName(style, cornerNameBottomRight, CornerShapeRoundK, cornerShapeKeywordRound)
	SetCornerShapeByName(style, cornerNameBottomLeft, CornerShapeRoundK, cornerShapeKeywordRound)
}

// splitCornerTopLevelPieces partitions a `corner` value into its radius
// piece (rejoined with single spaces, original order kept) and its shape
// tokens. Every token lands in exactly one piece: tokens that parse as a
// shape value go to the shape piece, everything else, including "/" and
// junk, stays in the radius piece so radius validation rejects it as a
// whole. ok is false when the value holds no token at all.
func splitCornerTopLevelPieces(value string) (string, []string, bool) {
	var radiusToks, shapeToks []string

	for start := 0; ; {
		token, next, has := nextSpaceToken(value, start)
		if !has {
			break
		}

		if _, _, valid := parseCornerShapeValue(token); valid {
			shapeToks = append(shapeToks, token)
		} else {
			radiusToks = append(radiusToks, token)
		}

		start = next
	}

	if len(radiusToks) == 0 && len(shapeToks) == 0 {
		return "", nil, false
	}

	return strings.Join(radiusToks, " "), shapeToks, true
}

// cornerShapeSidesValueAccepted mirrors expandCornerShapeSides for the
// cascade gate (supportedDeclaration) and the @supports probe, so a bad
// token never wins the cascade over an earlier valid declaration.
func cornerShapeSidesValueAccepted(value string) bool {
	_, ok := expandCornerShapeSides(value)

	return ok
}

// cornerTopLevelValueAccepted mirrors applyCornerTopLevel for the cascade
// gate and the @supports probe.
func cornerTopLevelValueAccepted(value string) bool {
	radiusPiece, shapeToks, ok := splitCornerTopLevelPieces(value)
	if !ok || len(shapeToks) > cornerShapeQuadValues {
		return false
	}

	if _, shapesOK := expandCornerShapeTokens(shapeToks); len(shapeToks) > 0 && !shapesOK {
		return false
	}

	if radiusPiece == "" {
		return true
	}

	return borderRadiusValueAccepted(radiusPiece)
}
