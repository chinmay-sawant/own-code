package layout

import (
	"context"
	"errors"
	"fmt"

	"github.com/chinmay-sawant/blinkless/internal/errs"
	pdf "github.com/chinmay-sawant/blinkless/internal/fonts"
	"github.com/chinmay-sawant/blinkless/internal/html"
)

// ResolveStyles runs the same cascade LayoutContext uses. Convert uses it
// once so independent blocks share interned style pointers.
func ResolveStyles(ctx context.Context, root *html.Node, opts Options) (map[*html.Node]*ResolvedStyle, error) {
	if root == nil {
		return nil, errors.New("layout: nil root") //nolint:err113 // matches LayoutContext
	}

	if err := opts.validate(); err != nil {
		return nil, err
	}

	if ctx == nil {
		return nil, errs.ErrNilContext
	}

	styles, _, err := resolveStylesForLayoutContext(ctx, root, opts)
	if err != nil {
		return nil, fmt.Errorf("layout: style resolution: %w", err)
	}

	return styles, nil
}

// NodeWithWorkspace builds one already-styled node into workspace.
// styles must be the document cascade (same map IndependentBlocks saw).
func NodeWithWorkspace(
	ctx context.Context,
	node *html.Node,
	opts Options,
	styles map[*html.Node]*ResolvedStyle,
	workspace *Workspace,
) (*Result, error) {
	if node == nil {
		return nil, errors.New("layout: nil node") //nolint:err113 // matches LayoutContext
	}

	if styles == nil {
		return nil, errors.New("layout: nil styles") //nolint:err113 // required shared cascade
	}

	if err := opts.validate(); err != nil {
		return nil, err
	}

	if ctx == nil {
		return nil, errs.ErrNilContext
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("layout: context: %w", err)
	}

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

	ops := workspaceOps(workspace, estimateOpCapacity(node))

	eng := newEngine(ctx, opts, faces, font, styles, nil, ops)

	//nolint:contextcheck // ctx is stored on the engine and read by SVG calls
	rootBox := eng.build(node, opts.Width, 0, 0)

	if eng.err != nil {
		return nil, eng.err
	}

	eng.finalizeChrome(rootBox)

	return assembleNodeResult(eng, rootBox, opts, workspace), nil
}

// assembleNodeResult turns the built tree into the caller-facing Result:
// flattened boxes, final height, transform stamps, and census counters.
func assembleNodeResult(eng *engine, rootBox *box, opts Options, workspace *Workspace) *Result {
	boxes := make([]*box, 0)
	flattenBoxes(rootBox, &boxes)

	res := &Result{ //nolint:exhaustruct // intentional zero fields
		Ops:            eng.ops,
		Width:          opts.Width,
		Height:         opts.Height,
		pageSnapHeight: eng.pageSnapHeight,
		root:           rootBox,
		boxes:          boxes,
	}
	if rootBox != nil {
		res.Height = rootBox.y + rootBox.height
	}

	if res.Height < eng.height {
		res.Height = eng.height
	}

	// The canvas is final now: the canvas default fill must cover it, not the
	// root box it was emitted for.
	canvasH := res.Height
	if opts.Height > canvasH {
		canvasH = opts.Height
	}

	applyCanvasBackgroundGeometry(res.Ops, res.Width, canvasH)

	if eng.needsXformStamp {
		propagatePerspective(rootBox)
		stampBoxTransforms(rootBox, IdentityMatrix(), res.Ops, opts.Media)
	}

	res.MaxContentX, res.HasFragmentLinks = censusOps(res.Ops, opts.Width)
	res.skipInitialBeforeAlways = true

	// The run is over: drop the per-run caches the Result did not adopt.
	eng.releaseScratch()

	if workspace != nil {
		workspace.ops = res.Ops
	}

	return res
}
