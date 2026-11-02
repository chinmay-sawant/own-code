# own-code v0.0.1 — Master Phase-Wise Checklist

> **Repo:** `/home/chinmay/ChinmayPersonalProjects/own-code`
> **Style:** scale-file, after `goslop/plans/port-phasewise-checklist.md`
> **Status:** draft — all boxes unchecked unless marked `[x]` with evidence
> **Canonical ledger:** this file only (do not fork status docs)
> **Scope:** v0.0.1 = editor core file ops, dirty/autosave, replace, goto/indent/comment, display settings, symbol/recent/breadcrumb, preview/pinned tabs, splits, settings persist, lazy load

---

## Overview

Ship a coherent v0.0.1 editor pass on top of the current app (`internal/app/`, `internal/edit/`, `internal/wsl/`, entry `cmd/own-code/main.go`) while keeping:

1. **Current product surface intact** — explorer, in-place editor (`Ctrl+E`/`Ctrl+S`), unified/split diffs, history lane, Markdown preview (`Ctrl+Shift+V` / `M`), six themes, terminal, merge resolver (README Features).
2. **Explicit limits honored** — 512 KB / 20,000-line / 32 KB-line caps, shared-scroll split, 200-hit workspace search, fsnotify desktop-only watcher, no unsaved-change prompt (README Limits — to be revisited deliberately in Phase 2).
3. **Key bindings stable** — `Ctrl+P/F/Shift+F/E/S/A/C/V/\`, `Z`, `N/P`, `R/W`, `Esc`, `F12` (README Keys). New bindings added only in their phase with docs + tests.
4. **Evidence per box** — path + command (`go test -p 1 ./...`, `go vet`, `gofmt -l`, `go run ./cmd/own-code`).

Grounding (skimmed 2026-11-01): `internal/app/` ~182 `*.go` files — `edit_ops.go`/`text_edit.go`/`selection_ops.go` (buffer ops, `replaceEditRange`, `dirty` map), `find.go` (in-file find, `findStep`), `search_ops.go`/`workspace_search.go` (workspace search), `session.go`/`app_session.go` (`sessionState`, `persistSession`/`restoreState`), `tabs.go`/`editor_group.go` (`editorGroup`, 8-tab model), `split_ops.go`/`split_scroll.go` (second group, shared scroll), `outline.go` (`outline`/`symbolRow` per-lang), `project_recent.go`/`quick_roots.go` (recent projects/roots), `file_nav_ops.go` (changed-file nav), `explorer_paste.go`/`workspace.go` (`ws.Lazy`, `loadDiskWorkspaceLazy`), `markdown_preview.go`, `themes.go`, `wrap.go`, `zoom.go`/`zoom_sections.go`, `watch.go`.

---

## Executive Summary

| Phase | Name | Goal | Depends |
|------:|------|------|---------|
| 0 | Bootstrap + baselines | Green tree, proof commands, fixture corpus, key inventory | - |
| 1 | Editor core file ops | Open/save/reload/close, large-file guards, external-file path | 0 |
| 2 | Dirty + autosave | Dirty model, tab/gutter flags, autosave + unsaved-close prompt | 1 |
| 3 | Replace | In-file replace (next/all, case, selection-scoped), preserve undo | 1–2 |
| 4 | Goto / indent / comment | Goto line, auto-indent, toggle comment per language | 1–3 |
| 5 | Display settings | Tab width, soft wrap, minimap/line-numbers toggles, zoom persist | 1 |
| 6 | Symbol / recent / breadcrumb | Outline symbols, recent files, breadcrumb bar + navigation | 1, 5 |
| 7 | Preview + pinned tabs | Markdown/preview tabs, pinned tabs, tab windowing (8-tab budget) | 1–2, 6 |
| 8 | Splits | Second group ops, focus, shared-scroll contract, split persistence | 1–2, 7 |
| 9 | Settings persist | Unified settings store, session restore, recent projects | 2, 5–8 |
| 10 | Lazy load | Lazy workspace/tree, on-demand dir load, paste/rescan integration | 1, 9 |
| 11 | v0.0.1 gates + delivery | Full proof suite, manual pass, README Keys/Limits refresh, release notes | 2–10 |

**Proof commands (repo root):**

```bash
go test -p 1 ./...
go vet ./...
gofmt -l internal cmd
go run ./cmd/own-code
go run ./cmd/own-code -root ./testdata/sample
```

---

## Phase 0: Bootstrap + baselines

### 0.1 Tree + toolchain
- [ ] `go 1.26` + sibling `../ownframe` replace documented; `go mod tidy` clean
- [ ] `go test -p 1 ./...` PASS recorded (note failures/host)
- [ ] `go vet ./...` clean; `gofmt -l internal cmd` empty
- [ ] `go run ./cmd/own-code -root ./testdata/sample` launches; screenshot script path noted (`OWNFRAME_CODE_VIEW_SCREENSHOT=1 go test -run '^TestScreenshot$' ./internal/app`)

### 0.2 Baseline inventory
- [ ] Key inventory: current README Keys table reconciled with `actions.go` bindings (`Ctrl+P/F/Shift+F/E/S/A/C/V/\`, `Z`, `N/P`, `R/W`, `Esc`, `F12`)
- [ ] Limit inventory: 512 KB / 20,000-line / 32 KB-line / 200-hit search / 8-tab / shared-scroll caps listed with code refs (`workspace.go`, `workspace_search.go`, `tabs.go`/`editor_group.go`, `split_scroll.go`)
- [ ] Fixture corpus fixed: `testdata/sample` (via `owncode.SampleFiles`) + `fixtures/` scenarios for large-file/binary/long-line cases
- [ ] Session log started (see Session log stub)

---

## Phase 1: Editor core file ops

> Files: `internal/app/edit_ops.go`, `text_edit.go`, `workspace.go` (`readLines`, `ensureFile`, `ensureExternalFile`), `pane.go`, `errors.go`

- [ ] Open path: `openFile`/`openFileIn`/`openFileAt` covers workspace + absolute/external (`ensureExternalFile`, palette absolute path)
- [ ] Save path: `saveEdit` writes buffer, clears `dirty[path]`, rescan-preserving (`rescan.go` `installRescan`)
- [ ] Reload path: `R` reloads open file from disk; discards buffer with explicit user confirm (ties to Phase 2 prompt)
- [ ] Close path: `W` closes active tab; edit buffer teardown (`finishEdit`) without losing other tabs' buffers
- [ ] Large-file guards: >512 KB / NUL byte → notice; >20,000 lines → first 20,000 + notice; >32 KB line → notice row (README Limits preserved or changed explicitly)
- [ ] New file + Save-As: empty buffer → save to workspace path; `workspace_write.go` `ensureFile` + rescan keeps lazy trees valid
- [ ] Read-only states: binary/diff-notice/commit-patch rows never enter edit mode (`beginEditAt` guards)
- [ ] Tests: open/save/reload/close round-trip, external absolute path, each large-file guard, read-only guard — `go test -p 1 ./internal/app/ -run 'TestEdit|TestWorkspace|TestSave'`

---

## Phase 2: Dirty + autosave

> Files: `internal/app/text_edit.go` (`replaceEditRange`, `dirty` map), `pane.go` (`EditDirty`), `edit_dirty_flag_test.go`, `feature_test.go`, `merge.go` (`mergeDirty`)

- [ ] Dirty model: single source — buffer `Dirty` → `a.dirty[path]` → `View().EditDirty` → tab dot + status; caret-only moves never set dirty
- [ ] Dirty indicators: tab marker (cf. `edit_dirty_flag_test.go`), status bar, gutter/line-card interplay limited to working-tree changes (no commit-patch leakage)
- [ ] Save clears dirty everywhere: buffer, map, tab row, status (cf. `feature_test.go` save-clears assertions)
- [ ] Autosave: debounced save-after-idle + save-on-focus-loss, configurable interval + enable flag; respects large-file/read-only guards; never autosaves merge-conflict staging implicitly
- [ ] Unsaved-close prompt: closing dirty tab / quitting / reloading offers Save / Discard / Cancel (replaces README "no unsaved change prompt" limit — update README explicitly)
- [ ] External change vs dirty: watcher refresh (`watch.go`) never silently overwrites dirty buffer; offers Reload/Diff/Keep
- [ ] Tests: caret-no-dirty, type-sets-dirty, save-clears, autosave fires/skips, close-prompt all three outcomes, external-change guard

---

## Phase 3: Replace

> Files: `internal/app/find.go` (`findHit`, `reindexFind`, `findStep`, `toggleFindCase`), `search_ops.go`, `selection_ops.go`, `edit_ops.go` (`onType/onPaste/insertEditText/replaceEditRange`)

- [ ] Replace UI: find bar extends to replace field; `Enter` next, `Shift+Enter` prev, replace-next / replace-all buttons + key bindings documented
- [ ] Case + scope: match-case toggle (reuse `toggleFindCase`), whole-word option, scope = selection if active else whole file
- [ ] Replace-next: single substitution at current hit, caret follows, undoable as one step (`pushUndo`)
- [ ] Replace-all: count + single undo unit; capped with progress/notice on huge files (respect 20,000-line budget)
- [ ] Regex optional (only if agreed): literal default; regex behind explicit toggle with invalid-pattern error state, no panic
- [ ] Find/replace state survives tab switch per file; `Esc` closes bar and restores focus (`closeFind`)
- [ ] Tests: next/prev wrap, case on/off, selection-scoped, replace-all count + single undo, regex-error path, `go test -p 1 ./internal/app/ -run 'TestFind|TestReplace'`

---

## Phase 4: Goto / indent / comment

> Files: `internal/app/text_edit.go`, `edit_ops.go`, `syntax.go` (+ `syntax_markdown/markup.go`), `actions.go` (key wiring)

- [ ] Goto line: `Ctrl+G` (new binding) palette field `:<line>[:<col]>`; out-of-range clamps with status note; `outlineLine(line)` reuse for scroll-into-view
- [ ] Goto symbol in file: `Ctrl+Shift+O` jumps via Phase 6 outline rows (deferred to Phase 6 if outline not ready — mark `[~]` with reason)
- [ ] Auto-indent: newline copies leading whitespace; `Tab`/`Shift+Tab` indent/dedent selection or insert `tabSpaces` at caret (`text_edit.go` tab expansion honored)
- [ ] Toggle comment: `Ctrl+/` per-language prefix (`//` Go/JS/TS, `#` Python/shell/YAML, `<!-- -->` HTML, `--` SQL if present); line vs block selection; idempotent re-toggle
- [ ] Comment in Markdown/code-fence aware: no-op or correct prefix inside fenced blocks (cf. `pane.go` fenced-code guard precedent)
- [ ] Keys docs: README Keys + in-app hints updated; no collisions with `F12`/`Tab` accessibility contract
- [ ] Tests: goto valid/clamped/invalid, indent/dedent round-trip, toggle-comment per language + idempotence, fenced-block case

