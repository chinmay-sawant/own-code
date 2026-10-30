// Ruby annotation geometry helpers (CSS Ruby 1, draft) plus the cascade and
// inline wiring that uses them.
//
// The engine stacks <rt>/<rtc> annotations above or below their base at half
// size: cascade storage lives in CustomProps (see applyRubyProps),
// segmentation and horizontal overlay in collectRubyElement, and vertical
// stacking in lineMetrics/emitLineItems. The gap test in
// css_behavior_ruby_test.go pinned the old flattened output; the Layout tests
// in css_behavior_ruby_layout_test.go assert the stacked geometry instead.
//
// Approximate on purpose (each documented where it applies): inter-character
// annotations paint inline at half size instead of beside the base;
// ruby-merge:merge concatenates a whole <ruby> into one group segment;
// line breaks may split a segment across lines; rp fallback text is dropped;
// nested annotations below a direct rt/rtc child stay flat.
//
// This file therefore holds only pure geometry: given measured base and
// annotation widths and heights in points, where does the annotation go?
// Each helper takes the already-parsed used value (never a stored string)
// and returns coordinates in points. Reference browser for every case:
// Chrome 143.0.7499.40. Spec: https://drafts.csswg.org/css-ruby-1/
//
// Used values and initials used here:
//
//	ruby-align: start | center | space-between | space-around, initial space-around
//	ruby-merge: separate | merge | auto, initial separate
//	ruby-overhang: auto | spaces (legacy alias none), initial auto
//	ruby-position: [ alternate || [ over | under ] ] | inter-character, initial alternate
package layout

import (
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

// Canonical ruby used values kept here so geometry helpers and tests share
// one spelling. Parsing itself is not wired to the cascade yet (see the seam
// note on rubyCascadeSeam).
const (
	rubyAlignStart        = "start"
	rubyAlignCenter       = "center"
	rubyAlignSpaceBetween = "space-between"
	rubyAlignSpaceAround  = "space-around"

	rubyMergeSeparate = "separate"
	rubyMergeMerge    = "merge"
	rubyMergeAuto     = "auto"

	rubyOverhangAuto   = "auto"
	rubyOverhangSpaces = "spaces"

	rubyPositionOver           = "over"
	rubyPositionUnder          = "under"
	rubyPositionAlternate      = "alternate"
	rubyPositionInterCharacter = "inter-character"
)

// Integration seam: LANDED. The four declarations are stored as canonical
// used values in CustomProps by applyRubyProps (registered in styleGroups in
// internal/layout/style_cascade.go), stacked in the inline collector by
// collectRubyElement (hooked in collectInlineElement in
// internal/layout/inline_collect.go), and emitted with a stacked baseline in
// emitLineItems/lineMetrics (internal/layout/inline.go). CustomProps was
// chosen over a new typed style group because style_intern_gen.go hashes
// every typed field: a new field set would need regenerating that file, while
// CustomProps is already inherited through mergeCustomProps, interned through
// maps.Equal plus the string-map hash, and precedented by the text-emphasis
// bookkeeping in style_text_props.go. The old drop path
// (applySVGPresentationProps in style_paint_props.go listing the four names,
// then applyLeftoversProps in style_leftovers.go returning false) is now
// harmless: dispatch tries those arms first, they decline, and applyRubyProps
// claims the property later in the table.

// parseRubyAlign normalizes a ruby-align value to its canonical used value.
// Unknown values return false so the caller keeps the initial space-around.
func parseRubyAlign(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case rubyAlignStart:
		return rubyAlignStart, true
	case rubyAlignCenter:
		return rubyAlignCenter, true
	case rubyAlignSpaceBetween:
		return rubyAlignSpaceBetween, true
	case rubyAlignSpaceAround:
		return rubyAlignSpaceAround, true
	default:
		return "", false
	}
}

