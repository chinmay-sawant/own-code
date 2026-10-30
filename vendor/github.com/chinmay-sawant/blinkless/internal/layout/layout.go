// Package layout turns the parsed HTML tree plus resolved styles into a
// display list: absolute-positioned drawing operations in a continuous
// canvas (y grows downward from the top of the page content area). The
// display list is the final output; consumers render or serialize it.
//
// Report-engine scope: block and inline flow, margin collapsing between
// siblings, tables (separate borders, colspan), images, lists, text wrapping
// with the embedded Liberation Sans font, float lite (left/right + clear),
// real inline-block, box-sizing, position relative/absolute/fixed lite,
// print-scoped sticky (page content box = scrollport; overflow boxes at
// scroll offset 0 — see sticky.go), and a
// partial flex (row/column) subset, CSS grid lite (tracks, gap, column span),
// and CSS multi-column lite (column-count/width/gap/span/fill).
package layout

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
	pdf "github.com/chinmay-sawant/blinkless/internal/fonts"
	"github.com/chinmay-sawant/blinkless/internal/html"
)

const (
	htmlTbody    = "tbody"
	htmlThead    = "thead"
	htmlCaption  = "caption"
	htmlColgroup = "colgroup"
	htmlTfoot    = "tfoot"
	htmlInput    = "input"
)

// CSS keyword constants shared by the layout engine. Kept here so repeated
// string literals resolve through one named value (goconst).
const (
	positionAbsolute        = "absolute"
	positionFixed           = "fixed"
	positionRelative        = "relative"
	positionStatic          = "static"
	positionSticky          = "sticky"
	balanceKeyword          = "balance"
	htmlMeter               = "meter"
	displayBlock            = "block"
	displayFlex             = "flex"
	displayGrid             = "grid"
	displayInlineFlex       = "inline-flex"
	displayInlineGrid       = "inline-grid"
	displaySubgrid          = "subgrid"
	displayFlowRoot         = "flow-root"
	displayTable            = "table"
	displayTableCell        = "table-cell"
	displayTableCaption     = "table-caption"
	displayTableRow         = "table-row"
	displayRowGroup         = "table-row-group"
	displayHeaderGroup      = "table-header-group"
	displayFooterGroup      = "table-footer-group"
	displayListItem         = "list-item"
	listStyleDisc           = "disc"
	listStyleSquare         = "square"
	listStyleDecimal        = "decimal"
	listPosInside           = "inside"
	listPosOutside          = "outside"
	htmlSection             = "section"
	pseudoBefore            = "before"
	pseudoAfter             = "after"
	bulletDisc              = "\u2022"
	borderCollapseValue     = "collapse"
	marginBreakKeep         = "keep"
	overflowWrapAnywhere    = "anywhere"
	overflowWrapBreakWord   = "break-word"
	borderStyleDashed       = "dashed"
	borderStyleDotted       = "dotted"
	textTransformNone       = "none"
	textTransformUppercase  = "uppercase"
	textTransformLowercase  = "lowercase"
	textTransformCapitalize = "capitalize"
	tableCellKind           = "cell"
	defaultTabSize          = 8
)

// Static validation errors for Options and PaintOptions. Dynamic values are
// wrapped with %w at the return site so messages keep their detail.
var (
	errInvalidViewportWidth  = errors.New("layout: width must be finite and greater than zero")
	errInvalidViewportHeight = errors.New("layout: height must be finite and non-negative")
	errInvalidZoom           = errors.New("layout: zoom must be zero or a finite positive value")
	errInvalidMediaValue     = errors.New("layout: media")
	errLayoutDepthExceeded   = errors.New("layout: document nesting depth exceeds 2048")
)

// maxBuildDepth caps DOM recursion during layout so pathologically deep
// documents stop with errLayoutDepthExceeded instead of overflowing the
// goroutine stack.
const maxBuildDepth = 2048

// Options controls a Layout run.
type Options struct {
	Width    float64 // viewport/content width in points
	Height   float64 // viewport height in points (for % heights)
	Font     *pdf.Font
	Faces    *pdf.FaceSet  // optional Liberation family; defaults loaded when nil
	Registry *pdf.Registry // optional discovered fonts (--font-path)
	Sheets   []*css.Stylesheet
	Media    string // "print" or "screen"; "" = apply "all" rules only
	// State carries the ids of the focused, hovered, and pressed elements for
	// the stateful pseudo-classes. An empty state matches none of them.
	State css.MatchState
	// ImagesContext is the cancellation-aware image resolver. When set, it
	// takes precedence over Images.
	ImagesContext func(ctx context.Context, src string) ([]byte, error)
	Images        func(src string) ([]byte, error)
	Background    bool    // paint background colors
	DebugBoxes    bool    // outline every box for test/golden output
	Zoom          float64 // zoom factor; style lengths are scaled by it (any positive value, < 1 shrinks)
	// ForcedColorsActive is the document-wide forced-colors mode switch
	// (Windows high-contrast / prefers forced palette). When true, paint maps
	// forced-color-adjust:auto elements to the system color while :none keeps
	// author colors, per css-color-adjust-1. Default false (print has no
	// forced-colors mode), matching Options.Background as a document setting.
	ForcedColorsActive bool
	// PrintLinkUnderline is an opt-in operator policy (--print-link-underline):
	// after cascade, force text-decoration:underline on a[href]. Default off
	// so author CSS (including inherit → none) is honored.
	PrintLinkUnderline bool
	// gridMemoBypass disables the grid preferred-height memo (see
	// grid_memo.go). Tests only: unexported so external callers keep the
	// default, while in-package tests can prove cached and uncached layouts
	// produce identical output and benchmark the memo's effect.
	gridMemoBypass bool
	// flexMinBypass disables the column flex auto-minimum memo (see flex.go).
	// Tests only: unexported so external callers keep the default, while
	// in-package tests can prove cached and uncached layouts produce
	// identical output and benchmark the memo's effect.
	flexMinBypass bool
}

// validate rejects option values that would otherwise be silently clamped
// or ignored: a non-positive viewport width, a negative viewport height (0
// means unset), a negative or non-finite zoom (zoomScale would clamp it to
// 1), and media values other than print, screen, or empty.
func (o Options) validate() error {
	if !finitePositive(o.Width) {
		return fmt.Errorf("%w, got %g", errInvalidViewportWidth, o.Width)
	}

	if !finiteNonNegative(o.Height) {
		return fmt.Errorf("%w, got %g", errInvalidViewportHeight, o.Height)
	}

	if o.Zoom != 0 && !finitePositive(o.Zoom) {
		return fmt.Errorf("%w, got %g", errInvalidZoom, o.Zoom)
	}

	switch strings.ToLower(strings.TrimSpace(o.Media)) {
	case "", "print", "screen":
		return nil
	default:
		return fmt.Errorf("%w %q is not print or screen", errInvalidMediaValue, o.Media)
	}
}

func finitePositive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

// Result is a display list plus the canvas bounds.
type Result struct {
	Ops    []Op
	Width  float64
	Height float64

	root  *box   // element box tree, kept for Paint (Locations)
	boxes []*box // flattened box list for repeated pagination updates

	// styles is the resolved cascade of this layout run, keyed by node. It
	// lets a later run re-lay out one subtree without resolving the whole
	// document again.
	styles map[*html.Node]*ResolvedStyle
	// svgRasters caches the raster of a serialized inline SVG across paint
	// restyles. It is created on the first RestyleSVGPaint and dropped with
	// the Result, so it never outlives the layout it belongs to.
	svgRasters *svgRasterCache
	// rootBox is the root of the element box tree. It is the same box as
	// root and the entry point for the subtree lookups in layout_state.go
	// (boxForID, nodeForID).
	rootBox *box

	// flowPages indexes non-fixed operations by their current canvas page while
	// pagination is settling. It is rebuilt when the display list is rewritten
	// and updated in place by shiftFlowY so flow shifts do not rescan operations
	// that are before the affected page.
	flowPages   [][]int
	flowPageOf  []int
	flowPos     []int
	flowBoxes   [][]int
	flowBoxPage []int
	flowBoxPos  []int
	// pageSnapHeight records the page content height that multicol column
	// snapping used during Layout (Options.Height). Paint compares it with
	// its own content height so columns cannot snap to one boundary while
	// paint splits at another. Zero means no page-height-dependent snapping
	// ran, so any paint height is acceptable.
	pageSnapHeight float64

	// Pages maps page index → indices into Ops of the ops painted on that
	// page. Filled by Paint using its pagination semantics (an op goes to
	// the page containing its top edge; ops crossing a page boundary move
	// wholly to the next page).
	Pages [][]int
	// Locations lists every element box in document order with the page its
	// first op landed on and its canvas rect. Filled by Paint; boxes without
	// ops use the page of their y position.
	Locations []ElementLocation

	// MaxContentX is the right edge of fill, stroke, and image ops. Layout
	// sets it to at least Width so convert can skip a second op walk. Zero
	// means unknown (hand-built Result values).
	MaxContentX float64
	// HasIDs is set during Paint when any location node has an id attribute.
	HasIDs bool
	// HasFragmentLinks is set during Layout when any OpLinkURI starts with '#'.
	HasFragmentLinks bool
	// skipInitialBeforeAlways is set for independently painted body blocks.
	// Those results already start a fresh page, so page-break-before:always
	// on the block itself would insert a blank page.
	skipInitialBeforeAlways bool
}

