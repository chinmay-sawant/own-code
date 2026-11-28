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

func anthEv(typ, data string) string { return "event: " + typ + "\ndata: " + data + "\n\n" }

func TestAnthropicResolveAndEndpoint(t *testing.T) {
	m := opencode.ConsoleModel{ProviderID: "anthropic", ID: "c", Type: "chat", ToolsKnown: true, ToolsSupported: true,
		APIs: map[string]opencode.ConsoleModelAPI{"messages": {BaseURL: "https://opencode.ai/inference/anthropic/v1/"}}}
	tg, err := ResolveModel(m)
	if err != nil || tg.Protocol != ProtocolMessages {
		t.Fatalf("%+v %v", tg, err)
	}
	if u, _ := tg.endpoint(); u != "https://opencode.ai/inference/anthropic/v1/messages" {
		t.Fatal(u)
	}
	tg.BaseURL = "https://opencode.ai/inference/anthropic/v1/messages"
	if u, _ := tg.endpoint(); !strings.HasSuffix(u, "/v1/messages") || strings.HasSuffix(u, "messages/messages") {
		t.Fatal(u)
	}
	// chat-completions still preferred when both offered.
	m.APIs[ProtocolChatCompletions] = opencode.ConsoleModelAPI{BaseURL: "https://x.example/v1"}
	if tg, _ := ResolveModel(m); tg.Protocol != ProtocolChatCompletions {
		t.Fatal(tg.Protocol)
	}
	m.APIs = map[string]opencode.ConsoleModelAPI{"messages": {BaseURL: "http://x.example"}}
	if Eligible(m) {
		t.Fatal("non-loopback http must be ineligible")
	}
}

func TestAnthropicCompleteHeadersAndHistory(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" || r.Header.Get(sessionHeader) != "local-1" ||
			r.Header.Get("Authorization") != "Bearer "+testKey || r.Header.Get("anthropic-version") == "" {
			t.Errorf("bad request %s %v", r.URL.Path, r.Header)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"yo"},{"type":"tool_use","id":"tu1","name":"read","input":{"p":1}}],"stop_reason":"tool_use","usage":{"input_tokens":3,"output_tokens":4}}`)
	}))
	defer srv.Close()
	req := newReq(srv.URL, ProtocolMessages)
	req.Messages = []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "go"},
		{Role: RoleAssistant, Content: "ok", ToolCalls: []ToolCall{{ID: "a", Name: "read", Arguments: json.RawMessage(`{"x":1}`)}, {ID: "b", Name: "read", Arguments: json.RawMessage(`{}`)}}},
		{Role: RoleTool, ToolCallID: "a", Content: "r1"},
		{Role: RoleTool, ToolCallID: "b", Content: "r2"},
	}
	res, err := client(t, Options{}).Complete(context.Background(), req)
	if err != nil || res.Text != "yo" || len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "tu1" ||
		string(res.ToolCalls[0].Arguments) != `{"p":1}` || res.Usage.TotalTokens != 7 {
		t.Fatalf("%+v %v", res, err)
	}
	if got["system"] != "sys" || got["max_tokens"] == nil {
		t.Fatalf("%v", got)
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("want user/assistant/user(merged results), got %d: %v", len(msgs), msgs)
	}
	last := msgs[2].(map[string]any)
	blocks := last["content"].([]any)
	if last["role"] != "user" || len(blocks) != 2 || blocks[0].(map[string]any)["type"] != "tool_result" ||
		blocks[1].(map[string]any)["tool_use_id"] != "b" {
		t.Fatalf("%v", last)
	}
	asst := msgs[1].(map[string]any)["content"].([]any)
	if len(asst) != 3 || asst[1].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("%v", asst)
	}
	tools := got["tools"].([]any)[0].(map[string]any)
	if tools["input_schema"] == nil {
		t.Fatalf("%v", tools)
	}
}

func TestAnthropicStreamSplitFramesToolsUsage(t *testing.T) {
	full := anthEv("message_start", `{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`) +
		": ping comment\n\n" + anthEv("ping", `{"type":"ping"}`) +
		anthEv("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		anthEv("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`) +
		anthEv("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`) +
		anthEv("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		anthEv("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu1","name":"read","input":{}}}`) +
		anthEv("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"pa"}}`) +
		anthEv("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"th\":\"a\"}"}}`) +
		anthEv("content_block_stop", `{"type":"content_block_stop","index":1}`) +
		anthEv("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`) +
		anthEv("message_stop", `{"type":"message_stop"}`)
	var chunks []string
	for i := 0; i < len(full); i += 7 {
		end := i + 7
		if end > len(full) {
			end = len(full)
		}
		chunks = append(chunks, full[i:end])
	}
	srv := httptest.NewServer(sse(chunks...))
	defer srv.Close()
	res, evs, err := collect(client(t, Options{}), newReq(srv.URL, ProtocolMessages))
	if err != nil || res.Status != StatusCompleted || res.Text != "Hello" || res.FinishReason != "tool_use" ||
		len(res.ToolCalls) != 1 || string(res.ToolCalls[0].Arguments) != `{"path":"a"}` ||
		res.Usage == nil || res.Usage.InputTokens != 10 || res.Usage.OutputTokens != 9 || res.Usage.TotalTokens != 19 {
		t.Fatalf("%+v %v", res, err)
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

func TestAnthropicStreamIncompleteAndMalformed(t *testing.T) {
	var e *Error
	// Tool block never stopped: no tool call reported, failure.
	srv := httptest.NewServer(sse(
		anthEv("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t","name":"r"}}`),
		anthEv("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a"}}`),
		anthEv("message_stop", `{"type":"message_stop"}`)))
	defer srv.Close()
	res, evs, err := collect(client(t, Options{}), newReq(srv.URL, ProtocolMessages))
	if err == nil || len(res.ToolCalls) != 0 || res.Status != StatusFailed {
		t.Fatalf("%+v %v", res, err)
	}
	for _, ev := range evs {
		if ev.Kind == EventToolCall {
			t.Fatal("incomplete tool call emitted")
		}
	}
	// Invalid JSON arguments at block stop.
	srv2 := httptest.NewServer(sse(
		anthEv("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t","name":"r"}}`),
		anthEv("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a"}}`),
		anthEv("content_block_stop", `{"type":"content_block_stop","index":0}`)))
	defer srv2.Close()
	res, _, err = collect(client(t, Options{}), newReq(srv2.URL, ProtocolMessages))
	if err == nil || len(res.ToolCalls) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	// Malformed event after text.
	srv3 := httptest.NewServer(sse(
		anthEv("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a"}}`),
		"data: {not json\n\n"))
	defer srv3.Close()
	res, _, err = collect(client(t, Options{}), newReq(srv3.URL, ProtocolMessages))
	if !errors.As(err, &e) || e.Kind != ErrKindMalformed || res.Text != "a" {
		t.Fatalf("%+v %v", res, err)
	}
	// Truncated before message_stop.
	srv4 := httptest.NewServer(sse(anthEv("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`)))
	defer srv4.Close()
	res, _, err = collect(client(t, Options{}), newReq(srv4.URL, ProtocolMessages))
	if err == nil || res.Status != StatusFailed || res.Text != "x" {
		t.Fatalf("%+v %v", res, err)
	}
	// In-stream error event.
	srv5 := httptest.NewServer(sse(anthEv("error", `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`)))
	defer srv5.Close()
	_, _, err = collect(client(t, Options{}), newReq(srv5.URL, ProtocolMessages))
	if !errors.As(err, &e) || e.Kind != ErrKindServer || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("%v", err)
	}
}

