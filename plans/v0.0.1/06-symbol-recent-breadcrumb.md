# Plan: Workspace Symbol Search, Recent Files, Clickable Breadcrumbs

## 1. Current behavior + files

### Ctrl+P quick open (filename only + absolute path)
- `internal/app/palette.go:37-224` — `paletteRows()`:
  - Absolute query → single row via `absolutePaletteRow()` (`palette_absolute.go:12-34`), incl. external files outside root (registered in `ByPath` only, never `Files`/tree — `workspace.go:805-833`).
  - Empty query → open tabs (`a.tabs`, `a.tabs2`) + `recentRows()` + first `ws.Files`, capped at `paletteDefaultRows=5`.
  - Non-empty query → in-memory candidates (tabs → recents → `ws.Files`) scored as prefix-then-contains on lowercased `Path`/`Name`; then disk fallback `diskSearchFiles(ctx, ws.Root)` (`palette_search.go:17-82`, rg → `git ls-files` → bounded `WalkDir`, 750 ms timeout, `MaxWorkspaceFiles` cap) fills remainder up to `paletteMaxRows=8`.
  - Row shape `paletteRow{Path,Name,Dir,Lang,Badge}` (`palette.go:24-32`); view marks `Active` (`paletteView()`); Enter opens via `onSubmit()` (`actions.go:2142-2222` → `openFile(path)`).
- Shortcuts/wiring: `Ctrl+P` → `openPalette()` (`actions.go:1786-1788`); palette keys Up/Down/Esc/Enter (`actions.go:1870-1887`); `palette-open`/`palette-close`/`open:<path>` actions; template `components/html/palette.html:40-61` (input `#palette` bound to `PaletteQuery`, rows `data-action="open:..."`); titlebar cmdbar button `components/html/titlebar.html:17` ("Quick open (Ctrl+P)").
- Matching is substring only — no fuzzy, no symbol/content awareness, no `foo:12` line jumping.
- Tests: `palette_test.go` (in-mem-first, disk fallback, git fallback, 8-row cap, long-dir truncation), `palette_absolute_test.go`, `input_test.go:163` (Ctrl+P opens).

### Outline (per-file only)
- `internal/app/outline.go:17-38` — `outline(lang, lines) []symbolRow`, capped `maxOutlineRows=60`, languages: Go (`func/type/const/var`), Python (`def/async def/class` + indent depth), JS (`function/class/const-let-var =>` arrow), Markdown (ATX headings, fence-aware), CSS (selectors/at-rules). No TS/TSX-specific, JSON/HTML/YAML/Shell/plain return nothing.
- `symbolRow{Name,Kind,Line,No,Depth}` (`view.go:547-553`); built in `workspace.go:1336` (`openDoc`) and stored on `fileDoc.Outline`; cached per open doc only.
- UI: details panel `components/html/details.html:24-32` (`data-action="outline:<line>"` → `actions.go:682-685` → `outlineLine(line)` scrolls). No palette integration, no workspace aggregation.
- `syntax.go` tokenizers (`scanLineC` for Go/Python/JS with `classFn/classType/classVar` states, `languageOf()` at `syntax.go:68-103`) exist but are never used for symbol extraction.

### Nav history (back/forward, not MRU)
- `internal/app/nav_history.go` — `nav []string` + `navAt`, cap 100, browser-style truncate-forward on new push, `goNavBack/goNavForward` (Alt+Left/Right, `navBackTitle/navForwardTitle`), `pruneNavHistory`, persisted in session (`session.go:52-53` `Nav/NavAt`, `app_session.go:132-141,214-215`).
- Records file-level opens only (`openFileAt(..., record)` in `app.go:2497`); no cursor positions, no MRU ordering, no picker UI.

