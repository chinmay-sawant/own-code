package layout

import (
	pdf "github.com/chinmay-sawant/blinkless/internal/fonts"
)

// faceStyleKey is the faceFor cache key for one CSS face identity, including
// variation axes so instanced faces are not reused across settings.
type faceStyleKey struct {
	famHash     uint64
	weight      int
	italic      bool
	optical     string
	variations  string
	opticalSize uint64
}

// faceRuneKey is the faceForRune fallback cache key for one (style face
// identity, rune). famHash is FNV-1a over FontFamily tokens (no Join alloc).
type faceRuneKey struct {
	famHash     uint64
	weight      int
	italic      bool
	r           rune
	optical     string
	variations  string
	opticalSize uint64
}

// faceFor selects the TrueType face for a resolved style (bold/italic),
// preferring CSS font-family matches from the opt-in registry, then the
// bundled Liberation FaceSet.
func (e *engine) faceFor(sty *ResolvedStyle) *pdf.Font {
	if sty == nil {
		return e.font
	}

	optical, variations, sizeBits := variationCacheBits(sty)
	key := faceStyleKey{
		famHash:     sty.famHash,
		weight:      sty.FontWeight,
		italic:      sty.FontItalic,
		optical:     optical,
		variations:  variations,
		opticalSize: sizeBits,
	}

	if e.hasLastFace && e.lastFaceKey == key && e.lastFace != nil {
		return e.lastFace
	}

	if e.faceByStyle != nil {
		if f, ok := e.faceByStyle[key]; ok {
			e.lastFaceKey, e.lastFace, e.hasLastFace = key, f, true

			return f
		}
	}

	face := e.lookupFaceFor(sty)

	if e.faceByStyle == nil {
		e.faceByStyle = make(map[faceStyleKey]*pdf.Font)
	}

	e.faceByStyle[key] = face
	e.lastFaceKey, e.lastFace, e.hasLastFace = key, face, true

	return face
}

// lookupFaceFor is the uncached faceFor path.
func (e *engine) lookupFaceFor(sty *ResolvedStyle) *pdf.Font {
	if sty == nil {
		return e.font
	}

	return resolveFontVariants(sty, e.lookupBaseFaceFor(sty))
}

// lookupBaseFaceFor resolves the CSS family/weight/italic face without the
// font-variation family consumer.
func (e *engine) lookupBaseFaceFor(sty *ResolvedStyle) *pdf.Font {
	if e.registry != nil {
		if f := e.registry.Lookup(sty.FontFamily, sty.FontWeight, sty.FontItalic); f != nil {
			return f
		}
	}

	if e.faces != nil {
		if f := e.faces.ResolveFamily(sty.FontFamily, sty.FontWeight, sty.FontItalic); f != nil {
			return f
		}

		if f := e.faces.Resolve(sty.FontWeight, sty.FontItalic); f != nil {
			return f
		}
	}

	return e.font
}

// faceForRune picks the first CSS font-family face (then defaults) that has a
// glyph for r — browser-like fallback so Hangul/Latin/CJK can come from
// different faces in one run.
//
// Fast path: when the primary face covers r (common for Latin/report text),
// return it without a map lookup. Fallback faces are cached under a hash key
// that does not allocate a joined family string.
func (e *engine) faceForRune(sty *ResolvedStyle, runeValue rune) *pdf.Font {
	if sty == nil {
		return e.font
	}

	primary := e.faceFor(sty)

	if isRuneWhitespace(runeValue) {
		return primary
	}

	if primary != nil && primary.GlyphID(runeValue) != 0 {
		return primary
	}

	return e.faceForRuneFallback(sty, runeValue, primary)
}

