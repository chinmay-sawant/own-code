//nolint:varnamelen,funlen,cyclop,mnd,wsl,intrange,nlreturn,goconst,dupl,unparam,exhaustive // filter
package layout

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"strconv"
	"strings"
)

// errImageDecodeBudget rejects a decode whose header exceeds the pixel budget.
// DecodeConfig reads the header only, so the full bitmap is never allocated.
var errImageDecodeBudget = errors.New("layout: image exceeds the pixel budget")

// filterKind names one CSS filter function. filterUnknown is the zero value:
// a parsedFilter that was never assigned a kind is not a valid filter, so
// every producer (parseFilterList) assigns an explicit member.
type filterKind int

const (
	filterUnknown filterKind = iota
	filterBlur
	filterOpacity
	filterDropShadow
	filterGrayscale
	filterBrightness
	filterContrast
	filterInvert
	filterSepia
	filterSaturate
	filterHueRotate
)

type parsedFilter struct {
	kind       filterKind
	val        float64 // opacity, radius, amount, angle
	dropShadow parsedBoxShadow
}

func parseFilterList(value string, current [3]float64, fsize float64) []parsedFilter {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, cssDisplayNone) {
		return nil
	}

	var filters []parsedFilter
	rest := value

	for {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			break
		}

		name, args, next, ok := splitTransformFunc(rest)
		if !ok {
			break
		}

		lowName := strings.ToLower(name)
		args = strings.TrimSpace(args)

		switch lowName {
		case "blur":
			if r, ok := plainLength(args, fsize, 0); ok && r >= 0 {
				filters = append(filters, parsedFilter{kind: filterBlur, val: r}) //nolint:exhaustruct
			}
		case "opacity":
			if op, ok := parseOpacityValue(args); ok {
				filters = append(filters, parsedFilter{kind: filterOpacity, val: op}) //nolint:exhaustruct
			}
		case "drop-shadow":
			if shadow, ok := parseBoxShadowLayer(args, current, fsize); ok {
				filters = append(filters, parsedFilter{kind: filterDropShadow, dropShadow: shadow}) //nolint:exhaustruct
			}
		case "grayscale":
			v := parseFilterAmount(args, 1.0)
			filters = append(filters, parsedFilter{kind: filterGrayscale, val: v}) //nolint:exhaustruct
		case "brightness":
			v := parseFilterAmount(args, 1.0)
			filters = append(filters, parsedFilter{kind: filterBrightness, val: v}) //nolint:exhaustruct
		case "contrast":
			v := parseFilterAmount(args, 1.0)
			filters = append(filters, parsedFilter{kind: filterContrast, val: v}) //nolint:exhaustruct
		case "invert":
			v := parseFilterAmount(args, 1.0)
			filters = append(filters, parsedFilter{kind: filterInvert, val: v}) //nolint:exhaustruct
		case "sepia":
			v := parseFilterAmount(args, 1.0)
			filters = append(filters, parsedFilter{kind: filterSepia, val: v}) //nolint:exhaustruct
		case "saturate":
			v := parseFilterAmount(args, 1.0)
			filters = append(filters, parsedFilter{kind: filterSaturate, val: v}) //nolint:exhaustruct
		case "hue-rotate":
			if deg, ok := parseAngleDeg(args); ok {
				filters = append(filters, parsedFilter{kind: filterHueRotate, val: deg}) //nolint:exhaustruct
			}
		}

		rest = next
	}

	return filters
}

func parseFilterAmount(args string, defaultVal float64) float64 {
	args = strings.TrimSpace(args)
	if args == "" {
		return defaultVal
	}
	if strings.HasSuffix(args, "%") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(args, "%"), 64); err == nil {
			return v / 100.0
		}
	}
	if v, err := strconv.ParseFloat(args, 64); err == nil {
		return v
	}
	return defaultVal
}

// buildGaussianKernel generates a 1D normalized Gaussian convolution kernel.
func buildGaussianKernel(radius float64) []float64 {
	sigma := math.Max(radius*0.57735, 0.5)
	kRadius := int(math.Ceil(3 * sigma))
	if kRadius > 25 {
		kRadius = 25
	}
	if kRadius < 1 {
		kRadius = 1
	}

	size := 2*kRadius + 1
	kernel := make([]float64, size)
	twoSigmaSq := 2 * sigma * sigma
	sum := 0.0

	for i := -kRadius; i <= kRadius; i++ {
		w := math.Exp(-float64(i*i) / twoSigmaSq)
		kernel[i+kRadius] = w
		sum += w
	}

	for i := range kernel {
		kernel[i] /= sum
	}

	return kernel
}

