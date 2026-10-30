package layout

import (
	"image"
	"image/draw"
	"math"
	"strings"
)

const (
	objectFitFill      = "fill"
	objectFitScaleDown = "scale-down"
)

func replacedIntrinsicPt(eng *engine, sty ResolvedStyle, ref *imageRef) (float64, float64) {
	if ref == nil || ref.w <= 0 || ref.h <= 0 {
		return 0, 0
	}

	scale := imageResolutionScale(sty, ref)
	width := pxToPt(float64(ref.w) * scale)
	height := pxToPt(float64(ref.h) * scale)

	if eng != nil {
		return eng.scalePt(width), eng.scalePt(height)
	}

	return width, height
}

// objectFitPaintRect sizes and positions the replaced image inside its content
// box per object-fit / object-position. Returns the paint rect and whether the
// image must be clipped to the content box (cover / none / scale-down overflow).
func objectFitPaintRect(
	fit string, posX, posY string,
	boxX, boxY, boxW, boxH, intrinsicW, intrinsicH float64,
) (float64, float64, float64, float64, bool) {
	fit = strings.ToLower(strings.TrimSpace(fit))
	if fit == "" {
		fit = objectFitFill
	}

	imgW, imgH := objectFitSize(fit, boxW, boxH, intrinsicW, intrinsicH)
	imgX, imgY := resolveBackgroundPosition(posX, posY, boxX, boxY, boxW, boxH, imgW, imgH)
	clip := fit == "cover" || fit == "none" || fit == objectFitScaleDown

	return imgX, imgY, imgW, imgH, clip && (imgW > boxW+0.01 || imgH > boxH+0.01 ||
		imgX < boxX-0.01 || imgY < boxY-0.01 ||
		imgX+imgW > boxX+boxW+0.01 || imgY+imgH > boxY+boxH+0.01)
}

//nolint:cyclop // object-fit has separate contain, cover, none, and scale-down rules
func objectFitSize(fit string, boxW, boxH, intrinsicW, intrinsicH float64) (float64, float64) {
	switch fit {
	case "contain":
		return objectFitScale(boxW, boxH, intrinsicW, intrinsicH, false)
	case "cover":
		return objectFitScale(boxW, boxH, intrinsicW, intrinsicH, true)
	case "none":
		if intrinsicW > 0 && intrinsicH > 0 {
			return intrinsicW, intrinsicH
		}

		return boxW, boxH
	case objectFitScaleDown:
		noneW, noneH := intrinsicW, intrinsicH
		if noneW <= 0 || noneH <= 0 {
			return boxW, boxH
		}

		contW, contH := objectFitScale(boxW, boxH, intrinsicW, intrinsicH, false)
		if noneW <= contW && noneH <= contH {
			return noneW, noneH
		}

		return contW, contH
	default: // fill
		return boxW, boxH
	}
}

// objectFitPaintForImage prepares one replaced bitmap for painting: it applies
// the style filter chain, sizes the payload per object-fit / object-position,
// crops it to the visible region when the fitted rect overflows the content
// box, and masks it with the element's clip-path. boxX/boxY/boxW/boxH is the
// content box; clipX/clipY/clipW/clipH is the clip-path reference box. A fully
// hidden image comes back with a zero-size rect, and the caller emits no op.
// isJPEG reports whether the returned bytes are still the original JPEG
// payload; isJPEG flips to false whenever the bytes are re-encoded.
func (e *engine) objectFitPaintForImage(
	ref *imageRef, data []byte, isJPEG bool, sty ResolvedStyle,
	boxX, boxY, boxW, boxH, clipX, clipY, clipW, clipH float64,
) ([]byte, bool, int, int, float64, float64, float64, float64) {
	if sty.Filter != "" {
		filters := parseFilterList(sty.Filter, sty.Color, sty.FontSize)
		data = applyImageFilterToImageWithInterp(data, filters, sty.ColorInterpolationFilters)
		isJPEG = false
	}

	intrinsicW, intrinsicH := replacedIntrinsicPt(e, sty, ref)
	fitX, fitY, fitW, fitH, clip := objectFitPaintRect(
		sty.ObjectFit, sty.ObjectPositionX, sty.ObjectPositionY,
		boxX, boxY, boxW, boxH, intrinsicW, intrinsicH,
	)
	pixW, pixH := ref.w, ref.h

	if clip {
		var cropped bool

		data, pixW, pixH, fitX, fitY, fitW, fitH, cropped = objectFitImagePaint(
			ref, data, fitX, fitY, fitW, fitH, boxX, boxY, boxW, boxH,
		)
		if fitW <= 0 || fitH <= 0 {
			return nil, false, 0, 0, 0, 0, 0, 0
		}

		if cropped {
			isJPEG = false
		}
	}

	data, isJPEG = applyPaintClipPathMask(
		sty, data, isJPEG, fitX, fitY, fitW, fitH, clipX, clipY, clipW, clipH,
	)

	return data, isJPEG, pixW, pixH, fitX, fitY, fitW, fitH
}

