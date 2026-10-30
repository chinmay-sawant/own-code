//nolint:cyclop // color-adjust, forced-colors, and dynamic-range property dispatch
package layout

import (
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
)

// Print color adjustment keywords. The apply arm stores only these normalized
// spellings; paint consumers compare against the same constants.
const (
	colorAdjustEconomy = "economy"
	colorAdjustExact   = "exact"

	forcedColorAdjustAuto = "auto"
	forcedColorAdjustNone = "none"
	// forcedColorAdjustPreserveParentColor is spec syntax (css-color-adjust-1).
	// Print has no forced-colors mode, so the paint path treats it like auto.
	forcedColorAdjustPreserveParentColor = "preserve-parent-color"

	colorSchemeNormal = "normal"
	colorSchemeLight  = "light"
	colorSchemeDark   = "dark"

	dynamicRangeLimitNoLimit         = "no-limit"
	dynamicRangeLimitStandard        = "standard"
	dynamicRangeLimitHigh            = "high"
	dynamicRangeLimitConstrainedHigh = "constrained-high"
)

// applyColorAdjustProps owns color-adjust / print-color-adjust,
// forced-color-adjust, color-scheme, and dynamic-range-limit. Values are
// normalized to the constants above; anything else is dropped. The CSS-wide
// keywords are resolved here because inheritProps suppresses its parent copy
// for any declared property (style_cascade.go), so an explicit
// "inherit"/"initial"/"unset"/"revert" must land on this arm.
func applyColorAdjustProps(
	style *ResolvedStyle, prop, value string, _ float64, _ *styleContext, parent *ResolvedStyle, hasParent bool,
) bool {
	switch prop {
	case "color-adjust", "print-color-adjust":
		parentValue := colorAdjustEconomy
		if parent != nil {
			parentValue = parent.ColorAdjust
		}

		if v, ok := inheritedToken(value, parentValue, colorAdjustEconomy, hasParent, normalizeColorAdjust); ok {
			style.ColorAdjust = v
		}
	case "forced-color-adjust":
		parentValue := forcedColorAdjustAuto
		if parent != nil {
			parentValue = parent.ForcedColorAdjust
		}

		if v, ok := inheritedToken(value, parentValue, forcedColorAdjustAuto, hasParent, normalizeForcedColorAdjust); ok {
			style.ForcedColorAdjust = v
		}
	case "color-scheme":
		parentValue := colorSchemeNormal
		if parent != nil {
			parentValue = parent.ColorScheme
		}

		if v, ok := inheritedToken(value, parentValue, colorSchemeNormal, hasParent, normalizeColorScheme); ok {
			style.ColorScheme = v
		}
	case "dynamic-range-limit":
		parentValue := dynamicRangeLimitNoLimit
		if parent != nil {
			parentValue = parent.DynamicRangeLimit
		}

		if v, ok := inheritedToken(value, parentValue, dynamicRangeLimitNoLimit, hasParent, normalizeDynamicRangeLimit); ok {
			style.DynamicRangeLimit = v
		}
	default:
		return false
	}

	return true
}

// inheritedToken resolves one declared token for an inherited property.
// CSS-wide keywords map to the parent's value (inherit/unset) or the property
// initial (initial/revert); ordinary tokens must pass normalize, and an
// invalid token leaves the field untouched.
func inheritedToken(
	value, parentValue, initial string, hasParent bool,
	normalize func(string) (string, bool),
) (string, bool) {
	if v, ok := cssWideValue(value, parentValue, initial, hasParent); ok {
		return v, true
	}

	return normalize(value)
}

// cssWideValue resolves the CSS-wide keywords shared by every property in
// this group: parent's value for inherit/unset (initial when there is no
// parent), property initial for initial/revert. Ordinary values return
// ("", false) so the caller can validate them.
func cssWideValue(value, parentValue, initial string, hasParent bool) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case inheritKeyword, cssKeywordUnset:
		if hasParent {
			return parentValue, true
		}

		return initial, true
	case cssKeywordInitial, cssKeywordRevert:
		return initial, true
	default:
		return "", false
	}
}

