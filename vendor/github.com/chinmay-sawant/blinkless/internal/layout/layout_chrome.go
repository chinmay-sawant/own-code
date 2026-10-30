//nolint:all
package layout

import (
	"math"
	"sort"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

func (e *engine) markOpsFixed(start, end int) {
	if end < start {
		return
	}

	for i := start; i <= end && i < len(e.ops); i++ {
		e.ops[i].Fixed = true
	}
}

// appendBorderLineOps appends border segment ops into dst (may be nil).
func appendBorderLineOps(
	dst []Op, posX, posY, boxW, boxH, width float64, style string, red, green, blue float64,
) []Op {
	var buf [8]GridSeg

	segs := appendBorderLineSegments(buf[:0], posX, posY, boxW, boxH, width, style, red, green, blue)
	for idx := range segs {
		dst = append(dst, gridSegOp(segs[idx]))
	}

	return dst
}

// appendBorderLineSegments appends one border edge as grid segments: one
// segment for solid styles, the expanded dash/dot run otherwise.
func appendBorderLineSegments(
	dst []GridSeg, posX, posY, boxW, boxH, width float64, style string, red, green, blue float64,
) []GridSeg {
	if width <= 0 || style == cssDisplayNone || (boxW <= 0 && boxH <= 0) {
		return dst
	}

	if style != borderStyleDashed && style != borderStyleDotted {
		return append(dst, GridSeg{X: posX, Y: posY, W: boxW, H: boxH, Width: width, R: red, G: green, B: blue})
	}

	return appendDashedLineGridSegments(
		dst, posX, posY, boxW, boxH, width, style == borderStyleDotted, red, green, blue,
	)
}

// gridSegOp converts one grid segment into the OpLine it stands for.
func gridSegOp(seg GridSeg) Op {
	return Op{ //nolint:exhaustruct // intentional zero fields
		Kind: OpLine, X: seg.X, Y: seg.Y, W: seg.W, H: seg.H,
		Width: seg.Width, R: seg.R, G: seg.G, B: seg.B, LineInset: seg.LineInset,
	}
}

func appendBorderLineOpsForSide(
	dst []Op, posX, posY, boxW, boxH, width float64, style string,
	red, green, blue float64, lineInset uint8,
) []Op {
	start := len(dst)
	dst = appendBorderLineOps(dst, posX, posY, boxW, boxH, width, style, red, green, blue)
	if lineInset == 0 {
		return dst
	}

	for idx := start; idx < len(dst); idx++ {
		dst[idx].LineInset = lineInset
	}

	return dst
}

// maxDashSegments caps dashed and dotted edge pieces. 1024 matches the
// background tile cap. Draw and gap are at least 0.5pt, so the painter
// steps at least 1pt; the count is only a capacity hint unless the loop
// stops at the same bound. Outlines and column rules share this helper.
const maxDashSegments = 1024

// dashedSegmentMetrics derives the draw and gap lengths for one dashed or
// dotted edge and the segment count. Callers that size a shared buffer for
// several sides use the count to allocate once, and it never exceeds
// maxDashSegments.
func dashedSegmentMetrics(length, width float64, dotted bool) (drawLen, gap float64, segments int) {
	drawLen, gap = width*3, width*2 // three=3, two=2 inlined; remaining two/three usages parked
	if dotted {
		drawLen, gap = width, width*1.5
	}

	if drawLen < 0.5 { // halfRatio 0.5 inlined
		drawLen = 0.5
	}

	if gap < 0.5 { // halfRatio 0.5 inlined
		gap = 0.5
	}

	return drawLen, gap, cappedDashSegments(length, drawLen+gap)
}

// cappedDashSegments is the number of dash or dot pieces that fit in length,
// never more than maxDashSegments. The float division is capped before the
// int conversion so a huge length cannot request a multi-million allocation.
func cappedDashSegments(length, step float64) int {
	if !(step > 0) || !(length > 0) {
		return 1
	}

	count := length / step
	if !(count < float64(maxDashSegments)) {
		return maxDashSegments
	}

	segments := int(count) + 1
	if segments < 1 {
		return 1
	}

	if segments > maxDashSegments {
		return maxDashSegments
	}

	return segments
}

// borderSideOpCount returns the largest number of ops one border side can
// append: one line for solid styles, the dashed segment bound otherwise.
// length is an upper bound on the painted edge length of that side.
func borderSideOpCount(length, width float64, style string) int {
	if style != borderStyleDashed && style != borderStyleDotted {
		return 1
	}

	_, _, segments := dashedSegmentMetrics(length, width, style == borderStyleDotted)
	if segments < 1 {
		return 1
	}

	return segments
}

// appendDashedLineGridSegments expands a dashed/dotted border edge into grid
// segments.
func appendDashedLineGridSegments(
	dst []GridSeg, posX, posY, boxW, boxH, width float64, dotted bool, red, green, blue float64,
) []GridSeg {
	horizontal := boxW > 0
	length := boxW

	if !horizontal {
		length = boxH
	}

	drawLen, gap, n := dashedSegmentMetrics(length, width, dotted)
	if n > maxDashSegments {
		n = maxDashSegments
	}

	if n < 1 {
		n = 1
	}

	if cap(dst)-len(dst) < n {
		// Grow once for the capped segment count, not the raw edge length.
		grown := make([]GridSeg, len(dst), len(dst)+n)
		copy(grown, dst)
		dst = grown
	}

	step := drawLen + gap
	emitted := 0
	for pos := 0.0; pos < length-0.001 && emitted < n; pos += step {
		seg := math.Min(drawLen, length-pos)
		if seg <= 0 {
			break
		}

		segX, segY, segW, segH := posX+pos, posY, seg, 0.0
		if !horizontal {
			segX, segY, segW, segH = posX, posY+pos, 0.0, seg
		}

		dst = append(dst, GridSeg{
			X: segX, Y: segY, W: segW, H: segH,
			Width: width, R: red, G: green, B: blue,
		})
		emitted++
	}

	return dst
}

// emitBorderLine appends one edge's border segments straight onto e.ops —
// no intermediate []Op (hot path for box borders).
func (e *engine) emitBorderLine(posX, posY, boxW, boxH, width float64, style string, red, green, blue float64) {
	var buf [8]GridSeg

	segs := appendBorderLineSegments(buf[:0], posX, posY, boxW, boxH, width, style, red, green, blue)
	for i := range segs {
		e.add(gridSegOp(segs[i]))
	}
}

// borderOps returns the four border line ops for the given border box.
func (e *engine) borderOps(sty ResolvedStyle, posX, posY, wid, height float64) []Op {
	return e.appendBorderOps(nil, sty, posX, posY, wid, height)
}

// appendBorderOps appends the four border line ops for the given border box
// into dst, so chrome assembly reuses one backing array instead of copying
// a per-side temporary into it.
func (e *engine) appendBorderOps(dst []Op, sty ResolvedStyle, posX, posY, wid, height float64) []Op {
	return e.appendBorderOpsSides(dst, sty, posX, posY, wid, height, true, true, true, true)
}

// borderOpsSides emits the requested sides of a border box.
func (e *engine) borderOpsSides(
	sty ResolvedStyle, posX, posY, wid, height float64, top, right, bottom, left bool,
) []Op {
	return e.appendBorderOpsSides(nil, sty, posX, posY, wid, height, top, right, bottom, left)
}

// growChromeOps ensures dst has room for need more ops with a single
// allocation, so the sequential chrome appends for one box do not leave a
// chain of discarded growth backings behind.
func growChromeOps(dst []Op, need int) []Op {
	if need <= 0 || cap(dst)-len(dst) >= need {
		return dst
	}

	grown := make([]Op, len(dst), len(dst)+need)
	copy(grown, dst)

	return grown
}

// appendBorderOpsSides appends the requested sides of a border box into dst,
// growing it at most once for the dashed-segment bound. A nil dst behaves
// exactly like the old allocating form.
func (e *engine) appendBorderOpsSides(
	dst []Op, sty ResolvedStyle, posX, posY, wid, height float64, top, right, bottom, left bool,
) []Op {
	wTop := 0.0
	if top && sty.BorderTop.Style != cssDisplayNone && !sty.BorderTop.Transparent && borderPaint(sty.BorderTop) > 0 {
		wTop = e.scalePt(borderPaint(sty.BorderTop))
	}

	wRight := 0.0
	if right && sty.BorderRight.Style != cssDisplayNone && !sty.BorderRight.Transparent && borderPaint(sty.BorderRight) > 0 {
		wRight = e.scalePt(borderPaint(sty.BorderRight))
	}

	wBottom := 0.0
	if bottom && sty.BorderBottom.Style != cssDisplayNone && !sty.BorderBottom.Transparent &&
		borderPaint(sty.BorderBottom) > 0 {
		wBottom = e.scalePt(borderPaint(sty.BorderBottom))
	}

	wLeft := 0.0
	if left && sty.BorderLeft.Style != cssDisplayNone && !sty.BorderLeft.Transparent && borderPaint(sty.BorderLeft) > 0 {
		wLeft = e.scalePt(borderPaint(sty.BorderLeft))
	}

	mixed := wTop != wRight || wTop != wBottom || wTop != wLeft

	topInset := borderSideLineInset(mixed, sty.BorderTop.Style, LineInsetTop)
	rightInset := borderSideLineInset(mixed, sty.BorderRight.Style, LineInsetRight)
	bottomInset := borderSideLineInset(mixed, sty.BorderBottom.Style, LineInsetBottom)
	leftInset := borderSideLineInset(mixed, sty.BorderLeft.Style, LineInsetLeft)

	// Size the slice once for the active sides. Edge lengths passed to the
	// dashed splitter are at most wid/height, so bounding with those keeps the
	// single allocation an upper bound; the per-call growth fallback still
	// covers every other appendDashedLineSegments caller.
	capHint := 0
	if wTop > 0 {
		capHint += borderSideOpCount(wid, wTop, sty.BorderTop.Style)
	}

	if wRight > 0 {
		capHint += borderSideOpCount(height, wRight, sty.BorderRight.Style)
	}

	if wBottom > 0 {
		capHint += borderSideOpCount(wid, wBottom, sty.BorderBottom.Style)
	}

	if wLeft > 0 {
		capHint += borderSideOpCount(height, wLeft, sty.BorderLeft.Style)
	}

	if dst == nil && capHint > 0 {
		dst = make([]Op, 0, capHint)
	} else {
		dst = growChromeOps(dst, capHint)
	}

	ops := dst

	if wTop > 0 {
		adjL := 0.0
		if wLeft > 0 && wLeft > wTop {
			adjL = (wLeft + wTop) / 2.0
		}
		adjR := 0.0
		if wRight > 0 && wRight > wTop {
			adjR = (wRight + wTop) / 2.0
		}
		topX := posX + adjL
		topW := wid - adjL - adjR
		if topW > 0 {
			ops = appendBorderLineOpsForSide(ops, topX, posY, topW, 0, wTop, sty.BorderTop.Style,
				sty.BorderTop.Color[0], sty.BorderTop.Color[1], sty.BorderTop.Color[2], topInset)
		}
	}

	if wRight > 0 {
		adjT := 0.0
		if wTop > 0 && wTop > wRight {
			adjT = (wTop + wRight) / 2.0
		}
		adjB := 0.0
		if wBottom > 0 && wBottom > wRight {
			adjB = (wBottom + wRight) / 2.0
		}
		rightY := posY + adjT
		rightH := height - adjT - adjB
		if rightH > 0 {
			ops = appendBorderLineOpsForSide(ops, posX+wid, rightY, 0, rightH, wRight, sty.BorderRight.Style,
				sty.BorderRight.Color[0], sty.BorderRight.Color[1], sty.BorderRight.Color[2], rightInset)
		}
	}

	if wBottom > 0 {
		adjL := 0.0
		if wLeft > 0 && wLeft > wBottom {
			adjL = (wLeft + wBottom) / 2.0
		}
		adjR := 0.0
		if wRight > 0 && wRight > wBottom {
			adjR = (wRight + wBottom) / 2.0
		}
		botX := posX + adjL
		botW := wid - adjL - adjR
		if botW > 0 {
			ops = appendBorderLineOpsForSide(ops, botX, posY+height, botW, 0, wBottom, sty.BorderBottom.Style,
				sty.BorderBottom.Color[0], sty.BorderBottom.Color[1], sty.BorderBottom.Color[2], bottomInset)
		}
	}

	if wLeft > 0 {
		adjT := 0.0
		if wTop > 0 && wTop > wLeft {
			adjT = (wTop + wLeft) / 2.0
		}
		adjB := 0.0
		if wBottom > 0 && wBottom > wLeft {
			adjB = (wBottom + wLeft) / 2.0
		}
		leftY := posY + adjT
		leftH := height - adjT - adjB
		if leftH > 0 {
			ops = appendBorderLineOpsForSide(ops, posX, leftY, 0, leftH, wLeft, sty.BorderLeft.Style,
				sty.BorderLeft.Color[0], sty.BorderLeft.Color[1], sty.BorderLeft.Color[2], leftInset)
		}
	}

	return ops
}

// borderSideLineInset returns the inward paint inset for one border side.
// Mixed-width frames inset every side so the wider strip owns the corner.
// Dashed and dotted sides always inset: their fragments sit centered in the
// strip instead of straddling the border-box edge, so the authored stroke
// width lands inside the box (Chrome's dotted geometry).
func borderSideLineInset(mixed bool, style string, flag uint8) uint8 {
	if mixed || isDashedOrDottedStyle(style) {
		return flag
	}

	return 0
}

func hasVerticalBorder(sty ResolvedStyle) bool {
	left := borderPaint(sty.BorderLeft) > 0 && sty.BorderLeft.Style != cssDisplayNone
	right := borderPaint(sty.BorderRight) > 0 && sty.BorderRight.Style != cssDisplayNone

	return left || right
}

const htmlFigure = "figure"

func isCaptionBox(caption *box) bool {
	if caption == nil || caption.node == nil || caption.style == nil {
		return false
	}

	return caption.node.Name == "figcaption" || caption.style.Display == displayTableCaption
}

func isCollapsedThumbPair(parent *html.Node, parentStyle, captionStyle *ResolvedStyle) bool {
	if parent == nil || parentStyle == nil || captionStyle == nil {
		return false
	}

	isFigure := parent.Name == htmlFigure || parentStyle.Display == displayTable
	if !isFigure || !hasVerticalBorder(*parentStyle) || !hasVerticalBorder(*captionStyle) {
		return false
	}

	if parentStyle.BorderCollapse == borderCollapseValue {
		return true
	}

	return borderPaint(parentStyle.BorderBottom) <= 0 && borderPaint(captionStyle.BorderTop) <= 0
}

// collapsedThumbCaption reports a MediaWiki-style figure/figcaption pair
// whose CSS is display:table + border-collapse (or matching open sides).
// Those sides must paint as one frame, not a second caption box.
func (e *engine) collapsedThumbCaption(caption *box) (*ResolvedStyle, bool) {
	if e == nil || !isCaptionBox(caption) {
		return nil, false
	}

	parent := caption.node.Parent
	if parent == nil {
		return nil, false
	}

	parentStyle := e.stylePtr(parent)
	if !isCollapsedThumbPair(parent, parentStyle, caption.style) {
		return nil, false
	}

	return parentStyle, true
}

// thumbImageInsideFigure reports an <img> whose nearest figure uses the
// collapsed thumb frame. Its left/right/top sit on the figure rails; only
// the bottom separator should paint.
func (e *engine) thumbImageInsideFigure(node *html.Node) bool {
	if e == nil || node == nil || node.Name != cssTagImg {
		return false
	}

	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Name != htmlFigure {
			continue
		}

		for _, child := range parent.Children {
			if child != nil && child.Name == "figcaption" {
				captionBox := &box{node: child, style: e.stylePtr(child)} //nolint:exhaustruct // style probe only
				_, ok := e.collapsedThumbCaption(captionBox)

				return ok
			}
		}

		return false
	}

	return false
}