### Recent files (welcome-only, session-persisted, tiny)
- `app.go:99` `maxRecent=4`; `a.recent []string`; `recentRows()` (`app.go:2444-2467`) filters to `ws.ByPath != nil`; `addRecent()` MRU-front (`app.go:2469-2482`); called from `openFileAt` path / session restore.
- Surfaced only on welcome panel (`view.go:92` `Recent []recentRow`, `app.go:918`) and as tiebreak inside empty-query palette. No `Ctrl+E`/`Ctrl+Tab` switcher, no filter box, no dedicated overlay.
- Persistence: `sessionState.Recents` capped `sessionMaxPaths=32` (`session.go:15,100-101,116-117`), sanitized to workspace-relative (`cleanSessionPaths`), restored in `app_session.go:50`, saved in `sessionState()` (`app_session.go:197`). `project_recent.go` is recent *projects* (max 10, `recent-projects.json`), unrelated to recent *files* — naming collision risk.

### Workspace text search (exists, not symbol search)
- `workspace_search.go` — `searchWorkspace(ctx, ws, query, caseSensitive, limit)` full-text scan of `ws.Files` (skips `Big`/binary, 8 hits/file, 200 total), `Ctrl+Shift+F` (`actions.go:1790-1792`), template `palette.html:1-34`. Usable as fallback content path but reads every file per keystroke — not a symbol index.

### Breadcrumb / crumb bar (static text, not clickable segments)
- `components/html/editor.html:53-122` `#crumbs`: three modes (merge / changes-diff / file). File mode (`editor.html:90-117`) renders `ActiveDirShort` + `ActiveNameShort` as one `<span id="crumb-path">` (clipped by `clipCrumbPath` in `diff.go:152-237`), plus Wrap/Copy-path/Copy-file buttons. No per-segment buttons, no `data-action` on path parts, no sibling dropdown.
- `file_crumb_clip_test.go` asserts single 20 px line; `file_nav_ops.go:146-193` `fileNavViewState` drives bottom-center changed-file widget (often confused with "crumbs" in tests) — separate concern.
- Backend helpers `baseName/dirName` (`workspace.go:1341-1357`), `ActiveDirShort/ActiveNameShort` in view; no `crumbSegments`/`siblings(dir)` view model exists.

## 2. Gaps

| Area | Missing vs request |
|---|---|
| Workspace symbols | No cross-file symbol index; outline is per-open-file, 5 languages, 60-row cap, regex-line-scan (misses methods with receivers nuances, TS interfaces/enums, nested classes, YAML keys, HTML ids); palette knows files only. |
| Recent files | No MRU switcher UI; `maxRecent=4` too small for switcher; `Ctrl+E` bound to `toggleEdit` (`actions.go:1804-1808`) — conflict; no `Ctrl+Tab` handling at all; no filter-as-you-type, no preview, no pinning. |
| Breadcrumb | Path is one clipped span; segments not individually focusable/clickable; no dropdown listing sibling files/folders; no keyboard nav; no action to reveal parent dir in explorer. |

## 3. Design

### 3a. Workspace symbols — `Ctrl+T` (all) / `Ctrl+Shift+O` (file)

**Reuse, don't rebuild.** Two-tier source:
1. `outline(lang, lines)` — already correct-enough per language; extend, don't replace.
2. `syntax.go` token classes (`classFn/classType/classVar/classDec`) — use as *supplement* for languages outline skips (TS `interface/enum`, HTML tags, JSON keys) only where cheap.

**New type** (suggest `internal/app/symbols.go`):
```go
type workspaceSymbol struct {
    Name, Kind, Path string
    Line int // 0-based; No = Line+1 for display
}
type symbolIndex struct {
    built time.Time
    byPath map[string][]workspaceSymbol // outline cache per file
    flat   []workspaceSymbol            // sorted by Name for palette
}
```

