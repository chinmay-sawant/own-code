package layout

import (
	"strconv"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
	"github.com/chinmay-sawant/blinkless/internal/html"
)

// Footnote collection for CSS Generated Content for Paged Media 3, section 2
// (https://drafts.csswg.org/css-gcpm-3/#footnotes). Chrome implements no
// footnote area, so the spec is the reference here, not a Chrome version.
//
// Observable subset in this file for footnote-display and footnote-policy:
//   - An element with float: footnote is collected as a footnote, numbered in
//     document order through the footnote counter, honoring counter-reset and
//     counter-increment on the footnote name.
//   - footnote-display parses to its used value (block, inline, compact) and
//     is reported per footnote. There is no footnote area yet, so the used
//     value does not move any box.
//   - footnote-policy parses to its used value (auto, line, block) and feeds
//     GroupFootnotesByPolicy. The engine has no page splitter, so a policy
//     cannot force a break; grouping is the feasible subset.
//
// Placement fallback, stated explicitly: footnote content stays in normal flow
// at its DOM position. FootnoteFallbackArea reports the document-end strip
// below the last laid-out box, which is where a footnote area would go. The
// follow-up seam is a per-page footnote area pass (@footnote rule, separator
// line, per-page counter reset) hooked into layout and paint.

const (
	footnotePropFloat   = "float"
	footnoteFloatValue  = "footnote"
	footnotePropHidden  = "display"
	footnotePropDisplay = "footnote-display"
	footnotePropPolicy  = "footnote-policy"

	footnoteDisplayBlock   = "block"
	footnoteDisplayInline  = "inline"
	footnoteDisplayCompact = "compact"

	footnotePolicyAuto  = "auto"
	footnotePolicyLine  = "line"
	footnotePolicyBlock = "block"

	footnoteCounterName = "footnote"
)

// Footnote is one collected footnote element.
type Footnote struct {
	Node    *html.Node
	Number  int
	Marker  string
	Display string
	Policy  string
	Text    string
}

// FootnoteArea is a canvas rectangle in points.
type FootnoteArea struct {
	X float64
	Y float64
	W float64
	H float64
}

// parseFootnoteDisplay reports the used footnote-display value for a raw
// declaration. Only block, inline, and compact are usable; anything else,
// including the empty value, is unusable and the caller keeps block.
func parseFootnoteDisplay(raw string) (string, bool) {
	value := normalizeCSSValue(raw)

	switch value {
	case footnoteDisplayBlock, footnoteDisplayInline, footnoteDisplayCompact:
		return value, true
	default:
		return "", false
	}
}

// parseFootnotePolicy reports the used footnote-policy value for a raw
// declaration. Only auto, line, and block are usable; anything else,
// including the empty value, is unusable and the caller keeps auto.
func parseFootnotePolicy(raw string) (string, bool) {
	value := normalizeCSSValue(raw)

	switch value {
	case footnotePolicyAuto, footnotePolicyLine, footnotePolicyBlock:
		return value, true
	default:
		return "", false
	}
}

// footnoteCollector walks one DOM tree numbering footnote elements. It reads
// declarations through the shared cascade matchers (matchedRules plus winning
// inline hits, the same path cascadedProp uses), so author sheets and inline
// styles both apply without touching the cascade itself.
type footnoteCollector struct {
	ctx     *styleContext
	counter int
	resets  []int
	items   []Footnote
}

func newFootnoteCollector(sheets []*css.Stylesheet) *footnoteCollector {
	return &footnoteCollector{
		ctx: &styleContext{ //nolint:exhaustruct // footnote pass needs sheets only
			sheets: sheets,
		},
		counter: 0,
		resets:  nil,
		items:   nil,
	}
}

// cascaded returns the winning declaration for prop on node.
func (collector *footnoteCollector) cascaded(node *html.Node, prop string) string {
	if collector == nil || collector.ctx == nil || node == nil {
		return ""
	}

	return cascadedProp(collector.ctx, node, prop)
}

// hidden reports whether node is explicitly display: none and therefore out
// of the box tree. Only an explicit none hides; undeclared display keeps the
// element, matching the engine which drops display: none boxes at build.
func (collector *footnoteCollector) hidden(node *html.Node) bool {
	return normalizeCSSValue(collector.cascaded(node, footnotePropHidden)) == cssDisplayNone
}

// isFootnote reports whether node declares float: footnote, which is what
// creates a footnote element per GCPM 3 section 2.2.
func (collector *footnoteCollector) isFootnote(node *html.Node) bool {
	return normalizeCSSValue(collector.cascaded(node, footnotePropFloat)) == footnoteFloatValue
}

// displayOf returns the used footnote-display value for a footnote element.
func (collector *footnoteCollector) displayOf(node *html.Node) string {
	if value, ok := parseFootnoteDisplay(collector.cascaded(node, footnotePropDisplay)); ok {
		return value
	}

	return footnoteDisplayBlock
}

// policyOf returns the used footnote-policy value for a footnote element.
func (collector *footnoteCollector) policyOf(node *html.Node) string {
	if value, ok := parseFootnotePolicy(collector.cascaded(node, footnotePropPolicy)); ok {
		return value
	}

	return footnotePolicyAuto
}

// resetValue reports the footnote counter-reset value declared on node.
func (collector *footnoteCollector) resetValue(node *html.Node) (int, bool) {
	spec := collector.cascaded(node, cssPropCounterReset)

	for _, op := range parseCounterList(spec, counterResetDefault) {
		if op.name == footnoteCounterName {
			return op.value, true
		}
	}

	return counterResetDefault, false
}

