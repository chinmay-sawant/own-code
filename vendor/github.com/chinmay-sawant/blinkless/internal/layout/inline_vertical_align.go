package layout

import (
	"strconv"
	"strings"
)

// vertical-align keyword spellings and the proportional sub/super shifts. A
// positive shift raises a run from the baseline; every consumer subtracts it.
const (
	verticalAlignSub   = "sub"
	verticalAlignSuper = "super"

	verticalAlignSubRatio   = 0.2
	verticalAlignSuperRatio = 0.4
)

// alignedInlineTop is the canvas Y of an atomic inline box (image or
// inline-block) border box. Keywords match CSS vertical-align; a length shift
// raises (positive) or lowers (negative) a baseline-aligned box. The margin
// box, not the border box, is what the keyword aligns (CSS 2.1 §10.8.1).
func (e *engine) alignedInlineTop(item *inlineItem, lineY, lineH, baseline float64) float64 {
	marginBoxH := item.marginT + item.h + item.marginB

	if item.style == nil {
		return baseline - item.h - item.marginB
	}

	switch item.style.VerticalAlign {
	case cssVerticalAlignTop:
		return lineY + item.marginT
	case cssVerticalAlignMiddle:
		return lineY + (lineH-marginBoxH)/2 + item.marginT
	case cssVerticalAlignBottom:
		return lineY + lineH - item.h - item.marginB
	default:
		ascent := item.marginT + item.h
		if item.marginBaseline {
			ascent += item.marginB
		}

		return baseline - ascent + item.marginT - e.scalePt(e.effectiveVerticalAlignShift(item.style))
	}
}

// effectiveVerticalAlignShift maps vertical-align keywords and lengths to a
// pt shift where positive raises. Handles sub/super and % of line-height
// in addition to plain <length> stored in VerticalAlignShift.
func (e *engine) effectiveVerticalAlignShift(style *ResolvedStyle) float64 {
	if style == nil {
		return 0
	}

	switch strings.ToLower(strings.TrimSpace(style.VerticalAlign)) {
	case verticalAlignSub:
		return style.FontSize * -verticalAlignSubRatio
	case verticalAlignSuper:
		return style.FontSize * verticalAlignSuperRatio
	}
	// If VerticalAlign looks like a percent (e.g. "50%"), compute against
	// line-height per CSS spec.
	if pct := strings.TrimSpace(style.VerticalAlign); strings.HasSuffix(pct, "%") {
		if percent, ok := parsePercent(pct); ok {
			lineH := e.lineHeightOf(style)

			return lineH * percent / oneHundred
		}
	}

	return style.VerticalAlignShift + e.alignmentBaselineShift(style)
}

// Baseline selector spellings and fallbacks shared by the metric shift
// table below. Alphabetic, ideographic, and middle reuse the existing
// initial-letter and vertical-align constants (same CSS keywords).
const (
	baselineAuto                 = "auto"
	baselineName                 = "baseline"
	baselineCentral              = "central"
	baselineMath                 = "mathematical"
	baselineBeforeEdge           = "text-before-edge"
	baselineBeforeEdgeAlt        = "before-edge"
	baselineAfterEdge            = "text-after-edge"
	baselineAfterEdgeAlt         = "after-edge"
	baselineHalfDivisor          = 2
	baselineFallbackFontSize     = 12
	baselineXHeightFallbackRatio = 0.5
)

// baselineFontMetrics returns the ascent, descent, and x-height behind style
// in style points, with print fallbacks when the face or its metrics are
// missing. Ascent sits above alphabetic, descent below it.
func (e *engine) baselineFontMetrics(style *ResolvedStyle) (float64, float64, float64) {
	size := style.FontSize
	if size <= 0 {
		size = baselineFallbackFontSize
	}

	face := e.faceFor(style)
	if face == nil || face.UnitsPerEm() <= 0 {
		return size * fallbackAscentRatio, size * fallbackDescentRatio, size * baselineXHeightFallbackRatio
	}

	upem := float64(face.UnitsPerEm())
	ascent := float64(face.Ascent()) / upem * size

	if ascent < 0 {
		ascent = 0
	}

	descent := float64(-face.Descent()) / upem * size

	if descent < 0 {
		descent = 0
	}

	xHeight := float64(face.XHeight()) / upem * size

	if xHeight <= 0 {
		xHeight = size * baselineXHeightFallbackRatio
	}

	return ascent, descent, xHeight
}

// baselineShiftFromMetrics maps one baseline selector to a pt shift where
// positive raises above alphabetic. Alphabetic is the zero reference.
// Hanging keeps the fixed Chrome-measured ratio because faces expose no
// hanging metric. Ideographic sits one descent below alphabetic, central
// halves the em box, middle and mathematical halve the x-height, and the
// before/after edges sit at ascent/descent. Selectors outside the table pin
// to alphabetic (0). Reference: Chrome 143.0.7499.40.
func (e *engine) baselineShiftFromMetrics(style *ResolvedStyle, selector string) float64 {
	switch selector {
	case "", baselineAuto, initialLetterAlignAlpha, baselineName:
		return 0
	case dominantBaselineHanging:
		return style.FontSize * dominantBaselineHangingRatio
	}

	ascent, descent, xHeight := e.baselineFontMetrics(style)

	switch selector {
	case initialLetterAlignIdeo:
		return -descent
	case baselineCentral:
		return (ascent - descent) / baselineHalfDivisor
	case cssVerticalAlignMiddle, baselineMath:
		return xHeight / baselineHalfDivisor
	case baselineBeforeEdge, baselineBeforeEdgeAlt:
		return ascent
	case baselineAfterEdge, baselineAfterEdgeAlt:
		return -descent
	default:
		return 0
	}
}

// dominantBaselineHangingRatio is the hanging-baseline rise above alphabetic
// as a fraction of font size. Chrome 143.0.7499.40 aligns a 16px hanging run
// about 5px above alphabetic (5/16 = 0.3125); the engine has no font baseline
// tables, so this fixed ratio stands in for the face hanging metric.
const dominantBaselineHangingRatio = 0.3125

// alignmentBaselineShift maps alignment-baseline the same way. auto and
// baseline defer to the dominant baseline; any other selector wins over it.
// Like the dominant shift it applies only when vertical-align is the
// baseline default. Reference: Chrome 143.0.7499.40.
func (e *engine) alignmentBaselineShift(style *ResolvedStyle) float64 {
	if style == nil {
		return 0
	}

	switch strings.ToLower(strings.TrimSpace(style.VerticalAlign)) {
	case "baseline", "":
		// Baseline default: the alignment baseline applies.
	default:
		return 0
	}

	switch selector := strings.ToLower(strings.TrimSpace(style.AlignmentBaseline)); selector {
	case "", "auto", "baseline":
		return e.baselineShiftFromMetrics(style, strings.ToLower(strings.TrimSpace(style.DominantBaseline)))
	default:
		return e.baselineShiftFromMetrics(style, selector)
	}
}

func parsePercent(value string) (float64, bool) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasSuffix(trimmed, "%") {
		return 0, false
	}

	num := strings.TrimSpace(strings.TrimSuffix(trimmed, "%"))
	parsed, err := strconv.ParseFloat(num, 64)

	if err != nil {
		return 0, false
	}

	return parsed, true
}
