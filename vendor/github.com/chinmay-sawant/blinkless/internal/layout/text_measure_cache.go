package layout

import (
	"math"

	pdf "github.com/chinmay-sawant/blinkless/internal/fonts"
)

const (
	// maxTextWidthCacheEntries bounds the per-Layout measureTextFace cache.
	// Thousands of repeated price labels share a few dozen distinct strings;
	// a huge document with all-distinct text stops storing once full.
	maxTextWidthCacheEntries = 8192
	// maxRuneWidthCacheEntries bounds the per-Layout measureRuneFace cache.
	maxRuneWidthCacheEntries = 4096
	// maxCachedTextLen caps cached strings by bytes. Short mono labels hit;
	// long paragraphs compute uncached so the key map cannot pin them.
	maxCachedTextLen = 128
	// initialWidthCacheEntries sizes a fresh per-Layout string width cache.
	// Thousands of repeated price labels share a few dozen distinct strings,
	// so a small initial map covers the steady state; the bounds above cap
	// growth for documents with all-distinct text.
	initialWidthCacheEntries = 256
)

// textWidthKey identifies one measureTextFace result. sizeBits is the scaled
// used size; lsBits/wsBits are the scaled spacing already added per rune;
// tabBits/tabIsLength select the tab-stop path; autospace and emoji select
// the per-rune gap and emoji-minimum paths; the family/weight/italic/optical
// fields select the fallback face for glyphs missing from the primary face.
type textWidthKey struct {
	face        *pdf.Font
	famHash     uint64
	weight      int
	italic      bool
	optical     string
	variations  string
	opticalSize uint64
	sizeBits    uint64
	lsBits      uint64
	wsBits      uint64
	tabBits     uint64
	tabIsLength bool
	autospace   bool
	emoji       string
	text        string
}

// runeWidthKey identifies one measureRuneFace result. Size here is the raw
// scaled FontSize (measureRuneFace does not apply font-size-adjust);
// widthScaleBits carries fontWidthScale; other fields mirror textWidthKey.
type runeWidthKey struct {
	famHash       uint64
	weight        int
	italic        bool
	optical       string
	variations    string
	opticalSize   uint64
	sizeBits      uint64
	lsBits        uint64
	wsBits        uint64
	tabBits       uint64
	tabIsLength   bool
	emoji         string
	widthScaleBit uint64
	r             rune
}

// textWidthCacheKey builds the measureTextFace key for sty and text. primary
// must be the already-resolved e.faceFor(sty) face and size the already
// scaled used size.
func textWidthCacheKey(sty *ResolvedStyle, primary *pdf.Font, size float64, text string) textWidthKey {
	optical, variations, sizeBits := variationCacheBits(sty)

	return textWidthKey{
		face:        primary,
		famHash:     sty.famHash,
		weight:      sty.FontWeight,
		italic:      sty.FontItalic,
		optical:     optical,
		variations:  variations,
		opticalSize: sizeBits,
		sizeBits:    math.Float64bits(size),
		lsBits:      math.Float64bits(sty.LetterSpacing),
		wsBits:      math.Float64bits(sty.WordSpacing),
		tabBits:     math.Float64bits(sty.TabSize),
		tabIsLength: isTabSizeLength(sty),
		autospace:   autospaceIdeographAlpha(sty.TextAutospace),
		emoji:       sty.FontVariantEmoji,
		text:        text,
	}
}

// lookupTextWidth returns the cached string width for key.
func (e *engine) lookupTextWidth(key textWidthKey) (float64, bool) {
	if len(e.textWidthCache) == 0 {
		return 0, false
	}

	width, ok := e.textWidthCache[key]

	return width, ok
}

// storeTextWidth records width under key unless the cache is full.
func (e *engine) storeTextWidth(key textWidthKey, width float64) {
	if e.textWidthCache == nil {
		e.textWidthCache = make(map[textWidthKey]float64, initialWidthCacheEntries)
	}

	if len(e.textWidthCache) >= maxTextWidthCacheEntries {
		return
	}

	e.textWidthCache[key] = width
}

// runeWidthCacheKey builds the measureRuneFace key for sty and runeValue.
func runeWidthCacheKey(sty *ResolvedStyle, scale float64, runeValue rune) runeWidthKey {
	optical, variations, sizeBits := variationCacheBits(sty)

	return runeWidthKey{
		famHash:       sty.famHash,
		weight:        sty.FontWeight,
		italic:        sty.FontItalic,
		optical:       optical,
		variations:    variations,
		opticalSize:   sizeBits,
		sizeBits:      math.Float64bits(sty.FontSize * scale),
		lsBits:        math.Float64bits(sty.LetterSpacing),
		wsBits:        math.Float64bits(sty.WordSpacing),
		tabBits:       math.Float64bits(sty.TabSize),
		tabIsLength:   isTabSizeLength(sty),
		emoji:         sty.FontVariantEmoji,
		widthScaleBit: math.Float64bits(fontWidthScale(sty)),
		r:             runeValue,
	}
}

// lookupRuneWidth returns the cached single-rune width for key.
func (e *engine) lookupRuneWidth(key runeWidthKey) (float64, bool) {
	if len(e.runeWidthCache) == 0 {
		return 0, false
	}

	w, ok := e.runeWidthCache[key]

	return w, ok
}

// storeRuneWidth records width under key unless the cache is full.
func (e *engine) storeRuneWidth(key runeWidthKey, width float64) {
	if e.runeWidthCache == nil {
		e.runeWidthCache = make(map[runeWidthKey]float64, initialWidthCacheEntries)
	}

	if len(e.runeWidthCache) >= maxRuneWidthCacheEntries {
		return
	}

	e.runeWidthCache[key] = width
}

// measureRuneFace measures a single rune with the same face selection as
// measureTextFace, without allocating string(r).
func (e *engine) measureRuneFace(curRune rune, sty *ResolvedStyle) float64 {
	if sty == nil {
		return 0
	}

	if curRune == softHyphenRune || isVariationSelector(curRune) {
		return 0
	}

	key := runeWidthCacheKey(sty, e.scale, curRune)
	if w, ok := e.lookupRuneWidth(key); ok {
		return w
	}

	w := e.measureRuneFaceUncached(curRune, sty)
	e.storeRuneWidth(key, w)

	return w
}

// measureRuneFaceUncached measures one rune without consulting the rune cache.
func (e *engine) measureRuneFaceUncached(curRune rune, sty *ResolvedStyle) float64 {
	if curRune == '\t' {
		if isTabSizeLength(sty) {
			return e.scalePt(sty.TabSize)
		}

		tabSize := sty.TabSize
		if tabSize <= 0 {
			tabSize = 8
		}

		// \t expands to TabSize * space width per CSS tab-size.
		spaceW := e.measureRuneFace(' ', sty)
		if spaceW <= 0 {
			spaceW = sty.FontSize * e.scale * tabSpaceRatio
		}

		return spaceW * tabSize
	}

	size := sty.FontSize * e.scale
	face := e.faceForRune(sty, curRune)

	if face == nil {
		face = e.font
	}

	advance := face.GlyphAdvancePoints(curRune, size)
	if em := emojiPresentationAdvance(sty, curRune, size); em > advance {
		advance = em
	}

	if sty.LetterSpacing != 0 {
		advance += sty.LetterSpacing * e.scale
	}

	if curRune == ' ' && sty.WordSpacing != 0 {
		advance += sty.WordSpacing * e.scale
	}

	return advance * fontWidthScale(sty)
}

type faceRun struct {
	text string
	face *pdf.Font
	w    float64
}
