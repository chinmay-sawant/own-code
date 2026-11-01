# Replace Plan: In-File + Across Workspace

## 1. Current behavior + files

### In-file find (`Ctrl+F`)

| Area | File:lines | Behavior |
|---|---|---|
| State | `internal/app/app.go:328-333` | `findOpen, findQuery, findCase, findIndex, findHits []findHit, findReturn string` |
| View model | `internal/app/view.go:79-83`, `internal/app/app.go:897-899` | `FindQuery/FindOpen/FindCase/FindCurrent/FindTotal` |
| Index | `internal/app/find.go:26-62` (`reindexFind`) | Literal substring scan over `activeDoc().Lines`, rune coords. Empty → no hits. Case-insensitive via `ToLower`. Non-overlapping. No regex/whole-word/caps. Skips `nil`, `Err`, `Binary` docs |
| Open/close | `internal/app/find.go:64-116`, `internal/app/actions.go:1794-1795,1889-1900,2186-2195` | `Ctrl+F → openFind(ctx)` only in `mode=="explorer"`. Saves `findReturn`, sets `findOpen=true`, `Redraw`, `Focus(ctx,"find")`, `SelectAll`. `Esc`/`find-close → closeFind` clears + restores focus. `Enter` → `findStep(1)`; `Shift+Enter → findStep(-1)` |
| Step | `internal/app/find.go:119-130` | Modulo wrap, `scrollToSourceLine`, `refresh` |
| Case toggle | `internal/app/find.go:133-137`, `actions.go:554-557` | Flip `findCase`, `reindexFind`, refocus |
| Highlight | `internal/app/find.go:139-190` | Copies visible rows, `markRange(..., "find-hit[ find-cur]")` post-tokenize; wrapped rows clipped |
| Live update | `internal/app/actions.go:704-712` | Per-keystroke `reindexFind → refresh` |
| Edit interaction | `internal/app/edit_ops.go:52`, `internal/app/watch.go:665` | `toggleEdit()` closes find; watcher reindexes on external change |

### Workspace search (`Ctrl+Shift+F`)

| Area | File:lines | Behavior |
|---|---|---|
| State | `internal/app/app.go:183-198` | `searchOpen/Query/Case/Hits/Index/Scroll/Note/Truncated/Token/Busy/Result chan/Cancel` |
| View | `internal/app/view.go:118-119,235-263`, `search_ops.go:20-59` | `searchView` + `searchRow{Path,Line,Col,Before,Match,After}`; window 8 rows × 44px |
| Open/close | `search_ops.go:71-120`, `actions.go:162-163,178-191,1837-1860` | `openWorkspaceSearch` saves return focus, `Focus("workspace-search")`; close cancels + clears |
| Async run | `search_ops.go:125-165,168-212` | Token++, `Busy=true, Note="Searching…"`, goroutine `searchWorkspace(ctx,ws,query,case,200)` → chan, `WantFrameIn(0)` |
| Core scan | `internal/app/workspace_search.go:47-140` | Iterates `ws.Files`; skips `Big`/`>512KB`/`isBinary`; naive rune fold; caps 200 total / 8 per file; sorts Path/Line/Col |
| Navigate/open | `search_ops.go:221-259` | `searchStep` wrap + clamp; `openSearchHit` closes panel + `openFile`+scroll |
| Tests | `workspace_search_test.go`, `search_scroll_test.go`, `search_caret_test.go` | Multi-file, case fold, 240-rune preview, limits, binary/big skip, cancel |

Caps today: 200 total + 8/file, 240-rune preview. Source is `ws.Files` (respects `skipDir/skipExt`, 512KB, NUL check). `searchWorkspace` does not filter gitignored `Ignored` — must decide. Lazy roots miss unloaded dirs. Sample workspace read-only.

### Selection / edit seams

* `selection_ops.go`: press/drag → offsets, `rawColumnAt` tab mapping.
* `text_edit.go:326-413`: `insertEditText → replaceEditRange`: `pushUndo` (cap 100), rune splice, `SetText`, `markDirty`, `commitEditBuffer`, `ensureEditCaretVisible`. Replace-all must group, not push N times.
* `edit_ops.go:122-202`: `applyEditText/commitEditBuffer/saveEdit/writeFile`; `saveDirtyActive` for folded dirty.
* `palette_search.go:26-82`: disk enumeration (`rg --files` → `git ls-files` → walk) — exclude-aware pattern to copy for workspace replace.
* `workspace_write.go:68-108`: `writeFile` (refuses sample, escape check); no multi-file atomic helper.
* Confirm modal: `view.go:564+ confirmView`, `actions.go:22-30` — reuse for workspace replace confirm.

