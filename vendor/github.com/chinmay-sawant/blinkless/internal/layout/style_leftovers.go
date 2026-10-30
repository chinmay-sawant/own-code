//nolint:all // centralized property dispatch for Wave 80.5
package layout

import (
	"math"
	"strconv"
	"strings"
)

// applyLeftoversProps handles SVG presentation and individual transform properties.
func applyLeftoversProps(style *ResolvedStyle, prop, value string, fsize float64) bool {
	switch prop {
	case "stroke-dasharray":
		applyStrokeDashArray(style, value, fsize)
	case "stroke-dashoffset":
		if off, ok := plainLength(value, fsize, 0); ok {
			style.StrokeDashOffset = off
		}
	case "stroke-linecap":
		style.StrokeLineCap = strings.ToLower(strings.TrimSpace(value))
	case "stroke-linejoin":
		style.StrokeLineJoin = strings.ToLower(strings.TrimSpace(value))
	case "stroke-miterlimit":
		if n, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && n >= 1 {
			style.StrokeMiterLimit = n
		}
	case "clip-rule":
		applyClipRuleProperty(style, value)
	case "color-interpolation":
		applyColorInterpolationProperty(style, value)
	case "color-interpolation-filters":
		applyColorInterpolationFiltersProperty(style, value)
	case "transform-box":
		applyTransformBoxProperty(style, value)
	case "transform-style":
		applyTransformStyleProperty(style, value)
	case "backface-visibility":
		applyBackfaceVisibilityProperty(style, value)
	case "perspective":
		applyPerspectiveProperty(style, value)
	case "perspective-origin":
		applyPerspectiveOriginProperty(style, value, fsize)
	case "fill-rule":
		applyFillRuleProperty(style, value)
	case "shape-rendering":
		applyShapeRenderingProperty(style, value)
	case "dominant-baseline":
		applyDominantBaselineProperty(style, value)
	case "alignment-baseline":
		applyAlignmentBaselineProperty(style, value)
	case "clip":
		applyClipProperty(style, value, fsize)
	case "rotate":
		applyRotateProperty(style, value)
	case "scale":
		applyScaleProperty(style, value)
	case "translate":
		applyTranslateProperty(style, value, fsize)

	default:
		return false
	}

	return true
}

func applyStrokeDashArray(style *ResolvedStyle, value string, fsize float64) {
	val := strings.TrimSpace(value)
	if val == "none" || val == "" {
		style.StrokeDashArray = nil
		return
	}
	raw := strings.ReplaceAll(val, ",", " ")
	for _, tok := range strings.Fields(raw) {
		if length, ok := plainLength(tok, fsize, 0); ok && length >= 0 {
			style.StrokeDashArray = append(style.StrokeDashArray, length)
		} else if n, err := strconv.ParseFloat(tok, 64); err == nil && n >= 0 {
			style.StrokeDashArray = append(style.StrokeDashArray, n)
		}
	}
}

// applyClipRuleProperty owns the standalone clip-rule property. Only the
// nonzero and evenodd fill rules are stored canonically; anything else leaves
// the previous declaration intact so the mask keeps its current rule.
func applyClipRuleProperty(style *ResolvedStyle, value string) {
	normalized := normalizeCSSValue(value)
	if normalized == clipPathEvenOdd || normalized == clipPathNonZero {
		style.ClipRule = normalized
	}
}

// applyColorInterpolationProperty owns color-interpolation. Only auto, srgb,
// and linearrgb (with or without a hyphen) are stored canonically; anything
// else leaves the previous declaration intact. srgb and auto sample gradients
// in gamma space; linearrgb samples in linear light with an sRGB round-trip.
// Reference: Chrome 143.0.7499.40.
func applyColorInterpolationProperty(style *ResolvedStyle, value string) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "")
	switch normalized {
	case "auto", "srgb", "linearrgb":
		if normalized == "auto" {
			style.ColorInterpolation = "srgb"
		} else {
			style.ColorInterpolation = normalized
		}
	}
}

// applyColorInterpolationFiltersProperty owns color-interpolation-filters with
// the same canonical values as color-interpolation. srgb and auto run filter
// primitives in gamma space; linearrgb runs them in linear light with an sRGB
// round-trip. Reference: Chrome 143.0.7499.40.
func applyColorInterpolationFiltersProperty(style *ResolvedStyle, value string) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "")
	switch normalized {
	case "auto", "srgb", "linearrgb":
		if normalized == "auto" {
			style.ColorInterpolationFilters = "srgb"
		} else {
			style.ColorInterpolationFilters = normalized
		}
	}
}

