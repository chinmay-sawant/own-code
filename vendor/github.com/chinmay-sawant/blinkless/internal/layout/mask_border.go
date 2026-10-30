package layout

import (
	"strconv"
	"strings"
)

// Mask-border property names. The six longhands carry the same image,
// slicing, and repeat rules as border-image; the shorthand is expanded in the
// cascade, and the -webkit-mask-box-image* aliases normalize onto these
// names.
const (
	maskBorderShorthandProperty = "mask-border"
	maskBorderSourceProperty    = "mask-border-source"
	maskBorderSliceProperty     = "mask-border-slice"
	maskBorderWidthProperty     = "mask-border-width"
	maskBorderOutsetProperty    = "mask-border-outset"
	maskBorderRepeatProperty    = "mask-border-repeat"
	maskBorderModeProperty      = "mask-border-mode"

	// maskBorderDeferredPrefix marks a longhand whose value is the whole
	// shorthand text still waiting for var() substitution, like the mask
	// family's maskShorthandDeferredPrefix.
	maskBorderDeferredPrefix = "\x00mask-border:"

	maskBorderValueMax        = 4
	maskBorderRepeatMaxTokens = 2
	maskBorderSlashToken      = "/"
	maskBorderFillKeyword     = "fill"
	maskBorderInitialSlice    = "0"
	maskBorderInitialOutset   = "0"
)

// MaskBorder is the resolved mask-border frame of one element group: the
// source image and the numbers that decide how it is cut and placed. The
// engine records the frame for the host. It emits no OpImage for a
// mask-border and paints no border-image cells for it.
type MaskBorder struct {
	// Source is the used CSS image token. An image-set() collapses to its
	// chosen candidate.
	Source string
	// ImageBytes is the encoded payload from the shared image resolver,
	// unchanged. Nil when the source did not resolve or is a gradient, which
	// waits on the mask-source ledger.
	ImageBytes  []byte
	PixelWidth  int
	PixelHeight int
	// Slice holds the four source insets in source pixels, in top, right,
	// bottom, left order. Numbers count source pixels and percentages
	// resolve against the source image size.
	Slice [4]float64
	// SliceFill is the mask-border-slice fill flag.
	SliceFill bool
	// Width holds the four frame thicknesses in points, top, right, bottom,
	// left. auto and bare numbers resolve against the painted border widths,
	// the same rules border-image uses.
	Width [4]float64
	// Outset holds the four outward offsets in points, top, right, bottom,
	// left.
	Outset [4]float64
	// Repeat is the used repeat pair, such as "stretch" or "repeat space".
	Repeat string
	// Mode is "alpha" or "luminance". It is stored, not baked into
	// ImageBytes; only the first-wave SVG mask preprocessing bakes a mode.
	Mode string
}

// normalizeWebkitMaskBoxImageAlias maps the six -webkit-mask-box-image*
// aliases onto the mask-border family. It lives here, not in
// style_cascade.go, so the cascade file stays under the size limit.
func normalizeWebkitMaskBoxImageAlias(prop string) (string, bool) {
	switch prop {
	case "-webkit-mask-box-image":
		return maskBorderShorthandProperty, true
	case "-webkit-mask-box-image-outset":
		return maskBorderOutsetProperty, true
	case "-webkit-mask-box-image-repeat":
		return maskBorderRepeatProperty, true
	case "-webkit-mask-box-image-slice":
		return maskBorderSliceProperty, true
	case "-webkit-mask-box-image-source":
		return maskBorderSourceProperty, true
	case "-webkit-mask-box-image-width":
		return maskBorderWidthProperty, true
	default:
		return prop, false
	}
}

// maskBorderEnabled reports whether a used mask-border source asks for a
// frame. none and the empty string do not.
func maskBorderEnabled(source string) bool {
	trimmed := strings.TrimSpace(source)

	return trimmed != "" && !strings.EqualFold(trimmed, cssDisplayNone)
}

// maskGroupRequested reports whether the element needs a compositing group
// for a first-wave mask or for a mask-border frame.
func maskGroupRequested(style ResolvedStyle) bool {
	return maskImageEnabled(style.MaskImage) || maskBorderEnabled(style.MaskBorderSource)
}

