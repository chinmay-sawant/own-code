package layout

// One-or-two-value corner-shape longhands: checklist rows 80-87.
//
// Each property takes one or two <corner-shape-value> tokens. One value sets
// both corners. Two values assign first then second corner in the order named
// on each function below. A bad token or a third token rejects the whole
// declaration, so the previous shape stays.
//
// This is not the border-top-radius two-token split. setCornerRadius in
// style_paint_props.go reads two tokens as the x and y radii of one corner.
// Here two tokens are the shapes of two corners.
//
// Parsing is parseCornerShapeValue from style_corner_shape.go: round is 1,
// squircle is 2, square is +Inf, bevel is 0, scoop is -1, notch is -Inf, and
// superellipse() takes a number, infinity, or -infinity.
//
// Logical pairs use the fixed horizontal left-to-right map from
// logicalRadiusCorner in style_logical_border.go: start-start is top-left,
// start-end is top-right, end-start is bottom-left, end-end is bottom-right.
// No writing-mode table in this wave.
//
// Storage is SetCornerShapeByName in style_corner_shape.go. This file only
// parses and expands pairs, then reports each corner through
// CornerShapePairSetter.

// CornerShapePairSetter stores one parsed corner shape. corner is one of the
// physical names above, kValue is the superellipse K, and keyword is the
// canonical declared form for tests.
type CornerShapePairSetter func(style *ResolvedStyle, corner string, kValue float64, keyword string)

// splitCornerShapePairTokens splits a one-or-two-value shape declaration into
// its per-corner tokens. One value sets both corners, so the second return
// repeats the first. Tokens split on spaces with paren awareness, so a
// superellipse with inner spaces stays one token.
func splitCornerShapePairTokens(value string) (string, string, bool) {
	var tokens [3]string

	count := 0

	for start := 0; ; {
		token, next, ok := nextSpaceToken(value, start)
		if !ok {
			break
		}

		if count < len(tokens) {
			tokens[count] = token
		}

		count++
		start = next
	}

	if count == 0 || count > cornerShapePairValues {
		return "", "", false
	}

	if count == 1 {
		return tokens[0], tokens[0], true
	}

	return tokens[0], tokens[1], true
}

// applyCornerShapePairTokens parses value as one or two shape values and, only
// when both parse, stores the first on firstCorner and the second on
// secondCorner. A bad token stores nothing. A nil set only validates.
func applyCornerShapePairTokens(
	style *ResolvedStyle, value, firstCorner, secondCorner string, set CornerShapePairSetter,
) bool {
	firstTok, secondTok, valid := splitCornerShapePairTokens(value)
	if !valid {
		return false
	}

	firstK, firstKeyword, parsed := parseCornerShapeValue(firstTok)
	if !parsed {
		return false
	}

	secondK, secondKeyword, parsed := parseCornerShapeValue(secondTok)
	if !parsed {
		return false
	}

	if set != nil {
		set(style, firstCorner, firstK, firstKeyword)
		set(style, secondCorner, secondK, secondKeyword)
	}

	return true
}

// ApplyCornerTopShapePair sets top-left then top-right.
func ApplyCornerTopShapePair(style *ResolvedStyle, value string, set CornerShapePairSetter) bool {
	return applyCornerShapePairTokens(style, value, cornerNameTopLeft, cornerNameTopRight, set)
}

// ApplyCornerBottomShapePair sets bottom-left then bottom-right.
func ApplyCornerBottomShapePair(style *ResolvedStyle, value string, set CornerShapePairSetter) bool {
	return applyCornerShapePairTokens(style, value, cornerNameBottomLeft, cornerNameBottomRight, set)
}

// ApplyCornerLeftShapePair sets top-left then bottom-left.
func ApplyCornerLeftShapePair(style *ResolvedStyle, value string, set CornerShapePairSetter) bool {
	return applyCornerShapePairTokens(style, value, cornerNameTopLeft, cornerNameBottomLeft, set)
}

// ApplyCornerRightShapePair sets top-right then bottom-right.
func ApplyCornerRightShapePair(style *ResolvedStyle, value string, set CornerShapePairSetter) bool {
	return applyCornerShapePairTokens(style, value, cornerNameTopRight, cornerNameBottomRight, set)
}

// ApplyCornerBlockStartShapePair sets start-start then start-end, which is
// top-left then top-right on the fixed map.
func ApplyCornerBlockStartShapePair(style *ResolvedStyle, value string, set CornerShapePairSetter) bool {
	return applyCornerShapePairTokens(style, value, cornerNameTopLeft, cornerNameTopRight, set)
}

// ApplyCornerBlockEndShapePair sets end-start then end-end, which is
// bottom-left then bottom-right on the fixed map.
func ApplyCornerBlockEndShapePair(style *ResolvedStyle, value string, set CornerShapePairSetter) bool {
	return applyCornerShapePairTokens(style, value, cornerNameBottomLeft, cornerNameBottomRight, set)
}

// ApplyCornerInlineStartShapePair sets start-start then end-start, which is
// top-left then bottom-left on the fixed map.
func ApplyCornerInlineStartShapePair(style *ResolvedStyle, value string, set CornerShapePairSetter) bool {
	return applyCornerShapePairTokens(style, value, cornerNameTopLeft, cornerNameBottomLeft, set)
}

// ApplyCornerInlineEndShapePair sets start-end then end-end, which is
// top-right then bottom-right on the fixed map.
func ApplyCornerInlineEndShapePair(style *ResolvedStyle, value string, set CornerShapePairSetter) bool {
	return applyCornerShapePairTokens(style, value, cornerNameTopRight, cornerNameBottomRight, set)
}

// ApplyCornerShapePairProp dispatches one of the 8 pair longhands. It reports
// true for an owned property even when the value is bad, which leaves the
// previous declaration, matching how applyRadiusLonghand treats bad radii.
func ApplyCornerShapePairProp(
	style *ResolvedStyle, prop, value string, set CornerShapePairSetter,
) bool {
	switch prop {
	case propCornerTopShape:
		ApplyCornerTopShapePair(style, value, set)
	case propCornerBottomShape:
		ApplyCornerBottomShapePair(style, value, set)
	case propCornerLeftShape:
		ApplyCornerLeftShapePair(style, value, set)
	case propCornerRightShape:
		ApplyCornerRightShapePair(style, value, set)
	case propCornerBlockStartShape:
		ApplyCornerBlockStartShapePair(style, value, set)
	case propCornerBlockEndShape:
		ApplyCornerBlockEndShapePair(style, value, set)
	case propCornerInlineStartShape:
		ApplyCornerInlineStartShapePair(style, value, set)
	case propCornerInlineEndShape:
		ApplyCornerInlineEndShapePair(style, value, set)
	default:
		return false
	}

	return true
}

// cornerShapePairValueAccepted mirrors applyCornerShapePairTokens for the
// cascade gate and @supports probe: one or two valid shape tokens.
func cornerShapePairValueAccepted(value string) bool {
	firstTok, secondTok, valid := splitCornerShapePairTokens(value)
	if !valid {
		return false
	}

	if _, _, ok := parseCornerShapeValue(firstTok); !ok {
		return false
	}

	_, _, ok := parseCornerShapeValue(secondTok)

	return ok
}