**Build strategy (lazy + incremental, bounded):**
- On `Ctrl+T` first use (or idle 500 ms after workspace load): build index over `ws.Files` for outline-supported langs only (Go/Python/JS/TS/TSX/Markdown/CSS). Read via `ws.readFile` with existing guards (`Big`, binary, `maxFileBytes`); reuse `outline()` on split lines. Skip unsupported langs silently (counts toward coverage metric, not error).
- Cache per-path + mtime/size key; invalidate on save (`saveDirtyActive` path) and watcher events (`watch.go`); `openDoc` already computes `Outline` — write-through into `byPath`.
- Cap: index at most `MaxWorkspaceFiles` (10k) files × 60 symbols = 600k rows worst case — too big. Enforce `maxWorkspaceSymbols = 20_000` with per-file 60 cap; when exceeded, prefer open tabs → recents → shortest paths, mark `Truncated=true` and show "Showing first N symbols" note (mirror `workspaceSearchNote` pattern).
- Never block UI: build in goroutine with `context.WithTimeout(1s)`; palette shows file results immediately, symbol section fills on `refresh+Redraw` when ready (same pattern as `paletteDiskSearchTimeout`).

**Palette integration:**
- New mode: `a.paletteMode ∈ {"files","symbols","recent"}` + `a.symbolQuery`. `Ctrl+T` → `openPalette` in `symbols` mode (placeholder "Search symbols across workspace"); `Ctrl+Shift+O` → `symbols` mode pre-filtered to active file (`Path == activeDocPath()`); `Ctrl+P` keeps `files` mode unchanged.
- Row: `symbolRow{Path, Name, Kind, Line}` rendered as `Name  Kind · Path:Line`; Enter → `openFile(path)` then `outlineLine(line)` (reuse existing `outline:` action path). Scoring: prefix on `Name` first, then contains on `Name`, then contains on `Path`; cap 8 rows total, files+symbols interleaved or sectioned (recommend two sections: top 4 symbols + remainder files, or single merged list with kind badge — pick one, document in code).
- Query syntax: `foo` matches name; `foo:12` jumps to line (parse suffix, reuse `cutName`); `@foo` forces current-file scope (VS Code compat, cheap to add).

**Outline extensions needed** (small, in `outline.go`):
- TS/TSX: `interface X`, `enum X`, `type X =` (currently JS branch misses `interface/enum`; `.ts` maps to `langJS` in `languageOf`).
- Python: `async def` already handled; add decorator-carry (optional v2).
- Leave JSON/YAML/HTML/shell for syntax-token v2; note as coverage gap.

### 3b. Recent files MRU — `Ctrl+E` + `Ctrl+Tab` with session persist + filter

**Conflict:** `Ctrl+E` = `toggleEdit` today. Decide: move toggle-edit to `Ctrl+Shift+E` (or keep `Ctrl+E` when editing-focused?) — call out in changelog; `Ctrl+Tab`/`Ctrl+Shift+Tab` currently unbound (verify in `actions.go` key switch; no match found) so safe to claim.

**State:**
- Bump MRU memory: keep `a.recent` (persisted, cap 32 via session) but raise in-memory MRU window to e.g. `mruMax=20` for switcher display while persisting all 32 (session already supports 32). No schema change: `sessionState.Recents` reused; `recentRows()` gains `limit` param or new `mruRows(n)` helper.
- `addRecent()` already MRU-front on open — keep; ensure `openFileAt` calls it for palette/symbol/crumb opens too (audit: session restore calls it; palette `onSubmit→openFile` flows through `openFileAt` — confirm push happens there, else add).

**UI (reuse palette shell):**
- `Ctrl+E` or hold-`Ctrl+Tab` → `openPalette` in `recent` mode: list = MRU order (`recent` slice filtered to `ByPath != nil`), each row shows `Name  Dir  Badge` + index hint; typing filters (same prefix/contains scorer as files); Enter opens; Esc closes.
- `Ctrl+Tab` tap (no typing): quick-toggle between the two most recent (VS Code behavior); hold + repeated `Tab` cycles `paletteIndex` (requires key-up tracking — if framework lacks key-up, fall back to repeated `Ctrl+Tab` presses cycling, `Ctrl+Shift+Tab` reverse; simpler and testable).
- Filter input is the existing `#palette` box with mode-specific placeholder; footer shows `N recent` + `↑↓ choose · Enter open · Esc close`.

**Persistence:** no new file; session `Recents` already round-trips. Add test that MRU order survives `sessionState()` → `restoreState()`.