// CloneResult returns an independent pagination result. Display-list image
// bytes, page/index slices, and the layout-owned box graph are copied so a
// second paint or pagination pass cannot mutate the source result through a
// shallow alias.
func CloneResult(res *Result) *Result {
	if res == nil {
		return nil
	}

	clone := *res
	clone.Ops = cloneOps(res.Ops)
	// A clone is an independent pagination result, so it starts without the
	// source's raster cache instead of sharing a mutable map of payloads.
	clone.svgRasters = nil
	clone.Pages = cloneIndexPages(res.Pages)
	clone.Locations = append([]ElementLocation(nil), res.Locations...)
	clone.flowPages = cloneIndexPages(res.flowPages)
	clone.flowPageOf = append([]int(nil), res.flowPageOf...)
	clone.flowPos = append([]int(nil), res.flowPos...)
	clone.flowBoxes = cloneIndexPages(res.flowBoxes)
	clone.flowBoxPage = append([]int(nil), res.flowBoxPage...)
	clone.flowBoxPos = append([]int(nil), res.flowBoxPos...)
	boxes := make(map[*box]*box, len(res.boxes))
	clone.root = cloneBoxGraph(res.root, boxes)
	clone.rootBox = clone.root
	clone.boxes = make([]*box, len(res.boxes))

	for i, source := range res.boxes {
		clone.boxes[i] = cloneBoxGraph(source, boxes)
	}

	return &clone
}

func cloneOps(src []Op) []Op {
	if src == nil {
		return nil
	}

	dst := make([]Op, len(src))
	for i := range src {
		dst[i] = src[i]
		dst[i].opExtra = cloneOpExtra(src[i].opExtra)
	}

	return dst
}

func cloneIndexPages(src [][]int) [][]int {
	if src == nil {
		return nil
	}

	dst := make([][]int, len(src))
	for i := range src {
		dst[i] = append([]int(nil), src[i]...)
	}

	return dst
}

func cloneBoxGraph(src *box, seen map[*box]*box) *box {
	if src == nil {
		return nil
	}

	if clone, ok := seen[src]; ok {
		return clone
	}

	clone := *src
	clone.children = nil
	clone.rows = nil
	clone.stickyPort = nil
	seen[src] = &clone

	for _, child := range src.children {
		clone.children = append(clone.children, cloneBoxGraph(child, seen))
	}

	for _, row := range src.rows {
		clonedRow := make([]*box, 0, len(row))
		for _, cell := range row {
			clonedRow = append(clonedRow, cloneBoxGraph(cell, seen))
		}

		clone.rows = append(clone.rows, clonedRow)
	}

	clone.stickyPort = cloneBoxGraph(src.stickyPort, seen)

	return &clone
}

// Workspace owns reusable display-list storage for sequential internal
// layouts. It is deliberately separate from Result so the established Layout
// and LayoutContext APIs keep their independent-result contract.
//
// A Workspace is not safe for concurrent use. Call Release only after Paint
// and every consumer has copied the result metadata it needs.
type Workspace struct {
	ops []Op
}

const (
	// workspaceReuseFloor keeps small workspaces reusable without churn:
	// below this backing capacity the ops slice is always retained and
	// reused for the next run.
	workspaceReuseFloor = 4096
	// workspaceReuseRatio bounds how oversized a retained backing may be
	// relative to what a run holds or needs before it is dropped. A huge
	// page followed by small ones must not pin the huge backing across
	// every later run.
	workspaceReuseRatio = 4
)

// workspaceOps returns reusable display-list storage for one run estimated
// to need entries. A retained backing that dwarfs the estimate is
// dropped instead of reused so the workspace does not pin the largest page
// ever laid out.
func workspaceOps(workspace *Workspace, need int) []Op {
	if workspace == nil || cap(workspace.ops) == 0 {
		return make([]Op, 0, need)
	}

	if backed := cap(workspace.ops); backed > workspaceReuseFloor && need > 0 &&
		backed > workspaceReuseRatio*need {
		return make([]Op, 0, need)
	}

	return workspace.ops[:0]
}

// Release returns a painted Result's display-list backing storage to w and
// clears Result references that would otherwise retain its box tree, its
// cascade, its raster cache, and its paint indexes. It is a no-op for nil
// inputs.
func (w *Workspace) Release(res *Result) {
	if w == nil || res == nil {
		return
	}

	ops := res.Ops
	for i := range ops {
		ops[i] = Op{} //nolint:exhaustruct // drop extra pointers so Release can GC rare payloads
	}

	// Drop a backing that dwarfs what it holds so one huge page does not pin
	// its storage across every later run; the next acquire allocates fresh.
	if backed, used := cap(ops), len(ops); backed > workspaceReuseFloor && used > 0 &&
		backed > workspaceReuseRatio*used {
		ops = nil
	}

	w.ops = ops[:0]
	res.Ops = nil
	res.root = nil
	res.rootBox = nil
	res.boxes = nil
	// The cascade and the restyle raster cache die with the Result: a
	// released Result keeps no box tree to patch, so keeping them would pin
	// the whole style map and up to svgRasterCacheMaxPixels of decoded
	// rasters across every later run of the workspace.
	res.styles = nil
	res.svgRasters = nil
	res.flowPages = nil
	res.flowPageOf = nil
	res.flowPos = nil
	res.flowBoxes = nil
	res.flowBoxPage = nil
	res.flowBoxPos = nil
	res.Pages = nil
	res.Locations = nil
}

// ElementLocation describes where one element box landed after pagination.
// X/Y/W/H are in canvas coordinates (y down from the top of the page content
// area); Page is the page index the box's first op was painted on.
type ElementLocation struct {
	Node *html.Node
	Page int
	X, Y float64
	W, H float64
}

// NodeRef provides the neutral node projection used by outline.
func (loc ElementLocation) NodeRef() *html.Node { return loc.Node }

// PageIndex provides the neutral page projection used by outline.
func (loc ElementLocation) PageIndex() int { return loc.Page }

// Bounds provides the neutral rectangle projection used by outline.
func (loc ElementLocation) Bounds() (float64, float64, float64, float64) {
	return loc.X, loc.Y, loc.W, loc.H
}

// OpKind discriminates display-list operations. uint8 keeps the hot Op
// record packed; the enum is smaller than 256 values.
type OpKind uint8

const (
	// OpUnknown is the zero value of OpKind. Layout emits it only as the
	// boundary marker of a blend or isolation group, which carries no paint,
	// and painters treat it as inert so a zero Op cannot paint as OpFillRect.
	OpUnknown OpKind = iota
	OpFillRect
	OpStrokeRect
	OpLine
	OpText
	OpImage
	OpLinkURI
	OpBullet
	// OpGridRun is one table row's collapsed border grid emitted as a single
	// display-list entry. Grid holds the ordered line segments the row used to
	// append as individual OpLine entries; painters replay them in order.
	OpGridRun
)

// Stroke masks are used only by rounded border display-list operations.
const (
	StrokeMaskTop uint8 = 1 << iota
	StrokeMaskRight
	StrokeMaskBottom
	StrokeMaskLeft
)

// LineInset selects the side of a mixed-width CSS border whose stroke should
// be painted inward from the border-box edge.
const (
	LineInsetTop uint8 = 1 << iota
	LineInsetRight
	LineInsetBottom
	LineInsetLeft
)

// Op is one display-list operation. Coordinates are in canvas points; for
// OpText and OpBullet, Y is the baseline.
//
// Rare payloads (URI, Image, Xform, BlendMode, structure tags, text-transform,
// text synthesis gates, and the canvas-background marker)
// live on the embedded *opExtra so the hot record is 256 bytes. Promoted
// field names stay so readers (paint, tests, bindings) keep op.URI and
// op.Image. Writers must detachExtra before mutating those fields.
//
//nolint:recvcheck // paint readers take Op by value; extra writers need *Op
type Op struct {
	// ID is the stable logical identity of the operation. Pagination may split
	// one operation into several fragments; fragments retain this ID so
	// location/range ownership can be remapped without treating a fragment as
	// a new document operation.
	ID uint64

	X, Y    float64
	W, H    float64
	R, G, B float64 // 0..1
	Alpha   float64
	Width   float64 // stroke width for OpLine

	Size float64
	// LetterSpacing is the CSS letter-spacing value in points for text paint.
	LetterSpacing float64
	// InkDescent is the glyph descent below the baseline. H remains the line
	// box height; pagination uses this narrower metric for generated text so a
	// line is not moved merely because its leading crosses a page boundary.
	InkDescent float64

	Text string
	Font *pdf.Font
	// Grid holds the line segments of an OpGridRun. Nil for every other kind.
	Grid *GridRun
	*opExtra

	// Radius is the uniform border radius for rounded fill/stroke rectangles.
	// RadiusY is the vertical radius when corners are elliptical; 0 means ry=rx.
	Radius                                                                 float64
	RadiusTopLeft, RadiusTopRight, RadiusBottomRight, RadiusBottomLeft     float64
	RadiusY                                                                float64
	RadiusTopLeftY, RadiusTopRightY, RadiusBottomRightY, RadiusBottomLeftY float64

	// StickyID links display-list ops to a position:sticky box after parent
	// prependChrome shifts op indices (0 = not sticky).
	StickyID int
	// ZIndex paints later (higher) above earlier ops when non-zero or set.
	ZIndex int
	// RotateDeg rotates the glyph around its baseline origin. Independent of
	// CSS transform CTM (which wraps the whole op via Xform).
	RotateDeg float32

	Kind OpKind
	// StrokeMask selects sides for a rounded OpStrokeRect. Zero means the
	// complete rounded rectangle; non-zero masks are used for mixed CSS
	// borders whose accented side must retain its corner arcs.
	StrokeMask uint8
	// LineInset selects inward paint geometry for mixed-width straight borders.
	// The logical OpLine coordinates remain on the border-box edge so
	// pagination ownership checks keep using layout geometry.
	LineInset   uint8
	Bold        bool
	FakeOblique bool // synthesize italic skew when face is upright
	IsJPEG      bool
	// IsBackground marks background/border images that belong to the chrome layer.
	IsBackground bool
	// Fixed marks ops from position:fixed boxes; Paint stamps them on every
	// page at viewport-relative coordinates.
	Fixed bool
	// Pinned keeps canvas Y stable under later index-suffix flow shifts
	// (repeated thead clones appended after document ops). Unlike Fixed,
	// pinned ops paint only on their natural page.
	Pinned bool
	// ZIndexSet marks an explicitly assigned ZIndex (including zero).
	ZIndexSet bool
	// Positioned marks operations emitted by an absolute/fixed subtree. Within
	// the same z-index band, positioned descendants paint above in-flow text,
	// while their own backgrounds remain below their own content.
	Positioned bool
	// XformSet marks a baked Xform; identity matrices stay unset.
	XformSet bool
}

