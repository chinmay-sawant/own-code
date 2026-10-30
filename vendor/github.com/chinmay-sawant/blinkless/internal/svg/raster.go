// Package svg provides SVG-as-image rasterization for <img src="*.svg">.
// Sole path: github.com/tdewolff/canvas (ParseSVG + rasterizer), which
// handles complex wiki logos (gradients, groups, clipPaths, arcs).
// On canvas failure, Rasterize returns a non-nil error and empty PNG
// (nil bytes, zero size); there is no in-tree fallback rasterizer and
// no ImageMagick/convert shell path.
//
// ponytail: canvas is sole SVG raster path; no second in-tree rasterizer.
package svg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers/rasterizer"
)

const (
	cssDPI            = 96.0
	mmPerInch         = 25.4
	viewBoxNumParts   = 4
	maxSVGBytes       = 32 << 20
	maxSVGProbeBytes  = 4096
	maxRasterSide     = 4096
	defaultRasterSide = 512
	maxEmulateTags    = 100000
	maxRasterLockWait = 30 * time.Second
	// rasterMemoMaxEntries bounds the shared raster memo independently of
	// bytes so many tiny icons cannot grow the map without limit.
	rasterMemoMaxEntries = 128
	// rasterMemoMaxBytes bounds the encoded PNG bytes the memo retains. Flat
	// icons compress to hundreds of bytes, so this holds thousands of them
	// or a few large rasters.
	rasterMemoMaxBytes = 8 << 20
)

var errRasterBusy = errors.New("svg: rasterizer lock wait exceeded")

// Static errors returned by Rasterize; callers can match with errors.Is.
var (
	errNotSVG          = errors.New("svg: not SVG")
	errCanvasEmptySize = errors.New("svg canvas: empty size")
	errCanvasPanic     = errors.New("svg canvas: panic")
	errCanvasZeroPixel = errors.New("svg canvas: zero pixel size")
	errSVGTooLarge     = errors.New("svg: input exceeds byte limit")

	// canvasMu serializes calls to tdewolff/canvas, which uses mutable package-level
	// globals in its path intersection algorithms (bentleyOttmann in path_intersection.go).
	canvasMu sync.Mutex //nolint:gochecknoglobals // guards non-thread-safe tdewolff/canvas package globals
)

// Rasterize decodes SVG XML into a PNG image via tdewolff/canvas only. The
// returned width and height are logical CSS-pixel dimensions. The PNG may
// contain more pixels because small SVGs are supersampled before encoding:
// targets below smallIconMaxDim supersample at most smallIconSuperSample,
// larger small targets up to printSuperSample.
// maxSide caps the longer edge in pixels (default 512).
// On failure (not SVG, parse/draw error, empty size, or canvas panic),
// returns err with nil pngBytes and zero w/h - callers must treat error
// as "no image". There is no second rasterizer or shell fallback.
func Rasterize(data []byte, maxSide int) ([]byte, int, int, error) {
	// Callers that have a context use RasterizeContext. This entry is for
	// tests and helpers that do not.
	return RasterizeContext(context.Background(), data, maxSide)
}

// RasterizeContext is Rasterize with a caller context. A cancelled context
// or a deadline returns before the canvas lock is taken and before drawing
// starts. The lock wait itself stops at the deadline or at maxRasterLockWait.
// Once canvas Draw has started, that library call runs to completion because
// it has no cancel point. The mutex is released when Draw returns.
func RasterizeContext(ctx context.Context, data []byte, maxSide int) ([]byte, int, int, error) {
	if ctx == nil {
		return nil, 0, 0, errNotSVG
	}

	if err := ctx.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("svg raster: %w", err)
	}

	if len(data) > maxSVGBytes {
		return nil, 0, 0, fmt.Errorf("%w: %d bytes, limit %d", errSVGTooLarge, len(data), maxSVGBytes)
	}

	if maxSide = normalizeRasterMaxSide(maxSide); !looksLikeSVG(data) {
		return nil, 0, 0, errNotSVG
	}

	return rasterizeCanvas(ctx, data, maxSide)
}

// lockCanvas waits until the shared canvas mutex is free, the context ends,
// or maxRasterLockWait elapses. TryLock avoids leaving a goroutine holding
// the mutex after the caller has given up.
func lockCanvas(ctx context.Context) error {
	wait := maxRasterLockWait
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
	}

	if wait <= 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("svg raster: %w", err)
		}

		return errRasterBusy
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		if canvasMu.TryLock() {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("svg raster: %w", ctx.Err())
		case <-timer.C:
			return errRasterBusy
		case <-ticker.C:
		}
	}
}

// rasterizeCanvas uses tdewolff/canvas to parse SVG and rasterize to PNG.
// Canvas stores sizes in millimeters (unitless root width/height may be
// treated as mm). We pick resolution so the longer edge is the SVG's
// viewBox/width/height in CSS pixels (capped by maxSide), matching layout's
// 96dpi intrinsic-size model.
//
// Canvas can panic on some malformed paths; recover turns that into a
// clean error so <img src="bad.svg"> does not crash the converter.
const (
	printSuperSample  = 4.0
	minSuperSampleDim = 256
	// smallIconMaxDim bounds the targets the small-icon supersample cap
	// applies to. Icons below this CSS-pixel size supersample at most
	// smallIconSuperSample, so a 16px trade icon encodes 32px, not 64px.
	smallIconMaxDim = 32
	// smallIconSuperSample caps the supersample factor for targets below
	// smallIconMaxDim. Halving the linear factor quarters the encoded
	// pixels, and PNG encode cost scales with pixels.
	smallIconSuperSample = 2.0
	// fastEncodeMinPixels floors the rasters the BestSpeed PNG path handles.
	// At icon scale the default level measures fastest: below this pixel
	// count fixed flate setup dominates and BestSpeed is slower as well as
	// larger (go1.26: 64x64 gradient encodes in ~186us default vs ~245us
	// BestSpeed at equal bytes). At and above this count BestSpeed measures
	// ~10-15% faster for tens of bytes (256x256 gradient: ~1438us/764B
	// default vs ~1250us/824B BestSpeed). Pixels are identical at any level.
	fastEncodeMinPixels = 128 * 128
)

