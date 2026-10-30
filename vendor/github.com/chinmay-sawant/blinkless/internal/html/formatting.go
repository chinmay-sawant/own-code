// Active formatting elements and the adoption agency algorithm. The list
// tracks the formatting elements (a, b, big, code, em, font, i, nobr, s,
// small, strike, strong, tt, u) so misnested tags are repaired with the
// standard's reconstruction and adoption rules. Markers (nil entries)
// separate the formatting opened inside cells, captions, and
// applet/marquee/object elements.
//
//nolint:all // adoption agency; same custom Node model and conventions as tree.go
package html

// formattingTags are the elements tracked in the active formatting list.
var formattingTags = map[string]bool{
	"a": true, "b": true, "big": true, "code": true, "em": true, "font": true,
	"i": true, "nobr": true, "s": true, "small": true, "strike": true,
	"strong": true, "tt": true, "u": true,
}

// defaultNoReconstruct lists start tags with their own in-body rules that
// insert without reconstructing active formatting elements. The engine routes
// them through the default start-tag branch until their dedicated rules land.
var defaultNoReconstruct = map[string]bool{
	"rb": true, "rtc": true, "rp": true, "rt": true,
}

// isFormattingTag reports whether name is tracked in the active formatting
// list.
func isFormattingTag(name string) bool {
	return formattingTags[name]
}

// --- list operations ---

// activeFormattingMarkerStart returns the index of the first entry after the
// last marker (0 when there is no marker).
func (b *treeBuilder) activeFormattingMarkerStart() int {
	for i := len(b.activeFormatting) - 1; i >= 0; i-- {
		if b.activeFormatting[i] == nil {
			return i + 1
		}
	}

	return 0
}

// maxActiveFormatting caps the active formatting list. Noah's Ark drops only
// a fourth identical element, so distinct elements such as <b id="N"> would
// otherwise grow the list without bound.
const maxActiveFormatting = 1024

// pushActiveFormatting adds node to the list, applying the Noah's Ark clause:
// when three entries after the last marker already have the same name,
// namespace, and attributes, the earliest of them is removed first. A nil
// node (dropped by the depth cap) is ignored. The list length stays at or
// below maxActiveFormatting.
func (b *treeBuilder) pushActiveFormatting(node *Node) {
	if node == nil {
		return
	}

	start := b.activeFormattingMarkerStart()
	matches := make([]int, 0, 4)

	for i := start; i < len(b.activeFormatting); i++ {
		if sameFormattingElement(b.activeFormatting[i], node) {
			matches = append(matches, i)
		}
	}

	if len(matches) >= 3 {
		idx := matches[0]
		b.activeFormatting = append(b.activeFormatting[:idx], b.activeFormatting[idx+1:]...)
	}

	if len(b.activeFormatting) >= maxActiveFormatting {
		return
	}

	b.activeFormatting = append(b.activeFormatting, node)
}

// activeFormattingContains reports whether node is in the list.
func (b *treeBuilder) activeFormattingContains(node *Node) bool {
	return b.activeFormattingIndex(node) >= 0
}

// activeFormattingIndex returns node's index in the list, or -1.
func (b *treeBuilder) activeFormattingIndex(node *Node) int {
	for i, entry := range b.activeFormatting {
		if entry == node {
			return i
		}
	}

	return -1
}

// activeFormattingRemove removes the first entry equal to node.
func (b *treeBuilder) activeFormattingRemove(node *Node) {
	if idx := b.activeFormattingIndex(node); idx >= 0 {
		b.activeFormatting = append(b.activeFormatting[:idx], b.activeFormatting[idx+1:]...)
	}
}

// replaceActiveFormatting swaps old for replacement in place.
func (b *treeBuilder) replaceActiveFormatting(old, replacement *Node) {
	if idx := b.activeFormattingIndex(old); idx >= 0 {
		b.activeFormatting[idx] = replacement
	}
}

// insertActiveFormattingAt inserts node at idx, clamping to the list bounds.
func (b *treeBuilder) insertActiveFormattingAt(idx int, node *Node) {
	if idx < 0 {
		idx = 0
	}

	if idx > len(b.activeFormatting) {
		idx = len(b.activeFormatting)
	}

	b.activeFormatting = append(b.activeFormatting, nil)
	copy(b.activeFormatting[idx+1:], b.activeFormatting[idx:])
	b.activeFormatting[idx] = node
}

