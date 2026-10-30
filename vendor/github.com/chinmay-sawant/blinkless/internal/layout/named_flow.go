// Named flows (css-regions-1 lite): flow-into, flow-from, region-fragment.
//
// Decision (plans/v0.0.3/regions/regions-checklist.md, Phase 0): a region is a
// named-flow move on one canvas, not a drawing-list comment and not a page
// split. An element with flow-into names its content (or the element itself,
// with the element keyword). A box with flow-from publishes its content box
// as the region that receives that named flow: the source's children are laid
// out inside the region box, so their paint ops use the region's coordinates
// and stop appearing at the source's old flow position. When a second box
// names the same flow, the content that does not fit the first region's
// content box is translated into the second; if no second region follows, the
// remainder is never painted. Nothing here allocates Result.Pages or invents a
// page: normal flow stays one continuous display list
// (internal/layout/css_behavior_backface_breaks_test.go pins that
// break-before:page does not move a box).
//
// Wire-up: flowChildren (internal/layout/layout_flow.go) calls
// namedFlowBegin/namedFlowFinish around its child loop; the region replaces
// its own children with the source's and the source's own flow walk returns
// no children. An element source is built inside the region under a style
// override that clears flow-into so the source's own box (borders included)
// paints inside the region.
//
// Boundary (honest): this is one canvas. Evidence is the drawing list
// (internal/layout/css_behavior_named_flow_test.go). Region chains fragment at
// op granularity: whole child boxes move as units, a straddling child under
// region-fragment: auto donates only its ops at or below the cut, and its
// deferred background/border stays with the first region. Shape-outside
// contours, region styling (::region), and named-flow events do not exist
// here.
package layout

import (
	"math"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
	"github.com/chinmay-sawant/blinkless/internal/html"
)

// Named-flow property names and keyword constants.
const (
	flowIntoPropName       = "flow-into"
	flowFromPropName       = "flow-from"
	regionFragmentPropName = "region-fragment"

	regionFragmentAuto  = "auto"
	regionFragmentBreak = "break"
	flowKeywordContent  = "content"
	flowKeywordElement  = "element"
	flowKeywordDefault  = "default"
)

// namedFlow is one flow ident collected for the current Layout run.
type namedFlow struct {
	source  *html.Node
	element bool
	// placed is set once a region has laid the source content out.
	placed bool
	// pending holds the ops that overflowed the previous region in the chain
	// so the next region with the same flow-from can adopt them.
	pending *namedFlowOverflow
}

// namedFlowOverflow is the remainder of one region's named-flow content,
// expressed in canvas coordinates so the next region can translate it to its
// own content origin. ops are individual op indices (a sliced straddler);
// boxes move as whole units (their op range, deferred chrome, and geometry).
// top is the block-start of the first moved item, height the moved block
// size.
type namedFlowOverflow struct {
	boxes  []*box
	ops    []int
	x      float64
	top    float64
	height float64
}

// namedFlowFrame carries one region's in-progress named-flow work from the
// flowChildren pre-hook to its post-hook.
type namedFlowFrame struct {
	flow           *namedFlow
	contentOpStart int
	childStart     int
	contentX       float64
	cutY           float64
	adopted        bool
	contentBoxes   []*box
	contentOps     []int
	// sourceOverride is true while an element source is built inside this
	// region under a flow-into-clearing style override.
	sourceOverride bool
}

// applyNamedFlowProps owns flow-into, flow-from, and region-fragment. It is
// registered in styleGroups (style_cascade.go). Invalid declarations leave
// the stored value alone; the acceptance gate in style_value_accept.go keeps
// them from winning the cascade in the first place.
func applyNamedFlowProps(
	style *ResolvedStyle, prop, value string, _ float64, _ *styleContext, _ *ResolvedStyle, _ bool,
) bool {
	switch prop {
	case flowIntoPropName:
		ident, element, ok := parseFlowInto(value)
		if !ok {
			return true
		}

		style.FlowInto = ident
		style.FlowIntoElement = element
	case flowFromPropName:
		ident, ok := parseFlowFrom(value)
		if !ok {
			return true
		}

		style.FlowFrom = ident
	case regionFragmentPropName:
		used, ok := parseRegionFragment(value)
		if !ok {
			return true
		}

		style.RegionFragment = used
	default:
		return false
	}

	return true
}