// chromeMustSpliceImmediately reports boxes whose chrome must land in e.ops
// during the build: sticky (StickyID stamp), fixed (Fixed stamp), and
// transform (op-range exclusive CTM stamp). Common static/relative boxes defer.
func chromeMustSpliceImmediately(st ResolvedStyle) bool {
	if st.HasTransform {
		return true
	}

	switch st.Position {
	case positionSticky, positionFixed:
		return true
	}

	return false
}

// prependChrome inserts background + border ops at insertAt so they paint
// under any content ops already appended for this box.
//
// Common path defers the splice until finalizeChrome (one linear merge).
// Sticky/fixed/transform keep an immediate splice so mid-build StickyID/Fixed
// stamps and transform exclusive ranges stay correct without re-derivation.
//
//nolint:cyclop,wsl,funlen // paint ordering and border geometry stay together
func (e *engine) prependChrome(insertAt int, boxNode *box, sty ResolvedStyle, posX, posY, width, height float64) {
	if e.noEmit {
		return
	}
	if isNeutralFrameSection(boxNode.node, sty) {
		sty.BorderTop = sty.BorderBottom
	}

	if overflowClipsPaint(sty.Overflow) && insertAt >= 0 && insertAt <= len(e.ops) {
		clipOpsSlice(e.ops[insertAt:], e.paddingBoxRect(posX, posY, width, height, sty))
	}

	var chrome []Op
	radii, radiiY := usedBorderRadiiXY(sty, width, height)
	shapeK := cornerShapePaintK(sty)
	cornerFillOps, cornerBorderOps, cornerFillHandled, cornerBorderHandled := cornerShapePaint(
		e, boxNode, sty, posX, posY, width, height, radii, radiiY, shapeK)
	radius := uniformRadius(radii)
	// Outset box-shadow paints behind the background so blur rings do not
	// cover the border box. Inset shadows must paint after the background
	// (CSS Backgrounds §7.1 / fixture-61 box-shadow-position), or the fill
	// hides them.
	chrome = e.appendBoxShadow(chrome, sty, posX, posY, width, height, radii, radiiY, false)
	if cornerFillHandled {
		chrome = append(chrome, cornerFillOps...)
	} else if sty.BGColor[3] > 0 && e.backgroundPaintEnabled(&sty) {
		usedBG := usedBGForPaintActive(sty, e.forcedColorsForPaint())
		bgOp := Op{ //nolint:exhaustruct // intentional zero fields
			Kind: OpFillRect, X: posX, Y: posY, W: width, H: height,
			R: usedBG[0], G: usedBG[1], B: usedBG[2], Alpha: usedBG[3], Radius: radius,
			RadiusTopLeft: radii[0], RadiusTopRight: radii[1], RadiusBottomRight: radii[2], RadiusBottomLeft: radii[3],
		}

		bgClip := sty.BackgroundClip
		if backgroundClipIsPaddingBox(bgClip) || backgroundClipIsContentBox(bgClip) {
			cx, cy, cw, ch := resolveBackgroundClipBox(sty, posX, posY, width, height)
			clip := clipRect{x: cx, y: cy, w: cw, h: ch}
			if !clip.empty() {
				clipRectOp(&bgOp, clip)
				if bgOp.W > 0 && bgOp.H > 0 {
					chrome = append(chrome, bgOp)
				}
			}
		} else {
			chrome = append(chrome, bgOp)
		}
	} else if isColorSchemeCanvasRoot(boxNode) {
		if fill, ok := canvasFillForRoot(&sty); ok && width > 0 && height > 0 {
			bgOp := Op{ //nolint:exhaustruct // intentional zero fields
				Kind: OpFillRect, X: posX, Y: posY, W: width, H: height,
				R: fill[0], G: fill[1], B: fill[2], Alpha: fill[3], Radius: radius,
				RadiusTopLeft: radii[0], RadiusTopRight: radii[1], RadiusBottomRight: radii[2], RadiusBottomLeft: radii[3],
			}
			bgOp.setCanvasBackground()
			chrome = append(chrome, bgOp)
		}
	}
	chrome = e.appendBackgroundImage(chrome, sty, posX, posY, width, height)
	chrome = e.appendBoxShadow(chrome, sty, posX, posY, width, height, radii, radiiY, true)

	if sty.BorderImageSource != "" {
		chrome = e.appendBorderImage(chrome, sty, posX, posY, width, height)
	} else if cornerBorderHandled {
		chrome = append(chrome, cornerBorderOps...)
	} else {
		switch {
		case hasRoundedRadii(radii) && roundedSolidBorder(sty):
			chrome = e.appendRoundedBorderOps(chrome, sty, posX, posY, width, height, radii)
		case hasRoundedRadii(radii) && roundedAccentBorder(sty):
			chrome = e.appendRoundedAccentBorderOps(chrome, sty, posX, posY, width, height, radii)
		default:
			chrome = e.appendCollapsedOrFullBorderOps(chrome, boxNode, sty, posX, posY, width, height)
		}
	}
	if boxNode != nil && outlinePaints(&sty) {
		effW, _ := effectiveOutline(&sty)
		boxNode.outlineInflate = e.scalePt(outlineInflate(effW, sty.OutlineOffset))
	}
	chrome = e.appendOutlineOpsForStyle(chrome, &sty, posX, posY, width, height)
	stampOpRadiiY(chrome, radiiY)
	if len(chrome) == 0 {
		return
	}
	e.assignChromeOpIDs(chrome)

	for i := range chrome {
		chrome[i].bindEmptyExtra()
		chrome[i].ZIndex = e.zIndex
		chrome[i].ZIndexSet = e.zIndexSet
		chrome[i].Positioned = e.positioned
		if e.blendGroup != nil {
			chrome[i].setBlendGroup(e.blendGroup)
		} else if chrome[i].BlendMode == "" || chrome[i].BlendMode == blendNormal {
			chrome[i].setBlendMode(e.blendMode)
		}
	}

	if !chromeMustSpliceImmediately(sty) {
		e.deferredChrome = append(e.deferredChrome, chromeEntry{at: insertAt, ops: chrome, b: boxNode})

		return
	}
	// Immediate splice (sticky/fixed/transform).
	shiftDescendantOpRanges(boxNode, insertAt, len(chrome))
	tail := append([]Op(nil), e.ops[insertAt:]...)
	e.ops = e.ops[:insertAt]
	e.ops = append(e.ops, chrome...)
	e.ops = append(e.ops, tail...)
	// Keep deferred insert indices valid after this mid-build splice.
	n := len(chrome)

	for i := range e.deferredChrome {
		if e.deferredChrome[i].at >= insertAt {
			e.deferredChrome[i].at += n
		}
	}
}