func dpmmScaleFactor(targetW, targetH, maxSide int) float64 {
	maxDim := math.Max(float64(targetW), float64(targetH))
	if maxDim <= 0 || maxDim >= minSuperSampleDim {
		return 1.0
	}

	scale := math.Min(printSuperSample, float64(maxSide)/maxDim)
	if scale < 1.0 {
		return 1.0
	}

	// Icons below smallIconMaxDim stay at or below smallIconSuperSample: the
	// returned logical size is unchanged, so layout sizes the element
	// exactly as before while the encoded pixels quarter.
	if maxDim < smallIconMaxDim && scale > smallIconSuperSample {
		scale = smallIconSuperSample
	}

	return scale
}

// normalizeRasterMaxSide applies the Rasterize maxSide policy: non-positive
// means the default, above the hard cap clamps. The memo key and the
// rasterizer share it so one logical request maps to one entry.
func normalizeRasterMaxSide(maxSide int) int {
	if maxSide <= 0 {
		return defaultRasterSide
	}

	if maxSide > maxRasterSide {
		return maxRasterSide
	}

	return maxSide
}

// encodeRasterPNG encodes one raster to PNG. Rasters at or above
// fastEncodeMinPixels use BestSpeed, where it measures faster than the
// default level; smaller rasters keep the default level, which measures
// fastest at icon scale. Decoded pixels are identical at any level, only
// the byte count changes by tens of bytes.
func encodeRasterPNG(buf *bytes.Buffer, img image.Image, pixW, pixH int) error {
	var err error

	if pixW > 0 && pixH > 0 && pixW*pixH >= fastEncodeMinPixels {
		err = (&png.Encoder{CompressionLevel: png.BestSpeed, BufferPool: nil}).Encode(buf, img)
	} else {
		err = png.Encode(buf, img)
	}

	if err != nil {
		return fmt.Errorf("svg raster: encode: %w", err)
	}

	return nil
}

// encodeAndMemoize PNG-encodes one drawn raster, stores a copy in the shared
// memo, and returns the caller-owned bytes. targetW and targetH are the
// logical CSS-pixel bounds the memo records alongside the PNG.
func encodeAndMemoize(
	data []byte, maxSide int, img image.Image, pixW, pixH, targetW, targetH int,
) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeRasterPNG(&buf, img, pixW, pixH); err != nil {
		return nil, err
	}

	// The memo keeps its own copy; the caller owns the returned slice.
	sharedRasterMemo.put(rasterMemoKeyFor(data, maxSide), bytes.Clone(buf.Bytes()), targetW, targetH)

	return buf.Bytes(), nil
}

// rasterMemoKey identifies one Rasterize result: the SHA-256 of the input
// SVG plus the normalized maxSide. Size is part of the key so the same
// document rasterized under two caps cannot share pixels; caches outside
// this package that key on the serialized document (for example the layout
// restyle cache) must include the size the same way.
type rasterMemoKey struct {
	sum     [sha256.Size]byte
	maxSide int
}

// rasterMemoEntry is one memoized raster: the encoded PNG and its logical
// CSS-pixel bounds.
type rasterMemoEntry struct {
	png  []byte
	w, h int
}

// rasterMemo is a bounded LRU of Rasterize results shared across calls, so
// identical icons (measure and build passes, repeated <img src>, restyle
// replays) skip canvas draw and PNG encode entirely. Only successes are
// stored. Stored slices never escape: get hands out copies, so callers own
// what they receive exactly like the uncached path.
type rasterMemo struct {
	mu      sync.Mutex
	entries map[rasterMemoKey]rasterMemoEntry
	order   []rasterMemoKey
	bytes   int
}

//nolint:gochecknoglobals // cross-call raster memo, bounded by rasterMemoMaxEntries/Bytes
var sharedRasterMemo = newRasterMemo()

func newRasterMemo() *rasterMemo {
	return &rasterMemo{ //nolint:exhaustruct // zero-value mutex, map, order, and counter are intentional
		entries: make(map[rasterMemoKey]rasterMemoEntry),
		order:   make([]rasterMemoKey, 0, rasterMemoMaxEntries),
	}
}

// rasterMemoKeyFor builds the memo key for one request. maxSide must already
// be normalized so one logical request maps to one entry.
func rasterMemoKeyFor(data []byte, maxSide int) rasterMemoKey {
	return rasterMemoKey{sum: sha256.Sum256(data), maxSide: maxSide}
}

// memoizedRaster returns the raster stored for one request, if a previous
// call already rasterized the same input under the same cap.
func memoizedRaster(data []byte, maxSide int) ([]byte, int, int, bool) {
	return sharedRasterMemo.get(rasterMemoKeyFor(data, maxSide))
}

