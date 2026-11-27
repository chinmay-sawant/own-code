# Agent sessions panel

**Goal:** Show OpenCode sessions, responses, and tool activity inside the existing own-code workspace.

## Phase 1: Manage sessions

- [ ] Add a session list for the selected workspace with create, select, rename, archive, and resume actions supported by the chosen API.
- [ ] Show active, queued, completed, failed, interrupted, and disconnected states.
- [ ] Let the user start more than one session and switch between them without mixing their messages or events.
- [ ] Show the local SQLite history when the server is temporarily disconnected, with a clear offline state.
- [ ] Do not automatically change the current workspace when a user selects a saved session. Ask for or visibly confirm a workspace switch.

**Gate:** Session action tests cover create, list, switch, resume, archive, workspace mismatch, and disconnected-server states.

## Phase 2: Render the transcript

- [ ] Add a native agent panel that fits the existing OwnFrame layout and remains usable at the minimum supported window size.
- [ ] Render streamed Markdown output incrementally. Handle split UTF-8 chunks, empty deltas, malformed events, and failed turns.
- [ ] Preserve the transcript scroll position while the user reads earlier messages. Follow new output only when the user is already at the end.
- [ ] Show model, agent, elapsed state, and execution result for each turn.
- [ ] Add stop/interrupt and retry actions. A queued-prompt response is not a completed turn.
- [ ] Keep all event parsing, persistence, and network waits off the UI loop. Drop stale results from sessions the user has switched away from.

**Gate:** Fake-server streaming tests cover chunking, errors, cancellation, interleaved sessions, stale results, and restart restoration. A live session shows incremental Markdown in the native window.

## Phase 3: Show tool activity

- [ ] Render tool calls in order with name, input summary, running/succeeded/failed state, and bounded output.
- [ ] Collapse completed tool details by default while keeping the action and result visible.
- [ ] Surface permission requests and OpenCode forms as explicit user decisions. Never silently approve a permission or submit a form.
- [ ] Link tool-reported paths to the corresponding workspace file only after validating them against the selected root.
- [ ] Store bounded tool summaries in SQLite without storing credentials or unbounded raw request payloads.

**Gate:** Event fixtures cover read, edit, shell, permission, form, success, failure, and cancellation. Invalid and out-of-root paths never open or modify workspace files.
