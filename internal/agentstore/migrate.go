package agentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const schemaVersion = 5

// The database file is capped at maxDatabasePages pages. With SQLite's default
// 4 KiB page that is MaxDatabaseBytes (256 MiB); writes past the cap fail with
// SQLITE_FULL instead of growing the profile without bound. There is no
// automatic pruning: sessions stay until a user deletes them.
const (
	maxDatabasePages = 65536
	MaxDatabaseBytes = maxDatabasePages * 4096
)

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA max_page_count = %d`, maxDatabasePages)); err != nil {
		return fmt.Errorf("agent store: set database size limit: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode = DELETE`); err != nil {
		return fmt.Errorf("agent store: set journal mode: %w", err)
	}

	// v5 rebuilds agent_sessions, which child tables reference. Foreign keys
	// must be off for the rebuild (the pragma is a no-op inside a transaction);
	// they are re-enabled and verified before returning.
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("agent store: disable foreign keys for migration: %w", err)
	}
	defer db.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("agent store: begin migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("agent store: create migration table: %w", err)
	}
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("agent store: read schema version: %w", err)
	}
	if current > schemaVersion {
		return fmt.Errorf("agent store: database schema %d is newer than supported schema %d", current, schemaVersion)
	}
	if current < 1 {
		if err := applySchemaV1(ctx, tx); err != nil {
			return err
		}
		if err := recordMigration(ctx, tx, 1); err != nil {
			return err
		}
	}
	if current < 2 {
		if err := applySchemaV2(ctx, tx); err != nil {
			return err
		}
		if err := recordMigration(ctx, tx, 2); err != nil {
			return err
		}
	}
	if current < 3 {
		if err := applySchemaV3(ctx, tx); err != nil {
			return err
		}
		if err := recordMigration(ctx, tx, 3); err != nil {
			return err
		}
	}
	if current < 4 {
		if err := applySchemaV4(ctx, tx); err != nil {
			return err
		}
		if err := recordMigration(ctx, tx, 4); err != nil {
			return err
		}
	}
	if current < 5 {
		if err := applySchemaV5(ctx, tx); err != nil {
			return err
		}
		if err := recordMigration(ctx, tx, 5); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("agent store: commit migration: %w", err)
	}
	return nil
}

