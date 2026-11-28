// Package agentstore persists non-secret OpenCode workbench state for one user.
package agentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const (
	maxIDBytes       = 256
	maxWorkspacePath = 4096
	maxTitleBytes    = 512
	maxAgentBytes    = 128
	maxModelBytes    = 256
	maxMessageBytes  = 256 * 1024
	maxToolNameBytes = 128
	maxToolStatus    = 64
	maxToolSummary   = 16 * 1024
	maxToolOutput    = 64 * 1024
)

var ErrNotFound = errors.New("agent store: record not found")

// ErrArchived is returned when a turn is started in an archived session.
var ErrArchived = errors.New("agent store: session is archived")

// ErrLegacySession is returned when a turn is started in a session imported
// from an old OpenCode Server backend; such sessions are read-only history.
var ErrLegacySession = errors.New("agent store: legacy server-backed session is read-only history")

var ErrProfileOwned = errors.New("agent store: profile is already open in another process")

type SessionStatus string

const (
	SessionQueued      SessionStatus = "queued"
	SessionRunning     SessionStatus = "running"
	SessionCompleted   SessionStatus = "completed"
	SessionFailed      SessionStatus = "failed"
	SessionInterrupted SessionStatus = "interrupted"
)

// Session contains display and reconciliation metadata. Credential fields are
// intentionally absent. ID is the local, primary identity. ServerID,
// OpenCodeSessionID and LastEventSequence are retired: they are only populated
// for rows imported before schema v5 and are never required or interpreted
// for Console sessions.
type Session struct {
	ID                string
	WorkspaceRoot     string
	WorktreeRoot      string
	BaselineCommit    string
	ServerID          string
	OpenCodeSessionID string
	Title             string
	Agent             string
	Model             string
	ProviderID        string
	ModelID           string
	Status            SessionStatus
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LastEventSequence *int64
	// Archived sessions stay readable but cannot run turns. ArchivedAt is the
	// zero time when Archived is false.
	Archived   bool
	ArchivedAt time.Time
	// Legacy marks rows imported from an OpenCode Server backend before schema
	// v5. They are readable history only and cannot run turns. Set by the
	// migration; callers cannot create legacy rows.
	Legacy bool
}

// ListOption adjusts ListSessions.
type ListOption func(*listOptions)

type listOptions struct{ includeArchived bool }

// IncludeArchived makes ListSessions return archived sessions too. By default
// they are excluded.
func IncludeArchived() ListOption { return func(o *listOptions) { o.includeArchived = true } }

type Message struct {
	ID                string
	SessionID         string
	OpenCodeMessageID string
	Ordinal           int64
	Role              string
	Body              string
	CreatedAt         time.Time
}

type ToolActivity struct {
	ID             string
	SessionID      string
	OpenCodePartID string
	Ordinal        int64
	ToolName       string
	Status         string
	Summary        string
	Output         string
	UpdatedAt      time.Time
}

type Checkpoint struct {
	SessionID string
	Sequence  int64
	UpdatedAt time.Time
}

type Store struct {
	db          *sql.DB
	path        string
	profileLock *profileLock
}

// OpenUserConfig stores the workbench database below a private child directory
// of the user's application config directory.
func OpenUserConfig(ctx context.Context, configDir string) (*Store, error) {
	if strings.TrimSpace(configDir) == "" {
		return nil, errors.New("agent store: config directory is required")
	}
	dir := filepath.Join(configDir, "agent-workbench")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("agent store: create profile directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("agent store: restrict profile directory: %w", err)
	}
	lock, err := acquireProfileLock(filepath.Join(dir, "runtime.lock"))
	if err != nil {
		return nil, err
	}
	store, err := Open(ctx, filepath.Join(dir, "sessions.sqlite"))
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	store.profileLock = lock
	return store, nil
}

// Open opens or creates a database at path. The parent directory is restricted
// to the current user, and the database file is chmodded after creation.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("agent store: database path is required")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("agent store: resolve database path: %w", err)
	}
	dir := filepath.Dir(absolutePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("agent store: create database directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("agent store: restrict database directory: %w", err)
	}

	db, err := sql.Open("sqlite", absolutePath)
	if err != nil {
		return nil, fmt.Errorf("agent store: open database: %w", err)
	}
	// One connection makes per-connection SQLite settings deterministic and
	// avoids concurrent writers in this small, local-only store.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("agent store: connect to database: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("agent store: enable foreign keys: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(absolutePath, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("agent store: restrict database file: %w", err)
	}
	return &Store{db: db, path: absolutePath}, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	if s.db != nil {
		closeErr = s.db.Close()
	}
	if s.profileLock != nil {
		if err := s.profileLock.Close(); closeErr == nil {
			closeErr = err
		}
		s.profileLock = nil
	}
	return closeErr
}

