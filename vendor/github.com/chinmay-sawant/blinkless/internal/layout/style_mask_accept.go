package layout

import "strings"

const (
	maskShorthandProperty       = "mask"
	maskShorthandDeferredPrefix = "\x00mask:"
	webkitMaskCompositeProperty = "-webkit-mask-composite"
	maskImageProperty           = "mask-image"
	maskModeProperty            = "mask-mode"
	maskPositionProperty        = "mask-position"
	maskSizeProperty            = "mask-size"
	maskRepeatProperty          = "mask-repeat"
	maskOriginProperty          = "mask-origin"
	maskClipProperty            = "mask-clip"
	maskCompositeProperty       = "mask-composite"
	maskTypeProperty            = "mask-type"
	maskModeMatchSource         = "match-source"
	maskModeAlpha               = "alpha"
	maskModeLuminance           = "luminance"
	maskCompositeAdd            = "add"
	maskCompositeSubtract       = "subtract"
	maskCompositeExclude        = "exclude"
	maskRepeatNoRepeat          = "no-repeat"
	maskRepeatKeywordValue      = "repeat"
	maskRepeatSpaceValue        = "space"
	maskRepeatRoundValue        = "round"
	maskInitialPosition         = "0% 0%"
	maskLayerComponentLimit     = 2
	maskPositionPairCount       = 2
	maskPositionOffsetTokens    = 4
	maskShorthandPositionLimit  = maskPositionOffsetTokens
	maskShorthandLegacyTokens   = 3
	maskRepeatPairCount         = 2
	cssSpaceTokenCapacity       = 4
)

type maskShorthandLayer struct {
	image     string
	mode      string
	position  string
	size      string
	repeat    string
	origin    string
	clip      string
	composite string
}

func maskShorthandValueAccepted(value string) bool {
	_, ok := parseMaskShorthand(value)

	return ok
}

func parseMaskShorthand(value string) ([]maskShorthandLayer, bool) {
	return parseMaskShorthandVariant(value, maskOriginLayerValueAccepted, true)
}

// parseWebkitMaskShorthand is the -webkit-mask spelling: Chrome 155 keeps
// the legacy box grammar on the prefixed shorthand, so the SVG boxes and
// no-clip are rejected there while the unprefixed shorthand accepts them.
func parseWebkitMaskShorthand(value string) ([]maskShorthandLayer, bool) {
	return parseMaskShorthandVariant(value, webkitMaskBoxValueAccepted, false)
}

func parseMaskShorthandVariant(
	value string, boxAccepted func(string) bool, allowNoClip bool,
) ([]maskShorthandLayer, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.HasPrefix(trimmed, ",") || strings.HasSuffix(trimmed, ",") {
		return nil, false
	}

	parts := splitParenArgs(trimmed, ',')
	if len(parts) == 0 {
		return nil, false
	}

	layers := make([]maskShorthandLayer, 0, len(parts))

	for _, part := range parts {
		layer, ok := parseMaskShorthandLayer(part, boxAccepted, allowNoClip)
		if !ok {
			return nil, false
		}

		layers = append(layers, layer)
	}

	return layers, true
}

func maskShorthandPropertyValue(layers []maskShorthandLayer, prop string) string {
	var value strings.Builder

	for idx, layer := range layers {
		if idx > 0 {
			value.WriteString(", ")
		}

		value.WriteString(layer.propertyValue(prop))
	}

	return value.String()
}

func (layer maskShorthandLayer) propertyValue(prop string) string {
	switch prop {
	case maskImageProperty:
		return layer.image
	case maskModeProperty:
		return layer.mode
	case maskPositionProperty:
		return layer.position
	case maskSizeProperty:
		return layer.size
	case maskRepeatProperty:
		return layer.repeat
	case maskOriginProperty:
		return layer.origin
	case maskClipProperty:
		return layer.clip
	case maskCompositeProperty:
		return layer.composite
	default:
		return ""
	}
}

type maskShorthandTokenKind uint8

