package layout

import (
	"math"
	"strconv"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
)

const (
	propAnimation                   = "animation"
	propAnimationDelay              = "animation-delay"
	propAnimationDuration           = "animation-duration"
	propAnimationTimingFunction     = "animation-timing-function"
	propAnimationIterationCount     = "animation-iteration-count"
	propAnimationDirection          = "animation-direction"
	propAnimationFillMode           = "animation-fill-mode"
	propAnimationPlayState          = "animation-play-state"
	propAnimationComposition        = "animation-composition"
	propAnimationDelayStart         = "animation-delay-start"
	propAnimationDelayEnd           = "animation-delay-end"
	propAnimationName               = "animation-name"
	propAnimationTimeline           = "animation-timeline"
	propAnimationRange              = "animation-range"
	propAnimationRangeStart         = "animation-range-start"
	propAnimationRangeEnd           = "animation-range-end"
	propAnimationRangeCenter        = "animation-range-center"
	propAnimationTrigger            = "animation-trigger"
	propTriggerScope                = "trigger-scope"
	propViewTransitionName          = "view-transition-name"
	propViewTransitionClass         = "view-transition-class"
	propViewTransitionGroup         = "view-transition-group"
	propViewTransitionScope         = "view-transition-scope"
	animationShorthandLonghandCount = 12
	animationEase                   = "ease"
	animationReplace                = "replace"
	animationRangeEndDefault        = "100%"
	animationFillBoth               = "both"
	animationRangeContain           = "contain"
	animationRangePairMaxTokens     = 4
	animationRangeEdgeTokenCount    = 2
	animationTimelineMaxArguments   = 2
	animationRangeCover             = "cover"
)

func animationShorthandLonghandProps() [animationShorthandLonghandCount]string {
	return [...]string{
		propAnimationDuration,
		propAnimationTimingFunction,
		propAnimationDelayStart,
		propAnimationIterationCount,
		propAnimationDirection,
		propAnimationFillMode,
		propAnimationPlayState,
		propAnimationName,
		propAnimationTimeline,
		propAnimationRangeStart,
		propAnimationRangeEnd,
		propAnimationTrigger,
	}
}

func animationDeclarationValueAccepted(prop, value string) (bool, bool) {
	if normalized, ok := normalizeWebkitAnimationTransitionProperty(prop); ok {
		if animationProperty(normalized) {
			prop = normalized
		}
	}

	if !animationProperty(prop) {
		return false, false
	}

	value = strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(value)) || containsVarFunc(value) {
		return true, true
	}

	if value == "" {
		return false, true
	}

	return animationValueAccepted(prop, value), true
}

func animationValueAccepted(prop, value string) bool {
	switch prop {
	case propAnimation, propAnimationDelay, propAnimationRange:
		_, valid := expandAnimationDeclaration(prop, value)

		return valid
	case propAnimationDelayStart, propAnimationDelayEnd:
		return animationDelayValueAccepted(prop, value)
	case propAnimationRangeStart, propAnimationRangeEnd:
		return animationRangeEdgeListValueAccepted(prop, value)
	case propAnimationTrigger:
		return animationListValueAccepted(value, animationTriggerValueAccepted)
	case propTriggerScope:
		return triggerScopeValueAccepted(value)
	case propViewTransitionName, propViewTransitionClass, propViewTransitionGroup, propViewTransitionScope:
		return viewTransitionValueAccepted(prop, value)
	default:
		return animationBasicLonghandValueAccepted(prop, value)
	}
}

func animationDelayValueAccepted(prop, value string) bool {
	allowNegative := prop == propAnimationDelayStart

	return animationListValueAccepted(value, func(value string) bool {
		return animationTimeValueAccepted(value, allowNegative)
	})
}

func animationRangeEdgeListValueAccepted(prop, value string) bool {
	isEnd := prop == propAnimationRangeEnd

	return animationListValueAccepted(value, func(value string) bool {
		return animationRangeEdgeValueAccepted(value, isEnd)
	})
}

func animationBasicLonghandValueAccepted(prop, value string) bool {
	switch prop {
	case propAnimationDuration:
		return animationListValueAccepted(value, animationDurationValueAccepted)
	case propAnimationTimingFunction:
		return animationListValueAccepted(value, easingValueAccepted)
	case propAnimationIterationCount:
		return animationListValueAccepted(value, animationIterationValueAccepted)
	case propAnimationDirection:
		return animationListValueAccepted(value, animationDirectionValueAccepted)
	case propAnimationFillMode:
		return animationListValueAccepted(value, animationFillValueAccepted)
	case propAnimationPlayState:
		return animationListValueAccepted(value, animationPlayStateValueAccepted)
	case propAnimationComposition:
		return animationListValueAccepted(value, animationCompositionValueAccepted)
	default:
		return animationExtendedLonghandValueAccepted(prop, value)
	}
}

func animationExtendedLonghandValueAccepted(prop, value string) bool {
	switch prop {
	case propAnimationName:
		return animationListValueAccepted(value, animationNameValueAccepted)
	case propAnimationTimeline:
		return animationListValueAccepted(value, animationTimelineValueAccepted)
	case propAnimationRangeCenter:
		return animationListValueAccepted(value, animationRangeCenterValueAccepted)
	default:
		return false
	}
}

