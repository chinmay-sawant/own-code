package layout

import (
	"math"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

// resolveGridRows sizes the row tracks, returning the final row count and
// whether preferred-height growth should be locked (fixed template / auto-rows).
//
//nolint:cyclop,gocognit,funlen // row sizing has separate definite, auto, and padding branches
func resolveGridRows(
	eng *engine,
	sty ResolvedStyle,
	kids []*html.Node,
	numRows int,
	contentH, rowGap float64,
	definiteRows bool,
) ([]float64, int, bool) {
	var rows []float64

	lockRows := false

	if definiteRows {
		rowDefs := parseGridTrackDefs(sty.GridTemplateRows)
		// Pad/truncate defs to placed row count when template is shorter.
		for len(rowDefs) < numRows {
			rowDefs = append(rowDefs, gridAutoTrackDef(sty.GridAutoRows))
		}

		rowIntrinsics := measureTrackIntrinsics(eng, kids, len(rowDefs), false)
		rows = resolveGridTrackSizes(rowDefs, contentH, rowGap, eng, rowIntrinsics)
		lockRows = true
	}

	switch {
	case len(rows) == 0:
		rows = make([]float64, numRows)

		if mins := parseGridTrackFixedMins(sty.GridTemplateRows, eng); len(mins) > 0 {
			for rowIndex := range numRows {
				if rowIndex >= len(mins) {
					break
				}

				if mins[rowIndex] > 0 {
					rows[rowIndex] = mins[rowIndex]
				}
			}
		}

		if autoPt := gridAutoFixedPt(sty.GridAutoRows, eng); autoPt > 0 {
			templateCount := len(parseGridTrackDefs(sty.GridTemplateRows))
			for i := range numRows {
				if i >= templateCount && rows[i] == 0 {
					rows[i] = autoPt
				}
			}

			if templateCount == 0 {
				for i := range numRows {
					if rows[i] == 0 {
						rows[i] = autoPt
					}
				}
			}

			lockRows = true
		}
	case len(rows) < numRows:
		rows = padGridRowSizes(rows, numRows)
		if autoPt := gridAutoFixedPt(sty.GridAutoRows, eng); autoPt > 0 {
			for i := range rows {
				if rows[i] == 0 {
					rows[i] = autoPt
				}
			}

			lockRows = true
		}
	case len(rows) > numRows:
		numRows = len(rows)
	}

	return rows, numRows, lockRows
}

// gridAutoFixedPt returns a fixed grid-auto-rows/columns length in scaled pt,
// or 0 when the value is auto/fr/intrinsic.
func gridAutoFixedPt(raw string, eng *engine) float64 {
	def := gridAutoTrackDef(raw)
	if def.min.kind == trackFixed && def.max.kind == trackFixed &&
		nearFloat(def.min.val, def.max.val) {
		if eng == nil {
			return def.min.val
		}

		return eng.scalePt(def.min.val)
	}

	return 0
}

func nearFloat(left, right float64) bool {
	const eps = 1e-6

	if left > right {
		return left-right < eps
	}

	return right-left < eps
}

// padGridRowSizes extends a row-size slice to n entries, zero-filling.
func padGridRowSizes(rows []float64, n int) []float64 {
	padded := make([]float64, n)
	copy(padded, rows)

	return padded
}

type trackIntrinsic struct {
	minContent float64
	maxContent float64
}

// measureTrackIntrinsics estimates min/max-content contributions per track
// using text measure APIs. Spanning items contribute to the first track only
// (lite). axisColumns=true measures widths; false measures preferred heights.
func measureTrackIntrinsics(eng *engine, kids []*html.Node, nTracks int, axisColumns bool) []trackIntrinsic {
	if nTracks < 1 {
		return nil
	}

	out := make([]trackIntrinsic, nTracks)
	if eng == nil || len(kids) == 0 {
		return out
	}

	// Percentage widths on items are cyclic during intrinsic track sizing
	// (CSS Grid L1 §11.5) and resolve as auto: a width:100% image must
	// contribute its intrinsic width, not the viewport. Without this the
	// percentage echo lands in a track's min-content base and an fr track is
	// floored at it, stretching every item past the grid content box.
	wasIndefinite := eng.imgCBIndefinite

	if axisColumns {
		eng.imgCBIndefinite = true
	}

	for i, kid := range kids {
		cstate := eng.stylePtr(kid)
		tidx := i % nTracks

		var minVal, maxVal float64
		if axisColumns {
			// Column tracks need both contributions: the auto minimum of an
			// fr track is min-content, while auto/max-content maxes grow to
			// max-content. Using max-content for both sized 1fr tracks from
			// their content instead of the equal free-space share (C2).
			minVal, maxVal = eng.measureCellMinMax(kid, cstate)
		} else {
			// Height intrinsic: single-line text approximation via font size.
			maxVal = eng.scalePt(cstate.FontSize) * textLineHeightFactor
			maxVal += eng.scalePt(cstate.PaddingTop) + eng.scalePt(cstate.PaddingBottom) +
				eng.scalePt(cstate.BorderTop.Width) + eng.scalePt(cstate.BorderBottom.Width)
			minVal = maxVal
		}

		if minVal > out[tidx].minContent {
			out[tidx].minContent = minVal
		}

		if maxVal > out[tidx].maxContent {
			out[tidx].maxContent = maxVal
		}
	}

	if axisColumns {
		eng.imgCBIndefinite = wasIndefinite
	}

	return out
}

// gridTrackPlan holds the resolved base/limit sizes and fr factors for the
// tracks of one axis.
type gridTrackPlan struct {
	base, limit, frCoef []float64
	frSum               float64
}

// planGridTrackSides resolves each track's base/limit sizes and fr factors.
func planGridTrackSides(
	defs []gridTrackDef,
	contentSize float64,
	definite bool,
	eng *engine,
	intrinsics []trackIntrinsic,
) gridTrackPlan {
	node := len(defs)

	plan := gridTrackPlan{
		base:   make([]float64, node),
		limit:  make([]float64, node),
		frCoef: make([]float64, node),
		frSum:  0,
	}

	for idx, def := range defs {
		var intr trackIntrinsic
		if idx < len(intrinsics) {
			intr = intrinsics[idx]
		}

		plan.base[idx] = resolveTrackSide(def.min, contentSize, definite, eng, intr, true)
		lim := resolveTrackSide(def.max, contentSize, definite, eng, intr, false)

		switch {
		case def.max.kind == trackFr:
			plan.frCoef[idx] = def.max.val
			if plan.frCoef[idx] <= 0 {
				plan.frCoef[idx] = 1
			}

			plan.frSum += plan.frCoef[idx]
			plan.limit[idx] = math.Inf(1)
		case def.min.kind == trackFr:
			// Rare minmax(1fr, 200px): treat fr as flex with max cap.
			plan.frCoef[idx] = def.min.val
			if plan.frCoef[idx] <= 0 {
				plan.frCoef[idx] = 1
			}

			plan.frSum += plan.frCoef[idx]
			plan.base[idx] = 0
			plan.limit[idx] = lim
		default:
			plan.limit[idx] = lim
			if plan.limit[idx] < plan.base[idx] {
				plan.limit[idx] = plan.base[idx]
			}
		}
		// Auto max with auto/fixed min -> growable to content (use max-content as soft limit).
		applyAutoSoftLimit(plan.limit, def, intr, idx)
	}

	return plan
}

// applyAutoSoftLimit caps growable auto/max-content tracks at their measured
// max-content size.
func applyAutoSoftLimit(limit []float64, def gridTrackDef, intr trackIntrinsic, idx int) {
	if def.max.kind != trackAuto && def.max.kind != trackMaxContent {
		return
	}

	if intr.maxContent <= limit[idx] && !math.IsInf(limit[idx], 1) {
		return
	}

	if def.max.kind == trackMaxContent && intr.maxContent > 0 {
		limit[idx] = intr.maxContent
	}
}

// distributeGridTracks shares leftover space between fr tracks, or between
// growable auto tracks when no fr tracks exist. space is the grid content box
// minus the track gaps.
func distributeGridTracks(defs []gridTrackDef, base, limit, frCoef []float64, frSum, space float64) []float64 {
	if frSum > 0 {
		return distributeFrGridTracks(base, limit, frCoef, space)
	}

	bases := 0.0
	for i := range base {
		bases += base[i]
	}

	free := space - bases
	if free < 0 {
		free = 0
	}

	return distributeAutoGridTracks(defs, base, limit, free, frSum)
}

// distributeFrGridTracks sizes flexible tracks with the CSS Grid "find the
// size of an fr" algorithm (Grid L1 §11.7): the base sizes of non-flexible
// tracks come out of the available space, the remainder is divided by the
// flex factor sum, and each flexible track is floored at its own base size.
// A base larger than its equal share makes the track inflexible and restarts
// the division, so floors never inflate the other tracks' shares.
func distributeFrGridTracks(base, limit, frCoef []float64, space float64) []float64 {
	out := make([]float64, len(base))
	copy(out, base)

	active, activeSum, remaining := frGridTrackState(base, frCoef, space)
	frSize := frTrackShare(base, active, frCoef, remaining, activeSum)

	for idx := range out {
		if active[idx] {
			out[idx] = frSize * frCoef[idx]
		}
	}

	clampGridTracks(out, limit)

	return out
}

// frGridTrackState marks the flexible tracks active and returns the space
// left after the non-flexible base sizes.
func frGridTrackState(base, frCoef []float64, space float64) ([]bool, float64, float64) {
	active := make([]bool, len(base))
	activeSum := 0.0
	remaining := space

	for idx := range base {
		if frCoef[idx] > 0 {
			active[idx] = true
			activeSum += frCoef[idx]

			continue
		}

		remaining -= base[idx]
	}

	if remaining < 0 {
		remaining = 0
	}

	return active, activeSum, remaining
}

// frTrackShare resolves the final per-fr-unit share after flooring tracks
// whose base size exceeds their equal share.
func frTrackShare(base []float64, active []bool, frCoef []float64, remaining, activeSum float64) float64 {
	for activeSum > 0 {
		frSize := remaining / activeSum
		floored := false

		for idx := range base {
			if !active[idx] {
				continue
			}

			if share := frSize * frCoef[idx]; base[idx] > share {
				remaining -= base[idx]
				activeSum -= frCoef[idx]
				active[idx] = false
				floored = true
			}
		}

		if !floored {
			return frSize
		}

		if remaining < 0 {
			remaining = 0
		}
	}

	return 0
}

// clampGridTracks caps track sizes at their max track sizing function.
func clampGridTracks(sizes, limit []float64) {
	for idx := range sizes {
		if sizes[idx] > limit[idx] {
			sizes[idx] = limit[idx]
		}
	}
}

// isAutoTrackKind reports whether a track can absorb leftover space.
func isAutoTrackKind(kind trackSizeKind) bool {
	return kind == trackAuto || kind == trackMaxContent || kind == trackMinContent
}

// distributeAutoGridTracks shares leftover space equally among auto tracks.
func distributeAutoGridTracks(defs []gridTrackDef, base, limit []float64, free, frSum float64) []float64 {
	out := make([]float64, len(defs))
	autoIdx := []int{}

	for i, d := range defs {
		out[i] = base[i]

		if isAutoTrackKind(d.max.kind) {
			autoIdx = append(autoIdx, i)
		}
	}

	if free > 0 && len(autoIdx) > 0 && frSum == 0 {
		each := free / float64(len(autoIdx))
		for _, i := range autoIdx {
			out[i] += each
			if out[i] > limit[i] && !math.IsInf(limit[i], 1) {
				out[i] = limit[i]
			}
		}
	}

	return out
}

// sanitizeGridTrackSizes clamps NaN/negative track sizes to zero.
func sanitizeGridTrackSizes(out []float64) {
	for i := range out {
		if out[i] < 0 || math.IsNaN(out[i]) {
			out[i] = 0
		}
	}
}

// resolveGridTrackSizes distributes free space with fr, honoring minmax floors.
// Percent mins/maxes require a definite contentSize (>=0); otherwise % -> auto.
func resolveGridTrackSizes(
	defs []gridTrackDef,
	contentSize, gap float64,
	eng *engine,
	intrinsics []trackIntrinsic,
) []float64 {
	node := len(defs)
	if node == 0 {
		return nil
	}

	gapTotal := 0.0
	if node > 1 {
		gapTotal = gap * float64(node-1)
	}

	definite := contentSize >= 0 && !math.IsNaN(contentSize) && !math.IsInf(contentSize, 0)

	plan := planGridTrackSides(defs, contentSize, definite, eng, intrinsics)

	// Flexible tracks size from the space left after the non-flexible bases,
	// not from a free amount that already subtracted the flexible bases.
	space := contentSize - gapTotal
	if !definite {
		space = 0
	}

	out := distributeGridTracks(defs, plan.base, plan.limit, plan.frCoef, plan.frSum, space)
	sanitizeGridTrackSizes(out)

	return out
}

// resolveTrackSide resolves one min or max track size.
// pctSentinel: trackFixed with val < 0 stores -percent.
func resolveTrackSide(
	size gridTrackSize,
	contentSize float64,
	definite bool,
	eng *engine,
	intr trackIntrinsic,
	isMin bool,
) float64 {
	switch size.kind {
	case trackUnknown:
		// Zero-value size: never produced by parseTrackSize; resolves to zero.
		return 0
	case trackFixed:
		return resolveTrackFixedSide(size, contentSize, definite, eng, isMin)
	case trackFr:
		if isMin {
			return 0
		}

		return math.Inf(1)
	case trackMinContent:
		return intr.minContent
	case trackMaxContent:
		return intr.maxContent
	case trackAuto:
		if isMin {
			return intr.minContent // auto min ~= min-content lite
		}

		return math.Inf(1)
	}

	return 0
}

// resolveTrackFixedSide resolves a fixed (or percentage) track side.
// Percentage: cyclic honesty - indefinite container -> auto (0 min / inf max).
func resolveTrackFixedSide(size gridTrackSize, contentSize float64, definite bool, eng *engine, isMin bool) float64 {
	if size.val < 0 {
		if !definite || contentSize < 0 {
			if isMin {
				return 0
			}

			return math.Inf(1)
		}

		pct := -size.val

		return contentSize * pct / oneHundred
	}

	if eng != nil {
		return eng.scalePt(size.val)
	}

	return size.val
}