// parseRubyMerge normalizes a ruby-merge value. Only the spec trio is
// accepted; anything else (including the old draft word collapse) returns
// false so the caller keeps the initial separate.
func parseRubyMerge(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case rubyMergeSeparate:
		return rubyMergeSeparate, true
	case rubyMergeMerge:
		return rubyMergeMerge, true
	case rubyMergeAuto:
		return rubyMergeAuto, true
	default:
		return "", false
	}
}

// parseRubyOverhang normalizes a ruby-overhang value. The legacy alias none
// maps to spaces per the spec; unknown values return false (initial auto).
func parseRubyOverhang(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case rubyOverhangAuto:
		return rubyOverhangAuto, true
	case rubyOverhangSpaces:
		return rubyOverhangSpaces, true
	case "none":
		return rubyOverhangSpaces, true
	default:
		return "", false
	}
}

// parseRubyPosition normalizes a ruby-position value to one of over, under,
// alternate, or inter-character. The grammar is
// [ alternate || [ over | under ] ] | inter-character; inter-character is
// exclusive, alternate may combine with at most one of over/under. Unknown
// values return false (initial alternate).
func parseRubyPosition(value string) (string, bool) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	if len(fields) == 0 || len(fields) > 2 {
		return "", false
	}

	if len(fields) == 1 {
		return parseRubyPositionSingle(fields[0])
	}

	return parseRubyPositionPair(fields)
}

// parseRubyPositionSingle normalizes a lone position keyword.
func parseRubyPositionSingle(field string) (string, bool) {
	switch field {
	case rubyPositionOver:
		return rubyPositionOver, true
	case rubyPositionUnder:
		return rubyPositionUnder, true
	case rubyPositionAlternate:
		return rubyPositionAlternate, true
	case rubyPositionInterCharacter:
		return rubyPositionInterCharacter, true
	default:
		return "", false
	}
}

// parseRubyPositionPair normalizes "alternate over|under" in either order,
// rejecting repeats and bare side pairs.
func parseRubyPositionPair(fields []string) (string, bool) {
	seenAlternate, seenSide := rubyPositionPairFlags(fields)

	if !seenAlternate || !seenSide {
		return "", false
	}

	for _, field := range fields {
		if field == rubyPositionUnder {
			return rubyPositionUnder, true
		}
	}

	return rubyPositionOver, true
}

// rubyPositionPairFlags scans a two-field position for the alternate marker
// and a side marker, rejecting repeats and unknown words.
func rubyPositionPairFlags(fields []string) (bool, bool) {
	seenAlternate, seenSide := false, false

	for _, field := range fields {
		switch field {
		case rubyPositionAlternate:
			if seenAlternate {
				return false, false
			}

			seenAlternate = true
		case rubyPositionOver, rubyPositionUnder:
			if seenSide {
				return false, false
			}

			seenSide = true
		default:
			return false, false
		}
	}

	return seenAlternate, seenSide
}

// rubyPositionIsUnder reports whether an interlinear annotation stacks below
// the base. Alternate alone behaves as over for the first level, so only a
// used value carrying under counts. Inter-character annotations sit to the
// side, never below.
func rubyPositionIsUnder(position string) bool {
	return strings.ToLower(strings.TrimSpace(position)) == rubyPositionUnder
}

// rubyPositionIsInterCharacter reports side placement (Taiwanese bopomofo
// style): the annotation sits to the right of the base in horizontal text,
// not stacked above or below.
func rubyPositionIsInterCharacter(position string) bool {
	return strings.ToLower(strings.TrimSpace(position)) == rubyPositionInterCharacter
}

// rubyCenterDivisor splits the leftover space evenly on both sides.
const rubyCenterDivisor = 2

// rubyAlignOffset returns the inline-axis offset of an annotation within its
// ruby column, in points. Column width is max(baseW, annotW) under separate
// sizing; the narrower side gets extra space distributed by ruby-align.
// When the annotation fills or overflows the column there is no extra space
// and every value returns 0. Start pins to the start edge; center and the two
// distribute values center a lone narrow annotation (for CJK text the
// distribute values would justify between characters, but a single short
// Latin annotation has no justification opportunity and centers, per the
// spec note on text-justify: ruby).
func rubyAlignOffset(baseW, annotW float64, align string) float64 {
	if annotW >= baseW {
		return 0
	}

	switch strings.ToLower(strings.TrimSpace(align)) {
	case rubyAlignStart:
		return 0
	default:
		return (baseW - annotW) / rubyCenterDivisor
	}
}

