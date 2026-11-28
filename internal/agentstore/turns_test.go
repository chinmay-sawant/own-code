package agentstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func consoleSession(t *testing.T, store *Store, id string) Session {
	t.Helper()
	session := testSession(id, "/work", "console", id)
	session.ProviderID = "opencode"
	session.ModelID = "big-model"
	stored, err := store.CreateSession(context.Background(), session)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	return stored
}

func TestMigrationFromV3PreservesLegacyTranscript(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for version, apply := range []func(context.Context, *sql.Tx) error{applySchemaV1, applySchemaV2, applySchemaV3} {
		if err := apply(ctx, tx); err != nil {
			t.Fatal(err)
		}
		if err := recordMigration(ctx, tx, version+1); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO workspaces VALUES ('/work', 1)`,
		`INSERT INTO agent_sessions (id, workspace_root, server_id, opencode_session_id, title, agent, model, status, created_at, updated_at, last_event_sequence)
			VALUES ('local-1', '/work', 'server-a', 'remote-1', 'old', 'build', 'prov/model', 'completed', 1, 2, 7)`,
		`INSERT INTO agent_messages VALUES ('m1', 'local-1', 'rm1', 0, 'user', 'hello old', 1)`,
		`INSERT INTO agent_messages VALUES ('m2', 'local-1', 'rm2', 1, 'assistant', 'old answer', 2)`,
		`INSERT INTO tool_activity VALUES ('t1', 'local-1', 'p1', 0, 'bash', 'completed', 'ls', 'out', 3)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() on v3 database error = %v", err)
	}
	defer func() { _ = store.Close() }()

	sessions, err := store.ListSessions(ctx, "/work")
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ListSessions() = %v, %v", sessions, err)
	}
	if got := sessions[0]; got.ID != "local-1" || got.ServerID != "server-a" || got.ModelID != "prov/model" || got.ProviderID != "" {
		t.Fatalf("migrated session = %+v", got)
	}
	history, err := store.LoadHistory(ctx, "local-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Parts[0].Text != "hello old" || history[1].Parts[0].Text != "old answer" {
		t.Fatalf("legacy history = %+v", history)
	}
	activity, err := store.ListToolActivity(ctx, "local-1")
	if err != nil || len(activity) != 1 {
		t.Fatalf("legacy tool activity = %v, %v", activity, err)
	}

	// A new Console session now stores provider and model separately.
	created := consoleSession(t, store, "local-2")
	if created.ProviderID != "opencode" || created.ModelID != "big-model" {
		t.Fatalf("console session = %+v", created)
	}
}

