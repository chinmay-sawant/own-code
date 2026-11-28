package opencode

import (
	"encoding/json"
)

// ServerInfo contains the stable identity fields returned by GET /api/info.
type ServerInfo struct {
	Version      string          `json:"version"`
	PID          int64           `json:"pid"`
	Hostname     *string         `json:"hostname,omitempty"`
	URLs         []string        `json:"urls"`
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
}

type integrationListResponse struct {
	Data []Integration `json:"data"`
}

// Integration describes an available server-side authentication integration.
type Integration struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Metadata    json.RawMessage     `json:"metadata,omitempty"`
	Methods     []IntegrationMethod `json:"methods"`
	Connections []json.RawMessage   `json:"connections"`
}

// IntegrationMethod preserves server-advertised form data and unknown fields.
// The current API has several method variants; callers should inspect Type
// before choosing an authentication flow.
type IntegrationMethod struct {
	ID    string          `json:"id,omitempty"`
	Type  string          `json:"type"`
	Label string          `json:"label,omitempty"`
	Form  json.RawMessage `json:"form,omitempty"`
	Raw   json.RawMessage `json:"-"`
}

func (m *IntegrationMethod) UnmarshalJSON(data []byte) error {
	type methodAlias IntegrationMethod
	var decoded methodAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*m = IntegrationMethod(decoded)
	m.Raw = append(m.Raw[:0], data...)
	return nil
}

// ConnectKeyInput is sent only to OpenCode's integration connect-key route.
// Answer is the method's optional form answer as advertised by the server.
type ConnectKeyInput struct {
	Key    string
	Answer json.RawMessage
	Label  *string
}

func (ConnectKeyInput) String() string         { return "ConnectKeyInput{key:[redacted]}" }
func (input ConnectKeyInput) GoString() string { return input.String() }

type connectKeyRequest struct {
	Key    string          `json:"key"`
	Answer json.RawMessage `json:"answer,omitempty"`
	Label  *string         `json:"label,omitempty"`
}

// Session is the subset of Session.Info needed by the workbench. Unknown
// session fields are ignored so server additions do not break this client.
type Session struct {
	ID        string      `json:"id"`
	ParentID  string      `json:"parentID,omitempty"`
	ProjectID string      `json:"projectID,omitempty"`
	Title     string      `json:"title,omitempty"`
	Time      SessionTime `json:"time"`
	Location  LocationRef `json:"location"`
}

type SessionTime struct {
	Created float64 `json:"created"`
	Updated float64 `json:"updated"`
}

// LocationRef identifies the project directory used by a session.
type LocationRef struct {
	Directory string `json:"directory"`
}

// ModelRef selects an OpenCode provider model.
type ModelRef struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
	Variant    string `json:"variant,omitempty"`
}

// CreateSessionInput creates a root session or a linked child session.
type CreateSessionInput struct {
	ID          string          `json:"id,omitempty"`
	ParentID    string          `json:"parentID,omitempty"`
	Title       string          `json:"title,omitempty"`
	Agent       string          `json:"agent,omitempty"`
	Model       *ModelRef       `json:"model,omitempty"`
	Location    *LocationRef    `json:"location,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
	Permissions json.RawMessage `json:"permissions,omitempty"`
}

type sessionResponse struct {
	Data Session `json:"data"`
}

// SessionListOptions controls pagination and filtering for ListSessions.
type SessionListOptions struct {
	Limit     int
	Order     string
	Search    string
	ParentID  string
	Archived  *bool
	Directory string
	Project   string
	Subpath   string
	Cursor    string
}

// SessionPage is the response from GET /api/session.
type SessionPage struct {
	Data   []Session `json:"data"`
	Cursor Cursor    `json:"cursor"`
}

// ActiveSession is one session whose agent is running in this server process.
type ActiveSession struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type activeSessionsResponse struct {
	Data json.RawMessage `json:"data"`
}

type interruptSessionResponse struct {
	Interrupted bool `json:"interrupted"`
}

// Cursor contains opaque pagination tokens from the OpenCode server.
type Cursor struct {
	Previous *string `json:"previous"`
	Next     *string `json:"next"`
}

// PromptInput queues a user prompt on an existing session. Attachment fields
// are raw JSON to preserve the evolving OpenCode schema.
type PromptInput struct {
	ID       string            `json:"id,omitempty"`
	Text     string            `json:"text"`
	Files    []json.RawMessage `json:"files,omitempty"`
	Agents   []json.RawMessage `json:"agents,omitempty"`
	Skills   []json.RawMessage `json:"skills,omitempty"`
	Metadata map[string]any    `json:"metadata,omitempty"`
	Delivery json.RawMessage   `json:"delivery,omitempty"`
	Resume   *bool             `json:"resume,omitempty"`
}

// InboxItem is the queued user message returned by the prompt route.
type InboxItem struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionID"`
	Time      InboxItemTime   `json:"time"`
	Type      string          `json:"type"`
	Payload   PromptPayload   `json:"payload"`
	Delivery  json.RawMessage `json:"delivery"`
}

type InboxItemTime struct {
	Created float64 `json:"created"`
}

type PromptPayload struct {
	Text     string            `json:"text"`
	Files    []json.RawMessage `json:"files,omitempty"`
	Agents   []json.RawMessage `json:"agents,omitempty"`
	Skills   []json.RawMessage `json:"skills,omitempty"`
	Metadata map[string]any    `json:"metadata,omitempty"`
}

type inboxItemResponse struct {
	Data InboxItem `json:"data"`
}

// MessageListOptions controls pagination and filtering for ListMessages.
type MessageListOptions struct {
	Limit  int
	Order  string
	Cursor string
	Type   string
}

// MessagePage contains raw message variants because the current API returns a
// discriminated union covering assistant, user, tool and lifecycle messages.
type MessagePage struct {
	Data   []json.RawMessage `json:"data"`
	Cursor Cursor            `json:"cursor"`
}
