# Settings — Font Size / Zoom / Theme / Keymap / Exclude Patterns

## 1. Current behavior + files

### 1.1 Themes — six, session-persisted, single-section Settings
- `themes.go:15-24` `themeCatalog()`: `midnight` (default), `dracula`, `light`, `solarized`, `nord`, `contrast`.
- `themes.go:31-46` `validTheme` (unknown → midnight), `themeRows()`, `themeCSS(id)` + `termPaletteFor`/`termCSS` per theme.
- `app.go:296-297,860-866` `App{settingsOpen, theme}`, `View{SettingsOpen, Themes}`.
- `components/html/settings.html:1-20` — only `Appearance` → `Color theme` list (`settings-theme` buttons `data-action="theme:<ID>"`).
- `actions.go:394-400` `theme:<name>` applies + `refresh()` (relies on later persist).
- Tests: `themes_test.go` (`TestSettingsThemesAndSelection`, `TestThemeSessionRoundTrip`, `TestThemeSelectionPaint`, `TestTermPaletteCoversCatalog`).

### 1.2 Persisted sessions — per-workspace JSON
- `session.go` `sessionState` (`Root/Mode/Theme/Recents/Tabs/Active/Tabs2/Active2/FocusGroup/Split/SplitRatio/Details/DevTools/Wrap/Preview/DiffSplit/HistoryRatio/Zoom/BranchLogs/Nav`); path `$XDG_CONFIG_HOME|~/.config/ownframe/code-view/session-<sha16(root)>.json`; atomic tmp+rename, `0700/0600`, cap 32, zero-on-missing, error-on-corrupt; `sanitize`, `clampSessionZoom` (wild → 1).
- `app_session.go` `EnableSession/restoreState/persistSession/sessionState` (dirty-check compare); `New*` sets `sessionReady=false` until enable.
- `session_test.go` round-trip/missing/corrupt/sanitize/per-root/mode.

### 1.3 Zoom — one global multiplier
- `zoom.go` `zoomMin=0.5/max=3.0/step=1.1`; `uiZoom/zx/zf/zoomLabel/zoomIn/Out/reset/setZoom` (rescale scroll, `persistSession`, `SetTheme(activeThemeCSS())`, `Redraw`); `activeThemeCSS=themeCSS+zoomOverrideCSS`; `zoomFontTable` (~100 selectors); `zoomOverrideCSS(1)==""` invariant.
- `zoom_sections.go` per-component rules incl. `.settings-panel/.settings-theme`.
- Bindings (`actions.go:1590-1599,784`): `Ctrl+=/-/0` (+KP), `Ctrl+wheel` (app-owned, `DisableKeyZoom/DisableWheelZoom=true`). Status `zoom-reset`.
- `zoom_test.go` scale/need-control/copy-unaffected/wheel/persist/override.

### 1.4 Scan — skips, budget, `.gitignore`
- `workspace.go:17-31` `MaxWorkspaceFiles=10000/maxTreeDepth=12/maxIgnoredDirs=200/maxIgnoreCandidates=5000/maxFileBytes=512KB/maxFileLines=20000`.
- `skipDir:1101-1111` (`.git/.hg/.svn/.cache/.idea/.next/.nuxt/.venv/__pycache__/build/coverage/dist/node_modules/target/temp/tmp/vendor` + dot-prefix except `.github`); `skipExt:1113-1125` (binary/archive/image/font/media); dotfiles except `.gitignore`/`.env.example`; symlinks skipped; depth cap.
- Budget `loadDiskWorkspaceWithLimit:228-339` stop-before-limit, `Truncated/TruncatedNote/ScanBudget`; `NewWithLimit` + `-files` flag.
- Default disk open is lazy (`loadDiskWorkspaceLazy`, `app.go:386`); full scan only when `limit>0 && !=Max`. `rescan.go:80-94` lazy passthrough; `watch.go:546`, `explorer_clipboard.go:526` reuse `scanLimit`.
- `.gitignore`: `loadIgnoredPaths` (`ls-files --others --ignored`), `loadIgnoredDirs` (`check-ignore`), `markIgnored/markTreeIgnored/addIgnoredDirs` (dimmed placeholders ≤200), `scanIgnoredDirs/walkIgnoredDir` descend on expand (never `.git`).
- `gitignore_rows_test.go` dimmed style + expand + rescan-keeps-open. `rescan.go:96-119` `isGitMetadataChange`.

### 1.5 Search — caps, no excludes
- `workspace_search.go` `limit=200/fileHits=8/textRunes=240`; skips `Big`/`>512KB`/`isBinary`; fold unless case; `Truncated/Note`; sorts path/line/col.
- `search_ops.go` window 8×44, async token/cancel/chan/`WantFrameIn(0)`, `toggleSearchCase`, `searchStep`, `openSearchHit`.
- `palette_search.go` `rg --files` → `git ls-files` → walk, capped at `MaxWorkspaceFiles`.

