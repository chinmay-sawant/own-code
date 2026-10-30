// Selected-content mirroring for the customizable select parsing model. The
// pinned html5lib corpus (html5lib-tests issue #180) expects an option's
// content to be cloned into the select's selectedcontent element even when no
// explicit </option> appears, with selectedness resolved as: the last option
// carrying the selected attribute wins, otherwise the first option. Because
// this engine builds the whole tree before returning, the mirroring runs as
// one post-pass over the finished tree.
//
//nolint:all // post-pass over the custom Node model; same conventions as tree.go
package html

// applySelectedContent fills every selectedcontent element with a clone of
// the selected option's children.
func applySelectedContent(root *Node) {
	root.Walk(func(node *Node) {
		if node.Type == ElementNode && node.Namespace == NamespaceHTML && node.Name == "select" {
			mirrorSelectedContent(node)
		}
	})
}

// mirrorSelectedContent finds the first selectedcontent descendant and the
// selected option of one select element, then replaces the selectedcontent
// children with a deep clone of the option's children.
func mirrorSelectedContent(selectNode *Node) {
	var (
		selectedContent *Node
		options         []*Node
	)

	selectNode.Walk(func(node *Node) {
		if node.Type != ElementNode || node.Namespace != NamespaceHTML {
			return
		}

		switch node.Name {
		case "selectedcontent":
			if selectedContent == nil {
				selectedContent = node
			}
		case "option":
			options = append(options, node)
		}
	})

	if selectedContent == nil {
		return
	}

	selected := (*Node)(nil)

	for _, option := range options {
		if _, ok := option.Attrs["selected"]; ok {
			selected = option
		}
	}

	if selected == nil && len(options) > 0 {
		selected = options[0]
	}

	selectedContent.Children = nil

	if selected == nil {
		return
	}

	// Bound the deep clone so a huge selected subtree cannot double the
	// tree's memory. When the estimate exceeds the cap, leave the
	// selectedcontent empty rather than cloning an unbounded subtree.
	total := 0

	for _, child := range selected.Children {
		total += subtreeByteSize(child, 0)
		if total > maxSelectedContentBytes {
			return
		}
	}

	for _, child := range selected.Children {
		childClone := cloneSubtree(child)
		if childClone == nil {
			continue
		}

		childClone.Parent = selectedContent
		selectedContent.Children = append(selectedContent.Children, childClone)
	}
}

// cloneSubtree deep-copies a node and all its rendered children, preserving
// attributes, namespace, and text. Template contents are copied too so a
// cloned template stays complete.
func cloneSubtree(node *Node) *Node {
	return cloneSubtreeDepth(node, 0)
}

func cloneSubtreeDepth(node *Node, depth int) *Node {
	if node == nil || depthExceeded(depth) {
		return nil
	}

	clone := &Node{ //nolint:exhaustruct
		Type:      node.Type,
		Name:      node.Name,
		Namespace: node.Namespace,
		Text:      node.Text,
		Doctype:   node.Doctype,
	}

	if len(node.Attrs) > 0 {
		clone.Attrs = make(map[string]string, len(node.Attrs))

		for key, value := range node.Attrs {
			clone.Attrs[key] = value
		}
	}

	if len(node.AttrList) > 0 {
		clone.AttrList = append([]Attr(nil), node.AttrList...)
	}

	for _, child := range node.Children {
		childClone := cloneSubtreeDepth(child, depth+1)
		if childClone == nil {
			continue
		}

		childClone.Parent = clone
		clone.Children = append(clone.Children, childClone)
	}

	for _, child := range node.Contents {
		childClone := cloneSubtreeDepth(child, depth+1)
		if childClone == nil {
			continue
		}

		childClone.Parent = clone
		clone.Contents = append(clone.Contents, childClone)
	}

	return clone
}

// subtreeByteSize estimates the in-memory size of a cloned subtree, including
// the depth cap so adversarial nesting cannot recurse without bound.
func subtreeByteSize(node *Node, depth int) int {
	if node == nil || depthExceeded(depth) {
		return 0
	}

	// Include the node allocation and child-slice entry, even for empty
	// elements. Text and attributes alone leave empty trees unbounded.
	const nodeBytes, attrBytes = 256, 64
	size := nodeBytes + len(node.Text) + len(node.Name) + attrBytes*(len(node.Attrs)+len(node.AttrList))

	for key, value := range node.Attrs {
		size += len(key) + len(value)
	}

	for _, attr := range node.AttrList {
		size += len(attr.Namespace) + len(attr.Name) + len(attr.Value)
	}

	for _, child := range node.Children {
		size += subtreeByteSize(child, depth+1)
		if size > maxSelectedContentBytes {
			return maxSelectedContentBytes + 1
		}
	}

	for _, child := range node.Contents {
		size += subtreeByteSize(child, depth+1)
		if size > maxSelectedContentBytes {
			return maxSelectedContentBytes + 1
		}
	}

	return size
}

// --- select and template state helpers ---

// selectInScope reports whether a select element is in the standard's select
// scope (the default scope list already ends at select).
func (b *treeBuilder) selectInScope() bool {
	return b.hasInScope("select", defaultScopeStops)
}

// popUntilSelectPopped pops elements until a select element has been popped.
func (b *treeBuilder) popUntilSelectPopped() {
	for len(b.stack) > 1 {
		top := b.top()
		b.stack = b.stack[:len(b.stack)-1]

		if top.Namespace == NamespaceHTML && top.Name == "select" {
			return
		}
	}
}

// isTemplateElement reports whether node is an HTML template element. Foreign
// elements named "template" (SVG) are not template elements.
