package layout

// Gap decorations: row rules and column rules, painted per css-gaps-1.
//
// One decoration runs along the centerline of one gap. Its painted pieces
// are gap decoration segments: pairs of segment endpoints created at the
// gap bounds and at every gap junction (a crossing gap that appears on at
// least one side of the gap). column-rule-break / row-rule-break control
// whether the segments break where items span across the gap or where
// junctions are not flanked by spanning items on opposing sides.
// column-rule-inset-* / row-rule-inset-* offset the endpoints of each
// segment.

// gapRuleBreakIntersection is the row-rule-break / column-rule-break value
// that also breaks at junctions that are not flanked by spanning items.
const gapRuleBreakIntersection = "intersection"

// gapInsetSet is the four inset values that apply to one decorated axis.
type gapInsetSet struct {
	capStart      ruleInset
	capEnd        ruleInset
	junctionStart ruleInset
	junctionEnd   ruleInset
}

// gapJunction is one crossing-gap interval along a decorated gap.
type gapJunction struct {
	start, end float64 // interval along the decorated axis, layout space
	crossSize  float64 // used crossing gap size, the percentage basis
	flanked    bool    // items on opposing sides span across the junction
}

// gapSpan is one child item interval covering the decorated gap.
type gapSpan struct {
	start, end float64
}

// ruleEndpoint is one segment endpoint on a gap centerline.
type ruleEndpoint struct {
	pos       float64
	start     bool    // start endpoint; false for an end endpoint
	junction  bool    // created at a gap junction
	crossSize float64 // crossing gap size at a junction endpoint
}

// ruleSegment is one painted piece of a gap decoration.
type ruleSegment struct {
	start, end float64
}

// gapRuleGeometry carries everything the segment algorithm reads for one
// decorated gap. bounds is the gap extent along its axis.
type gapRuleGeometry struct {
	bounds     [2]float64
	insets     gapInsetSet
	brk        string
	junctions  []gapJunction
	spans      []gapSpan
	crossRuleW float64 // used crossing rule width for overlap-join
}

// emitFlexRowGapRules paints one horizontal rule between each pair of
// wrapped flex lines when row-gap is positive. Tops and bottoms come from
// the children's final border boxes, read after align-content. That shift
// moves the boxes and does not update placed[i].y0. One line, a
// non-positive row gap, or a nil parent paints nothing. An empty child
// range skips that gap. The rule does not move children or add gap.
func (e *engine) emitFlexRowGapRules(
	parent *box, style ResolvedStyle, placed []flexLinePlace,
	rowGap, contentX, contentW float64,
) {
	if len(placed) <= 1 || rowGap <= 0 || parent == nil {
		return
	}

	for index := range len(placed) - 1 {
		_, bottom, okBottom := flexLineChildSpan(parent, placed[index])
		top, _, okTop := flexLineChildSpan(parent, placed[index+1])

		if !okBottom || !okTop {
			continue
		}

		e.emitRowGapRule(style, contentX, contentW, bottom, top-bottom)
	}

	if !rowRulePaintsAround(style) {
		return
	}

	_, _, okFirst := flexLineChildSpan(parent, placed[0])
	_, _, okLast := flexLineChildSpan(parent, placed[len(placed)-1])

	if !okFirst || !okLast {
		return
	}

	firstTop, _, _ := flexLineChildSpan(parent, placed[0])
	_, lastBottom, _ := flexLineChildSpan(parent, placed[len(placed)-1])

	e.emitRowRuleLine(style, contentX, contentW, firstTop)
	e.emitRowRuleLine(style, contentX, contentW, lastBottom)
}

// flexLineChildSpan reports one flex line's top and bottom from the
// children's final border boxes in [startChild, endChild). Top is the
// minimum y. Bottom is the maximum y+height. ok is false when that range
// is empty.
func flexLineChildSpan(parent *box, line flexLinePlace) (float64, float64, bool) {
	if parent == nil || line.startChild >= line.endChild || line.startChild >= len(parent.children) {
		return 0, 0, false
	}

	end := line.endChild
	if end > len(parent.children) {
		end = len(parent.children)
	}

	boxes := parent.children[line.startChild:end]
	if len(boxes) == 0 {
		return 0, 0, false
	}

	top := boxes[0].y
	bottom := boxes[0].y + boxes[0].height

	for _, child := range boxes[1:] {
		top = min(top, child.y)
		bottom = max(bottom, child.y+child.height)
	}

	return top, bottom, true
}

