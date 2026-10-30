package layout

import "sort"

// orphanLineEpsilonPt clusters text baselines into line rows. Ops on one
// formatted line share a baseline exactly; distinct rows differ by a full
// line height, so a 1pt window is far from either edge.
const orphanLineEpsilonPt = 1.0

// orphanMidDivisor halves a baseline span so the snapped band lands in the
// open gap between two text rows.
const orphanMidDivisor = 2.0

// stripLineBaselines returns the sorted distinct text baselines of ops in
// the half-open range [start, end). OpText Y is the baseline; OpBullet
// shares its line baseline. Non-ink ops (fills, rules, images) carry box
// geometry, not line rows, and are skipped.
func stripLineBaselines(ops []Op, start, end int) []float64 {
	if start < 0 {
		start = 0
	}

	if end > len(ops) {
		end = len(ops)
	}

	var collected []float64

	for k := start; k < end; k++ {
		if ops[k].Kind != OpText && ops[k].Kind != OpBullet {
			continue
		}

		collected = append(collected, ops[k].Y)
	}

	sort.Float64s(collected)

	out := collected[:0]

	for _, base := range collected {
		if len(out) > 0 && base-out[len(out)-1] < orphanLineEpsilonPt {
			continue
		}

		out = append(out, base)
	}

	return out
}

// snapAnonBandForOrphans moves the first multicol band boundary later so at
// least orphans line rows stay in the first column. lines holds the sorted
// strip baselines, top the strip top, totalH the strip height, bandH the
// even-split (or maxColH-capped) band, and maxColH the finite column cap
// (0 or negative means uncapped). It reports false, keeping bandH, when the
// even split already keeps enough lines or when the snap cannot fit: a
// non-positive orphans value, an empty strip, or a wanted band past a finite
// maxColH (definite height or remaining page), where the column box cannot
// grow. The wanted band is the mid-gap between the Nth and N+1th baselines,
// so exactly orphans rows fall below the first boundary; when the strip
// holds fewer rows than orphans the whole strip stays in column one.
func snapAnonBandForOrphans(top, totalH, bandH, maxColH float64, lines []float64, orphans int) (float64, bool) {
	if orphans < 1 || len(lines) == 0 || bandH <= 0 {
		return bandH, false
	}

	kept := countRowsBelow(lines, top+bandH)

	if kept >= orphans {
		return bandH, false
	}

	var want float64

	if orphans >= len(lines) {
		want = totalH
	} else {
		want = (lines[orphans-1]+lines[orphans])/orphanMidDivisor - top
	}

	if want <= bandH {
		return bandH, false
	}

	if maxColH > 0 && want > maxColH {
		return bandH, false
	}

	return want, true
}

// countRowsBelow counts sorted baselines strictly below bound. The strip
// assignment puts an op in column zero exactly when its baseline sits below
// top+bandH, so this mirrors that membership test.
func countRowsBelow(baselines []float64, bound float64) int {
	kept := 0

	for _, base := range baselines {
		if base >= bound {
			break
		}

		kept++
	}

	return kept
}
