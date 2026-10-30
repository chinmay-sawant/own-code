package layout

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"

	"github.com/chinmay-sawant/blinkless/css"
	pdf "github.com/chinmay-sawant/blinkless/internal/fonts"
	ihtml "github.com/chinmay-sawant/blinkless/internal/html"
	ilayout "github.com/chinmay-sawant/blinkless/internal/layout"
	"github.com/chinmay-sawant/blinkless/internal/pubstate"
)

var (
	// ErrNilDisplay means RelayoutElement was given no previous display
	// list to patch.
	ErrNilDisplay = errors.New("layout: nil display")

	// ErrNoElement means RelayoutElement found no element with the given id.
	ErrNoElement = errors.New("layout: no element with that id")

	// ErrNoRetainedState means the display carries no retained layout
	// state, so it cannot be patched. The display was laid out without
	// Options.RetainState, so the engine kept no box tree and no resolved
	// styles to re-place the element inside. Lay the page out with
	// Options.RetainState set and patch that display.
	ErrNoRetainedState = errors.New("layout: display carries no retained layout state")

	// ErrUnknownOverrideProperty means an override named a property the
	// relayout path cannot apply to the element's used style. A custom
	// property, a paint property, a transform, an SVG paint value, and a
	// display or flow property are refused here. The page is left untouched.
	ErrUnknownOverrideProperty = errors.New("layout: unknown override property")

	// ErrInvalidOverrideValue means an override value is empty, is a
	// CSS-wide keyword, contains var(), or is not a value the property's
	// grammar accepts. The page is left untouched.
	ErrInvalidOverrideValue = errors.New("layout: invalid override value")

	// ErrOverrideReflow means the override changed the size of the box the
	// patch would re-place, so the change escapes the containing block the
	// patch covers. Fall back to a full layout for that frame. The page is
	// left untouched.
	ErrOverrideReflow = errors.New("layout: override reflow escapes its containing block")
)

// ElementPatch is the result of patching one element of a laid out page.
type ElementPatch struct {
	// Ops are the operations the patched element's subtree emits. They are
	// for inspection; the whole page the host draws is Display.
	Ops []DisplayOp

	// OpOwners maps each operation index in Ops to the nearest element box
	// in Boxes that owns it, the rule Display.OpOwners uses.
	OpOwners []int

	// Boxes are the element border boxes of the patched subtree, in document
	// order, in CSS pixels.
	Boxes []Box

	// Old is the element's border box before the patch, in CSS pixels.
	Old image.Rectangle

	// New is the element's border box after the patch, in CSS pixels.
	New image.Rectangle

	// Display is the patched page: the operations, the paint order, the
	// owners and the boxes of the whole page after the patch. A host adopts
	// this display and treats the one it passed as stale, because the
	// engine splices into the retained placement and a count change moves
	// every later operation index, the paint order, and the owners.
	Display *Display
}

// RelayoutElement lays out one element's subtree inside prev and returns the
// operations, boxes and old and new bounds for that element only.
//
// doc is the styled document that holds the element and prev is the display
// list of that document before the change. The element is found by id in
// doc's tree, so a host that edited the tree first (html.Document.SetText,
// html.Document.SetAttribute) gets a patch of the edited element.
//
// prev must carry the retained layout state. The host asks for it when it
// lays the page out: layout.DisplayListOptions with Options.RetainState set.
// A display laid out without that state cannot be patched, and
// RelayoutElement returns ErrNoRetainedState for it.
//
// On success the element keeps its place and its size. A change that would
// move or resize it is refused, so the boxes after it stay valid. The patch
// carries the element's own operations and boxes for inspection (Ops, Boxes,
// and OpOwners), its old and new bounds (Old and New), and Display, the whole
// patched page. A host adopts Display and treats prev as stale.
//
// The engine does not lock the retained state, so one display must not be
// patched from two goroutines at once.
func RelayoutElement(
	ctx context.Context, doc *css.Document, prev *Display, elementID string, options Options,
) (*ElementPatch, error) {
	if err := checkRelayoutInput(ctx, doc, prev, elementID); err != nil {
		return nil, err
	}

	styled, ok := pubstate.StyledOf(doc)
	if !ok {
		return nil, ErrNilDocument
	}

	node := elementByID(styled.Root, elementID)
	if node == nil {
		return nil, ErrNoElement
	}

	if prev.result == nil {
		return nil, ErrNoRetainedState
	}

	res, err := subtreeResult(ctx, styled, prev, node, options)
	if err != nil {
		return nil, err
	}

	boxes := boxesFrom(ilayout.PlacedElements(res))

	return &ElementPatch{
		Ops:      res.Ops,
		OpOwners: ilayout.OperationOwners(res),
		Boxes:    boxes,
		Old:      elementBounds(prev.Boxes, elementID),
		New:      elementBounds(boxes, elementID),
		Display:  patchedDisplay(styled, prev),
	}, nil
}