func TestTurnRoundTripWithToolCallsAndRecovery(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	consoleSession(t, store, "s1")
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	// Turn 1: complete, with an approved and executed tool call.
	if _, err := store.BeginTurn(ctx, Turn{SessionID: "s1", ID: "turn1", UserMessageID: "u1", ProviderID: "opencode", ModelID: "big-model", StartedAt: at}); err != nil {
		t.Fatal(err)
	}
	mustNil(t, store.SaveTurnMessage(ctx, TurnMessage{ID: "u1", SessionID: "s1", TurnID: "turn1", Role: "user", CreatedAt: at}))
	mustNil(t, store.SaveMessagePart(ctx, MessagePart{SessionID: "s1", MessageID: "u1", ID: "p0", Ordinal: -1, Kind: PartText, Text: "list files"}))
	mustNil(t, store.SaveTurnMessage(ctx, TurnMessage{ID: "a1", SessionID: "s1", TurnID: "turn1", Role: "assistant", CreatedAt: at}))
	mustNil(t, store.SaveMessagePart(ctx, MessagePart{SessionID: "s1", MessageID: "a1", ID: "p0", Ordinal: -1, Kind: PartText, Text: "Let me look.", Status: PartStreaming}))
	mustNil(t, store.SaveMessagePart(ctx, MessagePart{SessionID: "s1", MessageID: "a1", ID: "p0", Kind: PartText, Text: "Let me look. ", Status: PartComplete}))
	mustNil(t, store.SaveMessagePart(ctx, MessagePart{SessionID: "s1", MessageID: "a1", ID: "p0", Kind: PartText, Text: "late stream", Status: PartStreaming})) // ignored
	const signature = "opaque/gemini+signature=="
	call, err := store.SaveToolCall(ctx, ToolCall{
		SessionID: "s1", ID: "c1", MessageID: "a1", TurnID: "turn1", Ordinal: -1,
		ToolName: "bash", Input: `{"cmd":"ls"}`, ThoughtSignature: signature,
	})
	if err != nil || call.Status != ToolRequested || call.ThoughtSignature != signature {
		t.Fatalf("SaveToolCall() = %+v, %v", call, err)
	}
	mustNil(t, store.RecordToolApproval(ctx, "s1", "c1", ApprovalApproved, at))
	mustNil(t, store.RecordToolApproval(ctx, "s1", "c1", ApprovalApproved, at)) // idempotent
	if err := store.RecordToolApproval(ctx, "s1", "c1", ApprovalDenied, at); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting approval error = %v", err)
	}
	mustNil(t, store.BeginToolDispatch(ctx, "s1", "c1", at))
	if err := store.BeginToolDispatch(ctx, "s1", "c1", at); !errors.Is(err, ErrAlreadyDispatched) {
		t.Fatalf("second dispatch error = %v", err)
	}
	mustNil(t, store.CompleteToolCall(ctx, "s1", "c1", ToolSucceeded, "a.go\nb.go", at))
	mustNil(t, store.CompleteToolCall(ctx, "s1", "c1", ToolSucceeded, "a.go\nb.go", at)) // idempotent
	mustNil(t, store.SaveMessagePart(ctx, MessagePart{SessionID: "s1", MessageID: "a1", ID: "p9", Ordinal: -1, Kind: PartText, Text: "Done."}))
	mustNil(t, store.FinishTurn(ctx, "s1", "turn1", TurnCompleted, "", at))
	mustNil(t, store.FinishTurn(ctx, "s1", "turn1", TurnCompleted, "", at))
	if err := store.FinishTurn(ctx, "s1", "turn1", TurnFailed, "x", at); !errors.Is(err, ErrConflict) {
		t.Fatalf("finish conflict = %v", err)
	}

	// Turn 2: crashes mid-run with a running call, a denied-pending call, and partial output.
	if _, err := store.BeginTurn(ctx, Turn{SessionID: "s1", ID: "turn2", StartedAt: at}); err != nil {
		t.Fatal(err)
	}
	mustNil(t, store.SaveTurnMessage(ctx, TurnMessage{ID: "a2", SessionID: "s1", TurnID: "turn2", Role: "assistant", CreatedAt: at}))
	mustNil(t, store.SaveMessagePart(ctx, MessagePart{SessionID: "s1", MessageID: "a2", ID: "p0", Ordinal: -1, Kind: PartText, Text: "partial", Status: PartStreaming}))
	for _, id := range []string{"c2", "c3"} {
		if _, err := store.SaveToolCall(ctx, ToolCall{SessionID: "s1", ID: id, MessageID: "a2", TurnID: "turn2", Ordinal: -1, ToolName: "edit", Input: `{}`}); err != nil {
			t.Fatal(err)
		}
	}
	mustNil(t, store.RecordToolApproval(ctx, "s1", "c2", ApprovalApproved, at))
	mustNil(t, store.BeginToolDispatch(ctx, "s1", "c2", at))
	if _, err := store.SaveToolCall(ctx, ToolCall{SessionID: "s1", ID: "c4", MessageID: "a2", TurnID: "turn2", Ordinal: -1, ToolName: "edit", Input: `{}`}); err != nil {
		t.Fatal(err)
	}
	mustNil(t, store.RecordToolApproval(ctx, "s1", "c4", ApprovalDismissed, at))
	if err := store.BeginToolDispatch(ctx, "s1", "c4", at); !errors.Is(err, ErrAlreadyDispatched) {
		t.Fatalf("dispatch of denied call = %v", err)
	}

	report, err := store.RecoverInterrupted(ctx, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.TurnsInterrupted != 1 || report.CallsUnknown != 1 || report.CallsInterrupted != 1 || report.PartsInterrupted != 1 {
		t.Fatalf("report = %+v", report)
	}
	again, _ := store.RecoverInterrupted(ctx, at.Add(2*time.Minute))
	if again != (RecoveryReport{}) {
		t.Fatalf("second recovery = %+v, want no-op", again)
	}
	assertStatus := func(id string, want ToolCallStatus) {
		t.Helper()
		got, err := store.GetToolCall(ctx, "s1", id)
		if err != nil || got.Status != want {
			t.Fatalf("call %s = %+v, %v; want %s", id, got, err, want)
		}
	}
	assertStatus("c1", ToolSucceeded)
	assertStatus("c2", ToolUnknown)
	assertStatus("c3", ToolInterrupted)
	assertStatus("c4", ToolDenied)

	turns, _ := store.ListTurns(ctx, "s1")
	if len(turns) != 2 || turns[0].Status != TurnCompleted || turns[1].Status != TurnInterrupted || turns[1].Ordinal != 1 {
		t.Fatalf("turns = %+v", turns)
	}
	sessions, _ := store.ListSessions(ctx, "/work")
	if sessions[0].Status != SessionInterrupted {
		t.Fatalf("session status = %s", sessions[0].Status)
	}

	history, err := store.LoadHistory(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("history len = %d", len(history))
	}
	a1 := history[1]
	if len(a1.Parts) != 3 || a1.Parts[0].Text != "Let me look. " || a1.Parts[1].ToolCall == nil ||
		a1.Parts[1].ToolCall.Result != "a.go\nb.go" || a1.Parts[1].ToolCall.ThoughtSignature != signature || a1.Parts[2].Text != "Done." {
		t.Fatalf("assistant history = %+v", a1)
	}
	a2 := history[2]
	if a2.Parts[0].Status != PartInterrupted || a2.Parts[0].Text != "partial" || len(a2.Parts) != 4 {
		t.Fatalf("interrupted assistant history = %+v", a2)
	}
	for _, part := range a2.Parts[1:] {
		if part.ToolCall.Result == "" {
			t.Fatalf("call %s has no result in history", part.ToolCall.ID)
		}
	}
	if !strings.Contains(a2.Parts[1].ToolCall.Result, "unknown") && !strings.Contains(a2.Parts[1].ToolCall.Result, "Outcome unknown") {
		t.Fatalf("unknown call result = %q", a2.Parts[1].ToolCall.Result)
	}
	// Legacy readers still see text.
	messages, _ := store.ListMessages(ctx, "s1")
	if messages[1].Body != "Let me look. Done." {
		t.Fatalf("legacy body = %q", messages[1].Body)
	}
}

