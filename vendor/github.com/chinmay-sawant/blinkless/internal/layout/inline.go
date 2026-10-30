package layout

import (
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

const (
	cssTagA                      = "a"
	cssTagBR                     = "br"
	cssTagImg                    = "img"
	cssDisplayInline             = "inline"
	cssDisplayInlineBlock        = "inline-block"
	cssDisplayNone               = "none"
	cssWhiteSpaceNowrap          = "nowrap"
	cssWhiteSpacePre             = "pre"
	cssWhiteSpacePreWrap         = "pre-wrap"
	cssWhiteSpacePreLine         = "pre-line"
	textJustifyNone              = "none"
	textJustifyInterCharacter    = "inter-character"
	writingModeHorizontalTB      = "horizontal-tb"
	cssTextAlignJustify          = "justify"
	cssVerticalAlignBottom       = "bottom"
	cssVerticalAlignMiddle       = "middle"
	cssVerticalAlignTop          = "top"
	cssTextDecorationLineThrough = "line-through"
	cssTextDecorationUnderline   = "underline"
	cssTextDecorationOverline    = "overline"
	writingModeVerticalRL        = "vertical-rl"
	writingModeVerticalLR        = "vertical-lr"
	nonASCIIStart                = 0x80

	minInlinePairLen    = 2
	glueBaseEmFactor    = 2.5
	glueNowrapEmFactor  = 8
	inlineFitEpsilon    = 0.01
	floatClearEpsilon   = 0.5
	tailGapBaseFactor   = 2.5
	tailGapNarrowFactor = 3.5
	inlineHalfDivisor   = 2
)

// inlineItem is one atomic piece of inline content.
type inlineItem struct {
	text    string
	style   *ResolvedStyle
	w, h    float64 // text: run width + line height; image: placed size
	marginL float64 // leading horizontal margin (e.g. span margin-left)
	marginR float64 // trailing horizontal margin
	marginT float64 // leading vertical margin (atomic margin box)
	marginB float64 // trailing vertical margin (atomic margin box)
	// marginBaseline marks an atomic whose baseline is its bottom margin edge
	// (replaced elements and empty or overflow-clipping inline-blocks): the
	// margin box then sits entirely above the baseline. When false the
	// baseline is approximated by the bottom border edge, so the bottom
	// margin hangs below it (CSS 2.1 §10.8.1).
	marginBaseline bool
	img            bool
	thumbImg       bool // img inside a collapsed wiki figure; outer frame owns L/R/T
	chrome         bool // text belongs to an inline element with its own decoration
	noSplit        bool // vertical writing-mode run must remain one rotated line
	// Wrap-break flags (css-text-4 lite, wrap_break.go): the first/last item
	// of an element with wrap-before/after: avoid carry the boundary flags,
	// and every item of a wrap-inside: avoid group that fits one line carries
	// avoidInside so the packer keeps the group together.
	avoidBefore bool
	avoidAfter  bool
	avoidInside bool
	// bidiScoped marks an item owned by a unicode-bidi scope (embed, isolate,
	// override, plaintext). The run-order heuristic and reverseInlineRange
	// leave scoped runs alone; the scope owner already ordered them.
	bidiScoped bool
	imgRef     *imageRef
	// svgBox is the retained box of an inline-level <svg>. The line that
	// paints the item attributes its raster op to this box, so boxForID finds
	// the element and RestyleSVGPaint can replace the payload.
	svgBox     *box
	alt        string
	href       string
	forceBreak bool
	// block-in-inline: a laid-out block box whose ops live in
	// e.ops[opStart:opEnd] and need relocating when placed on a line.
	blockBox *box
	opStart  int
	opEnd    int
	// rubyAnnot marks a ruby annotation run stacked above (rubyUnder false)
	// or below (rubyUnder true) its base at half size. The run paints on a
	// stacked baseline (see rubyAnnotBaseline) and reserves its height in
	// lineMetrics instead of joining the baseline row.
	rubyAnnot bool
	rubyUnder bool
}

func (e *engine) collectAndPrepareInlineItems(
	nodes []*html.Node, contentW float64, blockStyle *ResolvedStyle,
) []inlineItem {
	items := e.acquireInlineItems()

	oldMax := e.imgMaxW
	oldCB := e.inlineCBW

	if contentW > 0 {
		e.imgMaxW = contentW
		e.inlineCBW = contentW
	}

	e.collectInline(nodes, &items, blockStyle)
	e.imgMaxW = oldMax
	e.inlineCBW = oldCB

	if len(items) >= minInlinePairLen {
		items = squeezeInlineSpaces(items)
	}

	items = e.applyWhiteSpaceTrim(items, blockStyle)

	return items
}

// applyWhiteSpaceTrim discards block-edge whitespace for white-space-trim.
// discard-before trims the start, discard-after trims the end, discard-inner
// trims both (block-container subset). Leading and trailing force breaks
// (blank lines in pre) and whitespace-only runs are dropped; a surviving
// edge run keeps its ink but loses its leading or trailing spaces and tabs,
// with its measured width refreshed. Defaults ("", "none") return items
// untouched so existing fixtures do not move.
func (e *engine) applyWhiteSpaceTrim(items []inlineItem, blockStyle *ResolvedStyle) []inlineItem {
	if len(items) == 0 || blockStyle == nil {
		return items
	}

	trim := blockStyle.WhiteSpaceTrim
	if trim == "" {
		return items
	}

	wantBefore := whiteSpaceTrimHas(trim, "discard-before") || whiteSpaceTrimHas(trim, "discard-inner")
	wantAfter := whiteSpaceTrimHas(trim, "discard-after") || whiteSpaceTrimHas(trim, "discard-inner")

	if !wantBefore && !wantAfter {
		return items
	}

	if wantBefore {
		items = trimInlineLeading(items, e)
	}

	if wantAfter {
		items = trimInlineTrailing(items, e)
	}

	return items
}

// trimInlineLeading drops leading blank breaks and whitespace-only runs, then
// strips leading spaces and tabs from the first ink run.
func trimInlineLeading(items []inlineItem, eng *engine) []inlineItem {
	for len(items) > 0 {
		first := &items[0]
		if first.forceBreak {
			items = items[1:]

			continue
		}

		if first.img || first.blockBox != nil || first.style == nil {
			return items
		}

		if strings.TrimSpace(first.text) == "" {
			items = items[1:]

			continue
		}

		trimmed := strings.TrimLeft(first.text, " \t")
		if trimmed != first.text {
			first.text = trimmed
			first.w = eng.inlineTextWidth(trimmed, first.style, first.chrome)
		}

		return items
	}

	return items
}

// trimInlineTrailing drops trailing blank breaks and whitespace-only runs,
// then strips trailing spaces and tabs from the last ink run.
func trimInlineTrailing(items []inlineItem, eng *engine) []inlineItem {
	for len(items) > 0 {
		last := &items[len(items)-1]
		if last.forceBreak {
			items = items[:len(items)-1]

			continue
		}

		if last.img || last.blockBox != nil || last.style == nil {
			return items
		}

		if strings.TrimSpace(last.text) == "" {
			items = items[:len(items)-1]

			continue
		}

		trimmed := strings.TrimRight(last.text, " \t")
		if trimmed != last.text {
			last.text = trimmed
			last.w = eng.inlineTextWidth(trimmed, last.style, last.chrome)
		}

		return items
	}

	return items
}

func (e *engine) injectBlockPseudos(boxNode *box, items []inlineItem) []inlineItem {
	if boxNode == nil || boxNode.node == nil || boxNode.style == nil || boxNode.kind != boxKindBlock {
		return items
	}

	// No content rule anywhere means neither probe can produce an item.
	if !e.hasPseudoContent() {
		return items
	}

	if item, ok := e.makeInflowPseudoItem(boxNode.node, pseudoBefore, *boxNode.style); ok {
		items = append([]inlineItem{item}, items...)
	}

	if item, ok := e.makeInflowPseudoItem(boxNode.node, pseudoAfter, *boxNode.style); ok {
		items = append(items, item)
	}

	return items
}

func (e *engine) makeInflowPseudoItem(
	node *html.Node, pseudoEl string, host ResolvedStyle,
) (inlineItem, bool) {
	if src := e.pseudoContentURL(node, pseudoEl); src != "" {
		if ref := e.resolveImage(src); ref != nil && ref.data != nil {
			return e.inflowPseudoImage(node, pseudoEl, host, ref)
		}
	}

	txt := e.pseudoContent(node, pseudoEl)

	if txt == "" {
		return inlineItem{}, false //nolint:exhaustruct
	}

	pstyle := e.pseudoStyle(node, pseudoEl, host)
	if pstyle.Position == positionAbsolute || pstyle.Position == positionFixed {
		return inlineItem{}, false //nolint:exhaustruct
	}

	item := e.textItem(txt, pstyle)
	e.enableInlineChrome(&item)

	return item, true
}

func (e *engine) inflowPseudoImage(
	node *html.Node, pseudoEl string, host ResolvedStyle, ref *imageRef,
) (inlineItem, bool) {
	pstyle := e.pseudoStyle(node, pseudoEl, host)
	if pstyle.Position == positionAbsolute || pstyle.Position == positionFixed {
		return inlineItem{}, false //nolint:exhaustruct
	}

	imgW := e.scalePt(pxToPt(float64(ref.w)))
	imgH := e.scalePt(pxToPt(float64(ref.h)))

	if imgW <= 0 {
		imgW = e.scalePt(pstyle.FontSize)
	}

	if imgH <= 0 {
		imgH = e.scalePt(pstyle.FontSize)
	}

	return inlineItem{img: true, imgRef: ref, w: imgW, h: imgH, style: pstyle}, true //nolint:exhaustruct
}

// layoutInlineFloats lays out inline content into line boxes and emits
// text/image ops. It returns the consumed height and records the first line's
// baseline on the box. When floats is non-nil, each line re-queries exclusion
// at its canvas Y so text widens again after a float ends mid-paragraph.
//
//nolint:cyclop,gocognit,gocyclo,funlen,mnd,wsl,maintidx // hot path: per-line wrap against float exclusion zones
func (e *engine) layoutInlineFloats(
	boxNode *box, nodes []*html.Node, contentW, contentX, lineY float64,
	floats *floatState,
) float64 {
	var blockStyle *ResolvedStyle
	if boxNode != nil {
		blockStyle = boxNode.style
	}

	items := e.collectAndPrepareInlineItems(nodes, contentW, blockStyle)
	defer e.releaseInlineItems(items)

	items = e.injectBlockPseudos(boxNode, items)

	if len(items) == 0 {
		return 0
	}

	leftY := lineY
	lineCount := 0
	clampLimit := 0

	if boxNode != nil && boxNode.style != nil {
		if boxNode.style.LineClamp > 0 {
			clampLimit = boxNode.style.LineClamp
		} else if boxNode.style.MaxLines > 0 {
			clampLimit = boxNode.style.MaxLines
		}
	}

	// Drop-cap: size leading initial-letter run, paint it, exclude beside it.
	var localFloats floatState
	if letter, rest, hasIL := e.prepareInitialLetter(items, blockStyle); hasIL {
		if floats == nil {
			localFloats = newFloatState(contentX, contentW)
			floats = &localFloats
		}

		parentLH := e.surroundingLineHeight(blockStyle, letter[0].style) * e.scale
		e.placeInitialLetter(boxNode, letter, contentX, leftY, parentLH, floats)
		items = rest
		if len(items) == 0 {
			sink := initialLetterSinkLines(letter[0].style)
			if sink < 1 {
				sink = 1
			}

			return parentLH * float64(sink)
		}
	}

	// text-wrap-style: balance narrows the break width per forced-break
	// segment (Blink ParagraphLineBreaker bisection), computed lazily when a
	// segment starts. text-wrap-style: pretty narrows the break width per
	// segment to avoid a short last line, using prettyLineWidth.
	balanceEligible := balanceCanApply(floats, clampLimit, blockStyle)
	prettyEligible := prettyCanApply(floats, clampLimit, blockStyle)
	balanceW := 0.0
	prettyW := 0.0

	// shape-inside lite: the element's own content narrows to the inside
	// contour at each line Y before float exclusion runs (shape_inside.go).
	// nil when the element declared no usable shape: auto, a rejected token,
	// or a box without a definite content height.
	var inside *shapeExclusion

	if blockStyle != nil {
		inside = e.buildShapeInside(*blockStyle, boxNode, contentX, contentW)
	}

	idx := 0
	consecHyphenLines := 0

	for idx < len(items) {
		lineX, lineW := e.lineBounds(floats, inside, contentX, contentW, leftY)

		if idx == 0 && boxNode != nil && boxNode.style != nil && boxNode.style.TextIndent != 0 {
			indent := e.scalePt(boxNode.style.TextIndent)
			lineX += indent
			lineW -= indent

			if lineW < 0 {
				lineW = 0
			}
		}

		// Short remaining tail beside a float: if it fits as one full-width
		// line under the float, drop there instead of leaving an orphan in
		// the narrow column (e.g. wiki "big time."[71] left of a thumb).
		leftY, lineX, lineW = e.preferClearForTail(items, idx, lineX, lineW, contentX, contentW, leftY, floats, inside)

		if balanceEligible && (idx == 0 || items[idx-1].forceBreak) {
			balanceW = e.balanceSegmentWidth(items, idx, contentW)
		}

		if prettyEligible && (idx == 0 || items[idx-1].forceBreak) {
			prettyW = prettyLineWidth(items[idx:inlineSegmentEnd(items, idx)], contentW, pxToPt(1)*e.scale)
		}

		// Pack width: a balanced or pretty segment re-breaks at the narrowed
		// width, while alignment keeps the full line width (Chrome overrides
		// only the line breaker's available width).
		breakW := lineW
		if balanceW > 0 && breakW > balanceW {
			breakW = balanceW
		}

		if prettyW > 0 && breakW > prettyW {
			breakW = prettyW
		}

		// text-wrap-style: avoid-short-last-line narrows the break width per
		// segment to pull a word down from a short orphan tail, using
		// avoidShortLineWidth. Kept separate from the balance/pretty branches
		// above so those widths stay untouched.
		if avoidShortCanApply(floats, clampLimit, blockStyle) {
			segStart := idx
			for segStart > 0 && !items[segStart-1].forceBreak {
				segStart--
			}

			segEnd := inlineSegmentEnd(items, segStart)
			epsilon := pxToPt(1) * e.scale

			seg := items[segStart:segEnd]
			if w := avoidShortLineWidth(seg, contentW, epsilon); w > 0 && breakW > w {
				breakW = w
			}
		}

		// Pack one line under current exclusion width.
		start := idx
		tailW, _ := tailRemaining(items, start)
		lastLikely := tailW <= breakW+1e-6

		blockHyphen := true

		if blockStyle != nil {
			if hyphenateLimitLastBlocks(blockStyle, lastLikely) {
				blockHyphen = false
			}

			if blockStyle.HyphenateLimitLines >= 0 &&
				consecHyphenLines >= blockStyle.HyphenateLimitLines {
				blockHyphen = false
			}
		}

		nextIdx, packedX, packedW, packedY := e.packInlineLine(
			&items, start, lineX, breakW, leftY, contentX, contentW, floats, inside, blockHyphen,
		)
		idx = nextIdx
		leftY = packedY

		// A balanced or pretty segment keeps the full line width for
		// alignment; the packer can only narrow lineW for active floats,
		// which both modes already opted out of.
		if balanceW == 0 && prettyW == 0 {
			lineX, lineW = packedX, packedW
		}

		end := idx

		if idx < len(items) && items[idx].forceBreak {
			idx++ // consume br
		}

		if end == start {
			// Single unbreakable item wider than line — still emit it.
			if idx < len(items) && !items[idx].forceBreak {
				idx++
				end = idx
			} else {
				continue
			}
		}

		lineCount++
		lastLine := idx >= len(items) || (clampLimit > 0 && lineCount >= clampLimit)
		firstLine := lineCount == 1

		if clampLimit > 0 && lineCount >= clampLimit && idx < len(items) {
			if end > start && items[end-1].text != "" {
				items[end-1].text += "…"
			}
		}

		if lineEndsWithHyphen(items[start:end]) {
			consecHyphenLines++
		} else {
			consecHyphenLines = 0
		}

		leftY += e.emitLine(boxNode, items, start, end, lineW, lineX, leftY, lastLine, firstLine)

		if clampLimit > 0 && lineCount >= clampLimit {
			break
		}
	}

	return leftY - lineY
}

const maxPooledInlineItems = 256

// acquireInlineItems returns a reusable temporary item slice for one inline
// formatting context. Nested contexts consume separate entries from the same
// engine-local stack.
const initialInlineItemCapacity = 32

func (e *engine) acquireInlineItems() []inlineItem {
	if n := len(e.inlineItemPool); n > 0 {
		items := e.inlineItemPool[n-1]
		e.inlineItemPool = e.inlineItemPool[:n-1]

		return items[:0]
	}

	return make([]inlineItem, 0, initialInlineItemCapacity)
}

// releaseInlineItems returns a temporary item slice to the engine-local pool.
// Very large contexts are left for GC so one pathological line cannot pin a
// large backing array for the rest of the document.
func (e *engine) releaseInlineItems(items []inlineItem) {
	if cap(items) == 0 || cap(items) > maxPooledInlineItems {
		return
	}

	clear(items)
	e.inlineItemPool = append(e.inlineItemPool, items[:0])
}

// packInlineLine advances idx over the items that fit on one line under the
// current float exclusion, splitting overlong tokens as needed. It returns
// the next index and the updated line geometry (items may be replaced by the
// split, hence the pointer).
//
//nolint:cyclop,gocognit,nestif,funlen // line packing has separate split, fit, and float paths
func (e *engine) packInlineLine(
	items *[]inlineItem, start int, lineX, lineW, leftY float64,
	contentX, contentW float64, floats *floatState, inside *shapeExclusion, allowHyphen bool,
) (int, float64, float64, float64) {
	idx := start
	lineAdv := 0.0

	for idx < len(*items) {
		item := &(*items)[idx]
		if item.forceBreak {
			break
		}
		// Prefer an authored soft-hyphen break before emergency mid-word
		// overflow splits when the token does not fit the remaining width.
		remain := lineW - lineAdv
		if remain < 0 {
			remain = 0
		}

		// A wrap-inside: avoid group that fits the content width but not the
		// remaining space moves to the next line as one unit: break before its
		// first item. A group wider than the content width keeps the bounded
		// overflow behavior (glue within the group below).
		if lineAdv > 0 && item.avoidInside && !item.forceBreak &&
			wrapInsideGroupOpensAt(*items, idx) &&
			e.wrapInsideGroupFitsContent(*items, idx, contentW) &&
			!e.wrapInsideGroupFitsRemainder(*items, idx, remain) {
			break
		}

		if allowHyphen {
			if parts := e.maybeSplitHyphen(*item, remain, lineW); len(parts) > 1 {
				e.spliceInlineParts(items, idx, parts)
				item = &(*items)[idx]
			} else if parts := e.maybeSplitOverflow(*item, lineW, lineAdv, contentW); len(parts) > 1 {
				// Split long unbreakable runs (URLs, paths, base64) that would
				// overflow the line. Honors overflow-wrap / word-break; also
				// emergency-breaks when a token is wider than the line so text
				// does not paint past the page edge (print).
				e.spliceInlineParts(items, idx, parts)
				item = &(*items)[idx]
			}
		} else if parts := e.maybeSplitOverflow(*item, lineW, lineAdv, contentW); len(parts) > 1 {
			e.spliceInlineParts(items, idx, parts)
			item = &(*items)[idx]
		}

		adv := item.marginL + item.w + item.marginR
		// Always wrap to the next line when the next item does not fit.
		// white-space:nowrap must not glue an unbreakable span onto a line
		// that already has content and overflow into a float (wiki .IPA).
		// Exception: never break before attaching punctuation / mid-cite
		// (")[37]" → ")\n[" or "[\n37]" or "saying.[\n7]").
		if lineAdv > 0 && lineAdv+adv > lineW+1e-6 {
			// wrap-before/after: avoid and wrap-inside: avoid groups glue
			// across a soft wrap within a bounded slack (wrap_break.go).
			// A wrap-inside: avoid group that fits the content width but not
			// the remainder moves to the next line as a unit instead of
			// gluing past the content edge.
			glue := e.wrapBreakGlues(*items, idx, start, adv, lineW)

			if glue {
				lineAdv += adv
				idx++

				continue
			}
			// Last chance: SHY-split the current item into the remaining room.
			if allowHyphen {
				if parts := e.maybeSplitHyphen(*item, remain, lineW); len(parts) > 1 {
					e.spliceInlineParts(items, idx, parts)
					item = &(*items)[idx]
					adv = item.marginL + item.w + item.marginR

					if lineAdv+adv <= lineW+1e-6 {
						lineAdv += adv
						idx++

						continue
					}
				}
			}

			idx, _ = e.glueStickyTail(*items, idx, start, adv)

			break
		}
		// Empty line beside a float too narrow for this item: CSS2.1 §9.5
		// pushes the line box below the float and recomputes width.
		if lineAdv == 0 {
			nY, nX, nW, retry := e.clearFloatBelow(lineX, lineW, leftY, adv, contentX, contentW, floats, inside)
			leftY, lineX, lineW = nY, nX, nW

			if retry {
				continue
			}
		}

		lineAdv += adv
		idx++
	}

	return idx, lineX, lineW, leftY
}

// inlineItemsGrowFactor grows the inline item slice geometrically when a split
// needs more room, so repeated splits do not copy the slice every time.
const inlineItemsGrowFactor = 2

// spliceInlineParts replaces items[idx] with parts in place (grows the slice
// when needed). Shared by soft-hyphen and overflow mid-token splits.
func (e *engine) spliceInlineParts(items *[]inlineItem, idx int, parts []inlineItem) {
	oldLen := len(*items)
	extra := len(parts) - 1

	if need := oldLen + extra; need > cap(*items) {
		grownCap := cap(*items) * inlineItemsGrowFactor
		if grownCap < need {
			grownCap = need
		}

		grown := make([]inlineItem, oldLen, grownCap)
		copy(grown, *items)
		*items = grown
	}

	*items = (*items)[:oldLen+extra]

	copy((*items)[idx+len(parts):], (*items)[idx+1:oldLen])
	copy((*items)[idx:idx+len(parts)], parts)
}

// lineBounds returns the line origin and width under the inside contour and
// float exclusion at y. The element's own shape-inside interval (inside,
// shape_inside.go) narrows the line first; active floats then narrow it
// further, and a float with a shape-outside contour uses that contour's
// per-line interval instead of the rectangular margin box.
//
// floatState.exclusion reports the untouched content box when no float
// intrudes at y, so the intersection runs only when a float actually narrows
// the line. That keeps an inside span free to reach into its
// padding/border/margin reference box on float-free lines and keeps this
// path mathematically identical to plain float exclusion when inside is nil.
func (e *engine) lineBounds(
	floats *floatState, inside *shapeExclusion, contentX, contentW, lineY float64,
) (float64, float64) {
	if floats == nil && inside == nil {
		return contentX, contentW
	}

	lineX, lineW := insideSpanAt(inside, contentX, contentW, lineY)

	if floats != nil {
		exX, exW := floats.exclusion(contentX, contentW, 0, lineY)
		if exX != contentX || exW != contentW {
			lineX, lineW = intersectSpans(lineX, lineW, exX, exW)
		}
	}

	if lineW < 0 {
		lineW = 0
	}

	return lineX, lineW
}

// preferClearForTail drops the current line below active floats when the
// remaining tail fits one full-width line (see preferFloatClearForTail).
// Returns (lineY, lineX, lineW) to match the call site assignment order.
func (e *engine) preferClearForTail(
	items []inlineItem, idx int, lineX, lineW, contentX, contentW, lineY float64,
	floats *floatState, inside *shapeExclusion,
) (float64, float64, float64) {
	if floats == nil || lineW >= contentW-0.5 {
		return lineY, lineX, lineW
	}

	if next, ok := e.preferFloatClearForTail(items, idx, contentW, lineW, lineY, floats); ok {
		nextX, nextW := e.lineBounds(floats, inside, contentX, contentW, next)

		return next, nextX, nextW
	}

	return lineY, lineX, lineW
}

// maybeSplitOverflow splits an unbreakable text item that would overflow the
// current line; returns the replacement items, or nil when no split applies.
func (e *engine) maybeSplitOverflow(item inlineItem, lineW, lineAdv, contentW float64) []inlineItem {
	if item.img || item.blockBox != nil || item.text == "" {
		return nil
	}

	if item.noSplit {
		return nil
	}

	room := lineW - lineAdv
	if room < 0 {
		room = 0
	}

	restW := contentW
	if restW < lineW {
		restW = lineW
	}

	return e.breakOverflowItem(item, room, lineW, restW, lineAdv == 0)
}

// glueStickyTail advances idx across consecutive no-break items (cite
// clusters, IPA fragments) that may stick to the current line even when they
// overflow slightly; returns the new idx and accumulated line advance.
func (e *engine) glueStickyTail(items []inlineItem, idx, start int, adv float64) (int, float64) {
	if idx <= start || !noBreakBefore(items[idx-1], items[idx]) {
		return idx, adv
	}
	// Keep short sticky tails (cite ")", "[37]", commas) even if
	// they overflow slightly. Do NOT glue multi-em tokens
	// (bare URLs after "(") onto the line — that painted past
	// the page edge on reference lists.
	//
	// Nowrap-to-nowrap chains (wiki Ref cells "[127][128]") must
	// stay on one line even when the cell is a hair narrower
	// than the cluster; stacking at the same X was worse.
	emSize := items[idx].style.FontSize * e.scale
	if emSize < 1 {
		emSize = 10
	}

	if adv > glueLimit(items[idx-1], items[idx], emSize) {
		return idx, adv
	}

	return stickChain(items, idx+1, emSize, adv)
}

// glueLimit returns the max advance that may stick to the current line for
// the item pair: nowrap clusters (multi-cite / IPA fragments) may glue more.
func glueLimit(prev, cur inlineItem, emSize float64) float64 {
	limit := emSize * glueBaseEmFactor

	if isNowrapCluster(prev, cur) {
		limit = emSize * glueNowrapEmFactor // multi-cite / IPA fragments
	}

	return limit
}

// stickChain advances idx across consecutive no-break items whose advance
// stays within the pair's glue limit.
func stickChain(items []inlineItem, idx int, emSize, lineAdv float64) (int, float64) {
	for idx < len(items) && !items[idx].forceBreak && noBreakBefore(items[idx-1], items[idx]) {
		acc := items[idx].marginL + items[idx].w + items[idx].marginR
		if acc > glueLimit(items[idx-1], items[idx], emSize) {
			break
		}

		lineAdv += acc
		idx++
	}

	return idx, lineAdv
}

// isNowrapCluster reports that both items are white-space:nowrap runs that
// must stay together on one line.
func isNowrapCluster(a, b inlineItem) bool {
	return a.style.WhiteSpace == cssWhiteSpaceNowrap && b.style.WhiteSpace == cssWhiteSpaceNowrap
}

// clearFloatBelow drops the current (empty) line below active floats when it
// is too narrow for the next item; returns (lineY, lineX, lineW, retry) to
// match the call site assignment order.
func (e *engine) clearFloatBelow(
	lineX, lineW, lineY, adv, contentX, contentW float64,
	floats *floatState, inside *shapeExclusion,
) (float64, float64, float64, bool) {
	if floats == nil || adv <= lineW || lineW >= contentW-0.5 {
		return lineY, lineX, lineW, false
	}

	next := floats.clearY(lineY)
	if next <= lineY+0.5 {
		return lineY, lineX, lineW, false
	}

	nextX, nextW := e.lineBounds(floats, inside, contentX, contentW, next)
	// Retry this item at the new Y (do not advance i).
	if nextW >= contentW-0.5 || adv <= nextW {
		return next, nextX, nextW, true
	}

	// Still too narrow (e.g. both sides floated) — emit anyway.
	return next, nextX, nextW, false
}

// breakOverflowItem splits a text item that cannot fit in remainW on the
// current line (or in fullLineW alone). Returns nil when no split is needed
// or allowed; otherwise a sequence of items that replace the original.
// restLineW is the width used for chunks after the first (typically the BFC
// content width so fragments reflow full-width after a float ends).
//
// Policy (generic, not site-specific):
//   - word-break:break-all / overflow-wrap:anywhere → fill remainW, any grapheme
//   - overflow-wrap:break-word → soft then grapheme, but only when the token is
//     wider than a full line (CSS: do not mid-break a word that fits the next line)
//   - white-space:nowrap → never split
//   - otherwise emergency-split when the token alone exceeds fullLineW so
//     paint cannot run past the content box (print engines commonly do this)
func (e *engine) breakOverflowItem(
	item inlineItem, remainW, fullLineW, restLineW float64,
	aloneOnLine bool,
) []inlineItem {
	if item.img || item.blockBox != nil || item.forceBreak || item.text == "" {
		return nil
	}

	pol := wordBreakOf(item.style)
	if pol == breakNever {
		return nil
	}

	adv := item.marginL + item.w + item.marginR
	// Nothing to do when the whole item fits on this line.
	if adv <= remainW+inlineFitEpsilon {
		return nil
	}

	if !allowMidTokenBreak(pol, adv, fullLineW+inlineFitEpsilon, aloneOnLine) {
		return nil
	}

	firstRoom, restRoom, ok := breakRooms(remainW, fullLineW, restLineW, item.marginL, item.marginR, aloneOnLine)
	if !ok {
		return nil
	}

	chunks := e.breakToken(item.text, *item.style, firstRoom, restRoom)
	if len(chunks) <= 1 {
		return nil
	}

	return e.chunkParts(item, chunks)
}

// allowMidTokenBreak applies the CSS wrapping policy to decide whether the
// token may be split mid-word on the current line instead of wrapping whole.
func allowMidTokenBreak(pol wordBreakPolicy, adv, fullLineW float64, aloneOnLine bool) bool {
	if pol == breakKeepAll {
		return false
	}

	tokenExceedsLine := adv > fullLineW
	// Mid-line: a normal / break-word token that fits a full next line must
	// wrap whole — not mid-break into a tight remainW (captions: "International").
	if pol != breakAll && !tokenExceedsLine {
		return false
	}
	// Emergency path (overflow-wrap:normal): only when alone on the line and
	// the token is wider than that line.
	if pol == breakNormal {
		return aloneOnLine && tokenExceedsLine
	}

	return true
}

// breakRooms computes the usable widths for the first and subsequent chunks
// of a split token, or reports that splitting is not viable.
func breakRooms(
	remainW, fullLineW, restLineW, marginL, marginR float64,
	aloneOnLine bool,
) (float64, float64, bool) {
	// Width available for the first chunk of text (exclude leading margin once).
	firstRoom := remainW - marginL
	if firstRoom < 1 {
		// No room on this line — let the outer packer wrap to the next line
		// unless we are already alone (then force a progressive split).
		if !aloneOnLine {
			return 0, 0, false
		}

		firstRoom = fullLineW - marginL - marginR
	}

	if firstRoom < 1 {
		firstRoom = fullLineW
	}

	if firstRoom < 1 {
		return 0, 0, false
	}

	if restLineW < fullLineW {
		restLineW = fullLineW
	}

	restRoom := restLineW - marginL - marginR
	if restRoom < 1 {
		restRoom = restLineW
	}

	if restRoom < 1 {
		return 0, 0, false
	}

	return firstRoom, restRoom, true
}

// chunkParts builds the replacement items for a split token, zeroing the
// margins on fragments that are not at the token's outer edge.
func (e *engine) chunkParts(item inlineItem, chunks []string) []inlineItem {
	out := make([]inlineItem, 0, len(chunks))

	for idx, chunk := range chunks {
		part := item
		part.text = chunk
		part.w = e.inlineTextWidth(chunk, item.style, item.chrome)

		if idx > 0 {
			part.marginL = 0
		}

		if idx < len(chunks)-1 {
			part.marginR = 0
		}

		out = append(out, part)
	}

	return out
}

// breakToken splits s into chunks that each fit firstMax (first piece) then
// restMax under wordBreakOf(st). Shared by inline overflow packing; soft-mode
// selection lives only here (and softModeOf) so measure and pack cannot drift.
func (e *engine) breakToken(cssSheet string, sty ResolvedStyle, firstMax, restMax float64) []string {
	if cssSheet == "" {
		return nil
	}

	pol := wordBreakOf(&sty)
	if pol == breakNever {
		return []string{cssSheet}
	}

	return e.splitTextToWidth(cssSheet, &sty, firstMax, restMax, softModeOf(pol))
}

// preferFloatClearForTail reports whether remaining inline content from i
// should drop below active floats so the last line(s) use full content width
// instead of sitting as a short orphan beside the float.
func (e *engine) preferFloatClearForTail(
	items []inlineItem, idx int, contentW, lineW, lineY float64,
	floats *floatState,
) (float64, bool) {
	if floats == nil || idx >= len(items) {
		return lineY, false
	}
	// First line of the IFC: keep short content beside the float (CSS2.1 /
	// Chrome). Orphan-tail reclaim only applies after earlier lines already
	// packed beside the float (wiki "big time."[71]).
	if idx == 0 {
		return lineY, false
	}

	next := floats.clearY(lineY)

	if next-lineY <= floatClearEpsilon {
		return lineY, false
	}

	rem, estLH := tailRemaining(items, idx)
	if rem <= 0 {
		return lineY, false
	}

	if estLH < 1 {
		estLH = 12
	}
	// Only when the whole remaining tail fits one full-width line (true
	// short ending), not when a long paragraph still has room to wrap
	// usefully beside the float.
	if rem > contentW+1 {
		return lineY, false
	}
	// Near the float bottom, pull the last full-width-fitting chunk under the
	// float so we do not leave "…destined for the" beside + orphan
	// "big time."[71] under. Allow a slightly larger jump when the tail would
	// need multiple narrow lines but only one full-width line.
	maxGap := estLH * tailGapBaseFactor
	if rem > lineW+inlineFitEpsilon {
		maxGap = estLH * tailGapNarrowFactor
	}

	if next-lineY <= maxGap {
		return next, true
	}

	return lineY, false
}

// tailRemaining sums the advance of items[i:] up to the next hard break and
// estimates the line height of the tail.
func tailRemaining(items []inlineItem, i int) (float64, float64) {
	rem := 0.0
	estLH := 0.0

	for j := i; j < len(items); j++ {
		item := items[j]
		if item.forceBreak {
			// Hard break ends the tail we consider for a single-line clear.
			break
		}

		rem += item.marginL + item.w + item.marginR

		if estLH <= 0 && item.h > 0 {
			estLH = item.h
		}
	}

	return rem, estLH
}

// softBreakMode selects where splitTextToWidth may insert breaks inside a token.
//
// Intent: softBreakNone is intentionally the zero value. It is the narrowest
// mode (grapheme-only splits), so an uninitialized mode never introduces
// emergency URL/hyphen breaks a caller did not ask for; the only producer
// (softModeOf) returns an explicit member.
type softBreakMode int

const (
	softBreakNone softBreakMode = iota // grapheme only (word-break:break-all)
	softBreakURL                       // emergency: / ? & = # % : etc. (not hyphen)
	softBreakWord                      // overflow-wrap:break-word: URL + hyphen + underscore
)

// splitTextToWidth breaks s into substrings that each fit max widths.
// firstMax is for the first chunk (remaining space on the current line);
// restMax is for subsequent chunks (full line content width).
//
//nolint:cyclop // multi-chunk text width splitting
func (e *engine) splitTextToWidth(
	text string, style *ResolvedStyle, firstMax, restMax float64, mode softBreakMode,
) []string {
	if text == "" || style == nil {
		return nil
	}

	runes := []rune(text)

	var chunks []string

	for len(runes) > 0 {
		limit := restMax
		if len(chunks) == 0 && firstMax > 0 {
			limit = firstMax
		}

		if limit < 1 {
			limit = restMax
		}
		// Find the longest prefix that fits limit.
		node := e.fittingPrefix(runes, limit, style)
		// Prefer a soft wrap opportunity near the end of the fitting prefix
		// so URLs break after "/" etc. rather than mid-token when possible.
		if mode != softBreakNone && node < len(runes) && node > 1 {
			if soft := lastSoftBreak(runes[:node], mode); soft > 0 {
				node = soft
			}
		}

		chunks = append(chunks, string(runes[:node]))
		runes = runes[node:]
	}

	return chunks
}

// fittingPrefix returns the rune count of the longest prefix of runes that
// fits within limit using the style's font-size. Takes at least 1 rune so
// we always make progress.
func (e *engine) fittingPrefix(runes []rune, limit float64, style *ResolvedStyle) int {
	if style == nil {
		return 1
	}

	node := 0
	width := 0.0

	for node < len(runes) {
		// measureRuneFace avoids per-rune string(r) allocations.
		rowW := e.measureRuneFace(runes[node], style)
		if node > 0 && width+rowW > limit+0.01 {
			break
		}
		// Always take at least one rune so we make progress.
		if node == 0 && rowW > limit+0.01 {
			return 1
		}

		width += rowW
		node++
	}

	if node <= 0 {
		return 1
	}

	return node
}

// lastSoftBreak returns a split index after a soft-wrap character near the
// end of runes, or 0 if none is suitable (keep at least 1 rune on each side).
func lastSoftBreak(runes []rune, mode softBreakMode) int {
	for i := len(runes) - 1; i >= 1; i-- {
		if isSoftWrapRune(runes[i-1], mode) {
			return i
		}
	}

	return 0
}

func isSoftWrapRune(r rune, mode softBreakMode) bool {
	switch r {
	case '/', '\\', '?', '&', '=', '%', '#', ':', ';', ',', '+', '~', '@',
		'!', '*', '(', ')', '[', ']', '{', '}':
		return true
	case '-', '_', '.':
		// Hyphen/underscore/dot: only for overflow-wrap soft breaks, not pure
		// emergency (keeps "inner-a" / file names intact when slightly tight).
		return mode == softBreakWord
	}

	return false
}

func hidesPaint(style *ResolvedStyle) bool {
	if style == nil {
		return false
	}

	switch style.Visibility {
	case overflowHidden, borderCollapseValue:
		return true
	default:
		return false
	}
}

// emitLine renders items[start:end) as one line and returns its height.
// lastLine is true for the final line of the inline formatting context (used
// so text-align:justify leaves the last line start-aligned). firstLine gates
// text-box-trim on the block's first line box.
//
//nolint:cyclop // line emission and alignment dispatch
func (e *engine) emitLine( //nolint:funlen
	boxNode *box, items []inlineItem, start, end int,
	availW, startX, lineY float64, lastLine, firstLine bool,
) float64 {
	line := items[start:end]
	if len(line) == 0 {
		return 0
	}

	// trim trailing whitespace of the last run
	e.trimTrailingSpace(line)

	var blockStyle *ResolvedStyle
	if boxNode != nil {
		blockStyle = boxNode.style
	}

	textAlign := floatLeft

	if blockStyle != nil {
		if blockStyle.Direction == cssDirectionRTL {
			textAlign = "right"
		}

		if blockStyle.TextAlign != "" {
			textAlign = blockStyle.TextAlign
		}
	}

	if lastLine && blockStyle != nil &&
		blockStyle.TextAlignLast != "" && blockStyle.TextAlignLast != "auto" {
		textAlign = blockStyle.TextAlignLast
	}

	textAlign = resolveTextGroupAlign(blockStyle, textAlign)

	// text-justify:none disables justification even when text-align asks for
	// it (Chrome 143 justifies nothing and start-aligns the line).
	if textAlign == cssTextAlignJustify && textJustifyMode(blockStyle) == textJustifyNone {
		if blockStyle != nil && blockStyle.Direction == cssDirectionRTL {
			textAlign = floatRight
		} else {
			textAlign = floatLeft
		}
	}

	// A vertical-rl block advances columns right-to-left, so a single column
	// anchors at the content box's right edge. text-align along the vertical
	// axis is not implemented; justify keeps the shared path.
	if blockStyle != nil &&
		blockStyle.WritingMode == writingModeVerticalRL && textAlign != cssTextAlignJustify {
		textAlign = floatRight
	}

	// Coalesce adjacent same-style text runs into one op so text and image
	// paint advances match layout (avoids word-by-word gaps). Skip when
	// justifying — gaps are distributed between word items. Legacy
	// -webkit-box keeps items separate so pack backgrounds stay distinct.
	if textAlign != cssTextAlignJustify && (blockStyle == nil || !blockStyle.IsWebkitBox) {
		line = e.coalesceTextItems(line)
	}

	trimStart, trimEnd := textBoxTrimFlags(blockStyle, firstLine, lastLine)
	lineH, baseline := e.lineMetrics(line, lineY, blockStyle, trimStart, trimEnd)

	totalW := 0.0
	for i := range line {
		totalW += line[i].marginL + line[i].w + line[i].marginR
	}

	// text-justify:inter-character distributes the slack between every
	// character as extra letter spacing (Chrome 143 fills the line even
	// without word separators). The expansion updates the cloned item
	// styles and widths in place; the later word-gap path then sees no
	// remaining slack for this mode.
	if textAlign == cssTextAlignJustify && textJustifyMode(blockStyle) == textJustifyInterCharacter {
		totalW = e.applyInterCharJustify(line, availW, totalW, lastLine, blockStyle)
	}

	hang := 0.0
	trimLead := 0.0

	if len(line) > 0 && line[0].style != nil && line[0].text != "" {
		hang = e.hangingPunctuationFirstWidth(line[0].style, line[0].text)
		trimLead = e.textSpacingTrimLeading(line[0].style, line[0].text)
	}

	alignW := totalW - hang - trimLead
	if alignW < 0 {
		alignW = 0
	}

	leftX, justifyGap := e.lineOriginAndGap(textAlign, startX, availW, alignW, line, lastLine, blockStyle)
	leftX -= hang + trimLead

	e.emitLineItems(boxNode, line, leftX, baseline, lineH, lineY, justifyGap)

	if boxNode != nil && boxNode.firstBaseline == 0 {
		boxNode.firstBaseline = baseline
	}

	return lineH
}

// emitLineItems paints each item of a line at the given baseline, flushing
// the accumulated underline run when the styling changes.
//
//nolint:cyclop,wsl // blend scope restoration belongs immediately after item emission
func (e *engine) emitLineItems(boxNode *box, line []inlineItem, leftX, baseline, lineH, lineY, justifyGap float64) {
	var und undRun

	for idx := range line {
		item := &line[idx]
		if item.forceBreak || item.style == nil {
			continue
		}

		leftX += item.marginL

		if hidesPaint(item.style) {
			und.flush(e)

			leftX += item.w + item.marginR

			if idx < len(line)-1 && isJustifyGapAfter(*item) {
				leftX += justifyGap
			}

			continue
		}

		itemX := leftX
		blendScope := e.pushInlineBlend(item.style)
		switch {
		case item.blockBox != nil:
			leftX = e.emitInlineBlock(
				boxNode, item, leftX, lineY, lineH, baseline, justifyGap, idx < len(line)-1, &und,
			)
		case item.img:
			leftX = e.emitInlineImage(
				boxNode, item, leftX, lineY, lineH, baseline, justifyGap, idx < len(line)-1, &und,
			)
		default:
			// Ruby annotations paint on their stacked baseline (top of the
			// line box when over, bottom when under) instead of the base
			// baseline. Horizontal overlay already rode in on the collector
			// margins, so only the baseline changes here.
			paintBaseline := baseline
			if item.rubyAnnot {
				paintBaseline = e.rubyAnnotBaseline(item, lineY, lineH)
			}

			leftX = e.emitInlineText(item, leftX, paintBaseline, justifyGap, idx < len(line)-1, &und)
		}
		e.popInlineBlend(&blendScope)

		if blendScope.markStart >= 0 {
			e.patchGroupMark(blendScope.markStart, itemX, lineY, item.w, lineH)
			e.patchGroupMark(blendScope.endMark, itemX, lineY, item.w, lineH)
		}
	}

	und.flush(e)
}

// inlineBlendScope captures one inline item's enclosing compositing state.
type inlineBlendScope struct {
	prevBlend string
	prevGroup *BlendGroup
	prevOwner *ResolvedStyle
	markStart int
	endMark   int
}

// pushInlineBlend enters the compositing scope of one inline item. Inline
// items do not get their own box-layout pushZ call, so masks, isolation, and
// mix-blend-mode must be scoped while their text and decorations are emitted.
// popInlineBlend restores the previous blend mode, group, and owner.
func (e *engine) pushInlineBlend(style *ResolvedStyle) inlineBlendScope {
	scope := inlineBlendScope{
		prevBlend: e.blendMode,
		prevGroup: e.blendGroup,
		prevOwner: e.blendGroupOwner,
		markStart: -1,
		endMark:   -1,
	}
	if style == nil {
		return scope
	}

	// The block's own group already owns its inherited inline runs; opening a
	// second group for the same element would emit a duplicate nested form.
	if e.blendGroup != nil && e.blendGroupOwner == style {
		return scope
	}

	mode := nonNormalBlendMode(style.MixBlendMode)

	hasMask := maskGroupRequested(*style)
	if mode == "" && style.Isolation != unicodeBidiIsolate && !hasMask {
		return scope
	}

	e.nextGroupID++

	e.blendGroup = &BlendGroup{ //nolint:exhaustruct // mask frames are attached below
		ID:      e.nextGroupID,
		Mode:    mode,
		Isolate: true,
		Parent:  e.blendGroup,
		Mask:    nil,
	}
	if hasMask {
		e.attachMaskDescriptors(e.blendGroup, *style)
	}

	e.blendGroupOwner = style

	e.applyBlendMode(style, mode)

	scope.markStart = e.addGroupMark(e.blendGroup, groupMarkBegin)

	return scope
}

func nonNormalBlendMode(mode string) string {
	if mode == blendNormal {
		return ""
	}

	return mode
}

func (e *engine) applyBlendMode(style *ResolvedStyle, mode string) {
	if style.Isolation == unicodeBidiIsolate {
		e.blendMode = ""
	}

	if mode != "" {
		e.blendMode = mode
	}
}

// popInlineBlend closes the inline item's group (balanced markers) and
// restores the enclosing scope.
func (e *engine) popInlineBlend(scope *inlineBlendScope) {
	if scope.markStart >= 0 {
		scope.endMark = e.addGroupMark(e.blendGroup, groupMarkEnd)
	}

	e.blendMode = scope.prevBlend
	e.blendGroup = scope.prevGroup
	e.blendGroupOwner = scope.prevOwner
}

// trimTrailingSpace drops trailing whitespace from the last run of a line.
// Only ASCII spaces are trimmed; each one contributes exactly its per-rune
// measured width (advance + letter-spacing), so the width is adjusted by
// subtraction instead of re-measuring the whole run.
func (e *engine) trimTrailingSpace(line []inlineItem) {
	if len(line) == 0 || line[len(line)-1].img {
		return
	}

	last := &line[len(line)-1]
	trimmed := strings.TrimRight(last.text, " ")

	if trimmed != last.text {
		spaceCount := len(last.text) - len(trimmed)
		last.text = trimmed
		last.w -= float64(spaceCount) * e.measureRuneFace(' ', last.style)
	}
}

// lineMetrics returns the height of a line and the Y of its baseline.
// block + trimStart/trimEnd apply CSS text-box-trim / text-box-edge on the
// first and last line of a block container.
//
//nolint:cyclop // line metrics combine inline item classes and chrome
func (e *engine) lineMetrics( //nolint:funlen
	line []inlineItem, lineY float64, block *ResolvedStyle, trimStart, trimEnd bool,
) (float64, float64) {
	maxAscent, maxDescent := 0.0, 0.0
	rubyOverH, rubyUnderH := 0.0, 0.0
	edgeStyle := block

	// Every line box carries the block's strut: a zero-width inline box with
	// the block's font and line-height (CSS 2.1 §10.8.1). It is what keeps an
	// empty inline-block on the baseline and what gives an atomic-only line
	// its descent below the atomic margin box.
	if block != nil {
		maxAscent, maxDescent = e.strutMetrics(block, trimStart, trimEnd)
	}

	for i := range line {
		item := &line[i]
		if item.forceBreak || item.style == nil {
			continue
		}

		if item.img || item.blockBox != nil {
			ascent, descent := e.atomicInlineAlign(item)
			if ascent > maxAscent {
				maxAscent = ascent
			}

			if descent > maxDescent {
				maxDescent = descent
			}

			continue
		}

		// Ruby annotations stack outside the base row: reserve their height
		// above or below instead of joining the baseline max. Inter-character
		// runs never carry the flag and keep the normal path.
		if rubyStackReserve(item, &rubyOverH, &rubyUnderH) {
			continue
		}

		ascent, descent := e.inlineFontMetrics(item.text, item.style)
		lh := e.lineHeightOf(item.style) * e.scale

		// Negative half-leading is legal: an explicit line-height smaller
		// than the glyphs shrinks the line box and lets them overflow
		// (Chrome 143 on case-30's line-height:1 items).
		extra := (lh - ascent - descent) / inlineHalfDivisor

		face := e.faceFor(item.style)
		metricStyle := item.style

		if edgeStyle != nil && metricStyle.TextBoxEdgeOver == "auto" &&
			metricStyle.TextBoxEdgeUnder == "auto" {
			// Inherit edge from the block when the inline kept initials.
			metricStyle = edgeStyle
		}

		itemAscent, itemDescent := e.adjustTextBoxMetrics(
			metricStyle, face, item.style.FontSize*e.scale,
			ascent, descent, extra, trimStart, trimEnd,
		)

		if item.chrome {
			itemAscent += e.inlineChromeTop(item.style)
			itemDescent += e.inlineChromeBottom(item.style)
		}

		if itemAscent > maxAscent {
			maxAscent = itemAscent
		}

		if itemDescent > maxDescent {
			maxDescent = itemDescent
		}
	}

	lineH := maxAscent + maxDescent + rubyOverH + rubyUnderH
	if lineH <= 0 {
		lineH = 1
	}

	return lineH, lineY + maxAscent + rubyOverH
}

// rubyStackReserve accumulates a stacked ruby annotation item's height above
// or below the base row. It reports whether the item was a stacked
// annotation; normal items (including inter-character runs, which never carry
// the flag) return false so the caller joins the baseline max.
func rubyStackReserve(item *inlineItem, overH, underH *float64) bool {
	if !item.rubyAnnot {
		return false
	}

	if item.rubyUnder {
		if item.h > *underH {
			*underH = item.h
		}
	} else if item.h > *overH {
		*overH = item.h
	}

	return true
}

// strutMetrics is the block's line strut (ascent, descent): a zero-width
// inline box with the block's own font and line-height that participates in
// every line box (CSS 2.1 §10.8.1). text-box-trim applies to it like any
// inline box, and negative half-leading is legal for an explicit line-height
// smaller than the glyphs.
func (e *engine) strutMetrics(block *ResolvedStyle, trimStart, trimEnd bool) (float64, float64) {
	ascent, descent := e.inlineFontMetrics("", block)
	lh := e.lineHeightOf(block) * e.scale
	extra := (lh - ascent - descent) / inlineHalfDivisor

	return e.adjustTextBoxMetrics(block, e.faceFor(block), block.FontSize*e.scale,
		ascent, descent, extra, trimStart, trimEnd)
}

// atomicInlineAlign is the ascent/descent a replaced or inline-block item
// contributes to the line. The item's margin box is what sits on the line
// (CSS 2.1 §10.8.1). A bottom-margin-edge baseline (replaced elements and
// empty or overflow-clipping inline-blocks) puts the whole margin box above
// the baseline; otherwise the baseline is approximated by the bottom border
// edge and the bottom margin hangs below. A length vertical-align raises
// (positive) or lowers (negative) the box relative to the baseline.
func (e *engine) atomicInlineAlign(item *inlineItem) (float64, float64) {
	if item.style != nil {
		switch item.style.VerticalAlign {
		case cssVerticalAlignTop, cssVerticalAlignMiddle, cssVerticalAlignBottom:
			return item.marginT + item.h + item.marginB, 0
		}
	}

	ascent := item.marginT + item.h
	descent := item.marginB

	if item.marginBaseline {
		ascent += item.marginB
		descent = 0
	}

	shift := 0.0
	if item.style != nil {
		shift = e.scalePt(item.style.VerticalAlignShift)
	}

	ascent += shift
	descent -= shift

	if ascent < 0 {
		descent -= ascent
		ascent = 0
	}

	if descent < 0 {
		ascent -= descent
		descent = 0
	}

	return ascent, descent
}

// lineOriginAndGap returns the x where the line content starts and the extra
// inter-word space added by text-align:justify (0 unless justifying).
func (e *engine) lineOriginAndGap(
	textAlign string, originX, availW, totalW float64,
	line []inlineItem, lastLine bool, blockStyle *ResolvedStyle,
) (float64, float64) {
	switch textAlign {
	case floatRight:
		return originX + availW - totalW, 0
	case fxCenter:
		return originX + (availW-totalW)/2, 0
	case cssTextAlignJustify:
		return originX, e.justifyGapOf(line, availW, totalW, lastLine, blockStyle)
	default:
		return originX, 0
	}
}

// justifyGapOf computes the extra space added after each inter-word gap when
// text-align:justify stretches a non-last line.
// CSS justify expands inter-word spaces only — not every inline box
// boundary. Expanding after every item put rivers before commas, cites
// ("word [1]"), and apostrophes ("Roth 's") on wiki print pages.
// text-justify:none disables the expansion and text-justify:inter-character
// expands inside the runs instead, so both return no word gap here.
//
//nolint:cyclop // text justification spacing
func (e *engine) justifyGapOf(
	line []inlineItem, availW, totalW float64, lastLine bool, blockStyle *ResolvedStyle,
) float64 {
	mode := textJustifyMode(blockStyle)
	if mode == textJustifyNone || mode == textJustifyInterCharacter {
		return 0
	}

	allowLastLine := lastLine && len(line) > 0 &&
		line[0].style != nil && line[0].style.TextAlignLast == cssTextAlignJustify

	if (lastLine && !allowLastLine) || availW <= totalW || len(line) <= 1 {
		return 0
	}

	gaps := 0

	for i := range len(line) - 1 {
		if isJustifyGapAfter(line[i]) {
			gaps++
		}
	}

	if gaps == 0 {
		return 0
	}

	raw := (availW - totalW) / float64(gaps)

	maxGap := e.justifyMaxGap(line)

	if raw > maxGap*2 {
		return 0
	}

	if raw > maxGap {
		return maxGap // soft-cap rivers without abandoning justify
	}

	return raw
}

// justifyMaxGap returns the largest inter-word gap allowed for the line,
// up to 1em of the largest font size present.
func (e *engine) justifyMaxGap(line []inlineItem) float64 {
	maxGap := 6.0 // pt

	for i := range line {
		if line[i].style == nil {
			continue
		}

		if fs := line[i].style.FontSize * e.scale; fs > maxGap {
			maxGap = fs // up to 1em extra between words
		}
	}

	return maxGap
}

// applyInterCharJustify spreads the justification slack between every
// character of a justified line as extra letter spacing and returns the
// updated line width. The extra spacing rides on cloned item styles so the
// emitted OpText ops carry wider advances; the word-gap path stays at zero
// for this mode. Lines that must not justify (last line without
// text-align-last:justify, already-full lines) and overfull expansions are
// left untouched.
func (e *engine) applyInterCharJustify(
	line []inlineItem, availW, totalW float64, lastLine bool, blockStyle *ResolvedStyle,
) float64 {
	if !justifyLineEligible(lastLine, line, blockStyle) || availW <= totalW || len(line) == 0 {
		return totalW
	}

	counts, totalRunes := countLineSpacingRunes(line)

	if totalRunes == 0 {
		return totalW
	}

	perChar, ok := capInterCharGap(availW-totalW, totalRunes, e.justifyMaxGap(line))
	if !ok {
		return totalW
	}

	delta := perChar
	if e.scale > 0 {
		delta = perChar / e.scale
	}

	spreadInterCharSpacing(line, counts, perChar, delta)

	return totalW + perChar*float64(totalRunes)
}

// justifyLineEligible reports whether a line may justify: non-last lines
// always, last lines only with text-align-last:justify.
func justifyLineEligible(lastLine bool, line []inlineItem, blockStyle *ResolvedStyle) bool {
	allowLastLine := lastLine && len(line) > 0 &&
		line[0].style != nil && line[0].style.TextAlignLast == cssTextAlignJustify
	if blockStyle != nil && blockStyle.TextAlignLast == cssTextAlignJustify {
		allowLastLine = allowLastLine || lastLine
	}

	return !lastLine || allowLastLine
}

// capInterCharGap caps per-char slack at the soft gap limit and reports
// whether justification should proceed.
func capInterCharGap(slack float64, totalRunes int, maxGap float64) (float64, bool) {
	perChar := slack / float64(totalRunes)

	if perChar > maxGap*2 || perChar <= 0 {
		return 0, false
	}

	if perChar > maxGap {
		perChar = maxGap
	}

	return perChar, true
}

// countLineSpacingRunes counts the spacing runes per line item and their
// total, skipping breaks, images, boxes, and empty text.
func countLineSpacingRunes(line []inlineItem) ([]int, int) {
	counts := make([]int, len(line))
	totalRunes := 0

	for idx := range line {
		item := &line[idx]
		if item.forceBreak || item.img || item.blockBox != nil || item.style == nil || item.text == "" {
			continue
		}

		n := countSpacingRunes(item.text)
		counts[idx] = n
		totalRunes += n
	}

	return counts, totalRunes
}

// spreadInterCharSpacing adds perChar letter spacing to every counted item.
func spreadInterCharSpacing(line []inlineItem, counts []int, perChar, delta float64) {
	for idx := range line {
		if counts[idx] == 0 {
			continue
		}

		cloned := *line[idx].style
		cloned.LetterSpacing += delta
		line[idx].style = &cloned
		line[idx].w += perChar * float64(counts[idx])
	}
}

// countSpacingRunes counts the runes that carry letter spacing: every rune
// except soft hyphens and variation selectors, matching measureTextFace.
func countSpacingRunes(text string) int {
	count := 0

	for _, r := range text {
		if r == softHyphenRune || isVariationSelector(r) {
			continue
		}

		count++
	}

	return count
}