// faceForRuneFallback resolves and caches a non-primary face for a missing glyph.
func (e *engine) faceForRuneFallback(sty *ResolvedStyle, runeValue rune, primary *pdf.Font) *pdf.Font {
	if sty == nil {
		return primary
	}

	optical, variations, sizeBits := variationCacheBits(sty)
	key := faceRuneKey{
		famHash:     sty.famHash,
		weight:      sty.FontWeight,
		italic:      sty.FontItalic,
		r:           runeValue,
		optical:     optical,
		variations:  variations,
		opticalSize: sizeBits,
	}

	if e.faceByRune != nil {
		if f, ok := e.faceByRune[key]; ok {
			return f
		}
	}

	face := e.lookupFaceForRune(sty, runeValue)
	if face == nil {
		face = primary
	}

	if e.faceByRune == nil {
		e.faceByRune = make(map[faceRuneKey]*pdf.Font)
	}

	e.faceByRune[key] = face

	return face
}

// lookupFaceForRune is the uncached face resolution path.
func (e *engine) lookupFaceForRune(sty *ResolvedStyle, runeValue rune) *pdf.Font {
	if sty == nil {
		return e.font
	}

	return resolveFontVariants(sty, e.lookupBaseFaceForRune(sty, runeValue))
}

// lookupBaseFaceForRune resolves a per-rune fallback face without instancing.
func (e *engine) lookupBaseFaceForRune(sty *ResolvedStyle, runeValue rune) *pdf.Font {
	if f := e.registryFamilyWithGlyph(sty, runeValue); f != nil {
		return f
	}

	if f := e.facesWithGlyph(sty, runeValue); f != nil {
		return f
	}

	if e.font != nil && e.font.GlyphID(runeValue) != 0 {
		return e.font
	}

	// Last resort: any opt-in registry face that covers this codepoint
	// (DejaVu/Noto when --font-path / --use-system-fonts scanned them).
	if f := e.registryGlyphFallback(sty, runeValue); f != nil {
		return f
	}

	return e.faceFor(sty)
}

// isRuneWhitespace reports whether r is a rune that inline layout trims.
func isRuneWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// registryGlyphFallback is the last-resort registry lookup: any opt-in face
// covering r, regardless of CSS font-family.
func (e *engine) registryGlyphFallback(st *ResolvedStyle, r rune) *pdf.Font {
	if e.registry == nil || st == nil {
		return nil
	}

	return e.registry.FindWithGlyph(r, st.FontWeight, st.FontItalic)
}

// registryFamilyWithGlyph looks up the first CSS font-family face that has a
// glyph for runeValue.
func (e *engine) registryFamilyWithGlyph(style *ResolvedStyle, runeValue rune) *pdf.Font {
	if e.registry == nil || style == nil {
		return nil
	}

	// Reuse a one-element slice header so Lookup does not allocate per family.
	var one [1]string

	for _, fam := range style.FontFamily {
		one[0] = fam

		f := e.registry.Lookup(one[:], style.FontWeight, style.FontItalic)
		if f != nil && f.GlyphID(runeValue) != 0 {
			return f
		}
	}

	return nil
}

// facesWithGlyph resolves the default faces and returns the first that has a
// glyph for runeValue.
//
//nolint:cyclop,lll // ordered fallback search is intentionally explicit
func (e *engine) facesWithGlyph(style *ResolvedStyle, runeValue rune) *pdf.Font {
	if e.faces == nil || style == nil {
		return nil
	}

	if f := e.faces.ResolveFamily(style.FontFamily, style.FontWeight, style.FontItalic); f != nil && f.GlyphID(runeValue) != 0 {
		return f
	}

	if f := e.faces.Resolve(style.FontWeight, style.FontItalic); f != nil && f.GlyphID(runeValue) != 0 {
		return f
	}

	if style.FontWeight >= fontWeightBoldValue && e.faces.UnicodeFallbackBold != nil && e.faces.UnicodeFallbackBold.GlyphID(runeValue) != 0 {
		return e.faces.UnicodeFallbackBold
	}

	if e.faces.UnicodeFallback != nil && e.faces.UnicodeFallback.GlyphID(runeValue) != 0 {
		return e.faces.UnicodeFallback
	}

	return nil
}

// scalePt applies the engine zoom factor to a style length in points.
func (e *engine) scalePt(v float64) float64 { return v * e.scale }

// zoomScale returns the effective zoom factor: any positive opts.Zoom
// (values below 1 shrink; 0 means no zoom).
func zoomScale(z float64) float64 {
	if z > 0 {
		return z
	}

	return 1
}
