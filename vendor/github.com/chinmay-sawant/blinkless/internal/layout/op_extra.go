package layout

// emptyExtra is the shared zero extra for ops that never set a rare payload.
// Writers must detach before mutating so the singleton stays zero.
var emptyExtra = &opExtra{} //nolint:exhaustruct,gochecknoglobals // immutable zero singleton shared by all ops

// opExtra holds display-list payloads that are empty on the report fixture
// (no images, URIs, transforms, or structure tags on typical fill/text/grid
// ops). Embedding it keeps field names (op.URI, op.Image) while the hot Op
// record stays at 256 bytes.
type opExtra struct {
	URI           string
	Image         []byte
	ImgW, ImgH    int
	Alt           string
	Xform         Matrix2D
	Projective    ProjectiveTransform
	HasProjective bool
	Path          DisplayPath
	HasPath       bool
	GlyphBasis    GlyphBasis
	HasGlyphBasis bool
	BlendMode     string
	PaintOpacity  float64
	TextTransform string
	TextLanguage  string
	TextAutospace string
	// FontFeatures is the canonical OpenType feature list string built from
	// font-feature-settings / font-kerning / font-variant-* for the shaper.
	FontFeatures string
	NoFakeBold   bool
	// IsOutline marks CSS outline operations, which paint above descendant
	// content even though ordinary backgrounds and borders paint below it.
	IsOutline bool
	// BlendGroup is the owning CSS element group for masks, blending, or
	// isolation. GroupMark flags begin/end boundary markers that
	// carry the group without painting.
	BlendGroup *BlendGroup
	GroupMark  uint8
	// CornerPath holds the command list for an OpPath operation. Nil for
	// every other kind. It lives here, not on the hot Op record, so that
	// record stays at 256 bytes.
	CornerPath *CornerPath
	// CanvasBackground marks the root canvas default fill: the opaque rect a
	// dark color-scheme root contributes. Read it with IsCanvasBackground.
	// It is the canvas background, not an element background (CSS
	// Backgrounds 3 §2.11.2 gives the root's background to the canvas, whose
	// painting area is the whole canvas), so a consumer that paginates the
	// display list paints it across every page while a consumer painting one
	// canvas paints it once. Regular element backgrounds never set it, so
	// the two stay distinguishable.
	CanvasBackground bool
	// Pristine is the operation as it stood before FinalizeDisplayOps baked
	// its 3D projection into final geometry. A restamp pass restores it and
	// stamps again, because a projective map cannot be recovered from the
	// flattened path it produced. Nil on every op that was never projected,
	// so the common op pays one pointer.
	Pristine *Op
	// OverflowClip is the nearest ancestor overflow padding box in canvas
	// points. Geometry clipping bakes the clip into the op bounds, which a
	// later transform moves away; a host painting the op through a matrix
	// must intersect the paint with this rect instead. Set only for ops the
	// motion driver stamps (see SetOverflowClip); the common op pays
	// nothing.
	ClipX, ClipY, ClipW, ClipH float64
	HasClip                    bool
}

func (op *Op) detachExtra() *opExtra {
	if op.opExtra == nil || op.opExtra == emptyExtra {
		extra := new(opExtra)
		op.opExtra = extra

		return extra
	}

	return op.opExtra
}

func (op Op) detachedExtraCopy() *opExtra {
	if op.opExtra == nil || op.opExtra == emptyExtra {
		return new(opExtra)
	}

	cp := *op.opExtra
	cp.Image = append([]byte(nil), cp.Image...)
	cp.Path.Points = append([]DisplayPoint(nil), cp.Path.Points...)
	cp.CornerPath = cloneCornerPath(cp.CornerPath)

	return &cp
}

func (op *Op) bindEmptyExtra() {
	if op.opExtra == nil {
		op.opExtra = emptyExtra
	}
}

func (op *Op) setOutline() {
	op.detachExtra().IsOutline = true
}

