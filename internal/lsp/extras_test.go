package lsp

import (
	"encoding/json"
	"testing"
)

func TestParsePrepareRename(t *testing.T) {
	if _, ok := ParsePrepareRename(json.RawMessage(`null`)); ok {
		t.Fatal("null must refuse")
	}

	r, ok := ParsePrepareRename(json.RawMessage(`{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}`))
	if !ok || r.Range == nil || r.Range.End.Character != 5 {
		t.Fatalf("bare range = %+v ok=%v", r, ok)
	}

	r, ok = ParsePrepareRename(json.RawMessage(`{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":3}},"placeholder":"foo"}`))
	if !ok || r.Placeholder != "foo" {
		t.Fatalf("placeholder = %+v", r)
	}

	if _, ok = ParsePrepareRename(json.RawMessage(`{"defaultBehavior":true}`)); !ok {
		t.Fatal("defaultBehavior must be accepted")
	}
}

func TestParseSignatureHelp(t *testing.T) {
	if ParseSignatureHelp(json.RawMessage(`null`)) != nil {
		t.Fatal("null")
	}

	h := ParseSignatureHelp(json.RawMessage(`{"signatures":[{"label":"f(a int, b string)","documentation":{"kind":"markdown","value":"doc"},"parameters":[{"label":"a int"},{"label":[9,17]}]}],"activeParameter":1}`))
	if h == nil || len(h.Signatures) != 1 || h.ActiveParameter != 1 || h.Signatures[0].Documentation != "doc" {
		t.Fatalf("help = %+v", h)
	}

	p := h.Signatures[0].Parameters
	if p[0].Start != 2 || p[0].End != 7 || p[1].Label != "b string" || p[1].Start != 9 {
		t.Fatalf("params = %+v", p)
	}
}

func TestParseCodeActions(t *testing.T) {
	raw := `[{"title":"Fix import","kind":"quickfix","isPreferred":true,"edit":{"changes":{"file:///a.go":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"x"}]}}},
	{"title":"Run","command":"do.it","arguments":[1]},
	{"title":"Nested","command":{"title":"Nested","command":"c2"}},
	{"title":"Off","disabled":{"reason":"no"}},{"kind":"x"}]`

	got := ParseCodeActions(json.RawMessage(raw))
	if len(got) != 4 {
		t.Fatalf("got %d: %+v", len(got), got)
	}

	if got[0].Edit == nil || !got[0].IsPreferred || got[1].Command == nil || got[1].Command.Command != "do.it" ||
		got[2].Command == nil || got[2].Command.Command != "c2" || !got[3].Disabled {
		t.Fatalf("actions = %+v", got)
	}
}
