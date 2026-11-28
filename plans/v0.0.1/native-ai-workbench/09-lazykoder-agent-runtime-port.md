# Native AI workbench: adapt LazyCoder's agent runtime

> **Parent:** `plans/v0.0.1/native-ai-workbench/README.md`
> **Status:** Source review complete. Runtime implementation has not started.
> **Source baseline:** LazyCoder `master` at `816e2113a54f659f779f2c8592b266011829492c`, clean and equal to `origin/master` when reviewed.
> **Estimated effort:** Re-estimate after Phase 1 fixes the package contracts and initial tool set.

## Overview

Use LazyCoder's direct model loop, streaming, tool dispatch, approval flow, and session-history code as the source for own-code's agent runtime. Adapt the useful code into own-code's `internal/` packages. Keep the existing OwnFrame UI and local OpenCode Console API path. Do not add an OpenCode Server dependency.

This is an implementation plan only. No LazyCoder runtime code has been copied into own-code.

LazyCoder's database is project-local at `.lazykoder/lazykoder.db` and stores normalized sessions, messages, parts, and tool calls. Own-code already has a per-user SQLite store at `agent-workbench/sessions.sqlite`, with `agent_sessions`, `agent_messages`, `tool_activity`, and `event_checkpoints`. Keep own-code's store as the only source of truth. Compare the schemas and extend own-code with versioned migrations where its current rows cannot represent streamed messages, tool calls, approvals, or resume state. Do not introduce a second runtime database.

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

- [ ] Compare LazyCoder's provider interface and tool-call wire types with the current OpenCode Console inference docs and the model `api` metadata in own-code's catalog. Record the supported protocol set for the first implementation.
- [ ] Define the own-code agent/runtime API under `internal/agent`. Keep request, event, cancellation, and result types independent of `internal/app` and OwnFrame.
- [ ] Keep `internal/agentstore` as the only runtime database. Map LazyCoder's session/message/part/tool-call records to the existing own-code schema and identify required additive migrations.
- [ ] Put direct Console inference beside the existing Console catalog client or behind a small internal provider interface. Do not reuse LazyCoder endpoints or infer a protocol from model names when the catalog advertises it.
- [ ] Choose the first tool set. Start with workspace-scoped read, search, edit/write, and shell operations. Keep additional tools behind a registry so analysis and other tool families can be added without changing the turn loop.
- [ ] Record every copied source file and any substantive edits. Preserve the MIT notice where required.

**Gate:** A package map names the source and destination for every ported component. The plan identifies existing own-code code to keep, extend, or replace before implementation begins.

## Phase 2: Store complete turns and reconstruct history

- [ ] Add versioned `internal/agentstore` migrations for any missing message parts and tool-call request/result data. Preserve existing sessions and transcript rows.
- [ ] Store each turn's user message, assistant response parts, tool calls, tool results, approval outcome, provider/model, timestamps, and terminal status in display and execution order.
- [ ] Persist streamed text and reasoning into the active part as deltas arrive. Bound message, tool input, and tool output sizes; never store API keys, authorization headers, or raw credentials.
- [ ] Make writes idempotent by local session, message, part, and tool-call ID so reconnects or retries do not duplicate history.
- [ ] Build inference history from the local database, including assistant tool calls and matching tool results. Do not rely on a remote OpenCode session transcript.
- [ ] On restart, mark an unfinished request or tool as interrupted. Do not replay an incomplete shell or file operation automatically.

**Gate:** Migration fixtures preserve current own-code sessions. Round-trip checks restore a multi-turn transcript with tool calls, partial streaming output, approvals, and interrupted status.

## Phase 3: Add direct inference, streaming, and cancellation

- [ ] Implement requests for the selected model protocols using the current Console API endpoints and the model's advertised protocol metadata. Handle unsupported protocols with a clear error.
- [ ] Parse complete and streamed responses with bounded bodies, frame sizes, timeouts, and error messages. Keep the bearer key out of logs and user-visible errors.
- [ ] Pass a turn context through the Console request and stream reader. The Stop action cancels the request, closes the response stream, and prevents late deltas from updating another session.
- [ ] Persist partial output as it arrives. Mark a stopped turn `interrupted`, and distinguish it from a provider failure or a completed response.
- [ ] Route model tool calls back to the agent loop without executing them inside the provider client.

**Gate:** Provider fixtures cover split frames, malformed events, tool-call argument fragments, final usage, error responses, and cancellation. A canceled request stops producing persisted or rendered deltas.

## Phase 4: Add the agent loop and tool dispatcher