func (e *engine) assignChromeOpIDs(ops []Op) {
	for index := range ops {
		if ops[index].ID == 0 {
			e.nextOpID++
			ops[index].ID = e.nextOpID
		} else if ops[index].ID > e.nextOpID {
			e.nextOpID = ops[index].ID
		}
	}
}

// shiftDescendantOpRanges keeps child ownership aligned when immediate chrome
// is inserted before the box's content. Deferred chrome remaps ranges in
// finalizeChrome; transformed boxes splice during layout so they need this
// local shift before the parent range is finalized.
func shiftDescendantOpRanges(boxNode *box, insertAt, count int) {
	if boxNode == nil || count == 0 {
		return
	}

	for _, child := range boxNode.children {
		if child.opStart >= insertAt {
			child.opStart += count
		}
		if child.opEnd >= insertAt {
			child.opEnd += count
		}
		shiftDescendantOpRanges(child, insertAt, count)
	}
}

// backgroundClipIsContentBox reports a content-box background clip without
// allocating: the keyword comparison folds ASCII case and trims ASCII
// whitespace on subslices, so the hot per-box path keeps no strings.
func backgroundClipIsContentBox(clip string) bool {
	return asciiFoldEqual(trimSpaceASCII(clip), "content-box")
}

// backgroundClipIsPaddingBox reports a padding-box background clip without
// allocating (see backgroundClipIsContentBox).
func backgroundClipIsPaddingBox(clip string) bool {
	return asciiFoldEqual(trimSpaceASCII(clip), "padding-box")
}