## 2. Gaps

1. No replace state, no `Ctrl+H` binding.
2. Literal-only matching (both surfaces lack regex/whole-word/preserve-case).
3. Only one toggle (`findCase`/`searchCase`); two duplicated matchers to unify.
4. 200/8 caps block replace-all correctness — need full-count pass + "preview vs will-replace" semantics.
5. Lazy-tree blind spot: `searchWorkspace` scans `ws.Files`; misses unloaded subtrees.
6. No preview/diff, no per-file checkbox, no staged confirm.
7. No undo grouping (N replacements = N undo steps).
8. Dirty/unsaved interplay undefined (disk vs buffer).
9. Sample/binary/big/ignored undefined for writes — must surface skipped counts, never write binary.
10. Focus/keymap collisions: new `FocusID`s (`find-replace`, `workspace-replace`), Tab order, `mergeTextFocus` guards.

## 3. Design

### 3.1 Shared matcher (new `replace_match.go`)

```go
type searchOpts struct { Case, Regex, WholeWord bool }
type matchSpan struct { Start, End int }
func compileMatcher(query string, o searchOpts) (*matcher, error)
func (m *matcher) spansInLine(expandedRunes []rune) []matchSpan
```

* Literal preserves current semantics (non-overlapping, `SimpleFold` when `!Case`).
* Regex: Go RE2; query cap ~500 runes; ctx check per line; invalid → inline error, no search. `WholeWord`+regex = post-filter by word boundaries.
* Whole-word literal: `isWordChar` = letter|digit|_ (+unicode).
* Both `reindexFind` and `searchWorkspace` delegate. Keep `TrimSpace` for literal; do not trim regex.
* Replacement expansion: literal verbatim; regex `$1`/`$name` on matched substring only.
* Preserve-case: deferred v1 (ships `Case/Regex/WholeWord` only).

### 3.2 In-file replace bar (`Ctrl+H`)

```
[find input] [replace input] [N/M] [prev][next] [Replace] [Replace All] [x]  toggles: [Aa][.*][|ab|]
```

* State: `replaceOpen/replaceQuery/replaceRegex/replaceWholeWord` next to find fields; `findHits` computed with matcher.
* `openReplace(ctx)`: `findOpen=true, replaceOpen=true`, focus `find`. Seed `findQuery` from single-line selection if present.
* Keep toggles sticky across open/close (change current `closeFind` which resets `findCase=false`); persist in session; only clear queries/hits.
* `replaceOne`: take `findHits[findIndex]`, map rune offsets → buffer offsets; if editing use `edit.Text()` + `replaceEditRange` (single undo), else apply to `docs[path].Lines` + mark dirty; regex expansion; `reindexFind`, caret near replacement.
* `replaceAllInFile`: single `pushUndo`, splice per line end-to-start, one `commitEditBuffer + markDirty(all)` + refresh; toast count; `reindexFind`.
* Preview: current hit `find-cur` + `replace-cur` class showing replacement in bar (full inline diff deferred).
* Keys: `Ctrl+H` open; `Ctrl+F` keeps replace state; `Esc` closes both; `Enter` in find = next, in replace = replace-one, `Ctrl+Enter` = replace-all. `Alt+C/R/W` toggles. New `find-replace` focus id + `mergeTextFocus`/undo-guard updates.

### 3.3 Workspace replace (`Ctrl+Shift+H`)

Two-phase: search → review → confirm → apply.

```
[search input] [replace input] toggles [Aa][.*][|ab|]
rows grouped by file [x] path (N) + per-hit Before/Match/After → Before/Replacement/After
footer: [Replace All (K files, N hits, +S skipped)] note (caps/ignored)
```

* State: `searchReplace/searchRegex/searchWholeWord/replaceCheck map[path]bool` (file-level checkboxes v1) + per-hit `Replacement` preview.
* Search: extend `runWorkspaceSearch` with `searchOpts`; preview stays 200/8 but returns `totalEstimate + capped`; footer says "Showing first 200 of ≥N — Replace All scans everything". Apply does its own **uncapped rescan** with ctx + progress.
* Eligibility at apply (rechecked): skip `Big`/`>512KB`/`isBinary`/gitignored (default exclude, count in note; no include-ignored v1)/dirty/live-edit/sample (block all with toast)/external/read-errors. Hard ceiling e.g. 2000 hits — above requires narrowing.
* Lazy: force full load bounded by `MaxWorkspaceFiles` during apply with progress + cancel, else count unloaded as skipped (never silently miss).
* Apply per file: read raw (refuse dirty — "save first"), match on **raw runes** (not tab-expanded display), write via `ws.writeFile`, refresh `a.docs[path]`; block live-edit files. Journal `[]appliedFile{Path,Old,New,Count}` in memory (cap ~5MB) + `Undo last replace` button; per-file sequential, failures leave partial + report.
* Confirm: reuse `confirmView` (`confirmKind="replace-all"`): title `Replace N in K files?`, first ~10 files + skipped counts, `[Replace][Cancel]`, Esc cancels.
* Keys: `Ctrl+Shift+H` opens with replace focused; `Enter` in search = open hit; `Ctrl+Enter` = confirm; `Space` toggles file checkbox.

