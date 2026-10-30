package layout

import (
	"math"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
)

const (
	propTransition                  = "transition"
	propTransitionProperty          = "transition-property"
	propTransitionDuration          = "transition-duration"
	propTransitionDelay             = "transition-delay"
	propTransitionTimingFunction    = "transition-timing-function"
	propTransitionBehavior          = "transition-behavior"
	transitionBehaviorNormal        = "normal"
	transitionBehaviorAllowDiscrete = "allow-discrete"
)

func transitionLonghandProps() [5]string {
	return [...]string{
		propTransitionProperty,
		propTransitionDuration,
		propTransitionDelay,
		propTransitionTimingFunction,
		propTransitionBehavior,
	}
}

func transitionDeclarationValueAccepted(prop, value string) (bool, bool) {
	if !transitionProperty(prop) {
		return false, false
	}

	value = strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(value)) || containsVarFunc(value) {
		return true, true
	}

	if value == "" {
		return false, true
	}

	if prop == propTransition {
		return transitionShorthandValueAccepted(value)
	}

	return transitionLonghandValueAccepted(prop, value)
}

func transitionProperty(prop string) bool {
	switch prop {
	case propTransition, propTransitionProperty, propTransitionDuration, propTransitionDelay,
		propTransitionTimingFunction, propTransitionBehavior:
		return true
	default:
		return false
	}
}

func transitionShorthandValueAccepted(value string) (bool, bool) {
	_, accepted := expandTransitionShorthand(value)

	return accepted, true
}

func transitionLonghandValueAccepted(prop, value string) (bool, bool) {
	parts, valid := strictCommaParts(value)
	if !valid {
		return false, true
	}

	for _, part := range parts {
		if !transitionLonghandItemAccepted(prop, part) {
			return false, true
		}
	}

	if prop == propTransitionProperty && containsTransitionNone(parts) && len(parts) != 1 {
		return false, true
	}

	return true, true
}

