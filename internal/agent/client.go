package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	sessionHeader       = "x-opencode-session"
	defaultMaxBody      = 8 << 20
	defaultMaxFrame     = 1 << 20
	defaultMaxStream    = 32 << 20
	defaultConnectTime  = 20 * time.Second
	defaultIdleTimeout  = 60 * time.Second
	defaultRequestTime  = 3 * time.Minute
	maxErrorBody        = 8 << 10
	maxErrorMessageRune = 300
)

// ErrKind classifies provider failures.
type ErrKind string

const (
	ErrKindInvalid     ErrKind = "invalid_request"
	ErrKindAuth        ErrKind = "auth"
	ErrKindForbidden   ErrKind = "forbidden"
	ErrKindRateLimit   ErrKind = "rate_limit"
	ErrKindServer      ErrKind = "server"
	ErrKindHTTP        ErrKind = "http"
	ErrKindTransport   ErrKind = "transport"
	ErrKindTimeout     ErrKind = "timeout"
	ErrKindMalformed   ErrKind = "malformed"
	ErrKindIncomplete  ErrKind = "incomplete"
	ErrKindTooLarge    ErrKind = "too_large"
	ErrKindInterrupted ErrKind = "interrupted"
)

// Error is a user-safe provider error. It never contains the API key.
type Error struct {
	Kind    ErrKind
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// ErrInterrupted is returned (wrapped in *Error) when the caller cancels.
var ErrInterrupted = &Error{Kind: ErrKindInterrupted, Message: "request interrupted"}

// Options tunes limits. Zero values select defaults.
type Options struct {
	HTTPClient     *http.Client
	MaxBodyBytes   int64
	MaxFrameBytes  int
	MaxStreamBytes int64
	IdleTimeout    time.Duration
	RequestTimeout time.Duration // non-stream total timeout
}

// Client sends Console inference requests directly; no OpenCode Server.
type Client struct {
	apiKey string
	http   *http.Client
	opt    Options
}

var _ Provider = (*Client)(nil)

// NewClient creates a client for a workspace-bound Console API key.
func NewClient(apiKey string, opt Options) (*Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("OpenCode Console API key is required")
	}
	if opt.MaxBodyBytes <= 0 {
		opt.MaxBodyBytes = defaultMaxBody
	}
	if opt.MaxFrameBytes <= 0 {
		opt.MaxFrameBytes = defaultMaxFrame
	}
	if opt.MaxStreamBytes <= 0 {
		opt.MaxStreamBytes = defaultMaxStream
	}
	if opt.IdleTimeout <= 0 {
		opt.IdleTimeout = defaultIdleTimeout
	}
	if opt.RequestTimeout <= 0 {
		opt.RequestTimeout = defaultRequestTime
	}
	hc := opt.HTTPClient
	if hc == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.ResponseHeaderTimeout = defaultConnectTime
		hc = &http.Client{Transport: tr}
	}
	copyClient := *hc
	copyClient.Timeout = 0 // per-request contexts govern timing
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{apiKey: apiKey, http: &copyClient, opt: opt}, nil
}

func (c *Client) String() string   { return "agent.Client{key:[redacted]}" }
func (c *Client) GoString() string { return c.String() }

// Complete performs a non-streaming request.
func (c *Client) Complete(ctx context.Context, req Request) (Result, error) {
	return c.run(ctx, req, false, nil)
}

// Stream performs a streaming request. emit is called synchronously and never
// after ctx is canceled or after Stream returns.
func (c *Client) Stream(ctx context.Context, req Request, emit func(Event)) (Result, error) {
	if emit == nil {
		emit = func(Event) {}
	}
	return c.run(ctx, req, true, emit)
}

