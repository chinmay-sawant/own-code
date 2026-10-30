// flow-tolerance (css-grid-3 masonry lite): the masonry placement slack.
//
// Only the row-masonry packer reads it (grid-template-rows: masonry, see
// emitMasonryItems in internal/layout/grid_masonry.go). The property lives on
// the grid container:
//
//	normal    one em at the container's font size (the initial)
//	<length>  that slack in points; zero means no slack
//	infinite  keeps today's shortest-column choice even when the item would
//	          stick out past the next column
//
// Packing rule (infinite excepted): candidates are sorted by their current
// column bottom; a candidate is skipped when the item placed there would end
// more than the slack below the next candidate's bottom (the item "sticks
// out" past the neighboring column). Tied candidates never skip (placing the
// item at a level column is in-order fill), the last candidate accepts the
// item since nothing taller remains, and the shortest column is the fallback.
// A finite tolerance therefore skips to the next column when the item's block
// size would extend past the neighboring column bottom by more than the
// tolerance.
//
// Lite: the item is measured once at the shortest column's width and then
// moved; unequal track widths do not re-measure it.
package layout

import (
	"sort"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
)

// flow-tolerance property name and keywords.
const (
	flowTolerancePropName = "flow-tolerance"
	flowToleranceNormal   = "normal"
	flowToleranceInfinite = "infinite"
)

// masonryTolerance is the resolved container knob for the packer.
type masonryTolerance struct {
	infinite bool
	slack    float64 // points
}

// applyFlowToleranceProps owns flow-tolerance. Registered in styleGroups
// (style_cascade.go). normal stores the initial marker (-1) so an explicit
// normal and an undeclared value agree.
func applyFlowToleranceProps(
	style *ResolvedStyle, prop, value string, fsize float64, _ *styleContext, _ *ResolvedStyle, _ bool,
) bool {
	if prop != flowTolerancePropName {
		return false
	}

	points, infinite, ok := parseFlowTolerance(value, fsize)
	if !ok {
		return true
	}

	style.FlowToleranceInfinite = infinite
	style.FlowTolerancePt = points

	return true
}

// parseFlowTolerance parses normal | <length> | infinite. normal (and a
// CSS-wide reset) stores pt = -1, the same marker as the initial value.
func parseFlowTolerance(value string, fsize float64) (float64, bool, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return 0, false, false
	}

	if cssWideKeyword(trimmed) || trimmed == flowToleranceNormal {
		return -1, false, true
	}

	if trimmed == flowToleranceInfinite {
		return 0, true, true
	}

	if strings.HasPrefix(trimmed, "-") {
		return 0, false, false
	}

	val, unit, ok := css.ParseLength(trimmed)
	if !ok || val < 0 {
		return 0, false, false
	}

	pt, converted := lengthToPt(val, unit, fsize)
	if !converted {
		return 0, false, false
	}

	return pt, false, true
}

// flowToleranceDeclarationValueAccepted is the cascade/@supports gate for
// flow-tolerance.
func flowToleranceDeclarationValueAccepted(prop, value string) (bool, bool) {
	if prop != flowTolerancePropName {
		return false, false
	}

	_, _, ok := parseFlowTolerance(value, 0)

	return ok, true
}

// masonryToleranceOf resolves the container's used slack. A nil style keeps
// the shortest-column choice (hand-built engines and tests).
func masonryToleranceOf(eng *engine, container *ResolvedStyle) masonryTolerance {
	if container == nil {
		return masonryTolerance{ //nolint:exhaustruct // infinite is false; slack still zero
		}
	}

	if container.FlowToleranceInfinite {
		return masonryTolerance{infinite: true, slack: 0}
	}

	points := container.FlowTolerancePt
	if points < 0 {
		points = container.FontSize
	}

	return masonryTolerance{infinite: false, slack: eng.scalePt(points)}
}

// column returns the column for one auto-placed item of block size itemH.
func (t masonryTolerance) column(colBot []float64, span int, itemH float64) int {
	if t.infinite {
		return shortestMasonryColumn(colBot, span)
	}

	type candidate struct {
		col   int
		start float64
	}

	var candidates []candidate

	for col := 0; col+span <= len(colBot); col++ {
		candidates = append(candidates, candidate{col: col, start: masonrySpanStart(colBot, col, span)})
	}

	if len(candidates) == 0 {
		return 0
	}

	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].start < candidates[j].start })

	for idx, cand := range candidates {
		if idx == len(candidates)-1 {
			// Nothing taller remains to overhang; the last candidate accepts
			// the item.
			return cand.col
		}

		next := candidates[idx+1].start
		if next <= cand.start {
			// A level (tied) column fills in order.
			return cand.col
		}

		if cand.start+itemH <= next+t.slack {
			return cand.col
		}
	}

	return candidates[0].col
}
