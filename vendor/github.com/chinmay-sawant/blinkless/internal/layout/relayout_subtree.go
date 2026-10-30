package layout

import (
	"context"
	"errors"
	"fmt"

	"github.com/chinmay-sawant/blinkless/internal/css"
	"github.com/chinmay-sawant/blinkless/internal/errs"
	pdf "github.com/chinmay-sawant/blinkless/internal/fonts"
	"github.com/chinmay-sawant/blinkless/internal/html"
)

// The subtree splice reports every case it cannot represent as an error. A
// wrong display list is worse than no display list: the host would paint
// stale pixels over a page it believes is correct.
var (
	errNoPreviousLayout    = errors.New("layout: no previous layout to patch")
	errElementNotPlaced    = errors.New("layout: element has no box")
	errNilElementNode      = errors.New("layout: nil element node")
	errSubtreeNotSplicable = errors.New("layout: element subtree cannot be spliced")
	errSubtreeResized      = errors.New("layout: element subtree changed size")
)

// documentNodeName is the name the parser gives the synthetic root node.
const documentNodeName = "#document"

// relayoutEpsilon is the tolerance of the geometry check. The subtree is laid
// out by the same code with the same inputs, so the numbers match exactly;
// the tolerance only absorbs the last bit of a float comparison.
const relayoutEpsilon = 1e-6

// RelayoutSubtree re-lays out one element's subtree inside prev and returns
// the patched result. The rest of the tree keeps its boxes and its ops.
//
// prev is patched in place. The element's operations are spliced over the
// range its box owned, the operations after that range shift by the change in
// count, the box is replaced in the tree, and the ancestor ranges are
// re-unioned over their children. Nothing else in prev is laid out again, so
// the boxes and the operations of the rest of the document are the ones the
// page already had.
//
// The returned Result carries the patched subtree on its own: its operations,
// its boxes, and the operation ranges that point into that slice. A caller
// repaints that region and leaves the rest of the page alone.
//
// The element keeps its size and its place: it is laid out in the width the
// kept box carries, so a grid item is placed in its own track and a flex
// item in its own line. That width is the kept one, so a width the content
// drives is not seen as a change.
//
// The height is the check that catches a content change. The one exception
// is a block size the containing block assigned: a flex container gives an
// item its used main or cross size before the item is built, and that size
// can be larger than the item's content. The kept style carries the
// assignment, so the build carries the kept height as a definite size and
// the content change check stays in force for every other element.
//
// A subtree that holds an out-of-flow box, a float, an anchored element, or
// a named-flow region, and a subtree whose size changed, are reported as
// errors instead of a wrong result.
func RelayoutSubtree(ctx context.Context, prev *Result, node *html.Node, opts Options) (*Result, error) {
	if err := relayoutGuards(ctx, prev, node, opts); err != nil {
		return nil, err
	}

	target := findRelayoutTarget(prev.boxTreeRoot(), node)
	if target == nil {
		return nil, errElementNotPlaced
	}

	if err := checkRelayoutTarget(prev, target); err != nil {
		return nil, err
	}

	styles, err := relayoutSubtreeStyles(ctx, prev, target, node, opts)
	if err != nil {
		return nil, err
	}

	eng, err := relayoutEngine(ctx, prev, opts, styles)
	if err != nil {
		return nil, err
	}

	//nolint:contextcheck // the engine keeps the context and reads it at its recursion boundaries
	newBox, err := relayoutBuild(eng, prev, target, node, opts)
	if err != nil {
		return nil, err
	}

	start, oldCount, err := relayoutWindow(prev, target)
	if err != nil {
		return nil, err
	}

	newOps := eng.ops
	delta := len(newOps) - oldCount

	relayoutSpliceOps(prev, start, oldCount, newOps)
	relayoutRebaseTree(newBox, start)
	relayoutSwapBox(target, newBox)
	relayoutShiftRanges(prev.boxTreeRoot(), newBox, start, start+oldCount-1, delta)
	unionChildOpRanges(prev.boxTreeRoot())

	relayoutKeepBoxes(prev)
	relayoutKeepStyles(prev, styles)
	prev.MaxContentX, prev.HasFragmentLinks = censusOps(prev.Ops, prev.Width)

	return relayoutSubtreeResult(prev, newBox, start, len(newOps), styles), nil
}