func (op *Op) isOutline() bool {
	return op != nil && op.opExtra != nil && op.opExtra.IsOutline
}

// setCanvasBackground marks the op as the root canvas default fill.
func (op *Op) setCanvasBackground() {
	op.detachExtra().CanvasBackground = true
}

// BindEmptyExtra points a nil extra at the shared zero extra so readers in
// other packages can load BlendMode, Xform, and Image without panicking.
//
//nolint:stylecheck // Op methods already mix op/paintOp receivers
func (op *Op) BindEmptyExtra() { op.bindEmptyExtra() }

func (op *Op) setURI(uri string) {
	if uri == "" {
		if op.opExtra == nil || op.opExtra == emptyExtra {
			return
		}

		op.detachExtra().URI = ""

		return
	}

	op.detachExtra().URI = uri
}

// SetURI writes the link target, allocating a unique extra if needed.
func (op *Op) SetURI(uri string) { op.setURI(uri) }

// SetBlendMode writes the CSS blend mode, allocating a unique extra if needed.
func (op *Op) SetBlendMode(mode string) { op.setBlendMode(mode) }

// SetXform writes the baked CSS transform and marks it set.
func (op *Op) SetXform(matrix Matrix2D) {
	op.setXform(matrix)
	op.XformSet = true
}

// SetOverflowClip records the ancestor overflow padding box in canvas points.
// A host painting the op through a transform intersects the paint with this
// rect; geometry baking alone cannot hold the clip still while the matrix
// moves the op. Nil-safe on read; writing allocates a unique extra.
func (op *Op) SetOverflowClip(x, y, w, h float64) {
	extra := op.detachExtra()
	extra.ClipX, extra.ClipY, extra.ClipW, extra.ClipH = x, y, w, h
	extra.HasClip = true
}

// OverflowClip reads the clip SetOverflowClip recorded. It reports false
// when no clip was recorded or the op carries no extra.
func (op *Op) OverflowClip() (float64, float64, float64, float64, bool) {
	if op == nil || op.opExtra == nil || !op.opExtra.HasClip {
		return 0, 0, 0, 0, false
	}

	return op.opExtra.ClipX, op.opExtra.ClipY, op.opExtra.ClipW, op.opExtra.ClipH, true
}

// setProjectiveTransform keeps the plane projection inside Blinkless until
// FinalizeDisplayOps turns it into drawing geometry.
func (op *Op) setProjectiveTransform(matrix ProjectiveTransform) {
	extra := op.detachExtra()
	extra.Projective = matrix
	extra.HasProjective = true
}

func (op *Op) setDisplayPath(points []DisplayPoint, closed bool) {
	extra := op.detachExtra()
	extra.Path = DisplayPath{Points: points, Closed: closed}
	extra.HasPath = len(points) > 0
}

func (op *Op) setGlyphBasis(matrix Matrix2D) {
	extra := op.detachExtra()
	extra.GlyphBasis = GlyphBasis{A: matrix.A, B: matrix.B, C: matrix.C, D: matrix.D}
	extra.HasGlyphBasis = true
}

func (op *Op) clearProjectiveTransform() {
	if op == nil || op.opExtra == nil || !op.opExtra.HasProjective {
		return
	}

	op.detachExtra().HasProjective = false
}

// SetTextTransform writes the CSS text-transform, allocating a unique extra if needed.
func (op *Op) SetTextTransform(value string) { op.setTextTransform(value) }

// SetTextLanguage writes the CSS font-language-override tag, allocating a
// unique extra if needed. Empty and "normal" mean no shaping override.
func (op *Op) SetTextLanguage(value string) { op.setTextLanguage(value) }

// TextLanguage returns the op's OpenType language override, or "" when the op
// carries none. Ops that never bound an extra read as no override.
func (op Op) TextLanguage() string {
	if op.opExtra == nil {
		return ""
	}

	return op.opExtra.TextLanguage
}