func viewTransitionValueAccepted(prop, value string) bool {
	switch prop {
	case propViewTransitionName:
		return viewTransitionNameValueAccepted(value)
	case propViewTransitionClass:
		return viewTransitionClassValueAccepted(value)
	case propViewTransitionGroup:
		return viewTransitionGroupValueAccepted(value)
	case propViewTransitionScope:
		return viewTransitionScopeValueAccepted(value)
	default:
		return false
	}
}

func animationProperty(prop string) bool {
	switch prop {
	case propAnimation, propAnimationDelay, propAnimationRange,
		propAnimationDuration, propAnimationTimingFunction, propAnimationIterationCount,
		propAnimationDirection, propAnimationFillMode, propAnimationPlayState,
		propAnimationComposition, propAnimationDelayStart, propAnimationDelayEnd,
		propAnimationName, propAnimationTimeline, propAnimationRangeStart,
		propAnimationRangeEnd, propAnimationRangeCenter, propAnimationTrigger,
		propTriggerScope, propViewTransitionName, propViewTransitionClass,
		propViewTransitionGroup, propViewTransitionScope:
		return true
	default:
		return false
	}
}

func expandAnimationDeclaration(prop, value string) ([]logicalPropDecl, bool) {
	if normalized, ok := normalizeWebkitAnimationTransitionProperty(prop); ok {
		if animationProperty(normalized) {
			prop = normalized
		}
	}

	switch prop {
	case propAnimation:
		return expandAnimationShorthand(value)
	case propAnimationDelay:
		return expandAnimationDelay(value)
	case propAnimationRange:
		return expandAnimationRange(value)
	default:
		return nil, false
	}
}

func expandAnimationShorthand(value string) ([]logicalPropDecl, bool) {
	value = strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(value)) {
		return animationWideDeclarations(value), true
	}

	items, valid := animationCommaParts(value)
	if !valid {
		return nil, false
	}

	longhands := animationShorthandLonghandProps()
	values := make(map[string][]string, len(longhands))

	for _, prop := range longhands {
		values[prop] = make([]string, 0, len(items))
	}

	for _, item := range items {
		parts, parsed := parseSingleAnimation(item)
		if !parsed {
			return nil, false
		}

		for prop, part := range parts {
			values[prop] = append(values[prop], part)
		}
	}

	declarations := make([]logicalPropDecl, 0, len(longhands))
	for _, prop := range longhands {
		declarations = append(declarations, logicalPropDecl{
			prop: prop,
			val:  strings.Join(values[prop], ", "),
		})
	}

	return declarations, true
}

func animationWideDeclarations(value string) []logicalPropDecl {
	longhands := animationShorthandLonghandProps()
	declarations := make([]logicalPropDecl, 0, len(longhands))

	for _, prop := range longhands {
		declarations = append(declarations, logicalPropDecl{prop: prop, val: value})
	}

	return declarations
}

func expandAnimationDelay(value string) ([]logicalPropDecl, bool) {
	value = strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(value)) {
		return []logicalPropDecl{
			{prop: propAnimationDelayStart, val: value},
			{prop: propAnimationDelayEnd, val: value},
		}, true
	}

	items, valid := animationCommaParts(value)
	if !valid {
		return nil, false
	}

	starts := make([]string, 0, len(items))
	ends := make([]string, 0, len(items))

	for _, item := range items {
		tokens, valid := animationSpaceTokens(item)
		if !valid || len(tokens) < 1 || len(tokens) > 2 || !animationTimeValueAccepted(tokens[0], true) {
			return nil, false
		}

		start, end := tokens[0], "0s"

		if len(tokens) == animationRangeEdgeTokenCount {
			if !animationTimeValueAccepted(tokens[1], false) {
				return nil, false
			}

			end = tokens[1]
		}

		starts = append(starts, start)
		ends = append(ends, end)
	}

	return []logicalPropDecl{
		{prop: propAnimationDelayStart, val: strings.Join(starts, ", ")},
		{prop: propAnimationDelayEnd, val: strings.Join(ends, ", ")},
	}, true
}

func expandAnimationRange(value string) ([]logicalPropDecl, bool) {
	value = strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(value)) {
		return []logicalPropDecl{
			{prop: propAnimationRangeStart, val: value},
			{prop: propAnimationRangeEnd, val: value},
		}, true
	}

	items, valid := animationCommaParts(value)
	if !valid {
		return nil, false
	}

	starts := make([]string, 0, len(items))
	ends := make([]string, 0, len(items))

	for _, item := range items {
		start, end, valid := parseAnimationRangePair(item)
		if !valid {
			return nil, false
		}

		starts = append(starts, start)
		ends = append(ends, end)
	}

	return []logicalPropDecl{
		{prop: propAnimationRangeStart, val: strings.Join(starts, ", ")},
		{prop: propAnimationRangeEnd, val: strings.Join(ends, ", ")},
	}, true
}