func recordMigration(ctx context.Context, tx *sql.Tx, version int) error {
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (?, unixepoch('subsec') * 1000)`, version); err != nil {
		return fmt.Errorf("agent store: record migration %d: %w", version, err)
	}
	return nil
}

func applySchemaV1(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE workspaces (
			root TEXT PRIMARY KEY CHECK(length(root) <= 4096),
			last_seen_at INTEGER NOT NULL
		)`,
		`CREATE TABLE agent_sessions (
			id TEXT PRIMARY KEY CHECK(length(id) <= 256),
			workspace_root TEXT NOT NULL REFERENCES workspaces(root) ON DELETE CASCADE,
			server_id TEXT NOT NULL CHECK(length(server_id) <= 256),
			opencode_session_id TEXT NOT NULL CHECK(length(opencode_session_id) <= 256),
			title TEXT NOT NULL DEFAULT '' CHECK(length(title) <= 512),
			agent TEXT NOT NULL DEFAULT '' CHECK(length(agent) <= 128),
			model TEXT NOT NULL DEFAULT '' CHECK(length(model) <= 256),
			status TEXT NOT NULL CHECK(status IN ('queued', 'running', 'completed', 'failed', 'interrupted')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			last_event_sequence INTEGER CHECK(last_event_sequence IS NULL OR last_event_sequence >= 0),
			UNIQUE(server_id, opencode_session_id)
		)`,
		`CREATE INDEX agent_sessions_workspace_updated ON agent_sessions(workspace_root, updated_at DESC)`,
		`CREATE TABLE agent_messages (
			id TEXT PRIMARY KEY CHECK(length(id) <= 256),
			session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
			opencode_message_id TEXT NOT NULL CHECK(length(opencode_message_id) <= 256),
			ordinal INTEGER NOT NULL CHECK(ordinal >= 0),
			role TEXT NOT NULL CHECK(length(role) <= 32),
			body TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			UNIQUE(session_id, opencode_message_id),
			UNIQUE(session_id, ordinal),
			CHECK(length(body) <= 262144)
		)`,
		`CREATE TABLE tool_activity (
			id TEXT PRIMARY KEY CHECK(length(id) <= 256),
			session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
			opencode_part_id TEXT NOT NULL CHECK(length(opencode_part_id) <= 256),
			ordinal INTEGER NOT NULL CHECK(ordinal >= 0),
			tool_name TEXT NOT NULL CHECK(length(tool_name) <= 128),
			status TEXT NOT NULL CHECK(length(status) <= 64),
			summary TEXT NOT NULL DEFAULT '' CHECK(length(summary) <= 16384),
			output TEXT NOT NULL DEFAULT '' CHECK(length(output) <= 65536),
			updated_at INTEGER NOT NULL,
			UNIQUE(session_id, opencode_part_id),
			UNIQUE(session_id, ordinal)
		)`,
		`CREATE TABLE event_checkpoints (
			session_id TEXT PRIMARY KEY REFERENCES agent_sessions(id) ON DELETE CASCADE,
			sequence INTEGER NOT NULL CHECK(sequence >= 0),
			updated_at INTEGER NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("agent store: apply schema v1: %w", err)
		}
	}
	return nil
}

func applySchemaV2(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `ALTER TABLE agent_sessions ADD COLUMN worktree_root TEXT NOT NULL DEFAULT '' CHECK(length(worktree_root) <= 4096)`); err != nil {
		return fmt.Errorf("agent store: apply schema v2: %w", err)
	}
	return nil
}

func applySchemaV3(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `ALTER TABLE agent_sessions ADD COLUMN baseline_commit TEXT NOT NULL DEFAULT '' CHECK(length(baseline_commit) <= 128)`); err != nil {
		return fmt.Errorf("agent store: apply schema v3: %w", err)
	}
	return nil
}

// applySchemaV4 adds the Console runtime's durable turn model. Legacy columns
// (server_id, opencode_session_id, last_event_sequence) and tables stay in
// place so old server-backed transcripts remain readable; new code must not
// treat them as Console session state.
func applySchemaV4(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`ALTER TABLE agent_sessions ADD COLUMN provider_id TEXT NOT NULL DEFAULT '' CHECK(length(provider_id) <= 256)`,
		`ALTER TABLE agent_sessions ADD COLUMN model_id TEXT NOT NULL DEFAULT '' CHECK(length(model_id) <= 256)`,
		`UPDATE agent_sessions SET model_id = model WHERE model_id = ''`,
		`ALTER TABLE agent_messages ADD COLUMN turn_id TEXT NOT NULL DEFAULT '' CHECK(length(turn_id) <= 256)`,
		`CREATE TABLE agent_turns (
			session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
			id TEXT NOT NULL CHECK(length(id) > 0 AND length(id) <= 256),
			ordinal INTEGER NOT NULL CHECK(ordinal >= 0),
			user_message_id TEXT NOT NULL DEFAULT '' CHECK(length(user_message_id) <= 256),
			provider_id TEXT NOT NULL DEFAULT '' CHECK(length(provider_id) <= 256),
			model_id TEXT NOT NULL DEFAULT '' CHECK(length(model_id) <= 256),
			status TEXT NOT NULL CHECK(status IN ('running', 'completed', 'failed', 'interrupted')),
			error TEXT NOT NULL DEFAULT '' CHECK(length(error) <= 4096),
			started_at INTEGER NOT NULL,
			ended_at INTEGER,
			PRIMARY KEY(session_id, id),
			UNIQUE(session_id, ordinal)
		)`,
		`CREATE TABLE message_parts (
			session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
			message_id TEXT NOT NULL REFERENCES agent_messages(id) ON DELETE CASCADE,
			id TEXT NOT NULL CHECK(length(id) > 0 AND length(id) <= 256),
			ordinal INTEGER NOT NULL CHECK(ordinal >= 0),
			kind TEXT NOT NULL CHECK(kind IN ('text', 'tool_call')),
			text TEXT NOT NULL DEFAULT '' CHECK(length(text) <= 262144),
			tool_call_id TEXT NOT NULL DEFAULT '' CHECK(length(tool_call_id) <= 256),
			status TEXT NOT NULL CHECK(status IN ('streaming', 'complete', 'interrupted')),
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(session_id, message_id, id)
		)`,
		`CREATE INDEX message_parts_order ON message_parts(session_id, message_id, ordinal)`,
		`CREATE TABLE agent_tool_calls (
			session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
			id TEXT NOT NULL CHECK(length(id) > 0 AND length(id) <= 256),
			message_id TEXT NOT NULL CHECK(length(message_id) <= 256),
			turn_id TEXT NOT NULL DEFAULT '' CHECK(length(turn_id) <= 256),
			ordinal INTEGER NOT NULL CHECK(ordinal >= 0),
			tool_name TEXT NOT NULL CHECK(length(tool_name) > 0 AND length(tool_name) <= 128),
			input TEXT NOT NULL CHECK(length(input) <= 65536),
			status TEXT NOT NULL CHECK(status IN ('requested', 'approved', 'denied', 'running', 'succeeded', 'failed', 'interrupted', 'unknown')),
			approval TEXT NOT NULL DEFAULT '' CHECK(length(approval) <= 64),
			approval_at INTEGER,
			dispatch_started_at INTEGER,
			result TEXT NOT NULL DEFAULT '' CHECK(length(result) <= 65536),
			result_truncated INTEGER NOT NULL DEFAULT 0,
			is_error INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(session_id, id)
		)`,
		`CREATE INDEX agent_tool_calls_status ON agent_tool_calls(status)`,
		`CREATE INDEX agent_turns_status ON agent_turns(status)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("agent store: apply schema v4: %w", err)
		}
	}
	return nil
}