### 3c. Breadcrumb segments clickable + sibling dropdown

**View model** (extend `view.go`, compute in `app.go` refresh):
```go
type crumbSegment struct {
    Name, Path string // Path = dir path for folders, file path for leaf
    IsDir bool
    Index int
}
View.CrumbSegments []crumbSegment // e.g. ["pkg","feature","lazy_controller.go"]
View.CrumbSiblings [][]siblingRow // per-segment dropdown, lazy-filled on open
type siblingRow struct { Name, Path string; IsDir bool }
```

**Render** (`editor.html` file-mode branch): replace single `#crumb-path` span with per-segment `<button class="crumb-seg" data-action="crumb:<path>">` joined by `/` separators; keep `id="crumb-path"` wrapper for existing clip test; keep `title="{{.Active}}"` on wrapper.
- Click (or `Enter`/keyboard) on a folder segment → reveal/select that dir in explorer + set as filter root (reuse `tree` selection; do not navigate away from file).
- Click on leaf → no-op or copy-path (keep current copy buttons canonical).
- Dropdown: chevron/next-to-segment button `data-action="crumb-menu:<index>"` toggles sibling list (children of parent dir via `findTreeNode` / `fileNode.Children`, dirs-first via `sortTree` order); selecting a sibling file opens it, selecting a folder reveals it. Only one menu open at a time (`a.crumbMenu int, a.crumbMenuOpen bool`); Esc/blur closes (mirror `fileMenu` pattern).
- Clipping: keep `clipCrumbPath` budget logic; segments beyond budget collapse into leading `…` button that opens root-level dropdown (preserves `file_crumb_clip_test.go` single-line invariant — update test to assert segments still one line).

**Actions:** `crumb:<path>`, `crumb-menu:<index>`, `crumb-open:<path>` in `actions.go:onClick` switch next to `outline:`; all end with `refresh()`.

## 4. Checklist

- [ ] Symbols: new `internal/app/symbols.go` — `workspaceSymbol`, `symbolIndex`, `buildSymbolIndex(ws, opts)`, `symbolsFor(path)` write-through from `openDoc`, `matchSymbols(query, scope)` scorer.
- [ ] Symbols: extend `outline.go` JS branch for `interface/enum/type X =` (TS coverage); add tests in `outline`-style (check existing outline tests location).
- [ ] Symbols: palette mode plumbing — `paletteMode`, `symbolQuery`, `openSymbolPalette(ctx, scope)`; `Ctrl+T` + `Ctrl+Shift+O` bindings in `actions.go` key switch; placeholders/footers in `palette.html`.
- [ ] Symbols: `onSubmit` branch — open file + `outlineLine(line)`; `@` scope + `:line` suffix parsing.
- [ ] Symbols: truncation note + lazy background build with timeout; invalidate on save/watch.
- [ ] Recent: `mruRows(n)` helper; `Ctrl+E` (resolve `toggleEdit` conflict) + `Ctrl+Tab`/`Ctrl+Shift+Tab` bindings; tap-toggles-last-two; filter-as-type in recent mode.
- [ ] Recent: verify `addRecent` on all open paths (palette, symbol jump, crumb sibling, nav back/forward); session round-trip test.
- [ ] Crumbs: `CrumbSegments` view model + compute func (`crumbSegments(activePath)`); template segment buttons + separators; `crumb:`/`crumb-menu:`/`crumb-open:` actions; sibling listing via tree nodes; single-line clip preserved.
- [ ] Crumbs: keyboard access (Tab-focus segments, Enter opens, Esc closes menu); `aria-label`s mirroring `outline:` rows pattern.
- [ ] Docs: update shortcut hints (titlebar `Ctrl+P` title stays; add `Ctrl+T`/`Ctrl+E` titles where buttons exist); changelog entry for `Ctrl+E` rebind.

## 5. Acceptance + tests