// parseFlowInto parses flow-into: none | <custom-ident> [element|content]?
// (the second keyword defaults to content). ok is false only for an invalid
// declaration; a CSS-wide keyword resets to the initial none and still
// reports ok.
func parseFlowInto(value string) (string, bool, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return "", false, false
	}

	if cssWideKeyword(trimmed) {
		return "", false, true
	}

	if trimmed == cssDisplayNone {
		return "", false, true
	}

	tokens := strings.Fields(trimmed)
	if len(tokens) > twoTokens {
		return "", false, false
	}

	ident := tokens[0]
	if !flowIdentUsable(ident) {
		return "", false, false
	}

	element := false

	if len(tokens) == twoTokens {
		switch tokens[1] {
		case flowKeywordElement:
			element = true
		case flowKeywordContent:
			element = false
		default:
			return "", false, false
		}
	}

	return ident, element, true
}

// parseFlowFrom parses flow-from: none | <custom-ident>.
func parseFlowFrom(value string) (string, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return "", false
	}

	if cssWideKeyword(trimmed) || trimmed == cssDisplayNone {
		return "", true
	}

	if !flowIdentUsable(trimmed) {
		return "", false
	}

	return trimmed, true
}

// parseRegionFragment parses region-fragment: auto | break. auto stores "".
func parseRegionFragment(value string) (string, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return "", false
	}

	if cssWideKeyword(trimmed) || trimmed == regionFragmentAuto {
		return "", true
	}

	if trimmed == regionFragmentBreak {
		return regionFragmentBreak, true
	}

	return "", false
}

// flowIdentUsable reports a usable <custom-ident>: a valid CSS ident that is
// neither a CSS-wide keyword, none, nor default.
func flowIdentUsable(ident string) bool {
	if !css.IsIdentToken(ident) {
		return false
	}

	switch ident {
	case cssDisplayNone, flowKeywordDefault:
		return false
	}

	return !cssWideKeyword(ident)
}

// namedFlowDeclarationValueAccepted is the cascade/@supports gate for the
// named-flow properties (shared with the appliers' parsers).
func namedFlowDeclarationValueAccepted(prop, value string) (bool, bool) {
	switch prop {
	case flowIntoPropName:
		_, _, ok := parseFlowInto(value)

		return ok, true
	case flowFromPropName:
		_, ok := parseFlowFrom(value)

		return ok, true
	case regionFragmentPropName:
		_, ok := parseRegionFragment(value)

		return ok, true
	default:
		return false, false
	}
}

// namedFlowChildMentions reports whether any element child declares a
// named-flow property, so the registry scan only runs for documents that use
// regions at all.
func namedFlowChildMentions(eng *engine, children []*html.Node) bool {
	for _, child := range children {
		if child.Type != html.ElementNode {
			continue
		}

		style := eng.stylePtr(child)
		if style != nil && (style.FlowInto != "" || style.FlowFrom != "") {
			return true
		}
	}

	return false
}

// namedFlowRoot climbs to the top of the parsed tree (document node or the
// detached subtree root the caller passed to Layout).
func namedFlowRoot(node *html.Node) *html.Node {
	for node != nil && node.Parent != nil {
		node = node.Parent
	}

	return node
}

// scanNamedFlows registers every flow source in the document once per Layout
// run. The scan lets a region that appears before its source still receive
// the content; without it, registration happens only when the source's own
// parent flows.
func (e *engine) scanNamedFlows(root *html.Node) {
	if root == nil {
		return
	}

	if e.namedFlows == nil {
		e.namedFlows = make(map[string]*namedFlow)
	}

	root.Walk(func(node *html.Node) {
		if node.Type != html.ElementNode {
			return
		}

		style := e.stylePtr(node)
		if style == nil || style.FlowInto == "" {
			return
		}

		if _, exists := e.namedFlows[style.FlowInto]; exists {
			return
		}

		e.namedFlows[style.FlowInto] = &namedFlow{ //nolint:exhaustruct // placed/pending start zero
			source:  node,
			element: style.FlowIntoElement,
		}
	})
}