### 1.6 Keymap today — hardcoded `onKeyDown`
`actions.go:1572-2121` (+ explorer/terminal/menu): zoom `Ctrl+=/-/0`; `Ctrl+`` terminal; `Alt+Left/Right` nav; `F12`/`Ctrl+Shift+I` window-owned dock; `Ctrl+P/F/Shift+F/E/S/A/C/V/\`, `Ctrl+Z/Y` reserved-nil, `Ctrl+I` card; single-keys `1/2/3/arrows/pgup/pgdn/home/end/f/w/z/m/r/s/u/n/p/Esc`; terminal owns keys when focused; `settingsOpen` short-circuits.

## 2. Gaps

1. No font-size setting (zoom scales everything; no per-view base, no editor-only size).
2. Zoom global only (one float; no per-view overrides).
3. Settings UI Appearance-only (no Zoom/Font, Keyboard, Files/Search; no inputs/footer/search).
4. No keymap model (no list/rebind/conflicts/reset; shortcuts scattered with focus overrides).
5. No user excludes (hardcoded skips; cannot add `*.log`, `coverage/`; search scans `ws.Files` verbatim incl. ignored-once-expanded).
6. `.gitignore` vs custom excludes undefined (precedence, dimmed vs hidden vs searchable).
7. No global settings file (per-workspace sessions only; no roaming defaults).
8. No validation UX (bad globs, chord collisions, terminal-reserved keys).

## 3. Design

### 3.1 Schema + location

Global versioned file beside sessions: `defaultSettingsPath()` → `<UserConfigDir>/ownframe/code-view/settings.json` (`0700/0600`, atomic, reuse `session.go` helpers).

```jsonc
{
  "version": 1,
  "theme": "dracula",
  "fontSize": 12.0,          // 8..24, default 12
  "zoom": 1.0,               // 0.5..3.0 fallback
  "zoomPerView": { "editor": 1.1, "explorer": 1.0, "terminal": 1.0, "preview": 1.0 },
  "keymap": { "toggle-terminal": "Ctrl+`", "workspace-search": "Ctrl+Shift+F" },
  "excludes": { "scan": ["**/coverage/**"], "search": ["vendor/**"], "useGitignore": true, "searchIgnored": false }
}
```

Rules: unknown fields ignored; `sanitize()` clamps, drops invalid globs (cap ~100/list, 200 chars), validates chords; corrupt → zero + toast (like `loadSession`); precedence **session > settings > builtin**; writes debounced (`sessionLast` pattern); gestures write session + global (document wins).
New `internal/app/settings.go`: state, paths, load/save/sanitize, clamps, glob compile, chord parse. Prefer one glob lib (`doublestar` or extended `path.Match`) — note choice.

### 3.2 Font size / zoom per-view persisted

* Split `fontSize` (global base, default 12 feeding `zoomFontTable` body/`.cline`/term) + `zoom` (0.5–3.0) + `zoomPerView` (v1: `editor`/`terminal` only; explorer/search follow global).
* `App{fontSize, zoomPerView}` + `effectiveZoom(view)`; `zoomOverrideCSS` gains font base; keep `override(1,12)==""`; `zx/zf` stay global for scroll math (per-view CSS-only v1; document hit-test caveat).
* Status chip `110% · 12px` → opens Appearance; keep `zoom-reset`, add `font-reset`.
* Persist session `Zoom` + new `FontSize` (missing → global → 12/1); reuse `clampSessionZoom`/`clampFontSize(8..24)`.

### 3.3 Theme picker (keep + extend)

Keep six themes untouched. Picker stays `Settings → Appearance`: live swatch + `aria-pressed`; below: font stepper (8–24) + zoom stepper (50–300%) + collapsible per-view overrides. Click applies instantly + persists session + global; Esc closes. Extend `themes_test.go` pattern.

### 3.4 Keymap editor

* Inventory `keymapCatalog()` from `onKeyDown` (zoom, terminal, palette/find/search, save/edit/split/preview/paste/copy, modes, close, wrap, reload, hunks, filter, nav, devtools read-only).
* Storage `settings.keymap[action]=Chord` (diffs only; missing = default). Grammar `Ctrl/Alt/Shift/Meta + key` normalized; single-letter flagged terminal-risk.
* UI `Settings → Keyboard`: rows (label, chord kbd, Rebind → press-keys capture, Esc cancels), conflict banner (Swap/Replace/Cancel), per-row + `Reset all`.
* Conflicts: `resolveChord` → actions; block silent steal. Reserved always-conflict: `F12`/`Ctrl+Shift+I` (disallow), `Ctrl+C/V/X/Z/Y` in terminal/edit (warn or disallow; decide).
* Engine: `onKeyDown` consults `a.keymap(action)` for rebindable subset; precedence (terminal/overlays/editing) unchanged; non-rebindable marked Fixed.

### 3.5 Excludes with `.gitignore` interplay + UI

* `excludes.scan[]`: walk-time skips (never enter `Files/Tree` except explicit open). `**/*/?`, trailing `/` dir-only; v1 no `!` negation.
* `excludes.search[]`: search-time skips. Default inherits scan + gitignored (when `searchIgnored=false`).
* Interplay: `useGitignore=true` (default) keeps dimmed/expandable gitignored rows, searchable only if `searchIgnored=true`; `false` skips git calls (offline/fast). Custom scan excludes hidden entirely (distinct from dimmed); custom search excludes counted in `Note` ("3 files skipped"). Precedence: hard skips (`skipDir .git`, symlinks, depth, binary) > custom scan > gitignore (dim).
* Matching: compile once per settings/rescan; slash-relative + basename; `foo/` = `foo/**`; case-sensitive v1.
* UI `Settings → Files & Search`: scan editor (one glob/line, live count, Rescan button) + note about budget/`-files`; search editor + `useGitignore`/`searchIgnored` toggles + caps readout. Invalid glob → red row, not saved.
* Perf: prefilter in `WalkDir`; precompiled set before `readFile`; rescan on scan-change, next-search on search-change.

## 4. Checklist

- [x] `settings.go`: schema/paths/load/save/sanitize/clamps/glob/chord.
- [x] Wire into `New/NewWithLimit` + `EnableSession` (settings first, session fallback).
- [x] Font size: state, setters, `activeThemeCSS` param, chip, persist.
- [x] Per-view zoom: map, `effectiveZoom`, UI (editor/terminal v1), CSS scoping.
- [x] Theme picker refresh (swatches, font/zoom controls).
- [x] Keymap: catalog, `onKeyDown` indirection, capture UI, conflicts, reset.
- [x] Excludes: scan filter (`loadDiskWorkspaceWithLimit/Lazy/loadNode/walkIgnoredDir`), search filter + `Note`, editors + toggles + Rescan.
- [x] `settings.html` nav (Appearance/Keyboard/Files & Search) + CSS + zoom rules.
- [x] Migration: old sessions load as today; version forward-compat.
- [x] Docs: `-files` + Settings section; precedence comments.

## 5. Acceptance + tests

* Fresh profile → midnight/12px/100%, six themes; change survives restart (same ws) + roams as default.
* Per-view editor 110% + terminal 100% paint, hit-test intact, `override(1,12)==""`.
* Rebind works, old chord dead; collision blocked; Reset restores; `F12` not rebindable.
* `*.log` hides from tree + search after rescan; `dist/` dimmed/expandable with `useGitignore=true`, skipped in search unless `searchIgnored`; `false` skips git; invalid glob rejected.
* Tests: new `settings_test.go` (round-trip/clamps/corrupt/unknown); `themes_test.go` + font/zoom persist; `zoom_test.go` + font/per-view; new `keymap_test.go` (catalog/conflicts/reserved/reset/`onKeyDown` rebound); `workspace/rescan_test.go` + scan excludes; `workspace_search_test.go` + search excludes/`Note`; `gitignore_rows_test.go` green; manual F12/terminal/`Ctrl+=/P/F/`` regression.

## 6. Risks

* **Terminal `F12`/`Ctrl+Shift+I`**: window-owned; list read-only, never rebindable; warn on near-miss (`Ctrl+I`); terminal-focus precedence means rebound globals dead in shell — document; disallow `Ctrl+C/V/X/Z` rebind by default.
* **Single-key theft**: bare letters only when not-editing + terminal-blurred + no overlay; require `Ctrl/Alt` for new binds unless confirmed.
* **Migration**: keep `theme/zoom` fields; add `fontSize/zoomPerView` (keymap global-only); missing → global → default; no rewrite until next persist; `version:1` ignores unknown keys.
* **Geometry desync**: per-view CSS-only can misalign caret/terminal rows at extremes — v1 font+padding only, keep row heights global; paint assertions + `final_shot_test` before widening.
* **Exclude footguns**: overbroad patterns can empty tree — confirm when post-exclude drops >50%/to 0; always allow clear + rescan; document glob semantics; invalid never aborts scan.
* **Perf**: precompile, short-circuit on `Ignored`/size, keep `maxIgnoreCandidates`; search stays cancellable.
