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

	"github.com/chinmay-sawant/own-code/internal/opencode"
)

func gemEv(data string) string { return "data: " + data + "\r\n\r\n" }

func TestGeminiResolveAndEndpoint(t *testing.T) {
	m := opencode.ConsoleModel{ProviderID: "google", ID: "gem-x", Type: "chat", ToolsKnown: true, ToolsSupported: true,
		APIs: map[string]opencode.ConsoleModelAPI{"generate-content": {BaseURL: "https://opencode.ai/inference/google/v1beta/"}}}
	tg, err := ResolveModel(m)
	if err != nil || tg.Protocol != ProtocolGenerateContent {
		t.Fatalf("%+v %v", tg, err)
	}
	if u, _ := tg.endpoint(); u != "https://opencode.ai/inference/google/v1beta/models/gem-x:generateContent" {
		t.Fatal(u)
	}
	if u, _ := tg.endpointFor(true); u != "https://opencode.ai/inference/google/v1beta/models/gem-x:streamGenerateContent?alt=sse" {
		t.Fatal(u)
	}
	tg.BaseURL = "https://opencode.ai/inference/google/v1beta/models/gem-x:generateContent"
	if u, _ := tg.endpointFor(true); strings.Count(u, "/models/") != 1 || !strings.Contains(u, "gem-x:streamGenerateContent") {
		t.Fatal(u)
	}
	tg.ModelID, tg.BaseURL = "a/b", "https://x.example/v1beta"
	if tg.Validate() == nil {
		t.Fatal("model ID with slash must be rejected")
	}
	m.APIs = map[string]opencode.ConsoleModelAPI{"generate-content": {BaseURL: "http://x.example"}}
	if Eligible(m) {
		t.Fatal("non-loopback http must be ineligible")
	}
}

func TestGeminiCompleteHeadersAndHistory(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/m:generateContent" || r.Header.Get(sessionHeader) != "local-1" ||
			r.Header.Get("Authorization") != "Bearer "+testKey {
			t.Errorf("bad request %s %v", r.URL.Path, r.Header)
		}
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"yo"},{"functionCall":{"name":"read","args":{"p":1}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4,"totalTokenCount":7}}`)
	}))
	defer srv.Close()
	req := newReq(srv.URL, ProtocolGenerateContent)
	req.Messages = []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "go"},
		{Role: RoleAssistant, Content: "ok", ToolCalls: []ToolCall{{ID: "a", Name: "read", Arguments: json.RawMessage(`{"x":1}`)}, {ID: "b", Name: "list"}}},
		{Role: RoleTool, ToolCallID: "a", Content: "r1"},
		{Role: RoleTool, ToolCallID: "b", Content: "r2"},
	}
	res, err := client(t, Options{}).Complete(context.Background(), req)
	if err != nil || res.Text != "yo" || len(res.ToolCalls) != 1 || !strings.HasPrefix(res.ToolCalls[0].ID, "gemini_call_") ||
		string(res.ToolCalls[0].Arguments) != `{"p":1}` || res.Usage.TotalTokens != 7 || res.FinishReason != "STOP" {
		t.Fatalf("%+v %v", res, err)
	}
	res2, _ := client(t, Options{}).Complete(context.Background(), req)
	if res2.ToolCalls[0].ID != res.ToolCalls[0].ID {
		t.Fatal("local call IDs must be stable")
	}
	if got["systemInstruction"] == nil || got["tools"] == nil {
		t.Fatalf("%v", got)
	}
	cs := got["contents"].([]any)
	if len(cs) != 3 {
		t.Fatalf("want user/model/user(merged responses), got %v", cs)
	}
	last := cs[2].(map[string]any)
	parts := last["parts"].([]any)
	fr := parts[1].(map[string]any)["functionResponse"].(map[string]any)
	if last["role"] != "user" || len(parts) != 2 || fr["name"] != "list" {
		t.Fatalf("%v", last)
	}
	model := cs[1].(map[string]any)
	if model["role"] != "model" || len(model["parts"].([]any)) != 3 {
		t.Fatalf("%v", model)
	}
	// A tool result without a matching call is rejected before sending.
	req.Messages = []Message{{Role: RoleUser, Content: "x"}, {Role: RoleTool, ToolCallID: "zz", Content: "r"}}
	if _, err := client(t, Options{}).Complete(context.Background(), req); err == nil {
		t.Fatal("orphan tool result accepted")
	}
}

func TestGeminiStreamSplitFramesToolsUsage(t *testing.T) {
	full := ": comment\n\n" +
		gemEv(`{"candidates":[{"content":{"role":"model","parts":[{"text":"Hel"}]}}]}`) +
		gemEv(`{"candidates":[{"content":{"parts":[{"text":"lo"},{"text":"hidden","thought":true}]}}]}`) +
		gemEv(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read","args":{"path":"a"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"thoughtsTokenCount":4,"totalTokenCount":19}}`)
	var chunks []string
	for i := 0; i < len(full); i += 7 {
		chunks = append(chunks, full[i:min(i+7, len(full))])
	}
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		sse(chunks...)(w, r)
	}))
	defer srv.Close()
	res, err, evs := collect(client(t, Options{}), newReq(srv.URL, ProtocolGenerateContent))
	if err != nil || res.Status != StatusCompleted || res.Text != "Hello" || len(res.ToolCalls) != 1 ||
		string(res.ToolCalls[0].Arguments) != `{"path":"a"}` ||
		res.Usage == nil || res.Usage.InputTokens != 10 || res.Usage.OutputTokens != 9 || res.Usage.TotalTokens != 19 {
		t.Fatalf("%+v %v", res, err)
	}
	if gotPath != "/models/m:streamGenerateContent" || gotQuery != "alt=sse" {
		t.Fatal(gotPath, gotQuery)
	}
	calls := 0
	for _, e := range evs {
		if e.Kind == EventToolCall {
			calls++
		}
		if e.SessionID != "local-1" {
			t.Fatalf("event missing session: %+v", e)
		}
	}
	if calls != 1 {
		t.Fatalf("tool call events = %d", calls)
	}
}

