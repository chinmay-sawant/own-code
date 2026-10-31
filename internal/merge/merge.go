// Package merge parses and resolves Git conflict markers in text without
// touching the filesystem.
package merge

import (
	"fmt"
	"strings"
)

// Choice selects the text that replaces one conflict block.
type Choice int

const (
	// Keep leaves the conflict markers and both sides unchanged.
	Keep Choice = iota
	// Ours replaces the block with the lines from the current branch.
	Ours
	// Theirs replaces the block with the lines from the incoming branch.
	Theirs
	// Both replaces the block with the ours lines followed by the theirs lines.
	Both
	// Base replaces the block with the common ancestor lines. A two-way
	// conflict has no base, so Base removes the block.
	Base
)

const markerMinLen = 7

// Conflict is one recognized conflict block. Line numbers are 1-based and
// point at the start and closing marker lines.
type Conflict struct {
	Index       int
	OursLabel   string
	TheirsLabel string
	BaseLabel   string
	Ours        []string
	Base        []string
	Theirs      []string
	StartLine   int
	EndLine     int
}

// Document is parsed text and the conflicts found in it. Text is the exact
// input, including all line endings.
type Document struct {
	Text      string
	Conflicts []Conflict
	// CRLF reports whether Text contains at least one CRLF line ending.
	CRLF bool

	spans []conflictSpan
}

// Parse finds all complete conflict blocks in text.
func Parse(text string) Document {
	doc := Document{
		Text: text,
		CRLF: strings.Contains(text, "\r\n"),
	}
	lines := splitSourceLines(text)

	for i := 0; i < len(lines); i++ {
		m, ok := parseMarker(lines[i].text)
		if !ok || m.kind != markerOurs {
			continue
		}

		conflict, span, next, ok := parseConflict(lines, i, m)
		if !ok {
			continue
		}

		conflict.Index = len(doc.Conflicts)
		doc.Conflicts = append(doc.Conflicts, conflict)
		doc.spans = append(doc.spans, span)
		i = next - 1
	}

	return doc
}

// Count returns the number of conflicts in the document.
func (d Document) Count() int {
	return len(d.Conflicts)
}

// HasMarkers reports whether text contains at least one valid conflict marker
// line, even when the surrounding block is incomplete.
func HasMarkers(text string) bool {
	for _, line := range splitSourceLines(text) {
		if _, ok := parseMarker(line.text); ok {
			return true
		}
	}

	return false
}

// Resolve replaces the conflict at index with the text selected by choice and
// returns the new document text. Index is 0-based. On error the original text
// is returned unchanged.
func (d Document) Resolve(index int, choice Choice) (string, error) {
	if index < 0 || index >= len(d.Conflicts) || index >= len(d.spans) {
		return d.Text, fmt.Errorf("merge: conflict index %d out of range for %d conflicts", index, len(d.Conflicts))
	}

	if choice == Keep {
		return d.Text, nil
	}

	span := d.spans[index]
	lines, err := span.choose(choice)
	if err != nil {
		return d.Text, err
	}

	atEOF := span.end == len(d.Text) && span.closeEOL == ""
	return d.Text[:span.start] + renderLines(lines, atEOF) + d.Text[span.end:], nil
}

// ResolveConflict parses text and resolves the conflict at index.
func ResolveConflict(text string, index int, choice Choice) (string, error) {
	return Parse(text).Resolve(index, choice)
}

type markerKind uint8

const (
	markerNone markerKind = iota
	markerOurs
	markerBase
	markerSeparator
	markerTheirs
)

type marker struct {
	kind  markerKind
	label string
}

// sourceLine is one line with its own line ending, so mixed LF and CRLF text
// survives a resolve byte for byte.
type sourceLine struct {
	text  string
	eol   string
	start int
	end   int
}

type conflictSpan struct {
	start    int
	end      int
	closeEOL string
	ours     []sourceLine
	base     []sourceLine
	theirs   []sourceLine
}

