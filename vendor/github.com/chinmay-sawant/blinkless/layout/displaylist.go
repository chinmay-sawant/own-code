package layout

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/chinmay-sawant/blinkless/css"
	icss "github.com/chinmay-sawant/blinkless/internal/css"
	pdf "github.com/chinmay-sawant/blinkless/internal/fonts"
	ilayout "github.com/chinmay-sawant/blinkless/internal/layout"
	"github.com/chinmay-sawant/blinkless/internal/pubstate"
)

// cssPxToPt matches css.Apply and the old bitmap viewport: 1 CSS pixel is
// 0.75 points at 96 dpi.
const cssPxToPt = 72.0 / 96.0

// fallbackViewportPx is the bitmap path's width when the caller left the
// viewport unset. Percentage heights then use that same width as their
// containing block, which is the fallback the bitmap renderer used.
const fallbackViewportPx = 1024

// printFitMinOverflowPt is the smallest content overflow (in points) that
// triggers the print width fit, the same threshold the native PDF path uses
// for its smart shrink. A hair of overflow (a fractional pixel) is noise.
const printFitMinOverflowPt = 0.1

// DisplayOp is one display-list operation. It is the internal Op under an
// exported name so a caller outside this module can read the retained
// display list that layout produces and replay it onto its own canvas
// instead of consuming a flattened bitmap.
//
// Coordinates are in canvas points, y down, and for DisplayOpText and
// DisplayOpBullet Y is the baseline. Multiply by Display.PixelPerPoint to
// reach CSS pixels.
//
// Two kinds carry no paint and must be skipped: DisplayOpNoop, which the
// clipper leaves behind when it deactivates an operation, and DisplayOpUnknown,
// which is the boundary marker for a mask, blend, or isolation group. Treat any kind
// outside the list below as inert, so a kind added later cannot be mistaken for
// a fill.
//
// Use the accessor methods (LinkURI, ImageBytes, ImageAlt, Transform,
// ProjectiveTransformValue,
// BlendModeName, Opacity, Outline, IsCanvasBackground, FontFeatures, TextLanguage, TextAutospace,
// TextTransformValue, NoFakeBoldValue, PathValue, CornerPathValue, GlyphBasisValue) for the
// rare payload rather than the promoted fields: the payload sits behind an embedded pointer to an
// unexported type, so a caller cannot build or inspect it, and every accessor
// answers safely when it is absent. The plain fields (Kind, X, Y, W, H, R, G,
// B, Alpha, Width, Size, LetterSpacing, Text, Font, Grid, the radii,
// StrokeMask, LineInset, RotateDeg, Bold, FakeOblique, IsJPEG, IsBackground,
// Fixed, Pinned, Positioned, StickyID, ZIndex, ZIndexSet, XformSet, ID) are
// always safe to read; do not write through them.
//
// An element group is read through the existing Group, GroupBoundary,
// IsGroupBegin, and IsGroupEnd methods, which are also nil-safe.
//
// Font is a *pdf.Font from an internal package, so a caller cannot declare a
// variable of that type. It does not need to: the face exposes Ascent,
// UnitsPerEm, and Bytes, and a face cache can be keyed on the op.Font value
// itself. Bytes hands the raw SFNT face to an independent shaper.
type DisplayOp = ilayout.Op

// ProjectiveTransform is a perspective mapping retained for payloads that
// cannot be expressed as final vector geometry.
type ProjectiveTransform = ilayout.ProjectiveTransform

// DisplayPoint is one point in canvas coordinates.
type DisplayPoint = ilayout.DisplayPoint

// DisplayPath is straight-edged drawing geometry attached to an operation.
type DisplayPath = ilayout.DisplayPath

// GlyphBasis describes the local direction and scale of a projected text run.
type GlyphBasis = ilayout.GlyphBasis

// CornerPath is the command list of one DisplayOpPath: the shaped outline
// of a corner-shape box. Read it with CornerPathValue.
type CornerPath = ilayout.CornerPath

