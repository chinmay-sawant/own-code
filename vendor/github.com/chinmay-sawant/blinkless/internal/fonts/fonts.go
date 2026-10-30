package fonts

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"

	gtfont "github.com/go-text/typesetting/font"
)

var (
	errFontTooShort           = errors.New("font: file too short")
	errFontCFFNotSupported    = errors.New("font: CFF/OTTO OpenType not supported (TrueType outlines only)")
	errFontNotTrueType        = errors.New("font: not a TrueType font")
	errFontTruncatedDirectory = errors.New("font: truncated table directory")
	errFontMissingHead        = errors.New("font: missing head table")
	errFontBadUnitsPerEm      = errors.New("font: bad unitsPerEm")
	errFontMissingMaxp        = errors.New("font: missing maxp table")
	errFontMissingHhea        = errors.New("font: missing hhea table")
	errFontBadNumHMetrics     = errors.New("font: bad numberOfHMetrics")
	errFontMissingHmtx        = errors.New("font: missing hmtx table")
	errFontTruncatedHmtx      = errors.New("font: truncated hmtx table")
	errFontMissingCmap        = errors.New("font: missing cmap table")
	errFontEmptyCmap          = errors.New("font: empty cmap")
	errFontTruncatedCmap4     = errors.New("font: truncated cmap format 4")
	errFontTruncatedCmap4Segs = errors.New("font: truncated cmap format 4 segments")
	errFontTruncatedCmap12    = errors.New("font: truncated cmap format 12")
	errFontTooLarge           = errors.New("font: input exceeds byte limit")
	errFontNotRegular         = errors.New("font: not a regular file")
	errFontTooManyTables      = errors.New("font: too many tables in directory")
	errFontCmapRangeTooLarge  = errors.New("font: cmap range too large")
)

const maxFontBytes = 32 << 20

// maxFontTables caps the SFNT table-directory entry count: generous against
// real fonts (tens of tables) while bounding record scanning and overlap
// checks on hostile input.
const maxFontTables = 64

// maxCmap4Segments caps format-4 segment counts: real fonts use tens of
// segments, and each segment can expand to 65,536 codepoints.
const maxCmap4Segments = 4096

// maxCmapGroupSpan caps one format-12 group's codepoint span so a single
// group cannot drive billions of map insertions.
const maxCmapGroupSpan = 1 << 20

// maxCmapWork bounds total expanded entries across all encoding subtables.
const maxCmapWork = 4 * (unicode.MaxRune + 1)

// Font is a parsed TrueType/OpenType font (table-directory view). It exposes
// the metrics needed for text layout and the glyph data needed to embed and
// subset the font into the PDF.
type Font struct {
	data []byte
	// fingerprint identifies the loaded face independently of its display
	// name. Registries may assign the same PostScriptName to different files;
	// subset caching must not merge those faces.
	fingerprint [32]byte

	// PostScriptName is the PDF /BaseFont label (e.g. LiberationSans-Bold).
	// Empty when the font was loaded without a registry name.
	PostScriptName string

	unitsPerEm    int16
	indexToLocFmt int16
	numGlyphs     int
	ascender      int16 // font units
	descender     int16 // font units
	numHMetrics   int
	xMin, yMin    int16
	xMax, yMax    int16
	macStyle      uint16
	italicAngle   int16
	capHeight     int16
	xHeight       int16 // OS/2 sxHeight; 0 when absent

	tables   map[string][]byte // name -> raw table bytes (for rebuilding subset)
	cmap     map[uint32]uint16 // rune -> glyph id
	cmapWork int
	advance  []int32 // advance width in font units per glyph
	lsb      []int16 // left side bearing per glyph

	// Derived, immutable caches live on the Font next to the data they
	// derive from (locality) and disappear with it (bounds): the go-text
	// face and the reverse cmap are built at most once each.
	gotOnce  sync.Once
	gotFace  *gtfont.Face // parsed go-text face (nil on failure)
	revOnce  sync.Once
	rev      map[uint16]rune
	nameOnce sync.Once
	names    []string

	// lazyParse defers the whole load (asset clone, table directory,
	// fingerprint, metric and cmap parse) to the first accessor call, so a
	// document pays only for the bundled faces it uses. loadData supplies the
	// face bytes and is nil once they are owned; ParseTTF leaves lazyParse
	// false and loads eagerly.
	lazyParse bool
	loadData  func() []byte
	parseOnce sync.Once
	parseErr  error

	// instances caches static faces produced by Instance. The source Font
	// stays the default instance; derived faces have their own glyf/hmtx.
	instMu     sync.Mutex
	instances  map[string]*Font
	isInstance bool

	// advMu guards advUnits, the per-rune advance-units cache. Advance,
	// AdvanceInPoints and GlyphAdvancePoints share it so repeated price
	// labels pay one cmap lookup per distinct rune, not per occurrence.
	advMu    sync.RWMutex
	advUnits map[rune]float64
}