// attachMaskDescriptors records the element's mask and mask-border frames on
// its group. A family whose used source is none keeps a nil pointer.
func (e *engine) attachMaskDescriptors(group *BlendGroup, style ResolvedStyle) {
	if maskImageEnabled(style.MaskImage) {
		group.Mask = e.maskDescriptor(style)
	}

	if maskBorderEnabled(style.MaskBorderSource) {
		group.MaskBorder = e.maskBorderDescriptor(style)
	}
}

// maskBorderDescriptor resolves the used longhands into the mask-border frame
// recorded on the group. It borrows the slice, thickness, and outset
// arithmetic from border_image.go and appends no operations.
func (e *engine) maskBorderDescriptor(style ResolvedStyle) *MaskBorder {
	source := maskBorderUsedSource(style.MaskBorderSource)
	mode := maskBorderUsedMode(style.MaskBorderMode)
	layer := e.resolveMaskLayer(MaskLayer{ //nolint:exhaustruct // only the source and mode are read
		Source: source,
		Mode:   mode,
	})
	thick := borderImageThickness(
		parseBorderImageDimensionFour(style.MaskBorderWidth, style.FontSize),
		borderWidthsOf(style),
	)

	descriptor := &MaskBorder{ //nolint:exhaustruct // slice and outset are set below
		Source:      source,
		ImageBytes:  layer.ImageBytes,
		PixelWidth:  layer.PixelWidth,
		PixelHeight: layer.PixelHeight,
		Repeat:      maskBorderUsedRepeat(style.MaskBorderRepeat),
		Mode:        mode,
		Width:       thick,
	}
	descriptor.Slice, descriptor.SliceFill = maskBorderUsedSlice(style.MaskBorderSlice, layer)
	descriptor.Outset = borderImageOutset(
		parseBorderImageDimensionFour(style.MaskBorderOutset, style.FontSize),
		thick,
	)

	return descriptor
}

// maskBorderUsedSource collapses an image-set() source to its chosen
// candidate, matching the first-wave mask path.
func maskBorderUsedSource(value string) string {
	trimmed := strings.TrimSpace(value)
	if chosen, ok := maskImageSetChoice(trimmed); ok {
		return chosen
	}

	return trimmed
}

// maskBorderUsedMode falls back to alpha for an unreadable stored mode, the
// property initial.
func maskBorderUsedMode(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), maskModeLuminance) {
		return maskModeLuminance
	}

	return maskModeAlpha
}

// maskBorderUsedRepeat returns the stored repeat pair, or the initial stretch
// when the stored value is not a readable pair.
func maskBorderUsedRepeat(value string) string {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	if len(tokens) == 0 || len(tokens) > maskBorderRepeatMaxTokens {
		return fxStretch
	}

	for _, token := range tokens {
		if !isBorderImageRepeatToken(token) {
			return fxStretch
		}
	}

	return strings.Join(tokens, " ")
}

// maskBorderUsedSlice resolves the slice string to source-pixel insets plus
// the fill flag. An unreadable slice keeps the initial 0.
func maskBorderUsedSlice(value string, layer MaskLayer) ([4]float64, bool) {
	fracs, ok, fill := parseBorderImageSliceFracs(value, layer.PixelWidth, layer.PixelHeight)
	if !ok {
		return [4]float64{}, false
	}

	return maskBorderSliceInsets(fracs, layer.PixelWidth, layer.PixelHeight), fill
}

// maskBorderSliceInsets converts fractional slice geometry to source pixel
// insets in top, right, bottom, left order. The top and bottom axes use the
// image height, the left and right axes the image width.
func maskBorderSliceInsets(fracs [4]float64, imgW, imgH int) [4]float64 {
	var insets [4]float64

	for idx := range insets {
		insets[idx] = fracs[idx] * float64(maskBorderSliceAxisSize(idx, imgW, imgH))
	}

	return insets
}

func maskBorderSliceAxisSize(idx, imgW, imgH int) int {
	if idx == borderImageTopEdge || idx == borderImageBottomEdge {
		return imgH
	}

	return imgW
}

