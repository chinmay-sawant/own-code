package css

import (
	"math"
	"strconv"
	"strings"
)

// Modern CSS Color 4 functions: oklab(), oklch(), color-mix(in srgb, ...)
// and light-dark(). They share ParseColor's return shape: RGB 0..255 and
// alpha 0..1, ok=false for anything unrecognized.

const (
	oklabPercentABScale = 0.4 // 100% = 0.4 for oklab a/b and oklch C
	oklabHueDeg         = 360
	oklabHalfTurn       = 2
	oklabChannelCount   = 3
	lightDarkArgCount   = 2
	colorMixEvenSplit   = 0.5
	wideMixPartCount    = 3  // color-mix takes a space plus two stops
	wideHueNone         = -1 // rectangular mixing spaces carry no hue component
	wideHueFirst        = 0  // hsl/hwb hold hue in the first component
	wideHueLast         = 2  // oklch/lch hold hue in the third component
)

// Ottosson's Oklab -> linear sRGB matrices. The comments show the formula
// coefficient each constant stands for in oklabToRGB.
const (
	oklabLFromA     = 0.3963377774 // l = L + a*oklabLFromA + b*oklabLFromB
	oklabLFromB     = 0.2158037573
	oklabMFromA     = 0.1055613458 // m = L - a*oklabMFromA - b*oklabMFromB
	oklabMFromB     = 0.0638541728
	oklabSFromA     = 0.0894841775 // s = L - a*oklabSFromA - b*oklabSFromB
	oklabSFromB     = 1.2914855480
	oklabRedFromL   = 4.0767416621 // r = l*oklabRedFromL - m*oklabRedFromM + s*oklabRedFromS
	oklabRedFromM   = 3.3077115913
	oklabRedFromS   = 0.2309699292
	oklabGreenFromL = 1.2684380046 // g = -l*oklabGreenFromL + m*oklabGreenFromM - s*oklabGreenFromS
	oklabGreenFromM = 2.6097574011
	oklabGreenFromS = 0.3413193965
	oklabBlueFromL  = 0.0041960863 // b = -l*oklabBlueFromL - m*oklabBlueFromM + s*oklabBlueFromS
	oklabBlueFromM  = 0.7034186147
	oklabBlueFromS  = 1.7076147010
)

// sRGB transfer function constants (linear <-> gamma encoding).
const (
	srgbLinearThreshold = 0.0031308
	srgbLinearSlope     = 12.92
	srgbGammaScale      = 1.055
	srgbGammaExponent   = 2.4
	srgbGammaOffset     = 0.055
)

// parseModernColor dispatches the modern color functions. low is the
// lower-cased value; val keeps the original case for nested parsing.
func parseModernColor(val, low string) (int, int, int, float64, bool) {
	switch {
	case strings.HasPrefix(low, "oklch("):
		return parseOKLCHColor(val)
	case strings.HasPrefix(low, "oklab("):
		return parseOKLabColor(val)
	case strings.HasPrefix(low, "color-mix("):
		return parseColorMix(val)
	case strings.HasPrefix(low, "light-dark("):
		return parseLightDark(val)
	}

	return 0, 0, 0, 0, false
}

// colorFunctionBody returns the text between the first '(' and the last ')'.
func colorFunctionBody(val string) (string, bool) {
	open := strings.IndexByte(val, '(')
	closeIdx := strings.LastIndexByte(val, ')')

	if open < 0 || closeIdx <= open {
		return "", false
	}

	return strings.TrimSpace(val[open+1 : closeIdx]), true
}

// splitColorAlpha splits a space-separated channel list from an optional
// '/ alpha' tail at paren depth 0.
func splitColorAlpha(body string) (string, string, bool) {
	depth := 0

	for index := range len(body) {
		switch body[index] {
		case '(':
			depth++
		case ')':
			depth--
		case '/':
			if depth == 0 {
				return strings.TrimSpace(body[:index]), strings.TrimSpace(body[index+1:]), true
			}
		}
	}

	return body, "", false
}

// splitTopLevelCommas splits text on commas outside parentheses.
func splitTopLevelCommas(text string) []string {
	var parts []string

	depth, start := 0, 0

	for index := range len(text) {
		switch text[index] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(text[start:index]))

				start = index + 1
			}
		}
	}

	return append(parts, strings.TrimSpace(text[start:]))
}

func parseOKLabColor(val string) (int, int, int, float64, bool) {
	body, found := colorFunctionBody(val)
	if !found {
		return 0, 0, 0, 0, false
	}

	channels, alphaRaw, hasAlpha := splitColorAlpha(body)
	fields := strings.Fields(channels)

	if len(fields) != oklabChannelCount {
		return 0, 0, 0, 0, false
	}

	light, found := parseOKLabLight(fields[0])
	if !found {
		return 0, 0, 0, 0, false
	}

	aComp, found := parseOKLabAxis(fields[1])
	if !found {
		return 0, 0, 0, 0, false
	}

	bComp, found := parseOKLabAxis(fields[2])
	if !found {
		return 0, 0, 0, 0, false
	}

	alpha, found := parseModernAlpha(alphaRaw, hasAlpha)
	if !found {
		return 0, 0, 0, 0, false
	}

	red, green, blue := oklabToRGB(light, aComp, bComp)

	return red, green, blue, alpha, true
}

func parseOKLCHColor(val string) (int, int, int, float64, bool) {
	body, found := colorFunctionBody(val)
	if !found {
		return 0, 0, 0, 0, false
	}

	channels, alphaRaw, hasAlpha := splitColorAlpha(body)
	fields := strings.Fields(channels)

	if len(fields) != oklabChannelCount {
		return 0, 0, 0, 0, false
	}

	light, found := parseOKLabLight(fields[0])
	if !found {
		return 0, 0, 0, 0, false
	}

	chroma, found := parseOKLabAxis(fields[1])
	if !found {
		return 0, 0, 0, 0, false
	}

	if chroma < 0 {
		chroma = 0
	}

	hue, found := parseHueChannel(fields[2])
	if !found {
		return 0, 0, 0, 0, false
	}

	alpha, found := parseModernAlpha(alphaRaw, hasAlpha)
	if !found {
		return 0, 0, 0, 0, false
	}

	hue = math.Mod(hue, oklabHueDeg)
	if hue < 0 {
		hue += oklabHueDeg
	}

	rad := hue * math.Pi / (oklabHueDeg / oklabHalfTurn)

	red, green, blue := oklabToRGB(light, chroma*math.Cos(rad), chroma*math.Sin(rad))

	return red, green, blue, alpha, true
}

// parseOKLabLight parses oklab/oklch L: number 0..1 or percentage, clamped.
func parseOKLabLight(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasSuffix(raw, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(raw, "%"), 64)
		if err != nil {
			return 0, false
		}

		return clampUnit(f / percentScale), true
	}

	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}

	return clampUnit(f), true
}

// parseOKLabAxis parses oklab a/b or oklch C: number, or percentage where
// 100% maps to 0.4.
func parseOKLabAxis(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasSuffix(raw, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(raw, "%"), 64)
		if err != nil {
			return 0, false
		}

		return f / percentScale * oklabPercentABScale, true
	}

	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}

	return f, true
}

