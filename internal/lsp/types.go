// Package lsp is a minimal Language Server Protocol client: JSON-RPC
// transport, process lifecycle, UTF-16 position conversion and a server
// registry. It has no UI dependencies.
package lsp

import "encoding/json"

// Position is a zero-based line and UTF-16 code unit offset.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a half-open span between two positions.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location points into a document.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// TextDocumentItem is the payload of textDocument/didOpen.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// TextDocumentIdentifier names a document.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// VersionedTextDocumentIdentifier names a document at a version.
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// ContentChange is a change event. With Range nil it replaces the whole document.
type ContentChange struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

// DidOpenParams is textDocument/didOpen.
type DidOpenParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidChangeParams is textDocument/didChange.
type DidChangeParams struct {
	TextDocument   VersionedTextDocumentIdentifier `json:"textDocument"`
	ContentChanges []ContentChange                 `json:"contentChanges"`
}

// DidSaveParams is textDocument/didSave.
type DidSaveParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

// DidCloseParams is textDocument/didClose.
type DidCloseParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// Diagnostic severities.
const (
	SeverityError   = 1
	SeverityWarning = 2
	SeverityInfo    = 3
	SeverityHint    = 4
)

// Diagnostic is a server-reported problem.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"`
	Code     any    `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

// PublishDiagnosticsParams is textDocument/publishDiagnostics.
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// InitializeParams is the subset of initialize we send.
type InitializeParams struct {
	ProcessID        int                `json:"processId"`
	ClientInfo       *Info              `json:"clientInfo,omitempty"`
	RootURI          string             `json:"rootUri"`
	Capabilities     ClientCapabilities `json:"capabilities"`
	WorkspaceFolders []WorkspaceFolder  `json:"workspaceFolders,omitempty"`
	InitOptions      any                `json:"initializationOptions,omitempty"`
}

// WorkspaceFolder is a root folder.
type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// Info names a client or server.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ClientCapabilities advertises what the client supports.
type ClientCapabilities struct {
	TextDocument map[string]any `json:"textDocument,omitempty"`
	General      map[string]any `json:"general,omitempty"`
}

// InitializeResult is the server reply to initialize.
type InitializeResult struct {
	Capabilities json.RawMessage `json:"capabilities"`
	ServerInfo   *Info           `json:"serverInfo,omitempty"`
}