// namedFlowBegin is the flowChildren pre-hook. It withholds a source's
// children, replaces a region's children with the named flow's content, and
// captures the op/child cursors and the region cut for the post-hook.
//
//nolint:cyclop // one dispatch over the scan, source, and region paths
func (e *engine) namedFlowBegin(
	parent *box, children *[]*html.Node, sty ResolvedStyle, contentX, posY, curY float64,
) *namedFlowFrame {
	if e.noEmit {
		// Measure passes must not consume flow state (placed/pending); the
		// real build sees the content. A measured region sizes as empty.
		return nil
	}

	if !e.namedFlowScanDone &&
		(sty.FlowInto != "" || sty.FlowFrom != "" || namedFlowChildMentions(e, *children)) {
		e.scanNamedFlows(namedFlowRoot(namedFlowSeedNode(parent, *children)))
		e.namedFlowScanDone = true
	}

	// A source's own flow walk keeps its children out of normal flow. The
	// element keyword is authorised by its style override clearing flow-into
	// while the source builds inside its region.
	if sty.FlowInto != "" {
		*children = nil

		return nil
	}

	if sty.FlowFrom == "" {
		if e.namedFlows != nil {
			*children = e.namedFlowFilterChildren(*children)
		}

		return nil
	}

	flow := e.namedFlows[sty.FlowFrom]
	if flow == nil {
		*children = nil

		return nil
	}

	frame := &namedFlowFrame{ //nolint:exhaustruct // intentional zero fields
		flow:       flow,
		childStart: -1,
		contentX:   contentX,
	}

	if flow.placed {
		*children = nil
		frame.adopted = true
	} else {
		flow.placed = true
		e.namedFlowRegionChildren(children, flow, frame)
	}

	if parent != nil {
		frame.childStart = len(parent.children)
	}

	frame.contentOpStart = len(e.ops)

	if limit, ok := e.namedFlowContentLimit(sty); ok {
		frame.cutY = posY + curY + limit
	}

	return frame
}

// namedFlowRegionChildren installs the region's content: the source's
// children for the content keyword, or the source box itself (built under a
// flow-into-clearing override) for the element keyword.
func (e *engine) namedFlowRegionChildren(
	children *[]*html.Node, flow *namedFlow, frame *namedFlowFrame,
) {
	if !flow.element {
		*children = flow.source.Children

		return
	}

	*children = []*html.Node{flow.source}
	override := *e.stylePtr(flow.source)
	override.FlowInto = ""
	override.FlowIntoElement = false

	e.styleOverrides = append(e.styleOverrides, styleOverride{node: flow.source, style: &override})
	frame.sourceOverride = true
}

// namedFlowFilterChildren drops element-keyword sources from a parent's child
// list: the region builds those boxes instead, and with no region they are
// not painted at all. Content-keyword sources stay (their own flow walk
// returns no children, leaving an empty source box in flow).
func (e *engine) namedFlowFilterChildren(children []*html.Node) []*html.Node {
	if !namedFlowHasElementSource(e, children) {
		return children
	}

	out := make([]*html.Node, 0, len(children))

	for _, child := range children {
		if !namedFlowIsElementSource(e, child) {
			out = append(out, child)
		}
	}

	return out
}

// namedFlowHasElementSource reports whether any child is an element-keyword
// flow source.
func namedFlowHasElementSource(e *engine, children []*html.Node) bool {
	for _, child := range children {
		if namedFlowIsElementSource(e, child) {
			return true
		}
	}

	return false
}

// namedFlowIsElementSource reports whether one node is an element-keyword
// flow source.
func namedFlowIsElementSource(e *engine, child *html.Node) bool {
	if child.Type != html.ElementNode {
		return false
	}

	st := e.stylePtr(child)

	return st != nil && st.FlowInto != "" && st.FlowIntoElement
}

