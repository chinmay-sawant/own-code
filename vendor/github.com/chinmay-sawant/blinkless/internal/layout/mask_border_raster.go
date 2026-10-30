// Mask-border frame raster composition (v0.0.3 mask-border ledger, phase 5).
//
// The descriptor in mask_border.go records the frame geometry; this file turns
// it into the ready-to-paint mask raster the display-list host applies. The
// raster is composed at the element's border-box device size, so the tiling
// modes (repeat, round, space) can lay out their tiles against the real box.
//
// The composed raster is an alpha mask: the nine-slice frame is opaque, the
// center cell stays transparent unless mask-border-slice asked for fill. The
// host applies it as a soft mask over the element group, which is the same
// path the first-wave mask raster uses (blend_group.go MaskDescriptor).
//
// This file emits no operations and adds no new op kind. It runs after the
// element's box is built, when the group is popped, so the frame knows its
// destination size.
//
//nolint:varnamelen // pixel loops use x/y coordinates by convention
package layout

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"math"
	"strings"
)

// composeMaskBorderRaster builds the alpha frame raster for one mask-border
// descriptor at the element's border-box size in device pixels. It returns
// nil when the source is missing or unreadable, or when the box is empty.
func composeMaskBorderRaster(frame *MaskBorder, boxW, boxH float64) *MaskRaster {
	src, dstW, dstH := decodeMaskBorderFrame(frame, boxW, boxH)
	if src == nil {
		return nil
	}

	canvas := image.NewRGBA(image.Rect(0, 0, dstW, dstH))

	for row := range 3 {
		for col := range 3 {
			paintMaskBorderCell(canvas, src, frame, dstW, dstH, row, col)
		}
	}

	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil {
		return nil
	}

	return &MaskRaster{ImageBytes: out.Bytes(), PixelWidth: dstW, PixelHeight: dstH}
}

// decodeMaskBorderFrame decodes the frame source and resolves the destination
// size in device pixels. It returns a nil image when any input is unusable.
func decodeMaskBorderFrame(frame *MaskBorder, boxW, boxH float64) (image.Image, int, int) {
	if frame == nil || len(frame.ImageBytes) == 0 || boxW <= 0 || boxH <= 0 {
		return nil, 0, 0
	}

	src, _, err := image.Decode(bytes.NewReader(frame.ImageBytes))
	if err != nil {
		return nil, 0, 0
	}

	dstW := int(math.Round(boxW))
	dstH := int(math.Round(boxH))

	if dstW <= 0 || dstH <= 0 || !validImageSize(dstW, dstH) {
		return nil, 0, 0
	}

	return src, dstW, dstH
}

// paintMaskBorderCell paints one of the nine frame cells onto the canvas. The
// center cell stays untouched unless the slice asked for fill.
func paintMaskBorderCell(
	canvas *image.RGBA, src image.Image, frame *MaskBorder, dstW, dstH, row, col int,
) {
	if row == 1 && col == 1 && !frame.SliceFill {
		return
	}

	sliceX, sliceY, sliceW, sliceH := maskBorderSourceGrid(frame, src.Bounds())
	thick := maskBorderDestThickness(frame, dstW, dstH)
	dstX := [3]int{0, thick[3], dstW - thick[1]}
	dstY := [3]int{0, thick[0], dstH - thick[2]}
	dstW3 := [3]int{thick[3], dstW - thick[3] - thick[1], thick[1]}
	dstH3 := [3]int{thick[0], dstH - thick[0] - thick[2], thick[2]}

	if sliceW[col] <= 0 || sliceH[row] <= 0 || dstW3[col] <= 0 || dstH3[row] <= 0 {
		return
	}

	srcRect := image.Rect(
		sliceX[col], sliceY[row],
		sliceX[col]+sliceW[col], sliceY[row]+sliceH[row],
	)
	dstRect := image.Rect(
		dstX[col], dstY[row],
		dstX[col]+dstW3[col], dstY[row]+dstH3[row],
	)

	if row == 1 && col == 1 {
		// The center fills the whole inner rect; no tiling applies.
		tileStretched(canvas, src, srcRect, dstRect)

		return
	}

	tileMaskBorderCell(canvas, src, srcRect, dstRect, frame.Repeat, row, col)
}