// PaintLineGeometry returns the line geometry after applying mixed-border
// inward painting. The returned width is in the same points as the operation.
func (paintOp Op) PaintLineGeometry() (float64, float64, float64, float64, float64) {
	width := paintOp.Width
	if width <= 0 {
		width = 1
	}

	lineX, lineY, lineW, lineH := paintOp.X, paintOp.Y, paintOp.W, paintOp.H

	const halfWidthRatio = 0.5
	halfWidth := width * halfWidthRatio

	switch paintOp.LineInset {
	case LineInsetTop:
		lineX += halfWidth
		lineY += halfWidth
		lineW -= width
	case LineInsetRight:
		lineX -= halfWidth
		lineY += halfWidth
		lineH -= width
	case LineInsetBottom:
		lineX += halfWidth
		lineY -= halfWidth
		lineW -= width
	case LineInsetLeft:
		lineX += halfWidth
		lineY += halfWidth
		lineH -= width
	}

	if paintOp.LineInset != 0 {
		if lineW < 0 {
			lineW = 0
		}

		if lineH < 0 {
			lineH = 0
		}
	}

	return lineX, lineY, lineW, lineH, width
}

type engine struct {
	opts     Options
	ctx      context.Context //nolint:containedctx // ctx is checked at recursion boundaries (checkContext)
	err      error
	font     *pdf.Font // default/regular face (metrics fallback)
	faces    *pdf.FaceSet
	registry *pdf.Registry
	// styles holds immutable resolved styles per node (from resolveStylesCtx).
	// Transient layout sizes use styleOverrides and engine-generated anonymous
	// nodes use syntheticStyles; callers use stylePtr for shared
	// *ResolvedStyle without a second copy.
	styles          map[*html.Node]*ResolvedStyle
	syntheticStyles map[*html.Node]*ResolvedStyle
	styleOverrides  []styleOverride
	// styleIndex narrows selector matching for this layout's sheets. It is
	// built once and shared by the cascade and every pseudo-content lookup.
	styleIndex *ruleIndex
	// pseudoCtx is the shared generated-content cascade context, refreshed
	// per use by pseudoStyleContext so sequential ::before/::after probes
	// reuse its match buffers instead of allocating.
	pseudoCtx   *styleContext
	ops         []Op
	gridScratch []GridSeg // reusable row-grid collector storage
	noEmit      bool      // measurement mode: compute geometry without emitting ops
	// maxRotatedRunW is the widest rotated (sideways) text advance seen in the
	// current build subtree. verticalWritingHeight consumes it instead of
	// scanning e.ops so measured geometry matches emitted geometry when
	// noEmit drops the rotated text ops.
	maxRotatedRunW float64
	height         float64
	scale          float64 // zoom factor applied to style lengths (>= 1)
	zIndex         int
	zIndexSet      bool
	positioned     bool
	blendMode      string
	// blendGroup is the CSS element group that owns newly emitted ops
	// (masking, mix-blend-mode, or isolation). nil means page-level paint.
	// blendGroupOwner is the style that created the innermost group, so the
	// same element's inline runs are not wrapped in a second group.
	blendGroup      *BlendGroup
	blendGroupOwner *ResolvedStyle
	nextGroupID     int
	stickySeq       int // monotonically increasing sticky box IDs (for Op.StickyID)
	// transformCBDepth counts ancestors with transform≠none; fixed→absolute CB.
	transformCBDepth int
	// imgMaxW > 0 clamps replaced <img> boxes to this containing-block width
	// (table cell / float / inline formatting context).
	imgMaxW float64
	// imgCBIndefinite marks an intrinsic sizing pass with no definite
	// containing block (grid track sizing): a percentage width on replaced
	// content is cyclic there and resolves as auto (CSS Grid L1 §11.5), so the
	// contribution must not echo the viewport or container width back into a
	// track's base size.
	imgCBIndefinite bool
	// inlineCBW is the inline formatting context's content width when > 0;
	// used so width:% on inline-blocks resolves against the real CB.
	inlineCBW float64
	// imgCache resolves each <img src> at most once per Layout run (measure
	// and build share the same decode).
	imgCache map[string]*imageRef
	// gridMeasureCache memoizes grid-item preferred heights for one Layout
	// run (see grid_memo.go). The engine is constructed fresh per Layout, so
	// entries never survive past the run that measured them.
	gridMeasureCache map[gridMeasureKey]gridMeasureResult
	// gridMemoBypass disables the grid preferred-height memo. Tests only: an
	// unexported copy of Options.gridMemoBypass that proves cached and
	// uncached layouts produce identical output.
	gridMemoBypass bool
	// flexBaseH memoizes column flex base heights for this Layout run, keyed
	// by item node, resolved style identity, and containing sizes. A nested
	// flex container measures each item subtree once for the hypothetical
	// size and rebuilds it once for placement; without the memo the inner
	// container re-measures its items on the rebuild, doubling the work at
	// every nesting level.
	flexBaseH map[flexBaseKey]float64
	// flexMinH memoizes column flex auto-minimum floors for this Layout run,
	// keyed by item node, resolved style identity, and containing sizes (see
	// flexMinKey). Shrink and clamp ask for the same floor, and each noEmit
	// ancestor measure re-runs the owning container, so the memo keeps one
	// content flow per item per run. The engine is constructed fresh per
	// Layout, so entries never survive past the run that measured them.
	flexMinH map[flexMinKey]float64
	// pseudoContentCache memoizes the winning content declaration per
	// (element, pseudo-element) for one Layout run. Every block probes
	// ::before/::after during inline collection and every inline element
	// probes both again for pseudo items, so without the memo the same
	// selector match runs several times per element per run although the
	// cascade is fixed for the run.
	pseudoContentCache map[pseudoContentKey]pseudoContentEntry
	// contentEnvCache memoizes the counter/quote environment per (target
	// element, pseudo-element) for one Layout run. Resolving a counter()
	// walks the document from the root, so without the memo every row of
	// a counter-numbered list rewalks the same prefix per probe although
	// the cascade and the tree are fixed for the run. Entries are
	// canonical: callers receive a clone because evaluation mutates
	// quote depth.
	contentEnvCache map[contentEnvKey]*contentEnv
	// flexMinBypass disables the column flex auto-minimum memo. Tests only:
	// an unexported copy of Options.flexMinBypass that proves cached and
	// uncached layouts produce identical output.
	flexMinBypass bool
	// svgInlineCache memoizes inline-<svg> rasters within one Layout run,
	// keyed by serialized document plus raster resolution. Repeated identical
	// icons rasterize once per run instead of once per occurrence. The cache
	// dies with the engine, so it never outlives the layout it describes.
	svgInlineCache *svgRasterCache
	// containers is the last size-container map used for cascade (nil if none);
	// pseudo-content reuses it so @container gates match cascadeRaw.
	containers map[*html.Node]sizeContainer
	// bfcFloats is the floatState of the nearest enclosing BFC. Ordinary
	// blocks reuse it so floats affect later siblings; BFC roots push a
	// fresh state (see pushBFCFloats).
	bfcFloats *floatState
	// bfcStack restores previous BFC float states without allocating a
	// closure per push (table cells establish a BFC each).
	bfcStack []*floatState
	// bfcPool recycles floatState values for pushBFCFloats.
	bfcPool []*floatState
	// measureFloatDepth is non-zero while a noEmit measure pass is running.
	// Measurement must not leave floats in the enclosing BFC, or a later real
	// build packs a new float beside a ghost one (case 29: float:left in a
	// column flex item landed at the right content edge).
	measureFloatDepth int
	// measureFloats is the scratch state float registration is redirected to
	// during the outermost measure scope; savedBFCFloats restores the live
	// state when that scope ends.
	measureFloats  floatState
	savedBFCFloats *floatState
	// absCBHeights carries the containing-block height to deferred absolute
	// children after their in-flow parent has finished determining its size.
	absCBHeights map[*html.Node]float64
	// initialCBHeight is Options.Height while that viewport is the percentage
	// basis for the root element. flowCBHeight points at it for that element.
	initialCBHeight float64
	flowCBHeight    *float64 // nil means the in-flow containing-block height is indefinite
	// flowWritingMode is the containing block's writing mode while its
	// in-flow children build. An auto inline size resolves as fit-content
	// when a child's writing mode is orthogonal to it (CSS Writing Modes).
	flowWritingMode string
	// inlineItemPool recycles temporary inline-item backing arrays. The pool is
	// engine-local because layout is single-threaded and nested inline layout
	// must retain each active caller's slice.
	inlineItemPool [][]inlineItem
	// deferredChrome holds background/border ops to splice in one linear
	// pass (finalizeChrome) for the common non-sticky/non-fixed/non-transform
	// path. Sticky/fixed/transform boxes still splice immediately.
	deferredChrome []chromeEntry
	nextOpID       uint64
	// faceByStyle caches faceFor results for this Layout run (family hash +
	// weight/italic). Avoids repeating registry lookups on every measure call.
	faceByStyle map[faceStyleKey]*pdf.Font
	// lastFaceKey/lastFace are a one-entry cache for the common case of an
	// inline run reusing one face for every rune.
	lastFaceKey faceStyleKey
	lastFace    *pdf.Font
	hasLastFace bool
	// faceByRune caches faceForRune fallback results for this Layout run
	// (only glyphs missing from the primary face). Key uses a family hash so
	// lookups allocate no joined family string.
	faceByRune map[faceRuneKey]*pdf.Font
	// lineGapByFace caches each face's hhea line gap ratio (lineGap/upem) for
	// line-height: normal. The font package does not expose the gap.
	lineGapByFace map[*pdf.Font]float64
	// textWidthCache memoizes measureTextFace string widths for this Layout
	// run, keyed by face, size, spacing and text. Trade tables repeat short
	// mono price labels; the per-rune fallback loop is pure for a fixed key.
	textWidthCache map[textWidthKey]float64
	// runeWidthCache memoizes measureRuneFace single-rune widths for this
	// Layout run (decoration and min-content paths walk rune by rune).
	runeWidthCache map[runeWidthKey]float64
	// needsXformStamp is set when any built box has transform≠none or
	// opacity<1 so stampBoxTransforms can skip the full tree walk.
	needsXformStamp bool
	// buildDepth tracks DOM recursion during layout so pathologically
	// deep documents are stopped instead of overflowing the stack.
	buildDepth int
	// pageSnapHeight records the finite Options.Height that multicol column
	// snapping used (0 when no multicol page-height snapping ran). Paint
	// rejects a mismatch instead of splitting ops at a boundary the layout
	// never snapped to.
	pageSnapHeight float64
	// namedFlows maps one flow ident to its source for this Layout run
	// (css-regions-1 lite, see named_flow.go). namedFlowScanDone records the
	// one-time document scan that registers sources ahead of their regions.
	namedFlows        map[string]*namedFlow
	namedFlowScanDone bool
}

