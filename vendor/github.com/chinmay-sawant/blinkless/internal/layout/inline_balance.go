package layout

// text-wrap-style values with layout behavior. `balance` re-breaks a
// paragraph at a narrower width; `stable` wraps greedily like `auto`, which
// matches Chrome and needs no separate code path. `pretty` avoids a short
// last line by re-breaking narrower when the greedy tail is an orphan;
// its core width search lives beside balance, while the cascade acceptance
// and pack-loop hook stay owned elsewhere (named in prettyLineWidth).
const (
	textWrapStyleAuto       = "auto"
	textWrapStyleBalance    = "balance"
	textWrapStyleStable     = "stable"
	textWrapStylePretty     = "pretty"
	textWrapStyleAvoidShort = "avoid-short-last-line"
)

const (
	// Blink ParagraphLineBreaker balances at most six lines
	// (kMaxLinesForBalance) and starts the width bisection at 80% of the
	// average normal line width (paragraph_line_breaker.cc).
	maxBalanceLines       = 6
	minBalanceLines       = 2
	balanceMinWidthFactor = 0.8
)

// balanceLineWidth returns the line-break width for text-wrap-style: balance,
// or 0 when balancing does not apply.
//
// The model mirrors Blink's ParagraphLineBreaker bisection: pack the paragraph
// at the full width, opt out when it has fewer than two or more than six
// lines or when an item cannot fit whole (the packer would hyphenate or
// overflow-split it), then bisect for the widest width that still produces
// the same line count, starting at 80% of the average normal line width with
// a one CSS pixel epsilon.
//
// Blink tries its ScoreLineBreaker (dynamic programming over break points)
// before this bisection. The two agree when the greedy re-break balances the
// lines, which covers the pinned test cases; they can pick different break
// sets when uneven word widths leave several valid sets (named in the
// catalog limitations).
//
// Callers pass one forced-break segment at a time (inlineSegmentEnd); a
// forced break inside items still opts out defensively. Blocks with a
// first-line indent never reach here (balanceCanApply), because Chrome
// applies the balanced width without subtracting the indent, a quirk this
// engine does not copy.
//
// contentW and the returned width are engine points.
func balanceLineWidth(items []inlineItem, contentW, epsilon float64) float64 {
	normalLines, widthSum, ok := balanceBaseLines(items, contentW, epsilon)
	if !ok {
		return 0
	}

	lower := widthSum / float64(normalLines) * balanceMinWidthFactor
	if lower < 0 {
		lower = 0
	}

	upper := contentW

	for lower+epsilon < upper {
		middle := (upper + lower) / inlineHalfDivisor

		if lines, _, fits := countInlineLines(items, middle); fits && lines <= normalLines {
			upper = middle
		} else {
			lower = middle
		}
	}

	if upper >= contentW {
		return 0 // no narrower width keeps the line count; normal wrapping stands
	}

	return upper
}

// balanceBaseLines returns the full-width line count and width sum when the
// paragraph is eligible for balancing: a usable width and epsilon, and a
// normal line count between two and six. Indented blocks are excluded by
// balanceCanApply before this runs. It is split from balanceLineWidth to keep
// both functions under the cyclomatic limit.
func balanceBaseLines(items []inlineItem, contentW, epsilon float64) (int, float64, bool) {
	if contentW <= 0 || epsilon <= 0 {
		return 0, 0, false
	}

	lines, widthSum, ok := countInlineLines(items, contentW)
	if !ok || lines < minBalanceLines || lines > maxBalanceLines {
		return 0, 0, false
	}

	return lines, widthSum, true
}

// balanceCanApply reports whether a block's paragraphs may be balanced: no
// active floats, no line clamp, and a text-wrap-style: balance block without
// a first-line indent. See balanceLineWidth for the indent reason.
func balanceCanApply(floats *floatState, clampLimit int, blockStyle *ResolvedStyle) bool {
	if blockStyle == nil || clampLimit != 0 || blockStyle.TextIndent != 0 ||
		blockStyle.TextWrapStyle != textWrapStyleBalance {
		return false
	}

	return floats == nil || (!floats.hasLeft && !floats.hasRight)
}