// namedFlowSeedNode picks any node that can reach the parsed root.
func namedFlowSeedNode(parent *box, children []*html.Node) *html.Node {
	if parent != nil && parent.node != nil {
		return parent.node
	}

	for _, child := range children {
		if child != nil {
			return child
		}
	}

	return nil
}

// namedFlowContentLimit returns the region's definite content-box block size
// (a clamped height), used as the cut line for chaining. ok is false when the
// region grows with its content.
func (e *engine) namedFlowContentLimit(sty ResolvedStyle) (float64, bool) {
	if sty.Height < 0 {
		return 0, false
	}

	limit := e.scalePt(sty.Height)

	if sty.BoxSizing == borderBox {
		limit -= e.scalePt(sty.PaddingTop+sty.PaddingBottom) +
			e.scalePt(sty.BorderTop.Width+sty.BorderBottom.Width)
	}

	if limit <= 0 {
		return 0, false
	}

	return limit, true
}

// namedFlowFinish is the flowChildren post-hook: it adopts a previous
// region's overflow, then cuts this region's content at its definite height
// so the next region (same flow-from) can continue it.
func (e *engine) namedFlowFinish(
	frame *namedFlowFrame, parent *box, posY, curY float64,
) float64 {
	if frame == nil {
		return curY
	}

	if frame.sourceOverride {
		e.popStyleOverride()
	}

	if frame.flow == nil {
		return curY
	}

	// Adopt the remainder of the previous region in the chain. The region
	// flowed no children of its own, so its content top is the current end.
	if frame.flow.pending != nil {
		curY += e.adoptNamedFlowOverflow(frame, frame.flow.pending, frame.contentX, posY+curY)
		frame.flow.pending = nil
	}

	if frame.cutY > 0 {
		moved := e.cutNamedFlowOverflow(frame, parent, frame.cutY)
		frame.flow.pending = moved
	}

	return curY
}

// adoptNamedFlowOverflow translates a previous region's remainder into this
// region's content origin and returns its block size.
func (e *engine) adoptNamedFlowOverflow(
	frame *namedFlowFrame, pending *namedFlowOverflow, contentX, contentTop float64,
) float64 {
	offsetX := contentX - pending.x
	offsetY := contentTop - pending.top

	for _, boxNode := range pending.boxes {
		e.shiftBoxOps(boxNode, offsetX, offsetY)
		boxNode.x += offsetX
		boxNode.y += offsetY
	}

	for _, idx := range pending.ops {
		if idx >= 0 && idx < len(e.ops) {
			e.ops[idx].X += offsetX
			e.ops[idx].Y += offsetY
		}
	}

	frame.adopted = true
	frame.contentBoxes = pending.boxes
	frame.contentOps = pending.ops

	return pending.height
}

// cutNamedFlowOverflow slices one region's named-flow content at cutY.
// Whole boxes below the cut move as units; a straddling child under
// region-fragment: break moves whole, otherwise only its ops at or below the
// cut are collected. Nil means nothing overflowed.
func (e *engine) cutNamedFlowOverflow(
	frame *namedFlowFrame, parent *box, cutY float64,
) *namedFlowOverflow {
	pending := &namedFlowOverflow{ //nolint:exhaustruct // intentional zero fields
		x:   frame.contentX,
		top: math.MaxFloat64,
	}

	owned := make(map[int]*box)
	movedWhole := make(map[*box]bool)

	for _, boxNode := range namedFlowOverflowCandidates(frame, parent) {
		if boxNode == nil || boxNode.opEnd < boxNode.opStart {
			continue
		}

		for idx := boxNode.opStart; idx <= boxNode.opEnd; idx++ {
			owned[idx] = boxNode
		}

		if !e.namedFlowBoxMovesWhole(boxNode, cutY) {
			continue
		}

		pending.boxes = append(pending.boxes, boxNode)
		movedWhole[boxNode] = true
		pending.top = min(pending.top, boxNode.y)
		pending.height = maxY(pending.height, boxNode.y+boxNode.height)
	}

	e.collectNamedFlowOverflowOps(frame, owned, movedWhole, cutY, pending)

	if len(pending.boxes) == 0 && len(pending.ops) == 0 {
		return nil
	}

	pending.height -= pending.top
	if pending.height < 0 {
		pending.height = 0
	}

	return pending
}