const (
	maskTokenImage maskShorthandTokenKind = iota + 1
	maskTokenMode
	maskTokenRepeat
	maskTokenComposite
	maskTokenNoClip
	maskTokenBox
	maskTokenPosition
)

type maskShorthandState struct {
	layer             maskShorthandLayer
	positionTokens    []string
	repeatTokens      []string
	boxes             []string
	positionRunClosed bool
	repeatRunClosed   bool
	sourceSeen        bool
	modeSeen          bool
	compositeSeen     bool
	noClip            bool
}

func newMaskShorthandState() maskShorthandState {
	return maskShorthandState{
		positionTokens:    nil,
		repeatTokens:      nil,
		boxes:             nil,
		positionRunClosed: false,
		repeatRunClosed:   false,
		sourceSeen:        false,
		modeSeen:          false,
		compositeSeen:     false,
		noClip:            false,
		layer: maskShorthandLayer{
			image:     "none",
			mode:      maskModeMatchSource,
			position:  maskInitialPosition,
			size:      "auto",
			repeat:    maskRepeatKeywordValue,
			origin:    "border-box",
			clip:      "border-box",
			composite: maskCompositeAdd,
		},
	}
}

func parseMaskShorthandLayer(
	value string, boxAccepted func(string) bool, allowNoClip bool,
) (maskShorthandLayer, bool) {
	beforeSize, afterSize, hasSize, ok := splitMaskLayerSlash(value)
	if !ok {
		return emptyMaskShorthandLayer(), false
	}

	state := newMaskShorthandState()

	if !state.addTokens(cssSpaceTokenList(beforeSize), true, boxAccepted, allowNoClip) {
		return emptyMaskShorthandLayer(), false
	}

	if !state.applyPositionValue() {
		return emptyMaskShorthandLayer(), false
	}

	if hasSize {
		sizeTokens := cssSpaceTokenList(afterSize)
		sizeCount := maskShorthandSizeTokenCount(sizeTokens)

		if sizeCount == 0 || !maskSizeLayerValueAccepted(strings.Join(sizeTokens[:sizeCount], " ")) {
			return emptyMaskShorthandLayer(), false
		}

		state.layer.size = normalizeCSSValue(strings.Join(sizeTokens[:sizeCount], " "))

		if !state.addTokens(sizeTokens[sizeCount:], false, boxAccepted, allowNoClip) {
			return emptyMaskShorthandLayer(), false
		}
	}

	applyMaskShorthandBoxDefaults(&state)

	return state.layer, true
}

// applyPositionValue stores the collected position tokens, but only when
// the whole run is a complete shorthand position. A run like "top 10px" is
// a viable prefix while scanning (it can grow into "top 10px left") yet is
// not a position on its own, and Chrome rejects the shorthand there.
func (state *maskShorthandState) applyPositionValue() bool {
	if len(state.positionTokens) == 0 {
		return true
	}

	joined := strings.Join(state.positionTokens, " ")
	if !maskShorthandPositionValueAccepted(joined) {
		return false
	}

	state.layer.position = normalizeCSSValue(joined)

	return true
}

func emptyMaskShorthandLayer() maskShorthandLayer {
	return maskShorthandLayer{
		image:     "",
		mode:      "",
		position:  "",
		size:      "",
		repeat:    "",
		origin:    "",
		clip:      "",
		composite: "",
	}
}

func applyMaskShorthandBoxDefaults(state *maskShorthandState) {
	if len(state.boxes) > 0 {
		state.layer.origin = state.boxes[0]
		state.layer.clip = state.boxes[0]
	}

	if len(state.boxes) == maskLayerComponentLimit {
		state.layer.clip = state.boxes[1]
	}

	if state.noClip {
		state.layer.clip = "no-clip"
	}
}

func (state *maskShorthandState) addTokens(
	tokens []string, allowPosition bool, boxAccepted func(string) bool, allowNoClip bool,
) bool {
	for _, token := range tokens {
		kind, value := classifyMaskShorthandToken(token, boxAccepted, allowNoClip)
		if !state.addToken(kind, value, allowPosition) {
			return false
		}
	}

	return true
}