// parseModernAlpha parses the optional '/ alpha' tail: number or percentage.
func parseModernAlpha(raw string, hasAlpha bool) (float64, bool) {
	if !hasAlpha {
		return 1, true
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}

	if strings.HasSuffix(raw, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(raw, "%"), 64)
		if err != nil {
			return 0, false
		}

		return clampAlpha(f / percentScale), true
	}

	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}

	return clampAlpha(f), true
}

// oklabToRGB converts an Oklab triplet to sRGB bytes (Ottosson's matrices),
// clamping out-of-gamut results.
func oklabToRGB(light, aComp, bComp float64) (int, int, int) {
	rgb := wideOklabToSRGB(light, aComp, bComp)

	return clampByte(rgb[0] * maxRGBChannel),
		clampByte(rgb[1] * maxRGBChannel),
		clampByte(rgb[2] * maxRGBChannel)
}

type colorMixStop struct {
	red, green, blue int
	alpha            float64
	percent          float64
	hasPercent       bool
}

// zeroColorMixStop is the all-zero stop: no channels and no percentage.
func zeroColorMixStop() colorMixStop {
	return colorMixStop{
		red:        0,
		green:      0,
		blue:       0,
		alpha:      0,
		percent:    0,
		hasPercent: false,
	}
}

func parseColorMix(val string) (int, int, int, float64, bool) {
	body, ok := colorFunctionBody(val)
	if !ok {
		return 0, 0, 0, 0, false
	}

	parts := splitTopLevelCommas(body)
	if len(parts) != 3 || !strings.EqualFold(strings.Join(strings.Fields(parts[0]), " "), "in srgb") {
		return 0, 0, 0, 0, false
	}

	first, okFirst := parseColorMixStop(parts[1])
	second, okSecond := parseColorMixStop(parts[2])

	if !okFirst || !okSecond {
		return 0, 0, 0, 0, false
	}

	weightFirst, weightSecond := colorMixWeights(first, second)

	red, green, blue, alpha := mixPremultiplied(first, second, weightFirst, weightSecond)

	return red, green, blue, alpha, true
}

// parseColorMixStop parses '<color> <percentage>?'.
func parseColorMixStop(raw string) (colorMixStop, bool) {
	raw = strings.TrimSpace(raw)
	stop := zeroColorMixStop()

	if strings.HasSuffix(raw, "%") {
		space := strings.LastIndexByte(raw, ' ')
		if space < 0 {
			return zeroColorMixStop(), false
		}

		f, err := strconv.ParseFloat(strings.TrimSpace(raw[space+1:len(raw)-1]), 64)
		if err != nil {
			return zeroColorMixStop(), false
		}

		stop.percent = clampPercent(f)
		stop.hasPercent = true

		raw = strings.TrimSpace(raw[:space])
	}

	red, green, blue, alpha, ok := ParseColor(raw)
	if !ok {
		return zeroColorMixStop(), false
	}

	stop.red, stop.green, stop.blue, stop.alpha = red, green, blue, alpha

	return stop, true
}

// colorMixWeights normalizes the stop percentages: both omitted means an even
// split, one omitted takes the remainder, both given are scaled to sum to 100%.
func colorMixWeights(first, second colorMixStop) (float64, float64) {
	return mixPercentWeights(first.percent, first.hasPercent, second.percent, second.hasPercent)
}

// mixPercentWeights normalizes two stop percentages with the color-mix rules.
// Both the byte pipeline (colorMixWeights) and the wide-gamut pipeline share
// it so percentage handling cannot drift between them.
func mixPercentWeights(firstPercent float64, firstHas bool, secondPercent float64, secondHas bool) (float64, float64) {
	switch {
	case firstHas && secondHas:
		sum := firstPercent + secondPercent
		if sum == 0 {
			return 0, 0
		}

		return firstPercent / sum, secondPercent / sum
	case firstHas:
		weight := firstPercent / percentScale

		return weight, 1 - weight
	case secondHas:
		weight := secondPercent / percentScale

		return 1 - weight, weight
	default:
		return colorMixEvenSplit, colorMixEvenSplit
	}
}

func clampPercent(percent float64) float64 {
	if percent < 0 {
		return 0
	}

	if percent > percentScale {
		return percentScale
	}

	return percent
}

// mixPremultiplied blends two sRGB colors with premultiplied alpha, per the
// color-mix interpolation rules.
func mixPremultiplied(first, second colorMixStop, weightFirst, weightSecond float64) (int, int, int, float64) {
	outAlpha := first.alpha*weightFirst + second.alpha*weightSecond
	if outAlpha == 0 {
		return 0, 0, 0, 0
	}

	red := (float64(first.red)*first.alpha*weightFirst + float64(second.red)*second.alpha*weightSecond) / outAlpha
	green := (float64(first.green)*first.alpha*weightFirst + float64(second.green)*second.alpha*weightSecond) / outAlpha
	blue := (float64(first.blue)*first.alpha*weightFirst + float64(second.blue)*second.alpha*weightSecond) / outAlpha

	return clampByte(red), clampByte(green), clampByte(blue), clampAlpha(outAlpha)
}

// parseLightDark returns the light-scheme color. Both arguments must parse.
func parseLightDark(val string) (int, int, int, float64, bool) {
	body, ok := colorFunctionBody(val)
	if !ok {
		return 0, 0, 0, 0, false
	}

	parts := splitTopLevelCommas(body)
	if len(parts) != lightDarkArgCount {
		return 0, 0, 0, 0, false
	}

	lightR, lightG, lightB, lightA, okLight := ParseColor(parts[0])
	darkR, darkG, darkB, darkA, okDark := ParseColor(parts[1])

	if !okLight || !okDark {
		return 0, 0, 0, 0, false
	}

	// The engine always renders the light scheme; the dark channels are
	// parsed only to reject a malformed pair.
	_ = darkR
	_ = darkG
	_ = darkB
	_ = darkA

	return lightR, lightG, lightB, lightA, true
}

// Wide-gamut color parsing with HDR headroom.
//
// ParseColor returns sRGB bytes, so every out-of-gamut channel folds at
// parse time and the dynamic-range-limit clamp in internal/layout only ever
// sees 0..1 channels. ParseColorWide is the alongside representation for
// that clamp: it accepts everything ParseColor does plus the wide-gamut
// functions below and returns unclamped sRGB channels on a 0..1 scale, so a
// display-p3 red or a high-chroma oklch can exceed [0, 1] and survive until
// the paint clamp. SDR spellings (hex, named colors, rgb(), hsl()) keep
// ParseColor's clamped values; only the wide-gamut functions carry
// headroom. Reference: Chrome 143.0.7499.40.
//
// ParseColorWide parses a color and keeps wide-gamut channels unclamped.
func ParseColorWide(val string) ([3]float64, float64, bool) {
	return parseColorWideDepth(val, 0)
}