// get returns a copy of the raster stored for key and marks it most recently
// used.
func (memo *rasterMemo) get(key rasterMemoKey) ([]byte, int, int, bool) {
	memo.mu.Lock()
	defer memo.mu.Unlock()

	entry, ok := memo.entries[key]
	if !ok {
		return nil, 0, 0, false
	}

	for index, old := range memo.order {
		if old == key {
			memo.order = append(memo.order[:index], memo.order[index+1:]...)

			break
		}
	}

	memo.order = append(memo.order, key)

	return bytes.Clone(entry.png), entry.w, entry.h, true
}

// put stores one raster, evicting least-recently-used entries until both the
// entry count and the retained byte budget fit. A raster larger than the
// whole byte budget is not stored: it could never fit and would evict every
// useful entry first.
func (memo *rasterMemo) put(key rasterMemoKey, encoded []byte, width, height int) {
	if len(encoded) == 0 || len(encoded) > rasterMemoMaxBytes || width <= 0 || height <= 0 {
		return
	}

	memo.mu.Lock()
	defer memo.mu.Unlock()

	if _, ok := memo.entries[key]; ok {
		return
	}

	size := len(encoded)

	for len(memo.order) >= rasterMemoMaxEntries ||
		(memo.bytes+size > rasterMemoMaxBytes && len(memo.order) > 0) {
		oldest := memo.order[0]
		memo.order = memo.order[1:]
		memo.bytes -= len(memo.entries[oldest].png)
		delete(memo.entries, oldest)
	}

	memo.entries[key] = rasterMemoEntry{png: encoded, w: width, h: height}
	memo.order = append(memo.order, key)
	memo.bytes += size
}

//nolint:nonamedreturns,cyclop // defer-recover must override the result values; SVG raster has inherent branches
func rasterizeCanvas(ctx context.Context, data []byte, maxSide int) (pngBytes []byte, w, h int, err error) {
	if err = lockCanvas(ctx); err != nil {
		return nil, 0, 0, err
	}
	defer canvasMu.Unlock()

	if err = ctx.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("svg raster: %w", err)
	}

	// Identical inputs skip draw and encode. The lookup stays under canvasMu
	// so lock-wait and deadline semantics never change: a caller that cannot
	// take the lock still fails instead of being served a hit.
	if memoPNG, memoW, memoH, ok := memoizedRaster(data, maxSide); ok {
		return memoPNG, memoW, memoH, nil
	}

	defer func() {
		if r := recover(); r != nil {
			pngBytes, w, h = nil, 0, 0
			err = fmt.Errorf("%w: %v", errCanvasPanic, r)
		}
	}()

	// The pinned canvas parser drops fill-rule and shape-rendering in its
	// setAttribute switch even though its core Style and rasterizer honor
	// FillRule. Emulate both here so they observably change raster pixels;
	// without this the baked attributes rasterize identically (pinned gap).
	data = emulateDroppedPresentation(data)
	// Drop text before ParseSVG. canvas getFontFace calls LoadSystemFont
	// while canvasMu is held, and a missing family panics.
	data = stripSVGTextElements(data)

	svgCanvas, err := canvas.ParseSVG(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("svg canvas: %w", err)
	}

	canvasW, canvasH, err := canvasSize(svgCanvas)
	if err != nil {
		return nil, 0, 0, err
	}

	// Intrinsic CSS-pixel size from viewBox / width / height attributes.
	targetW, targetH := svgCSSPixelSize(data, maxSide)
	dpmm := canvasDPMM(canvasW, canvasH, targetW, targetH) * dpmmScaleFactor(targetW, targetH, maxSide)
	pixelW, pixelH := canvasW*dpmm, canvasH*dpmm

	if math.IsNaN(pixelW) || math.IsNaN(pixelH) || pixelW < 0.5 || pixelH < 0.5 ||
		pixelW > maxRasterSide || pixelH > maxRasterSide {
		return nil, 0, 0, errCanvasZeroPixel
	}

	if err = ctx.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("svg raster: %w", err)
	}

	img := rasterizer.Draw(svgCanvas, canvas.DPMM(dpmm), nil)
	bounds := img.Bounds()

	pixW, pixH := bounds.Dx(), bounds.Dy()
	if pixW < 1 || pixH < 1 {
		return nil, 0, 0, errCanvasZeroPixel
	}

	encoded, err := encodeAndMemoize(data, maxSide, img, pixW, pixH, targetW, targetH)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("svg canvas: encode: %w", err)
	}

	return encoded, targetW, targetH, nil
}

// canvasSize returns the parsed canvas size in millimeters, rejecting
// non-finite and non-positive sizes before they reach the rasterizer. NaN
// fails <= 0, so every degenerate case needs an explicit check.
func canvasSize(svgCanvas *canvas.Canvas) (float64, float64, error) {
	canvasW, canvasH := svgCanvas.Size()

	if canvasW <= 0 || canvasH <= 0 ||
		math.IsNaN(canvasW) || math.IsNaN(canvasH) ||
		math.IsInf(canvasW, 0) || math.IsInf(canvasH, 0) {
		return 0, 0, errCanvasEmptySize
	}

	return canvasW, canvasH, nil
}

// canvasDPMM maps the canvas mm size to the target CSS-pixel size: it picks
// the smaller axis ratio so both raster dimensions stay within target.
func canvasDPMM(canvasW, canvasH float64, targetW, targetH int) float64 {
	return math.Min(float64(targetW)/canvasW, float64(targetH)/canvasH)
}