// ParseTTF parses a TrueType (or OpenType with TrueType outlines) font file.
// CFF-based fonts return an error - this writer targets TrueType outlines.
func ParseTTF(data []byte) (*Font, error) {
	font := &Font{data: data} //nolint:exhaustruct // intentional zero-value fields

	if err := font.initFromData(); err != nil {
		return nil, err
	}

	return font, nil
}

// newLazyFont builds a font whose asset bytes are cloned and whose tables are
// parsed on first use. Bundled default faces use it so a document only pays
// for the faces it touches.
func newLazyFont(load func() []byte) *Font {
	return &Font{ //nolint:exhaustruct // intentional zero-value fields
		lazyParse: true,
		loadData:  load,
	}
}

// initFromData validates the sfnt header and table directory, fingerprints the
// bytes, and parses the metric and cmap tables.
func (f *Font) initFromData() error {
	data := f.data

	if len(data) > maxFontBytes {
		return fmt.Errorf("%w: %d bytes, limit %d", errFontTooLarge, len(data), maxFontBytes)
	}

	if len(data) < sfntOffsetTableSize {
		return errFontTooShort
	}

	if !bytes.Equal(data[0:4], []byte{0, 1, 0, 0}) &&
		!bytes.Equal(data[0:4], []byte("true")) {
		if bytes.Equal(data[0:4], []byte("OTTO")) {
			return errFontCFFNotSupported
		}

		return errFontNotTrueType
	}

	tables, err := parseTableDirectory(data)
	if err != nil {
		return err
	}

	f.fingerprint = sha256.Sum256(data)
	f.tables = tables

	return f.parseAll()
}

// ensureParsed loads a lazy font exactly once and runs initFromData. It is a
// no-op for eager fonts and after the first call. A failed lazy load leaves
// the derived fields at their zero values; the bundled assets are compile-time
// constants pinned by TestLazyFaceMatchesEager, so no runtime error path
// exists for them.
func (f *Font) ensureParsed() {
	if f == nil || !f.lazyParse {
		return
	}

	f.parseOnce.Do(func() {
		if f.loadData != nil {
			f.data = f.loadData()
			f.loadData = nil
		}

		f.parseErr = f.initFromData()
	})
}

// parseAll runs the table parsers in dependency order; the first failure
// aborts the parse.
func (f *Font) parseAll() error {
	if err := f.parseHead(); err != nil {
		return err
	}

	if err := f.parseMaxp(); err != nil {
		return err
	}

	if err := f.parseHhea(); err != nil {
		return err
	}

	if err := f.parseHmtx(); err != nil {
		return err
	}

	f.parseOS2()

	return f.parseCmap()
}

