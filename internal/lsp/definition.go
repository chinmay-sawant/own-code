package lsp

import (
	"context"
	"encoding/json"
	"strings"
)

// TextDocumentPositionParams is the payload of position-based requests such as
// textDocument/definition, textDocument/declaration, and textDocument/typeDefinition.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// LocationLink represents a link between a source and a target location.
type LocationLink struct {
	OriginSelectionRange *Range `json:"originSelectionRange,omitempty"`
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

// ParseLocations normalizes a textDocument/definition (or declaration /
// typeDefinition) JSON result — which per LSP 3.17 may be null, a single
// Location, []Location, or []LocationLink — into a flat []Location slice.
func ParseLocations(raw json.RawMessage) []Location {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	switch raw[0] {
	case '{':
		if loc, ok := parseOneLocation(raw); ok {
			return []Location{loc}
		}
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return nil
		}

		out := make([]Location, 0, len(items))
		for _, it := range items {
			if loc, ok := parseOneLocation(it); ok {
				out = append(out, loc)
			}
		}

		return out
	}

	return nil
}

func parseOneLocation(raw json.RawMessage) (Location, bool) {
	var probe struct {
		URI                  string `json:"uri"`
		Range                Range  `json:"range"`
		TargetURI            string `json:"targetUri"`
		TargetRange          Range  `json:"targetRange"`
		TargetSelectionRange *Range `json:"targetSelectionRange"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return Location{}, false
	}

	if probe.URI != "" {
		return Location{URI: probe.URI, Range: probe.Range}, true
	}

	if probe.TargetURI != "" {
		r := probe.TargetRange
		if probe.TargetSelectionRange != nil {
			r = *probe.TargetSelectionRange
		}

		return Location{URI: probe.TargetURI, Range: r}, true
	}

	return Location{}, false
}

func (c *Client) gotoLocations(ctx context.Context, method, uri string, pos Position) ([]Location, error) {
	var raw json.RawMessage

	err := c.Call(ctx, method, TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     pos,
	}, &raw)
	if err != nil {
		return nil, err
	}

	return ParseLocations(raw), nil
}

// Definition requests textDocument/definition at pos.
func (c *Client) Definition(ctx context.Context, uri string, pos Position) ([]Location, error) {
	return c.gotoLocations(ctx, "textDocument/definition", uri, pos)
}

// Declaration requests textDocument/declaration at pos.
func (c *Client) Declaration(ctx context.Context, uri string, pos Position) ([]Location, error) {
	return c.gotoLocations(ctx, "textDocument/declaration", uri, pos)
}

// TypeDefinition requests textDocument/typeDefinition at pos.
func (c *Client) TypeDefinition(ctx context.Context, uri string, pos Position) ([]Location, error) {
	return c.gotoLocations(ctx, "textDocument/typeDefinition", uri, pos)
}