// releaseScratch drops every per-run cache the Result does not adopt, so
// nothing but Ops and styles outlives the run. Ops and styles move into the
// Result and are left alone; the maps below die with the engine either way,
// and nil-ing them here releases their backing the moment the run ends
// instead of at GC. Call it after the Result is built and never touch the
// engine again.
func (e *engine) releaseScratch() {
	if e == nil {
		return
	}

	e.syntheticStyles = nil
	e.styleOverrides = nil
	e.styleIndex = nil
	e.pseudoCtx = nil
	e.gridScratch = nil
	e.imgCache = nil
	e.gridMeasureCache = nil
	e.flexBaseH = nil
	e.flexMinH = nil
	e.pseudoContentCache = nil
	e.contentEnvCache = nil
	e.svgInlineCache = nil
	e.containers = nil
	e.bfcFloats = nil
	e.bfcStack = nil
	e.bfcPool = nil
	e.savedBFCFloats = nil
	e.absCBHeights = nil
	e.inlineItemPool = nil
	e.deferredChrome = nil
	e.faceByStyle = nil
	e.lastFace = nil
	e.hasLastFace = false
	e.faceByRune = nil
	e.lineGapByFace = nil
	e.textWidthCache = nil
	e.runeWidthCache = nil
	e.namedFlows = nil
	e.namedFlowScanDone = false
}

// styleOverride temporarily substitutes one node's resolved style while that
// node is built. Overrides are engine-local and form a stack so nested builds
// always observe the most recent override for the same node.
type styleOverride struct {
	node  *html.Node
	style *ResolvedStyle
}

// chromeEntry records one box's background/border ops for insertion before
// the content op at index `at`. Owner is the box that requested chrome so
// chrome-only ranges and geometry shifts can be applied after merge.
type chromeEntry struct {
	at  int
	ops []Op
	b   *box
}

func (e *engine) add(paintOp Op) {
	if e.checkContext() || e.noEmit {
		return
	}

	if paintOp.ID == 0 {
		e.nextOpID++
		paintOp.ID = e.nextOpID
	}

	if !e.noEmit {
		paintOp.bindEmptyExtra()
		paintOp.ZIndex = e.zIndex
		paintOp.ZIndexSet = e.zIndexSet
		paintOp.Positioned = e.positioned

		if e.blendGroup != nil {
			// Group members carry the group pointer, not the inherited blend
			// mode: the painter composites the buffered group once instead of
			// blending every operation separately.
			paintOp.setBlendGroup(e.blendGroup)
		} else if paintOp.BlendMode == "" || paintOp.BlendMode == blendNormal {
			paintOp.setBlendMode(e.blendMode)
		}

		e.ops = append(e.ops, paintOp)
	}
}

// checkContext is intentionally cheap and is called at recursive layout
// boundaries. Existing Layout callers keep the same behavior through the
// background-context wrapper, while LayoutContext can stop large documents
// during tree construction instead of waiting for painting.
func (e *engine) checkContext() bool {
	if e.err != nil {
		return true
	}

	if e.ctx == nil {
		return false
	}

	if err := e.ctx.Err(); err != nil {
		e.err = err

		return true
	}

	return false
}

// blendScope captures the state pushZ overrides for one element so popZ can
// restore it and close the element's group.
type blendScope struct {
	prevZ          int
	prevZSet       bool
	prevPositioned bool
	prevBlendMode  string
	prevGroup      *BlendGroup
	prevGroupOwner *ResolvedStyle
	// beginMark is the op index of this element's begin marker, -1 when the
	// element creates no group.
	beginMark int
}

// pushZ enters one element's stacking and compositing scope, emitting the
// group begin marker when masking, blending, or isolation creates a group.
// owner is the resolved style pointer that identifies the element.
func (e *engine) pushZ(style ResolvedStyle, owner *ResolvedStyle) blendScope {
	prev := blendScope{
		prevZ:          e.zIndex,
		prevZSet:       e.zIndexSet,
		prevPositioned: e.positioned,
		prevBlendMode:  e.blendMode,
		prevGroup:      e.blendGroup,
		prevGroupOwner: e.blendGroupOwner,
		beginMark:      -1,
	}
	createsGroupContext := style.MixBlendMode != blendNormal || style.Isolation == unicodeBidiIsolate ||
		maskGroupRequested(style)

	if style.Position == positionAbsolute || style.Position == positionFixed {
		e.positioned = true
	}

	e.enterStackingContext(style, createsGroupContext)
	e.enterBlendIsolation(style, owner)

	if e.blendGroup != prev.prevGroup {
		prev.beginMark = e.addGroupMark(e.blendGroup, groupMarkBegin)
	}

	return prev
}

// enterStackingContext applies CSS stacking-context creation: an explicit
// z-index wins, otherwise transform, opacity, masking, blending, or isolation
// reset the level to zero.
func (e *engine) enterStackingContext(style ResolvedStyle, createsGroupContext bool) {
	if style.ZIndexSet {
		e.zIndex = style.ZIndex
		e.zIndexSet = true
	} else if style.HasTransform || style.Opacity < 1 || createsGroupContext {
		// CSS: transform, opacity, blend, and isolation create a stacking context.
		e.zIndex = 0
		e.zIndexSet = true
	}
}

// enterBlendIsolation applies the transform stamp and enters the element's
// paint group. The group starts on a transparent backdrop; descendants inherit
// its membership so mask, blend, or isolation is applied once to the subtree.
func (e *engine) enterBlendIsolation(style ResolvedStyle, owner *ResolvedStyle) {
	if style.HasTransform || style.Opacity < 1 {
		e.needsXformStamp = true
	}

	// normalizeBlendMode accepts the CSS vocabulary; the style apply arm
	// already normalized MixBlendMode, so only "normal"/empty skip.
	mode := nonNormalBlendMode(style.MixBlendMode)

	hasMask := maskGroupRequested(style)
	if mode == "" && style.Isolation != "isolate" && !hasMask {
		return
	}

	e.nextGroupID++

	e.blendGroup = &BlendGroup{ //nolint:exhaustruct // mask frames are attached below
		ID:      e.nextGroupID,
		Mode:    mode,
		Isolate: true,
		Parent:  e.blendGroup,
		Mask:    nil,
	}
	if hasMask {
		e.attachMaskDescriptors(e.blendGroup, style)
	}

	e.blendGroupOwner = owner

	e.applyBlendMode(&style, mode)
}

// addGroupMark appends a begin/end boundary op for group and returns its op
// index, or -1 when emission is suppressed.
func (e *engine) addGroupMark(group *BlendGroup, mark uint8) int {
	if group == nil || e.checkContext() || e.noEmit {
		return -1
	}

	e.nextOpID++

	markOp := Op{ //nolint:exhaustruct // marker ops carry only identity and scope
		ID:         e.nextOpID,
		Kind:       OpUnknown,
		ZIndex:     e.zIndex,
		ZIndexSet:  e.zIndexSet,
		Positioned: e.positioned,
	}
	markOp.setGroupMark(group, mark)
	e.ops = append(e.ops, markOp)

	return len(e.ops) - 1
}

// patchGroupMark stamps the owning element's geometry onto a boundary marker.
// Flow scans (table sliver repair, row tops, page buckets) read op Y, so a
// zero-height marker at the canvas origin would move unrelated boxes.
func (e *engine) patchGroupMark(idx int, posX, posY, width, height float64) {
	if idx < 0 || idx >= len(e.ops) {
		return
	}

	e.ops[idx].X = posX
	e.ops[idx].Y = posY
	e.ops[idx].W = width
	e.ops[idx].H = height
}

