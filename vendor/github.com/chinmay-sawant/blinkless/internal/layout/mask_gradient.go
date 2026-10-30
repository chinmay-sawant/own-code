package layout

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// maskStopProbeFontPt is the font size in points used to probe em and ex
// stops. Percentage stops are handled before the probe runs.
const maskStopProbeFontPt = 12

// maskStopPercentBase converts a fraction of the gradient-line length into a
// percentage stop position.
const maskStopPercentBase = 100.0

// directionKeywordDegrees maps an already-lowercased linear-gradient
// direction keyword ("top", "bottom right", ...) to its gradient angle in
// degrees. Unknown keywords keep the default bottom-pointing angle.
func directionKeywordDegrees(direction string) float64 {
	angle := 180.0

	switch direction {
	case "top":
		angle = 0
	case "right":
		angle = 90
	case "bottom":
		angle = 180
	case "left":
		angle = 270
	case "top right", "right top":
		angle = 45
	case "bottom right", "right bottom":
		angle = 135
	case "bottom left", "left bottom":
		angle = 225
	case "top left", "left top":
		angle = 315
	}

	return angle
}

// maskGradientPrefix reports whether low names a linear or repeating linear
// gradient function and returns the canonical function prefix.
func maskGradientPrefix(low string) (string, bool) {
	if strings.HasPrefix(low, "repeating-linear-gradient(") {
		return "repeating-linear-gradient(", true
	}

	if strings.HasPrefix(low, "linear-gradient(") {
		return "linear-gradient(", true
	}

	return "", false
}

// maskGradientLineLength is the gradient-line length in points for the given
// angle. A non-positive result floors to 1 so stop percentages stay finite.
func maskGradientLineLength(width, height, angleDeg float64) float64 {
	rad := angleDeg * math.Pi / degToRadFactor
	sinA := math.Sin(rad)
	cosA := math.Cos(rad)
	length := math.Abs(width*sinA) + math.Abs(height*cosA)

	if length <= 0 {
		return 1
	}

	return length
}

// normalizeMaskLinearGradient converts CSS length stop positions in linear
// (and repeating-linear) gradient masks into percentage positions along the
// gradient line. renderGradientPNG stores stop positions verbatim, which
// means `black 6px, transparent 12px` would be interpreted as t=6..12
// instead of 6..12 CSS pixels measured along the gradient line.
//
// The returned string keeps the original angle and colors; only positions
// expressed as lengths are rewritten as percentages of the rasterized box's
// gradient-line length.
func normalizeMaskLinearGradient(raw string, width, height float64) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	low := strings.ToLower(trimmed)

	prefix, found := maskGradientPrefix(low)
	if !found {
		return "", false
	}

	body := trimmed[len(prefix) : len(trimmed)-1]
	parts := splitFunctionArgs(body)

	if len(parts) == 0 {
		return "", false
	}

	angle := 180.0
	stopStart := 0
	firstPart := strings.TrimSpace(parts[0])
	firstLow := strings.ToLower(firstPart)

	if strings.HasPrefix(firstLow, "to ") {
		stopStart = 1
		angle = directionKeywordDegrees(strings.TrimSpace(firstLow[3:]))
	} else if deg, ok := parseAngleDeg(firstPart); ok {
		stopStart = 1
		angle = deg
	}

	if stopStart >= len(parts) {
		return "", false
	}

	length := maskGradientLineLength(width, height, angle)

	// Rebuild parts with normalized stops.
	newParts := make([]string, len(parts))
	copy(newParts, parts)

	for partIdx := stopStart; partIdx < len(parts); partIdx++ {
		normalized, ok := normalizeMaskGradientStop(strings.TrimSpace(parts[partIdx]), length)
		if !ok {
			return "", false
		}

		newParts[partIdx] = normalized
	}

	args := strings.Join(newParts, ", ")

	return prefix + args + ")", true
}

func normalizeMaskGradientStop(stop string, lineLength float64) (string, bool) {
	tokens := strings.Fields(stop)
	if len(tokens) == 0 {
		return "", false
	}

	out := make([]string, 0, len(tokens))

	for _, tok := range tokens {
		if strings.HasSuffix(tok, "%") {
			out = append(out, tok)

			continue
		}

		if _, err := strconv.ParseFloat(strings.TrimSuffix(tok, "%"), 64); err == nil && strings.Contains(tok, "%") {
			out = append(out, tok)

			continue
		}

		if v, ok := plainLength(tok, maskStopProbeFontPt, 0); ok {
			percent := 0.0
			if lineLength > 0 {
				percent = v / lineLength * maskStopPercentBase
			}

			out = append(out, fmt.Sprintf("%.6g%%", percent))

			continue
		}

		out = append(out, tok)
	}

	return strings.Join(out, " "), true
}