// lastActiveFormatting returns the last list entry with the given name before
// the last marker, or nil.
func (b *treeBuilder) lastActiveFormatting(name string) *Node {
	for i := len(b.activeFormatting) - 1; i >= 0; i-- {
		entry := b.activeFormatting[i]
		if entry == nil {
			return nil
		}

		if entry.Namespace == NamespaceHTML && entry.Name == name {
			return entry
		}
	}

	return nil
}

// sameFormattingElement reports whether two nodes match for the Noah's Ark
// clause: same tag name, namespace, and attributes (order-insensitive).
func sameFormattingElement(a, b *Node) bool {
	if a == nil || b == nil || a.Name != b.Name || a.Namespace != b.Namespace ||
		len(a.AttrList) != len(b.AttrList) {
		return false
	}

	for _, attr := range a.AttrList {
		found := false

		for _, other := range b.AttrList {
			if attr.Namespace == other.Namespace && attr.Name == other.Name && attr.Value == other.Value {
				found = true

				break
			}
		}

		if !found {
			return false
		}
	}

	return true
}

// cloneFormattingElement copies an element's identity and attributes without
// its children, for reconstruction and the adoption agency.
func cloneFormattingElement(node *Node) *Node {
	clone := &Node{Type: ElementNode, Name: node.Name, Namespace: node.Namespace} //nolint:exhaustruct

	if len(node.Attrs) > 0 {
		clone.Attrs = make(map[string]string, len(node.Attrs))

		for key, value := range node.Attrs {
			clone.Attrs[key] = value
		}
	}

	if len(node.AttrList) > 0 {
		clone.AttrList = append([]Attr(nil), node.AttrList...)
	}

	return clone
}

// --- reconstruction ---

// reconstructActiveFormatting reopens formatting elements that were closed
// implicitly: every entry after the last one that is still open is recreated
// and pushed back on the stack.
func (b *treeBuilder) reconstructActiveFormatting() {
	n := len(b.activeFormatting)
	if n == 0 {
		return
	}

	last := b.activeFormatting[n-1]
	if last == nil || b.stackIndex(last) >= 0 {
		return
	}

	entry := n - 1
	for entry > 0 {
		prev := b.activeFormatting[entry-1]
		if prev == nil || b.stackIndex(prev) >= 0 {
			break
		}

		entry--
	}

	for ; entry < len(b.activeFormatting); entry++ {
		clone := b.insertClone(cloneFormattingElement(b.activeFormatting[entry]))
		if clone == nil {
			return
		}

		b.activeFormatting[entry] = clone
	}
}

// insertClone inserts a cloned formatting element at the current insertion
// point and pushes it on the stack.
func (b *treeBuilder) insertClone(clone *Node) *Node {
	if len(b.stack)-1 >= maxElementDepth {
		return nil
	}

	point := b.appropriatePlace()
	insertChildAt(point, clone)
	clone.Parent = point.parent
	b.stack = append(b.stack, clone)

	return clone
}

// --- adoption agency ---

