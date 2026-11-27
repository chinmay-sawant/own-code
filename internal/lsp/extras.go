package lsp

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf16"
)

// This file holds the smaller LSP requests: prepareRename, signature help,
// code actions, executeCommand and completionItem/resolve.

// capabilityFlag decodes one provider capability, which is either a bool or
// an options object. sub names a bool option inside the object ("" tests the
// provider itself).
func (c *Client) capabilityFlag(provider, sub string) bool {
	var caps map[string]json.RawMessage
	if json.Unmarshal(c.Result.Capabilities, &caps) != nil {
		return false
	}

	raw := caps[provider]
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" {
		return false
	}

	if sub == "" {
		return true
	}

	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return false
	}

	return string(obj[sub]) == "true"
}

// CapabilityList reads a string list option of a provider capability.
func (c *Client) capabilityList(provider, key string) []string {
	var caps map[string]json.RawMessage
	if json.Unmarshal(c.Result.Capabilities, &caps) != nil {
		return nil
	}

	var obj map[string]json.RawMessage
	if json.Unmarshal(caps[provider], &obj) != nil {
		return nil
	}

	var out []string

	_ = json.Unmarshal(obj[key], &out)

	return out
}

// SupportsPrepareRename reports renameProvider.prepareProvider.
func (c *Client) SupportsPrepareRename() bool {
	return c.capabilityFlag("renameProvider", "prepareProvider")
}

// SupportsCodeActions reports codeActionProvider.
func (c *Client) SupportsCodeActions() bool { return c.capabilityFlag("codeActionProvider", "") }

// SupportsCompletionResolve reports completionProvider.resolveProvider.
func (c *Client) SupportsCompletionResolve() bool {
	return c.capabilityFlag("completionProvider", "resolveProvider")
}

// CompletionCommitChars is completionProvider.allCommitCharacters.
func (c *Client) CompletionCommitChars() []string {
	return c.capabilityList("completionProvider", "allCommitCharacters")
}

// SignatureTriggers is signatureHelpProvider's trigger and retrigger
// characters; nil when the server has no signature help.
func (c *Client) SignatureTriggers() []string {
	if !c.capabilityFlag("signatureHelpProvider", "") {
		return nil
	}

	return append(c.capabilityList("signatureHelpProvider", "triggerCharacters"),
		c.capabilityList("signatureHelpProvider", "retriggerCharacters")...)
}

// PrepareRenameResult is the answer of textDocument/prepareRename.
type PrepareRenameResult struct {
	Range       *Range
	Placeholder string
}

// ParsePrepareRename decodes the reply. ok=false means the server refused
// (null). A {defaultBehavior} reply yields an empty result with ok=true.
func ParsePrepareRename(raw json.RawMessage) (PrepareRenameResult, bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return PrepareRenameResult{}, false
	}

	var v struct {
		Range       *Range `json:"range"`
		Placeholder string `json:"placeholder"`
	}

	var probe struct {
		Start *Position `json:"start"`
		End   *Position `json:"end"`
	}

	if json.Unmarshal(raw, &probe) == nil && probe.Start != nil && probe.End != nil {
		return PrepareRenameResult{Range: &Range{Start: *probe.Start, End: *probe.End}}, true
	}

	if json.Unmarshal(raw, &v) != nil {
		return PrepareRenameResult{}, false
	}

	return PrepareRenameResult{Range: v.Range, Placeholder: v.Placeholder}, true
}

// PrepareRename requests textDocument/prepareRename.
func (c *Client) PrepareRename(ctx context.Context, uri string, pos Position) (PrepareRenameResult, bool, error) {
	var raw json.RawMessage

	p := TextDocumentPositionParams{TextDocument: TextDocumentIdentifier{URI: uri}, Position: pos}
	if err := c.Call(ctx, "textDocument/prepareRename", p, &raw); err != nil {
		return PrepareRenameResult{}, false, err
	}

	res, ok := ParsePrepareRename(raw)

	return res, ok, nil
}

