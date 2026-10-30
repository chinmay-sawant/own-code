package layout

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"slices"

	"github.com/chinmay-sawant/blinkless/css"
	ilayout "github.com/chinmay-sawant/blinkless/internal/layout"
	"github.com/chinmay-sawant/blinkless/internal/pubstate"
)

// ErrInvalidTransformOverride means a restamp override carried a transform
// list the engine's transform parser rejects. The page is left untouched.
var ErrInvalidTransformOverride = errors.New("layout: invalid transform override")

// ErrNotRestampable means the element a restamp override names cannot be
// re-projected on its own: its box is not reachable through the children chain
// the stamp walk follows (a table cell lives in its row list, for example).
var ErrNotRestampable = errors.New("layout: element subtree cannot be restamped")

// TransformOverride replaces one element's resolved transform values for one
// re-projection pass.
//
// A 3D transform and a perspective have no 2D affine equivalent: the engine
// computes them across a whole subtree at stamp time. A host that animates
// either one serializes the interpolated transform list to CSS and passes it
// here, and receives a new display list with the subtree re-stamped.
//
// Every field is optional. An override with HasTransform=false,
// HasOrigin=false, HasPerspective=false, and HasPerspectiveOrigin=false
// changes nothing.
//
// The origins and the perspective distance are in layout points, the unit of
// Matrix2D and Display.PointsPerPixel, not CSS pixels. OriginX, OriginY,
// PerspectiveOriginX, and PerspectiveOriginY are page coordinates: the point
// the engine resolves the origin to, which is the same value
// TransformInfo.OriginX reports for the element.
type TransformOverride struct {
	// ElementID names the element by its id attribute.
	ElementID string

	// Transform replaces the element's own transform for this pass. It is a
	// CSS transform list, so 2D and 3D functions parse on the same path the
	// cascade uses: rotateX, rotateY, rotateZ, rotate3d, translate3d,
	// translateZ, scale3d, scaleZ, matrix3d, and a perspective() function in
	// the list are all accepted.
	//
	// HasTransform=false leaves the transform unchanged. HasTransform=true
	// with an empty string means none.
	Transform    string
	HasTransform bool

	// OriginX and OriginY replace transform-origin, in layout points.
	// HasOrigin=false leaves the resolved transform-origin unchanged.
	OriginX   float64
	OriginY   float64
	HasOrigin bool

	// Perspective replaces the perspective property this element gives its
	// descendants, in layout points. A distance of zero means none.
	Perspective    float64
	HasPerspective bool

	// PerspectiveOriginX and PerspectiveOriginY replace perspective-origin,
	// in layout points. They are page coordinates, the vanishing point the
	// engine resolves, not an offset inside the box.
	PerspectiveOriginX   float64
	PerspectiveOriginY   float64
	HasPerspectiveOrigin bool
}

// RestampTransforms re-stamps the transform state of the elements named by
// overrides inside an already laid out page, and returns a patch the host
// adopts through the same path as RelayoutElement.
//
// The pass re-runs the perspective propagation and the transform stamp walk for
// each overridden subtree. It does not re-cascade and does not re-place: the
// boxes keep their geometry and only the projection stamped on their operations
// changes. It is the path for a 3D transform or a perspective animation, which
// a 2D layer matrix cannot represent; a 2D-only transform keeps the cheaper
// matrix path on the host.
//
// The overrides apply to this pass only; the cached resolved styles are left
// untouched, so the next static frame is the one the cascade produced. An
// override list that changes nothing returns a patch with an empty damage
// rectangle and leaves every operation as it was. Nested overrides apply
// outermost first: an inner target is stamped inside the outer target's
// subtree, so the inner value wins for its own subtree and still sees the outer
// camera.
//
// prev must carry retained state: lay the page out with
// DisplayListOptions and Options.RetainState set. Without it the pass returns
// ErrNoRetainedState.
//
// The returned patch carries the whole patched page in Display, the re-stamped
// operations in Ops, and Old/New, the painted area of the patched subtrees in
// CSS pixels before and after the pass. A host adopts Display and treats prev
// as stale. The engine does not lock the retained state, so one display must
// not be patched from two goroutines at once.
func RestampTransforms(
	ctx context.Context,
	doc *css.Document,
	prev *Display,
	overrides []TransformOverride,
	_ Options,
) (*ElementPatch, error) {
	if err := checkRestampInput(ctx, doc, prev, overrides); err != nil {
		return nil, err
	}

	styled, ok := pubstate.StyledOf(doc)
	if !ok {
		return nil, ErrNilDocument
	}

	if prev.result == nil {
		return nil, ErrNoRetainedState
	}

	restamped, err := ilayout.RestampTransforms(ctx, prev.result, engineOverrides(overrides), engineOptions(styled.Media))
	if err != nil {
		return nil, restampError(err)
	}

	if !restamped.Changed || len(restamped.Windows) == 0 {
		// Nothing moved. Hand the page back unchanged so a host that adopts
		// the patch keeps the display it already draws.
		return &ElementPatch{Display: prev}, nil //nolint:exhaustruct // no damage, no work
	}

	return restampElementPatch(prev, overrides, restamped), nil
}

