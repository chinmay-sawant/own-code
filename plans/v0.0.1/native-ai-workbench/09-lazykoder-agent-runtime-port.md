# Native AI workbench: adapt LazyCoder's agent runtime

> **Parent:** `plans/v0.0.1/native-ai-workbench/README.md`
> **Status:** Implementation in progress. No runtime phase gate has passed yet.
> **Source baseline:** LazyCoder `master` at `816e2113a54f659f779f2c8592b266011829492c`, clean and equal to `origin/master` when reviewed.
> **Estimated effort:** Re-estimate after Phase 1 fixes the package contracts and initial tool set.

## Overview

Use the pinned LazyCoder source as a one-time reference for the model loop, streaming, tool dispatch, approval flow, and session-history behavior. Adapt selected patterns into own-code's `internal/` packages. Do not add LazyCoder as a Go module or vendor the application, and do not set up ongoing source sync. Own-code's provider, storage, UI, and tests remain authoritative. Keep the existing OwnFrame UI and direct OpenCode Console API path. Do not add an OpenCode Server dependency.

This is an implementation plan only. No LazyCoder runtime code has been copied into own-code.

LazyCoder's database is project-local at `.lazykoder/lazykoder.db` and stores normalized sessions, messages, parts, and tool calls. Own-code already has a per-user SQLite store at `agent-workbench/sessions.sqlite`, with `agent_sessions`, `agent_messages`, `tool_activity`, and `event_checkpoints`. The current schema also carries OpenCode Server IDs and event cursors. Keep own-code's store as the only source of truth. Extend it with versioned migrations where its current rows cannot represent streamed messages, tool calls, approvals, or local resume state. Do not introduce a second runtime database.

## Current implementation

- `internal/opencode/console.go` loads the Console model catalog and retains model protocol metadata. It does not send inference requests.
- `internal/secretstore/opencode_api_key.go` stores the Console key outside settings and SQLite.
- `internal/agentstore` owns the per-user SQLite file, migrations, local sessions, transcript messages, tool activity, and checkpoints.
- `internal/app/opencode_panel.go` lists and creates local sessions and reads their local transcript. Prompt submission remains paused.
- The native panel uses OwnFrame. LazyCoder's Bubble Tea UI is not part of this port.

## LazyCoder source map

| LazyCoder source | Behavior to adapt |
| --- | --- |
| `internal/provider/opencode/client.go`, `stream.go`, `responses.go` | OpenCode Go/Zen routing, Chat Completions and Responses calls, streaming, usage, and retries. Reuse generic transport and parser patterns only. Rebuild Console auth and routes from current docs; do not copy the Go/Zen endpoint assumptions. |
| `internal/agent/agent.go`, `stream.go`, `history.go` | Turn lifecycle, provider/tool loop, local history reconstruction, and database-backed stream deltas. |
| `internal/agent/tools_registry.go`, `tools_exec.go` | Tool descriptions, argument checks, tool dispatch, and stored tool results. |
| `internal/tools/`, `internal/workspace/`, `internal/policy/` | Bounded built-in tools, workspace path checks, and command permission decisions. |
| `internal/db/models.go`, `store.go`, `queries.go`, `docs/storage.md` | Session/message/part/tool-call data shape, ordering, constraints, and migrations. Use as a schema reference, not a second own-code store. |
| `internal/ui/chat/runtime.go`, `gate.go`, `sessions.go` | Turn cancellation, approval handoff, event handling, and session restoration. Port the behavior, not the Bubble Tea components. |

LazyCoder is MIT licensed. Preserve its copyright and license notice in files that contain copied substantial portions. Keep own-code's native app boundaries and tests independent of the LazyCoder module.

## Phase 1: Set the port boundaries