func parseAnimationRangePair(value string) (string, string, bool) {
	tokens, valid := animationSpaceTokens(value)
	if !valid || len(tokens) == 0 || len(tokens) > animationRangePairMaxTokens {
		return "", "", false
	}

	start, startCount, valid := parseAnimationRangeEdgeTokens(tokens, false)
	if !valid {
		return "", "", false
	}

	if startCount == len(tokens) {
		end := "normal"
		if strings.Contains(start, " ") {
			end = strings.Fields(start)[0] + " " + animationRangeEndDefault
		} else if animationTimelineRangeName(start) {
			end = start + " " + animationRangeEndDefault
		}

		return start, end, true
	}

	end, endCount, valid := parseAnimationRangeEdgeTokens(tokens[startCount:], true)
	if !valid || startCount+endCount != len(tokens) {
		return "", "", false
	}

	return start, end, true
}

func parseAnimationRangeEdgeTokens(tokens []string, isEnd bool) (string, int, bool) {
	if len(tokens) == 0 {
		return "", 0, false
	}

	if strings.EqualFold(tokens[0], "normal") {
		return "normal", 1, true
	}

	if animationTimelineRangeName(tokens[0]) {
		position := "0%"
		if isEnd {
			position = animationRangeEndDefault
		}

		if len(tokens) > 1 && animationLengthPercentageValueAccepted(tokens[1]) {
			position = tokens[1]

			return tokens[0] + " " + position, animationRangeEdgeTokenCount, true
		}

		return tokens[0] + " " + position, 1, true
	}

	if animationLengthPercentageValueAccepted(tokens[0]) {
		return tokens[0], 1, true
	}

	return "", 0, false
}

func animationRangeEdgeValueAccepted(value string, isEnd bool) bool {
	tokens, valid := animationSpaceTokens(value)
	if !valid || len(tokens) == 0 || len(tokens) > animationRangeEdgeTokenCount {
		return false
	}

	_, count, valid := parseAnimationRangeEdgeTokens(tokens, isEnd)

	return valid && count == len(tokens)
}

func parseSingleAnimation(value string) (map[string]string, bool) {
	tokens, valid := animationSpaceTokens(value)
	if !valid || len(tokens) == 0 {
		return nil, false
	}

	components := newAnimationComponents(len(tokens))
	for _, token := range tokens {
		if !components.addToken(token) {
			return nil, false
		}
	}

	return components.values, true
}

type animationComponents struct {
	values  map[string]string
	seen    map[string]bool
	nameSet bool
}

func newAnimationComponents(tokenCount int) animationComponents {
	return animationComponents{
		values: map[string]string{
			propAnimationDuration:       "auto",
			propAnimationTimingFunction: animationEase,
			propAnimationDelayStart:     "0s",
			propAnimationIterationCount: "1",
			propAnimationDirection:      "normal",
			propAnimationFillMode:       "none",
			propAnimationPlayState:      "running",
			propAnimationName:           "none",
			propAnimationTimeline:       "auto",
			propAnimationRangeStart:     "normal",
			propAnimationRangeEnd:       "normal",
			propAnimationTrigger:        "none",
		},
		seen:    make(map[string]bool, tokenCount),
		nameSet: false,
	}
}

func (components *animationComponents) addToken(token string) bool {
	if handled, valid := components.addTime(token); handled {
		return valid
	}

	if handled, valid := components.addEasing(token); handled {
		return valid
	}

	if handled, valid := components.addIteration(token); handled {
		return valid
	}

	if handled, valid := components.addDirectionFillPlay(token); handled {
		return valid
	}

	if handled, valid := components.addTimelineOrName(token); handled {
		return valid
	}

	return false
}

func (components *animationComponents) addTime(token string) (bool, bool) {
	if !animationTimeToken(token) {
		return false, false
	}

	prop, allowNegative := propAnimationDuration, false
	if components.seen[propAnimationDuration] {
		prop, allowNegative = propAnimationDelayStart, true
	}

	if components.seen[prop] || !animationTimeValueAccepted(token, allowNegative) {
		return true, false
	}

	components.values[prop] = token
	components.seen[prop] = true

	return true, true
}

func (components *animationComponents) addEasing(token string) (bool, bool) {
	if !easingValueAccepted(token) || components.seen[propAnimationTimingFunction] {
		return false, false
	}

	return components.setValue(propAnimationTimingFunction, token)
}

func (components *animationComponents) addIteration(token string) (bool, bool) {
	if !animationIterationToken(token) {
		return false, false
	}

	if components.seen[propAnimationIterationCount] {
		return false, false
	}

	if !animationIterationValueAccepted(token) {
		return true, false
	}

	return components.setValue(propAnimationIterationCount, strings.ToLower(token))
}

func (components *animationComponents) addDirectionFillPlay(token string) (bool, bool) {
	prop, valid := animationDirectionFillPlayProperty(token)
	if !valid || components.seen[prop] {
		return false, false
	}

	return components.setValue(prop, strings.ToLower(token))
}

func animationDirectionFillPlayProperty(token string) (string, bool) {
	switch {
	case animationDirectionValueAccepted(token):
		return propAnimationDirection, true
	case animationFillValueAccepted(token):
		return propAnimationFillMode, true
	case animationPlayStateValueAccepted(token):
		return propAnimationPlayState, true
	default:
		return "", false
	}
}

