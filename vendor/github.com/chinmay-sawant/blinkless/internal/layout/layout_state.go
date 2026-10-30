package layout

import "github.com/chinmay-sawant/blinkless/internal/html"

// The state carried by Result lets a later layout run re-lay out one subtree
// of a document that was already laid out: the resolved styles say how each
// node was styled, and the box tree says where each element ended up.

// boxForID returns the laid-out box of the element with the given id, or nil
// when no element carries that id. The walk follows the box tree this layout
// run produced, so the result is the box that was placed on the canvas, not a
// fresh build. Table cell boxes are searched too: a cell is held in the
// owning table's row list, not in the children chain.
func (r *Result) boxForID(elementID string) *box {
	if r == nil || elementID == "" {
		return nil
	}

	return findBoxByID(r.boxTreeRoot(), elementID)
}

// nodeForID returns the html node of the element with the given id, or nil
// when no element carries that id. The box tree is searched first, so the node
// is the one that was laid out. An element that produced no box (display:none)
// is still found in the document tree behind the root box.
func (r *Result) nodeForID(elementID string) *html.Node {
	if r == nil || elementID == "" {
		return nil
	}

	if found := r.boxForID(elementID); found != nil && found.node != nil {
		return found.node
	}

	return findNodeByID(r.documentRootNode(), elementID)
}

// boxTreeRoot returns the root of the box tree. rootBox is the state field
// kept for the subtree lookups; root is the older alias for the same box.
func (r *Result) boxTreeRoot() *box {
	if r == nil {
		return nil
	}

	if r.rootBox != nil {
		return r.rootBox
	}

	return r.root
}

// documentRootNode returns the html node the layout run started from.
func (r *Result) documentRootNode() *html.Node {
	root := r.boxTreeRoot()
	if root == nil {
		return nil
	}

	return root.node
}

// findBoxByID walks the box tree in document order.
func findBoxByID(root *box, elementID string) *box {
	if root == nil {
		return nil
	}

	if root.node != nil && root.node.Type == html.ElementNode && root.node.Attribute("id") == elementID {
		return root
	}

	for _, child := range root.children {
		if found := findBoxByID(child, elementID); found != nil {
			return found
		}
	}

	for _, row := range root.rows {
		for _, cell := range row {
			if found := findBoxByID(cell, elementID); found != nil {
				return found
			}
		}
	}

	return nil
}

// findNodeByID walks the document tree in document order.
func findNodeByID(root *html.Node, elementID string) *html.Node {
	if root == nil {
		return nil
	}

	var found *html.Node

	root.Walk(func(node *html.Node) {
		if found != nil {
			return
		}

		if node.Type == html.ElementNode && node.Attribute("id") == elementID {
			found = node
		}
	})

	return found
}
