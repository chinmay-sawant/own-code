package lsp

import (
	"encoding/json"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

func TestParseSyncKind(t *testing.T) {
	cases := map[string]int{
		`{}`:                                      SyncFull,
		`{"textDocumentSync":2}`:                  SyncIncremental,
		`{"textDocumentSync":1}`:                  SyncFull,
		`{"textDocumentSync":{"change":2}}`:       SyncIncremental,
		`{"textDocumentSync":{"openClose":true}}`: SyncFull,
		`{"textDocumentSync":0}`:                  SyncNone,
		`garbage`:                                 SyncFull,
	}
	for in, want := range cases {
		if got := ParseSyncKind(json.RawMessage(in)); got != want {
			t.Errorf("%s: got %d want %d", in, got, want)
		}
	}
}

// applyChange applies an incremental change to text the way a server would.
func applyChange(text string, c ContentChange) string {
	lines := SplitLines(text)
	// Rebuild offsets with LF joins is wrong for CR; walk runes instead.
	toOff := func(p Position) int {
		off := 0
		rs := []rune(text)
		line := 0
		for off < len(rs) && line < p.Line {
			if rs[off] == '\r' && off+1 < len(rs) && rs[off+1] == '\n' {
				off++
			}
			if rs[off] == '\n' || rs[off] == '\r' {
				line++
			}
			off++
		}
		u := 0
		for off < len(rs) && u < p.Character {
			u += utf16Width(rs[off])
			off++
		}
		return off
	}
	_ = lines
	s, e := toOff(c.Range.Start), toOff(c.Range.End)
	rs := []rune(text)
	return string(rs[:s]) + c.Text + string(rs[e:])
}

func TestChangeForRoundTrip(t *testing.T) {
	cases := [][2]string{
		{"hello\nworld\n", "hello\nbrave world\n"},
		{"a😀b\nc", "a😀X\nc"},
		{"a\r\nb\r\nc", "a\r\nb2\r\nc"},
		{"abc", ""},
		{"", "abc"},
		{"x\ny\nz", "x\nz"},
		{"é😀\n😀é", "é😀\n😀Zé"},
	}
	for _, c := range cases {
		e, ok := edit.Diff(c[0], c[1])
		if !ok {
			t.Fatalf("no diff for %q", c)
		}
		if got := applyChange(c[0], ChangeFor(c[0], e)); got != c[1] {
			t.Errorf("%q -> %q: got %q", c[0], c[1], got)
		}
	}
}

func TestOffsetPositionUTF16(t *testing.T) {
	if p := OffsetPosition("a😀b", 2); p.Line != 0 || p.Character != 3 {
		t.Fatalf("got %+v", p)
	}
	if p := OffsetPosition("ab\ncd", 4); p.Line != 1 || p.Character != 1 {
		t.Fatalf("got %+v", p)
	}
	if p := OffsetPosition("a\r\nb", 3); p.Line != 1 || p.Character != 0 {
		t.Fatalf("got %+v", p)
	}
}
