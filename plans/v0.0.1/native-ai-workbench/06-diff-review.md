# Agent change review

**Goal:** Let the user review own-code agent proposals and accept, reject, or revert edits without losing existing work.

**Implementation note (2026-11-28):** The worktree manager creates a random detached Git worktree under the private user-config directory. It maps selected subdirectories, starts at committed `HEAD`, refuses checkout filters and tracked symlinks, disables inherited Git configuration, hooks, and fsmonitor during checkout, and validates containment and Git registration before non-forcing cleanup. A reopen helper revalidates the original repository and saved base commit after restart. `opencode_proposal_snapshot.go` adds a read-only bounded snapshot of created, modified, and deleted text files relative to the saved base commit, including ignored untracked paths so oversized or unsupported output fails visibly instead of disappearing. It rejects rename, binary, symlink, traversal, control-character, and oversized changes. The snapshot appears in the session panel, but it is not connected to existing diff views and no accept/reject operation exists. Direct Console inference is not wired to this path, and own-code's local agent loop, tool permission policy, and tool execution are still open. A worktree is not an OS sandbox and does not replace those controls. This slice has only passed `go build ./...`; no tests or live inference/edit run have been recorded. The remaining checklist rows and gates stay open.

**Hunk model slice (2026-11-28):** `internal/agentreview` adds the pure hunk model (diff, apply, revert, per-session/file state, baseline capture, path mapping, change classification) and `internal/app/opencode_hunks.go` wires accept/reject/revert to disk or the open editor buffer (via `replaceEditRanges`, so undo/dirty apply). Not yet done: panel/diff-view controls and session/file identity in the unified/split views, wiring `CaptureBaseline`/`Classify` into turn start and staging (rename/delete/binary), buffer-path tests, persistence. Checked rows below are model-level only.

## Phase 1: Isolate proposed edits

- [x] Choose an isolation mechanism before connecting agent writes to the main workspace: a private worktree or another patch-backed staging area.
- [x] Keep agent writes away from the user's checked-out files until the review contract is proven.
- [ ] Capture the pre-agent worktree, index, and open dirty buffers so the review can distinguish user changes from agent changes.
- [x] Map every agent-produced path to a file under the selected workspace. Reject traversal, symlink escape, and out-of-root changes. (`agentreview.MapPath`, `TestReviewMapPathRejectsEscapes`; used by `recordAgentReview`.)
- [ ] Handle create, delete, rename, binary, and oversized files explicitly.

**Gate:** A fake agent edit cannot alter the main workspace before review. Pre-existing staged, unstaged, and dirty-buffer changes remain attributable to the user.

## Phase 2: Reuse native diff views

- [ ] Show agent proposals in the existing unified and split diff views with session and file identity.
- [ ] Add per-hunk accept and reject controls that apply only agent-owned lines.
- [x] Add revert for an accepted agent hunk while preserving adjacent user changes. (Model-level; `TestReviewRevertKeepsAdjacentUserChange`, `TestHunkReviewAcceptRejectRevertOnDiskKeepsUserLines`. No UI control yet.)
- [x] Track accepted, rejected, and pending hunks independently for each session and file. (In-memory `agentreview.Registry`; `TestReviewStateTransitions`, `TestReviewOverlappingSessionsOnOneFile`. Not persisted.)
- [x] Keep Git stage/unstage actions separate from agent accept/reject actions. (Hunk actions never run git; the app test asserts the index stays empty.)
- [ ] Apply accepted hunks into an open editor buffer through its normal undo and dirty-state model.

**Gate:** Tests cover partial accept/reject/revert, overlapping edits, multiple sessions touching one file, staged changes, dirty buffers, undo, and cancellation. A live OpenCode edit appears in the native diff and remains unchanged when rejected.