// PathCmd is one command of a CornerPath: a verb with its control and end
// points.
type PathCmd = ilayout.PathCmd

// PathVerb names one CornerPath command.
type PathVerb = ilayout.PathVerb

// Path command verbs. An alias does not inherit the constants, so they are
// restated here next to the CornerPath alias.
const (
	// PathMoveTo starts a new contour at Pts[0].
	PathMoveTo = ilayout.PathMoveTo
	// PathLineTo draws a straight edge to Pts[0].
	PathLineTo = ilayout.PathLineTo
	// PathQuadTo draws a quadratic curve through control Pts[0] to Pts[1].
	PathQuadTo = ilayout.PathQuadTo
	// PathCubicTo draws a cubic curve through controls Pts[0], Pts[1] to Pts[2].
	PathCubicTo = ilayout.PathCubicTo
	// PathClose closes the current contour.
	PathClose = ilayout.PathClose
)

// DisplayGroup is the CSS element compositing group an operation belongs to.
type DisplayGroup = ilayout.BlendGroup

// MaskDescriptor, MaskRaster, MaskLayer, and MaskBorder describe the mask
// frames carried by a display group.
type MaskDescriptor = ilayout.MaskDescriptor
type MaskRaster = ilayout.MaskRaster
type MaskLayer = ilayout.MaskLayer
type MaskBorder = ilayout.MaskBorder

// DisplayKind is the operation kind. It is a distinct type, so a consumer
// working with kinds declares values of this type rather than plain integers.
type DisplayKind = ilayout.OpKind

// Display operation kinds. These mirror the internal OpKind values; an alias
// does not inherit the constants, so they are restated here.
const (
	// DisplayOpNoop is a deactivated operation. The clipper writes it when
	// overflow removes an operation from the page, and it paints nothing.
	// Skip it. It is 255, outside the sequence below.
	DisplayOpNoop = ilayout.OpKindNoop
	// DisplayOpUnknown is the zero kind. Layout emits it only as the boundary
	// marker that opens or closes a mask, blend, or isolation group, which carries no
	// paint; test GroupBoundary to tell a marker from an unset kind. No painter
	// matches it, so treat it as inert rather than as a fill.
	DisplayOpUnknown = ilayout.OpUnknown
	// DisplayOpFillRect is a filled rectangle, with per-corner radii for a
	// rounded or elliptical box.
	DisplayOpFillRect = ilayout.OpFillRect
	// DisplayOpStrokeRect is a stroked rectangle. StrokeMask selects sides
	// when non-zero; zero means the complete rounded rectangle.
	DisplayOpStrokeRect = ilayout.OpStrokeRect
	// DisplayOpLine is one stroked segment. Width is in points.
	DisplayOpLine = ilayout.OpLine
	// DisplayOpText is one shaped text run. Y is the baseline.
	DisplayOpText = ilayout.OpText
	// DisplayOpImage is one image with its encoded payload and pixel bounds.
	// Read the payload with ImageBytes, not the promoted fields.
	DisplayOpImage = ilayout.OpImage
	// DisplayOpLinkURI is a link target; it paints nothing itself.
	DisplayOpLinkURI = ilayout.OpLinkURI
	// DisplayOpBullet is a generated list marker. Y is the baseline.
	DisplayOpBullet = ilayout.OpBullet
	// DisplayOpGridRun is one table row's collapsed border grid, carried as
	// ordered segments in Grid. Replay the segments in order.
	DisplayOpGridRun = ilayout.OpGridRun
	// DisplayOpPath is one corner-shape path for a non-round box. Read the
	// commands with CornerPathValue; the op bounds stay the border box.
	// A round box never uses this kind: it keeps the fill and stroke radii.
	DisplayOpPath = ilayout.OpPath
)