func classifyMaskShorthandToken(
	token string, boxAccepted func(string) bool, allowNoClip bool,
) (maskShorthandTokenKind, string) {
	value := normalizeCSSValue(token)

	switch {
	case maskImageLayerValueAccepted(token):
		return maskTokenImage, strings.TrimSpace(token)
	case maskModeLayerValueAccepted(value):
		return maskTokenMode, value
	case maskRepeatLayerValueAccepted(value):
		return maskTokenRepeat, value
	case maskCompositeLayerValueAccepted(value):
		return maskTokenComposite, value
	case allowNoClip && value == "no-clip":
		return maskTokenNoClip, value
	case boxAccepted(value):
		return maskTokenBox, value
	default:
		return maskTokenPosition, token
	}
}

func (state *maskShorthandState) addToken(kind maskShorthandTokenKind, value string, allowPosition bool) bool {
	state.closeRunsFor(kind)

	switch kind {
	case maskTokenImage:
		return state.addImage(value)
	case maskTokenMode:
		return state.addMode(value)
	case maskTokenRepeat:
		return state.addRepeat(value)
	case maskTokenComposite:
		return state.addComposite(value)
	case maskTokenNoClip:
		return state.addNoClip()
	case maskTokenBox:
		return state.addBox(value)
	case maskTokenPosition:
		return state.addPosition(value, allowPosition)
	default:
		return false
	}
}

// closeRunsFor ends the position and repeat runs when a different component
// starts. Chrome consumes each component once, so "space left top round"
// (repeat, position, repeat) and "left top alpha 10px" (position broken by
// the masking mode) are invalid even though each run alone is accepted.
func (state *maskShorthandState) closeRunsFor(kind maskShorthandTokenKind) {
	if kind != maskTokenPosition && len(state.positionTokens) > 0 {
		state.positionRunClosed = true
	}

	if kind != maskTokenRepeat && len(state.repeatTokens) > 0 {
		state.repeatRunClosed = true
	}
}

func (state *maskShorthandState) addImage(value string) bool {
	if state.sourceSeen {
		return false
	}

	state.sourceSeen = true
	state.layer.image = value

	return true
}

func (state *maskShorthandState) addMode(value string) bool {
	if state.modeSeen {
		return false
	}

	state.modeSeen = true
	state.layer.mode = value

	return true
}

func (state *maskShorthandState) addRepeat(value string) bool {
	if state.repeatRunClosed || len(state.repeatTokens) == maskLayerComponentLimit {
		return false
	}

	joined := strings.Join(append(state.repeatTokens, value), " ")
	if !maskRepeatLayerValueAccepted(joined) {
		return false
	}

	state.repeatTokens = append(state.repeatTokens, value)
	state.layer.repeat = joined

	return true
}

func (state *maskShorthandState) addComposite(value string) bool {
	if state.compositeSeen {
		return false
	}

	state.compositeSeen = true
	state.layer.composite = value

	return true
}

func (state *maskShorthandState) addNoClip() bool {
	if state.noClip || state.boxSlotCount() >= maskLayerComponentLimit {
		return false
	}

	state.noClip = true

	return true
}

func (state *maskShorthandState) addBox(value string) bool {
	if state.boxSlotCount() >= maskLayerComponentLimit {
		return false
	}

	state.boxes = append(state.boxes, value)

	return true
}

// boxSlotCount is the number of geometry-box values consumed so far,
// counting no-clip as the clip slot. Chrome rejects a third box-ish value
// such as "content-box no-clip view-box".
func (state *maskShorthandState) boxSlotCount() int {
	count := len(state.boxes)
	if state.noClip {
		count++
	}

	return count
}

func (state *maskShorthandState) addPosition(token string, allowPosition bool) bool {
	if !allowPosition || state.positionRunClosed || len(state.positionTokens) == maskShorthandPositionLimit {
		return false
	}

	state.positionTokens = append(state.positionTokens, token)

	return maskShorthandPositionTokensViable(state.positionTokens)
}

