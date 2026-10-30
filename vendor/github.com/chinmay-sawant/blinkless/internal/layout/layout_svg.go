//nolint:all // inline SVG serialize+raster path
package layout

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/chinmay-sawant/blinkless/internal/errs"
	"github.com/chinmay-sawant/blinkless/internal/html"
	"github.com/chinmay-sawant/blinkless/internal/svg"
)

const cssTagSVG = "svg"

// buildInlineSVG rasterizes an inline <svg> subtree (with cascade presentation
// props baked into attributes). When paint is false, only size/imgRef are
// filled so the inline line placer can emit at the final position (same
// contract as buildImage); painting at (0,0) here left stray SVGs on the
// masthead and hid logo.png.
func (e *engine) buildInlineSVG(node *html.Node, sty ResolvedStyle, posX, posY float64, paint bool) *box {
	boxNode := &box{ //nolint:exhaustruct // intentional zero fields
		node: node, style: e.stylePtr(node), kind: boxKindReplaced, x: posX, y: posY,
	}

	data := e.serializeInlineSVG(node)
	ref := &imageRef{src: "#inline-svg"} //nolint:exhaustruct // synthetic raster
	cache := e.inlineSVGRasterCache()
	key := svgInlineCacheKey(data, svgRasterMaxDim)
	if png, pw, ph, ok := cache.get(key); ok {
		ref.data, ref.w, ref.h = png, pw, ph
	} else if png, pw, ph, err := svg.RasterizeContext(e.rasterContext(), data, svgRasterMaxDim); err == nil && len(png) > 0 {
		ref.data, ref.w, ref.h = png, pw, ph
		cache.put(key, png, pw, ph)
	}
	boxNode.img = ref

	size := e.usedInlineSVGSize(node, sty, ref)
	padL := e.scalePt(sty.PaddingLeft)
	padR := e.scalePt(sty.PaddingRight)
	padT := e.scalePt(sty.PaddingTop)
	padB := e.scalePt(sty.PaddingBottom)
	borderL := e.scalePt(borderPaint(sty.BorderLeft))
	borderR := e.scalePt(borderPaint(sty.BorderRight))
	borderT := e.scalePt(borderPaint(sty.BorderTop))
	borderB := e.scalePt(borderPaint(sty.BorderBottom))
	boxNode.w = size.w + padL + padR + borderL + borderR
	boxNode.height = size.h + padT + padB + borderT + borderB

	if paint && ref.data != nil && !e.noEmit {
		imgX := posX + borderL + padL
		imgY := posY + borderT + padT
		opStart := len(e.ops)
		e.add((Op{ //nolint:exhaustruct // intentional zero fields
			Kind: OpImage, X: imgX, Y: imgY, W: size.w, H: size.h,
		}).withImage(ref.data, ref.w, ref.h, ""))
		e.prependChrome(opStart, boxNode, sty, posX, posY, boxNode.w, boxNode.height)
	}

	return boxNode
}

// usedInlineSVGSize prefers width/height attributes (CSS px), then the
// raster intrinsic size, then a small fallback.
func (e *engine) usedInlineSVGSize(node *html.Node, sty ResolvedStyle, ref *imageRef) imageUsedSize {
	// Attribute lengths are CSS px → pt, then zoomed like other style lengths.
	wAttr := e.scalePt(parseSVGLengthPx(node.Attribute("width")))
	hAttr := e.scalePt(parseSVGLengthPx(node.Attribute("height")))
	if sty.Width >= 0 {
		wAttr = e.scalePt(sty.Width)
	} else if sty.WidthPercent >= 0 && e.opts.Width > 0 {
		wAttr = e.opts.Width * sty.WidthPercent / 100
	}
	if sty.Height >= 0 {
		hAttr = e.scalePt(sty.Height)
	}

	intrW, intrH := 0.0, 0.0
	if ref != nil && ref.w > 0 && ref.h > 0 {
		intrW = e.scalePt(pxToPt(float64(ref.w)))
		intrH = e.scalePt(pxToPt(float64(ref.h)))
	}
	if wAttr <= 0 {
		wAttr = intrW
	}
	if hAttr <= 0 {
		hAttr = intrH
	}
	if wAttr <= 0 {
		wAttr = e.scalePt(pxToPt(64))
	}
	if hAttr <= 0 {
		hAttr = e.scalePt(pxToPt(28))
	}

	// Max constraints clamp like usedImageSize does for <img>: a definite
	// width or height keeps its axis, and only an auto axis follows the used
	// ratio (Chrome 143: a 200x200 svg with max-height 100pt stays 200 wide).
	size := imageUsedSize{w: wAttr, h: hAttr}
	cssW := sty.Width >= 0
	if sty.WidthPercent >= 0 && e.imageContainingWidth() > 0 {
		cssW = true
	}
	widthDefinite := cssW || parseSVGLengthPx(node.Attribute("width")) > 0
	heightDefinite := sty.Height >= 0 || parseSVGLengthPx(node.Attribute("height")) > 0
	size = clampImageWidth(size, e.imageMaxWidth(sty, cssW), heightDefinite)
	size = clampImageHeight(e, size, sty, widthDefinite)

	return size
}

func parseSVGLengthPx(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	raw = strings.TrimSuffix(raw, "px")
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 {
		return 0
	}

	return pxToPt(v)
}

// serializeInlineSVG emits SVG XML for canvas rasterization, baking resolved
// fill/stroke presentation props onto each element as attributes.
func (e *engine) serializeInlineSVG(node *html.Node) []byte {
	return e.serializeInlineSVGWithPaint(node, nil)
}

// serializeInlineSVGWithPaint is serializeInlineSVG with one restyle pass. The
// pass carries the used styles of the overridden elements and the paint
// properties whose authored presentation attribute the override replaces. A
// nil pass leaves the serialization byte for byte unchanged.
//
// The writer comes from a shared pool so repeated icons do not allocate a
// builder buffer per element, and the returned bytes are an exact-size copy:
// the pooled buffer is reused by the next serialization.
func (e *engine) serializeInlineSVGWithPaint(node *html.Node, paint *svgPaintPass) []byte {
	buf := svgSerializePool.Get().(*bytes.Buffer)
	buf.Reset()
	e.writeSVGNode(buf, node, paint)
	out := bytes.Clone(buf.Bytes())
	// A huge inline SVG must not pin its buffer across every later run:
	// oversized buffers are dropped for the GC instead of pooled.
	if buf.Cap() <= maxPooledSerializeBytes {
		svgSerializePool.Put(buf)
	}

	return out
}

// maxPooledSerializeBytes bounds the serialize buffers the shared pool
// keeps. Repeated icons serialize to a few kilobytes, so this covers the
// steady state while a megabyte-scale document falls through to the GC.
const maxPooledSerializeBytes = 64 << 10

// svgSerializePool reuses serialize buffers across inline SVGs. Layout is
// single-threaded per engine but engines run concurrently, so a pool (not an
// engine field) owns the shared buffers.
//
//nolint:gochecknoglobals // shared buffer pool, reset on every Get
var svgSerializePool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// svgWrittenSet tracks serialized attribute names without a per-element map
// allocation. SVG elements carry few attributes, so a linear scan over a
// stack array beats hashing; larger elements spill to a map.
type svgWrittenSet struct {
	names [16]string
	n     int
	spill map[string]bool
}

