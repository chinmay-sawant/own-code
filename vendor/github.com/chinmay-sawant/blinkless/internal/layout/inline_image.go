package layout

// This file owns inline replaced-content (image) emission: chrome insets,
// bitmap filters, object-fit sizing, and clip-path masking.

func (e *engine) applyInlineImageBorders(item *inlineItem, leftX, top float64) (float64, float64, float64, float64) {
	imgX, imgY, imgW, imgH := leftX, top, item.w, item.h
	if item.style == nil || !inlineHasBorder(*item.style) {
		return imgX, imgY, imgW, imgH
	}

	if item.thumbImg {
		// Collapsed figure owns L/R/T; only the image/caption separator paints.
		e.emitThumbImageBottomSeparator(*item.style, leftX, top, item.w, item.h)

		return imgX, imgY, imgW, imgH
	}

	insetL := e.inlineChromeLeft(item.style)
	insetT := e.inlineChromeTop(item.style)
	insetR := e.inlineChromeRight(item.style)
	insetB := e.inlineChromeBottom(item.style)
	imgX += insetL
	imgY += insetT
	imgW -= insetL + insetR
	imgH -= insetT + insetB

	for _, op := range e.borderOps(*item.style, leftX, top, item.w, item.h) {
		e.add(op)
	}

	return imgX, imgY, imgW, imgH
}

// emitInlineImage places an image (or inline-block) item on the line and
// returns the updated x cursor. Replaced content never receives text
// underline decorations (that was the thumb hairline source). An inline-level
// <svg> keeps the box built during collection, and the raster op is attributed
// to that box so the element stays addressable after layout.
func (e *engine) emitInlineImage(
	boxNode *box, item *inlineItem, leftX, lineY, lineH, baseline, justifyGap float64,
	gapAfter bool, und *undRun,
) float64 {
	und.flush(e)

	top := e.alignedInlineTop(item, lineY, lineH, baseline)
	start := len(e.ops)
	imgX, imgY, imgW, imgH := e.applyInlineImageBorders(item, leftX, top)

	e.paintInlineImageItem(item, leftX, top, imgX, imgY, imgW, imgH)
	e.attributeInlineSVGBox(item, boxNode, start, leftX, top)

	if item.href != "" {
		e.add((Op{ //nolint:exhaustruct // intentional zero fields
			Kind: OpLinkURI, X: leftX, Y: top, W: item.w, H: item.h,
		}).withURI(item.href))
	}

	leftX += item.w + item.marginR
	if gapAfter && isJustifyGapAfter(*item) {
		leftX += justifyGap
	}

	return leftX
}

// paintInlineImageItem paints one inline image bitmap: filters, object-fit
// sizing, and clip-path masking, mirroring the replaced-image paint path.
func (e *engine) paintInlineImageItem(item *inlineItem, leftX, top, imgX, imgY, imgW, imgH float64) {
	if item.imgRef == nil || item.imgRef.data == nil || imgW <= 0 || imgH <= 0 {
		return
	}

	sty := ResolvedStyle{} //nolint:exhaustruct // image paint defaults are filled conditionally

	if item.style != nil {
		sty = *item.style
	}

	imgData, isJPEG, pixW, pixH, fitX, fitY, fitW, fitH := e.objectFitPaintForImage(
		item.imgRef, item.imgRef.data, item.imgRef.isJPEG, sty,
		imgX, imgY, imgW, imgH, leftX, top, item.w, item.h,
	)
	if fitW <= 0 || fitH <= 0 {
		return
	}

	e.add((Op{ //nolint:exhaustruct // intentional zero fields
		Kind: OpImage, X: fitX, Y: fitY, W: fitW, H: fitH, IsJPEG: isJPEG,
	}).withImage(imgData, pixW, pixH, item.alt))
}