// maskBorderSourceGrid maps the frame's source-pixel slice insets to the
// source column/row origins and sizes, mirroring borderImageSourceGrid.
func maskBorderSourceGrid(frame *MaskBorder, bounds image.Rectangle) ([3]int, [3]int, [3]int, [3]int) {
	sliceTop := int(math.Round(frame.Slice[0]))
	sliceRight := int(math.Round(frame.Slice[1]))
	sliceBottom := int(math.Round(frame.Slice[2]))
	sliceLeft := int(math.Round(frame.Slice[3]))

	srcW := bounds.Dx()
	srcH := bounds.Dy()

	clamp := func(value, limit int) int {
		if value < 0 {
			return 0
		}

		if value > limit {
			return limit
		}

		return value
	}

	sliceLeft = clamp(sliceLeft, srcW)
	sliceRight = clamp(sliceRight, srcW-sliceLeft)
	sliceTop = clamp(sliceTop, srcH)
	sliceBottom = clamp(sliceBottom, srcH-sliceTop)

	srcX := [3]int{bounds.Min.X, bounds.Min.X + sliceLeft, bounds.Max.X - sliceRight}
	srcY := [3]int{bounds.Min.Y, bounds.Min.Y + sliceTop, bounds.Max.Y - sliceBottom}
	srcW3 := [3]int{sliceLeft, srcW - sliceLeft - sliceRight, sliceRight}
	srcH3 := [3]int{sliceTop, srcH - sliceTop - sliceBottom, sliceBottom}

	return srcX, srcY, srcW3, srcH3
}

// maskBorderDestThickness resolves the frame thickness to destination device
// pixels, clamped so the four sides cannot overlap inside the box.
func maskBorderDestThickness(frame *MaskBorder, dstW, dstH int) [4]int {
	scaled := [4]int{
		int(math.Round(frame.Width[0])),
		int(math.Round(frame.Width[1])),
		int(math.Round(frame.Width[2])),
		int(math.Round(frame.Width[3])),
	}

	for idx := range scaled {
		if scaled[idx] < 0 {
			scaled[idx] = 0
		}
	}

	if scaled[3]+scaled[1] > dstW && dstW > 0 {
		scale := float64(dstW) / float64(scaled[3]+scaled[1])
		scaled[3] = int(math.Round(float64(scaled[3]) * scale))
		scaled[1] = int(math.Round(float64(scaled[1]) * scale))
	}

	if scaled[0]+scaled[2] > dstH && dstH > 0 {
		scale := float64(dstH) / float64(scaled[0]+scaled[2])
		scaled[0] = int(math.Round(float64(scaled[0]) * scale))
		scaled[2] = int(math.Round(float64(scaled[2]) * scale))
	}

	return scaled
}

// tileMaskBorderCell paints one of the nine frame cells. Corners always
// stretch; edges follow the used repeat pair. The first token is the
// horizontal mode, the second the vertical mode, and a single token applies
// to both axes, matching border-image-repeat.
func tileMaskBorderCell(
	dst draw.Image, src image.Image, srcRect, dstRect image.Rectangle, repeat string, row, col int,
) {
	horizontal, vertical := maskBorderRepeatPair(repeat)

	mode := horizontal
	if row == 1 {
		mode = vertical
	}

	if row != 1 && col != 1 {
		mode = fxStretch
	}

	switch mode {
	case borderRepeatRepeat:
		tileRepeated(dst, src, srcRect, dstRect)
	case borderRepeatRound:
		tileRounded(dst, src, srcRect, dstRect)
	case borderRepeatSpace:
		tileSpaced(dst, src, srcRect, dstRect)
	default:
		tileStretched(dst, src, srcRect, dstRect)
	}
}