func (s *svgWrittenSet) mark(name string) {
	if s.has(name) {
		return
	}

	if s.n < len(s.names) {
		s.names[s.n] = name
		s.n++

		return
	}

	if s.spill == nil {
		s.spill = make(map[string]bool)
	}

	s.spill[name] = true
}

func (s *svgWrittenSet) has(name string) bool {
	for i := 0; i < s.n; i++ {
		if s.names[i] == name {
			return true
		}
	}

	return s.spill != nil && s.spill[name]
}

// svgStyleDecls is one parsed inline style attribute: declaration names and
// values in source order. Parsing once per serialized element replaces one
// strings.Split per baked property below.
type svgStyleDecls struct {
	names  [8]string
	values [8]string
	n      int
	spillN []string
	spillV []string
}

// parseSVGStyleDecls splits styleAttr on ";" and the first ":" exactly like
// svgInlineStyleProp, preserving first-match order for EqualFold lookups.
func parseSVGStyleDecls(styleAttr string) svgStyleDecls {
	var decls svgStyleDecls

	start := 0
	for i := 0; i <= len(styleAttr); i++ {
		if i < len(styleAttr) && styleAttr[i] != ';' {
			continue
		}

		name, value, ok := strings.Cut(styleAttr[start:i], ":")
		start = i + 1

		if !ok {
			continue
		}

		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)

		if decls.n < len(decls.names) {
			decls.names[decls.n] = name
			decls.values[decls.n] = value
			decls.n++
		} else {
			decls.spillN = append(decls.spillN, name)
			decls.spillV = append(decls.spillV, value)
		}
	}

	return decls
}

// prop returns the trimmed value of the first declaration naming prop
// (case-insensitive), or "" when absent — the parse-once form of
// svgInlineStyleProp.
func (d *svgStyleDecls) prop(prop string) string {
	for i := 0; i < d.n; i++ {
		if strings.EqualFold(d.names[i], prop) {
			return d.values[i]
		}
	}

	for i := range d.spillN {
		if strings.EqualFold(d.spillN[i], prop) {
			return d.spillV[i]
		}
	}

	return ""
}

// empty reports whether the attribute held no name:value declarations.
func (d *svgStyleDecls) empty() bool {
	return d.n == 0 && len(d.spillN) == 0
}

func (e *engine) writeSVGNode(b *bytes.Buffer, node *html.Node, paint *svgPaintPass) {
	if node == nil {
		return
	}
	switch node.Type {
	case html.TextNode:
		b.WriteString(escapeXML(node.Text))
	case html.ElementNode:
		b.WriteByte('<')
		b.WriteString(node.Name)
		var written svgWrittenSet
		// CSS d overrides the d presentation attribute (CSS wins over
		// presentation attributes in Chrome 143.0.7499.40), so the attribute
		// is suppressed here and the baked CSS value is emitted by
		// writeSVGExtraPresentationAttrs below. Applies to path only: d is
		// not a valid property on other elements.
		suppressDAttr := false
		styleAttr := node.Attribute("style")
		decls := parseSVGStyleDecls(styleAttr)
		if node.Name == "path" {
			_, suppressDAttr = svgCSSPathData(&decls)
		}
		// Attributes are written in sorted order so the serialization is
		// stable across calls: node.Attrs is a map, and the restyle raster
		// cache keys on the serialized document, so a random order would let
		// an unchanged element miss the cache on every pass. Keys sort in a
		// stack array so the common small-attribute case allocates nothing.
		var keys [16]string
		var sorted []string
		if len(node.Attrs) <= len(keys) {
			n := 0
			for k := range node.Attrs {
				keys[n] = k
				n++
			}
			sorted = keys[:n]
			sort.Strings(sorted)
		} else {
			sorted = make([]string, 0, len(node.Attrs))
			for k := range node.Attrs {
				sorted = append(sorted, k)
			}
			sort.Strings(sorted)
		}
		for _, k := range sorted {
			v := node.Attrs[k]
			if k == "style" {
				continue
			}
			if k == "d" && suppressDAttr {
				continue
			}
			if paint.suppresses(node, k) {
				continue
			}
			b.WriteByte(' ')
			b.WriteString(k)
			b.WriteString(`="`)
			b.WriteString(escapeXML(v))
			b.WriteByte('"')
			written.mark(k)
		}
		if node.Name == cssTagSVG && !written.has("xmlns") {
			b.WriteString(` xmlns="http://www.w3.org/2000/svg"`)
		}
		e.writeSVGPresentationAttrs(b, node, &written, &decls, paint)
		writeSVGExtraPresentationAttrs(b, node, &written, &decls)
		if len(node.Children) == 0 {
			b.WriteString("/>")
			return
		}
		b.WriteByte('>')
		for _, c := range node.Children {
			e.writeSVGNode(b, c, paint)
		}
		b.WriteString("</")
		b.WriteString(node.Name)
		b.WriteByte('>')
	}
}