// relayoutGuards rejects the inputs the splice cannot work with.
func relayoutGuards(ctx context.Context, prev *Result, node *html.Node, opts Options) error {
	if ctx == nil {
		return errs.ErrNilContext
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("layout: context: %w", err)
	}

	if prev == nil || prev.boxTreeRoot() == nil {
		return errNoPreviousLayout
	}

	if node == nil {
		return errNilElementNode
	}

	if err := opts.validate(); err != nil {
		return err
	}

	// A @container rule depends on the used size of a container, which the
	// kept state does not carry. The subtree pass would resolve the subtree
	// against the wrong container sizes, so the splice refuses the document.
	if css.HasContainerRules(opts.Sheets) {
		return fmt.Errorf("%w: the document uses @container rules", errSubtreeNotSplicable)
	}

	return nil
}

// relayoutTarget is the element a subtree patch works on: the box that owns
// the element, the box that holds it, and the boxes above it.
type relayoutTarget struct {
	box       *box
	parent    *box
	ancestors []*box
}

// findRelayoutTarget returns the box that owns node, the box that holds it,
// and the boxes above it, or nil when the element owns no box.
func findRelayoutTarget(root *box, node *html.Node) *relayoutTarget {
	if root == nil || node == nil {
		return nil
	}

	var found *relayoutTarget

	var walk func(boxNode *box, parent *box, ancestors []*box) bool

	walk = func(boxNode *box, parent *box, ancestors []*box) bool {
		if boxNode.node == node {
			found = &relayoutTarget{box: boxNode, parent: parent, ancestors: ancestors}

			return true
		}

		chain := make([]*box, len(ancestors), len(ancestors)+1)
		copy(chain, ancestors)
		chain = append(chain, boxNode)

		for _, child := range boxNode.children {
			if walk(child, boxNode, chain) {
				return true
			}
		}

		for _, row := range boxNode.rows {
			for _, cell := range row {
				if walk(cell, boxNode, chain) {
					return true
				}
			}
		}

		return false
	}

	walk(root, nil, nil)

	return found
}

// checkRelayoutTarget rejects the elements and subtrees the splice cannot
// place: the document root, a table box, a subtree that leaves the flow, and
// a subtree under a transformed or faded ancestor.
func checkRelayoutTarget(prev *Result, target *relayoutTarget) error {
	if err := checkRelayoutBox(target); err != nil {
		return err
	}

	if err := checkRelayoutAncestors(target); err != nil {
		return err
	}

	if err := checkRelayoutSubtree(target.box, target.box.node, prev.styles); err != nil {
		return err
	}

	return checkRelayoutAnchors(prev.boxTreeRoot(), target.box)
}

// checkRelayoutBox rejects the element itself: the document root carries the
// canvas fill, and a table box is placed by the table layout, not by the
// block flow the splice uses.
func checkRelayoutBox(target *relayoutTarget) error {
	if target.parent == nil {
		return fmt.Errorf("%w: the element is the root of the document", errSubtreeNotSplicable)
	}

	name := ""
	if target.box.node != nil {
		name = target.box.node.Name
	}

	if name == documentNodeName || name == htmlRootName {
		return fmt.Errorf("%w: the element is the document root", errSubtreeNotSplicable)
	}

	if target.box.kind != boxKindBlock {
		return fmt.Errorf("%w: the element is not a block box", errSubtreeNotSplicable)
	}

	return nil
}

// checkRelayoutAncestors rejects a subtree under an ancestor that transforms
// or fades its content: the operations of the subtree carry that transform or
// that opacity, and a subtree laid out on its own does not.
func checkRelayoutAncestors(target *relayoutTarget) error {
	for _, ancestor := range target.ancestors {
		if ancestor.style == nil {
			continue
		}

		if ancestor.style.HasTransform || ancestor.style.Opacity < 1 {
			return fmt.Errorf(
				"%w: an ancestor carries a transform or an opacity",
				errSubtreeNotSplicable,
			)
		}
	}

	return nil
}

