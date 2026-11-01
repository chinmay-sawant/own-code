# Plan: Dirty indicator, unsaved prompt on tab close, auto-save option

## 1. Current behavior + file refs

### 1a. Edit model: single live buffer, per-path dirty map

- `internal/app/text_edit.go:27-49` (`onType`), `:52-73` (`onBackspace`), `:334-363` (`replaceEditRange`): every keystroke mutates the single `a.edit (*edit.Buffer)` and sets `a.dirty[a.edit.Path] = a.edit.Dirty`.
- `internal/app/edit_ops.go:11-56` (`toggleEdit` / `beginEditAt`): only left group (`groupDoc(0)`) is editable; right group is view-only. Entering edit reads `a.ws.readFile(doc.Path)`, except when `a.dirty[doc.Path]` is already true it reconstructs from `doc.Lines`.
- `internal/app/edit_ops.go:136-148` (`finishEdit`, Esc path in `text_edit.go:306-310`): folds buffer into `a.docs[path]` via `commitEditBuffer()` but **keeps `a.dirty[path]=true`**. `a.edit` is set to nil — dirty doc with no live buffer.
- `internal/app/edit_ops.go:152-178` (`commitEditBuffer`): rewrites `doc.Lines/Tokens/Outline/ByteSize`, clears cached `Preview/Wrap`. Same path used by undo/redo (`text_edit.go:415-426`).
- `internal/app/edit_ops.go:181-202` (`saveEdit`): `a.edit.Save()` → `a.ws.writeFile` → `MarkSaved()` → `a.dirty[path]=false` → re-open doc from disk. Failure toasts, keeps dirty.
- `internal/app/diff_edit.go:206-246` (`saveDirtyActive`): covers the no-live-buffer case (after Esc, or dirty working-tree file surfaced from changes mode). Picks `a.diffPath` when `mode=="changes"`, else `activeDoc()` if `a.dirty[doc.Path]`.
- `internal/app/app.go:204-215` (`dirty map[string]bool`, `dirtyCount()` at `edit_ops.go:204-215`, `view.Dirty` at `app.go:893`, `EditDirty` at `app.go:947` / `pane.go:260`).

### 1b. Ctrl+S save path

- `internal/app/text_edit.go:268-272` (`editKey`, Ctrl+S while `a.editing`): `a.saveEdit()` + redraw.
- `internal/app/actions.go:1798-1802` (global Ctrl+S): `a.saveDirtyActive()` + redraw. This is the only path that saves a folded-but-dirty doc (no `a.edit`).
- `internal/app/workspace_write.go:72-108` (`writeFile`): refuses sample workspace for workspace-relative paths, allows absolute external paths in place, `MkdirAll`s parent, updates `meta.Size/Big`.
- `internal/app/workspace_write_test.go`: covers this contract — keep green.

### 1c. Tabs: two groups, windowed strip, close discards silently

- `internal/app/app.go:2411-2441` (`tabRowsFor`): builds `tabRow{Index,Path,Name,..,Dirty: a.dirty[path]}` per group (`groupTabs/groupActive`). `view.go:291-303` defines `tabRow`.
- `internal/app/tabs.go:28-40` (`tabWidthPx`), `:58-84` (`tabWindowEnd`), `:89-171` (`windowTabRows/ensureTabVisible`): `Dirty` already widens the tab for the dot (`tabDotPx`); `edit_dirty_flag_test.go:59-67` asserts `tab-dot-N` box renders and `tabRow.Dirty==true`.
- Close entry points, **all unguarded**:
  - `internal/app/app.go:2635-2689` (`closeTab/closeTabIn`): calls `a.finishEdit()` (folds buffer, keeps dirty) then drops the tab from `a.tabs/a.tabs2`. No dirty check. Same for split-collapse.
  - `internal/app/actions.go:640-647` (`tab-close:`, `tab2-close:` click), `:2076` (Ctrl+W equivalent → `closeTab(a.active)`).
  - Context-menu bulk closes: `internal/app/context_menu.go:29-33` IDs (`tab-close:`, `-others:`, `-all:`, `-left:`, `-right:`); bridge test only checks IDs at `context_menu_bridge_test.go:37-103` — actual bulk-close handler needs locating before edit (grep found no `closeAll` impl in `app.go`; confirm whether menu bridge maps to repeated `closeTabIn`).
  - `internal/app/editor_group.go:70-107` (`Close/ClosePath`): pure data structure, no dirty notion.