// emitRowGapRule paints one horizontal rule centered in a row gap.
// gapTop is the y of the top of the gap in paint space. gap is the gap size.
// contentX and contentW are the content box. A zero gap, a none style,
// or a zero width paints nothing.
func (e *engine) emitRowGapRule(style ResolvedStyle, contentX, contentW, gapTop, gap float64) {
	if e == nil || e.noEmit || gap <= 0 || contentW <= 0 {
		return
	}

	if style.RowRuleStyle == "" || style.RowRuleStyle == cssDisplayNone {
		return
	}

	e.emitRowRuleLine(style, contentX, contentW, gapTop+gap*gapCenter)
}

// emitRowRuleLine paints one row rule at y across the content box using
// cap-only geometry: no junctions exist at content edges. Cap insets
// shorten the ends; junction insets and overlap-join have no effect.
func (e *engine) emitRowRuleLine(style ResolvedStyle, contentX, contentW, lineY float64) {
	if e == nil || e.noEmit || contentW <= 0 {
		return
	}

	if style.RowRuleStyle == "" || style.RowRuleStyle == cssDisplayNone {
		return
	}

	ruleW := e.scalePt(style.RowRuleWidth)
	if ruleW <= 0 {
		return
	}

	segments := e.gapRuleSegments(&gapRuleGeometry{
		bounds:     [2]float64{contentX, contentX + contentW},
		insets:     rowRuleInsets(style),
		brk:        style.RowRuleBreak,
		junctions:  nil,
		spans:      nil,
		crossRuleW: 0,
	})

	e.emitRowRuleSegments(style, lineY, segments, ruleW)
}

const rowRuleVisibilityAround = "around"

const rowRuleVisibilityAll = "all"

const gapCenter = 0.5

// rowRulePaintsAround reports whether a rule is also drawn on the content edges.
func rowRulePaintsAround(style ResolvedStyle) bool {
	switch style.RowRuleVisibility {
	case rowRuleVisibilityAround, rowRuleVisibilityAll:
		return true
	default:
		return false
	}
}

// rowRuleInsets is the inset set for row rules.
func rowRuleInsets(style ResolvedStyle) gapInsetSet {
	return gapInsetSet{
		capStart:      style.RowRuleInsetCapStart,
		capEnd:        style.RowRuleInsetCapEnd,
		junctionStart: style.RowRuleInsetJunctionStart,
		junctionEnd:   style.RowRuleInsetJunctionEnd,
	}
}

// columnRuleInsets is the inset set for column rules.
func columnRuleInsets(style ResolvedStyle) gapInsetSet {
	return gapInsetSet{
		capStart:      style.ColumnRuleInsetCapStart,
		capEnd:        style.ColumnRuleInsetCapEnd,
		junctionStart: style.ColumnRuleInsetJunctionStart,
		junctionEnd:   style.ColumnRuleInsetJunctionEnd,
	}
}

// gapRuleSegments computes the painted segments of one gap decoration
// (css-gaps-1: determine gap decoration segments). Endpoints run from the
// gap's start bound to its end bound, with an end endpoint entering and a
// start endpoint exiting each junction. A candidate pair is dropped when
// its band intersects a child item spanning the gap; pairs merge while the
// merged candidate stays clear of such items and, for break intersection,
// stays flanked by spanning items at every junction strictly between its
// endpoints.
func (e *engine) gapRuleSegments(geo *gapRuleGeometry) []ruleSegment {
	endpoints := gapRuleEndpoints(geo)
	if len(endpoints) < gapRuleEndpointPair {
		return nil
	}

	if geo.brk == cssDisplayNone {
		segment := [2]ruleEndpoint{endpoints[0], endpoints[len(endpoints)-1]}

		return []ruleSegment{e.gapRuleOffsetSegment(geo, segment)}
	}

	var segments []ruleSegment

	for len(endpoints) >= 2 {
		current := [2]ruleEndpoint{endpoints[0], endpoints[1]}
		endpoints = endpoints[2:]

		if gapRuleDiscontiguous(geo, current[0].pos, current[1].pos) {
			continue
		}

		for len(endpoints) >= 2 {
			next := [2]ruleEndpoint{endpoints[0], endpoints[1]}
			if gapRuleDiscontiguous(geo, current[0].pos, next[1].pos) {
				break
			}

			if geo.brk == gapRuleBreakIntersection &&
				!gapRuleFlanked(geo, current[0].pos, next[1].pos) {
				break
			}

			current[1] = next[1]
			endpoints = endpoints[2:]
		}

		segments = append(segments, e.gapRuleOffsetSegment(geo, current))
	}

	return segments
}

