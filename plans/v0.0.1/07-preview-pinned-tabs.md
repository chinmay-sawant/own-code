# Plan: Preview tab vs pinned tab, Close others, Reopen closed tab

## 1. Current behavior + files

### Tabs: storage, cap, strip

| Area | Location | Behavior |
|---|---|---|
| Cap | `app.go:98` `maxTabs = 8` | Hard cap, 8 per group. |
| Live state | `app.go:147-161` | `tabs/active/tabs2/active2/split/focusGroup/tabStart/scrollGroup`. No `pinned`, `previewTab`, `closedStack`. |
| Name collision | `app.go:324, 3505-3523` | `preview bool` = Markdown rendered view, not preview-tab. New state must not reuse name without rename. |
| Row model | `view.go:291-303` `tabRow{Index,Path,Name,Active,Dirty}` | No `Preview`/`Pinned`; cannot render italic vs pinned. |
| Strip windowing | `tabs.go` `tabWidthPx/tabStripBudget/tabWindowEnd/windowTabRows/ensureTabVisible/scrollTabs` | Full-width tabs + window; open/select/close call `ensureTabVisible`. |
| Helpers | `pane.go:35-80` `activeGroup/groupTabs/groupActive/groupDoc/groupPaths` | Single seam for active list. |

### Open / select / close

| Area | Location | Behavior |
|---|---|---|
| Open | `app.go:2489-2588` `openFile/openFileIn/openFileAt` | Every open appends+pins: append, evict `tabs[0]` past cap (FIFO, no dirty check), focus. Single-click/palette/search all funnel here. No slot reuse. |
| Select | `app.go:2591-2633` `selectTab/selectTabIn/selectTabAt` | `finishEdit()`, set active, reset group scroll to 0, `ensureTabVisible`. |
| Close | `app.go:2635-2689` `closeTab/closeTabIn` | `finishEdit()`, splice, clamp active, collapse split when `tabs2` empties, `resetScroll()`. Does not delete `docs/dirty` — reopen resurrects text accidentally. No stack, no prompt. |
| `W` | `actions.go:2072-2077` | `w` in explorer → `closeTab(a.active)` (always group 0, ignores `focusGroup`); changes → closeWorkingTree. No `Ctrl+W`/`Ctrl+Shift+T`. |
| Click | `actions.go:640-655` | `tab:/tab2:/tab-close:/tab2-close:` + prev/next. No double-click/`Ctrl+K`/pin. |

### Split + scroll