- `README.md:181` documents the limit: *"no unsaved change prompt when a tab closes."* This plan removes that sentence.

### 1d. Session persistence (layout only, no contents)

- `internal/app/session.go:27-54` (`sessionState`): persists `Root/Mode/Theme/Recents/Tabs/Tabs2/Active/Active2/FocusGroup/Split/.../Nav` — **no dirty map, no buffer text, no auto-save setting**.
- `internal/app/app_session.go:11-38` (`EnableSession`), `:40-152` (`restoreState`), `:168-224` (`persistSession/sessionState`): restores tabs by re-opening docs from disk. A dirty close today silently loses the in-memory text on next launch; a dirty tab restored after restart always comes back clean.

### 1e. Confirm modal precedent to reuse

- `internal/app/scm_ops.go:470-575` (`openDiscardConfirm/closeConfirm/confirmDiscard/confirmViewState`, fields `app.go:230-234`): `confirmOpen/Kind/Paths/Title/Msg`, Enter=confirm, Esc=cancel, `actions.go:1632-1648` key handling, `confirmPreviewLimit=6`. The unsaved-close dialog should extend this, not invent a second modal system.
- Settings overlay precedent: `actions.go:198-208` (`settings-open/close`), `app.go:296-297,865-866` (`settingsOpen`, modal flag). Auto-save setting UI lives here.

### 1f. Dirty-adjacent systems

- Explorer rows (`app.go:2297,2332`, `pane.go:255`): carry git `statuses[path]`, **no dirty dot today**.
- Merge editor (`merge.go:105-111,418-433,532,579,638-684`, `app.go:215-228`): separate `mergeDirty` bool + `mergeResult` source of truth; `editMergeResult()` refuses when `a.dirty[path]` is already true (`merge.go:649-654`); `forgetSCMDoc` (`scm_ops.go:608-620`) drops both `docs` and `dirty`.
- Watcher/rescan (`rescan.go:80-94`, `explorer_clipboard.go:526`, `scm_ops.go:624-632`): `installRescan` swaps `a.ws`; external write under a dirty buffer is currently last-write-wins with no warning.

## 2. Gaps

1. **Dirty is global per path, not per tab/group.** Same path open in both groups shares one `a.dirty[path]` — correct for "file is dirty", but close-guard must not double-prompt, and bulk-close must de-dupe.
2. **Tab dot exists; explorer does not.** `tabRow.Dirty` + `tab-dot-N` covered by `edit_dirty_flag_test.go`; explorer tree has no dirty affordance, so a dirty file scrolled out of the tab window is invisible.
3. **Close = silent data loss.** `closeTabIn` folds the buffer (`finishEdit`) so text survives in `a.docs[path]` until eviction/restart, but the tab goes away with no Save/Don't Save/Cancel, and `a.dirty[path]` entry lingers (leak: never deleted on close).
4. **No auto-save.** No timer, no on-focus-change hook, no setting, no persistence for it.
5. **Bulk-close paths unexamined.** Close-others/all/left/right via context menu need the same guard with a multi-file dialog; single-file modal design does not cover them.
6. **Eviction path.** `editorGroup.Open` (`editor_group.go:17-46`) evicts oldest tab past `maxTabs` with no guard — same data-loss shape as explicit close.
7. **Session restores tabs but not dirtiness.** Reopening after quit loses unsaved text; autosave-off users need at minimum a prompt on quit/window close (out of scope for this plan unless decided — see risk).

## 3. Design

### 3a. Dirty flag: keep `map[string]bool`, add helpers, clean on close