// namedFlowOverflowCandidates returns the child boxes a cut considers: the
// adopted content boxes when this region adopted a previous remainder, or the
// parent's children from the region's own child start.
func namedFlowOverflowCandidates(frame *namedFlowFrame, parent *box) []*box {
	switch {
	case frame.adopted:
		return frame.contentBoxes
	case parent != nil && frame.childStart >= 0:
		return parent.children[frame.childStart:]
	default:
		return nil
	}
}

// namedFlowBoxMovesWhole reports whether a candidate child travels to the
// next region as one unit: it starts at or below the cut, or it straddles the
// cut with region-fragment: break (remaining space shorter than the child).
func (e *engine) namedFlowBoxMovesWhole(boxNode *box, cutY float64) bool {
	if boxNode.y >= cutY {
		return true
	}

	if boxNode.y+boxNode.height <= cutY {
		return false
	}

	return boxNode.style != nil && boxNode.style.RegionFragment == regionFragmentBreak
}

// collectNamedFlowOverflowOps collects the individually moved ops: ops inside
// a straddling child at or below the cut, plus loose ops (text runs emitted
// directly by the region) at or below the cut. Ops of wholly moved boxes
// travel with their box and are skipped.
func (e *engine) collectNamedFlowOverflowOps(
	frame *namedFlowFrame, owned map[int]*box, movedWhole map[*box]bool,
	cutY float64, pending *namedFlowOverflow,
) {
	consider := func(idx int) {
		if idx < 0 || idx >= len(e.ops) || e.ops[idx].Kind == opKindNoop {
			return
		}

		if e.ops[idx].Y < cutY {
			return
		}

		pending.ops = append(pending.ops, idx)
		// A text op's Y is its baseline; the line box starts one line height
		// above it, so the remainder's top keeps the line inside the next
		// region.
		pending.top = min(pending.top, e.ops[idx].Y-e.ops[idx].H)
		pending.height = maxY(pending.height, e.ops[idx].Y+e.ops[idx].H)
	}

	if frame.adopted {
		// Adopted whole boxes are covered by their op ranges; the rest is
		// the loose op list the previous cut produced.
		for _, idx := range frame.contentOps {
			consider(idx)
		}

		return
	}

	for idx := frame.contentOpStart; idx < len(e.ops); idx++ {
		if e.namedFlowOpTravelsWithBox(owned, movedWhole, idx, cutY) {
			continue
		}

		consider(idx)
	}
}

// namedFlowOpTravelsWithBox reports whether op idx belongs to a box that
// already travels to the next region: a wholly moved box, or a straddling
// child whose ops end at or above the cut.
func (e *engine) namedFlowOpTravelsWithBox(
	owned map[int]*box, movedWhole map[*box]bool, idx int, cutY float64,
) bool {
	boxNode, boxed := owned[idx]
	if !boxed {
		return false
	}

	if movedWhole[boxNode] {
		return true
	}

	return boxNode.y+boxNode.height <= cutY
}

// finishNamedFlows hides the remainders of flows that never reached another
// region: the content past the last region's box is not painted (it does not
// invent a page). Runs before the chrome merge so op indices are still valid.
func (e *engine) finishNamedFlows() {
	for _, flow := range e.namedFlows {
		if flow.pending == nil {
			continue
		}

		for _, idx := range flow.pending.ops {
			if idx >= 0 && idx < len(e.ops) {
				e.ops[idx].Kind = opKindNoop
			}
		}

		for _, boxNode := range flow.pending.boxes {
			e.hideBoxOps(boxNode)
		}

		flow.pending = nil
	}
}

// hideBoxOps deactivates every op in one box's range.
func (e *engine) hideBoxOps(boxNode *box) {
	if boxNode == nil || boxNode.opEnd < boxNode.opStart {
		return
	}

	for idx := boxNode.opStart; idx <= boxNode.opEnd && idx < len(e.ops); idx++ {
		e.ops[idx].Kind = opKindNoop
	}
}
