package lsp

import (
	"encoding/json"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// Text document sync kinds from the server capabilities.
const (
	SyncNone        = 0
	SyncFull        = 1
	SyncIncremental = 2
)

// SyncKind reads textDocumentSync (a number, or an object with "change")
// from the server capabilities. Unknown or absent returns SyncFull, the
// safe default every server accepts.
func (c *Client) SyncKind() int {
	return ParseSyncKind(c.Result.Capabilities)
}

// ParseSyncKind is SyncKind over raw capabilities JSON.
func ParseSyncKind(caps json.RawMessage) int {
	var v struct {
		Sync json.RawMessage `json:"textDocumentSync"`
	}
	if json.Unmarshal(caps, &v) != nil || len(v.Sync) == 0 {
		return SyncFull
	}

	var n int
	if json.Unmarshal(v.Sync, &n) == nil {
		return n
	}

	var o struct {
		Change *int `json:"change"`
	}
	if json.Unmarshal(v.Sync, &o) == nil && o.Change != nil {
		return *o.Change
	}

	return SyncFull
}

// OffsetPosition converts a rune offset in text to an LSP position: the
// zero-based line (\n, \r\n and lone \r all break lines) and the UTF-16
// column within it. Offsets past the end clamp to the end.
func OffsetPosition(text string, off int) Position {
	line, col, i := 0, 0, 0
	rs := []rune(text)

	for i < len(rs) && i < off {
		switch r := rs[i]; r {
		case '\n':
			line++

			col = 0
		case '\r':
			if i+1 < len(rs) && rs[i+1] == '\n' && i+1 < off {
				i++
			} else if i+1 < len(rs) && rs[i+1] == '\n' {
				// Offset lands between \r and \n: stay at end of the line.
				col++

				i++

				continue
			}

			line++

			col = 0
		default:
			col += utf16Width(r)
		}

		i++
	}

	return Position{Line: line, Character: col}
}

// ChangeFor builds the incremental content change for e applied to old. The
// range covers e's replaced span in old's coordinates.
func ChangeFor(old string, e edit.Edit) ContentChange {
	return ContentChange{
		Range: &Range{Start: OffsetPosition(old, e.Start), End: OffsetPosition(old, e.End)},
		Text:  e.Text,
	}
}
