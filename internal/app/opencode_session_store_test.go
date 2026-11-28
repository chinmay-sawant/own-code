package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

func openCodeHistoryTestStore(t *testing.T) *agentstore.Store {
	t.Helper()
	store, err := agentstore.Open(context.Background(), filepath.Join(t.TempDir(), "config", "sessions.sqlite"))
	if err != nil {
		t.Fatalf("agentstore.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close() error = %v", err)
		}
	})
	return store
}

func TestOpenCodeSessionHistoryScopesAndImportsIdempotently(t *testing.T) {
	ctx := context.Background()
	store := openCodeHistoryTestStore(t)
	root := filepath.Join(t.TempDir(), "workspace")
	if err := osMkdirAll(root); err != nil {
		t.Fatal(err)
	}
	history, err := newOpenCodeSessionHistory(store, root, "server-local-a")
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	remote := opencode.Session{
		ID:       "remote-1",
		Title:    "Fix compiler",
		Time:     opencode.SessionTime{Created: float64(created.UnixMilli()), Updated: float64(created.Add(time.Minute).UnixMilli())},
		Location: opencode.LocationRef{Directory: root},
	}
	first, err := history.PersistSession(ctx, remote, agentstore.SessionRunning)
	if err != nil {
		t.Fatalf("PersistSession() error = %v", err)
	}
	second, err := history.PersistSession(ctx, remote, agentstore.SessionCompleted)
	if err != nil {
		t.Fatalf("PersistSession(retry) error = %v", err)
	}
	if first.ID != second.ID || first.Status != second.Status || first.Status != agentstore.SessionRunning {
		t.Fatalf("idempotent import changed the original session: first=%+v second=%+v", first, second)
	}
	if first.WorkspaceRoot != root || first.ServerID != "server-local-a" || first.OpenCodeSessionID != remote.ID || !first.CreatedAt.Equal(created) {
		t.Fatalf("imported session has wrong scope or timestamps: %+v", first)
	}
	listed, err := history.ListSessions(ctx)
	if err != nil || len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("ListSessions() = %+v, %v; want one imported session", listed, err)
	}
	if _, err := history.PersistSession(ctx, opencode.Session{
		ID: "remote-other", Location: opencode.LocationRef{Directory: filepath.Join(t.TempDir(), "elsewhere")},
	}, agentstore.SessionQueued); err == nil {
		t.Fatal("PersistSession() accepted a different workspace")
	}
	if _, err := history.PersistSession(ctx, opencode.Session{
		ID: "remote-missing-location",
	}, agentstore.SessionQueued); err == nil {
		t.Fatal("PersistSession() accepted a session without a workspace")
	}
}

func TestOpenCodeSessionHistoryFiltersOtherServersAndWorkspaces(t *testing.T) {
	ctx := context.Background()
	store := openCodeHistoryTestStore(t)
	root := filepath.Join(t.TempDir(), "workspace")
	otherRoot := filepath.Join(t.TempDir(), "other")
	for _, dir := range []string{root, otherRoot} {
		if err := osMkdirAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	for _, session := range []agentstore.Session{
		{ID: "a", WorkspaceRoot: root, ServerID: "server-a", OpenCodeSessionID: "remote-a", Status: agentstore.SessionCompleted},
		{ID: "b", WorkspaceRoot: root, ServerID: "server-b", OpenCodeSessionID: "remote-b", Status: agentstore.SessionCompleted},
		{ID: "c", WorkspaceRoot: otherRoot, ServerID: "server-a", OpenCodeSessionID: "remote-c", Status: agentstore.SessionCompleted},
	} {
		if _, err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("CreateSession(%q) error = %v", session.ID, err)
		}
	}
	history, err := newOpenCodeSessionHistory(store, root, "server-a")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := history.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "a" {
		t.Fatalf("ListSessions() = %+v, want only session a", listed)
	}
	if _, err := history.LoadMessages(ctx, "b"); err == nil {
		t.Fatal("LoadMessages() accepted a session belonging to another server")
	}
	if _, err := history.LoadMessages(ctx, "c"); err == nil {
		t.Fatal("LoadMessages() accepted a session belonging to another workspace")
	}
}

