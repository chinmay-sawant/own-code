// recovery conventions as the tokenizer in html.go
//
//nolint:all // doctype tokenizer states and quirks-mode classification; same
package html

import "strings"

// Doctype is the structured content of one DOCTYPE token: the name and the
// optional public and system identifiers, plus the force-quirks flag. The
// raw declaration text is kept separately on Node.Text for callers that
// print the source form.
type Doctype struct {
	Name        string
	PublicID    string
	HasPublicID bool
	SystemID    string
	HasSystemID bool
	ForceQuirks bool
}

// DocumentMode is the quirks mode derived from the doctype under the
// "initial" insertion mode rules. The zero value is NoQuirks.
type DocumentMode uint8

const (
	// NoQuirks is standards mode.
	NoQuirks DocumentMode = iota
	// LimitedQuirks is almost standards mode.
	LimitedQuirks
	// Quirks is quirks mode.
	Quirks
)

// String names the mode for tests and diagnostics.
func (m DocumentMode) String() string {
	switch m {
	case NoQuirks:
		return "no-quirks"
	case LimitedQuirks:
		return "limited-quirks"
	case Quirks:
		return "quirks"
	default:
		return "unknown"
	}
}

// The quirks-mode tables from the HTML standard's "initial" insertion mode,
// lowercased for ASCII case-insensitive comparison. A system identifier that
// is present but empty is not missing; the 4.01 entries below therefore
// distinguish "missing or empty" from "neither missing nor empty".
var (
	// quirksPublicExactIDs set quirks mode when the public identifier is
	// exactly one of these strings.
	quirksPublicExactIDs = []string{
		"-//w3o//dtd w3 html strict 3.0//en//",
		"-/w3c/dtd html 4.0 transitional/en",
		"html",
	}

	// quirksSystemExactID sets quirks mode when the system identifier is
	// exactly this string.
	quirksSystemExactID = "http://www.ibm.com/data/dtd/v11/ibmxhtml1-transitional.dtd"

	// quirksPublicPrefixes set quirks mode when the public identifier starts
	// with one of these strings.
	quirksPublicPrefixes = []string{
		"+//silmaril//dtd html pro v0r11 19970101//",
		"-//as//dtd html 3.0 aswedit + extensions//",
		"-//advasoft ltd//dtd html 3.0 aswedit + extensions//",
		"-//ietf//dtd html 2.0 level 1//",
		"-//ietf//dtd html 2.0 level 2//",
		"-//ietf//dtd html 2.0 strict level 1//",
		"-//ietf//dtd html 2.0 strict level 2//",
		"-//ietf//dtd html 2.0 strict//",
		"-//ietf//dtd html 2.0//",
		"-//ietf//dtd html 2.1e//",
		"-//ietf//dtd html 3.0//",
		"-//ietf//dtd html 3.2 final//",
		"-//ietf//dtd html 3.2//",
		"-//ietf//dtd html 3//",
		"-//ietf//dtd html level 0//",
		"-//ietf//dtd html level 1//",
		"-//ietf//dtd html level 2//",
		"-//ietf//dtd html level 3//",
		"-//ietf//dtd html strict level 0//",
		"-//ietf//dtd html strict level 1//",
		"-//ietf//dtd html strict level 2//",
		"-//ietf//dtd html strict level 3//",
		"-//ietf//dtd html strict//",
		"-//ietf//dtd html//",
		"-//metrius//dtd metrius presentational//",
		"-//microsoft//dtd internet explorer 2.0 html strict//",
		"-//microsoft//dtd internet explorer 2.0 html//",
		"-//microsoft//dtd internet explorer 2.0 tables//",
		"-//microsoft//dtd internet explorer 3.0 html strict//",
		"-//microsoft//dtd internet explorer 3.0 html//",
		"-//microsoft//dtd internet explorer 3.0 tables//",
		"-//netscape comm. corp.//dtd html//",
		"-//netscape comm. corp.//dtd strict html//",
		"-//o'reilly and associates//dtd html 2.0//",
		"-//o'reilly and associates//dtd html extended 1.0//",
		"-//o'reilly and associates//dtd html extended relaxed 1.0//",
		"-//sq//dtd html 2.0 hotmetal + extensions//",
		"-//softquad software//dtd hotmetal pro 6.0::19990601::extensions to html 4.0//",
		"-//softquad//dtd hotmetal pro 4.0::19971010::extensions to html 4.0//",
		"-//spyglass//dtd html 2.0 extended//",
		"-//sun microsystems corp.//dtd hotjava html//",
		"-//sun microsystems corp.//dtd hotjava strict html//",
		"-//w3c//dtd html 3 1995-03-24//",
		"-//w3c//dtd html 3.2 draft//",
		"-//w3c//dtd html 3.2 final//",
		"-//w3c//dtd html 3.2//",
		"-//w3c//dtd html 3.2s draft//",
		"-//w3c//dtd html 4.0 frameset//",
		"-//w3c//dtd html 4.0 transitional//",
		"-//w3c//dtd html experimental 19960712//",
		"-//w3c//dtd html experimental 970421//",
		"-//w3c//dtd w3 html//",
		"-//w3o//dtd w3 html 3.0//",
		"-//webtechs//dtd mozilla html 2.0//",
		"-//webtechs//dtd mozilla html//",
	}

	// html401PublicPrefixes are the HTML 4.01 identifiers that depend on the
	// system identifier: quirks when it is missing or empty, limited-quirks
	// when it is present and non-empty.
	html401PublicPrefixes = []string{
		"-//w3c//dtd html 4.01 frameset//",
		"-//w3c//dtd html 4.01 transitional//",
	}

	// limitedQuirksPublicPrefixes set limited-quirks mode on their own.
	limitedQuirksPublicPrefixes = []string{
		"-//w3c//dtd xhtml 1.0 frameset//",
		"-//w3c//dtd xhtml 1.0 transitional//",
	}
)