- [x] Set the first model scope to chat models that advertise tool support and use `chat-completions` or `responses`. Hide models with other API families until own-code has a tested adapter. Use the catalog's `api` key and `base_url`; reject missing or unknown metadata at both selection and request time. Never route by model name. Evidence: `agent.ResolveModel`/`Eligible`/`FilterEligible` (chat + tool support + chat-completions/responses + base_url) and `ModelTarget.Validate` per request; `TestResolveModel`, `TestFilterEligible`.
- [x] Define provider-neutral request, event, cancellation, and result types under `internal/agent`. Keep them independent of `internal/app` and OwnFrame. Put protocol-specific encoding and decoding behind provider adapters.
- [x] Keep `internal/agentstore` as the only runtime database. Map LazyCoder's session/message/part/tool-call records to own-code's schema. Store provider and model IDs in separate fields; do not pack them into the current `Model` value. Evidence: `Session.ProviderID`/`ModelID` columns in `internal/agentstore`; `TestMigrationFromV3PreservesLegacyTranscript` creates a Console session with separate IDs.
- [x] Establish one runtime owner for the user's agent store. Acquire an OS-backed profile lock before startup recovery. If another app process owns the profile, do not alter its active turns.
- [x] Define one run context per local session. Scope cancellation and event routing by local session ID and turn ID. Replace the app's current process-wide OpenCode operation token before allowing runs in different sessions to overlap.
- [x] Choose the first tool set. Start with workspace-scoped read and search. Write and edit tools may change only the session worktree and must produce proposals for a native accept/reject review path. The current proposal snapshot is read-only, so build that bridge before claiming edits can be accepted. Require approval for each shell command, show its full command and worktree directory, and state that the process has the app user's filesystem and network access. A worktree directory does not sandbox shell commands. Evidence: `agenttools.DefaultPolicy`, `run_shell` summary shows command, directory, and host-user authority, write/edit stage proposals only (`propose_test.go`), accept/reject bridge in `internal/agentbridge`.
- [x] Record every copied source file and any substantive edits. Preserve the MIT notice where required. Evidence: `internal/agent/PORTING.md` (no files copied, pattern reference only, so no MIT notice needed).

**Gate:** A package map names the source and destination for every adapted component. The plan names the supported API families, provider/runtime boundary, session concurrency contract, tool policy, and schema changes before implementation begins.

## Phase 2: Store complete turns and reconstruct history

- [ ] Add versioned `internal/agentstore` migrations for missing message parts and durable tool-call request/result data. Preserve local session IDs and transcript text. Retire `server_id`, `opencode_session_id`, `last_event_sequence`, and `event_checkpoints` from the active Console runtime after removing their consumers. Keep old server-backed transcripts readable as non-resumable history; never treat old server IDs or cursors as Console session state.
- [x] Store separate provider and model IDs, each turn's user message, assistant response parts, tool calls, tool results, approval outcome, timestamps, and terminal status in display and execution order.
- [x] Persist only content that the UI may show or the protocol needs to rebuild a request. Do not request or store private model reasoning. Bound message, tool input, and tool output sizes; never store API keys, authorization headers, or raw credentials.
- [x] Make writes idempotent by local session, message, part, and tool-call ID so reconnects or retries do not duplicate history.
- [x] Build inference history from the local database, including assistant tool calls and matching tool results. Do not rely on a remote OpenCode session transcript.
- [x] After acquiring profile ownership, mark unfinished turns interrupted and calls with an uncertain outcome unknown. Never replay an incomplete shell or file operation automatically.

**Gate:** Migration fixtures preserve local session IDs and readable transcripts from the current server-backed schema, then create a new Console session with separate provider/model IDs. Round-trip checks restore a multi-turn transcript with tool calls, partial output, approvals, and interrupted status.

## Phase 3: Add direct inference, streaming, and cancellation

- [x] Implement `chat-completions` and `responses` requests from the selected model's advertised protocol and `base_url`. Handle unsupported or missing protocols with a clear error and reject models that do not advertise tool support.
- [x] Parse complete and streamed responses with bounded bodies, frame sizes, timeouts, and error messages. Keep the bearer key out of logs and user-visible errors. Apply documented Console eligibility rules and explain access failures such as HTTP 403; catalog membership alone does not guarantee inference access.
- [x] Send a stable opaque local session ID in `x-opencode-session` on every inference request. Use it only for Console routing and prompt-cache consistency; it does not create or retrieve an OpenCode Server session or transcript.
- [x] Pass a turn context through the Console request and stream reader. The Stop action cancels the request, closes the response stream, and prevents late deltas from updating another session. Evidence: `TestCancelStopsDeltas`, `TestCancelDuringStreamInterruptsTurn`.
- [x] Persist partial output as it arrives. Mark a stopped turn `interrupted`, and distinguish it from a provider failure or a completed response. Evidence: `TestCancelDuringStreamInterruptsTurn`, `TestProviderFailure`, `TestTurnRoundTripWithToolCallsAndRecovery` (streaming parts, interrupted vs failed vs completed).
- [x] Route only complete tool calls back to the agent loop. Never validate, approve, or execute partial streamed arguments. Keep tool execution outside the provider client. Evidence: `TestChatStreamIncompleteToolCallNotEmitted`, `TestResponsesStreamFailureAndTruncation`; executors live in `internal/agenttools`.