// parseColorWideDepth is ParseColorWide with a var() fallback depth.
// depth counts fallbacks already entered. At maxColorVarDepth another var()
// is rejected, matching parseColorDepth.
//
//nolint:cyclop // linear dispatch across color forms; extraction would obscure it
func parseColorWideDepth(val string, depth int) ([3]float64, float64, bool) {
	val = strings.TrimSpace(val)
	if val == "" {
		return [3]float64{}, 0, false
	}

	if strings.HasPrefix(strings.ToLower(val), "var(") {
		if depth >= maxColorVarDepth {
			return [3]float64{}, 0, false
		}

		if fb, okFB := cssVarFallback(val); okFB {
			return parseColorWideDepth(fb, depth+1)
		}

		return [3]float64{}, 0, false
	}

	if val[0] == '#' {
		red, green, blue, alpha, ok := parseHexColor(val[1:])
		if !ok {
			return [3]float64{}, 0, false
		}

		return bytesToUnitWide(red, green, blue), alpha, true
	}

	low := strings.ToLower(val)
	if low == "transparent" {
		return [3]float64{}, 0, true
	}

	if name, found := namedColorTable[low]; found {
		return bytesToUnitWide(name[0], name[1], name[2]), 1, true
	}

	if strings.HasPrefix(low, "rgb") {
		red, green, blue, alpha, ok := parseRGBColor(val, low)
		if !ok {
			return [3]float64{}, 0, false
		}

		return bytesToUnitWide(red, green, blue), alpha, true
	}

	if strings.HasPrefix(low, "hsl") {
		red, green, blue, alpha, ok := parseHSLColor(val, low)
		if !ok {
			return [3]float64{}, 0, false
		}

		return bytesToUnitWide(red, green, blue), alpha, true
	}

	return parseWideModern(val, low)
}

// parseWideModern dispatches the wide-gamut functions. oklab()/oklch() go
// through the unclamped float path here; the clamped byte path stays in
// parseModernColor so ParseColor never carries headroom.
func parseWideModern(val, low string) ([3]float64, float64, bool) {
	switch {
	case strings.HasPrefix(low, "oklch("):
		return parseWideOKLCH(val)
	case strings.HasPrefix(low, "oklab("):
		return parseWideOKLab(val)
	case strings.HasPrefix(low, "color-mix("):
		return parseWideColorMix(val)
	case strings.HasPrefix(low, "light-dark("):
		return parseWideLightDark(val)
	case strings.HasPrefix(low, "color("):
		return parseWideColorFunc(val)
	case strings.HasPrefix(low, "lab("):
		return parseWideLab(val)
	case strings.HasPrefix(low, "lch("):
		return parseWideLCH(val)
	case strings.HasPrefix(low, "hwb("):
		return parseWideHWB(val)
	}

	return [3]float64{}, 0, false
}

// bytesToUnitWide scales clamped sRGB bytes onto the 0..1 wide scale.
func bytesToUnitWide(red, green, blue int) [3]float64 {
	return [3]float64{
		float64(red) / maxRGBChannel,
		float64(green) / maxRGBChannel,
		float64(blue) / maxRGBChannel,
	}
}

// matMul3 applies a row-major 3x3 matrix to a triplet.
func matMul3(mat [9]float64, vec [3]float64) [3]float64 {
	return [3]float64{
		mat[0]*vec[0] + mat[1]*vec[1] + mat[2]*vec[2],
		mat[3]*vec[0] + mat[4]*vec[1] + mat[5]*vec[2],
		mat[6]*vec[0] + mat[7]*vec[1] + mat[8]*vec[2],
	}
}

const (
	wideSRGBDecodeKnee = 0.04045         // sRGB EOTF knee between the linear and power limbs
	wideGammaA98       = 2.2             // Adobe RGB uses a pure 2.2 power transfer
	wideGammaProphoto  = 1.8             // ROMM RGB power limb exponent
	wideProphotoSlope  = 16.0            // ROMM RGB linear limb slope
	wideProphotoBreak  = 0.001953125     // ROMM RGB linear breakpoint (2^-9)
	wideRec2020Slope   = 4.5             // BT.2020 linear limb slope
	wideRec2020Alpha   = 1.0993          // BT.2020 10-bit OETF constant
	wideRec2020Beta    = 0.0181          // BT.2020 10-bit linear breakpoint
	wideRec2020Gamma   = 0.45            // BT.2020 OETF power
	wideLabEpsilon     = 216.0 / 24389.0 // Lab f(t) breakpoint
	wideLabKappa       = 24389.0 / 27.0  // Lab f(t) linear slope
	wideLabFScale      = 116.0           // Lab f(t) output scale
	wideLabFOffset     = 16.0            // Lab f(t) output offset
	wideLabAScale      = 500.0           // Lab a divisor
	wideLabBScale      = 200.0           // Lab b divisor
	wideLabPercentAB   = 125.0           // lab() a/b: 100% maps onto 125
	wideLabPercentC    = 150.0           // lch() chroma: 100% maps onto 150
	wideD50WhiteX      = 0.96422         // CIE D50 white point X
	wideD50WhiteZ      = 0.82521         // CIE D50 white point Z
	wideHueSectors     = 6               // hue wheel sectors in sRGB-from-hue math
)

// Forward linear-sRGB to LMS cone matrix: the exact inverse of the
// oklabToRGB limb above (Ottosson's forward variant), in the same comment
// style as the oklabLFromA block.
const (
	wideConeLFromR = 0.4122214708 // l = R*wideConeLFromR + G*wideConeLFromG + B*wideConeLFromB
	wideConeLFromG = 0.5363325363
	wideConeLFromB = 0.0514459929
	wideConeMFromR = 0.2119034982 // m = R*wideConeMFromR + G*wideConeMFromG + B*wideConeMFromB
	wideConeMFromG = 0.6806995451
	wideConeMFromB = 0.1073969566
	wideConeSFromR = 0.0883024619 // s = R*wideConeSFromR + G*wideConeSFromG + B*wideConeSFromB
	wideConeSFromG = 0.2817188376
	wideConeSFromB = 0.6299787005
)

