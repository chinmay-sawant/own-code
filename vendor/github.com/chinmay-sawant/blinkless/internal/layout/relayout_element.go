package layout

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

// The override relayout reports the cases it cannot represent instead of
// applying a value the used style cannot carry.
var (
	// ErrUnknownOverrideProperty means an override named a property this
	// path does not apply to an element's used style. It is returned for a
	// misspelled name, a custom property, and a property whose family is
	// handled by another pass (transform, SVG paint, background paint).
	ErrUnknownOverrideProperty = errors.New("layout: unknown override property")

	// ErrInvalidOverrideValue means an override value is empty, is a
	// CSS-wide keyword, contains var(), or is not a value the property's
	// grammar accepts. Such a value cannot be resolved after the cascade,
	// so it is refused instead of leaving the element at its base value.
	ErrInvalidOverrideValue = errors.New("layout: invalid override value")

	// ErrOverrideReflow means the override changed the size of the box the
	// patch would re-place, so the change escapes the containing block the
	// patch covers. A host falls back to a full layout for that frame.
	ErrOverrideReflow = errors.New("layout: override reflow escapes its containing block")
)

// fontSizeProperty is the one property applied before the others, because
// every font-relative length in the same override list depends on it.
const fontSizeProperty = "font-size"

// StyleOverride is one property value applied to an element's used style for
// one relayout pass. The value is a CSS value for Property, not a computed
// value.
type StyleOverride struct {
	Property string
	Value    string
}

// RelayoutSubtreeWithOverrides re-lays out the containing block of node with
// overrides applied to node's used style, and returns node's subtree result.
//
// The override is applied after the cascade and before placement. It is never
// written to the parsed tree and never stored in the cached style map: the
// engine clones the element's used style, applies the properties to the clone,
// resolves the element's descendants against the clone so inherited properties
// reach them, and puts the cached style back when the call returns. prev's
// boxes, styles and operations after the call are the ones the static cascade
// produces, not the overridden frame, so the next patch starts from the base
// page again.
//
// The patch target is the element's containing block, not the element itself:
// a width or font-size change can move and resize the element's siblings, and
// only a re-place of the containing block reflows them. The target must be a
// block box that is not the document root and whose own border box keeps its
// geometry; when it does not, the pass returns ErrOverrideReflow so the host
// can fall back to a full layout. A table box, a float, a sticky box, or an
// anchored box inside the target is refused for the same reasons
// RelayoutSubtree refuses them.
//
// prev must carry the retained layout state. An empty override list delegates
// to RelayoutSubtree, so the call is the ordinary element patch.
//
//nolint:cyclop // the guard, style, build, splice, and result steps are one linear pass
func RelayoutSubtreeWithOverrides(
	ctx context.Context,
	prev *Result,
	node *html.Node,
	overrides []StyleOverride,
	opts Options,
) (*Result, error) {
	if err := relayoutGuards(ctx, prev, node, opts); err != nil {
		return nil, err
	}

	if len(overrides) == 0 {
		return RelayoutSubtree(ctx, prev, node, opts)
	}

	element := findRelayoutTarget(prev.boxTreeRoot(), node)
	if element == nil {
		return nil, errElementNotPlaced
	}

	target, err := overridePatchTarget(element)
	if err != nil {
		return nil, err
	}

	if err := checkRelayoutTarget(prev, target); err != nil {
		return nil, err
	}

	if target.box.node == nil {
		return nil, fmt.Errorf("%w: the containing block has no element", errSubtreeNotSplicable)
	}

	styles, restore, overridden, err := overrideSubtreeStyles(ctx, prev, target, node, overrides, opts)
	if err != nil {
		return nil, err
	}

	newBox, newOps, err := buildOverrideTarget(ctx, prev, target, node, styles, overridden, opts)
	if err != nil {
		return nil, err
	}

	elementBox := findBoxByNode(newBox, node)
	if elementBox == nil {
		return nil, errElementNotPlaced
	}

	// The retained state must describe the static page after this call, so
	// the overridden style is put back everywhere the pass replaced it. The
	// operations already carry the overridden values.
	restoreOverrideStyles(styles, restore)
	restoreOverrideBoxStyles(newBox, restore)

	if err := spliceOverridePatch(prev, target, newBox, newOps); err != nil {
		return nil, err
	}

	relayoutKeepBoxes(prev)
	relayoutKeepStyles(prev, styles)
	prev.MaxContentX, prev.HasFragmentLinks = censusOps(prev.Ops, prev.Width)

	return elementSubtreeResult(prev, elementBox, styles), nil
}