func parseTableDirectory(data []byte) (map[string][]byte, error) {
	if len(data) < sfntOffsetTableSize {
		return nil, errFontTruncatedDirectory
	}

	numTables := int(binary.BigEndian.Uint16(data[4:6]))
	if numTables > maxFontTables {
		return nil, errFontTooManyTables
	}

	if sfntOffsetTableSize+sfntTableRecordSize*numTables > len(data) {
		return nil, errFontTruncatedDirectory
	}

	tables := make(map[string][]byte, numTables)

	type tableSpan struct {
		off, end int
	}

	spans := make([]tableSpan, 0, numTables)

	for i := range numTables {
		base := sfntOffsetTableSize + sfntTableRecordSize*i
		rec := data[base : base+sfntTableRecordSize]
		tag := string(rec[0:4])
		offset := uint64(binary.BigEndian.Uint32(rec[8:12]))
		length64 := uint64(binary.BigEndian.Uint32(rec[12:16]))

		if offset > uint64(len(data)) || length64 > uint64(len(data))-offset {
			return nil, fmt.Errorf("font: table %q out of range", tag) //nolint:err113 // dynamic tag in message
		}

		off, length := int(offset), int(length64) //nolint:gosec // both fit within len(data), checked above

		// Reject overlapping or aliased tables: the same bytes must not
		// back two tables, or a crafted directory could double-expose
		// memory through two tags.
		for _, span := range spans {
			if off < span.end && span.off < off+length {
				return nil, fmt.Errorf("font: table %q overlaps another table", tag) //nolint:err113 // dynamic tag in message
			}
		}

		spans = append(spans, tableSpan{off: off, end: off + length})
		tables[tag] = data[off : off+length]
	}

	return tables, nil
}

func (f *Font) parseHead() error {
	tbl, ok := f.tables["head"]
	if !ok || len(tbl) < 54 {
		return errFontMissingHead
	}

	//nolint:gosec // font units are verified <= 0 after conversion; int16 range is the format's
	f.unitsPerEm = int16(binary.BigEndian.Uint16(tbl[18:20]))
	if f.unitsPerEm <= 0 {
		return errFontBadUnitsPerEm
	}

	//nolint:gosec // raw table int16 fields; wrapping is not possible at these fixed offsets
	f.xMin = int16(binary.BigEndian.Uint16(tbl[36:38]))
	//nolint:gosec // raw table int16 fields; wrapping is not possible at these fixed offsets
	f.yMin = int16(binary.BigEndian.Uint16(tbl[38:40]))
	//nolint:gosec // raw table int16 fields; wrapping is not possible at these fixed offsets
	f.xMax = int16(binary.BigEndian.Uint16(tbl[40:42]))
	//nolint:gosec // raw table int16 fields; wrapping is not possible at these fixed offsets
	f.yMax = int16(binary.BigEndian.Uint16(tbl[42:44]))
	f.macStyle = binary.BigEndian.Uint16(tbl[44:46])
	//nolint:gosec // indexToLocFmt is 0 or 1 per spec
	f.indexToLocFmt = int16(binary.BigEndian.Uint16(tbl[50:52]))

	return nil
}

func (f *Font) parseMaxp() error {
	t, ok := f.tables["maxp"]
	if !ok || len(t) < 6 {
		return errFontMissingMaxp
	}

	f.numGlyphs = int(binary.BigEndian.Uint16(t[4:6]))

	return nil
}

func (f *Font) parseHhea() error {
	tbl, ok := f.tables["hhea"]
	if !ok || len(tbl) < 36 {
		return errFontMissingHhea
	}

	//nolint:gosec // raw table int16 fields; the TrueType format stores signed values here
	f.ascender = int16(binary.BigEndian.Uint16(tbl[4:6]))
	//nolint:gosec // raw table int16 fields; the TrueType format stores signed values here
	f.descender = int16(binary.BigEndian.Uint16(tbl[6:8]))
	f.numHMetrics = int(binary.BigEndian.Uint16(tbl[34:36]))

	if f.numHMetrics <= 0 {
		return errFontBadNumHMetrics
	}

	return nil
}