// filterUsesLinearLight reports whether a color-interpolation-filters
// selector means the linear-light working space. Only the linearRGB spellings
// qualify; srgb, auto, empty, and anything unrecognized run primitives in
// gamma space so historic output is unchanged. Reference: Chrome 143.0.7499.40.
func filterUsesLinearLight(interp string) bool {
	normalized := strings.ToLower(strings.TrimSpace(interp))
	normalized = strings.ReplaceAll(normalized, "-", "")
	normalized = strings.ReplaceAll(normalized, " ", "")

	return normalized == "linearrgb"
}

// applyGaussianBlur performs a 2D separable Gaussian blur on an RGBA image.
func applyGaussianBlur(src *image.NRGBA, radius float64) *image.NRGBA {
	return applyGaussianBlurInSpace(src, radius, false)
}

// applyGaussianBlurInSpace blurs in gamma space (linear=false, the historic
// path) or in linear light with an sRGB round-trip (linear=true).
func applyGaussianBlurInSpace(src *image.NRGBA, radius float64, linear bool) *image.NRGBA {
	if radius <= 0 || radius > 20 || src == nil {
		return src
	}

	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 1 && h <= 1 {
		return src
	}

	kernel := buildGaussianKernel(radius)
	kRadius := (len(kernel) - 1) / 2

	if linear {
		return blurInLinearLight(src, kernel, kRadius, w, h, bounds)
	}

	// Horizontal pass
	temp := image.NewNRGBA(bounds)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var rSum, gSum, bSum, aSum float64
			for k := -kRadius; k <= kRadius; k++ {
				ix := x + k
				if ix < 0 {
					ix = 0
				} else if ix >= w {
					ix = w - 1
				}
				c := src.NRGBAAt(ix, y)
				weight := kernel[k+kRadius]
				rSum += float64(c.R) * weight
				gSum += float64(c.G) * weight
				bSum += float64(c.B) * weight
				aSum += float64(c.A) * weight
			}
			temp.SetNRGBA(x, y, color.NRGBA{
				R: uint8(math.Round(clamp01(rSum/255.0) * 255.0)),
				G: uint8(math.Round(clamp01(gSum/255.0) * 255.0)),
				B: uint8(math.Round(clamp01(bSum/255.0) * 255.0)),
				A: uint8(math.Round(clamp01(aSum/255.0) * 255.0)),
			})
		}
	}

	// Vertical pass
	dst := image.NewNRGBA(bounds)
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			var rSum, gSum, bSum, aSum float64
			for k := -kRadius; k <= kRadius; k++ {
				iy := y + k
				if iy < 0 {
					iy = 0
				} else if iy >= h {
					iy = h - 1
				}
				c := temp.NRGBAAt(x, iy)
				weight := kernel[k+kRadius]
				rSum += float64(c.R) * weight
				gSum += float64(c.G) * weight
				bSum += float64(c.B) * weight
				aSum += float64(c.A) * weight
			}
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(math.Round(clamp01(rSum/255.0) * 255.0)),
				G: uint8(math.Round(clamp01(gSum/255.0) * 255.0)),
				B: uint8(math.Round(clamp01(bSum/255.0) * 255.0)),
				A: uint8(math.Round(clamp01(aSum/255.0) * 255.0)),
			})
		}
	}

	return dst
}