// gapRuleEndpointPair is one start and one end endpoint: the pair used at
// the gap bounds and at each junction.
const gapRuleEndpointPair = 2

// gapRuleEndpoints orders the segment endpoints of one gap. The list
// alternates start and end endpoints starting from the gap's start bound.
func gapRuleEndpoints(geo *gapRuleGeometry) []ruleEndpoint {
	endpoints := make([]ruleEndpoint, 0, gapRuleEndpointPair+len(geo.junctions)*gapRuleEndpointPair)
	endpoints = append(endpoints, ruleEndpoint{
		pos: geo.bounds[0], start: true, junction: false, crossSize: 0,
	})

	for _, junction := range geo.junctions {
		endpoints = append(endpoints,
			ruleEndpoint{
				pos: junction.start, start: false, junction: true,
				crossSize: junction.crossSize,
			},
			ruleEndpoint{
				pos: junction.end, start: true, junction: true,
				crossSize: junction.crossSize,
			},
		)
	}

	endpoints = append(endpoints, ruleEndpoint{
		pos: geo.bounds[1], start: false, junction: false, crossSize: 0,
	})

	return endpoints
}

// gapRuleDiscontiguous reports whether the band from start to end along the
// gap intersects a child item covering the gap.
func gapRuleDiscontiguous(geo *gapRuleGeometry, start, end float64) bool {
	for _, span := range geo.spans {
		if span.start < end && span.end > start {
			return true
		}
	}

	return false
}

// gapRuleFlanked reports whether every junction strictly between start and
// end is flanked by spanning items on opposing sides.
func gapRuleFlanked(geo *gapRuleGeometry, start, end float64) bool {
	for _, junction := range geo.junctions {
		if start < junction.start && junction.end < end && !junction.flanked {
			return false
		}
	}

	return true
}

// gapRuleOffsetSegment applies the insets to one segment pair. Positive
// start-side insets move the start endpoint toward the end; positive
// end-side insets move the end endpoint toward the start. overlap-join
// extends both endpoint kinds into their junction instead.
func (e *engine) gapRuleOffsetSegment(
	geo *gapRuleGeometry, segment [2]ruleEndpoint,
) ruleSegment {
	start := segment[0].pos + e.gapRuleInsetOffset(geo, segment[0], true)
	end := segment[1].pos - e.gapRuleInsetOffset(geo, segment[1], false)

	return ruleSegment{start: start, end: end}
}

// gapRuleInsetPercentDivisor converts a percentage inset value to a fraction.
const gapRuleInsetPercentDivisor = 100

// gapRuleInsetOffset returns the distance the given endpoint moves before
// the direction sign (plus for start endpoints, minus for end endpoints)
// is applied. Junction endpoints read the junction insets, cap endpoints
// the cap insets. Percentages resolve against the crossing gap size at
// junctions and 0 elsewhere; overlap-join extends into the junction by
// half the crossing gap plus half the used crossing rule width.
func (e *engine) gapRuleInsetOffset(
	geo *gapRuleGeometry, endpoint ruleEndpoint, isStart bool,
) float64 {
	inset := geo.insets.capStart
	if !isStart {
		inset = geo.insets.capEnd
	}

	if endpoint.junction {
		if isStart {
			inset = geo.insets.junctionStart
		} else {
			inset = geo.insets.junctionEnd
		}
	}

	switch {
	case inset.OverlapJoin:
		if !endpoint.junction {
			return 0
		}

		return -(endpoint.crossSize/2 + geo.crossRuleW/2)
	case inset.Percent:
		if !endpoint.junction {
			return 0
		}

		return endpoint.crossSize * inset.Value / gapRuleInsetPercentDivisor
	default:
		return e.scalePt(inset.Value)
	}
}

