# Inline agent edits

**Goal:** Let the user ask OpenCode to change the current selection or nearby code and review the proposal inline.

## Phase 1: Prompt from the editor

- [ ] Add an inline prompt action for the current selection and a caret-based action when no selection exists.
- [ ] Keep the original file, selection range, document version, and workspace root with the request.
- [ ] Show the exact context sent with the prompt and let the user remove optional context before submission.
- [ ] Stream the response into an inline result card without inserting it into the document.
- [ ] Preserve current editor content, selection, caret, undo history, and dirty state while the request runs.

**Gate:** Editor interaction checks prove the correct selection and file version reach the request. Typing during a request does not move or replace unrelated text.

## Phase 2: Review and apply

- [ ] Convert proposed changes into an explicit patch against the captured document version.
- [ ] Show the proposed change inline with accept, reject, and retry actions.
- [ ] Apply an accepted edit as one undoable buffer operation. Rejecting leaves the document unchanged.
- [ ] Refuse to apply a patch when the file changed since the request snapshot. Offer refresh or rerun instead.
- [ ] Keep multi-file edits in the workspace review flow. Do not apply them through the single-selection inline path.

**Gate:** Tests prove accept, reject, undo, stale-document refusal, selection boundaries, CRLF/final-newline behavior, and preservation of pre-existing dirty edits.