// trimSpaceASCII trims ASCII whitespace from both ends, returning a subslice
// of s. Unlike strings.TrimSpace it never allocates and never decodes UTF-8.
func trimSpaceASCII(s string) string {
	start := 0
	for start < len(s) {
		if c := s[start]; c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '\f' {
			break
		}
		start++
	}
	end := len(s)
	for end > start {
		if c := s[end-1]; c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '\f' {
			break
		}
		end--
	}
	return s[start:end]
}

// asciiFoldEqual reports whether s equals want under ASCII case folding,
// without allocating. CSS keywords are ASCII, so full Unicode folding is
// unnecessary on this hot per-box path.
func asciiFoldEqual(s, want string) bool {
	if len(s) != len(want) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c, w := s[i], want[i]
		if c == w {
			continue
		}
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != w {
			return false
		}
	}
	return true
}

// appendCollapsedOrFullBorderOps appends a single joined frame for a
// collapsed figure/figcaption thumb into dst: the figure already owns the
// vertical rails, so the caption emits only the closing bottom edge at the
// figure's border-box width.
func (e *engine) appendCollapsedOrFullBorderOps(
	dst []Op, boxNode *box, sty ResolvedStyle, posX, posY, width, height float64,
) []Op {
	parentStyle, ok := e.collapsedThumbCaption(boxNode)
	if !ok {
		return e.appendBorderOps(dst, sty, posX, posY, width, height)
	}

	padL := e.scalePt(parentStyle.PaddingLeft + parentStyle.BorderLeft.Width)
	padR := e.scalePt(parentStyle.PaddingRight + parentStyle.BorderRight.Width)

	return e.appendBorderOpsSides(dst, sty, posX-padL, posY, width+padL+padR, height, false, false, true, false)
}

