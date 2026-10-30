// Flex line formation: wrapping measured items into lines, the line-fit
// trial, and the per-line order helpers. Extracted from flex.go (module-wise
// split, no behavior change) so the file-size gate stays satisfied; see
// flex.go for the flex layout overview.
package layout

import (
	"slices"
)

// flexWrapLines packs measured items into flex lines, honoring wrap mode and
// reverse direction (item order within each line, then line order). The trial
// line is checked after flex grow/shrink and min/max clamping, because a
// content-based minimum can make a line overflow even when raw flex bases fit.
func (e *engine) flexWrapLines(
	items []flexMeas, wrap, reverse, wrapReverse bool, colGap, contentW float64,
) [][]flexMeas {
	if !wrap {
		if reverse {
			reverseFlexMeas(items)
		}

		return [][]flexMeas{items}
	}

	var lines [][]flexMeas

	line := make([]flexMeas, 0, len(items))
	// Trial, width, and frozen scratches are sized once per container. Every
	// wrap trial reuses them instead of allocating a trial line plus width
	// and clamp slices per item.
	trial := make([]flexMeas, len(items))
	widthScratch := make([]float64, len(items))
	frozenScratch := make([]bool, len(items))

	poll := newCtxPoll(e.ctx)
	for _, item := range items {
		if poll.poll() {
			e.err = poll.err

			return nil
		}

		line = e.flexWrapPlaceItem(&lines, line, item, reverse, colGap, contentW, trial, widthScratch, frozenScratch)
	}

	if len(line) > 0 {
		lines = append(lines, finalizeFlexLine(line, reverse))
	}

	if wrapReverse {
		slices.Reverse(lines)
	}

	return lines
}

// flexWrapPlaceItem places one item on the current flex line, closing the line
// first when wrap-before: flex asks for it or the item does not fit after flex
// grow/shrink, and closing again when wrap-after: flex asks for it. It returns
// the (possibly empty) current line.
func (e *engine) flexWrapPlaceItem(
	lines *[][]flexMeas, line []flexMeas, item flexMeas,
	reverse bool, colGap, contentW float64,
	trial []flexMeas, widthScratch []float64, frozenScratch []bool,
) []flexMeas {
	// wrap-before: flex forces a new flex line even when the item fits
	// (wrap_break.go; nowrap returns before this loop).
	if len(line) > 0 && e.flexWrapBreakBefore(item) {
		*lines = append(*lines, finalizeFlexLine(line, reverse))
		line = nil
	}

	// The fit trial copies the committed line plus the candidate into the
	// container-owned scratch instead of allocating a trial slice per item.
	n := copy(trial, line)
	trial[n] = item

	if len(line) > 0 && !e.flexLineFitsInto(trial[:n+1], contentW, colGap, widthScratch[:n+1], frozenScratch[:n+1]) {
		*lines = append(*lines, finalizeFlexLine(line, reverse))
		line = nil
	}

	line = append(line, item)

	// wrap-after: flex ends the line after the item.
	if e.flexWrapBreakAfter(item) {
		*lines = append(*lines, finalizeFlexLine(line, reverse))
		line = nil
	}

	return line
}

func (e *engine) flexLineFitsInto(items []flexMeas, contentW, gap float64, widths []float64, frozen []bool) bool {
	e.flexLineWidthsInto(items, contentW, gap, widths, frozen)
	total := gap * float64(len(widths)-1)

	for idx, width := range widths {
		total += width + flexRowNormalMargins(e, items[idx])
	}

	if total > contentW+layoutEpsilon {
		return false
	}

	// A wrapped line may be a few points over its raw hypothetical sizes when
	// flex-shrink resolves font-metric rounding. Do not use a large shrink to
	// defeat wrapping: that collapses later items into unreadable slivers and
	// also pulls full-width captions into the row. A candidate is therefore
	// accepted only when every positive-base item remains within ten percent of
	// its hypothetical size.
	const maxLineShrink = 0.10

	for idx, item := range items {
		if item.baseW <= layoutEpsilon || item.shrink <= 0 {
			continue
		}

		if widths[idx] < item.baseW*(1-maxLineShrink) {
			return false
		}
	}

	return true
}

func finalizeFlexLine(line []flexMeas, reverse bool) []flexMeas {
	if reverse {
		reverseFlexMeas(line)
	}

	return line
}

func reverseFlexMeas(line []flexMeas) {
	slices.Reverse(line)
}