func (c *Client) run(ctx context.Context, req Request, stream bool, emit func(Event)) (Result, error) {
	fail := func(e *Error) (Result, error) { return Result{Status: StatusFailed}, e }
	if c == nil || c.http == nil || c.apiKey == "" {
		return fail(&Error{Kind: ErrKindInvalid, Message: "inference client is not configured"})
	}
	if ctx == nil {
		return fail(&Error{Kind: ErrKindInvalid, Message: "request context is nil"})
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return fail(&Error{Kind: ErrKindInvalid, Message: "local session ID is required"})
	}
	if len(req.Messages) == 0 {
		return fail(&Error{Kind: ErrKindInvalid, Message: "request has no messages"})
	}
	if err := req.Model.Validate(); err != nil {
		return fail(&Error{Kind: ErrKindInvalid, Message: "model is not eligible: " + err.Error()})
	}
	endpoint, _ := req.Model.endpointFor(stream)
	var proto protocolAdapter = chatAdapter{}
	switch req.Model.Protocol {
	case ProtocolResponses:
		proto = responsesAdapter{}
	case ProtocolMessages:
		proto = anthropicAdapter{}
	case ProtocolGenerateContent:
		proto = geminiAdapter{}
	}
	body, err := proto.encode(req, stream)
	if err != nil {
		return fail(&Error{Kind: ErrKindInvalid, Message: "could not encode request: " + err.Error()})
	}
	if ctx.Err() != nil {
		return Result{Status: StatusInterrupted}, ErrInterrupted
	}

	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var timedOut atomic.Bool
	var timer *time.Timer
	if stream {
		timer = time.AfterFunc(c.opt.IdleTimeout, func() { timedOut.Store(true); cancel() })
	} else {
		timer = time.AfterFunc(c.opt.RequestTimeout, func() { timedOut.Store(true); cancel() })
	}
	defer timer.Stop()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fail(&Error{Kind: ErrKindInvalid, Message: "could not create inference request"})
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set(sessionHeader, req.SessionID)
	if req.Model.Protocol == ProtocolMessages {
		httpReq.Header.Set("anthropic-version", anthropicVersion)
	}
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}

	// classify maps a failure after the request started.
	var partial Result
	classify := func(err error, kind ErrKind, msg string) (Result, error) {
		if ctx.Err() != nil {
			partial.Status = StatusInterrupted
			return partial, ErrInterrupted
		}
		if timedOut.Load() {
			partial.Status = StatusFailed
			return partial, &Error{Kind: ErrKindTimeout, Message: "inference request timed out"}
		}
		var e *Error
		if errors.As(err, &e) {
			partial.Status = StatusFailed
			return partial, e
		}
		partial.Status = StatusFailed
		return partial, &Error{Kind: kind, Message: msg}
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return classify(err, ErrKindTransport, "inference request failed: "+c.redact(errString(err)))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fail(c.httpError(resp))
	}

	// live emit gate: nothing is delivered once the caller has canceled.
	gate := func(ev Event) {
		if emit == nil || ctx.Err() != nil {
			return
		}
		ev.SessionID, ev.TurnID = req.SessionID, req.TurnID
		emit(ev)
	}
	if !stream {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, c.opt.MaxBodyBytes+1))
		if err != nil {
			return classify(err, ErrKindTransport, "could not read inference response")
		}
		if int64(len(raw)) > c.opt.MaxBodyBytes {
			return fail(&Error{Kind: ErrKindTooLarge, Message: "inference response exceeds the size limit"})
		}
		res, err := proto.decode(raw)
		if err != nil {
			return classify(err, ErrKindMalformed, "could not decode inference response")
		}
		res.Status = StatusCompleted
		return res, nil
	}

	sr := &sseReader{
		r:        bufio.NewReaderSize(&activityReader{r: resp.Body, timer: timer, idle: c.opt.IdleTimeout, limit: c.opt.MaxStreamBytes}, 32<<10),
		maxFrame: c.opt.MaxFrameBytes,
	}
	st := proto.newStream(gate)
	for {
		data, err := sr.next()
		if err != nil {
			partial = st.partial()
			if errors.Is(err, io.EOF) && ctx.Err() == nil {
				// Some providers omit the terminator; finish decides whether
				// the stream was complete.
				break
			}
			return classify(err, ErrKindTransport, "inference stream failed")
		}
		if ctx.Err() != nil {
			partial = st.partial()
			return classify(nil, ErrKindTransport, "")
		}
		done, err := st.handle(data)
		if err != nil {
			partial = st.partial()
			return classify(err, ErrKindMalformed, "malformed inference stream event")
		}
		if done {
			break
		}
	}
	res, err := st.finish()
	if err != nil {
		partial = st.partial()
		return classify(err, ErrKindIncomplete, "inference stream ended before completion")
	}
	res.Status = StatusCompleted
	return res, nil
}

// protocolAdapter encodes requests and decodes responses for one API family.
type protocolAdapter interface {
	encode(Request, bool) ([]byte, error)
	decode([]byte) (Result, error)
	newStream(emit func(Event)) streamState
}

