package layout

import (
	"math"
	"sort"
)

// PageBreaks returns the page-break metadata for a finished layout: canvas
// Y positions where a new page must start, and canvas [start, end] ranges a
// paginator must keep on one page.
//
// Forced positions come from page-break-before/after: always. Keep ranges
// come from break-inside: avoid boxes, from every table row (a row is
// atomic: it moves whole instead of leaving an empty leading fragment),
// from a heading and the content that follows it, and from single-line
// blocks: a line cannot be split, so a block whose only line cannot stay on
// the current page must move whole and carry its padding, border, and
// background with it. pageH is the page content height the layout was
// produced for (the layout viewport height); a non-positive value skips the
// page-relative single-line ranges. All values are canvas points.
func PageBreaks(res *Result, pageH float64) ([]float64, [][2]float64) {
	if res == nil || res.root == nil {
		return nil, nil
	}

	collector := &breakCollector{ops: res.Ops} //nolint:exhaustruct // collectors start empty by design

	var walk func(boxNode *box, insideTable, outOfFlow bool)

	walk = func(boxNode *box, insideTable, outOfFlow bool) {
		if boxNode == nil {
			return
		}

		collector.add(boxNode, res.Height)
		collector.addHeadingKeeps(boxNode)

		insideTable = insideTable || boxNode.kind == boxKindTable || boxNode.kind == boxKindCell
		outOfFlow = outOfFlow || boxOutOfFlow(boxNode.style)

		collector.addSingleLineKeep(res, pageH, boxNode, insideTable, outOfFlow)

		for _, child := range boxNode.children {
			walk(child, insideTable, outOfFlow)
		}
	}
	walk(res.root, false, false)

	forced := collector.forcedOut()

	return forced, collector.keepOut(forced)
}

// boxOutOfFlow reports whether a box fragments under its own rules rather
// than in normal flow: positioned boxes (absolute, fixed, sticky) and
// floats. Single-line keeps do not apply to them or their subtrees.
func boxOutOfFlow(style *ResolvedStyle) bool {
	if style == nil {
		return true
	}

	switch style.Position {
	case positionAbsolute, positionFixed, positionSticky:
		return true
	}

	return style.Float != "" && style.Float != cssDisplayNone
}

// inlineLevelBox reports whether a box is inline-level. An inline-level box
// shares its line with surrounding siblings, so moving it alone would tear
// the line apart.
func inlineLevelBox(style *ResolvedStyle) bool {
	if style == nil {
		return true
	}

	switch style.Display {
	case cssDisplayInline, cssDisplayInlineBlock, displayInlineFlex, displayInlineGrid:
		return true
	default:
		return false
	}
}

// singleLineKeepRange reports whether boxNode formats exactly one line of
// text and that line cannot fit the page containing the box top, and returns
// the canvas range a paginator must keep on one page. The range covers the
// border box, so the line moves with its padding, border, and background
// instead of leaving an empty leading fragment behind. Boxes taller than one
// page cannot be kept whole and split between lines instead.
func singleLineKeepRange(res *Result, boxNode *box, pageH float64) ([2]float64, bool) {
	var none [2]float64

	if boxNode == nil || pageH <= 0 || boxNode.height <= 0 || boxNode.height > pageH+layoutCoordEpsilon {
		return none, false
	}

	if inlineLevelBox(boxNode.style) || boxNode.opEnd < boxNode.opStart {
		return none, false
	}

	baselines := stripLineBaselines(res.Ops, boxNode.opStart, boxNode.opEnd+1)
	if len(baselines) != 1 {
		return none, false
	}

	if !boxOwnsLine(res, boxNode, baselines[0]) {
		return none, false
	}

	boundary := float64(int(boxNode.y/pageH)+1) * pageH
	if !lineExtendsPast(res.Ops, boxNode, baselines[0], boundary) {
		return none, false
	}

	return [2]float64{boxNode.y, boxNode.y + boxNode.height}, true
}

// boxOwnsLine reports whether any text or bullet op on the given baseline is
// formatted by boxNode itself rather than by a block-level child. An
// inline-level child shares the parent's line and does not take it away, so
// a row holding an inline-block still owns the line.
func boxOwnsLine(res *Result, boxNode *box, baseline float64) bool {
	for idx := boxNode.opStart; idx <= boxNode.opEnd; idx++ {
		if !opOnBaseline(res.Ops[idx], baseline) {
			continue
		}

		if !opInBlockChild(boxNode, idx) {
			return true
		}
	}

	return false
}

// opOnBaseline reports whether an op paints on a text line baseline. The
// epsilon matches stripLineBaselines so both agree on what one line is.
func opOnBaseline(paintOp Op, baseline float64) bool {
	if paintOp.Kind != OpText && paintOp.Kind != OpBullet {
		return false
	}

	return math.Abs(paintOp.Y-baseline) < orphanLineEpsilonPt
}