func (e *engine) writeSVGPresentationAttrs(
	b *bytes.Buffer, node *html.Node, written *svgWrittenSet, decls *svgStyleDecls, paint *svgPaintPass,
) {
	if !e.hasStyle(node) {
		return
	}

	st := e.stylePtr(node)
	if paint.paintsNoFill(node) {
		if !written.has("fill") {
			b.WriteString(` fill="none"`)
		}
	} else if st.FillSet && !written.has("fill") {
		if st.FillOpacity == 0 && st.Fill == [3]float64{} {
			b.WriteString(` fill="none"`)
		} else {
			fmt.Fprintf(b, ` fill="%s"`, cssColorHex(st.Fill))
		}
	}
	if st.FillOpacity >= 0 && st.FillOpacity < 1 && !written.has("fill-opacity") {
		fmt.Fprintf(b, ` fill-opacity="%g"`, st.FillOpacity)
	}
	if st.StrokeSet && !written.has("stroke") {
		fmt.Fprintf(b, ` stroke="%s"`, cssColorHex(st.Stroke))
	}
	if st.StrokeWidthSet && st.StrokeWidth > 0 && !written.has("stroke-width") {
		fmt.Fprintf(b, ` stroke-width="%g"`, st.StrokeWidth)
	}
	if st.StrokeOpacity >= 0 && st.StrokeOpacity < 1 && !written.has("stroke-opacity") {
		fmt.Fprintf(b, ` stroke-opacity="%g"`, st.StrokeOpacity)
	}
	if len(st.StrokeDashArray) > 0 && !written.has("stroke-dasharray") {
		parts := make([]string, 0, len(st.StrokeDashArray))
		for _, d := range st.StrokeDashArray {
			parts = append(parts, strconv.FormatFloat(d, 'g', -1, 64))
		}
		b.WriteString(` stroke-dasharray="`)
		b.WriteString(escapeXML(strings.Join(parts, " ")))
		b.WriteByte('"')
	}
	if st.StrokeDashOffset != 0 && !written.has("stroke-dashoffset") {
		fmt.Fprintf(b, ` stroke-dashoffset="%g"`, st.StrokeDashOffset)
	}
	if st.StrokeLineCap != "" && !written.has("stroke-linecap") {
		switch st.StrokeLineCap {
		case "butt", "round", "square":
			fmt.Fprintf(b, ` stroke-linecap="%s"`, st.StrokeLineCap)
		}
	}
	// stroke-miterlimit is baked before stroke-linejoin on purpose: the
	// pinned canvas parser keeps the limit in its state and only reads it
	// when a later linejoin value builds the miter joiner, so the reverse
	// order would leave a bare miterlimit with no observable effect.
	if st.StrokeMiterLimit >= 1 && !written.has("stroke-miterlimit") {
		fmt.Fprintf(b, ` stroke-miterlimit="%g"`, st.StrokeMiterLimit)
	}
	if st.StrokeLineJoin != "" && !written.has("stroke-linejoin") {
		switch st.StrokeLineJoin {
		case "miter", "miter-clip", "round", "bevel", "arcs":
			fmt.Fprintf(b, ` stroke-linejoin="%s"`, st.StrokeLineJoin)
		}
	}
	// fill-rule and shape-rendering have no ResolvedStyle field, so they are
	// recovered from the element's inline style attribute here. A matching
	// presentation attribute already passed through above (written map) and
	// wins by the no-duplicate guard. Stylesheet rules for these two need a
	// stored field owned elsewhere and stay unbaked.
	if !written.has("fill-rule") {
		if rule := decls.prop("fill-rule"); rule != "" {
			switch strings.ToLower(rule) {
			case "nonzero", "evenodd":
				fmt.Fprintf(b, ` fill-rule="%s"`, strings.ToLower(rule))
			}
		}
	}
	if !written.has("shape-rendering") {
		if hint := decls.prop("shape-rendering"); hint != "" {
			switch strings.ToLower(hint) {
			case "auto":
				b.WriteString(` shape-rendering="auto"`)
			case "optimizespeed":
				b.WriteString(` shape-rendering="optimizeSpeed"`)
			case "crispedges":
				b.WriteString(` shape-rendering="crispEdges"`)
			case "geometricprecision":
				b.WriteString(` shape-rendering="geometricPrecision"`)
			}
		}
	}
	// SVG geometry and stop presentation properties below are recovered from
	// the element's inline style attribute, like fill-rule above: they have
	// no ResolvedStyle field, and the canvas rasterizer only reads them as
	// XML attributes (drawShape and parseDefs in the pinned canvas svg.go),
	// never from CSS style text (its setAttribute switch has no case for
	// them, and the style attribute itself is skipped above). A matching
	// presentation attribute already passed through above (written map) and
	// wins by the no-duplicate guard. Stylesheet rules for these need a
	// stored field owned elsewhere and stay unbaked.
	if !written.has("cx") {
		if v, ok := svgBakeLengthValue(decls.prop("cx")); ok {
			fmt.Fprintf(b, ` cx="%s"`, escapeXML(v))
		}
	}
	if !written.has("cy") {
		if v, ok := svgBakeLengthValue(decls.prop("cy")); ok {
			fmt.Fprintf(b, ` cy="%s"`, escapeXML(v))
		}
	}
	if !written.has("r") {
		if v, ok := svgBakeNonNegativeLength(decls.prop("r")); ok {
			fmt.Fprintf(b, ` r="%s"`, escapeXML(v))
		}
	}
	if !written.has("rx") {
		if v, ok := svgBakeNonNegativeLength(decls.prop("rx")); ok {
			fmt.Fprintf(b, ` rx="%s"`, escapeXML(v))
		}
	}
	if !written.has("ry") {
		if v, ok := svgBakeNonNegativeLength(decls.prop("ry")); ok {
			fmt.Fprintf(b, ` ry="%s"`, escapeXML(v))
		}
	}
	if !written.has("x") {
		if v, ok := svgBakeLengthValue(decls.prop("x")); ok {
			fmt.Fprintf(b, ` x="%s"`, escapeXML(v))
		}
	}
	if !written.has("y") {
		if v, ok := svgBakeLengthValue(decls.prop("y")); ok {
			fmt.Fprintf(b, ` y="%s"`, escapeXML(v))
		}
	}
	// stop-color and stop-opacity only affect gradient stops: canvas
	// parseDefs reads them from the stop tag attributes, so baking them on
	// any other element would be noise that the rasterizer ignores.
	if node.Name == "stop" {
		if !written.has("stop-color") {
			if v := strings.TrimSpace(decls.prop("stop-color")); svgBakeStopColorValid(v) {
				fmt.Fprintf(b, ` stop-color="%s"`, escapeXML(v))
			}
		}
		if !written.has("stop-opacity") {
			if v, ok := svgBakeOpacityValue(decls.prop("stop-opacity")); ok {
				fmt.Fprintf(b, ` stop-opacity="%s"`, escapeXML(v))
			}
		}
	}
	// paint-order, vector-effect, and text-rendering are baked for
	// serialization parity with Chrome 143, but the pinned canvas
	// setAttribute has no case for any of them, so the raster cannot
	// observe them. Each gap is pinned in css_behavior_svg_geo_test.go with
	// a serialize-only assertion, never a pixel assertion.
	if !written.has("paint-order") {
		if v, ok := svgBakePaintOrder(decls.prop("paint-order")); ok {
			fmt.Fprintf(b, ` paint-order="%s"`, escapeXML(v))
		}
	}
	if !written.has("vector-effect") {
		if v, ok := svgBakeVectorEffect(decls.prop("vector-effect")); ok {
			fmt.Fprintf(b, ` vector-effect="%s"`, escapeXML(v))
		}
	}
	if !written.has("text-rendering") {
		if v, ok := svgBakeTextRendering(decls.prop("text-rendering")); ok {
			fmt.Fprintf(b, ` text-rendering="%s"`, escapeXML(v))
		}
	}
}

// svgInlineStyleProp returns the trimmed value of one CSS property from a raw
// inline style attribute, or "" when absent. It splits on the first colon so
// values holding URLs keep working.
func svgInlineStyleProp(styleAttr, prop string) string {
	for _, decl := range strings.Split(styleAttr, ";") {
		name, value, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), prop) {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

// splitBakeUnit splits a validated-looking CSS length into its numeric text
// and lowercase unit ("" for plain numbers, "%" for percentages).
func splitBakeUnit(v string) (string, string) {
	if strings.HasSuffix(v, "%") {
		return v[:len(v)-1], "%"
	}

	i := len(v)
	for i > 0 {
		c := v[i-1]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			i--
		} else {
			break
		}
	}

	return v[:i], strings.ToLower(v[i:])
}

// svgBakeLengthValue validates a CSS geometry value for the cx/cy/x/y bake
// arms. Only units the canvas rasterizer resolves (plain numbers, px, pt,
// pc, cm, mm, q, in, %) pass: font and viewport units stay unbaked so a
// value the rasterizer would misread never changes pixels.
func svgBakeLengthValue(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" || strings.ContainsAny(v, " \t\n") {
		return "", false
	}

	num, unit := splitBakeUnit(v)

	if _, err := strconv.ParseFloat(num, 64); err != nil {
		return "", false
	}

	switch unit {
	case "", "px", "pt", "pc", "cm", "mm", "q", "in", "%":
		return v, true
	}

	return "", false
}

// svgBakeNonNegativeLength validates a CSS geometry value for the r/rx/ry
// bake arms. A negative radius is invalid per SVG2 and is dropped, matching
// Chrome 143 which ignores the declaration.
func svgBakeNonNegativeLength(raw string) (string, bool) {
	v, ok := svgBakeLengthValue(raw)
	if !ok {
		return "", false
	}

	num, _ := splitBakeUnit(v)

	f, err := strconv.ParseFloat(num, 64)
	if err != nil || f < 0 {
		return "", false
	}

	return v, true
}

