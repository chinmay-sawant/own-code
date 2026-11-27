package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer is an in-process LSP server on io.Pipe.
type fakeServer struct {
	conn *Conn
	mu   sync.Mutex
	got  []string // methods received
}

func (s *fakeServer) methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

func newPair(t *testing.T, opt Options) (*Client, *fakeServer) {
	t.Helper()
	cr, sw := io.Pipe() // server -> client
	sr, cw := io.Pipe() // client -> server
	srv := &fakeServer{conn: NewConn(sr, sw)}
	go func() {
		for {
			m, err := srv.conn.Read()
			if err != nil {
				_ = sw.Close()
				return
			}
			srv.mu.Lock()
			srv.got = append(srv.got, m.Method)
			srv.mu.Unlock()
			switch m.Method {
			case "initialize":
				if err := srv.conn.Write(&Message{ID: m.ID, Result: json.RawMessage(`{"capabilities":{"textDocumentSync":1},"serverInfo":{"name":"fake"}}`)}); err != nil {
					t.Errorf("write initialize response: %v", err)
					return
				}
				// Also push a diagnostic and a server request.
				if err := srv.conn.Write(&Message{Method: "textDocument/publishDiagnostics", Params: json.RawMessage(`{"uri":"file:///a.go","diagnostics":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"message":"boom"}]}`)}); err != nil {
					t.Errorf("write diagnostics notification: %v", err)
					return
				}
			case "slow":
				// never answers
			case "echo":
				if err := srv.conn.Write(&Message{ID: m.ID, Result: m.Params}); err != nil {
					t.Errorf("write echo response: %v", err)
					return
				}
			case "fail":
				if err := srv.conn.Write(&Message{ID: m.ID, Error: &ResponseError{Code: -32000, Message: "nope"}}); err != nil {
					t.Errorf("write error response: %v", err)
					return
				}
			case "shutdown":
				if err := srv.conn.Write(&Message{ID: m.ID, Result: json.RawMessage("null")}); err != nil {
					t.Errorf("write shutdown response: %v", err)
					return
				}
			case "exit":
				_ = sw.Close()
				return
			}
		}
	}()
	c := NewClient(cr, cw, opt)
	t.Cleanup(c.Close)
	return c, srv
}

