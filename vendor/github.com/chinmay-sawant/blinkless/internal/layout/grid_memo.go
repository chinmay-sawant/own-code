package layout

import (
	"math"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

// gridMeasureKey identifies one grid-item preferred-height measurement. It
// mirrors flexBaseKey: node pins the subtree, style pins the resolved style
// identity so a measurement under a style override never stands in for the
// base-cascade one, and widthBits is math.Float64bits of the containing width.
// cbBits/cbSet pin the percentage-height basis, the ambient that moves a
// height at a fixed width when an ancestor resolves descendant % heights
// against a definite cross size in one pass and indefinite in another.
// imgMaxBits pins the replaced-image clamp: the measure pass runs under the
// ambient clamp while the emit pass clamps to the cell width, so a responsive
// max-width:100% image measures differently and must never share an entry.
type gridMeasureKey struct {
	node       *html.Node
	style      *ResolvedStyle
	widthBits  uint64
	cbBits     uint64
	cbSet      bool
	imgMaxBits uint64
}

// gridMeasureResult is the cached outcome of one grid-item preferred-height
// measurement. sticky and groups are the engine counter deltas the skipped
// build would have consumed (sticky IDs, blend-group IDs); replaying them on
// a hit keeps every later ID identical to an uncached run.
type gridMeasureResult struct {
	prefH  float64
	sticky int
	groups int
}

// gridMeasureBits normalizes a width-like ambient for map keys. Negative zero
// maps to zero so equal widths share a key; non-finite values are uncacheable.
func gridMeasureBits(value float64) (uint64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}

	if value == 0 {
		return 0, true
	}

	return math.Float64bits(value), true
}

// gridPrefHKey builds the memo key for measuring node at width under the
// engine's current ambients. valid is false for a nil node or a non-finite
// width, height basis, or image clamp, and that measurement runs uncached.
func gridPrefHKey(eng *engine, node *html.Node, width float64) (gridMeasureKey, bool) {
	if eng == nil || node == nil {
		return gridMeasureKey{}, false //nolint:exhaustruct // zero key means uncacheable
	}

	widthBits, valid := gridMeasureBits(width)
	if !valid {
		return gridMeasureKey{}, false //nolint:exhaustruct // zero key means uncacheable
	}

	var cbBits uint64

	cbSet := eng.flowCBHeight != nil

	if cbSet {
		var cbValid bool

		if cbBits, cbValid = gridMeasureBits(*eng.flowCBHeight); !cbValid {
			return gridMeasureKey{}, false //nolint:exhaustruct // zero key means uncacheable
		}
	}

	imgMaxBits, valid := gridMeasureBits(eng.imgMaxW)
	if !valid {
		return gridMeasureKey{}, false //nolint:exhaustruct // zero key means uncacheable
	}

	key := gridMeasureKey{
		node:       node,
		style:      eng.stylePtr(node),
		widthBits:  widthBits,
		cbBits:     cbBits,
		cbSet:      cbSet,
		imgMaxBits: imgMaxBits,
	}

	return key, true
}

// gridPrefHLookup returns the cached preferred height for measuring node at
// width when an identical measurement already ran. On a hit it replays the
// sticky and blend-group counter deltas the skipped build would have consumed.
func (e *engine) gridPrefHLookup(node *html.Node, width float64) (float64, bool) {
	if e == nil || e.gridMemoBypass {
		return 0, false
	}

	key, valid := gridPrefHKey(e, node, width)
	if !valid || e.gridMeasureCache == nil {
		return 0, false
	}

	cached, found := e.gridMeasureCache[key]
	if !found {
		return 0, false
	}

	e.stickySeq += cached.sticky
	e.nextGroupID += cached.groups

	return cached.prefH, true
}

// gridPrefHStore records one grid-item preferred-height measurement for the
// rest of the Layout run.
func (e *engine) gridPrefHStore(node *html.Node, width, prefH float64, stickyDelta, groupsDelta int) {
	if e == nil || e.gridMemoBypass {
		return
	}

	key, valid := gridPrefHKey(e, node, width)
	if !valid {
		return
	}

	if e.gridMeasureCache == nil {
		e.gridMeasureCache = make(map[gridMeasureKey]gridMeasureResult)
	}

	e.gridMeasureCache[key] = gridMeasureResult{prefH: prefH, sticky: stickyDelta, groups: groupsDelta}
}

// measureGridItemPrefH builds one grid item without emitting ops and returns
// its border-box height. Repeated measurements of the same node at the same
// containing width (nested grids rebuilt by an outer measure-then-emit pass,
// grids under flex/table/shrink-to-fit remeasures) hit the per-run cache and
// skip the subtree rebuild.
func measureGridItemPrefH(eng *engine, node *html.Node, contW, curX, buildY float64) float64 {
	if prefH, hit := eng.gridPrefHLookup(node, contW); hit {
		return prefH
	}

	was := eng.noEmit
	eng.noEmit = true
	stickyBefore, groupsBefore := eng.stickySeq, eng.nextGroupID
	measured := eng.build(node, contW, curX, buildY)
	stickyDelta := eng.stickySeq - stickyBefore
	groupsDelta := eng.nextGroupID - groupsBefore
	eng.noEmit = was

	prefH := 0.0
	if measured != nil {
		prefH = measured.height
	}

	if eng.err == nil {
		eng.gridPrefHStore(node, contW, prefH, stickyDelta, groupsDelta)
	}

	return prefH
}
