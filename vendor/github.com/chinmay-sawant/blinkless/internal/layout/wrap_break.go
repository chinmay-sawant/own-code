// Line and flex wrap opportunities (css-text-4 lite): wrap-before,
// wrap-after, wrap-inside.
//
// These are not page breaks. They change line and flex geometry on the one
// canvas this engine returns (internal/layout/css_behavior_named_flow_test.go
// pins that a wrap-before: line box stays in flow above Options.Height):
//
//	line        force a line break before (wrap-before) or after
//	            (wrap-after) the element
//	avoid,      skip a soft wrap at that boundary when the next item fits
//	avoid-line  within a bounded slack (one em); longer content wraps anyway
//	            and never overflows without bound
//	flex        on an inline-level box this is ignored; on a flex item it
//	            forces a new flex line (wrap-before) or ends the current one
//	            (wrap-after) even when the item would fit
//	avoid-flex  keeps a flex item on the current line when it fits (the
//	            default; a nowrap container ignores both flex values)
//
// wrap-inside: avoid keeps an inline element's text on one line when the
// text fits the content width: its items are glued across soft wrap
// opportunities and the whole group still moves to the next line when it
// does not fit the remainder. Text wider than the content box wraps normally.
package layout

import (
	"strings"
)

// Wrap-break property names and keywords.
const (
	wrapBeforePropName = "wrap-before"
	wrapAfterPropName  = "wrap-after"
	wrapInsidePropName = "wrap-inside"

	wrapBreakLine      = "line"
	wrapBreakAvoid     = "avoid"
	wrapBreakAvoidLine = "avoid-line"
	wrapBreakFlex      = "flex"
	wrapBreakAvoidFlex = "avoid-flex"
)

// applyWrapBreakProps owns wrap-before, wrap-after, and wrap-inside.
// Registered in styleGroups (style_cascade.go).
func applyWrapBreakProps(
	style *ResolvedStyle, prop, value string, _ float64, _ *styleContext, _ *ResolvedStyle, _ bool,
) bool {
	used, ok := parseWrapBreak(value)
	if !ok {
		return false
	}

	switch prop {
	case wrapBeforePropName:
		style.WrapBefore = used
	case wrapAfterPropName:
		style.WrapAfter = used
	case wrapInsidePropName:
		style.WrapInside = used
	default:
		return false
	}

	return true
}

// parseWrapBreak parses auto | line | avoid | avoid-line | flex | avoid-flex.
// auto (the initial) stores "".
func parseWrapBreak(value string) (string, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return "", false
	}

	if cssWideKeyword(trimmed) || trimmed == wrapFlowAuto {
		return "", true
	}

	switch trimmed {
	case wrapBreakLine, wrapBreakAvoid, wrapBreakAvoidLine, wrapBreakFlex, wrapBreakAvoidFlex:
		return trimmed, true
	default:
		return "", false
	}
}

// wrapBreakDeclarationValueAccepted is the cascade/@supports gate for the
// wrap-break properties.
func wrapBreakDeclarationValueAccepted(prop, value string) (bool, bool) {
	switch prop {
	case wrapBeforePropName, wrapAfterPropName, wrapInsidePropName:
		_, ok := parseWrapBreak(value)

		return ok, true
	default:
		return false, false
	}
}

// wrapBreakLineBefore reports wrap-before: line (forces a line break before
// the element).
func wrapBreakLineBefore(sty ResolvedStyle) bool {
	return sty.WrapBefore == wrapBreakLine
}

// wrapBreakLineAfter reports wrap-after: line.
func wrapBreakLineAfter(sty ResolvedStyle) bool {
	return sty.WrapAfter == wrapBreakLine
}

// wrapBreakAvoidsBefore reports wrap-before: avoid | avoid-line.
func wrapBreakAvoidsBefore(sty ResolvedStyle) bool {
	return sty.WrapBefore == wrapBreakAvoid || sty.WrapBefore == wrapBreakAvoidLine
}

// wrapBreakAvoidsAfter reports wrap-after: avoid | avoid-line.
func wrapBreakAvoidsAfter(sty ResolvedStyle) bool {
	return sty.WrapAfter == wrapBreakAvoid || sty.WrapAfter == wrapBreakAvoidLine
}

// wrapBreakInsideAvoid reports wrap-inside: avoid | avoid-line.
func wrapBreakInsideAvoid(sty ResolvedStyle) bool {
	return sty.WrapInside == wrapBreakAvoid || sty.WrapInside == wrapBreakAvoidLine
}

// wrapBreakForcesFlexBefore reports wrap-before: flex on a flex item.
func wrapBreakForcesFlexBefore(sty *ResolvedStyle) bool {
	return sty.WrapBefore == wrapBreakFlex
}

