package layout

import (
	"fmt"
	"math"
	"slices"
)

// snapEpsilon absorbs float noise on exact device-pixel multiples, such as a
// 2px border at dsf 1.25 whose cssPx*dsf computes just under 2.5.
const snapEpsilon = 1e-6

// SnapDisplayToDevicePixels returns a copy of display with the painted stroke
// widths of OpStrokeRect and OpLine quantized to the device pixel grid of the
// given device scale factor. It is opt-in: layout keeps exact CSS point
// geometry, and a consumer that paints a screen raster calls this before
// replay. A print consumer replays the unquantized list, so print keeps the
// exact pt values the stylesheet asked for.
//
// Only the positive Width field of OpStrokeRect and OpLine is changed, which
// covers borders, table rules, outlines, and text decorations. Coordinates,
// Boxes, Order, the canvas size, the point/pixel factors, payloads, text,
// fills, images, links, and groups are left as they were. OpGridRun segments
// are not snapped: the segments of a collapsed table grid keep their exact
// widths, because the run is replayed as one batched table row.
//
// For one stroke, cssPx is the width in CSS pixels (width / 0.75, the
// cssPxToPt factor in displaylist.go). The snapped width is
// max(1, floor(cssPx*dsf+1e-6)) device pixels, converted back to CSS pixels
// by dividing by dsf, and then to points. At dsf 1 a 1pt border becomes
// 0.75pt (1px), and at dsf 2 a 0.75px border becomes 0.5px, which is one
// device pixel.
//
// It returns ErrNilDocument when display is nil and ErrBadDeviceScale when
// dsf is NaN, infinite, or not positive.
func SnapDisplayToDevicePixels(display *Display, dsf float64) (*Display, error) {
	if display == nil {
		return nil, ErrNilDocument
	}

	if math.IsNaN(dsf) || math.IsInf(dsf, 0) || dsf <= 0 {
		return nil, fmt.Errorf("layout: snap display: %v: %w", dsf, ErrBadDeviceScale)
	}

	snapped := *display
	snapped.Ops = slices.Clone(display.Ops)
	snapped.Order = slices.Clone(display.Order)
	snapped.Boxes = slices.Clone(display.Boxes)

	for idx := range snapped.Ops {
		paintOp := &snapped.Ops[idx]

		// Every other kind is left alone: fill, text, image, link, bullet,
		// and grid-run ops keep their exact widths. OpUnknown is a group
		// boundary and OpNoop a deactivated op; neither paints.
		if paintOp.Kind != DisplayOpStrokeRect && paintOp.Kind != DisplayOpLine {
			continue
		}

		if paintOp.Width > 0 {
			paintOp.Width = snapStrokeWidth(paintOp.Width, dsf)
		}
	}

	return &snapped, nil
}

// snapStrokeWidth quantizes one stroke width in points to the device pixel
// grid. A stroke always keeps at least one device pixel, so a hairline does
// not disappear. The epsilon absorbs float noise on exact multiples, such as
// a 2px border at dsf 1.25 whose cssPx*dsf computes just under 2.5.
func snapStrokeWidth(widthPt, dsf float64) float64 {
	cssPx := widthPt / cssPxToPt
	devicePx := math.Floor(cssPx*dsf + snapEpsilon)

	if devicePx < 1 {
		devicePx = 1
	}

	return devicePx / dsf * cssPxToPt
}