// engineOverrides converts the public override list for the engine.
func engineOverrides(overrides []TransformOverride) []ilayout.RestampOverride {
	converted := make([]ilayout.RestampOverride, len(overrides))

	for idx := range overrides {
		override := &overrides[idx]
		converted[idx] = ilayout.RestampOverride{
			ElementID:            override.ElementID,
			Transform:            override.Transform,
			HasTransform:         override.HasTransform,
			OriginX:              override.OriginX,
			OriginY:              override.OriginY,
			HasOrigin:            override.HasOrigin,
			Perspective:          override.Perspective,
			HasPerspective:       override.HasPerspective,
			PerspectiveOriginX:   override.PerspectiveOriginX,
			PerspectiveOriginY:   override.PerspectiveOriginY,
			HasPerspectiveOrigin: override.HasPerspectiveOrigin,
		}
	}

	return converted
}

// restampElementPatch builds the patch the host adopts: the patched display,
// the re-stamped operations, the patched element boxes, and the damage the
// old and new painted areas cover.
func restampElementPatch(
	prev *Display, overrides []TransformOverride, restamped *ilayout.RestampPatch,
) *ElementPatch {
	display := restampedDisplay(prev)

	ops := make([]DisplayOp, 0)
	owners := make([]int, 0)

	for _, window := range restamped.Windows {
		end := min(window.End, len(display.Ops)-1)
		if window.Start < 0 || window.Start > end {
			continue
		}

		ops = append(ops, display.Ops[window.Start:end+1]...)
		owners = append(owners, display.OpOwners[window.Start:end+1]...)
	}

	return &ElementPatch{
		Ops:      ops,
		OpOwners: owners,
		Boxes:    restampedBoxes(prev.Boxes, overrides),
		Old:      pointBoundsToRect(restamped.OldX, restamped.OldY, restamped.OldW, restamped.OldH),
		New:      pointBoundsToRect(restamped.NewX, restamped.NewY, restamped.NewW, restamped.NewH),
		Display:  display,
	}
}

// restampedDisplay copies prev without rebuilding the metadata a re-stamp
// cannot change. The pass mutates the operation slice in place; the operation
// owners, paint order, keyframes, boxes, and dimensions stay as they were.
// Rebuilding them with newDisplay on every animated frame dominated the 3D
// profile, so the patch shares those slices with prev and treats prev as stale.
func restampedDisplay(prev *Display) *Display {
	next := *prev
	next.Ops = prev.result.Ops
	next.result = prev.result

	return &next
}

// checkRestampInput rejects the inputs a restamp cannot work with. It mirrors
// the checks RelayoutElement makes so the two patch paths answer alike.
func checkRestampInput(
	ctx context.Context, doc *css.Document, prev *Display, overrides []TransformOverride,
) error {
	if ctx == nil {
		return ErrNilContext
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("layout: context: %w", err)
	}

	if doc == nil {
		return ErrNilDocument
	}

	if prev == nil {
		return ErrNilDisplay
	}

	for idx := range overrides {
		if overrides[idx].ElementID == "" {
			return fmt.Errorf("%w: empty element id", ErrNoElement)
		}
	}

	return nil
}