// gapRuleColor returns the used rule color: the rule-specific color when
// set, otherwise the element color.
func gapRuleColor(base, override [3]float64, overrideSet bool) [3]float64 {
	if overrideSet {
		return override
	}

	return base
}

// emitRowRuleSegments paints the horizontal segments of one row rule.
func (e *engine) emitRowRuleSegments(
	style ResolvedStyle, lineY float64, segments []ruleSegment, ruleW float64,
) {
	col := gapRuleColor(style.Color, style.RowRuleColor, style.RowRuleColorSet)

	for _, segment := range segments {
		width := segment.end - segment.start
		if width <= 0 {
			continue
		}

		e.emitBorderLine(
			segment.start, lineY, width, 0, ruleW, style.RowRuleStyle, col[0], col[1], col[2],
		)
	}
}

// paintGridGapRules paints column lines from the column-rule fields and row
// lines from the row-rule fields. rule-overlap chooses which line is later
// in the display list. column-over-row paints the column line last.
func (e *engine) paintGridGapRules(
	style ResolvedStyle, placed []gridCell, contentX, contentW float64,
	cols []float64, columnGap float64,
	rowYs, rows []float64, posY float64,
) {
	if e == nil || len(rowYs) == 0 || len(rowYs) != len(rows) {
		return
	}

	paintColumnsFirst := style.RuleOverlap != "column-over-row"
	if paintColumnsFirst {
		e.paintGridColumnRules(style, placed, contentX, cols, columnGap, rowYs, rows, posY)
	}

	e.paintGridRowRules(style, placed, contentX, contentW, cols, columnGap, rowYs, rows, posY)

	if !paintColumnsFirst {
		e.paintGridColumnRules(style, placed, contentX, cols, columnGap, rowYs, rows, posY)
	}
}

func (e *engine) paintGridRowRules(
	style ResolvedStyle, placed []gridCell, contentX, contentW float64,
	cols []float64, columnGap float64, rowYs, rows []float64, posY float64,
) {
	if rowRulePaintsAround(style) {
		top := posY + rowYs[0]
		bottom := posY + rowYs[len(rows)-1] + rows[len(rows)-1]

		e.emitRowRuleLine(style, contentX, contentW, top)
		e.emitRowRuleLine(style, contentX, contentW, bottom)
	}

	for index := range len(rows) - 1 {
		gapTop := posY + rowYs[index] + rows[index]
		gap := posY + rowYs[index+1] - gapTop

		if gap <= 0 {
			continue
		}

		e.emitGridRowGapRule(
			style, placed, contentX, contentW, cols, columnGap,
			index, gapTop+gap*gapCenter,
		)
	}
}

// emitGridRowGapRule paints the row rule of one grid row gap with the
// junctions and item spans of that gap.
func (e *engine) emitGridRowGapRule(
	style ResolvedStyle, placed []gridCell, contentX, contentW float64,
	cols []float64, columnGap float64, gapIndex int, lineY float64,
) {
	if e == nil || e.noEmit || contentW <= 0 {
		return
	}

	if style.RowRuleStyle == "" || style.RowRuleStyle == cssDisplayNone {
		return
	}

	ruleW := e.scalePt(style.RowRuleWidth)
	if ruleW <= 0 {
		return
	}

	segments := e.gapRuleSegments(&gapRuleGeometry{
		bounds:     [2]float64{contentX, contentX + contentW},
		insets:     rowRuleInsets(style),
		brk:        style.RowRuleBreak,
		junctions:  gridRowGapJunctions(placed, cols, columnGap, contentX, gapIndex),
		spans:      gridRowGapSpans(placed, cols, columnGap, contentX, gapIndex),
		crossRuleW: e.scalePt(style.ColumnRuleWidth),
	})

	e.emitRowRuleSegments(style, lineY, segments, ruleW)
}