// Display is a retained display list plus the canvas it was laid out for.
type Display struct {
	// Ops are the operations in source order. Replay them in the order
	// DisplayOrder returns, not in this order, so z-index, the outline paint
	// layer, and the chrome-below-content rule are honored.
	Ops []DisplayOp
	// OpOwners maps each operation index in Ops to the nearest element box
	// that owns it. Shared operations spanning sibling boxes map to their
	// nearest common element ancestor. -1 means no element owns the operation.
	OpOwners []int

	// Order indexes Ops in paint order, ready to iterate.
	Order []int

	// Boxes are the element border boxes from the same placement, in document
	// order, in CSS pixels. A caller uses them for hit testing.
	Boxes []Box

	// Keyframes are parsed @keyframes rules in stylesheet source order. When
	// names repeat, the last rule with that name is the selected rule.
	Keyframes []KeyframesRule

	// Width and Height are the canvas size in CSS pixels. Height is the
	// taller of the laid-out content and the requested minimum, converted
	// straight from points. An unset requested height does not raise the
	// canvas; it only supplies the percentage-height containing block.
	Width, Height int

	// PointsPerPixel and PixelPerPoint convert between the op coordinate space
	// (points) and the canvas size above (CSS pixels). PointsPerPixel is
	// points per CSS pixel (72/96); PixelPerPoint is CSS pixels per point
	// (96/72). They are reciprocals.
	PointsPerPixel, PixelPerPoint float64

	// ForcedBreaks holds canvas-point Y coordinates where a new page must
	// start, from page-break-before and page-break-after always. Values are
	// sorted ascending with near duplicates merged. A paginator clamps them
	// to its own page grid and ignores any at or past the content end.
	ForcedBreaks []float64

	// KeepTogether holds canvas-point [start, end] ranges that must not
	// straddle a page boundary: break-inside avoid boxes, every table row,
	// a heading and the content that follows it, and single-line blocks
	// whose line cannot fit the page it starts on, so a line moves with its
	// padding, border, and background. A paginator keeps a range on one page
	// when the range fits that page and lets taller ranges split.
	KeepTogether [][2]float64

	// result keeps the placement this display list was built from. A host
	// that edits the tree and asks for one element's patch hands it back to
	// the engine, so the engine re-places that element inside the layout it
	// already made. It is unexported: the public surface of a display is
	// the list itself.
	//
	// It is kept only when the caller asked for it with
	// Options.RetainState. Without that the placement is dropped when the
	// layout returns, and the display keeps nothing but the list a host
	// draws.
	result *ilayout.Result

	// zoom is the scale the layout ran at. A print layout fitted to the
	// canvas width keeps that scale here, so a later element patch uses the
	// same scale as the page it patches. Zero means no scale.
	zoom float64
}

// DisplayList lays doc out and returns its display list. It does not paint
// a page bitmap. A caller replays the operations onto its own canvas and
// keeps text as glyphs. Images on the list stay encoded payloads: an
// orientation or clip that cannot stay in the source bytes is re-encoded
// as a PNG on that one operation, which is the bitmap fallback.
//
// A print layout is fitted to the canvas width: when the laid-out content
// draws past it, the document is re-laid out at a uniform scale of
// width/contentWidth, the print renderers' smart shrink (Chrome print fit,
// wkhtmltopdf smart shrinking). Without the fit an overflowing composition
// (a transformed full-page background, a wide table) produces operations
// that a paginating consumer may split across pages.
//
// DisplayList does not apply a raster pixel budget. A caller that paints
// the list should apply its own size limit.
//
// The display keeps no retained layout state, so RelayoutElement refuses it.
// A host that patches elements lays the page out with
// DisplayListOptions and Options.RetainState set.
func DisplayList(ctx context.Context, doc *css.Document) (*Display, error) {
	return DisplayListOptions(ctx, doc, Options{Images: nil, RetainState: false})
}

