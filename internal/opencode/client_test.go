package opencode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetInfoUsesV2BasicAuthAndBasePath(t *testing.T) {
	const password = "sensitive-server-password"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxy/api/info" {
			t.Errorf("unexpected request path: %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Basic "+base64.StdEncoding.EncodeToString([]byte("opencode:"+password)) {
			t.Errorf("unexpected Authorization header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"version":"2.0.0","pid":42,"urls":[],"paths":{"tmp":"/tmp"}}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/proxy/", password)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	info, err := client.GetInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "2.0.0" || info.PID != 42 {
		t.Fatalf("unexpected server info: %#v", info)
	}
	if strings.Contains(fmt.Sprintf("%#v", client), password) || strings.Contains(client.String(), password) {
		t.Fatal("client formatting exposed the server password")
	}
}

func TestListIntegrationsPreservesAdvertisedMethodForm(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/integration" {
			t.Errorf("unexpected request path: %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("location[directory]"); got != "/workspace/project" {
			t.Errorf("unexpected location query: %q", got)
		}
		_, _ = io.WriteString(w, `{"location":{"directory":"/workspace/project"},"data":[{"id":"provider-x","name":"Provider X","methods":[{"type":"key","label":"API key","form":[{"key":"region","type":"string"}],"futureField":true}],"connections":[]}]}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	integrations, err := client.ListIntegrations(context.Background(), "/workspace/project")
	if err != nil {
		t.Fatal(err)
	}
	if len(integrations) != 1 || len(integrations[0].Methods) != 1 {
		t.Fatalf("unexpected integrations: %#v", integrations)
	}
	method := integrations[0].Methods[0]
	if method.Type != "key" || !strings.Contains(string(method.Form), "region") || !strings.Contains(string(method.Raw), "futureField") {
		t.Fatalf("method details were not preserved: %#v", method)
	}
}

func TestConnectKeyDoesNotIncludeServerErrorBody(t *testing.T) {
	const key = "provider-key-that-must-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/integration/provider%2Fx/connect/key" && r.URL.EscapedPath() != "/api/integration/provider%2Fx/connect/key" {
			t.Errorf("unexpected request: %s %s (%s)", r.Method, r.URL.Path, r.URL.EscapedPath())
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		var input map[string]any
		if err := json.Unmarshal(body, &input); err != nil {
			t.Errorf("invalid request body: %v", err)
		}
		if input["key"] != key {
			t.Errorf("provider key was not sent in the JSON body: %#v", input)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, key)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	err = client.ConnectKey(context.Background(), "provider/x", ConnectKeyInput{Key: key}, "")
	if err == nil {
		t.Fatal("expected the server's error status")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("error exposed the provider key: %v", err)
	}
}

func TestSessionPathsAreEscapedAndPromptResponseIsDecoded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/session/session%2Fid%3Fpart/prompt" {
			t.Errorf("session ID was not escaped as one path segment: %q", r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"id":"msg_1","sessionID":"session/id?part","time":{"created":123},"type":"user","payload":{"text":"hello"},"delivery":{"status":"queued"}}}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := client.Prompt(context.Background(), "session/id?part", PromptInput{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "msg_1" || item.Payload.Text != "hello" {
		t.Fatalf("unexpected queued message: %#v", item)
	}
}

func TestSubscribeEventsReadsServerSentEventAndFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/event" || r.URL.Query().Get("types") != "session.text.delta" {
			t.Errorf("unexpected event subscription URL: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: evt_1\nevent: session.text.delta\ndata: {\"delta\":\"hello\"}\n\n")
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := client.SubscribeEvents(ctx, "session.text.delta")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != "evt_1" || event.Type != "session.text.delta" || string(event.Data) != `{"delta":"hello"}` {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestNewClientRejectsInsecureRemoteServer(t *testing.T) {
	if _, err := NewClient("http://example.com", ""); err == nil {
		t.Fatal("expected non-loopback HTTP URL to be rejected")
	}
	if _, err := NewClient("http://127.0.0.1:4096", ""); err != nil {
		t.Fatalf("loopback HTTP URL should be accepted: %v", err)
	}
}