// rubyAnnotationStackOffset returns the block-axis offset from the base
// container top edge to the annotation container top edge, in points.
// Containers stack outward with no intervening space: over/alternate sits
// directly above (-annotH), under sits directly below (+baseH),
// inter-character sits beside the base (0 vertical offset).
func rubyAnnotationStackOffset(baseH, annotH float64, position string) float64 {
	pos := strings.ToLower(strings.TrimSpace(position))
	if pos == rubyPositionInterCharacter {
		return 0
	}

	if pos == rubyPositionUnder {
		return baseH
	}

	return -annotH
}

// rubyOverhangExpand returns how much wider (in points) a ruby segment must
// grow to avoid overlapping adjacent text. Auto allows overhang so nothing
// grows; spaces forbids it so the base side grows to fit a wider annotation.
func rubyOverhangExpand(baseW, annotW float64, overhang string) float64 {
	if strings.ToLower(strings.TrimSpace(overhang)) == rubyOverhangAuto {
		return 0
	}

	if annotW > baseW {
		return annotW - baseW
	}

	return 0
}

// rubyMergeSegmentWidth returns the total inline size of one ruby segment in
// points. Separate sizes each column to its widest member and sums them
// (mono ruby). Merge concatenates all annotations into one span, so the
// segment fits the wider of the whole base run and the whole annotation run
// (group ruby). Auto renders as separate when every annotation fits its base
// and as merge otherwise (the simplest jukugo algorithm named by the spec).
func rubyMergeSegmentWidth(baseWs, annotWs []float64, merge string) float64 {
	switch strings.ToLower(strings.TrimSpace(merge)) {
	case rubyMergeMerge:
		return rubyMergedSpanWidth(baseWs, annotWs)
	case rubyMergeAuto:
		if rubyAnnotationsFitBases(baseWs, annotWs) {
			return rubySeparateSegmentWidth(baseWs, annotWs)
		}

		return rubyMergedSpanWidth(baseWs, annotWs)
	default:
		return rubySeparateSegmentWidth(baseWs, annotWs)
	}
}

// rubySeparateSegmentWidth sums per-column maxima.
func rubySeparateSegmentWidth(baseWs, annotWs []float64) float64 {
	total := 0.0
	count := len(baseWs)

	if len(annotWs) < count {
		count = len(annotWs)
	}

	for i := range count {
		if annotWs[i] > baseWs[i] {
			total += annotWs[i]
		} else {
			total += baseWs[i]
		}
	}

	return total
}

// rubyMergedSpanWidth fits the wider of the summed runs.
func rubyMergedSpanWidth(baseWs, annotWs []float64) float64 {
	baseTotal, annotTotal := 0.0, 0.0

	for _, w := range baseWs {
		baseTotal += w
	}

	for _, w := range annotWs {
		annotTotal += w
	}

	if annotTotal > baseTotal {
		return annotTotal
	}

	return baseTotal
}

// rubyAnnotationsFitBases reports whether every annotation fits its base.
func rubyAnnotationsFitBases(baseWs, annotWs []float64) bool {
	count := len(baseWs)

	if len(annotWs) < count {
		count = len(annotWs)
	}

	for i := range count {
		if annotWs[i] > baseWs[i] {
			return false
		}
	}

	return true
}

