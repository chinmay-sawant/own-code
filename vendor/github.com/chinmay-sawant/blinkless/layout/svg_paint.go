package layout

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"

	"github.com/chinmay-sawant/blinkless/css"
	ihtml "github.com/chinmay-sawant/blinkless/internal/html"
	ilayout "github.com/chinmay-sawant/blinkless/internal/layout"
	"github.com/chinmay-sawant/blinkless/internal/pubstate"
)

var (
	// ErrNotInlineSVG means RestyleInlineSVG was given the id of an element
	// that is not an inline <svg>.
	ErrNotInlineSVG = errors.New("layout: element is not an inline svg")

	// ErrNoSVGPart means a paint override names an SVG child id that the
	// inline SVG does not contain.
	ErrNoSVGPart = errors.New("layout: no svg part with that id")

	// ErrSVGNotLaidOut means the inline <svg> emitted no retained layout box,
	// so there is no raster to restyle. A hidden element, or one whose raster
	// could not be built, has no box.
	ErrSVGNotLaidOut = errors.New("layout: inline svg has no retained layout box")
)

// SVGPaintOverride restyles the paint of one inline <svg> element or one part
// of it. The Has* flags select the properties the override changes; a field
// whose flag is false keeps the used value the cascade produced.
//
// Fill and Stroke take a CSS color or none. StrokeDashArray takes the CSS
// stroke-dasharray syntax, so the engine parses units and comma separators.
// StrokeWidth and StrokeDashOffset are used values in layout points. Opacities
// are in the 0..1 range; opacity clamps and a negative width clamps to zero. A
// paint server reference such as url(#gradient) is not a color and is rejected,
// because ResolvedStyle stores paint as RGB.
type SVGPaintOverride struct {
	// ElementID is the id of the inline <svg> element to restyle.
	ElementID string

	// PartID is the id of one element inside the SVG subtree. Empty means
	// the <svg> root. A part override changes that element and the
	// descendants that inherit its paint.
	PartID string

	Fill             string
	FillOpacity      float64
	Stroke           string
	StrokeOpacity    float64
	StrokeWidth      float64
	StrokeDashArray  string
	StrokeDashOffset float64

	HasFill, HasFillOpacity     bool
	HasStroke, HasStrokeOpacity bool
	HasStrokeWidth              bool
	HasStrokeDashArray          bool
	HasStrokeDashOffset         bool
}

// RestyleInlineSVG applies paint overrides to inline SVG elements and replaces
// their rasterized images in prev. It does not re-cascade CSS and does not
// re-place the elements: a paint change does not change the SVG's intrinsic
// size, so the painted rectangles stay where they were.
//
// The overrides are grouped by ElementID. A root override (PartID empty)
// enters the <svg> element's used style before serialization, so a shape that
// inherits the root paint follows it while a shape with its own paint keeps
// it. A part override enters the named child's used style and wins over the
// root override for the properties it sets. Colors and dash lists are parsed
// by the engine, not by the caller.
//
// doc is the styled document that holds the elements and prev is the display
// list of that document before the change. prev must carry the retained
// layout state, so lay the page out with Options.RetainState set; otherwise
// RestyleInlineSVG returns ErrNoRetainedState.
//
// The element is found by its id in the retained box tree. An inline-level
// <svg> keeps a retained box too: the line that paints its raster attributes
// the image operation to the SVG box, so an icon inside a paragraph restyles
// the same way a block-level SVG does.
//
// No overrides, or overrides that set no property, return an empty patch whose
// Display is prev and leave the raster untouched. A non-empty patch carries
// the restyled elements' operations and boxes, and the union of their old and
// new image bounds in CSS pixels. A host adopts patch.Display and treats prev
// as stale, the same rule as RelayoutElement.
//
// Serializing and rasterizing an SVG is the cost of this call. It does the work
// for every element named in overrides, so a host should pass only the
// elements whose paint changed. The raster of each serialized document is
// cached on the retained layout, so repeating the same paint for the same
// element reuses the previous raster instead of rasterizing again.
//
//nolint:cyclop,funlen // validates, groups, and batches one public request list
func RestyleInlineSVG(
	ctx context.Context,
	doc *css.Document,
	prev *Display,
	overrides []SVGPaintOverride,
	options Options,
) (*ElementPatch, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("layout: context: %w", err)
	}

	if doc == nil {
		return nil, ErrNilDocument
	}

	if prev == nil {
		return nil, ErrNilDisplay
	}

	if prev.result == nil {
		return nil, ErrNoRetainedState
	}

	if len(overrides) == 0 {
		return &ElementPatch{Display: prev}, nil //nolint:exhaustruct // an empty patch carries only the display
	}

	styled, ok := pubstate.StyledOf(doc)
	if !ok {
		return nil, ErrNilDocument
	}

	requests, err := svgPaintRequests(styled.Root, overrides)
	if err != nil {
		return nil, err
	}

	for index := range requests {
		if !displayHasBox(prev, requests[index].Node.Attribute("id")) {
			return nil, fmt.Errorf(
				"%w: %q",
				ErrSVGNotLaidOut,
				requests[index].Node.Attribute("id"),
			)
		}
	}

	widthPt, heightPt := displayViewportPt(styled)
	layoutOptions := ilayout.Options{ //nolint:exhaustruct // only the paint pass inputs
		Width:      widthPt,
		Height:     heightPt,
		Images:     options.Images,
		Background: true,
		Zoom:       prev.zoom,
	}

	edits, err := ilayout.RestyleSVGPaint(ctx, prev.result, requests, layoutOptions)
	if err != nil {
		return nil, fmt.Errorf("layout: restyle inline svg: %w", err)
	}

	if len(edits) == 0 {
		return &ElementPatch{Display: prev}, nil //nolint:exhaustruct // an empty patch carries only the display
	}

	patch := &ElementPatch{} //nolint:exhaustruct // filled from edits
	haveBounds := false

	for index := range edits {
		edit := &edits[index]
		boxOffset := len(patch.Boxes)

		for _, owner := range ilayout.OperationOwners(edit.Element) {
			if owner >= 0 {
				owner += boxOffset
			}

			patch.OpOwners = append(patch.OpOwners, owner)
		}

		patch.Ops = append(patch.Ops, edit.Element.Ops...)
		patch.Boxes = append(patch.Boxes, boxesFrom(ilayout.PlacedElements(edit.Element))...)

		oldBounds := svgImageBounds(edit.OldImage)
		newBounds := svgImageBounds(edit.NewImage)

		if !haveBounds {
			patch.Old = oldBounds
			patch.New = newBounds
			haveBounds = true

			continue
		}

		patch.Old = patch.Old.Union(oldBounds)
		patch.New = patch.New.Union(newBounds)
	}

	patch.Display = patchedDisplay(styled, prev)

	return patch, nil
}