// svgBakeStopColorValid reports whether a stop-color value survives the
// canvas rasterizer: it parses hex, rgb(), and rgba() but degrades anything
// else to black, so named colors and currentcolor stay unbaked.
func svgBakeStopColorValid(raw string) bool {
	v := strings.TrimSpace(raw)
	if v == "" {
		return false
	}

	if strings.HasPrefix(v, "#") {
		hex := v[1:]
		if len(hex) != 3 && len(hex) != 6 {
			return false
		}

		for i := 0; i < len(hex); i++ {
			c := hex[i]
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}

		return true
	}

	lower := strings.ToLower(v)

	return strings.HasPrefix(lower, "rgb(") && strings.HasSuffix(lower, ")") ||
		strings.HasPrefix(lower, "rgba(") && strings.HasSuffix(lower, ")")
}

// svgBakeOpacityValue validates a stop-opacity value: a plain number in
// 0..1 or a percentage in 0%..100%, matching what the canvas parseNumber
// resolves for the stop tag.
func svgBakeOpacityValue(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", false
	}

	if strings.HasSuffix(v, "%") {
		f, err := strconv.ParseFloat(strings.TrimSpace(v[:len(v)-1]), 64)
		if err != nil || f < 0 || f > 100 {
			return "", false
		}

		return v, true
	}

	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f > 1 {
		return "", false
	}

	return v, true
}

// svgBakePaintOrder validates a paint-order value: normal alone, or one or
// more of fill/stroke/markers in any order without repeats.
func svgBakePaintOrder(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false
	}

	if v == "normal" {
		return "normal", true
	}

	seen := map[string]bool{}

	var out []string

	for _, tok := range strings.Fields(v) {
		if tok != "fill" && tok != "stroke" && tok != "markers" {
			return "", false
		}

		if seen[tok] {
			return "", false
		}

		seen[tok] = true
		out = append(out, tok)
	}

	if len(out) == 0 {
		return "", false
	}

	return strings.Join(out, " "), true
}

// svgBakeVectorEffect validates a vector-effect value. Only the values with
// an observable SVG meaning are baked; the rasterizer ignores them (pinned
// gap), but an invalid token must not reach the serialized output.
func svgBakeVectorEffect(raw string) (string, bool) {
	switch v := strings.ToLower(strings.TrimSpace(raw)); v {
	case "none", "non-scaling-stroke":
		return v, true
	}

	return "", false
}

// svgBakeTextRendering validates a text-rendering value and returns its
// canonical camelCase attribute form.
func svgBakeTextRendering(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "auto":
		return "auto", true
	case "optimizespeed":
		return "optimizeSpeed", true
	case "optimizelegibility":
		return "optimizeLegibility", true
	case "geometricprecision":
		return "geometricPrecision", true
	}

	return "", false
}

func cssColorHex(c [3]float64) string {
	r := int(c[0]*255 + 0.5)
	g := int(c[1]*255 + 0.5)
	bl := int(c[2]*255 + 0.5)
	if r < 0 {
		r = 0
	}
	if g < 0 {
		g = 0
	}
	if bl < 0 {
		bl = 0
	}
	if r > 255 {
		r = 255
	}
	if g > 255 {
		g = 255
	}
	if bl > 255 {
		bl = 255
	}

	var buf [7]byte

	out := append(buf[:0], '#')
	out = appendHexByte(out, byte(r))
	out = appendHexByte(out, byte(g))
	out = appendHexByte(out, byte(bl))

	return string(out)
}

// appendHexByte appends b as a zero-padded 2-digit lowercase hex number.
func appendHexByte(dst []byte, b byte) []byte {
	const hexDigits = "0123456789abcdef"

	return append(dst, hexDigits[b>>4], hexDigits[b&0x0F])
}

// xmlEscaper escapes the five predefined XML entities. It is package level
// because escapeXML runs once per serialized SVG node.
//
//nolint:gochecknoglobals // immutable escaper, safe to share
var xmlEscaper = strings.NewReplacer(
	`&`, "&amp;",
	`<`, "&lt;",
	`>`, "&gt;",
	`"`, "&quot;",
	`'`, "&apos;",
)

func escapeXML(s string) string {
	return xmlEscaper.Replace(s)
}

// collectInlineSVGItem flattens an inline <svg> into one replaced inline item.
// paint=false so emitInlineImage places the bitmap at the line position. The
// built box rides along in the item; the line attributes the raster op to it so
// the element keeps a retained box.
func (e *engine) collectInlineSVGItem(node *html.Node, sty ResolvedStyle, out *[]inlineItem) {
	svgBox := e.buildInlineSVG(node, sty, 0, 0, false)
	*out = append(*out, inlineItem{ //nolint:exhaustruct // intentional zero fields
		img: true, w: svgBox.w, h: svgBox.height, style: e.stylePtr(node),
		imgRef:  svgBox.img,
		svgBox:  svgBox,
		marginL: e.scalePt(sty.MarginLeft), marginR: e.scalePt(sty.MarginRight),
		marginT: e.scalePt(sty.MarginTop), marginB: e.scalePt(sty.MarginBottom),
		// Replaced elements rest their bottom margin edge on the baseline.
		marginBaseline: true,
	})
}

// attributeInlineSVGBox retains the box of an inline-level <svg> and
// attributes the image op the line just emitted to it. The range starts at the
// item's first border op and ends on the raster, the same chrome-then-content
// order a block-level SVG keeps, so boxForID finds the element through the
// ordinary box tree walk and svgImageOpIndex can replace the payload. Nothing
// is attached when the line emitted no raster (noEmit, a hidden style, or a
// failed decode), which keeps a missing SVG missing instead of faking a box.
func (e *engine) attributeInlineSVGBox(item *inlineItem, parent *box, start int, leftX, top float64) {
	boxNode := item.svgBox
	if boxNode == nil || parent == nil || start < 0 || start >= len(e.ops) {
		return
	}

	end := len(e.ops) - 1
	if end < start || e.ops[end].Kind != OpImage {
		return
	}

	boxNode.x = leftX
	boxNode.y = top
	boxNode.w = item.w
	boxNode.height = item.h
	boxNode.opStart = start
	boxNode.opEnd = end
	parent.children = append(parent.children, boxNode)
}

// SVGPaintOverride restyles the used paint of one inline <svg> element or of
// one SVG part inside it. The Has* flags select the properties the override
// changes; a field whose flag is false keeps the used value the cascade
// produced.
type SVGPaintOverride struct {
	// ElementID is the id of the <svg> element to restyle.
	ElementID string
	// PartID is the id of one element inside the SVG subtree. Empty means
	// the <svg> root.
	PartID string

	Fill             string
	FillOpacity      float64
	Stroke           string
	StrokeOpacity    float64
	StrokeWidth      float64
	StrokeDashArray  string
	StrokeDashOffset float64

	HasFill, HasFillOpacity     bool
	HasStroke, HasStrokeOpacity bool
	HasStrokeWidth              bool
	HasStrokeDashArray          bool
	HasStrokeDashOffset         bool
}

// SVGPaintRequest is one inline <svg> element and the overrides to apply to
// it. A request that changes no paint property is skipped.
type SVGPaintRequest struct {
	Node      *html.Node
	Overrides []SVGPaintOverride
}

// SVGPaintResult is the engine-side outcome of restyling one inline <svg>.
type SVGPaintResult struct {
	// Element carries the restyled element's operations and boxes after the
	// patch. Its operation slice aliases the patched Result.
	Element *Result
	// OldImage and NewImage are the image operation before and after the
	// restyle. A paint change replaces the encoded payload and the raster
	// pixel bounds, not the painted rectangle.
	OldImage Op
	NewImage Op
}