// checkRelayoutSubtree rejects a subtree that leaves the normal flow, floats,
// sticks, anchors, or feeds a named flow. Each of those places the box from
// state the kept result does not carry. An out-of-flow box is the exception
// when its containing block is inside the subtree, because the subtree lays
// that block out again and places the box against it.
func checkRelayoutSubtree(root *box, rootNode *html.Node, styles map[*html.Node]*ResolvedStyle) error {
	var walk func(boxNode *box) error

	walk = func(boxNode *box) error {
		if reason := relayoutSubtreeReason(boxNode.style); reason != "" {
			if reason != relayoutOutOfFlowReason ||
				!relayoutContainingBlockInside(boxNode.node, rootNode, styles) {
				return fmt.Errorf("%w: %s", errSubtreeNotSplicable, reason)
			}
		}

		for _, child := range boxNode.children {
			if err := walk(child); err != nil {
				return err
			}
		}

		for _, row := range boxNode.rows {
			for _, cell := range row {
				if err := walk(cell); err != nil {
					return err
				}
			}
		}

		return nil
	}

	return walk(root)
}

// relayoutOutOfFlowReason names an out-of-flow box in a subtree.
const relayoutOutOfFlowReason = "the subtree holds an out-of-flow box"

// relayoutContainingBlockInside reports whether the containing block of an
// out-of-flow box is inside the patched subtree. The block is the nearest
// ancestor that establishes a containing block, so a box under a relative
// stage inside the subtree is placed again by the subtree layout, while a box
// under a positioned ancestor above the subtree is not.
//
// The walk starts at the box's parent: a box never establishes its own
// containing block. A fixed box uses the viewport unless an ancestor inside
// the subtree establishes one through transform or containment.
func relayoutContainingBlockInside(node, rootNode *html.Node, styles map[*html.Node]*ResolvedStyle) bool {
	if node == nil || rootNode == nil {
		return false
	}

	fixed := false

	if style, ok := styles[node]; ok && style != nil {
		fixed = style.Position == positionFixed
	}

	for current := node.Parent; current != nil; current = current.Parent {
		if current.Type != html.ElementNode {
			continue
		}

		style, ok := styles[current]
		if ok && relayoutCBAncestor(style, fixed) {
			return true
		}

		if current == rootNode {
			return false
		}
	}

	return false
}

// relayoutCBAncestor reports whether one ancestor establishes the containing
// block of the out-of-flow box.
func relayoutCBAncestor(style *ResolvedStyle, fixed bool) bool {
	if style == nil {
		return false
	}

	if fixed {
		return fixedContainingBlock(*style)
	}

	return establishesAbsCB(*style)
}

// fixedContainingBlock reports whether one ancestor establishes a containing
// block for a fixed box. Position alone does not: a fixed box against a
// relative ancestor still uses the viewport.
func fixedContainingBlock(style ResolvedStyle) bool {
	if style.HasTransform {
		return true
	}

	if containsLayout(style) {
		return true
	}

	return containsPaint(style)
}

// relayoutSubtreeReason names the flow the style leaves, or an empty string
// when the style keeps the box in the normal flow.
func relayoutSubtreeReason(style *ResolvedStyle) string {
	if style == nil {
		return ""
	}

	switch {
	case style.Float != cssDisplayNone:
		return "the subtree holds a float"
	case style.Position == positionAbsolute || style.Position == positionFixed:
		return relayoutOutOfFlowReason
	case style.Position == positionSticky:
		return "the subtree holds a sticky box"
	case len(style.AnchorNames) > 0 || style.PositionAnchor != "":
		return "the subtree holds an anchored element"
	case style.FlowInto != "" || style.FlowFrom != "":
		return "the subtree holds a named flow"
	}

	return ""
}

// checkRelayoutAnchors rejects a document that anchors a box outside the
// patched subtree: that box sits where its anchor sits, so a patch that
// moved the anchor would leave it stale.
func checkRelayoutAnchors(root *box, patched *box) error {
	var walk func(boxNode *box) error

	walk = func(boxNode *box) error {
		if boxNode == patched {
			return nil
		}

		if boxNode.style != nil && boxNode.style.PositionAnchor != "" {
			return fmt.Errorf("%w: an anchored box sits outside the subtree", errSubtreeNotSplicable)
		}

		for _, child := range boxNode.children {
			if err := walk(child); err != nil {
				return err
			}
		}

		for _, row := range boxNode.rows {
			for _, cell := range row {
				if err := walk(cell); err != nil {
					return err
				}
			}
		}

		return nil
	}

	return walk(root)
}