**Acceptance:**
1. `Ctrl+T`, type `Serve` → symbols from unopened files appear (name + `file:line`), Enter lands on the definition line.
2. `Ctrl+Shift+O` in a Go file, type `han` → only current-file `func Handle…` rows.
3. `Ctrl+Tab` toggles last two files with no typing; `Ctrl+E`, type `cont` filters MRU; order persists across restart.
4. Clicking breadcrumb `pkg` selects `pkg/` in explorer without closing the file; sibling dropdown on `feature` lists peer files/dirs and opens on pick; bar stays one line on narrow panes.

**Tests (Go, mirror existing style):**
- `symbols_test.go`:
  - `TestWorkspaceSymbolsFindUnopenedFile` — write 3 files, build index without opening, query matches symbol in unopened file with right path+line.
  - `TestSymbolSearchScopesToCurrentFile` — `Ctrl+Shift+O` scope returns only active-path rows.
  - `TestSymbolIndexRespectsCap` — fake 21k symbols → `Truncated=true`, ≤ cap rows, note non-empty.
  - `TestSymbolEnterOpensAtLine` — `onSubmit` in symbol mode opens doc and scrolls to `outlineLine`.
  - `TestOutlineTSExtensions` — `interface Foo`, `enum Bar`, `type Baz =` detected.
- `recent` (extend `session_test.go` / new `mru_test.go`):
  - `TestMRUToggleLastTwo`, `TestMRUFilter`, `TestMRUSessionRoundTrip` (order preserved through `sessionState→sanitize→restoreState`).
- `crumbs` (extend `file_crumb_clip_test.go`):
  - `TestCrumbSegmentsSplitPath`, `TestCrumbSiblingDropdownListsPeers`, `TestCrumbClickKeepsSingleLine` (existing clip test updated, not deleted).
- Manual: 10k-file repo timing (index build < 1 s P50, palette keystroke < 50 ms with index warm); screen-reader labels on segments (`aria-label="Go to folder pkg"`).

## 6. Risks

- **Index cost on 10k files:** full `readFile`+split per file on first `Ctrl+T` can exceed 1 s and blow memory (600k rows uncapped). Mitigations: outline-supported-langs filter, `Big`/binary skip, 20k-symbol cap with recency-biased ordering, 1 s ctx timeout + background fill, per-path cache with mtime invalidation, never index on startup (only on first symbol query or idle). Measure with `ScanMillis`-style metric; log build ms in test.
- **Syntax language coverage:** outline covers 5/10 langs; TS `interface/enum`, Python decorators/nested defs, HTML/YAML/JSON/shell symbols will be missing or noisy if token-class fallback is naïve (e.g. every `classVar` becomes a symbol). Mitigation: ship v1 as outline-only + TS extension; gate token-supplement behind explicit allowlist per lang; surface "N files without symbol support" count in footer rather than silently empty.
- **Keybinding collisions:** `Ctrl+E` (toggleEdit), `Ctrl+T`/`Ctrl+Tab` may be captured by terminal/browser host or OS (new-tab). Check `text_edit.go` overlay guards and terminal focus path before binding; provide fallback `Ctrl+Shift+O` (safe) and document remap. `Ctrl+E` rebind needs user-visible notice.
- **`Ctrl+Tab` hold-vs-press:** framework appears keydown-only (`onKeyDown` switch, no key-up tracking seen); hold-to-preview MRU like VS Code may be infeasible — fallback to press-to-cycle is acceptable but must be specced upfront.
- **Session schema:** reusing `Recents` avoids migration, but raising displayed MRU to 20 while `maxRecent=4` drives welcome panel needs care — parameterize limit instead of changing the const globally.
- **Crumb template churn:** `editor.html` crumb bar has 3 modes × narrow/wide variants plus `clipCrumbPath` pixel budgets; segment buttons change measured widths → clip test and zoom tests (`zoom_test.go` asserts `.crumbs`/`.crumb-btn` selectors) may need budget re-tuning; keep wrapper `#crumb-path` id and single-line assert.
- **Absolute/external files:** symbols for external (absolute-path) files have no workspace-relative dir — exclude from index, show file-scope only; crumbs for external files show full-path segments with no explorer reveal (disable folder-segment action, keep title).
