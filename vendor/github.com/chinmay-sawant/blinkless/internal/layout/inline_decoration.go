// Decoration position helpers shared by the underline and emphasis
// paint paths: underline Y shifts plus emphasis-skip classification.
package layout

import (
	"strings"
	"unicode"
)

// underlinePositionShift returns the extra underline Y for
// text-underline-position. auto and from-font keep the alphabetic baseline
// spot. under drops the stroke below descenders per CSS Text Decoration 4
// section 2.7. Bare left and right are auto in horizontal text per the same
// section, but this engine maps them to a smaller under drop so the declared
// value stays observable in the drawing list; vertical side placement is not
// modeled.
const (
	underlineUnderDescentRatio = 0.4
	underlineUnderSizeRatio    = 0.15
	underlineSideDescentRatio  = 0.25
	underlineSideSizeRatio     = 0.10
	underlinePositionFromFont  = "from-font"
)

func underlinePositionShift(sty *ResolvedStyle, size, descent float64) float64 {
	if sty == nil {
		return 0
	}

	low := strings.ToLower(strings.TrimSpace(sty.TextUnderlinePosition))
	if low == "" || low == "auto" || low == underlinePositionFromFont {
		return 0
	}

	if strings.Contains(low, "under") {
		extra := descent * underlineUnderDescentRatio
		if floor := size * underlineUnderSizeRatio; extra < floor {
			extra = floor
		}

		return extra
	}

	if strings.Contains(low, "left") || strings.Contains(low, "right") {
		extra := descent * underlineSideDescentRatio
		if floor := size * underlineSideSizeRatio; extra < floor {
			extra = floor
		}

		return extra
	}

	return 0
}

// emphasisSkipModes parses the __emph_skip custom prop per CSS Text
// Decoration 4 section 3.5 (spaces || punctuation || symbols || narrow,
// initial spaces punctuation). Empty means no explicit declaration, so the
// initial applies. A value with no recognized token (for example the
// non-standard all used by older pins) skips nothing so plain letters keep
// their marks.
const (
	emphasisSkipSpaces      = "spaces"
	emphasisSkipPunctuation = "punctuation"
	emphasisSkipSymbols     = "symbols"
	emphasisSkipNarrow      = "narrow"
)

func emphasisSkipModes(style *ResolvedStyle) (bool, bool, bool, bool) {
	var skipSpaces, skipPunct, skipSymbols, skipNarrow bool

	if style == nil || style.CustomProps == nil {
		return true, true, false, false
	}

	raw := strings.ToLower(strings.TrimSpace(style.CustomProps["__emph_skip"]))
	if raw == "" {
		return true, true, false, false
	}

	var recognized bool

	for _, tok := range strings.Fields(raw) {
		switch tok {
		case emphasisSkipSpaces:
			skipSpaces = true
			recognized = true
		case emphasisSkipPunctuation:
			skipPunct = true
			recognized = true
		case emphasisSkipSymbols:
			skipSymbols = true
			recognized = true
		case emphasisSkipNarrow:
			skipNarrow = true
			recognized = true
		}
	}

	if !recognized {
		return false, false, false, false
	}

	return skipSpaces, skipPunct, skipSymbols, skipNarrow
}

// skipEmphasisRune reports whether no emphasis mark is drawn for runic under
// the parsed skip modes. Control, format, and unassigned handling is a
// subset: Cc and Cf are always skipped; Cn needs font tables the engine does
// not carry.
//
//nolint:cyclop // one flat skip check per skip token stays readable
func skipEmphasisRune(runic rune, spaces, punct, symbols, narrow bool) bool {
	if unicode.Is(unicode.Cc, runic) || unicode.Is(unicode.Cf, runic) {
		return true
	}

	if spaces && (unicode.Is(unicode.Z, runic) || runic == '\u00A0') {
		return true
	}

	if punct && unicode.Is(unicode.P, runic) && !isEmphasisSymbolPo(runic) {
		return true
	}

	if symbols && (unicode.Is(unicode.S, runic) || isEmphasisSymbolPo(runic)) {
		return true
	}

	if narrow && isEmphasisNarrow(runic) {
		return true
	}

	return false
}

// isEmphasisSymbolPo reports the Po characters the emphasis skip spec counts
// as symbols rather than punctuation.
func isEmphasisSymbolPo(runic rune) bool {
	switch runic {
	case '#', '%', '&', '@', '\u00A7', '\u00B6',
		'\u0609', '\u060A', '\u066A',
		'\u2030', '\u2031', '\u204A', '\u204B', '\u2053',
		'\u303D':
		return true
	default:
		return false
	}
}

const (
	emphasisNarrowHangulBase = 0x1100
	emphasisHalfwidthStart   = 0xFF61
	emphasisHalfwidthEnd     = 0xFFDC
	emphasisGeneralPunctLo   = 0x2000
	emphasisGeneralPunctHi   = 0x206F
)

// isEmphasisNarrow approximates East_Asian_Width not F or W: text below
// Hangul Jamo plus halfwidth and general punctuation forms count as narrow,
// while CJK wide and fullwidth forms do not.
func isEmphasisNarrow(runic rune) bool {
	if runic < emphasisNarrowHangulBase {
		return true
	}

	if runic >= emphasisHalfwidthStart && runic <= emphasisHalfwidthEnd {
		return true
	}

	if runic >= emphasisGeneralPunctLo && runic <= emphasisGeneralPunctHi {
		return true
	}

	return false
}