// relayoutSubtreeStyles resolves the styles of the patched subtree and hands
// back the map the fresh engine reads. The subtree inherits from the style
// its parent box carries, and rem lengths resolve against the root's used
// font size, so the branch comes out with the same values a full cascade
// would give it.
func relayoutSubtreeStyles(
	ctx context.Context, prev *Result, target *relayoutTarget, node *html.Node, opts Options,
) (map[*html.Node]*ResolvedStyle, error) {
	styleCtx := &styleContext{ //nolint:exhaustruct // intentional zero fields
		ctx:                ctx,
		sheets:             opts.Sheets,
		properties:         registeredProperties(opts.Sheets),
		media:              opts.Media,
		viewportW:          opts.Width,
		viewportH:          opts.Height,
		printLinkUnderline: opts.PrintLinkUnderline,
		state:              opts.State,
	}

	parent := relayoutParentStyle(target)
	remBase := relayoutRemBase(prev)

	if err := resolveStylesForSubtree(ctx, styleCtx, node, parent, remBase); err != nil {
		return nil, err
	}

	styles := subtreeStyles(styleCtx, node, parent)
	if len(styles) == 0 {
		return nil, fmt.Errorf("%w: the subtree pass resolved no styles", errSubtreeNotSplicable)
	}

	relayoutTextStyles(styles)

	// The engine reads the styles of the ancestors when it walks up the tree
	// for the containing block of an out-of-flow child, so the kept styles of
	// the boxes above the subtree join the map.
	for _, ancestor := range target.ancestors {
		if ancestor.node == nil || ancestor.style == nil {
			continue
		}

		if _, ok := styles[ancestor.node]; !ok {
			styles[ancestor.node] = ancestor.style
		}
	}

	return styles, nil
}

// relayoutParentStyle returns the style the patched element inherits from.
func relayoutParentStyle(target *relayoutTarget) *ResolvedStyle {
	if target.parent == nil || target.parent.node == nil || target.parent.style == nil {
		return nil
	}

	if target.parent.node.Name == documentNodeName {
		return nil
	}

	return target.parent.style
}

// relayoutRemBase returns the used font size of the root element, the base
// rem lengths resolve against. Zero means the document has no root element,
// and the cascade then uses the initial medium size.
func relayoutRemBase(prev *Result) float64 {
	for _, boxNode := range prev.boxes {
		if boxNode == nil || boxNode.node == nil || boxNode.style == nil {
			continue
		}

		if boxNode.node.Name == htmlRootName && boxNode.style.FontSize > 0 {
			return boxNode.style.FontSize
		}
	}

	return 0
}

// relayoutTextStyles gives every text node of the subtree the style of its
// parent element, the way the document cascade does.
func relayoutTextStyles(styles map[*html.Node]*ResolvedStyle) {
	for node, style := range styles {
		if node == nil || node.Type != html.ElementNode {
			continue
		}

		for _, child := range node.Children {
			if child.Type != html.TextNode {
				continue
			}

			if _, ok := styles[child]; !ok {
				styles[child] = style
			}
		}
	}
}

// relayoutEngine builds the fresh engine that lays out the subtree. The
// operation counter continues past the operations the kept result already
// carries, so the patched operations keep their own identity.
func relayoutEngine(
	ctx context.Context, prev *Result, opts Options, styles map[*html.Node]*ResolvedStyle,
) (*engine, error) {
	faces, err := pdf.LoadDefaultFaces()
	if err != nil {
		return nil, fmt.Errorf("layout: load default faces: %w", err)
	}

	if opts.Faces != nil {
		faces = opts.Faces
	}

	font := opts.Font
	if font == nil {
		font = faces.Regular
	}

	eng := newEngine(ctx, opts, faces, font, styles, nil, nil)
	eng.nextOpID = relayoutMaxOpID(prev.Ops) + 1

	return eng, nil
}