### 3.4 Toggles UX

Shared `searchOpts`, two instances (file + workspace), each sticky: Match case `Aa` (`Alt+C`), Regex `.*` (`Alt+R`), Whole word (`Alt+W`). Invalid regex → red outline + note, Replace disabled.

### 3.5 Undo grouping

In-file one/all = exactly one `pushUndo` per gesture (add grouped variant; current `replaceEditRange` pushes internally). Workspace: app-level journal + explicit undo button (not `Ctrl+Z`, avoids clobbering text/hunk stacks).

### 3.6 Caps + excludes

Preview caps stay; apply uncapped but bounded by `maxFileBytes`/`MaxWorkspaceFiles`/ctx/ceiling. Single eligibility predicate shared by preview+apply; default exclude ignored; footer `Skipped: B binary, L large, I ignored, D dirty`. Unify duplicated `200` const.

## 4. Checklist

- [x] `matcher` (`searchOpts`, compile, spans, expansion) + unit tests; unify `find.go` + `workspace_search.go`; unify `200` const.
  Evidence: `internal/app/replace_match.go` + `replace_match_test.go` (TestMatcherLiteral/WholeWord/Regex/Expansion PASS); `reindexFind` and `searchWorkspaceOpts` both delegate; `searchResultLimit` removed, preview uses `workspaceSearchLimit`. `go test -run TestReplace` green, `go vet ./internal/app/` clean.
- [x] In-file state + template bar (2 inputs + 3 toggles + count + buttons); sticky toggles in session.
  Evidence: `App.replaceOpen/replaceQuery/replaceRegex/replaceWholeWord/findError`, `components/html/editor.html` findbar (find + find-replace inputs, Aa/.*/W toggles, count, Replace/All), session persists FindCase/FindRegex/FindWholeWord (`session.go`, `app_session.go`). TestReplaceTogglesSticky PASS (toggles survive closeFind, queries clear).
- [x] Matcher-aware `reindexFind`; `replaceOne`/`replaceAllInFile` single-undo; raw/display col correctness; regex errors.
  Evidence: `find.go` (`replaceEditRanges` = one `pushUndo` per gesture; `hitBufferRange` maps display→raw via `rawColumnAt`; doc path folds into dirty doc). TestReplaceOneLiteral, TestReplaceAllGroupedUndo (exactly +1 undo, one Ctrl+Z restores), TestReplaceTabMapping (tab-indented line), TestReplaceRegexGroups (`$1`), TestReplaceWholeWord, TestReplaceInvalidRegex (inline error, replace disabled) PASS.
- [x] Keys/focus: `Ctrl+H`, `Enter`/`Ctrl+Enter`, `Alt+C/R/W`, `Esc`, `find-replace` id, guards.
  Evidence: `actions.go` (`Ctrl+H`→openReplace, `Ctrl+Shift+H`→openWorkspaceReplace, Enter=`replaceOne`/open-hit, Ctrl+Enter=`replaceAll`/confirm, Alt+C/R/W→toggleMatchKeys, Esc closes both, Space toggles file checkbox); `find-replace`+`workspace-replace` ids in `mergeTextFocus`, `pasteTargetReady`, Ctrl+A guard, click passthrough; keymap catalog entries `replace-in-file`/`workspace-replace` + `runKeymapAction` arms. TestReplaceKeys (Ctrl+H/Esc/Ctrl+F) + TestReplaceAltToggles PASS. `onUndo` overlay guard already covers the search panel; in-file bar keeps prior find behavior.
- [x] Workspace state + file-group checkboxes + `Replacement` preview + footer counts.
  Evidence: `searchReplace/searchRegex/searchWholeWord/replaceCheck`, `searchView`/`searchRow` (Replacement/Checked/GroupFirst/GroupCount, ReplaceFiles/ReplaceTotal), `palette.html` group headers with checkboxes + →replacement lines + footer. TestWorkspaceReplaceCheckbox PASS.