func (components *animationComponents) addTimelineOrName(token string) (bool, bool) {
	if handled, valid := components.addTimelineFunction(token); handled {
		return handled, valid
	}

	if handled, valid := components.addAutoOrNone(token); handled {
		return handled, valid
	}

	if handled, valid := components.addDashedTimeline(token); handled {
		return handled, valid
	}

	return components.addKeyframesName(token)
}

func (components *animationComponents) addTimelineFunction(token string) (bool, bool) {
	if !animationTimelineFunctionValueAccepted(token) || components.seen[propAnimationTimeline] {
		return false, false
	}

	return components.setValue(propAnimationTimeline, token)
}

func (components *animationComponents) addAutoOrNone(token string) (bool, bool) {
	switch strings.ToLower(token) {
	case "auto":
		if !components.seen[propAnimationDuration] {
			return components.setValue(propAnimationDuration, "auto")
		}

		if components.seen[propAnimationTimeline] {
			return false, false
		}

		return components.setValue(propAnimationTimeline, "auto")
	case "none":
		if !components.nameSet {
			components.nameSet = true

			return components.setValue(propAnimationName, "none")
		}

		if components.seen[propAnimationTimeline] {
			return false, false
		}

		return components.setValue(propAnimationTimeline, "none")
	default:
		return false, false
	}
}

func (components *animationComponents) addDashedTimeline(token string) (bool, bool) {
	if !animationDashedIdent(token) || components.seen[propAnimationTimeline] {
		return false, false
	}

	return components.setValue(propAnimationTimeline, token)
}

func (components *animationComponents) addKeyframesName(token string) (bool, bool) {
	if !animationKeyframesNameValueAccepted(token) {
		return false, false
	}

	if components.nameSet {
		return true, false
	}

	components.nameSet = true

	return components.setValue(propAnimationName, token)
}

func (components *animationComponents) setValue(prop, value string) (bool, bool) {
	if components.seen[prop] {
		return true, false
	}

	components.values[prop] = value
	components.seen[prop] = true

	return true, true
}

func applyAnimationProps(
	style *ResolvedStyle,
	prop, value string,
	_ float64,
	_ *styleContext,
	parent *ResolvedStyle,
	hasParent bool,
) bool {
	if style == nil {
		return false
	}

	prop = normalizedAnimationProperty(prop)
	if animationShorthandProperty(prop) {
		return applyAnimationShorthandProps(style, prop, value, parent, hasParent)
	}

	return applyAnimationLonghand(style, prop, value, parent, hasParent)
}

func normalizedAnimationProperty(prop string) string {
	normalized, ok := normalizeWebkitAnimationTransitionProperty(prop)
	if ok && animationProperty(normalized) {
		return normalized
	}

	return prop
}

func animationShorthandProperty(prop string) bool {
	return prop == propAnimation || prop == propAnimationDelay || prop == propAnimationRange
}

func applyAnimationShorthandProps(
	style *ResolvedStyle,
	prop, value string,
	parent *ResolvedStyle,
	hasParent bool,
) bool {
	declarations, valid := expandAnimationDeclaration(prop, value)
	if !valid {
		return false
	}

	for _, declaration := range declarations {
		if !applyAnimationLonghand(style, declaration.prop, declaration.val, parent, hasParent) {
			return false
		}
	}

	return true
}

func applyAnimationLonghand(style *ResolvedStyle, prop, value string, parent *ResolvedStyle, hasParent bool) bool {
	if !animationLonghand(prop) {
		return false
	}

	value = strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(value)) {
		setAnimationWideValue(style, prop, strings.ToLower(value), parent, hasParent)

		return true
	}

	if accepted, handled := animationDeclarationValueAccepted(prop, value); !handled || !accepted {
		return false
	}

	setAnimationField(style, prop, value)

	return true
}

func animationLonghand(prop string) bool {
	isShorthand := prop == propAnimation || prop == propAnimationDelay || prop == propAnimationRange

	return animationProperty(prop) && !isShorthand
}

func setAnimationWideValue(style *ResolvedStyle, prop, keyword string, parent *ResolvedStyle, hasParent bool) {
	value := animationInitialValue(prop)
	if keyword == inheritKeyword && hasParent && parent != nil {
		value = animationFieldValue(parent.Animation, prop)
		if value == "" {
			value = animationInitialValue(prop)
		}
	}

	setAnimationField(style, prop, value)
}

func animationInitialValue(prop string) string {
	if value, found := animationCoreInitialValue(prop); found {
		return value
	}

	return animationExtendedInitialValue(prop)
}

func animationCoreInitialValue(prop string) (string, bool) {
	switch prop {
	case propAnimationDuration:
		return "auto", true
	case propAnimationTimingFunction:
		return animationEase, true
	case propAnimationIterationCount:
		return "1", true
	case propAnimationDirection:
		return "normal", true
	case propAnimationFillMode:
		return "none", true
	case propAnimationPlayState:
		return "running", true
	case propAnimationComposition:
		return animationReplace, true
	case propAnimationDelayStart, propAnimationDelayEnd:
		return "0s", true
	default:
		return "", false
	}
}