// relayoutMaxOpID returns the highest operation identity the kept result
// carries.
func relayoutMaxOpID(ops []Op) uint64 {
	var maxID uint64

	for index := range ops {
		if ops[index].ID > maxID {
			maxID = ops[index].ID
		}
	}

	return maxID
}

// relayoutBuild lays the subtree out and returns its new box. The build
// starts at the place the old box held, so the operations land where the
// operations they replace stood.
func relayoutBuild(
	eng *engine, prev *Result, target *relayoutTarget, node *html.Node, opts Options,
) (*box, error) {
	availW, posX, posY := relayoutOrigin(eng, target, opts)

	newBox := eng.buildWithStyle(node, relayoutBlockSizeOverride(eng, target), availW, posX, posY)

	if eng.err != nil {
		return nil, eng.err
	}

	if newBox == nil {
		return nil, fmt.Errorf("%w: the element placed no box", errElementNotPlaced)
	}

	// The deferred backgrounds and borders of the subtree merge here. The
	// merge is scoped to the subtree's own operation window, and it remaps
	// the ranges of the boxes it placed.
	eng.finalizeChrome(newBox)

	relayoutApplyDocumentColor(eng, prev, node)
	relayoutApplyAncestorClip(eng, target)

	if eng.needsXformStamp {
		propagatePerspective(newBox)
		stampBoxTransforms(newBox, IdentityMatrix(), eng.ops, opts.Media)
	}

	if !relayoutSameBox(newBox, target.box) {
		return nil, fmt.Errorf(
			"%w: the element is %gx%g at (%g, %g), the patch makes it %gx%g at (%g, %g)",
			errSubtreeResized,
			target.box.w, target.box.height, target.box.x, target.box.y,
			newBox.w, newBox.height, newBox.x, newBox.y,
		)
	}

	return newBox, nil
}

// relayoutBlockSizeOverride returns the style the subtree build carries when
// the containing block assigned the element's used block size, or nil when
// the element's own style decides it.
//
// A flex container sizes its items before they are built: a column item gets
// the flex-used main size and a stretched row item gets the line's cross
// size, which the container writes into the item's style. The standalone
// build has no parent flex pass to repeat that, so the retained height is
// installed as a definite size. The override is the one the flex pass writes:
// the block size and the max block size in the box's own sizing, so a taller
// content still overflows the assigned box instead of growing it.
func relayoutBlockSizeOverride(eng *engine, target *relayoutTarget) *ResolvedStyle {
	if target == nil || target.box == nil || target.box.node == nil {
		return nil
	}

	fresh := eng.styles[target.box.node]
	if fresh == nil {
		return nil
	}

	height, ok := relayoutAssignedBlockSize(eng, target, fresh)
	if !ok {
		return nil
	}

	override := *fresh
	eng.forceFlexItemCrossSize(&override, height)

	return &override
}

// relayoutAssignedBlockSize returns the block size the containing block
// assigned the element, when the element's own cascade leaves the size auto.
//
// The kept style carries the fingerprint of that assignment: the flex pass
// writes the used border-box height into both Height and MaxHeight and
// clears both percentages, a pair the cascade does not produce on its own.
// The parent must be the formatting context that assigns it: a column flex
// container with a definite content height, or a row flex container whose
// align stretch applies to the item.
//
// A content-sized element keeps no such fingerprint, and a flex item in an
// indefinite column keeps the size its content chose, so both still fail the
// size check when their content changes.
func relayoutAssignedBlockSize(
	eng *engine, target *relayoutTarget, fresh *ResolvedStyle,
) (float64, bool) {
	if target.parent == nil || target.parent.style == nil || fresh == nil {
		return 0, false
	}

	// The element's own cascade decides the size when it names one. The
	// guard keeps its power over a class that changes that size.
	if fresh.Height >= 0 || fresh.HeightPercent >= 0 {
		return 0, false
	}

	if !relayoutFlexAssignedHeight(target.box.style) {
		return 0, false
	}

	if !relayoutFlexAssignsBlockSize(eng, target, fresh) {
		return 0, false
	}

	return target.box.height, true
}