// Standard colorimetry coefficient tables, row-major 3x3. The sRGB pair is
// IEC 61966-2-1; display-p3 and rec2020 use D65 primaries; Adobe RGB is
// D65; ProPhoto RGB is D50. The Bradford pair is constructed from the D50
// white consts below and the IEC D65 column sums, so achromatic CIE Lab
// maps back onto exact sRGB white instead of drifting on truncated tables.
//
//nolint:gochecknoglobals // static read-only colorimetry tables
var (
	srgbToXYZWide = [9]float64{
		0.4123907993, 0.3575843394, 0.1804807884,
		0.2126390059, 0.7151686788, 0.0721923154,
		0.0193308187, 0.1191947798, 0.9505321525,
	}
	xyzToSRGBWide = [9]float64{
		3.2409699419, -1.5373831776, -0.4986107603,
		-0.9692436363, 1.8759675015, 0.0415550574,
		0.0556300797, -0.2039769589, 1.0569715142,
	}
	p3ToXYZWide = [9]float64{
		0.4865709486, 0.2656676932, 0.1982172852,
		0.2289745641, 0.6917385218, 0.0792869141,
		0.0000000000, 0.0451133819, 1.0439443689,
	}
	xyzToP3Wide = [9]float64{
		2.4934969124, -0.9313836182, -0.4027107844,
		-0.8294889699, 1.7626640606, 0.0236246858,
		0.0358458303, -0.0761723893, 0.956884524,
	}
	a98ToXYZWide = [9]float64{
		0.5766690429, 0.1855582379, 0.1882286462,
		0.2973449753, 0.6273635664, 0.0752914583,
		0.0270313612, 0.0706888525, 0.9913375365,
	}
	xyzToA98Wide = [9]float64{
		2.0415879037, -0.5650069741, -0.3447313509,
		-0.9692436362, 1.8759675010, 0.0415550578,
		0.0134442810, -0.1183623923, 1.0151749947,
	}
	rec2020ToXYZWide = [9]float64{
		0.6369580483, 0.1446169036, 0.1688809752,
		0.2627002120, 0.6779980715, 0.0593017165,
		0.0000000000, 0.0280726930, 1.0609850577,
	}
	xyzToRec2020Wide = [9]float64{
		1.7166511880, -0.3556707838, -0.2533662814,
		-0.6666843518, 1.6164812367, 0.0157685458,
		0.0176398574, -0.0427706132, 0.9421031212,
	}
	prophotoToXYZWide = [9]float64{
		0.7976749, 0.1351917, 0.0313534,
		0.2880402, 0.7118741, 0.0000857,
		0.0000000, 0.0000000, 0.8252100,
	}
	xyzToProphotoWide = [9]float64{
		1.3459433669, -0.2556075181, -0.0511118324,
		-0.5445988225, 1.5081673018, 0.0205351060,
		0.0000000000, 0.0000000000, 1.2118127507,
	}
	bradfordD50ToD65Wide = [9]float64{
		0.9555366721, -0.0230601728, 0.0632184897,
		-0.0283153747, 1.0099514123, 0.0210259672,
		0.0123087532, -0.0205005992, 1.3301947434,
	}
	bradfordD65ToD50Wide = [9]float64{
		1.0478572906, 0.0229074518, -0.0501622841,
		0.0295704932, 0.9904754998, -0.0170614922,
		-0.0092404545, 0.0150529681, 0.7519708444,
	}
	identityWide = [9]float64{
		1, 0, 0,
		0, 1, 0,
		0, 0, 1,
	}
)

// srgbEncodeWide applies the sRGB OETF, preserving sign so negative linear
// headroom encodes to negative channels instead of NaN.
func srgbEncodeWide(channel float64) float64 {
	if channel < 0 {
		return -srgbEncodeWide(-channel)
	}

	if channel <= srgbLinearThreshold {
		return srgbLinearSlope * channel
	}

	return srgbGammaScale*math.Pow(channel, 1.0/srgbGammaExponent) - srgbGammaOffset
}

// srgbDecodeWide applies the sRGB EOTF, preserving sign for headroom
// channels below zero.
func srgbDecodeWide(channel float64) float64 {
	if channel < 0 {
		return -srgbDecodeWide(-channel)
	}

	if channel <= wideSRGBDecodeKnee {
		return channel / srgbLinearSlope
	}

	return math.Pow((channel+srgbGammaOffset)/srgbGammaScale, srgbGammaExponent)
}

// gammaDecodeWide applies a pure power transfer, preserving sign.
func gammaDecodeWide(value, gamma float64) float64 {
	if value < 0 {
		return -gammaDecodeWide(-value, gamma)
	}

	return math.Pow(value, gamma)
}

// gammaEncodeWide inverts a pure power transfer, preserving sign.
func gammaEncodeWide(channel, gamma float64) float64 {
	if channel < 0 {
		return -gammaEncodeWide(-channel, gamma)
	}

	return math.Pow(channel, 1.0/gamma)
}

// a98DecodeWide decodes Adobe RGB (pure 2.2 gamma).
func a98DecodeWide(value float64) float64 {
	return gammaDecodeWide(value, wideGammaA98)
}

// a98EncodeWide encodes Adobe RGB (pure 2.2 gamma).
func a98EncodeWide(channel float64) float64 {
	return gammaEncodeWide(channel, wideGammaA98)
}

// prophotoDecodeWide decodes ROMM RGB (linear limb, then 1.8 power).
func prophotoDecodeWide(value float64) float64 {
	if value < 0 {
		return -prophotoDecodeWide(-value)
	}

	if value < wideProphotoSlope*wideProphotoBreak {
		return value / wideProphotoSlope
	}

	return math.Pow(value, wideGammaProphoto)
}

// prophotoEncodeWide encodes ROMM RGB (linear limb, then 1/1.8 power).
func prophotoEncodeWide(channel float64) float64 {
	if channel < 0 {
		return -prophotoEncodeWide(-channel)
	}

	if channel < wideProphotoBreak {
		return wideProphotoSlope * channel
	}

	return math.Pow(channel, 1.0/wideGammaProphoto)
}

// rec2020DecodeWide decodes the BT.2020 10-bit OETF, preserving sign.
func rec2020DecodeWide(value float64) float64 {
	if value < 0 {
		return -rec2020DecodeWide(-value)
	}

	if value < wideRec2020Slope*wideRec2020Beta {
		return value / wideRec2020Slope
	}

	return math.Pow((value+wideRec2020Alpha-1.0)/wideRec2020Alpha, 1.0/wideRec2020Gamma)
}

// rec2020EncodeWide encodes the BT.2020 10-bit OETF, preserving sign.
func rec2020EncodeWide(channel float64) float64 {
	if channel < 0 {
		return -rec2020EncodeWide(-channel)
	}

	if channel < wideRec2020Beta {
		return wideRec2020Slope * channel
	}

	return wideRec2020Alpha*math.Pow(channel, wideRec2020Gamma) - (wideRec2020Alpha - 1.0)
}

// wideRGBSpace describes one RGB working space for color() parsing and
// color-mix interpolation: its transfer pair, its XYZ matrices, and the
// Bradford adaptation across the D50/D65 change (identity for D65 spaces).
type wideRGBSpace struct {
	decode       func(float64) float64
	encode       func(float64) float64
	toXYZ        [9]float64
	fromXYZ      [9]float64
	adaptToD65   [9]float64
	adaptFromD65 [9]float64
}

//nolint:gochecknoglobals // static read-only gamut vocabulary, never mutated
var wideRGBSpaceTable = map[string]wideRGBSpace{
	"srgb": {
		decode: srgbDecodeWide, encode: srgbEncodeWide,
		toXYZ: srgbToXYZWide, fromXYZ: xyzToSRGBWide,
		adaptToD65: identityWide, adaptFromD65: identityWide,
	},
	"display-p3": {
		decode: srgbDecodeWide, encode: srgbEncodeWide,
		toXYZ: p3ToXYZWide, fromXYZ: xyzToP3Wide,
		adaptToD65: identityWide, adaptFromD65: identityWide,
	},
	"a98-rgb": {
		decode: a98DecodeWide, encode: a98EncodeWide,
		toXYZ: a98ToXYZWide, fromXYZ: xyzToA98Wide,
		adaptToD65: identityWide, adaptFromD65: identityWide,
	},
	"prophoto-rgb": {
		decode: prophotoDecodeWide, encode: prophotoEncodeWide,
		toXYZ: prophotoToXYZWide, fromXYZ: xyzToProphotoWide,
		adaptToD65: bradfordD50ToD65Wide, adaptFromD65: bradfordD65ToD50Wide,
	},
	"rec2020": {
		decode: rec2020DecodeWide, encode: rec2020EncodeWide,
		toXYZ: rec2020ToXYZWide, fromXYZ: xyzToRec2020Wide,
		adaptToD65: identityWide, adaptFromD65: identityWide,
	},
}