// svgPaintRequests resolves each override's element id in the document and
// groups the overrides by element, keeping the first-seen element order. Every
// part id is checked here so a missing part is ErrNoSVGPart before the engine
// does any work.
func svgPaintRequests(
	root *ihtml.Node, overrides []SVGPaintOverride,
) ([]ilayout.SVGPaintRequest, error) {
	order := make([]string, 0, len(overrides))
	nodes := make(map[string]*ihtml.Node, len(overrides))
	grouped := make(map[string][]ilayout.SVGPaintOverride, len(overrides))

	for _, override := range overrides {
		elementID := override.ElementID
		if elementID == "" {
			return nil, fmt.Errorf("%w: empty element id", ErrNoElement)
		}

		node, ok := nodes[elementID]
		if !ok {
			node = elementByID(root, elementID)
			if node == nil {
				return nil, fmt.Errorf("%w: %q", ErrNoElement, elementID)
			}

			if node.Name != "svg" {
				return nil, fmt.Errorf("%w: %q is <%s>", ErrNotInlineSVG, elementID, node.Name)
			}

			nodes[elementID] = node

			order = append(order, elementID)
		}

		if override.PartID != "" && !svgPartExists(node, override.PartID) {
			return nil, fmt.Errorf("%w: %q in svg %q", ErrNoSVGPart, override.PartID, elementID)
		}

		grouped[elementID] = append(grouped[elementID], internalSVGPaintOverride(override))
	}

	requests := make([]ilayout.SVGPaintRequest, 0, len(order))

	for _, elementID := range order {
		requests = append(requests, ilayout.SVGPaintRequest{
			Node:      nodes[elementID],
			Overrides: grouped[elementID],
		})
	}

	return requests, nil
}

// internalSVGPaintOverride converts the public override into the engine's
// record. The two carry the same fields; the public one is the documented
// surface and the engine one is what the serializer reads.
func internalSVGPaintOverride(override SVGPaintOverride) ilayout.SVGPaintOverride {
	return ilayout.SVGPaintOverride{
		ElementID:           override.ElementID,
		PartID:              override.PartID,
		Fill:                override.Fill,
		FillOpacity:         override.FillOpacity,
		Stroke:              override.Stroke,
		StrokeOpacity:       override.StrokeOpacity,
		StrokeWidth:         override.StrokeWidth,
		StrokeDashArray:     override.StrokeDashArray,
		StrokeDashOffset:    override.StrokeDashOffset,
		HasFill:             override.HasFill,
		HasFillOpacity:      override.HasFillOpacity,
		HasStroke:           override.HasStroke,
		HasStrokeOpacity:    override.HasStrokeOpacity,
		HasStrokeWidth:      override.HasStrokeWidth,
		HasStrokeDashArray:  override.HasStrokeDashArray,
		HasStrokeDashOffset: override.HasStrokeDashOffset,
	}
}

// displayHasBox reports whether the retained page placed the element box with
// the given id. A block-level SVG is one of the tree's own boxes; an
// inline-level SVG is attributed to the box its line kept, so both pass.
func displayHasBox(display *Display, elementID string) bool {
	for index := range display.Boxes {
		if display.Boxes[index].ID == elementID {
			return true
		}
	}

	return false
}

// svgPartExists reports whether the SVG subtree contains an element with the
// given id. The root counts as a part.
func svgPartExists(root *ihtml.Node, partID string) bool {
	if root == nil || partID == "" {
		return false
	}

	found := false

	root.Walk(func(node *ihtml.Node) {
		if found || node.Type != ihtml.ElementNode {
			return
		}

		if node.Attribute("id") == partID {
			found = true
		}
	})

	return found
}

// svgImageBounds converts one image operation's painted rectangle from layout
// points to CSS pixels.
func svgImageBounds(imageOp ilayout.Op) image.Rectangle {
	left := int(math.Round(imageOp.X * ptToPx))
	top := int(math.Round(imageOp.Y * ptToPx))

	return image.Rect(
		left,
		top,
		left+int(math.Round(imageOp.W*ptToPx)),
		top+int(math.Round(imageOp.H*ptToPx)),
	)
}
