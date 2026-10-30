package svg

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"strings"
)

const (
	maskModeAlpha     = "alpha"
	maskModeLuminance = "luminance"
	maskModeMatch     = "match-source"
	maxMaskSide       = 4096
	colorChannelMax   = 255.0
	maskOpaqueChannel = 255
	luminanceRed      = 0.2126
	luminanceGreen    = 0.7152
	luminanceBlue     = 0.0722
	sRGBLinearLimit   = 0.04045
	sRGBLinearSlope   = 12.92
	sRGBOffset        = 0.055
	sRGBScale         = 1.055
	sRGBExponent      = 2.4
)

var (
	errMaskNotSVG       = errors.New("svg mask: not an SVG document")
	errMaskNotFound     = errors.New("svg mask: id not found")
	errMaskTooLarge     = errors.New("svg mask: input exceeds byte limit")
	errMaskEmptyID      = errors.New("svg mask: empty id")
	errMaskBadSize      = errors.New("svg mask: output dimensions must both be zero or positive")
	errMaskBadMode      = errors.New("svg mask: mode must be alpha, luminance, or match-source")
	errMaskBadStructure = errors.New("svg mask: malformed SVG document")
	errMaskBadPixel     = errors.New("svg mask: decoded pixel is not NRGBA")
)

type svgMaskPart struct {
	element *svgMaskElement
	token   xml.Token
}

type svgMaskElement struct {
	start    xml.StartElement
	children []svgMaskPart
}

type maskRasterInput struct {
	source     []byte
	mode       string
	colorSpace string
	width      int
	height     int
}

// RasterizeMask extracts one SVG <mask> by id, paints its child graphics into
// a PNG, and converts the pixels to an alpha mask. width and height are the
// requested logical pixel dimensions. Pass both as zero to use the SVG's
// intrinsic dimensions. mode is alpha, luminance, or match-source; match-source
// uses mask-type on the selected <mask>, whose default is luminance.
//
// The helper uses the SVG rasterizer's supported shape and paint subset. It
// does not apply SVG mask-region bounds, external stylesheet cascade, filters,
// animation, or nested mask references.
func RasterizeMask(data []byte, id string, width, height int, mode string) ([]byte, int, int, error) {
	// Tests and callers without a context use the background entry.
	return RasterizeMaskContext(context.Background(), data, id, width, height, mode)
}

// RasterizeMaskContext is RasterizeMask with the caller's context. The
// context stops the lock wait and prevents a draw from starting after cancel.
func RasterizeMaskContext(
	ctx context.Context, data []byte, id string, width, height int, mode string,
) ([]byte, int, int, error) {
	prepared, err := prepareMaskRaster(data, id, width, height, mode)
	if err != nil {
		return nil, 0, 0, err
	}

	maxSide := prepared.width
	if prepared.height > maxSide {
		maxSide = prepared.height
	}

	rasterPNG, rasterW, rasterH, err := RasterizeContext(ctx, prepared.source, maxSide)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("svg mask: rasterize contents: %w", err)
	}

	if rasterW <= 0 || rasterH <= 0 {
		return nil, 0, 0, errCanvasZeroPixel
	}

	raster, err := png.Decode(bytes.NewReader(rasterPNG))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("svg mask: decode raster: %w", err)
	}

	maskPNG, err := alphaMaskPNG(raster, prepared.mode, prepared.colorSpace)
	if err != nil {
		return nil, 0, 0, err
	}

	return maskPNG, rasterW, rasterH, nil
}

func prepareMaskRaster(data []byte, maskID string, width, height int, mode string) (maskRasterInput, error) {
	if err := validateMaskInput(data, maskID, width, height); err != nil {
		return maskRasterInput{}, err
	}

	root, mask, err := findMaskInDocument(data, maskID)
	if err != nil {
		return maskRasterInput{}, err
	}

	width, height, err = resolveMaskDimensions(data, width, height)
	if err != nil {
		return maskRasterInput{}, err
	}

	effectiveMode, err := svgMaskMode(mask, mode)
	if err != nil {
		return maskRasterInput{}, err
	}

	rasterSource, err := maskRasterSource(root, mask, width, height)
	if err != nil {
		return maskRasterInput{}, err
	}

	return maskRasterInput{
		source:     rasterSource,
		mode:       effectiveMode,
		colorSpace: svgMaskColorSpace(mask),
		width:      width,
		height:     height,
	}, nil
}