// applyMaskBorderProps stores the six mask-border longhands. The shorthand is
// expanded in the cascade, so a raw mask-border key resolves the deferred
// var() form and is otherwise a no-op.
func applyMaskBorderProps(
	style *ResolvedStyle, prop, value string, _ float64, _ *styleContext,
	parent *ResolvedStyle, hasParent bool,
) bool {
	if prop == maskBorderShorthandProperty {
		return true
	}

	property, ok := maskBorderStylePropertyFor(style, parent, prop)
	if !ok {
		return false
	}

	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, maskBorderDeferredPrefix) {
		trimmed = resolveMaskBorderComponent(prop, trimmed)
	}

	if wide, ok := cssWideValue(trimmed, maskParentValue(property.parentField), property.initial, hasParent); ok {
		*property.field = wide

		return true
	}

	if property.lower {
		trimmed = strings.ToLower(trimmed)
	}

	*property.field = trimmed

	return true
}

func maskBorderStylePropertyFor(style, parent *ResolvedStyle, prop string) (maskStyleProperty, bool) {
	field := maskBorderStyleField(style, prop)

	initial, ok := maskBorderStyleInitial(prop)
	if !ok {
		return maskStyleProperty{field: nil, parentField: nil, initial: "", lower: false}, false
	}

	var parentField *string
	if parent != nil {
		parentField = maskBorderStyleField(parent, prop)
	}

	return maskStyleProperty{
		field:       field,
		parentField: parentField,
		initial:     initial,
		lower:       maskBorderStyleLowercase(prop),
	}, true
}

func maskBorderStyleField(style *ResolvedStyle, prop string) *string {
	switch prop {
	case maskBorderSourceProperty:
		return &style.MaskBorderSource
	case maskBorderSliceProperty:
		return &style.MaskBorderSlice
	case maskBorderWidthProperty:
		return &style.MaskBorderWidth
	case maskBorderOutsetProperty:
		return &style.MaskBorderOutset
	case maskBorderRepeatProperty:
		return &style.MaskBorderRepeat
	case maskBorderModeProperty:
		return &style.MaskBorderMode
	default:
		return nil
	}
}

func maskBorderStyleInitial(prop string) (string, bool) {
	switch prop {
	case maskBorderSourceProperty:
		return cssDisplayNone, true
	case maskBorderSliceProperty:
		return maskBorderInitialSlice, true
	case maskBorderWidthProperty:
		return overflowAuto, true
	case maskBorderOutsetProperty:
		return maskBorderInitialOutset, true
	case maskBorderRepeatProperty:
		return fxStretch, true
	case maskBorderModeProperty:
		return maskModeAlpha, true
	default:
		return "", false
	}
}

func maskBorderStyleLowercase(prop string) bool {
	switch prop {
	case maskBorderRepeatProperty, maskBorderModeProperty:
		return true
	default:
		return false
	}
}

// resolveMaskBorderComponent reads one longhand out of a deferred shorthand
// value once var() substitution has run. A value that no longer parses falls
// back to the property initial, so a failed substitution cannot revive an
// earlier declaration.
func resolveMaskBorderComponent(prop, prefixed string) string {
	shorthand := strings.TrimPrefix(prefixed, maskBorderDeferredPrefix)

	if cssWideKeyword(shorthand) {
		return shorthand
	}

	if values, ok := parseMaskBorderShorthand(shorthand); ok {
		return values.component(prop)
	}

	initial, _ := maskBorderStyleInitial(prop)

	return initial
}

// maskBorderDeclarationAccepted is the cascade and @supports acceptance rule
// for the mask-border family. It mirrors each longhand reader so an invalid
// declaration neither wins the cascade nor satisfies a feature query.
func maskBorderDeclarationAccepted(prop, value string) (bool, bool) {
	switch prop {
	case maskBorderShorthandProperty:
		return maskBorderShorthandValueAccepted(value), true
	case maskBorderSourceProperty:
		return maskBorderSourceValueAccepted(value), true
	case maskBorderSliceProperty:
		return maskBorderSliceValueAccepted(value), true
	case maskBorderWidthProperty:
		return maskBorderWidthValueAccepted(value), true
	case maskBorderOutsetProperty:
		return maskBorderOutsetValueAccepted(value), true
	case maskBorderRepeatProperty:
		return maskBorderRepeatValueAccepted(value), true
	case maskBorderModeProperty:
		return maskBorderModeValueAccepted(value), true
	default:
		return false, false
	}
}