---

## Phase 5: Display settings

> Files: `internal/app/wrap.go`, `zoom.go`/`zoom_sections.go`, `themes.go`, `hscroll.go`, `view.go`, `components.go`

- [ ] Tab width: 2/4/8 selector; applies to editor render + `text_edit.go` tab expansion consistently
- [ ] Soft wrap: global `Z` + per-file override; diff clipping-with-ellipsis preserved when wrap off (README Limits behavior kept)
- [ ] Line numbers + whitespace rendering toggles; gutter width reflows without breaking `gutter_markers.go` paint
- [ ] Font/zoom: editor zoom in/out/reset (`resetZoom`), persisted per Phase 9; preview zoom (`preview_zoom_test.go`) follows same setting or explicit split setting
- [ ] Theme: six themes selectable + persisted; `activeThemeCSS` regression-free (`themes_test.go`)
- [ ] Settings surface: single place (command palette or Details panel section) — no scattered flags; every toggle has key + command + persisted value
- [ ] Tests: each toggle render assertion, wrap on/off diff-clip case, zoom persist round-trip stub (full persist in Phase 9)

---

## Phase 6: Symbol / recent / breadcrumb

> Files: `internal/app/outline.go` (`outline`, `symbolRow`, per-lang `outlineGo/Python/JS/Markdown/CSS`), `project_recent.go`, `quick_roots.go`, `file_nav_ops.go`, `palette.go`/`palette_search.go`, `tabs.go`

