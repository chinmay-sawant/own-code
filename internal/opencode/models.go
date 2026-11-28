package opencode

import (
	"encoding/json"
)

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
