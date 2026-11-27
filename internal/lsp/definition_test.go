package lsp

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestParseLocations(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []Location
	}{
		{"null", "null", nil},
		{"empty", "", nil},
		{"empty array", "[]", []Location{}},
		{
			"single location",
			`{"uri":"file:///a.go","range":{"start":{"line":3,"character":5},"end":{"line":3,"character":8}}}`,
			[]Location{{URI: "file:///a.go", Range: Range{Start: Position{3, 5}, End: Position{3, 8}}}},
		},
		{
			"location array",
			`[{"uri":"file:///a.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":4}}},{"uri":"file:///b.go","range":{"start":{"line":9,"character":0},"end":{"line":9,"character":3}}}]`,
			[]Location{
				{URI: "file:///a.go", Range: Range{Start: Position{1, 2}, End: Position{1, 4}}},
				{URI: "file:///b.go", Range: Range{Start: Position{9, 0}, End: Position{9, 3}}},
			},
		},
		{
			"location link prefers selection range",
			`[{"targetUri":"file:///c.go","targetRange":{"start":{"line":10,"character":0},"end":{"line":15,"character":1}},"targetSelectionRange":{"start":{"line":10,"character":5},"end":{"line":10,"character":12}}}]`,
			[]Location{{URI: "file:///c.go", Range: Range{Start: Position{10, 5}, End: Position{10, 12}}}},
		},
		{
			"location link falls back to target range",
			`[{"targetUri":"file:///d.go","targetRange":{"start":{"line":4,"character":2},"end":{"line":4,"character":9}}}]`,
			[]Location{{URI: "file:///d.go", Range: Range{Start: Position{4, 2}, End: Position{4, 9}}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseLocations(json.RawMessage(tc.raw))
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (%+v)", len(got), len(tc.want), got)
			}

			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestClientDefinitionFamily(t *testing.T) {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	srv := NewConn(sr, sw)

	go func() {
		defer sw.Close()

		for {
			m, err := srv.Read()
			if err != nil {
				return
			}

			var res string
			switch m.Method {
			case "textDocument/definition":
				res = `{"uri":"file:///def.go","range":{"start":{"line":2,"character":4},"end":{"line":2,"character":7}}}`
			case "textDocument/declaration":
				res = `[{"uri":"file:///decl.go","range":{"start":{"line":5,"character":1},"end":{"line":5,"character":6}}}]`
			case "textDocument/typeDefinition":
				res = `[{"targetUri":"file:///type.go","targetRange":{"start":{"line":8,"character":0},"end":{"line":10,"character":1}},"targetSelectionRange":{"start":{"line":8,"character":5},"end":{"line":8,"character":9}}}]`
			default:
				res = "null"
			}

			_ = srv.Write(&Message{ID: m.ID, Result: json.RawMessage(res)})
		}
	}()

	c := NewClient(cr, cw, Options{})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pos := Position{Line: 1, Character: 3}

	locs, err := c.Definition(ctx, "file:///main.go", pos)
	if err != nil || len(locs) != 1 || locs[0].URI != "file:///def.go" || locs[0].Range.Start != (Position{2, 4}) {
		t.Fatalf("Definition = %+v, %v", locs, err)
	}

	locs, err = c.Declaration(ctx, "file:///main.go", pos)
	if err != nil || len(locs) != 1 || locs[0].URI != "file:///decl.go" || locs[0].Range.Start != (Position{5, 1}) {
		t.Fatalf("Declaration = %+v, %v", locs, err)
	}

	locs, err = c.TypeDefinition(ctx, "file:///main.go", pos)
	if err != nil || len(locs) != 1 || locs[0].URI != "file:///type.go" || locs[0].Range.Start != (Position{8, 5}) {
		t.Fatalf("TypeDefinition = %+v, %v", locs, err)
	}
}