- [ ] Symbols: outline rows (name/kind/line/depth, 64-rune cap, row cap) shown in Details drawer; click/`Enter` jumps (`outlineLine`); new languages reuse `addSymbol` contract
- [ ] Symbol search: `Ctrl+Shift+O` filters current file symbols (fuzzy on name, `kind` badge); `Enter` jumps, `Esc` closes
- [ ] Recent files: MRU per workspace (cap, dedupe, missing-file eviction like `loadRecentProjects`); quick-open section + `Ctrl+Tab` switcher
- [ ] Breadcrumb: path segments bar above editor (root › dir › file); click segment opens folder / jumps; long paths clip with ellipsis (cf. `file_crumb_clip_test.go`, `browse_path_clip_test.go`)
- [ ] Breadcrumb + symbol combo: trailing segment shows enclosing symbol at caret (outline lookup), updates on scroll/edit
- [ ] Tests: outline per-language goldens, MRU cap/evict, breadcrumb clip + click nav, symbol-jump scrolls correctly

---

## Phase 7: Preview + pinned tabs

> Files: `internal/app/markdown_preview.go` (`previewRows`, `previewText`), `markdown_html.go`, `markdown_mermaid.go`, `tabs.go`, `editor_group.go`, `pane.go`

- [ ] Preview tabs: `Ctrl+Shift+V`/`M` opens side preview as a real tab (not only toggle overlay); source↔preview sync scroll; fixed-rows approximation documented (README Limits kept)
- [ ] Preview fidelity: tables + basic Mermaid flowcharts/sequence (no subgraphs) — `preview_fit/spacing/style/zoom_test.go` green
- [ ] Pinned tabs: pin/unpin action; pinned tabs compact to left, immune to 8-tab auto-evict and `W`-close-all-unpinned; persist order in Phase 9
- [ ] Tab windowing: 8-tab budget + `windowTabRows`/`ensureTabVisible`/`scrollTabs` overflow arrows; active tab always visible after open/close/pin
- [ ] Dirty + pinned/preview interplay: dirty dot on pinned; preview tabs never dirty; closing pinned requires explicit unpin or confirm
- [ ] Tests: preview open/sync/close, pin immune-to-evict, windowing arrows, `tab_fullname_test.go` + `tabs_windowing_test.go` green

