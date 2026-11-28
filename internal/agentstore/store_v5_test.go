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

func TestSessionsDoNotRequireServerIdentity(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"local-a", "local-b"} {
		if _, err := store.CreateSession(ctx, Session{ID: id, WorkspaceRoot: "/work", Status: SessionQueued, CreatedAt: now}); err != nil {
			t.Fatalf("CreateSession(%q) without server IDs error = %v", id, err)
		}
	}
	again, err := store.CreateSession(ctx, Session{ID: "local-a", WorkspaceRoot: "/work", Title: "ignored"})
	if err != nil || again.ID != "local-a" {
		t.Fatalf("idempotent CreateSession() = %+v, %v", again, err)
	}
	updated, err := store.UpdateSessionMetadata(ctx, Session{ID: "local-a", WorkspaceRoot: "/work", Title: "renamed", Status: SessionCompleted})
	if err != nil || updated.Title != "renamed" || updated.Status != SessionCompleted || updated.Legacy {
		t.Fatalf("UpdateSessionMetadata() = %+v, %v", updated, err)
	}
	if _, err := store.UpdateSessionMetadata(ctx, Session{ID: "local-a", WorkspaceRoot: "/other"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateSessionMetadata(wrong workspace) error = %v, want ErrNotFound", err)
	}
	sessions, err := store.ListSessions(ctx, "/work")
	if err != nil || len(sessions) != 2 {
		t.Fatalf("ListSessions() = %v, %v", sessions, err)
	}
}

func TestRenameSession(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	consoleSession(t, store, "s1")
	at := time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := store.RenameSession(ctx, "s1", "  Fix the build  ", at); err != nil {
		t.Fatal(err)
	}
	got, _ := store.ListSessions(ctx, "/work")
	if len(got) != 1 || got[0].Title != "Fix the build" || !got[0].UpdatedAt.Equal(at) {
		t.Fatalf("after rename = %+v", got)
	}
	if err := store.RenameSession(ctx, "s1", "   ", at); err == nil {
		t.Fatal("RenameSession() accepted a blank title")
	}
	if err := store.RenameSession(ctx, "missing", "x", at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RenameSession(missing) error = %v, want ErrNotFound", err)
	}
	if err := store.RenameSession(ctx, "s1", strings.Repeat("é", 600), at); err != nil {
		t.Fatal(err)
	}
	got, _ = store.ListSessions(ctx, "/work")
	if len(got[0].Title) > maxTitleBytes {
		t.Fatalf("title length = %d, want <= %d", len(got[0].Title), maxTitleBytes)
	}
}

func TestArchiveSessionHidesListsAndBlocksTurns(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	for _, id := range []string{"s1", "s2"} {
		consoleSession(t, store, id)
		if _, err := store.UpdateSessionMetadata(ctx, Session{ID: id, WorkspaceRoot: "/work", Status: SessionCompleted}); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)

	if err := store.ArchiveSession(ctx, "s1", true, at); err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveSession(ctx, "s1", true, at.Add(time.Hour)); err != nil {
		t.Fatalf("repeat archive error = %v", err)
	}
	visible, _ := store.ListSessions(ctx, "/work")
	if len(visible) != 1 || visible[0].ID != "s2" {
		t.Fatalf("default list = %+v, want only s2", visible)
	}
	all, _ := store.ListSessions(ctx, "/work", IncludeArchived())
	if len(all) != 2 {
		t.Fatalf("IncludeArchived list = %+v", all)
	}
	for _, s := range all {
		if s.ID == "s1" && (!s.Archived || !s.ArchivedAt.Equal(at)) {
			t.Fatalf("archived session = %+v", s)
		}
		if s.ID == "s2" && s.Archived {
			t.Fatalf("s2 unexpectedly archived")
		}
	}
	if _, err := store.BeginTurn(ctx, Turn{SessionID: "s1", ID: "t1", StartedAt: at}); !errors.Is(err, ErrArchived) {
		t.Fatalf("BeginTurn(archived) error = %v, want ErrArchived", err)
	}
	if err := store.ArchiveSession(ctx, "s1", false, at); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginTurn(ctx, Turn{SessionID: "s1", ID: "t1", StartedAt: at}); err != nil {
		t.Fatalf("BeginTurn(restored) error = %v", err)
	}
	if err := store.ArchiveSession(ctx, "s1", true, at); !errors.Is(err, ErrConflict) {
		t.Fatalf("archive running session error = %v, want ErrConflict", err)
	}
	if err := store.ArchiveSession(ctx, "missing", true, at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archive missing error = %v, want ErrNotFound", err)
	}
	if _, err := store.BeginTurn(ctx, Turn{SessionID: "nope", ID: "t1", StartedAt: at}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("BeginTurn(missing session) error = %v, want ErrNotFound", err)
	}
}

func TestLegacyServerSessionsStayReadableButCannotRunTurns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
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
		`INSERT INTO agent_sessions (id, workspace_root, server_id, opencode_session_id, title, agent, model, status, created_at, updated_at)
			VALUES ('old', '/work', 'server-a', 'remote-1', 'old', 'build', 'p/m', 'completed', 1, 2)`,
		`INSERT INTO agent_sessions (id, workspace_root, server_id, opencode_session_id, title, agent, model, status, created_at, updated_at)
			VALUES ('con', '/work', 'opencode-console', 'con', 'c', 'opencode', 'p/m', 'completed', 1, 3)`,
		`INSERT INTO agent_messages VALUES ('m1', 'old', 'rm1', 0, 'user', 'hello', 1)`,
		`INSERT INTO event_checkpoints VALUES ('old', 9, 2)`,
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
		t.Fatal(err)
	}
	defer store.Close()
	sessions, err := store.ListSessions(ctx, "/work")
	if err != nil || len(sessions) != 2 {
		t.Fatalf("ListSessions() = %v, %v", sessions, err)
	}
	byID := map[string]Session{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	if !byID["old"].Legacy || byID["con"].Legacy {
		t.Fatalf("legacy flags: old=%v con=%v", byID["old"].Legacy, byID["con"].Legacy)
	}
	if msgs, err := store.ListMessages(ctx, "old"); err != nil || len(msgs) != 1 {
		t.Fatalf("legacy messages = %v, %v", msgs, err)
	}
	if cp, err := store.GetCheckpoint(ctx, "old"); err != nil || cp.Sequence != 9 {
		t.Fatalf("legacy checkpoint = %+v, %v", cp, err)
	}
	if _, err := store.BeginTurn(ctx, Turn{SessionID: "old", ID: "t", StartedAt: time.Now()}); !errors.Is(err, ErrLegacySession) {
		t.Fatalf("BeginTurn(legacy) error = %v, want ErrLegacySession", err)
	}
	if _, err := store.BeginTurn(ctx, Turn{SessionID: "con", ID: "t", StartedAt: time.Now()}); err != nil {
		t.Fatalf("BeginTurn(old console row) error = %v", err)
	}
	var fk int
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d, %v", fk, err)
	}
}