// RestyleSVGPaint re-serializes and re-rasterizes inline <svg> elements with
// paint overrides applied to their used styles, then replaces the image
// payloads in prev. It does not re-cascade CSS and does not re-place the
// element: a paint change does not change the SVG's intrinsic size.
//
// Both a block-level SVG and an inline-level SVG work. An inline-level icon
// keeps the box the line built at collection time; the line attributes its
// raster op to that box, so the box tree holds it exactly like a block-level
// SVG. A hidden SVG, or one whose raster failed to build, keeps no box and is
// rejected.
//
// Each request names one inline <svg> by node, and every override in that
// request belongs to it. An override with PartID set changes one SVG child by
// id; the override enters that child's used style before serialization. A
// root override is inherited by the descendants whose paint the cascade
// inherited from the root, so a shape with its own paint keeps it.
//
// The raster is cached on prev by its serialized document, so a call that
// repeats the same paint for the same element reuses the previous raster
// instead of rasterizing again. The cache is bounded and dropped with prev.
//
// prev is patched in place. The call is all-or-nothing: every request is
// serialized and rasterized before any operation is replaced, so a failure
// leaves prev untouched. A request that changes no paint property produces no
// result and no raster work. The returned results carry the element's
// operations and boxes after the patch, and the image operation before and
// after the change.
func RestyleSVGPaint(
	ctx context.Context, prev *Result, requests []SVGPaintRequest, opts Options,
) ([]SVGPaintResult, error) {
	if ctx == nil {
		return nil, errs.ErrNilContext
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("layout: context: %w", err)
	}

	if prev == nil || prev.boxTreeRoot() == nil {
		return nil, errNoPreviousLayout
	}

	if err := opts.validate(); err != nil {
		return nil, err
	}

	prepared := make([]svgPaintPrep, 0, len(requests))
	seen := make(map[*box]bool, len(requests))

	for _, request := range requests {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("layout: context: %w", err)
		}

		prep, err := prepareSVGPaint(ctx, prev, request, opts)
		if err != nil {
			return nil, err
		}

		if prep == nil {
			continue
		}

		if seen[prep.box] {
			return nil, fmt.Errorf(
				"layout: inline svg %q is restyled twice in one call",
				prep.box.node.Attribute("id"),
			)
		}

		seen[prep.box] = true
		prepared = append(prepared, *prep)
	}

	if len(prepared) == 0 {
		return nil, nil
	}

	ops := make([]Op, len(prev.Ops))
	copy(ops, prev.Ops)

	for index := range prepared {
		ops[prepared[index].opIndex] = prepared[index].newOp
	}

	prev.Ops = ops

	results := make([]SVGPaintResult, len(prepared))

	for index := range prepared {
		data, width, height := prepared[index].newOp.ImageBytes()
		prepared[index].box.img.data = data
		prepared[index].box.img.w = width
		prepared[index].box.img.h = height

		results[index] = SVGPaintResult{
			Element:  svgPaintElementResult(prev, prepared[index].box),
			OldImage: prepared[index].oldOp,
			NewImage: prepared[index].newOp,
		}
	}

	return results, nil
}

// svgPaintPrep is one fully serialized and rasterized element, ready to
// replace its image operation.
type svgPaintPrep struct {
	box     *box
	opIndex int
	oldOp   Op
	newOp   Op
}

// prepareSVGPaint serializes and rasterizes one element without mutating prev.
// It returns nil when the request changes no paint property.
func prepareSVGPaint(
	ctx context.Context, prev *Result, request SVGPaintRequest, opts Options,
) (*svgPaintPrep, error) {
	node := request.Node
	if node == nil {
		return nil, errNilElementNode
	}

	if node.Type != html.ElementNode || node.Name != cssTagSVG {
		return nil, fmt.Errorf("layout: %q is not an inline svg", node.Name)
	}

	elementID := node.Attribute("id")
	if elementID == "" {
		return nil, fmt.Errorf("%w: the inline svg has no id", errElementNotPlaced)
	}

	boxNode := prev.boxForID(elementID)
	if boxNode == nil || boxNode.node != node {
		return nil, errElementNotPlaced
	}

	opIndex := svgImageOpIndex(prev.Ops, boxNode)
	if opIndex < 0 {
		return nil, fmt.Errorf("layout: inline svg %q emitted no image", elementID)
	}

	eng := newEngine(ctx, opts, nil, nil, prev.styles, nil, nil)

	pass, err := newSVGPaintPass(eng, node, request.Overrides)
	if err != nil {
		return nil, err
	}

	if len(pass.styles) == 0 {
		return nil, nil
	}

	data := eng.serializeInlineSVGWithPaint(node, pass)

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("layout: svg paint: %w", err)
	}

	cache := prev.svgRasterCache()
	// The key carries the cap that sized the raster, so the same document
	// restyled under a different svgRasterMaxDim cannot share pixels.
	key := svgInlineCacheKey(data, svgRasterMaxDim)

	png, width, height, cached := cache.get(key)
	if !cached {
		var err error

		png, width, height, err = svg.RasterizeContext(eng.rasterContext(), data, svgRasterMaxDim)
		if err != nil {
			return nil, fmt.Errorf("layout: rasterize inline svg %q: %w", elementID, err)
		}

		if len(png) == 0 {
			return nil, fmt.Errorf("layout: rasterize inline svg %q: empty raster", elementID)
		}

		cache.put(key, png, width, height)
	}

	oldOp := prev.Ops[opIndex]

	return &svgPaintPrep{
		box:     boxNode,
		opIndex: opIndex,
		oldOp:   oldOp,
		newOp:   oldOp.withImage(png, width, height, oldOp.ImageAlt()),
	}, nil
}

// svgImageOpIndex returns the index of the raster operation of one inline SVG
// box. Chrome prepends the box's background and border operations, so the SVG
// raster is the last image operation in the box's range. The box keeps the
// pixel slice it baked, so a matching payload identifies the raster even when
// a later operation in the range is also an image.
func svgImageOpIndex(ops []Op, boxNode *box) int {
	if boxNode == nil || boxNode.opStart < 0 ||
		boxNode.opEnd < boxNode.opStart || boxNode.opEnd >= len(ops) {
		return -1
	}

	var source []byte

	if boxNode.img != nil {
		source = boxNode.img.data
	}

	fallback := -1

	for index := boxNode.opEnd; index >= boxNode.opStart; index-- {
		if ops[index].Kind != OpImage {
			continue
		}

		data, _, _ := ops[index].ImageBytes()
		if len(data) == 0 {
			continue
		}

		if fallback < 0 {
			fallback = index
		}

		if len(source) > 0 && &data[0] == &source[0] {
			return index
		}
	}

	return fallback
}

// svgPaintElementResult slices one box's operations into a subtree result the
// public patch reads, with the same shape relayoutSubtreeResult returns.
func svgPaintElementResult(prev *Result, boxNode *box) *Result {
	start, end := boxNode.opStart, boxNode.opEnd
	subRoot := rebaseBox(boxNode, start)

	boxes := make([]*box, 0, 1)
	flattenBoxes(subRoot, &boxes)

	return &Result{ //nolint:exhaustruct // a subtree result keeps the zero fields
		Ops:     prev.Ops[start : end+1],
		Width:   prev.Width,
		Height:  prev.Height,
		root:    subRoot,
		rootBox: subRoot,
		boxes:   boxes,
		styles:  prev.styles,
	}
}