func animationExtendedInitialValue(prop string) string {
	switch prop {
	case propAnimationName:
		return "none"
	case propAnimationTimeline:
		return "auto"
	case propAnimationRangeStart, propAnimationRangeEnd, propAnimationRangeCenter:
		return "normal"
	case propAnimationTrigger, propTriggerScope, propViewTransitionName, propViewTransitionClass, propViewTransitionScope:
		return "none"
	case propViewTransitionGroup:
		return "normal"
	default:
		return ""
	}
}

func animationFieldValue(style animationStyle, prop string) string {
	if value, found := animationCoreFieldValue(style, prop); found {
		return value
	}

	return animationExtendedFieldValue(style, prop)
}

func animationCoreFieldValue(style animationStyle, prop string) (string, bool) {
	if value, found := animationTimingFieldValue(style, prop); found {
		return value, true
	}

	switch prop {
	case propAnimationIterationCount:
		return style.IterationCount, true
	case propAnimationDirection:
		return style.Direction, true
	case propAnimationFillMode:
		return style.FillMode, true
	case propAnimationPlayState:
		return style.PlayState, true
	case propAnimationComposition:
		return style.Composition, true
	default:
		return "", false
	}
}

func animationTimingFieldValue(style animationStyle, prop string) (string, bool) {
	switch prop {
	case propAnimationDuration:
		return style.Duration, true
	case propAnimationTimingFunction:
		return style.TimingFunction, true
	case propAnimationDelayStart:
		return style.DelayStart, true
	case propAnimationDelayEnd:
		return style.DelayEnd, true
	default:
		return "", false
	}
}

func animationExtendedFieldValue(style animationStyle, prop string) string {
	switch prop {
	case propAnimationName:
		return style.Name
	case propAnimationTimeline:
		return style.Timeline
	case propAnimationRangeStart:
		return style.RangeStart
	case propAnimationRangeEnd:
		return style.RangeEnd
	case propAnimationRangeCenter:
		return style.RangeCenter
	case propAnimationTrigger:
		return style.Trigger
	case propTriggerScope:
		return style.TriggerScope
	default:
		return animationViewTransitionFieldValue(style, prop)
	}
}

func animationViewTransitionFieldValue(style animationStyle, prop string) string {
	switch prop {
	case propViewTransitionName:
		return style.ViewTransitionName
	case propViewTransitionClass:
		return style.ViewTransitionClass
	case propViewTransitionGroup:
		return style.ViewTransitionGroup
	case propViewTransitionScope:
		return style.ViewTransitionScope
	default:
		return ""
	}
}

func setAnimationField(style *ResolvedStyle, prop, value string) {
	if !setAnimationCoreField(&style.Animation, prop, value) {
		setAnimationExtendedField(&style.Animation, prop, value)
	}

	if strings.HasPrefix(prop, "animation-") {
		style.Animation.Set = true
	}
}

func setAnimationCoreField(style *animationStyle, prop, value string) bool {
	if setAnimationTimingField(style, prop, value) {
		return true
	}

	switch prop {
	case propAnimationIterationCount:
		style.IterationCount = value
	case propAnimationDirection:
		style.Direction = value
	case propAnimationFillMode:
		style.FillMode = value
	case propAnimationPlayState:
		style.PlayState = value
	case propAnimationComposition:
		style.Composition = value
	default:
		return false
	}

	return true
}

func setAnimationTimingField(style *animationStyle, prop, value string) bool {
	switch prop {
	case propAnimationDuration:
		style.Duration = value
	case propAnimationTimingFunction:
		style.TimingFunction = value
	case propAnimationDelayStart:
		style.DelayStart = value
	case propAnimationDelayEnd:
		style.DelayEnd = value
	default:
		return false
	}

	return true
}

func setAnimationExtendedField(style *animationStyle, prop, value string) {
	switch prop {
	case propAnimationName:
		style.Name = value
	case propAnimationTimeline:
		style.Timeline = value
	case propAnimationRangeStart:
		style.RangeStart = value
	case propAnimationRangeEnd:
		style.RangeEnd = value
	case propAnimationRangeCenter:
		style.RangeCenter = value
	case propAnimationTrigger:
		style.Trigger = value
	case propTriggerScope:
		style.TriggerScope = value
	default:
		setAnimationViewTransitionField(style, prop, value)
	}
}

func setAnimationViewTransitionField(style *animationStyle, prop, value string) {
	switch prop {
	case propViewTransitionName:
		style.ViewTransitionName = value
	case propViewTransitionClass:
		style.ViewTransitionClass = value
	case propViewTransitionGroup:
		style.ViewTransitionGroup = value
	case propViewTransitionScope:
		style.ViewTransitionScope = value
	}
}

func animationListValueAccepted(value string, accept func(string) bool) bool {
	items, valid := animationCommaParts(value)
	if !valid {
		return false
	}

	for _, item := range items {
		if !accept(item) {
			return false
		}
	}

	return true
}

func animationCommaParts(value string) ([]string, bool) {
	return strictCommaParts(value)
}

func animationSpaceTokens(value string) ([]string, bool) {
	parser := animationSpaceTokenParser{
		value:   value,
		tokens:  []string{},
		depth:   0,
		quote:   0,
		start:   -1,
		escaped: false,
	}

	for index := range value {
		if !parser.consume(value[index], index) {
			return nil, false
		}
	}

	return parser.finish()
}