- Keep `a.dirty: map[string]bool` as the single source of truth (per path = per file; matches save-to-disk semantics). Do **not** split per group.
- Add `internal/app/dirty.go` (new, small):
  - `func (a *App) isDirty(path string) bool`
  - `func (a *App) setDirty(path string, d bool)` (deletes key when clean to avoid leak)
  - `func (a *App) dirtyTabs(group int) []string` (ordered, de-duped across groups for bulk dialogs)
  - `func (a *App) discardDirty(path string)` — reload `a.docs[path] = a.ws.openDoc(path)` (or `delete(a.docs)` + `forgetSCMDoc`-lite when file is new/untracked), `delete(a.dirty, path)`; if `a.editPath()==path`, `finishEdit`-without-commit (drop buffer, do not fold).
- Migrate all direct `a.dirty[x] = ...` writes (`text_edit.go:351,422`, `edit_ops.go:130,177,197`, `diff_edit.go:242`, `merge.go:679`, `scm_ops.go:619`) to the helper in a mechanical pass.
- On successful close-with-Don't-Save **and** on Save, delete the entry; on Cancel, touch nothing.

### 3b. Indicators: tab dot (keep) + explorer dot (new)

- Tab: no model change (`tabRow.Dirty` already flows). Verify CSS `tab-dot` renders in both groups (`tab2` strip — check template parity; `tabs.go` math is group-agnostic but template may only paint `tab-dot-N` for group 0 — confirm and fix).
- Explorer: add `Dirty bool` to the tree row struct (`view.go` explorer row near `:285`), populate from `a.dirty[node.Path]` where `a.treeVisibleRows`/`fillTree` builds rows (`app.go:~2297`). Paint a small dot/badge with the same accent as `tab-dot`; muted-gray `IGNORED` rows keep precedence (dirty dot only on files, never dirs).
- Status bar: `view.Dirty` count already exists (`app.go:893`); add tooltip/title `"N unsaved files"` — no new field.

### 3c. Close guard modal: extend `confirm*`, three buttons

- New kinds: `unsaved-close-one`, `unsaved-close-many`. Reuse `confirmOpen/Title/Msg/Paths` plus two new fields on `App`:
  - `confirmPending []tabRef` (`{group, index, path}` resolved at open time — indices shift as tabs close, so store path+group and resolve index at execution).
  - `confirmAfter func()` or enum: after Save/Don't-Save completes, continue the original action (close one / close many / evict / quit-if-added).
- Copy:
  - One: Title `"Save changes to X?"`, Msg `"Your changes will be lost if you don't save them."`, Files `[path]`.
  - Many: Title `"Save N files?"`, Msg same, Files preview capped by existing `confirmPreviewLimit`.
- Buttons (all three, keyboard-first):
  - `Save` (Enter, primary): save each pending path (`saveEdit` if live buffer for that path else `saveDirtyActive`-for-path — needs a `savePath(path)` refactor extracting the per-path core out of both), then perform pending closes.
  - `Don't Save` (e.g. `D` / second button): `discardDirty(path)` per pending path, then close.
  - `Cancel` (Esc, existing `closeConfirm` path): close dialog, close nothing, refocus originating tab-close button.
- Interception points (all route through one gate):
  - `closeTabIn(group, index)` → `requestCloseTabIn(group, index)` (dialog when dirty, else direct). Click actions `tab-close:/tab2-close:`, Ctrl+W (`actions.go:2076`), and context-menu singles call the gate.
  - Bulk closes (others/all/left/right): collect dirty subset; clean ones close immediately only after dialog resolves (Cancel must restore nothing — so compute the full close set upfront, apply after choice).
  - `editorGroup.Open` eviction: when `evicted != "" && isDirty(evicted)`, open the one-file dialog with pending = evict-then-open.
  - Merge editor: if `mergeOpen && mergeDirty` and user closes the conflict tab / leaves merge mode / runs `editMergeResult`, reuse the same dialog with `saveMergeEditor` as the Save arm. Do not special-case inside the generic gate — branch on `mergePath`.
- While `confirmOpen`, existing guard (`actions.go:1646-1648`) already swallows other keys — keep; add `left/right` focus between the three buttons + `Enter/S/Esc` handling.