func (e *engine) paintGridColumnRules(
	style ResolvedStyle, placed []gridCell, contentX float64, cols []float64, columnGap float64,
	rowYs, rows []float64, posY float64,
) {
	if style.ColumnRuleStyle == "" || style.ColumnRuleStyle == cssDisplayNone ||
		columnGap <= 0 || len(cols) < 2 || e == nil || e.noEmit {
		return
	}

	ruleW := e.scalePt(style.ColumnRuleWidth)
	if ruleW <= 0 {
		return
	}

	top := posY + rowYs[0]
	bottom := posY + rowYs[len(rows)-1] + rows[len(rows)-1]

	if bottom-top <= 0 {
		return
	}

	insets := columnRuleInsets(style)
	crossRuleW := e.scalePt(style.RowRuleWidth)

	cursor := contentX

	for index := range len(cols) - 1 {
		cursor += cols[index]
		center := cursor + columnGap*gapCenter
		cursor += columnGap

		segments := e.gapRuleSegments(&gapRuleGeometry{
			bounds:     [2]float64{top, bottom},
			insets:     insets,
			brk:        style.ColumnRuleBreak,
			junctions:  gridColumnGapJunctions(placed, rowYs, rows, posY, index),
			spans:      gridColumnGapSpans(placed, rowYs, rows, posY, index),
			crossRuleW: crossRuleW,
		})

		e.emitColumnRuleSegments(style, center, segments, ruleW)
	}
}

// emitColumnRuleSegments paints the vertical segments of one column rule.
func (e *engine) emitColumnRuleSegments(
	style ResolvedStyle, center float64, segments []ruleSegment, ruleW float64,
) {
	col := gapRuleColor(style.Color, style.ColumnRuleColor, style.ColumnRuleColorSet)

	for _, segment := range segments {
		height := segment.end - segment.start
		if height <= 0 {
			continue
		}

		e.emitBorderLine(
			center, segment.start, 0, height, ruleW, style.ColumnRuleStyle,
			col[0], col[1], col[2],
		)
	}
}

// gridRowGapJunctions lists the junctions of the row gap between grid rows
// gapIndex and gapIndex+1. A column gap forms a junction when it appears in
// either adjacent row: no child in that row spans across the column gap.
func gridRowGapJunctions(
	placed []gridCell, cols []float64, columnGap, contentX float64, gapIndex int,
) []gapJunction {
	if columnGap <= 0 || len(cols) < 2 {
		return nil
	}

	var junctions []gapJunction

	cursor := contentX

	for index := range len(cols) - 1 {
		cursor += cols[index]
		start := cursor
		end := cursor + columnGap
		cursor += columnGap

		if gridColumnGapVisibleInRow(placed, index, gapIndex) ||
			gridColumnGapVisibleInRow(placed, index, gapIndex+1) {
			junctions = append(junctions, gapJunction{
				start:     start,
				end:       end,
				crossSize: columnGap,
				flanked:   gridColumnGapFlanked(placed, index, gapIndex),
			})
		}
	}

	return junctions
}

// gridColumnGapVisibleInRow reports whether the column gap between columns
// columnGapIndex and columnGapIndex+1 appears in row rowIndex, meaning no
// child in that row spans across the gap.
func gridColumnGapVisibleInRow(placed []gridCell, columnGapIndex, rowIndex int) bool {
	for _, cell := range placed {
		if cell.row <= rowIndex && cell.row+cell.rowSpan-1 >= rowIndex &&
			cell.col <= columnGapIndex && cell.col+cell.colSpan-1 >= columnGapIndex+1 {
			return false
		}
	}

	return true
}

// gridColumnGapFlanked reports whether children on both sides of the row gap
// between rows rowIndex and rowIndex+1 span across the column gap between
// columns columnGapIndex and columnGapIndex+1.
func gridColumnGapFlanked(placed []gridCell, columnGapIndex, rowIndex int) bool {
	above := false
	below := false

	for _, cell := range placed {
		if cell.col > columnGapIndex || cell.col+cell.colSpan-1 < columnGapIndex+1 {
			continue
		}

		if cell.row <= rowIndex && rowIndex <= cell.row+cell.rowSpan-1 {
			above = true
		}

		if cell.row <= rowIndex+1 && rowIndex+1 <= cell.row+cell.rowSpan-1 {
			below = true
		}
	}

	return above && below
}