// opInBlockChild reports whether opIndex falls inside a block-level child
// box's op range.
func opInBlockChild(boxNode *box, opIndex int) bool {
	for _, child := range boxNode.children {
		if inlineLevelBox(child.style) {
			continue
		}

		if child.opStart <= opIndex && opIndex <= child.opEnd {
			return true
		}
	}

	return false
}

// lineExtendsPast reports whether any op of the line extends past a page
// boundary. Text and bullets carry their resolved line height in H and the
// baseline in Y, so baseline plus line height is the same crossing test the
// paginator uses to snap a text op to the next page. A missing height falls
// back to the face-size line factor generated ops use.
func lineExtendsPast(ops []Op, boxNode *box, baseline, boundary float64) bool {
	for idx := boxNode.opStart; idx <= boxNode.opEnd; idx++ {
		paintOp := ops[idx]
		if !opOnBaseline(paintOp, baseline) {
			continue
		}

		lineH := paintOp.H
		if lineH <= 0 {
			lineH = paintOp.Size * textLineHeightFactor
		}

		if paintOp.Y+lineH > boundary+layoutCoordEpsilon {
			return true
		}
	}

	return false
}

type breakCollector struct {
	ops    []Op
	forced []float64
	keep   [][2]float64
}

func (c *breakCollector) add(boxNode *box, canvasH float64) {
	if c == nil || boxNode == nil || boxNode.style == nil {
		return
	}

	// Table rows are atomic: a row moves whole to the next page when it
	// straddles a boundary, so its top padding and border are never eaten
	// by a page split and no empty leading fragment paints.
	c.addTableRowKeeps(boxNode)

	if boxNode.height <= 0 {
		return
	}

	if boxNode.style.PageBreakBefore == pageBreakAlways && boxNode.y > 0 {
		c.forced = append(c.forced, boxNode.y)
	}

	if boxNode.style.PageBreakAfter == pageBreakAlways && boxNode.y+boxNode.height < canvasH {
		c.forced = append(c.forced, boxNode.y+boxNode.height)
	}

	if boxNode.style.PageBreakInside == avoidKeyword {
		c.keep = append(c.keep, [2]float64{boxNode.y, boxNode.y + boxNode.height})
	}
}

// addSingleLineKeep adds the keep range for a box whose single text line
// cannot stay on the current page. Table contexts and out-of-flow boxes
// fragment under their own rules and are skipped, and a non-positive pageH
// disables page-relative single-line ranges entirely.
func (c *breakCollector) addSingleLineKeep(res *Result, pageH float64, boxNode *box, insideTable, outOfFlow bool) {
	if pageH <= 0 || insideTable || outOfFlow {
		return
	}

	if keepRange, ok := singleLineKeepRange(res, boxNode, pageH); ok {
		c.keep = append(c.keep, keepRange)
	}
}

// addHeadingKeeps emits one KeepTogether range per heading child: a heading
// keeps with the content that follows it, so a paginator that honors keep
// ranges moves the whole opening group to the next page instead of leaving
// the heading and one line dangling at a page bottom. The range starts at
// the heading top and ends after the heading run (following headings, plus
// intro blocks that lead into the next heading) and the first content block
// that closes the run. Ranges that do not fit one page are ignored by the
// paginator, per the Display.KeepTogether contract.
func (c *breakCollector) addHeadingKeeps(parent *box) {
	if c == nil || parent == nil {
		return
	}

	for idx, child := range parent.children {
		if !isHeadingBox(child) || child.opStart > child.opEnd {
			continue
		}

		end := headingGroupEnd(parent.children, idx)
		if end <= child.y+child.height {
			continue
		}

		c.keep = append(c.keep, [2]float64{child.y, end})
	}
}

// headingGroupEnd returns the bottom edge of the content the heading at
// headingIdx keeps with in its sibling list. The group runs through
// following headings (they open subsections of the same heading) and
// through intro blocks that immediately precede a heading (a lead line
// before a subsection), and closes after the first content block that ends
// the run. Boxes without operations (zero-height break markers, hidden
// elements) neither extend the group nor close it.
func headingGroupEnd(siblings []*box, headingIdx int) float64 {
	heading := siblings[headingIdx]
	end := heading.y + heading.height

	for next := headingIdx + 1; next < len(siblings); next++ {
		block := siblings[next]
		if block.opStart > block.opEnd {
			continue
		}

		if blockBottom := block.y + block.height; blockBottom > end {
			end = blockBottom
		}

		if isHeadingBox(block) || nextOpensHeading(siblings, next+1) {
			continue
		}

		break
	}

	return end
}

// nextOpensHeading reports whether the next sibling box with operations is
// a heading. A block right before one is intro content for the subsection,
// so it extends the keep group instead of closing it.
func nextOpensHeading(siblings []*box, from int) bool {
	for idx := from; idx < len(siblings); idx++ {
		if siblings[idx].opStart > siblings[idx].opEnd {
			continue
		}

		return isHeadingBox(siblings[idx])
	}

	return false
}

// isHeadingBox reports whether the box is an h1-h6 element.
func isHeadingBox(boxNode *box) bool {
	if boxNode == nil || boxNode.node == nil {
		return false
	}

	switch boxNode.node.Name {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	default:
		return false
	}
}

