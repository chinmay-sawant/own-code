package layout

import (
	"strconv"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
)

const (
	currentColorKeyword = "currentcolor"
	propContent         = "content"

	// Cascade storage for string-set (CSS Content 3,
	// https://drafts.csswg.org/css-content-3/#propdef-string-set) and the
	// GCPM bookmark longhands (https://www.w3.org/TR/css-gcpm-3/#bookmarks
	// with longhands in CSS Content 3): canonical used values in CustomProps,
	// following the ruby precedent in ruby.go. Chrome has no string-set or
	// bookmark support, so the drafts are the oracle. string-set stores the
	// canonical assignment list (none reads as no key, the initial);
	// bookmark-level stores the canonical integer, bookmark-state open or
	// closed, bookmark-label the trimmed declaration (element text resolves
	// the content keyword at read time in bookmark_outline.go). An absent or
	// invalid declaration leaves no key, except bookmark-state where any
	// value including bogus resolves to the used open or closed flag. The
	// style store interns the CustomProps map without regenerating
	// style_intern_gen.go.
	stringSetPropName = "string-set"

	stringSetCustomKey     = "__string_set"
	bookmarkLabelCustomKey = "__bookmark_label"
	bookmarkLevelCustomKey = "__bookmark_level"
	bookmarkStateCustomKey = "__bookmark_state"
)

func isCurrentColor(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), currentColorKeyword)
}

// parseUsedColorAlpha maps a CSS color token onto 0..1 RGB plus its source
// alpha. currentColor resolves opaque: inherited used colors carry no alpha.
func parseUsedColorAlpha(value string, current [3]float64) ([3]float64, float64, bool) {
	if isCurrentColor(value) {
		return current, 1, true
	}

	red, green, blue, alpha, ok := css.ParseColor(value)
	if !ok {
		// Wide-gamut spellings (color(), lab(), lch(), hwb(), wide
		// color-mix) carry out-of-range channels for dynamic-range-limit;
		// legacy ParseColor rejects them, so fall back to the headroom
		// representation. Legacy results are untouched.
		wide, wideAlpha, wideOK := css.ParseColorWide(value)
		if !wideOK {
			return [3]float64{}, 0, false
		}

		return wide, wideAlpha, true
	}

	return [3]float64{float64(red) / 255, float64(green) / 255, float64(blue) / 255}, alpha, true
}

// parseUsedColor maps a CSS color token onto 0..1 RGB. currentColor uses the
// element's used color (already inherited or applied).
func parseUsedColor(value string, current [3]float64) ([3]float64, bool) {
	color, _, ok := parseUsedColorAlpha(value, current)

	return color, ok
}

func applyOutlineProps(style *ResolvedStyle, prop, value string, fsize float64) bool {
	if applyBoxShadowProp(style, prop, value, fsize) {
		return true
	}

	if applySVGPresentationProps(style, prop, value, fsize) {
		return true
	}

	if prop == "outline" {
		applyOutlineShorthand(style, value, fsize)

		return true
	}

	return applyOutlineLonghands(style, prop, value, fsize)
}

//nolint:cyclop // SVG presentation longhands
func applySVGPresentationProps(style *ResolvedStyle, prop, value string, fsize float64) bool {
	switch prop {
	case "fill":
		if strings.EqualFold(strings.TrimSpace(value), "none") {
			style.Fill = [3]float64{}
			style.FillSet = true
			style.FillOpacity = 0
		} else if color, parsed := parseUsedColor(value, style.Color); parsed {
			style.Fill = color
			style.FillSet = true
		}
	case "fill-opacity":
		if opacity, parsed := parseOpacityValue(value); parsed {
			style.FillOpacity = opacity
		}
	case "stroke":
		if color, parsed := parseUsedColor(value, style.Color); parsed {
			style.Stroke = color
			style.StrokeSet = true
		}
	case "stroke-width":
		if width, parsed := plainLength(value, fsize, 0); parsed {
			style.StrokeWidth = width
			style.StrokeWidthSet = true
		}
	case "stroke-opacity":
		if opacity, parsed := parseOpacityValue(value); parsed {
			style.StrokeOpacity = opacity
		}
	case "stroke-dasharray", "stroke-dashoffset", "stroke-linecap", "stroke-linejoin",
		"stroke-miterlimit", "fill-rule", "clip-rule", "color-interpolation",
		"color-interpolation-filters", "shape-rendering", "text-anchor",
		"dominant-baseline", "alignment-baseline", "clip-path", "clip",
		"overflow-clip-margin", "scroll-margin", "scroll-margin-top", "scroll-margin-right",
		"scroll-margin-bottom", "scroll-margin-left", "ruby-align", "ruby-position",
		"ruby-merge", "ruby-overhang":
		return applyLeftoversProps(style, prop, value, fsize)
	default:
		return false
	}

	return true
}

