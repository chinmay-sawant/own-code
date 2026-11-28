package agentstore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "private", "sessions.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func testSession(id, workspace, server, remote string) Session {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	return Session{
		ID:                id,
		WorkspaceRoot:     workspace,
		ServerID:          server,
		OpenCodeSessionID: remote,
		Title:             "review",
		Agent:             "build",
		Model:             "model-x",
		Status:            SessionRunning,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func TestOpenMigratesAndReopensDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "sessions.sqlite")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer func() { _ = second.Close() }()
	var version int
	if err := second.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}

	for _, table := range []string{"workspaces", "agent_sessions", "agent_messages", "tool_activity", "event_checkpoints"} {
		var forbiddenColumns int
		query := `SELECT count(*) FROM pragma_table_info(?)
			WHERE lower(name) LIKE '%key%' OR lower(name) LIKE '%password%' OR lower(name) LIKE '%token%' OR lower(name) LIKE '%auth%'`
		if err := second.db.QueryRow(query, table).Scan(&forbiddenColumns); err != nil {
			t.Fatalf("inspect %s columns: %v", table, err)
		}
		if forbiddenColumns != 0 {
			t.Fatalf("%s has %d secret-like columns", table, forbiddenColumns)
		}
	}
}

func TestCreateSessionIsIdempotentAndScopesWorkspace(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	firstInput := testSession("local-1", "/work/one", "server-a", "ses-1")
	firstInput.WorktreeRoot = "/worktrees/one"
	one, err := store.CreateSession(ctx, firstInput)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	retry, err := store.CreateSession(ctx, testSession("different-local-id", "/work/orphan", "server-a", "ses-1"))
	if err != nil {
		t.Fatalf("idempotent CreateSession() error = %v", err)
	}
	if retry.ID != one.ID || retry.WorkspaceRoot != one.WorkspaceRoot || retry.WorktreeRoot != one.WorktreeRoot {
		t.Fatalf("retry returned %+v; want original session %+v", retry, one)
	}

	if _, err := store.CreateSession(ctx, testSession("local-2", "/work/two", "server-a", "ses-2")); err != nil {
		t.Fatalf("CreateSession(second workspace) error = %v", err)
	}
	all, err := store.ListSessions(ctx, "")
	if err != nil {
		t.Fatalf("ListSessions(all) error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListSessions(all) returned %d rows, want 2", len(all))
	}
	firstWorkspace, err := store.ListSessions(ctx, "/work/one")
	if err != nil {
		t.Fatalf("ListSessions(workspace) error = %v", err)
	}
	if len(firstWorkspace) != 1 || firstWorkspace[0].ID != "local-1" || firstWorkspace[0].WorktreeRoot != "/worktrees/one" {
		t.Fatalf("ListSessions(workspace) = %+v, want only local-1", firstWorkspace)
	}
}