func (f *Font) parseHmtx() error {
	tbl, ok := f.tables["hmtx"]
	if !ok {
		return errFontMissingHmtx
	}

	need := f.numHMetrics * bytesPerHMetric
	if len(tbl) < need {
		return errFontTruncatedHmtx
	}

	// numHMetrics is the number of glyphs that carry their own advance.
	// The spec requires it to be at most numGlyphs. A larger count would
	// index past the advance slice allocated from numGlyphs.
	if f.numGlyphs <= 0 || f.numHMetrics > f.numGlyphs {
		return errFontBadNumHMetrics
	}

	lastAdv := int32(binary.BigEndian.Uint16(tbl[need-4 : need-2]))
	f.advance = make([]int32, f.numGlyphs)
	f.lsb = make([]int16, f.numGlyphs)

	for i := range f.numHMetrics {
		f.advance[i] = int32(binary.BigEndian.Uint16(tbl[i*4 : i*4+2]))
		f.lsb[i] = int16(binary.BigEndian.Uint16(tbl[i*4+2 : i*4+4])) //nolint:gosec // lsb is int16 per hmtx spec
	}

	sideBearings := tbl[need:]

	for i := f.numHMetrics; i < f.numGlyphs; i++ {
		f.advance[i] = lastAdv
		off := (i - f.numHMetrics) * bytesPerLongHorMetricSide

		if off+2 <= len(sideBearings) {
			f.lsb[i] = int16(binary.BigEndian.Uint16(sideBearings[off : off+2])) //nolint:gosec // lsb is int16 per hmtx spec
		}
	}

	return nil
}

func (f *Font) parseOS2() {
	tbl, ok := f.tables["OS/2"]
	if !ok || len(tbl) < 90 {
		// OS/2 optional; use hhea for cap height fallback
		f.capHeight = f.ascender

		return
	}

	// OS/2 version 2+ stores sxHeight at 86 and sCapHeight at 88.
	if v := int16(binary.BigEndian.Uint16(tbl[86:88])); v != 0 { //nolint:gosec // sxHeight is int16 per OS/2
		f.xHeight = v
	}

	if v := int16(binary.BigEndian.Uint16(tbl[88:90])); v != 0 { //nolint:gosec // capHeight is int16 per OS/2 spec
		f.capHeight = v
	} else {
		f.capHeight = f.ascender
	}

	if f.italicAngle == 0 {
		// italic angle comes from 'post' when present (Fixed 16.16 format)
		if post, ok := f.tables["post"]; ok && len(post) >= 6 {
			//nolint:gosec // italicAngle is int16 per post spec
			f.italicAngle = int16(binary.BigEndian.Uint16(post[4:6]))
		}
	}
}

func (f *Font) parseCmap() error {
	tbl, ok := f.tables["cmap"]
	if !ok || len(tbl) < 4 {
		return errFontMissingCmap
	}

	f.cmap = map[uint32]uint16{}
	f.cmapWork = 0
	subs := parseCmapSubtables(tbl)
	mergeCmapSubtables(f, tbl, subs)

	if len(f.cmap) == 0 {
		return errFontEmptyCmap
	}

	return nil
}

// cmapSubtable is one cmap encoding record.
type cmapSubtable struct {
	platform, encoding int
	offset             int
}

func parseCmapSubtables(tbl []byte) []cmapSubtable {
	if len(tbl) < cmapCountHeaderSize {
		return nil
	}

	num := int(binary.BigEndian.Uint16(tbl[2:4]))
	subs := make([]cmapSubtable, 0, num)

	for i := range num {
		if cmapCountHeaderSize+i*8+8 > len(tbl) {
			break
		}

		rec := tbl[cmapCountHeaderSize+i*8:]
		subs = append(subs, cmapSubtable{
			platform: int(binary.BigEndian.Uint16(rec[0:2])),
			encoding: int(binary.BigEndian.Uint16(rec[2:4])),
			offset:   int(binary.BigEndian.Uint32(rec[4:8])),
		})
	}

	return subs
}