// relayoutFlexAssignedHeight reports whether style carries the block size a
// flex container assigned an item. forceFlexItemCrossSize writes the used
// border-box height into Height and MaxHeight and clears both percentages.
func relayoutFlexAssignedHeight(style *ResolvedStyle) bool {
	if style == nil || style.Height < 0 || style.MaxHeight < 0 {
		return false
	}

	if style.HeightPercent >= 0 || style.MaxHeightPercent >= 0 {
		return false
	}

	return relayoutNear(style.Height, style.MaxHeight)
}

// relayoutFlexAssignsBlockSize reports whether the parent flex container
// assigned the item's used block size. The assignment is expressed on the
// physical block axis, so a vertical writing mode keeps the full layout.
func relayoutFlexAssignsBlockSize(
	eng *engine, target *relayoutTarget, fresh *ResolvedStyle,
) bool {
	parentStyle := target.parent.style

	if isVerticalWritingMode(parentStyle.WritingMode) || isVerticalWritingMode(fresh.WritingMode) {
		return false
	}

	if parentStyle.Display != displayFlex && parentStyle.Display != displayInlineFlex {
		return false
	}

	switch relayoutFlexDirection(parentStyle) {
	case fxRow, fxRowRev:
		// The line's cross size is the item's used block size, whatever the
		// item's content measures.
		return flexItemCrossStretchNode(target.box.node, parentStyle, fresh)
	case fxCol, fxColRev:
		// The flex main size is definite only when the container's content
		// height is and the retained line consumed it. A content-sized
		// column, or one that leaves free space, keeps every item at its
		// content height, so the check must not claim an assignment there.
		return relayoutFlexColumnFilled(eng, target.parent)
	}

	return false
}

// relayoutFlexColumnFilled reports whether the retained column consumed its
// definite content height with flex sizing. When the items leave free space,
// no grow or shrink ran and every used main size is the item's content-based
// base, so the kept height is not an assignment and the size check keeps its
// power. Wrapped lines have per-line free space the sum does not expose, so
// they keep the full layout.
func relayoutFlexColumnFilled(eng *engine, parent *box) bool {
	if parent.style.FlexWrap == fxWrap || parent.style.FlexWrap == fxWrapRev {
		return false
	}

	contentH := resolveFlexColumnContentHeight(*parent.style, eng)
	if contentH < 0 {
		return false
	}

	used := 0.0
	items := 0

	for _, child := range parent.children {
		if child == nil || child.style == nil {
			continue
		}

		if child.style.Position == positionAbsolute || child.style.Position == positionFixed {
			continue
		}

		used += child.height + eng.scalePt(child.style.MarginTop) + eng.scalePt(child.style.MarginBottom)
		items++
	}

	if items == 0 {
		return false
	}

	rowGap, _ := eng.styleGaps(parent.style)
	used += rowGap * float64(items-1)

	return relayoutNear(used, contentH)
}

// relayoutFlexDirection returns the container's main axis, applying the
// initial row direction when the declaration is absent.
func relayoutFlexDirection(style *ResolvedStyle) string {
	if style.FlexDirection == "" {
		return fxRow
	}

	return style.FlexDirection
}

// relayoutOrigin returns the width the element is laid out in, the x it is
// placed at, and the y it is placed at.
//
// The element keeps the box it had, so the kept box gives the place the
// parent gave it: a grid item is placed in its own track and a flex item in
// its own line, not in the whole holding box.
//
// The engine puts the border box at the origin plus the element's left
// margin, and it takes both margins off the width it is given. The origin is
// therefore the kept origin minus the left margin, and the width is the
// kept width plus both margins.
func relayoutOrigin(eng *engine, target *relayoutTarget, opts Options) (float64, float64, float64) {
	posY := target.box.y

	kept := target.box
	if kept.style == nil {
		return opts.Width, kept.x, posY
	}

	marginL := eng.scalePt(kept.style.MarginLeft)
	marginR := eng.scalePt(kept.style.MarginRight)

	return relayoutAvailableWidth(eng, kept, marginL, marginR), kept.x - marginL, posY
}

