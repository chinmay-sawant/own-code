package layout

import (
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

// Root margin accounting. Chrome places the body's border edge at the
// collapsed top margin shared by body and its first in-flow block child, and
// lets the last child's collapsed bottom margin extend the html element.
// absorbRootBodyTopMargin applies the top collapse before the body flows its
// children, so the body box and every descendant share the collapsed edge.
// extendRootHTMLHeight applies the bottom escape after the body flow.

// marginCollapsesThrough reports whether a box's margin can collapse through
// one edge: the edge has no padding or border, and the box is not a BFC root
// or a layout-containment box.
func (e *engine) marginCollapsesThrough(style ResolvedStyle, top bool) bool {
	if establishesBFC(style) || containsLayout(style) {
		return false
	}

	if top {
		return style.PaddingTop == 0 && style.BorderTop.Width == 0
	}

	return style.PaddingBottom == 0 && style.BorderBottom.Width == 0
}

// applyRootBoxMargins applies the root html margin-collapse reporting
// adjustments: a collapsed last-child bottom margin extends the html height
// (Chrome root geometry). The top side is handled before the body flows its
// children by absorbRootBodyTopMargin.
func (e *engine) applyRootBoxMargins(node *html.Node, boxNode *box, curY float64) float64 {
	if node.Name == htmlRootName {
		return e.extendRootHTMLHeight(boxNode, curY)
	}

	return curY
}

// inFlowBlockChild classifies one child for first/last in-flow block lookup:
// a block child is returned, a skipped child (display:none, out-of-flow,
// float, non-element) yields (nil, true) to keep scanning, and inline content
// yields (nil, false) because it stops margin collapse.
func (e *engine) inFlowBlockChild(child *html.Node) (*html.Node, bool) {
	if child.Type == html.TextNode {
		if strings.TrimSpace(child.Text) != "" {
			return nil, false
		}

		return nil, true
	}

	if child.Type != html.ElementNode {
		return nil, true
	}

	childStyle := e.stylePtr(child)
	if childStyle.Display == cssDisplayNone || isOutOfFlowNode(child, childStyle) ||
		isFlowFloat(child, childStyle) {
		return nil, true
	}

	if e.isInlineChild(child) {
		return nil, false
	}

	return child, true
}

// firstInFlowBlockChild returns the first in-flow block-level element child of
// parent. It returns nil when the first in-flow content is inline, which
// prevents parent/first-child margin collapse.
func (e *engine) firstInFlowBlockChild(parent *html.Node) *html.Node {
	if parent == nil {
		return nil
	}

	for _, child := range parent.Children {
		block, keepScanning := e.inFlowBlockChild(child)
		if !keepScanning {
			return nil
		}

		if block != nil {
			return block
		}
	}

	return nil
}

// lastInFlowBlockChild returns the last in-flow block-level element child of
// parent, or nil when the last in-flow content is inline.
func (e *engine) lastInFlowBlockChild(parent *html.Node) *html.Node {
	if parent == nil {
		return nil
	}

	for idx := len(parent.Children) - 1; idx >= 0; idx-- {
		block, keepScanning := e.inFlowBlockChild(parent.Children[idx])
		if !keepScanning {
			return nil
		}

		if block != nil {
			return block
		}
	}

	return nil
}

// collapsedTopThrough returns the top margin that collapses through node's
// top edge: the max of its own top margin and, when the edge has no border or
// padding, its first in-flow block child's chain.
func (e *engine) collapsedTopThrough(node *html.Node, style ResolvedStyle) float64 {
	margin := e.scalePt(style.MarginTop)
	if !e.marginCollapsesThrough(style, true) {
		return margin
	}

	child := e.firstInFlowBlockChild(node)
	if child == nil {
		return margin
	}

	if childMargin := e.collapsedTopThrough(child, *e.stylePtr(child)); childMargin > margin {
		margin = childMargin
	}

	return margin
}

// collapsedBottomThrough returns the bottom margin that collapses through
// node's bottom edge: the max of its own bottom margin and, when the edge has
// no border or padding, its last in-flow block child's chain.
func (e *engine) collapsedBottomThrough(node *html.Node, style ResolvedStyle) float64 {
	margin := e.scalePt(style.MarginBottom)
	if !e.marginCollapsesThrough(style, false) {
		return margin
	}

	child := e.lastInFlowBlockChild(node)
	if child == nil {
		return margin
	}

	if childMargin := e.collapsedBottomThrough(child, *e.stylePtr(child)); childMargin > margin {
		margin = childMargin
	}

	return margin
}

// absorbRootBodyTopMargin is the layout-time half of the root top-margin
// collapse. Chrome places the body border edge and its first in-flow block
// child chain on the collapsed margin max(body margin, first-child chain).
// The engine lays the body at its own margin and stacks the chain margins
// inside it, so this moves the body box down by the uncollapsed remainder and
// zeroes the chain's top margins through style overrides. Children then flow
// at the final position, which keeps positioned and floated descendants
// aligned with the moved body. It returns the adjusted flow origin and the
// number of overrides the caller must pop.
func (e *engine) absorbRootBodyTopMargin(
	node *html.Node, boxNode *box, style ResolvedStyle, posY float64,
) (float64, int) {
	if node == nil || node.Name != htmlBodyName || !e.marginCollapsesThrough(style, true) {
		return posY, 0
	}

	child := e.firstInFlowBlockChild(node)
	if child == nil {
		return posY, 0
	}

	childTop := e.collapsedTopThrough(child, *e.stylePtr(child))
	if childTop <= 0 {
		return posY, 0
	}

	if bodyMargin := e.scalePt(style.MarginTop); childTop > bodyMargin {
		delta := childTop - bodyMargin
		boxNode.y += delta
		posY += delta
	}

	return posY, e.pushCollapsedTopOverrides(child)
}

// pushCollapsedTopOverrides zeroes the top margin of every element in the
// first-child collapse chain and returns the number of overrides pushed. A
// non-collapsing element still contributes its own margin to the chain, so it
// is zeroed before the walk stops.
func (e *engine) pushCollapsedTopOverrides(child *html.Node) int {
	pushed := 0

	for chainNode := child; chainNode != nil; chainNode = e.firstInFlowBlockChild(chainNode) {
		cst := e.stylePtr(chainNode)
		if cst == nil {
			break
		}

		if cst.MarginTop != 0 {
			zeroed := *cst
			zeroed.MarginTop = 0
			e.styleOverrides = append(e.styleOverrides, styleOverride{node: chainNode, style: &zeroed})
			pushed++
		}

		if !e.marginCollapsesThrough(*cst, true) {
			break
		}
	}

	return pushed
}

// extendRootHTMLHeight lets the collapsed bottom margin that escapes the body
// extend the html height, matching Chrome's root box.
func (e *engine) extendRootHTMLHeight(boxNode *box, curY float64) float64 {
	body := e.rootBodyBox(boxNode)
	if body == nil || body.style == nil || !e.marginCollapsesThrough(*body.style, false) {
		return curY
	}

	// The body box already carries Chrome's collapsed top edge and excludes
	// the absorbed child margins (absorbRootBodyTopMargin ran before the body
	// flowed its children), so the escaped bottom margin is all that remains
	// to extend the html height.
	escaped := e.collapsedBottomThrough(body.node, *body.style)
	bodyBottom := body.y + body.height - boxNode.y + escaped

	if bodyBottom > curY {
		return bodyBottom
	}

	return curY
}

// rootBodyBox returns the body element box that is a direct child of an html
// box, or nil when the tree has no such box.
func (e *engine) rootBodyBox(htmlBox *box) *box {
	if htmlBox == nil || htmlBox.node == nil || htmlBox.node.Name != htmlRootName {
		return nil
	}

	for _, child := range htmlBox.children {
		if child.node != nil && child.node.Name == htmlBodyName {
			return child
		}
	}

	return nil
}
