// Package opencode implements the OpenCode Server V2 HTTP API used by the
// native agent workbench.
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	serverUsername  = "opencode"
	maxResponseSize = 16 << 20
	maxRequestSize  = 16 << 20
)

// Client sends requests to an OpenCode Server V2 instance.
//
// A client should be used with caller-provided contexts that have deadlines.
// The event stream intentionally has no fixed duration limit.
type Client struct {
	baseURL  url.URL
	password string
	http     *http.Client
}

// NewClient creates a client for an OpenCode Server V2 instance. HTTP is
// allowed for loopback addresses. Non-loopback servers must use HTTPS.
func NewClient(rawURL, password string) (*Client, error) {
	return NewClientWithHTTPClient(rawURL, password, defaultHTTPClient())
}

// NewClientWithHTTPClient creates a client with a caller-supplied HTTP client.
// Passing nil uses the package default client.
func NewClientWithHTTPClient(rawURL, password string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, errors.New("invalid OpenCode server URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("OpenCode server URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("non-loopback OpenCode servers require HTTPS")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.EscapedPath(), "/")
	if httpClient == nil {
		httpClient = defaultHTTPClient()
	} else {
		copy := *httpClient
		httpClient = &copy
	}
	// Provider keys and prompts must never be replayed to a redirect target.
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Client{
		baseURL:  *parsed,
		password: password,
		http:     httpClient,
	}, nil
}

func defaultHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 15 * time.Second
	return &http.Client{Transport: transport}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// String returns a redacted representation so formatting a Client cannot
// reveal its server password.
func (c *Client) String() string {
	return "OpenCodeClient{server:" + c.baseURL.Redacted() + ", password:[redacted]}"
}

// GoString returns a redacted representation for %#v formatting.
func (c *Client) GoString() string { return c.String() }

func (c *Client) endpoint(segments ...string) *url.URL {
	u := c.baseURL
	decoded := strings.TrimRight(u.Path, "/")
	encoded := strings.TrimRight(u.EscapedPath(), "/")
	for _, segment := range segments {
		decoded += "/" + segment
		encoded += "/" + url.PathEscape(segment)
	}
	u.Path = decoded
	u.RawPath = encoded
	return &u
}

func (c *Client) newRequest(ctx context.Context, method string, endpoint *url.URL, body []byte, contentType string) (*http.Request, error) {
	if ctx == nil {
		return nil, errors.New("request context is nil")
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("could not construct OpenCode request")
	}
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if c.password != "" {
		request.SetBasicAuth(serverUsername, c.password)
	}
	return request, nil
}

func (c *Client) do(ctx context.Context, method string, endpoint *url.URL, input any, output any, acceptedStatus ...int) error {
	var body []byte
	contentType := ""
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return errors.New("could not encode OpenCode request")
		}
		if len(encoded) > maxRequestSize {
			clear(encoded)
			return errors.New("OpenCode request exceeds size limit")
		}
		body = encoded
		contentType = "application/json"
		defer clear(body)
	}
	request, err := c.newRequest(ctx, method, endpoint, body, contentType)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("OpenCode request failed: %w", err)
	}
	defer response.Body.Close()
	if !statusAccepted(response.StatusCode, acceptedStatus) {
		return &HTTPError{StatusCode: response.StatusCode}
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return errors.New("could not read OpenCode response")
	}
	if len(data) > maxResponseSize {
		return errors.New("OpenCode response exceeds size limit")
	}
	if err := json.Unmarshal(data, output); err != nil {
		return errors.New("could not decode OpenCode response")
	}
	return nil
}

func statusAccepted(status int, accepted []int) bool {
	for _, candidate := range accepted {
		if status == candidate {
			return true
		}
	}
	return false
}

// HTTPError reports a non-success response without retaining server-supplied
// response text, which could contain sensitive values.
type HTTPError struct {
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("OpenCode server returned HTTP status %d", e.StatusCode)
}

// GetInfo returns the server's identity and readiness information.
func (c *Client) GetInfo(ctx context.Context) (ServerInfo, error) {
	var info ServerInfo
	err := c.do(ctx, http.MethodGet, c.endpoint("api", "info"), nil, &info, http.StatusOK)
	return info, err
}

// ListIntegrations discovers available authentication methods and current
// connection status for the requested location. An empty directory uses the
// server's default location.
func (c *Client) ListIntegrations(ctx context.Context, directory string) ([]Integration, error) {
	endpoint := c.endpoint("api", "integration")
	if directory != "" {
		query := endpoint.Query()
		query.Set("location[directory]", directory)
		endpoint.RawQuery = query.Encode()
	}
	var response integrationListResponse
	err := c.do(ctx, http.MethodGet, endpoint, nil, &response, http.StatusOK)
	return response.Data, err
}

// ConnectKey submits a provider credential to OpenCode. OpenCode stores it;
// this client does not persist the key. Integration fields beyond the key are
// carried in Answer and Label when the advertised method requires them.
func (c *Client) ConnectKey(ctx context.Context, integrationID string, input ConnectKeyInput, directory string) error {
	if integrationID == "" {
		return errors.New("integration ID is required")
	}
	if input.Key == "" {
		return errors.New("integration key is required")
	}
	endpoint := c.endpoint("api", "integration", integrationID, "connect", "key")
	if directory != "" {
		query := endpoint.Query()
		query.Set("location[directory]", directory)
		endpoint.RawQuery = query.Encode()
	}
	body := connectKeyRequest{Key: input.Key, Answer: input.Answer, Label: input.Label}
	return c.do(ctx, http.MethodPost, endpoint, body, nil, http.StatusNoContent)
}