// maskBorderSourceValueAccepted mirrors the descriptor reader: none, a url(),
// or an image-set() whose chosen candidate is a url(). Gradients are rejected
// until the mask-source ledger accepts them, so a gradient cannot replace an
// earlier resolvable source.
func maskBorderSourceValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, cssDisplayNone) {
		return true
	}

	if isGradientFunc(trimmed) {
		return false
	}

	if chosen, ok := maskImageSetChoice(trimmed); ok {
		return maskBorderURLValueAccepted(chosen)
	}

	return maskBorderURLValueAccepted(trimmed)
}

func maskBorderURLValueAccepted(value string) bool {
	if !cssURLFunctionValue(value) {
		return false
	}

	_, ok := firstCSSUrl(value)

	return ok
}

// maskBorderSliceValueAccepted takes one to four non-negative numbers or
// percentages with the optional fill keyword.
func maskBorderSliceValueAccepted(value string) bool {
	withoutFill, _ := stripBorderImageFill(strings.TrimSpace(value))

	tokens := strings.Fields(withoutFill)
	if len(tokens) == 0 || len(tokens) > maskBorderValueMax {
		return false
	}

	for _, token := range tokens {
		if !maskBorderSliceTokenAccepted(token) {
			return false
		}
	}

	return true
}

func maskBorderSliceTokenAccepted(token string) bool {
	if percent, ok := strings.CutSuffix(token, "%"); ok {
		value, err := strconv.ParseFloat(strings.TrimSpace(percent), 64)

		return err == nil && value >= 0
	}

	value, err := strconv.ParseFloat(token, 64)

	return err == nil && value >= 0
}

// maskBorderWidthValueAccepted takes one to four auto, number, or non-negative
// length values. Percentages are rejected: the descriptor is written before
// the element box exists, and border_image.go resolves a percentage to zero,
// so a percentage would store a wrong thickness instead of keeping an earlier
// valid declaration.
func maskBorderWidthValueAccepted(value string) bool {
	return maskBorderValueListAccepted(value, maskBorderWidthTokenAccepted)
}

func maskBorderWidthTokenAccepted(token string) bool {
	if strings.EqualFold(token, overflowAuto) {
		return true
	}

	return maskBorderBareOrLengthAccepted(token)
}

// maskBorderOutsetValueAccepted takes one to four non-negative numbers or
// lengths, the border-image-outset grammar.
func maskBorderOutsetValueAccepted(value string) bool {
	return maskBorderValueListAccepted(value, maskBorderBareOrLengthAccepted)
}

func maskBorderValueListAccepted(value string, accepted func(string) bool) bool {
	tokens := strings.Fields(strings.TrimSpace(value))
	if len(tokens) == 0 || len(tokens) > maskBorderValueMax {
		return false
	}

	for _, token := range tokens {
		if !accepted(token) {
			return false
		}
	}

	return true
}

func maskBorderBareOrLengthAccepted(token string) bool {
	if value, err := strconv.ParseFloat(token, 64); err == nil {
		return value >= 0
	}

	if strings.Contains(token, "%") {
		return false
	}

	length, ok := plainLength(token, 0, 0)

	return ok && length >= 0
}

// maskBorderRepeatValueAccepted takes one or two border-image repeat keywords.
func maskBorderRepeatValueAccepted(value string) bool {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	if len(tokens) == 0 || len(tokens) > maskBorderRepeatMaxTokens {
		return false
	}

	for _, token := range tokens {
		if !isBorderImageRepeatToken(token) {
			return false
		}
	}

	return true
}

func maskBorderModeValueAccepted(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case maskModeAlpha, maskModeLuminance:
		return true
	default:
		return false
	}
}

// maskBorderShorthandValueAccepted reports whether the shorthand parses. The
// acceptance rule and the expander share one parser, so an accepted value
// always expands into longhands.
func maskBorderShorthandValueAccepted(value string) bool {
	_, ok := parseMaskBorderShorthand(value)

	return ok
}

