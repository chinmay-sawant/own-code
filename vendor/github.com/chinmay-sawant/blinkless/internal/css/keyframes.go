package css

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

const keyframePercentScale = 100

// KeyframesRule is one @keyframes or @-webkit-keyframes rule. Stylesheet keeps
// repeated names in source order so a consumer can apply the last-rule-wins
// rule from CSS Animations.
type KeyframesRule struct {
	Name      string
	Keyframes []Keyframe
}

// Keyframe stores one offset and the declarations written for it. A selector
// list creates one Keyframe per offset, each with its own declaration slice.
type Keyframe struct {
	Offset       float64
	Declarations []Declaration
}

func parseKeyframesRule(src string, stylesheet *Stylesheet) (string, error) {
	open, err := findBlock(src)
	if err != nil {
		return skipAtRule(src)
	}

	body, rest, err := takeBlock(src, open)
	if err != nil {
		return "", err
	}

	prefixLen := len("@keyframes")
	if hasFoldPrefix(src, "@-webkit-keyframes") {
		prefixLen = len("@-webkit-keyframes")
	}

	if open <= prefixLen || !isKeyframesSpace(src[prefixLen]) {
		return rest, nil
	}

	name, ok := parseKeyframesName(src[prefixLen:open])
	if !ok {
		return rest, nil
	}

	keyframes, err := parseKeyframeBlocks(body)
	if err != nil {
		return "", err
	}

	stylesheet.Keyframes = append(stylesheet.Keyframes, KeyframesRule{
		Name:      name,
		Keyframes: keyframes,
	})

	return rest, nil
}

func parseKeyframeBlocks(body string) ([]Keyframe, error) {
	keyframes := make([]Keyframe, 0)

	for body != "" {
		body = strings.TrimLeft(body, " \t\r\n\f")
		if body == "" {
			break
		}

		open, err := findBlock(body)
		if err != nil {
			if errors.Is(err, errNoBlock) {
				next := skipGarbagePrelude(body)
				if next == body {
					break
				}

				body = next

				continue
			}

			break
		}

		declarations, rest, err := takeBlock(body, open)
		if err != nil {
			return nil, err
		}

		offsets, valid := parseKeyframeSelectorList(body[:open])
		if valid {
			decls := parseDeclarations(declarations)
			for _, offset := range offsets {
				keyframes = append(keyframes, Keyframe{
					Offset:       offset,
					Declarations: append([]Declaration(nil), decls...),
				})
			}
		}

		body = rest
	}

	return keyframes, nil
}

func parseKeyframesName(prelude string) (string, bool) {
	prelude = strings.TrimSpace(prelude)
	if prelude == "" {
		return "", false
	}

	if prelude[0] == '\'' || prelude[0] == '"' {
		return parseQuotedKeyframesName(prelude)
	}

	if !IsIdentToken(prelude) {
		return "", false
	}

	switch strings.ToLower(prelude) {
	case "none", "initial", "inherit", "unset", "revert", "revert-layer":
		return "", false
	default:
		return prelude, true
	}
}

func parseQuotedKeyframesName(prelude string) (string, bool) {
	quote := prelude[0]

	for idx := 1; idx < len(prelude); idx++ {
		switch prelude[idx] {
		case '\\':
			idx++
		case quote:
			if strings.TrimSpace(prelude[idx+1:]) != "" {
				return "", false
			}

			return prelude[1:idx], true
		case '\n', '\r', '\f':
			return "", false
		}
	}

	return "", false
}

func parseKeyframeSelectorList(selectorList string) ([]float64, bool) {
	selectors := splitTopLevel(selectorList, ',')
	offsets := make([]float64, 0, len(selectors))

	for _, selector := range selectors {
		offset, ok := parseKeyframeSelector(selector)
		if !ok {
			return nil, false
		}

		offsets = append(offsets, offset)
	}

	return offsets, len(offsets) > 0
}

func parseKeyframeSelector(selector string) (float64, bool) {
	selector = strings.TrimSpace(selector)

	switch strings.ToLower(selector) {
	case "from":
		return 0, true
	case "to":
		return 1, true
	}

	if !strings.HasSuffix(selector, "%") {
		return 0, false
	}

	value, err := strconv.ParseFloat(selector[:len(selector)-1], 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > keyframePercentScale {
		return 0, false
	}

	return value / keyframePercentScale, true
}

func isKeyframesSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '\f':
		return true
	default:
		return false
	}
}