---

## Phase 8: Splits

> Files: `internal/app/split_ops.go`, `split_scroll.go`, `editor_group.go`, `pane.go`, `actions.go` (`toggleSplit`, `setFocusGroup`, `activeGroup`)

- [ ] Split ops: `Ctrl+\` toggles second group; open-in-split, close-split (focus returns, tabs merge or drop per explicit rule), swap groups
- [ ] Focus model: exactly one focused group; guarded `openFileIn`/`closeTabIn(group, …)`; status + breadcrumb reflect focused group
- [ ] Shared-scroll contract: both groups share one window + page scroll (README Limits) — `split_scroll.go` keeps same row offset; document as kept limit, not silently changed
- [ ] Split persistence: group tabs + actives + focus survive reload via Phase 9 session; single-group sessions restore cleanly
- [ ] Diff + split: split diff (`S`/`U`) and editor split compose without row-ID collisions (`split_divider_revert_test.go` green)
- [ ] Tests: toggle/focus/close rules, shared-offset assertion, persist round-trip, `split_scroll/divider_revert/editor_group_test.go` green

---

## Phase 9: Settings persist

> Files: `internal/app/session.go` (`sessionState`, `saveSession`/`loadSession`, `sanitize`), `app_session.go` (`persistSession`, `restoreState`, `sessionState`), `project_recent.go`, `quick_roots.go`

- [ ] Unified settings store: one JSON file (display + editor + autosave + preview + split + recent), versioned, atomic write, corrupt-file → defaults + toast (never crash)
- [ ] Session restore: open tabs both groups, actives, focus, scroll, wrap/zoom/theme, preview open, pinned set — `sanitize` drops missing paths, clamps indexes/ratios (`clampSessionIndex/Ratio/Zoom`, `capSessionPaths`, `cleanSessionPaths`)
- [ ] Recent projects: MRU 10 (`recentProjectsMax`), missing-dir eviction (`TestLoadRecentProjectsDropsMissing`), startup root resolution (`ResolveStartupRoot`), `-root` override wins
- [ ] Dirty is never persisted as content: dirty files restore as saved-on-disk + optional "was dirty" marker; no phantom edits
- [ ] Migrate + docs: old session files migrate or reset with notice; settings documented in README/docs (location, keys, reset procedure)
- [ ] Tests: save/load round-trip, sanitize missing + clamp cases, corrupt-file fallback, `session/project_recent/rescan_test.go` green

---

## Phase 10: Lazy load

> Files: `internal/app/workspace.go` (`loadDiskWorkspaceLazy`, `Lazy`, `LoadDir`/`loadDir`/`loadNode`, `ensureFileLoaded`, `loadChildren`), `explorer_paste.go`, `rescan.go`, `browse_project.go`, `lazy_tree_test.go`

- [ ] Lazy workspace default for large trees: top-level only at startup (`loadDiskWorkspaceLazy`), `Truncated=false`; `-files` budget still honored with status-bar note
- [ ] On-demand dir load: expand/toggle/search/open triggers `LoadDir`; open-folders warmed on render (`ensureOpenFoldersLoaded`); file open ensures parents (`ensureFileLoadedOnOpen`)
- [ ] Paste/rescan integration: `explorer_paste.go` lazy branches + `sortPasteFiles` ordering; `rescan.go` preserves `Lazy` across installs (`old.Lazy → next.Lazy`)
- [ ] Search over lazy tree: quick-open (`palette_search.go` rg/git-ls paths) + workspace content search cover unloaded dirs with honest caps (200-hit / 8-per-file); ignored-dir placeholders (`IGNORED` badge + single placeholder row) preserved
- [ ] Perf budget: startup scan time + memory on a fixed large fixture recorded; no full-tree walk on open; `perf_editor_bench_test.go` green, no regression vs eager baseline
- [ ] Tests: `lazy_tree_test.go` suite (initial-load, load-dir, toggle, file-node children, ensure-on-open) + paste-into-lazy + rescan-preserves-lazy

---

## Phase 11: v0.0.1 gates + delivery

### 11.1 Proof suite
- [ ] `go test -p 1 ./...` PASS (full, serial — required: parallel flakes historically)
- [ ] `go vet ./...` clean; `gofmt -l internal cmd` empty
- [ ] `go run ./cmd/own-code -root ./testdata/sample` smoke: open/edit/save/replace/goto/split/preview/session-restore/lazy-expand each exercised
- [ ] Screenshot pass: `OWNFRAME_CODE_VIEW_SCREENSHOT=1 go test -run '^TestScreenshot$' ./internal/app` → `/tmp/opencode/code-view-shots/` frames eyeballed (explorer, preview, palette, changes, split, details, edit, search)

### 11.2 Docs + release
- [ ] README Features/Keys/Limits refreshed: new bindings (replace, goto, comment, pin, symbol), autosave + unsaved-prompt change, preview-tab + breadcrumb + lazy-load notes
- [ ] `docs/` deltas: terminal/merge docs untouched or explicitly updated; new settings doc if store added
- [ ] Release notes stub: scope (the 11 features), known gaps marked `[~]` with reason, next version candidates
- [ ] Checklist reconciled: every `[x]` has path + command evidence; every `[~]` links an issue/reason; no silent stubs in default flows

---

## Dependencies (phase graph)

```
0 → 1 → 2 → 3 → 4
        ↓   ↘
        5 → 6 → 7 → 8
        ↓       ↓   ↓
        └——→ 9 ←┘───┘
        1 → 10 (needs 9 for persist)
        2–10 → 11