func validateMaskInput(data []byte, maskID string, width, height int) error {
	if len(data) > maxSVGBytes {
		return fmt.Errorf("%w: limit %d", errMaskTooLarge, maxSVGBytes)
	}

	if maskID == "" {
		return errMaskEmptyID
	}

	if width < 0 || height < 0 || (width == 0) != (height == 0) {
		return errMaskBadSize
	}

	return nil
}

func findMaskInDocument(data []byte, maskID string) (*svgMaskElement, *svgMaskElement, error) {
	root, err := parseSVGMaskDocument(data)
	if err != nil {
		return nil, nil, err
	}

	if root.start.Name.Local != "svg" {
		return nil, nil, errMaskNotSVG
	}

	mask := findSVGMask(root, maskID)
	if mask == nil {
		return nil, nil, fmt.Errorf("%w: %q", errMaskNotFound, maskID)
	}

	return root, mask, nil
}

func resolveMaskDimensions(data []byte, width, height int) (int, int, error) {
	if width == 0 {
		width, height = svgCSSPixelSize(data, maxMaskSide)
	}

	if width <= 0 || height <= 0 {
		return 0, 0, errMaskBadSize
	}

	return width, height, nil
}

// maxMaskDepth caps element nesting while parsing an SVG mask document, so
// adversarial or pathological documents cannot drive unbounded recursion in
// readSVGMaskElement.
const maxMaskDepth = 1024

func parseSVGMaskDocument(data []byte) (*svgMaskElement, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))

	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errMaskBadStructure, err)
		}

		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}

		return readSVGMaskElement(decoder, start, 0)
	}
}

func readSVGMaskElement(decoder *xml.Decoder, start xml.StartElement, depth int) (*svgMaskElement, error) {
	if depth > maxMaskDepth {
		return nil, errMaskBadStructure
	}

	element := &svgMaskElement{
		start:    start,
		children: []svgMaskPart{},
	}

	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errMaskBadStructure, err)
		}

		switch item := token.(type) {
		case xml.StartElement:
			child, err := readSVGMaskElement(decoder, item, depth+1)
			if err != nil {
				return nil, err
			}

			childPart := svgMaskPart{
				element: child,
				token:   nil,
			}
			element.children = append(element.children, childPart)
		case xml.EndElement:
			if item.Name != start.Name {
				return nil, errMaskBadStructure
			}

			return element, nil
		default:
			element.children = append(element.children, svgMaskPart{element: nil, token: token})
		}
	}
}

func findSVGMask(element *svgMaskElement, maskID string) *svgMaskElement {
	if element.start.Name.Local == "mask" && svgMaskAttribute(element, "id") == maskID {
		return element
	}

	for _, part := range element.children {
		if part.element == nil {
			continue
		}

		if mask := findSVGMask(part.element, maskID); mask != nil {
			return mask
		}
	}

	return nil
}

func maskRasterSource(root, mask *svgMaskElement, width, height int) ([]byte, error) {
	rootCopy := &svgMaskElement{
		start: xml.StartElement{
			Name: root.start.Name,
			Attr: append([]xml.Attr{}, root.start.Attr...),
		},
		children: []svgMaskPart{},
	}
	setSVGMaskAttribute(&rootCopy.start, "width", strconv.Itoa(width))
	setSVGMaskAttribute(&rootCopy.start, "height", strconv.Itoa(height))

	maskContentUnits := svgMaskAttribute(mask, "maskContentUnits")

	if strings.EqualFold(strings.TrimSpace(maskContentUnits), "objectBoundingBox") {
		setSVGMaskAttribute(&rootCopy.start, "viewBox", "0 0 1 1")
	}

	for _, part := range root.children {
		if part.element == nil {
			continue
		}

		elementName := part.element.start.Name.Local

		switch elementName {
		case "defs", "style":
			rootCopy.children = append(rootCopy.children, part)
		}
	}

	group := svgMaskGroup(mask)
	rootCopy.children = append(rootCopy.children, svgMaskPart{element: group, token: nil})

	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)

	if err := writeSVGMaskElement(encoder, rootCopy); err != nil {
		return nil, fmt.Errorf("svg mask: encode raster source: %w", err)
	}

	if err := encoder.Flush(); err != nil {
		return nil, fmt.Errorf("svg mask: encode raster source: %w", err)
	}

	return output.Bytes(), nil
}

func svgMaskGroup(mask *svgMaskElement) *svgMaskElement {
	group := &svgMaskElement{
		start: xml.StartElement{
			Name: xml.Name{Space: mask.start.Name.Space, Local: "g"},
			Attr: []xml.Attr{},
		},
		children: append([]svgMaskPart{}, mask.children...),
	}

	for _, attr := range mask.start.Attr {
		switch attr.Name.Local {
		case "id", "x", "y", "width", "height", "maskUnits", "maskContentUnits", "mask-type":
			continue
		}

		group.start.Attr = append(group.start.Attr, attr)
	}

	return group
}