// SetFontFeatures writes the OpenType feature list string for shaping.
func (op *Op) SetFontFeatures(value string) { op.setFontFeatures(value) }

// FontFeatures returns the op's OpenType feature settings string, or "" when
// the op carries none.
func (op Op) FontFeatures() string {
	if op.opExtra == nil {
		return ""
	}

	return op.opExtra.FontFeatures
}

// TextAutospace returns the text-autospace value carried by a text op.
func (op Op) TextAutospace() string {
	if op.opExtra == nil {
		return ""
	}

	return op.opExtra.TextAutospace
}

// SetPaintOpacity writes element opacity, allocating a unique extra if needed.
func (op *Op) SetPaintOpacity(value float64) { op.setPaintOpacity(value) }

// Clone returns a copy whose extra payload does not alias op.
func (op Op) Clone() Op {
	op.opExtra = op.detachedExtraCopy()

	return op
}

func (op *Op) setImage(data []byte, width, height int, alt string) {
	extra := op.detachExtra()
	extra.Image = data
	extra.ImgW = width
	extra.ImgH = height
	extra.Alt = alt
}

func (op *Op) setBlendMode(mode string) {
	if mode == "" || mode == blendNormal {
		if op.opExtra == nil || op.opExtra == emptyExtra {
			return
		}

		op.detachExtra().BlendMode = mode

		return
	}

	op.detachExtra().BlendMode = mode
}

func (op *Op) setTextTransform(value string) {
	if value == "" || value == textTransformNone {
		if op.opExtra == nil || op.opExtra == emptyExtra {
			return
		}

		op.detachExtra().TextTransform = value

		return
	}

	op.detachExtra().TextTransform = value
}

func (op *Op) setTextLanguage(value string) {
	if value == fontVariantNormal {
		value = ""
	}

	if value == "" {
		if op.opExtra == nil || op.opExtra == emptyExtra {
			return
		}

		op.detachExtra().TextLanguage = ""

		return
	}

	op.detachExtra().TextLanguage = value
}

func (op *Op) setFontFeatures(value string) {
	if value == "" {
		if op.opExtra == nil || op.opExtra == emptyExtra {
			return
		}

		op.detachExtra().FontFeatures = ""

		return
	}

	op.detachExtra().FontFeatures = value
}

func (op *Op) setPaintOpacity(value float64) {
	if value <= 0 || value >= 1 {
		if op.opExtra == nil || op.opExtra == emptyExtra {
			return
		}

		op.detachExtra().PaintOpacity = 0

		return
	}

	op.detachExtra().PaintOpacity = value
}

func (op *Op) setXform(matrix Matrix2D) {
	op.detachExtra().Xform = matrix
}

func (op Op) withImage(data []byte, width, height int, alt string) Op {
	extra := op.detachedExtraCopy()
	extra.Image = data
	extra.ImgW = width
	extra.ImgH = height
	extra.Alt = alt
	op.opExtra = extra

	return op
}

func (op Op) withURI(uri string) Op {
	if uri == "" {
		return op
	}

	extra := op.detachedExtraCopy()
	extra.URI = uri
	op.opExtra = extra

	return op
}

func (op Op) withBlendMode(mode string) Op {
	if mode == "" || mode == blendNormal {
		return op
	}

	extra := op.detachedExtraCopy()
	extra.BlendMode = mode
	op.opExtra = extra

	return op
}

func (op Op) withTextTransform(value string) Op {
	if value == "" || value == textTransformNone {
		return op
	}

	extra := op.detachedExtraCopy()
	extra.TextTransform = value
	op.opExtra = extra

	return op
}

func (op Op) withTextLanguage(value string) Op {
	if value == "" || value == fontVariantNormal {
		return op
	}

	extra := op.detachedExtraCopy()
	extra.TextLanguage = value
	op.opExtra = extra

	return op
}

func (op Op) withFontFeatures(value string) Op {
	if value == "" {
		return op
	}

	extra := op.detachedExtraCopy()
	extra.FontFeatures = value
	op.opExtra = extra

	return op
}