// wideSpaceToSRGB converts one working-space triplet to unclamped sRGB on
// the 0..1 scale, preserving out-of-gamut headroom.
func wideSpaceToSRGB(space wideRGBSpace, comp [3]float64) [3]float64 {
	linear := [3]float64{space.decode(comp[0]), space.decode(comp[1]), space.decode(comp[2])}
	xyz := matMul3(space.toXYZ, linear)
	xyz = matMul3(space.adaptToD65, xyz)
	linearSRGB := matMul3(xyzToSRGBWide, xyz)

	return [3]float64{
		srgbEncodeWide(linearSRGB[0]),
		srgbEncodeWide(linearSRGB[1]),
		srgbEncodeWide(linearSRGB[2]),
	}
}

// wideSRGBToSpace converts unclamped sRGB to one working-space triplet, the
// inverse of wideSpaceToSRGB for color-mix interpolation.
func wideSRGBToSpace(space wideRGBSpace, rgb [3]float64) [3]float64 {
	linearSRGB := [3]float64{srgbDecodeWide(rgb[0]), srgbDecodeWide(rgb[1]), srgbDecodeWide(rgb[2])}
	xyz := matMul3(srgbToXYZWide, linearSRGB)
	xyz = matMul3(space.adaptFromD65, xyz)
	linear := matMul3(space.fromXYZ, xyz)

	return [3]float64{space.encode(linear[0]), space.encode(linear[1]), space.encode(linear[2])}
}

// wideOklabToSRGB converts an Oklab triplet to unclamped sRGB (0..1 scale)
// without the gamut folding oklabToRGB applies.
func wideOklabToSRGB(light, aComp, bComp float64) [3]float64 {
	lmsL := light + oklabLFromA*aComp + oklabLFromB*bComp
	lmsM := light - oklabMFromA*aComp - oklabMFromB*bComp
	lmsS := light - oklabSFromA*aComp - oklabSFromB*bComp

	lmsL = lmsL * lmsL * lmsL
	lmsM = lmsM * lmsM * lmsM
	lmsS = lmsS * lmsS * lmsS

	return [3]float64{
		srgbEncodeWide(oklabRedFromL*lmsL - oklabRedFromM*lmsM + oklabRedFromS*lmsS),
		srgbEncodeWide(-oklabGreenFromL*lmsL + oklabGreenFromM*lmsM - oklabGreenFromS*lmsS),
		srgbEncodeWide(-oklabBlueFromL*lmsL - oklabBlueFromM*lmsM + oklabBlueFromS*lmsS),
	}
}

// wideSRGBToOklab converts unclamped sRGB to Oklab, the inverse of
// wideOklabToSRGB for color-mix interpolation.
func wideSRGBToOklab(rgb [3]float64) (float64, float64, float64) {
	linear := [3]float64{srgbDecodeWide(rgb[0]), srgbDecodeWide(rgb[1]), srgbDecodeWide(rgb[2])}
	coneL := wideConeLFromR*linear[0] + wideConeLFromG*linear[1] + wideConeLFromB*linear[2]
	coneM := wideConeMFromR*linear[0] + wideConeMFromG*linear[1] + wideConeMFromB*linear[2]
	coneS := wideConeSFromR*linear[0] + wideConeSFromG*linear[1] + wideConeSFromB*linear[2]
	cbrtL, cbrtM, cbrtS := math.Cbrt(coneL), math.Cbrt(coneM), math.Cbrt(coneS)

	return oklabLFromCube(cbrtL, cbrtM, cbrtS),
		oklabAFromCube(cbrtL, cbrtM, cbrtS),
		oklabBFromCube(cbrtL, cbrtM, cbrtS)
}

// oklabLFromCube combines the cube-rooted cones into Oklab lightness.
func oklabLFromCube(cbrtL, cbrtM, cbrtS float64) float64 {
	return 0.2104542553*cbrtL + 0.7936177850*cbrtM - 0.0040720468*cbrtS
}

// oklabAFromCube combines the cube-rooted cones into Oklab a.
func oklabAFromCube(cbrtL, cbrtM, cbrtS float64) float64 {
	return 1.9779984951*cbrtL - 2.4285922050*cbrtM + 0.4505937099*cbrtS
}

// oklabBFromCube combines the cube-rooted cones into Oklab b.
func oklabBFromCube(cbrtL, cbrtM, cbrtS float64) float64 {
	return 0.0259040371*cbrtL + 0.7827717662*cbrtM - 0.8086757660*cbrtS
}

// labForwardF is the CIE Lab f(t) limb mapping onto XYZ ratios.
func labForwardF(ratio float64) float64 {
	cubed := ratio * ratio * ratio
	if cubed > wideLabEpsilon {
		return cubed
	}

	return (wideLabFScale*ratio - wideLabFOffset) / wideLabKappa
}

// labInverseF inverts labForwardF for the sRGB-to-Lab direction.
func labInverseF(ratio float64) float64 {
	if ratio > wideLabEpsilon {
		return math.Cbrt(ratio)
	}

	return (wideLabKappa*ratio + wideLabFOffset) / wideLabFScale
}

// wideLabToSRGB converts CIE Lab (D50, L on 0..100, a/b unbounded) to
// unclamped sRGB, preserving headroom.
func wideLabToSRGB(light, aComp, bComp float64) [3]float64 {
	fyVal := (light + wideLabFOffset) / wideLabFScale
	fxVal := aComp/wideLabAScale + fyVal
	fzVal := fyVal - bComp/wideLabBScale

	xyz := [3]float64{
		wideD50WhiteX * labForwardF(fxVal),
		labForwardF(fyVal),
		wideD50WhiteZ * labForwardF(fzVal),
	}
	xyz = matMul3(bradfordD50ToD65Wide, xyz)
	linear := matMul3(xyzToSRGBWide, xyz)

	return [3]float64{
		srgbEncodeWide(linear[0]),
		srgbEncodeWide(linear[1]),
		srgbEncodeWide(linear[2]),
	}
}

// wideSRGBToLab converts unclamped sRGB to CIE Lab, the inverse of
// wideLabToSRGB for color-mix interpolation.
func wideSRGBToLab(rgb [3]float64) [3]float64 {
	linear := [3]float64{srgbDecodeWide(rgb[0]), srgbDecodeWide(rgb[1]), srgbDecodeWide(rgb[2])}
	xyz := matMul3(srgbToXYZWide, linear)
	xyz = matMul3(bradfordD65ToD50Wide, xyz)
	fxVal := labInverseF(xyz[0] / wideD50WhiteX)
	fyVal := labInverseF(xyz[1])
	fzVal := labInverseF(xyz[2] / wideD50WhiteZ)

	return [3]float64{
		wideLabFScale*fyVal - wideLabFOffset,
		wideLabAScale * (fxVal - fyVal),
		wideLabBScale * (fyVal - fzVal),
	}
}

// wideHSLToSRGB converts HSL (hue degrees, sat/light 0..100) to unclamped
// sRGB through the shared hue-wheel core.
func wideHSLToSRGB(hue, sat, light float64) [3]float64 {
	red, green, blue := hslToRGBFloat(hue, sat, light)

	return [3]float64{red, green, blue}
}

