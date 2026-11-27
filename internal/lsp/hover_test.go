package lsp

import (
	"encoding/json"
	"testing"
)

func TestHoverText(t *testing.T) {
	cases := map[string]string{
		`{"contents":"plain"}`:                               "plain",
		`{"contents":{"kind":"markdown","value":"**x**"}}`:   "**x**",
		`{"contents":{"language":"go","value":"func F()"}}`:  "```go\nfunc F()\n```",
		`{"contents":["a",{"language":"go","value":"b"}]}`:   "a\n\n```go\nb\n```",
		`{"contents":[]}`:                                    "",
		`{"contents":""}`:                                    "",
		`{"contents":null}`:                                  "",
		`{}`:                                                 "",
		`{"contents":{"kind":"plaintext","value":"  hi  "}}`: "hi",
	}

	for in, want := range cases {
		var h Hover
		if err := json.Unmarshal([]byte(in), &h); err != nil {
			t.Fatal(err)
		}

		if got := h.Text(); got != want {
			t.Errorf("%s -> %q, want %q", in, got, want)
		}
	}

	var nilHover *Hover
	if nilHover.Text() != "" {
		t.Fatal("nil hover has text")
	}
}