// CreateSession inserts a local session and its workspace in one transaction.
// Repeating a create for the same local ID (or, for legacy rows, the same
// server/session pair) returns the existing row.
func (s *Store) CreateSession(ctx context.Context, session Session) (Session, error) {
	if err := validateSession(session); err != nil {
		return Session{}, err
	}
	created := session.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	updated := session.UpdatedAt
	if updated.IsZero() {
		updated = created
	}
	if session.Status == "" {
		session.Status = SessionQueued
	}
	session.Title = bounded(session.Title, maxTitleBytes)
	session.Agent = bounded(session.Agent, maxAgentBytes)
	session.Model = bounded(session.Model, maxModelBytes)
	session.ProviderID = bounded(session.ProviderID, maxModelBytes)
	session.ModelID = bounded(session.ModelID, maxModelBytes)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("agent store: begin session transaction: %w", err)
	}
	defer tx.Rollback()

	existing, err := scanSession(tx.QueryRowContext(ctx, sessionSelect+` WHERE id = ?`, session.ID))
	if errors.Is(err, ErrNotFound) && session.ServerID != "" && session.OpenCodeSessionID != "" {
		existing, err = scanSession(tx.QueryRowContext(ctx, sessionSelect+`
			WHERE server_id = ? AND opencode_session_id = ?`, session.ServerID, session.OpenCodeSessionID))
	}
	if err == nil {
		if err := tx.Commit(); err != nil {
			return Session{}, fmt.Errorf("agent store: finish idempotent session lookup: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Session{}, fmt.Errorf("agent store: check existing session: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspaces (root, last_seen_at)
		VALUES (?, ?)
		ON CONFLICT(root) DO UPDATE SET last_seen_at = excluded.last_seen_at`,
		session.WorkspaceRoot, updated.UnixMilli()); err != nil {
		return Session{}, fmt.Errorf("agent store: save workspace: %w", err)
	}

	var sequence any
	if session.LastEventSequence != nil {
		sequence = *session.LastEventSequence
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_sessions (
			id, workspace_root, worktree_root, baseline_commit, server_id, opencode_session_id, title, agent, model,
			status, created_at, updated_at, last_event_sequence, provider_id, model_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		session.ID, session.WorkspaceRoot, session.WorktreeRoot, session.BaselineCommit, session.ServerID, session.OpenCodeSessionID,
		session.Title, session.Agent, session.Model, string(session.Status),
		created.UnixMilli(), updated.UnixMilli(), sequence, session.ProviderID, session.ModelID); err != nil {
		return Session{}, fmt.Errorf("agent store: create session: %w", err)
	}

	stored, err := scanSession(tx.QueryRowContext(ctx, sessionSelect+` WHERE id = ?`, session.ID))
	if err != nil {
		return Session{}, fmt.Errorf("agent store: read created session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("agent store: commit session: %w", err)
	}
	return stored, nil
}

// UpdateSessionMetadata refreshes server-owned display fields without changing
// the local workspace, worktree, baseline, or session identity.
func (s *Store) UpdateSessionMetadata(ctx context.Context, session Session) (Session, error) {
	if err := validateSession(session); err != nil {
		return Session{}, err
	}
	updated := session.UpdatedAt
	if updated.IsZero() {
		updated = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("agent store: begin session metadata update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_sessions
		SET title = CASE WHEN ? = '' THEN title ELSE ? END,
			agent = CASE WHEN ? = '' THEN agent ELSE ? END,
			model = CASE WHEN ? = '' THEN model ELSE ? END,
			provider_id = CASE WHEN ? = '' THEN provider_id ELSE ? END,
			model_id = CASE WHEN ? = '' THEN model_id ELSE ? END,
			status = CASE WHEN ? = '' THEN status ELSE ? END,
			updated_at = CASE WHEN updated_at < ? THEN ? ELSE updated_at END
		WHERE id = ? AND workspace_root = ?`,
		bounded(session.Title, maxTitleBytes), bounded(session.Title, maxTitleBytes),
		bounded(session.Agent, maxAgentBytes), bounded(session.Agent, maxAgentBytes),
		bounded(session.Model, maxModelBytes), bounded(session.Model, maxModelBytes),
		bounded(session.ProviderID, maxModelBytes), bounded(session.ProviderID, maxModelBytes),
		bounded(session.ModelID, maxModelBytes), bounded(session.ModelID, maxModelBytes),
		string(session.Status), string(session.Status), updated.UnixMilli(), updated.UnixMilli(),
		session.ID, session.WorkspaceRoot)
	if err != nil {
		return Session{}, fmt.Errorf("agent store: update session metadata: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Session{}, fmt.Errorf("agent store: confirm session metadata update: %w", err)
	}
	if count == 0 {
		return Session{}, ErrNotFound
	}
	stored, err := scanSession(tx.QueryRowContext(ctx, sessionSelect+` WHERE id = ?`, session.ID))
	if err != nil {
		return Session{}, fmt.Errorf("agent store: read updated session metadata: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("agent store: commit session metadata: %w", err)
	}
	return stored, nil
}

// ListSessions returns sessions for one workspace. An empty root lists all
// rows. Archived sessions are excluded unless IncludeArchived is passed.
func (s *Store) ListSessions(ctx context.Context, workspaceRoot string, opts ...ListOption) ([]Session, error) {
	var o listOptions
	for _, opt := range opts {
		opt(&o)
	}
	query := sessionSelect
	args := []any{}
	var where []string
	if workspaceRoot != "" {
		where = append(where, `workspace_root = ?`)
		args = append(args, workspaceRoot)
	}
	if !o.includeArchived {
		where = append(where, `archived_at IS NULL`)
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY updated_at DESC, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("agent store: list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("agent store: scan session: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agent store: list sessions: %w", err)
	}
	return sessions, nil
}

// AppendMessage inserts or updates a backend message. Bodies are bounded to
// keep local history from growing without limit due to one unusually large row.
func (s *Store) AppendMessage(ctx context.Context, message Message) error {
	if err := validateMessage(message); err != nil {
		return err
	}
	created := message.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_messages (
			id, session_id, opencode_message_id, ordinal, role, body, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, opencode_message_id) DO UPDATE SET
			ordinal = excluded.ordinal, role = excluded.role,
			body = excluded.body, created_at = excluded.created_at`,
		message.ID, message.SessionID, message.OpenCodeMessageID,
		message.Ordinal, message.Role, bounded(message.Body, maxMessageBytes), created.UnixMilli())
	if err != nil {
		return fmt.Errorf("agent store: save message: %w", err)
	}
	return nil
}

func (s *Store) ListMessages(ctx context.Context, sessionID string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, opencode_message_id, ordinal, role, body, created_at
		FROM agent_messages WHERE session_id = ? ORDER BY ordinal, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent store: list messages: %w", err)
	}
	defer rows.Close()
	var messages []Message
	for rows.Next() {
		var message Message
		var created int64
		if err := rows.Scan(&message.ID, &message.SessionID, &message.OpenCodeMessageID,
			&message.Ordinal, &message.Role, &message.Body, &created); err != nil {
			return nil, fmt.Errorf("agent store: scan message: %w", err)
		}
		message.CreatedAt = time.UnixMilli(created).UTC()
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agent store: list messages: %w", err)
	}
	return messages, nil
}

func (s *Store) UpsertToolActivity(ctx context.Context, activity ToolActivity) error {
	if err := validateToolActivity(activity); err != nil {
		return err
	}
	updated := activity.UpdatedAt
	if updated.IsZero() {
		updated = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tool_activity (
			id, session_id, opencode_part_id, ordinal, tool_name, status,
			summary, output, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, opencode_part_id) DO UPDATE SET
			ordinal = excluded.ordinal, tool_name = excluded.tool_name,
			status = excluded.status, summary = excluded.summary,
			output = excluded.output, updated_at = excluded.updated_at`,
		activity.ID, activity.SessionID, activity.OpenCodePartID, activity.Ordinal,
		bounded(activity.ToolName, maxToolNameBytes), bounded(activity.Status, maxToolStatus),
		bounded(activity.Summary, maxToolSummary), bounded(activity.Output, maxToolOutput), updated.UnixMilli())
	if err != nil {
		return fmt.Errorf("agent store: save tool activity: %w", err)
	}
	return nil
}

func (s *Store) ListToolActivity(ctx context.Context, sessionID string) ([]ToolActivity, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, opencode_part_id, ordinal, tool_name, status,
			summary, output, updated_at
		FROM tool_activity WHERE session_id = ? ORDER BY ordinal, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent store: list tool activity: %w", err)
	}
	defer rows.Close()
	var activities []ToolActivity
	for rows.Next() {
		var activity ToolActivity
		var updated int64
		if err := rows.Scan(&activity.ID, &activity.SessionID, &activity.OpenCodePartID,
			&activity.Ordinal, &activity.ToolName, &activity.Status,
			&activity.Summary, &activity.Output, &updated); err != nil {
			return nil, fmt.Errorf("agent store: scan tool activity: %w", err)
		}
		activity.UpdatedAt = time.UnixMilli(updated).UTC()
		activities = append(activities, activity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agent store: list tool activity: %w", err)
	}
	return activities, nil
}

// SaveCheckpoint stores only a durable sequence number. Raw event payloads are
// deliberately not part of this database contract.
func (s *Store) SaveCheckpoint(ctx context.Context, checkpoint Checkpoint) error {
	if strings.TrimSpace(checkpoint.SessionID) == "" || len(checkpoint.SessionID) > maxIDBytes {
		return errors.New("agent store: checkpoint session ID is required and must be at most 256 bytes")
	}
	if checkpoint.Sequence < 0 {
		return errors.New("agent store: checkpoint sequence cannot be negative")
	}
	updated := checkpoint.UpdatedAt
	if updated.IsZero() {
		updated = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("agent store: begin checkpoint transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO event_checkpoints (session_id, sequence, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			sequence = excluded.sequence, updated_at = excluded.updated_at`,
		checkpoint.SessionID, checkpoint.Sequence, updated.UnixMilli()); err != nil {
		return fmt.Errorf("agent store: save checkpoint: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_sessions SET last_event_sequence = ?, updated_at = ? WHERE id = ?`,
		checkpoint.Sequence, updated.UnixMilli(), checkpoint.SessionID); err != nil {
		return fmt.Errorf("agent store: update session checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("agent store: commit checkpoint: %w", err)
	}
	return nil
}

func (s *Store) GetCheckpoint(ctx context.Context, sessionID string) (Checkpoint, error) {
	var checkpoint Checkpoint
	var updated int64
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id, sequence, updated_at
		FROM event_checkpoints WHERE session_id = ?`, sessionID).
		Scan(&checkpoint.SessionID, &checkpoint.Sequence, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Checkpoint{}, ErrNotFound
	}
	if err != nil {
		return Checkpoint{}, fmt.Errorf("agent store: get checkpoint: %w", err)
	}
	checkpoint.UpdatedAt = time.UnixMilli(updated).UTC()
	return checkpoint, nil
}

const sessionSelect = `
	SELECT id, workspace_root, worktree_root, baseline_commit, server_id, opencode_session_id, title, agent, model,
		status, created_at, updated_at, last_event_sequence, provider_id, model_id, archived_at, legacy
	FROM agent_sessions`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(row rowScanner) (Session, error) {
	var session Session
	var created, updated int64
	var sequence, archived sql.NullInt64
	var legacy int
	err := row.Scan(
		&session.ID, &session.WorkspaceRoot, &session.WorktreeRoot, &session.BaselineCommit, &session.ServerID, &session.OpenCodeSessionID,
		&session.Title, &session.Agent, &session.Model, &session.Status,
		&created, &updated, &sequence, &session.ProviderID, &session.ModelID, &archived, &legacy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	session.CreatedAt = time.UnixMilli(created).UTC()
	session.UpdatedAt = time.UnixMilli(updated).UTC()
	if sequence.Valid {
		session.LastEventSequence = &sequence.Int64
	}
	session.Legacy = legacy == 1
	if archived.Valid {
		session.Archived = true
		session.ArchivedAt = time.UnixMilli(archived.Int64).UTC()
	}
	return session, nil
}

func validateSession(session Session) error {
	if err := requiredID("session ID", session.ID); err != nil {
		return err
	}
	// Server identity is optional: only the local session ID is required.
	if len(session.OpenCodeSessionID) > maxIDBytes || len(session.ServerID) > maxIDBytes {
		return errors.New("agent store: legacy server fields must be at most 256 bytes")
	}
	if strings.Contains(session.ServerID, "://") || strings.Contains(session.ServerID, "@") {
		return errors.New("agent store: server ID must be an opaque local identifier, not a URL")
	}
	if strings.TrimSpace(session.WorkspaceRoot) == "" || len(session.WorkspaceRoot) > maxWorkspacePath {
		return errors.New("agent store: workspace root is required and must be at most 4096 bytes")
	}
	if len(session.WorktreeRoot) > maxWorkspacePath {
		return errors.New("agent store: worktree root must be at most 4096 bytes")
	}
	if len(session.BaselineCommit) > 128 {
		return errors.New("agent store: baseline commit must be at most 128 bytes")
	}
	if session.LastEventSequence != nil && *session.LastEventSequence < 0 {
		return errors.New("agent store: event sequence cannot be negative")
	}
	switch session.Status {
	case "", SessionQueued, SessionRunning, SessionCompleted, SessionFailed, SessionInterrupted:
	default:
		return fmt.Errorf("agent store: invalid session status %q", session.Status)
	}
	return nil
}

func validateMessage(message Message) error {
	if err := requiredID("message ID", message.ID); err != nil {
		return err
	}
	if err := requiredID("session ID", message.SessionID); err != nil {
		return err
	}
	if err := requiredID("OpenCode message ID", message.OpenCodeMessageID); err != nil {
		return err
	}
	if message.Ordinal < 0 {
		return errors.New("agent store: message ordinal cannot be negative")
	}
	if strings.TrimSpace(message.Role) == "" || len(message.Role) > 32 {
		return errors.New("agent store: message role is required and must be at most 32 bytes")
	}
	return nil
}

func validateToolActivity(activity ToolActivity) error {
	if err := requiredID("tool activity ID", activity.ID); err != nil {
		return err
	}
	if err := requiredID("session ID", activity.SessionID); err != nil {
		return err
	}
	if err := requiredID("OpenCode part ID", activity.OpenCodePartID); err != nil {
		return err
	}
	if activity.Ordinal < 0 {
		return errors.New("agent store: tool activity ordinal cannot be negative")
	}
	if strings.TrimSpace(activity.ToolName) == "" || len(activity.ToolName) > maxToolNameBytes {
		return errors.New("agent store: tool name is required and must be at most 128 bytes")
	}
	if strings.TrimSpace(activity.Status) == "" || len(activity.Status) > maxToolStatus {
		return errors.New("agent store: tool status is required and must be at most 64 bytes")
	}
	return nil
}

func requiredID(name, value string) error {
	if strings.TrimSpace(value) == "" || len(value) > maxIDBytes {
		return fmt.Errorf("agent store: %s is required and must be at most 256 bytes", name)
	}
	return nil
}

func bounded(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

// RenameSession sets a user-chosen title. The title is trimmed, must be
// non-empty, and is bounded to 512 bytes.
func (s *Store) RenameSession(ctx context.Context, sessionID, title string, at time.Time) error {
	if err := requiredID("session ID", sessionID); err != nil {
		return err
	}
	title = bounded(strings.TrimSpace(title), maxTitleBytes)
	if title == "" {
		return errors.New("agent store: session title is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE agent_sessions SET title = ?, updated_at = MAX(updated_at, ?) WHERE id = ?`,
		title, at.UnixMilli(), sessionID)
	if err != nil {
		return fmt.Errorf("agent store: rename session: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("agent store: confirm session rename: %w", err)
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ArchiveSession archives or restores a session. Archived sessions are hidden
// from the default ListSessions result and cannot begin turns. A running
// session cannot be archived (ErrConflict); the call is idempotent otherwise
// and does not move the original archive time.
func (s *Store) ArchiveSession(ctx context.Context, sessionID string, archived bool, at time.Time) error {
	if err := requiredID("session ID", sessionID); err != nil {
		return err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("agent store: begin archive: %w", err)
	}
	defer tx.Rollback()
	var status string
	var current sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT status, archived_at FROM agent_sessions WHERE id = ?`, sessionID).Scan(&status, &current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("agent store: read session for archive: %w", err)
	}
	if archived == current.Valid {
		return nil
	}
	if archived && status == string(SessionRunning) {
		return ErrConflict
	}
	var value any
	if archived {
		value = at.UnixMilli()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_sessions SET archived_at = ?, updated_at = MAX(updated_at, ?) WHERE id = ?`,
		value, at.UnixMilli(), sessionID); err != nil {
		return fmt.Errorf("agent store: archive session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("agent store: commit archive: %w", err)
	}
	return nil
}