// wideSRGBToHSL converts unclamped sRGB to HSL (hue degrees, sat/light
// 0..100) for color-mix interpolation.
func wideSRGBToHSL(rgb [3]float64) [3]float64 {
	maxC := math.Max(rgb[0], math.Max(rgb[1], rgb[2]))
	minC := math.Min(rgb[0], math.Min(rgb[1], rgb[2]))
	delta := maxC - minC
	light := (maxC + minC) / hslChromaHalf

	if delta == 0 {
		return [3]float64{0, 0, light * percentScale}
	}

	sat := delta / (1 - math.Abs(hslChromaHalf*light-1))

	return [3]float64{wideHueOfMax(rgb, maxC, delta), sat * percentScale, light * percentScale}
}

// wideHueOfMax recovers the hue wheel angle of the dominant channel.
func wideHueOfMax(rgb [3]float64, maxC, delta float64) float64 {
	var hue float64

	switch maxC {
	case rgb[0]:
		hue = hslSectorDeg * math.Mod((rgb[1]-rgb[2])/delta, wideHueSectors)
	case rgb[1]:
		hue = hslSectorDeg * ((rgb[2]-rgb[0])/delta + hslSectorYG)
	default:
		hue = hslSectorDeg * ((rgb[0]-rgb[1])/delta + hslSectorCB)
	}

	if hue < 0 {
		hue += hslCircleDeg
	}

	return hue
}

// wideHWBToSRGB converts HWB (hue degrees, white/black 0..1) to unclamped
// sRGB. HWB stays inside sRGB by construction; the float path just skips
// the byte fold.
func wideHWBToSRGB(hue, white, black float64) [3]float64 {
	if white+black >= 1 {
		gray := white / (white + black)

		return [3]float64{gray, gray, gray}
	}

	hue = math.Mod(hue, hslCircleDeg)
	if hue < 0 {
		hue += hslCircleDeg
	}

	hSector := hue / hslSectorDeg
	xVal := 1 - math.Abs(math.Mod(hSector, hslEvenPeriod)-1)
	pureRed, pureGreen, pureBlue := hslSectorRGB(hSector, 1, xVal)
	scale := 1 - white - black

	return [3]float64{
		pureRed*scale + white,
		pureGreen*scale + white,
		pureBlue*scale + white,
	}
}

// wideSRGBToHWB converts unclamped sRGB to HWB for color-mix interpolation.
func wideSRGBToHWB(rgb [3]float64) [3]float64 {
	hue := wideSRGBToHSL(rgb)[0]
	white := math.Min(rgb[0], math.Min(rgb[1], rgb[2]))
	black := 1 - math.Max(rgb[0], math.Max(rgb[1], rgb[2]))

	return [3]float64{hue, white, black}
}

// wideRectToPolar converts (a, b) axes to (chroma, hue degrees).
func wideRectToPolar(aComp, bComp float64) (float64, float64) {
	chroma := math.Hypot(aComp, bComp)
	hue := math.Atan2(bComp, aComp) * (oklabHueDeg / oklabHalfTurn) / math.Pi

	if hue < 0 {
		hue += oklabHueDeg
	}

	return chroma, hue
}

// lerpHueShort interpolates hue angles along the shorter arc, in degrees.
func lerpHueShort(first, second, weightSecond float64) float64 {
	delta := math.Mod(second-first, oklabHueDeg)
	if delta > oklabHueDeg/oklabHalfTurn {
		delta -= oklabHueDeg
	}

	if delta < -oklabHueDeg/oklabHalfTurn {
		delta += oklabHueDeg
	}

	return first + delta*weightSecond
}

// parseWideOKLab parses oklab() without gamut folding: lightness, a and b
// are unbounded so out-of-gamut triplets carry headroom.
func parseWideOKLab(val string) ([3]float64, float64, bool) {
	body, found := colorFunctionBody(val)
	if !found {
		return [3]float64{}, 0, false
	}

	channels, alphaRaw, hasAlpha := splitColorAlpha(body)
	fields := strings.Fields(channels)

	if len(fields) != oklabChannelCount {
		return [3]float64{}, 0, false
	}

	light, found := parseWideOKLabLight(fields[0])
	if !found {
		return [3]float64{}, 0, false
	}

	aComp, found := parseOKLabAxis(fields[1])
	if !found {
		return [3]float64{}, 0, false
	}

	bComp, found := parseOKLabAxis(fields[2])
	if !found {
		return [3]float64{}, 0, false
	}

	alpha, found := parseModernAlpha(alphaRaw, hasAlpha)
	if !found {
		return [3]float64{}, 0, false
	}

	return wideOklabToSRGB(light, aComp, bComp), alpha, true
}

// parseWidePolarFields parses the shared oklch()/lch() shape: lightness,
// chroma and hue plus the optional alpha tail. parseLight and parseChroma
// differ per space; negative chroma floors at zero and hue normalizes onto
// the 0..360 wheel in both.
func parseWidePolarFields(
	val string,
	parseLight func(string) (float64, bool),
	parseChroma func(string) (float64, bool),
) (float64, float64, float64, float64, bool) {
	body, found := colorFunctionBody(val)
	if !found {
		return 0, 0, 0, 0, false
	}

	channels, alphaRaw, hasAlpha := splitColorAlpha(body)
	fields := strings.Fields(channels)

	if len(fields) != oklabChannelCount {
		return 0, 0, 0, 0, false
	}

	light, found := parseLight(fields[0])
	if !found {
		return 0, 0, 0, 0, false
	}

	chroma, found := parseChroma(fields[1])
	if !found {
		return 0, 0, 0, 0, false
	}

	if chroma < 0 {
		chroma = 0
	}

	hue, found := parseHueChannel(fields[2])
	if !found {
		return 0, 0, 0, 0, false
	}

	alpha, found := parseModernAlpha(alphaRaw, hasAlpha)
	if !found {
		return 0, 0, 0, 0, false
	}

	hue = math.Mod(hue, oklabHueDeg)
	if hue < 0 {
		hue += oklabHueDeg
	}

	return light, chroma, hue, alpha, true
}

// parseWideOKLCH parses oklch() without gamut folding: chroma is unbounded
// so high-chroma spellings carry headroom to the paint clamp.
func parseWideOKLCH(val string) ([3]float64, float64, bool) {
	light, chroma, hue, alpha, found := parseWidePolarFields(val, parseWideOKLabLight, parseOKLabAxis)
	if !found {
		return [3]float64{}, 0, false
	}

	rad := hue * math.Pi / (oklabHueDeg / oklabHalfTurn)

	return wideOklabToSRGB(light, chroma*math.Cos(rad), chroma*math.Sin(rad)), alpha, true
}

// parseWideOKLabLight parses oklab/oklch L without clamping: number 0..1 or
// percentage, so out-of-range lightness carries headroom.
func parseWideOKLabLight(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasSuffix(raw, "%") {
		num, err := strconv.ParseFloat(strings.TrimSuffix(raw, "%"), 64)
		if err != nil {
			return 0, false
		}

		return num / percentScale, true
	}

	num, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}

	return num, true
}

