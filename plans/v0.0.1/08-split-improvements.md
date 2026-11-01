# Split Improvements — Independent Scroll, Focus Switch, Close One Side

## 1. Current behavior + files

### 1.1 Split model: 2 groups, toggle only

| File | Behavior |
|---|---|
| `app.go:155-161` | `split/focusGroup/tabs/active/tabs2/active2/splitRatio (default 0.5)/scrollGroup[2]/splitWheelAccum[2]/tabStart[2]` |
| `split_ops.go:5-39` | `toggleSplit()` only open/close entry. Explorer-only guard. Open mirrors left active into `tabs2`, `focusGroup=1`, `resetScroll()`. Close resets + `focusGroup=0`. No close-left/right/keep-tabs variant. |
| `split_ops.go:50-70` | `setFocusGroup(group)` exists; wired to `focus-group:N` (`actions.go:115-118`). |
| `actions.go:1810-1814` | `Ctrl+\` → `toggleSplit()`. No `Ctrl+1/2`; plain `1/2` are mode switches. |
| `pane.go:35-42` | `activeGroup()` = 1 iff split+focus. Routes `openFile()`, preview, `codeColsForGroup`. |
| `view.go:101-105,191-232` | `View{Split,FocusGroup,PaneLeft,PaneWidth,Second}`; `Second` carries own tabs/rows/scroll. |
| `feature_test.go:270-306` | Canonical contract: open mirrors right, `editor-window-2` exists, closing last `tabs2` auto-closes split. |

### 1.2 Independent vertical scroll — mostly done, shared leftovers

* `split_scroll.go:53-163`: `windowSliceForGroup` from `scrollGroup[group]` (split) vs `scrollY` (single); `maxGroupScroll/clampGroupScroll/scrollGroupBy/To`; single-pane mirrors group 0 → `scrollY`; `scrollFocusedGroup` routes arrows/pgup/pgdn/home/end.
* `actions.go:880-893`: wheel routes via `isOverPane(1/0)`; `editor_scroll.go:16-53` per-group rails/drag (`editor-scroll-thumb[-2]`).
* Coverage: `editor_scroll_test.go:39-62`, `split_scroll_test.go` (6 tests) — independent offsets, slicing, wheel/keyboard routing, clamping, toggle-reset, tab-switch-preserves-other-pane.
* Leftovers (gaps): `scrollTo` always writes group 0; `resetScroll` zeroes both + `scrollX`; `openFileAt/selectTabAt` zero the group (destroys per-tab position, no per-path memory); `closeTabIn` blanket `resetScroll()`; horizontal `scrollX` single shared field (`hscroll.go`); `onScroll` ignores offsets in split; single page spacer reflects group 0 only.

### 1.3 Focus switch — state exists, affordance missing

`setFocusGroup` + click action exist, but no plain body-click focus, no active-pane highlight field (`FocusGroup` only), no keyboard switch (`Ctrl+1/2` unassigned), `w` closes group 0 even when focus is group 1 (likely bug).

### 1.4 Close-one-side — missing

Only `toggleSplit()` (retains hidden `tabs2`) + auto-close on empty `tabs2`. No `closeGroup(0|1, preserve)`, no merge/drop policy, no menu/palette/shortcut. `w`/close wrapper group-0-only.

### 1.5 Drag divider — does NOT exist

`splitRatio` never mutates except init + session restore. No `split-divider` box, no `splitDrag` state (`onDragStart/Move/End` handle other thumbs/dividers only). Diff-revert divider is unrelated.

## 2. Gaps

1. Per-tab scroll memory (open/select zero group; test asserts reset-to-0 — must change to restore).
2. Per-group horizontal scroll (`scrollX` → per-group).
3. Page/spacer reconciliation (single `page.ScrollTo`/`SpaceHeight`/`onScroll` vs two windows — keep virtual, document).
4. Focus affordance + shortcuts (highlight, click-anywhere, `Ctrl+1/2`, `F6` cycle fallback).
5. Close-one-side with merge/keep/drop policies + dirty guards + renumber.
6. Draggable divider (new box + drag + keyboard + persist).
7. Key-nav audit (`w` → focused group; arrows h-scroll → focused; edit/find/preview singletons).
8. Session: persist `split/focusGroup/tabs2/ratio` (+ optionally not per-tab scroll).

## 3. Design

### 3.1 Per-group scroll + rails (no page-model rewrite)

* Keep `scrollGroup[2]` + `splitWheelAccum[2]` in split; `scrollY` single-pane only.
* Add `tabScroll [2]map[string]int` (same path can differ per side) + `scrollXGrp [2]int` (mirror to `scrollX` single-pane) + `splitDrag` state.
* `openFileAt/selectTabAt`: save `scrollTab[group][old]`, restore saved-or-0, clear only that group's accum; never blanket `resetScroll()` in split; add `resetGroupScroll(group)`.
* `closeTabIn`: `resetGroupScroll(group)` + `ensureTabVisible(group)`; blanket reset only on collapse/last-tab.
* H-scroll: thread `group` through `scrollHorizontal`; split shift+wheel + arrows route to hovered/focused group (test `isOverPane(1)` before 0).
* Rails unchanged structurally; add thumb-2 isolation test.
* `onScroll` in split: ignore `offsetY` for groups, still update viewport + rebuild + regression test.
* `SpaceHeight`: keep group-0 (document right pane virtual + own rail); fallback `max(h0,h1)` if web QA fights.

### 3.2 Focus: highlight + `Ctrl+1/2` + click

* Template class from `FocusGroup` (recommend `Second.Focused` + `View.FocusedPane` for clarity); 1-2px accent underline/dim inactive; reuse theme tokens.
* `onClick` fallback: `editor-window-2/code-body-2/…-2` → focus 1; un-suffixed → focus 0; before `Focus(ctx,"")`; must not steal caret clicks; welcome click focuses side.
* `onKeyDown` ctrl-gated (before mode keys): `Ctrl+1` → focus 0; `Ctrl+2` → focus 1 (no-op single); optional `F6` cycle. Plain `1/2` untouched. Decide terminal-focused behavior (recommend yes, like `Ctrl+``).
* `setFocusGroup`: keep `finishEdit/preview=false/resetFind`; add `ensureTabVisible(group)`.

