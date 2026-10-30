package layout

import (
	"net/url"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/svg"
)

func maskImageEnabled(value string) bool {
	// Fast path for the initial value: every element carries mask-image:none
	// unless the author declares otherwise, and splitting that one keyword
	// through splitParenArgs only to rediscover "none" costs a slice per
	// box (maskGroupRequested runs in pushZ and pushInlineBlend). "" and a
	// lone "none" can never enable a mask, so answer without parsing.
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.EqualFold(trimmed, "none") {
		return false
	}

	layers, ok := splitMaskLayerList(value)
	if !ok {
		return false
	}

	for _, layer := range layers {
		if !strings.EqualFold(strings.TrimSpace(layer), "none") {
			return true
		}
	}

	return false
}

func (e *engine) maskDescriptor(style ResolvedStyle) *MaskDescriptor {
	images, ok := splitMaskLayerList(style.MaskImage)
	if !ok || len(images) == 0 {
		return nil
	}

	descriptor := &MaskDescriptor{Layers: make([]MaskLayer, 0, len(images))} //nolint:exhaustruct // Raster set below

	for index, source := range images {
		layer := MaskLayer{
			Source:       strings.TrimSpace(source),
			Mode:         maskLayerValue(style.MaskMode, index, maskModeMatchSource),
			Composite:    maskLayerValue(style.MaskComposite, index, maskCompositeAdd),
			Position:     maskLayerValue(style.MaskPosition, index, maskInitialPosition),
			Repeat:       maskLayerValue(style.MaskRepeat, index, "repeat"),
			Size:         maskLayerValue(style.MaskSize, index, "auto"),
			Origin:       maskLayerValue(style.MaskOrigin, index, "border-box"),
			Clip:         maskLayerValue(style.MaskClip, index, "border-box"),
			ImageBytes:   nil,
			PixelWidth:   0,
			PixelHeight:  0,
			Preprocessed: false,
		}

		if chosen, ok := maskImageSetChoice(layer.Source); ok {
			layer.Source = chosen
		}

		if isGradientFunc(layer.Source) {
			e.fillMaskGradient(&layer, style)

			if layer.Mode == maskModeMatchSource {
				layer.Mode = maskModeAlpha
			}
		} else if !strings.EqualFold(layer.Source, "none") {
			layer = e.resolveMaskLayer(layer)
		}

		descriptor.Layers = append(descriptor.Layers, layer)
	}

	// The current paint-list subset emits the first resolved layer as the
	// group's ready-to-paint mask raster. Multi-layer composition remains
	// outside this subset.
	if len(descriptor.Layers) > 0 {
		layer := descriptor.Layers[0]
		if len(layer.ImageBytes) > 0 {
			descriptor.Raster = &MaskRaster{
				ImageBytes:  layer.ImageBytes,
				PixelWidth:  layer.PixelWidth,
				PixelHeight: layer.PixelHeight,
			}
		}
	}

	return descriptor
}

// maskGradientMinPt is the raster side used when the element has no definite
// positive length. The gradient function still ceils this to pixels and caps
// a side at 512.
const maskGradientMinPt = 1

const maskGradientAxisCount = 2

// fillMaskGradient stores one gradient mask as PNG bytes. The display list
// carries the resolved raster on its group; it is not a standalone image op.
func (e *engine) fillMaskGradient(layer *MaskLayer, style ResolvedStyle) {
	width, height := e.maskGradientPaintSize(style, layer.Size)
	pngBytes, pixelW, pixelH, rendered := maskGradientPNG(
		layer.Source,
		width,
		height,
		style.Color,
	)

	if !rendered {
		return
	}

	layer.ImageBytes = pngBytes
	layer.PixelWidth = pixelW
	layer.PixelHeight = pixelH
}

// maskGradientPaintSize is the raster size in paint points. auto, cover, and
// contain use the element border box. One length sets the width and keeps
// the border-box height. Two lengths set width, then height.
func (e *engine) maskGradientPaintSize(style ResolvedStyle, size string) (float64, float64) {
	boxW, boxH := maskGradientBorderBox(style)
	width, height := maskGradientLayerSize(size, boxW, boxH, style.FontSize)

	return e.scalePt(width), e.scalePt(height)
}