func (op Op) withTextAutospace(value string) Op {
	if value == "" || value == textAutospaceNone {
		return op
	}

	extra := op.detachedExtraCopy()
	extra.TextAutospace = value
	op.opExtra = extra

	return op
}

// styleHasShapingFeatures reports whether sty can produce a non-empty
// OpenType feature list (fontShapingFeatureSettings) or language override
// (fontShapingLanguage). The common element sets none of the shaping fields,
// and building those strings costs a map, a slice, a closure, and several
// small splits per text run, so decorateTextOp checks this first and skips
// both computations when the answer is empty by construction. A true result
// only means "compute it"; every shaping keyword still decides the output.
func styleHasShapingFeatures(sty *ResolvedStyle) bool {
	if sty == nil {
		return false
	}

	if sty.FontKerning == fontKerningNone {
		return true
	}

	if sty.FontFeatureSettings != "" && sty.FontFeatureSettings != fontVariantNormal {
		return true
	}

	if sty.FontLanguageOverride != "" && sty.FontLanguageOverride != fontVariantNormal {
		return true
	}

	return fontVariantShapingSet(sty)
}

// fontVariantShapingSet reports whether any font-variant longhand carries a
// value beyond the initial one. Split from styleHasShapingFeatures so the
// per-run fast path stays a flat sequence of early returns.
func fontVariantShapingSet(sty *ResolvedStyle) bool {
	return isSetFontVariantValue(sty.FontVariantCaps) ||
		isSetFontVariantValue(sty.FontVariantLigatures) ||
		isSetFontVariantValue(sty.FontVariantNumeric) ||
		isSetFontVariantValue(sty.FontVariantPosition) ||
		isSetFontVariantValue(sty.FontVariantAlternates) ||
		isSetFontVariantValue(sty.FontVariantEastAsian)
}

// isSetFontVariantValue reports whether a font-variant longhand carries a
// value beyond the initial one. "" and "normal" never produce feature tags,
// so they skip shaping; anything else falls through to the full computation
// (which may still find nothing to emit).
func isSetFontVariantValue(value string) bool {
	return value != "" && value != fontVariantNormal
}

// decorateTextOp attaches language, OpenType features, and synthesis gates
// shared by every OpText emit site.
func decorateTextOp(paintOp Op, sty *ResolvedStyle) Op {
	if sty == nil {
		return paintOp
	}

	// The shaping strings are empty unless the element sets a shaping field
	// (see styleHasShapingFeatures); computing them unconditionally pays the
	// map/slice/split garbage on every default text run. The with* setters
	// are identity on "" so skipping the computation changes nothing.
	language, features := "", ""
	if styleHasShapingFeatures(sty) {
		language = fontShapingLanguage(sty)
		features = fontShapingFeatureSettings(sty)
	}

	paintOp = paintOp.withTextTransform(sty.TextTransform).
		withTextLanguage(language).
		withFontFeatures(features).
		withTextAutospace(sty.TextAutospace)

	if textOpDisablesFakeBold(sty) {
		paintOp.detachExtra().NoFakeBold = true
	}

	if needsFakeOblique(sty, paintOp.Font) {
		paintOp.FakeOblique = true
	}

	if r, g, b, ok := fontPaletteFill(sty, paintOp.Font); ok {
		paintOp.R, paintOp.G, paintOp.B = r, g, b
	} else if r, g, b, ok := fontVariantEmojiFill(sty, paintOp.Text); ok {
		paintOp.R, paintOp.G, paintOp.B = r, g, b
	}

	return paintOp
}

func cloneOpExtra(src *opExtra) *opExtra {
	if src == nil || src == emptyExtra {
		return src
	}

	cp := *src
	cp.Image = append([]byte(nil), src.Image...)
	cp.CornerPath = cloneCornerPath(src.CornerPath)

	return &cp
}
