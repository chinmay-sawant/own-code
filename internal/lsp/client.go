package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrClosed is returned for calls on a closed or exited client.
var ErrClosed = errors.New("lsp: client closed")

// Handler receives server-initiated notifications. It runs on the read loop
// goroutine and must not block.
type Handler func(method string, params json.RawMessage)

// Client is a JSON-RPC client bound to one language server.
type Client struct {
	conn    *Conn
	closer  io.Closer // closes our write side
	cmd     *exec.Cmd
	handler Handler
	logf    func(format string, args ...any)
	initOpt any

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan *Message
	closed  bool
	done    chan struct{}
	readErr error

	Result InitializeResult
}

// Options configures a Client.
type Options struct {
	Handler Handler
	// Log receives protocol traces and server stderr lines. May be nil.
	Log func(format string, args ...any)
	// InitOptions is sent as initializationOptions. May be nil.
	InitOptions any
}

// NewClient builds a client over an existing stream pair (used by tests and by Start).
func NewClient(r io.Reader, w io.WriteCloser, opt Options) *Client {
	c := &Client{
		conn:    NewConn(r, w),
		closer:  w,
		handler: opt.Handler,
		logf:    opt.Log,
		initOpt: opt.InitOptions,
		pending: map[int64]chan *Message{},
		done:    make(chan struct{}),
	}
	if c.logf == nil {
		c.logf = func(string, ...any) {}
	}
	go c.readLoop()
	return c
}

// Start launches the server process described by cfg and returns a client
// (not yet initialized).
func Start(cfg ServerConfig, dir string, opt Options) (*Client, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Dir = dir
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("lsp: start %s: %w", cfg.Command, err)
	}
	c := NewClient(out, in, opt)
	c.cmd = cmd
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := errPipe.Read(buf)
			if n > 0 {
				c.logf("[%s stderr] %s", cfg.Name, strings.TrimRight(string(buf[:n]), "\n"))
			}
			if err != nil {
				return
			}
		}
	}()
	return c, nil
}

func (c *Client) readLoop() {
	defer close(c.done)
	for {
		m, err := c.conn.Read()
		if err != nil {
			c.mu.Lock()
			c.readErr = err
			c.closed = true
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		switch {
		case m.IsResponse():
			id, err := strconv.ParseInt(string(*m.ID), 10, 64)
			if err != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case m.IsRequest():
			c.handleServerRequest(m)
		case m.IsNotification():
			c.logf("<- %s", m.Method)
			if c.handler != nil {
				c.handler(m.Method, m.Params)
			}
		}
	}
}

// handleServerRequest answers server->client requests minimally.
func (c *Client) handleServerRequest(m *Message) {
	resp := &Message{ID: m.ID}
	switch m.Method {
	case "workspace/configuration":
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(m.Params, &p)
		nulls := make([]any, len(p.Items))
		resp.Result, _ = json.Marshal(nulls)
	case "client/registerCapability", "client/unregisterCapability",
		"window/workDoneProgress/create", "window/showMessageRequest":
		resp.Result = json.RawMessage("null")
	default:
		resp.Error = &ResponseError{Code: -32601, Message: "method not found: " + m.Method}
	}
	_ = c.conn.Write(resp)
}

// Call sends a request and waits for its result, ctx cancellation or close.
// result may be nil to discard the response.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ch := make(chan *Message, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()

	idRaw := json.RawMessage(strconv.FormatInt(id, 10))
	c.logf("-> %s #%d", method, id)
	if err := c.conn.Write(&Message{ID: &idRaw, Method: method, Params: raw}); err != nil {
		c.drop(id)
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if m.Error != nil {
			return m.Error
		}
		if result != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	case <-ctx.Done():
		c.drop(id)
		idRaw := json.RawMessage(strconv.FormatInt(id, 10))
		_ = c.conn.Write(&Message{Method: "$/cancelRequest", Params: json.RawMessage(`{"id":` + string(idRaw) + `}`)})
		return ctx.Err()
	}
}

func (c *Client) drop(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// Notify sends a notification.
func (c *Client) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	c.logf("-> %s", method)
	return c.conn.Write(&Message{Method: method, Params: raw})
}

// Initialize performs the initialize/initialized handshake.
func (c *Client) Initialize(ctx context.Context, rootDir string) error {
	root := URIFromPath(rootDir)
	p := InitializeParams{
		ProcessID:        os.Getpid(),
		ClientInfo:       &Info{Name: "own-code"},
		RootURI:          root,
		WorkspaceFolders: []WorkspaceFolder{{URI: root, Name: filepath.Base(rootDir)}},
		InitOptions:      c.initOpt,
		Capabilities: ClientCapabilities{
			TextDocument: map[string]any{
				"synchronization":    map[string]any{"didSave": true},
				"publishDiagnostics": map[string]any{},
				"hover":              map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
				"definition":         map[string]any{"linkSupport": true},
				"declaration":        map[string]any{"linkSupport": true},
				"typeDefinition":     map[string]any{"linkSupport": true},
				"references":         map[string]any{},
				"rename":             map[string]any{"prepareSupport": true},
				"signatureHelp": map[string]any{
					"signatureInformation": map[string]any{"documentationFormat": []string{"plaintext"}, "parameterInformation": map[string]any{"labelOffsetSupport": true}},
				},
				"codeAction": map[string]any{
					"codeActionLiteralSupport": map[string]any{"codeActionKind": map[string]any{"valueSet": []string{"quickfix", "refactor", "source"}}},
					"resolveSupport":           map[string]any{"properties": []string{"edit"}},
				},
				"documentSymbol": map[string]any{"hierarchicalDocumentSymbolSupport": true},
				"completion": map[string]any{
					"completionItem": map[string]any{
						"snippetSupport":      true,
						"documentationFormat": []string{"plaintext"},
					},
				},
			},
			Window: map[string]any{"workDoneProgress": true},
			Workspace: map[string]any{
				"workspaceEdit": map[string]any{"documentChanges": true},
				"symbol":        map[string]any{},
			},
			General: map[string]any{"positionEncodings": []string{"utf-16"}},
		},
	}
	if err := c.Call(ctx, "initialize", p, &c.Result); err != nil {
		return err
	}
	return c.Notify("initialized", struct{}{})
}

// Shutdown performs shutdown/exit and releases the process. It is bounded by
// ctx and always closes the transport.
func (c *Client) Shutdown(ctx context.Context) error {
	err := c.Call(ctx, "shutdown", nil, nil)
	if err == nil {
		_ = c.Notify("exit", nil)
	}
	c.Close()
	return err
}

// Close tears down the transport and kills the process if still running.
func (c *Client) Close() {
	_ = c.closer.Close()
	if c.cmd != nil {
		exited := make(chan struct{})
		go func() { _ = c.cmd.Wait(); close(exited) }()
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			_ = c.cmd.Process.Kill()
			<-exited
		}
	}
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
}

// Done is closed when the read loop ends (server exit or Close).
func (c *Client) Done() <-chan struct{} { return c.done }

// URIFromPath converts an absolute filesystem path to a file:// URI.
func URIFromPath(p string) string {
	p = filepath.ToSlash(p)
	if runtime.GOOS == "windows" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// PathFromURI converts a file:// URI back to a filesystem path.
func PathFromURI(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("lsp: not a file URI: %q", uri)
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		p = strings.TrimPrefix(p, "/")
	}
	return filepath.FromSlash(p), nil
}