- [x] Opts-aware search + total-vs-capped note + invalid-regex handling.
  Evidence: `runWorkspaceSearch` compiles first (regex untrimmed, literal trimmed), `Total`/`Capped` counted past preview caps, `workspaceReplaceNote` ("Replace All scans everything"), invalid regex → `searchError` + no scan. TestWorkspaceSearchOpts (in `workspace_search_test.go`) + TestWorkspaceSearchInvalidRegex PASS.
- [x] Apply: eligibility recheck, lazy sweep, uncapped rescan + ceiling, raw-line replace, `writeFile` + refresh, skipped reporting.
  Evidence: `workspace_replace.go` (`replaceRescan` uncapped to 2000+1, `applyWorkspaceReplace` rechecks Ignored/size/binary/read per file, `sweepWorkspace` loads lazy dirs bounded by `MaxWorkspaceFiles`, `replaceRawBytes` preserves CR/BOM and matches raw runes, `writeFile` + doc refresh, `replaceSkipped` footer). TestWorkspaceReplaceApplyAndUndo, TestWorkspaceReplaceSkips, TestWorkspaceReplaceLazySweep, TestWorkspaceReplaceCeiling, TestWorkspaceReplaceCRLF, TestWorkspaceReplaceRegex PASS.
- [x] Confirm modal + `undoWorkspaceReplace` journal with cap.
  Evidence: `confirmKindReplaceAll` ("Replace N in K files?", first 10 files, Replace label/hint via OkLabel/OkHint), `appliedFile` journal capped at ~5 MB (`replaceJournalCapBytes`, undo disables with note), explicit undo button (not Ctrl+Z), dirty-since-apply files kept + reported. TestWorkspaceReplaceConfirm + TestWorkspaceReplaceJournalCap PASS.
- [x] Dirty/sample guards.
  Evidence: checked dirty/live-edit files block the whole gesture with a name list (`replaceBlockedFiles`); sample workspace refuses confirm and apply with toasts; no partial apply. TestWorkspaceReplaceDirtyBlocks + TestWorkspaceReplaceSampleBlocked PASS.
- [x] Tests: matcher tables; find replace-one/all (case/regex/word, single undo, tabs); workspace apply on temp dirs (caps, skips, groups, journal undo).
  Evidence: `replace_match_test.go`, `find_replace_test.go` (8 tests), `workspace_replace_test.go` (12 tests), `TestWorkspaceSearchOpts` appended to `workspace_search_test.go`. `go test -p 1 ./internal/app/ -run 'TestFind|TestReplace|TestSearch|TestWorkspace' -count=1` → ok; full `./internal/app/` suite green in the isolated worktree at /tmp/opencode/replace03-val; in the shared tree only unrelated in-flight explorer-menu tests fail (their files, absent at HEAD).
- [x] Docs/help + accessibility labels.
  Evidence: README key table (Ctrl+H, Ctrl+Shift+H, Alt+C/R/W) + matcher/undo semantics note; every new control carries title + aria-label (Aa/.*/W toggles, Replace/All, checkboxes, Replace All, Undo replace, inputs), inline errors use role=alert, toggle buttons use aria-pressed. TestAccessibilityLabels + TestAccessibilityPaletteDialog PASS.

## 5. Acceptance + tests

1. `Ctrl+H` bar with count/prev/next/Replace/All + toggles; `Ctrl+F` intact.
2. Replace-one respects literal/case/regex-groups/whole-word, advances, single `Ctrl+Z` undoes.
3. Replace-all one undo step + toast count.
4. Invalid regex inline error, Replace disabled.
5. `Ctrl+Shift+H` panel with checkboxes + `→ replacement` preview; caps labeled preview-only.
6. Replace-all confirms (N/K + skipped), applies to eligible only, blocks sample, offers undo.
7. New tests: `replace_match_test.go`, `find_replace_test.go`, `workspace_replace_test.go`; update `workspace_search_test.go`; focus test for `Ctrl+H`/`Esc`.

## 6. Risks

* **Regex**: RE2 safe; cap length, per-line cancel, `MustCompile` never, `$` escaping documented; test evil patterns.
* **Binary/large**: recheck `isBinary` on full bytes at apply; refuse `>512KB`; never normalize CRLF (preserve `\r`).
* **Ignored drift**: single predicate; default exclude ignored; unloaded dirs counted, never missed — force sweep bounded by budget.
* **Dirty loss**: refuse dirty/live-edit, list them, require save; in-file non-edit path sets dirty + refreshes tokens/outline.
* **Undo memory**: cap journal (~5MB/files); disable workspace-undo with note when exceeded.
* **Offset drift**: per-line end-to-start or rebuild; runes on raw line, convert once.
* **Sample**: early-out with toast, no partial apply.
* **Focus regressions**: every new `FocusID` added to `mergeTextFocus`, undo-guard, paste/explorer-ready audits.