func applyOutlineLonghands(style *ResolvedStyle, prop, value string, fsize float64) bool {
	switch prop {
	case "outline-width":
		if width, parsed := parseOutlineWidth(value, fsize); parsed {
			style.OutlineWidth = width
		}
	case "outline-style":
		if outlineStyle, parsed := parseOutlineStyle(value); parsed {
			style.OutlineStyle = outlineStyle
		}
	case "outline-color":
		if color, parsed := parseUsedColor(value, style.Color); parsed {
			style.OutlineColor = color
			style.OutlineColorSet = true
		}
	case "outline-offset":
		if offset, parsed := plainLength(value, fsize, 0); parsed {
			style.OutlineOffset = offset
		}
	default:
		return false
	}

	return true
}

func applyBoxShadowProp(style *ResolvedStyle, prop, value string, fsize float64) bool {
	switch prop {
	case boxShadowProp:
		applyBoxShadowValue(style, value, fsize)
	case "box-shadow-blur":
		ApplyBoxShadowBlur(style, value, fsize)
	case "box-shadow-spread":
		ApplyBoxShadowSpread(style, value, fsize)
	case "box-shadow-color":
		ApplyBoxShadowColor(style, value)
	case "box-shadow-offset":
		ApplyBoxShadowOffset(style, value, fsize)
	case "box-shadow-position", "box-shadow-inset":
		ApplyBoxShadowPosition(style, value)
	default:
		return false
	}

	return true
}

func applyBoxShadowValue(style *ResolvedStyle, value string, fsize float64) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}

	if strings.EqualFold(value, cssDisplayNone) {
		clearBoxShadow(style)

		return
	}

	shadows := parseBoxShadowList(value, style.Color, fsize)
	if len(shadows) == 0 {
		return
	}

	first := shadows[0]
	style.BoxShadowX = first.x
	style.BoxShadowY = first.y
	style.BoxShadowBlur = first.blur
	style.BoxShadowSpread = first.spread
	style.BoxShadowColor = first.color
	style.BoxShadowInset = first.inset
	style.BoxShadowRaw = value
	style.BoxShadowSet = true
}

func clearBoxShadow(style *ResolvedStyle) {
	style.BoxShadowX = 0
	style.BoxShadowY = 0
	style.BoxShadowBlur = 0
	style.BoxShadowSpread = 0
	style.BoxShadowColor = [3]float64{}
	style.BoxShadowSet = false
	style.BoxShadowInset = false
	style.BoxShadowRaw = ""
}

func parseRuleShorthand(value string, fsize float64, current [3]float64) (float64, string, [3]float64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, "", [3]float64{}, false
	}

	// Unspecified shorthand components reset to CSS initial values.
	width := borderWidth(mediumKeyword, fsize)
	ruleStyle := cssDisplayNone
	color := current

	if strings.EqualFold(value, cssDisplayNone) {
		return width, ruleStyle, color, true
	}

	for start := 0; ; {
		token, next, ok := nextSpaceToken(value, start)
		if !ok {
			return width, ruleStyle, color, true
		}

		if parsedStyle, parsed := parseOutlineStyle(token); parsed {
			ruleStyle = parsedStyle
		} else if parsedWidth, parsed := parseOutlineWidth(token, fsize); parsed {
			width = parsedWidth
		} else if parsedColor, parsed := parseUsedColor(token, current); parsed {
			color = parsedColor
		}

		start = next
	}
}

func applyOutlineShorthand(style *ResolvedStyle, value string, fsize float64) {
	width, outlineStyle, color, ok := parseRuleShorthand(value, fsize, style.Color)
	if !ok {
		return
	}

	style.OutlineWidth = width
	style.OutlineStyle = outlineStyle
	style.OutlineColor = color
	style.OutlineColorSet = true
}

