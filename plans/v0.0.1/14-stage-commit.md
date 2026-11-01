# 04 — Per-Hunk Stage + Commit Dialog

## 1. Spec

Per-hunk stage plus a commit dialog, no new dependencies.

### Stage hunk (`internal/app/stage_commit.go`)

* `stageHunk(ctx, hunkIdx, selection...)` stages ONLY the addressed hunk via
  `git apply --cached` with a shaped patch (`git diff -U0 -- path` filtered
  by `selectRawHunks` overlap, the same addressing `revertHunk` uses in
  `internal/app/revert_hunk.go`). Worktree bytes are never touched; the rest
  of the file stays unstaged. Success toasts "Hunk staged" and refreshes
  through `refreshAfterSCMMutation`.
* `stageClickedBlock` / `hunkBlockSelection` mirror `revertClickedBlock`
  (`internal/app/revert_block.go`): the line button id resolves to the same
  contiguous source block, so the Stage and Discard menu rows address the
  same block.
* Triggers: keyboard `Shift+S` (`stage-hunk` keymap entry, home branch on
  `case "s"` gated by `keymapAllows`) stages the hunk under the highlighted
  change block (`hunkIndexAtCursor`, same current-row rule as the pane
  highlight); right-click on a `revert-line-*` button opens a hunk menu with
  `Stage Hunk` next to `Discard Hunk` (`hunkContextMenu`,
  `code-view:stage-hunk:` / `code-view:revert-hunk:` rows in `MenuAction`).
* Explicit notes instead of guessed stages: working-tree-only guard,
  conflicted files ("Resolve the merge conflict before staging hunks"),
  binary/oversized ("File-level stage only … use the file + button"),
  untracked ("Untracked files stage whole …"), already-staged ("Hunk already
  staged"), no-hunk ("No hunk here …"). Renames are allowed through
  `git apply --cached`; an apply failure toasts "Stage failed".

### Commit dialog (`internal/app/stage_commit.go`, `components/html/commit_dialog.html`)

* State: `commitDialogOpen/Msg/Err` on `App`; `View.CommitMessage` (top-level
  for `data-bind`, textarea like `components/html/settings.html`) plus
  `commitDialogView` (`Show/Message/Files/FileCount/Shortstat/Error`).
  `Modal` includes the dialog; clicks/presses/keys outside its ids are
  swallowed while it is open (confirm keeps precedence when both show).
* Content: message textarea (multiline, first line = subject), staged file
  list with stats, `stagedShortstat` line
  ("1 file changed, 1 insertion(+), 1 deletion(-)"), inline error area,
  Commit and Cancel buttons, `Ctrl+Enter commit · Esc cancel` hint.
* Open: sidebar `Commit` button (`commit-dialog` action, `components/html/sidebar.html`
  + `.commit-btn` in `components/css/sidebar.css`). Blocked with an info
  toast when nothing is staged.
* `submitCommitDialog`: empty message is blocked with the inline error
  ("Write a commit message before committing."). Dirty open buffers covering
  staged paths open the unsaved dialog first via `gateUnsavedDirty` (Save
  writes then commits, Don't Save commits, Cancel aborts); otherwise
  `doCommit` runs `git commit -m <subject> [-m <body>]` (first line subject,
  rest body). Success clears the draft, closes the dialog, refreshes through
  `refreshAfterSCMMutation`, and toasts "Committed". Failure keeps the draft
  with the error shown. Cancel closes and keeps the draft for reopen.
* Never pushes, never amends: `doCommit` builds only `commit -m…` args.

### Key files

| Area | File |
|---|---|
| Stage + commit logic | `internal/app/stage_commit.go` (new) |
| App state, `Modal`, view fields | `internal/app/app.go`, `internal/app/view.go` |
| Click/key/submit/change wiring, `Shift+S` | `internal/app/actions.go`, `internal/app/selection_ops.go` |
| Hunk menu rows + actions, `stage-hunk` key | `internal/app/context_menu.go`, `internal/app/settings.go` |
| Dialog template, registration | `components/html/commit_dialog.html` (new), `internal/app/build.go` |
| Commit button + dialog styles | `components/html/sidebar.html`, `components/css/sidebar.css` |
| Tests | `internal/app/stage_commit_test.go` (new) |

Environment note: the sibling `ownframe` checkout moved to blinkless
v0.1.1, so `GOPROXY=off go mod tidy` was needed to add the missing sums
(`go.mod`/`go.sum` diff is only that bump). Pre-existing drift, kept
uncommitted in the worktree.

## 2. Checklist (boxes checked only with test evidence)

- [x] Hunk stage stages one hunk of two and leaves the rest unstaged, worktree intact — `TestStageHunkStagesOneOfTwo` (staged diff has TWO only, unstaged has ELEVEN only, file keeps both, one staged + one unstaged file, "Hunk staged" toast).
- [x] Keyboard stages the hunk under the cursor — `TestStageHunkAtCursorAndHunkMenu` (`Shift+S` via `onKeyDown` stages TWO).
- [x] Context menu row next to revert stages/discards the block — `TestStageHunkAtCursorAndHunkMenu` (menu is Stage Hunk + Discard Hunk; menu stage completes the index to a clean worktree-vs-index diff; menu discard reverts the ELEVEN+TWELVE block while staged TWO survives).
- [x] Binary/untracked/conflicted files get explicit notes, index untouched — `TestStageHunkNotes` (info toasts naming file-level stage / whole-file stage / conflict resolution; `git diff --cached` empty, `??` kept, unmerged entries kept).
- [x] Dialog shows message input, staged summary, Commit/Cancel — `TestCommitCreatesCommit` (dialog shows one staged file, "1 file changed" shortstat).
- [x] Empty message blocked with inline error, no commit — `TestCommitEmptyBlocked` (error shown, dialog open, `rev-list --count` stays 1).
- [x] Commit runs git commit, refreshes, clears input on success — `TestCommitCreatesCommit` (subject "Add feature", body kept, dialog closed and draft cleared, staged empty after refresh, "Committed" toast, ELEVEN survives unstaged; count goes 1 → 2 with base untouched, so no amend).
- [x] Failure keeps input with the error shown — `TestCommitFailureKeepsInput` (index emptied behind the dialog; error shown, dialog open, draft kept, count stays 1).
- [x] Cancel keeps input — `TestCommitCancelKeepsInput` (closed via `commit-cancel` click, draft kept, reopen restores it, no commit).
- [x] Dirty buffers covering staged paths warn first — `TestCommitDirtyGuard` (unsaved dialog opens, no commit; Save writes then commits "Guarded commit", both dialogs close).
- [x] Dialog refuses to open with nothing staged — `TestCommitDialogNeedsStaged` (info toast, dialog stays closed).
- [x] No auto push, no amend — `TestCommitCreatesCommit` (count 1 → 2 with base intact, so history extended not rewritten; `doCommit` builds only `commit -m…` args and the suite has no remote, a push would fail the commit path).

Validation: `gofmt -l internal/app/` clean, `go vet ./internal/app/` clean,
`go test -p 1 ./internal/app/ -run 'TestStage|TestCommit|TestRevert|TestHunk|TestSCM|TestDiscard' -count=1` green,
full `go test -p 1 ./internal/app/ -count=1` green.