- [ ] Port and adapt LazyCoder's bounded step loop into `internal/agent`. Each step rebuilds history from SQLite, requests a model response, dispatches validated tool calls, persists their results, then continues until the model returns a final answer or the step limit is reached.
- [ ] Define a narrow tool interface and registry under `internal/agenttools` or another package chosen in Phase 1. Validate tool names, JSON arguments, path scope, and output bounds before execution.
- [ ] Adapt only the selected tools. Keep filesystem access inside the selected workspace and use the existing own-code path-containment rules where available.
- [ ] Run shell commands with `exec.CommandContext` or the platform equivalent so turn cancellation reaches the process. Capture bounded stdout, stderr, exit status, and duration.
- [ ] Persist every tool call before execution, then update it with approved, denied, running, succeeded, failed, or interrupted status. Never execute an unknown tool or an invalid call.
- [ ] Keep model-side capabilities separate from local authorization. A model response can request a tool but cannot approve its own request.

**Gate:** Runtime tests cover final responses, multiple tool steps, unknown tools, invalid arguments, tool errors, maximum-step stops, canceled shell commands, and concurrent sessions without state mixing.

## Phase 5: Add native permission prompts

- [ ] Audit LazyCoder's `internal/policy` rules against own-code's workspace and shell security requirements. Port the policy behavior only after recording the allow/ask/deny matrix for each initial tool.
- [ ] Add a native OwnFrame approval view through the app's existing asynchronous result path. Do not port the Bubble Tea confirmation view.
- [ ] Require an explicit decision for shell and workspace mutations in the first version. Treat dismissal, timeout, missing policy, and cancellation as denial.
- [ ] Bind each approval to one session, tool-call ID, workspace root, and validated argument set. Reject stale approvals after session changes or argument changes.
- [ ] Persist the decision before dispatch and show denied calls in session history without running them.

**Gate:** Tests prove that deny, dismiss, cancel, stale approval, out-of-workspace path, and invalid tool input cannot reach an executor. Approved calls execute once and record their result.

## Phase 6: Connect runtime events to session history

- [ ] Extend the existing OpenCode panel to show messages, streamed output, tool activity, approval requests, errors, and interrupted/completed status from own-code events.
- [ ] Keep HTTP requests, SQLite writes, and tool execution outside the OwnFrame render loop. Ignore stale events after a user switches session or workspace.
- [ ] Restore the local session list and transcript offline. Resume by rebuilding the request from persisted history after checking the workspace and selected model.
- [ ] Add Stop and retry controls. Retry must not repeat a previously completed side effect without a new model request and approval.
- [ ] Keep local session IDs as the primary identifiers. If sent, use them as Console request correlation IDs only; they are not OpenCode Server session IDs.

**Gate:** Native UI checks cover streaming, approval, stop, session switching, offline history, and restart after an interrupted turn. A slow session cannot overwrite the active session's view.

## Phase 7: Close integration and safety checks

- [ ] Run targeted package tests for `internal/agent`, `internal/agentstore`, `internal/agenttools`, the Console provider, and app event handling. Run the repository's full test and lint gates before merging.
- [ ] Verify SQLite migration and restart behavior with a copy of an existing own-code workbench database.
- [ ] Verify the API key is absent from SQLite, app settings, editor session files, tool history, logs, and rendered views.
- [ ] Verify no active path starts `opencode serve` or calls OpenCode Server session, prompt, message, or event routes.
- [ ] Exercise one Console-backed turn with an eligible model, one approved tool, one denied tool, one stopped stream, and one restored session.
- [ ] Update the existing [connection](01-opencode-connection.md), [session-store](02-session-store.md), [agent-panel](03-agent-panel.md), and [acceptance](08-acceptance-and-docs.md) checklists only after their evidence gates pass.

**Gate:** A full turn persists user and assistant output, streams into the native panel, runs only approved tools, handles cancellation, and restores its session from own-code's SQLite database after restart.

## Dependencies

- [01-opencode-connection.md](01-opencode-connection.md) for the Console key, catalog, and direct inference contract.
- [02-session-store.md](02-session-store.md) for own-code's local database and session recovery requirements.
- [03-agent-panel.md](03-agent-panel.md) for local session and transcript behavior.
- [05-workspace-context.md](05-workspace-context.md) and [07-terminal-and-actions.md](07-terminal-and-actions.md) for workspace scope and native actions.
- [08-acceptance-and-docs.md](08-acceptance-and-docs.md) for release evidence and documentation.
- LazyCoder source snapshot: [master at `816e211`](https://github.com/chinmay-sawant/lazykoder/tree/816e2113a54f659f779f2c8592b266011829492c).
- Current Console references: [Inference API](https://opencode.ai/docs/console/api/inference/), [Console harness sessions](https://opencode.ai/docs/console/harnesses), and [Permissions](https://opencode.ai/docs/permissions/).