type animationSpaceTokenParser struct {
	value   string
	tokens  []string
	depth   int
	quote   byte
	start   int
	escaped bool
}

func (parser *animationSpaceTokenParser) consume(char byte, index int) bool {
	if parser.escaped {
		parser.escaped = false

		return true
	}

	if char == '\\' {
		if parser.start < 0 {
			parser.start = index
		}

		parser.escaped = true

		return true
	}

	if parser.quote != 0 {
		return parser.consumeQuoted(char)
	}

	return parser.consumeUnquoted(char, index)
}

func (parser *animationSpaceTokenParser) consumeQuoted(char byte) bool {
	if char == parser.quote {
		parser.quote = 0
	}

	return true
}

func (parser *animationSpaceTokenParser) consumeUnquoted(char byte, index int) bool {
	switch char {
	case '"', '\'':
		parser.startToken(index)
		parser.quote = char
	case '(':
		parser.startToken(index)

		parser.depth++
	case ')':
		parser.depth--
		if parser.depth < 0 {
			return false
		}
	case ' ', '\t', '\n', '\r', '\f', '\v':
		parser.endToken(index)
	default:
		parser.startToken(index)
	}

	return true
}

func (parser *animationSpaceTokenParser) startToken(index int) {
	if parser.start < 0 {
		parser.start = index
	}
}

func (parser *animationSpaceTokenParser) endToken(index int) {
	if parser.depth != 0 || parser.start < 0 {
		return
	}

	parser.tokens = append(parser.tokens, parser.value[parser.start:index])
	parser.start = -1
}

func (parser *animationSpaceTokenParser) finish() ([]string, bool) {
	if parser.depth != 0 || parser.quote != 0 || parser.escaped {
		return nil, false
	}

	if parser.start >= 0 {
		parser.tokens = append(parser.tokens, parser.value[parser.start:])
	}

	return parser.tokens, len(parser.tokens) > 0
}

func animationDurationValueAccepted(value string) bool {
	return strings.EqualFold(value, "auto") || animationTimeValueAccepted(value, false)
}

func animationTimeValueAccepted(value string, allowNegative bool) bool {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	unit, valid := transitionTimeUnit(lower)

	if !valid {
		return false
	}

	numberText := value[:len(value)-len(unit)]
	if strings.TrimSpace(numberText) != numberText {
		return false
	}

	number, valid := cssNumberValue(numberText)
	if !valid || (!allowNegative && number < 0) {
		return false
	}

	_, valid = cssTimeMilliseconds(value, allowNegative)

	return valid
}

func animationTimeToken(value string) bool {
	return animationTimeValueAccepted(value, true)
}

func animationIterationToken(value string) bool {
	return strings.EqualFold(value, "infinite") || animationNumberToken(value)
}

func animationNumberToken(value string) bool {
	_, ok := cssNumberValue(value)

	return ok
}

func animationIterationValueAccepted(value string) bool {
	return strings.EqualFold(value, "infinite") || animationNumberToken(value) && cssNumberNonnegative(value)
}

func cssNumberNonnegative(value string) bool {
	number, ok := cssNumberValue(value)

	return ok && number >= 0 && !math.IsInf(number, 0) && !math.IsNaN(number)
}

func animationDirectionValueAccepted(value string) bool {
	switch strings.ToLower(value) {
	case "normal", "reverse", "alternate", "alternate-reverse":
		return true
	default:
		return false
	}
}

func animationFillValueAccepted(value string) bool {
	switch strings.ToLower(value) {
	case "none", "forwards", "backwards", animationFillBoth:
		return true
	default:
		return false
	}
}

func animationPlayStateValueAccepted(value string) bool {
	switch strings.ToLower(value) {
	case "running", "paused":
		return true
	default:
		return false
	}
}

func animationCompositionValueAccepted(value string) bool {
	switch strings.ToLower(value) {
	case animationReplace, "add", "accumulate":
		return true
	default:
		return false
	}
}

func animationNameValueAccepted(value string) bool {
	if strings.EqualFold(value, "none") {
		return true
	}

	if animationCSSString(value) {
		return true
	}

	return animationCustomIdent(value, "none")
}

func animationKeyframesNameValueAccepted(value string) bool {
	if animationCSSString(value) {
		return true
	}

	return animationCustomIdent(value, "none")
}

func animationCustomIdent(value string, reserved ...string) bool {
	if !css.IsIdentToken(value) {
		return false
	}

	lower := strings.ToLower(value)
	if lower == flowKeywordDefault || cssWideKeyword(lower) {
		return false
	}

	for _, keyword := range reserved {
		if lower == keyword {
			return false
		}
	}

	return true
}

func animationCSSString(value string) bool {
	if len(value) < 2 || !isCSSQuote(value[0]) || value[len(value)-1] != value[0] {
		return false
	}

	return animationCSSStringBodyAccepted(value[1:len(value)-1], value[0])
}

func isCSSQuote(char byte) bool {
	return char == '\'' || char == '"'
}

func animationCSSStringBodyAccepted(value string, quote byte) bool {
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char == quote || isCSSNewline(char) {
			return false
		}

		if char == '\\' {
			if index+1 >= len(value) || isCSSNewline(value[index+1]) {
				return false
			}

			index++
		}
	}

	return true
}

