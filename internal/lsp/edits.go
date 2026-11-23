package lsp

import (
	"errors"
	"sort"
)

// ApplyEdits applies LSP text edits (UTF-16 positions against the original
// text) and returns the new text. Overlapping edits are rejected.
func ApplyEdits(text string, edits []TextEdit) (string, error) {
	type span struct {
		start, end int
		text       string
		order      int
	}

	spans := make([]span, 0, len(edits))

	for i, e := range edits {
		s, en := OffsetAtPosition(text, e.Range.Start), OffsetAtPosition(text, e.Range.End)
		if en < s {
			return "", errors.New("lsp: edit range ends before it starts")
		}

		spans = append(spans, span{s, en, e.NewText, i})
	}

	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}

		return spans[i].order < spans[j].order
	})

	out := make([]byte, 0, len(text))
	last := 0

	for _, sp := range spans {
		if sp.start < last {
			return "", errors.New("lsp: overlapping edits")
		}

		out = append(out, text[last:sp.start]...)
		out = append(out, sp.text...)
		last = sp.end
	}

	out = append(out, text[last:]...)

	return string(out), nil
}