// addTableRowKeeps exports one keep-together range per non-empty table row.
// The range covers the row band plus the row's crossing-checked ops: a
// paginator measures a text op from its baseline (Y) down by its recorded
// line height (H), which pokes below the band, and snaps any op crossing a
// page boundary on its own. Marking the row as straddling there moves the
// whole row instead, keeping text and chrome on the same page.
func (c *breakCollector) addTableRowKeeps(boxNode *box) {
	if boxNode.kind != boxKindTable {
		return
	}

	for rowIdx, top := range boxNode.rowTops {
		if rowIdx >= len(boxNode.rowHeights) {
			return
		}

		rowH := boxNode.rowHeights[rowIdx]
		if rowH <= layoutCoordEpsilon {
			continue
		}

		end := c.rowKeepEnd(boxNode, rowIdx, top, top+rowH)
		c.keep = append(c.keep, [2]float64{top, end})
	}
}

// rowKeepEnd extends a row's keep end to the lowest bottom of the row's
// snap-eligible ops (text, bullet, image, link) whose baseline lies in the
// row band. Ops of a rowspan cell that sit in a later spanned row are below
// the band and stay out: a break between spanned rows is allowed.
func (c *breakCollector) rowKeepEnd(tbl *box, rowIdx int, top, rowBottom float64) float64 {
	end := rowBottom

	if c == nil || rowIdx < 0 || rowIdx >= len(tbl.rows) {
		return end
	}

	first, last := rowOpRange(tbl.rows[rowIdx])
	if first < 0 {
		return end
	}

	if bottom := rowSnapBottom(c.ops, first, last, top, rowBottom); bottom > end {
		end = bottom
	}

	return end
}

// rowOpRange returns the first and last op index covered by the row's cells,
// or -1, -1 when the row has no ops.
func rowOpRange(cells []*box) (int, int) {
	first, last := -1, -1

	for _, cell := range cells {
		if cell == nil || cell.opStart < 0 || cell.opStart > cell.opEnd {
			continue
		}

		if first < 0 || cell.opStart < first {
			first = cell.opStart
		}

		if cell.opEnd > last {
			last = cell.opEnd
		}
	}

	return first, last
}

// rowSnapBottom returns the lowest snapping bottom of the ops in [first,
// last] whose baseline lies in the row band [top, rowBottom]. Ops outside
// the band (for example a rowspan cell continued in a later row) do not
// count.
func rowSnapBottom(ops []Op, first, last int, top, rowBottom float64) float64 {
	// Same coordinate tolerance the paginator uses at page edges.
	const eps = 0.01

	var bottom float64

	for idx := first; idx <= last && idx < len(ops); idx++ {
		paintOp := &ops[idx]
		if paintOp.Y < top-eps || paintOp.Y > rowBottom+eps {
			continue
		}

		if snapBottom := opSnapBottom(paintOp); snapBottom > bottom {
			bottom = snapBottom
		}
	}

	return bottom
}

// opSnapBottom is the bottom a paginator tests against a page boundary when
// it snaps a crossing op. It mirrors the paginator crossing model: text and
// bullets are measured from the baseline down by the line height when one is
// recorded, and other paint kinds use their own box.
func opSnapBottom(paintOp *Op) float64 {
	if paintOp == nil || paintOp.Fixed {
		return 0
	}

	switch paintOp.Kind {
	case OpText, OpBullet, OpImage, OpLinkURI:
		// Snapping kinds: measured from the baseline down below.
	case OpUnknown, OpFillRect, OpStrokeRect, OpLine, OpGridRun, OpPath, opKindNoop:
		return 0
	}

	ink := paintOp.H
	if ink <= 0 && (paintOp.Kind == OpText || paintOp.Kind == OpBullet) {
		ink = paintOp.Size * textLineHeightFactor
	}

	return paintOp.Y + ink
}

// keepOut drops keep ranges a forced break cuts. The paginator shifts each
// range endpoint at or below a forced break independently, so a range that
// starts above a break and ends on or below it comes back stretched and then
// triggers a bogus keep shift. A forced break wins over keep-together.
func (c *breakCollector) keepOut(forced []float64) [][2]float64 {
	if c == nil || len(c.keep) == 0 {
		return nil
	}

	if len(forced) == 0 {
		return c.keep
	}

	out := c.keep[:0]

	for _, keepRange := range c.keep {
		cut := false

		for _, breakY := range forced {
			if keepRange[0] < breakY-0.01 && keepRange[1] >= breakY-0.01 {
				cut = true

				break
			}
		}

		if !cut {
			out = append(out, keepRange)
		}
	}

	return out
}

func (c *breakCollector) forcedOut() []float64 {
	if c == nil {
		return nil
	}

	sort.Float64s(c.forced)

	out := c.forced[:0]
	for _, y := range c.forced {
		if len(out) == 0 || y-out[len(out)-1] > 0.01 {
			out = append(out, y)
		}
	}

	return out
}