const (
	inOurs = iota
	inBase
	inTheirs
)

func parseConflict(lines []sourceLine, start int, startMarker marker) (Conflict, conflictSpan, int, bool) {
	conflict := Conflict{
		OursLabel: startMarker.label,
		StartLine: start + 1,
	}
	var span conflictSpan
	state := inOurs

	for j := start + 1; j < len(lines); j++ {
		m, isMarker := parseMarker(lines[j].text)
		if isMarker {
			switch {
			case state == inOurs && m.kind == markerBase:
				conflict.BaseLabel = m.label
				state = inBase
				continue
			case state != inTheirs && m.kind == markerSeparator:
				state = inTheirs
				continue
			case state == inTheirs && m.kind == markerTheirs:
				span.start = lines[start].start
				span.end = lines[j].end
				span.closeEOL = lines[j].eol
				conflict.EndLine = j + 1
				conflict.TheirsLabel = m.label
				conflict.Ours = lineTexts(span.ours)
				conflict.Base = lineTexts(span.base)
				conflict.Theirs = lineTexts(span.theirs)

				return conflict, span, j + 1, true
			}
		}

		switch state {
		case inOurs:
			span.ours = append(span.ours, lines[j])
		case inBase:
			span.base = append(span.base, lines[j])
		case inTheirs:
			span.theirs = append(span.theirs, lines[j])
		}
	}

	return Conflict{}, conflictSpan{}, 0, false
}

func (s conflictSpan) choose(choice Choice) ([]sourceLine, error) {
	switch choice {
	case Ours:
		return s.ours, nil
	case Theirs:
		return s.theirs, nil
	case Base:
		return s.base, nil
	case Both:
		both := make([]sourceLine, 0, len(s.ours)+len(s.theirs))
		both = append(both, s.ours...)
		both = append(both, s.theirs...)

		return both, nil
	default:
		return nil, fmt.Errorf("merge: unknown choice %d", choice)
	}
}

func renderLines(lines []sourceLine, atEOF bool) string {
	var b strings.Builder

	for i, line := range lines {
		b.WriteString(line.text)
		if atEOF && i == len(lines)-1 {
			break
		}
		b.WriteString(line.eol)
	}

	return b.String()
}

func lineTexts(lines []sourceLine) []string {
	if len(lines) == 0 {
		return nil
	}

	texts := make([]string, len(lines))
	for i := range lines {
		texts[i] = lines[i].text
	}

	return texts
}

func parseMarker(line string) (marker, bool) {
	if len(line) < markerMinLen {
		return marker{}, false
	}

	ch := line[0]

	var kind markerKind

	switch ch {
	case '<':
		kind = markerOurs
	case '>':
		kind = markerTheirs
	case '|':
		kind = markerBase
	case '=':
		kind = markerSeparator
	default:
		return marker{}, false
	}

	n := 1
	for n < len(line) && line[n] == ch {
		n++
	}
	if n < markerMinLen {
		return marker{}, false
	}

	rest := line[n:]
	if rest != "" {
		if rest[0] != ' ' && rest[0] != '\t' {
			return marker{}, false
		}
		rest = strings.TrimSpace(rest)
	}

	return marker{kind: kind, label: rest}, true
}

func splitSourceLines(text string) []sourceLine {
	var lines []sourceLine
	start := 0

	for i := 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}

		contentEnd, eolStart := i, i
		if i > start && text[i-1] == '\r' {
			contentEnd, eolStart = i-1, i-1
		}

		lines = append(lines, sourceLine{
			text:  text[start:contentEnd],
			eol:   text[eolStart : i+1],
			start: start,
			end:   i + 1,
		})
		start = i + 1
	}

	if start < len(text) {
		lines = append(lines, sourceLine{
			text:  text[start:],
			start: start,
			end:   len(text),
		})
	}

	return lines
}