// Cascade storage: canonical used values in CustomProps. CustomProps inherits
// through mergeCustomProps, so a ruby-position set on <ruby> reaches the <rt>
// without an inherit-table slot, and the style store interns the map without
// regenerating style_intern_gen.go.
const (
	rubyAlignPropName    = "ruby-align"
	rubyMergePropName    = "ruby-merge"
	rubyOverhangPropName = "ruby-overhang"
	rubyPositionPropName = "ruby-position"

	rubyAlignCustomKey    = "__ruby_align"
	rubyMergeCustomKey    = "__ruby_merge"
	rubyOverhangCustomKey = "__ruby_overhang"
	rubyPositionCustomKey = "__ruby_position"

	// rubyAnnotationScale shrinks annotation text to half the base size. The
	// old gap test named this ratio, and Chrome 143.0.7499.40 paints <rt>
	// smaller than its base by default.
	rubyAnnotationScale = 0.5

	// Ruby element names shared with the inline collector hook.
	rubyElementName = "ruby"
	rubyRTName      = "rt"
	rubyRTCName     = "rtc"
	rubyRPName      = "rp"
)

// applyRubyProps stores one ruby declaration as its canonical used value. It
// is registered in styleGroups (style_cascade.go) and returns false for any
// other property so dispatch keeps going.
func applyRubyProps(
	style *ResolvedStyle, prop, value string, _ float64, _ *styleContext,
	parent *ResolvedStyle, _ bool,
) bool {
	switch prop {
	case rubyAlignPropName:
		setRubyCustom(style, parent, rubyAlignCustomKey, value, parseRubyAlign)
	case rubyMergePropName:
		setRubyCustom(style, parent, rubyMergeCustomKey, value, parseRubyMerge)
	case rubyOverhangPropName:
		setRubyCustom(style, parent, rubyOverhangCustomKey, value, parseRubyOverhang)
	case rubyPositionPropName:
		setRubyCustom(style, parent, rubyPositionCustomKey, value, parseRubyPosition)
	default:
		return false
	}

	return true
}

// setRubyCustom validates value with parse and stores the canonical used
// value. CSS-wide keywords resolve first: inherit keeps the already-inherited
// map entry, everything else resets to the initial (deletes the key). An
// invalid value drops the declaration to the initial too, so a bogus
// ruby-merge never revives an earlier valid one.
func setRubyCustom(
	style *ResolvedStyle, parent *ResolvedStyle, key, value string, parse func(string) (string, bool),
) {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(trimmed) {
		applyRubyWideKeyword(style, parent, key, trimmed)

		return
	}

	used, ok := parse(value)
	if !ok {
		if style.CustomProps != nil {
			delete(style.CustomProps, key)
		}

		return
	}

	ensureRubyMap(style)
	style.CustomProps[key] = used
}

// applyRubyWideKeyword resolves a CSS-wide keyword for a ruby key. Inherit
// keeps the parent entry that mergeCustomProps already folded in (deleting
// only when the parent carries nothing, which is the initial anyway).
// Initial, unset, revert, and revert-layer all reset to the initial.
func applyRubyWideKeyword(style, parent *ResolvedStyle, key, keyword string) {
	if keyword == inheritKeyword {
		if parent == nil || parent.CustomProps[key] == "" {
			if style.CustomProps != nil {
				delete(style.CustomProps, key)
			}
		}

		return
	}

	if style.CustomProps != nil {
		delete(style.CustomProps, key)
	}
}

// ensureRubyMap allocates the CustomProps map for one ruby write.
func ensureRubyMap(style *ResolvedStyle) {
	if style.CustomProps == nil {
		style.CustomProps = make(map[string]string)
	}
}

// rubyCustomOr reads one stored used value, falling back to its initial.
func rubyCustomOr(style *ResolvedStyle, key, initial string) string {
	if style == nil || style.CustomProps == nil {
		return initial
	}

	if value := style.CustomProps[key]; value != "" {
		return value
	}

	return initial
}

// rubyAlignUsed returns the used ruby-align (initial space-around).
func rubyAlignUsed(style *ResolvedStyle) string {
	return rubyCustomOr(style, rubyAlignCustomKey, rubyAlignSpaceAround)
}

// rubyMergeUsed returns the used ruby-merge (initial separate).
func rubyMergeUsed(style *ResolvedStyle) string {
	return rubyCustomOr(style, rubyMergeCustomKey, rubyMergeSeparate)
}