// cascadeMaskBorderShorthand handles the mask-border shorthand before the
// general expansion runs. A var() value is deferred onto the six longhands so
// the substituted text can resolve later, and the declaration is fully
// handled. A CSS-wide keyword is recorded on the shorthand name itself; the
// six longhands still expand below, so handled stays false for it.
func cascadeMaskBorderShorthand(
	wins map[string]cascadeWin, prop, value string,
	ids, classes, types, order, layer int, important bool,
) bool {
	if prop != maskBorderShorthandProperty {
		return false
	}

	trimmed := strings.TrimSpace(value)

	if cssWideKeyword(trimmed) {
		applyCascadeWin(wins, prop, value, ids, classes, types, order, layer, important)

		return false
	}

	if !containsVarFunc(value) {
		return false
	}

	for _, longhand := range maskBorderLonghandProps() {
		applyCascadeWin(wins, longhand, maskBorderDeferredPrefix+value, ids, classes, types, order, layer, important)
	}

	return true
}

// expandMaskBorderShorthandDeclaration expands the mask-border shorthand into
// its six longhands at the shorthand's own origin and specificity, so a later
// longhand overrides the piece it names and an earlier one loses. Omitted
// pieces reset to the initial values, which is why the mask shorthand reset
// stays effective. A CSS-wide keyword resets the six longhands to that
// keyword; applyCascadeDeclaration records the keyword on the shorthand name
// itself, because re-emitting it here would re-enter this expander.
func expandMaskBorderShorthandDeclaration(prop, value string) ([]logicalPropDecl, bool) {
	if prop != maskBorderShorthandProperty {
		return nil, false
	}

	trimmed := strings.TrimSpace(value)

	if containsVarFunc(value) {
		return nil, false
	}

	if cssWideKeyword(trimmed) {
		return maskBorderLonghandDecls(maskBorderKeywordValues(trimmed)), true
	}

	values, ok := parseMaskBorderShorthand(value)
	if !ok {
		return nil, false
	}

	return maskBorderLonghandDecls(values), true
}

// maskBorderKeywordValues mirrors one CSS-wide keyword onto every component.
func maskBorderKeywordValues(value string) maskBorderShorthandValues {
	return maskBorderShorthandValues{
		source: value,
		slice:  value,
		width:  value,
		outset: value,
		repeat: value,
		mode:   value,
	}
}

func maskBorderLonghandDecls(values maskBorderShorthandValues) []logicalPropDecl {
	props := maskBorderLonghandProps()
	out := make([]logicalPropDecl, 0, len(props))

	for _, prop := range props {
		out = append(out, logicalPropDecl{prop: prop, val: values.component(prop)})
	}

	return out
}

func maskBorderLonghandProps() [6]string {
	return [6]string{
		maskBorderSourceProperty, maskBorderSliceProperty, maskBorderWidthProperty,
		maskBorderOutsetProperty, maskBorderRepeatProperty, maskBorderModeProperty,
	}
}

// maskBorderShorthandValues is one parsed mask-border shorthand in longhand
// form. Every component is present; omitted pieces carry the initial value.
type maskBorderShorthandValues struct {
	source string
	slice  string
	width  string
	outset string
	repeat string
	mode   string
}

func maskBorderInitialValues() maskBorderShorthandValues {
	return maskBorderShorthandValues{
		source: cssDisplayNone,
		slice:  maskBorderInitialSlice,
		width:  overflowAuto,
		outset: maskBorderInitialOutset,
		repeat: fxStretch,
		mode:   maskModeAlpha,
	}
}

func (values maskBorderShorthandValues) component(prop string) string {
	switch prop {
	case maskBorderSourceProperty:
		return values.source
	case maskBorderSliceProperty:
		return values.slice
	case maskBorderWidthProperty:
		return values.width
	case maskBorderOutsetProperty:
		return values.outset
	case maskBorderRepeatProperty:
		return values.repeat
	case maskBorderModeProperty:
		return values.mode
	default:
		return ""
	}
}

// accepted reports whether every component passes its longhand rule.
func (values maskBorderShorthandValues) accepted() bool {
	return maskBorderSourceValueAccepted(values.source) &&
		maskBorderSliceValueAccepted(values.slice) &&
		maskBorderWidthValueAccepted(values.width) &&
		maskBorderOutsetValueAccepted(values.outset) &&
		maskBorderRepeatValueAccepted(values.repeat) &&
		maskBorderModeValueAccepted(values.mode)
}