func TestProjectOpenCodeTextMessageOmitsToolAndCrossSessionData(t *testing.T) {
	raw := json.RawMessage(`{"info":{"id":"message-1","sessionID":"session-1","role":"assistant","time":{"created":1750000000000}},"parts":[{"id":"text-1","type":"text","text":"Answer"},{"id":"tool-1","type":"tool","tool":"read","input":{"path":"secret.txt"}},{"id":"text-2","type":"text","text":"continued"}]}`)
	projection, ok := projectOpenCodeTextMessage(raw, "session-1")
	if !ok {
		t.Fatal("projectOpenCodeTextMessage() rejected a valid assistant text message")
	}
	if projection.id != "message-1" || projection.role != "assistant" || projection.body != "Answer\ncontinued" || projection.ordinal == 0 {
		t.Fatalf("unexpected projection: %+v", projection)
	}
	if _, ok := projectOpenCodeTextMessage(raw, "different-session"); ok {
		t.Fatal("projectOpenCodeTextMessage() accepted a different session ID")
	}
	toolOnly := json.RawMessage(`{"info":{"id":"tool-message","sessionID":"session-1","role":"assistant","time":{"created":1750000000001}},"parts":[{"id":"tool-1","type":"tool","input":{"apiKey":"do-not-store"}}]}`)
	if _, ok := projectOpenCodeTextMessage(toolOnly, "session-1"); ok {
		t.Fatal("projectOpenCodeTextMessage() retained a tool-only message")
	}
}

func TestSyncOpenCodeMessagesPersistsBoundedTextOnlyAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "workspace")
	if err := osMkdirAll(root); err != nil {
		t.Fatal(err)
	}
	store := openCodeHistoryTestStore(t)
	history, err := newOpenCodeSessionHistory(store, root, "server-a")
	if err != nil {
		t.Fatal(err)
	}
	local, err := history.PersistSession(ctx, opencode.Session{
		ID:       "remote-session",
		Title:    "Sync me",
		Time:     opencode.SessionTime{Created: 1750000000000, Updated: 1750000000002},
		Location: opencode.LocationRef{Directory: root},
	}, agentstore.SessionRunning)
	if err != nil {
		t.Fatal(err)
	}

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/remote-session/message" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		if got := r.URL.Query().Get("cursor"); got == "" {
			_, _ = fmt.Fprint(w, `{"data":[{"info":{"id":"user-1","sessionID":"remote-session","role":"user","time":{"created":1750000000000}},"parts":[{"type":"text","text":"Question"}]},{"info":{"id":"tool-1","sessionID":"remote-session","role":"assistant","time":{"created":1750000000001}},"parts":[{"type":"tool","input":{"apiKey":"do-not-store"}}]}],"cursor":{"next":"page-2"}}`)
			return
		}
		if got := r.URL.Query().Get("cursor"); got != "page-2" {
			t.Errorf("unexpected cursor: %q", got)
		}
		_, _ = fmt.Fprint(w, `{"data":[{"info":{"id":"assistant-1","sessionID":"remote-session","role":"assistant","time":{"created":1750000000002}},"parts":[{"type":"text","text":"Answer"},{"type":"tool","input":{"apiKey":"do-not-store"}}]}],"cursor":{}}`)
	}))
	defer server.Close()
	client, err := opencode.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	messages, err := history.SyncMessages(ctx, client, local.ID)
	if err != nil {
		t.Fatalf("SyncMessages() error = %v", err)
	}
	if len(messages) != 2 || messages[0].OpenCodeMessageID != "user-1" || messages[1].OpenCodeMessageID != "assistant-1" {
		t.Fatalf("SyncMessages() = %+v, want two ordered text messages", messages)
	}
	if messages[0].Role != "user" || messages[0].Body != "Question" || messages[1].Role != "assistant" || messages[1].Body != "Answer" {
		t.Fatalf("SyncMessages() retained unexpected content: %+v", messages)
	}
	if strings.Contains(messages[0].Body+messages[1].Body, "do-not-store") {
		t.Fatal("tool payload was persisted into the text transcript")
	}
	if _, err := history.SyncMessages(ctx, client, local.ID); err != nil {
		t.Fatalf("SyncMessages(retry) error = %v", err)
	}
	if calls != 4 {
		t.Fatalf("message synchronization made %d requests, want 4 across two paginated passes", calls)
	}
	stored, err := history.LoadMessages(ctx, local.ID)
	if err != nil || len(stored) != 2 {
		t.Fatalf("LoadMessages() = %+v, %v; want two idempotently stored messages", stored, err)
	}
}

func TestOpenCodeSessionHistoryRejectsUnlistedLocalSession(t *testing.T) {
	history, err := newOpenCodeSessionHistory(openCodeHistoryTestStore(t), filepath.Join(t.TempDir(), "workspace"), "server-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := history.LoadMessages(context.Background(), "missing"); err == nil {
		t.Fatal("LoadMessages() accepted an unknown session")
	}
}

func osMkdirAll(path string) error { return os.MkdirAll(path, 0o700) }
