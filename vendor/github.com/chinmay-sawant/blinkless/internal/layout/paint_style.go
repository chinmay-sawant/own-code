package layout

// maxLatin1Rune gates fake-bold synthesis to Latin text. Stroking CJK outlines
// leaves horizontal streaks, so those runs stay unstroked.
const maxLatin1Rune = 0xFF

// PaintStyle is the resolved per-op appearance for display-list consumers:
// raw RGB, source alpha, stroke min-width, and the Latin-only fake-bold gate.
type PaintStyle struct {
	FillR, FillG, FillB float64
	FillAlpha           float64
	StrokeWidth         float64
	FakeBold            bool
}

// StyleOf resolves paint appearance for op. Translucent fills keep their
// source RGB and alpha. Consumers composite that color with source-over alpha.
func StyleOf(paintOp *Op) PaintStyle {
	if paintOp == nil {
		return PaintStyle{FillAlpha: 1, StrokeWidth: 1} //nolint:exhaustruct // intentional zero fields
	}

	pstyle := PaintStyle{ //nolint:exhaustruct // intentional zero fields
		FillR: paintOp.R, FillG: paintOp.G, FillB: paintOp.B, FillAlpha: 1,
		StrokeWidth: paintOp.Width,
	}
	if pstyle.StrokeWidth <= 0 {
		pstyle.StrokeWidth = 1
	}

	if paintOp.Alpha > 0 && paintOp.Alpha < 1 {
		pstyle.FillAlpha = paintOp.Alpha
	}

	pstyle.FakeBold = FakeBoldFor(paintOp)

	return pstyle
}

// FakeBoldFor reports whether CSS bold should be synthesized for op.
// Latin only. CJK stroking produces streak artifacts.
func FakeBoldFor(paintOp *Op) bool {
	noFakeBold := paintOp != nil && paintOp.opExtra != nil && paintOp.opExtra.NoFakeBold
	if paintOp == nil || noFakeBold || !paintOp.Bold ||
		(paintOp.Font != nil && paintOp.Font.Bold()) {
		return false
	}

	for _, r := range paintOp.Text {
		if r > maxLatin1Rune {
			return false
		}
	}

	return true
}

// pdfPaintOpacity folds element opacity with the op alpha. Zero means no
// override. Only values strictly between 0 and 1 change the result.
func pdfPaintOpacity(paintOp *Op, includeAlpha bool) float64 {
	opacity := 1.0
	if paintOp == nil {
		return opacity
	}

	paintOp.bindEmptyExtra()

	if paintOp.PaintOpacity > 0 && paintOp.PaintOpacity < 1 {
		opacity = paintOp.PaintOpacity
	}

	if includeAlpha && paintOp.Alpha > 0 && paintOp.Alpha < 1 {
		opacity *= paintOp.Alpha
	}

	return opacity
}