// svgCSSPixelSize returns the target raster size in CSS pixels (capped by
// maxSide), derived from the root SVG viewBox or width/height attributes.
// Only the root element is scanned; no shape parsing.
//
//nolint:cyclop // pixel size scaling with bounds check
func svgCSSPixelSize(data []byte, maxSide int) (int, int) {
	viewW, viewH := rootSVGSize(data)
	if math.IsNaN(viewW) || math.IsInf(viewW, 0) || viewW <= 0 {
		viewW = 100
	}

	if math.IsNaN(viewH) || math.IsInf(viewH, 0) || viewH <= 0 {
		viewH = 100
	}

	maxSide = normalizeRasterMaxSide(maxSide)

	scale := 1.0
	if viewW > float64(maxSide) || viewH > float64(maxSide) {
		scale = float64(maxSide) / math.Max(viewW, viewH)
	}

	pixW := int(math.Ceil(viewW * scale))
	pixH := int(math.Ceil(viewH * scale))

	if pixW < 1 {
		pixW = 1
	} else if pixW > maxSide {
		pixW = maxSide
	}

	if pixH < 1 {
		pixH = 1
	} else if pixH > maxSide {
		pixH = maxSide
	}

	return pixW, pixH
}

// rootSVGSize reads viewBox / width / height from the first <svg> start tag.
func rootSVGSize(data []byte) (float64, float64) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return 0, 0
		}

		elem, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		if strings.ToLower(elem.Name.Local) != "svg" {
			continue
		}

		return svgSizeAttrs(elem)
	}

	return 0, 0
}

// svgSizeAttrs derives the intrinsic size from one root SVG element: the
// viewBox wins, falling back to width/height attributes (in CSS pixels).
func svgSizeAttrs(elem xml.StartElement) (float64, float64) {
	attrs := map[string]string{}
	for _, a := range elem.Attr {
		attrs[strings.ToLower(a.Name.Local)] = a.Value
	}

	width := parseLen(attrs["width"], 0)
	height := parseLen(attrs["height"], 0)

	sizeW, sizeH := width, height

	if vb := attrs["viewbox"]; vb != "" {
		parts := splitNums(vb)
		if len(parts) >= viewBoxNumParts {
			sizeW, sizeH = parts[2], parts[3]
		}
	}

	if sizeW <= 0 {
		sizeW = width
	}

	if sizeH <= 0 {
		sizeH = height
	}

	return sizeW, sizeH
}

func looksLikeSVG(data []byte) bool {
	if len(data) > maxSVGProbeBytes {
		data = data[:maxSVGProbeBytes]
	}

	data = bytes.TrimSpace(data)
	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		data = bytes.TrimSpace(data[3:])
	}

	if len(data) >= 5 && bytes.EqualFold(data[:5], []byte("<?xml")) {
		return true
	}

	return bytesContainsFold(data, []byte("<svg"))
}

func bytesContainsFold(data, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}

	for idx := 0; idx+len(needle) <= len(data); idx++ {
		if bytes.EqualFold(data[idx:idx+len(needle)], needle) {
			return true
		}
	}

	return false
}

func parseLen(raw string, def float64) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}

	raw = strings.TrimSuffix(raw, "px")
	raw = strings.TrimSuffix(raw, "pt")

	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return def
	}

	return f
}

func splitNums(s string) []float64 {
	s = strings.ReplaceAll(s, ",", " ")

	var out []float64

	for _, p := range strings.Fields(s) {
		f, err := strconv.ParseFloat(p, 64)
		if err == nil {
			out = append(out, f)
		}
	}

	return out
}

// emulateDroppedPresentation rewrites SVG bytes so the pinned canvas parser
// observably honors two presentation attributes its setAttribute switch
// drops: fill-rule="evenodd" (core Style.FillRule exists and the rasterizer
// switches winding on it, but the parser never sets it) and
// shape-rendering="crispEdges" (no parser case and no rasterizer switch).
// Reference: Chrome 143.0.7499.40 applies evenodd winding to holed paths and
// snaps crispEdges to device pixels.
//
// Fill-rule emulation reverses the winding of nested straight-line subpaths
// so the parser's fixed nonzero winding paints the evenodd hole. Only
// absolute M/L/H/V/Z paths with exactly two nested subpaths qualify; curves,
// relative moves, and group-inherited rules pass through unmodified (named
// limits below). CrispEdges emulation rounds rect/circle/ellipse/line
// geometry to whole pixels. Inputs without either hint return unchanged.
func emulateDroppedPresentation(data []byte) []byte {
	if !bytesContainsFold(data, []byte("fill-rule")) &&
		!bytesContainsFold(data, []byte("shape-rendering")) {
		return data
	}

	out := emulateTags(data)
	if out == nil {
		return data
	}

	return out
}

// emulateAttr is one parsed tag attribute for rewrite.
type emulateAttr struct {
	name  string
	value string
}

// emulateTags scans top-level tags and rewrites qualifying geometry.
// It returns nil when no tag changes, so callers keep the input slice.
func emulateTags(data []byte) []byte {
	var out bytes.Buffer

	changed := false
	flush := 0
	pos := 0

	tags := 0

	for pos < len(data) {
		tags++
		if tags > maxEmulateTags {
			return nil
		}

		rel := bytes.IndexByte(data[pos:], '<')
		if rel < 0 {
			break
		}

		start := pos + rel

		end, ok := emulateTagEnd(data, start)
		if !ok {
			break
		}

		tagName, attrs, selfClose := emulateParseTag(data, start, end)
		rewritten, modified := emulateRewriteTag(tagName, attrs)

		if !modified {
			pos = start + 1

			continue
		}

		changed = true

		out.Write(data[flush:start])
		out.WriteString(emulateEmitTag(tagName, attrs, rewritten, selfClose))

		flush = end + 1
		pos = flush
	}

	if !changed {
		return nil
	}

	out.Write(data[flush:])

	return out.Bytes()
}

