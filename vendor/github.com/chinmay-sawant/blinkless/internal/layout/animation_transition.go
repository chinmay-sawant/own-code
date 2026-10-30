package layout

import (
	"math"
	"strconv"
	"strings"
)

// animationStyle stores the accepted animation declarations before the
// comma-separated values are aligned into records for the host.
type animationStyle struct {
	Set                 bool
	Duration            string
	TimingFunction      string
	IterationCount      string
	Direction           string
	FillMode            string
	PlayState           string
	Composition         string
	DelayStart          string
	DelayEnd            string
	Name                string
	Timeline            string
	RangeStart          string
	RangeEnd            string
	RangeCenter         string
	Trigger             string
	TriggerScope        string
	ViewTransitionName  string
	ViewTransitionClass string
	ViewTransitionGroup string
	ViewTransitionScope string
}

// transitionStyle stores accepted transition declarations before the
// comma-separated values are aligned into records for the host.
type transitionStyle struct {
	Set            bool
	Property       string
	Duration       string
	Delay          string
	TimingFunction string
	Behavior       string
}

// AnimationRecord is the timing and keyframe name for one animation list
// item. Times are milliseconds. The host samples the record and paints frames.
type AnimationRecord struct {
	Name              string
	DurationMS        float64
	DurationAuto      bool
	TimingFunction    string
	IterationCount    float64
	IterationInfinite bool
	Direction         string
	FillMode          string
	PlayState         string
	Composition       string
	DelayStartMS      float64
	DelayEndMS        float64
	Timeline          string
	RangeStart        string
	RangeEnd          string
	RangeCenter       string
	Trigger           string
}

// TransitionRecord contains the values for one transition list item. The
// host compares display lists and samples this timing data.
type TransitionRecord struct {
	Property       string
	DurationMS     float64
	DelayMS        float64
	TimingFunction string
	Behavior       string
}

// animationRecordsFromStyle resolves the comma-separated animation longhands
// into the host-facing records. Each list repeats its final cycle to match the
// animation-name list, as CSS list-valued properties require.
func animationRecordsFromStyle(style animationStyle) []AnimationRecord {
	if !style.Set {
		return nil
	}

	names := cssListOr(style.Name, "none")
	records := make([]AnimationRecord, len(names))

	for index, name := range names {
		duration, durationAuto := animationDurationAt(style.Duration, index)
		iteration, iterationInfinite := animationIterationAt(style.IterationCount, index)
		delayStart, _ := cssTimeMilliseconds(cssListAtOr(style.DelayStart, index, "0s"), true)
		delayEnd, _ := cssTimeMilliseconds(cssListAtOr(style.DelayEnd, index, "0s"), false)

		records[index] = AnimationRecord{
			Name:              name,
			DurationMS:        duration,
			DurationAuto:      durationAuto,
			TimingFunction:    cssListAtOr(style.TimingFunction, index, "ease"),
			IterationCount:    iteration,
			IterationInfinite: iterationInfinite,
			Direction:         cssListAtOr(style.Direction, index, "normal"),
			FillMode:          cssListAtOr(style.FillMode, index, "none"),
			PlayState:         cssListAtOr(style.PlayState, index, "running"),
			Composition:       cssListAtOr(style.Composition, index, "replace"),
			DelayStartMS:      delayStart,
			DelayEndMS:        delayEnd,
			Timeline:          cssListAtOr(style.Timeline, index, "auto"),
			RangeStart:        cssListAtOr(style.RangeStart, index, "normal"),
			RangeEnd:          cssListAtOr(style.RangeEnd, index, "normal"),
			RangeCenter:       cssListAtOr(style.RangeCenter, index, "normal"),
			Trigger:           cssListAtOr(style.Trigger, index, "none"),
		}
	}

	return records
}