// maskBorderShorthandParse accumulates the shorthand components while its
// tokens are scanned. Components are recognized by kind, so repeat and mode
// keywords may sit on either side of the slice/width/outset slashes, the way
// the border-image grammar allows.
type maskBorderShorthandParse struct {
	values   maskBorderShorthandValues
	slice    []string
	width    []string
	outset   []string
	repeats  []string
	sources  []string
	modes    []string
	section  int
	fillSeen bool
}

// parseMaskBorderShorthand reads the mask-border shorthand: an optional
// source, the slice with its fill flag, optional / width, optional / outset,
// and the repeat and mode keywords. At least one component must be present,
// and the slash groups require the component before them.
func parseMaskBorderShorthand(value string) (maskBorderShorthandValues, bool) {
	initial := maskBorderInitialValues()
	parse := maskBorderShorthandParse{
		values:   initial,
		slice:    nil,
		width:    nil,
		outset:   nil,
		repeats:  nil,
		sources:  nil,
		modes:    nil,
		section:  borderImageSliceSection,
		fillSeen: false,
	}

	for _, token := range maskBorderTokens(value) {
		if !parse.addToken(token) {
			return initial, false
		}
	}

	return parse.resolve()
}

// maskBorderTokens splits a shorthand value on top-level whitespace. A
// top-level "/" is always its own token, even without surrounding spaces, and
// text inside parentheses or quotes stays in one token.
func maskBorderTokens(value string) []string {
	scanner := maskBorderTokenScanner{
		tokens:  make([]string, 0, cssSpaceTokenCapacity),
		current: strings.Builder{},
		depth:   0,
		quote:   0,
	}

	for idx := range len(value) {
		scanner.consume(value[idx])
	}

	scanner.flush()

	return scanner.tokens
}

// maskBorderTokenScanner carries the lexer state for one shorthand value:
// the tokens emitted so far, the token under construction, the parenthesis
// depth, and the open quote byte.
type maskBorderTokenScanner struct {
	tokens  []string
	current strings.Builder
	depth   int
	quote   byte
}

func (scanner *maskBorderTokenScanner) consume(char byte) {
	if scanner.quote != 0 {
		scanner.consumeQuote(char)

		return
	}

	switch char {
	case '"', '\'':
		scanner.quote = char
		scanner.current.WriteByte(char)
	case '(':
		scanner.depth++
		scanner.current.WriteByte(char)
	case ')':
		scanner.closeParen()
	case '/':
		scanner.slash()
	default:
		scanner.spaceOrByte(char)
	}
}

func (scanner *maskBorderTokenScanner) consumeQuote(char byte) {
	if char == scanner.quote {
		scanner.quote = 0
	}

	scanner.current.WriteByte(char)
}

func (scanner *maskBorderTokenScanner) closeParen() {
	if scanner.depth > 0 {
		scanner.depth--
	}

	scanner.current.WriteByte(')')
}

// slash records a top-level slash as its own token. A slash inside a
// function, such as a url() path, stays part of the token.
func (scanner *maskBorderTokenScanner) slash() {
	if scanner.depth != 0 {
		scanner.current.WriteByte('/')

		return
	}

	scanner.flush()
	scanner.tokens = append(scanner.tokens, maskBorderSlashToken)
}

func (scanner *maskBorderTokenScanner) spaceOrByte(char byte) {
	if scanner.depth == 0 && isCSSSpace(char) {
		scanner.flush()

		return
	}

	scanner.current.WriteByte(char)
}

func (scanner *maskBorderTokenScanner) flush() {
	if scanner.current.Len() == 0 {
		return
	}

	scanner.tokens = append(scanner.tokens, scanner.current.String())
	scanner.current.Reset()
}

func (parse *maskBorderShorthandParse) addToken(token string) bool {
	switch {
	case token == maskBorderSlashToken:
		return parse.addSlash()
	case strings.EqualFold(token, maskBorderFillKeyword):
		return parse.addFill()
	case maskBorderModeValueAccepted(token):
		return parse.addMode(strings.ToLower(token))
	case isBorderImageRepeatToken(strings.ToLower(token)):
		return parse.addRepeat(strings.ToLower(token))
	case maskBorderSourceValueAccepted(token):
		return parse.addSource(token)
	default:
		return parse.addDimension(token)
	}
}