// ParameterInfo is one signature parameter. Start/End are rune offsets into
// the signature label (-1 when unknown).
type ParameterInfo struct {
	Label      string
	Start, End int
}

// SignatureInfo is one overload.
type SignatureInfo struct {
	Label         string
	Documentation string
	Parameters    []ParameterInfo
	ActiveParam   int // -1 when unset
}

// SignatureHelp is the textDocument/signatureHelp result.
type SignatureHelp struct {
	Signatures      []SignatureInfo
	ActiveSignature int
	ActiveParameter int
}

func docString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	var mc struct {
		Value string `json:"value"`
	}

	_ = json.Unmarshal(raw, &mc)

	return mc.Value
}

// ParseSignatureHelp decodes the reply; nil when empty.
func ParseSignatureHelp(raw json.RawMessage) *SignatureHelp {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return nil
	}

	var v struct {
		Signatures []struct {
			Label         string          `json:"label"`
			Documentation json.RawMessage `json:"documentation"`
			Parameters    []struct {
				Label json.RawMessage `json:"label"`
			} `json:"parameters"`
			ActiveParameter *int `json:"activeParameter"`
		} `json:"signatures"`
		ActiveSignature *int `json:"activeSignature"`
		ActiveParameter *int `json:"activeParameter"`
	}

	if json.Unmarshal(raw, &v) != nil || len(v.Signatures) == 0 {
		return nil
	}

	out := &SignatureHelp{}
	if v.ActiveSignature != nil {
		out.ActiveSignature = *v.ActiveSignature
	}

	if v.ActiveParameter != nil {
		out.ActiveParameter = *v.ActiveParameter
	}

	for _, sg := range v.Signatures {
		info := SignatureInfo{Label: sg.Label, Documentation: docString(sg.Documentation), ActiveParam: -1}
		if sg.ActiveParameter != nil {
			info.ActiveParam = *sg.ActiveParameter
		}

		for _, pr := range sg.Parameters {
			pi := ParameterInfo{Start: -1, End: -1}

			var name string

			var span [2]int

			switch {
			case json.Unmarshal(pr.Label, &name) == nil:
				pi.Label = name
				if i := strings.Index(sg.Label, name); i >= 0 && name != "" {
					pi.Start = len([]rune(sg.Label[:i]))
					pi.End = pi.Start + len([]rune(name))
				}
			case json.Unmarshal(pr.Label, &span) == nil:
				// Offsets are UTF-16 units into the label.
				u := utf16.Encode([]rune(sg.Label))
				a, b := max(0, min(span[0], len(u))), max(0, min(span[1], len(u)))
				pi.Label = string(utf16.Decode(u[a:b]))
				pi.Start = len(utf16.Decode(u[:a]))
				pi.End = pi.Start + len([]rune(pi.Label))
			}

			info.Parameters = append(info.Parameters, pi)
		}

		out.Signatures = append(out.Signatures, info)
	}

	if out.ActiveSignature < 0 || out.ActiveSignature >= len(out.Signatures) {
		out.ActiveSignature = 0
	}

	return out
}

// SignatureHelp requests textDocument/signatureHelp.
func (c *Client) SignatureHelp(ctx context.Context, uri string, pos Position, trigger string) (*SignatureHelp, error) {
	p := struct {
		TextDocumentPositionParams
		Context struct {
			TriggerKind      int    `json:"triggerKind"`
			TriggerCharacter string `json:"triggerCharacter,omitempty"`
			IsRetrigger      bool   `json:"isRetrigger"`
		} `json:"context"`
	}{TextDocumentPositionParams: TextDocumentPositionParams{TextDocument: TextDocumentIdentifier{URI: uri}, Position: pos}}
	p.Context.TriggerKind = 1

	if trigger != "" {
		p.Context.TriggerKind, p.Context.TriggerCharacter = 2, trigger
	}

	var raw json.RawMessage
	if err := c.Call(ctx, "textDocument/signatureHelp", p, &raw); err != nil {
		return nil, err
	}

	return ParseSignatureHelp(raw), nil
}

