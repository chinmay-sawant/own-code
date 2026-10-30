package html

import (
	stdhtml "html"
	"strings"
)

// Constants for the numeric character reference algorithm.
const (
	maxUnicodeCodePoint   = 0x10FFFF
	surrogateCodePointMin = 0xD800
	surrogateCodePointMax = 0xDFFF
	windows1252Min        = 0x80
	windows1252Max        = 0x9F
	numericRefPrefixLen   = 2 // len("&#")
	decimalBase           = 10
	hexBase               = 16
	hexLetterOffset       = 10
)

// UnescapeEntities decodes HTML character references in text context
// (&amp; -> &, &#NN; / &#xHH; numeric refs). Attribute values use
// decodeCharRefAt with inAttribute=true, which keeps ambiguous and
// historically flushed references literal.
func UnescapeEntities(text string) string {
	if text == "" || !strings.Contains(text, "&") {
		return text
	}

	var builder strings.Builder

	builder.Grow(len(text))

	for pos := 0; pos < len(text); {
		amp := strings.IndexByte(text[pos:], '&')
		if amp < 0 {
			builder.WriteString(text[pos:])

			break
		}

		amp += pos

		builder.WriteString(text[pos:amp])

		if decoded, consumed, ok := decodeCharRefAt(text, amp, false); ok {
			builder.WriteString(decoded)

			pos = amp + consumed
		} else {
			builder.WriteByte('&')

			pos = amp + 1
		}
	}

	return builder.String()
}

// decodeCharRefAt decodes one character reference whose '&' sits at src[i].
// It returns the decoded text and the number of source bytes consumed. ok is
// false for an ambiguous ampersand and, in attribute context, for named
// references without a semicolon followed by '=' or an ASCII alphanumeric,
// which the spec flushes literally for historical reasons.
//
//nolint:cyclop // flat decision tree over the character reference states
func decodeCharRefAt(src string, offset int, inAttribute bool) (string, int, bool) {
	if offset+1 >= len(src) {
		return "", 0, false
	}

	if src[offset+1] == '#' {
		return decodeNumericRef(src, offset)
	}

	runStart := offset + 1
	end := runStart

	for end < len(src) && isASCIIAlnum(src[end]) {
		end++
	}

	if end == runStart {
		return "", 0, false
	}

	if end < len(src) && src[end] == ';' {
		candSemi := src[offset : end+1]
		semiDecoded := decodeNamedRef(candSemi)

		if semiDecoded == candSemi {
			return "", 0, false
		}

		if inAttribute {
			// When the semicolon candidate decoded to the plain candidate
			// plus the semicolon, the match was a semicolonless prefix and
			// the historical flush rule applies.
			candPlain := src[offset:end]
			if semiDecoded == decodeNamedRef(candPlain)+";" {
				return "", 0, false
			}
		}

		return semiDecoded, len(candSemi), true
	}

	candPlain := src[offset:end]
	plainDecoded := decodeNamedRef(candPlain)

	if plainDecoded == candPlain {
		return "", 0, false
	}

	if inAttribute {
		// A decoded value ending in an ASCII alphanumeric means the match
		// was a proper prefix of the run, so the next input character is
		// alphanumeric and the historical flush rule applies.
		if endsWithASCIIAlnum(plainDecoded) {
			return "", 0, false
		}

		if end < len(src) && src[end] == '=' {
			return "", 0, false
		}
	}

	return plainDecoded, len(candPlain), true
}

// decodeNamedRef decodes a named character reference candidate. The stdlib
// table matches the spec table except for two names added after its snapshot;
// those are filled in here.
func decodeNamedRef(ref string) string {
	switch ref {
	case "&nGt;":
		return "\u226B\u20D2"
	case "&nLt;":
		return "\u226A\u20D2"
	default:
		return stdhtml.UnescapeString(ref)
	}
}

// decodeNumericRef decodes a numeric character reference whose '&' sits at
// src[i]. It follows the numeric character reference end state: zero, values
// above U+10FFFF, and surrogates become U+FFFD, and the C1 range uses the
// Windows-1252 interpretation.
func decodeNumericRef(src string, offset int) (string, int, bool) {
	idx := offset + numericRefPrefixLen
	base := uint64(decimalBase)

	if idx < len(src) && (src[idx] == 'x' || src[idx] == 'X') {
		base = hexBase

		idx++
	}

	digitsStart := idx

	value, idx := scanNumericDigits(src, idx, base)

	if idx == digitsStart {
		return "", 0, false
	}

	consumed := idx - offset
	if idx < len(src) && src[idx] == ';' {
		consumed++
	}

	return numericRefValue(value), consumed, true
}

// scanNumericDigits consumes the full digit run in one pass. Saturation
// prevents overflow without changing references with many leading zeros.
// The caller advances past this run, so each input digit is visited once.
func scanNumericDigits(src string, idx int, base uint64) (uint64, int) {
	value := uint64(0)

	for idx < len(src) {
		digit, ok := digitValue(src[idx], base)
		if !ok {
			break
		}

		if value <= maxUnicodeCodePoint {
			value = value*base + digit
		}

		idx++
	}

	return value, idx
}

func digitValue(char byte, base uint64) (uint64, bool) {
	switch {
	case char >= '0' && char <= '9':
		return uint64(char - '0'), true
	case base == hexBase && char >= 'a' && char <= 'f':
		return uint64(char-'a') + hexLetterOffset, true
	case base == hexBase && char >= 'A' && char <= 'F':
		return uint64(char-'A') + hexLetterOffset, true
	default:
		return 0, false
	}
}

func numericRefValue(value uint64) string {
	if value == 0 || value > maxUnicodeCodePoint {
		return "\uFFFD"
	}

	codePoint := rune(value)
	if codePoint >= surrogateCodePointMin && codePoint <= surrogateCodePointMax {
		return "\uFFFD"
	}

	if mapped, ok := windows1252Rune(codePoint); ok {
		return string(mapped)
	}

	return string(codePoint)
}

// windows1252Refs holds the Windows-1252 interpretation of the C1 control
// code points U+0080..U+009F, in order, as required by the numeric character
// reference end state.
const windows1252Refs = "\u20AC\u0081\u201A\u0192\u201E\u2026\u2020\u2021\u02C6\u2030" +
	"\u0160\u2039\u0152\u008D\u017D\u008F\u0090\u2018\u2019\u201C\u201D\u2022" +
	"\u2013\u2014\u02DC\u2122\u0161\u203A\u0153\u009D\u017E\u0178"

func windows1252Rune(code rune) (rune, bool) {
	if code < windows1252Min || code > windows1252Max {
		return 0, false
	}

	index := 0

	for _, mapped := range windows1252Refs {
		if index == int(code-windows1252Min) {
			return mapped, true
		}

		index++
	}

	return 0, false
}

func isASCIIAlnum(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func endsWithASCIIAlnum(s string) bool {
	if s == "" {
		return false
	}

	return isASCIIAlnum(s[len(s)-1])
}