**Gate:** Provider fixtures cover both supported protocols, split frames, malformed events, incomplete and complete tool-call arguments, final usage, error responses, and cancellation. Unsupported catalog entries fail closed. A canceled request stops producing persisted or rendered deltas.

## Phase 4: Add native permission prompts

- [x] Audit LazyCoder's `internal/policy` rules against own-code's workspace and shell security requirements. Port the policy behavior only after recording the allow/ask/deny matrix for each initial tool. Matrix and decision: [09a-policy-audit.md](09a-policy-audit.md); nothing ported.
- [x] Add a native OwnFrame approval view through the app's existing asynchronous result path. Do not port the Bubble Tea confirmation view.
- [x] Require an explicit decision for shell commands and workspace mutations in the first version. Treat dismissal, timeout, missing policy, and cancellation as denial. Evidence: `TestNilApproverDeniesApprovalTools`, `TestShellNeverRunsWithoutValidApproval`, `TestCancelDuringApproval`, `TestPolicyMatrix`.
- [x] Bind each approval to one session, turn, tool-call ID, worktree root, and complete validated argument set. Reject stale approvals after the session, worktree, or arguments change. Evidence: `Binding`/`Approval.verify` in `agenttools/policy.go`; `TestShellNeverRunsWithoutValidApproval` (stale args), `TestShellRequiresBoundApproval`, loop `TestDenialPersistedAndNotRun` (stale hash).
- [x] Persist the decision before dispatch and show denied calls in session history without running them. Evidence: `TestApprovedExecutionPersistsApprovalBeforeDispatch`, `TestDenialPersistedAndNotRun`.

**Gate:** Policy and UI tests prove that deny, dismiss, cancel, stale approval, out-of-workspace path, and invalid tool input cannot reach an executor. No real tool executor is available until this gate passes.

## Phase 5: Add the agent loop and tool dispatcher

- [x] Port and adapt LazyCoder's bounded step loop into `internal/agent`. Each step rebuilds history from SQLite, requests a model response, validates complete tool calls, obtains the Phase 4 permission decision, persists results, then continues until the model returns a final answer or the step limit is reached.
- [x] Define a narrow tool interface and registry under `internal/agenttools` or another package chosen in Phase 1. Reject unknown tools and invalid or oversized arguments before execution. Never execute truncated arguments.
- [x] Keep file reads and searches inside the session worktree. Reject absolute paths and traversal. Resolve symlinks and recheck containment against the session's captured worktree root immediately before execution.
- [x] Write and edit only in the session worktree. The current proposal snapshot is read-only; build an explicit accept/reject bridge into the native diff review. Revalidate the worktree, target paths, symlinks, baseline, and dirty editor buffers before applying an accepted change. Never write directly into the user's checked-out files or overwrite dirty buffers before review.
- [x] Run shell commands in the session worktree with `exec.CommandContext` or the platform equivalent. Require approval for every command and show the exact command, working directory, and host-user authority before it runs. A worktree working directory does not restrict filesystem or network access. Apply a time limit, bound stdout and stderr, capture exit status and duration, and stop the process tree when the turn is canceled. Persist a truncation marker when displayed or stored output exceeds its limit. Evidence: `TestShellOutputExitAndTruncation`, `TestShellCancelKillsProcessTree`, `TestShellTimeout`.
- [x] Persist a durable dispatch claim before a tool starts. Record approved, denied, running, succeeded, failed, interrupted, or unknown status. If the app stops after dispatch may have begun, require inspection and fresh approval; never automatically retry that call. Do not claim exact-once side effects across crashes. Evidence: `BeginToolDispatch`/`ErrAlreadyDispatched`, `RecoverInterrupted`; `TestRecoveryNeverRedispatches`, `TestTurnRoundTripWithToolCallsAndRecovery`.
- [x] Keep model-side capabilities separate from local authorization. A model response can request a tool but cannot approve its own request.
- [x] Keep run state and cancellation scoped to session and turn IDs. Different sessions may run concurrently without sharing contexts, tool results, or stale UI events.

