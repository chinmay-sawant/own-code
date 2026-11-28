# Workspace context

**Goal:** Attach useful editor context to a direct OpenCode Console inference request only when the user chooses it.

## Phase 1: Define context sources

- [x] Support the active file, current selection, selected files, working-tree Git diff, and selected LSP Problems entries.
- [x] Add per-source controls so the user can include or remove context before sending the prompt.
- [x] Show file names, line ranges, and diagnostic summaries in a context preview.
- [x] Capture each source as a bounded snapshot. Record its workspace root and file version so stale context can be identified.
- [x] Exclude `.env`, key files, credential files, ignored files, and other sensitive paths by default. Never attach own-code credentials or OpenCode authorization data.
- [x] Bound file count, bytes, lines, diagnostics, and total prompt context. Report truncation clearly.

**Implementation status:** `internal/app/opencode_mentions.go` adds the `@` mention popup (editor sources `@selection`, `@file`, `@diff`, `@problems` plus up to 8 fuzzy-matched workspace files; `.env`, key, credential, ignored and `.git` paths are excluded) and removable chips. Chips are the only context sent: `composeOpenCodeUserText` appends `renderOpenCodeContextBlock` (the same text the panel preview shows) to the user message in `submitOpenCodePrompt`, and stale sources block send. Limits: 8 chips, 32 KiB per chip, 128 KiB total, 400 lines per file, 800 diff lines / 48 KiB diff, 25 problems; truncation is called out in the chip and sent block. Evidence includes `TestMention*`, `TestDiffRecheckDeferredOffUIThread`, `TestUnicode*`, `TestAgentOutputPathsNeverTriggerReads`, and `TestSensitiveListSearchSkip`. Context actions fill the composer without auto-sending. The focused context and prompt-path tests pass; the live Console flow remains in plan 08.

**Gate:** Serialization tests cover every source, ordering, Unicode, stale buffers, missing files, size limits, excluded secrets, and workspace-root enforcement.

## Phase 2: Add context actions

- [x] Add `Explain Selection`, `Explain Problem`, and `Review Changes` actions that assemble only the context named by the action. (`planOpenCodeAction` in `opencode_actions.go`; tests `TestOCActionReviewChangesOnlyDiffAndKeepsSession`, `TestOCActionExplainProblemOnlyThatProblem`. A positive Explain Selection test needs an edit-buffer harness and is not written.)
- [x] Let the user inspect the final prompt and context before the agent receives it. (Actions only fill the composer and chips; the exact block shows in `opencode-composer-preview`; nothing is auto-sent, asserted by `len(runs)==0`.)
- [x] Preserve the selected agent session when follow-up context is sent. (`runOpenCodeAction` never changes `selectedSessionID`; asserted in the review test. A typed draft is kept ahead of the action prompt.)
- [x] (Evidence: `TestAgentOutputPathsNeverTriggerReads` plants FIFO tripwires in the workspace and drives streamed text and tool events naming them through `handleOpenCodeRunEvent` and the panel render; any open would hang, and no chip or context is created. Only a staged edit proposal reads its own already-mapped baseline file (`recordAgentReview`, `MapPath`-checked, hash-verified) for hunk review.) Do not read arbitrary workspace files only because their paths appear in an agent response or tool event.

**Gate:** Palette and editor actions produce deterministic prompt payloads. Tests prove that each action includes only its declared context sources.