func TestGeminiStreamIncompleteAndMalformed(t *testing.T) {
	var e *Error
	// Truncated: no finishReason, so tool calls are withheld.
	srv := httptest.NewServer(sse(gemEv(`{"candidates":[{"content":{"parts":[{"text":"x"},{"functionCall":{"name":"r","args":{}}}]}}]}`)))
	defer srv.Close()
	res, err, evs := collect(client(t, Options{}), newReq(srv.URL, ProtocolGenerateContent))
	if err == nil || res.Status != StatusFailed || res.Text != "x" || len(res.ToolCalls) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, ev := range evs {
		if ev.Kind == EventToolCall {
			t.Fatal("incomplete tool call emitted")
		}
	}
	// Malformed event after text.
	srv2 := httptest.NewServer(sse(gemEv(`{"candidates":[{"content":{"parts":[{"text":"a"}]}}]}`), "data: {not json\n\n"))
	defer srv2.Close()
	res, err, _ = collect(client(t, Options{}), newReq(srv2.URL, ProtocolGenerateContent))
	if !errors.As(err, &e) || e.Kind != ErrKindMalformed || res.Text != "a" {
		t.Fatalf("%+v %v", res, err)
	}
	// Unterminated trailing frame is discarded as incomplete.
	srv3 := httptest.NewServer(sse(`data: {"candidates":[{"content":{"parts":[{"text":"b"}]},"finishReason":"STOP"}]}`))
	defer srv3.Close()
	res, err, _ = collect(client(t, Options{}), newReq(srv3.URL, ProtocolGenerateContent))
	if err == nil || res.Status != StatusFailed {
		t.Fatalf("%+v %v", res, err)
	}
	// In-stream error object.
	srv4 := httptest.NewServer(sse(gemEv(`{"error":{"code":503,"message":"busy"}}`)))
	defer srv4.Close()
	_, err, _ = collect(client(t, Options{}), newReq(srv4.URL, ProtocolGenerateContent))
	if !errors.As(err, &e) || e.Kind != ErrKindServer || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("%v", err)
	}
	// Blocked prompt.
	srv5 := httptest.NewServer(sse(gemEv(`{"promptFeedback":{"blockReason":"SAFETY"}}`)))
	defer srv5.Close()
	_, err, _ = collect(client(t, Options{}), newReq(srv5.URL, ProtocolGenerateContent))
	if err == nil || !strings.Contains(err.Error(), "SAFETY") {
		t.Fatalf("%v", err)
	}
	// Non-stream malformed body.
	srv6 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "{nope") }))
	defer srv6.Close()
	_, err = client(t, Options{}).Complete(context.Background(), newReq(srv6.URL, ProtocolGenerateContent))
	if !errors.As(err, &e) || e.Kind != ErrKindMalformed {
		t.Fatalf("%v", err)
	}
}

func TestGeminiCancelBoundsAndKeyRedaction(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, gemEv(`{"candidates":[{"content":{"parts":[{"text":"one"}]}}]}`))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, gemEv(`{"candidates":[{"content":{"parts":[{"text":"late"}]}}]}`))
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	res, err := client(t, Options{}).Stream(ctx, newReq(srv.URL, ProtocolGenerateContent), func(Event) { n++; cancel() })
	if !errors.Is(err, ErrInterrupted) || res.Status != StatusInterrupted || res.Text != "one" || n != 1 {
		t.Fatalf("%+v %v n=%d", res, err, n)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"code":400,"message":"bad `+testKey+`"}}`)
	}))
	defer bad.Close()
	_, err = client(t, Options{}).Complete(context.Background(), newReq(bad.URL, ProtocolGenerateContent))
	if err == nil || strings.Contains(err.Error(), testKey) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("%v", err)
	}
	var e *Error
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, strings.Repeat("x", 2000)) }))
	defer big.Close()
	_, err = client(t, Options{MaxBodyBytes: 100}).Complete(context.Background(), newReq(big.URL, ProtocolGenerateContent))
	if !errors.As(err, &e) || e.Kind != ErrKindTooLarge {
		t.Fatalf("body: %v", err)
	}
	frame := httptest.NewServer(sse("data: " + strings.Repeat("y", 5000) + "\n\n"))
	defer frame.Close()
	_, err, _ = collect(client(t, Options{MaxFrameBytes: 1000}), newReq(frame.URL, ProtocolGenerateContent))
	if !errors.As(err, &e) || e.Kind != ErrKindTooLarge {
		t.Fatalf("frame: %v", err)
	}
}
