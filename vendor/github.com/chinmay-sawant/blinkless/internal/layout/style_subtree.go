package layout

import (
	"context"
	"fmt"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

// resolveStylesForSubtree resolves the styles of node's subtree and stores
// them in sc.memo. parent is the resolved style the subtree inherits from and
// remBase is the root's used font size, which rem lengths need.
//
// The walk covers node and its descendants only. No sibling, ancestor, or
// other branch is cascaded, so a host can re-style one branch without
// re-cascading the document.
//
// Every element goes through resolveElementStyleMemo, so the pass-local memo
// stays the single store: a repeated declaration shape inside the subtree
// reuses the cached record, and the resolved styles stay reachable in sc.memo
// for subtreeStyles.
//
// A container pass (sc.containers set) resolves the subtree without the memo:
// a @container gate outcome is measured, not static, so it is never cached.
//
// A nil ctx means no cancellation: the walk then runs to the end.
func resolveStylesForSubtree(
	ctx context.Context,
	sc *styleContext, //nolint:varnamelen // short name kept: the style context of the pass
	node *html.Node,
	parent *ResolvedStyle,
	remBase float64,
) error {
	if sc == nil || node == nil {
		return nil
	}

	// rem lengths resolve against the root's used font size, not the
	// subtree's own. The value is also part of the memo key. A zero remBase
	// keeps the size the context already knows.
	if remBase > 0 {
		sc.remBase = remBase
	}

	store := styleStore{} //nolint:exhaustruct // intentional zero-value store

	visited := 0

	var walk func(elem *html.Node, inherited *ResolvedStyle) error

	walk = func(elem *html.Node, inherited *ResolvedStyle) error {
		visited++

		if visited&63 == 0 && ctx != nil {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("layout: style subtree: %w", err)
			}
		}

		sty := subtreeNodeStyle(elem, sc, inherited, &store)

		for _, child := range elem.Children {
			if child.Type == html.TextNode {
				continue
			}

			if err := walk(child, sty); err != nil {
				return err
			}
		}

		return nil
	}

	return walk(node, parent)
}

// subtreeNodeStyle resolves one node's style for the subtree walk. An element
// goes through the memo path; a text node carries no declarations and takes
// the parent's style; a comment, a doctype, or an unknown node takes the
// shared zero style.
func subtreeNodeStyle(
	node *html.Node, styleCtx *styleContext, inherited *ResolvedStyle, store *styleStore,
) *ResolvedStyle {
	if node.Type == html.ElementNode {
		return resolveElementStyleMemo(node, styleCtx, inherited, store)
	}

	if node.Type == html.TextNode {
		if inherited != nil {
			return inherited
		}

		return &defaultTextStyle
	}

	return &zeroResolvedStyle
}

// subtreeStyles returns the resolved styles the subtree pass stored in
// sc.memo for node's subtree, keyed by node.
//
// The memo keys on the parent style, the node name, the inline style, the
// rem base, and the link underline policy, and it validates an entry by the
// matched rule sequence. The walk below therefore threads one parent style
// into the next: an element's parent style is the style the subtree pass
// resolved for its parent.
//
// The map holds only what the pass stored. The memo never caches the "html"
// root, so a subtree rooted at <html> carries no entry for it, and the walk
// stops there.
func subtreeStyles(styleCtx *styleContext, node *html.Node, parent *ResolvedStyle) map[*html.Node]*ResolvedStyle {
	out := make(map[*html.Node]*ResolvedStyle)
	if styleCtx == nil || node == nil {
		return out
	}

	var walk func(elem *html.Node, inherited *ResolvedStyle)

	walk = func(elem *html.Node, inherited *ResolvedStyle) {
		if elem.Type != html.ElementNode {
			return
		}

		sty := memoStyleFor(styleCtx, elem, inherited)
		if sty == nil {
			return
		}

		out[elem] = sty

		for _, child := range elem.Children {
			walk(child, sty)
		}
	}

	walk(node, parent)

	return out
}

// memoStyleFor returns the style the memo holds for node under the given
// parent style, or nil when the memo holds none.
func memoStyleFor(styleCtx *styleContext, node *html.Node, parent *ResolvedStyle) *ResolvedStyle {
	if styleCtx == nil || node == nil || parent == nil {
		return nil
	}

	key := styleMemoKey{
		parent:        parent,
		nodeName:      node.Name,
		inline:        node.Attribute("style"),
		remBase:       styleCtx.remBase,
		linkUnderline: linkUnderlinePolicy(styleCtx, node),
	}

	return styleCtx.memo.lookup(key, styleCtx.matchedRules(node, ""))
}
