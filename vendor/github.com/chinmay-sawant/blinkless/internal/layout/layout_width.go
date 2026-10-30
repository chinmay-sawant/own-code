package layout

import "github.com/chinmay-sawant/blinkless/internal/html"

// resolveDefiniteWidth applies the width/width% to *w. Returns false when the
// width resolves to auto (cyclic % honesty: indefinite containing block).
func resolveDefiniteWidth(
	eng *engine, node *html.Node, style *ResolvedStyle, availW float64, width *float64,
) (bool, bool) {
	if isIntrinsicWidth(style.Width) {
		*width = eng.flexIntrinsicWidth(node, style, style.Width == widthMinContent)

		return true, true
	}

	definiteW := style.Width >= 0 || style.WidthPercent >= 0

	switch {
	case style.WidthPercent >= 0:
		// Cyclic % honesty: indefinite containing block → treat as auto.
		if availW > 0 && availW < 1e12 {
			*width = availW * style.WidthPercent / oneHundred
		} else {
			definiteW = false
		}
	case style.Width >= 0:
		*width = eng.scalePt(style.Width)
	}

	return definiteW, false
}

// resolveBlockWidth computes a block's used border-box width and the scaled
// left margin. Horizontal auto margins center (or push) a definite-width box.
func resolveBlockWidth(eng *engine, node *html.Node, style *ResolvedStyle, availW float64) (float64, float64) {
	margR := eng.scalePt(style.MarginRight)
	margL := eng.scalePt(style.MarginLeft)
	// Default: fill remaining width after horizontal margins.
	width := availW - margL - margR
	if width < 0 {
		width = 0
	}

	// CSS Writing Modes §7.2: a box whose writing mode is orthogonal to its
	// containing block shrink-wraps its auto inline size. Chrome 143 on
	// case-30-wpt-auto-margins-column.html: the vertical-lr wrapper around
	// the 300pt flex case is 402px (fit-content), not the 1024px viewport.
	if ortho := orthogonalAutoWidth(eng, node, style, margL, margR); ortho >= 0 {
		width = ortho
	}

	definiteW, intrinsicW := resolveDefiniteWidth(eng, node, style, availW, &width)
	// content-box (default): specified width is the content width, so the
	// border box grows by horizontal padding + border. border-box: specified
	// width already is the border-box size.
	if definiteW && !intrinsicW && style.BoxSizing != borderBox {
		width += style.horizontalChrome(eng)
	}

	width = clampBlockMinMax(eng, style, availW, width)
	margL = resolveAutoMargins(style, definiteW, width, availW, margL, margR)

	return width, margL
}

// orthogonalAutoWidth returns the fit-content border-box width for a block
// whose writing mode is orthogonal to its containing block and whose inline
// size is auto, or -1 when the rule does not apply (CSS Writing Modes §7.2).
func orthogonalAutoWidth(
	eng *engine, node *html.Node, style *ResolvedStyle, margL, margR float64,
) float64 {
	if eng.flowWritingMode == "" ||
		isVerticalWritingMode(eng.flowWritingMode) == isVerticalWritingMode(style.WritingMode) ||
		style.Width >= 0 || style.WidthPercent >= 0 || isIntrinsicWidth(style.Width) {
		return -1
	}

	fit := eng.blockFitContentMarginBox(node, *style)
	if fit <= 0 {
		return -1
	}

	width := fit - margL - margR
	if width < 0 {
		width = 0
	}

	return width
}

// resolveAutoMargins centers (or pushes) a definite-width block via auto
// horizontal margins (CSS2.1 §10.3.3).
func resolveAutoMargins(style *ResolvedStyle, definiteW bool, width, availW, margL, margR float64) float64 {
	if definiteW && (style.MarginLeftAuto || style.MarginRightAuto) {
		free := availW - width
		if free < 0 {
			free = 0
		}

		switch {
		case style.MarginLeftAuto && style.MarginRightAuto:
			margL = free / two
		case style.MarginLeftAuto:
			margL = free - margR
			if margL < 0 {
				margL = 0
			}
		}
	}

	return margL
}

// clampBlockMinMax applies the min/max-width constraints to w.
func clampBlockMinMax(eng *engine, style *ResolvedStyle, availW, width float64) float64 {
	width = clampBlockMaxWidth(eng, style, availW, width)

	return clampBlockMinWidth(eng, style, availW, width)
}

func clampBlockMinWidth(eng *engine, style *ResolvedStyle, availW, width float64) float64 {
	hChrome := 0.0
	if style.BoxSizing != borderBox {
		hChrome = style.horizontalChrome(eng)
	}

	if style.MinWidthPercent >= 0 && availW > 0 && availW < 1e12 {
		minW := availW * style.MinWidthPercent / oneHundred

		if style.BoxSizing != borderBox {
			minW += hChrome
		}

		if width < minW {
			return minW
		}

		return width
	}

	if style.MinWidth > 0 {
		minW := eng.scalePt(style.MinWidth)
		if style.BoxSizing != borderBox {
			minW += hChrome
		}

		if width < minW {
			return minW
		}
	}

	return width
}

func clampBlockMaxWidth(eng *engine, style *ResolvedStyle, availW, width float64) float64 {
	hChrome := 0.0
	if style.BoxSizing != borderBox {
		hChrome = style.horizontalChrome(eng)
	}

	if style.MaxWidthPercent >= 0 && availW > 0 && availW < 1e12 {
		maxW := availW * style.MaxWidthPercent / oneHundred

		if style.BoxSizing != borderBox {
			maxW += hChrome
		}

		if width > maxW {
			return maxW
		}

		return width
	}

	if style.MaxWidth >= 0 {
		maxW := eng.scalePt(style.MaxWidth)
		if style.BoxSizing != borderBox {
			maxW += hChrome
		}

		if width > maxW {
			return maxW
		}
	}

	return width
}