// parseWideColorFunc parses color(<space> <c1> <c2> <c3> [/ <alpha>]) for
// the srgb, display-p3, a98-rgb, prophoto-rgb and rec2020 spaces. Channels
// are numbers or percentages (100% maps onto 1), unbounded by design.
func parseWideColorFunc(val string) ([3]float64, float64, bool) {
	body, found := colorFunctionBody(val)
	if !found {
		return [3]float64{}, 0, false
	}

	channels, alphaRaw, hasAlpha := splitColorAlpha(body)
	fields := strings.Fields(channels)

	if len(fields) != rgbaChannelCount {
		return [3]float64{}, 0, false
	}

	space, found := wideRGBSpaceTable[strings.ToLower(fields[0])]
	if !found {
		return [3]float64{}, 0, false
	}

	comp := [3]float64{}

	for index := range oklabChannelCount {
		num, parsed := parseWideSpaceChannel(fields[index+1])
		if !parsed {
			return [3]float64{}, 0, false
		}

		comp[index] = num
	}

	alpha, found := parseModernAlpha(alphaRaw, hasAlpha)
	if !found {
		return [3]float64{}, 0, false
	}

	return wideSpaceToSRGB(space, comp), alpha, true
}

// parseWideSpaceChannel parses one color() channel: a number, or a
// percentage mapping 100% onto 1. Values are unbounded by design.
func parseWideSpaceChannel(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasSuffix(raw, "%") {
		num, err := strconv.ParseFloat(strings.TrimSuffix(raw, "%"), 64)
		if err != nil {
			return 0, false
		}

		return num / percentScale, true
	}

	num, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}

	return num, true
}

// parseWideLab parses lab() without gamut folding: L is unbounded on the
// 0..100 scale and a/b are unbounded, so wide triplets carry headroom.
func parseWideLab(val string) ([3]float64, float64, bool) {
	body, found := colorFunctionBody(val)
	if !found {
		return [3]float64{}, 0, false
	}

	channels, alphaRaw, hasAlpha := splitColorAlpha(body)
	fields := strings.Fields(channels)

	if len(fields) != oklabChannelCount {
		return [3]float64{}, 0, false
	}

	light, found := parseWideLabLight(fields[0])
	if !found {
		return [3]float64{}, 0, false
	}

	aComp, found := parseWideLabAxis(fields[1])
	if !found {
		return [3]float64{}, 0, false
	}

	bComp, found := parseWideLabAxis(fields[2])
	if !found {
		return [3]float64{}, 0, false
	}

	alpha, found := parseModernAlpha(alphaRaw, hasAlpha)
	if !found {
		return [3]float64{}, 0, false
	}

	return wideLabToSRGB(light, aComp, bComp), alpha, true
}

// parseWideLCH parses lch() without gamut folding: chroma is unbounded on
// the Lab scale, so high-chroma spellings carry headroom.
func parseWideLCH(val string) ([3]float64, float64, bool) {
	light, chroma, hue, alpha, found := parseWidePolarFields(val, parseWideLabLight, parseWideLabChroma)
	if !found {
		return [3]float64{}, 0, false
	}

	rad := hue * math.Pi / (oklabHueDeg / oklabHalfTurn)

	return wideLabToSRGB(light, chroma*math.Cos(rad), chroma*math.Sin(rad)), alpha, true
}

// parseWideLabLight parses lab/lch L without clamping: number 0..100 or
// percentage of the same scale.
func parseWideLabLight(raw string) (float64, bool) {
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "%"))

	num, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}

	return num, true
}

// parseWideLabAxis parses lab a/b: number, or percentage mapping 100% onto
// 125. Values are unbounded by design.
func parseWideLabAxis(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasSuffix(raw, "%") {
		num, err := strconv.ParseFloat(strings.TrimSuffix(raw, "%"), 64)
		if err != nil {
			return 0, false
		}

		return num / percentScale * wideLabPercentAB, true
	}

	num, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}

	return num, true
}

// parseWideLabChroma parses lch chroma: number, or percentage mapping 100%
// onto 150. Values are unbounded by design.
func parseWideLabChroma(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasSuffix(raw, "%") {
		num, err := strconv.ParseFloat(strings.TrimSuffix(raw, "%"), 64)
		if err != nil {
			return 0, false
		}

		return num / percentScale * wideLabPercentC, true
	}

	num, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}

	return num, true
}

// parseWideHWB parses hwb() through the float hue-wheel core. HWB stays
// inside sRGB by construction; the float path just skips the byte fold.
func parseWideHWB(val string) ([3]float64, float64, bool) {
	body, found := colorFunctionBody(val)
	if !found {
		return [3]float64{}, 0, false
	}

	channels, alphaRaw, hasAlpha := splitColorAlpha(body)
	fields := strings.Fields(channels)

	if len(fields) != oklabChannelCount {
		return [3]float64{}, 0, false
	}

	hue, found := parseHueChannel(fields[0])
	if !found {
		return [3]float64{}, 0, false
	}

	white, found := parseWideHWBAmount(fields[1])
	if !found {
		return [3]float64{}, 0, false
	}

	black, found := parseWideHWBAmount(fields[2])
	if !found {
		return [3]float64{}, 0, false
	}

	alpha, found := parseModernAlpha(alphaRaw, hasAlpha)
	if !found {
		return [3]float64{}, 0, false
	}

	return wideHWBToSRGB(hue, white, black), alpha, true
}

// parseWideHWBAmount parses one hwb whiteness/blackness percentage onto
// 0..1. Values are unbounded by design; white+black above 1 grays out.
func parseWideHWBAmount(raw string) (float64, bool) {
	num, parsed := parsePercentUnit(raw)
	if !parsed {
		return 0, false
	}

	return num / percentScale, true
}

// parseWideLightDark returns the light-scheme color through the wide
// pipeline so wide arguments keep their headroom. Both arguments must
// parse; the engine always renders the light scheme.
func parseWideLightDark(val string) ([3]float64, float64, bool) {
	body, ok := colorFunctionBody(val)
	if !ok {
		return [3]float64{}, 0, false
	}

	parts := splitTopLevelCommas(body)
	if len(parts) != lightDarkArgCount {
		return [3]float64{}, 0, false
	}

	light, lightAlpha, okLight := ParseColorWide(parts[0])
	_, _, okDark := ParseColorWide(parts[1])

	if !okLight || !okDark {
		return [3]float64{}, 0, false
	}

	return light, lightAlpha, true
}

// wideMixStop is one color-mix stop on the unclamped 0..1 sRGB scale.
type wideMixStop struct {
	rgb        [3]float64
	alpha      float64
	percent    float64
	hasPercent bool
}

// wideMixSpace converts between unclamped sRGB and one mixing space. hue is
// the component index holding the hue angle for polar spaces (shorter-arc
// interpolation), or -1 for rectangular spaces.
type wideMixSpace struct {
	toComp   func([3]float64) [3]float64
	fromComp func([3]float64) [3]float64
	hue      int
}

// parseWideColorMix mixes two wide stops in the named space without folding
// into bytes, so wide stops and wide spaces carry headroom to the result.
func parseWideColorMix(val string) ([3]float64, float64, bool) {
	body, ok := colorFunctionBody(val)
	if !ok {
		return [3]float64{}, 0, false
	}

	parts := splitTopLevelCommas(body)
	if len(parts) != wideMixPartCount {
		return [3]float64{}, 0, false
	}

	fields := strings.Fields(parts[0])
	if len(fields) != lightDarkArgCount || !strings.EqualFold(fields[0], "in") {
		return [3]float64{}, 0, false
	}

	converters, found := wideMixConverters(strings.ToLower(fields[1]))
	if !found {
		return [3]float64{}, 0, false
	}

	first, okFirst := parseWideMixStop(parts[1])
	second, okSecond := parseWideMixStop(parts[2])

	if !okFirst || !okSecond {
		return [3]float64{}, 0, false
	}

	weightFirst, weightSecond := mixPercentWeights(
		first.percent, first.hasPercent, second.percent, second.hasPercent,
	)

	return mixWideStops(first, second, converters, weightFirst, weightSecond)
}

