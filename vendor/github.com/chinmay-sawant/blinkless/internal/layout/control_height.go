package layout

import (
	"strconv"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

// nativeWidgetAutoContentBottom returns the content-flow endpoint for an
// auto-sized native value control whose border-box height is one scaled font
// size. Padding and the top border are already part of the flow coordinate;
// the caller adds bottom padding after this endpoint.
func (e *engine) nativeWidgetAutoContentBottom(style ResolvedStyle) float64 {
	targetHeight := e.scalePt(style.FontSize)
	topChrome := e.scalePt(style.BorderTop.Width + style.PaddingTop)
	contentHeight := targetHeight - topChrome - e.scalePt(style.PaddingBottom)

	if contentHeight < 0 {
		contentHeight = 0
	}

	return topChrome + contentHeight
}

// maxTextareaRows caps the rows attribute so malformed HTML cannot size a
// textarea into a huge page.
const maxTextareaRows = 30

// textareaAutoContentBottom returns the content-flow endpoint for an
// auto-sized textarea whose intrinsic height is rows * line-height. The caller
// has already added top padding/border to curY and will add bottom padding
// after this call.
func (e *engine) textareaAutoContentBottom(
	style ResolvedStyle, node *html.Node, boxStyle *ResolvedStyle, curY float64,
) float64 {
	rowsStr := strings.TrimSpace(node.Attribute("rows"))
	rows := 2

	if n, err := strconv.Atoi(rowsStr); err == nil && n > 0 {
		rows = n
		// Cap absurd rows to avoid huge pages from malformed HTML.
		if rows > maxTextareaRows {
			rows = maxTextareaRows
		}
	}

	lineH := e.lineHeightOf(&style)
	if lineH <= 0 {
		lineH = defaultLineHeightRatio * style.FontSize
	}

	scaledLineH := e.scalePt(lineH)
	contentH := scaledLineH * float64(rows)
	topChrome := e.scalePt(boxStyle.PaddingTop) + e.scalePt(borderLayoutWidth(boxStyle, boxStyle.BorderTop))
	desired := topChrome + contentH

	if curY < desired {
		return desired
	}

	return curY
}
