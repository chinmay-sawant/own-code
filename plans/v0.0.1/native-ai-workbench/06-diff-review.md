# Agent change review

**Goal:** Let the user accept, reject, and revert agent edits without losing existing work.

## Phase 1: Isolate proposed edits

- [ ] Choose an isolation mechanism before connecting agent writes to the main workspace: OpenCode worktree support, a private worktree, or another patch-backed staging area.
- [ ] Keep agent writes away from the user's checked-out files until the review contract is proven.
- [ ] Capture the pre-agent worktree, index, and open dirty buffers so the review can distinguish user changes from agent changes.
- [ ] Map every agent-produced path to a file under the selected workspace. Reject traversal, symlink escape, and out-of-root changes.
- [ ] Handle create, delete, rename, binary, and oversized files explicitly.

**Gate:** A fake agent edit cannot alter the main workspace before review. Pre-existing staged, unstaged, and dirty-buffer changes remain attributable to the user.

## Phase 2: Reuse native diff views

- [ ] Show agent proposals in the existing unified and split diff views with session and file identity.
- [ ] Add per-hunk accept and reject controls that apply only agent-owned lines.
- [ ] Add revert for an accepted agent hunk while preserving adjacent user changes.
- [ ] Track accepted, rejected, and pending hunks independently for each session and file.
- [ ] Keep Git stage/unstage actions separate from agent accept/reject actions.
- [ ] Apply accepted hunks into an open editor buffer through its normal undo and dirty-state model.

**Gate:** Tests cover partial accept/reject/revert, overlapping edits, multiple sessions touching one file, staged changes, dirty buffers, undo, and cancellation. A live OpenCode edit appears in the native diff and remains unchanged when rejected.