// applyTransformBoxProperty owns transform-box. Only the five spec boxes are
// stored canonically; anything else leaves the previous declaration intact.
// Reference: Chrome 143.0.7499.40 accepts content-box, border-box, fill-box,
// stroke-box, view-box.
func applyTransformBoxProperty(style *ResolvedStyle, value string) {
	switch normalized := normalizeCSSValue(value); normalized {
	case "content-box", "border-box", "fill-box", "stroke-box", "view-box":
		style.TransformBox = normalized
	}
}

// applyTransformStyleProperty owns transform-style. Only flat and preserve-3d
// are stored; anything else leaves the previous declaration intact. preserve-3d
// composes nested 3D before one flatten. flat flattens at this element.
// Reference: Chrome 143.0.7499.40.
func applyTransformStyleProperty(style *ResolvedStyle, value string) {
	switch normalized := normalizeCSSValue(value); normalized {
	case "flat", "preserve-3d":
		style.TransformStyle = normalized
	}
}

// applyBackfaceVisibilityProperty owns backface-visibility. Only visible and
// hidden are stored; anything else leaves the previous declaration intact.
// hidden zeroes a 3D face whose flattened facing is negative, including a
// child turned around by a preserve-3d parent. A 2D rotation is not culled.
// Reference: Chrome 143.0.7499.40.
func applyBackfaceVisibilityProperty(style *ResolvedStyle, value string) {
	switch normalized := normalizeCSSValue(value); normalized {
	case "visible", "hidden":
		style.BackfaceVisibility = normalized
	}
}

// applyPerspectiveProperty owns the perspective property. none clears the
// distance; a positive length stores the camera distance in pt. The pre-pass
// copies that distance onto a 3D descendant, and boxTransformAccum projects
// with it. Reference: Chrome 143.0.7499.40.
func applyPerspectiveProperty(style *ResolvedStyle, value string) {
	val := strings.TrimSpace(strings.ToLower(value))
	if val == "" || val == cssDisplayNone {
		style.HasPerspective = false
		style.PerspectiveDist = 0

		return
	}

	pt, _, isPct, ok := parseTransformLength(val, 0)
	if !ok || isPct || pt <= 0 {
		return
	}

	style.PerspectiveDist = pt
	style.HasPerspective = true
}

// applyPerspectiveOriginProperty owns perspective-origin. One or two tokens
// from percentages, lengths, and the left/center/right/top/bottom keywords
// store the vanishing-point offset; anything else leaves the previous
// declaration intact. perspectiveCenter reads the stored offset when it
// projects a 3D descendant. Reference: Chrome 143.0.7499.40.
func applyPerspectiveOriginProperty(style *ResolvedStyle, value string, fsize float64) {
	fields := strings.Fields(strings.ToLower(value))
	if len(fields) == 0 || len(fields) > 2 {
		return
	}

	resolve := func(tok string) (float64, bool, bool, bool) {
		// returns (v, isPct, isVertical, ok)
		switch tok {
		case "left":
			return 0, true, false, true
		case "center":
			return 50, true, false, true
		case "right":
			return 100, true, false, true
		case "top":
			return 0, true, true, true
		case "bottom":
			return 100, true, true, true
		}

		if strings.HasSuffix(tok, "%") {
			num, ok := parseUnitless(strings.TrimSuffix(tok, "%"))
			if ok {
				return num, true, false, true
			}

			return 0, false, false, false
		}

		pt, _, isPct, ok := parseTransformLength(tok, fsize)
		if !ok || isPct {
			return 0, false, false, false
		}

		return pt, false, false, true
	}

	first, firstPct, firstVert, ok := resolve(fields[0])
	if !ok {
		return
	}

	second, secondPct := 50.0, true

	if len(fields) == 2 {
		var ok bool

		second, secondPct, _, ok = resolve(fields[1])
		if !ok {
			return
		}
	}

	x, xPct, y, yPct := first, firstPct, second, secondPct
	if len(fields) == 1 {
		// Single vertical keyword sets Y and centers X; anything else sets
		// X and centers Y.
		if firstVert {
			x, xPct, y, yPct = 50, true, first, firstPct
		} else {
			y, yPct = 50, true
		}
	} else if firstVert && isHorizTok(fields[1]) {
		// "top left" order: swap so X stays horizontal.
		x, xPct, y, yPct = second, secondPct, first, firstPct
	}
	style.PerspectiveOriginX = x
	style.PerspectiveOriginXPct = xPct
	style.PerspectiveOriginY = y
	style.PerspectiveOriginYPct = yPct
	style.PerspectiveOriginSet = true
}