// transitionRecordsFromStyle aligns transition longhands to transition-property.
func transitionRecordsFromStyle(style transitionStyle) []TransitionRecord {
	if !style.Set {
		return nil
	}

	properties := cssListOr(style.Property, "all")
	records := make([]TransitionRecord, len(properties))

	for index, property := range properties {
		duration, _ := cssTimeMilliseconds(cssListAtOr(style.Duration, index, "0s"), false)
		delay, _ := cssTimeMilliseconds(cssListAtOr(style.Delay, index, "0s"), true)
		records[index] = TransitionRecord{
			Property:       property,
			DurationMS:     duration,
			DelayMS:        delay,
			TimingFunction: cssListAtOr(style.TimingFunction, index, "ease"),
			Behavior:       cssListAtOr(style.Behavior, index, "normal"),
		}
	}

	return records
}

func animationDurationAt(value string, index int) (float64, bool) {
	value = cssListAtOr(value, index, "auto")
	if value == "auto" {
		return 0, true
	}

	ms, _ := cssTimeMilliseconds(value, false)

	return ms, false
}

func animationIterationAt(value string, index int) (float64, bool) {
	value = cssListAtOr(value, index, "1")
	if value == "infinite" {
		return 0, true
	}

	count, _ := strconv.ParseFloat(value, 64)

	return count, false
}

func cssListOr(value, fallback string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{fallback}
	}

	return splitCommaLayers(value)
}

func cssListAtOr(value string, index int, fallback string) string {
	values := cssListOr(value, fallback)
	if len(values) == 0 {
		return fallback
	}

	return values[index%len(values)]
}

// cssTimeMilliseconds parses the CSS <time> units used by the public records.
// It is shared by the animation and transition declaration handlers.
func cssTimeMilliseconds(raw string, allowNegative bool) (float64, bool) {
	value := strings.TrimSpace(strings.ToLower(raw))
	value, unitScale, valid := cssTimeNumberAndScale(value)

	if !valid {
		return 0, false
	}

	number, valid := cssTimeNumberAccepted(value, allowNegative)
	if !valid {
		return 0, false
	}

	milliseconds := number * unitScale
	if math.IsInf(milliseconds, 0) || math.IsNaN(milliseconds) {
		return 0, false
	}

	return milliseconds, true
}

func cssTimeNumberAndScale(value string) (string, float64, bool) {
	unitScale := 1.0

	switch {
	case strings.HasSuffix(value, "ms"):
		value = strings.TrimSpace(strings.TrimSuffix(value, "ms"))
	case strings.HasSuffix(value, "s"):
		unitScale = 1000
		value = strings.TrimSpace(strings.TrimSuffix(value, "s"))
	default:
		return "", 0, false
	}

	return value, unitScale, true
}

func cssTimeNumberAccepted(value string, allowNegative bool) (float64, bool) {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || (!allowNegative && number < 0) {
		return 0, false
	}

	return number, true
}

func normalizeWebkitAnimationTransitionProperty(prop string) (string, bool) {
	if normalized, ok := normalizeWebkitAnimationProperty(prop); ok {
		return normalized, true
	}

	return normalizeWebkitTransitionProperty(prop)
}

func normalizeWebkitAnimationProperty(prop string) (string, bool) {
	if prop == "-webkit-animation" {
		return propAnimation, true
	}

	return normalizeWebkitAnimationLonghand(prop)
}

func normalizeWebkitAnimationLonghand(prop string) (string, bool) {
	switch prop {
	case "-webkit-animation-delay":
		return propAnimationDelay, true
	case "-webkit-animation-direction":
		return propAnimationDirection, true
	case "-webkit-animation-duration":
		return propAnimationDuration, true
	case "-webkit-animation-fill-mode":
		return propAnimationFillMode, true
	case "-webkit-animation-iteration-count":
		return propAnimationIterationCount, true
	case "-webkit-animation-name":
		return propAnimationName, true
	case "-webkit-animation-play-state":
		return propAnimationPlayState, true
	case "-webkit-animation-timing-function":
		return propAnimationTimingFunction, true
	default:
		return prop, false
	}
}

func normalizeWebkitTransitionProperty(prop string) (string, bool) {
	switch prop {
	case "-webkit-transition":
		return propTransition, true
	case "-webkit-transition-delay":
		return propTransitionDelay, true
	case "-webkit-transition-duration":
		return propTransitionDuration, true
	case "-webkit-transition-property":
		return propTransitionProperty, true
	case "-webkit-transition-timing-function":
		return propTransitionTimingFunction, true
	default:
		return prop, false
	}
}