// restampError maps the engine's restamp failures onto the public sentinels.
func restampError(err error) error {
	switch {
	case errors.Is(err, ilayout.ErrNoElement):
		return fmt.Errorf("%w: %w", ErrNoElement, err)
	case errors.Is(err, ilayout.ErrNoRetainedState):
		return fmt.Errorf("%w: %w", ErrNoRetainedState, err)
	case errors.Is(err, ilayout.ErrInvalidTransform):
		return fmt.Errorf("%w: %w", ErrInvalidTransformOverride, err)
	case errors.Is(err, ilayout.ErrNotStampable):
		return fmt.Errorf("%w: %w", ErrNotRestampable, err)
	default:
		return err
	}
}

// engineOptions carries the display's own layout inputs into the restamp pass.
// The pass re-places nothing and reads no image bytes, so only the media the
// stamp walk keys on travels.
func engineOptions(media string) ilayout.Options {
	return ilayout.Options{Media: media} //nolint:exhaustruct // restamp reads media only
}

// restampedBoxes returns the element boxes of the patched subtrees in document
// order, the way ElementPatch.Boxes reports them for RelayoutElement.
func restampedBoxes(boxes []Box, overrides []TransformOverride) []Box {
	ranges := make([]indexRange, 0, len(overrides))

	for idx := range overrides {
		boxIndex := boxIndexByID(boxes, overrides[idx].ElementID)
		if boxIndex < 0 {
			continue
		}

		end := boxSubtreeEnd(boxes, boxIndex)
		ranges = append(ranges, indexRange{start: boxIndex, end: end})
	}

	if len(ranges) == 0 {
		return nil
	}

	slices.SortFunc(ranges, func(left, right indexRange) int { return left.start - right.start })

	out := make([]Box, 0)
	next := -1

	for _, item := range ranges {
		start := max(item.start, next)
		if start > item.end {
			continue
		}

		out = append(out, boxes[start:item.end+1]...)
		next = item.end + 1
	}

	return out
}

// indexRange is an inclusive half-open-free box range: both ends are indexes.
type indexRange struct {
	start int
	end   int
}

// boxIndexByID returns the index of the first box with the given id, or -1.
func boxIndexByID(boxes []Box, elementID string) int {
	if elementID == "" {
		return -1
	}

	for idx := range boxes {
		if boxes[idx].ID == elementID {
			return idx
		}
	}

	return -1
}

// boxSubtreeEnd returns the last index of the box's subtree in document order.
// Display.Boxes is in document order and ParentIndex points at the enclosing
// element, so a subtree is a contiguous run.
func boxSubtreeEnd(boxes []Box, index int) int {
	end := index

	for idx := index + 1; idx < len(boxes); idx++ {
		if !boxBelow(boxes, idx, index) {
			break
		}

		end = idx
	}

	return end
}

// boxBelow reports whether boxes[index] is the box at root or sits inside it.
func boxBelow(boxes []Box, index, root int) bool {
	for idx := index; idx >= 0 && idx < len(boxes); idx = boxes[idx].ParentIndex {
		if idx == root {
			return true
		}
	}

	return false
}

// pointBoundsToRect converts a layout-point rectangle to CSS pixels. The
// bounds are expanded outward so a damage region never clips a painted edge.
func pointBoundsToRect(left, top, width, height float64) image.Rectangle {
	if width <= 0 || height <= 0 {
		return image.Rectangle{} //nolint:exhaustruct // no painted area
	}

	x := int(math.Floor(left * ptToPx))
	y := int(math.Floor(top * ptToPx))
	right := int(math.Ceil((left + width) * ptToPx))
	bottom := int(math.Ceil((top + height) * ptToPx))

	return image.Rect(x, y, right, bottom)
}