func normalizeColorAdjust(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case colorAdjustEconomy:
		return colorAdjustEconomy, true
	case colorAdjustExact:
		return colorAdjustExact, true
	default:
		return "", false
	}
}

func normalizeForcedColorAdjust(value string) (string, bool) {
	switch low := strings.ToLower(strings.TrimSpace(value)); low {
	case forcedColorAdjustAuto, forcedColorAdjustNone, forcedColorAdjustPreserveParentColor:
		return low, true
	default:
		return "", false
	}
}

// normalizeColorScheme accepts the keyword combinations this engine can use:
// normal, light, dark, light dark, dark light, only light, only dark. Custom
// idents are legal CSS but carry no print meaning here, so they are dropped.
func normalizeColorScheme(value string) (string, bool) {
	normalized := strings.Join(strings.Fields(strings.ToLower(value)), " ")

	switch normalized {
	case colorSchemeNormal, colorSchemeLight, colorSchemeDark,
		"light dark", "dark light", "only light", "only dark":
		return normalized, true
	default:
		return "", false
	}
}

// normalizeDynamicRangeLimit accepts the spec keywords plus the "constrained"
// spelling the catalog records, canonicalizing it to constrained-high.
func normalizeDynamicRangeLimit(value string) (string, bool) {
	switch low := strings.ToLower(strings.TrimSpace(value)); low {
	case dynamicRangeLimitNoLimit, dynamicRangeLimitStandard, dynamicRangeLimitHigh:
		return low, true
	case dynamicRangeLimitConstrainedHigh, "constrained":
		return dynamicRangeLimitConstrainedHigh, true
	default:
		return "", false
	}
}

// darkSchemeCanvasByte and darkSchemeTextByte are the used paint bytes for a
// dark color-scheme root: #121212 canvas with #e8e8e8 default text, matching
// the fixture-63 prose and Chrome 143.0.7499.40 dark defaults.
const (
	darkSchemeCanvasByte = 0x12
	darkSchemeTextByte   = 0xe8
	// srgbChannelMax scales a paint byte into the 0..1 channel range.
	srgbChannelMax = 255
)

// colorSchemeHasDark reports whether a normalized color-scheme value lets the
// element use a dark scheme: "dark", "light dark", "dark light", "only dark".
// "normal", "light", and "only light" return false. Custom idents never reach
// here because normalizeColorScheme drops them.
func colorSchemeHasDark(scheme string) bool {
	for _, token := range strings.Fields(strings.ToLower(scheme)) {
		if token == colorSchemeDark {
			return true
		}
	}

	return false
}

// defaultCanvasForScheme returns the used canvas fill for a color-scheme
// value. A dark scheme paints an opaque #121212 fill; any other scheme keeps
// the paper itself (transparent, no fill op). Only the html root's value is
// consumed by the paint path; nested values stay parsed and inherited.
func defaultCanvasForScheme(scheme string) [4]float64 {
	if !colorSchemeHasDark(scheme) {
		return [4]float64{}
	}

	channel := float64(darkSchemeCanvasByte) / srgbChannelMax

	return [4]float64{channel, channel, channel, 1}
}

// defaultTextForScheme returns the used default text color for a color-scheme
// value: #e8e8e8 under a dark scheme, black otherwise. Elements with an
// author color keep it; this only replaces the initial black.
func defaultTextForScheme(scheme string) [3]float64 {
	if !colorSchemeHasDark(scheme) {
		return [3]float64{}
	}

	channel := float64(darkSchemeTextByte) / srgbChannelMax

	return [3]float64{channel, channel, channel}
}

// clampDynamicRangeColor clamps one used sRGB color to the output range of a
// dynamic-range-limit value. "standard" and "constrained-high" target sRGB
// output, so channels fold into [0, 1]; "no-limit" and "high" preserve
// headroom and return the input unchanged. The color parser only produces
// 0..1 channels, so every parseable author color passes through untouched;
// the clamp only bites on out-of-range computed values.
func clampDynamicRangeColor(color [3]float64, limit string) [3]float64 {
	switch limit {
	case dynamicRangeLimitStandard, dynamicRangeLimitConstrainedHigh:
		return [3]float64{
			clampDynamicRangeChannel(color[0]),
			clampDynamicRangeChannel(color[1]),
			clampDynamicRangeChannel(color[2]),
		}
	default:
		return color
	}
}

