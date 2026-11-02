package app

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	_ "image/gif"  // registers GIF with image.DecodeConfig
	_ "image/jpeg" // registers JPEG with image.DecodeConfig
	_ "image/png"  // registers PNG with image.DecodeConfig
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// maxImagePixels and maxImageSide match ownframe's own image budget, so
	// an image this app accepts cannot be rejected later by the page.
	maxImagePixels = 16 * 1024 * 1024
	maxImageSide   = 16384

	// An SVG without a usable width/height or viewBox uses the HTML default.
	svgDefaultWidth  = 300
	svgDefaultHeight = 150
)

// imageDoc holds the encoded bytes and intrinsic size of one image file.
type imageDoc struct {
	Bytes  []byte
	Src    string
	Format string // "PNG", "JPEG", "GIF", "SVG"
	Width  int    // natural pixel width
	Height int    // natural pixel height
}

// imageFormatOf maps a path extension to the image format name the viewer
// shows. Only formats the standard library and ownframe can decode count.
func imageFormatOf(path string) (format string, ok bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "PNG", true
	case ".jpg", ".jpeg":
		return "JPEG", true
	case ".gif":
		return "GIF", true
	case ".svg":
		return "SVG", true
	default:
		return "", false
	}
}

// isImagePath reports whether path names an image this viewer can open.
func isImagePath(path string) bool {
	_, ok := imageFormatOf(path)
	return ok
}

// imageSrcFor returns a stable page image key for path. The key must not
// contain ':' because ownframe reads a colon as a URL scheme.
func imageSrcFor(path string) string {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(path))

	return fmt.Sprintf("owncode-image-%x", hash.Sum64())
}

// fitImageSize scales a natural image size down to fit the available box.
// It never upscales and never returns a zero dimension for a valid input.
func fitImageSize(natW, natH, availW, availH int) (w, h int) {
	if natW <= 0 || natH <= 0 || availW <= 0 || availH <= 0 {
		return 0, 0
	}

	if natW <= availW && natH <= availH {
		return natW, natH
	}

	scale := math.Min(float64(availW)/float64(natW), float64(availH)/float64(natH))
	w = int(math.Floor(float64(natW) * scale))
	h = int(math.Floor(float64(natH) * scale))

	if w < 1 {
		w = 1
	}

	if h < 1 {
		h = 1
	}

	return w, h
}

// openImageDoc builds the image document for path. It skips the text-file
// size cap and binary probe because the encoded bytes go straight to the page.
func (w *workspace) openImageDoc(doc *fileDoc, path string) *fileDoc {
	doc.Lang = langImage

	if doc.ByteSize > maxImageBytes {
		doc.Err = fmt.Sprintf("%s is larger than %s", path, humanSize(maxImageBytes))

		return doc
	}

	raw, err := w.readFile(path)
	if err != nil {
		doc.Err = err.Error()

		return doc
	}

	if int64(len(raw)) > maxImageBytes {
		doc.Err = fmt.Sprintf("%s is larger than %s", path, humanSize(maxImageBytes))

		return doc
	}

	format, ok := imageFormatOf(path)
	if !ok {
		doc.Err = fmt.Sprintf("%s is not a supported image", path)

		return doc
	}

	width, height, format, err := imageSize(raw, format)
	if err != nil {
		doc.Err = err.Error()

		return doc
	}

	if err := checkImageDimensions(width, height); err != nil {
		doc.Err = err.Error()

		return doc
	}

	doc.Image = &imageDoc{
		Bytes:  raw,
		Src:    imageSrcFor(path),
		Format: format,
		Width:  width,
		Height: height,
	}

	return doc
}

// imageSize returns the intrinsic size of an encoded image. Raster formats
// use the registered standard-library decoders; SVG parses its root element.
func imageSize(raw []byte, fallbackFormat string) (width, height int, format string, err error) {
	if fallbackFormat == "SVG" {
		width, height, err = svgSize(raw)
		return width, height, fallbackFormat, err
	}

	cfg, name, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return 0, 0, fallbackFormat, fmt.Errorf("decode %s: %w", fallbackFormat, err)
	}

	return cfg.Width, cfg.Height, decodedImageFormat(name, fallbackFormat), nil
}

// decodedImageFormat maps image.DecodeConfig's name to the viewer's label.
func decodedImageFormat(name, fallback string) string {
	switch strings.ToLower(name) {
	case "png":
		return "PNG"
	case "jpeg":
		return "JPEG"
	case "gif":
		return "GIF"
	default:
		if name != "" {
			return strings.ToUpper(name)
		}

		return fallback
	}
}