// applyRadiusLonghand sets one corner radius. Percentages store the percent
// number on both the corner field and BorderRadiusPercent (paint uses the
// uniform percent path). Absolute lengths clear BorderRadiusPercent so paint
// uses the per-corner fields.
//
//nolint:cyclop // radius longhands
func applyRadiusLonghand(style *ResolvedStyle, prop, value string, fsize float64) bool {
	switch prop {
	case "border-top-left-radius":
		setCornerRadius(style, &style.BorderRadiusTopLeft, &style.BorderRadiusTopLeftY, value, fsize)
	case "border-top-right-radius":
		setCornerRadius(style, &style.BorderRadiusTopRight, &style.BorderRadiusTopRightY, value, fsize)
	case "border-bottom-right-radius":
		setCornerRadius(style, &style.BorderRadiusBottomRight, &style.BorderRadiusBottomRightY, value, fsize)
	case "border-bottom-left-radius":
		setCornerRadius(style, &style.BorderRadiusBottomLeft, &style.BorderRadiusBottomLeftY, value, fsize)
	case "border-top-radius":
		setCornerRadius(style, &style.BorderRadiusTopLeft, &style.BorderRadiusTopLeftY, value, fsize)
		setCornerRadius(style, &style.BorderRadiusTopRight, &style.BorderRadiusTopRightY, value, fsize)
	case "border-bottom-radius":
		setCornerRadius(style, &style.BorderRadiusBottomLeft, &style.BorderRadiusBottomLeftY, value, fsize)
		setCornerRadius(style, &style.BorderRadiusBottomRight, &style.BorderRadiusBottomRightY, value, fsize)
	case "border-left-radius":
		setCornerRadius(style, &style.BorderRadiusTopLeft, &style.BorderRadiusTopLeftY, value, fsize)
		setCornerRadius(style, &style.BorderRadiusBottomLeft, &style.BorderRadiusBottomLeftY, value, fsize)
	case "border-right-radius":
		setCornerRadius(style, &style.BorderRadiusTopRight, &style.BorderRadiusTopRightY, value, fsize)
		setCornerRadius(style, &style.BorderRadiusBottomRight, &style.BorderRadiusBottomRightY, value, fsize)
	case "border-start-start-radius", "border-start-end-radius",
		"border-end-start-radius", "border-end-end-radius",
		"border-block-start-radius", "border-block-end-radius",
		"border-inline-start-radius", "border-inline-end-radius":
		return applyLogicalRadiusLonghand(style, prop, value, fsize)
	default:
		return false
	}

	return true
}

func setCornerRadius(style *ResolvedStyle, destX, destY *float64, value string, fsize float64) {
	rxTok, ryTok, hasY := splitCornerRadiusTokens(value)
	if rxTok == "" {
		return
	}

	if applyCornerRadiusPercent(style, destX, destY, rxTok) {
		return
	}

	if !applyCornerRadiusX(style, destX, destY, rxTok, fsize) {
		return
	}

	if hasY {
		applyCornerRadiusY(destY, ryTok, fsize)
	}
}

func applyCornerRadiusX(style *ResolvedStyle, destX, destY *float64, token string, fsize float64) bool {
	radius, ok := lengthBox(token, fsize, 0, cssDisplayNone)
	if !ok || radius < 0 {
		return false
	}

	*destX = radius
	*destY = 0
	style.BorderRadius = 0
	style.BorderRadiusPercent = -1

	return true
}

func applyCornerRadiusY(destY *float64, token string, fsize float64) {
	if token == "" {
		return
	}

	if _, unit, parsed := css.ParseLength(token); parsed && unit == "%" {
		return
	}

	radiusY, ok := lengthBox(token, fsize, 0, cssDisplayNone)
	if !ok || radiusY < 0 {
		return
	}

	*destY = radiusY
}

func applyCornerRadiusPercent(style *ResolvedStyle, destX, destY *float64, token string) bool {
	percent, unit, ok := css.ParseLength(token)
	if !ok || unit != "%" || percent < 0 {
		return false
	}

	*destX = percent
	*destY = 0
	style.BorderRadius = 0
	style.BorderRadiusPercent = percent

	return true
}