// mergeCmapSubtables walks the preferred Unicode encodings in priority order
// so CJK + Hangul + Latin are all covered (DroidSansFallback puts Hangul in
// a format-12 table while format-4 (3,1) alone is incomplete).
func mergeCmapSubtables(fnt *Font, tbl []byte, subs []cmapSubtable) {
	order := [][2]int{{3, 10}, {3, 1}, {0, 4}, {0, 3}, {0, 5}, {0, 2}, {0, 1}, {0, 0}}

	for _, encodingPair := range order {
		for idx := range subs {
			if fnt.cmapWork >= maxCmapWork {
				return
			}

			if subs[idx].platform != encodingPair[0] || subs[idx].encoding != encodingPair[1] {
				continue
			}

			if subs[idx].offset < 0 || subs[idx].offset > len(tbl)-2 {
				continue
			}

			state := tbl[subs[idx].offset:]
			format := int(binary.BigEndian.Uint16(state[0:2]))

			fnt.parseCmapSubtable(state, format)
		}
	}
}

func (f *Font) parseCmapSubtable(state []byte, format int) {
	switch format {
	case cmapFormat4:
		_ = f.parseCmap4(state)
	case cmapFormat12:
		_ = f.parseCmap12(state)
	case cmapFormat6:
		f.parseCmap6(state)
	case 0:
		f.parseCmap0(state)
	}
}

func (f *Font) parseCmap6(state []byte) {
	if len(state) < cmapFormat6Header {
		return
	}

	first := binary.BigEndian.Uint16(state[6:8])
	count := int(binary.BigEndian.Uint16(state[8:10]))

	if 10+2*count > len(state) {
		return
	}

	if !f.reserveCmapWork(count) {
		return
	}

	for index := range count {
		glyph := binary.BigEndian.Uint16(state[10+index*2:])
		if glyph == 0 {
			continue
		}

		cp := uint32(first) + uint32(index) //nolint:gosec // first+count cannot exceed uint32
		if _, ok := f.cmap[cp]; !ok {
			f.cmap[cp] = glyph
		}
	}
}

func (f *Font) parseCmap0(state []byte) {
	if len(state) < cmapFormat0Size {
		return
	}

	if !f.reserveCmapWork(cmapFormat0GlyphCount) {
		return
	}

	for j := range 256 {
		if g := state[6+j]; g != 0 {
			cp := uint32(j) //nolint:gosec // j is bounded by 256
			if _, ok := f.cmap[cp]; !ok {
				f.cmap[cp] = uint16(g)
			}
		}
	}
}

func (f *Font) parseCmap4(state []byte) error {
	if len(state) < cmapFormat4Header {
		return errFontTruncatedCmap4
	}

	segCount := int(binary.BigEndian.Uint16(state[6:8])) / uint16Bytes
	if segCount > maxCmap4Segments {
		return errFontCmapRangeTooLarge
	}

	endOff := 14
	startOff := endOff + uint16Bytes*segCount + uint16Bytes // + reservedPad
	deltaOff := startOff + uint16Bytes*segCount

	rangeOff := deltaOff + uint16Bytes*segCount
	if rangeOff+2*segCount > len(state) {
		return errFontTruncatedCmap4Segs
	}

	for idx := range segCount {
		end := uint32(binary.BigEndian.Uint16(state[endOff+idx*2 : endOff+idx*2+2]))
		start := uint32(binary.BigEndian.Uint16(state[startOff+idx*2 : startOff+idx*2+2]))

		if end == 0xFFFF && start == 0xFFFF {
			continue // final sentinel segment
		}

		if end < start || !f.reserveCmapWork(int(end-start)+1) {
			return errFontCmapRangeTooLarge
		}

		//nolint:gosec // idDelta is signed int16 per cmap-4 spec
		idDelta := int32(int16(binary.BigEndian.Uint16(state[deltaOff+idx*2 : deltaOff+idx*2+2])))
		idRange := int32(binary.BigEndian.Uint16(state[rangeOff+idx*2 : rangeOff+idx*2+2]))

		f.mapCmap4Range(state, idx, start, end, idDelta, idRange, rangeOff)
	}

	return nil
}

