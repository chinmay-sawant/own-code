# Issue #9 — Native AI coding and agentic workbench

| Field | Value |
|---|---|
| Repository | `github.com/chinmay-sawant/own-code` |
| Issue | [#9 Native AI coding & agentic workbench](https://github.com/chinmay-sawant/own-code/issues/9) |
| Status | In progress. Direct Console key settings, model catalog loading/selection, private key storage, and local SQLite session creation/transcript reads are implemented. Direct inference, streaming, local agent execution, session restoration, review, and end-to-end validation remain open. |
| Scope | Direct OpenCode Console API integration, with own-code owning local sessions, tool execution, permissions, context, and review. |

This plan lives under `plans/v0.0.1` and is separate from the historical v0.0.1 release checklist. The OpenCode integration uses the hosted Console API directly. It does not start or attach to `opencode serve`, and it does not depend on remote OpenCode Server sessions or events.

## Goal

Add a native Go AI workbench to own-code. Let users configure an OpenCode Console API key, choose a model available to that key's workspace, and send requests to the matching inference API. Keep session IDs, transcripts, tool activity, and restoration state in own-code's per-user SQLite database. own-code runs the agent loop and local tools, asks for permission before sensitive actions, and presents proposed edits for review before applying them.

The workbench also needs inline edits and explicit prompt context. The user chooses which file, selection, Git diff, or LSP problem to include in a request.

## API and credential boundaries

- OpenCode Console keys use the `oc_sk_` prefix, are scoped to one workspace when created, and are sent as `Authorization: Bearer …`. The selected workspace is part of the key; requests do not need a workspace header. See the official [Console API overview](https://opencode.ai/docs/console/api).
- Fetch the workspace's provider/model catalog from `GET https://opencode.ai/console/api/v1/models`. The response is keyed by provider ID and includes model capabilities, limits, and protocol-specific API base URLs. See the official [model list API](https://opencode.ai/docs/console/api/models).
- Send inference directly to the protocol endpoint advertised for the selected model. OpenCode documents OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, and Gemini endpoints under `https://opencode.ai/inference`. Free-tier chat models are restricted to OpenCode; integrations should use eligible paid models or Go models. See the official [Inference API](https://opencode.ai/docs/console/api/inference).
- Console inference is a model API, not a remote agent runtime. own-code creates and stores local session IDs and transcripts, assembles conversation history, runs the agent/tool loop, enforces tool permissions, and handles cancellation and review. Do not call or launch `opencode serve` for this workflow.
- Store the Console API key outside `settings.json`, editor session files, SQLite, workspace files, rendered view data, errors, and logs. Prefer an OS credential store; if the app uses a file, keep it in the private per-user config directory with owner-only permissions. Do not create a repository-local `.env` for the key.
- Keep the workbench in Go and the existing native OwnFrame UI. Do not add a browser or Electron runtime.

## Work files

Use each linked file as the checklist for that slice. Keep checkboxes open until the evidence named by the item is recorded.

| Order | Checklist | Scope | Depends on |
|---:|---|---|---|
| 0 | [01-opencode-connection.md](01-opencode-connection.md) | Console key, model catalog, direct inference, credential storage | — |
| 1 | [02-session-store.md](02-session-store.md) | Local SQLite sessions, transcripts, and restoration | 0 |
| 2 | [03-agent-panel.md](03-agent-panel.md) | Local sessions, transcript, run status, and tool activity | 0–1 |
| 3 | [04-inline-edits.md](04-inline-edits.md) | Selection and caret based agent edits | 0–3 |
| 4 | [05-workspace-context.md](05-workspace-context.md) | User-selected files, diffs, selections, and LSP context | 0–3 |
| 5 | [06-diff-review.md](06-diff-review.md) | Isolated edits and per-hunk accept, reject, and revert | 2–5 |
| 6 | [07-terminal-and-actions.md](07-terminal-and-actions.md) | PTY sessions, file links, palette actions, keybindings | 0–6 |
| 7 | [08-acceptance-and-docs.md](08-acceptance-and-docs.md) | End-to-end gates, supported setup, documentation | 0–7 |

## Shared acceptance rules

- Keep the Console API key out of settings JSON, editor session files, SQLite, workspace files, rendered views, errors, and logs.
- Scope every local session, tool request, and path-bearing result to its selected workspace. Reject traversal and out-of-workspace edits.
- Execute tools in own-code only. Validate tool arguments, apply the permission policy, and get explicit user approval where required; a model response is never authorization to run a command or modify a file.
- Do not let an agent overwrite pre-existing dirty buffers or silently change the user's checked-out files before review.
- Keep HTTP, streaming, storage, tool execution, and session coordination outside the UI render loop. Deliver async results through the app's existing notification and stale-result patterns.
- Keep this plan honest. Mark a row complete only after the implementation exists and the listed evidence passes. Record a concrete reason and next gate for any intentional deferral.

## Remaining work (final audit)

Genuine feature gaps:
- Anthropic Messages and Gemini `generateContent` adapters are not implemented (`internal/agent` supports only chat-completions and responses; models offering only those APIs are ineligible). Open in 01 (two rows). The Anthropic adapter needs its own request builder, SSE event parser, tool_use/tool_result history mapping and fixtures; not attempted in the last pass.
- 06: delete and rename hunks are reported unsupported (no agent tool produces them).
- 03: malformed-frame coverage exists for the chat-completions stream (`TestMalformedEvent`) but is not asserted per protocol for Responses.
- 03: no dedicated assertion for the session state labels.
- 09: no explicit offline restore-and-resume test (local list and transcript are loaded from SQLite, but the "rebuild request after workspace/model check" path has no end-to-end test).

Manual or live checks (no code gap expected):
- 03: dock layout at minimum window size, visual check.
- 07: terminal focus/keyboard/resize/scroll with the dock open; clicking a real `file:line` link.
- 08: the end-to-end acceptance rows (fresh setup with a real `oc_sk_` key, catalog fetch, a real streamed turn, cancel/disconnect, inline edit accept/reject/undo, multi-file hunk review, restart restore, pre-turn user changes survive).
- 08 and 09: full serial test, vet and lint gate on a host that tolerates whole-package runs; 09 live Console turn with approve/deny/stop/restore/interrupted-tool cases; 09 final cross-checklist update once those pass.
