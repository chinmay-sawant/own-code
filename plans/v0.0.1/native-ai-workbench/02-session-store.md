# Local session storage and restoration

**Goal:** Keep own-code-created sessions, conversation transcripts, and tool activity in a per-user SQLite database so the workbench can restore locally after a restart.

## Phase 1: Define the SQLite boundary

- [x] Use a per-user database under the own-code config directory, separate from project files and `settings.json`.
- [x] Keep session identity local to own-code. A local session ID is the primary identifier; do not require or persist an OpenCode Server session ID or server identity. Evidence: schema v5 (`applySchemaV5`) rebuilds `agent_sessions` with optional `server_id`/`opencode_session_id`; `validateSession` requires only the local ID; `UpdateSessionMetadata` matches on `id` + workspace; `TestSessionsDoNotRequireServerIdentity`, `TestLegacyServerSessionsStayReadableButCannotRunTurns`. The app no longer writes server fields (`opencode_panel.go`), and the server-history code (`openCodeSessionHistory`, remote sync) was removed.
- [x] Select and pin a Go SQLite driver after checking supported desktop platforms, CGO requirements, and release packaging. Evidence: `modernc.org/sqlite` (pure Go, no CGO) pinned in go.mod.
- [x] Store workspace root, optional private worktree and baseline commit, selected provider/model IDs, title, lifecycle state, and timestamps for each local session. Evidence: `agentstore.Session` fields; `TestOpenMigratesV1DatabaseAndPreservesOptionalWorktreeRoot`.
- [x] Store user/assistant transcript entries in display order with local turn/message IDs. Store own-code tool calls and bounded results with their local execution status. Evidence: `agentstore/turns.go`; `TestTurnRoundTripWithToolCallsAndRecovery`.
- [x] Never store the Console API key, bearer token, authorization header, or any secret in SQLite. Keep the credential in the separate credential store defined in [01-opencode-connection.md](01-opencode-connection.md). Evidence: no credential fields in schema; `redactSecrets`; `TestBoundsAndRedaction`.
- [x] Set owner-only permissions on the database directory and file. Use transactions for session creation, transcript updates, and tool-state updates. Evidence: `TestDatabasePermissions`; store writes use transactions.
- [x] Define limits for retained message bodies, tool summaries, context snapshots, and database size. Do not retain unbounded raw provider payloads. Limits: message body 256 KiB, message part text 256 KiB, tool input 64 KiB, tool result 64 KiB (truncation marker persisted), legacy tool summary 16 KiB, title 512 B, turn error 4 KiB; the database file is capped at 65536 pages (`MaxDatabaseBytes`, 256 MiB at the default 4 KiB page) via `PRAGMA max_page_count`, so writes past the cap fail instead of growing the profile. Context snapshots and raw provider payloads are not stored. There is no automatic pruning or retention window; sessions stay until deleted, and archiving is the way to hide them. A full database surfaces as a write error on the next turn.
- [x] Define how deleted projects, archived sessions, and unavailable historical models appear in the local session list. Definition: sessions are listed per workspace root, so a deleted or moved project simply has no list to appear in and its rows stay in SQLite untouched. Archived sessions (`archived_at`, set by `ArchiveSession`, reversible) are hidden from `ListSessions` unless `IncludeArchived()` is passed; the UI should show them in a separate Archived group with a Restore action, keep the transcript readable, and `BeginTurn` returns `ErrArchived`. A running session cannot be archived (`ErrConflict`). Sessions whose saved provider/model is no longer in the catalog (or whose key is missing) stay listed and readable with a muted "model unavailable" note; continuing requires choosing an eligible model (`resolveOpenCodeSessionModel` rejects the stale one at request time). Pre-v5 server-backed rows have `Session.Legacy = true`, list as "Imported history (read-only)", and `BeginTurn` returns `ErrLegacySession`. Store semantics are tested (`TestArchiveSessionHidesListsAndBlocksTurns`, `TestRenameSession`); the panel rendering of these states is a separate UI task.

### Illustrative Go and SQLite shapes

These sketches describe local own-code state. They are not a committed schema or final exported API. There is no remote OpenCode session ID.

```go
type SessionStatus string

const (
	SessionQueued     SessionStatus = "queued"
	SessionRunning    SessionStatus = "running"
	SessionCompleted  SessionStatus = "completed"
	SessionFailed     SessionStatus = "failed"
	SessionInterrupted SessionStatus = "interrupted"
)

type AgentSession struct {
	ID             string // Local own-code session ID.
	WorkspaceRoot  string
	WorktreeRoot   string
	BaselineCommit string
	ProviderID     string
	ModelID        string
	Title          string
	Status         SessionStatus
	CreatedAt      time.Time
	UpdatedAt      time.Time
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
    provider_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE agent_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    role TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (session_id, ordinal)
);

CREATE TABLE tool_activity (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    turn_id TEXT NOT NULL,
    tool_call_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    tool_name TEXT NOT NULL,
    status TEXT NOT NULL,
    input_summary TEXT NOT NULL DEFAULT '',
    output_summary TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL,
    UNIQUE (session_id, tool_call_id)
);
```

**Gate:** Migration and storage behavior has isolated database fixtures. Permission checks pass on supported platforms, and secret-leak checks prove the Console key cannot enter any database row.

## Phase 2: Persist local session state

- [x] Create an own-code session ID and insert the session before sending its first inference request. The panel can create a local session before the not-yet-implemented inference step.
- [x] Save user and assistant messages in display order. Save tool calls/results only after own-code validates the call and applies the permission decision. Evidence: `TestApprovedExecutionPersistsApprovalBeforeDispatch`, `TestDenialPersistedAndNotRun`.
- [x] Persist enough run state to distinguish queued, active, completed, failed, and interrupted turns after restart. Do not treat a process-local stream position as a durable remote checkpoint. Evidence: `RecoverInterrupted`; `TestTurnRoundTripWithToolCallsAndRecovery`.
- [x] Make session creation and initial user-message insertion idempotent so retries do not duplicate a prompt. Evidence: `TestIdempotentRetriesDoNotDuplicate`.
- [ ] Keep the existing editor `sessionState` focused on editor layout and navigation. Do not embed transcripts or credentials into `session-*.json`.
- [x] Migrate or retire any prior server-specific IDs, event checkpoints, and connection identities without losing locally stored transcript history. Evidence: `agentstore/migrate.go`; `TestMigrationFromV3PreservesLegacyTranscript`.

**Gate:** SQLite round-trip covers multiple workspaces and sessions, interrupted writes, schema migration, duplicate create retries, archive state, bounded transcript/tool data, and secret exclusion.

## Phase 3: Restore after restart

- [x] Reopen the local session list and transcript at startup without requiring a network request. Evidence: `loadOpenCodeCachedSessions` reads SQLite only.
- [ ] Show whether a session can continue with the currently configured key and model; retain readable local history if the key or model is unavailable.
- [x] Continue a selected session by rebuilding the inference conversation from its local transcript and tool results. Do not request remote session history. Evidence: `Store.LoadHistory` feeds the runner.
- [x] Reconcile any interrupted local tool/run state explicitly before allowing continuation; do not silently re-run a command or file mutation after restart. Evidence: `TestRecoveryNeverRedispatches`.
- [x] Isolate concurrent updates by local session ID and workspace root. Evidence: `TestConcurrentSessionsDoNotMix`.

**Gate:** Close and reopen own-code, restore the visible session list and selected transcript offline, and continue a selected session only after verifying the workspace, credential, model, and pending tool state.