// RelayoutElementWithOverrides lays out one element with a set of property
// values applied on top of the cascade, and returns the patch for that element.
//
// It is the geometry-animation path: a host samples an animated width, a font
// size, or another layout property, hands the values here, and adopts the
// returned display. The values are applied to the element's used style after
// the cascade and before placement, are not written to the parsed tree, and
// are gone when the call returns. The next patch starts from the base page.
//
// An inherited property reaches the element's descendants: the engine resolves
// the element's subtree against the overridden style, so a font-size override
// sizes the child text runs the way a stylesheet declaration would.
//
// The patch target is the element's containing block, not the element alone,
// because a width or font-size change can move the element's siblings. Ops and
// Boxes describe the element's own subtree, the shape RelayoutElement returns;
// Display is the whole page with the reflowed siblings. Old and New are the
// element's border box before and after the call.
//
// prev must carry the retained layout state, the same rule RelayoutElement
// has. An empty override list is RelayoutElement exactly.
//
// An unknown property is ErrUnknownOverrideProperty, a malformed value is
// ErrInvalidOverrideValue, and a change that would resize the containing block
// is ErrOverrideReflow (fall back to a full layout for that frame). Every error
// leaves prev untouched.
//
// The engine does not lock the retained state, so one display must not be
// patched from two goroutines at once.
func RelayoutElementWithOverrides(
	ctx context.Context, doc *css.Document, prev *Display,
	elementID string, overrides []Override, options Options,
) (*ElementPatch, error) {
	if len(overrides) == 0 {
		return RelayoutElement(ctx, doc, prev, elementID, options)
	}

	if err := checkRelayoutInput(ctx, doc, prev, elementID); err != nil {
		return nil, err
	}

	styled, ok := pubstate.StyledOf(doc)
	if !ok {
		return nil, ErrNilDocument
	}

	node := elementByID(styled.Root, elementID)
	if node == nil {
		return nil, ErrNoElement
	}

	if prev.result == nil {
		return nil, ErrNoRetainedState
	}

	engineOverrides, err := engineStyleOverrides(elementID, overrides)
	if err != nil {
		return nil, err
	}

	layoutOpts, err := relayoutOptions(styled, prev, options)
	if err != nil {
		return nil, err
	}

	res, err := ilayout.RelayoutSubtreeWithOverrides(ctx, prev.result, node, engineOverrides, layoutOpts)
	if err != nil {
		return nil, overrideRelayoutError(err)
	}

	boxes := boxesFrom(ilayout.PlacedElements(res))

	return &ElementPatch{
		Ops:      res.Ops,
		OpOwners: ilayout.OperationOwners(res),
		Boxes:    boxes,
		Old:      elementBounds(prev.Boxes, elementID),
		New:      elementBounds(boxes, elementID),
		Display:  patchedDisplay(styled, prev),
	}, nil
}

// engineStyleOverrides converts the public override list for the engine. An
// override may name the relayout target or leave the id empty; any other id is
// a caller error, not a silently ignored entry.
func engineStyleOverrides(elementID string, overrides []Override) ([]ilayout.StyleOverride, error) {
	converted := make([]ilayout.StyleOverride, len(overrides))

	for index := range overrides {
		override := &overrides[index]

		if override.ElementID != "" && override.ElementID != elementID {
			return nil, fmt.Errorf(
				"%w: override names %q, the relayout target is %q",
				ErrNoElement, override.ElementID, elementID,
			)
		}

		converted[index] = ilayout.StyleOverride{
			Property: override.Property,
			Value:    override.Value,
		}
	}

	return converted, nil
}