// ListSessions returns a page of sessions. OpenCode session routes are marked
// experimental in the current V2 API.
func (c *Client) ListSessions(ctx context.Context, options SessionListOptions) (SessionPage, error) {
	endpoint := c.endpoint("api", "session")
	query := endpoint.Query()
	if options.Limit > 0 {
		query.Set("limit", fmt.Sprint(options.Limit))
	}
	if options.Order != "" {
		query.Set("order", options.Order)
	}
	if options.Search != "" {
		query.Set("search", options.Search)
	}
	if options.ParentID != "" {
		query.Set("parentID", options.ParentID)
	}
	if options.Archived != nil {
		query.Set("archived", fmt.Sprint(*options.Archived))
	}
	if options.Directory != "" {
		query.Set("directory", options.Directory)
	}
	if options.Project != "" {
		query.Set("project", options.Project)
	}
	if options.Subpath != "" {
		query.Set("subpath", options.Subpath)
	}
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}
	endpoint.RawQuery = query.Encode()
	var page SessionPage
	err := c.do(ctx, http.MethodGet, endpoint, nil, &page, http.StatusOK)
	return page, err
}

// CreateSession creates an OpenCode session at the requested location.
func (c *Client) CreateSession(ctx context.Context, input CreateSessionInput) (Session, error) {
	var response sessionResponse
	err := c.do(ctx, http.MethodPost, c.endpoint("api", "session"), input, &response, http.StatusOK)
	return response.Data, err
}

// ListActiveSessions returns the sessions currently executing in the server
// process. The endpoint is used only as a transient status signal; session
// messages remain the source of transcript truth.
func (c *Client) ListActiveSessions(ctx context.Context) ([]ActiveSession, error) {
	var response activeSessionsResponse
	err := c.do(ctx, http.MethodGet, c.endpoint("api", "session", "active"), nil, &response, http.StatusOK)
	if err != nil {
		return nil, err
	}
	return decodeActiveSessions(response.Data)
}

func decodeActiveSessions(data json.RawMessage) ([]ActiveSession, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err == nil {
		active := make([]ActiveSession, 0, len(items))
		for _, item := range items {
			var session ActiveSession
			if err := json.Unmarshal(item, &session); err == nil && session.ID != "" {
				active = append(active, session)
				continue
			}
			var id string
			if err := json.Unmarshal(item, &id); err == nil && id != "" {
				active = append(active, ActiveSession{ID: id, Type: "running"})
			}
		}
		return active, nil
	}
	var keyed map[string]json.RawMessage
	if err := json.Unmarshal(data, &keyed); err == nil {
		active := make([]ActiveSession, 0, len(keyed))
		for id, raw := range keyed {
			if id == "" {
				continue
			}
			var status struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(raw, &status)
			active = append(active, ActiveSession{ID: id, Type: status.Type})
		}
		return active, nil
	}
	return nil, errors.New("could not decode active OpenCode sessions")
}

// Interrupt stops the active execution for a session. A false result means no
// execution owned by this process was running.
func (c *Client) Interrupt(ctx context.Context, sessionID string) (bool, error) {
	if sessionID == "" {
		return false, errors.New("session ID is required")
	}
	var response interruptSessionResponse
	err := c.do(ctx, http.MethodPost, c.endpoint("api", "session", sessionID, "interrupt"), nil, &response, http.StatusOK)
	return response.Interrupted, err
}

// Prompt queues a user message and starts the session unless Resume is false.
// A successful return means the message was queued, not that the agent has
// finished; consume events and then retrieve messages with ListMessages.
func (c *Client) Prompt(ctx context.Context, sessionID string, input PromptInput) (InboxItem, error) {
	if sessionID == "" {
		return InboxItem{}, errors.New("session ID is required")
	}
	var response inboxItemResponse
	err := c.do(ctx, http.MethodPost, c.endpoint("api", "session", sessionID, "prompt"), input, &response, http.StatusOK)
	return response.Data, err
}

// ListMessages retrieves a page of persisted session messages. Variant message
// shapes are retained as RawMessage because the API exposes a discriminated union.
func (c *Client) ListMessages(ctx context.Context, sessionID string, options MessageListOptions) (MessagePage, error) {
	if sessionID == "" {
		return MessagePage{}, errors.New("session ID is required")
	}
	endpoint := c.endpoint("api", "session", sessionID, "message")
	query := endpoint.Query()
	if options.Limit > 0 {
		query.Set("limit", fmt.Sprint(options.Limit))
	}
	if options.Order != "" {
		query.Set("order", options.Order)
	}
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}
	if options.Type != "" {
		query.Set("type", options.Type)
	}
	endpoint.RawQuery = query.Encode()
	var page MessagePage
	err := c.do(ctx, http.MethodGet, endpoint, nil, &page, http.StatusOK)
	return page, err
}

// SubscribeEvents opens the volatile OpenCode server event stream. Events can
// be missed while disconnected and must not be used as a durable checkpoint.
func (c *Client) SubscribeEvents(ctx context.Context, types ...string) (*EventStream, error) {
	endpoint := c.endpoint("api", "event")
	if len(types) != 0 {
		query := endpoint.Query()
		for _, eventType := range types {
			if eventType != "" {
				query.Add("types", eventType)
			}
		}
		endpoint.RawQuery = query.Encode()
	}
	request, err := c.newRequest(ctx, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("OpenCode event stream failed: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, &HTTPError{StatusCode: response.StatusCode}
	}
	return newEventStream(response.Body), nil
}
