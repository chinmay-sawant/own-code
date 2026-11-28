package agentstore

import (
	"context"
	"database/sql"
	"fmt"
)

const schemaVersion = 3

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `PRAGMA max_page_count = 65536`); err != nil {
		return fmt.Errorf("agent store: set database size limit: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode = DELETE`); err != nil {
		return fmt.Errorf("agent store: set journal mode: %w", err)
	}

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