// blurInLinearLight convolves in linear light: pixels decode to linear once,
// both separable passes average linear values (alpha averages directly), and
// the result encodes back to sRGB. Averaging gamma values instead darkens
// mid-tones, so the two spaces observably differ on edges.
func blurInLinearLight(src *image.NRGBA, kernel []float64, kRadius, w, h int, bounds image.Rectangle) *image.NRGBA {
	linR := make([]float64, w*h)
	linG := make([]float64, w*h)
	linB := make([]float64, w*h)
	alpha := make([]float64, w*h)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.NRGBAAt(x, y)
			i := y*w + x
			linR[i] = srgbChannelToLinear(float64(c.R) / 255.0)
			linG[i] = srgbChannelToLinear(float64(c.G) / 255.0)
			linB[i] = srgbChannelToLinear(float64(c.B) / 255.0)
			alpha[i] = float64(c.A)
		}
	}

	tmpR := make([]float64, w*h)
	tmpG := make([]float64, w*h)
	tmpB := make([]float64, w*h)
	tmpA := make([]float64, w*h)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var rSum, gSum, bSum, aSum float64
			for k := -kRadius; k <= kRadius; k++ {
				ix := x + k
				if ix < 0 {
					ix = 0
				} else if ix >= w {
					ix = w - 1
				}
				j := y*w + ix
				weight := kernel[k+kRadius]
				rSum += linR[j] * weight
				gSum += linG[j] * weight
				bSum += linB[j] * weight
				aSum += alpha[j] * weight
			}
			i := y*w + x
			tmpR[i], tmpG[i], tmpB[i], tmpA[i] = rSum, gSum, bSum, aSum
		}
	}

	dst := image.NewNRGBA(bounds)
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			var rSum, gSum, bSum, aSum float64
			for k := -kRadius; k <= kRadius; k++ {
				iy := y + k
				if iy < 0 {
					iy = 0
				} else if iy >= h {
					iy = h - 1
				}
				j := iy*w + x
				weight := kernel[k+kRadius]
				rSum += tmpR[j] * weight
				gSum += tmpG[j] * weight
				bSum += tmpB[j] * weight
				aSum += tmpA[j] * weight
			}
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(math.Round(clamp01(linearChannelToSRGB(rSum)) * 255.0)),
				G: uint8(math.Round(clamp01(linearChannelToSRGB(gSum)) * 255.0)),
				B: uint8(math.Round(clamp01(linearChannelToSRGB(bSum)) * 255.0)),
				A: uint8(math.Round(clamp01(aSum/255.0) * 255.0)),
			})
		}
	}

	return dst
}

func toNRGBA(img image.Image) *image.NRGBA {
	if nrgba, ok := img.(*image.NRGBA); ok {
		return nrgba
	}
	bounds := img.Bounds()
	nrgba := image.NewNRGBA(bounds)
	draw.Draw(nrgba, bounds, img, bounds.Min, draw.Src)
	return nrgba
}

// imageHeaderWithinBudget reads only the format header. GIF is included
// because image.DecodeConfig uses the decoders registered by this package.
func imageHeaderWithinBudget(imgBytes []byte) bool {
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(imgBytes)); err == nil {
		return validImageSize(cfg.Width, cfg.Height)
	}

	if cfg, err := png.DecodeConfig(bytes.NewReader(imgBytes)); err == nil {
		return validImageSize(cfg.Width, cfg.Height)
	}

	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(imgBytes)); err == nil {
		return validImageSize(cfg.Width, cfg.Height)
	}

	return false
}

// decodeImageBytes decodes PNG, JPEG, or GIF bytes, retrying with the
// explicit decoders when the sniffed format probe fails. The header budget
// runs first so a huge logical size cannot allocate a bitmap.
func decodeImageBytes(imgBytes []byte) (image.Image, error) {
	if !imageHeaderWithinBudget(imgBytes) {
		return nil, errImageDecodeBudget
	}

	if srcImg, _, err := image.Decode(bytes.NewReader(imgBytes)); err == nil {
		return srcImg, nil
	}

	// Try jpeg/png explicitly.
	if srcImg, err := png.Decode(bytes.NewReader(imgBytes)); err == nil {
		return srcImg, nil
	}

	decoded, err := jpeg.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, fmt.Errorf("layout: image decode: %w", err)
	}

	return decoded, nil
}

// applyImageFilterToImage decodes PNG/JPEG, applies filters (blur, grayscale, etc.), and returns PNG bytes.
func applyImageFilterToImage(imgBytes []byte, filters []parsedFilter) []byte {
	return applyImageFilterToImageWithInterp(imgBytes, filters, "srgb")
}