// buildOverrideTarget builds the target's subtree with the element's
// overridden style installed for the duration of the build, and returns the
// new box tree and the operations it emitted.
func buildOverrideTarget(
	ctx context.Context,
	prev *Result,
	target *relayoutTarget,
	node *html.Node,
	styles map[*html.Node]*ResolvedStyle,
	overridden *ResolvedStyle,
	opts Options,
) (*box, []Op, error) {
	eng, err := relayoutEngine(ctx, prev, opts, styles)
	if err != nil {
		return nil, nil, err
	}

	// eng.styleOverrides is the list buildWithStyle pushes onto. The pass
	// builds the containing block, not the element, so it installs the
	// element's clone for the duration of that build directly. The clone is
	// also the parent the descendants were resolved against, so an inherited
	// property reaches them through the style map.
	eng.styleOverrides = append(eng.styleOverrides, styleOverride{node: node, style: overridden})

	//nolint:contextcheck // the engine keeps the context and reads it at its recursion boundaries
	newBox, err := relayoutBuild(eng, prev, target, target.box.node, opts)
	eng.popStyleOverride()

	if err != nil {
		return nil, nil, overrideBuildError(err)
	}

	return newBox, eng.ops, nil
}

// spliceOverridePatch replaces the target's operation window with the new
// subtree's operations and puts the new box tree in the target's place. It is
// the splice RelayoutSubtree uses, scoped to the target the override pass
// re-placed.
func spliceOverridePatch(prev *Result, target *relayoutTarget, newBox *box, newOps []Op) error {
	start, oldCount, err := relayoutWindow(prev, target)
	if err != nil {
		return err
	}

	delta := len(newOps) - oldCount

	relayoutSpliceOps(prev, start, oldCount, newOps)
	relayoutRebaseTree(newBox, start)
	relayoutSwapBox(target, newBox)
	relayoutShiftRanges(prev.boxTreeRoot(), newBox, start, start+oldCount-1, delta)
	unionChildOpRanges(prev.boxTreeRoot())

	return nil
}

// overridePatchTarget returns the box the override patch re-places. An in-flow
// element is placed by its parent's flow, so the parent is the target. An
// absolutely or fixed positioned element is placed against its containing
// block, which can be a positioned ancestor above the parent; patching that
// ancestor keeps the element's offsets relative to the block it used.
func overridePatchTarget(element *relayoutTarget) (*relayoutTarget, error) {
	if len(element.ancestors) == 0 {
		return nil, fmt.Errorf("%w: the element is the root of the document", errSubtreeNotSplicable)
	}

	position := ""
	if element.box.style != nil {
		position = element.box.style.Position
	}

	if position == positionAbsolute || position == positionFixed {
		fixed := position == positionFixed

		for index := len(element.ancestors) - 1; index >= 0; index-- {
			ancestor := element.ancestors[index]
			if ancestor.style != nil && relayoutCBAncestor(ancestor.style, fixed) {
				return relayoutTargetAt(element.ancestors, index), nil
			}
		}

		return nil, fmt.Errorf(
			"%w: the element's containing block is above the patch",
			errSubtreeNotSplicable,
		)
	}

	return relayoutTargetAt(element.ancestors, len(element.ancestors)-1), nil
}

// relayoutTargetAt builds the target for one box in an ancestor chain. The
// root box of the chain has no parent box to splice into; the target check
// that follows refuses it.
func relayoutTargetAt(ancestors []*box, index int) *relayoutTarget {
	if index <= 0 {
		return &relayoutTarget{ //nolint:exhaustruct // a root target has no ancestors
			box:    ancestors[0],
			parent: nil,
		}
	}

	return &relayoutTarget{
		box:       ancestors[index],
		parent:    ancestors[index-1],
		ancestors: ancestors[:index],
	}
}

// overrideBuildError maps the splice's size check onto the override error.
func overrideBuildError(err error) error {
	if errors.Is(err, errSubtreeResized) {
		return fmt.Errorf("%w: %w", ErrOverrideReflow, err)
	}

	return err
}