// svgPaintValues is one override after the engine parsed its strings: colors
// are used RGB, lengths are points, and the dash list is the cascade's form.
type svgPaintValues struct {
	fillSet  bool
	fillNone bool
	fill     [3]float64

	fillOpacitySet bool
	fillOpacity    float64

	strokeSet  bool
	strokeNone bool
	stroke     [3]float64

	strokeOpacitySet bool
	strokeOpacity    float64

	strokeWidthSet bool
	strokeWidth    float64

	strokeDashArraySet bool
	strokeDashArray    []float64

	strokeDashOffsetSet bool
	strokeDashOffset    float64
}

// svgPaintFields records which paint properties an override sets on one
// element. The serializer uses it to drop the element's authored presentation
// attribute so the override's used style wins.
type svgPaintFields struct {
	fill             bool
	fillOpacity      bool
	stroke           bool
	strokeOpacity    bool
	strokeWidth      bool
	strokeDashArray  bool
	strokeDashOffset bool
}

// svgPaintPass carries the used styles and local paint fields of one
// re-serialization. A nil pass leaves the serializer unchanged.
type svgPaintPass struct {
	styles   map[*html.Node]*ResolvedStyle
	fields   map[*html.Node]svgPaintFields
	fillNone map[*html.Node]bool
}

// suppresses reports whether the override on this element replaces the
// element's authored presentation attribute.
func (pass *svgPaintPass) suppresses(node *html.Node, attr string) bool {
	if pass == nil {
		return false
	}

	fields, ok := pass.fields[node]
	if !ok {
		return false
	}

	switch attr {
	case "fill":
		return fields.fill
	case "fill-opacity":
		return fields.fillOpacity
	case "stroke":
		return fields.stroke
	case "stroke-opacity":
		return fields.strokeOpacity
	case "stroke-width":
		return fields.strokeWidth
	case "stroke-dasharray":
		return fields.strokeDashArray
	case "stroke-dashoffset":
		return fields.strokeDashOffset
	}

	return false
}

// paintsNoFill reports whether the override paints the element's fill as
// none. The serializer writes fill="none" for it without using the
// fill-opacity zero the cascade keeps for its own fill:none value, so a
// descendant that states its own fill does not inherit an opacity of zero.
func (pass *svgPaintPass) paintsNoFill(node *html.Node) bool {
	if pass == nil {
		return false
	}

	return pass.fillNone[node]
}

// newSVGPaintPass resolves the overrides into used-style copies for the
// element and every descendant that inherits from an overridden paint. A
// descendant keeps its own paint when it declares the property itself.
func newSVGPaintPass(
	eng *engine, root *html.Node, overrides []SVGPaintOverride,
) (*svgPaintPass, error) {
	rootRaw, partRaw := mergeSVGPaintOverrides(overrides)

	if err := validateSVGPaintParts(root, partRaw); err != nil {
		return nil, err
	}

	pass := &svgPaintPass{
		styles:   make(map[*html.Node]*ResolvedStyle),
		fields:   make(map[*html.Node]svgPaintFields),
		fillNone: make(map[*html.Node]bool),
	}

	if !eng.hasStyle(root) {
		return nil, fmt.Errorf("layout: inline svg has no resolved style")
	}

	rootBase := eng.stylePtr(root)

	var walk func(node *html.Node, base *ResolvedStyle, inherited svgPaintValues, isRoot bool) error

	walk = func(node *html.Node, base *ResolvedStyle, inherited svgPaintValues, isRoot bool) error {
		var local svgPaintValues

		if isRoot {
			values, err := parseSVGPaintValues(base, rootRaw)
			if err != nil {
				return err
			}

			local.overlay(values)
		}

		if raw, ok := partRaw[node.Attribute("id")]; ok {
			values, err := parseSVGPaintValues(base, raw)
			if err != nil {
				return err
			}

			local.overlay(values)
		}

		effective := inherited
		effective.overlay(local)

		if effective.any() {
			used := *base
			effective.apply(&used)
			pass.styles[node] = &used
			pass.fields[node] = effective.fields()

			if effective.fillSet && effective.fillNone {
				pass.fillNone[node] = true
			}
		}

		for _, child := range node.Children {
			if child.Type != html.ElementNode || !eng.hasStyle(child) {
				continue
			}

			childBase := eng.stylePtr(child)
			childInherited := effective.inherit(base, child, childBase)

			if err := walk(child, childBase, childInherited, false); err != nil {
				return err
			}
		}

		return nil
	}

	if err := walk(root, rootBase, svgPaintValues{}, true); err != nil {
		return nil, err
	}

	// Register the copies on the engine so the serializer reads them through
	// stylePtr while it bakes each element's presentation attributes.
	for node, style := range pass.styles {
		eng.styleOverrides = append(eng.styleOverrides, styleOverride{node: node, style: style})
	}

	return pass, nil
}

// mergeSVGPaintOverrides folds repeated overrides per target in slice order,
// so the last value of a property wins.
func mergeSVGPaintOverrides(
	overrides []SVGPaintOverride,
) (SVGPaintOverride, map[string]SVGPaintOverride) {
	var root SVGPaintOverride

	parts := make(map[string]SVGPaintOverride)

	for _, override := range overrides {
		if override.PartID == "" {
			root = mergeSVGPaintOverride(root, override)

			continue
		}

		parts[override.PartID] = mergeSVGPaintOverride(parts[override.PartID], override)
	}

	return root, parts
}

// mergeSVGPaintOverride overlays the fields src sets onto dst.
func mergeSVGPaintOverride(dst, src SVGPaintOverride) SVGPaintOverride {
	if src.HasFill {
		dst.HasFill = true
		dst.Fill = src.Fill
	}

	if src.HasFillOpacity {
		dst.HasFillOpacity = true
		dst.FillOpacity = src.FillOpacity
	}

	if src.HasStroke {
		dst.HasStroke = true
		dst.Stroke = src.Stroke
	}

	if src.HasStrokeOpacity {
		dst.HasStrokeOpacity = true
		dst.StrokeOpacity = src.StrokeOpacity
	}

	if src.HasStrokeWidth {
		dst.HasStrokeWidth = true
		dst.StrokeWidth = src.StrokeWidth
	}

	if src.HasStrokeDashArray {
		dst.HasStrokeDashArray = true
		dst.StrokeDashArray = src.StrokeDashArray
	}

	if src.HasStrokeDashOffset {
		dst.HasStrokeDashOffset = true
		dst.StrokeDashOffset = src.StrokeDashOffset
	}

	return dst
}

// validateSVGPaintParts checks that every part id an override names exists in
// the SVG subtree. A part that does not exist is an error: the caller asked
// for a shape the serialized document does not have.
func validateSVGPaintParts(root *html.Node, parts map[string]SVGPaintOverride) error {
	if len(parts) == 0 {
		return nil
	}

	found := make(map[string]bool, len(parts))

	root.Walk(func(node *html.Node) {
		if node.Type != html.ElementNode {
			return
		}

		id := node.Attribute("id")
		if id == "" {
			return
		}

		if _, wanted := parts[id]; wanted {
			found[id] = true
		}
	})

	for id := range parts {
		if !found[id] {
			return fmt.Errorf("layout: inline svg has no part with id %q", id)
		}
	}

	return nil
}

// fields reports which paint properties the effective values set. The
// serializer uses it to drop an authored presentation attribute so the
// override's used style wins.
func (values svgPaintValues) fields() svgPaintFields {
	return svgPaintFields{
		fill:             values.fillSet,
		fillOpacity:      values.fillOpacitySet,
		stroke:           values.strokeSet,
		strokeOpacity:    values.strokeOpacitySet,
		strokeWidth:      values.strokeWidthSet,
		strokeDashArray:  values.strokeDashArraySet,
		strokeDashOffset: values.strokeDashOffsetSet,
	}
}

