# Session storage and restoration

**Goal:** Keep a searchable list of OpenCode sessions and enough local history to restore the workbench after own-code restarts.

## Phase 1: Define the SQLite boundary

- [x] Use a per-user database under the own-code config directory, separate from project files and `settings.json`.
- [ ] Select and pin a Go SQLite driver after checking supported desktop platforms, CGO requirements, and release packaging.
- [x] Create migrations for workspaces, agent sessions, messages, tool activity, and the last synchronized event position. Store the OpenCode session ID and workspace root as explicit fields.
- [x] Persist only non-secret session data. Never store provider API keys, server passwords, bearer tokens, or raw authorization headers in SQLite.
- [ ] Set owner-only permissions on the database directory and file. Use transactions for session creation, message updates, and event checkpoints.
- [x] Define limits for retained message bodies, tool logs, event history, and database size. Do not retain unbounded raw event payloads.
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
	WorktreeRoot       string // Private checkout used for an isolated OpenCode session.
	BaselineCommit     string // Full commit ID used to compare the proposal.
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
    worktree_root TEXT NOT NULL DEFAULT '',
    baseline_commit TEXT NOT NULL DEFAULT '',
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
- [x] Make session creation and initial database insertion idempotent so a retry does not create duplicate local sessions.
- [x] Keep the existing editor `sessionState` focused on editor layout and navigation. Do not embed transcripts or secrets into `session-*.json`.

**Gate:** SQLite round-trip covers multiple workspaces and sessions, interrupted writes, schema migration, duplicate create retries, archive state, and secret exclusion.

## Phase 3: Restore after restart

- [ ] Reopen the local session list at startup without requiring an OpenCode request to render the editor.
- [ ] Mark each row as available, disconnected, archived, or missing on the server after a bounded status refresh.
- [ ] Resume a selected session by backend session ID and reload messages from OpenCode before accepting new prompts.
- [ ] Reconcile cached transcript and tool rows with server messages after a disconnect. If an experimental durable event log is used, gate it by the supported OpenCode version.
- [x] Isolate interleaved updates by both workspace root and backend session ID.

**Gate:** Close and reopen own-code, reconnect to the same server, and restore the visible session list and selected transcript without duplicating messages or cross-loading another workspace's data.

**Evidence (2026-11-28):** `go test ./internal/agentstore` passed. `go test ./internal/app -run '^Test(OpenCodeSessionHistory|ProjectOpenCodeTextMessage|SyncOpenCodeMessages)' -count=1` passed. The SQLite migration creates workspace, session, message, tool, and checkpoint tables; the tests cover migration/reopen, scoped workspaces, duplicate session imports, bounded messages/tool data, checkpoint round-trip, and absence of secret-named columns. The app adapter imports sessions only when the server directory matches the canonical selected workspace and stores only user/assistant text message parts for the matching server/session.

**Remaining gate:** Driver support and packaging still need explicit supported-platform evidence. The permissions test skips Windows, where no explicit user-only ACL is set. Archive/deleted-project behavior, interrupted-write and archive fixtures, and close/reopen with a live OpenCode server remain unverified. Agent/model metadata and tool activity are represented in storage but are not yet fully synchronized by the app. OpenCode's current event stream is volatile, so no durable reconnect checkpoint is wired; do not mark it replayable.

**Implementation note (2026-11-28):** Agentstore schema v2 adds `agent_sessions.worktree_root`; schema v3 adds `baseline_commit`, both through transactional `ALTER TABLE` migrations with empty defaults for old sessions. New isolated OpenCode sessions persist the private worktree path and full base commit alongside the original workspace and remote session ID. App integration maps known worktree sessions back to the selected workspace and verifies persisted worktrees before listing them. Older rows without a baseline can still load transcripts, but proposal review stays unavailable. Session metadata updates preserve local worktree identity while refreshing title, timestamps, and status; the app queries OpenCode's active-session endpoint before assigning running/completed states. This integration has only passed `go build ./...`; migration and restart fixtures were not rerun after schema v3. Remaining checklist rows and gates stay open pending platform, permissions, migration, and restart evidence.
