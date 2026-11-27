# Workspace context

**Goal:** Attach useful editor context to an agent request only when the user chooses it.

## Phase 1: Define context sources

- [ ] Support the active file, current selection, selected files, working-tree Git diff, and selected LSP Problems entries.
- [ ] Add per-source controls so the user can include or remove context before sending the prompt.
- [ ] Show file names, line ranges, and diagnostic summaries in a context preview.
- [ ] Capture each source as a bounded snapshot. Record its workspace root and file version so stale context can be identified.
- [ ] Exclude `.env`, key files, credential files, ignored files, and other sensitive paths by default. Never attach own-code credentials or OpenCode authorization data.
- [ ] Bound file count, bytes, lines, diagnostics, and total prompt context. Report truncation clearly.

**Gate:** Serialization tests cover every source, ordering, Unicode, stale buffers, missing files, size limits, excluded secrets, and workspace-root enforcement.

## Phase 2: Add context actions

- [ ] Add `Explain Selection`, `Explain Problem`, and `Review Changes` actions that assemble only the context named by the action.
- [ ] Let the user inspect the final prompt and context before the agent receives it.
- [ ] Preserve the selected agent session when follow-up context is sent.
- [ ] Do not read arbitrary workspace files only because their paths appear in an agent response or tool event.

**Gate:** Palette and editor actions produce deterministic prompt payloads. Tests prove that each action includes only its declared context sources.