// parseSVGPaintValues parses one override with the engine's color and dash
// parsers. The color keywords resolve against the element's used color.
func parseSVGPaintValues(
	base *ResolvedStyle, override SVGPaintOverride,
) (svgPaintValues, error) {
	var values svgPaintValues

	if override.HasFill {
		color, none, err := svgPaintColor(base, override.Fill)
		if err != nil {
			return values, fmt.Errorf("layout: svg fill: %w", err)
		}

		values.fillSet = true
		values.fill = color
		values.fillNone = none
	}

	if override.HasFillOpacity {
		opacity, err := svgPaintOpacity(override.FillOpacity)
		if err != nil {
			return values, fmt.Errorf("layout: svg fill-opacity: %w", err)
		}

		values.fillOpacitySet = true
		values.fillOpacity = opacity
	}

	if override.HasStroke {
		color, none, err := svgPaintColor(base, override.Stroke)
		if err != nil {
			return values, fmt.Errorf("layout: svg stroke: %w", err)
		}

		values.strokeSet = true
		values.stroke = color
		values.strokeNone = none
	}

	if override.HasStrokeOpacity {
		opacity, err := svgPaintOpacity(override.StrokeOpacity)
		if err != nil {
			return values, fmt.Errorf("layout: svg stroke-opacity: %w", err)
		}

		values.strokeOpacitySet = true
		values.strokeOpacity = opacity
	}

	if override.HasStrokeWidth {
		width, err := svgPaintLength(override.StrokeWidth)
		if err != nil {
			return values, fmt.Errorf("layout: svg stroke-width: %w", err)
		}

		values.strokeWidthSet = true
		values.strokeWidth = width
	}

	if override.HasStrokeDashArray {
		dashed := *base
		dashed.StrokeDashArray = nil
		applyStrokeDashArray(&dashed, override.StrokeDashArray, base.FontSize)
		values.strokeDashArraySet = true
		values.strokeDashArray = dashed.StrokeDashArray
	}

	if override.HasStrokeDashOffset {
		offset, err := svgPaintNumber(override.StrokeDashOffset)
		if err != nil {
			return values, fmt.Errorf("layout: svg stroke-dashoffset: %w", err)
		}

		values.strokeDashOffsetSet = true
		values.strokeDashOffset = offset
	}

	return values, nil
}

// svgPaintColor parses one fill or stroke value. none is the no-paint
// keyword; every other value is a CSS color resolved against the element's
// used color.
func svgPaintColor(base *ResolvedStyle, raw string) ([3]float64, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return [3]float64{}, false, fmt.Errorf("empty value")
	}

	if strings.EqualFold(value, cssDisplayNone) {
		return [3]float64{}, true, nil
	}

	color, ok := parseUsedColor(value, base.Color)
	if !ok {
		return [3]float64{}, false, fmt.Errorf("%q is not a color", value)
	}

	return color, false, nil
}

// svgPaintOpacity validates an opacity override and clamps it to [0, 1].
func svgPaintOpacity(value float64) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%g is not finite", value)
	}

	return clamp01(value), nil
}

// svgPaintLength validates a non-negative length override. A negative used
// value clamps to zero, the used value of a stroke width below the minimum.
func svgPaintLength(value float64) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%g is not finite", value)
	}

	if value < 0 {
		return 0, nil
	}

	return value, nil
}

// svgPaintNumber validates a finite signed number override.
func svgPaintNumber(value float64) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%g is not finite", value)
	}

	return value, nil
}

// any reports whether the values change any paint property.
func (values svgPaintValues) any() bool {
	return values.fillSet || values.fillOpacitySet ||
		values.strokeSet || values.strokeOpacitySet || values.strokeWidthSet ||
		values.strokeDashArraySet || values.strokeDashOffsetSet
}

// overlay replaces the properties local sets.
func (values *svgPaintValues) overlay(local svgPaintValues) {
	if local.fillSet {
		values.fillSet = true
		values.fill = local.fill
		values.fillNone = local.fillNone
	}

	if local.fillOpacitySet {
		values.fillOpacitySet = true
		values.fillOpacity = local.fillOpacity
	}

	if local.strokeSet {
		values.strokeSet = true
		values.stroke = local.stroke
		values.strokeNone = local.strokeNone
	}

	if local.strokeOpacitySet {
		values.strokeOpacitySet = true
		values.strokeOpacity = local.strokeOpacity
	}

	if local.strokeWidthSet {
		values.strokeWidthSet = true
		values.strokeWidth = local.strokeWidth
	}

	if local.strokeDashArraySet {
		values.strokeDashArraySet = true
		values.strokeDashArray = local.strokeDashArray
	}

	if local.strokeDashOffsetSet {
		values.strokeDashOffsetSet = true
		values.strokeDashOffset = local.strokeDashOffset
	}
}

// apply writes the values into one used-style copy.
func (values svgPaintValues) apply(style *ResolvedStyle) {
	if values.fillSet {
		// The cascade stores fill:none as black at opacity zero. When an
		// override replaces the fill, that zero is an artifact of the
		// representation, not a declared fill-opacity, so start from the
		// initial one. An explicit fill-opacity override is applied below and
		// still wins.
		if style.FillSet && style.FillOpacity == 0 && style.Fill == [3]float64{} {
			style.FillOpacity = 1
		}

		style.FillSet = true

		if values.fillNone {
			style.Fill = [3]float64{}
		} else {
			style.Fill = values.fill
		}
	}

	if values.fillOpacitySet {
		style.FillOpacity = values.fillOpacity
	}

	if values.strokeSet {
		if values.strokeNone {
			style.Stroke = [3]float64{}
			style.StrokeSet = false
		} else {
			style.Stroke = values.stroke
			style.StrokeSet = true
		}
	}

	if values.strokeOpacitySet {
		style.StrokeOpacity = values.strokeOpacity
	}

	if values.strokeWidthSet {
		style.StrokeWidth = values.strokeWidth
		style.StrokeWidthSet = true
	}

	if values.strokeDashArraySet {
		style.StrokeDashArray = values.strokeDashArray
	}

	if values.strokeDashOffsetSet {
		style.StrokeDashOffset = values.strokeDashOffset
	}
}