// wrapBreakForcesFlexAfter reports wrap-after: flex on a flex item.
func wrapBreakForcesFlexAfter(sty *ResolvedStyle) bool {
	return sty.WrapAfter == wrapBreakFlex
}

// flexWrapBreakBefore reports wrap-before: flex on one measured flex item.
func (e *engine) flexWrapBreakBefore(item flexMeas) bool {
	st := e.stylePtr(item.n)

	return st != nil && wrapBreakForcesFlexBefore(st)
}

// flexWrapBreakAfter reports wrap-after: flex on one measured flex item.
func (e *engine) flexWrapBreakAfter(item flexMeas) bool {
	st := e.stylePtr(item.n)

	return st != nil && wrapBreakForcesFlexAfter(st)
}

// applyWrapBreakFlags marks the items one inline element contributed:
// the first/last item carry the avoid boundary flags, and wrap-inside: avoid
// marks the whole run as a keep-together group when it fits the available
// line width (e.inlineCBW is set by the collector; 0 means unknown, so the
// group is left breakable).
func (e *engine) applyWrapBreakFlags(sty ResolvedStyle, items []inlineItem, start int) {
	if start >= len(items) {
		return
	}

	if wrapBreakAvoidsBefore(sty) && !items[start].forceBreak {
		items[start].avoidBefore = true
	}

	if wrapBreakAvoidsAfter(sty) && !items[len(items)-1].forceBreak {
		items[len(items)-1].avoidAfter = true
	}

	if !wrapBreakInsideAvoid(sty) {
		return
	}

	if !e.wrapInsideGroupFits(items, start) {
		return
	}

	for idx := start; idx < len(items); idx++ {
		items[idx].avoidInside = true
	}
}

// wrapInsideGroupFits reports whether the items one element contributed fit
// the available inline line width as one group. e.inlineCBW is set by the
// collector; 0 means unknown, so the group stays breakable.
func (e *engine) wrapInsideGroupFits(items []inlineItem, start int) bool {
	width := e.inlineCBW
	if width <= 0 {
		return false
	}

	total := 0.0

	for idx := start; idx < len(items); idx++ {
		total += items[idx].marginL + items[idx].w + items[idx].marginR
	}

	return total <= width+inlineFitEpsilon
}

// wrapInsideGroupFitsContent reports whether the wrap-inside: avoid group
// that opens at items[start] fits the content width as one unit.
func (e *engine) wrapInsideGroupFitsContent(items []inlineItem, start int, contentW float64) bool {
	return wrapInsideGroupWidth(items, start) <= contentW+inlineFitEpsilon
}

// wrapInsideGroupFitsRemainder reports whether the group fits the remainder
// of the current line. A group that fits the content width but not the
// remainder moves to the next line as a unit.
func (e *engine) wrapInsideGroupFitsRemainder(items []inlineItem, start int, remainder float64) bool {
	return wrapInsideGroupWidth(items, start) <= remainder+inlineFitEpsilon
}

// wrapInsideGroupOpensAt reports whether items[idx] is the first item of a
// wrap-inside: avoid group.
func wrapInsideGroupOpensAt(items []inlineItem, idx int) bool {
	return idx <= 0 || !items[idx-1].avoidInside
}

// wrapInsideGroupWidth returns the total advance of the wrap-inside: avoid
// group that opens at start: the run of consecutive items flagged avoidInside.
func wrapInsideGroupWidth(items []inlineItem, start int) float64 {
	total := 0.0

	for idx := start; idx < len(items) && items[idx].avoidInside; idx++ {
		total += items[idx].marginL + items[idx].w + items[idx].marginR
	}

	return total
}

// wrapBreakGlues reports that the packer should skip the soft wrap before
// items[idx]: an avoid boundary within one em of slack, or a soft wrap
// inside a wrap-inside: avoid group.
func (e *engine) wrapBreakGlues(items []inlineItem, idx, start int, adv, _ float64) bool {
	if idx <= 0 || idx >= len(items) || idx <= start {
		return false
	}

	item, prev := items[idx], items[idx-1]
	if item.forceBreak || prev.forceBreak {
		return false
	}

	if item.avoidInside && prev.avoidInside {
		return true
	}

	if !item.avoidBefore && !prev.avoidAfter {
		return false
	}

	return wrapBreakFitsSlack(item, e.scale, adv)
}

// wrapBreakFitsSlack reports whether the advance fits within one em of slack
// at the item's font size. A missing style or a non-positive size means no
// slack, so the wrap is not glued.
func wrapBreakFitsSlack(item inlineItem, scale, adv float64) bool {
	if item.style == nil {
		return false
	}

	slack := item.style.FontSize * scale
	if slack <= 0 {
		return false
	}

	return adv <= slack
}