// maskBorderRepeatPair splits the used repeat pair into horizontal and
// vertical modes. One token applies to both axes.
func maskBorderRepeatPair(repeat string) (string, string) {
	tokens := splitRepeatTokens(repeat)
	switch len(tokens) {
	case 0:
		return fxStretch, fxStretch
	case 1:
		return tokens[0], tokens[0]
	default:
		return tokens[0], tokens[1]
	}
}

// tileStretched scales one source slice to the whole destination rect,
// clipping the source when the destination is smaller than the slice.
func tileStretched(dst draw.Image, src image.Image, srcRect, dstRect image.Rectangle) {
	if srcRect.Empty() || dstRect.Empty() {
		return
	}

	srcClip := srcRect

	if dstRect.Dx() < srcRect.Dx() {
		srcClip.Max.X = srcClip.Min.X + dstRect.Dx()
	}

	if dstRect.Dy() < srcRect.Dy() {
		srcClip.Max.Y = srcClip.Min.Y + dstRect.Dy()
	}

	copyScaled(dst, src, srcClip, dstRect)
}

// tileRepeated repeats the source slice at its own pixel size from the
// destination origin, clipping the final tile at the destination edge. No
// scaling happens, matching border-image-repeat: repeat.
func tileRepeated(dst draw.Image, src image.Image, srcRect, dstRect image.Rectangle) {
	if srcRect.Empty() || dstRect.Empty() {
		return
	}

	for y := dstRect.Min.Y; y < dstRect.Max.Y; y += srcRect.Dy() {
		for x := dstRect.Min.X; x < dstRect.Max.X; x += srcRect.Dx() {
			tile := image.Rect(x, y, x+srcRect.Dx(), y+srcRect.Dy()).Intersect(dstRect)
			copyTile(dst, src, srcRect, tile)
		}
	}
}

// tileRounded tiles the source slice with the tile count rounded to fill the
// destination exactly, scaling each tile by the remainder. One tile stretches
// to cover the whole destination.
func tileRounded(dst draw.Image, src image.Image, srcRect, dstRect image.Rectangle) {
	if srcRect.Empty() || dstRect.Empty() {
		return
	}

	count := int(math.Round(float64(dstRect.Dx()) / float64(srcRect.Dx())))
	if count <= 0 {
		count = 1
	}

	tileW := float64(dstRect.Dx()) / float64(count)

	for idx := range count {
		x0 := dstRect.Min.X + int(math.Round(float64(idx)*tileW))
		x1 := dstRect.Min.X + int(math.Round(float64(idx+1)*tileW))
		tile := image.Rect(x0, dstRect.Min.Y, x1, dstRect.Max.Y).Intersect(dstRect)
		copyScaled(dst, src, srcRect, tile)
	}
}

// tileSpaced tiles the source slice with equal gaps between tiles, so the
// first and last tiles sit at the destination edges. One tile centers itself.
func tileSpaced(dst draw.Image, src image.Image, srcRect, dstRect image.Rectangle) {
	if srcRect.Empty() || dstRect.Empty() {
		return
	}

	room := dstRect.Dx() - srcRect.Dx()
	if room < 0 {
		tileStretched(dst, src, srcRect, dstRect)

		return
	}

	count := room/srcRect.Dx() + 1
	if count <= 1 {
		offset := dstRect.Min.X + room/maskBorderSpacedCentering
		tile := image.Rect(offset, dstRect.Min.Y, offset+srcRect.Dx(), dstRect.Max.Y).Intersect(dstRect)
		copyTile(dst, src, srcRect, tile)

		return
	}

	gap := float64(room) / float64(count-1)

	for idx := range count {
		x0 := dstRect.Min.X + int(math.Round(float64(idx)*gap))
		tile := image.Rect(x0, dstRect.Min.Y, x0+srcRect.Dx(), dstRect.Max.Y).Intersect(dstRect)
		copyTile(dst, src, srcRect, tile)
	}
}

