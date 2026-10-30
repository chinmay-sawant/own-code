package layout

import (
	"errors"
	"fmt"
)

// Static errors for projection finalization. Dynamic values are wrapped with
// %w at the return site so messages keep their detail.
var (
	errPerspectiveImageVector  = errors.New("layout: perspective image cannot be emitted as vector geometry")
	errFinalizePerspectiveKind = errors.New("layout: cannot finalize perspective operation kind")
)

// DisplayPoint is one point in canvas coordinates.
type DisplayPoint struct {
	X, Y float64
}

// DisplayPath is a straight-edged path emitted when a display-list shape no
// longer fits the axis-aligned rectangle fields.
type DisplayPath struct {
	Points []DisplayPoint
	Closed bool
}

// GlyphBasis describes the local direction and scale of a projected text run.
// Its values use the display list's y-down canvas coordinates.
type GlyphBasis struct {
	A, B, C, D float64
}

// FinalizeDisplayOps bakes supported 3D projection into display geometry.
// Consumers receive paths and final text positions instead of CSS projection
// matrices for those operations.
func FinalizeDisplayOps(ops []Op) error {
	for idx := range ops {
		operation := &ops[idx]

		projective, ok := operation.ProjectiveTransformValue()
		if !ok {
			continue
		}

		operation.savePristineGeometry()

		err := finalizeProjectedOp(operation, projective)
		if err != nil {
			return err
		}

		// The projective matrix already includes the operation's affine
		// transforms. Do not let a downstream painter apply them a second time.
		operation.XformSet = false
		operation.clearProjectiveTransform()
	}

	return nil
}

// finalizeProjectedOp rewrites one projected operation into final display
// geometry. It reports operation kinds that cannot be finalized.
func finalizeProjectedOp(operation *Op, projective ProjectiveTransform) error {
	switch operation.Kind {
	case OpFillRect, OpStrokeRect:
		points := projectedRectPath(*operation, projective)
		operation.setDisplayPath(points, true)
		setProjectedBounds(operation, points)
	case OpPath:
		finalizeProjectedCornerPath(operation, projective)
	case OpLine:
		finalizeProjectedLine(operation, projective)
	case OpText, OpBullet:
		finalizeProjectedText(operation, projective)
	case OpLinkURI:
		finalizeProjectedLink(operation, projective)
	case OpGridRun:
		finalizeProjectedGrid(operation, projective)
	case OpUnknown, OpKindNoop:
		// Group boundary and deactivated operations carry no geometry.
	case OpImage:
		return errPerspectiveImageVector
	default:
		return fmt.Errorf("%w %d", errFinalizePerspectiveKind, operation.Kind)
	}

	return nil
}

func projectedRectPath(op Op, projective ProjectiveTransform) []DisplayPoint {
	points := []DisplayPoint{
		{X: op.X, Y: op.Y},
		{X: op.X + op.W, Y: op.Y},
		{X: op.X + op.W, Y: op.Y + op.H},
		{X: op.X, Y: op.Y + op.H},
	}
	for idx := range points {
		points[idx].X, points[idx].Y = projective.Apply(points[idx].X, points[idx].Y)
	}

	return points
}

// pathCmdPointCount returns how many points one path command consumes.
func pathCmdPointCount(verb PathVerb) int {
	switch verb {
	case PathMoveTo, PathLineTo:
		return 1
	case PathQuadTo:
		return pathQuadPointCount
	case PathCubicTo:
		return pathCubicPointCount
	case PathClose:
		return 0
	default:
		return 0
	}
}

// Point counts of the multi-point path verbs.
const (
	pathQuadPointCount  = 2
	pathCubicPointCount = 3
)

// finalizeProjectedCornerPath projects every corner-path command point and
// re-bounds the op. The verb and stroke flag survive the rewrite.
func finalizeProjectedCornerPath(operation *Op, projective ProjectiveTransform) {
	path, ok := operation.CornerPathValue()
	if !ok || path == nil {
		return
	}

	commands := make([]PathCmd, len(path.Cmds))

	var bounds []DisplayPoint

	for idx := range path.Cmds {
		command := path.Cmds[idx]
		count := pathCmdPointCount(command.Verb)

		for pointIdx := range count {
			projectedX, projectedY := projective.Apply(command.Pts[pointIdx].X, command.Pts[pointIdx].Y)
			command.Pts[pointIdx].X, command.Pts[pointIdx].Y = projectedX, projectedY
			bounds = append(bounds, command.Pts[pointIdx])
		}

		commands[idx] = command
	}

	operation.setCornerPath(commands, path.Stroke)
	setProjectedBounds(operation, bounds)
}

func setProjectedBounds(operation *Op, points []DisplayPoint) {
	if len(points) == 0 {
		return
	}

	left, right := points[0].X, points[0].X
	top, bottom := points[0].Y, points[0].Y

	for _, point := range points[1:] {
		left = min(left, point.X)
		right = max(right, point.X)
		top = min(top, point.Y)
		bottom = max(bottom, point.Y)
	}

	operation.X, operation.Y = left, top
	operation.W, operation.H = right-left, bottom-top
}

func finalizeProjectedLine(operation *Op, projective ProjectiveTransform) {
	lineX, lineY, lineW, lineH, width := operation.PaintLineGeometry()
	startX, startY := projective.Apply(lineX, lineY)
	endX, endY := projective.Apply(lineX+lineW, lineY+lineH)
	operation.X, operation.Y = startX, startY
	operation.W, operation.H = endX-startX, endY-startY
	operation.Width = width
	operation.LineInset = 0
}

func finalizeProjectedText(operation *Op, projective ProjectiveTransform) {
	oldX, oldY := operation.X, operation.Y
	operation.X, operation.Y = projective.Apply(oldX, oldY)
	operation.setGlyphBasis(projective.Jacobian(oldX, oldY))
}

func finalizeProjectedLink(operation *Op, projective ProjectiveTransform) {
	points := projectedRectPath(*operation, projective)
	setProjectedBounds(operation, points)
}

// projectedGridPointsPerSegment is the number of display points each grid
// segment projects to: one start and one end point.
const projectedGridPointsPerSegment = 2

func finalizeProjectedGrid(operation *Op, projective ProjectiveTransform) {
	if operation.Grid == nil {
		return
	}

	bounds := make([]DisplayPoint, 0, projectedGridPointsPerSegment*len(operation.Grid.Segs))

	for idx := range operation.Grid.Segs {
		line := operation.Grid.asLine(operation, idx)
		lineX, lineY, lineW, lineH, width := line.PaintLineGeometry()
		startX, startY := projective.Apply(lineX, lineY)
		endX, endY := projective.Apply(lineX+lineW, lineY+lineH)
		segment := &operation.Grid.Segs[idx]
		segment.X, segment.Y = startX, startY
		segment.W, segment.H = endX-startX, endY-startY
		segment.Width = width
		segment.LineInset = 0

		bounds = append(bounds, DisplayPoint{X: startX, Y: startY}, DisplayPoint{X: endX, Y: endY})
	}

	setProjectedBounds(operation, bounds)
}