### 3d. Auto-save: modes `off | afterDelay | onFocusChange`

- New setting `autoSave: string` + `autoSaveDelayMs: int` (default `off`, `1000`):
  - `off`: today's behavior + close guard.
  - `afterDelay`: debounce after last `replaceEditRange/restoreEdit/applyEditText`; timer fires `savePath(editPath)` (or active dirty path when no live buffer). Reset on each keystroke; only when `ws.writable()` and file listed in `ws.ByPath` (or absolute external); skip sample workspace with the existing read-only toast suppressed (silent skip + status hint instead).
  - `onFocusChange`: save on `selectTab/openFileAt` group switch, `setMode` explorer↔changes, `toggleEdit`→`finishEdit`, and tab-close gate (Save arm becomes automatic — dialog never appears for autosaved paths).
- Implementation: small `internal/app/autosave.go` with `scheduleAutoSave()` (called from `replaceEditRange`, `restoreEdit`, `applyEditText`), `cancelAutoSave()`, `fireAutoSave(ctx)`. Single `time.Timer` (or `time.AfterFunc` + generation counter to avoid stale fires); test with fake clock or `autoSaveDelayMs=1` + polling. Must run on the app event loop — never write from the timer goroutine directly; post back via existing redraw/refresh channel pattern (check how watcher posts frames; mirror it).
- Dirty indicator stays truthful: dot visible between keystroke and debounced save; tests assert dot-then-clean without user Ctrl+S.
- Interaction with close guard: gate checks `isDirty` at execution time — a path autosaved between dialog-open and button-press drops out of the pending set.

### 3e. Settings persistence

- settingsOpen panel gets an `Auto-save` row (select: Off / After delay / On focus change + numeric delay input shown only for afterDelay, clamped e.g. 250–5000 ms).
- Persist in `sessionState` (per workspace): add `AutoSave string` + `AutoSaveDelay int` with sanitize (`off` default, unknown → `off`, delay clamped). `app_session.go:sessionState/restoreState` read/write them; `persistSession` already diffs via JSON so no extra plumbing. Global default (new workspaces): `off` — least surprising, matches VS Code default `off` historically.
- Theme precedent (`session.go` Theme field + `validTheme`) is the template for sanitize.

## 4. Checklist (atomic boxes)

- [ ] Add `isDirty/setDirty/discardDirty/dirtyTabs` helpers; migrate direct `a.dirty` writes.
- [ ] Delete `a.dirty[path]` on tab close (all arms) — fix leak.
- [ ] Verify `tab-dot` paints in group-1 strip; fix template ID if missing.
- [ ] Add explorer dirty dot (view row field + fill + CSS + test).
- [ ] Add `confirmPending` + `unsaved-close-one/many` kinds; three-button dialog copy.
- [ ] Gate `closeTabIn` via `requestCloseTabIn`; wire click/Ctrl+W singles.
- [ ] Implement `savePath(path)` extracted from `saveEdit`/`saveDirtyActive`; use in dialog Save arm.
- [ ] Wire bulk closes (others/all/left/right) through the many-dialog.
- [ ] Guard `editorGroup.Open` eviction with the one-dialog.
- [ ] Guard merge-editor leave paths (`leaveMergeMode`, `editMergeResult`, conflict close) with merge-aware Save arm.
- [ ] Add `autosave.go` (modes, debounce, focus-change hooks, writable/sample guards).
- [ ] Add settings UI row (mode select + delay input) + session persist/restore + sanitize.
- [ ] Update `README.md:181` (remove "no unsaved change prompt" limit; document auto-save modes).
- [ ] Tests (see §5) green; `go test -p 1 ./...` clean.

## 5. Acceptance + tests

