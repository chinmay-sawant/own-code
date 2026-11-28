package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chinmay-sawant/own-code/internal/opencode"
)

const testKey = "sk-secret-key-123"

func target(url, proto string) ModelTarget {
	return ModelTarget{ProviderID: "p", ModelID: "m", Protocol: proto, BaseURL: url, ToolsSupported: true}
}

func newReq(url, proto string) Request {
	return Request{SessionID: "local-1", TurnID: "t1", Model: target(url, proto),
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
		Tools:    []ToolSpec{{Name: "read", Description: "d"}}}
}

func client(t *testing.T, opt Options) *Client {
	t.Helper()
	c, err := NewClient(testKey, opt)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// sse writes chunks with flushes between them so frames can be split anywhere.
func sse(chunks ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			_, _ = io.WriteString(w, c)
			w.(http.Flusher).Flush()
			time.Sleep(time.Millisecond)
		}
	}
}

func collect(c *Client, req Request) (Result, []Event, error) {
	var evs []Event
	res, err := c.Stream(context.Background(), req, func(e Event) { evs = append(evs, e) })
	return res, evs, err
}

func TestResolveModel(t *testing.T) {
	ok := opencode.ConsoleModel{ProviderID: "p", ID: "m", Type: "chat", ToolsKnown: true, ToolsSupported: true,
		APIs: map[string]opencode.ConsoleModelAPI{"responses": {BaseURL: "https://x.example/v1"}}}
	if tg, err := ResolveModel(ok); err != nil || tg.Protocol != ProtocolResponses {
		t.Fatalf("want responses, got %+v %v", tg, err)
	}
	bad := map[string]func(*opencode.ConsoleModel){
		"tools unknown": func(m *opencode.ConsoleModel) { m.ToolsKnown = false },
		"tools off":     func(m *opencode.ConsoleModel) { m.ToolsSupported = false },
		"not chat":      func(m *opencode.ConsoleModel) { m.Type = "image" },
		"empty base":    func(m *opencode.ConsoleModel) { m.APIs = map[string]opencode.ConsoleModelAPI{"responses": {}} },
		"unknown api": func(m *opencode.ConsoleModel) {
			m.APIs = map[string]opencode.ConsoleModelAPI{"generateContent": {BaseURL: "https://x"}}
		},
		"http non-local": func(m *opencode.ConsoleModel) {
			m.APIs = map[string]opencode.ConsoleModelAPI{"responses": {BaseURL: "http://x.example"}}
		},
		"no provider": func(m *opencode.ConsoleModel) { m.ProviderID = "" },
	}
	for name, mut := range bad {
		m := ok
		mut(&m)
		if Eligible(m) {
			t.Errorf("%s: should be ineligible", name)
		}
	}
}

func TestRequestTimeValidation(t *testing.T) {
	c := client(t, Options{})
	req := newReq("https://x.example", "messages")
	if _, err := c.Complete(context.Background(), req); err == nil {
		t.Fatal("unknown protocol must fail closed")
	}
	req = newReq("https://x.example", ProtocolResponses)
	req.Model.ToolsSupported = false
	if _, err := c.Complete(context.Background(), req); err == nil {
		t.Fatal("no tool support must fail")
	}
	req = newReq("", ProtocolResponses)
	if _, err := c.Complete(context.Background(), req); err == nil {
		t.Fatal("missing base_url must fail")
	}
}

func TestChatCompleteAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("x-opencode-session") != "local-1" ||
			r.Header.Get("Authorization") != "Bearer "+testKey {
			t.Errorf("bad request: %s %v", r.URL.Path, r.Header)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != false || body["model"] != "m" {
			t.Errorf("body %v", body)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok","tool_calls":[{"id":"c1","function":{"name":"read","arguments":"{\"p\":1}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`)
	}))
	defer srv.Close()
	res, err := client(t, Options{}).Complete(context.Background(), newReq(srv.URL+"/v1", ProtocolChatCompletions))
	if err != nil || res.Text != "ok" || len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "read" || res.Usage.TotalTokens != 7 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestChatStreamSplitFramesToolsUsage(t *testing.T) {
	srv := httptest.NewServer(sse(
		"data: {\"choices\":[{\"delta\":{\"content\":\"He", "llo\"}}]}\r\n\r\n",
		": keepalive\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"a\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\":1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n",
		"data: [DONE]\n\n"))
	defer srv.Close()
	res, evs, err := collect(client(t, Options{}), newReq(srv.URL, ProtocolChatCompletions))
	if err != nil || res.Status != StatusCompleted || res.Text != "Hello" || res.Usage.TotalTokens != 7 {
		t.Fatalf("%+v %v", res, err)
	}
	if len(res.ToolCalls) != 1 || string(res.ToolCalls[0].Arguments) != `{"a":1}` {
		t.Fatalf("tool calls %+v", res.ToolCalls)
	}
	var tools int
	for _, e := range evs {
		if e.SessionID != "local-1" || e.TurnID != "t1" {
			t.Fatalf("event ids %+v", e)
		}
		if e.Kind == EventToolCall {
			tools++
		}
	}
	if tools != 1 {
		t.Fatalf("tool events %d", tools)
	}
}

func TestChatStreamIncompleteToolCallNotEmitted(t *testing.T) {
	srv := httptest.NewServer(sse(
		"data: {\"choices\":[{\"delta\":{\"content\":\"part\",\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"a\\\":\"}}]}}]}\n\n"))
	defer srv.Close()
	res, evs, err := collect(client(t, Options{}), newReq(srv.URL, ProtocolChatCompletions))
	if err == nil || res.Status != StatusFailed || res.Text != "part" || len(res.ToolCalls) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, e := range evs {
		if e.Kind == EventToolCall {
			t.Fatal("partial tool call emitted")
		}
	}
	// finish_reason seen but arguments truncated: still no tool call.
	srv2 := httptest.NewServer(sse(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"a\"}}]},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"))
	defer srv2.Close()
	res, evs, err = collect(client(t, Options{}), newReq(srv2.URL, ProtocolChatCompletions))
	if err == nil || len(res.ToolCalls) != 0 || len(evs) != 0 {
		t.Fatalf("%+v %v %v", res, err, evs)
	}
}

func TestMalformedEvent(t *testing.T) {
	srv := httptest.NewServer(sse("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n", "data: {not json\n\n"))
	defer srv.Close()
	res, _, err := collect(client(t, Options{}), newReq(srv.URL, ProtocolChatCompletions))
	var e *Error
	if !errors.As(err, &e) || e.Kind != ErrKindMalformed || res.Status != StatusFailed || res.Text != "a" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestResponsesStream(t *testing.T) {
	srv := httptest.NewServer(sse(
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hi\"}\n\n",
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"c1\",\"name\":\"read\"}}\n\n",
		"data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"a\"}\n\n",
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"c1\",\"name\":\"read\",\"arguments\":\"{\\\"a\\\":1}\"}}\n\n",
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}}\n\n"))
	defer srv.Close()
	res, _, err := collect(client(t, Options{}), newReq(srv.URL+"/v1/", ProtocolResponses))
	if err != nil || res.Text != "Hi" || len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "c1" || res.Usage.TotalTokens != 5 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestResponsesStreamFailureAndTruncation(t *testing.T) {
	srv := httptest.NewServer(sse("data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"boom\"}}}\n\n"))
	defer srv.Close()
	if _, _, err := collect(client(t, Options{}), newReq(srv.URL, ProtocolResponses)); err == nil {
		t.Fatal("want failure")
	}
	srv2 := httptest.NewServer(sse("data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n"))
	defer srv2.Close()
	res, _, err := collect(client(t, Options{}), newReq(srv2.URL, ProtocolResponses))
	if err == nil || res.Status != StatusFailed || res.Text != "x" {
		t.Fatalf("%+v %v", res, err)
	}
	srv3 := httptest.NewServer(sse("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"c\",\"name\":\"r\",\"arguments\":\"{\"}}\n\n"))
	defer srv3.Close()
	res, _, err = collect(client(t, Options{}), newReq(srv3.URL, ProtocolResponses))
	if err == nil || len(res.ToolCalls) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestResponsesComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"yo"}]},{"type":"function_call","call_id":"c1","name":"read","arguments":"{}"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()
	res, err := client(t, Options{}).Complete(context.Background(), newReq(srv.URL, ProtocolResponses))
	if err != nil || res.Text != "yo" || len(res.ToolCalls) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestHTTPErrorsNoKeyLeak(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500, 400} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":{"message":"bad key `+testKey+` here"}}`)
		}))
		_, err := client(t, Options{}).Complete(context.Background(), newReq(srv.URL, ProtocolChatCompletions))
		srv.Close()
		var e *Error
		if !errors.As(err, &e) || e.Status != status {
			t.Fatalf("%d: %v", status, err)
		}
		if strings.Contains(err.Error(), testKey) {
			t.Fatalf("%d: key leaked: %v", status, err)
		}
		if status == 403 && (e.Kind != ErrKindForbidden || !strings.Contains(err.Error(), "does not guarantee inference access")) {
			t.Fatalf("403 explanation missing: %v", err)
		}
	}
}

