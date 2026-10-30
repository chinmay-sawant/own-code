package layout

// Print-scoped position:sticky (CSS Positioned Layout Level 3 lite).
//
// tagSticky records the resolved non-auto insets on the sticky box and stamps
// StickyID on its ops so later passes can find the sticky subtree after
// prependChrome shifts op indices (see reapplyStickyID).

// tagSticky records sticky insets and stamps StickyID on the box's ops so
// later paint passes can find them after parent prependChrome shifts op
// indices.
func (e *engine) tagSticky(boxNode *box) {
	if boxNode == nil || boxNode.style == nil || boxNode.style.Position != positionSticky {
		return
	}

	boxNode.sticky = true
	e.stickySeq++
	boxNode.stickyID = e.stickySeq

	boxNode.stickyTopSet, boxNode.stickyTop = stickyInset(e, boxNode.style.TopAuto, boxNode.style.Top)
	boxNode.stickyRightSet, boxNode.stickyRight = stickyInset(e, boxNode.style.RightAuto, boxNode.style.Right)
	boxNode.stickyBottomSet, boxNode.stickyBottom = stickyInset(e, boxNode.style.BottomAuto, boxNode.style.Bottom)
	boxNode.stickyLeftSet, boxNode.stickyLeft = stickyInset(e, boxNode.style.LeftAuto, boxNode.style.Left)

	if boxNode.opEnd >= boxNode.opStart && boxNode.opStart >= 0 {
		for i := boxNode.opStart; i <= boxNode.opEnd && i < len(e.ops); i++ {
			e.ops[i].StickyID = boxNode.stickyID
		}
	}
}

// stickyInset resolves a non-auto sticky inset; auto insets stay unset (0).
func stickyInset(e *engine, auto bool, v float64) (bool, float64) {
	if auto {
		return false, 0
	}

	return true, e.scalePt(v)
}