// clampDynamicRangeChannel folds one color channel into [0, 1].
func clampDynamicRangeChannel(channel float64) float64 {
	if channel < 0 {
		return 0
	}

	if channel > 1 {
		return 1
	}

	return channel
}

// forcedColorsPaintColor resolves one used paint color for forced-colors
// mode. When forced mode is off (print has none), the author color always
// wins. When forced mode is on, "auto" and "preserve-parent-color" map to the
// system color while "none" preserves the author color per css-color-adjust-1.
func forcedColorsPaintColor(forcedActive bool, adjust string, author, system [3]float64) [3]float64 {
	if !forcedActive {
		return author
	}

	if adjust == forcedColorAdjustNone {
		return author
	}

	return system
}

// forcedColorsTestActive is the explicit test-only forced-colors mode
// override. Print has no forced-colors mode (Windows high-contrast only),
// so paint defaults to off. Tests enable it to observe the auto vs none
// split: auto maps to the system color while none keeps author colors,
// matching Chrome 143.0.7499.40 with forced-colors active.
var forcedColorsTestActive bool //nolint:gochecknoglobals // test-only forced-colors mode override

// setForcedColorsTestActive enables the test-only forced-colors mode.
// Callers must defer setForcedColorsTestActive(false) and must not run in
// parallel with other paint tests.
func setForcedColorsTestActive(active bool) {
	forcedColorsTestActive = active
}

// forcedColorsActive reports whether forced-colors mode is on for paint.
// Print has no forced-colors mode (Windows high-contrast only), so this is
// false unless the test override is enabled. The call site exists so
// forced-color-adjust: none keeps author colors through the same mapping
// Chrome 143.0.7499.40 uses when the mode is on.
func forcedColorsActive() bool {
	return forcedColorsTestActive
}

// forcedColorsForPaint reports whether forced-colors mode is on for this
// Layout run: the test-only override or the document setting
// (Options.ForcedColorsActive). Nil-engine safe for helper-only callers.
func (e *engine) forcedColorsForPaint() bool {
	if forcedColorsActive() {
		return true
	}

	return e != nil && e.opts.ForcedColorsActive
}

// canvasFillForRoot returns the root canvas fill for paint. A dark
// color-scheme root with no author background paints opaque #121212; an
// author background wins and non-dark schemes keep the transparent paper.
// Only the html root's value is consumed; nested values stay parsed.
func canvasFillForRoot(rootStyle *ResolvedStyle) ([4]float64, bool) {
	if rootStyle == nil {
		return [4]float64{}, false
	}

	if rootStyle.BGColor[3] > 0 {
		return [4]float64{}, false
	}

	fill := defaultCanvasForScheme(rootStyle.ColorScheme)
	if fill[3] <= 0 {
		return [4]float64{}, false
	}

	return fill, true
}

// applyCanvasBackgroundGeometry snaps every marked canvas fill to the canvas
// rect (0,0,canvasW,canvasH).
//
// The fill is emitted while the root box is laid out, when the only geometry
// in hand is that box: a root margin offsets and shrinks it, and a canvas
// raised by the requested viewport minimum is taller than it. The canvas
// background covers the whole canvas regardless of the root box (CSS
// Backgrounds 3 §2.11.2: the root's background propagates to the canvas,
// whose painting area is the full canvas), so the rect is fixed up here once
// the canvas size is final. Only the flat radiused OpFillRect is normed; a
// corner-shape OpPath keeps its baked command list and its emission rect.
func applyCanvasBackgroundGeometry(ops []Op, canvasW, canvasH float64) {
	if canvasW <= 0 || canvasH <= 0 {
		return
	}

	for opIdx := range ops {
		if !ops[opIdx].IsCanvasBackground() || ops[opIdx].Kind != OpFillRect {
			continue
		}

		ops[opIdx].X, ops[opIdx].Y = 0, 0
		ops[opIdx].W, ops[opIdx].H = canvasW, canvasH
	}
}