// isNeutralFrameSection recognizes a generic frame pattern rather than a
// fixture ID or literal text: a section with a solid, neutral outer border
// keeps its top edge consistent with its bottom edge across page fragments.
//
//nolint:wsl // generic frame checks remain adjacent to their border gates.
func isNeutralFrameSection(node *html.Node, sty ResolvedStyle) bool {
	if node == nil || node.Name != htmlSection {
		return false
	}
	if sty.BorderTop.Style != solidKeyword || sty.BorderBottom.Style != solidKeyword {
		return false
	}

	return nearlyEqual(sty.BorderTop.Width, sty.BorderBottom.Width)
}

func uniformRadius(radii [4]float64) float64 {
	if radii[0] == radii[1] && radii[1] == radii[2] && radii[2] == radii[3] {
		return radii[0]
	}

	return 0
}

func hasRoundedRadii(radii [4]float64) bool {
	for _, radius := range radii {
		if radius > 0 {
			return true
		}
	}

	return false
}

func uniformRoundedBorder(sty ResolvedStyle) bool {
	top := sty.BorderTop

	return top.Width > 0 && top.Style == solidKeyword &&
		top == sty.BorderRight && top == sty.BorderBottom && top == sty.BorderLeft
}

func roundedSolidBorder(sty ResolvedStyle) bool {
	return sty.BorderTop.Width > 0 && sty.BorderRight.Width > 0 &&
		sty.BorderBottom.Width > 0 && sty.BorderLeft.Width > 0 &&
		sty.BorderTop.Style == solidKeyword && sty.BorderRight.Style == solidKeyword &&
		sty.BorderBottom.Style == solidKeyword && sty.BorderLeft.Style == solidKeyword
}

func roundedAccentBorder(sty ResolvedStyle) bool {
	return (sty.BorderTop.Width > 0 && sty.BorderTop.Style == solidKeyword) ||
		(sty.BorderRight.Width > 0 && sty.BorderRight.Style == solidKeyword) ||
		(sty.BorderBottom.Width > 0 && sty.BorderBottom.Style == solidKeyword) ||
		(sty.BorderLeft.Width > 0 && sty.BorderLeft.Style == solidKeyword)
}

func squareSideBorderRadii(side border, radii [4]float64, left bool) [4]float64 {
	const radiusWidthSlack = 0.25
	if left {
		if radii[0] <= borderPaint(side)+radiusWidthSlack && radii[3] <= borderPaint(side)+radiusWidthSlack {
			return [4]float64{}
		}

		return radii
	}

	if radii[1] <= borderPaint(side)+radiusWidthSlack && radii[2] <= borderPaint(side)+radiusWidthSlack {
		return [4]float64{}
	}

	return radii
}

// mixedChromeSide pairs one border with the stroke mask its overlay paints.
type mixedChromeSide struct {
	border border
	mask   uint8
}

