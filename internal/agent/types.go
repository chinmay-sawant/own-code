// Package agent defines provider-neutral inference types and the Console
// protocol adapters (chat-completions and responses). It has no dependency on
// internal/app, OwnFrame, or storage. Tool execution lives outside this
// package: adapters only report complete tool calls.
package agent

import (
	"context"
	"encoding/json"
)

// Protocol names match the Console catalog's "api" keys exactly.
const (
	ProtocolChatCompletions = "chat-completions"
	ProtocolResponses       = "responses"
	// ProtocolMessages is the Anthropic Messages family. "messages" is the
	// exact api key the Console catalog uses (see internal/opencode/console_test.go
	// fixture and the Inference API docs: /inference/anthropic/v1/messages).
	ProtocolMessages = "messages"
)

// Role identifies the author of a Message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one provider-neutral history entry. Assistant messages may carry
// complete ToolCalls; tool messages carry the matching ToolCallID.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

// ToolCall is a complete tool request. Arguments is always valid JSON.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolSpec advertises one function tool to the model.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON schema object
}

// ModelTarget is the catalog-derived routing for a request. Provider and model
// IDs are kept separate; routing never depends on the model name.
type ModelTarget struct {
	ProviderID     string
	ModelID        string
	Protocol       string
	BaseURL        string
	ToolsSupported bool
}

// Request is one inference call for a single turn.
type Request struct {
	// SessionID is the stable opaque local session ID sent as x-opencode-session.
	SessionID       string
	TurnID          string
	Model           ModelTarget
	Messages        []Message
	Tools           []ToolSpec
	MaxOutputTokens int
}

// EventKind enumerates streamed events.
type EventKind string

const (
	EventTextDelta EventKind = "text_delta"
	EventToolCall  EventKind = "tool_call" // complete calls only
	EventUsage     EventKind = "usage"
	// EventToolStart and EventToolDone report local tool lifecycle. ToolCall.ID
	// is the stored call ID; Text on Done is the terminal status
	// (succeeded, failed, denied, rejected, skipped).
	EventToolStart EventKind = "tool_start"
	EventToolDone  EventKind = "tool_done"
)

// Event is delivered synchronously to the emit callback. SessionID and TurnID
// let consumers drop stale events.
type Event struct {
	Kind      EventKind
	SessionID string
	TurnID    string
	Text      string
	ToolCall  *ToolCall
	Usage     *Usage
}

// Usage reports token counts when the provider supplies them.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// Status is the terminal state of a turn request.
type Status string

const (
	StatusCompleted   Status = "completed"
	StatusInterrupted Status = "interrupted" // canceled by the caller
	StatusFailed      Status = "failed"      // provider, transport, or protocol failure
)

// Result is returned for both success and failure; on failure and interruption
// Text holds the partial output received before the stop.
type Result struct {
	Status       Status
	Text         string
	ToolCalls    []ToolCall
	Usage        *Usage
	FinishReason string
}

// Provider performs inference. Cancel the context to stop a request: the
// response stream is closed and no further events are emitted.
type Provider interface {
	Complete(ctx context.Context, req Request) (Result, error)
	Stream(ctx context.Context, req Request, emit func(Event)) (Result, error)
}