// copyTile copies one source slice into dstTile at 1:1 scale, clipping the
// source when the tile is smaller than the slice.
func copyTile(dst draw.Image, src image.Image, srcRect, dstTile image.Rectangle) {
	if srcRect.Empty() || dstTile.Empty() {
		return
	}

	for y := dstTile.Min.Y; y < dstTile.Max.Y; y++ {
		sy := srcRect.Min.Y + (y - dstTile.Min.Y)
		if sy >= srcRect.Max.Y {
			break
		}

		for x := dstTile.Min.X; x < dstTile.Max.X; x++ {
			sx := srcRect.Min.X + (x - dstTile.Min.X)
			if sx >= srcRect.Max.X {
				break
			}

			dst.Set(x, y, src.At(sx, sy))
		}
	}
}

// copyScaled copies one source slice into dstTile, scaling it to the tile
// size. The tile never exceeds the source slice size.
func copyScaled(dst draw.Image, src image.Image, srcRect, dstTile image.Rectangle) {
	if srcRect.Empty() || dstTile.Empty() {
		return
	}

	scaleX := float64(srcRect.Dx()) / float64(dstTile.Dx())
	scaleY := float64(srcRect.Dy()) / float64(dstTile.Dy())

	for y := dstTile.Min.Y; y < dstTile.Max.Y; y++ {
		sy := srcRect.Min.Y + int((float64(y-dstTile.Min.Y)+maskBorderSampleCenter)*scaleY)
		sy = clampInt(sy, srcRect.Min.Y, srcRect.Max.Y-1)

		for x := dstTile.Min.X; x < dstTile.Max.X; x++ {
			sx := srcRect.Min.X + int((float64(x-dstTile.Min.X)+maskBorderSampleCenter)*scaleX)
			sx = clampInt(sx, srcRect.Min.X, srcRect.Max.X-1)
			dst.Set(x, y, src.At(sx, sy))
		}
	}
}

const (
	// maskBorderSpacedCentering halves the free room when a single spaced
	// tile centers itself.
	maskBorderSpacedCentering = 2
	// maskBorderSampleCenter samples the middle of one scaled source pixel.
	maskBorderSampleCenter = 0.5
)

// splitRepeatTokens returns the readable repeat tokens of the used pair.
func splitRepeatTokens(repeat string) []string {
	tokens := make([]string, 0, maskBorderRepeatMaxTokens)

	for _, token := range strings.Fields(repeat) {
		switch token {
		case fxStretch, borderRepeatRepeat, borderRepeatRound, borderRepeatSpace:
			tokens = append(tokens, token)
		}
	}

	return tokens
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}

	if value > high {
		return high
	}

	return value
}

// composeMaskBorderOnGroup composes the frame raster for one popped group and
// stores it as the group's ready-to-paint mask when no mask-image raster
// exists. It runs after the element's box is built, so the tiling modes know
// the destination size. A nil or empty frame leaves the group untouched.
func (e *engine) composeMaskBorderOnGroup(group *BlendGroup, boxNode *box) {
	if group == nil || group.MaskBorder == nil || boxNode == nil {
		return
	}

	if group.Mask != nil && len(group.Mask.RasterBytes()) > 0 {
		// The first-wave mask already carries a raster; the frame joins it
		// once layer composition lands. The frame descriptor stays recorded.
		return
	}

	raster := composeMaskBorderRaster(group.MaskBorder, boxNode.w, boxNode.height)
	if raster == nil {
		return
	}

	group.Mask = &MaskDescriptor{ //nolint:exhaustruct // layers stay empty for a frame raster
		Raster: raster,
	}
}

// RasterBytes returns the raster payload of the descriptor, or nil.
func (m *MaskDescriptor) RasterBytes() []byte {
	if m == nil || m.Raster == nil {
		return nil
	}

	return m.Raster.ImageBytes
}