// isHorizTok reports whether tok is a horizontal origin keyword.
func isHorizTok(tok string) bool {
	return tok == "left" || tok == "right"
}

// and evenodd are stored canonically; anything else leaves the previous
// declaration intact. The external canvas rasterizer (tdewolff/canvas via
// internal/svg) has no fill-rule case even though its core Style carries a
// FillRule field, so the serialized SVG bake cannot observe it and clip masks
// keep using ClipRule (pinned raster gap). Reference: Chrome 143.0.7499.40
// would apply evenodd winding to holed paths.
func applyFillRuleProperty(style *ResolvedStyle, value string) {
	normalized := normalizeCSSValue(value)
	if normalized == clipPathEvenOdd || normalized == clipPathNonZero {
		style.FillRule = normalized
	}
}

// applyShapeRenderingProperty owns shape-rendering. Only the four spec hints
// are stored canonically; anything else leaves the previous declaration
// intact. The external canvas rasterizer has no shape-rendering case, so the
// hint bakes into serialized SVG but cannot change raster pixels (pinned gap).
// Reference: Chrome 143.0.7499.40 would snap edges with crispEdges.
func applyShapeRenderingProperty(style *ResolvedStyle, value string) {
	switch normalized := normalizeCSSValue(value); normalized {
	case "auto", "optimizespeed", "crispedges", "geometricprecision":
		style.ShapeRendering = normalized
	}
}

// dominantBaselineHanging is the hanging baseline selector, shared by the
// applier below and the vertical-align shift consumer.
const dominantBaselineHanging = "hanging"

// applyDominantBaselineProperty owns dominant-baseline. Only the baseline
// selectors below are stored canonically; anything else leaves the previous
// declaration intact. Hanging shifts text in effectiveVerticalAlignShift;
// other baselines pin to alphabetic until font baseline tables exist.
// Reference: Chrome 143.0.7499.40.
func applyDominantBaselineProperty(style *ResolvedStyle, value string) {
	switch normalized := normalizeCSSValue(value); normalized {
	case "auto", "alphabetic", dominantBaselineHanging, "ideographic", "middle", "central",
		"mathematical", "text-before-edge", "text-after-edge":
		style.DominantBaseline = normalized
	}
}

// applyAlignmentBaselineProperty owns alignment-baseline. Only the
// selectors below are stored canonically; anything else leaves the previous
// declaration intact. auto and baseline defer to DominantBaseline in
// effectiveVerticalAlignShift; other selectors shift the run from font
// metrics there. Not inherited per CSS Inline 3, so no parent copy in
// style_cascade.go. Reference: Chrome 143.0.7499.40.
func applyAlignmentBaselineProperty(style *ResolvedStyle, value string) {
	switch normalized := normalizeCSSValue(value); normalized {
	case "auto", "baseline", "alphabetic", "hanging", "ideographic", "middle", "central",
		"mathematical", "text-before-edge", "text-after-edge", "before-edge", "after-edge":
		style.AlignmentBaseline = normalized
	}
}

// clipRectEdges is a parsed deprecated clip rect: each edge is auto (no cut
// on that side) or a length resolved by the clip-path length parser.
type clipRectEdges struct {
	top, right, bottom, left clipPathLength
	topAuto, rightAuto       bool
	bottomAuto, leftAuto     bool
}

// applyClipProperty owns the deprecated clip property. auto clears to the
// no-clip initial; a valid rect() stores canonically, resolved against the
// border box in overflow_clip.go. Anything else leaves the previous
// declaration intact. Only absolutely positioned boxes clip (checked at the
// clip site, per CSS 2.1). Reference: Chrome 143.0.7499.40.
func applyClipProperty(style *ResolvedStyle, value string, fsize float64) {
	normalized := normalizeCSSValue(value)
	if normalized == "auto" {
		style.Clip = ""
		return
	}
	if edges, ok := parseDeprecatedClipRect(normalized, fsize); ok {
		style.Clip = formatDeprecatedClipRect(edges)
	}
}