// popZ restores the scope pushZ saved. When the element created a group, the
// end marker is emitted here so the markers stay balanced even when the
// subtree build emitted no paint operations.
func (e *engine) popZ(scope blendScope, boxNode *box) {
	// A mask-border frame raster is composed here, while the element's box
	// size and its group are both known, so repeat/round/space can tile
	// against the real destination box.
	if boxNode != nil {
		e.composeMaskBorderOnGroup(e.blendGroup, boxNode)
	}

	if scope.beginMark >= 0 {
		if boxNode != nil {
			e.patchGroupMark(scope.beginMark, boxNode.x, boxNode.y, boxNode.w, boxNode.height)
		}

		endMark := e.addGroupMark(e.blendGroup, groupMarkEnd)

		if boxNode != nil {
			e.patchGroupMark(endMark, boxNode.x, boxNode.y, boxNode.w, boxNode.height)
		}
	}

	e.zIndex = scope.prevZ
	e.zIndexSet = scope.prevZSet
	e.positioned = scope.prevPositioned
	e.blendMode = scope.prevBlendMode
	e.blendGroup = scope.prevGroup
	e.blendGroupOwner = scope.prevGroupOwner
}

// Layout renders the document into a display list.
func Layout(root *html.Node, opts Options) (*Result, error) {
	return LayoutContext(context.Background(), root, opts)
}

// LayoutContext renders the document into a display list and observes ctx at
// style-pass and recursive tree-construction checkpoints. Layout remains the
// compatibility entry point for callers that do not need cancellation.
func LayoutContext(ctx context.Context, //nolint:revive // legacy Layout adapter
	root *html.Node, opts Options,
) (*Result, error) {
	return layoutContext(ctx, root, opts, nil)
}

// WithWorkspace is the sequential internal layout form that
// borrows display-list storage from workspace. Call workspace.Release after
// Paint and metadata projection, before the next layout using it.
func WithWorkspace(ctx context.Context, root *html.Node, opts Options, workspace *Workspace) (*Result, error) {
	return layoutContext(ctx, root, opts, workspace)
}

func layoutContext(
	ctx context.Context,
	root *html.Node, opts Options, workspace *Workspace,
) (*Result, error) {
	return layoutContextWithStyles(ctx, root, opts, workspace, nil)
}

// censusOps records the right edge of fill/stroke/image ops and whether any
// link URI is a same-document fragment. Width is the floor so MaxContentX is
// never zero after a real Layout (zero stays the hand-built Result signal).
func censusOps(ops []Op, width float64) (float64, bool) {
	maxX := width
	hasFrag := false

	for idx := range ops {
		switch ops[idx].Kind {
		case OpFillRect, OpStrokeRect, OpImage, OpPath:
			if ext := ops[idx].X + ops[idx].W; ext > maxX {
				maxX = ext
			}
		case OpLinkURI:
			if !hasFrag && strings.HasPrefix(ops[idx].URI, "#") {
				hasFrag = true
			}
		case OpLine, OpGridRun, OpText, OpBullet, OpUnknown, opKindNoop:
			continue
		}
	}

	return maxX, hasFrag
}

//nolint:wsl // container remount gates are intentionally kept in lifecycle order.
func resolveStylesForLayoutContext(
	ctx context.Context, root *html.Node, opts Options,
) (map[*html.Node]*ResolvedStyle, map[*html.Node]sizeContainer, error) {
	// Pass 1: cascade without @container (used sizes unknown).
	styles, err := resolveStylesWithContext(ctx, root, opts, nil)
	if err != nil {
		return nil, nil, err
	}

	if !css.HasContainerRules(opts.Sheets) {
		return styles, nil, nil
	}
	// After definite inline sizes of size containers are known, re-cascade so
	// matching @container rules apply, then lay out once with final styles.
	cinfo, err := measureSizeContainersContext(ctx, root, styles, opts.Width)
	if err != nil {
		return nil, nil, err
	}
	if len(cinfo) == 0 {
		return styles, nil, nil
	}

	styles, err = resolveStylesWithContext(ctx, root, opts, cinfo)
	if err != nil {
		return nil, nil, err
	}

	// One nested remount: @container may change nested container-type.
	cinfo2, err := measureSizeContainersContext(ctx, root, styles, opts.Width)
	if err != nil {
		return nil, nil, err
	}
	if len(cinfo2) != len(cinfo) || !sameSizeContainers(cinfo, cinfo2) {
		styles, err = resolveStylesWithContext(ctx, root, opts, cinfo2)
		if err != nil {
			return nil, nil, err
		}

		return styles, cinfo2, nil
	}

	return styles, cinfo, nil
}

// sameSizeContainers reports whether two container measurements agree for
// every node (a changed used inline size / font size forces another pass).
func sameSizeContainers(a, b map[*html.Node]sizeContainer) bool {
	for n, sa := range a {
		sb, ok := b[n]
		if !ok || !sameSizeContainerState(sa, sb) {
			return false
		}
	}

	return true
}

func (e *engine) stylePtr(node *html.Node) *ResolvedStyle {
	for _, override := range slices.Backward(e.styleOverrides) {
		if override.node == node {
			return override.style
		}
	}

	if p := e.syntheticStyles[node]; p != nil {
		return p
	}

	styles := e.styles
	if p := styles[node]; p != nil {
		return p
	}

	// Missing node (should not happen for walked trees): stable empty style.
	return &zeroResolvedStyle
}

// hasStyle reports whether node has a resolved, overridden, or synthetic
// style. stylePtr returns the shared zero style when it does not.
func (e *engine) hasStyle(node *html.Node) bool {
	return e.stylePtr(node) != &zeroResolvedStyle
}

// setSyntheticStyle records the used style of an engine-generated anonymous
// node (flex/multicol item wrappers). Synthetic styles live beside the
// immutable cascade map so every reader observes them through stylePtr.
func (e *engine) setSyntheticStyle(node *html.Node, style *ResolvedStyle) {
	if e.syntheticStyles == nil {
		e.syntheticStyles = make(map[*html.Node]*ResolvedStyle)
	}

	e.syntheticStyles[node] = style
}

// styleVal returns a by-value ResolvedStyle for APIs that still take values.
// Prefer field access on stylePtr to avoid the copy when possible.
func (e *engine) styleVal(node *html.Node) ResolvedStyle {
	return *e.stylePtr(node)
}

// HorizChrome returns unscaled horizontal padding plus border width.
func (style *ResolvedStyle) HorizChrome() float64 {
	if style == nil {
		return 0
	}

	return style.PaddingLeft + style.PaddingRight + style.BorderLeft.Width + style.BorderRight.Width
}

// horizontalChrome returns the scaled horizontal padding and border width for
// sizing helpers.
func (style *ResolvedStyle) horizontalChrome(eng *engine) float64 {
	return eng.scalePt(style.PaddingLeft) + eng.scalePt(style.PaddingRight) +
		eng.scalePt(style.BorderLeft.Width) + eng.scalePt(style.BorderRight.Width)
}

// verticalChrome returns the scaled vertical padding and border width for
// sizing helpers.
func (style *ResolvedStyle) verticalChrome(eng *engine) float64 {
	return eng.scalePt(style.PaddingTop) + eng.scalePt(style.PaddingBottom) +
		eng.scalePt(style.BorderTop.Width) + eng.scalePt(style.BorderBottom.Width)
}

// buildWithStyle builds node with an engine-local style override. The override
// remains live through the complete recursive build, so boxes created for node
// can safely retain its pointer while descendants continue to read their own
// resolved styles from e.styles.
func (e *engine) buildWithStyle(
	node *html.Node, override *ResolvedStyle, availW, posX, posY float64,
) *box {
	if override == nil {
		return e.build(node, availW, posX, posY)
	}

	e.styleOverrides = append(e.styleOverrides, styleOverride{node: node, style: override})
	defer e.popStyleOverride()

	return e.build(node, availW, posX, posY)
}

func (e *engine) popStyleOverride() {
	e.styleOverrides = e.styleOverrides[:len(e.styleOverrides)-1]
}

// popStyleOverrides removes the last n transient style overrides pushed by one
// caller (root margin absorption pushes a variable-length chain).
func (e *engine) popStyleOverrides(n int) {
	if n <= 0 {
		return
	}

	e.styleOverrides = e.styleOverrides[:len(e.styleOverrides)-n]
}

func flattenBoxes(boxNode *box, out *[]*box) {
	if boxNode == nil {
		return
	}

	*out = append(*out, boxNode)
	for _, child := range boxNode.children {
		flattenBoxes(child, out)
	}
}

// minOpCapacity floors the preallocated op display list.
const minOpCapacity = 64

func estimateOpCapacity(root *html.Node) int {
	if root == nil {
		return 0
	}

	nodes, cells := 0, 0

	root.Walk(func(node *html.Node) {
		nodes++

		if node.Type == html.ElementNode && (node.Name == "td" || node.Name == "th") {
			cells++
		}
	})

	// Collapsed table grids batch a row's border lines into one OpGridRun, so
	// the old 3/2 ops-per-node bound over-reserved by about two entries per
	// cell (the grid lines a cell used to contribute). Subtract that bound;
	// append growth still covers documents that emit more than the estimate.
	const gridLinesPerCell = 2

	capacity := nodes*three/two - gridLinesPerCell*cells
	if capacity < minOpCapacity {
		capacity = minOpCapacity
	}

	const maxCapacity = 1 << 20
	if capacity > maxCapacity {
		capacity = maxCapacity
	}

	return capacity
}