// DisplayListOptions is DisplayList with the optional image resolver and the
// optional retained layout state.
//
// The image resolver is the one DisplayListOptions has always taken. The
// retained state is opt-in: set Options.RetainState when the host will patch
// an element with RelayoutElement. Without it the placement the layout
// produced is dropped when the layout returns, and the display keeps only
// the operations and the boxes.
func DisplayListOptions(ctx context.Context, doc *css.Document, options Options) (*Display, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("layout: context: %w", err)
	}

	styled, ok := pubstate.StyledOf(doc)
	if !ok {
		return nil, ErrNilDocument
	}

	widthPt, heightPt := displayViewportPt(styled)

	font, err := pdf.DefaultFont()
	if err != nil {
		return nil, fmt.Errorf("layout: default font: %w", err)
	}

	layoutOpts := displayLayoutOptions(styled, font, options.Images, widthPt, heightPt)

	res, err := ilayout.LayoutContext(ctx, styled.Root, layoutOpts)
	if err != nil {
		return nil, fmt.Errorf("layout: display: %w", err)
	}

	// Print fit: re-lay out at the scale that fits op content to the canvas
	// width when the document draws past it. See DisplayList.
	res, zoom, err := fitPrintWidth(ctx, styled, layoutOpts, res, widthPt)
	if err != nil {
		return nil, err
	}

	// CSS perspective is resolved in Blinkless. The public list carries the
	// resulting paths and text basis, leaving consumers with drawing geometry.
	if err := ilayout.FinalizeDisplayOps(res.Ops); err != nil {
		return nil, fmt.Errorf("layout: display geometry: %w", err)
	}

	// Canvas height is the taller of the content and the requested minimum.
	// A zero request does not raise the canvas.
	canvasPt := res.Height
	if minPt := float64(styled.HeightPx) * cssPxToPt; minPt > canvasPt {
		canvasPt = minPt
	}

	forced, keep := ilayout.PageBreaks(res, heightPt)

	return newDisplay(res, styled.Sheets, canvasPt, forced, keep, zoom, options.RetainState), nil
}

// newDisplay builds the public display list from a placement.
//
// The retained layout state is opt-in. Without it the placement is dropped
// here, so the resolved styles and the box tree the layout built become
// garbage instead of living as long as the display does.
func newDisplay(
	res *ilayout.Result,
	sheets []*icss.Stylesheet,
	canvasPt float64,
	forced []float64,
	keep [][2]float64,
	zoom float64,
	retain bool,
) *Display {
	display := &Display{
		Ops:            res.Ops,
		OpOwners:       ilayout.OperationOwners(res),
		Order:          ilayout.PaintOrder(res.Ops),
		Boxes:          boxesFrom(ilayout.PlacedElements(res)),
		Keyframes:      keyframesFrom(sheets),
		Width:          int(res.Width * ptToPx),
		Height:         int(canvasPt * ptToPx),
		PointsPerPixel: 1 / ptToPx,
		PixelPerPoint:  ptToPx,
		ForcedBreaks:   forced,
		KeepTogether:   keep,
		result:         nil,
		zoom:           0,
	}

	if retain {
		display.result = res
		display.zoom = zoom
	}

	return display
}

func keyframesFrom(sheets []*icss.Stylesheet) []KeyframesRule {
	var rules []KeyframesRule

	for _, sheet := range sheets {
		if sheet == nil {
			continue
		}

		for _, source := range sheet.Keyframes {
			rule := KeyframesRule{Name: source.Name, Keyframes: make([]Keyframe, len(source.Keyframes))}

			for frameIndex, frame := range source.Keyframes {
				declarations := make([]KeyframeDeclaration, 0, len(frame.Declarations))

				for _, declaration := range frame.Declarations {
					if declaration.Important {
						continue
					}

					declarations = append(declarations, KeyframeDeclaration{
						Property: declaration.Prop,
						Value:    declaration.Value,
					})
				}

				rule.Keyframes[frameIndex] = Keyframe{Offset: frame.Offset, Declarations: declarations}
			}

			rules = append(rules, rule)
		}
	}

	return rules
}

