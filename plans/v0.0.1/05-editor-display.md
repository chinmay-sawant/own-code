# Plan: Line numbers, word wrap per file, tab size + insert spaces, trailing-whitespace trim

## 1. Current behavior + files

### Line numbers (always on, no toggle)
| Area | File | Behavior |
|---|---|---|
| Rows | `components/html/editor.html:264-294,369-376`, `components/css/editor.css:415-433` | Every `crow` renders `<button class="ln">No</button>` (continuation `No==0` → `cont`). Width 44px + 10px pad. Second pane duplicates with `<span>`. |
| Geometry | `app.go:42-50` (`rowH=20/codeGutter=54/codeCellW=7.33/wrapMinCols=8`), `pane.go:120-151` (`codeColsFor` subtracts gutter+pad+slack) | Gutter baked into wrap budget. `zoom_sections.go:140` + `zoom.go:121-122` scale `.ln`/fonts. Hiding changes budget. |
| Marks | `gutter_markers.go`, `inline_ops.go:22-50`, `editor.html:266,288` | `gutterMark{+/-/~/class/hint}` 16px column left of `.ln`. `cloneCodeRows` (`pane.go:337`) avoids leak into `wrapPlan`. `codeRow` in `view.go:305-324`. |
| Preview | `editor.html:275-282`, `pane.go:13-33` | Markdown own gutter `.pln` (44px) + `.pmark` (18px). Separate surface. |
| Diff | `editor.html:164-208`, `diff.css` | `.dnum` (46/44px), never hidden. Same global `Wrap` for text only. |

No `showLineNumbers`/`relativeNumbers` in `App`/`View`/`sessionState`.

### Word wrap (single global `Z`, no per-file, no h-scroll when on)
| Area | File | Behavior |
|---|---|---|
| State | `app.go:324-326` (`wrap/scrollX/viewportCols`), `session.go:44` (`Wrap`), `app_session.go:117,207` | One bool, per-workspace JSON. Default false. |
| Toggle | `app.go:2043-2070` (`toggleWrap`), `actions.go:543,2079`, `editor.html:81,113` | Flips `wrap`; on-enable `scrollX=0`. Editor preserves top line via `topSourceLine→firstRow`; diff `resetScroll()`. `hscroll_test.go:229` asserts X-reset. |
| Engine | `wrap.go` (`wrapPlan{Cols,Rows,First}`, `wrapped(cols)` per `fileDoc`, `wrapLine` space-preferring + `cols/3,min 4` fallback, `firstRow/lineAt`) | Cached `fileDoc.Wrap` (`workspace.go:1287`); rebuilt on cols change. `No==0` continuation. |
| Unwrapped | `app.go:1085-1118` (`fillCode`), `pane.go:283-334`, `hscroll.go` | One row/line, `sliceUnwrappedCodeRows→sliceSegments + ScrollX=rem`; `hscrollWindow` px→(col,rem); `scrollHorizontal/setScrollX` no-op/zero when wrap; `maxLineWidth()` O(n) scan; `diffColumns()` separate budgets. |
| Diff wrap | `diff_wrap.go`, `app.go:1120-1173` | Same global flag; cache `diffWrapCache`; continuations `Cont=true`, blank numbers, `EditID+"-c"+i`, first chunk keeps arrow. Off = clipped copy. |
| Caret | `text_edit.go:438-454,482-543,638-657`, `edit_hit.go:47` | Branches on `wrap`. Shift+wheel/arrows drive h-scroll only when `!wrap` (`actions.go:832,2051-2058`). |
| Preview | `pane.go:142-151`, `app.go:1043-1057` | Preview bypasses `wrap` (own `previewColsForDoc`); `preview-toggle` orthogonal. |

### Caps
`workspace.go:17-31` 512KB/20000 lines/32KB line; `readLines:1129-1186` rejects `Big`/binary, truncates 20k (`Truncated`); `commits.go:18` patch 512KB; bench `perf_editor_bench_test.go:275`.

### Tab size + insert spaces (hardcoded 4, display-only label)
| Area | File | Behavior |
|---|---|---|
| Consts | `workspace.go:18-19` (`tabWidth=4/tabSpaces="    "`), `diff.go:689-710` (`expandTabs`: strip `\r`, `\t`→4 spaces; no tab-stop align) | Everywhere: `readLines:1174`, `commitEditBuffer`, diff/merge/git expand. |
| Typing | `text_edit.go:26-49` | `\t` → spaces before insert. No toggle, no width. |
| Columns | `selection_ops.go:150-173`, `text_edit.go:456-471,599-606` | `rawColumnAt/displayColumn` assume 4. |
| Detect | `workspace.go:1228-1253` (`detectIndent`: Tabs if leading `\t` else `Spaces: N` smallest run else None), `view.go:69`, `statusbar.html:22` | Shown only; never feeds editing/persist/config. |

No `.editorconfig`, no `tabSize`/`insertSpaces` in App/View/session, no picker, no override.