func (parse *maskBorderShorthandParse) addSlash() bool {
	if parse.section >= borderImageOutsetSection {
		return false
	}

	parse.section++

	return true
}

func (parse *maskBorderShorthandParse) addFill() bool {
	if parse.fillSeen || parse.section != borderImageSliceSection {
		return false
	}

	parse.fillSeen = true

	return true
}

func (parse *maskBorderShorthandParse) addMode(value string) bool {
	return appendMaskBorderOnce(&parse.modes, strings.ToLower(value))
}

func (parse *maskBorderShorthandParse) addRepeat(value string) bool {
	if len(parse.repeats) >= maskBorderRepeatMaxTokens {
		return false
	}

	parse.repeats = append(parse.repeats, value)

	return true
}

func (parse *maskBorderShorthandParse) addSource(value string) bool {
	return appendMaskBorderOnce(&parse.sources, strings.TrimSpace(value))
}

func (parse *maskBorderShorthandParse) addDimension(token string) bool {
	switch parse.section {
	case borderImageSliceSection:
		return appendMaskBorderValue(&parse.slice, token, maskBorderSliceTokenAccepted)
	case borderImageWidthSection:
		return appendMaskBorderValue(&parse.width, token, maskBorderWidthTokenAccepted)
	default:
		return appendMaskBorderValue(&parse.outset, token, maskBorderBareOrLengthAccepted)
	}
}

func appendMaskBorderValue(dst *[]string, token string, accepted func(string) bool) bool {
	if len(*dst) >= maskBorderValueMax || !accepted(token) {
		return false
	}

	*dst = append(*dst, token)

	return true
}

func appendMaskBorderOnce(dst *[]string, value string) bool {
	if len(*dst) > 0 {
		return false
	}

	*dst = append(*dst, value)

	return true
}

func (parse *maskBorderShorthandParse) anyComponent() bool {
	return len(parse.sources) > 0 || len(parse.modes) > 0 || parse.fillSeen ||
		len(parse.slice) > 0 || len(parse.width) > 0 ||
		len(parse.outset) > 0 || len(parse.repeats) > 0
}

// sectionsComplete reports whether every slash group carries the component
// before it, so "30 / 10px" is valid but "url(a.png) / 10px" and
// "30 / / 10px" are not.
func (parse *maskBorderShorthandParse) sectionsComplete() bool {
	if parse.section > borderImageSliceSection && len(parse.slice) == 0 {
		return false
	}

	if parse.section > borderImageWidthSection && len(parse.width) == 0 {
		return false
	}

	return true
}

// resolve assembles the longhand values. Omitted pieces keep the initial
// value, and every assembled component is checked against its longhand rule.
func (parse *maskBorderShorthandParse) resolve() (maskBorderShorthandValues, bool) {
	if !parse.anyComponent() || !parse.sectionsComplete() {
		return parse.values, false
	}

	values := maskBorderInitialValues()
	values.source = maskBorderJoined(parse.sources, values.source)
	values.width = maskBorderJoined(parse.width, values.width)
	values.outset = maskBorderJoined(parse.outset, values.outset)
	values.repeat = maskBorderJoined(parse.repeats, values.repeat)
	values.mode = maskBorderJoined(parse.modes, values.mode)
	values.slice = maskBorderSliceValue(parse.slice, parse.fillSeen, values.slice)

	if !values.accepted() {
		return parse.values, false
	}

	return values, true
}

// maskBorderJoined joins a token run, or returns fallback when the run is
// empty.
func maskBorderJoined(tokens []string, fallback string) string {
	if len(tokens) == 0 {
		return fallback
	}

	return strings.Join(tokens, " ")
}

// maskBorderSliceValue joins the slice tokens and appends fill when set. A
// lone fill is not a valid slice, so it is returned bare for the acceptance
// check to reject.
func maskBorderSliceValue(tokens []string, fill bool, fallback string) string {
	if len(tokens) == 0 && !fill {
		return fallback
	}

	value := strings.Join(tokens, " ")
	if !fill {
		return value
	}

	return strings.TrimSpace(value + " " + maskBorderFillKeyword)
}