// mapCmap4Range expands one format-4 segment's codepoint range into the cmap,
// keeping existing (higher-priority) mappings.
func (f *Font) mapCmap4Range(
	state []byte, idx int, start, end uint32, idDelta, idRange int32, rangeOff int,
) {
	for codepoint := start; codepoint <= end; codepoint++ {
		if _, exists := f.cmap[codepoint]; exists {
			continue
		}

		glyph := cmap4GlyphID(state, idx, codepoint, start, idDelta, idRange, rangeOff)
		if glyph > 0 && glyph < int32(f.numGlyphs) { //nolint:gosec // numGlyphs is small relative to int32
			f.cmap[codepoint] = uint16(glyph) //nolint:gosec // range-checked above
		}
	}
}

func cmap4GlyphID(state []byte, idx int, codepoint, start uint32, idDelta, idRange int32, rangeOff int) int32 {
	if idRange == 0 {
		return (int32(codepoint) + idDelta) & maxUint16Val //nolint:gosec // codepoint is BMP-scoped in format 4
	}

	gAddr := rangeOff + idx*uint16Bytes + int(idRange) + int(codepoint-start)*uint16Bytes
	if gAddr+2 > len(state) {
		return 0
	}

	g := binary.BigEndian.Uint16(state[gAddr:])
	if g == 0 {
		return 0
	}

	return (int32(g) + idDelta) & maxUint16Val
}

func (f *Font) parseCmap12(state []byte) error {
	if len(state) < cmapFormat12Header {
		return errFontTruncatedCmap12
	}

	groups := int(binary.BigEndian.Uint32(state[12:16]))
	for i := range groups {
		if 16+i*12+12 > len(state) {
			return errFontTruncatedCmap12
		}

		rec := state[16+i*12:]
		start := binary.BigEndian.Uint32(rec[0:4])
		end := binary.BigEndian.Uint32(rec[4:8])
		startGlyph := binary.BigEndian.Uint32(rec[8:12])

		if end < start || end > unicode.MaxRune || end-start > maxCmapGroupSpan {
			return errFontCmapRangeTooLarge
		}

		if !f.reserveCmapWork(int(end-start) + 1) {
			return errFontCmapRangeTooLarge
		}

		f.mapCmap12Range(start, end, startGlyph)
	}

	return nil
}

func (f *Font) mapCmap12Range(start, end, startGlyph uint32) {
	for codepoint := start; codepoint <= end; codepoint++ {
		if _, exists := f.cmap[codepoint]; exists {
			continue
		}

		glyph := uint64(startGlyph) + uint64(codepoint-start)
		if glyph < uint64(f.numGlyphs) { //nolint:gosec // numGlyphs is non-negative from maxp
			f.cmap[codepoint] = uint16(glyph) //nolint:gosec // range-checked above
		}
	}
}

func (f *Font) reserveCmapWork(count int) bool {
	if count > maxCmapWork-f.cmapWork {
		f.cmapWork = maxCmapWork

		return false
	}

	f.cmapWork += count

	return true
}

// Bytes returns the raw SFNT face bytes this font was loaded from. It exists
// so a display-list consumer can hand the face to an independent shaper (for
// example text.NewGoTextFaceSource) without re-reading the asset or the
// system. The returned slice aliases the font's own buffer: read it, never
// write it, and treat it as valid only for the life of this Font.
func (f *Font) Bytes() []byte {
	if f == nil {
		return nil
	}

	f.ensureParsed()

	return f.data
}

// UnitsPerEm returns the font's design size.
func (f *Font) UnitsPerEm() int16 {
	f.ensureParsed()

	return f.unitsPerEm
}

// Ascent returns the typographic ascent in font units.
func (f *Font) Ascent() int16 {
	f.ensureParsed()

	return f.ascender
}

// Descent returns the typographic descent (negative) in font units.
func (f *Font) Descent() int16 {
	f.ensureParsed()

	return f.descender
}

// CapHeight returns the cap height in font units.
func (f *Font) CapHeight() int16 {
	f.ensureParsed()

	return f.capHeight
}

// XHeight returns the OS/2 sxHeight in font units, or 0 when the face has no
// usable x-height metric.
func (f *Font) XHeight() int16 {
	f.ensureParsed()

	return f.xHeight
}