**Gate:** Runtime tests cover final responses, multiple tool steps, unknown tools, invalid and oversized arguments, permission denial, approved execution, tool errors, maximum-step stops, canceled shell process trees, restart at each dispatch state, stale approvals, and concurrent sessions without state mixing. Interrupted or unknown calls never run again automatically. File changes appear only as proposals in the existing diff review.

## Phase 6: Connect runtime events to session history

- [ ] Extend the existing OpenCode panel to show messages, streamed output, tool activity, approval requests, errors, and interrupted/completed status from own-code events.
- [x] Keep HTTP requests, SQLite writes, and tool execution outside the OwnFrame render loop. Ignore stale events whose session or turn ID no longer matches the active view.
- [ ] Restore the local session list and transcript offline. Resume by rebuilding the request from persisted history after checking the workspace and selected model.
- [x] Add Stop and retry controls. Retry must not repeat a previously completed side effect without a new model request and approval.
- [ ] Keep local session IDs as the primary identifiers. Never use provider-issued IDs to retrieve or reconstruct remote session history.
- [x] Connect worktree snapshots to a native accept/reject flow. Recheck the proposal baseline, target paths, symlinks, and dirty editor state when applying a decision. Preserve staged and unstaged user changes.

**Gate:** Native UI checks cover streaming, approval, stop, session switching, offline history, and restart after an interrupted turn. A slow session cannot overwrite the active session's view.

## Phase 7: Close integration and safety checks

- [ ] Run targeted package tests for `internal/agent`, `internal/agentstore`, `internal/agenttools`, the Console provider, and app event handling. Run the repository's full test and lint gates before merging.
- [x] Verify SQLite migration and restart behavior with a copy of an existing own-code workbench database. Evidence: `TestMigrationFromV3PreservesLegacyTranscript` (v3 fixture, reopen, history/tool activity readable, new Console session). Fixture is built from the real migration functions, not a captured user DB.
- [ ] Verify the API key is absent from SQLite, app settings, editor session files, tool history, logs, and rendered views.
- [x] Verify the Console agent path does not start `opencode serve` or call OpenCode Server session, prompt, message, or event routes. Evidence (agent packages only): `TestConsolePathHasNoOpenCodeServerRoutes` greps `internal/agent`, `agentstore`, `agenttools`, `agentbridge` sources. App-level wiring not re-audited.
- [ ] Exercise one Console-backed turn with an eligible model, one approved tool, one denied tool, one stopped stream, one restored session, and one interrupted tool that requires inspection before a new approval.
- [ ] Update the existing [connection](01-opencode-connection.md), [session-store](02-session-store.md), [agent-panel](03-agent-panel.md), and [acceptance](08-acceptance-and-docs.md) checklists only after their evidence gates pass.

**Gate:** A full turn persists user and assistant output, streams into the native panel, runs only approved tools, handles cancellation, and restores its session from own-code's SQLite database after restart.

## Dependencies

- [01-opencode-connection.md](01-opencode-connection.md) for the Console key, catalog, and direct inference contract.
- [02-session-store.md](02-session-store.md) for own-code's local database and session recovery requirements.
- [03-agent-panel.md](03-agent-panel.md) for local session and transcript behavior.
- [05-workspace-context.md](05-workspace-context.md) and [07-terminal-and-actions.md](07-terminal-and-actions.md) for workspace scope and native actions.
- [08-acceptance-and-docs.md](08-acceptance-and-docs.md) for release evidence and documentation.
- LazyCoder source snapshot: [master at `816e211`](https://github.com/chinmay-sawant/lazykoder/tree/816e2113a54f659f779f2c8592b266011829492c).
- Current Console references: [Model list API](https://opencode.ai/docs/console/api/models), [Inference API](https://opencode.ai/docs/console/api/inference/), [Console harness sessions](https://opencode.ai/docs/console/harnesses#sessions), and [Permissions](https://opencode.ai/docs/permissions/).