// streamState consumes SSE data payloads. handle returns done at the terminal
// event; finish returns the final result containing only complete tool calls.
type streamState interface {
	handle(data []byte) (done bool, err error)
	finish() (Result, error)
	partial() Result
}

func (c *Client) httpError(resp *http.Response) *Error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	detail := c.redact(extractErrorMessage(raw))
	status := resp.StatusCode
	e := &Error{Status: status}
	switch {
	case status == http.StatusUnauthorized:
		e.Kind, e.Message = ErrKindAuth, "OpenCode Console rejected the API key (HTTP 401). Check the key in settings."
	case status == http.StatusForbidden:
		e.Kind = ErrKindForbidden
		e.Message = "OpenCode Console denied access to this model (HTTP 403). Catalog membership does not guarantee inference access: the model may require a paid plan or balance, or the workspace or key may not be eligible for it."
	case status == http.StatusTooManyRequests:
		e.Kind, e.Message = ErrKindRateLimit, "OpenCode Console rate limit reached (HTTP 429). Try again shortly."
	case status >= 500:
		e.Kind, e.Message = ErrKindServer, fmt.Sprintf("OpenCode Console provider error (HTTP %d).", status)
	default:
		e.Kind, e.Message = ErrKindHTTP, fmt.Sprintf("OpenCode Console rejected the request (HTTP %d).", status)
	}
	if detail != "" {
		e.Message += " " + detail
	}
	return e
}

func (c *Client) redact(s string) string {
	if c.apiKey != "" {
		s = strings.ReplaceAll(s, c.apiKey, "[redacted]")
	}
	return s
}

func errString(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}

func extractErrorMessage(raw []byte) string {
	var env struct {
		Error json.RawMessage `json:"error"`
		Msg   string          `json:"message"`
	}
	msg := ""
	if json.Unmarshal(raw, &env) == nil {
		msg = env.Msg
		if len(env.Error) > 0 {
			var obj struct {
				Message string `json:"message"`
			}
			var str string
			if json.Unmarshal(env.Error, &obj) == nil && obj.Message != "" {
				msg = obj.Message
			} else if json.Unmarshal(env.Error, &str) == nil && str != "" {
				msg = str
			}
		}
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if r := []rune(msg); len(r) > maxErrorMessageRune {
		msg = string(r[:maxErrorMessageRune]) + "..."
	}
	return msg
}

// activityReader resets the idle timer on every read and caps total bytes.
type activityReader struct {
	r     io.Reader
	timer *time.Timer
	idle  time.Duration
	limit int64
	n     int64
}

func (a *activityReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if n > 0 {
		a.timer.Reset(a.idle)
		a.n += int64(n)
		if a.n > a.limit {
			return n, &Error{Kind: ErrKindTooLarge, Message: "inference stream exceeds the size limit"}
		}
	}
	return n, err
}

// sseReader yields the data payload of each SSE event with bounded size.
type sseReader struct {
	r        *bufio.Reader
	maxFrame int
}

func (s *sseReader) readLine() ([]byte, error) {
	var line []byte
	for {
		chunk, err := s.r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > s.maxFrame {
			return nil, &Error{Kind: ErrKindTooLarge, Message: "inference stream frame exceeds the size limit"}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF && len(line) > 0 {
				return line, nil
			}
			return nil, err
		}
		return line, nil
	}
}

// next returns the next complete event's data. A trailing event without a
// blank-line terminator is discarded as incomplete (io.EOF).
func (s *sseReader) next() ([]byte, error) {
	var data []byte
	has := false
	for {
		line, err := s.readLine()
		if err != nil {
			return nil, err
		}
		complete := bytes.HasSuffix(line, []byte("\n"))
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			if has {
				return data, nil
			}
			continue
		}
		if !complete {
			return nil, io.EOF
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		if string(field) != "data" {
			continue
		}
		value = bytes.TrimPrefix(value, []byte(" "))
		if has {
			data = append(data, '\n')
		}
		data = append(data, value...)
		has = true
		if len(data) > s.maxFrame {
			return nil, &Error{Kind: ErrKindTooLarge, Message: "inference stream frame exceeds the size limit"}
		}
	}
}

// Forget drops the client's reference to the API key. Go strings are
// immutable, so the backing bytes cannot be zeroed and remain until garbage
// collected; this only ensures a finished client no longer holds or can send
// the key (it reports "not configured"). Call it after the last request.
func (c *Client) Forget() {
	if c != nil {
		c.apiKey = ""
	}
}
