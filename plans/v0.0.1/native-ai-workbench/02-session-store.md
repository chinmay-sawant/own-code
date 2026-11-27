# Session storage and restoration

**Goal:** Keep a searchable list of OpenCode sessions and enough local history to restore the workbench after own-code restarts.

## Phase 1: Define the SQLite boundary

- [ ] Use a per-user database under the own-code config directory, separate from project files and `settings.json`.
- [ ] Select and pin a Go SQLite driver after checking supported desktop platforms, CGO requirements, and release packaging.
- [ ] Create migrations for workspaces, agent sessions, messages, tool activity, and the last synchronized event position. Store the OpenCode session ID and workspace root as explicit fields.
- [ ] Persist only non-secret session data. Never store provider API keys, server passwords, bearer tokens, or raw authorization headers in SQLite.
- [ ] Set owner-only permissions on the database directory and file. Use transactions for session creation, message updates, and event checkpoints.
- [ ] Define limits for retained message bodies, tool logs, event history, and database size. Do not retain unbounded raw event payloads.
- [ ] Define how deleted projects and archived OpenCode sessions appear in the local session list.

### Illustrative Go and SQLite shapes

These snippets show the ownership boundary and likely persisted fields. They are not a committed schema or final exported API. In particular, provider credentials and server passwords do not belong in these types or tables.

```go
type SessionStatus string

const (
	SessionQueued     SessionStatus = "queued"
	SessionRunning   SessionStatus = "running"
	SessionCompleted SessionStatus = "completed"
	SessionFailed    SessionStatus = "failed"
	SessionInterrupted SessionStatus = "interrupted"
)

type AgentSession struct {
	ID                 string
	WorkspaceRoot      string
	ServerID           string // Stable local identifier. Never include URL credentials.
	OpenCodeSessionID  string
	Title              string
	Status             SessionStatus
	CreatedAt          time.Time
	UpdatedAt          time.Time
	LastEventSequence  *int64
}

type SessionStore interface {
	List(ctx context.Context, workspaceRoot string) ([]AgentSession, error)
	Create(ctx context.Context, session AgentSession) error
	AppendMessage(ctx context.Context, message AgentMessage) error
	UpsertToolActivity(ctx context.Context, activity ToolActivity) error
	Close() error
}
```

```sql
CREATE TABLE agent_sessions (
    id TEXT PRIMARY KEY,
    workspace_root TEXT NOT NULL,
    server_id TEXT NOT NULL,
    opencode_session_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_event_sequence INTEGER,
    UNIQUE (server_id, opencode_session_id)
);

CREATE TABLE agent_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    backend_message_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    role TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (session_id, backend_message_id),
    UNIQUE (session_id, ordinal)
);

CREATE TABLE tool_activity (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    backend_part_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    tool_name TEXT NOT NULL,
    status TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL,
    UNIQUE (session_id, backend_part_id)
);
```

**Gate:** Migration and storage behavior has isolated database fixtures. Permission checks pass on supported platforms, and secret-leak checks prove credentials do not enter any database row.

## Phase 2: Persist session state

- [ ] Save the workspace root, OpenCode server identity, backend session ID, title, agent/model selection, timestamps, and last known state.
- [ ] Save user and assistant messages in display order. Save tool activity with session, turn, tool, status, and bounded output references.
- [ ] Save a reconnect checkpoint only when the selected OpenCode API exposes a durable sequence. Do not treat `/api/event` as replayable.
- [ ] Make session creation and initial database insertion idempotent so a retry does not create duplicate local sessions.
- [ ] Keep the existing editor `sessionState` focused on editor layout and navigation. Do not embed transcripts or secrets into `session-*.json`.

**Gate:** SQLite round-trip covers multiple workspaces and sessions, interrupted writes, schema migration, duplicate create retries, archive state, and secret exclusion.

## Phase 3: Restore after restart

- [ ] Reopen the local session list at startup without requiring an OpenCode request to render the editor.
- [ ] Mark each row as available, disconnected, archived, or missing on the server after a bounded status refresh.
- [ ] Resume a selected session by backend session ID and reload messages from OpenCode before accepting new prompts.
- [ ] Reconcile cached transcript and tool rows with server messages after a disconnect. If an experimental durable event log is used, gate it by the supported OpenCode version.
- [ ] Isolate interleaved updates by both workspace root and backend session ID.

**Gate:** Close and reopen own-code, reconnect to the same server, and restore the visible session list and selected transcript without duplicating messages or cross-loading another workspace's data.