func applyBackgroundShorthand(style *ResolvedStyle, value string) {
	applyBackgroundImageValue(style, value)

	if r, g, b, a, ok := firstBackgroundColor(value); ok {
		style.BGColor = [4]float64{float64(r) / 255, float64(g) / 255, float64(b) / 255, a}
	}
}

func applyBackgroundImageValue(style *ResolvedStyle, value string) {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, cssDisplayNone) {
		style.BackgroundImage = ""

		return
	}

	if url, ok := firstCSSUrl(trimmed); ok && !strings.Contains(trimmed, ",") && !isGradientFunc(trimmed) {
		style.BackgroundImage = url

		return
	}

	if trimmed != "" {
		style.BackgroundImage = trimmed
	}
}

func firstCSSUrl(value string) (string, bool) {
	urls := css.FontFaceURLs(value)
	if len(urls) == 0 {
		return "", false
	}

	return urls[0], true
}

func applyGeneratedContentProps(style *ResolvedStyle, prop, value string) bool {
	switch prop {
	case "quotes":
		applyQuotesValue(style, value)
	case "counter-reset":
		setTrimmedStyleValue(&style.CounterReset, value)
	case "counter-set":
		setTrimmedStyleValue(&style.CounterSet, value)
	case "counter-increment":
		setTrimmedStyleValue(&style.CounterIncrement, value)
	case "list-style-image":
		applyListStyleImageValue(style, value)
	case propContent:
		style.Content = strings.TrimSpace(value)
	default:
		return applyGeneratedGCPMProps(style, prop, value)
	}

	return true
}

// applyGeneratedGCPMProps owns the GCPM generated-content longhands:
// string-set and the bookmark family. Split from applyGeneratedContentProps
// to keep the dispatch cyclomatic complexity within the lint budget.
func applyGeneratedGCPMProps(style *ResolvedStyle, prop, value string) bool {
	switch prop {
	case stringSetPropName:
		setStringSetCustom(style, value)
	case bookmarkLabelProp:
		setBookmarkLabelCustom(style, value)
	case bookmarkLevelProp:
		setBookmarkLevelCustom(style, value)
	case bookmarkStateProp:
		setBookmarkStateCustom(style, value)
	default:
		return false
	}

	return true
}

func applyQuotesValue(style *ResolvedStyle, value string) {
	style.QuotesRaw = value
	if openQuote, closeQuote, parsed := parseQuotesPair(value); parsed {
		style.QuotesOpen = openQuote
		style.QuotesClose = closeQuote
	}
}

func setTrimmedStyleValue(dst *string, value string) {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		*dst = trimmed
	}
}

// setStringSetCustom stores the canonical used string-set value: the
// re-encoded assignment list from ParseStringSet, so escapes stay decoded and
// adjacent strings stay concatenated. none and initial read as no key (the
// initial). CSS-wide inherit keeps the already-inherited map entry;
// initial, unset, revert, and revert-layer reset to none. An invalid value
// drops to none too, so a bogus string-set never revives an earlier valid one.
func setStringSetCustom(style *ResolvedStyle, value string) {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		if style.CustomProps != nil {
			delete(style.CustomProps, stringSetCustomKey)
		}

		return
	}

	used, ok := canonicalStringSet(value)
	if !ok || used == "none" {
		if style.CustomProps != nil {
			delete(style.CustomProps, stringSetCustomKey)
		}

		return
	}

	ensureGeneratedMap(style)
	style.CustomProps[stringSetCustomKey] = used
}

// canonicalStringSet re-encodes a declaration into its used assignment list:
// name plus double-quoted decoded strings joined by commas, or none for the
// empty assignment list. ok is false outside the supported syntax.
func canonicalStringSet(raw string) (string, bool) {
	assignments, ok := ParseStringSet(raw)
	if !ok {
		return "", false
	}

	if len(assignments) == 0 {
		return "none", true
	}

	parts := make([]string, 0, len(assignments))

	for _, assignment := range assignments {
		parts = append(parts, assignment.Name+" "+quoteStringSetValue(assignment.Value))
	}

	return strings.Join(parts, ", "), true
}