### 3.3 Close-one-side preserving tabs

```go
type splitCloseMode int // merge-into-other vs drop
func (a *App) closeSplitSide(side int, mode splitCloseMode)
```

* Default explicit affordance: merge tabs into survivor (dedup, order, cap `maxTabs` + toast); `Ctrl+\` keeps legacy hide (retain hidden `tabs2`).
* Closing side 0 → renumber survivor to group 0 (copy tabs/active/scroll/tabStart) so single-pane invariants hold.
* Guards: `finishEdit` on closing side (consistent with `closeTabIn`); drop-mode with dirty → toast + abort.
* UI: menu items per strip (`Close pane`, `Close other pane`), palette commands, optional `Ctrl+Alt+1/2`. Last-tab-of-0-while-split → collapse to renumbered 1 (decide + test). Auto-close on empty-1 stays.

### 3.4 Drag divider (new)

* Template `split-divider` box (full height, ~5-7px, centered on `PaneLeft+PaneWidth`, focusable, `aria-label`, col-resize cursor). Reuse `history-divider` focus pattern.
* `onDragStart` claims when split+explorer → `splitDrag`; `onDragMove(dx)`: `ratio = start + dx/editorWidth`, clamp `[paneMinWidth, total-min]` + session min/max; `refresh + Redraw`, no scroll touch (wrap recomputes; pin top line via `topSourceLine` optional).
* `onDragEnd`: `persistSession`; refocus pane. Keyboard on focused divider: arrows adjust, home resets default.
* Persist `splitRatio` (already in session); add missing `split/focusGroup/tabs2` if needed.

### 3.5 Keyboard summary

`Ctrl+\` toggle (existing, explorer-only); `Ctrl+1/2` focus (new); `F6` cycle (optional); arrows/pgkeys → focused (keep); `w` → fix to focused group; divider-focused arrows/home → resize/reset.

## 4. Checklist

- [x] Audit `app_session.go` persisted split fields; decide tab-scroll persistence (recommend no).
- [x] `tabScroll[2]map + scrollXGrp[2]`; init + session load.
- [x] `resetGroupScroll(group)`; replace split-path blanket resets.
- [x] Save/restore on open/select/close; update preserve-other-pane expectation (restore not zero).
- [x] Group-threaded h-scroll + routing + tests.
- [x] `onScroll` split guard + regression test.
- [x] Click-to-focus + tests; `Ctrl+1/2` + help text.
- [x] Active-pane highlight + box test.
- [x] Fix `w` → focused group + test.
- [ ] `closeSplitSide(merge|drop)` + renumber + dirty guard + menu/palette/shortcut + tests.
- [x] `split-divider` drag + keyboard + clamp + persist + tests.
- [x] Extend `split_scroll_test.go`, `editor_scroll_test.go`, `feature_test.go`.
- [ ] Web-mode + zoom QA; docs/help/session note.

## 4b. Evidence (2026-11-01, implementer session)

Validated in an isolated `HEAD` worktree (`/tmp/opencode/split08-base`, with
`../ownframe` symlinked): `gofmt` clean on touched files, `go vet
./internal/app/` clean, `go test -p 1 ./internal/app/ -run
'TestSplit|TestEditorScroll|TestHscroll|TestScroll' -count=1` green, and the
full `go test -p 1 ./internal/app/ -count=1` green. The same change set is
ported into the working tree; the working tree itself does not compile right
now because of another worker's in-flight refactor (missing
`replaceRegex`/`searchReplace`/confirm-field symbols, duplicate
`paletteCommandRows`), so working-tree test evidence is pending their
landing. New tests live in `internal/app/split_improvements_test.go`.

- Audit: `sessionState` carries `Split/FocusGroup/Tabs2/Active2/SplitRatio`
  (`session.go`), restored in `app_session.go` (`restoreState`) and snapshotted
  in `sessionState()`; `tabScroll`/`scrollXGrp`/drag state are runtime-only by
  decision. Session note added to README Limits.
- `tabScroll [2]map[string]int` + `scrollXGrp [2]int` + `splitDrag` on `App`
  (`app.go`), initialized in `New`/`resetLayout`/`switchProject`, guarded in
  `resetScroll`; toggle-open seeds both groups from `scrollX`.
- `resetGroupScroll(group)` (`split_scroll.go`); split paths in
  `openFileAt`/`openFileAtMode`(`finishOpenCommon`)`/`selectTabAt`/`closeTabIn`/`closeTabsIn`/menu
  close use save/restore or per-group reset — no blanket `resetScroll()`.
  `toggleSplit` keeps its legacy blanket reset.