// objectFitImagePaint sizes one replaced bitmap per object-fit /
// object-position and returns the payload, its pixel size, and the canvas
// rect to draw. When the fitted rect overflows the content box (cover, or
// none / scale-down pushed outside by object-position), the source pixels of
// the visible sub-rect are cropped and re-encoded as PNG so the emitted op is
// exactly the painted region; cropped then reports the re-encode. The display
// list carries no clip entry for an image: an op spilling past its box paints
// over sibling content and, in a consumer's paginator, a crossing op drags
// its whole band across the page boundary. The re-encode is the same
// one-payload fallback image-orientation and object-view-box already use.
// An undecodable payload keeps its fitted rect (cropped false), and a fully
// hidden image returns a zero-size rect.
func objectFitImagePaint(
	ref *imageRef, data []byte,
	fitX, fitY, fitW, fitH,
	boxX, boxY, boxW, boxH float64,
) ([]byte, int, int, float64, float64, float64, float64, bool) {
	if ref == nil || len(data) == 0 {
		return data, 0, 0, boxX, boxY, 0, 0, false
	}

	if fitX >= boxX && fitY >= boxY && fitX+fitW <= boxX+boxW && fitY+fitH <= boxY+boxH {
		return data, ref.w, ref.h, fitX, fitY, fitW, fitH, false
	}

	vx0 := math.Max(fitX, boxX)
	vy0 := math.Max(fitY, boxY)
	vx1 := math.Min(fitX+fitW, boxX+boxW)
	vy1 := math.Min(fitY+fitH, boxY+boxH)

	if vx1-vx0 <= clipPointTolerance || vy1-vy0 <= clipPointTolerance {
		return nil, 0, 0, vx0, vy0, 0, 0, false
	}

	// The fitted rect maps the whole bitmap onto (fitX, fitY, fitW, fitH), so
	// each source pixel is fitW/ref.w canvas points wide.
	src := image.Rect(
		int(math.Round((vx0-fitX)*float64(ref.w)/fitW)),
		int(math.Round((vy0-fitY)*float64(ref.h)/fitH)),
		int(math.Round((vx1-fitX)*float64(ref.w)/fitW)),
		int(math.Round((vy1-fitY)*float64(ref.h)/fitH)),
	)

	cropped, cw, ch, ok := cropImageRect(data, src)
	if !ok {
		return data, ref.w, ref.h, fitX, fitY, fitW, fitH, false
	}

	return cropped, cw, ch, vx0, vy0, vx1 - vx0, vy1 - vy0, true
}

// cropImageRect crops encoded image bytes to src (clamped to the decoded
// bounds) and re-encodes the result as PNG. ok is false when the bytes do not
// decode or the intersection is empty; callers then keep the uncropped paint.
func cropImageRect(data []byte, src image.Rectangle) ([]byte, int, int, bool) {
	if src.Dx() <= 0 || src.Dy() <= 0 {
		return nil, 0, 0, false
	}

	decoded, err := decodeImageBytes(data)
	if err != nil {
		return nil, 0, 0, false
	}

	src = src.Intersect(decoded.Bounds())
	if src.Empty() {
		return nil, 0, 0, false
	}

	cropped := image.NewRGBA(image.Rect(0, 0, src.Dx(), src.Dy()))
	draw.Draw(cropped, cropped.Bounds(), decoded, src.Min, draw.Src)

	out, err := encodePNGImage(cropped)
	if err != nil {
		return nil, 0, 0, false
	}

	return out, src.Dx(), src.Dy(), true
}

func objectFitScale(boxW, boxH, intrinsicW, intrinsicH float64, cover bool) (float64, float64) {
	if intrinsicW <= 0 || intrinsicH <= 0 || boxW <= 0 || boxH <= 0 {
		return boxW, boxH
	}

	scaleX := boxW / intrinsicW
	scaleY := boxH / intrinsicH
	scale := scaleX

	if cover {
		if scaleY > scaleX {
			scale = scaleY
		}
	} else if scaleY < scaleX {
		scale = scaleY
	}

	return intrinsicW * scale, intrinsicH * scale
}