// incrementStep reports the footnote counter-increment step declared on node.
// The GCPM 3 default user agent sheet increments the footnote counter on
// every footnote, so an undeclared step is one.
func (collector *footnoteCollector) incrementStep(node *html.Node) int {
	spec := collector.cascaded(node, cssPropCounterIncrement)

	for _, op := range parseCounterList(spec, counterIncrementDefault) {
		if op.name == footnoteCounterName {
			return op.value
		}
	}

	return counterIncrementDefault
}

// footnoteText returns the whitespace-collapsed text content of a footnote.
func footnoteText(node *html.Node) string {
	if node == nil {
		return ""
	}

	return strings.Join(strings.Fields(node.TextContent()), " ")
}

// enterReset pushes the counter when node resets the footnote counter. A
// reset on the element scopes the element itself too, matching CSS Lists 3.
func (collector *footnoteCollector) enterReset(node *html.Node) {
	value, ok := collector.resetValue(node)
	if !ok {
		return
	}

	collector.resets = append(collector.resets, collector.counter)
	collector.counter = value
}

// exitReset pops the counter scope opened by enterReset on the same node.
func (collector *footnoteCollector) exitReset(node *html.Node) {
	if _, ok := collector.resetValue(node); !ok {
		return
	}

	last := len(collector.resets) - 1
	collector.counter = collector.resets[last]
	collector.resets = collector.resets[:last]
}

// appendFootnote numbers one footnote element and records it.
func (collector *footnoteCollector) appendFootnote(node *html.Node) {
	collector.counter += collector.incrementStep(node)

	collector.items = append(collector.items, Footnote{
		Node:    node,
		Number:  collector.counter,
		Marker:  strconv.Itoa(collector.counter),
		Display: collector.displayOf(node),
		Policy:  collector.policyOf(node),
		Text:    footnoteText(node),
	})
}

// walk visits element nodes in document order, numbering footnotes as the
// GCPM 3 footnote counter would: document order, scoped by counter-reset.
func (collector *footnoteCollector) walk(node *html.Node) {
	if node == nil || node.Type != html.ElementNode {
		return
	}

	if collector.hidden(node) {
		return
	}

	collector.enterReset(node)

	if collector.isFootnote(node) {
		collector.appendFootnote(node)
	}

	for _, child := range node.Children {
		collector.walk(child)
	}

	collector.exitReset(node)
}

// CollectFootnotes enumerates the footnote elements under root in document
// order, numbered through the footnote counter. Sheets participate with the
// same selector matching the cascade uses; inline styles win over sheets.
// Elements under display: none are skipped with their subtrees.
func CollectFootnotes(root *html.Node, sheets ...*css.Stylesheet) []Footnote {
	collector := newFootnoteCollector(sheets)
	collector.walk(root)

	return collector.items
}

// findFootnoteBox returns the laid-out box built for target, if any.
func findFootnoteBox(current *box, target *html.Node) *box {
	if current == nil {
		return nil
	}

	if current.node == target {
		return current
	}

	for _, child := range current.children {
		if found := findFootnoteBox(child, target); found != nil {
			return found
		}
	}

	return nil
}

// FootnoteGeometry reports the laid-out border box of a collected footnote.
// Footnote content stays in normal flow at its DOM position, so this is the
// in-flow geometry, not a footnote area slot. It returns false when the
// result has no box for the footnote, for example display: none content
// collected from a different tree.
func FootnoteGeometry(res *Result, item Footnote) (FootnoteArea, bool) {
	var empty FootnoteArea

	if res == nil || res.root == nil || item.Node == nil {
		return empty, false
	}

	found := findFootnoteBox(res.root, item.Node)
	if found == nil {
		return empty, false
	}

	return FootnoteArea{X: found.x, Y: found.y, W: found.w, H: found.height}, true
}

// FootnoteFallbackArea returns the document-end fallback strip: full content
// width starting at the bottom of the lowest laid-out box with no reserved
// height. Height is zero on purpose, since nothing is moved there yet;
// footnote content stays in flow. A per-page footnote area with a separator
// line is the follow-up seam.
func FootnoteFallbackArea(res *Result) FootnoteArea {
	var area FootnoteArea

	if res == nil {
		return area
	}

	var bottom float64

	for _, candidate := range res.boxes {
		if candidate == nil {
			continue
		}

		if edge := candidate.y + candidate.height; edge > bottom {
			bottom = edge
		}
	}

	area.Y = bottom
	area.W = res.Width

	return area
}

// GroupFootnotesByPolicy clusters collected footnotes the way the fallback
// area would stack them. Block-policy footnotes must travel with their
// paragraph, so they merge into one group; line and auto policies keep one
// group per footnote, since each may break at its own line. This is the
// feasible subset of footnote-policy: the engine has no page splitter, so no
// group here forces a break. Wiring these groups into forced breaks in the
// pagination pass is the follow-up seam.
func GroupFootnotesByPolicy(items []Footnote) [][]Footnote {
	groups := make([][]Footnote, 0, len(items))
	pending := make([]Footnote, 0, len(items))

	flushPending := func() {
		if len(pending) == 0 {
			return
		}

		groups = append(groups, pending)
		pending = nil
	}

	for _, item := range items {
		if item.Policy != footnotePolicyBlock {
			flushPending()

			groups = append(groups, []Footnote{item})

			continue
		}

		pending = append(pending, item)
	}

	flushPending()

	return groups
}
