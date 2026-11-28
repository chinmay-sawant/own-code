# Local agent sessions panel

**Goal:** Show own-code-managed sessions, model responses, and local tool activity inside the existing workspace.

**Implementation status:** Settings can load and select the Console catalog. The panel lists locally persisted SQLite sessions, creates a local session, and reads its local transcript without connecting to `opencode serve`. Prompt send, streaming, stop/retry, the agent loop, tool permissions, and tool execution are implemented (see ticked items). Rename/archive, per-turn provider/model display, and the offline/credential-state indicator remain open. Console inference does not create remote agent sessions or provide a server event stream; session and turn state belong to own-code.

## Phase 1: Manage local sessions

- [x] Add a session list for the selected workspace with create, select, rename, archive, and resume actions backed by the local SQLite store. Evidence: `opencode_session_actions.go` (`saveOpenCodeRename`, `archiveOpenCodeSession`, `unarchiveOpenCodeSession`) over `Store.RenameSession/ArchiveSession`; `TestPanelGapsRenameArchiveAndPanelRenders` (rename, archive, restore round trip and template redraw). Not visually exercised in a live window.
- [ ] (State labels implemented in `openCodeSessionState`: Running/Waiting for approval/Stopping/Completed/Failed/Interrupted/Ready/Disconnected; no dedicated assertion yet.) Show active, queued, completed, failed, interrupted, and disconnected states from own-code's local run lifecycle.
- [x] Let the user start more than one local session and switch between them without mixing messages, tool calls, or workspace state. Evidence: `TestRunEventsDoNotCrossSessions`, `TestConcurrentSessionsDoNotMix`.
- [x] Show cached SQLite history while offline, with a clear indication that a new inference request needs a working Console credential and model. Evidence: `openCodeRequestNotice` banner (`#opencode-notice`); `TestPanelGapsOfflineNoticeAndWorkspaceSwitchConfirmation`.
- [x] Do not automatically change the current workspace when a user selects a saved session. Ask for or visibly confirm a workspace switch. Evidence: "Other workspaces" list, `requestOpenCodeWorkspaceSwitch` shows `#opencode-switch` confirm/cancel banner; workspace changes only in `confirmOpenCodeWorkspaceSwitch`; `TestPanelGapsOfflineNoticeAndWorkspaceSwitchConfirmation`.

**Gate:** Session action checks cover create, list, switch, resume from local transcript, archive, workspace mismatch, and disconnected/invalid-credential states.

## Phase 2: Run and render a turn

- [ ] (Session list now shrinks, flex 110px-38%; not verified at the minimum window size.) Add a native agent panel that fits the existing OwnFrame layout and remains usable at the minimum supported window size.
- [x] Add a prompt composer and send action for the selected local session and catalog model. Evidence: `submitOpenCodePrompt` in `internal/app/opencode_run.go`.
- [x] Build each inference request from the selected model's documented protocol and the session's local conversation history. Evidence: `agent.ResolveModel` + `Store.LoadHistory`.
- [ ] (Done: incremental Markdown subset with HTML escaping, split UTF-8 and empty deltas: `renderOpenCodeMarkdown`, `openCodeRun.appendDelta`; `TestPanelGapsStreamedUTF8AndEmptyDeltas`, `TestPanelGapsMarkdownEscapes`. Per-protocol malformed-frame coverage lives in the agent package, not re-verified here.) Render streamed Markdown output incrementally. Handle split UTF-8 chunks, empty deltas, malformed frames, provider errors, cancellation, and failed turns for each supported protocol.
- [x] Preserve transcript scroll position while the user reads earlier messages. Follow new output only when the user is already at the end. Evidence: `openCodeTranscriptScroll` (app-owned offset, wheel handled in `openCodeWheelAt`); `TestPanelGapsTranscriptScrollFollowsOnlyAtEnd`. The model is tested; wheel and live layout measurement are not exercised in a real window.
- [x] Show provider/model, elapsed state, and execution result for each turn. Evidence: persisted `turnmeta-*` rows (`openCodeTurnSummary`) and live turn line; `TestPanelGapsLiveToolRowsAndTurnSummary`. Live elapsed refreshes only when events arrive.
- [x] Add stop/cancel and retry actions. A partial or interrupted response must not be treated as a completed turn. Evidence: `interruptOpenCodeSession`, `retryOpenCodeTurn`; `TestApprovalAndStop`, `TestRetryNeedsRetryText`, `TestCancelDuringStreamInterruptsTurn`.
- [x] Keep HTTP, stream parsing, persistence, and agent coordination off the UI loop. Drop stale results from sessions the user has switched away from. Evidence: `executeOpenCodeTurn` runs in a goroutine; `TestRunEventStreamingAndStale`.

**Gate:** Fake inference streams cover chunking, errors, cancellation, interleaved local sessions, stale results, and restart restoration. A live response renders incrementally in the native window.

## Phase 3: Own the agent and tool loop

- [x] Implement the local loop that sends model requests, detects tool calls, records assistant/tool messages, and continues inference with tool results. Evidence: `agent/loop.go` Runner; `TestMultipleToolStepsRebuildHistory`.
- [x] Define a small own-code tool registry. Validate tool names and typed arguments before dispatch; do not execute arbitrary model-provided commands or paths. Evidence: `internal/agenttools/registry.go`; `TestInvalidInputRejectedBeforeExecutor`, `TestUnknownInvalidAndOversizedNeverExecute`.
- [x] Add an explicit permission policy and user approval UI for tools with side effects. Denial, cancellation, and approval must be stored in local turn state. Evidence: `agenttools/policy.go`; `TestPolicyMatrix`, `TestApprovalAndStop`, `TestDenialPersistedAndNotRun`.
- [x] Execute approved tools through own-code's workspace and process boundaries. Revalidate paths and workspace identity at execution time. Evidence: `TestSymlinkEscapeRejectedAtExecution`, `TestShellNeverRunsWithoutValidApproval`.
- [x] Render tool calls in order with name, bounded input summary, running/succeeded/failed/denied state, and bounded output. Evidence: `openCodeToolRow`; `TestTranscriptProjectionInterruptedAndInspection`.
- [x] Keep secrets, raw authorization headers, and unbounded request/response payloads out of local tool history and logs. Evidence: `TestBoundsAndRedaction`, `TestHTTPErrorsNoKeyLeak`.

**Gate:** Fixtures cover tool-call decoding, invalid arguments, unknown tools, approval, denial, cancellation, success, failure, restart during execution, and out-of-workspace paths. No model response can cause tool execution without the own-code permission path.
