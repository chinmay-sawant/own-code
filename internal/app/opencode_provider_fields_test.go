package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/opencode"
)

func TestParseOpenCodeProviderKeyFields(t *testing.T) {
	method := openCodeProviderMethod(`[
		{"key":"region","type":"string","title":"Region","required":true,"options":[{"value":"us","label":"US"}]},
		{"key":"accountId","type":"string","title":"Account ID"},
		{"key":"accessToken","type":"string","title":"Access token","required":true}
	]`)
	fields, err := ParseOpenCodeProviderKeyFields(method)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 {
		t.Fatalf("field count = %d, want 3", len(fields))
	}
	if !fields[2].Sensitive {
		t.Fatal("access token field was not marked sensitive")
	}
	if fields[1].Sensitive {
		t.Fatal("account ID field was marked sensitive")
	}
}

func TestBuildOpenCodeProviderAnswerTypesAndRedaction(t *testing.T) {
	method := openCodeProviderMethod(`[
		{"key":"region","type":"string","required":true,"options":[{"value":"us","label":"US"}]},
		{"key":"accountId","type":"string"},
		{"key":"timeout","type":"integer","minimum":1,"maximum":10},
		{"key":"allowPreview","type":"boolean"},
		{"key":"models","type":"multiselect","options":[{"value":"fast","label":"Fast"},{"value":"safe","label":"Safe"}],"minItems":1}
	]`)
	answer, err := BuildOpenCodeProviderAnswer(method, map[string]string{
		"region":       "us",
		"accountId":    "acct-42",
		"timeout":      "5",
		"allowPreview": "false",
		"models":       `["fast","safe"]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got["region"] != "us" || got["accountId"] != "acct-42" || got["timeout"] != float64(5) || got["allowPreview"] != false {
		t.Fatalf("unexpected typed values: %#v", got)
	}
	if fmt.Sprint(got["models"]) != "[fast safe]" {
		t.Fatalf("multiselect = %#v", got["models"])
	}
	for _, format := range []string{fmt.Sprint(answer), fmt.Sprintf("%#v", answer)} {
		if strings.Contains(format, "acct-42") {
			t.Fatalf("formatted answer leaked a value: %q", format)
		}
	}
}

func TestBuildOpenCodeProviderAnswerValidatesBoundsAndUnknownFields(t *testing.T) {
	method := openCodeProviderMethod(`[
		{"key":"accountId","type":"string","required":true,"minLength":2,"maxLength":8},
		{"key":"timeout","type":"number","minimum":0.1,"maximum":9.5}
	]`)
	for name, values := range map[string]map[string]string{
		"required":         {"timeout": "1"},
		"string too short": {"accountId": "x", "timeout": "1"},
		"string too long":  {"accountId": "123456789", "timeout": "1"},
		"number too small": {"accountId": "ab", "timeout": "0"},
		"unknown field":    {"accountId": "ab", "timeout": "1", "extra": "no"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildOpenCodeProviderAnswer(method, values); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestBuildOpenCodeProviderAnswerAppliesConditionalFields(t *testing.T) {
	method := openCodeProviderMethod(`[
		{"key":"cloud","type":"boolean","required":true},
		{"key":"endpoint","type":"string","required":true,"when":[{"key":"cloud","op":"eq","value":true}]}
	]`)
	answer, err := BuildOpenCodeProviderAnswer(method, map[string]string{"cloud": "false"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "endpoint") {
		t.Fatalf("inactive conditional field was sent: %s", encoded)
	}
	if _, err := BuildOpenCodeProviderAnswer(method, map[string]string{"cloud": "true"}); err == nil {
		t.Fatal("expected the active required field to be enforced")
	}
}

func TestParseOpenCodeProviderKeyFieldsRejectsUnsupportedOrUnboundedSchemas(t *testing.T) {
	for name, method := range map[string]opencode.IntegrationMethod{
		"wrong method":      {Type: "oauth"},
		"malformed":         {Type: "key", Form: json.RawMessage(`[{"key":"token","type":"string"}`)},
		"unsupported field": openCodeProviderMethod(`[{"key":"source","type":"external"}]`),
		"reserved key":      openCodeProviderMethod(`[{"key":"key","type":"string"}]`),
		"duplicate key":     openCodeProviderMethod(`[{"key":"tenant","type":"string"},{"key":"tenant","type":"string"}]`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseOpenCodeProviderKeyFields(method); err == nil {
				t.Fatal("expected schema error")
			}
		})
	}

	tooLarge := openCodeProviderMethod(`[ {"key":"x","type":"string"} ]`)
	tooLarge.Form = json.RawMessage("[" + strings.Repeat(" ", maxOpenCodeProviderFormBytes) + "]")
	if _, err := ParseOpenCodeProviderKeyFields(tooLarge); err == nil {
		t.Fatal("expected oversized form error")
	}
}

func TestBuildOpenCodeProviderAnswerUsesDefaultsAndOmitsOptionalBlank(t *testing.T) {
	method := openCodeProviderMethod(`[
		{"key":"region","type":"string","required":true,"default":"us-east"},
		{"key":"note","type":"string"}
	]`)
	answer, err := BuildOpenCodeProviderAnswer(method, map[string]string{"note": "   "})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"region":"us-east"}`; got != want {
		t.Fatalf("answer = %s, want %s", got, want)
	}
}

func openCodeProviderMethod(form string) opencode.IntegrationMethod {
	return opencode.IntegrationMethod{Type: "key", Form: json.RawMessage(form)}
}