// overrideSubtreeStyles resolves the target's subtree with node's style
// replaced by the overridden clone. It returns the pass style map, the base
// styles the call must put back, and the clone the engine reads while it
// builds the target.
func overrideSubtreeStyles(
	ctx context.Context,
	prev *Result,
	target *relayoutTarget,
	node *html.Node,
	overrides []StyleOverride,
	opts Options,
) (map[*html.Node]*ResolvedStyle, map[*html.Node]*ResolvedStyle, *ResolvedStyle, error) {
	styleCtx := relayoutOverrideStyleContext(ctx, prev, opts)

	styles, err := resolveOverrideStyles(styleCtx, target.box.node, relayoutParentStyle(target))
	if err != nil {
		return nil, nil, nil, err
	}

	base := styles[node]
	if base == nil {
		return nil, nil, nil, fmt.Errorf("%w: the element has no resolved style", errSubtreeNotSplicable)
	}

	overridden := *base
	overridden.CustomProps = maps.Clone(base.CustomProps)

	if err := applyOverrides(&overridden, styles[target.box.node], overrides, styleCtx); err != nil {
		return nil, nil, nil, err
	}

	restore := map[*html.Node]*ResolvedStyle{node: base}
	if err := inheritOverriddenStyle(styleCtx, node, &overridden, styles, restore); err != nil {
		return nil, nil, nil, err
	}

	return styles, restore, &overridden, nil
}

// relayoutOverrideStyleContext builds the cascade context of the override
// pass. It mirrors the context relayoutSubtreeStyles builds; the override
// pass needs the same inputs because it resolves the same branch.
func relayoutOverrideStyleContext(ctx context.Context, prev *Result, opts Options) *styleContext {
	styleCtx := &styleContext{ //nolint:exhaustruct // the pass fills only the inputs the subtree cascade reads
		ctx:                ctx,
		sheets:             opts.Sheets,
		properties:         registeredProperties(opts.Sheets),
		media:              opts.Media,
		viewportW:          opts.Width,
		viewportH:          opts.Height,
		printLinkUnderline: opts.PrintLinkUnderline,
		state:              opts.State,
	}

	if remBase := relayoutRemBase(prev); remBase > 0 {
		styleCtx.remBase = remBase
	}

	return styleCtx
}

// resolveOverrideStyles resolves the styles of root's subtree and returns them
// keyed by node. It is the map form of resolveStylesForSubtree: the override
// pass needs the resolved pointer for every node, including the subtree root,
// so it walks and stores in one pass instead of reading the pass memo back.
func resolveOverrideStyles(
	styleCtx *styleContext,
	root *html.Node,
	parent *ResolvedStyle,
) (map[*html.Node]*ResolvedStyle, error) {
	store := styleStore{} //nolint:exhaustruct // the store grows as the walk interns styles
	styles := make(map[*html.Node]*ResolvedStyle)

	var walk func(node *html.Node, inherited *ResolvedStyle) error

	walk = func(node *html.Node, inherited *ResolvedStyle) error {
		style := subtreeNodeStyle(node, styleCtx, inherited, &store)
		styles[node] = style

		for _, child := range node.Children {
			if err := walk(child, style); err != nil {
				return err
			}
		}

		return nil
	}

	if err := walk(root, parent); err != nil {
		return nil, err
	}

	return styles, nil
}

// inheritOverriddenStyle resolves node's descendants against the overridden
// style and records the base style of every node it replaces. The inherited
// properties of the subtree therefore come from the override, not from the
// cached style.
func inheritOverriddenStyle(
	styleCtx *styleContext,
	node *html.Node,
	overridden *ResolvedStyle,
	styles map[*html.Node]*ResolvedStyle,
	restore map[*html.Node]*ResolvedStyle,
) error {
	store := styleStore{} //nolint:exhaustruct // the store grows as the walk interns styles

	var walk func(parent *html.Node, inherited *ResolvedStyle) error

	walk = func(parent *html.Node, inherited *ResolvedStyle) error {
		for _, child := range parent.Children {
			if base, ok := styles[child]; ok {
				restore[child] = base
			}

			style := subtreeNodeStyle(child, styleCtx, inherited, &store)
			styles[child] = style

			if err := walk(child, style); err != nil {
				return err
			}
		}

		return nil
	}

	return walk(node, overridden)
}

// restoreOverrideStyles puts the base styles back into the pass map.
func restoreOverrideStyles(styles, restore map[*html.Node]*ResolvedStyle) {
	for node, style := range restore {
		styles[node] = style
	}
}