// emulateTagEnd returns the index of the tag-closing '>' honoring quotes.
func emulateTagEnd(data []byte, start int) (int, bool) {
	var quote byte

	for idx := start + 1; idx < len(data); idx++ {
		chr := data[idx]

		if quote != 0 {
			if chr == quote {
				quote = 0
			}

			continue
		}

		if chr == '"' || chr == '\'' {
			quote = chr

			continue
		}

		if chr == '>' {
			return idx, true
		}
	}

	return 0, false
}

// emulateScanTagName returns the length of the tag name in inner.
func emulateScanTagName(inner []byte) int {
	for idx, ch := range inner {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '/' {
			return idx
		}
	}

	return len(inner)
}

// emulateParseTag splits one tag span into its lowercased name and attrs.
// Comment, close, and processing-instruction spans report an empty name.
func emulateParseTag(data []byte, start, end int) (string, []emulateAttr, bool) {
	inner := data[start+1 : end]
	if len(inner) == 0 || inner[0] == '/' || inner[0] == '!' || inner[0] == '?' {
		return "", nil, false
	}

	trimmed := bytes.TrimRight(inner, " \t\n\r")
	selfClose := len(trimmed) > 0 && trimmed[len(trimmed)-1] == '/'
	nameEnd := emulateScanTagName(inner)
	tagName := strings.ToLower(string(inner[:nameEnd]))

	pos := start + 1 + nameEnd

	attrs := emulateParseAttrs(data, pos, end)

	return tagName, attrs, selfClose
}

// emulateAttrScanner walks one tag span emitting name="value" pairs.
// Malformed tails are skipped one byte at a time so scanning always makes
// progress and ends at the span end.
type emulateAttrScanner struct {
	data []byte
	pos  int
	end  int
}

// emulateParseAttrs collects name="value" pairs between pos and end.
func emulateParseAttrs(data []byte, pos, end int) []emulateAttr {
	scanner := emulateAttrScanner{data: data, pos: pos, end: end}

	var attrs []emulateAttr

	for scanner.pos < scanner.end {
		attr, ok := scanner.next()
		if ok {
			attrs = append(attrs, attr)
		}
	}

	return attrs
}

// next returns the next attribute and always advances past the consumed
// text, so callers loop to the span end without stalling on junk.
func (s *emulateAttrScanner) next() (emulateAttr, bool) {
	s.skipAttrSpace()

	if s.pos >= s.end {
		return emulateAttr{}, false //nolint:exhaustruct // zero value is the absent attribute
	}

	name, nameOK := s.scanAttrName()
	if !nameOK {
		s.pos++

		return emulateAttr{}, false //nolint:exhaustruct // zero value is the absent attribute
	}

	s.skipAttrSpace()

	if !s.consumeByte('=') {
		s.pos++

		return emulateAttr{}, false //nolint:exhaustruct // zero value is the absent attribute
	}

	s.skipAttrSpace()

	value, valueOK := s.scanAttrValue()
	if !valueOK {
		return emulateAttr{}, false //nolint:exhaustruct // zero value is the absent attribute
	}

	return emulateAttr{name: name, value: value}, true
}

// skipAttrSpace skips blanks and the self-close slash between attributes.
func (s *emulateAttrScanner) skipAttrSpace() {
	for s.pos < s.end {
		ch := s.data[s.pos]
		if ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r' && ch != '/' {
			break
		}

		s.pos++
	}
}

// scanAttrName reads one attribute name at the scan position.
func (s *emulateAttrScanner) scanAttrName() (string, bool) {
	start := s.pos

	for s.pos < s.end && isEmulateNameChar(s.data[s.pos]) {
		s.pos++
	}

	if s.pos <= start {
		return "", false
	}

	return string(s.data[start:s.pos]), true
}

// consumeByte consumes one expected byte at the scan position.
func (s *emulateAttrScanner) consumeByte(want byte) bool {
	if s.pos >= s.end || s.data[s.pos] != want {
		return false
	}

	s.pos++

	return true
}

// scanAttrValue reads one quoted attribute value. A missing opening quote
// skips one byte; an unterminated value ends the span.
func (s *emulateAttrScanner) scanAttrValue() (string, bool) {
	if s.pos >= s.end || (s.data[s.pos] != '"' && s.data[s.pos] != '\'') {
		s.pos++

		return "", false
	}

	quote := s.data[s.pos]
	s.pos++
	start := s.pos

	for s.pos < s.end && s.data[s.pos] != quote {
		s.pos++
	}

	value := string(s.data[start:s.pos])

	if s.pos < s.end {
		s.pos++
	}

	return value, true
}

// isEmulateNameChar reports whether ch can appear in an attribute name.
func isEmulateNameChar(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' ||
		ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == ':' || ch == '.'
}