// appendRoundedBorderOps keeps the rounded outer geometry for a solid border whose
// sides differ in width or color. The base stroke supplies the corner arcs;
// differing sides are overlaid as masked OpStrokeRect sides so accent rails
// keep corner arcs without a second OpLine rewrite.
func (e *engine) appendRoundedBorderOps(
	dst []Op, sty ResolvedStyle, posX, posY, width, height float64, radii [4]float64,
) []Op {
	// Use the bottom side as the base geometry. CSS frequently uses an
	// accented top rail (for example the architecture pipeline cards); using
	// BorderTop here would spread that accent across the rounded stroke on all
	// four sides before the per-side overlays are painted.
	base := sty.BorderBottom
	// At most one base stroke plus one overlay per side; growing once keeps
	// the sequential appends below from leaving growth garbage.
	ops := growChromeOps(dst, 5)
	if !base.Transparent {
		ops = append(ops, Op{ //nolint:exhaustruct // intentional zero fields
			Kind: OpStrokeRect, X: posX, Y: posY, W: width, H: height,
			R: base.Color[0], G: base.Color[1], B: base.Color[2], Width: e.scalePt(borderPaint(base)),
			Radius: uniformRadius(radii), RadiusTopLeft: radii[0], RadiusTopRight: radii[1],
			RadiusBottomRight: radii[2], RadiusBottomLeft: radii[3],
		})
	}

	var sides [4]mixedChromeSide
	sides[0] = mixedChromeSide{border: sty.BorderTop, mask: StrokeMaskTop}
	sides[1] = mixedChromeSide{border: sty.BorderRight, mask: StrokeMaskRight}
	sides[2] = mixedChromeSide{border: sty.BorderBottom, mask: StrokeMaskBottom}
	sides[3] = mixedChromeSide{border: sty.BorderLeft, mask: StrokeMaskLeft}

	for _, side := range sides {
		if side.border == base || side.border.Style != solidKeyword || side.border.Transparent {
			continue
		}

		sideRadii := radii
		if side.mask == StrokeMaskLeft || side.mask == StrokeMaskRight {
			sideRadii = squareSideBorderRadii(side.border, radii, side.mask == StrokeMaskLeft)
		}

		ops = append(ops, Op{ //nolint:exhaustruct // intentional zero fields
			Kind: OpStrokeRect, X: posX, Y: posY, W: width, H: height,
			R: side.border.Color[0], G: side.border.Color[1], B: side.border.Color[2],
			Width: e.scalePt(borderPaint(side.border)), Radius: uniformRadius(sideRadii),
			RadiusTopLeft: sideRadii[0], RadiusTopRight: sideRadii[1],
			RadiusBottomRight: sideRadii[2], RadiusBottomLeft: sideRadii[3],
			StrokeMask: side.mask,
		})
	}

	return ops
}

// appendRoundedAccentBorderOps keeps a solid accent rail curved when the remaining
// border sides are absent or use dotted/dashed styles.
// Solid remnant sides use StrokeMask* overlays; dashed/dotted sides stay as
// segmented OpLines that stop at the corner radii.
func (e *engine) appendRoundedAccentBorderOps(
	dst []Op, sty ResolvedStyle,
	posX, posY, width, height float64,
	radii [4]float64,
) []Op {
	// At most one overlay per side; dashed segments append after.
	ops := growChromeOps(dst, 4)

	appendSolidMask := func(side border, mask uint8, sideRadii [4]float64) {
		if side.Transparent || borderPaint(side) <= 0 || side.Style != solidKeyword {
			return
		}

		ops = append(ops, Op{ //nolint:exhaustruct // intentional zero fields
			Kind: OpStrokeRect, X: posX, Y: posY, W: width, H: height,
			R: side.Color[0], G: side.Color[1], B: side.Color[2],
			Width: e.scalePt(borderPaint(side)), Radius: uniformRadius(sideRadii),
			RadiusTopLeft: sideRadii[0], RadiusTopRight: sideRadii[1],
			RadiusBottomRight: sideRadii[2], RadiusBottomLeft: sideRadii[3],
			StrokeMask: mask,
		})
	}

	appendDashedSide := func(sideX, sideY, sideW, sideH float64, side border) {
		if side.Transparent || borderPaint(side) <= 0 || side.Style == solidKeyword {
			return
		}

		ops = appendBorderLineOps(
			ops, sideX, sideY, sideW, sideH,
			e.scalePt(borderPaint(side)), side.Style,
			side.Color[0], side.Color[1], side.Color[2],
		)
	}

	rightRadii := squareSideBorderRadii(sty.BorderRight, radii, false)
	leftRadii := squareSideBorderRadii(sty.BorderLeft, radii, true)
	appendSolidMask(sty.BorderTop, StrokeMaskTop, radii)
	appendSolidMask(sty.BorderRight, StrokeMaskRight, rightRadii)
	appendSolidMask(sty.BorderBottom, StrokeMaskBottom, radii)
	appendSolidMask(sty.BorderLeft, StrokeMaskLeft, leftRadii)

	appendDashedSide(posX+radii[0], posY, math.Max(width-radii[0]-radii[1], 0), 0, sty.BorderTop)
	appendDashedSide(posX+width, posY+radii[1], 0, math.Max(height-radii[1]-radii[2], 0), sty.BorderRight)
	appendDashedSide(posX+radii[3], posY+height, math.Max(width-radii[3]-radii[2], 0), 0, sty.BorderBottom)
	appendDashedSide(posX, posY+radii[0], 0, math.Max(height-radii[0]-radii[3], 0), sty.BorderLeft)

	return ops
}

// finalizeChrome merges deferred background/border ops into e.ops in one
// linear pass and reindexes box op ranges. Paint order for multiple entries
// at the same index follows document order (ancestors before descendants,
// earlier siblings before later ones).
func (e *engine) finalizeChrome(root *box) {
	if len(e.deferredChrome) > 0 {
		entries := e.deferredChrome
		e.deferredChrome = nil

		out, oldToNew, ownerChrome := mergeDeferredChrome(e.ops, entries, boxPreorder(root))

		e.ops = out

		// Remap content op ranges, expand owners with their chrome, then union
		// parent ranges over children so ancestor ranges still cover nested chrome.
		remapBoxRangesWithChrome(root, oldToNew, ownerChrome)
		unionChildOpRanges(root)

		// Deferred chrome under sticky ancestors never received StickyID at build
		// time; re-stamp from the box tree. Fixed content already marked Fixed -
		// expand Fixed onto chrome in the same range when any op is Fixed.
		restampStickyFixed(root, e.ops)
	}

	e.applyColorAdjustPaintPolicy(root)
	e.applyOverflowClips(root)
}

