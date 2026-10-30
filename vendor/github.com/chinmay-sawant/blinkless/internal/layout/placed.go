package layout

import "github.com/chinmay-sawant/blinkless/internal/html"

// PlacedElement is one element border box in canvas points. Y grows downward
// from the top of the layout canvas. Layout fills these boxes before pagination.
type PlacedElement struct {
	ID                  string
	Tag                 string
	Action              string
	Text                string
	ParentIndex         int
	OperationIDs        []uint64
	Animations          []AnimationRecord
	Transitions         []TransitionRecord
	TriggerScope        string
	ViewTransitionName  string
	ViewTransitionClass string
	ViewTransitionGroup string
	ViewTransitionScope string
	// Timelines holds the named scroll and view timelines this element
	// declares. Scroll comes before view; a nil slice means no declaration.
	Timelines []TimelineDef
	// OverflowClip is the accumulated ancestor overflow padding box in
	// canvas points, empty with HasOverflowClip false when no ancestor
	// clips. A host painting this element through a transform intersects
	// the paint with it; geometry-baked clipping alone moves with the
	// matrix and cannot hold transformed paint inside the box.
	OverflowClip    [4]float64
	HasOverflowClip bool
	// Transform carries the resolved transform metadata for this element. It is
	// the zero value when the element declares no transform.
	Transform TransformInfo
	X         float64
	Y         float64
	W         float64
	H         float64
}

// PlacedElements walks the laid-out element boxes in document order.
// A nil result returns nil. Paint and pagination are not required.
func PlacedElements(res *Result) []PlacedElement {
	if res == nil || res.root == nil {
		return nil
	}

	out := make([]PlacedElement, 0, len(res.boxes))
	walkPlaced(res.root, res.Ops, -1, IdentityMatrix(), svgViewport{}, &out) //nolint:exhaustruct // identity root viewport

	return out
}

func walkPlaced(
	boxNode *box, ops []Op, parentIndex int, parentAccum Matrix2D, viewport svgViewport,
	out *[]PlacedElement,
) {
	if boxNode == nil {
		return
	}

	childParent := parentIndex
	childAccum := parentAccum
	info := transformInfoForBox(boxNode, parentAccum, viewport)

	if info.HasMatrix {
		childAccum = info.World
	}

	if boxNode.node != nil && boxNode.node.Type == html.ElementNode {
		index := len(*out)
		elementID := boxNode.node.Attribute("id")
		*out = append(*out, PlacedElement{
			ID:                  elementID,
			Tag:                 boxNode.node.Name,
			Action:              boxNode.node.Attribute("data-action"),
			Text:                boxNode.node.TextContent(),
			ParentIndex:         parentIndex,
			OperationIDs:        operationIDs(ops, boxNode.opStart, boxNode.opEnd),
			Animations:          animationRecordsFromStyle(boxNode.style.Animation),
			Transitions:         transitionRecordsFromStyle(boxNode.style.Transition),
			TriggerScope:        boxNode.style.Animation.TriggerScope,
			ViewTransitionName:  boxNode.style.Animation.ViewTransitionName,
			ViewTransitionClass: boxNode.style.Animation.ViewTransitionClass,
			ViewTransitionGroup: boxNode.style.Animation.ViewTransitionGroup,
			ViewTransitionScope: boxNode.style.Animation.ViewTransitionScope,
			Timelines:           timelineDefsFromStyle(boxNode.style, elementID),
			OverflowClip:        elementOverflowClip(boxNode),
			HasOverflowClip:     boxNode.overflowClip != nil,
			Transform:           info,
			X:                   boxNode.x,
			Y:                   boxNode.y,
			W:                   boxNode.w,
			H:                   boxNode.height,
		})
		childParent = index
	}

	// An <svg> box establishes a viewport for its subtree; the box's own
	// transform resolves against the ancestor viewport above, matching the
	// stamp walk.
	childViewport := childViewportForBox(boxNode, viewport)

	for _, child := range boxNode.children {
		walkPlaced(child, ops, childParent, childAccum, childViewport, out)
	}
}

// elementOverflowClip reads the ancestor overflow box clipOverflowTree
// recorded, in canvas points. Zero with no clip.
func elementOverflowClip(boxNode *box) [4]float64 {
	if boxNode == nil || boxNode.overflowClip == nil {
		return [4]float64{}
	}

	c := boxNode.overflowClip

	return [4]float64{c.x, c.y, c.w, c.h}
}

func operationIDs(ops []Op, start, end int) []uint64 {
	if start < 0 || end < start {
		return make([]uint64, 0)
	}

	ids := make([]uint64, 0, end-start+1)

	for index := start; index <= end && index < len(ops); index++ {
		if ops[index].ID != 0 {
			ids = append(ids, ops[index].ID)
		}
	}

	return ids
}
