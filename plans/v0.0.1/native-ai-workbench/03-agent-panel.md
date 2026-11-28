# Local agent sessions panel

**Goal:** Show own-code-managed sessions, model responses, and local tool activity inside the existing workspace.

**Implementation status:** Settings can load and select the Console catalog. The panel lists locally persisted SQLite sessions, creates a local session, and reads its local transcript without connecting to `opencode serve`. Sending prompts, streaming, rename/archive/resume, the agent loop, tool permissions, and tool execution remain open. Console inference does not create remote agent sessions or provide a server event stream; session and turn state belong to own-code.

## Phase 1: Manage local sessions

- [ ] Add a session list for the selected workspace with create, select, rename, archive, and resume actions backed by the local SQLite store.
- [ ] Show active, queued, completed, failed, interrupted, and disconnected states from own-code's local run lifecycle.
- [ ] Let the user start more than one local session and switch between them without mixing messages, tool calls, or workspace state.
- [ ] Show cached SQLite history while offline, with a clear indication that a new inference request needs a working Console credential and model.
- [ ] Do not automatically change the current workspace when a user selects a saved session. Ask for or visibly confirm a workspace switch.

**Gate:** Session action checks cover create, list, switch, resume from local transcript, archive, workspace mismatch, and disconnected/invalid-credential states.

## Phase 2: Run and render a turn

- [ ] Add a native agent panel that fits the existing OwnFrame layout and remains usable at the minimum supported window size.
- [ ] Add a prompt composer and send action for the selected local session and catalog model.
- [ ] Build each inference request from the selected model's documented protocol and the session's local conversation history.
- [ ] Render streamed Markdown output incrementally. Handle split UTF-8 chunks, empty deltas, malformed frames, provider errors, cancellation, and failed turns for each supported protocol.
- [ ] Preserve transcript scroll position while the user reads earlier messages. Follow new output only when the user is already at the end.
- [ ] Show provider/model, elapsed state, and execution result for each turn.
- [ ] Add stop/cancel and retry actions. A partial or interrupted response must not be treated as a completed turn.
- [ ] Keep HTTP, stream parsing, persistence, and agent coordination off the UI loop. Drop stale results from sessions the user has switched away from.

**Gate:** Fake inference streams cover chunking, errors, cancellation, interleaved local sessions, stale results, and restart restoration. A live response renders incrementally in the native window.

## Phase 3: Own the agent and tool loop

- [ ] Implement the local loop that sends model requests, detects tool calls, records assistant/tool messages, and continues inference with tool results.
- [ ] Define a small own-code tool registry. Validate tool names and typed arguments before dispatch; do not execute arbitrary model-provided commands or paths.
- [ ] Add an explicit permission policy and user approval UI for tools with side effects. Denial, cancellation, and approval must be stored in local turn state.
- [ ] Execute approved tools through own-code's workspace and process boundaries. Revalidate paths and workspace identity at execution time.
- [ ] Render tool calls in order with name, bounded input summary, running/succeeded/failed/denied state, and bounded output.
- [ ] Keep secrets, raw authorization headers, and unbounded request/response payloads out of local tool history and logs.

**Gate:** Fixtures cover tool-call decoding, invalid arguments, unknown tools, approval, denial, cancellation, success, failure, restart during execution, and out-of-workspace paths. No model response can cause tool execution without the own-code permission path.