func isCSSNewline(char byte) bool {
	return char == '\n' || char == '\r' || char == '\f'
}

func animationTimelineValueAccepted(value string) bool {
	lower := strings.ToLower(value)
	if lower == "auto" || lower == "none" {
		return true
	}

	if animationDashedIdent(value) {
		return true
	}

	return animationTimelineFunctionValueAccepted(value)
}

func animationTimelineFunctionValueAccepted(value string) bool {
	name, body, ok := animationFunctionParts(value)
	if !ok {
		return false
	}

	switch name {
	case "scroll":
		return animationScrollTimelineArgsAccepted(body)
	case "view":
		return animationViewTimelineArgsAccepted(body)
	default:
		return false
	}
}

func animationFunctionParts(value string) (string, string, bool) {
	open := strings.IndexByte(value, '(')
	if open <= 0 || value[len(value)-1] != ')' {
		return "", "", false
	}

	name := strings.TrimSpace(value[:open])
	if name == "" || strings.ContainsAny(name, " \t\n\r\f\v()") {
		return "", "", false
	}

	if !animationFunctionBodyBalanced(value[open:]) {
		return "", "", false
	}

	return strings.ToLower(name), value[open+1 : len(value)-1], true
}

func animationFunctionBodyBalanced(value string) bool {
	parser := animationFunctionParser{depth: 0, quote: 0, escaped: false}
	for index := range value {
		if !parser.consume(value[index], index, len(value)) {
			return false
		}
	}

	return parser.depth == 0 && parser.quote == 0 && !parser.escaped
}

type animationFunctionParser struct {
	depth   int
	quote   byte
	escaped bool
}

func (parser *animationFunctionParser) consume(char byte, index, length int) bool {
	if parser.escaped {
		parser.escaped = false

		return true
	}

	if char == '\\' {
		parser.escaped = true

		return true
	}

	if parser.quote != 0 {
		return parser.consumeQuoted(char)
	}

	return parser.consumeUnquoted(char, index, length)
}

func (parser *animationFunctionParser) consumeQuoted(char byte) bool {
	if char == parser.quote {
		parser.quote = 0
	}

	return true
}

func (parser *animationFunctionParser) consumeUnquoted(char byte, index, length int) bool {
	switch char {
	case '"', '\'':
		parser.quote = char
	case '(':
		parser.depth++
	case ')':
		parser.depth--
		if parser.depth < 0 || (parser.depth == 0 && index != length-1) {
			return false
		}
	}

	return true
}

func animationScrollTimelineArgsAccepted(body string) bool {
	tokens := strings.Fields(body)
	if len(tokens) > animationTimelineMaxArguments {
		return false
	}

	scroller, axis := false, false

	for _, token := range tokens {
		switch strings.ToLower(token) {
		case "root", "nearest", "self":
			if scroller {
				return false
			}

			scroller = true
		case marginTrimBlock, floatRefInline, "x", "y":
			if axis {
				return false
			}

			axis = true
		default:
			return false
		}
	}

	return true
}

func animationViewTimelineArgsAccepted(body string) bool {
	tokens := strings.Fields(body)
	if len(tokens) == 0 {
		return true
	}

	if isAnimationTimelineAxis(tokens[0]) {
		tokens = tokens[1:]
	}

	if len(tokens) == 0 {
		return true
	}

	if len(tokens) == 1 && strings.EqualFold(tokens[0], "auto") {
		return true
	}

	if len(tokens) > animationTimelineMaxArguments {
		return false
	}

	for _, token := range tokens {
		if !animationLengthPercentageValueAccepted(token) {
			return false
		}
	}

	return true
}

func isAnimationTimelineAxis(value string) bool {
	switch strings.ToLower(value) {
	case marginTrimBlock, floatRefInline, "x", "y":
		return true
	default:
		return false
	}
}

func animationDashedIdent(value string) bool {
	return strings.HasPrefix(value, "--") && len(value) > 2 && css.IsIdentToken(value)
}

func animationTimelineRangeName(value string) bool {
	switch strings.ToLower(value) {
	case animationRangeCover, animationRangeContain, "entry", "exit", "entry-crossing", "exit-crossing":
		return true
	default:
		return false
	}
}

func animationRangeCenterValueAccepted(value string) bool {
	tokens, ok := animationSpaceTokens(value)
	if !ok || len(tokens) == 0 || len(tokens) > 2 {
		return false
	}

	if len(tokens) == 1 {
		return strings.EqualFold(tokens[0], "normal") || strings.EqualFold(tokens[0], "source") ||
			strings.EqualFold(tokens[0], "target") || animationLengthPercentageValueAccepted(tokens[0])
	}

	center := strings.EqualFold(tokens[0], "source") || strings.EqualFold(tokens[0], "target")

	return center && animationLengthPercentageValueAccepted(tokens[1])
}

func animationTriggerValueAccepted(value string) bool {
	if strings.EqualFold(value, "none") {
		return true
	}

	tokens, ok := animationSpaceTokens(value)
	if !ok {
		return false
	}

	seenName := false
	currentHasAction := false

	for _, token := range tokens {
		if animationDashedIdent(token) {
			if seenName && !currentHasAction {
				return false
			}

			seenName = true
			currentHasAction = false

			continue
		}

		if !seenName || !animationActionValueAccepted(token) {
			return false
		}

		currentHasAction = true
	}

	return seenName && currentHasAction
}

