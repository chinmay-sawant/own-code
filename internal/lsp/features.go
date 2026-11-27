package lsp

import (
	"context"
	"encoding/json"
	"strings"
)

// TextEdit replaces Range with NewText.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// Completion trigger kinds.
const (
	CompletionInvoked          = 1
	CompletionTriggerCharacter = 2
)

// Insert text formats.
const (
	InsertPlainText = 1
	InsertSnippet   = 2
)

// CompletionItem is one textDocument/completion entry (the subset we use).
type CompletionItem struct {
	Label            string `json:"label"`
	Kind             int    `json:"kind,omitempty"`
	Detail           string `json:"detail,omitempty"`
	SortText         string `json:"sortText,omitempty"`
	FilterText       string `json:"filterText,omitempty"`
	InsertText       string `json:"insertText,omitempty"`
	InsertTextFormat int    `json:"insertTextFormat,omitempty"`
	// TextEdit is the primary edit. For an InsertReplaceEdit the replace
	// range is used.
	TextEdit            *TextEdit  `json:"-"`
	AdditionalTextEdits []TextEdit `json:"additionalTextEdits,omitempty"`
	// CommitCharacters accept the item when typed; Data round-trips through
	// completionItem/resolve.
	CommitCharacters []string        `json:"commitCharacters,omitempty"`
	Data             json.RawMessage `json:"data,omitempty"`
}

// UnmarshalJSON accepts both TextEdit and InsertReplaceEdit in textEdit.
func (c *CompletionItem) UnmarshalJSON(data []byte) error {
	type plain CompletionItem

	aux := struct {
		*plain
		TextEdit *struct {
			NewText string `json:"newText"`
			Range   *Range `json:"range"`
			Replace *Range `json:"replace"`
			Insert  *Range `json:"insert"`
		} `json:"textEdit"`
	}{plain: (*plain)(c)}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if te := aux.TextEdit; te != nil {
		r := te.Range
		if r == nil {
			r = te.Replace
		}

		if r == nil {
			r = te.Insert
		}

		if r != nil {
			c.TextEdit = &TextEdit{Range: *r, NewText: te.NewText}
		}
	}

	return nil
}

// CompletionParams is textDocument/completion.
type CompletionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      struct {
		TriggerKind      int    `json:"triggerKind"`
		TriggerCharacter string `json:"triggerCharacter,omitempty"`
	} `json:"context"`
}

// ParseCompletion normalizes a completion result (null, []CompletionItem or
// CompletionList).
func ParseCompletion(raw json.RawMessage) []CompletionItem {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var items []CompletionItem

	if raw[0] == '[' {
		_ = json.Unmarshal(raw, &items)

		return items
	}

	var list struct {
		Items []CompletionItem `json:"items"`
	}

	_ = json.Unmarshal(raw, &list)

	return list.Items
}

// Completion requests textDocument/completion at pos. trigger is the typed
// trigger character, or "" for an invoked request.
func (c *Client) Completion(ctx context.Context, uri string, pos Position, trigger string) ([]CompletionItem, error) {
	p := CompletionParams{TextDocument: TextDocumentIdentifier{URI: uri}, Position: pos}
	p.Context.TriggerKind = CompletionInvoked

	if trigger != "" {
		p.Context.TriggerKind, p.Context.TriggerCharacter = CompletionTriggerCharacter, trigger
	}

	var raw json.RawMessage
	if err := c.Call(ctx, "textDocument/completion", p, &raw); err != nil {
		return nil, err
	}

	return ParseCompletion(raw), nil
}

// CompletionTriggers reads completionProvider.triggerCharacters from the
// server capabilities (nil when absent).
func (c *Client) CompletionTriggers() []string {
	var caps struct {
		Completion *struct {
			Triggers []string `json:"triggerCharacters"`
		} `json:"completionProvider"`
	}

	if json.Unmarshal(c.Result.Capabilities, &caps) != nil || caps.Completion == nil {
		return nil
	}

	return caps.Completion.Triggers
}

// References requests textDocument/references.
func (c *Client) References(ctx context.Context, uri string, pos Position, includeDecl bool) ([]Location, error) {
	p := struct {
		TextDocumentPositionParams
		Context struct {
			IncludeDeclaration bool `json:"includeDeclaration"`
		} `json:"context"`
	}{TextDocumentPositionParams: TextDocumentPositionParams{TextDocument: TextDocumentIdentifier{URI: uri}, Position: pos}}
	p.Context.IncludeDeclaration = includeDecl

	var raw json.RawMessage
	if err := c.Call(ctx, "textDocument/references", p, &raw); err != nil {
		return nil, err
	}

	return ParseLocations(raw), nil
}

// WorkspaceEdit is the changes of a rename: edits grouped by document URI.
type WorkspaceEdit struct {
	Changes         map[string][]TextEdit `json:"changes,omitempty"`
	DocumentChanges []json.RawMessage     `json:"documentChanges,omitempty"`
}

