package lsp

import (
	"context"
	"encoding/json"
	"strings"
)

// HoverParams is textDocument/hover.
type HoverParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// Hover is the textDocument/hover result. Contents is one of a string, a
// MarkedString ({language, value}), an array of those, or a MarkupContent
// ({kind, value}); Text flattens any of them to Markdown.
type Hover struct {
	Contents json.RawMessage `json:"contents"`
	Range    *Range          `json:"range,omitempty"`
}

// Text flattens the hover contents to Markdown. It returns "" for an empty or
// malformed result.
func (h *Hover) Text() string {
	if h == nil || len(h.Contents) == 0 {
		return ""
	}

	return strings.TrimSpace(flattenHover(h.Contents))
}

func flattenHover(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	switch raw[0] {
	case '"':
		var s string
		_ = json.Unmarshal(raw, &s)

		return s
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return ""
		}

		var parts []string

		for _, it := range items {
			if t := strings.TrimSpace(flattenHover(it)); t != "" {
				parts = append(parts, t)
			}
		}

		return strings.Join(parts, "\n\n")
	case '{':
		var o struct {
			Kind     string `json:"kind"`
			Language string `json:"language"`
			Value    string `json:"value"`
		}
		if json.Unmarshal(raw, &o) != nil {
			return ""
		}

		// A MarkedString with a language is a code block.
		if o.Language != "" {
			return "```" + o.Language + "\n" + o.Value + "\n```"
		}

		return o.Value
	}

	return ""
}

// Hover requests hover information at pos and returns it as Markdown. An
// empty string means the server had nothing to show.
func (c *Client) Hover(ctx context.Context, uri string, pos Position) (string, error) {
	var h *Hover

	err := c.Call(ctx, "textDocument/hover", HoverParams{TextDocument: TextDocumentIdentifier{URI: uri}, Position: pos}, &h)
	if err != nil {
		return "", err
	}

	return h.Text(), nil
}
