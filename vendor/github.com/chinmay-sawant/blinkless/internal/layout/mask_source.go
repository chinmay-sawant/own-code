package layout

import (
	"strconv"
	"strings"
)

// maskGradientAccepted reports whether value is a CSS gradient function
// that the existing gradient parsers accept. Repeating forms are included.
// A broken gradient returns false so it does not replace an earlier image.
func maskGradientAccepted(value string) bool {
	if !isGradientFunc(value) {
		return false
	}

	current := [3]float64{0, 0, 0}
	if _, ok := parseLinearGradient(value, current); ok {
		return true
	}

	if _, ok := parseRadialGradient(value, current); ok {
		return true
	}

	_, ok := parseConicGradient(value, current)

	return ok
}

// maskGradientPNG rasterizes one gradient mask. width and height are the
// same point sizes renderGradientPNG already takes for backgrounds.
// The 512 cap stays inside renderGradientPNG. ok is false when the
// gradient does not parse or the size is not positive.
func maskGradientPNG(raw string, width, height float64, current [3]float64) ([]byte, int, int, bool) {
	if normalized, ok := normalizeMaskLinearGradient(raw, width, height); ok {
		raw = normalized
	}

	return renderGradientPNG(raw, width, height, current)
}

// maskImageSetChoice picks the 1x candidate from image-set(), or the first
// candidate when no 1x is declared. The result is that candidate's image
// text, not the whole image-set() string. An empty list is rejected.
func maskImageSetChoice(value string) (string, bool) {
	body, ok := imageSetBody(value)
	if !ok {
		return "", false
	}

	parts := splitParenArgs(body, ',')
	if len(parts) == 0 {
		return "", false
	}

	chosen := ""
	sawOneX := false

	for _, part := range parts {
		image, oneX, parsed := parseImageSetCandidate(strings.TrimSpace(part))
		if !parsed {
			return "", false
		}

		if chosen == "" {
			chosen = image
		}

		if oneX && !sawOneX {
			chosen = image
			sawOneX = true
		}
	}

	return chosen, chosen != ""
}

const imageSetPrefix = "image-set("

const imageSetDPPX = "dppx"

func imageSetBody(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)

	if !strings.HasPrefix(lower, imageSetPrefix) || !strings.HasSuffix(trimmed, ")") {
		return "", false
	}

	return strings.TrimSpace(trimmed[len(imageSetPrefix) : len(trimmed)-1]), true
}

func parseImageSetCandidate(value string) (string, bool, bool) {
	if value == "" {
		return "", false, false
	}

	image := ""
	oneX := false
	sawResolution := false

	for start := 0; ; {
		token, next, ok := nextSpaceToken(value, start)
		if !ok {
			break
		}

		if image == "" {
			if !maskImageSetImageToken(token) {
				return "", false, false
			}

			image = token
			start = next

			continue
		}

		if imageSetTypeToken(token) {
			start = next

			continue
		}

		isOne, resolutionOK := imageSetResolution(token)
		if !resolutionOK || sawResolution {
			return "", false, false
		}

		sawResolution = true
		oneX = isOne
		start = next
	}

	return image, oneX, image != ""
}

func maskImageSetImageToken(token string) bool {
	if maskGradientAccepted(token) {
		return true
	}

	if !cssURLFunctionValue(token) {
		return false
	}

	_, ok := firstCSSUrl(token)

	return ok
}

func imageSetTypeToken(token string) bool {
	lower := strings.ToLower(token)

	return strings.HasPrefix(lower, "type(") && strings.HasSuffix(token, ")")
}

func imageSetResolution(token string) (bool, bool) {
	lower := strings.ToLower(token)

	switch {
	case strings.HasSuffix(lower, imageSetDPPX):
		return imageSetScaleIsOne(lower, imageSetDPPX)
	case strings.HasSuffix(lower, "x"):
		return imageSetScaleIsOne(lower, "x")
	default:
		return false, false
	}
}

func imageSetScaleIsOne(lower, unit string) (bool, bool) {
	number, err := strconv.ParseFloat(lower[:len(lower)-len(unit)], 64)
	if err != nil || number <= 0 {
		return false, false
	}

	return number == 1, true
}
