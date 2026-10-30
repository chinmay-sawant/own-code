package layout

import (
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

// measureFlexItemMaxContent includes block descendants in a flex item's
// intrinsic width. The generic cell measure intentionally stops at nested
// block formatting contexts, which is correct for table-cell line collection
// but makes a wrapper such as a section header measure only its eyebrow and
// collapse the heading into a narrow column.
//
//nolint:cyclop,wsl // intrinsic flex measurement keeps the CSS cases together
func (e *engine) measureFlexItemMaxContent(node *html.Node, style *ResolvedStyle) float64 {
	if style.Display == displayFlex || style.Display == displayInlineFlex {
		return e.flexIntrinsicWidth(node, style, false)
	}

	_, maxW := e.measureCellMinMax(node, style)
	chrome := e.scalePt(style.PaddingLeft) + e.scalePt(style.PaddingRight) +
		e.scalePt(style.BorderLeft.Width) + e.scalePt(style.BorderRight.Width)
	contentW := maxW - chrome
	if contentW < 0 {
		contentW = 0
	}
	if style.Width >= 0 {
		specified := e.flexBoxSized(style, e.scalePt(style.Width), chrome)
		if specified > contentW+chrome {
			contentW = specified - chrome
		}
	}
	rowFlex := style.Display == displayFlex && style.FlexDirection != fxCol && style.FlexDirection != fxColRev
	rowContentW := 0.0
	rowChildCount := 0

	for _, child := range node.Children {
		if child.Type != html.ElementNode {
			continue
		}

		childStyle := e.stylePtr(child)
		if childStyle.Display == cssDisplayNone {
			continue
		}

		childW := e.measureFlexItemMaxContent(child, childStyle)
		childW += e.scalePt(childStyle.MarginLeft) + e.scalePt(childStyle.MarginRight)
		if rowFlex {
			rowContentW += childW
			rowChildCount++
		} else if childW > contentW {
			contentW = childW
		}
	}

	if rowFlex {
		_, columnGap := e.styleGaps(style)
		if rowChildCount > 1 {
			rowContentW += columnGap * float64(rowChildCount-1)
		}

		if rowContentW > contentW {
			contentW = rowContentW
		}
	}

	return contentW + chrome
}

// flexIntrinsicWidth returns the border-box width contribution of a flex
// container. Row containers sum their item contributions; column containers
// use the largest contribution because their items share the cross axis.
//
//nolint:cyclop,wsl // intrinsic flex contributions keep the row and column rules together
func (e *engine) flexIntrinsicWidth(node *html.Node, style *ResolvedStyle, _ bool) float64 {
	rowFlex := style.FlexDirection != fxCol && style.FlexDirection != fxColRev
	chrome := e.scalePt(style.PaddingLeft) + e.scalePt(style.PaddingRight) +
		e.scalePt(style.BorderLeft.Width) + e.scalePt(style.BorderRight.Width)
	contentW := 0.0
	if !rowFlex {
		_, maxW := e.measureCellMinMax(node, style)
		contentW = maxW - chrome
		if contentW < 0 {
			contentW = 0
		}
	}

	childW := 0.0
	childCount := 0
	// Bare text forms anonymous flex items in layout (flexChildren); the
	// intrinsic sum must count the same items, or a pill with an orb and a
	// text run shrink-wraps to the orb alone and the text overflows the box.
	// Non-flex callers (block width:max-content) keep the historical element
	// walk so their line formation does not gain flex gaps.
	isFlexItems := style.Display == displayFlex || style.Display == displayInlineFlex
	var run *cellMeasure
	nowrap := style.WhiteSpace == cssWhiteSpaceNowrap || style.WhiteSpace == cssWhiteSpacePre
	flushRun := func() {
		if run == nil {
			return
		}
		w := run.lineW
		run = nil
		if w <= 0 || !rowFlex || !isFlexItems {
			return
		}
		childW += w
		childCount++
	}
	for _, child := range node.Children {
		if child.Type == html.TextNode {
			if !isFlexItems || !rowFlex || strings.TrimSpace(child.Text) == "" {
				continue
			}
			if run == nil {
				//nolint:exhaustruct // zero fields are the flushed-line state
				run = &cellMeasure{engine: e, em: style.FontSize, root: node}
			}

			run.measureText(child.Text, style, nowrap)

			continue
		}
		if child.Type != html.ElementNode {
			continue
		}
		flushRun()

		childW, childCount = e.accumulateIntrinsicElementWidth(child, rowFlex, childW, childCount)
	}
	flushRun()

	contentW = e.applyIntrinsicGapsAndMax(style, rowFlex, childW, childCount, contentW)

	return contentW + chrome
}

// applyIntrinsicGapsAndMax folds the row gap total into childW and returns
// the winning content width. Split from flexIntrinsicWidth so the intrinsic
// pass stays within the statement budget.
func (e *engine) applyIntrinsicGapsAndMax(
	style *ResolvedStyle, rowFlex bool, childW float64, childCount int, contentW float64,
) float64 {
	if rowFlex {
		_, columnGap := e.styleGaps(style)
		if childCount > 1 {
			childW += columnGap * float64(childCount-1)
		}
	}

	if childW > contentW {
		contentW = childW
	}

	return contentW
}

// accumulateIntrinsicElementWidth folds one in-flow element item into the row
// sum or the column max, skipping display:none and out-of-flow nodes.
// Definite widths resolve directly, everything else measures max-content, and
// the item margins join the contribution the row sum adds back. Split from
// flexIntrinsicWidth so the intrinsic pass stays within the complexity
// budget; the arithmetic is unchanged.
func (e *engine) accumulateIntrinsicElementWidth(
	child *html.Node, rowFlex bool, childW float64, childCount int,
) (float64, int) {
	childStyle := e.stylePtr(child)
	if childStyle.Display == cssDisplayNone || isOutOfFlowNode(child, childStyle) {
		return childW, childCount
	}

	var contribution float64

	if childStyle.Width >= 0 && childStyle.WidthPercent < 0 {
		pad := e.scalePt(childStyle.PaddingLeft) + e.scalePt(childStyle.PaddingRight) +
			e.scalePt(childStyle.BorderLeft.Width) + e.scalePt(childStyle.BorderRight.Width)
		contribution = e.flexBoxSized(childStyle, e.scalePt(childStyle.Width), pad)
	} else {
		contribution = e.measureFlexItemMaxContent(child, childStyle)
	}

	contribution += e.scalePt(childStyle.MarginLeft) + e.scalePt(childStyle.MarginRight)

	if rowFlex {
		return childW + contribution, childCount + 1
	}

	if contribution > childW {
		return contribution, childCount
	}

	return childW, childCount
}

// flexSpecifiedBaseWidth resolves a definite flex base size (flex-basis then
// width). Cyclic percentage basis against an indefinite main size falls
// through to the next specified size, then content.
func (e *engine) flexSpecifiedBaseWidth(style *ResolvedStyle, mainSize, pad float64) (float64, bool) {
	switch {
	case style.FlexBasisPercent >= 0 && mainSize >= 0:
		return e.flexBoxSized(style, mainSize*style.FlexBasisPercent/oneHundred, pad), true
	case style.FlexBasis >= 0:
		return e.flexBoxSized(style, e.scalePt(style.FlexBasis), pad), true
	case style.WidthPercent >= 0 && mainSize >= 0:
		return e.flexBoxSized(style, mainSize*style.WidthPercent/oneHundred, pad), true
	case style.Width >= 0:
		return e.flexBoxSized(style, e.scalePt(style.Width), pad), true
	}

	return 0, false
}

// flexBoxSized converts a content-box size to border-box when BoxSizing says so.
// The style is read through a pointer so intrinsic measurement does not copy
// the 6KB record per item.
func (e *engine) flexBoxSized(style *ResolvedStyle, size, pad float64) float64 {
	if style.BoxSizing != borderBox {
		return size + pad
	}

	return size
}