// adoptionAgency implements the standard's adoption agency algorithm for an
// end tag token, repairing misnested formatting elements.
func (b *treeBuilder) adoptionAgency(tokItem *token) {
	subject := tokItem.data

	// Depth guard: when the stack is already at the element-depth cap, do not
	// re-parent or clone formatting elements, which could otherwise push the
	// tree past the cap. Fall back to the ordinary end-tag rules instead.
	if depthExceeded(len(b.stack) - 1) {
		b.closeHTMLElement(subject)

		return
	}

	// Step 2: a current node matching subject that is not a formatting entry
	// is simply popped.
	if cur := b.top(); cur.Namespace == NamespaceHTML && cur.Name == subject &&
		!b.activeFormattingContains(cur) {
		b.stack = b.stack[:len(b.stack)-1]

		return
	}

	// Steps 3-4: up to eight outer iterations.
	for range 8 {
		formatting := b.lastActiveFormatting(subject)
		if formatting == nil {
			// Step 3: fall back to the ordinary end-tag rules.
			b.closeHTMLElement(subject)

			return
		}

		formattingIdx := b.stackIndex(formatting)
		if formattingIdx < 0 {
			// Step 4: no longer open; drop the entry.
			b.activeFormattingRemove(formatting)

			return
		}

		if !b.nodeInScope(formatting, defaultScopeStops) {
			// Step 5.
			return
		}

		// Step 7: the topmost special element below the formatting element.
		furthestIdx := -1

		for i := formattingIdx + 1; i < len(b.stack); i++ {
			if isSpecialElement(b.stack[i]) {
				furthestIdx = i

				break
			}
		}

		if furthestIdx < 0 {
			// Step 8: nothing between; pop through the formatting element.
			b.stack = b.stack[:formattingIdx]
			b.activeFormattingRemove(formatting)

			return
		}

		furthestBlock := b.stack[furthestIdx]
		commonAncestor := b.stack[formattingIdx-1]
		bookmark := b.activeFormattingIndex(formatting)

		node := furthestBlock
		lastNode := furthestBlock
		removedAbove := (*Node)(nil)

		// Steps 12-13: the inner loop walks up from the furthest block.
		for inner := 1; ; inner++ {
			var above *Node

			if removedAbove != nil {
				above = removedAbove
				removedAbove = nil
			} else {
				above = b.stack[b.stackIndex(node)-1]
			}

			if above == formatting {
				break
			}

			if inner > 3 && b.activeFormattingContains(above) {
				b.activeFormattingRemove(above)
			}

			if !b.activeFormattingContains(above) {
				aboveIdx := b.stackIndex(above)
				removedAbove = b.stack[aboveIdx-1]
				b.removeFromStack(above)

				continue
			}

			clone := cloneFormattingElement(above)
			b.replaceActiveFormatting(above, clone)
			b.replaceInStack(above, clone)

			if lastNode == furthestBlock {
				bookmark = b.activeFormattingIndex(clone) + 1
			}

			appendChildNode(clone, lastNode)
			lastNode = clone
			node = clone
		}

		// Step 14: the adjusted insertion location under the common ancestor.
		point := b.appropriatePlaceFor(commonAncestor)

		// Steps 15-16: move lastNode under the common ancestor.
		detachNode(lastNode)
		insertChildAt(point, lastNode)
		lastNode.Parent = point.parent

		// Steps 17-19: wrap the furthest block's children in a clone of the
		// formatting element.
		newElement := cloneFormattingElement(formatting)
		moveChildren(furthestBlock, newElement)
		appendChildNode(furthestBlock, newElement)

		// Step 20: replace the formatting entry at the bookmark.
		removedIdx := b.activeFormattingIndex(formatting)
		b.activeFormattingRemove(formatting)

		if bookmark > removedIdx {
			bookmark--
		}

		b.insertActiveFormattingAt(bookmark, newElement)

		// Step 21: replace the formatting element in the stack, immediately
		// below the furthest block.
		b.removeFromStack(formatting)
		b.insertIntoStackAfter(furthestBlock, newElement)
	}
}

// --- stack and tree surgery helpers ---

// stackIndex returns node's position in the open-element stack, or -1.
func (b *treeBuilder) stackIndex(node *Node) int {
	for i, entry := range b.stack {
		if entry == node {
			return i
		}
	}

	return -1
}

// nodeInScope reports whether target is in the given scope.
func (b *treeBuilder) nodeInScope(target *Node, stops map[string]bool) bool {
	for i := len(b.stack) - 1; i > 0; i-- {
		node := b.stack[i]
		if node == target {
			return true
		}

		if isScopeBoundary(node, stops) {
			return false
		}
	}

	return false
}

// replaceInStack swaps old for replacement in place.
func (b *treeBuilder) replaceInStack(old, replacement *Node) {
	if idx := b.stackIndex(old); idx >= 0 {
		b.stack[idx] = replacement
	}
}

// insertIntoStackAfter inserts node immediately after anchor in the stack.
func (b *treeBuilder) insertIntoStackAfter(anchor, node *Node) {
	idx := b.stackIndex(anchor)
	if idx < 0 {
		return
	}

	b.stack = append(b.stack, nil)
	copy(b.stack[idx+2:], b.stack[idx+1:])
	b.stack[idx+1] = node
}

// detachNode removes node from its parent's children.
func detachNode(node *Node) {
	if node.Parent == nil {
		return
	}

	parent := node.Parent

	for i, child := range parent.Children {
		if child == node {
			parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)

			break
		}
	}

	node.Parent = nil
}

// appendChildNode moves child to the end of parent's children.
func appendChildNode(parent, child *Node) {
	detachNode(child)
	parent.Children = append(parent.Children, child)
	child.Parent = parent
}

// moveChildren moves every child of from to the end of to.
func moveChildren(from, to *Node) {
	children := from.Children
	from.Children = nil

	for _, child := range children {
		child.Parent = to
		to.Children = append(to.Children, child)
	}
}