- `TestSplitScrollTabSwitchPreservesOtherPane` now asserts restore (round
  trip), plus `TestSplitScrollPerTabRestore`. Note: a file *open* still ends
  with the first-change jump, so reopen-restore only survives for changeless
  files; tab switches restore unconditionally.
- `scrollHorizontalFor/scrollXFor/setScrollXFor/maxLineWidthFor/hscrollWindowFor`
  (`hscroll.go`); wheel tests `isOverPane(1)` before 0, arrows route to the
  focused group (`TestSplitHScrollIndependent`).
- `onScroll` ignores `offsetY` for scroll state in split; viewport resizes
  still rebuild (`TestSplitOnScrollInert`). `SpaceHeight` stays group-0/max
  with a code comment; dual-spacer out of scope.
- `focusGroupForClick` + `onClick` fallback (`TestSplitFocusClick`);
  `Ctrl+1/2` before terminal keys (like `Ctrl+\``) + `F6` cycle + keymap
  catalog entries (`focus-left/focus-right/cycle-pane`) + README Keys rows
  (`TestSplitFocusKeys`; plain `1/2` asserted untouched).
- `View.FocusedPane` + `Second.Focused` drive the `focus` classes;
  `.editor.split:not(.focus) .tabstrip` dims the idle pane
  (`TestSplitActiveHighlight`, incl. `split-divider` box geometry).