func animationActionValueAccepted(value string) bool {
	switch strings.ToLower(value) {
	case "none", "play", "play-once", "play-forwards", "play-backwards", "pause", "reset", "replay":
		return true
	default:
		return false
	}
}

func triggerScopeValueAccepted(value string) bool {
	if strings.EqualFold(value, "none") || strings.EqualFold(value, "all") {
		return true
	}

	return animationListValueAccepted(value, animationDashedIdent)
}

func viewTransitionNameValueAccepted(value string) bool {
	return strings.EqualFold(value, "none") || animationCustomIdent(value, "none", "auto", "match-element")
}

func viewTransitionClassValueAccepted(value string) bool {
	if strings.EqualFold(value, "none") {
		return true
	}

	tokens := strings.Fields(value)
	if len(tokens) == 0 {
		return false
	}

	for _, token := range tokens {
		if !animationCustomIdent(token, "none") {
			return false
		}
	}

	return true
}

func viewTransitionGroupValueAccepted(value string) bool {
	switch strings.ToLower(value) {
	case "normal", animationRangeContain, "nearest":
		return true
	default:
		return animationCustomIdent(value, "normal", animationRangeContain, "nearest")
	}
}

func viewTransitionScopeValueAccepted(value string) bool {
	switch strings.ToLower(value) {
	case "none", "all":
		return true
	default:
		return false
	}
}

func animationLengthPercentageValueAccepted(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	if animationMathFunctionValueAccepted(value) {
		return true
	}

	if strings.ContainsAny(value, " \t\n\r\f\v") {
		return false
	}

	for index := len(value); index > 0; index-- {
		number, valid := cssNumberValue(value[:index])
		if !valid {
			continue
		}

		return animationLengthUnitAccepted(strings.ToLower(value[index:]), number)
	}

	return false
}

func animationLengthUnitAccepted(unit string, number float64) bool {
	if unit == "" {
		return number == 0
	}

	switch unit {
	case "px", "cm", "mm", "q", "in", "pc", "pt",
		"em", "ex", "ch", textBoxEdgeCapKeyword, "ic", "lh", remUnit, "rlh", "rex", "rch", "rcap", "ric",
		"vw", "vh", "vi", "vb", "vmin", "vmax",
		"svw", "svh", "svi", "svb", "svmin", "svmax",
		"lvw", "lvh", "lvi", "lvb", "lvmin", "lvmax",
		"dvw", "dvh", "dvi", "dvb", "dvmin", "dvmax",
		"cqw", "cqh", "cqi", "cqb", "cqmin", "cqmax", "%":
		return true
	default:
		return false
	}
}

func animationMathFunctionValueAccepted(value string) bool {
	name, _, valid := animationFunctionParts(value)
	if !valid || (name != "calc" && name != "min" && name != "max" && name != "clamp") {
		return false
	}

	if !animationMathUnitsAccepted(value) {
		return false
	}

	_, valid = css.EvalMath(value, css.MathEnv{
		FontSizePx: 0,
		PercentPx:  0,
		ViewportW:  0,
		ViewportH:  0,
	})

	return valid
}

func animationMathUnitsAccepted(value string) bool {
	for index := 0; index < len(value); {
		if !animationMathNumberStart(value, index) {
			index++

			continue
		}

		start := index
		index = consumeAnimationMathNumber(value, index)
		number, err := strconv.ParseFloat(value[start:index], 64)

		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return false
		}

		unitStart := index
		index = consumeAnimationMathUnit(value, index)
		unit := strings.ToLower(value[unitStart:index])

		if !animationMathUnitAccepted(unit, number) {
			return false
		}
	}

	return true
}

func animationMathNumberStart(value string, index int) bool {
	char := value[index]

	return isCSSNumericDigit(char) || (char == '.' && index+1 < len(value) && isCSSNumericDigit(value[index+1]))
}

func consumeAnimationMathNumber(value string, index int) int {
	for index < len(value) && (isCSSNumericDigit(value[index]) || value[index] == '.') {
		index++
	}

	return consumeAnimationMathExponent(value, index)
}

func consumeAnimationMathExponent(value string, index int) int {
	if index == len(value) || (value[index] != 'e' && value[index] != 'E') {
		return index
	}

	index++
	if index < len(value) && (value[index] == '+' || value[index] == '-') {
		index++
	}

	for index < len(value) && isCSSNumericDigit(value[index]) {
		index++
	}

	return index
}

func consumeAnimationMathUnit(value string, index int) int {
	for index < len(value) && (isCSSMathLetter(value[index]) || value[index] == '%') {
		index++
	}

	return index
}

func isCSSMathLetter(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
}

func animationMathUnitAccepted(unit string, number float64) bool {
	if unit == "" {
		return number == 0
	}

	switch unit {
	case "px", "pt", "pc", "in", "cm", "mm", "em", remUnit, "ex", "ch",
		"%", "vw", "vh", "dvh", "svh", "lvh", "vmin", "vmax":
		return true
	default:
		return false
	}
}