//nolint:cyclop,funlen // One scanner must track nested functions, quotes, escapes, and a single top-level slash.
func splitMaskLayerSlash(value string) (string, string, bool, bool) {
	depth := 0
	quote := byte(0)
	escaped := false
	slash := -1

	for idx := range len(value) {
		char := value[idx]

		if escaped {
			escaped = false

			continue
		}

		if quote != 0 {
			switch char {
			case '\\':
				escaped = true
			case quote:
				quote = 0
			}

			continue
		}

		switch char {
		case '\'', '"':
			quote = char
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return "", "", false, false
			}
		case '/':
			if depth == 0 {
				if slash >= 0 {
					return "", "", false, false
				}

				slash = idx
			}
		}
	}

	if depth != 0 || quote != 0 {
		return "", "", false, false
	}

	if slash < 0 {
		return strings.TrimSpace(value), "", false, true
	}

	before := strings.TrimSpace(value[:slash])
	after := strings.TrimSpace(value[slash+1:])

	if before == "" || after == "" {
		return "", "", false, false
	}

	return before, after, true, true
}

func maskShorthandSizeTokenCount(tokens []string) int {
	if len(tokens) == 0 {
		return 0
	}

	if strings.EqualFold(tokens[0], "cover") || strings.EqualFold(tokens[0], "contain") {
		return 1
	}

	if !maskSizeDimensionValueAccepted(tokens[0]) {
		return 0
	}

	if len(tokens) > 1 && maskSizeDimensionValueAccepted(tokens[1]) {
		return maskLayerComponentLimit
	}

	return 1
}

func cssSpaceTokenList(value string) []string {
	tokens := make([]string, 0, cssSpaceTokenCapacity)

	for start := 0; ; {
		token, next, ok := nextSpaceToken(value, start)
		if !ok {
			return tokens
		}

		tokens = append(tokens, token)
		start = next
	}
}

func splitMaskLayerList(value string) ([]string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.HasPrefix(trimmed, ",") || strings.HasSuffix(trimmed, ",") {
		return nil, false
	}

	layers := splitParenArgs(trimmed, ',')
	if len(layers) == 0 {
		return nil, false
	}

	for _, layer := range layers {
		if strings.TrimSpace(layer) == "" {
			return nil, false
		}
	}

	return layers, true
}

func maskLayerListValueAccepted(value string, accepted func(string) bool) bool {
	layers, ok := splitMaskLayerList(value)
	if !ok {
		return false
	}

	for _, layer := range layers {
		if !accepted(strings.TrimSpace(layer)) {
			return false
		}
	}

	return true
}

func maskImageLayerValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, "none") {
		return true
	}

	if maskGradientAccepted(trimmed) {
		return true
	}

	if _, ok := maskImageSetChoice(trimmed); ok {
		return true
	}

	if !cssURLFunctionValue(trimmed) {
		return false
	}

	_, ok := firstCSSUrl(trimmed)

	return ok
}

//nolint:cyclop // URL validation tracks nested parentheses, quoted text, and escapes.
func cssURLFunctionValue(value string) bool {
	if len(value) < len("url()") || !strings.EqualFold(value[:4], "url(") {
		return false
	}

	depth := 1
	quote := byte(0)
	escaped := false

	for idx := 4; idx < len(value); idx++ {
		char := value[idx]

		if escaped {
			escaped = false

			continue
		}

		if quote != 0 {
			switch char {
			case '\\':
				escaped = true
			case quote:
				quote = 0
			}

			continue
		}

		switch char {
		case '\'', '"':
			quote = char
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return strings.TrimSpace(value[idx+1:]) == ""
			}
		}
	}

	return false
}

func maskModeLayerValueAccepted(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case maskModeMatchSource, maskModeAlpha, maskModeLuminance:
		return true
	default:
		return false
	}
}