func TestOpenMigratesV1DatabaseAndPreservesOptionalWorktreeRoot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy", "sessions.sqlite")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(legacy) error = %v", err)
	}
	legacy.SetMaxOpenConns(1)
	tx, err := legacy.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx(legacy) error = %v", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create legacy migration table: %v", err)
	}
	if err := applySchemaV1(ctx, tx); err != nil {
		t.Fatalf("apply legacy schema v1: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (root, last_seen_at) VALUES (?, ?)`, "/legacy/workspace", 1); err != nil {
		t.Fatalf("seed legacy workspace: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_sessions (
		id, workspace_root, server_id, opencode_session_id, title, agent, model,
		status, created_at, updated_at, last_event_sequence
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"legacy-session", "/legacy/workspace", "server-legacy", "remote-legacy", "legacy title", "", "",
		string(SessionCompleted), 2, 3, nil); err != nil {
		t.Fatalf("seed legacy session: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (1, ?)`, 4); err != nil {
		t.Fatalf("record legacy schema version: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit legacy schema: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open(v1 database) error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	var version int
	if err := store.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read migrated version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("migrated schema version = %d, want %d", version, schemaVersion)
	}
	legacySessions, err := store.ListSessions(ctx, "/legacy/workspace")
	if err != nil {
		t.Fatalf("ListSessions(legacy) error = %v", err)
	}
	if len(legacySessions) != 1 || legacySessions[0].ID != "legacy-session" || legacySessions[0].WorktreeRoot != "" {
		t.Fatalf("legacy session after migration = %+v, want preserved row with empty worktree root", legacySessions)
	}

	newSession := testSession("new-session", "/legacy/workspace", "server-legacy", "remote-new")
	newSession.WorktreeRoot = filepath.Join(t.TempDir(), "isolated-worktree")
	created, err := store.CreateSession(ctx, newSession)
	if err != nil {
		t.Fatalf("CreateSession(with worktree) error = %v", err)
	}
	if created.WorktreeRoot != newSession.WorktreeRoot {
		t.Fatalf("created worktree root = %q, want %q", created.WorktreeRoot, newSession.WorktreeRoot)
	}
	listed, err := store.ListSessions(ctx, "/legacy/workspace")
	if err != nil {
		t.Fatalf("ListSessions(after migration) error = %v", err)
	}
	if len(listed) != 2 || listed[0].WorktreeRoot != newSession.WorktreeRoot {
		t.Fatalf("sessions after worktree migration = %+v, want new worktree path round-tripped", listed)
	}
}

func TestMessageToolAndCheckpointRoundTripWithBounds(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	session, err := store.CreateSession(ctx, testSession("s1", "/work", "server", "remote-1"))
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	largeBody := strings.Repeat("☃", maxMessageBytes)
	if err := store.AppendMessage(ctx, Message{
		ID: "m1", SessionID: session.ID, OpenCodeMessageID: "remote-msg-1",
		Ordinal: 1, Role: "assistant", Body: largeBody,
	}); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	messages, err := store.ListMessages(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 1 || len(messages[0].Body) > maxMessageBytes || !utf8Valid(messages[0].Body) {
		t.Fatalf("message was not safely bounded: rows=%d bytes=%d validUTF8=%t", len(messages), len(messages[0].Body), utf8Valid(messages[0].Body))
	}

	if err := store.UpsertToolActivity(ctx, ToolActivity{
		ID: "tool-1", SessionID: session.ID, OpenCodePartID: "part-1", Ordinal: 2,
		ToolName: "read", Status: "completed", Summary: strings.Repeat("s", maxToolSummary+10),
		Output: strings.Repeat("o", maxToolOutput+20),
	}); err != nil {
		t.Fatalf("UpsertToolActivity() error = %v", err)
	}
	activities, err := store.ListToolActivity(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListToolActivity() error = %v", err)
	}
	if len(activities) != 1 || len(activities[0].Summary) > maxToolSummary || len(activities[0].Output) > maxToolOutput {
		t.Fatalf("tool activity was not bounded: %+v", activities)
	}

	checkpoint := Checkpoint{SessionID: session.ID, Sequence: 12}
	if err := store.SaveCheckpoint(ctx, checkpoint); err != nil {
		t.Fatalf("SaveCheckpoint() error = %v", err)
	}
	got, err := store.GetCheckpoint(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetCheckpoint() error = %v", err)
	}
	if got.Sequence != checkpoint.Sequence || got.SessionID != checkpoint.SessionID {
		t.Fatalf("GetCheckpoint() = %+v, want %+v", got, checkpoint)
	}
	updatedSessions, err := store.ListSessions(ctx, "/work")
	if err != nil {
		t.Fatalf("ListSessions(after checkpoint) error = %v", err)
	}
	if len(updatedSessions) != 1 || updatedSessions[0].LastEventSequence == nil || *updatedSessions[0].LastEventSequence != checkpoint.Sequence {
		t.Fatalf("session checkpoint = %+v, want sequence %d", updatedSessions, checkpoint.Sequence)
	}
	if _, err := store.GetCheckpoint(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetCheckpoint(missing) error = %v, want ErrNotFound", err)
	}
}

func TestDatabasePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not enforced on Windows")
	}
	store := openTestStore(t)
	fileInfo, err := os.Stat(store.Path())
	if err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("database mode = %04o, want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(store.Path()))
	if err != nil {
		t.Fatalf("stat database directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("database directory mode = %04o, want 0700", got)
	}
}

func utf8Valid(value string) bool {
	return strings.ToValidUTF8(value, "") == value
}