- `w` closes the focused group's tab (`TestSplitWClosesFocused`).
- Divider: `split-divider` button + `splitDividerGeometry` + drag
  start/move/end with `/uiZoom` math, clamp (`clampSplitRatio`: session
  0.2–0.8 + `paneMinWidth`), scroll-preserve, `persistSession`, keyboard
  arrows/`Home` (`TestSplitDividerDrag`, `TestSplitDividerKeyboard`).
- Rail isolation: `TestSplitThumbDragIsolation`. Zoom rescales groups +
  memory (`setZoom` split-aware tail, `TestSplitZoomRescalesGroups`).
- Close-side box left unchecked: merge/drop/renumber-to-0/dirty-guard/menu
  rows/shortcut are implemented and tested (`TestSplitCloseSideMerge`,
  `TestSplitCloseLeftRenumber`, `TestSplitCloseDirtyBlock`,
  `TestSplitCloseSideDrop`, `TestSplitCloseMergeCap`,
  `TestSplitCloseLastLeftAdoptsRight`, `TestSplitPaneMenu`), but **palette
  commands are missing** — the palette is file-only and its command-row
  infrastructure is another worker's in-flight change. Wire
  `code-view:split-close:<side>:<merge|drop>` into the palette once that
  lands.
- Web/zoom box left unchecked: zoom rescale is implemented + tested and docs
  (README Keys/Limits/Features, divider + pane `title`/`aria-label` help)
  are done; **web-mode QA was not performed** (no web harness available).

## 5. Acceptance + tests

1. Panes scroll V+H independently (wheel/rail/keyboard); other pane byte-identical.
2. Click-anywhere focuses with highlight; `Ctrl+1/2` switches; pgkeys follow focus.
3. Close-one-side merges by default, never loses unedited tabs; dirty-block toasts; left-close renumbers; focus lands survivor.
4. Divider drag clamps, persists, preserves offsets; keyboard resizes.
5. `Ctrl+\` + plain `1/2` unaffected.

Tests: `TestSplitScrollPerTabRestore`, `TestSplitHScrollIndependent`, `TestSplitOnScrollInert`, `TestSplitFocusClick/Keys`, `TestSplitCloseSideMerge`, `TestSplitCloseLeftRenumber`, `TestSplitCloseDirtyBlock`, `TestSplitDividerDrag`, `TestSplitWClosesFocused`; extend thumb/track tests; adjust toggle-reset/clamp/routing suites.

## 6. Risks

* Page model: one spacer/callback vs two windows — keep spacer group-0/max, document, test inert; dual-spacer out of scope.
* Web mode: zoom-divided drag math (`/uiZoom` like `termDrag`), small-width hit targets, `paneMinWidth` vs `minWidth`.
* Perf: full `View` rebuild per wheel/drag tick; throttle ratio-delta, skip refresh when clamped; wrap recompute hot path.
* Singletons (edit/find/preview): focus switch keeps `finishEdit/resetFind`; rapid toggling loses in-progress edit (accepted); Markdown-in-both-panes test.
* Collisions: terminal/browser capture `Ctrl+1/2`; fallback `F6`/menu; plain `1/2` keep working when `!ctrl`.
* Session compat: clamp/validate old files; don't persist per-tab scroll.
* Fragility: pin tests to behavior (offsets, `Rows[0].No`, focus, ratio range) over exact pixels.