// classifyDocumentMode implements the DOCTYPE classification from the
// standard's "initial" insertion mode: no-quirks, limited-quirks, or quirks.
// A missing or non-"html" name and the force-quirks flag always mean quirks.
func classifyDocumentMode(doctype Doctype) DocumentMode {
	if doctype.ForceQuirks || !strings.EqualFold(doctype.Name, "html") {
		return Quirks
	}

	public := strings.ToLower(doctype.PublicID)
	system := strings.ToLower(doctype.SystemID)

	for _, exact := range quirksPublicExactIDs {
		if public == exact {
			return Quirks
		}
	}

	if doctype.HasSystemID && system == quirksSystemExactID {
		return Quirks
	}

	if hasAnyPrefix(public, quirksPublicPrefixes) {
		return Quirks
	}

	systemMissingOrEmpty := !doctype.HasSystemID || doctype.SystemID == ""
	if systemMissingOrEmpty && hasAnyPrefix(public, html401PublicPrefixes) {
		return Quirks
	}

	if hasAnyPrefix(public, limitedQuirksPublicPrefixes) {
		return LimitedQuirks
	}

	if !systemMissingOrEmpty && hasAnyPrefix(public, html401PublicPrefixes) {
		return LimitedQuirks
	}

	return NoQuirks
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}

	return false
}

// DOCTYPE tokenizer states, in the order the WHATWG tokenizer defines them.
type doctypeState uint8

const (
	doctypeStateDoctype doctypeState = iota
	doctypeStateBeforeName
	doctypeStateName
	doctypeStateAfterName
	doctypeStateAfterPublicKeyword
	doctypeStateBeforePublicID
	doctypeStatePublicIDDouble
	doctypeStatePublicIDSingle
	doctypeStateAfterPublicID
	doctypeStateBetweenIDs
	doctypeStateAfterSystemKeyword
	doctypeStateBeforeSystemID
	doctypeStateSystemIDDouble
	doctypeStateSystemIDSingle
	doctypeStateAfterSystemID
	doctypeStateBogus
)