// FileEdits flattens Changes and TextDocumentEdit entries of DocumentChanges
// into per-URI edits. Resource operations (create, rename, delete) are
// reported by unsupported.
func (w *WorkspaceEdit) FileEdits() (edits map[string][]TextEdit, unsupported int) {
	edits = map[string][]TextEdit{}

	for uri, list := range w.Changes {
		edits[uri] = append(edits[uri], list...)
	}

	for _, raw := range w.DocumentChanges {
		var dc struct {
			Kind         string                 `json:"kind"`
			TextDocument TextDocumentIdentifier `json:"textDocument"`
			Edits        []TextEdit             `json:"edits"`
		}
		if json.Unmarshal(raw, &dc) != nil {
			continue
		}

		if dc.Kind != "" || dc.TextDocument.URI == "" {
			unsupported++

			continue
		}

		edits[dc.TextDocument.URI] = append(edits[dc.TextDocument.URI], dc.Edits...)
	}

	return edits, unsupported
}

// Rename requests textDocument/rename. A nil edit means the server declined.
func (c *Client) Rename(ctx context.Context, uri string, pos Position, newName string) (*WorkspaceEdit, error) {
	p := struct {
		TextDocumentPositionParams
		NewName string `json:"newName"`
	}{TextDocumentPositionParams{TextDocument: TextDocumentIdentifier{URI: uri}, Position: pos}, newName}

	var edit *WorkspaceEdit
	if err := c.Call(ctx, "textDocument/rename", p, &edit); err != nil {
		return nil, err
	}

	return edit, nil
}

// Symbol is a flattened document or workspace symbol.
type Symbol struct {
	Name      string
	Kind      int
	Container string
	URI       string // workspace symbols only
	Range     Range  // selection range for document symbols
	Depth     int
}

// ParseDocumentSymbols flattens DocumentSymbol[] (hierarchical, pre-order) or
// SymbolInformation[] into one list.
func ParseDocumentSymbols(raw json.RawMessage) []Symbol {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}

	var out []Symbol

	var walk func(list []json.RawMessage, depth int)

	walk = func(list []json.RawMessage, depth int) {
		for _, it := range list {
			var s struct {
				Name           string            `json:"name"`
				Kind           int               `json:"kind"`
				Range          *Range            `json:"range"`
				SelectionRange *Range            `json:"selectionRange"`
				Children       []json.RawMessage `json:"children"`
				Location       *Location         `json:"location"`
				ContainerName  string            `json:"containerName"`
			}
			if json.Unmarshal(it, &s) != nil || s.Name == "" {
				continue
			}

			sym := Symbol{Name: s.Name, Kind: s.Kind, Container: s.ContainerName, Depth: depth}

			switch {
			case s.SelectionRange != nil:
				sym.Range = *s.SelectionRange
			case s.Range != nil:
				sym.Range = *s.Range
			case s.Location != nil:
				sym.Range, sym.URI = s.Location.Range, s.Location.URI
			}

			out = append(out, sym)
			walk(s.Children, depth+1)
		}
	}

	walk(items, 0)

	return out
}

// DocumentSymbols requests textDocument/documentSymbol.
func (c *Client) DocumentSymbols(ctx context.Context, uri string) ([]Symbol, error) {
	var raw json.RawMessage
	if err := c.Call(ctx, "textDocument/documentSymbol", map[string]any{"textDocument": TextDocumentIdentifier{URI: uri}}, &raw); err != nil {
		return nil, err
	}

	return ParseDocumentSymbols(raw), nil
}

// WorkspaceSymbols requests workspace/symbol.
func (c *Client) WorkspaceSymbols(ctx context.Context, query string) ([]Symbol, error) {
	var raw json.RawMessage
	if err := c.Call(ctx, "workspace/symbol", map[string]any{"query": query}, &raw); err != nil {
		return nil, err
	}

	syms := ParseDocumentSymbols(raw)
	out := syms[:0]

	for _, s := range syms {
		if s.URI != "" {
			out = append(out, s)
		}
	}

	return out, nil
}

// SymbolKindName maps an LSP SymbolKind to the outline kind vocabulary
// ("fn", "type", "class", "interface", "enum", "const", "var").
func SymbolKindName(kind int) string {
	switch kind {
	case 6, 9, 12:
		return "fn"
	case 5:
		return "class"
	case 11:
		return "interface"
	case 10:
		return "enum"
	case 14:
		return "const"
	case 2, 3, 4, 23, 26:
		return "type"
	default:
		return "var"
	}
}

// Progress is one $/progress report.
type Progress struct {
	Token   string
	Kind    string // begin, report, end
	Title   string
	Message string
	Percent int // -1 when unknown
}

// ParseProgress decodes $/progress params with a work-done value.
func ParseProgress(params json.RawMessage) (Progress, bool) {
	var p struct {
		Token json.RawMessage `json:"token"`
		Value struct {
			Kind       string `json:"kind"`
			Title      string `json:"title"`
			Message    string `json:"message"`
			Percentage *int   `json:"percentage"`
		} `json:"value"`
	}
	if json.Unmarshal(params, &p) != nil || p.Value.Kind == "" {
		return Progress{}, false
	}

	pr := Progress{
		Token: strings.Trim(string(p.Token), `"`), Kind: p.Value.Kind,
		Title: p.Value.Title, Message: p.Value.Message, Percent: -1,
	}
	if p.Value.Percentage != nil {
		pr.Percent = *p.Value.Percentage
	}

	return pr, true
}