// inherit returns the properties a child inherits from this element. A child
// keeps its own paint when it declares the property, and a value the cascade
// did not inherit (the child differs from the parent's used value) is its
// own.
func (values svgPaintValues) inherit(
	parentBase *ResolvedStyle, child *html.Node, childBase *ResolvedStyle,
) svgPaintValues {
	var inherited svgPaintValues

	if values.fillSet && !svgNodeDeclaresPaint(child, "fill") &&
		parentBase.FillSet == childBase.FillSet && parentBase.Fill == childBase.Fill {
		inherited.fillSet = true
		inherited.fill = values.fill
		inherited.fillNone = values.fillNone
	}

	if values.fillOpacitySet && !svgNodeDeclaresPaint(child, "fill-opacity") &&
		childBase.FillOpacity == 1 {
		inherited.fillOpacitySet = true
		inherited.fillOpacity = values.fillOpacity
	}

	if values.strokeSet && !svgNodeDeclaresPaint(child, "stroke") &&
		parentBase.StrokeSet == childBase.StrokeSet && parentBase.Stroke == childBase.Stroke {
		inherited.strokeSet = true
		inherited.stroke = values.stroke
		inherited.strokeNone = values.strokeNone
	}

	if values.strokeOpacitySet && !svgNodeDeclaresPaint(child, "stroke-opacity") &&
		childBase.StrokeOpacity == 1 {
		inherited.strokeOpacitySet = true
		inherited.strokeOpacity = values.strokeOpacity
	}

	if values.strokeWidthSet && !svgNodeDeclaresPaint(child, "stroke-width") &&
		parentBase.StrokeWidthSet == childBase.StrokeWidthSet &&
		parentBase.StrokeWidth == childBase.StrokeWidth {
		inherited.strokeWidthSet = true
		inherited.strokeWidth = values.strokeWidth
	}

	if values.strokeDashArraySet && !svgNodeDeclaresPaint(child, "stroke-dasharray") &&
		slices.Equal(parentBase.StrokeDashArray, childBase.StrokeDashArray) {
		inherited.strokeDashArraySet = true
		inherited.strokeDashArray = values.strokeDashArray
	}

	if values.strokeDashOffsetSet && !svgNodeDeclaresPaint(child, "stroke-dashoffset") &&
		parentBase.StrokeDashOffset == childBase.StrokeDashOffset {
		inherited.strokeDashOffsetSet = true
		inherited.strokeDashOffset = values.strokeDashOffset
	}

	return inherited
}

// svgNodeDeclaresPaint reports whether the element states the property itself,
// either as a presentation attribute or in its inline style. The cascade
// keeps no declaration provenance, so this check keeps the common authored
// forms from following an ancestor override. inherit and unset do not declare
// a value of their own.
func svgNodeDeclaresPaint(node *html.Node, prop string) bool {
	if node == nil {
		return false
	}

	if value := strings.TrimSpace(node.Attribute(prop)); value != "" {
		return !svgPaintInheritedKeyword(value)
	}

	value := strings.TrimSpace(svgInlineStyleProp(node.Attribute("style"), prop))
	if value == "" {
		return false
	}

	return !svgPaintInheritedKeyword(value)
}

// svgPaintInheritedKeyword reports whether a declaration takes its value from
// the parent instead of stating one.
func svgPaintInheritedKeyword(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "inherit", "unset", "revert", "revert-layer":
		return true
	default:
		return false
	}
}

// writeSVGExtraPresentationAttrs bakes CSS-only SVG properties that have no
// ResolvedStyle field into presentation attributes. It runs after
// writeSVGPresentationAttrs so the written map keeps authored attributes
// winning over same-name CSS (matching the fill-rule precedent above),
// except CSS d which replaces the d attribute suppressed in writeSVGNode.
// Kept as free functions in this section so the geometry+paint bake arms
// owned elsewhere stay untouched. Reference browser for every branch:
// Chrome 143.0.7499.40.
func writeSVGExtraPresentationAttrs(b *bytes.Buffer, node *html.Node, written *svgWrittenSet, decls *svgStyleDecls) {
	if node == nil || node.Type != html.ElementNode {
		return
	}

	if decls.empty() {
		return
	}

	writeSVGMarkerAttrs(b, decls, written)
	writeSVGTextAnchorAttr(b, node, decls, written)

	// CSS d (SVG2 geometry property, Chrome 121+) sets path data. The pinned
	// canvas rasterizer reads path data only from the d attribute, so the
	// property value is baked as one. Restricted to path: d is invalid
	// elsewhere and canvas ignores it there too.
	if node.Name == "path" {
		if d, ok := svgCSSPathData(decls); ok && !written.has("d") {
			b.WriteString(` d="`)
			b.WriteString(escapeXML(d))
			b.WriteByte('"')
		}
	}
}

// writeSVGTextAnchorAttr bakes text-anchor from inline style into a
// presentation attribute on text containers. Chrome 143.0.7499.40 anchors
// SVG text only (start, middle, end); HTML text is unaffected. The pinned
// canvas rasterizer reads the anchor only as an XML attribute (its
// setAttribute switch has the case, but the style attribute itself is
// skipped above), so without this bake the declaration never moves pixels.
// An authored text-anchor attribute wins by the no-duplicate guard, and
// stylesheet rules stay unbaked like the fill-rule precedent above.
func writeSVGTextAnchorAttr(b *bytes.Buffer, node *html.Node, decls *svgStyleDecls, written *svgWrittenSet) {
	if written.has("text-anchor") {
		return
	}
	switch node.Name {
	case "text", "tspan", "textPath":
		// Baked below.
	default:
		return
	}
	if anchor, ok := svgBakeTextAnchor(decls.prop("text-anchor")); ok {
		b.WriteString(` text-anchor="`)
		b.WriteString(anchor)
		b.WriteByte('"')
	}
}

// svgBakeTextAnchor validates a text-anchor value. Only the three SVG
// anchors reach the canvas text renderer; anything else stays unbaked.
func svgBakeTextAnchor(raw string) (string, bool) {
	switch v := strings.ToLower(strings.TrimSpace(raw)); v {
	case "start", "middle", "end":
		return v, true
	}
	return "", false
}

// writeSVGMarkerAttrs bakes the marker shorthand and the marker-start,
// marker-mid, and marker-end longhands from inline style into presentation
// attributes. Chrome maps these SVG presentation attributes to CSS, so
// style="marker-end:url(#arrow)" renders markers; the pinned canvas renderer
// draws markers at path vertices from url(#id) refs. The marker shorthand
// expands onto every unset longhand because canvas has no shorthand case.
// A lone "none" stays unbaked: it matches the no-marker initial and there is
// nothing to emit.
func writeSVGMarkerAttrs(b *bytes.Buffer, decls *svgStyleDecls, written *svgWrittenSet) {
	shorthand := decls.prop("marker")

	for _, prop := range [3]string{"marker-start", "marker-mid", "marker-end"} {
		if written.has(prop) {
			continue
		}

		val := decls.prop(prop)
		if val == "" {
			val = shorthand
		}

		if !isSVGMarkerRef(val) {
			continue
		}

		b.WriteByte(' ')
		b.WriteString(prop)
		b.WriteString(`="`)
		b.WriteString(escapeXML(strings.TrimSpace(val)))
		b.WriteByte('"')
	}
}

// isSVGMarkerRef reports whether a marker property value points at a marker
// definition. Only url(#id) refs reach the canvas marker renderer; none and
// anything unparsable mean no marker.
func isSVGMarkerRef(val string) bool {
	v := strings.TrimSpace(val)
	if v == "" || strings.EqualFold(v, "none") {
		return false
	}

	return strings.HasPrefix(strings.ToLower(v), "url(") && strings.HasSuffix(v, ")")
}

// svgCSSPathData returns the path data carried by the CSS d property, which
// must have the form path("...") (double or single quotes). "none" and any
// other shape mean no override.
func svgCSSPathData(decls *svgStyleDecls) (string, bool) {
	raw := strings.TrimSpace(decls.prop("d"))
	if raw == "" || strings.EqualFold(raw, "none") {
		return "", false
	}

	if !strings.HasPrefix(strings.ToLower(raw), "path(") || !strings.HasSuffix(raw, ")") {
		return "", false
	}

	inner := strings.TrimSpace(raw[len("path(") : len(raw)-1])
	inner = strings.Trim(inner, `"'`)
	if strings.TrimSpace(inner) == "" {
		return "", false
	}

	return inner, true
}