// parseDeprecatedClipRect parses rect(top, right, bottom, left) where each
// component is auto or a non-negative length. Lengths reuse
// parseClipPathLength so clip maps onto the existing clip-path machinery.
// Commas are required per CSS 2.1 but stray spacing is tolerated.
func parseDeprecatedClipRect(raw string, fsize float64) (clipRectEdges, bool) {
	var empty clipRectEdges
	lowered := strings.ToLower(strings.TrimSpace(raw))
	if !strings.HasPrefix(lowered, "rect(") || !strings.HasSuffix(lowered, ")") {
		return empty, false
	}
	inner := lowered[len("rect(") : len(lowered)-1]
	inner = strings.ReplaceAll(inner, ",", " ")
	fields := strings.Fields(inner)
	if len(fields) != 4 {
		return empty, false
	}
	var edges clipRectEdges
	autos := []*bool{&edges.topAuto, &edges.rightAuto, &edges.bottomAuto, &edges.leftAuto}
	lengths := []*clipPathLength{&edges.top, &edges.right, &edges.bottom, &edges.left}
	for i, field := range fields {
		if field == "auto" {
			*autos[i] = true
			continue
		}
		length, ok := parseClipPathLength(field, fsize)
		if !ok {
			return empty, false
		}
		*lengths[i] = length
	}
	return edges, true
}

// formatDeprecatedClipRect renders parsed edges back to canonical rect()
// text for used-value storage.
func formatDeprecatedClipRect(edges clipRectEdges) string {
	parts := make([]string, 4)
	lengths := []clipPathLength{edges.top, edges.right, edges.bottom, edges.left}
	autos := []bool{edges.topAuto, edges.rightAuto, edges.bottomAuto, edges.leftAuto}
	for i := range parts {
		if autos[i] {
			parts[i] = "auto"
			continue
		}
		if lengths[i].percent {
			parts[i] = strconv.FormatFloat(lengths[i].value*100, 'g', -1, 64) + "%"
			continue
		}
		parts[i] = strconv.FormatFloat(lengths[i].value, 'g', -1, 64) + "pt"
	}
	return "rect(" + strings.Join(parts, ", ") + ")"
}

func applyRotateProperty(style *ResolvedStyle, value string) {
	rad, ok := parseCSSAngle(value)
	if !ok {
		return
	}
	cos := math.Cos(rad)
	sin := math.Sin(rad)
	rot := Matrix2D{A: cos, B: sin, C: -sin, D: cos, E: 0, F: 0}
	style.Transform = style.Transform.Mul(rot)
	style.HasTransform = true
}

func applyScaleProperty(style *ResolvedStyle, value string) {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return
	}
	sx, err1 := strconv.ParseFloat(parts[0], 64)
	if err1 != nil {
		return
	}
	sy := sx
	if len(parts) >= 2 {
		if s2, err2 := strconv.ParseFloat(parts[1], 64); err2 == nil {
			sy = s2
		}
	}
	sc := Matrix2D{A: sx, B: 0, C: 0, D: sy, E: 0, F: 0}
	style.Transform = style.Transform.Mul(sc)
	style.HasTransform = true
}

func applyTranslateProperty(style *ResolvedStyle, value string, fsize float64) {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return
	}
	tx, ok1 := plainLength(parts[0], fsize, 0)
	if !ok1 {
		return
	}
	ty := 0.0
	if len(parts) >= 2 {
		if t2, ok2 := plainLength(parts[1], fsize, 0); ok2 {
			ty = t2
		}
	}
	tr := Matrix2D{A: 1, B: 0, C: 0, D: 1, E: tx, F: ty}
	style.Transform = style.Transform.Mul(tr)
	style.HasTransform = true
}

func parseCSSAngle(value string) (float64, bool) {
	v := strings.ToLower(strings.TrimSpace(value))
	if strings.HasSuffix(v, "deg") {
		deg, err := strconv.ParseFloat(strings.TrimSuffix(v, "deg"), 64)
		return deg * math.Pi / 180.0, err == nil
	}
	if strings.HasSuffix(v, "rad") {
		rad, err := strconv.ParseFloat(strings.TrimSuffix(v, "rad"), 64)
		return rad, err == nil
	}
	if strings.HasSuffix(v, "grad") {
		grad, err := strconv.ParseFloat(strings.TrimSuffix(v, "grad"), 64)
		return grad * math.Pi / 200.0, err == nil
	}
	if strings.HasSuffix(v, "turn") {
		turn, err := strconv.ParseFloat(strings.TrimSuffix(v, "turn"), 64)
		return turn * 2 * math.Pi, err == nil
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		return n * math.Pi / 180.0, true
	}

	return 0, false
}