// applySchemaV5 makes the local session ID the only identity of a session.
// agent_sessions is rebuilt so server_id/opencode_session_id default to ” and
// their uniqueness applies only to legacy rows that still carry a server pair;
// archived_at adds soft archiving. Legacy rows, their messages, tool_activity
// and event_checkpoints are copied unchanged and stay readable as
// non-resumable history: the migration sets legacy = 1 on rows that carry a
// server identity other than the old Console marker.
func applySchemaV5(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE agent_sessions_v5 (
			id TEXT PRIMARY KEY CHECK(length(id) <= 256),
			workspace_root TEXT NOT NULL REFERENCES workspaces(root) ON DELETE CASCADE,
			server_id TEXT NOT NULL DEFAULT '' CHECK(length(server_id) <= 256),
			opencode_session_id TEXT NOT NULL DEFAULT '' CHECK(length(opencode_session_id) <= 256),
			title TEXT NOT NULL DEFAULT '' CHECK(length(title) <= 512),
			agent TEXT NOT NULL DEFAULT '' CHECK(length(agent) <= 128),
			model TEXT NOT NULL DEFAULT '' CHECK(length(model) <= 256),
			status TEXT NOT NULL CHECK(status IN ('queued', 'running', 'completed', 'failed', 'interrupted')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			last_event_sequence INTEGER CHECK(last_event_sequence IS NULL OR last_event_sequence >= 0),
			worktree_root TEXT NOT NULL DEFAULT '' CHECK(length(worktree_root) <= 4096),
			baseline_commit TEXT NOT NULL DEFAULT '' CHECK(length(baseline_commit) <= 128),
			provider_id TEXT NOT NULL DEFAULT '' CHECK(length(provider_id) <= 256),
			model_id TEXT NOT NULL DEFAULT '' CHECK(length(model_id) <= 256),
			archived_at INTEGER,
			legacy INTEGER NOT NULL DEFAULT 0 CHECK(legacy IN (0, 1))
		)`,
		`INSERT INTO agent_sessions_v5 (
			id, workspace_root, server_id, opencode_session_id, title, agent, model, status,
			created_at, updated_at, last_event_sequence, worktree_root, baseline_commit, provider_id, model_id, legacy
		) SELECT
			id, workspace_root, server_id, opencode_session_id, title, agent, model, status,
			created_at, updated_at, last_event_sequence, worktree_root, baseline_commit, provider_id, model_id,
			CASE WHEN server_id <> '' AND server_id <> 'opencode-console' THEN 1 ELSE 0 END
		FROM agent_sessions`,
		`DROP TABLE agent_sessions`,
		`ALTER TABLE agent_sessions_v5 RENAME TO agent_sessions`,
		`CREATE INDEX agent_sessions_workspace_updated ON agent_sessions(workspace_root, updated_at DESC)`,
		`CREATE UNIQUE INDEX agent_sessions_legacy_server_pair ON agent_sessions(server_id, opencode_session_id) WHERE server_id <> '' AND opencode_session_id <> ''`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("agent store: apply schema v5: %w", err)
		}
	}
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("agent store: verify schema v5 foreign keys: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("agent store: schema v5 migration left dangling foreign keys")
	}
	return rows.Err()
}