func TestLifecycle(t *testing.T) {
	diag := make(chan PublishDiagnosticsParams, 1)
	c, srv := newPair(t, Options{Handler: func(method string, p json.RawMessage) {
		if method == "textDocument/publishDiagnostics" {
			var d PublishDiagnosticsParams
			if err := json.Unmarshal(p, &d); err != nil {
				t.Errorf("decode diagnostic notification: %v", err)
				return
			}
			diag <- d
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Initialize(ctx, "/tmp/proj"); err != nil {
		t.Fatal(err)
	}
	if c.Result.ServerInfo == nil || c.Result.ServerInfo.Name != "fake" {
		t.Fatalf("server info %+v", c.Result)
	}
	select {
	case d := <-diag:
		if d.Diagnostics[0].Message != "boom" {
			t.Fatal(d)
		}
	case <-ctx.Done():
		t.Fatal("no diagnostics")
	}
	var out map[string]int
	if err := c.Call(ctx, "echo", map[string]int{"x": 7}, &out); err != nil || out["x"] != 7 {
		t.Fatalf("echo %v %v", out, err)
	}
	err := c.Call(ctx, "fail", nil, nil)
	if re, ok := err.(*ResponseError); !ok || re.Code != -32000 {
		t.Fatalf("fail err %v", err)
	}
	if err := c.Notify("textDocument/didOpen", DidOpenParams{TextDocument: TextDocumentItem{URI: "file:///a.go", LanguageID: "go", Version: 1, Text: "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	m := strings.Join(srv.methods(), ",")
	for _, want := range []string{"initialize", "initialized", "textDocument/didOpen", "shutdown", "exit"} {
		if !strings.Contains(m, want) {
			t.Errorf("missing %s in %s", want, m)
		}
	}
	if err := c.Call(ctx, "echo", nil, nil); err != ErrClosed {
		t.Fatalf("after close: %v", err)
	}
}

func TestCallCancelAndServerExit(t *testing.T) {
	c, _ := newPair(t, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "slow", nil, nil); err != context.DeadlineExceeded {
		t.Fatalf("got %v", err)
	}
	// Server death unblocks pending calls.
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "slow", nil, nil) }()
	time.Sleep(50 * time.Millisecond)
	if err := c.Notify("exit", nil); err != nil {
		t.Fatalf("notify exit: %v", err)
	}
	select {
	case err := <-errc:
		if err != ErrClosed {
			t.Fatalf("got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pending call not released")
	}
}

func TestFramingErrors(t *testing.T) {
	cases := map[string]string{
		"missing length": "X: 1\r\n\r\n{}",
		"bad length":     "Content-Length: abc\r\n\r\n",
		"truncated":      "Content-Length: 10\r\n\r\n{}",
		"bad json":       "Content-Length: 3\r\n\r\nxyz",
		"huge":           "Content-Length: 999999999999\r\n\r\n",
	}
	for name, in := range cases {
		if _, err := NewConn(strings.NewReader(in), io.Discard).Read(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := NewConn(strings.NewReader(""), io.Discard).Read(); err != io.EOF {
		t.Errorf("empty: %v", err)
	}
}

func TestFramingRoundTripMultibyteAndSplit(t *testing.T) {
	var buf bytes.Buffer
	w := NewConn(strings.NewReader(""), &buf)
	id := json.RawMessage("1")
	if err := w.Write(&Message{ID: &id, Method: "m", Params: json.RawMessage(`{"s":"😀é"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(&Message{Method: "n"}); err != nil {
		t.Fatal(err)
	}
	// Feed one byte at a time.
	r := NewConn(iotestOneByte{bytes.NewReader(buf.Bytes())}, io.Discard)
	m1, err := r.Read()
	if err != nil || !m1.IsRequest() || !strings.Contains(string(m1.Params), "😀") {
		t.Fatalf("%v %+v", err, m1)
	}
	m2, err := r.Read()
	if err != nil || !m2.IsNotification() {
		t.Fatalf("%v %+v", err, m2)
	}
}

type iotestOneByte struct{ r io.Reader }

func (o iotestOneByte) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

func TestURIRoundTrip(t *testing.T) {
	p := "/tmp/my proj/ü.go"
	u := URIFromPath(p)
	got, err := PathFromURI(u)
	if err != nil || got != p {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := PathFromURI("http://x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	for path, name := range map[string]string{
		"a.go": "gopls", "a.TSX": "typescript-language-server", "a.py": "pyright",
		"a.rs": "rust-analyzer", "a.cpp": "clangd", "a.h": "clangd",
	} {
		s, ok := r.ForPath(path)
		if !ok || s.Name != name {
			t.Errorf("%s -> %v %v", path, s.Name, ok)
		}
	}
	if _, ok := r.ForPath("README"); ok {
		t.Error("no ext matched")
	}
	s, _ := r.ForPath("x.tsx")
	if s.LanguageID("x.tsx") != "typescriptreact" {
		t.Error(s.LanguageID("x.tsx"))
	}
	g, _ := r.ForPath("x.go")
	g.Disabled = true
	r.Set(g)
	if _, ok := r.ForPath("x.go"); ok {
		t.Error("disabled still matches")
	}
	r.Set(ServerConfig{Name: "zls", Command: "zls", Extensions: []string{"ZIG"}, LanguageIDs: []string{"zig"}})
	if s, ok := r.ForPath("a.zig"); !ok || s.LanguageID("a.zig") != "zig" {
		t.Error("custom server")
	}
}

func TestFindRoot(t *testing.T) {
	exists := func(p string) bool { return p == "/w/proj/go.mod" }
	if got := FindRoot("/w/proj/a/b", []string{"go.mod"}, "/fb", exists); got != "/w/proj" {
		t.Fatal(got)
	}
	if got := FindRoot("/other", []string{"go.mod"}, "/fb", exists); got != "/fb" {
		t.Fatal(got)
	}
}