func transitionLonghandItemAccepted(prop, value string) bool {
	value = strings.TrimSpace(value)

	switch prop {
	case propTransitionProperty:
		return transitionPropertyValueAccepted(value)
	case propTransitionDuration:
		return transitionTimeValueAccepted(value, false)
	case propTransitionDelay:
		return transitionTimeValueAccepted(value, true)
	case propTransitionTimingFunction:
		return easingValueAccepted(value)
	case propTransitionBehavior:
		switch strings.ToLower(value) {
		case transitionBehaviorNormal, transitionBehaviorAllowDiscrete:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func transitionPropertyValueAccepted(value string) bool {
	lower := strings.ToLower(value)
	if lower == "all" || lower == "none" {
		return true
	}

	return transitionCustomIdent(value)
}

func transitionCustomIdent(value string) bool {
	if !css.IsIdentToken(value) {
		return false
	}

	lower := strings.ToLower(value)
	if lower == "none" || lower == "default" || cssWideKeyword(lower) {
		return false
	}

	return true
}

func transitionTimeValueAccepted(value string, allowNegative bool) bool {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	unit, valid := transitionTimeUnit(lower)

	if !valid {
		return false
	}

	numberText := value[:len(value)-len(unit)]
	if numberText != strings.TrimSpace(numberText) {
		return false
	}

	number, valid := cssNumberValue(numberText)
	if !valid || (!allowNegative && number < 0) {
		return false
	}

	milliseconds, valid := cssTimeMilliseconds(value, allowNegative)

	return valid && !math.IsInf(milliseconds, 0)
}

func transitionTimeUnit(value string) (string, bool) {
	switch {
	case strings.HasSuffix(value, "ms"):
		return "ms", true
	case strings.HasSuffix(value, "s"):
		return "s", true
	default:
		return "", false
	}
}

func containsTransitionNone(parts []string) bool {
	for _, part := range parts {
		if strings.EqualFold(strings.TrimSpace(part), "none") {
			return true
		}
	}

	return false
}

// expandTransitionShorthand expands the transition shorthand, including its
// reset-only transition-behavior longhand, into the declaration cascade.
func expandTransitionShorthand(value string) ([]logicalPropDecl, bool) {
	value = strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(value)) {
		return transitionDeclarations(value, value, value, value, value), true
	}

	items, valid := strictCommaParts(value)
	if !valid {
		return nil, false
	}

	properties := make([]string, 0, len(items))
	durations := make([]string, 0, len(items))
	delays := make([]string, 0, len(items))
	timings := make([]string, 0, len(items))
	behaviors := make([]string, 0, len(items))

	for _, item := range items {
		property, duration, delay, timing, behavior, valid := parseSingleTransition(item)
		if !valid {
			return nil, false
		}

		properties = append(properties, property)
		durations = append(durations, duration)
		delays = append(delays, delay)
		timings = append(timings, timing)
		behaviors = append(behaviors, behavior)
	}

	if len(items) > 1 && containsTransitionNone(properties) {
		return nil, false
	}

	return transitionDeclarations(
		strings.Join(properties, ", "),
		strings.Join(durations, ", "),
		strings.Join(delays, ", "),
		strings.Join(timings, ", "),
		strings.Join(behaviors, ", "),
	), true
}

func parseSingleTransition(value string) (string, string, string, string, string, bool) {
	parts := transitionComponents{
		property:    "all",
		duration:    "0s",
		delay:       "0s",
		timing:      "ease",
		behavior:    transitionBehaviorNormal,
		propertySet: false,
		durationSet: false,
		delaySet:    false,
		timingSet:   false,
		behaviorSet: false,
	}

	var tokens [6]string
	count := splitSpaceTokens(value, tokens[:])

	if count == 0 || count > len(tokens) {
		return "", "", "", "", "", false
	}

	for _, token := range tokens[:count] {
		if !parts.addToken(token) {
			return "", "", "", "", "", false
		}
	}

	return parts.property, parts.duration, parts.delay, parts.timing, parts.behavior, true
}

type transitionComponents struct {
	property    string
	duration    string
	delay       string
	timing      string
	behavior    string
	propertySet bool
	durationSet bool
	delaySet    bool
	timingSet   bool
	behaviorSet bool
}

func (parts *transitionComponents) addToken(token string) bool {
	switch {
	case transitionPropertyKeyword(token):
		return parts.setProperty(token)
	case isTransitionTimeToken(token):
		return parts.setTime(token)
	case easingValueAccepted(token):
		return parts.setTiming(token)
	case isTransitionBehavior(token):
		return parts.setBehavior(token)
	default:
		return parts.setCustomProperty(token)
	}
}

func (parts *transitionComponents) setProperty(token string) bool {
	if parts.propertySet {
		return false
	}

	parts.property = strings.ToLower(token)
	parts.propertySet = true

	return true
}

func (parts *transitionComponents) setTime(token string) bool {
	if !parts.durationSet {
		if !transitionTimeValueAccepted(token, false) {
			return false
		}

		parts.duration, parts.durationSet = token, true

		return true
	}

	if parts.delaySet || !transitionTimeValueAccepted(token, true) {
		return false
	}

	parts.delay, parts.delaySet = token, true

	return true
}

func (parts *transitionComponents) setTiming(token string) bool {
	if parts.timingSet {
		return false
	}

	parts.timing, parts.timingSet = token, true

	return true
}

func (parts *transitionComponents) setBehavior(token string) bool {
	if parts.behaviorSet {
		return false
	}

	parts.behavior, parts.behaviorSet = strings.ToLower(token), true

	return true
}

func (parts *transitionComponents) setCustomProperty(token string) bool {
	if parts.propertySet || !transitionCustomIdent(token) {
		return false
	}

	parts.property, parts.propertySet = token, true

	return true
}

func transitionPropertyKeyword(token string) bool {
	switch strings.ToLower(token) {
	case "none", "all":
		return true
	default:
		return false
	}
}

func isTransitionTimeToken(value string) bool {
	lower := strings.ToLower(value)

	return strings.HasSuffix(lower, "ms") || strings.HasSuffix(lower, "s")
}

func isTransitionBehavior(value string) bool {
	switch strings.ToLower(value) {
	case transitionBehaviorNormal, transitionBehaviorAllowDiscrete:
		return true
	default:
		return false
	}
}

func transitionDeclarations(property, duration, delay, timing, behavior string) []logicalPropDecl {
	return []logicalPropDecl{
		{prop: propTransitionProperty, val: property},
		{prop: propTransitionDuration, val: duration},
		{prop: propTransitionDelay, val: delay},
		{prop: propTransitionTimingFunction, val: timing},
		{prop: propTransitionBehavior, val: behavior},
	}
}

func applyTransitionProps(
	style *ResolvedStyle,
	prop, value string,
	_ float64,
	_ *styleContext,
	parent *ResolvedStyle,
	hasParent bool,
) bool {
	if prop == propTransition {
		declarations, ok := expandTransitionShorthand(value)
		if !ok {
			return false
		}

		for _, declaration := range declarations {
			if !applyTransitionLonghand(style, declaration.prop, declaration.val, parent, hasParent) {
				return false
			}
		}

		return true
	}

	return applyTransitionLonghand(style, prop, value, parent, hasParent)
}

func applyTransitionLonghand(style *ResolvedStyle, prop, value string, parent *ResolvedStyle, hasParent bool) bool {
	if _, handled := transitionLonghandIndex(prop); !handled {
		return false
	}

	value = strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(value)) {
		setTransitionWideValue(style, prop, strings.ToLower(value), parent, hasParent)

		return true
	}

	if accepted, handled := transitionDeclarationValueAccepted(prop, value); !handled || !accepted {
		return false
	}

	setTransitionField(style, prop, value)

	return true
}

func transitionLonghandIndex(prop string) (int, bool) {
	for i, longhand := range transitionLonghandProps() {
		if prop == longhand {
			return i, true
		}
	}

	return 0, false
}

func setTransitionWideValue(style *ResolvedStyle, prop, keyword string, parent *ResolvedStyle, hasParent bool) {
	value := transitionInitialValue(prop)
	if keyword == inheritKeyword && hasParent && parent != nil && parent.Transition.Set {
		value = transitionFieldValue(parent.Transition, prop)
		if value == "" {
			value = transitionInitialValue(prop)
		}
	}

	setTransitionField(style, prop, value)
}

func transitionInitialValue(prop string) string {
	switch prop {
	case propTransitionProperty:
		return "all"
	case propTransitionDuration, propTransitionDelay:
		return "0s"
	case propTransitionTimingFunction:
		return "ease"
	case propTransitionBehavior:
		return transitionBehaviorNormal
	default:
		return ""
	}
}

func transitionFieldValue(style transitionStyle, prop string) string {
	switch prop {
	case propTransitionProperty:
		return style.Property
	case propTransitionDuration:
		return style.Duration
	case propTransitionDelay:
		return style.Delay
	case propTransitionTimingFunction:
		return style.TimingFunction
	case propTransitionBehavior:
		return style.Behavior
	default:
		return ""
	}
}

func setTransitionField(style *ResolvedStyle, prop, value string) {
	switch prop {
	case propTransitionProperty:
		style.Transition.Property = value
	case propTransitionDuration:
		style.Transition.Duration = value
	case propTransitionDelay:
		style.Transition.Delay = value
	case propTransitionTimingFunction:
		style.Transition.TimingFunction = value
	case propTransitionBehavior:
		style.Transition.Behavior = value
	default:
		return
	}

	style.Transition.Set = true
}