// emulateRewriteTag applies the fill-rule and crispEdges rewrites to one
// tag's attributes. It returns the replacement values by attribute index.
func emulateRewriteTag(tagName string, attrs []emulateAttr) (map[int]string, bool) {
	lowered := emulateLowerMap(attrs)
	evenOdd := emulateEvenOddRequested(lowered)
	crisp := emulateCrispRequested(lowered)

	rewritten := map[int]string{}

	for idx, attr := range attrs {
		lower := strings.ToLower(attr.name)

		if evenOdd && tagName == "path" && lower == "d" {
			if fixed, ok := emulateEvenOddPathD(attr.value); ok {
				rewritten[idx] = fixed
			}

			continue
		}

		if crisp && emulateCrispAttr(tagName, lower) {
			if snapped, ok := emulateSnapPixel(attr.value); ok {
				rewritten[idx] = snapped
			}
		}
	}

	return rewritten, len(rewritten) > 0
}

// emulateLowerMap indexes attributes by lowercased name.
func emulateLowerMap(attrs []emulateAttr) map[string]string {
	out := make(map[string]string, len(attrs))

	for _, attr := range attrs {
		if _, ok := out[strings.ToLower(attr.name)]; !ok {
			out[strings.ToLower(attr.name)] = attr.value
		}
	}

	return out
}

// emulateEvenOddRequested reports whether the element asks for evenodd
// winding via attribute or inline style. Group inheritance (g fill-rule) is
// not tracked: the scanner is per-tag, so an inherited rule passes through
// unmodified (named limit).
func emulateEvenOddRequested(attrs map[string]string) bool {
	if rule, ok := attrs["fill-rule"]; ok &&
		strings.EqualFold(strings.TrimSpace(rule), "evenodd") {
		return true
	}

	if style, ok := attrs["style"]; ok &&
		strings.EqualFold(emulateStyleProp(style, "fill-rule"), "evenodd") {
		return true
	}

	return false
}

// emulateCrispRequested reports whether the element asks for crispEdges via
// attribute or inline style (camelCase or lowercase).
func emulateCrispRequested(attrs map[string]string) bool {
	if hint, ok := attrs["shape-rendering"]; ok &&
		strings.EqualFold(strings.TrimSpace(hint), "crispedges") {
		return true
	}

	if style, ok := attrs["style"]; ok &&
		strings.EqualFold(emulateStyleProp(style, "shape-rendering"), "crispedges") {
		return true
	}

	return false
}

// emulateStyleProp returns one property value from an inline style string.
func emulateStyleProp(styleAttr, prop string) string {
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

// emulateCrispIn reports whether lower names one of the listed geometry
// attributes.
func emulateCrispIn(lower string, names ...string) bool {
	for _, name := range names {
		if lower == name {
			return true
		}
	}

	return false
}

// emulateCrispAttr reports whether a geometry attribute is pixel-snapped for
// crispEdges. Path data is excluded: rounding curve-heavy d strings would
// distort shapes instead of snapping edges.
func emulateCrispAttr(tagName, lower string) bool {
	switch tagName {
	case "rect":
		return emulateCrispIn(lower, "x", "y", "width", "height", "rx", "ry")
	case "circle":
		return emulateCrispIn(lower, "cx", "cy", "r")
	case "ellipse":
		return emulateCrispIn(lower, "cx", "cy", "rx", "ry")
	case "line":
		return emulateCrispIn(lower, "x1", "y1", "x2", "y2")
	default:
		return false
	}
}

// emulateSnapPixel rounds a plain-number geometry value to whole pixels.
// Values with units stay untouched so the rasterizer keeps resolving them.
func emulateSnapPixel(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}

	num, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return "", false
	}

	rounded := math.Round(num)
	if rounded == num {
		return "", false
	}

	return strconv.FormatFloat(rounded, 'f', -1, 64), true
}

// emulateEmitTag rebuilds one tag with replacement values. Original values
// pass through byte-identical; only rewritten values change (double-quoted).
func emulateEmitTag(
	tagName string, attrs []emulateAttr, rewritten map[int]string, selfClose bool,
) string {
	var out strings.Builder

	out.WriteByte('<')
	out.WriteString(tagName)

	for idx, attr := range attrs {
		value := attr.value
		if fixed, ok := rewritten[idx]; ok {
			value = fixed
		}

		out.WriteByte(' ')
		out.WriteString(attr.name)
		out.WriteString(`="`)
		out.WriteString(value)
		out.WriteByte('"')
	}

	if selfClose {
		out.WriteString("/>")
	} else {
		out.WriteByte('>')
	}

	return out.String()
}

// emulatePathPoint is one absolute vertex of a straight-line subpath.
type emulatePathPoint struct {
	x, y float64
}

const (
	// emulateArityPair is the M/L coordinate count; emulateAritySingle is
	// the H/V coordinate count in straight-line path data.
	emulateArityPair   = 2
	emulateAritySingle = 1
	// emulateAreaHalver halves the shoelace sum into a polygon area.
	emulateAreaHalver = 2
)

// emulateEvenOddPathD rewrites a holed straight-line path so the parser's
// fixed nonzero winding paints the evenodd hole: with exactly two nested
// subpaths of the same winding, the inner subpath is re-emitted reversed.
// It reports false for anything else (curves, relative commands, disjoint
// or already-opposite subpaths), which rasterize correctly either way.
func emulateEvenOddPathD(d string) (string, bool) {
	subs, ok := emulateParseStraightSubs(d)
	if !ok || len(subs) != 2 {
		return "", false
	}

	outer, inner := 0, 1
	if emulateAbsArea(subs[1]) > emulateAbsArea(subs[0]) {
		outer, inner = 1, 0
	}

	if !emulateContainsBox(subs[outer], subs[inner]) {
		return "", false
	}

	if emulateSignedArea(subs[outer])*emulateSignedArea(subs[inner]) <= 0 {
		return "", false
	}

	subs[inner] = emulateReverseSub(subs[inner])

	var out strings.Builder

	for idx, sub := range subs {
		if idx > 0 {
			out.WriteByte(' ')
		}

		emulateWriteSub(&out, sub)
	}

	return out.String(), true
}