// checkImageDimensions rejects dimensions the page cannot render.
func checkImageDimensions(width, height int) error {
	switch {
	case width <= 0 || height <= 0:
		return fmt.Errorf("image dimensions %dx%d are invalid", width, height)
	case width > maxImageSide || height > maxImageSide:
		return fmt.Errorf("image dimensions %dx%d exceed the %d px limit", width, height, maxImageSide)
	case int64(width)*int64(height) > maxImagePixels:
		return fmt.Errorf("image is %dx%d, over the %d megapixel limit", width, height, maxImagePixels>>20)
	default:
		return nil
	}
}

// svgSize reads the natural size of an SVG document. It prefers the root
// width/height, falls back to the viewBox, then to the 300x150 default.
func svgSize(raw []byte) (int, int, error) {
	var (
		width, height       int
		hasWidth, hasHeight bool
		viewW, viewH        int
		hasViewBox          bool
	)

	decoder := xml.NewDecoder(bytes.NewReader(raw))
	found := false

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return 0, 0, fmt.Errorf("parse SVG: %w", err)
		}

		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}

		found = true

		if !strings.EqualFold(start.Name.Local, "svg") {
			return 0, 0, fmt.Errorf("parse SVG: root element is <%s>, not <svg>", start.Name.Local)
		}

		for _, attr := range start.Attr {
			switch strings.ToLower(attr.Name.Local) {
			case "width":
				if value, ok := svgLength(attr.Value); ok {
					width, hasWidth = value, true
				}
			case "height":
				if value, ok := svgLength(attr.Value); ok {
					height, hasHeight = value, true
				}
			case "viewbox":
				if w, h, ok := svgViewBox(attr.Value); ok {
					viewW, viewH, hasViewBox = w, h, true
				}
			}
		}

		// CSS style declarations override the width/height attributes.
		for _, attr := range start.Attr {
			if !strings.EqualFold(attr.Name.Local, "style") {
				continue
			}

			if value, ok := svgStyleLength(attr.Value, "width"); ok {
				width, hasWidth = value, true
			}

			if value, ok := svgStyleLength(attr.Value, "height"); ok {
				height, hasHeight = value, true
			}
		}

		break
	}

	if !found {
		return 0, 0, errors.New("parse SVG: no <svg> root element")
	}

	if hasWidth && hasHeight {
		return width, height, nil
	}

	if hasViewBox {
		return viewW, viewH, nil
	}

	return svgDefaultWidth, svgDefaultHeight, nil
}

// svgLength parses one SVG length into CSS pixels. Percentages have no
// intrinsic meaning without a viewport, so they report false and the caller
// falls back to the viewBox or the default size.
func svgLength(value string) (int, bool) {
	text := strings.TrimSpace(value)
	if text == "" || strings.HasSuffix(text, "%") {
		return 0, false
	}

	lower := strings.ToLower(text)
	unit := ""

	for _, candidate := range []string{"px", "pt", "pc", "in", "cm", "mm"} {
		if strings.HasSuffix(lower, candidate) {
			unit = candidate
			text = strings.TrimSpace(text[:len(text)-len(candidate)])

			break
		}
	}

	number, err := strconv.ParseFloat(text, 64)
	if err != nil || number <= 0 {
		return 0, false
	}

	switch unit {
	case "pt":
		number *= 96.0 / 72.0
	case "pc":
		number *= 16
	case "in":
		number *= 96
	case "cm":
		number *= 96.0 / 2.54
	case "mm":
		number *= 96.0 / 25.4
	}

	px := int(math.Round(number))
	if px < 1 {
		px = 1
	}

	return px, true
}

// svgStyleLength returns one width or height declaration from an inline
// style attribute.
func svgStyleLength(style, property string) (int, bool) {
	for _, declaration := range strings.Split(style, ";") {
		name, value, ok := strings.Cut(declaration, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), property) {
			continue
		}

		return svgLength(value)
	}

	return 0, false
}

// svgViewBox returns the width and height from a viewBox attribute.
func svgViewBox(value string) (int, int, bool) {
	fields := strings.Fields(strings.ReplaceAll(value, ",", " "))
	if len(fields) != 4 {
		return 0, 0, false
	}

	width, errW := strconv.ParseFloat(fields[2], 64)
	height, errH := strconv.ParseFloat(fields[3], 64)
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}

	return int(math.Round(width)), int(math.Round(height)), true
}