// usedBGForPaint resolves one background fill for paint: forced-mode mapping
// then dynamic-range clamp, preserving alpha. Print forced mode is off by
// default (test override or Options.ForcedColorsActive aside) so author
// colors survive; the clamp only folds out-of-range computed values into
// sRGB since the parser only produces 0..1 channels.
func usedBGForPaint(sty ResolvedStyle) [4]float64 {
	return usedBGForPaintActive(sty, forcedColorsActive())
}

// usedBGForPaintActive is usedBGForPaint with an explicit forced-colors mode
// flag so engine paint paths can pass the document setting
// (Options.ForcedColorsActive) instead of the test-only override.
func usedBGForPaintActive(sty ResolvedStyle, forcedActive bool) [4]float64 {
	base := sty.BGColor
	if base[3] <= 0 {
		return base
	}

	rgb := [3]float64{base[0], base[1], base[2]}
	rgb = forcedColorsPaintColor(forcedActive, sty.ForcedColorAdjust, rgb, [3]float64{})
	rgb = clampDynamicRangeColor(rgb, sty.DynamicRangeLimit)

	return [4]float64{rgb[0], rgb[1], rgb[2], base[3]}
}

// usedTextForPaint resolves one text ink color for paint: dark-scheme default
// substitution (initial black only), then forced mapping, then dynamic clamp.
// rootScheme is the html root's color-scheme; only it selects the default.
// Author colors (anything but initial black) keep their value before the
// forced and clamp stages. Reference: Chrome 143.0.7499.40.
func usedTextForPaint(color [3]float64, rootScheme, adjust, limit string) [3]float64 {
	return usedTextForPaintActive(color, rootScheme, adjust, limit, forcedColorsActive())
}

// usedTextForPaintActive is usedTextForPaint with an explicit forced-colors
// mode flag so engine paint paths can pass the document setting
// (Options.ForcedColorsActive) instead of the test-only override.
func usedTextForPaintActive(color [3]float64, rootScheme, adjust, limit string, forcedActive bool) [3]float64 {
	if color == ([3]float64{}) {
		if def := defaultTextForScheme(rootScheme); def != ([3]float64{}) {
			color = def
		}
	}

	color = forcedColorsPaintColor(forcedActive, adjust, color, [3]float64{})

	return clampDynamicRangeColor(color, limit)
}

// usedWideBGForPaint resolves an author background-color string to a used
// paint fill under a dynamic-range-limit value. Wide-gamut spellings keep
// their headroom through css.ParseColorWide, so standard and
// constrained-high fold them into sRGB while no-limit and high preserve the
// out-of-range channels. The legacy css.ParseColor byte pipeline cannot
// carry headroom (its 0..255 int return folds at parse time), so this is the
// author-string entry point that reaches the clamp with headroom intact.
// Forced-colors mapping stays in the full pipeline (usedBGForPaintActive);
// this covers the parse-to-clamp seam with forced mode off. Reference:
// Chrome 143.0.7499.40.
func usedWideBGForPaint(value, limit string) ([4]float64, bool) {
	rgb, alpha, ok := css.ParseColorWide(value)
	if !ok {
		return [4]float64{}, false
	}

	if alpha <= 0 {
		return [4]float64{rgb[0], rgb[1], rgb[2], alpha}, true
	}

	rgb = clampDynamicRangeColor(rgb, limit)

	return [4]float64{rgb[0], rgb[1], rgb[2], alpha}, true
}

// usedWideTextForPaint resolves an author text color string to used ink
// under a dynamic-range-limit value, preserving wide-gamut headroom until
// the clamp. Reference: Chrome 143.0.7499.40.
func usedWideTextForPaint(value, limit string) ([3]float64, bool) {
	rgb, _, ok := css.ParseColorWide(value)
	if !ok {
		return [3]float64{}, false
	}

	return clampDynamicRangeColor(rgb, limit), true
}