### Trailing whitespace (no trim)
`edit_buffer.go` preserves verbatim (CRLF→LF + newline sentinel); `edit_ops.go:150-202` strips `\r` only; `workspace_write.go:68-108` writes as-is. No flag/hook/setting (`settings.html` theme-only).

### Zoom/templates coupling
`zoom.go:13-22` 0.5–3.0 step 1.1: `zx/zf` scale geometry + `codeCellW`; `zoomOverrideCSS` + `zoom_sections.go` per-component (gutter/`.cline`/diff/preview). Gutter/column changes must stay in sync or hit-test desyncs. Templates: `editor.html` (track/crow/clinetext/edit-surface left:60px ≈54+16), `editor.css` (`.crow{--row-h}`, `.cline{nowrap}`, `.clinetext{absolute,pre}`), `settings.html` (theme-only → host Editor section), `statusbar.html` (indent/encoding/zoom chips → host tab/wrap indicators).

## 2. Gaps

1. Line numbers: no hide; no relative/highlight; fixed 44px breaks ≥10000 lines / wastes when hidden; preview/diff not unified.
2. Wrap: global only (Markdown wants on, Go/logs off); toggle resets scroll + wipes X; no per-file memory; split panes share flag though widths differ; `commitEditBuffer` clears `doc.Wrap` wholesale.
3. H-scroll: no scrollbar UI; `maxLineWidth()` full scan per `setScrollX`; no per-pane X (group 1 reuses 0).
4. Tabs: hardcoded 4; naive `detectIndent`; typing/paste/columns disagree if changed; no UI.
5. Trim: no on-save/on-type/diff-safe; naive trim dirties all + pollutes diffs; no preview.
6. Session: only global `Wrap` persisted; no `WrapByFile/TabSize/InsertSpaces/Trim/ShowLineNumbers`. Settings no Editor section; `Indent` read-only.

## 3. Design

### 3.1 Gutter toggle + relative

```go
ShowLineNumbers bool `json:"showLineNumbers,omitempty"` // default true via accessor
RelativeNumbers bool `json:"relativeNumbers,omitempty"` // only when shown
```

`App` mirrors + `uiShowLineNumbers()` default-true; `View`/`secondaryView` + `codeRow.RelNo` (or compute in `fillCode`/`secondView` from caret). Rendering: `editor.html` both panes `{{if .ShowLineNumbers}}<button class="ln">{{else}}<span class="ln hidden">`; v1 keep 16px mark column, hide number text (`.ln.hidden{visibility:hidden}`) to avoid geometry fork; v2 collapses width + `--gutter-w` var + `edit-surface` left. Relative: caret-line first-row absolute, others `|No-caret|`, continuations blank; shared `decorateLineNumbers(rows, caretLine)` for wrapped+unwrapped, both groups. Preview `.pln`/diff `.dnum` out of scope (document). Actions `line-numbers-toggle`/`relative-numbers-toggle` (disabled unless shown); no default key; palette entries; `persistSession` + `sanitize`. CSS + `zoomEditorCSS` entry; `edit-surface`/`edit-line-hit` left becomes var in v2.

### 3.2 Per-file wrap override in session

```go
Wrap bool `json:"wrap"`
WrapByFile map[string]bool `json:"wrapByFile,omitempty"` // presence = override
```

`App.wrapByFile + presence set`; `wrapFor(path)` = override else global; `wrapForGroup(g)`. Cap 32 + `cleanSessionPaths`; evict oldest/non-open; drop closed/deleted on persist. Toggle `wrap-toggle-file` 3-state (On → Off → FollowGlobal) with toast/label; crumb shows `Wrap*`/`Wrap (this file)`; per-file `scrollX=0` only when enabling on that file. Plumbing: replace `a.wrap` reads on editor paths (`fillCode/secondView/paneRowCount/hscroll×4/text_edit×5/inline×5/actions wheel+keys/sliceUnwrapped/fillDiff key`) — diff stays global (multi-file rows; document split). Cache `fileDoc.Wrap` already per-file; override flip triggers `refresh` (cols recompute). Scroll preserves top line (`firstRow` snap).

### 3.3 tabSize / insertSpaces, EditorConfig-aware

```go
TabSize int `json:"tabSize,omitempty"` // 0=auto; sanitize {2,4,8} else 4; default 4
InsertSpaces bool `json:"insertSpaces,omitempty"` // default true via accessor
TabSizeByFile / IndentByFile map[string]indentOverride `json:"indentByFile,omitempty"`
```