func TestThoughtSignaturesAndPartOrderSurviveStoreReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	consoleSession(t, store, "s1")
	if _, err := store.BeginTurn(ctx, Turn{SessionID: "s1", ID: "turn1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTurnMessage(ctx, TurnMessage{ID: "a1", SessionID: "s1", TurnID: "turn1", Role: "assistant"}); err != nil {
		t.Fatal(err)
	}
	callOrdinal := int64(1)
	const callSignature = "opaque-call-signature"
	const textSignature = "opaque-text-signature"
	if _, err := store.SaveToolCall(ctx, ToolCall{
		SessionID: "s1", ID: "call1", MessageID: "a1", TurnID: "turn1", Ordinal: -1,
		MessagePartOrdinal: &callOrdinal, ToolName: "read", Input: `{}`, ThoughtSignature: callSignature,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceMessageTextParts(ctx, "s1", "a1", []MessagePart{
		{SessionID: "s1", MessageID: "a1", ID: "text0", Ordinal: 0, Kind: PartText, Text: "before", ThoughtSignature: textSignature, Status: PartComplete},
		{SessionID: "s1", MessageID: "a1", ID: "text2", Ordinal: 2, Kind: PartText, Text: "after", Status: PartComplete},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	history, err := store.LoadHistory(ctx, "s1")
	if err != nil || len(history) != 1 || len(history[0].Parts) != 3 {
		t.Fatalf("LoadHistory() = %+v, %v", history, err)
	}
	parts := history[0].Parts
	if parts[0].Ordinal != 0 || parts[0].Text != "before" || parts[0].ThoughtSignature != textSignature ||
		parts[1].Ordinal != 1 || parts[1].ToolCall == nil || parts[1].ThoughtSignature != callSignature || parts[1].ToolCall.ThoughtSignature != callSignature ||
		parts[2].Ordinal != 2 || parts[2].Text != "after" {
		t.Fatalf("reopened parts = %+v", parts)
	}
}

func TestIdempotentRetriesDoNotDuplicate(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	consoleSession(t, store, "s1")
	for i := 0; i < 3; i++ {
		if _, err := store.BeginTurn(ctx, Turn{SessionID: "s1", ID: "t"}); err != nil {
			t.Fatal(err)
		}
		mustNil(t, store.SaveTurnMessage(ctx, TurnMessage{ID: "m", SessionID: "s1", TurnID: "t", Role: "assistant"}))
		mustNil(t, store.SaveMessagePart(ctx, MessagePart{SessionID: "s1", MessageID: "m", ID: "p", Ordinal: -1, Kind: PartText, Text: "x"}))
		if _, err := store.SaveToolCall(ctx, ToolCall{SessionID: "s1", ID: "c", MessageID: "m", Ordinal: -1, ToolName: "bash", Input: `{}`}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			mustNil(t, store.RecordToolApproval(ctx, "s1", "c", ApprovalApproved, time.Time{}))
		}
	}
	history, _ := store.LoadHistory(ctx, "s1")
	if len(history) != 1 || len(history[0].Parts) != 2 {
		t.Fatalf("history = %+v", history)
	}
	// Retrying the request must not reset the approval.
	if call, _ := store.GetToolCall(ctx, "s1", "c"); call.Status != ToolApproved {
		t.Fatalf("status after retry = %s", call.Status)
	}
	if turns, _ := store.ListTurns(ctx, "s1"); len(turns) != 1 {
		t.Fatalf("turns = %d", len(turns))
	}
}

func TestBoundsAndRedaction(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	consoleSession(t, store, "s1")
	mustNil(t, store.SaveTurnMessage(ctx, TurnMessage{ID: "m", SessionID: "s1", Role: "assistant"}))

	big := `{"a":"` + strings.Repeat("x", maxToolInput) + `"}`
	if _, err := store.SaveToolCall(ctx, ToolCall{SessionID: "s1", ID: "big", MessageID: "m", Ordinal: -1, ToolName: "bash", Input: big}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize input error = %v", err)
	}
	if _, err := store.SaveToolCall(ctx, ToolCall{SessionID: "s1", ID: "bad", MessageID: "m", Ordinal: -1, ToolName: "bash", Input: `{"a":`}); err == nil {
		t.Fatal("truncated JSON input was accepted")
	}
	if _, err := store.SaveToolCall(ctx, ToolCall{SessionID: "s1", ID: "c", MessageID: "m", Ordinal: -1, ToolName: "bash", Input: `{"k":"sk-abcdefghijklmnopqrstuvwxyz"}`}); err != nil {
		t.Fatal(err)
	}
	mustNil(t, store.RecordToolApproval(ctx, "s1", "c", ApprovalApproved, time.Time{}))
	mustNil(t, store.BeginToolDispatch(ctx, "s1", "c", time.Time{}))
	out := "Authorization: Bearer abcdefghijkl\n" + strings.Repeat("y", maxToolResult)
	mustNil(t, store.CompleteToolCall(ctx, "s1", "c", ToolSucceeded, out, time.Time{}))
	got, _ := store.GetToolCall(ctx, "s1", "c")
	if !got.ResultTruncated || len(got.Result) > maxToolResult || strings.Contains(got.Result, "abcdefghijkl") || strings.Contains(got.Input, "sk-abc") {
		t.Fatalf("stored call leaks or is unbounded: input=%q result prefix=%q truncated=%v len=%d", got.Input, got.Result[:40], got.ResultTruncated, len(got.Result))
	}
	mustNil(t, store.SaveMessagePart(ctx, MessagePart{SessionID: "s1", MessageID: "m", ID: "p", Ordinal: -1, Kind: PartText, Text: strings.Repeat("z", maxPartText+10)}))
	history, _ := store.LoadHistory(ctx, "s1")
	if len(history[0].Parts[0].Text) > maxPartText {
		t.Fatal("part text is unbounded")
	}
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenUserConfigRejectsSecondOwner(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first, err := OpenUserConfig(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenUserConfig(ctx, dir); !errors.Is(err, ErrProfileOwned) {
		t.Fatalf("second OpenUserConfig error = %v, want ErrProfileOwned", err)
	}
	mustNil(t, first.Close())
	again, err := OpenUserConfig(ctx, dir)
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	mustNil(t, again.Close())
}

func TestRejectToolCallAndInspectionList(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	consoleSession(t, store, "s1")
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := store.BeginTurn(ctx, Turn{SessionID: "s1", ID: "turn1", StartedAt: at}); err != nil {
		t.Fatal(err)
	}
	mustNil(t, store.SaveTurnMessage(ctx, TurnMessage{ID: "a1", SessionID: "s1", TurnID: "turn1", Role: "assistant", CreatedAt: at}))
	save := func(id string) {
		t.Helper()
		if _, err := store.SaveToolCall(ctx, ToolCall{SessionID: "s1", ID: id, MessageID: "a1", TurnID: "turn1", Ordinal: -1, ToolName: "run_shell", Input: `{}`}); err != nil {
			t.Fatal(err)
		}
	}
	save("rej")
	mustNil(t, store.RejectToolCall(ctx, "s1", "rej", "Rejected: unknown tool", at))
	mustNil(t, store.RejectToolCall(ctx, "s1", "rej", "Rejected: unknown tool", at)) // idempotent
	got, err := store.GetToolCall(ctx, "s1", "rej")
	if err != nil || got.Status != ToolFailed || !got.IsError || got.Result != "Rejected: unknown tool" || !got.DispatchStartedAt.IsZero() {
		t.Fatalf("rejected call = %+v, %v", got, err)
	}
	if err := store.BeginToolDispatch(ctx, "s1", "rej", at); !errors.Is(err, ErrAlreadyDispatched) {
		t.Fatalf("dispatch of rejected call = %v", err)
	}
	// A call that already progressed cannot be rejected.
	save("run")
	mustNil(t, store.RecordToolApproval(ctx, "s1", "run", ApprovalApproved, at))
	if err := store.RejectToolCall(ctx, "s1", "run", "x", at); !errors.Is(err, ErrConflict) {
		t.Fatalf("reject approved call = %v", err)
	}
	mustNil(t, store.BeginToolDispatch(ctx, "s1", "run", at))
	save("pending")
	mustNil(t, store.RecordToolApproval(ctx, "s1", "pending", ApprovalApproved, at))
	if list, _ := store.ListCallsNeedingInspection(ctx, "s1"); len(list) != 0 {
		t.Fatalf("before recovery = %+v", list)
	}
	if _, err := store.RecoverInterrupted(ctx, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListCallsNeedingInspection(ctx, "s1")
	if err != nil || len(list) != 2 || list[0].ID != "run" || list[0].Status != ToolUnknown || list[1].ID != "pending" || list[1].Status != ToolInterrupted {
		t.Fatalf("needing inspection = %+v, %v", list, err)
	}
	// A retry must be a new call ID with fresh approval: the old call can
	// never be approved or dispatched again.
	_ = store.RecordToolApproval(ctx, "s1", "run", ApprovalApproved, at) // repeat is a no-op
	if c, _ := store.GetToolCall(ctx, "s1", "run"); c.Status != ToolUnknown {
		t.Fatalf("re-approval changed status to %q", c.Status)
	}
	if err := store.BeginToolDispatch(ctx, "s1", "run", at); !errors.Is(err, ErrAlreadyDispatched) {
		t.Fatalf("re-dispatch unknown call = %v", err)
	}
	save("run-retry")
	fresh, _ := store.GetToolCall(ctx, "s1", "run-retry")
	if fresh.Status != ToolRequested || fresh.Approval != "" {
		t.Fatalf("retry must start unapproved: %+v", fresh)
	}
	if err := store.BeginToolDispatch(ctx, "s1", "run-retry", at); !errors.Is(err, ErrAlreadyDispatched) {
		t.Fatalf("dispatch without fresh approval = %v", err)
	}
}