func TestAnthropicCancelAndKeyRedaction(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, anthEv("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"one"}}`))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, anthEv("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"late"}}`))
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	res, err := client(t, Options{}).Stream(ctx, newReq(srv.URL, ProtocolMessages), func(Event) { n++; cancel() })
	if !errors.Is(err, ErrInterrupted) || res.Status != StatusInterrupted || res.Text != "one" || n != 1 {
		t.Fatalf("%+v %v n=%d", res, err, n)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad `+testKey+`"}}`)
	}))
	defer bad.Close()
	_, err = client(t, Options{}).Complete(context.Background(), newReq(bad.URL, ProtocolMessages))
	if err == nil || strings.Contains(err.Error(), testKey) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("%v", err)
	}
}

func TestAnthropicBounds(t *testing.T) {
	var e *Error
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 2000))
	}))
	defer big.Close()
	_, err := client(t, Options{MaxBodyBytes: 100}).Complete(context.Background(), newReq(big.URL, ProtocolMessages))
	if !errors.As(err, &e) || e.Kind != ErrKindTooLarge {
		t.Fatalf("body: %v", err)
	}
	frame := httptest.NewServer(sse("data: " + strings.Repeat("y", 5000) + "\n\n"))
	defer frame.Close()
	_, _, err = collect(client(t, Options{MaxFrameBytes: 1000}), newReq(frame.URL, ProtocolMessages))
	if !errors.As(err, &e) || e.Kind != ErrKindTooLarge {
		t.Fatalf("frame: %v", err)
	}
}

func TestResponsesStreamMalformedFrames(t *testing.T) {
	var e *Error
	// Malformed JSON after a valid delta keeps partial text.
	srv := httptest.NewServer(sse(
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"a\"}\n\n",
		"data: {not json\n\n"))
	defer srv.Close()
	res, _, err := collect(client(t, Options{}), newReq(srv.URL, ProtocolResponses))
	if !errors.As(err, &e) || e.Kind != ErrKindMalformed || res.Status != StatusFailed || res.Text != "a" {
		t.Fatalf("%+v %v", res, err)
	}
	// Unterminated trailing frame (no blank line) is discarded as incomplete.
	srv2 := httptest.NewServer(sse(
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"b\"}\n\n",
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}"))
	defer srv2.Close()
	res, _, err = collect(client(t, Options{}), newReq(srv2.URL, ProtocolResponses))
	if err == nil || res.Status != StatusFailed || res.Text != "b" {
		t.Fatalf("%+v %v", res, err)
	}
	// Oversized frame.
	srv3 := httptest.NewServer(sse("data: " + strings.Repeat("z", 5000) + "\n\n"))
	defer srv3.Close()
	_, _, err = collect(client(t, Options{MaxFrameBytes: 1000}), newReq(srv3.URL, ProtocolResponses))
	if !errors.As(err, &e) || e.Kind != ErrKindTooLarge {
		t.Fatalf("%v", err)
	}
	// Frame split mid-JSON still parses; unknown event types and comments are ignored.
	whole := ": hi\n\n" + "data: {\"type\":\"response.future_event\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
	var parts []string
	for i := 0; i < len(whole); i += 5 {
		end := min(i+5, len(whole))
		parts = append(parts, whole[i:end])
	}
	srv4 := httptest.NewServer(sse(parts...))
	defer srv4.Close()
	res, _, err = collect(client(t, Options{}), newReq(srv4.URL, ProtocolResponses))
	if err != nil || res.Text != "ok" {
		t.Fatalf("%+v %v", res, err)
	}
	// Complete() with malformed JSON body.
	srv5 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "{nope") }))
	defer srv5.Close()
	_, err = client(t, Options{}).Complete(context.Background(), newReq(srv5.URL, ProtocolResponses))
	if !errors.As(err, &e) || e.Kind != ErrKindMalformed {
		t.Fatalf("%v", err)
	}
}
