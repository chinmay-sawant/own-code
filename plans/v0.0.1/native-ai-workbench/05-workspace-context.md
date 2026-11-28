# Workspace context

**Goal:** Attach useful editor context to an agent request only when the user chooses it.

## Phase 1: Define context sources

- [ ] Support the active file, current selection, selected files, working-tree Git diff, and selected LSP Problems entries.
- [ ] Add per-source controls so the user can include or remove context before sending the prompt.
- [ ] Show file names, line ranges, and diagnostic summaries in a context preview.
- [ ] Capture each source as a bounded snapshot. Record its workspace root and file version so stale context can be identified.
- [ ] Exclude `.env`, key files, credential files, ignored files, and other sensitive paths by default. Never attach own-code credentials or OpenCode authorization data.
- [ ] Bound file count, bytes, lines, diagnostics, and total prompt context. Report truncation clearly.

**Implementation note:** The same `captureAgentContext` helper snapshots a caller-supplied active editor buffer and selection, records its canonical workspace root and full-buffer version, and returns a clearly marked bounded excerpt. The OpenCode panel has a toggle and exact preview for this snapshot. Focused helper tests passed with `go test internal/app/opencode_context.go internal/app/opencode_context_test.go -count=1`. Prompt submission remains paused pending isolated review. Context-source selection, Git diff and LSP capture, ignored-file checks, and broader prompt serialization are not wired yet; the complete phase gate is unmet and all checklist items remain open.

**Additional helper:** `collectSelectedAgentFiles` in `internal/app/opencode_context_sources.go` accepts only an explicit ordered path list and an optional ignored-path set. It rejects traversal, symlinks, non-regular files, `.git` metadata, common sensitive paths, and non-UTF-8 content; bounds output to 8 files, 32 KiB per file, 128 KiB total, and 400 lines per file; preserves selection order; and returns truncation and exclusion details. The UI and prompt path do not call it yet. The package build attempt currently fails in the separately added `opencode_worktree.go`, so this helper has not been independently build-verified; no tests were run. For truncated files its version token combines the opened file size and modification time with the captured excerpt hash, so it is a snapshot identifier rather than a full-file content hash. The checklist remains open.

**Gate:** Serialization tests cover every source, ordering, Unicode, stale buffers, missing files, size limits, excluded secrets, and workspace-root enforcement.

## Phase 2: Add context actions

- [ ] Add `Explain Selection`, `Explain Problem`, and `Review Changes` actions that assemble only the context named by the action.
- [ ] Let the user inspect the final prompt and context before the agent receives it.
- [ ] Preserve the selected agent session when follow-up context is sent.
- [ ] Do not read arbitrary workspace files only because their paths appear in an agent response or tool event.

**Gate:** Palette and editor actions produce deterministic prompt payloads. Tests prove that each action includes only its declared context sources.