// displayLayoutOptions builds the layout options for one display run: the
// fixed viewport, the document's registry, sheets, media, and state, the
// caller's image resolver, and backgrounds on.
func displayLayoutOptions(
	styled pubstate.Styled,
	font *pdf.Font,
	images func(string) ([]byte, error),
	widthPt, heightPt float64,
) ilayout.Options {
	return ilayout.Options{ //nolint:exhaustruct // fixed viewport, backgrounds on
		Width:      widthPt,
		Height:     heightPt,
		Font:       font,
		Registry:   styled.Registry,
		Sheets:     styled.Sheets,
		Media:      styled.Media,
		State:      styled.State,
		Images:     images,
		Background: true,
	}
}

// fitPrintWidth re-lays a print result out at width/contentWidth when its
// content draws past the canvas width, the print renderers' smart shrink.
// A screen result and a fitting print result return unchanged. The returned
// scale is the one the layout ran at, zero when no fit applied.
func fitPrintWidth(
	ctx context.Context,
	styled pubstate.Styled,
	opts ilayout.Options,
	res *ilayout.Result,
	widthPt float64,
) (*ilayout.Result, float64, error) {
	zoom, fit := printFitZoom(styled.Media, widthPt, res.MaxContentX)
	if !fit {
		return res, 0, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, 0, fmt.Errorf("layout: context: %w", err)
	}

	opts.Zoom = zoom

	fitted, err := ilayout.LayoutContext(ctx, styled.Root, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("layout: display: %w", err)
	}

	return fitted, zoom, nil
}

// printFitZoom returns the uniform scale that fits op content to the canvas
// width for a print layout, or false when no fit applies. Print renderers
// shrink an overflowing composition to the paper width (Chrome's print fit,
// wkhtmltopdf's smart shrinking); the native PDF path applies the same
// width/contentWidth factor before painting. Only print media fits, because
// a screen renderer keeps overflowing content at its authored scale.
func printFitZoom(media string, widthPt, contentX float64) (float64, bool) {
	if !strings.EqualFold(strings.TrimSpace(media), "print") {
		return 0, false
	}

	if widthPt <= 0 || math.IsNaN(widthPt) || math.IsInf(widthPt, 0) ||
		contentX <= widthPt+printFitMinOverflowPt {
		return 0, false
	}

	zoom := widthPt / contentX
	if zoom <= 0 || zoom >= 1 {
		return 0, false
	}

	return zoom, true
}

// displayViewportPt returns the viewport width and height in points for a
// styled document. An unset width falls back to fallbackViewportPx, and an
// unset height uses that resolved width.
func displayViewportPt(styled pubstate.Styled) (float64, float64) {
	widthPx := float64(styled.WidthPx)
	if widthPx <= 0 {
		widthPx = fallbackViewportPx
	}

	// Unset height uses the viewport width, in points. That is the bitmap
	// renderer's containing-block fallback for percentage heights.
	heightPt := float64(styled.HeightPx) * cssPxToPt
	if heightPt <= 0 {
		heightPt = widthPx * cssPxToPt
	}

	return widthPx * cssPxToPt, heightPt
}

// DisplayOrder returns the indices that put ops in paint order. It is the
// same policy the PDF writer and the rasterizer use, so a replay matches
// their layering.
func DisplayOrder(ops []DisplayOp) []int {
	return ilayout.PaintOrder(ops)
}

// DisplayFakeBold reports whether a text op must synthesize weight by
// double-striking, because its face is upright but the op is bold. The check
// is CJK-aware and honors the no-fake-bold gate, so a caller should ask rather
// than test op.Bold itself.
func DisplayFakeBold(op *DisplayOp) bool {
	return ilayout.FakeBoldFor(op)
}

// DisplayTransformText applies the CSS text-transform named by an op and
// returns the text to draw. It returns the input unchanged for "none" and for
// an unrecognized value.
func DisplayTransformText(text, transform string) string {
	return ilayout.TransformInlineText(text, transform)
}