// relayoutAvailableWidth returns the width the element is laid out in.
//
// The element keeps the width it had, so the width the parent gave it comes
// from the kept box. An auto width takes the kept border-box width plus the
// margins the engine takes off it, and a percentage width takes the width
// the kept content width was a percentage of.
func relayoutAvailableWidth(eng *engine, kept *box, marginL, marginR float64) float64 {
	pct := kept.style.WidthPercent
	if pct <= 0 {
		return kept.w + marginL + marginR
	}

	contentW := kept.w
	if kept.style.BoxSizing != borderBox {
		contentW -= kept.style.horizontalChrome(eng)
	}

	if contentW < 0 {
		contentW = 0
	}

	return contentW * oneHundred / pct
}

// relayoutSameBox reports whether two boxes sit at the same place with the
// same size.
func relayoutSameBox(left, right *box) bool {
	if left == nil || right == nil {
		return false
	}

	return relayoutNear(left.x, right.x) && relayoutNear(left.y, right.y) &&
		relayoutNear(left.w, right.w) && relayoutNear(left.height, right.height)
}

// relayoutNear reports whether two lengths are the same length.
func relayoutNear(left, right float64) bool {
	diff := left - right
	if diff < 0 {
		diff = -diff
	}

	return diff <= relayoutEpsilon
}

// relayoutApplyDocumentColor applies the document's color-adjust paint
// policy to the new operations. The subtree pass runs the policy with the
// subtree root, which is not the document root, so the policy is applied
// here with the root the document laid out.
func relayoutApplyDocumentColor(eng *engine, prev *Result, node *html.Node) {
	if node != nil && node.Name == htmlRootName {
		return
	}

	eng.applyColorAdjustPaintPolicy(prev.boxTreeRoot())
}

// relayoutApplyAncestorClip clips the new operations to the padding box of
// the ancestors that clip paint. The fresh engine knows the clips inside the
// subtree; the clips above it live in the kept result.
func relayoutApplyAncestorClip(eng *engine, target *relayoutTarget) {
	var clip *clipRect

	for _, ancestor := range target.ancestors {
		next := eng.computeBoxOverflowClip(ancestor, clip)
		clip = next
	}

	if clip == nil || clip.empty() || len(eng.ops) == 0 {
		return
	}

	clipOpsRange(eng.ops, 0, len(eng.ops)-1, *clip)
}

// relayoutWindow returns the operation range the element's box owned and the
// number of operations in it. An element that emitted no operation has no
// range to replace, so the place its operations would have taken is derived
// from its neighbours.
func relayoutWindow(prev *Result, target *relayoutTarget) (int, int, error) {
	boxNode := target.box

	if boxNode.opStart >= 0 && boxNode.opEnd >= boxNode.opStart && boxNode.opEnd < len(prev.Ops) {
		return boxNode.opStart, boxNode.opEnd - boxNode.opStart + 1, nil
	}

	start := relayoutInsertionIndex(prev, target)
	if start < 0 || start > len(prev.Ops) {
		return 0, 0, fmt.Errorf("%w: the element owns no operations", errSubtreeNotSplicable)
	}

	return start, 0, nil
}

// relayoutInsertionIndex returns the place the operations of an element that
// emitted none belong: in front of the next sibling that has operations, or
// behind the last operation of the box that holds it.
func relayoutInsertionIndex(prev *Result, target *relayoutTarget) int {
	parent := target.parent

	if parent != nil {
		for index, child := range parent.children {
			if child != target.box {
				continue
			}

			for _, next := range parent.children[index+1:] {
				if next.opEnd >= next.opStart && next.opStart >= 0 {
					return next.opStart
				}
			}

			break
		}
	}

	if parent != nil && parent.opEnd >= parent.opStart && parent.opEnd < len(prev.Ops) {
		return parent.opEnd + 1
	}

	return len(prev.Ops)
}

// relayoutSpliceOps replaces the operations of the old range with the new
// ones and shifts the tail. The result keeps its own operation list, so a
// host that holds the old one still reads the operations it had until it
// copies the patch over.
func relayoutSpliceOps(prev *Result, start, oldCount int, newOps []Op) {
	ops := make([]Op, 0, len(prev.Ops)-oldCount+len(newOps))
	ops = append(ops, prev.Ops[:start]...)
	ops = append(ops, newOps...)
	ops = append(ops, prev.Ops[start+oldCount:]...)

	prev.Ops = ops
}