// restoreOverrideBoxStyles points every box the pass built at the base style
// it kept. The boxes keep the overridden geometry the call produced; only the
// style reference a later patch inherits from goes back to the cascade result.
func restoreOverrideBoxStyles(root *box, restore map[*html.Node]*ResolvedStyle) {
	var walk func(node *box)

	walk = func(node *box) {
		if node == nil {
			return
		}

		if style, ok := restore[node.node]; ok {
			node.style = style
		}

		for _, child := range node.children {
			walk(child)
		}

		for _, row := range node.rows {
			for _, cell := range row {
				walk(cell)
			}
		}
	}

	walk(root)
}

// findBoxByNode returns the box that owns node in root's subtree, or nil.
func findBoxByNode(root *box, node *html.Node) *box {
	if root == nil || node == nil {
		return nil
	}

	if root.node == node {
		return root
	}

	for _, child := range root.children {
		if found := findBoxByNode(child, node); found != nil {
			return found
		}
	}

	for _, row := range root.rows {
		for _, cell := range row {
			if found := findBoxByNode(cell, node); found != nil {
				return found
			}
		}
	}

	return nil
}

// elementSubtreeResult returns the element box's subtree as its own result.
// The result is rooted at the element and its operation ranges point into its
// sliced operation list, the shape relayoutSubtreeResult gives a patched
// element.
func elementSubtreeResult(prev *Result, element *box, styles map[*html.Node]*ResolvedStyle) *Result {
	start, count := element.opStart, element.opEnd-element.opStart+1
	if start < 0 || start > len(prev.Ops) || count < 0 || start+count > len(prev.Ops) {
		start, count = 0, 0
	}

	return relayoutSubtreeResult(prev, element, start, count, styles)
}

// propertyOverride is one validated override declaration in list order.
type propertyOverride struct {
	property string
	value    string
}

// applyOverrides writes the override declarations into style. Font-size runs
// first because em lengths in the same list resolve against it, then the
// cascade's shorthand-before-longhand order runs over the rest.
func applyOverrides(
	style, parent *ResolvedStyle,
	overrides []StyleOverride,
	styleCtx *styleContext,
) error {
	decls := make([]propertyOverride, 0, len(overrides))

	var fontSizes []string

	for index := range overrides {
		prop, value, err := overrideDeclaration(overrides[index])
		if err != nil {
			return err
		}

		if prop == fontSizeProperty {
			fontSizes = append(fontSizes, value)

			continue
		}

		decls = append(decls, propertyOverride{property: prop, value: value})
	}

	if len(fontSizes) > 0 {
		applyFontSizeOverrides(style, parent, fontSizes, decls, styleCtx)
	}

	raw := overrideRawDeclarations(decls)
	if len(raw) > 0 {
		applyRestProps(style, raw, styleCtx, parent)
	}

	return nil
}

// overrideRawDeclarations turns the ordered declarations into the raw map the
// cascade applier reads. A logical border width is expanded to its physical
// longhands here, and the list order decides between two declarations that
// write the same physical longhand.
func overrideRawDeclarations(decls []propertyOverride) map[string]string {
	raw := make(map[string]string, len(decls))

	for _, decl := range decls {
		if expanded, ok := expandLogicalBorder(decl.property, decl.value); ok {
			for _, item := range expanded {
				raw[item.prop] = item.val
			}

			continue
		}

		raw[decl.property] = decl.value
	}

	return raw
}

// applyFontSizeOverrides applies the font-size values and rescales a unitless
// line-height the element inherited, the way the cascade does when font-size
// and line-height both come from declarations.
func applyFontSizeOverrides(
	style, parent *ResolvedStyle,
	values []string,
	decls []propertyOverride,
	styleCtx *styleContext,
) {
	parentSize := style.FontSize
	if parent != nil {
		parentSize = parent.FontSize
	}

	remBase := pxToPt(defaultRootFontPx)
	if styleCtx != nil && styleCtx.remBase > 0 {
		remBase = styleCtx.remBase
	}

	single := map[string]string{fontSizeProperty: ""}

	for _, value := range values {
		single[fontSizeProperty] = value
		applyFontSizeValue(style, single, parentSize, styleCtx, remBase)
	}

	if !overrideDeclares(decls, "line-height") && style.LineHeightUnitless > 0 {
		style.LineHeight = style.FontSize * style.LineHeightUnitless
	}
}

// overrideDeclares reports whether the declaration list names prop.
func overrideDeclares(decls []propertyOverride, prop string) bool {
	for _, decl := range decls {
		if decl.property == prop {
			return true
		}
	}

	return false
}