// balanceSegmentWidth is the balanced break width for the forced-break
// segment starting at idx.
func (e *engine) balanceSegmentWidth(items []inlineItem, idx int, contentW float64) float64 {
	return balanceLineWidth(items[idx:inlineSegmentEnd(items, idx)], contentW, pxToPt(1)*e.scale)
}

// prettyCanApply reports whether a block's paragraphs may use pretty
// wrapping: no active floats, no line clamp, and a text-wrap-style: pretty
// block without a first-line indent. It mirrors balanceCanApply so the two
// stays aligned; the inline pack loop does not call it yet (it only consults
// balanceCanApply in inline.go), so pretty widths are consumed by tests and
// future wiring, never by shipped layout. Reference: Chrome 143.0.7499.40
// keeps pretty paragraphs greedy except for the short-tail fix below.
func prettyCanApply(floats *floatState, clampLimit int, blockStyle *ResolvedStyle) bool {
	if blockStyle == nil || clampLimit != 0 || blockStyle.TextIndent != 0 ||
		blockStyle.TextWrapStyle != textWrapStylePretty {
		return false
	}

	return floats == nil || (!floats.hasLeft && !floats.hasRight)
}

// prettyMinLastWords is the smallest acceptable last-line word count for
// text-wrap-style: pretty. A greedy tail of one word is an orphan; Chrome
// 143.0.7499.40 pulls another word down instead of leaving it alone.
const prettyMinLastWords = 2

// prettyLineStat is one greedily packed line: its advance and word count.
type prettyLineStat struct {
	width float64
	words int
}

// prettyLineWidth returns a narrower break width that avoids a short last
// line, or 0 when greedy wrapping already ends well.
//
// The model mirrors the balance bisection's greedy packer but optimizes for
// the tail instead of evenness: pack at the full width, keep greedy when it
// yields one line or a last line of at least prettyMinLastWords words, and
// otherwise scan downward for the widest width that keeps the same line
// count with an acceptable tail. The scan steps by epsilon (one CSS pixel
// at the caller's scale) so the result is stable across scales.
//
// End-to-end CSS wiring is a named gap, not part of this close: the cascade
// acceptance table (style_value_accept.go) still rejects pretty and the
// inline pack loop (inline.go) only consults balanceCanApply, so no shipped
// paragraph reaches here through layoutHTML. Unit tests drive this helper
// directly with synthetic items. Reference: Chrome 143.0.7499.40 avoids the
// orphan without adding lines.
//
// contentW and the returned width are engine points.
func prettyLineWidth(items []inlineItem, contentW, epsilon float64) float64 {
	if contentW <= 0 || epsilon <= 0 {
		return 0
	}

	normalLines, ok := prettyPackLines(items, contentW)
	if !ok || len(normalLines) < minBalanceLines {
		return 0
	}

	if prettyTailOK(normalLines[len(normalLines)-1]) {
		return 0
	}

	for width := contentW - epsilon; width > 0; width -= epsilon {
		candidate, ok := prettyPackLines(items, width)
		if !ok || len(candidate) != len(normalLines) {
			continue
		}

		if prettyTailOK(candidate[len(candidate)-1]) {
			return width
		}
	}

	return 0
}

// prettyTailOK reports whether a packed last line avoids the orphan fix.
func prettyTailOK(last prettyLineStat) bool {
	return last.words >= prettyMinLastWords
}

// prettyPackLines packs items greedily at the given width and returns one
// stat per line. ok is false when the width cannot hold an item whole or
// the items contain a forced break, matching countInlineLines.
func prettyPackLines(items []inlineItem, width float64) ([]prettyLineStat, bool) {
	var lines []prettyLineStat

	adv := 0.0
	words := 0

	flush := func() {
		lines = append(lines, prettyLineStat{width: adv, words: words})
		adv = 0
		words = 0
	}

	for _, item := range items {
		if item.forceBreak {
			return nil, false
		}

		itemW := item.marginL + item.w + item.marginR

		if adv > 0 && adv+itemW > width+1e-6 {
			flush()
		}

		if adv == 0 && itemW > width+1e-6 {
			return nil, false
		}

		adv += itemW
		words++
	}

	flush()

	return lines, true
}