```

- Phases 3 (replace) and 4 (goto/indent/comment) need the Phase 2 dirty/undo contract.
- Phase 6 (symbol) feeds 4-goto-symbol and 7-preview/symbol-jump.
- Phase 9 (persist) depends on display (5), tabs (7), splits (8) shapes being final.
- Phase 10 (lazy) builds on Phase 1 file ops + Phase 9 session shape; lands before final gates.

---

## Working agreements

1. **README Limits are load-bearing** — changing a limit (unsaved prompt, lazy vs full scan, shared scroll) requires a README edit in the same change.
2. **One checklist** — update this file when closing work; do not fork status docs.
3. **Atomic rows** — check a box only with path + command evidence (`go test -p 1 ./...` at minimum for behavior rows).
4. **Quarantine honestly** — if a feature cannot meet the phase bar yet, mark `[~]` with reason + follow-up (like goslop taint FNs).
5. **Small phases** — land editor-core + dirty before replace/goto; land tabs before splits before persist.
6. **No silent key grabs** — every new binding is checked against the Keys table + `Tab`/`F12` accessibility contract, with a test.
7. **Serial tests are the gate** — `go test -p 1 ./...` is the proof command; parallel runs are informational only.

---

## Session log

| Date | Session | Outcome |
|------|---------|---------|
| 2026-11-01 | Draft | Master checklist drafted from README + `internal/app` file/grep skim; all boxes open |
| | Phase 0 | |
| | Phase 1 | |
| | Phase 2 | |
| | Phase 3 | |
| | Phase 4 | |
| | Phase 5 | |
| | Phase 6 | |
| | Phase 7 | |
| | Phase 8 | |
| | Phase 9 | |
| | Phase 10 | |
| | Phase 11 | |