// gridRowGapSpans lists the children that cover the row gap between rows
// gapIndex and gapIndex+1: children whose row range includes both rows.
func gridRowGapSpans(
	placed []gridCell, cols []float64, columnGap, contentX float64, gapIndex int,
) []gapSpan {
	spans := make([]gapSpan, 0, len(placed))

	for _, cell := range placed {
		if cell.row > gapIndex || cell.row+cell.rowSpan-1 < gapIndex+1 {
			continue
		}

		start := gridColumnTrackStart(contentX, cols, columnGap, cell.col)
		last := cell.col + cell.colSpan - 1
		end := gridColumnTrackStart(contentX, cols, columnGap, last) + cols[last]

		spans = append(spans, gapSpan{start: start, end: end})
	}

	return spans
}

// gridColumnGapJunctions lists the junctions of the column gap between
// columns columnGapIndex and columnGapIndex+1. A row gap forms a junction
// when it appears in either adjacent column: no child spanning across the
// row gap covers that column.
func gridColumnGapJunctions(
	placed []gridCell, rowYs, rows []float64, posY float64, columnGapIndex int,
) []gapJunction {
	var junctions []gapJunction

	for index := range len(rows) - 1 {
		gapTop := posY + rowYs[index] + rows[index]
		gap := posY + rowYs[index+1] - gapTop

		if gap <= 0 {
			continue
		}

		if gridRowGapVisibleInColumn(placed, index, columnGapIndex) ||
			gridRowGapVisibleInColumn(placed, index, columnGapIndex+1) {
			junctions = append(junctions, gapJunction{
				start:     gapTop,
				end:       gapTop + gap,
				crossSize: gap,
				flanked:   gridRowGapFlanked(placed, index, columnGapIndex),
			})
		}
	}

	return junctions
}

// gridRowGapVisibleInColumn reports whether the row gap between rows
// rowGapIndex and rowGapIndex+1 appears in column columnIndex, meaning no
// child spanning across the gap covers that column.
func gridRowGapVisibleInColumn(placed []gridCell, rowGapIndex, columnIndex int) bool {
	for _, cell := range placed {
		if cell.col <= columnIndex && cell.col+cell.colSpan-1 >= columnIndex &&
			cell.row <= rowGapIndex && cell.row+cell.rowSpan-1 >= rowGapIndex+1 {
			return false
		}
	}

	return true
}

// gridRowGapFlanked reports whether children to the left and right of the
// column gap between columns columnGapIndex and columnGapIndex+1 span
// across the row gap between rows rowGapIndex and rowGapIndex+1.
func gridRowGapFlanked(placed []gridCell, rowGapIndex, columnGapIndex int) bool {
	left := false
	right := false

	for _, cell := range placed {
		if cell.row > rowGapIndex || cell.row+cell.rowSpan-1 < rowGapIndex+1 {
			continue
		}

		left = left || cell.col <= columnGapIndex && columnGapIndex <= cell.col+cell.colSpan-1
		right = right ||
			cell.col <= columnGapIndex+1 && columnGapIndex+1 <= cell.col+cell.colSpan-1
	}

	return left && right
}

// gridColumnGapSpans lists the children that cover the column gap between
// columns columnGapIndex and columnGapIndex+1: children spanning both.
func gridColumnGapSpans(
	placed []gridCell, rowYs, rows []float64, posY float64, columnGapIndex int,
) []gapSpan {
	spans := make([]gapSpan, 0, len(placed))

	for _, cell := range placed {
		if cell.col > columnGapIndex || cell.col+cell.colSpan-1 < columnGapIndex+1 {
			continue
		}

		start := posY + rowYs[cell.row]
		last := cell.row + cell.rowSpan - 1
		end := posY + rowYs[last] + rows[last]

		spans = append(spans, gapSpan{start: start, end: end})
	}

	return spans
}

// gridColumnTrackStart returns the layout x where column index col begins.
func gridColumnTrackStart(contentX float64, cols []float64, columnGap float64, col int) float64 {
	start := contentX

	for index := range col {
		start += cols[index] + columnGap
	}

	return start
}