// relayoutRebaseTree moves the operation ranges of a new box tree from the
// window the fresh engine used to the window the patched result uses.
func relayoutRebaseTree(boxNode *box, shift int) {
	if boxNode == nil {
		return
	}

	boxNode.opStart += shift
	boxNode.opEnd += shift

	for _, child := range boxNode.children {
		relayoutRebaseTree(child, shift)
	}

	for _, row := range boxNode.rows {
		for _, cell := range row {
			relayoutRebaseTree(cell, shift)
		}
	}
}

// relayoutSwapBox puts the new box where the old one stood.
func relayoutSwapBox(target *relayoutTarget, newBox *box) {
	if target.parent == nil {
		return
	}

	for index, child := range target.parent.children {
		if child == target.box {
			target.parent.children[index] = newBox

			return
		}
	}
}

// relayoutShiftRanges moves the operation ranges of the boxes outside the
// patched subtree. The ranges after the window shift by delta, and the ranges
// that cover the window grow or shrink with it.
func relayoutShiftRanges(root *box, patched *box, start, end, delta int) {
	var walk func(boxNode *box)

	walk = func(boxNode *box) {
		if boxNode == patched {
			return
		}

		if boxNode.opEnd >= boxNode.opStart && boxNode.opEnd >= start {
			switch {
			case boxNode.opStart > end:
				boxNode.opStart += delta
				boxNode.opEnd += delta
			default:
				if boxNode.opStart >= start {
					boxNode.opStart = start
				}

				boxNode.opEnd += delta
			}
		}

		for _, child := range boxNode.children {
			walk(child)
		}

		for _, row := range boxNode.rows {
			for _, cell := range row {
				walk(cell)
			}
		}
	}

	walk(root)
}

// relayoutKeepBoxes rebuilds the flattened box list of the patched result.
func relayoutKeepBoxes(prev *Result) {
	boxes := make([]*box, 0)
	flattenBoxes(prev.boxTreeRoot(), &boxes)

	prev.boxes = boxes
}

// relayoutKeepStyles writes the styles of the patched subtree into the kept
// style map, so the map and the box tree agree after the patch.
func relayoutKeepStyles(prev *Result, styles map[*html.Node]*ResolvedStyle) {
	if prev.styles == nil {
		prev.styles = make(map[*html.Node]*ResolvedStyle, len(styles))
	}

	for node, style := range styles {
		prev.styles[node] = style
	}
}

// relayoutSubtreeResult returns the patched subtree on its own: its
// operations and its boxes, with the box ranges pointing into that slice.
func relayoutSubtreeResult(
	prev *Result, newBox *box, start, count int, styles map[*html.Node]*ResolvedStyle,
) *Result {
	subRoot := rebaseBox(newBox, start)

	boxes := make([]*box, 0)
	flattenBoxes(subRoot, &boxes)

	return &Result{ //nolint:exhaustruct // a subtree result keeps the zero fields
		Ops:     prev.Ops[start : start+count],
		Width:   prev.Width,
		Height:  prev.Height,
		root:    subRoot,
		rootBox: subRoot,
		boxes:   boxes,
		styles:  styles,
	}
}

// rebaseBox copies one box subtree and shifts every operation range so the
// indices point into the sliced operation list of the subtree.
func rebaseBox(src *box, shift int) *box {
	if src == nil {
		return nil
	}

	clone := *src
	clone.children = nil
	clone.rows = nil
	clone.stickyPort = nil

	if src.opEnd >= src.opStart {
		clone.opStart = src.opStart - shift
		clone.opEnd = src.opEnd - shift
	}

	for _, child := range src.children {
		clone.children = append(clone.children, rebaseBox(child, shift))
	}

	for _, row := range src.rows {
		clonedRow := make([]*box, 0, len(row))

		for _, cell := range row {
			clonedRow = append(clonedRow, rebaseBox(cell, shift))
		}

		clone.rows = append(clone.rows, clonedRow)
	}

	clone.stickyPort = rebaseBox(src.stickyPort, shift)

	return &clone
}