// overrideRelayoutError maps the engine's override failures onto the public
// sentinels.
func overrideRelayoutError(err error) error {
	switch {
	case errors.Is(err, ilayout.ErrUnknownOverrideProperty):
		return fmt.Errorf("%w: %w", ErrUnknownOverrideProperty, err)
	case errors.Is(err, ilayout.ErrInvalidOverrideValue):
		return fmt.Errorf("%w: %w", ErrInvalidOverrideValue, err)
	case errors.Is(err, ilayout.ErrOverrideReflow):
		return fmt.Errorf("%w: %w", ErrOverrideReflow, err)
	default:
		return err
	}
}

// patchedDisplay builds the display list of the patched page. The engine
// patched the placement the previous display was built from, so the display
// carries that placement and the operations, the paint order, the owners and
// the boxes that go with them. Page breaks are recomputed because a style
// patch can change them.
func patchedDisplay(styled pubstate.Styled, prev *Display) *Display {
	canvasPt := float64(prev.Height) / ptToPx
	forced, keep := ilayout.PageBreaks(prev.result, canvasPt)

	return newDisplay(prev.result, styled.Sheets, canvasPt,
		forced, keep, prev.zoom, true)
}

// checkRelayoutInput rejects the inputs RelayoutElement cannot work with.
func checkRelayoutInput(ctx context.Context, doc *css.Document, prev *Display, elementID string) error {
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

	if elementID == "" {
		return ErrNoElement
	}

	return nil
}

// subtreeResult asks the engine to place the element's subtree inside the
// layout the previous display list was built from.
func subtreeResult(
	ctx context.Context,
	styled pubstate.Styled,
	prev *Display,
	node *ihtml.Node,
	options Options,
) (*ilayout.Result, error) {
	layoutOpts, err := relayoutOptions(styled, prev, options)
	if err != nil {
		return nil, err
	}

	res, err := ilayout.RelayoutSubtree(ctx, prev.result, node, layoutOpts)
	if err != nil {
		return nil, fmt.Errorf("layout: relayout element: %w", err)
	}

	return res, nil
}

// relayoutOptions builds the engine options a patch pass runs with. The
// viewport and the media come from the styled document, and the zoom is the
// one the display was laid out at.
func relayoutOptions(styled pubstate.Styled, prev *Display, options Options) (ilayout.Options, error) {
	font, err := pdf.DefaultFont()
	if err != nil {
		var empty ilayout.Options

		return empty, fmt.Errorf("layout: default font: %w", err)
	}

	widthPt, heightPt := displayViewportPt(styled)
	layoutOpts := displayLayoutOptions(styled, font, options.Images, widthPt, heightPt)
	layoutOpts.Zoom = prev.zoom

	return layoutOpts, nil
}

// elementByID returns the element node with the given id in the tree, or nil.
func elementByID(root *ihtml.Node, elementID string) *ihtml.Node {
	if root == nil || elementID == "" {
		return nil
	}

	return root.FindFirst(func(candidate *ihtml.Node) bool {
		return candidate.Type == ihtml.ElementNode && candidate.Attribute("id") == elementID
	})
}

// elementBounds returns the border box of the element with the given id, in
// CSS pixels. A missing element gives the empty rectangle.
func elementBounds(boxes []Box, elementID string) image.Rectangle {
	for i := range boxes {
		if boxes[i].ID == elementID {
			return boxBounds(boxes[i])
		}
	}

	return image.Rectangle{} //nolint:exhaustruct // the zero rectangle marks a missing element
}

// boxBounds converts one element box to a rectangle in CSS pixels.
func boxBounds(box Box) image.Rectangle {
	left := int(math.Round(box.X))
	top := int(math.Round(box.Y))

	return image.Rect(left, top, left+int(math.Round(box.W)), top+int(math.Round(box.H)))
}