func wideMixConverters(space string) (wideMixSpace, bool) {
	switch space {
	case "srgb":
		return wideMixSpace{
			toComp:   func(rgb [3]float64) [3]float64 { return rgb },
			fromComp: func(rgb [3]float64) [3]float64 { return rgb },
			hue:      wideHueNone,
		}, true
	case "display-p3", "a98-rgb", "prophoto-rgb", "rec2020":
		selected, found := wideRGBSpaceTable[space]
		if !found {
			return zeroWideMixSpace(), false
		}

		return wideMixSpace{
			toComp:   func(rgb [3]float64) [3]float64 { return wideSRGBToSpace(selected, rgb) },
			fromComp: func(comp [3]float64) [3]float64 { return wideSpaceToSRGB(selected, comp) },
			hue:      wideHueNone,
		}, true
	case "oklab":
		return wideMixSpace{toComp: srgbToOklabComps, fromComp: oklabCompsToSRGB, hue: wideHueNone}, true
	case "lab":
		return wideMixSpace{toComp: wideSRGBToLab, fromComp: labCompsToSRGB, hue: wideHueNone}, true
	case "oklch":
		return wideMixSpace{toComp: srgbToOklchComps, fromComp: oklchCompsToSRGB, hue: wideHueLast}, true
	case "lch":
		return wideMixSpace{toComp: srgbToLchComps, fromComp: lchCompsToSRGB, hue: wideHueLast}, true
	case "hsl":
		return wideMixSpace{toComp: wideSRGBToHSL, fromComp: hslCompsToSRGB, hue: wideHueFirst}, true
	case "hwb":
		return wideMixSpace{toComp: wideSRGBToHWB, fromComp: hwbCompsToSRGB, hue: wideHueFirst}, true
	}

	return zeroWideMixSpace(), false
}

// zeroWideMixSpace is the absent converter: no mapping and no hue.
func zeroWideMixSpace() wideMixSpace {
	return wideMixSpace{toComp: nil, fromComp: nil, hue: wideHueNone}
}

// zeroWideMixStop is the all-zero stop: no color and no percentage.
func zeroWideMixStop() wideMixStop {
	return wideMixStop{
		rgb:        [3]float64{},
		alpha:      0,
		percent:    0,
		hasPercent: false,
	}
}

// parseWideMixStop parses '<color> <percentage>?' with wide colors.
func parseWideMixStop(raw string) (wideMixStop, bool) {
	raw = strings.TrimSpace(raw)
	stop := zeroWideMixStop()

	if strings.HasSuffix(raw, "%") {
		space := strings.LastIndexByte(raw, ' ')
		if space < 0 {
			return zeroWideMixStop(), false
		}

		num, err := strconv.ParseFloat(strings.TrimSpace(raw[space+1:len(raw)-1]), 64)
		if err != nil {
			return zeroWideMixStop(), false
		}

		stop.percent = clampPercent(num)
		stop.hasPercent = true

		raw = strings.TrimSpace(raw[:space])
	}

	rgb, alpha, ok := ParseColorWide(raw)
	if !ok {
		return zeroWideMixStop(), false
	}

	stop.rgb, stop.alpha = rgb, alpha

	return stop, true
}

// mixWideStops blends two wide stops premultiplied in one mixing space,
// preserving headroom: components are never folded into bytes. Hue lerps
// along the shorter arc by the normalized second weight.
func mixWideStops(
	first, second wideMixStop, converters wideMixSpace, weightFirst, weightSecond float64,
) ([3]float64, float64, bool) {
	scaledFirst := first.alpha * weightFirst
	scaledSecond := second.alpha * weightSecond
	outAlpha := scaledFirst + scaledSecond

	if outAlpha == 0 {
		return [3]float64{}, 0, true
	}

	firstComp := converters.toComp(first.rgb)
	secondComp := converters.toComp(second.rgb)
	mixed := [3]float64{}

	for index := range oklabChannelCount {
		if index == converters.hue {
			mixed[index] = lerpHueShort(firstComp[index], secondComp[index], scaledSecond/outAlpha)

			continue
		}

		mixed[index] = (firstComp[index]*scaledFirst + secondComp[index]*scaledSecond) / outAlpha
	}

	return converters.fromComp(mixed), clampAlpha(outAlpha), true
}

// srgbToOklabComps adapts wideSRGBToOklab to the mixer component shape.
func srgbToOklabComps(rgb [3]float64) [3]float64 {
	light, aComp, bComp := wideSRGBToOklab(rgb)

	return [3]float64{light, aComp, bComp}
}

// oklabCompsToSRGB adapts wideOklabToSRGB to the mixer component shape.
func oklabCompsToSRGB(comp [3]float64) [3]float64 {
	return wideOklabToSRGB(comp[0], comp[1], comp[2])
}

// labCompsToSRGB adapts wideLabToSRGB to the mixer component shape.
func labCompsToSRGB(comp [3]float64) [3]float64 {
	return wideLabToSRGB(comp[0], comp[1], comp[2])
}

// srgbToOklchComps converts sRGB to Oklch components for mixing.
func srgbToOklchComps(rgb [3]float64) [3]float64 {
	light, aComp, bComp := wideSRGBToOklab(rgb)
	chroma, hue := wideRectToPolar(aComp, bComp)

	return [3]float64{light, chroma, hue}
}

// oklchCompsToSRGB converts Oklch components back to sRGB for mixing.
func oklchCompsToSRGB(comp [3]float64) [3]float64 {
	rad := comp[2] * math.Pi / (oklabHueDeg / oklabHalfTurn)

	return wideOklabToSRGB(comp[0], comp[1]*math.Cos(rad), comp[1]*math.Sin(rad))
}

// srgbToLchComps converts sRGB to LCH components for mixing.
func srgbToLchComps(rgb [3]float64) [3]float64 {
	lab := wideSRGBToLab(rgb)
	chroma, hue := wideRectToPolar(lab[1], lab[2])

	return [3]float64{lab[0], chroma, hue}
}

// lchCompsToSRGB converts LCH components back to sRGB for mixing.
func lchCompsToSRGB(comp [3]float64) [3]float64 {
	rad := comp[2] * math.Pi / (oklabHueDeg / oklabHalfTurn)

	return wideLabToSRGB(comp[0], comp[1]*math.Cos(rad), comp[1]*math.Sin(rad))
}

// hslCompsToSRGB adapts wideHSLToSRGB to the mixer component shape.
func hslCompsToSRGB(comp [3]float64) [3]float64 {
	return wideHSLToSRGB(comp[0], comp[1], comp[2])
}

// hwbCompsToSRGB adapts wideHWBToSRGB to the mixer component shape.
func hwbCompsToSRGB(comp [3]float64) [3]float64 {
	return wideHWBToSRGB(comp[0], comp[1], comp[2])
}