func maskTypeValueAccepted(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case maskModeAlpha, maskModeLuminance:
		return true
	default:
		return false
	}
}

// maskPositionLayerValueAccepted validates one <position> value for the
// unprefixed mask-position longhand against Chrome 155. The grammar is
// stricter than parseObjectPosition: a bare length only binds to the
// horizontal slot before a vertical keyword ("10px top" yes, "top 10px" and
// "10px left" no), and the four-value form carries left/right and top/bottom
// edges. calc(), min(), max(), and clamp() count as lengths.
func maskPositionLayerValueAccepted(value string) bool {
	tokens := maskPositionTokens(value)

	switch len(tokens) {
	case 1:
		return maskPositionKeywordValue(tokens[0]) || maskPositionLengthValue(tokens[0])
	case maskPositionPairCount:
		return maskPositionPairValueAccepted(tokens[0], tokens[1])
	case maskPositionOffsetTokens:
		return maskPositionOffsetValueAccepted(tokens)
	default:
		return false
	}
}

// maskShorthandPositionValueAccepted validates one position run inside the
// mask shorthand. Chrome's shorthand position grammar is a superset of the
// longhand grammar: it also takes the legacy three-token forms ("left top
// 10px", "left 10px top") that standalone mask-position rejects.
func maskShorthandPositionValueAccepted(value string) bool {
	tokens := maskPositionTokens(value)
	if len(tokens) == maskShorthandLegacyTokens {
		return maskShorthandLegacyPositionValueAccepted(tokens)
	}

	return maskPositionLayerValueAccepted(value)
}

// maskShorthandPositionTokensViable reports whether a running sequence of
// position tokens can still grow into a complete shorthand position. The
// scanner checks viability while adding tokens because "top 10px" only
// becomes valid once "left" follows ("top 10px left").
func maskShorthandPositionTokensViable(tokens []string) bool {
	switch len(tokens) {
	case 1:
		return maskPositionKeywordValue(tokens[0]) || maskPositionLengthValue(tokens[0])
	case maskPositionPairCount:
		return maskPositionPairValueAccepted(tokens[0], tokens[1]) ||
			maskShorthandPositionPairCanExtend(tokens[0], tokens[1])
	case maskShorthandLegacyTokens:
		return maskShorthandLegacyPositionValueAccepted(tokens)
	case maskPositionOffsetTokens:
		return maskPositionOffsetValueAccepted(tokens)
	default:
		return false
	}
}

func maskPositionTokens(value string) []string {
	tokens := cssSpaceTokenList(value)

	for idx := range tokens {
		tokens[idx] = strings.ToLower(strings.TrimSpace(tokens[idx]))
	}

	return tokens
}

func maskPositionKeywordValue(token string) bool {
	return isObjectPositionXKeyword(token) || isObjectPositionYKeyword(token)
}

// maskPositionLengthValue accepts one <length-percentage>, including the
// math functions Chrome takes where parseObjectPosition only looks at plain
// lengths and percentages.
func maskPositionLengthValue(token string) bool {
	if token == "" || cssWideKeyword(token) {
		return false
	}

	_, ok := lengthBox(token, 0, 0, "")

	return ok
}

func maskPositionPairValueAccepted(first, second string) bool {
	if maskPositionKeywordValue(first) && maskPositionKeywordValue(second) {
		return maskPositionKeywordPairAccepted(first, second)
	}

	return maskPositionHorizontalSlotValue(first) && maskPositionVerticalSlotValue(second)
}

// maskPositionKeywordPairAccepted rejects two keywords on one axis
// ("left right", "top top") while center fills either slot.
func maskPositionKeywordPairAccepted(first, second string) bool {
	firstX := isObjectPositionXKeyword(first)
	firstY := isObjectPositionYKeyword(first)
	secondX := isObjectPositionXKeyword(second)
	secondY := isObjectPositionYKeyword(second)

	if firstX && secondX && !firstY && !secondY {
		return false
	}

	return !(firstY && secondY && !firstX && !secondX)
}