func maskGradientBorderBox(style ResolvedStyle) (float64, float64) {
	contentBox := style.BoxSizing != borderBox
	width := maskGradientBorderSide(
		style.Width,
		style.PaddingLeft+style.PaddingRight+style.BorderLeft.Width+style.BorderRight.Width,
		contentBox,
	)
	height := maskGradientBorderSide(
		style.Height,
		style.PaddingTop+style.PaddingBottom+style.BorderTop.Width+style.BorderBottom.Width,
		contentBox,
	)

	return width, height
}

func maskGradientBorderSide(length, chrome float64, contentBox bool) float64 {
	if length <= 0 {
		return maskGradientMinPt
	}

	side := length
	if contentBox {
		side += chrome
	}

	if side <= 0 {
		return maskGradientMinPt
	}

	return side
}

func maskGradientLayerSize(size string, boxW, boxH, fontSize float64) (float64, float64) {
	parts := strings.Fields(size)
	switch len(parts) {
	case 1:
		if maskGradientSizeKeyword(parts[0]) {
			return boxW, boxH
		}

		return maskGradientLength(parts[0], boxW, fontSize), boxH
	case maskGradientAxisCount:
		return maskGradientLength(parts[0], boxW, fontSize),
			maskGradientLength(parts[1], boxH, fontSize)
	default:
		return boxW, boxH
	}
}

func maskGradientSizeKeyword(token string) bool {
	token = strings.TrimSpace(token)

	return strings.EqualFold(token, overflowAuto) ||
		strings.EqualFold(token, "cover") ||
		strings.EqualFold(token, "contain")
}

func maskGradientLength(token string, borderBox, fontSize float64) float64 {
	length, ok := lengthBox(strings.TrimSpace(token), fontSize, borderBox, overflowAuto)
	if !ok || length <= 0 {
		return borderBox
	}

	return length
}

func maskLayerValue(value string, index int, initial string) string {
	values, ok := splitMaskLayerList(value)
	if !ok || len(values) == 0 {
		return initial
	}

	return strings.TrimSpace(values[index%len(values)])
}

func (e *engine) resolveMaskLayer(layer MaskLayer) MaskLayer {
	target := backgroundImageSrc(layer.Source)
	if target == "" {
		return layer
	}

	resourceURL, fragment, ok := splitMaskSourceURL(target)
	if !ok {
		return layer
	}

	if fragment != "" {
		return e.resolveSVGMaskLayer(layer, resourceURL, fragment)
	}

	return e.resolveRasterMaskLayer(layer, resourceURL)
}

func (e *engine) resolveSVGMaskLayer(layer MaskLayer, resourceURL, fragment string) MaskLayer {
	if resourceURL == "" {
		resourceURL = "#" + fragment
	}

	ref := e.resolveImage(resourceURL)
	if ref == nil || len(ref.sourceData) == 0 {
		return layer
	}

	maskBytes, width, height, err := svg.RasterizeMaskContext(
		e.rasterContext(), ref.sourceData, fragment, 0, 0, layer.Mode,
	)
	if err != nil {
		return layer
	}

	layer.ImageBytes = maskBytes
	layer.PixelWidth = width
	layer.PixelHeight = height
	layer.Preprocessed = true

	return layer
}

func (e *engine) resolveRasterMaskLayer(layer MaskLayer, resourceURL string) MaskLayer {
	ref := e.resolveImage(resourceURL)
	if ref == nil || len(ref.data) == 0 {
		return layer
	}

	layer.ImageBytes = ref.data
	layer.PixelWidth = ref.w
	layer.PixelHeight = ref.h

	if layer.Mode == maskModeMatchSource {
		// CSS image sources use their alpha channel for match-source.
		layer.Mode = maskModeAlpha
	}

	return layer
}

func splitMaskSourceURL(target string) (string, string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return "", "", false
	}

	fragment := parsed.Fragment
	parsed.Fragment = ""

	return parsed.String(), fragment, true
}
