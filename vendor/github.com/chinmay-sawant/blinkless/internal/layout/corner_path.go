package layout

import (
	"slices"
)

// Corner-shape path contract (phase 0 of plans/v0.0.3/corner-shape).
//
// OpPath carries one outer path for a non-round corner box: one op for the
// background fill and one for the border stroke. The used border radius stays
// the corner box; the path commands replace the quarter-ellipse inside it.
// The border image painter, box-shadow, and the overflow clip keep today's
// radii and boxes in this wave.

// OpPath is one corner-shape path. It is 9 with an explicit value in this
// file, not a new arm of the iota block in layout.go. OpGridRun is the last
// iota value (8) and opKindNoop is 255 (layout_measure.go), so 9 is the next
// free value. A future iota insert must not reuse 9.
const OpPath OpKind = 9

// opPathIotaGuard fails the build if the iota block in layout.go grows past
// OpGridRun (8) and collides with the explicit OpPath value (9).
var _ [8 - int(OpGridRun)]struct{}

// PathVerb names one corner-path command. Zero is unset; commands start at 1.
type PathVerb uint8

const (
	// PathMoveTo starts a new contour at Pts[0].
	PathMoveTo PathVerb = iota + 1
	// PathLineTo draws a straight edge to Pts[0].
	PathLineTo
	// PathQuadTo draws a quadratic curve through Pts[0] (control) to Pts[1] (end).
	PathQuadTo
	// PathCubicTo draws a cubic curve through Pts[0] and Pts[1] (controls) to Pts[2] (end).
	PathCubicTo
	// PathClose closes the current contour. Pts is unused.
	PathClose
)

// PathCmd is one corner-path command in canvas points (y down).
// Pts holds 1 point for move and line (end), 2 for quad (control, end),
// 3 for cubic (control 1, control 2, end), and none for close.
type PathCmd struct {
	Verb PathVerb
	Pts  [3]DisplayPoint
}

// CornerPath is the command list for one OpPath operation. Cmds runs in paint
// order and ends with PathClose for a closed outline. Stroke is false for a
// background fill (painted with the op R/G/B/Alpha) and true for a border
// stroke (painted with the op Width and side color).
type CornerPath struct {
	Cmds   []PathCmd
	Stroke bool
}

// CornerShapeRoundK is the superellipse exponent for `round`, the initial
// corner shape. Keyword map from CSS Borders 4: round 1, squircle 2,
// square +Inf, bevel 0, scoop -1, notch -Inf.
const CornerShapeRoundK float64 = 1

// CornerShapeIsRound reports whether k is the round exponent. Paint must not
// emit OpPath when every corner of a box is round: a round box keeps today's
// OpFillRect radii and OpStrokeRect arcs unchanged.
func CornerShapeIsRound(k float64) bool { return k == CornerShapeRoundK }

// setCornerPath stores cmds on the op extra payload, copying them so later
// edits of the caller slice cannot move painted geometry. An empty list
// clears the payload.
func (op *Op) setCornerPath(cmds []PathCmd, stroke bool) {
	extra := op.detachExtra()
	if len(cmds) == 0 {
		extra.CornerPath = nil

		return
	}

	extra.CornerPath = &CornerPath{
		Cmds:   slices.Clone(cmds),
		Stroke: stroke,
	}
}

// CornerPathValue returns the path commands for an OpPath op, if any. The
// returned list belongs to the display list and must not be modified.
func (op Op) CornerPathValue() (*CornerPath, bool) {
	if op.opExtra == nil || op.opExtra.CornerPath == nil {
		return nil, false
	}

	return op.opExtra.CornerPath, true
}

// cloneCornerPath deep-copies a corner path so CloneResult and Op.Clone never
// alias the source command list.
func cloneCornerPath(src *CornerPath) *CornerPath {
	if src == nil {
		return nil
	}

	return &CornerPath{
		Cmds:   slices.Clone(src.Cmds),
		Stroke: src.Stroke,
	}
}