// XHeightAspect returns x-height / units-per-em for font-size-adjust. Zero
// means the face has no usable metric and size-adjust must leave size alone.
func (f *Font) XHeightAspect() float64 {
	f.ensureParsed()

	if f.xHeight <= 0 || f.unitsPerEm <= 0 {
		return 0
	}

	return float64(f.xHeight) / float64(f.unitsPerEm)
}

// BBox returns the font bounding box in font units.
func (f *Font) BBox() (int16, int16, int16, int16) {
	f.ensureParsed()

	return f.xMin, f.yMin, f.xMax, f.yMax
}

// Bold reports whether the font declares a bold macStyle.
func (f *Font) Bold() bool {
	f.ensureParsed()

	return f.macStyle&1 != 0
}

// Italic reports whether the font declares an italic macStyle.
func (f *Font) Italic() bool {
	f.ensureParsed()

	return f.macStyle&2 != 0
}

// GlyphID maps a rune to its glyph id (0 = .notdef when missing).
func (f *Font) GlyphID(r rune) uint16 {
	f.ensureParsed()

	if g, ok := f.cmap[uint32(r)]; ok {
		return g
	}

	return 0
}

// FamilyNames returns CSS-friendly family names from the font's name table
// (NameIDs 1 and 16 when present). Empty when the name table is missing.
// See LoadNames for the deliberate PostScriptName fill that backs this.
func (f *Font) FamilyNames() []string {
	return f.LoadNames()
}

// LoadNames reads the name table and makes the PostScriptName fill explicit:
// when f.PostScriptName is still empty, the first NameID 6/4 record becomes
// it (the PDF /BaseFont label). Callers that need the names without the
// mutation can call this once up front. Returns family names (NameIDs 1 and
// 16), or nil when the name table is missing.
func (f *Font) LoadNames() []string {
	if f == nil {
		return nil
	}

	f.ensureParsed()

	f.nameOnce.Do(func() {
		f.names = f.loadNames()
	})

	return f.names
}

func (f *Font) loadNames() []string {
	tbl, ok := f.tables["name"]
	if !ok || len(tbl) < 6 {
		return nil
	}

	count := int(binary.BigEndian.Uint16(tbl[2:4]))
	strOff := int(binary.BigEndian.Uint16(tbl[4:6]))
	seen := map[string]bool{}

	var out []string

	for i := range count {
		rec := sfntNameHeaderSize + i*sfntNameRecordSize
		if rec+12 > len(tbl) {
			break
		}

		nameID := binary.BigEndian.Uint16(tbl[rec+6 : rec+8])
		if !isNameRecordID(nameID) {
			continue
		}

		str, decodable := decodeNameRecord(tbl, strOff, rec)
		if !decodable {
			continue
		}

		if isFamilyNameID(nameID) {
			out = appendUnique(out, seen, str)
		}

		if f.PostScriptName == "" && isPSNameID(nameID) {
			f.PostScriptName = strings.ReplaceAll(str, " ", "")
		}
	}

	return out
}

// isNameRecordID reports whether nameID is a name we consume (family,
// PostScript or both).
func isNameRecordID(nameID uint16) bool {
	return nameID == 1 || nameID == 16 || nameID == 4 || nameID == 6
}

// isFamilyNameID reports whether nameID selects a family name (1 or 16).
func isFamilyNameID(nameID uint16) bool {
	return nameID == 1 || nameID == 16
}

// isPSNameID reports whether nameID selects a PostScript name (4 or 6).
func isPSNameID(nameID uint16) bool {
	return nameID == 6 || nameID == 4
}