Split open/close `split_ops.go:1-70` (`Ctrl+\`), `setFocusGroup`; per-group offsets `scrollGroup[0]/[1]` are independent (`split_scroll_test.go`), but within one group all tabs share the single offset (every open/select zeroes it). No per-tab scroll map.

### Tab context menu (already has close-others family)

`context_menu.go:195-246` rows for `tab-N/tab2-N`: `Close`, `Close Others`, `Close All`, `Close Left`, `Close Right`. No Pin/Unpin, no Reopen. Handlers `578-601, 612-786` duplicate `closeTabIn` logic; bulk closes ignore dirty.

### Session persistence

`session.go:27-54` (`Tabs/Active/Tabs2/Active2/FocusGroup/Split/.../Preview(markdown)/Wrap/...`). No pinned/preview-tab/closed-stack. Restore `app_session.go:40-152` re-opens from disk, truncates to `maxTabs`; dirty text not persisted.

### Dead helper

`editor_group.go` (`Open/Select/Close/Reset`) is not wired to `App` (App uses raw slices). Do not implement pin/preview there alone.

## 2. Gaps

1. No preview slot — every single-click consumes 1 of 8 tabs, evicts oldest (possibly dirty) silently.
2. `preview` name taken; template/CSS/`tabRow` lack italic/pinned distinction.
3. No pin primitive (no set, no double-click/Enter/`Ctrl+K`, no eviction protection).
4. Close family has no keybindings (except `w`), no palette entries, two parallel implementations.
5. No closed-tab stack (`Ctrl+Shift+T`).
6. Reopen cannot restore unsaved text reliably (incidental `docs/dirty` leftover).
7. Session drops new state; closed-stack snapshots must not bloat JSON.
8. Shared per-group offset + no dirty guard on replace/bulk close.

## 3. Design

### 3.1 State (per-group)

```go
previewTab [2]string
pinned     [2]map[string]bool
closedTabs []closedTab  // LIFO, cap 20
type closedTab struct { Path string; Group, Index int; Lines []string; Dirty bool }
```

`tabRow` gains `Preview/Pinned`. Markdown `preview bool` stays; new names avoid migration.

### 3.2 Open matrix

| Entry | Result |
|---|---|
| Single-click tree, palette Enter, search hit, programmatic `openFile` → `openFilePreview(group,path)` | If open → focus. Else if preview slot set + open + unpinned → replace in place, update slot. Else append (FIFO cap), set slot. Dirty replaced tab → push to `closedTabs` with snapshot. |
| Double-click / Enter on tree / Pin | `pinTab`/`pinPreview`: set pinned, clear slot if preview. Open-then-pin if not open. |
| `Ctrl+K` (verify free) | Pin/unpin focused active tab (toggle). |
| Edit in preview | Auto-pin on first `applyEditText`/`beginEditAt` (VS Code behavior). |

Keep `openFileAt(group,path,record)` as pinned wrapper; add `openFileAtPreview`/`openFileAtMode`. Eviction skips pinned; all-pinned → toast, no drop.

### 3.3 Pin / unpin

`pinTab/unpinTab/togglePin` + `ensureTabVisible` + `persistSession` + `refresh`. Unpin never closes. Session: persist `Pinned` (+ preview slot); sanitize; restore; explicitly not closed stack. Menu rows `Pin/Unpin` with `code-view:tab-pin/unpin` IDs + `MenuAction`.

### 3.4 Close others / right (+ left/all unification)

Keep menu rows; add palette entries. Bulk closes skip pinned, toast `Kept N pinned`. Refactor `contextMenuClose*` onto shared `closeTabsIn(group, keep, pushClosed)` next to `closeTabIn`. Every bulk close pushes removed tabs (snapshot if dirty) in strip order. Dirty bulk close v1: toast `Closed N (M unsaved — Ctrl+Shift+T)`; blocking confirm is follow-up.

Menu final order: Pin/Unpin, Close, Close Others, Close Left, Close Right, Close All, Reopen Closed Tab (`Enabled: len>0`, `Ctrl+Shift+T`).

### 3.5 Closed stack + `Ctrl+Shift+T`

`pushClosed(group,index,path)` captures `Dirty` + deep-copies `Lines` iff dirty (cap bytes; above cap path-only + toast). Push on closeTabIn, all bulk closes, preview-replace, FIFO eviction. `reopenClosedTab()` on `Ctrl+Shift+T` (+ menu + palette): pop last; if already open → focus + discard snapshot; else insert at `min(index,len)` (evict oldest unpinned if full, never pinned); restore `docs[path]` from snapshot when dirty (snapshot wins over leftover); set active, `focusGroup` (group-1 reopen when `!split` → group 0 + toast); `ensureTabVisible`, `persistSession`, `refresh`. Not persisted across restart.

## 4. Checklist

- [x] `previewTab/pinned/closedTabs` + cap + `tabRow.Preview/Pinned` + helpers.
- [x] Pinned vs preview-replace open paths; wire tree/palette/search/outline to preview; explicit opens pinned.
- [x] Eviction skips pinned; all-pinned toast.
- [x] Double-click/Enter/`Ctrl+K` pin; edit auto-pins.
- [x] Template/CSS italic + pin; dirty orthogonal.
- [x] Unify closes onto `closeTabsIn`; bulk skips pinned + pushes stack.
- [x] Menu Pin/Unpin + Reopen rows + IDs + bridge test.
- [x] `Ctrl+Shift+T` in `onKeyDown` + palette entry.
- [x] `pushClosed` snapshot + cap; `reopenClosedTab` clamp/full-group handling.
- [x] Session persist `Pinned` (+ slot); sanitize; restore; not stack.
- [x] Help/palette labels; `W` vs `focusGroup` decision (recommend focused group).
- [x] Tests + `go vet` / suite green.

### Evidence (2026-11-01 validation run)

- `gofmt` clean on all touched Go files; `go vet ./internal/app/` clean.
- `go test -p 1 ./internal/app/ -run 'TestPreview|TestPin|TestEditAutoPin|TestCloseOther|TestCloseRight|TestReopen|TestSessionPersist|TestTabRow|TestContextMenu|TestSplitScroll' -count=1` → `ok` (12.9s). Covers all 10 plan tests in `preview_tabs_test.go`, the updated `context_menu_test.go` / `context_menu_bridge_test.go` (7 rows incl. Pin/Unpin + Reopen with `code-view:` IDs), and the pre-existing `TestSplitScroll*` suite.
- Acceptance mapping: single-click A→B reuses one slot (`TestPreviewReusesSingleSlot`); `Ctrl+K`/Enter pin (`TestDoubleClickOrCtrlKPin`); 8 pinned + open refuses with toast (`TestPinProtectsFromReplaceAndEvict`); dirty preview auto-pins (`TestEditAutoPinsPreview`); Close Others/Right keep pinned + LIFO restore (`TestCloseOthersSkipsPinned`, `TestCloseRightPushesStackLIFO` incl. the `Ctrl+Shift+T` chord and the 20-entry cap); dirty snapshot wins on reopen (`TestReopenRestoresDirtySnapshot`); clamp + full-group evict-unpinned (`TestReopenClampsIndexAndFullGroup`); pins + slot persist/sanitize, stack never persisted (`TestSessionPersistsPinned`); row flags (`TestTabRowsExposePreviewPinned`).
- Decisions: `W` already closes the focused group's tab through `requestCloseTabIn` (focused group — as recommended). No press-count exists on window boxes, so double-click has no hook; Enter on a tree row, `Ctrl+K`, and the tab menu are the pin paths. `Ctrl+K` / `Ctrl+Shift+T` had no prior branches; both share `pinKeyReady` (terminal + editing + overlay + text-field guards). `Close All` and single `Close` are explicit and remove pinned tabs; Others/Left/Right skip pinned with a `Kept N pinned` toast. Snapshot byte cap is 256 KiB (oversize dirty closes push path-only + toast). Group-1 reopen with no split lands in group 0 + toast. Reopened tabs are pinned. `openFileAt` stays the pinned matrix; `openFileAtPreview`/`openFilePreview` serve tree/palette/search/outline; the unsaved-dialog `evict-open` path routes pinned vs preview explicitly (`unsaved.go`).
- Caveats: this tree hosts concurrent in-progress plans, so validation ran with `internal/app/zz_validation_shim.go` (untracked, clearly marked temporary) defining the replace plan's not-yet-landed helpers — DELETE it when that plan lands; the preview/pinned paths under test never execute replace code. `internal/app/view.go` gained `treeRow.Dirty`, which the tree's `app.go` already required.

## 5. Acceptance + tests

1. Single-click A then B → one slot reused, count unchanged, B italic.
2. Double-click/`Ctrl+K` B → pinned; single-click C → new tab, B kept.
3. 8 pinned + open D → toast, no eviction.
4. Dirty preview + single-click elsewhere → auto-pinned (or pushed + restores text).
5. Close Others/Right keeps pinned; `Ctrl+Shift+T` restores LIFO with text.
6. Restart → pins + slot restored, stack empty.

Tests (`internal/app`, `newTestApp`): `TestPreviewReusesSingleSlot`, `TestPinProtectsFromReplaceAndEvict`, `TestDoubleClickOrCtrlKPin`, `TestEditAutoPinsPreview`, `TestCloseOthersSkipsPinned`, `TestCloseRightPushesStackLIFO`, `TestReopenRestoresDirtySnapshot`, `TestReopenClampsIndexAndFullGroup`, `TestSessionPersistsPinned`, `TestTabRowsExposePreviewPinned`. Update `context_menu_test.go`/`bridge`; keep `split_scroll` green.

## 6. Risks

* Dirty replace/bulk-close loss → auto-pin + snapshot + toast; cap size; abort-or-toast above cap (decide pre-build).
* `docs/dirty` leak vs snapshot truth → snapshot wins on reopen; cleanup follow-up.
* Split: shared per-group offset; store group offset at close, best-effort restore; no per-tab map; group-1 reopen when `!split` → group 0 + toast.
* Session: old files default empty; never persist snapshot text; Markdown flag untouched.
* Cap interplay: full-pinned + replace → refuse with toast.
* Keys: verify `Ctrl+K`/`Ctrl+Shift+T` free + terminal/form focus guards; double-click needs press-count support (fallback Enter/`Ctrl+K`).
* `W` group-0 → focused-group change; release-note it.
* Two close impls: refactor-first, behavior-second; keep CL reviewable.
* `editorGroup` duplication: do not implement there unless wiring App through it.