// isColorSchemeCanvasRoot reports whether boxNode is the html element whose
// color-scheme selects the root canvas fill. Only the html root is consumed;
// nested values stay parsed and inherited.
func isColorSchemeCanvasRoot(boxNode *box) bool {
	return boxNode != nil && boxNode.node != nil && boxNode.node.Name == htmlRootName
}

// colorAdjustRootStyle returns the html root's resolved style for the
// color-adjust paint policy, or nil when the tree has no html box.
func colorAdjustRootStyle(root *box) *ResolvedStyle {
	for _, candidate := range colorAdjustRootCandidates(root) {
		if candidate != nil && candidate.node != nil && candidate.node.Name == htmlRootName {
			return candidate.style
		}
	}

	return nil
}

// colorAdjustRootCandidates lists the boxes that may hold the html root: the
// tree root itself plus its direct children (document node wrapping html).
func colorAdjustRootCandidates(root *box) []*box {
	if root == nil {
		return nil
	}

	return append([]*box{root}, root.children...)
}

// applyColorAdjustPaintPolicy rewrites text ink for the color-adjust family
// after chrome is final: dark-scheme default substitution (initial black
// only) plus forced-mode mapping and dynamic-range clamp. Background fills
// already resolved through usedBGForPaintActive at emission; table and inline
// span fills keep their own emission paths. Forced mode is on when the
// test override or Options.ForcedColorsActive selects it; the parser only
// yields in-gamut colors, so the clamp stage stays identity for real
// documents. Reference: Chrome 143.0.7499.40.
func (e *engine) applyColorAdjustPaintPolicy(root *box) {
	if e == nil || e.noEmit {
		return
	}

	rootStyle := colorAdjustRootStyle(root)
	if rootStyle == nil {
		return
	}

	rootScheme := rootStyle.ColorScheme
	dark := colorSchemeHasDark(rootScheme)
	if !dark {
		rootScheme = colorSchemeNormal
	}

	rootAdjust := rootStyle.ForcedColorAdjust
	rootLimit := rootStyle.DynamicRangeLimit
	forced := e.forcedColorsForPaint()

	// Fast path for the ordinary document: a light color scheme leaves the
	// dark default substitution as identity, a calm forced-colors mode keeps
	// author ink, and any limit outside standard/constrained-high keeps the
	// clamp as identity (see clampDynamicRangeColor). Then every op keeps its
	// ink and the per-text-op default lookup (which re-lowercases the scheme
	// for each black run) is pure overhead, so skip the sweep entirely.
	if !dark && !forced && rootLimit != dynamicRangeLimitStandard &&
		rootLimit != dynamicRangeLimitConstrainedHigh {
		return
	}

	for i := range e.ops {
		kind := e.ops[i].Kind
		if kind != OpText && kind != OpBullet {
			continue
		}

		before := [3]float64{e.ops[i].R, e.ops[i].G, e.ops[i].B}
		after := usedTextForPaintActive(before, rootScheme, rootAdjust, rootLimit, forced)
		e.ops[i].R, e.ops[i].G, e.ops[i].B = after[0], after[1], after[2]
	}
}

// chromeSpan is an inclusive op range owned by one box's chrome.
type chromeSpan struct{ start, end int }

// mergeDeferredChrome splices deferred background/border ops into oldOps in
// one linear pass. Paint order for multiple entries at the same index follows
// document order (ancestors before descendants, earlier siblings before later
// ones) so sibling op ranges stay disjoint. The merged sequence is built
// backwards so spare capacity in oldOps is reused in place when it fits;
// otherwise one exact-sized list is allocated.
func mergeDeferredChrome(
	oldOps []Op, entries []chromeEntry, paintOrder map[*box]int,
) ([]Op, []int, map[*box]chromeSpan) {
	// Sort by insert index ascending; within one index, document order keeps
	// an outer box under its descendants without letting a later sibling's
	// chrome splice ahead of an earlier sibling's.
	type indexed struct {
		ord int
		ent chromeEntry
	}

	order := make([]indexed, len(entries))
	for i, ent := range entries {
		order[i] = indexed{ord: i, ent: ent}
	}

	sort.SliceStable(order, func(i, j int) bool {
		if order[i].ent.at != order[j].ent.at {
			return order[i].ent.at < order[j].ent.at
		}

		ri, iRanked := paintOrder[order[i].ent.b]
		rj, jRanked := paintOrder[order[j].ent.b]

		if iRanked && jRanked && ri != rj {
			return ri < rj
		}
		// Same box (or a box outside the tree): later register paints first,
		// which keeps an outer entry under its inner entries.
		return order[i].ord > order[j].ord
	})

	totalChrome := 0
	for _, it := range order {
		totalChrome += len(it.ent.ops)
	}

	// Fill the merged list backwards: the forward sequence is
	// [chrome at idx][oldOps[idx]] per index plus a trailing chrome block, so
	// walking backwards writes old ops first, then the chrome block that
	// precedes each one. Spare capacity in oldOps is used in place; otherwise
	// one exact-sized list is allocated (no append growth).
	need := len(oldOps) + totalChrome

	var out []Op
	if cap(oldOps)-len(oldOps) >= totalChrome {
		out = oldOps[:need]
	} else {
		out = make([]Op, need)
	}

	oldToNew := make([]int, len(oldOps))
	ownerChrome := map[*box]chromeSpan{}
	write := need

	// Trailing chrome lands after every old op: entries whose at is past the
	// last op. A negative at is unreachable by the forward cursor and blocks it
	// for the rest of the walk, so then every entry becomes trailing.
	firstTrailing := len(order)
	for firstTrailing > 0 && order[firstTrailing-1].ent.at >= len(oldOps) {
		firstTrailing--
	}

	if len(order) > 0 && order[0].ent.at < 0 {
		firstTrailing = 0
	}

	oidx := len(order) - 1
	for oidx >= firstTrailing {
		ent := order[oidx].ent
		start := write - len(ent.ops)
		copy(out[start:], ent.ops)
		recordOwnerChrome(ownerChrome, ent.b, start, write-1)

		write = start
		oidx--
	}

	for idx := len(oldOps) - 1; idx >= 0; idx-- {
		write--
		out[write] = oldOps[idx]
		oldToNew[idx] = write

		for oidx >= 0 && order[oidx].ent.at == idx {
			ent := order[oidx].ent
			start := write - len(ent.ops)
			copy(out[start:], ent.ops)
			recordOwnerChrome(ownerChrome, ent.b, start, write-1)

			write = start
			oidx--
		}
	}

	return out, oldToNew, ownerChrome
}