// overrideDeclaration validates one override and returns its normalized
// property name and trimmed value.
func overrideDeclaration(override StyleOverride) (string, string, error) {
	prop := strings.TrimSpace(override.Property)
	value := strings.TrimSpace(override.Value)

	if prop == "" {
		return "", "", fmt.Errorf("%w: the property is empty", ErrUnknownOverrideProperty)
	}

	if strings.HasPrefix(prop, "--") {
		return "", "", fmt.Errorf("%w: %q is a custom property", ErrUnknownOverrideProperty, prop)
	}

	prop = normalizeVendorPrefix(strings.ToLower(prop))

	if !overridePropertyAllowed(prop) {
		return "", "", fmt.Errorf("%w: %q", ErrUnknownOverrideProperty, prop)
	}

	if err := validateOverrideValue(prop, value); err != nil {
		return "", "", err
	}

	return prop, value, nil
}

// validateOverrideValue checks that value is a concrete CSS value the
// property's applier can apply after the cascade.
func validateOverrideValue(prop, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %q has an empty value", ErrInvalidOverrideValue, prop)
	}

	if cssWideKeyword(value) {
		return fmt.Errorf("%w: %q is a CSS-wide keyword", ErrInvalidOverrideValue, prop)
	}

	if containsVarFunc(value) {
		return fmt.Errorf("%w: %q contains var()", ErrInvalidOverrideValue, prop)
	}

	if !supportedDeclaration(prop, value) {
		return fmt.Errorf("%w: %q is not a valid %s", ErrInvalidOverrideValue, value, prop)
	}

	if decls, ok := expandLogicalBorder(prop, value); ok {
		for _, decl := range decls {
			if !supportedDeclaration(decl.prop, decl.val) {
				return fmt.Errorf("%w: %q is not a valid %s", ErrInvalidOverrideValue, value, prop)
			}
		}
	}

	return nil
}

// overridePropertyAllowed reports whether the override path can write prop
// into an element's used style. The list is the layout tier: sizes, insets,
// box spacing, flex and grid placement, border and outline geometry, text
// metrics, and object position. A paint property, a transform, an SVG paint
// value, a display or flow property, and a property whose value the cascade
// expands into other declarations are refused because the used style alone
// does not carry the result.
//
//nolint:goconst // the allow list is written as CSS property names
func overridePropertyAllowed(prop string) bool {
	switch prop {
	case propWidth, propHeight, propMinWidth, propMinHeight, propMaxWidth, propMaxHeight,
		containerInlineSize, propBlockSize,
		propMinInlineSize, propMaxInlineSize, propMinBlockSize, propMaxBlockSize,
		"top", "right", "bottom", "left",
		insetKeyword, cssPropInsetBlock, cssPropInsetInline,
		cssPropInsetBlockStart, cssPropInsetBlockEnd, cssPropInsetInlineStart, cssPropInsetInlineEnd,
		marginProperty, propMarginTop, propMarginRight, propMarginBottom, propMarginLeft,
		cssPropMarginBlock, propMarginBlockStart, propMarginBlockEnd,
		cssPropMarginInline, propMarginInlineStart, propMarginInlineEnd,
		paddingProperty, propPaddingTop, propPaddingRight, propPaddingBottom, propPaddingLeft,
		cssPropPaddingBlock, propPaddingBlockStart, propPaddingBlockEnd,
		cssPropPaddingInline, propPaddingInlineStart, propPaddingInlineEnd,
		gapKeyword, propRowGap, propColumnGap,
		"flex-basis", "flex-grow", "flex-shrink",
		"grid-template-columns", "grid-template-rows", "grid-auto-columns", "grid-auto-rows",
		"aspect-ratio",
		borderWidthKeyword, propBorderTopWidth, propBorderRightWidth, propBorderBottomWidth, propBorderLeftWidth,
		cssPropBorderBlockWidth, cssPropBorderBlockStartWidth, cssPropBorderBlockEndWidth,
		cssPropBorderInlineWidth, cssPropBorderInlineStartWidth, cssPropBorderInlineEndWidth,
		"border-radius", "border-top-left-radius", "border-top-right-radius",
		"border-bottom-right-radius", "border-bottom-left-radius",
		"border-start-start-radius", "border-start-end-radius",
		"border-end-start-radius", "border-end-end-radius",
		propBorderSpacing,
		"outline-width", "outline-offset",
		"column-width", "column-height", "column-rule-width",
		fontSizeProperty, "line-height", "letter-spacing", "word-spacing",
		"text-indent", tabSizeProperty, "vertical-align",
		"text-decoration-thickness", "text-underline-offset",
		"object-position":
		return true
	default:
		return false
	}
}