// rubyOverhangUsed returns the used ruby-overhang (initial auto).
func rubyOverhangUsed(style *ResolvedStyle) string {
	return rubyCustomOr(style, rubyOverhangCustomKey, rubyOverhangAuto)
}

// rubyPositionUsed returns the used ruby-position (initial alternate).
func rubyPositionUsed(style *ResolvedStyle) string {
	return rubyCustomOr(style, rubyPositionCustomKey, rubyPositionAlternate)
}

// rubySegment is one base run plus the annotation run that stacks over it.
// Nodes are HTML tree nodes so collection keeps each node's own resolved
// style; rp fallback nodes never reach a segment (see rubySegments).
type rubySegment struct {
	base  []*html.Node
	annot []*html.Node
}

// rubyMeasured is one segment collected into inline items with content
// widths in scaled points.
type rubyMeasured struct {
	baseItems  []inlineItem
	annotItems []inlineItem
	baseW      float64
	annotW     float64
}

// isRubyAnnotNode reports annotation elements: rt and its rtc container.
// Anything else under <ruby> (rb, text, nested inlines) is base content, and
// rp fallback is dropped by the segmenter, never by this predicate.
func isRubyAnnotNode(node *html.Node) bool {
	if node == nil || node.Type != html.ElementNode {
		return false
	}

	return node.Name == rubyRTName || node.Name == rubyRTCName
}

// isRubySkippedNode reports direct <ruby> children that never become items:
// rp fallback parentheses plus inter-element whitespace. Whitespace inside
// rb/rt survives because it is collected through its own element, not here.
func isRubySkippedNode(node *html.Node) bool {
	if node == nil {
		return true
	}

	if node.Type == html.ElementNode {
		return node.Name == rubyRPName
	}

	if node.Type == html.TextNode {
		return strings.TrimSpace(node.Text) == ""
	}

	return true
}

// rubySegments pairs each annotation run with the base run before it. A base
// run after an annotation starts a new segment; an annotation with no base
// opens an empty-base segment. Returns nil when no segment carries annotation
// text, so the caller keeps the legacy flat path byte for byte.
func rubySegments(node *html.Node) []rubySegment {
	if node == nil {
		return nil
	}

	var segments []rubySegment

	current := -1

	for _, child := range node.Children {
		if isRubySkippedNode(child) {
			continue
		}

		current = appendRubyChild(&segments, current, child)
	}

	hasAnnot := false

	for _, segment := range segments {
		if len(segment.annot) > 0 {
			hasAnnot = true

			break
		}
	}

	if !hasAnnot {
		return nil
	}

	return segments
}

// appendRubyChild appends one non-skipped child to the open segment and
// returns the updated current index. An annotation run joins the open segment
// (opening an empty-base one when needed); base content after an annotation
// starts a new segment.
func appendRubyChild(segments *[]rubySegment, current int, child *html.Node) int {
	if isRubyAnnotNode(child) {
		if current < 0 {
			*segments = append(*segments, rubySegment{base: nil, annot: nil})
			current = len(*segments) - 1
		}

		(*segments)[current].annot = append((*segments)[current].annot, child)

		return current
	}

	if current < 0 || len((*segments)[current].annot) > 0 {
		*segments = append(*segments, rubySegment{base: nil, annot: nil})
		current = len(*segments) - 1
	}

	(*segments)[current].base = append((*segments)[current].base, child)

	return current
}

// collectRubyElement lays out one <ruby> element: base runs stay inline while
// each annotation run stacks above (or below for ruby-position:under) at half
// size. Annotation items pull back over their base with negative leading
// margins, so the pair advances one column width instead of two run widths.
func (e *engine) collectRubyElement(node *html.Node, sty ResolvedStyle, out *[]inlineItem) {
	segments := rubySegments(node)
	if len(segments) == 0 {
		e.collectInlineSpan(node, sty, out)

		return
	}

	measured := make([]rubyMeasured, 0, len(segments))

	for _, segment := range segments {
		measured = append(measured, e.measureRubySegment(segment))
	}

	if e.rubyMerges(measured, &sty) {
		e.appendMergedRuby(measured, &sty, out)

		return
	}

	for idx := range measured {
		e.appendSeparateRuby(&measured[idx], &sty, out)
	}
}