func maskPositionHorizontalSlotValue(token string) bool {
	return isObjectPositionXKeyword(token) || maskPositionLengthValue(token)
}

func maskPositionVerticalSlotValue(token string) bool {
	return isObjectPositionYKeyword(token) || maskPositionLengthValue(token)
}

// maskPositionOffsetValueAccepted is the four-value edge form:
// [left|right] <length> [top|bottom] <length> in either pair order.
func maskPositionOffsetValueAccepted(tokens []string) bool {
	horizontal, vertical := false, false

	for start := 0; start < len(tokens); start += 2 {
		switch {
		case maskPositionHorizontalOffsetPair(tokens[start], tokens[start+1]):
			if horizontal {
				return false
			}

			horizontal = true
		case maskPositionVerticalOffsetPair(tokens[start], tokens[start+1]):
			if vertical {
				return false
			}

			vertical = true
		default:
			return false
		}
	}

	return horizontal && vertical
}

func maskPositionHorizontalOffsetPair(keyword, length string) bool {
	return (keyword == "left" || keyword == "right") && maskPositionLengthValue(length)
}

func maskPositionVerticalOffsetPair(keyword, length string) bool {
	return (keyword == "top" || keyword == "bottom") && maskPositionLengthValue(length)
}

// maskShorthandPositionPairCanExtend reports whether a two-token run can
// start one of the legacy three-token forms: a length after a definite
// keyword ("left 10px" then "top"), or a definite keyword after any keyword
// ("top left" then "10px").
func maskShorthandPositionPairCanExtend(first, second string) bool {
	if maskPositionDefiniteKeywordValue(first) && maskPositionLengthValue(second) {
		return true
	}

	return maskPositionDefiniteKeywordValue(second) && maskPositionKeywordValue(first)
}

// maskShorthandLegacyPositionValueAccepted covers Chrome's legacy
// three-token shorthand forms: <definite keyword> <length> <keyword>
// ("left 10px top") and <keyword> <definite keyword> <length>
// ("left top 10px", "center left 10px").
func maskShorthandLegacyPositionValueAccepted(tokens []string) bool {
	if len(tokens) != maskShorthandLegacyTokens {
		return false
	}

	if maskPositionDefiniteKeywordValue(tokens[0]) && maskPositionLengthValue(tokens[1]) &&
		maskShorthandPositionTrailingKeywordAccepted(tokens[0], tokens[2]) {
		return true
	}

	return maskPositionDefiniteKeywordValue(tokens[1]) && maskPositionLengthValue(tokens[2]) &&
		maskShorthandPositionLeadingKeywordAccepted(tokens[1], tokens[0])
}

func maskPositionDefiniteKeywordValue(token string) bool {
	switch token {
	case "left", "right", "top", "bottom":
		return true
	default:
		return false
	}
}

// maskShorthandPositionTrailingKeywordAccepted pairs the trailing keyword
// with the leading definite one: center fills the other slot, and a
// same-axis keyword is invalid ("left 10px right").
func maskShorthandPositionTrailingKeywordAccepted(first, second string) bool {
	if !maskPositionKeywordValue(second) {
		return false
	}

	if second == "center" {
		return true
	}

	if isObjectPositionXKeyword(first) {
		return isObjectPositionYKeyword(second)
	}

	return isObjectPositionXKeyword(second)
}

// maskShorthandPositionLeadingKeywordAccepted pairs a leading keyword with
// the definite one after it: center fills the other slot, and a same-axis
// keyword is invalid ("left left 10px").
func maskShorthandPositionLeadingKeywordAccepted(definite, first string) bool {
	if !maskPositionKeywordValue(first) {
		return false
	}

	if first == "center" {
		return true
	}

	if isObjectPositionXKeyword(definite) {
		return isObjectPositionYKeyword(first)
	}

	return isObjectPositionXKeyword(first)
}