// decodeNameRecord decodes one name-table record into a string when its
// platform encoding is supported.
func decodeNameRecord(tbl []byte, strOff, rec int) (string, bool) {
	platform := binary.BigEndian.Uint16(tbl[rec : rec+2])
	encoding := binary.BigEndian.Uint16(tbl[rec+2 : rec+4])
	length := int(binary.BigEndian.Uint16(tbl[rec+8 : rec+10]))
	offset := int(binary.BigEndian.Uint16(tbl[rec+10 : rec+12]))

	start := strOff + offset
	if start < 0 || start+length > len(tbl) {
		return "", false
	}

	raw := tbl[start : start+length]

	switch {
	case platform == 3 && (encoding == 1 || encoding == 10):
		return decodeUTF16BE(raw), true
	case platform == 0:
		return decodeUTF16BE(raw), true
	case platform == 1:
		return string(raw), true
	default:
		return "", false
	}
}

// appendUnique appends str to out unless it is empty or already seen.
func appendUnique(out []string, seen map[string]bool, str string) []string {
	str = strings.TrimSpace(str)
	if str == "" || seen[str] {
		return out
	}

	seen[str] = true

	return append(out, str)
}

func decodeUTF16BE(buf []byte) string {
	if len(buf)%2 != 0 {
		return string(buf)
	}

	u := make([]uint16, len(buf)/uint16Bytes)
	for i := range u {
		u[i] = binary.BigEndian.Uint16(buf[i*2:])
	}

	return string(utf16.Decode(u))
}

// Advance returns the horizontal advance width in font units for a rune.
func (f *Font) Advance(runeValue rune) float64 {
	f.ensureParsed()

	if len(f.advance) == 0 {
		return 0
	}

	return f.advanceUnits(runeValue)
}

// AdvanceInPoints converts an advance from font units to points for a size.
func (f *Font) AdvanceInPoints(runeValue rune, size float64) float64 {
	f.ensureParsed()

	if f.unitsPerEm <= 0 {
		return 0
	}

	return f.advanceUnits(runeValue) / float64(f.unitsPerEm) * size
}

// GlyphAdvancePoints returns the advance width in points for r at size with
// a single cmap lookup: the same glyph table and out-of-range fallback that
// Advance uses, so the result equals AdvanceInPoints(r, size).
func (f *Font) GlyphAdvancePoints(runeValue rune, size float64) float64 {
	f.ensureParsed()

	if f.unitsPerEm <= 0 || len(f.advance) == 0 {
		return 0
	}

	return f.advanceUnits(runeValue) / float64(f.unitsPerEm) * size
}

// glyphOutline returns the glyf bytes for a glyph id (raw, incl. header).
func (f *Font) glyphOutline(glob uint16) []byte {
	f.ensureParsed()

	found, okVal := f.tables["glyf"]
	if !okVal {
		return nil
	}

	loca, okVal := f.tables["loca"]
	if !okVal {
		return nil
	}

	count := int(glob)

	off, next, ok := locaOffsets(loca, f.indexToLocFmt, count)
	if !ok {
		return nil
	}

	if off < 0 || next < off || next > len(found) {
		return nil
	}

	return found[off:next]
}

// locaOffsets resolves glyf byte offsets for a glyph id from the loca table,
// validating the format selector and the entry bounds first.
func locaOffsets(loca []byte, indexToLocFmt int16, count int) (int, int, bool) {
	switch indexToLocFmt {
	case 0:
		if (count+1)*uint16Bytes+uint16Bytes > len(loca) {
			return 0, 0, false
		}

		off := int(binary.BigEndian.Uint16(loca[count*uint16Bytes:])) * uint16Bytes
		next := int(binary.BigEndian.Uint16(loca[(count+1)*uint16Bytes:])) * uint16Bytes

		return off, next, true
	case 1:
		if (count+1)*uint32Bytes+uint32Bytes > len(loca) {
			return 0, 0, false
		}

		off := int(binary.BigEndian.Uint32(loca[count*uint32Bytes:]))
		next := int(binary.BigEndian.Uint32(loca[(count+1)*uint32Bytes:]))

		return off, next, true
	default:
		return 0, 0, false
	}
}

// Runes returns all runes mapped by the font, sorted.
func (f *Font) Runes() []rune {
	f.ensureParsed()

	out := make([]rune, 0, len(f.cmap))
	for cp := range f.cmap {
		out = append(out, rune(cp))
	}

	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })

	return out
}