func writeSVGMaskElement(encoder *xml.Encoder, element *svgMaskElement) error {
	if err := encoder.EncodeToken(element.start); err != nil {
		return fmt.Errorf("svg mask: encode start element: %w", err)
	}

	for _, part := range element.children {
		if part.element != nil {
			if err := writeSVGMaskElement(encoder, part.element); err != nil {
				return err
			}

			continue
		}

		if err := encoder.EncodeToken(part.token); err != nil {
			return fmt.Errorf("svg mask: encode token: %w", err)
		}
	}

	if err := encoder.EncodeToken(element.start.End()); err != nil {
		return fmt.Errorf("svg mask: encode end element: %w", err)
	}

	return nil
}

func svgMaskAttribute(element *svgMaskElement, name string) string {
	for _, attr := range element.start.Attr {
		if attr.Name.Local == name && attr.Name.Space == "" {
			return attr.Value
		}
	}

	return ""
}

func setSVGMaskAttribute(element *xml.StartElement, name, value string) {
	for idx := range element.Attr {
		if element.Attr[idx].Name.Local == name && element.Attr[idx].Name.Space == "" {
			element.Attr[idx].Value = value

			return
		}
	}

	element.Attr = append(element.Attr, xml.Attr{
		Name:  xml.Name{Space: "", Local: name},
		Value: value,
	})
}

func svgMaskMode(mask *svgMaskElement, requested string) (string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		requested = maskModeMatch
	}

	if requested == maskModeMatch {
		requested = strings.ToLower(strings.TrimSpace(svgMaskProperty(mask, "mask-type")))
		if requested != maskModeAlpha && requested != maskModeLuminance {
			requested = maskModeLuminance
		}
	} else if requested != maskModeAlpha && requested != maskModeLuminance {
		return "", errMaskBadMode
	}

	return requested, nil
}

func svgMaskColorSpace(mask *svgMaskElement) string {
	value := strings.ToLower(strings.TrimSpace(svgMaskProperty(mask, "color-interpolation")))
	if value == "linearrgb" {
		return value
	}

	return "srgb"
}

func svgMaskProperty(mask *svgMaskElement, property string) string {
	value := svgMaskAttribute(mask, property)

	style := svgMaskAttribute(mask, "style")
	for _, declaration := range strings.Split(style, ";") {
		name, item, ok := strings.Cut(declaration, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), property) {
			value = strings.TrimSpace(item)
		}
	}

	return value
}

func alphaMaskPNG(source image.Image, mode, colorSpace string) ([]byte, error) {
	bounds := source.Bounds()
	mask := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))

	for pixelY := range bounds.Dy() {
		for pixelX := range bounds.Dx() {
			pixel, ok := color.NRGBAModel.Convert(source.At(bounds.Min.X+pixelX, bounds.Min.Y+pixelY)).(color.NRGBA)
			if !ok {
				return nil, errMaskBadPixel
			}

			alpha := maskPixelAlpha(pixel, mode, colorSpace)
			mask.SetNRGBA(pixelX, pixelY, color.NRGBA{
				R: maskOpaqueChannel,
				G: maskOpaqueChannel,
				B: maskOpaqueChannel,
				A: alpha,
			})
		}
	}

	var output bytes.Buffer
	if err := encodeRasterPNG(&output, mask, bounds.Dx(), bounds.Dy()); err != nil {
		return nil, fmt.Errorf("svg mask: encode alpha image: %w", err)
	}

	return output.Bytes(), nil
}

func maskPixelAlpha(pixel color.NRGBA, mode, colorSpace string) uint8 {
	if mode != maskModeLuminance {
		return pixel.A
	}

	red := float64(pixel.R) / colorChannelMax
	green := float64(pixel.G) / colorChannelMax
	blue := float64(pixel.B) / colorChannelMax

	if colorSpace == "linearrgb" {
		red = srgbToLinear(red)
		green = srgbToLinear(green)
		blue = srgbToLinear(blue)
	}

	luminance := luminanceRed*red + luminanceGreen*green + luminanceBlue*blue

	return uint8(math.Round(luminance * float64(pixel.A)))
}

func srgbToLinear(value float64) float64 {
	if value <= sRGBLinearLimit {
		return value / sRGBLinearSlope
	}

	return math.Pow((value+sRGBOffset)/sRGBScale, sRGBExponent)
}