// box is one laid-out box.
type box struct {
	node *html.Node
	// style points at the shared cascade ResolvedStyle (engine.styles).
	// Using a pointer avoids embedding ~1.3KB of style into every box — table
	// cells dominate box count on multi-page reports.
	style     *ResolvedStyle
	x, y      float64 // border-box top-left
	w, height float64 // border-box size
	// outlineInflate is the scaled distance from the border box to the
	// outline stroke centerline (0 when no outline paints). Stamped where
	// outline ops are emitted so paint-time ownership checks need no engine
	// scale (opOwnedBy).
	outlineInflate float64
	kind           boxKind
	// packed flags — keep together to avoid padding.
	hasInk          bool // cell has non-whitespace ink (see nodeHasTableInk)
	sticky          bool
	stickyTopSet    bool
	stickyRightSet  bool
	stickyBottomSet bool
	stickyLeftSet   bool
	// opStart/opEnd bound the inclusive range of e.ops indices that this
	// box's subtree emitted. opEnd < opStart means the box emitted nothing
	// (e.g. boxes built during a noEmit measure pass).
	opStart, opEnd int
	// overflowClip is the accumulated ancestor overflow padding box in
	// canvas points recorded by clipOverflowTree. A host painting this
	// element's ops through a transform intersects the paint with it,
	// because geometry-baked clipping moves with the matrix. Nil when no
	// ancestor clips.
	overflowClip *clipRect
	// Gap-L 3D contracts. perspDist/hasPerspDist carry the inherited
	// perspective camera distance for stamp time: the perspective
	// workstream fills them in a pre-pass over the box tree (nearest
	// ancestor with the perspective property wins) and boxTransformAccum
	// reads them when projecting 3D state. perspCX/perspCY carry the
	// vanishing point computed from that ancestor via perspectiveCenter.
	// preserveComposed is set by the transform-style workstream when a
	// preserve-3d chain already composed this box's 3D state, telling
	// boxTransformAccum to skip its own 3D branch and keep the 2D path.
	// All default zero/false (no 3D).
	perspDist        float64
	hasPerspDist     bool
	perspCX          float64
	perspCY          float64
	preserveComposed bool
	children         []*box
	firstBaseline    float64
	// table cells
	col, span int
	row       int // owning table row index, set once at placement
	rowSpan   int // vertical span (default 1) for <td rowspan>
	// rowBoxH is the height of the cell's starting row only. For rowspan>1,
	// h covers the full span (background/borders) while rowBoxH is what
	// rowsIntact uses so bottom-edge paint ops do not make the first row
	// look multi-page.
	rowBoxH    float64
	contentW   float64 // max-content border-box (preferred column contribution)
	contentMin float64 // min-content border-box (shrink floor)
	contentH   float64
	// rows[i] holds the cell boxes of table row i, in document order. The
	// row's op range is from rows[i][0].opStart to rows[i][len-1].opEnd.
	rows [][]*box
	// rowTops[i] and rowHeights[i] are row i's canvas top and border-box
	// height after rowspan growth. PageBreaks exports each row as a
	// keep-together range so a row never straddles a page boundary.
	rowTops    []float64
	rowHeights []float64
	// headerRows is the number of leading rows that came from <thead> /
	// table-header-group (for repeating headers across pages).
	headerRows int
	// sticky: print-scoped position:sticky (see sticky.go). Insets are scaled
	// points. stickyPort is carried through cloneBoxGraph; the page-renderer
	// clamp that consumed it is gone.
	stickyID                 int
	stickyTop, stickyRight   float64
	stickyBottom, stickyLeft float64
	stickyPort               *box
	// replaced image (nil when missing/failed); shared decode via resolveImage.
	img *imageRef
}

func (e *engine) build(node *html.Node, availW, posX, posY float64) *box {
	if node == nil {
		return nil
	}

	if e.checkContext() {
		return nil
	}

	if e.buildDepth >= maxBuildDepth {
		e.err = errLayoutDepthExceeded

		return nil
	}

	if node.Type == html.TextNode {
		return nil
	}

	e.buildDepth++
	defer func() { e.buildDepth-- }()

	sty := e.styleVal(node)

	if sty.Display == cssDisplayNone {
		return nil
	}

	// Measure builds must not record floats in the live BFC (see
	// beginMeasureFloats). Pair with endMeasureFloats before returning.
	measure := e.noEmit
	if measure {
		e.beginMeasureFloats()
	}

	scope := e.pushZ(sty, e.stylePtr(node))
	// Ancestor transforms only (own transform does not change this box's CB).
	underXformCB := e.transformCBDepth > 0
	start := len(e.ops)
	// verticalWritingHeight reads the widest rotated run in this box's
	// subtree. Reset the accumulator for the subtree and merge it back into
	// the parent scope after the build so sibling content cannot leak in.
	previousRotatedRunW := e.maxRotatedRunW
	e.maxRotatedRunW = 0

	boxNode := e.buildDisplayBox(node, sty, availW, posX, posY, underXformCB)

	if boxNode != nil {
		boxNode.opStart, boxNode.opEnd = start, len(e.ops)-1
		e.finishBuiltBox(boxNode, sty, underXformCB)
	}

	if e.maxRotatedRunW < previousRotatedRunW {
		e.maxRotatedRunW = previousRotatedRunW
	}

	e.popZ(scope, boxNode)

	if measure {
		e.endMeasureFloats()
	}

	return boxNode
}

// buildDisplayBox runs the display dispatch for build: replaced elements,
// out-of-flow positioning, then the in-flow formatting context. The
// transform containing-block depth is owned here so descendants built by the
// in-flow branch see this box exactly as build did before the extraction.
func (e *engine) buildDisplayBox(
	node *html.Node, sty ResolvedStyle, availW, posX, posY float64, underXformCB bool,
) *box {
	var boxNode *box

	switch node.Name {
	case cssTagImg:
		boxNode = e.buildImage(node, sty, posX, posY, true)
	case cssTagSVG:
		boxNode = e.buildInlineSVG(node, sty, posX, posY, true)
	case "hr":
		boxNode = e.buildHR(node, sty, availW, posX, posY)
	}
	// Out-of-flow positioning wraps the display type so fixed/absolute flex
	// and grid containers still get the right formatting context.
	// Transformed ancestors establish a CB for fixed (treated as absolute).
	if boxNode == nil {
		boxNode = e.buildOutOfFlowIfPositioned(node, sty, availW, posX, posY, underXformCB)
	}
	// Descendants of a transformed box see this as a containing block.
	if sty.HasTransform {
		e.transformCBDepth++
		defer func() { e.transformCBDepth-- }()
	}

	if boxNode == nil {
		boxNode = e.buildInFlowDisplay(node, sty, availW, posX, posY)
	}

	return boxNode
}

// finishBuiltBox applies position post-processing to a built box: relative
// offset, sticky tagging, and the viewport-fixed op stamp.
func (e *engine) finishBuiltBox(boxNode *box, sty ResolvedStyle, underXformCB bool) {
	if sty.Position == positionRelative {
		e.applyRelativeOffset(boxNode)
	}

	if sty.Position == positionSticky {
		e.tagSticky(boxNode)
	}

	if sty.Position == positionFixed && !underXformCB {
		// Only viewport-fixed when not under a transformed ancestor CB.
		e.markOpsFixed(boxNode.opStart, boxNode.opEnd)
	}
}

// buildOutOfFlowIfPositioned wraps fixed/absolute boxes in an out-of-flow
// formatting context (fixed under a transformed ancestor is absolute).
func (e *engine) buildOutOfFlowIfPositioned(
	node *html.Node, sty ResolvedStyle, availW, posX, posY float64, underXformCB bool,
) *box {
	if sty.Position == positionFixed {
		return e.buildOutOfFlow(node, sty, availW, posX, posY, !underXformCB)
	}

	if sty.Position == positionAbsolute {
		return e.buildOutOfFlow(node, sty, availW, posX, posY, false)
	}

	return nil
}

func isTableDisplay(d string) bool {
	switch d {
	case displayTable, displayTableRow, displayRowGroup, displayHeaderGroup,
		displayFooterGroup, displayTableCell, displayTableCaption:
		return true
	}

	return false
}

// useBlockForTableDisplay reports elements that set display:table only for
// shrink-to-fit (wiki figure thumbs) rather than real tabular structure.
func useBlockForTableDisplay(node *html.Node) bool {
	if node == nil || node.Type != html.ElementNode {
		return false
	}

	if node.Name == displayTable {
		return false
	}
	// Real tables nest tr/tbody/thead/tfoot/colgroup; figures nest a/img/figcaption.
	for _, c := range node.Children {
		if c.Type != html.ElementNode {
			continue
		}

		switch c.Name {
		case "tr", htmlTbody, htmlThead, htmlTfoot, htmlColgroup, "col", htmlCaption:
			return false
		}
	}

	return true
}