// emulateSubParser accumulates absolute vertices per straight-line subpath.
type emulateSubParser struct {
	subs    [][]emulatePathPoint
	cur     emulatePathPoint
	start   emulatePathPoint
	haveCur bool
	curSub  int
}

// emulateParseStraightSubs splits d into absolute vertices per subpath.
// Only M/L/H/V/Z in absolute form qualify; any other command bails out.
func emulateParseStraightSubs(d string) ([][]emulatePathPoint, bool) {
	cmds, args, ok := emulateTokenizePath(d)
	if !ok {
		return nil, false
	}

	parser := emulateSubParser{curSub: -1} //nolint:exhaustruct // zero subpaths accumulate below

	for idx, cmd := range cmds {
		if !parser.apply(cmd, args[idx]) {
			return nil, false
		}
	}

	if len(parser.subs) == 0 {
		return nil, false
	}

	return parser.subs, true
}

// apply folds one tokenized command into the accumulated subpaths.
func (p *emulateSubParser) apply(cmd byte, nums []float64) bool {
	switch cmd {
	case 'M':
		return p.applyMove(nums)
	case 'L':
		return p.applyLine(nums)
	case 'H':
		return p.applyHorizontal(nums)
	case 'V':
		return p.applyVertical(nums)
	case 'Z':
		return p.applyClose(nums)
	default:
		return false
	}
}

// ready reports whether a drawing command has a current subpath.
func (p *emulateSubParser) ready(nums []float64, arity int) bool {
	return p.haveCur && p.curSub >= 0 && len(nums) == arity
}

// applyMove starts a new subpath at one absolute point.
func (p *emulateSubParser) applyMove(nums []float64) bool {
	if len(nums) != emulateArityPair {
		return false
	}

	p.cur = emulatePathPoint{x: nums[0], y: nums[1]}
	p.start = p.cur
	p.haveCur = true

	p.subs = append(p.subs, []emulatePathPoint{p.cur})
	p.curSub = len(p.subs) - 1

	return true
}

// applyLine appends one absolute line segment to the current subpath.
func (p *emulateSubParser) applyLine(nums []float64) bool {
	if !p.ready(nums, emulateArityPair) {
		return false
	}

	p.cur = emulatePathPoint{x: nums[0], y: nums[1]}
	p.subs[p.curSub] = append(p.subs[p.curSub], p.cur)

	return true
}

// applyHorizontal appends one absolute horizontal segment.
func (p *emulateSubParser) applyHorizontal(nums []float64) bool {
	if !p.ready(nums, emulateAritySingle) {
		return false
	}

	p.cur.x = nums[0]
	p.subs[p.curSub] = append(p.subs[p.curSub], p.cur)

	return true
}

// applyVertical appends one absolute vertical segment.
func (p *emulateSubParser) applyVertical(nums []float64) bool {
	if !p.ready(nums, emulateAritySingle) {
		return false
	}

	p.cur.y = nums[0]
	p.subs[p.curSub] = append(p.subs[p.curSub], p.cur)

	return true
}

// applyClose seals the current subpath back at its start point.
func (p *emulateSubParser) applyClose(nums []float64) bool {
	if !p.ready(nums, 0) {
		return false
	}

	p.subs[p.curSub] = append(p.subs[p.curSub], p.start)
	p.cur = p.start

	return true
}

// emulatePathTokenizer accumulates tokenized path commands.
type emulatePathTokenizer struct {
	d       string
	pos     int
	cmds    []byte
	args    [][]float64
	nums    []float64
	cmd     byte
	haveCmd bool
}

// emulateTokenizePath splits d into commands with their numeric arguments.
func emulateTokenizePath(pathData string) ([]byte, [][]float64, bool) {
	tokenizer := emulatePathTokenizer{d: pathData} //nolint:exhaustruct // position and buffers start empty

	for tokenizer.pos < len(tokenizer.d) {
		if !tokenizer.step() {
			return nil, nil, false
		}
	}

	tokenizer.flushCmd()

	if len(tokenizer.cmds) == 0 {
		return nil, nil, false
	}

	return tokenizer.cmds, tokenizer.args, true
}

// step consumes one separator, command letter, or number.
func (t *emulatePathTokenizer) step() bool {
	chr := t.d[t.pos]

	if chr == ' ' || chr == '\t' || chr == '\n' || chr == '\r' || chr == ',' {
		t.pos++

		return true
	}

	if isEmulateCommandLetter(chr) {
		return t.stepLetter(chr)
	}

	if !t.haveCmd {
		return false
	}

	return t.stepNumber()
}

// flushCmd seals the pending command with its collected numbers.
func (t *emulatePathTokenizer) flushCmd() {
	if !t.haveCmd {
		return
	}

	t.cmds = append(t.cmds, t.cmd)
	t.args = append(t.args, t.nums)
	t.nums = nil
}

// stepLetter starts one absolute straight-line command.
func (t *emulatePathTokenizer) stepLetter(chr byte) bool {
	t.flushCmd()

	if chr != 'M' && chr != 'L' && chr != 'H' && chr != 'V' && chr != 'Z' {
		return false
	}

	t.cmd = chr
	t.haveCmd = true
	t.pos++

	return true
}

