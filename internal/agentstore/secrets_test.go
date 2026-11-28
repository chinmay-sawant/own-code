package agentstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAPIKeyNeverReachesDatabaseBytes stores a fake key through every
// redaction path (tool input, tool result, turn error) and scans the SQLite
// file and its WAL/SHM siblings for the key.
func TestAPIKeyNeverReachesDatabaseBytes(t *testing.T) {
	const key = "oc_sk_FAKEKEYSECRET1234567890abcdef"
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private", "sessions.sqlite")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	consoleSession(t, store, "s1")
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := store.BeginTurn(ctx, Turn{SessionID: "s1", ID: "t1", StartedAt: at}); err != nil {
		t.Fatal(err)
	}
	mustNil(t, store.SaveTurnMessage(ctx, TurnMessage{ID: "a1", SessionID: "s1", TurnID: "t1", Role: "assistant", CreatedAt: at}))
	if _, err := store.SaveToolCall(ctx, ToolCall{SessionID: "s1", ID: "c1", MessageID: "a1", TurnID: "t1", Ordinal: -1, ToolName: "shell",
		Input: `{"cmd":"curl -H 'Authorization: Bearer ` + key + `' x","api_key":"` + key + `"}`}); err != nil {
		t.Fatal(err)
	}
	mustNil(t, store.RecordToolApproval(ctx, "s1", "c1", ApprovalApproved, at))
	mustNil(t, store.BeginToolDispatch(ctx, "s1", "c1", at))
	mustNil(t, store.CompleteToolCall(ctx, "s1", "c1", ToolSucceeded, "Authorization: Bearer "+key+"\nx-api-key: "+key+"\nsent bearer "+key, at))
	mustNil(t, store.FinishTurn(ctx, "s1", "t1", TurnFailed, "401 Authorization: Bearer "+key, at))
	call, err := store.GetToolCall(ctx, "s1", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(call.Input+call.Result, key) {
		t.Fatalf("tool history leaks key: %+v", call)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) == 0 {
		t.Fatalf("ReadDir() = %v, %v", entries, err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(path), entry.Name()))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), key) || strings.Contains(string(data), "FAKEKEYSECRET") {
			t.Fatalf("API key found in %s", entry.Name())
		}
	}
}
