// Exclusions (css-exclusions-1 lite): wrap-flow and wrap-through.
//
// Wrap-flow turns a block-level box into an exclusion: the box is laid out
// where normal flow put it, then leaves the flow (later content keeps the
// same Y) and contributes its border box to the current block formatting
// context's wrapping context. Line boxes that overlap the exclusion are
// shortened through the existing floatState exclusion path
// (internal/layout/float.go), not a second wrap engine:
//
//	start   content flows beside the box's start edge (LTR: lines to the
//	        left of the box), the end side stays empty
//	end     content flows beside the box's end edge (LTR: lines start after
//	        the box), the start side stays empty
//	both    the side with more free space wins (the line model has one
//	        interval per line)
//	minimum the side with less free space wins
//	maximum the side with more free space wins
//	clear   no side flow: an overlapping line is pushed below the box (the
//	        same Y outcome as clear: both past a float)
//
// wrap-through: none makes the element's content ignore exclusions inherited
// from outside it: its build runs against a scratch floatState and the live
// state is restored afterwards. Inner floats therefore do not leak to later
// siblings (lite deviation from CSS2.1 float scope).
//
// Boundary (honest): shape-outside contours are not consulted for exclusion
// boxes (they stay float-only), and the scratch floatState means floats
// inside a wrap-through: none subtree stay inside it.
package layout

import (
	"strings"
)

// Exclusion property names and keywords.
const (
	wrapFlowPropName    = "wrap-flow"
	wrapThroughPropName = "wrap-through"

	wrapFlowAuto    = "auto"
	wrapFlowBoth    = "both"
	wrapFlowStart   = "start"
	wrapFlowEnd     = "end"
	wrapFlowMinimum = "minimum"
	wrapFlowMaximum = "maximum"
	wrapFlowClear   = "clear"

	wrapThroughWrap = "wrap"
	wrapThroughNone = "none"
)

// applyExclusionProps owns wrap-flow and wrap-through. Registered in
// styleGroups (style_cascade.go).
func applyExclusionProps(
	style *ResolvedStyle, prop, value string, _ float64, _ *styleContext, _ *ResolvedStyle, _ bool,
) bool {
	switch prop {
	case wrapFlowPropName:
		used, ok := parseWrapFlow(value)
		if !ok {
			return true
		}

		style.WrapFlow = used
	case wrapThroughPropName:
		used, ok := parseWrapThrough(value)
		if !ok {
			return true
		}

		style.WrapThrough = used
	default:
		return false
	}

	return true
}

// parseWrapFlow parses wrap-flow: auto | both | start | end | minimum |
// maximum | clear. auto (the initial, and the computed value on floats)
// stores "".
func parseWrapFlow(value string) (string, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return "", false
	}

	if cssWideKeyword(trimmed) || trimmed == wrapFlowAuto {
		return "", true
	}

	switch trimmed {
	case wrapFlowBoth, wrapFlowStart, wrapFlowEnd, wrapFlowMinimum, wrapFlowMaximum, wrapFlowClear:
		return trimmed, true
	default:
		return "", false
	}
}

// parseWrapThrough parses wrap-through: wrap | none. wrap stores "".
func parseWrapThrough(value string) (string, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return "", false
	}

	if cssWideKeyword(trimmed) || trimmed == wrapThroughWrap {
		return "", true
	}

	if trimmed == wrapThroughNone {
		return wrapThroughNone, true
	}

	return "", false
}

// exclusionDeclarationValueAccepted is the cascade/@supports gate for the
// exclusion properties.
func exclusionDeclarationValueAccepted(prop, value string) (bool, bool) {
	switch prop {
	case wrapFlowPropName:
		_, ok := parseWrapFlow(value)

		return ok, true
	case wrapThroughPropName:
		_, ok := parseWrapThrough(value)

		return ok, true
	default:
		return false, false
	}
}

// exclusionChildNode is the node shape an exclusion box must have: a
// block-level element that is not floated (float != none computes wrap-flow
// to auto per spec).
func exclusionApplies(sty ResolvedStyle) bool {
	return sty.WrapFlow != "" && sty.Float == cssDisplayNone
}

// placeExclusion contributes one built exclusion box to the BFC's wrapping
// context. contentX/contentW are the BFC's content box, used to compare the
// free gaps on each side.
func (f *floatState) placeExclusion(fbox *box, wrapFlow string, contentX, contentW float64) {
	if f == nil || fbox == nil || fbox.w <= 0 {
		return
	}

	bottom := fbox.y + fbox.height

	switch wrapFlow {
	case wrapFlowClear:
		// Both edges intrude: an overlapping line has zero width and the
		// empty-line path (clearFloatBelow) drops it below the box.
		f.placeLeft(fbox, bottom, 0)
		f.placeRight(fbox, bottom, 0)
		f.leftShape, f.rightShape = nil, nil
	case wrapFlowStart, wrapFlowEnd, wrapFlowBoth, wrapFlowMinimum, wrapFlowMaximum:
		side := wrapFlowLineSide(fbox, wrapFlow, contentX, contentW)

		if side == wrapFlowStart {
			// Lines stay on the box's start side: the line ends at the box's
			// left edge (a right-edge intrusion).
			f.placeRight(fbox, bottom, 0)
			f.rightShape = nil
		} else {
			// Lines start after the box's right edge (a left-edge intrusion).
			f.placeLeft(fbox, bottom, 0)
			f.leftShape = nil
		}
	}
}

// wrapFlowLineSide resolves the side the line takes for the one-interval
// model: start means lines end at the box's left edge, end means lines start
// after the box's right edge.
func wrapFlowLineSide(fbox *box, wrapFlow string, contentX, contentW float64) string {
	leftGap := fbox.x - contentX
	rightGap := contentX + contentW - (fbox.x + fbox.w)

	switch wrapFlow {
	case wrapFlowStart:
		return wrapFlowStart
	case wrapFlowEnd:
		return wrapFlowEnd
	case wrapFlowMinimum:
		if leftGap < rightGap {
			return wrapFlowStart
		}

		return wrapFlowEnd
	default: // both, maximum: the larger free gap wins.
		if leftGap >= rightGap {
			return wrapFlowStart
		}

		return wrapFlowEnd
	}
}

// wrapThroughBypass swaps in a scratch floatState while a wrap-through: none
// element builds so exclusions from outside do not shorten its lines. The
// returned state (nil when no bypass applies) restores the live state.
func (e *engine) beginWrapThroughBypass(floats *floatState, sty ResolvedStyle) *floatState {
	if sty.WrapThrough != wrapThroughNone || floats == nil {
		return nil
	}

	saved := *floats
	*floats = newFloatState(floats.contentX, floats.contentW)

	return &saved
}

// endWrapThroughBypass restores the BFC state saved by beginWrapThroughBypass.
func endWrapThroughBypass(floats, saved *floatState) {
	if saved == nil || floats == nil {
		return
	}

	*floats = *saved
}