// stepNumber reads one float argument onto the pending command.
func (t *emulatePathTokenizer) stepNumber() bool {
	num, next, ok := emulateScanNumber(t.d, t.pos)
	if !ok {
		return false
	}

	t.nums = append(t.nums, num)
	t.pos = next

	return true
}

// isEmulateCommandLetter reports whether chr starts a path command.
func isEmulateCommandLetter(chr byte) bool {
	return chr >= 'A' && chr <= 'Z' || chr >= 'a' && chr <= 'z'
}

// emulateScanNumber reads one float starting at pos.
func emulateScanNumber(pathData string, pos int) (float64, int, bool) {
	mantissaEnd, hasDigits := emulateScanMantissa(pathData, emulateSkipNumberSign(pathData, pos))
	if !hasDigits {
		return 0, pos, false
	}

	numEnd := emulateScanExponent(pathData, mantissaEnd)

	num, err := strconv.ParseFloat(pathData[pos:numEnd], 64)
	if err != nil {
		return 0, pos, false
	}

	return num, numEnd, true
}

// emulateSkipNumberSign skips one leading sign of a number.
func emulateSkipNumberSign(pathData string, pos int) int {
	if pos < len(pathData) && (pathData[pos] == '+' || pathData[pos] == '-') {
		return pos + 1
	}

	return pos
}

// emulateScanDigits consumes ASCII digits, reporting the end and whether
// any were found.
func emulateScanDigits(pathData string, pos int) (int, bool) {
	end := pos

	for end < len(pathData) && pathData[end] >= '0' && pathData[end] <= '9' {
		end++
	}

	return end, end > pos
}

// emulateScanMantissa consumes the integer and fraction digit runs of one
// number, reporting the end and whether any digit was found.
func emulateScanMantissa(pathData string, pos int) (int, bool) {
	end, hasDigits := emulateScanDigits(pathData, pos)

	if end < len(pathData) && pathData[end] == '.' {
		fracEnd, hasFrac := emulateScanDigits(pathData, end+1)

		return fracEnd, hasDigits || hasFrac
	}

	return end, hasDigits
}

// emulateScanExponent consumes one optional exponent run; without trailing
// digits the exponent is left for the caller to reject as a separator.
func emulateScanExponent(pathData string, pos int) int {
	if pos >= len(pathData) || (pathData[pos] != 'e' && pathData[pos] != 'E') {
		return pos
	}

	end := emulateSkipNumberSign(pathData, pos+1)

	expEnd, hasDigits := emulateScanDigits(pathData, end)
	if !hasDigits {
		return pos
	}

	return expEnd
}

// emulateSignedArea returns the shoelace signed area of one subpath.
func emulateSignedArea(sub []emulatePathPoint) float64 {
	area := 0.0

	for idx := 0; idx+1 < len(sub); idx++ {
		area += sub[idx].x*sub[idx+1].y - sub[idx+1].x*sub[idx].y
	}

	return area / emulateAreaHalver
}

// emulateAbsArea returns the absolute area of one subpath.
func emulateAbsArea(sub []emulatePathPoint) float64 {
	area := emulateSignedArea(sub)
	if area < 0 {
		return -area
	}

	return area
}

// emulateContainsBox reports whether the outer bbox encloses the inner one.
func emulateContainsBox(outer, inner []emulatePathPoint) bool {
	ominX, ominY, omaxX, omaxY := emulateBBox(outer)
	iminX, iminY, imaxX, imaxY := emulateBBox(inner)

	const epsilon = 1e-9

	return ominX <= iminX+epsilon && ominY <= iminY+epsilon &&
		imaxX <= omaxX+epsilon && imaxY <= omaxY+epsilon
}

// emulateBBox returns the bounding box of one subpath.
func emulateBBox(sub []emulatePathPoint) (float64, float64, float64, float64) {
	minX, minY := sub[0].x, sub[0].y
	maxX, maxY := sub[0].x, sub[0].y

	for _, point := range sub[1:] {
		minX = math.Min(minX, point.x)
		minY = math.Min(minY, point.y)
		maxX = math.Max(maxX, point.x)
		maxY = math.Max(maxY, point.y)
	}

	return minX, minY, maxX, maxY
}

// emulateReverseSub returns the subpath with flipped winding, keeping the
// start vertex so the re-emitted M command stays put.
func emulateReverseSub(sub []emulatePathPoint) []emulatePathPoint {
	closed := len(sub) > 1 && sub[0] == sub[len(sub)-1]

	body := sub
	if closed {
		body = sub[:len(sub)-1]
	}

	out := make([]emulatePathPoint, 0, len(sub))
	out = append(out, body[0])

	for idx := len(body) - 1; idx >= 1; idx-- {
		out = append(out, body[idx])
	}

	out = append(out, body[0])

	return out
}

// emulateWriteSub emits one reversed-capable subpath as M/L/Z commands.
func emulateWriteSub(out *strings.Builder, sub []emulatePathPoint) {
	for idx, point := range sub {
		switch {
		case idx == 0:
			out.WriteString("M" + emulateFormatNum(point.x) + " " + emulateFormatNum(point.y))
		case idx == len(sub)-1:
			out.WriteString(" Z")
		default:
			out.WriteString(" L" + emulateFormatNum(point.x) + " " + emulateFormatNum(point.y))
		}
	}
}

// emulateFormatNum formats one coordinate without exponent notation.
func emulateFormatNum(num float64) string {
	return strconv.FormatFloat(num, 'f', -1, 64)
}