// buildBlock lays out a block-level box.
//
//nolint:cyclop // block layout owns ordered CSS flow phases
func (e *engine) buildBlock(node *html.Node, style ResolvedStyle, availW, posX, posY float64) *box {
	boxNode := &box{ //nolint:exhaustruct // intentional zero fields
		node: node, style: e.stylePtr(node), kind: boxKindBlock, x: posX, y: posY,
	}
	boxStyle := &style
	w, margL := resolveBlockWidth(e, node, boxStyle, availW)
	boxNode.w = w

	boxNode.x = posX + margL
	contentX, contentW := e.contentBox(boxNode.x, boxNode.w, boxStyle)

	// Content ops are recorded first so we know the box height; background
	// and borders are then inserted *before* those ops so paint order is
	// bg → borders → children (otherwise fills cover text).
	contentStart := len(e.ops)

	curY := e.scalePt(boxStyle.PaddingTop) + e.scalePt(borderLayoutWidth(boxStyle, boxStyle.BorderTop))
	enclose := e.pushBFCFloatsForce(style, contentX, contentW, e.isFlexItemNode(node))
	widget := node.Name == htmlMeter || node.Name == "progress"
	chkWidget := isInputCheckbox(node)
	children := blockFlowChildren(node, widget || chkWidget)

	if chkWidget {
		curY = applyCheckboxAutoSize(e, style, boxNode, curY)
	}

	previousCB, _ := e.setFlowCB(style)
	// The synthetic document box has an auto height, so setFlowCB drops the
	// viewport. The html element's containing block is that viewport.
	if node.Name == "#document" && e.flowCBHeight == nil {
		e.seedInitialContainingBlock()
	}

	// Root body: place the body and its first-child margin chain on the
	// collapsed top margin before the children flow, so descendants,
	// positioned boxes, and floats all share the final edge.
	posY, marginOverrides := e.absorbRootBodyTopMargin(node, boxNode, style, posY)

	curY = e.flowChildren(boxNode, children, style, contentW, contentX, posY, curY)
	e.popStyleOverrides(marginOverrides)
	e.flowCBHeight = previousCB
	curY = e.autoSizedControlContentBottom(node, boxStyle, widget, curY)

	if enclose && e.bfcFloats != nil {
		curY = e.bfcFloats.extentCy(posY, curY)
	}

	e.popBFCFloats(enclose)

	curY = e.applyRootBoxMargins(node, boxNode, curY)

	if isVerticalWritingMode(style.WritingMode) && style.Height < 0 && style.HeightPercent < 0 {
		curY = e.verticalWritingHeight(curY, style)
	}

	// list marker (outside the principal box content — in the marker area)
	if node.Name == "li" && boxNode.firstBaseline > 0 {
		e.emitListMarker(node, style, contentX, boxNode.firstBaseline)
	}

	// Bottom padding and border are inside the border box (space above the
	// bottom border / letterhead rules - fixture-07/16). The shared resolver
	// adds them once for every formatting context and then applies
	// height/min-height/max-height.
	resolvedHeightStyle := withAspectRatioHeight(style, contentW, e)
	boxNode.height = e.applyHeightConstraintsWithCB(&resolvedHeightStyle,
		e.borderBoxBottom(resolvedHeightStyle, curY), e.containingBlockHeight())
	e.paintWidgetControl(node, style, boxNode, widget, chkWidget, posY)

	e.paintPositionedPseudo(node, style, boxNode, pseudoBefore)
	e.paintPositionedPseudo(node, style, boxNode, pseudoAfter)

	e.prependChrome(contentStart, boxNode, style, boxNode.x, boxNode.y, boxNode.w, boxNode.height)

	return boxNode
}

// paintPositionedPseudo paints generated content whose used position takes it
// out of the host's normal inline flow. This covers block and flex-item hosts
// such as diagram cards; inline hosts continue through collectInlineSpan.
func (e *engine) paintPositionedPseudo( //nolint:cyclop
	node *html.Node, host ResolvedStyle, boxNode *box, pseudoElem string,
) {
	// No content rule anywhere means the probe below answers empty.
	if !e.hasPseudoContent() {
		return
	}

	text := e.pseudoContent(node, pseudoElem)
	if text == "" || boxNode == nil {
		return
	}

	style := e.pseudoStyle(node, pseudoElem, host)
	if style.Position != positionAbsolute && style.Position != positionFixed {
		return
	}

	face := e.faceFor(style)
	size := style.FontSize * e.scale

	if face == nil || size <= 0 {
		return
	}

	contentX, contentW := e.contentBox(boxNode.x, boxNode.w, &host)
	pseudoX := contentX + e.scalePt(style.MarginLeft)

	if !style.LeftAuto {
		pseudoX = contentX + e.scalePt(style.Left)
	} else if !style.RightAuto {
		pseudoX = contentX + contentW - e.measureTextFace(text, style) - e.scalePt(style.Right)
	}

	staticAfter := pseudoElem == pseudoAfter && style.TopAuto && style.BottomAuto
	pseudoY := boxNode.y + e.scalePt(host.PaddingTop) + e.scalePt(host.BorderTop.Width) +
		e.scalePt(style.MarginTop)

	if staticAfter {
		// An absolutely positioned ::after without an inset uses the static
		// position after its block host, not the host's content origin. This is
		// the baseline position used by diagram connectors between stacked cards.
		pseudoY = boxNode.y + boxNode.height + e.scalePt(style.MarginTop)
	}

	if !style.TopAuto {
		pseudoY = boxNode.y + e.scalePt(style.Top)
	} else if !style.BottomAuto {
		pseudoY = boxNode.y + boxNode.height - e.scalePt(style.Bottom)
	}

	baseline := pseudoY + e.fontAscent(size)

	if staticAfter {
		baseline = pseudoY
	}

	scope := e.pushZ(*style, style)
	e.add(Op{ //nolint:exhaustruct // generated pseudo text has no DOM box
		Kind: OpText, X: pseudoX, Y: baseline, W: e.measureTextFace(text, style),
		H: style.LineHeight * e.scale, Text: text, Font: face, Size: size,
		InkDescent: e.fontDescentFace(face, size),
		R:          style.Color[0], G: style.Color[1], B: style.Color[2],
		Bold: style.FontWeight >= fontWeightBoldValue,
	})
	e.popZ(scope, nil)
}

func (e *engine) paintValueWidget(node *html.Node, style ResolvedStyle, leftX, topY, width, height float64) {
	minValue, maxValue := 0.0, 1.0
	if node.Name == htmlMeter {
		minValue = widgetNumber(node.Attribute("min"), 0)
		maxValue = widgetNumber(node.Attribute("max"), 1)
	}

	if maxValue <= minValue {
		return
	}

	value := widgetNumber(node.Attribute("value"), minValue)

	ratio := (value - minValue) / (maxValue - minValue)
	if ratio < 0 {
		ratio = 0
	} else if ratio > 1 {
		ratio = 1
	}

	contentX, contentW := e.contentBox(leftX, width, &style)
	contentY := topY + e.scalePt(style.BorderTop.Width) + e.scalePt(style.PaddingTop)

	contentH := height - e.scalePt(style.BorderTop.Width+style.BorderBottom.Width) -
		e.scalePt(style.PaddingTop+style.PaddingBottom)
	if contentW <= 0 || contentH <= 0 || ratio <= 0 {
		return
	}

	// Native progress/meter fill: CSS accent-color, then an authored
	// background, then the widget default. Custom properties participate
	// only after they resolve into those properties (e.g. accent-color:
	// var(--token)); inherited document tokens are not scanned here.
	color := widgetValueColor(node.Name, style)

	const indicatorRatio = 0.3

	indicatorH := e.scalePt(style.FontSize) * indicatorRatio
	if indicatorH <= 0 || indicatorH > contentH {
		indicatorH = contentH
	}

	indicatorY := contentY + (contentH-indicatorH)/two

	e.add(Op{ //nolint:exhaustruct // intentional zero fields
		Kind: OpFillRect, X: contentX, Y: indicatorY, W: contentW * ratio, H: indicatorH,
		R: color[0], G: color[1], B: color[2], Alpha: 1,
		Radius: usedBorderRadius(style, contentW*ratio, indicatorH),
	})
}

func widgetValueColor(tag string, style ResolvedStyle) [3]float64 {
	if style.AccentColorSet {
		return style.AccentColor
	}

	if tag == htmlMeter {
		return style.Color
	}

	if tag == htmlInput {
		return [3]float64{0, 0.46, 1}
	}

	return [3]float64{0, 0.5, 0}
}

func widgetNumber(raw string, fallback float64) float64 {
	if value, err := strconv.ParseFloat(raw, 64); err == nil {
		return value
	}

	return fallback
}

// closedDetailsChildren implements the print-time disclosure rule: a closed
// details element paints its summary but does not lay out the hidden payload.
func closedDetailsChildren(node *html.Node) []*html.Node {
	for _, child := range node.Children {
		if child.Type == html.ElementNode && child.Name == "summary" {
			return []*html.Node{child}
		}
	}

	return nil
}

// blockFlowChildren returns the children a block should flow: fallback text is
// dropped for native controls (meter, progress, checkbox, radio), and a closed
// details element collapses to its summary.
func blockFlowChildren(node *html.Node, nativeControl bool) []*html.Node {
	children := node.Children
	if nativeControl {
		children = nil // fallback text is replaced by the native-style control
	}

	if node.Name == "details" {
		_, open := node.Attrs["open"]
		if !open {
			children = closedDetailsChildren(node)
		}
	}

	return children
}

// applyHeightConstraints enforces the used/min/max-height constraints on the
// current content height (extracted so buildBlock stays readable).
// cbH is the containing-block height for % heights: <0 means indefinite (auto).
func (e *engine) applyHeightConstraints(style ResolvedStyle, curY float64) float64 {
	return e.applyHeightConstraintsWithCB(&style, curY, -1)
}

func (e *engine) clampBlockMaxHeight(style *ResolvedStyle, curY, cbH, vChrome float64) float64 {
	if style.MaxHeightPercent >= 0 && cbH >= 0 && cbH < 1e12 {
		maxH := cbH * style.MaxHeightPercent / oneHundred
		if style.BoxSizing != borderBox {
			maxH += vChrome
		}

		if curY > maxH {
			return maxH
		}

		return curY
	}

	if style.MaxHeight >= 0 {
		maxHeight := e.scalePt(style.MaxHeight)
		if style.BoxSizing != borderBox {
			maxHeight += vChrome
		}

		if curY > maxHeight {
			return maxHeight
		}
	}

	return curY
}