func maskSizeLayerValueAccepted(value string) bool {
	tokens := cssSpaceTokenList(value)
	if len(tokens) == 1 {
		switch strings.ToLower(tokens[0]) {
		case "auto", "cover", "contain":
			return true
		}
	}

	if len(tokens) < 1 || len(tokens) > 2 {
		return false
	}

	for _, token := range tokens {
		if !maskSizeDimensionValueAccepted(token) {
			return false
		}
	}

	return true
}

func maskSizeDimensionValueAccepted(value string) bool {
	if strings.EqualFold(strings.TrimSpace(value), "auto") {
		return true
	}

	length, ok := lengthBox(value, 0, 0, "")

	return ok && length >= 0
}

// maskRepeatLayerValueAccepted takes one layer of mask-repeat: a single
// <repeat-style> or a two-token pair where both tokens are one of repeat,
// space, round, and no-repeat (Chrome 155). repeat-x and repeat-y stand
// alone and cannot open or join a pair ("repeat-x space" is rejected).
func maskRepeatLayerValueAccepted(value string) bool {
	tokens := strings.Fields(normalizeCSSValue(value))

	switch len(tokens) {
	case 1:
		return maskRepeatSingleValueAccepted(tokens[0])
	case maskRepeatPairCount:
		return maskRepeatPairValueAccepted(tokens)
	default:
		return false
	}
}

func maskRepeatSingleValueAccepted(token string) bool {
	switch token {
	case maskRepeatKeywordValue, "repeat-x", "repeat-y", maskRepeatSpaceValue,
		maskRepeatRoundValue, maskRepeatNoRepeat:
		return true
	default:
		return false
	}
}

func maskRepeatPairValueAccepted(tokens []string) bool {
	for _, token := range tokens {
		switch token {
		case maskRepeatKeywordValue, maskRepeatSpaceValue, maskRepeatRoundValue, maskRepeatNoRepeat:
		default:
			return false
		}
	}

	return true
}

func maskOriginLayerValueAccepted(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "border-box", "padding-box", "content-box", "fill-box", "stroke-box", "view-box":
		return true
	default:
		return false
	}
}

func maskClipLayerValueAccepted(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "no-clip") || maskOriginLayerValueAccepted(value)
}

func maskCompositeLayerValueAccepted(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case maskCompositeAdd, maskCompositeSubtract, "intersect", maskCompositeExclude:
		return true
	default:
		return false
	}
}

func webkitMaskCompositeValueAccepted(value string) bool {
	return maskLayerListValueAccepted(value, func(layer string) bool {
		switch strings.ToLower(strings.TrimSpace(layer)) {
		case "source-over", "source-out", "source-in", "xor":
			return true
		default:
			return false
		}
	})
}

func remapWebkitMaskCompositeValue(value string) string {
	layers, ok := splitMaskLayerList(value)
	if !ok {
		return value
	}

	for idx, layer := range layers {
		switch strings.ToLower(strings.TrimSpace(layer)) {
		case "source-over":
			layers[idx] = "add"
		case "source-out":
			layers[idx] = "subtract"
		case "source-in":
			layers[idx] = "intersect"
		case "xor":
			layers[idx] = "exclude"
		default:
			return value
		}
	}

	return strings.Join(layers, ", ")
}

// webkitMaskLegacyValueAccepted models where Chrome 155 drops a value on a
// prefixed mask property that the unprefixed property takes. The legacy
// -webkit-mask-origin, -webkit-mask-clip, and -webkit-mask grammar keeps
// only content-box, padding-box, and border-box: no-clip and the SVG
// fill-box, stroke-box, and view-box values are unprefixed-only.
func webkitMaskLegacyValueAccepted(prop, value string) (bool, bool) {
	switch prop {
	case "-webkit-mask-origin", "-webkit-mask-clip":
		return maskLayerListValueAccepted(value, webkitMaskBoxValueAccepted), true
	case "-webkit-mask":
		_, ok := parseWebkitMaskShorthand(value)

		return ok, true
	default:
		return false, false
	}
}

func webkitMaskBoxValueAccepted(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "border-box", "padding-box", "content-box":
		return true
	default:
		return false
	}
}
