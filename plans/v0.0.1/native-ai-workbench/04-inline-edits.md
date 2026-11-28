# Inline agent edits

**Goal:** Let the user ask a selected OpenCode Console model to change the current selection or nearby code, then review the proposal inline.

## Phase 1: Prompt from the editor

- [x] Add an inline prompt action for the current selection and a caret-based action when no selection exists.
- [x] Keep the original file, selection range, document version, and workspace root with the request.
- [x] Show the exact context sent with the prompt and let the user remove optional context before submission.
- [x] Stream the response into an inline result card without inserting it into the document.
- [x] Preserve current editor content, selection, caret, undo history, and dirty state while the request runs.

**Implementation note:** `internal/app/opencode_context.go` now provides a bounded immutable snapshot helper for a caller-supplied editor buffer. It records the canonical workspace root, relative file path, full-buffer SHA-256 version, and directional selection/caret offsets; it keeps the selection in a maximum 32 KiB excerpt and rejects unsafe paths and common credential files. Focused helper tests passed with `go test internal/app/opencode_context.go internal/app/opencode_context_test.go -count=1`. The editor action, prompt preview, direct Console inference request, and apply/review flow are not wired yet, so all checklist items remain open.

**Implementation note (inline flow):** `internal/app/opencode_inline.go` with `opencode_inline_test.go`. The palette command "OpenCode: Edit Selection Inline…" captures the snapshot (path, selection or caret, full-buffer SHA-256 version, workspace root) and shows a draft card with the exact prompt and removable optional context (surrounding lines, file path). Send runs a no-tools `Provider.Stream` request off the UI loop with a per-request cancel context. The reply streams into the card, becomes an explicit patch, and accept applies it through `replaceEditRanges` (or an equivalent single-undo insert in caret mode). Gaps: the card is shown in the docked OpenCode panel, not anchored in the editor text; there is no dedicated keybinding; the real Console transport is not exercised by tests; the multi-file pointer is a card note only. Tests: `go test -count=1 -p 1 -run TestInline ./internal/app`.

**Gate:** Editor interaction checks prove the correct selection and file version reach the request. Typing during a request does not move or replace unrelated text.

## Phase 2: Review and apply

- [x] Convert proposed changes into an explicit patch against the captured document version.
- [x] Show the proposed change inline with accept, reject, and retry actions.
- [x] Apply an accepted edit as one undoable buffer operation. Rejecting leaves the document unchanged.
- [x] Refuse to apply a patch when the file changed since the request snapshot. Offer refresh or rerun instead.
- [x] Keep multi-file edits in the workspace review flow. Do not apply them through the single-selection inline path.

**Gate:** Tests prove accept, reject, undo, stale-document refusal, selection boundaries, CRLF/final-newline behavior, and preservation of pre-existing dirty edits.