func (e *engine) calcMinHeight(style *ResolvedStyle, cbH, vChrome float64) float64 {
	if style.MinHeightPercent >= 0 && cbH >= 0 && cbH < 1e12 {
		minH := cbH * style.MinHeightPercent / oneHundred
		if style.BoxSizing != borderBox {
			minH += vChrome
		}

		if minH < 0 {
			return 0
		}

		return minH
	}

	minHeight := e.scalePt(style.MinHeight)
	if style.BoxSizing != borderBox {
		minHeight += vChrome
	}

	if minHeight < 0 {
		return 0
	}

	return minHeight
}

func (e *engine) clampBlockMinHeight(style *ResolvedStyle, curY, cbH, vChrome float64) float64 {
	minH := e.calcMinHeight(style, cbH, vChrome)
	if minH > 0 && curY < minH {
		return minH
	}

	return curY
}

// borderBoxBottom adds a box's auto bottom chrome (padding-bottom and
// border-bottom) to a content-flow bottom. Every formatting context calls it
// so an auto-height bordered box is the same height whether it is a block,
// flex container, multicol container, grid, table, or measured cell. The
// border-image device width is used when the border paints as an image.
func (e *engine) borderBoxBottom(style ResolvedStyle, contentBottom float64) float64 {
	boxStyle := &style
	contentBottom += e.scalePt(boxStyle.PaddingBottom)
	contentBottom += e.scalePt(borderLayoutWidth(boxStyle, boxStyle.BorderBottom))

	return contentBottom
}

// applyHeightConstraintsWithCB is the definite-CB form for min/max percent.
// A definite height (length or resolvable percentage) sets the used height
// outright: taller content overflows visibly instead of growing the box
// (CSS 2.1 §10.6.3; Chrome 143 on cases 34 and 40).
func (e *engine) applyHeightConstraintsWithCB(style *ResolvedStyle, curY float64, cbH float64) float64 {
	if h, ok := resolveUsedHeight(style, cbH, e); ok {
		curY = h
	}

	vChrome := 0.0
	if style.BoxSizing != borderBox {
		vChrome = style.verticalChrome(e)
	}

	curY = e.clampBlockMaxHeight(style, curY, cbH, vChrome)

	return e.clampBlockMinHeight(style, curY, cbH, vChrome)
}

// resolveUsedHeight returns a definite border-box height when the style has a
// usable height. HeightPercent requires a definite containing-block height
// (cbH >= 0); otherwise the percentage is treated as auto (cyclic honesty).
func resolveUsedHeight(sty *ResolvedStyle, cbH float64, engN *engine) (float64, bool) {
	if sty.HeightPercent >= 0 {
		if cbH < 0 {
			return 0, false
		}

		height := cbH * sty.HeightPercent / oneHundred
		if sty.BoxSizing != borderBox {
			height += sty.verticalChrome(engN)
		}

		return height, true
	}

	if sty.Height < 0 {
		return 0, false
	}

	height := engN.scalePt(sty.Height)
	if sty.BoxSizing != borderBox {
		height += sty.verticalChrome(engN)
	}

	return height, true
}

// buildOutOfFlow places absolute (viewportFixed=false) or fixed
// (viewportFixed=true) boxes. Fixed under a transformed ancestor is absolute
// (caller passes viewportFixed=false). Fixed ops are marked by build().
func (e *engine) buildOutOfFlow(node *html.Node, sty ResolvedStyle, availW, x, y float64, viewportFixed bool) *box {
	cbX, cbY, cbW := x, y, availW
	if viewportFixed {
		cbX, cbY = 0, 0

		cbW = e.opts.Width
		if cbW <= 0 {
			cbW = availW
		}
	}

	start := len(e.ops)
	chromeFrom := len(e.deferredChrome)

	cbH := e.absCBHeights[node]

	// CSS 2.1 §10.6.4: an absolutely positioned box with auto height and
	// both top and bottom set stretches to fill the containing block (see
	// absStretchUsedHeight). The synthesized height feeds the build below.
	if stretch, stretched := e.absStretchUsedHeight(sty, cbH, viewportFixed); stretched {
		sty.Height = stretch
	}

	buildW := e.absoluteBuildWidth(node, sty, cbW)

	boxNode := e.buildInFlowDisplay(node, sty, buildW, cbX, cbY)
	if boxNode == nil {
		return nil
	}

	boxNode.opStart, boxNode.opEnd = start, len(e.ops)-1

	absX := cbX
	if !sty.LeftAuto {
		absX = cbX + e.scalePt(sty.Left)
	} else if !sty.RightAuto {
		absX = cbX + cbW - boxNode.w - e.scalePt(sty.Right)
	}

	absY := e.resolveAbsY(sty, boxNode, cbY, viewportFixed, cbH)

	dx, dy := absX-boxNode.x, absY-boxNode.y
	boxNode.x, boxNode.y = absX, absY
	e.shiftOutOfFlowBox(boxNode, dx, dy, chromeFrom)

	return boxNode
}

// absStretchUsedHeight synthesizes the used height for an abspos box with
// auto height and both top and bottom set (CSS 2.1 §10.6.4). Backgrounds,
// borders, and content lay out at the stretched size; without it a top:0;
// bottom:0 overlay (such as a sheen sweep) collapses to zero height and its
// animation paints nothing. Fixed boxes resolve against the viewport in
// resolveAbsY, so only the deferred content height stretches here. It
// reports the height in style units, or false when the box does not stretch.
// Split from buildOutOfFlow so the placement sequence stays within the
// complexity budget.
func (e *engine) absStretchUsedHeight(sty ResolvedStyle, cbH float64, viewportFixed bool) (float64, bool) {
	if viewportFixed || sty.Height >= 0 || sty.HeightPercent >= 0 || sty.TopAuto || sty.BottomAuto || cbH <= 0 {
		return 0, false
	}

	stretch := cbH - e.scalePt(sty.Top) - e.scalePt(sty.Bottom) - e.scalePt(sty.MarginTop) - e.scalePt(sty.MarginBottom)
	if stretch < 0 {
		stretch = 0
	}

	if sty.BoxSizing == borderBox {
		stretch -= sty.verticalChrome(e)
		if stretch < 0 {
			stretch = 0
		}
	}

	return stretch / e.scale, true
}

// absoluteBuildWidth is the width available while building an abspos/fixed box.
// Specified width uses the containing block as the build ceiling. When left and
// right are both set, width fills the remaining space. Otherwise width is
// shrink-to-fit capped by the remaining CB space (CSS 2.1 §10.3.7).
func (e *engine) absoluteBuildWidth(node *html.Node, sty ResolvedStyle, cbW float64) float64 {
	if sty.Width >= 0 || sty.WidthPercent >= 0 {
		return cbW
	}

	if !sty.LeftAuto && !sty.RightAuto {
		buildW := cbW - e.scalePt(sty.Left+sty.Right)
		if buildW < 0 {
			return 0
		}

		return buildW
	}

	avail := cbW
	if !sty.LeftAuto {
		avail -= e.scalePt(sty.Left)
	}

	if !sty.RightAuto {
		avail -= e.scalePt(sty.Right)
	}

	if avail < 0 {
		avail = 0
	}

	// Shrink-to-fit resolves against the remaining space, then CSS 2.1 §10.4
	// clamps it by min/max-width. Without the clamp a min-width badge
	// shrink-wraps to its text and paints as an oval instead of a circle.
	return clampBlockMinMax(e, &sty, cbW, e.floatIntrinsicAvail(node, sty, avail))
}

// resolveAbsY places the out-of-flow box vertically: top wins, then bottom
// (fixed resolves against the viewport bottom; absolute uses the containing
// block's bottom when its deferred height is known).
//
//nolint:wsl // ordered absolute-positioning cases mirror CSS precedence
func (e *engine) resolveAbsY(sty ResolvedStyle, boxNode *box, cbY float64, viewportFixed bool, cbH float64) float64 {
	if !sty.TopAuto {
		return cbY + e.scalePt(sty.Top)
	}

	if sty.BottomAuto {
		return cbY
	}

	if viewportFixed {
		absY := e.opts.Height - boxNode.height - e.scalePt(sty.Bottom)
		if absY < 0 {
			return e.scalePt(sty.Bottom)
		}

		return absY
	}
	if cbH > 0 {
		return cbY + cbH - boxNode.height - e.scalePt(sty.Bottom)
	}

	return cbY + e.scalePt(sty.Bottom)
}

// buildInFlowDisplay builds flex/grid/multicol/table/block ignoring position.
// Single display dispatch shared with build() so abspos/fixed get the same
// formatting context (including table display:table-as-block heuristic).
func (e *engine) buildInFlowDisplay(nodeN *html.Node, sty ResolvedStyle, availW, posX, posY float64) *box {
	if sty.Display == displayFlex || sty.Display == displayInlineFlex {
		return e.buildFlex(nodeN, sty, availW, posX, posY)
	}

	if sty.Display == displayGrid || sty.Display == displayInlineGrid || sty.Display == displaySubgrid {
		return e.buildGrid(nodeN, sty, availW, posX, posY)
	}

	if isMulticol(sty) {
		return e.buildMulticol(nodeN, sty, availW, posX, posY)
	}

	if isTableDisplay(sty.Display) {
		// MediaWiki thumbs use figure{display:table;float:right} purely for
		// shrink-to-fit. Routing those through buildTable drops nested <img>
		// (no table-row/cell structure) and leaves empty floated boxes that
		// clear following paragraphs with huge gaps. Treat non-table hosts
		// without table-structure children as ordinary blocks.
		if useBlockForTableDisplay(nodeN) {
			return e.buildBlock(nodeN, sty, availW, posX, posY)
		}

		return e.buildTable(nodeN, sty, availW, posX, posY)
	}

	return e.buildBlock(nodeN, sty, availW, posX, posY)
}