// applyImageFilterToImageWithInterp is applyImageFilterToImage with a working
// space selector: "linearrgb" (or "linear-rgb") runs blur, grayscale, and
// invert in linear light with an sRGB round-trip, every other value runs them
// in gamma space directly. Opacity stays at the display-list level in both
// spaces. Reference: Chrome 143.0.7499.40.
func applyImageFilterToImageWithInterp(imgBytes []byte, filters []parsedFilter, interp string) []byte {
	if len(imgBytes) == 0 || len(filters) == 0 {
		return imgBytes
	}

	hasEffect := false
	for _, f := range filters {
		if f.kind != filterOpacity && f.kind != filterUnknown {
			hasEffect = true
			break
		}
	}
	if !hasEffect {
		return imgBytes
	}

	srcImg, err := decodeImageBytes(imgBytes)
	if err != nil {
		return imgBytes
	}

	nrgba := toNRGBA(srcImg)
	bounds := nrgba.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	linear := filterUsesLinearLight(interp)

	for _, f := range filters {
		switch f.kind {
		case filterUnknown:
			// Zero-value filter: never produced by parseFilterList; no-op.
		case filterBlur:
			if linear {
				nrgba = applyGaussianBlurInSpace(nrgba, f.val, true)
			} else {
				nrgba = applyGaussianBlur(nrgba, f.val)
			}
		case filterGrayscale:
			applyGrayscaleInSpace(nrgba, w, h, clamp01(f.val), linear)
		case filterInvert:
			applyInvertInSpace(nrgba, w, h, clamp01(f.val), linear)
		case filterOpacity:
			// Handled at the display-list level via style.Opacity and
			// PaintOpacity. Scaling pixel alpha here would double-multiply
			// opacity on images.
		}
	}

	var out bytes.Buffer
	if err := png.Encode(&out, nrgba); err != nil {
		return imgBytes
	}
	return out.Bytes()
}

// applyGrayscaleInSpace mixes each pixel toward its luminance. In gamma space
// the weights apply to encoded channels (the historic path); in linear light
// the channels decode first, mix there, and encode back to sRGB.
func applyGrayscaleInSpace(nrgba *image.NRGBA, w, h int, amount float64, linear bool) {
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := nrgba.NRGBAAt(x, y)
			if !linear {
				gray := 0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)
				r := float64(c.R)*(1-amount) + gray*amount
				g := float64(c.G)*(1-amount) + gray*amount
				b := float64(c.B)*(1-amount) + gray*amount
				nrgba.SetNRGBA(x, y, color.NRGBA{
					R: uint8(math.Round(r)),
					G: uint8(math.Round(g)),
					B: uint8(math.Round(b)),
					A: c.A,
				})

				continue
			}
			lr := srgbChannelToLinear(float64(c.R) / 255.0)
			lg := srgbChannelToLinear(float64(c.G) / 255.0)
			lb := srgbChannelToLinear(float64(c.B) / 255.0)
			grayLin := 0.2126*lr + 0.7152*lg + 0.0722*lb
			mixR := lr*(1-amount) + grayLin*amount
			mixG := lg*(1-amount) + grayLin*amount
			mixB := lb*(1-amount) + grayLin*amount
			nrgba.SetNRGBA(x, y, color.NRGBA{
				R: uint8(math.Round(clamp01(linearChannelToSRGB(mixR)) * 255.0)),
				G: uint8(math.Round(clamp01(linearChannelToSRGB(mixG)) * 255.0)),
				B: uint8(math.Round(clamp01(linearChannelToSRGB(mixB)) * 255.0)),
				A: c.A,
			})
		}
	}
}

// applyInvertInSpace mixes each pixel toward its inverse. In gamma space the
// complement applies to encoded channels (the historic path); in linear light
// the channels decode first, complement there, and encode back to sRGB.
func applyInvertInSpace(nrgba *image.NRGBA, w, h int, amount float64, linear bool) {
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := nrgba.NRGBAAt(x, y)
			if !linear {
				invR := 255.0 - float64(c.R)
				invG := 255.0 - float64(c.G)
				invB := 255.0 - float64(c.B)
				r := float64(c.R)*(1-amount) + invR*amount
				g := float64(c.G)*(1-amount) + invG*amount
				b := float64(c.B)*(1-amount) + invB*amount
				nrgba.SetNRGBA(x, y, color.NRGBA{
					R: uint8(math.Round(r)),
					G: uint8(math.Round(g)),
					B: uint8(math.Round(b)),
					A: c.A,
				})

				continue
			}
			lr := srgbChannelToLinear(float64(c.R) / 255.0)
			lg := srgbChannelToLinear(float64(c.G) / 255.0)
			lb := srgbChannelToLinear(float64(c.B) / 255.0)
			mixR := lr*(1-amount) + (1-lr)*amount
			mixG := lg*(1-amount) + (1-lg)*amount
			mixB := lb*(1-amount) + (1-lb)*amount
			nrgba.SetNRGBA(x, y, color.NRGBA{
				R: uint8(math.Round(clamp01(linearChannelToSRGB(mixR)) * 255.0)),
				G: uint8(math.Round(clamp01(linearChannelToSRGB(mixG)) * 255.0)),
				B: uint8(math.Round(clamp01(linearChannelToSRGB(mixB)) * 255.0)),
				A: c.A,
			})
		}
	}
}