// measureRubySegment collects one segment's base and annotation items.
// Annotation items are scaled to half size (see scaleRubyAnnotItems).
func (e *engine) measureRubySegment(segment rubySegment) rubyMeasured {
	var measured rubyMeasured

	for _, child := range segment.base {
		e.collectInlineNode(child, &measured.baseItems)
	}

	for _, child := range segment.annot {
		e.collectInlineNode(child, &measured.annotItems)
	}

	measured.annotItems = e.scaleRubyAnnotItems(measured.annotItems)
	measured.baseW = rubyItemsWidth(measured.baseItems)
	measured.annotW = rubyItemsWidth(measured.annotItems)

	return measured
}

// rubyItemsWidth sums content widths in scaled points. Item margins stay out:
// the overlay math assigns the annotation margins, and base margins (rare
// inside ruby) ride along untouched.
func rubyItemsWidth(items []inlineItem) float64 {
	total := 0.0

	for idx := range items {
		total += items[idx].w
	}

	return total
}

// scaleRubyAnnotItems clones each annotation text item at half font size and
// re-measures it. Clones stay transient: they live only in this inline
// formatting context and never enter the interned style store. Non-text items
// (breaks, replaced content) pass through untouched.
func (e *engine) scaleRubyAnnotItems(items []inlineItem) []inlineItem {
	for idx := range items {
		item := &items[idx]
		if item.style == nil || item.text == "" || item.img || item.blockBox != nil || item.forceBreak {
			continue
		}

		scaled := *item.style
		scaled.FontSize *= rubyAnnotationScale

		if scaled.LineHeight > 0 {
			scaled.LineHeight *= rubyAnnotationScale
		}

		item.style = &scaled
		item.w = e.measureTextFace(item.text, &scaled)

		if item.chrome {
			item.w += e.inlineChromeLeft(&scaled) + e.inlineChromeRight(&scaled)
		}

		item.h = e.lineHeightOf(&scaled) * e.scale
		item.noSplit = true
	}

	return items
}

// rubyMerges reports whether a <ruby> lays out as one group segment.
// ruby-merge:merge always merges; auto merges when any annotation overflows
// its base (the jukugo rule inside rubyMergeSegmentWidth); separate never
// merges. Calling rubyMergeSegmentWidth wires the helper to the layout path.
func (e *engine) rubyMerges(measured []rubyMeasured, sty *ResolvedStyle) bool {
	_ = e

	baseWs := make([]float64, 0, len(measured))
	annotWs := make([]float64, 0, len(measured))

	for idx := range measured {
		baseWs = append(baseWs, measured[idx].baseW)
		annotWs = append(annotWs, measured[idx].annotW)
	}

	merge := rubyMergeUsed(sty)
	_ = rubyMergeSegmentWidth(baseWs, annotWs, merge)

	return merge == rubyMergeMerge || (merge == rubyMergeAuto && !rubyAnnotationsFitBases(baseWs, annotWs))
}

// appendSeparateRuby appends one mono-ruby column: bases inline, then the
// annotation pulled back over them. Column width is the base plus the
// overhang growth (auto spills, spaces contains); the annotation offset
// inside comes from ruby-align.
func (e *engine) appendSeparateRuby(measured *rubyMeasured, sty *ResolvedStyle, out *[]inlineItem) {
	_ = e

	*out = append(*out, measured.baseItems...)

	if len(measured.annotItems) == 0 {
		return
	}

	position := e.rubySegmentPosition(measured, sty)
	if rubyPositionIsInterCharacter(position) {
		// Side placement is out of scope: paint the half-size run inline
		// after the base instead of beside it vertically.
		*out = append(*out, measured.annotItems...)

		return
	}

	columnW := measured.baseW + rubyOverhangExpand(measured.baseW, measured.annotW, rubyOverhangUsed(sty))
	if columnW <= 0 && measured.annotW > 0 {
		// Orphan annotation with no base: size to the annotation so the
		// run still advances instead of collapsing under its follower.
		columnW = measured.annotW
	}

	offset := rubyAlignOffset(measured.baseW, measured.annotW, rubyAlignUsed(sty))
	e.stampRubyAnnot(measured.annotItems, measured.baseW, measured.annotW, columnW, offset, position)
	*out = append(*out, measured.annotItems...)
}