// Command is a server command.
type Command struct {
	Title     string          `json:"title"`
	Command   string          `json:"command"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CodeAction is one quick fix or refactor. Raw keeps the server's object for
// codeAction/resolve.
type CodeAction struct {
	Title       string
	Kind        string
	IsPreferred bool
	Disabled    bool
	Edit        *WorkspaceEdit
	Command     *Command
	Raw         json.RawMessage
}

// ParseCodeActions decodes a Command | CodeAction array. A bare Command
// (command is a string) becomes an action that only runs the command.
func ParseCodeActions(raw json.RawMessage) []CodeAction {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}

	var out []CodeAction

	for _, it := range items {
		var v struct {
			Title       string          `json:"title"`
			Kind        string          `json:"kind"`
			IsPreferred bool            `json:"isPreferred"`
			Disabled    json.RawMessage `json:"disabled"`
			Edit        *WorkspaceEdit  `json:"edit"`
			Command     json.RawMessage `json:"command"`
			Arguments   json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(it, &v) != nil || v.Title == "" {
			continue
		}

		ca := CodeAction{Title: v.Title, Kind: v.Kind, IsPreferred: v.IsPreferred, Edit: v.Edit, Raw: it}
		ca.Disabled = len(v.Disabled) > 0 && string(v.Disabled) != "null"

		var name string
		if json.Unmarshal(v.Command, &name) == nil && name != "" {
			ca.Command = &Command{Title: v.Title, Command: name, Arguments: v.Arguments}
		} else {
			var cmd Command
			if json.Unmarshal(v.Command, &cmd) == nil && cmd.Command != "" {
				ca.Command = &cmd
			}
		}

		out = append(out, ca)
	}

	return out
}

// CodeActions requests textDocument/codeAction for r with the diagnostics
// that overlap it.
func (c *Client) CodeActions(ctx context.Context, uri string, r Range, diags []Diagnostic) ([]CodeAction, error) {
	if diags == nil {
		diags = []Diagnostic{}
	}

	p := map[string]any{
		"textDocument": TextDocumentIdentifier{URI: uri},
		"range":        r,
		"context":      map[string]any{"diagnostics": diags},
	}

	var raw json.RawMessage
	if err := c.Call(ctx, "textDocument/codeAction", p, &raw); err != nil {
		return nil, err
	}

	return ParseCodeActions(raw), nil
}

// ResolveCodeAction requests codeAction/resolve for an action's raw object.
func (c *Client) ResolveCodeAction(ctx context.Context, ca CodeAction) (CodeAction, error) {
	var raw json.RawMessage
	if err := c.Call(ctx, "codeAction/resolve", ca.Raw, &raw); err != nil {
		return ca, err
	}

	if got := ParseCodeActions(json.RawMessage("[" + string(raw) + "]")); len(got) == 1 {
		return got[0], nil
	}

	return ca, nil
}

// ExecuteCommand requests workspace/executeCommand.
func (c *Client) ExecuteCommand(ctx context.Context, cmd Command) error {
	var args any
	if len(cmd.Arguments) > 0 {
		args = cmd.Arguments
	}

	var raw json.RawMessage

	return c.Call(ctx, "workspace/executeCommand", map[string]any{"command": cmd.Command, "arguments": args}, &raw)
}

// ResolveCompletion requests completionItem/resolve. The returned item only
// carries the fields servers fill lazily (detail, additionalTextEdits).
func (c *Client) ResolveCompletion(ctx context.Context, it CompletionItem) (CompletionItem, error) {
	var out CompletionItem
	if err := c.Call(ctx, "completionItem/resolve", it, &out); err != nil {
		return it, err
	}

	return out, nil
}