// scanDoctype tokenizes a doctype starting at '<'. It implements the full
// WHATWG doctype state machine: name, PUBLIC/SYSTEM keywords, quoted
// identifiers, force-quirks, and bogus recovery. The raw declaration body is
// kept in token.data for Node.Text; the structured fields travel in
// token.doctype.
func scanDoctype(src string, pos int, emit tokenSink) int {
	var (
		nameBuf   strings.Builder
		publicBuf strings.Builder
		systemBuf strings.Builder
	)

	tok := token{kind: tokDoctype}
	state := doctypeStateDoctype
	i := pos + len("<!DOCTYPE")

	setQuirks := func() {
		tok.doctype.ForceQuirks = true
	}

	emitToken := func(end int) {
		tok.doctype.Name = nameBuf.String()
		tok.doctype.PublicID = publicBuf.String()
		tok.doctype.SystemID = systemBuf.String()
		tok.data = replaceNUL(src[pos+2 : end])
		emit(tok)
	}

	for i < len(src) {
		c := src[i]

		switch state {
		case doctypeStateDoctype:
			switch {
			case c == '>':
				setQuirks()
				emitToken(i)

				return i + 1
			case isWhitespace(c):
				state = doctypeStateBeforeName

				i++
			default:
				state = doctypeStateBeforeName // missing whitespace: reconsume
			}
		case doctypeStateBeforeName:
			switch {
			case isWhitespace(c):
				i++
			case isASCIIUpper(c):
				nameBuf.WriteByte(c | 0x20)
				state = doctypeStateName

				i++
			case c == 0:
				nameBuf.WriteString(nulReplacement)
				state = doctypeStateName

				i++
			case c == '>':
				setQuirks()
				emitToken(i)

				return i + 1
			default:
				nameBuf.WriteByte(c)
				state = doctypeStateName

				i++
			}
		case doctypeStateName:
			switch {
			case isWhitespace(c):
				state = doctypeStateAfterName

				i++
			case c == '>':
				emitToken(i)

				return i + 1
			case isASCIIUpper(c):
				nameBuf.WriteByte(c | 0x20)

				i++
			case c == 0:
				nameBuf.WriteString(nulReplacement)

				i++
			default:
				nameBuf.WriteByte(c)

				i++
			}
		case doctypeStateAfterName:
			switch {
			case isWhitespace(c):
				i++
			case c == '>':
				emitToken(i)

				return i + 1
			case hasPrefixFold(src[i:], "public"):
				state = doctypeStateAfterPublicKeyword

				i += len("public")
			case hasPrefixFold(src[i:], "system"):
				state = doctypeStateAfterSystemKeyword

				i += len("system")
			default:
				setQuirks()
				state = doctypeStateBogus
			}
		case doctypeStateAfterPublicKeyword:
			switch {
			case isWhitespace(c):
				state = doctypeStateBeforePublicID

				i++
			case c == '"':
				tok.doctype.HasPublicID = true
				state = doctypeStatePublicIDDouble

				i++
			case c == '\'':
				tok.doctype.HasPublicID = true
				state = doctypeStatePublicIDSingle

				i++
			case c == '>':
				setQuirks()
				emitToken(i)

				return i + 1
			default:
				setQuirks()
				state = doctypeStateBogus
			}
		case doctypeStateBeforePublicID:
			switch {
			case isWhitespace(c):
				i++
			case c == '"':
				tok.doctype.HasPublicID = true
				state = doctypeStatePublicIDDouble

				i++
			case c == '\'':
				tok.doctype.HasPublicID = true
				state = doctypeStatePublicIDSingle

				i++
			case c == '>':
				setQuirks()
				emitToken(i)

				return i + 1
			default:
				setQuirks()
				state = doctypeStateBogus
			}
		case doctypeStatePublicIDDouble, doctypeStatePublicIDSingle:
			quote := byte('"')
			if state == doctypeStatePublicIDSingle {
				quote = '\''
			}

			switch {
			case c == quote:
				state = doctypeStateAfterPublicID

				i++
			case c == 0:
				publicBuf.WriteString(nulReplacement)

				i++
			case c == '>':
				setQuirks()
				emitToken(i)

				return i + 1
			default:
				publicBuf.WriteByte(c)

				i++
			}
		case doctypeStateAfterPublicID:
			switch {
			case isWhitespace(c):
				state = doctypeStateBetweenIDs

				i++
			case c == '>':
				emitToken(i)

				return i + 1
			case c == '"':
				tok.doctype.HasSystemID = true
				state = doctypeStateSystemIDDouble

				i++
			case c == '\'':
				tok.doctype.HasSystemID = true
				state = doctypeStateSystemIDSingle

				i++
			default:
				setQuirks()
				state = doctypeStateBogus
			}
		case doctypeStateBetweenIDs:
			switch {
			case isWhitespace(c):
				i++
			case c == '>':
				emitToken(i)

				return i + 1
			case c == '"':
				tok.doctype.HasSystemID = true
				state = doctypeStateSystemIDDouble

				i++
			case c == '\'':
				tok.doctype.HasSystemID = true
				state = doctypeStateSystemIDSingle

				i++
			default:
				setQuirks()
				state = doctypeStateBogus
			}
		case doctypeStateAfterSystemKeyword:
			switch {
			case isWhitespace(c):
				state = doctypeStateBeforeSystemID

				i++
			case c == '"':
				tok.doctype.HasSystemID = true
				state = doctypeStateSystemIDDouble

				i++
			case c == '\'':
				tok.doctype.HasSystemID = true
				state = doctypeStateSystemIDSingle

				i++
			case c == '>':
				setQuirks()
				emitToken(i)

				return i + 1
			default:
				setQuirks()
				state = doctypeStateBogus
			}
		case doctypeStateBeforeSystemID:
			switch {
			case isWhitespace(c):
				i++
			case c == '"':
				tok.doctype.HasSystemID = true
				state = doctypeStateSystemIDDouble

				i++
			case c == '\'':
				tok.doctype.HasSystemID = true
				state = doctypeStateSystemIDSingle

				i++
			case c == '>':
				setQuirks()
				emitToken(i)

				return i + 1
			default:
				setQuirks()
				state = doctypeStateBogus
			}
		case doctypeStateSystemIDDouble, doctypeStateSystemIDSingle:
			quote := byte('"')
			if state == doctypeStateSystemIDSingle {
				quote = '\''
			}

			switch {
			case c == quote:
				state = doctypeStateAfterSystemID

				i++
			case c == 0:
				systemBuf.WriteString(nulReplacement)

				i++
			case c == '>':
				setQuirks()
				emitToken(i)

				return i + 1
			default:
				systemBuf.WriteByte(c)

				i++
			}
		case doctypeStateAfterSystemID:
			switch {
			case isWhitespace(c):
				i++
			case c == '>':
				emitToken(i)

				return i + 1
			default:
				state = doctypeStateBogus // unexpected character, no force-quirks
			}
		case doctypeStateBogus:
			switch {
			case c == '>':
				emitToken(i)

				return i + 1
			case c == 0:
				i++
			default:
				i++
			}
		}
	}

	// EOF in any state but bogus forces quirks; bogus keeps whatever flag it
	// already carries.
	if state != doctypeStateBogus {
		setQuirks()
	}

	emitToken(len(src))

	return len(src)
}

// hasPrefixFold reports whether s starts with prefix under ASCII
// case-insensitive comparison.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// isASCIIUpper reports whether b is an ASCII uppercase letter.
func isASCIIUpper(b byte) bool {
	return b >= 'A' && b <= 'Z'
}
