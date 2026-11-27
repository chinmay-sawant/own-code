# Issue #9 — Native AI coding and agentic workbench

| Field | Value |
|---|---|
| Repository | `github.com/chinmay-sawant/own-code` |
| Issue | [#9 Native AI coding & agentic workbench](https://github.com/chinmay-sawant/own-code/issues/9) |
| Status | Proposed plan. Every implementation item remains open. |
| Scope | OpenCode first, then session history, inline editing, workspace context, review, and terminal workflows. |

This plan lives under `plans/v0.0.1` as requested. It is a separate plan for issue #9. It does not change the historical v0.0.1 release checklist.

## Goal

Add a native Go agent workbench to own-code. Start and attach to OpenCode sessions from the editor, show streamed responses and tool activity, and let the user review agent edits in the existing workspace. Keep session history in a per-user SQLite database so sessions remain visible after restart.

The workbench also needs an inline edit flow and explicit prompt context. The user chooses which file, selection, Git diff, or LSP problem to include in a prompt.

## Protocol and credential boundaries

- Use the OpenCode Server API for agent sessions. The current OpenCode 2 migration guide says server integrations must use the revised V2 API. The generated API reference labels its selected-instance HTTP surface experimental. Pin a supported OpenCode version and record any experimental dependency in [01-opencode-connection.md](01-opencode-connection.md).
- Keep the OpenCode server password separate from model-provider API keys. The server uses HTTP Basic auth with the fixed username `opencode`. `OPENCODE_PASSWORD` sets the password; `OPENCODE_SERVER_PASSWORD` remains supported for compatibility.
- Discover provider integrations and their available authentication methods from OpenCode. Send a typed provider key to OpenCode's `connect/key` endpoint so OpenCode owns the provider credential. Do not write that key to own-code settings, the session database, workspace files, or logs.
- Treat LazyCoder's OpenCode Go integration as a different product path. It calls the hosted inference API directly. That API's bearer key, model routes, Responses support, and `x-opencode-session` header do not define the local OpenCode Server session protocol.
- Store own-code's server connection secret outside `settings.json` and the SQLite session database. Prefer an OS credential store. If a file is needed, keep it in the user config directory with owner-only permissions. Do not write credentials into an opened project. If implementation adds a repository-local environment file, add its ignore rules before creating it.
- The current `/api/event` stream is volatile and misses events during disconnects. `session.prompt` returns after queueing work. Reconnect by reading persisted messages or by using the experimental durable session log only where its version dependency is acceptable.
- Keep the workbench in Go and the existing native OwnFrame UI. Do not add a browser or Electron runtime.

## Work files

Use each linked file as the checklist for that slice. Keep checkboxes open until the evidence named by the item is recorded.

| Order | Checklist | Scope | Depends on |
|---:|---|---|---|
| 0 | [01-opencode-connection.md](01-opencode-connection.md) | OpenCode V2 contract, settings, credentials, launch and attach | — |
| 1 | [02-session-store.md](02-session-store.md) | SQLite schema, migrations, session restoration | 0 |
| 2 | [03-agent-panel.md](03-agent-panel.md) | Sessions, streamed transcript, status, tool log | 0–1 |
| 3 | [04-inline-edits.md](04-inline-edits.md) | Selection and caret based agent edits | 0–3 |
| 4 | [05-workspace-context.md](05-workspace-context.md) | User-selected files, diffs, selections, and LSP context | 0–3 |
| 5 | [06-diff-review.md](06-diff-review.md) | Isolated edits and per-hunk accept, reject, and revert | 2–5 |
| 6 | [07-terminal-and-actions.md](07-terminal-and-actions.md) | PTY sessions, file links, palette actions, keybindings | 0–6 |
| 7 | [08-acceptance-and-docs.md](08-acceptance-and-docs.md) | End-to-end gates, supported versions, documentation | 0–7 |

## Shared acceptance rules

- Keep API keys and server passwords out of `globalSettings`, `sessionState`, SQLite, rendered view data, errors, and logs.
- Scope every OpenCode session and every path-bearing event to the selected workspace. Reject path traversal and out-of-workspace edits.
- Do not let an agent overwrite pre-existing dirty buffers or silently change the user's checked-out files before review.
- Keep OpenCode HTTP, event parsing, storage, and session coordination outside the UI render loop. Deliver async results through the app's existing notification and stale-result patterns.
- Keep this plan honest. Mark a row complete only after the implementation exists and the listed evidence passes. Record a concrete reason and next gate for any intentional deferral.