// quoteGrowOverhead is the two quote bytes quoteStringSetValue adds.
const quoteGrowOverhead = 2

// quoteStringSetValue quotes one decoded used value with double quotes,
// escaping backslashes and double quotes so ParseStringSet reads it back.
func quoteStringSetValue(value string) string {
	var built strings.Builder

	built.Grow(len(value) + quoteGrowOverhead)
	built.WriteByte('"')

	for i := range len(value) {
		if value[i] == '"' || value[i] == '\\' {
			built.WriteByte('\\')
		}

		built.WriteByte(value[i])
	}

	built.WriteByte('"')

	return built.String()
}

// setBookmarkLabelCustom stores the trimmed bookmark-label declaration. The
// used title needs the element text (the content keyword resolves at read
// time in bookmark_outline.go), so the cascade keeps the declaration and the
// reader computes the used value. none stays stored (it means no entry, which
// differs from the absent initial that reads as the element text). CSS-wide
// inherit keeps the already-inherited entry; initial, unset, revert, and
// revert-layer delete it back to the element-text initial. Anything else,
// including an unbalanced quote the reader falls back from, stays stored for
// the reader to resolve.
func setBookmarkLabelCustom(style *ResolvedStyle, value string) {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		if !strings.EqualFold(trimmed, inheritKeyword) && style.CustomProps != nil {
			delete(style.CustomProps, bookmarkLabelCustomKey)
		}

		return
	}

	if trimmed == "" {
		return
	}

	ensureGeneratedMap(style)
	style.CustomProps[bookmarkLabelCustomKey] = trimmed
}

// setBookmarkLevelCustom stores the canonical used bookmark-level integer.
// none, out-of-range integers, and unparsable values read as no key (no
// bookmark). CSS-wide inherit keeps the already-inherited entry; every other
// wide keyword resets to none.
func setBookmarkLevelCustom(style *ResolvedStyle, value string) {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		if !strings.EqualFold(trimmed, inheritKeyword) && style.CustomProps != nil {
			delete(style.CustomProps, bookmarkLevelCustomKey)
		}

		return
	}

	level, ok := parseBookmarkLevelValue(value)
	if !ok {
		if style.CustomProps != nil {
			delete(style.CustomProps, bookmarkLevelCustomKey)
		}

		return
	}

	ensureGeneratedMap(style)
	style.CustomProps[bookmarkLevelCustomKey] = strconv.Itoa(level)
}

// setBookmarkStateCustom stores the used bookmark-state: closed only for the
// closed keyword, open for open, absent, and anything else. CSS-wide inherit
// keeps the already-inherited entry; every other wide keyword resets to the
// open initial.
func setBookmarkStateCustom(style *ResolvedStyle, value string) {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		if !strings.EqualFold(trimmed, inheritKeyword) && style.CustomProps != nil {
			delete(style.CustomProps, bookmarkStateCustomKey)
		}

		return
	}

	if trimmed == "" {
		return
	}

	ensureGeneratedMap(style)

	if parseBookmarkOpenValue(value) {
		style.CustomProps[bookmarkStateCustomKey] = "open"
	} else {
		style.CustomProps[bookmarkStateCustomKey] = "closed"
	}
}

// ensureGeneratedMap allocates the CustomProps map for one generated-content
// family write.
func ensureGeneratedMap(style *ResolvedStyle) {
	if style.CustomProps == nil {
		style.CustomProps = make(map[string]string)
	}
}

// applyListStyleImageValue stores the first url(...) from list-style-image or
// the list-style shorthand. none (the whole value, or a lone none token with
// no url) clears the image so the type marker is used instead.
func applyListStyleImageValue(style *ResolvedStyle, value string) {
	if url, ok := firstCSSUrl(value); ok {
		style.ListStyleImage = url

		return
	}

	if listStyleImageNone(value) {
		style.ListStyleImage = ""
	}
}

func listStyleImageNone(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}

	if strings.EqualFold(trimmed, cssDisplayNone) {
		return true
	}

	for start := 0; ; {
		token, next, ok := nextSpaceToken(trimmed, start)
		if !ok {
			return false
		}

		if strings.EqualFold(token, cssDisplayNone) {
			return true
		}

		start = next
	}
}