**Acceptance:**
1. Typing in edit mode dots the tab **and** the explorer row; Ctrl+S or debounced auto-save clears both.
2. Closing a dirty tab (×, Ctrl+W, menu) opens Save/Don't Save/Cancel; Save closes and writes disk bytes; Don't Save closes and reverts doc to disk; Cancel keeps tab + text + dirty flag.
3. Bulk close with K dirty files lists up to 6 names, acts on all K per one choice.
4. `afterDelay` saves ~delay after last keystroke without further input; `onFocusChange` saves on tab/mode switch; `off` never autosaves.
5. Setting survives restart per workspace (session JSON round-trip).

**Tests (new, in `internal/app/`):**
- `TestUnsavedCloseGuard_Save/DontSave/Cancel`: open dirty buffer → `requestCloseTabIn` → assert `confirmOpen`, kind, pending; drive each arm; assert disk bytes / doc lines / `isDirty` / tab count.
- `TestUnsavedCloseMany`: two dirty tabs → close-all → many dialog with `Count==2`; Save writes both.
- `TestEvictDirtyTabPrompts`: fill to `maxTabs` with dirty oldest → open one more → dialog names evicted path.
- `TestExplorerDirtyDot`: dirty path asserts explorer row `Dirty==true` + box id present; save clears.
- `TestAutoSaveAfterDelay`: `autoSave=afterDelay, delay=1ms` → type → poll → file on disk updated, dirty cleared, no toast error.
- `TestAutoSaveFocusChange`: dirty → `selectTab`/`setMode` → saved.
- `TestAutoSaveSessionRoundTrip`: set mode+delay → `sessionState()` → sanitize → restore into fresh `App` → values equal.
- `TestMergeCloseGuard`: dirty merge result → leave attempt → dialog; Save arm calls `saveMergeEditor` path (assert no markers error path with clean fixture).
- Keep green: `edit_dirty_flag_test.go:TestEditDirtyFlag`, `diff_edit_test.go:TestCtrlSSavesDirtyWithoutEditBuffer`, `workspace_write_test.go`, `session_test.go`, `context_menu_bridge_test.go`.

## 6. Risks

- **Merge editor (`merge.go`).** Two dirty systems (`a.dirty[path]` vs `mergeDirty`+`mergeResult`). Risk: dialog saves the plain buffer while merge result is newer or vice versa. Mitigation: gate branches — when `mergeOpen && mergePath==path`, Save arm = `saveMergeEditor`, Don't Save = `leaveMergeMode` without write; refuse `editMergeResult` while plain-dirty (existing `merge.go:649` guard stays). Test both directions.
- **Large files.** Debounced auto-save of a 512 KB / 20k-line buffer rewrites + re-tokenizes (`commitEditBuffer` + `ws.writeFile`) per pause — jank and disk churn. Mitigation: skip `afterDelay` autosave when `doc.Truncated || meta.Big`, show "file too large for auto-save" hint; `onFocusChange` still allowed (explicit navigation). Check `maxFileBytes/maxFileLines` constants before implementing.
- **Watcher echo.** Our own `writeFile` triggers fsnotify → `rescanDiskWorkspace` → `installRescan` may swap `ws.ByPath/meta` under a dirty buffer or clobber `docs`. Mitigation: record `lastSelfWrite{path, size, mtime}` on save; rescan path ignores content-change for that path within a short window / same size+mtime; never auto-reload a path with `isDirty(path)` — toast "file changed on disk" instead. Verify `installRescan`'s doc-refresh behavior (whether it calls `openDoc` unconditionally) before finalizing.
- **Bulk-close index shift.** Closing tab 0 then 2 by stored index closes the wrong tab. Mitigation: pending list stores `{group, path}`, resolves index at execution, closes descending.
- **Timer threading.** `time.AfterFunc` firing `savePath` off-loop races the single-buffer model. Mitigation: timer only posts a redraw/action back to the loop (same pattern as watcher frame requests); authoritative save runs on-loop; generation counter drops stale fires.
- **Sample workspace.** `writeFile` refuses sample root; auto-save must silently skip (not error-toast every second). Gate `scheduleAutoSave` on `ws.writable()`.
- **Quit-with-dirty.** This plan guards tab close, not app/window quit. If quit path exists without prompt, dirty text is still lost — call out in README; consider follow-up.