`View.TabLabel` (e.g. `Spaces: 4`/`Tabs: 4`); status chip becomes picker button (`sb-indent`). Resolution per file: explicit override → `.editorconfig` (`indent_style/size/tab_width`, glob-scoped, root-aware, dir cache) → improved `detectIndent` (tab-vs-space score, delta mode, mixed flag) → global 4/spaces. Engine: `expandTabs(text,size)` (all 7+ sites: load/commit/diff/merge/git/display/columns); `rawColumnAt(line,col,size)`; `onType` Tab: spaces→N else `\t` (display still expands); `reexpandDoc` + retokenize on change. EditorConfig read-only v1 (honor `trim_trailing_whitespace` as trim default when unset); depth-capped, mtime-cached, sample = none. UI settings Editor section: size stepper (2/4/8+Auto), spaces checkbox, use-editorconfig (default on), per-file pin. Status shows source (`Spaces: 2 · ec`).

### 3.4 Trim on save, diff-safe option

```go
TrimMode string `json:"trimMode,omitempty"` // off|all|touched; default off
```

`off` byte-identical; `all` strips `[ \t]+$` every line at `saveEdit` (preserve newline sentinel; empty→empty); `touched` only buffer-changed lines (line-diff `Original` vs current; reuse inline/gutter change map = saved-vs-current set when unsaved). `trimLines` + `touchedLines` pure in `internal/edit`; call after `commitEditBuffer`, before `writeFile`; caret col-clamp; `MarkSaved`, reopen, toast `Saved · trimmed N` when N>0; undo snapshot before trim so Ctrl+Z restores. Exclude merge-save/diff-revert v1. Settings select + caption; EditorConfig bridge when unset.

## 4. Checklist

- [ ] Gutter: fields + default-true accessor + `decorateLineNumbers` (both panes, wrapped+unwrapped) + template/CSS/zoom + `codeColsFor` note + settings/palette actions + persist/sanitize/restore; preview/diff documented unaffected.
- [ ] Wrap: `WrapByFile` + `wrapFor/wrapForGroup` + replace editor-path reads (diff stays global) + 3-state toggle + indicator + persist/sanitize/restore + per-file scroll preserve.
- [ ] Tabs: `expandTabs(text,size)` + all sites + `rawColumnAt(size)` + `onType` path + `resolveIndent` (override→editorconfig→heuristic→default) + editorconfig parser/cache + improved `detectIndent` + `reexpandDoc` + picker + settings section.
- [ ] Trim: `trimLines`/`touchedLines` + `saveEdit` hook (off/all/touched) + undo + toast + editorconfig bridge; exclude merge/diff-revert.
- [ ] Cross: session round-trip tests (defaults/corrupt/cap), zoom parity, docs/labels.

## 5. Acceptance + tests

* `TestLineNumbersToggle`: hidden → no `ln-*` text, marks stay; shown → back; both panes. `TestRelativeNumbers` (wrapped+unwrapped; ignored when hidden). `TestGutterZoomParity` (override contains `.ln.hidden`; budget on/off).
* `TestWrapPerFileOverride` (A wraps, B unwrapped; global flip leaves A); `TestWrapOverridePersist` (round-trip, sanitizes bad keys, caps 32); `TestWrapToggleKeepsLine`; existing `nowrap_slice/hscroll:229/diff_wrap/preview_spacing:157` green.
* `TestExpandTabsSizes` + `rawColumnAt` round-trip; `TestEditorConfigResolve` (root+scoped, `tab_width` vs `indent_size=tab`, missing → default); `TestIndentPersist` (pin+global; Tab inserts per mode).
* `TestTrimAll` (strips, keeps newline, empty-line, toast); `TestTrimTouchedDiffSafe` (untouched preserved, minimal diff; binary/oversized/truncated skipped); `TestTrimUndo`.
* Regression `go test ./internal/app/...` + `go vet`; bench 20k wrapped/unwrapped/zoom-3.0 before/after.

## 6. Risks

* **Perf 20k/512KB**: wrap O(runes) + allocs per row; per-file plans × split panes. Keep cache, invalidate active-only, cache `maxRunes` (invalidate on commit) or clamp; visible-rows-only slicing. Bench before merge.
* **Diff**: shares flag/cache/action id; override must not leak into `currentDiffRows/diffColumns/wrapDiffRows` (padding/arrow/`EditID+"-c"` corruption). Keep diff global; separate `diff-wrap-toggle` if relabeling; run wrap/expand/revert suites.
* **Preview**: own budget + gutter; hide/wrap-override must not touch preview layout/alignment — gate editor-only.
* **Caret drift**: missed `a.wrap` site → misplaced caret/vertical/click. `grep "a\.wrap"` audit; split A-wrapped/B-unwrapped click test.
* **Tab blast radius**: 7+ `expandTabs` sites; one miss = width-4 ghost path (columns disagree). Changing global must re-expand open docs. EditorConfig glob/cache/`unset` mis-attribute risk.
* **Trim perception**: `all` → noisy diffs, lying gutter marks, broken patches. Default `off` (or `touched`); toast count; undo; skip binary/oversized/sample; never merge-revert.
* **Session compat**: new maps bloat JSON; old ignore unknown (fine); missing `ShowLineNumbers` needs default-true accessor; cap 32, prune closed, sanitize keys.