func TestBoundsAndTimeouts(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 2000))
	}))
	defer big.Close()
	_, err := client(t, Options{MaxBodyBytes: 100}).Complete(context.Background(), newReq(big.URL, ProtocolChatCompletions))
	var e *Error
	if !errors.As(err, &e) || e.Kind != ErrKindTooLarge {
		t.Fatalf("body: %v", err)
	}
	frame := httptest.NewServer(sse("data: " + strings.Repeat("y", 5000) + "\n\n"))
	defer frame.Close()
	_, _, err = collect(client(t, Options{MaxFrameBytes: 1000}), newReq(frame.URL, ProtocolChatCompletions))
	if !errors.As(err, &e) || e.Kind != ErrKindTooLarge {
		t.Fatalf("frame: %v", err)
	}
	idle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer idle.Close()
	_, _, err = collect(client(t, Options{IdleTimeout: 50 * time.Millisecond}), newReq(idle.URL, ProtocolChatCompletions))
	if !errors.As(err, &e) || e.Kind != ErrKindTimeout {
		t.Fatalf("idle: %v", err)
	}
}

func TestCancelStopsDeltas(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"one\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"late\"}}]}\n\n")
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	var got []string
	res, err := client(t, Options{}).Stream(ctx, newReq(srv.URL, ProtocolChatCompletions), func(e Event) {
		got = append(got, e.Text)
		cancel()
	})
	if !errors.Is(err, ErrInterrupted) || res.Status != StatusInterrupted || res.Text != "one" {
		t.Fatalf("%+v %v", res, err)
	}
	if len(got) != 1 {
		t.Fatalf("deltas after cancel: %v", got)
	}
	// Already-canceled context sends nothing.
	res, err = client(t, Options{}).Stream(ctx, newReq(srv.URL, ProtocolChatCompletions), func(Event) { t.Error("event") })
	if !errors.Is(err, ErrInterrupted) || res.Status != StatusInterrupted {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestFilterEligible(t *testing.T) {
	ok := opencode.ConsoleModel{ProviderID: "p", ID: "good", Type: "chat", ToolsKnown: true, ToolsSupported: true,
		APIs: map[string]opencode.ConsoleModelAPI{ProtocolChatCompletions: {BaseURL: "https://x.example/v1"}}}
	noTools := ok
	noTools.ID, noTools.ToolsSupported = "notools", false
	badAPI := ok
	badAPI.ID, badAPI.APIs = "badapi", map[string]opencode.ConsoleModelAPI{"anthropic": {BaseURL: "https://x.example"}}
	emb := ok
	emb.ID, emb.Type = "emb", "embedding"
	in := []opencode.ConsoleModel{noTools, ok, badAPI, emb}
	got := FilterEligible(in)
	if len(got) != 1 || got[0].ID != "good" || len(in) != 4 {
		t.Fatalf("FilterEligible = %+v", got)
	}
	if got := FilterEligible(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil input = %#v", got)
	}
}