// boxPreorder ranks every box in document (preorder) order so deferred chrome
// entries that share an insertion index splice in document order: ancestors
// before descendants, earlier siblings before later ones. Registration order
// alone reverses unrelated siblings at a shared index, which let a later
// section's chrome splice ahead of an earlier section's subtree and left the
// earlier section's op range covering the later chrome (Chrome Flex sections
// 11-13: the later overflow clips then deactivated the earlier fills).
func boxPreorder(root *box) map[*box]int {
	ranks := make(map[*box]int)

	rank := 0

	var walk func(b *box)

	walk = func(b *box) {
		if b == nil {
			return
		}

		ranks[b] = rank
		rank++

		for _, child := range b.children {
			walk(child)
		}
	}

	walk(root)

	return ranks
}

// recordOwnerChrome widens the chrome op span recorded for a box so a later
// (outer) entry keeps the owner's range covering all of its chrome.
func recordOwnerChrome(ownerChrome map[*box]chromeSpan, boxNode *box, start, endIdx int) {
	if boxNode == nil || endIdx < start {
		return
	}

	if prev, ok := ownerChrome[boxNode]; ok {
		if start < prev.start {
			prev.start = start
		}

		if endIdx > prev.end {
			prev.end = endIdx
		}

		ownerChrome[boxNode] = prev

		return
	}

	ownerChrome[boxNode] = chromeSpan{start: start, end: endIdx}
}

// remapBoxRangesWithChrome rewrites box op ranges through the old→new index
// map, then expands each owner with its recorded chrome span.
func remapBoxRangesWithChrome(root *box, oldToNew []int, ownerChrome map[*box]chromeSpan) {
	var remap func(b *box)
	remap = func(boxNode *box) {
		if boxNode == nil {
			return
		}

		if boxNode.opEnd >= boxNode.opStart && boxNode.opStart >= 0 && boxNode.opEnd < len(oldToNew) {
			boxNode.opStart = oldToNew[boxNode.opStart]
			boxNode.opEnd = oldToNew[boxNode.opEnd]
		}

		if span, ok := ownerChrome[boxNode]; ok {
			mergeOwnerChromeSpan(boxNode, span)
		}

		for _, child := range boxNode.children {
			remap(child)
		}
	}
	remap(root)
}

// mergeOwnerChromeSpan unions a box's content range with its chrome span.
func mergeOwnerChromeSpan(boxNode *box, span chromeSpan) {
	if boxNode.opEnd < boxNode.opStart {
		boxNode.opStart, boxNode.opEnd = span.start, span.end

		return
	}

	if span.start < boxNode.opStart {
		boxNode.opStart = span.start
	}

	if span.end > boxNode.opEnd {
		boxNode.opEnd = span.end
	}
}

// unionChildOpRanges widens every box range over its children's ranges so
// ancestor ranges still cover nested chrome.
func unionChildOpRanges(root *box) {
	var unionChildren func(b *box)
	unionChildren = func(boxNode *box) {
		if boxNode == nil {
			return
		}

		for _, child := range boxNode.children {
			unionChildren(child)

			if child.opEnd < child.opStart {
				continue
			}

			if boxNode.opEnd < boxNode.opStart {
				boxNode.opStart, boxNode.opEnd = child.opStart, child.opEnd

				continue
			}

			if child.opStart < boxNode.opStart {
				boxNode.opStart = child.opStart
			}

			if child.opEnd > boxNode.opEnd {
				boxNode.opEnd = child.opEnd
			}
		}
	}
	unionChildren(root)
}

// restampStickyFixed re-applies StickyID from sticky boxes and expands Fixed
// onto the full op range when the box was viewport-fixed (any op already Fixed).
func restampStickyFixed(boxNode *box, ops []Op) {
	if boxNode == nil {
		return
	}

	reapplyStickyID(boxNode, ops)
	expandFixedOps(boxNode, ops)

	for _, c := range boxNode.children {
		restampStickyFixed(c, ops)
	}
}

// reapplyStickyID stamps StickyID onto a sticky box's whole op range.
func reapplyStickyID(boxNode *box, ops []Op) {
	if !boxNode.sticky || boxNode.stickyID == 0 || boxNode.opEnd < boxNode.opStart || boxNode.opStart < 0 {
		return
	}

	for i := boxNode.opStart; i <= boxNode.opEnd && i < len(ops); i++ {
		ops[i].StickyID = boxNode.stickyID
	}
}

// expandFixedOps spreads the Fixed mark over a viewport-fixed box's op range
// when any op in it is already Fixed (chrome added after build is included).
func expandFixedOps(boxNode *box, ops []Op) {
	if boxNode.style.Position != positionFixed || boxNode.opEnd < boxNode.opStart || boxNode.opStart < 0 {
		return
	}

	hasFixed := false

	for i := boxNode.opStart; i <= boxNode.opEnd && i < len(ops); i++ {
		if ops[i].Fixed {
			hasFixed = true

			break
		}
	}

	if !hasFixed {
		return
	}

	for i := boxNode.opStart; i <= boxNode.opEnd && i < len(ops); i++ {
		ops[i].Fixed = true
	}
}

// contentBox returns the content-box origin and width for a border box.
// Single home for "content = border-box − scaled padding − scaled border".