// avoidShortMinLastWords is the smallest acceptable last-line word count for
// text-wrap-style: avoid-short-last-line. A greedy tail of one word is short;
// Chrome 143.0.7499.40 pulls another word down instead of leaving it alone.
const avoidShortMinLastWords = 2

// avoidShortCanApply reports whether a block's paragraphs may use
// avoid-short-last-line wrapping: no active floats, no line clamp, and a
// text-wrap-style: avoid-short-last-line block without a first-line indent.
// It mirrors prettyCanApply so the two stay aligned.
func avoidShortCanApply(floats *floatState, clampLimit int, blockStyle *ResolvedStyle) bool {
	if blockStyle == nil || clampLimit != 0 || blockStyle.TextIndent != 0 ||
		blockStyle.TextWrapStyle != textWrapStyleAvoidShort {
		return false
	}

	return floats == nil || (!floats.hasLeft && !floats.hasRight)
}

// avoidShortTailOK reports whether a packed last line avoids the orphan fix.
func avoidShortTailOK(last prettyLineStat) bool {
	return last.words >= avoidShortMinLastWords
}

// avoidShortLineWidth returns a narrower break width that avoids a short last
// line, or 0 when greedy wrapping already ends well.
//
// It mirrors prettyLineWidth: pack at the full width, keep greedy when it
// yields one line or a last line of at least avoidShortMinLastWords words,
// and otherwise scan downward for the widest width that keeps the same line
// count with an acceptable tail. The scan steps by epsilon (one CSS pixel at
// the caller's scale) so the result is stable across scales. Reference:
// Chrome 143.0.7499.40 avoids the orphan without adding lines.
//
// contentW and the returned width are engine points.
func avoidShortLineWidth(items []inlineItem, contentW, epsilon float64) float64 {
	if contentW <= 0 || epsilon <= 0 {
		return 0
	}

	normalLines, ok := prettyPackLines(items, contentW)
	if !ok || len(normalLines) < minBalanceLines {
		return 0
	}

	if avoidShortTailOK(normalLines[len(normalLines)-1]) {
		return 0
	}

	for width := contentW - epsilon; width > 0; width -= epsilon {
		candidate, ok := prettyPackLines(items, width)
		if !ok || len(candidate) != len(normalLines) {
			continue
		}

		if avoidShortTailOK(candidate[len(candidate)-1]) {
			return width
		}
	}

	return 0
}

// inlineSegmentEnd returns the index of the first forced break at or after
// start, or len(items) when the segment runs to the end. balanceLineWidth is
// applied per forced-break segment, matching Blink, which balances across
// `<br>` boundaries.
func inlineSegmentEnd(items []inlineItem, start int) int {
	for idx := start; idx < len(items); idx++ {
		if items[idx].forceBreak {
			return idx
		}
	}

	return len(items)
}

// countInlineLines packs items greedily at the given line width and returns
// the line count and the sum of the line advances. ok is false when the width
// cannot hold an item whole or the items contain a forced break: the dry run
// does not model hyphenation, overflow splitting, or `<br>` segments, so
// balancing must not apply.
func countInlineLines(items []inlineItem, width float64) (int, float64, bool) {
	lines := 0
	widthSum := 0.0

	for idx := 0; idx < len(items); {
		lineW := width

		adv := 0.0

		for idx < len(items) {
			item := items[idx]
			if item.forceBreak {
				return 0, 0, false
			}

			itemW := item.marginL + item.w + item.marginR
			if adv > 0 && adv+itemW > lineW+1e-6 {
				break
			}

			if adv == 0 && itemW > lineW+1e-6 {
				return 0, 0, false
			}

			adv += itemW
			idx++
		}

		lines++
		widthSum += adv
	}

	return lines, widthSum, true
}