// appendMergedRuby appends a whole <ruby> as one group segment: every base in
// order, then every annotation concatenated over the run. The advance is the
// wider of the two runs (group ruby), so sharing space narrows the total
// against separate columns.
func (e *engine) appendMergedRuby(measured []rubyMeasured, sty *ResolvedStyle, out *[]inlineItem) {
	baseW, annotW := 0.0, 0.0
	baseWs := make([]float64, 0, len(measured))
	annotWs := make([]float64, 0, len(measured))

	var annotItems []inlineItem

	for idx := range measured {
		*out = append(*out, measured[idx].baseItems...)
		annotItems = append(annotItems, measured[idx].annotItems...)
		baseW += measured[idx].baseW
		annotW += measured[idx].annotW
		baseWs = append(baseWs, measured[idx].baseW)
		annotWs = append(annotWs, measured[idx].annotW)
	}

	if len(annotItems) == 0 {
		return
	}

	position := rubyPositionUsed(sty)
	if rubyPositionIsInterCharacter(position) {
		*out = append(*out, annotItems...)

		return
	}

	mergedW := rubyMergeSegmentWidth(baseWs, annotWs, rubyMergeUsed(sty))
	if grown := baseW + rubyOverhangExpand(baseW, annotW, rubyOverhangUsed(sty)); grown > mergedW {
		mergedW = grown
	}

	offset := rubyAlignOffset(baseW, annotW, rubyAlignUsed(sty))
	e.stampRubyAnnot(annotItems, baseW, annotW, mergedW, offset, position)
	*out = append(*out, annotItems...)
}

// rubySegmentPosition reads the annotation position for one segment from its
// first annotation node's style, so an rt-level ruby-position wins while an
// unset rt inherits the container through CustomProps.
func (e *engine) rubySegmentPosition(measured *rubyMeasured, sty *ResolvedStyle) string {
	_ = e

	for idx := range measured.annotItems {
		if measured.annotItems[idx].style != nil {
			return rubyPositionUsed(measured.annotItems[idx].style)
		}
	}

	return rubyPositionUsed(sty)
}

// stampRubyAnnot marks annotation items stacked and assigns the overlay
// margins: the first item pulls back over the base plus the align offset, the
// last pads the column remainder. The pair then advances columnW in total.
func (e *engine) stampRubyAnnot(
	items []inlineItem, baseW, annotW, columnW, offset float64, position string,
) {
	_ = e

	under := rubyPositionIsUnder(position)
	items[0].marginL += -baseW + offset
	items[len(items)-1].marginR += columnW - offset - annotW

	for idx := range items {
		items[idx].rubyAnnot = true
		items[idx].rubyUnder = under
	}
}

// rubyAnnotBaseline returns the baseline an annotation paints on: the top of
// the line box for over/alternate, the bottom for under. Containers stack
// with no gap (see rubyAnnotationStackOffset); the line box reserves the
// annotation height in lineMetrics, so top/bottom placement lands adjacent.
func (e *engine) rubyAnnotBaseline(item *inlineItem, lineY, lineH float64) float64 {
	ascent, descent := e.inlineFontMetrics(item.text, item.style)

	if ascent+descent <= 0 {
		size := item.style.FontSize * e.scale

		ascent = size * fallbackAscentRatio
		descent = size * fallbackDescentRatio
	}

	if item.rubyUnder {
		return lineY + lineH - descent
	}

	return lineY + ascent
}
