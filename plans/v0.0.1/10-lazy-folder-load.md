# Lazy folder load for 10k+ file repos — plan

## 1. Current behavior + files

### Startup scan: full-tree crawl up to budget

| Piece | File | Behavior |
|---|---|---|
| Budget | `workspace.go:24` `MaxWorkspaceFiles = 10000` | Default cap. |
| Flag | `cmd/own-code/main.go:21,38` `-files`, `NewWithLimit` | `-files 25000` raises cap. |
| Entry | `app.go:370-387` `New/NewWithLimit`; `switchProject:696-702` | `root==""` → sample; `limit>0 && !=Max` → `loadDiskWorkspaceWithLimit`; else → `loadDiskWorkspaceLazy`. |
| Full scan | `workspace.go:230-339` `loadDiskWorkspaceWithLimit` | `WalkDir`, `skipDir/skipExt`/dotfile/symlink/`maxTreeDepth=12`, stop-before-limit + `Truncated/truncatedNote/ScanBudget:696-717`. Sorts `Files`, builds full `Tree`. Then git-ignore subprocesses (`loadIgnoredPaths/loadIgnoredDirs` ≤5000 dirs) + `scanIgnoredDirs/walkIgnoredDir` re-descend + `markIgnored/addIgnoredDirs` placeholders (≤200). |
| `finish():837-866` | Sorts all `Files`, builds every `fileNode`, `Loaded:true` everywhere. O(N log N) + node/meta per file resident. |
| Status | `app.go:890` `ScanBudget`, `feature_test.go:308` | `Showing the first N files...` when truncated. |

### Tokenization: README vs code

`README:186` says "tokenizes every line" at startup. Code does not: `openDoc:1301-1339` tokenizes per opened file (`maxFileBytes=512KB/maxFileLines=20000/maxLineBytes=32KB`; `readLines:1129-1186` expands tabs, detects indent). Startup cost is walk + stat + sort + git-ignore + tree, not highlighting. Fix README sentence in this work.

### Partial lazy infra (sync, not async)

| Piece | File | State |
|---|---|---|
| Root-only scan | `workspace.go:136-226` `loadDiskWorkspaceLazy` | `ReadDir` root only; dirs `Loaded:false`, files `Loaded:true`. No budget. Default when `-files` unset. |
| Node | `workspace.go:50-59` `fileNode{Path,Name,Dir,Ignored,Loaded,Meta,Children}` | `Loaded` is the lazy bit. |
| Sync load | `workspace.go:868-931/986-1062/952-982/1064-1073` `loadChildren/LoadChildren/loadNode/loadDir/ensureFileLoaded` | `ReadDir` one dir, same filters, appends `Files/ByPath`, re-sorts `Files` per expand (O(E·N log N) scaling bug). |
| Toggle | `actions.go:2336-2349` `onToggleDir` | Flips `folders`, sync load, `refresh()` — blocks page loop. |
| Render load | `app.go:2271-2311/2242-2266` `walkTree/ensureOpenFoldersLoaded` | Inline `loadNode` while building rows — unbounded render latency. |
| Open-by-path | `workspace.go:740-800/1301-1305` `ensureFile/openDoc→ensureFileLoaded` | Ancestor dirs on demand. Covered `lazy_tree_test.go:268-302`. |
| Tests | `lazy_tree_test.go`, `sticky_tree_test.go:173-218` | Lock sync behavior. No async/placeholder coverage. |

### Rescan: lazy flag preserved, work still full

`rescan.go:80-94` always full-walks then sets `next.Lazy=true`/clears truncation if `old.Lazy`. Callers `watch.go:340`, `scm_ops.go:626`, `explorer_clipboard.go:526`, `explorer_paste.go:368`.

### Watcher: full-tree handles, 2000-dir cap

`watch.go:15-24` `maxWatchDirs=2000/debounce=150ms/buffer=4096/gitPoll=2s`; `StartWatcher:48-106` desktop only (`-web` never; `README:177`); `addWatchTree:150-234` walks every dir until 2000; `watchLoop:298-471` any change → full rescan + history reload; new dirs background-added; overflow forces rescan.

### Quick-open + search

Quick-open `palette.go:140-224` in-memory `Files` filter + sync `diskSearchFiles` (`palette_search.go:25-82`: `rg --files` → `git ls-files` → walk, timeout, `MaxWorkspaceFiles`). Content search `workspace_search.go:47-140` loops `ws.Files` (`readFile`+binary+split per file, 200/8 caps, ctx per file/line); async wrapper `search_ops.go:125-165` token/chan. Under lazy, unloaded subtrees silently missed. Sidebar filter same `Files`-only scope.

## 2. Gaps vs VS Code

Reference `README:184-193`.

| Area | own-code | VS Code | Gap |
|---|---|---|---|
| Tree | Full walk or root-lazy with sync expand + full re-sort; no spinner; render blocks on I/O. | Async expand with placeholder + spinner. | Async worker + placeholder/spinner/cancel; I/O off render loop. |
| Quick-open/search | In-memory slice; one-shot timeout disk fallback; content over loaded `Files` only. | Streaming ripgrep from disk, respects ignores, streams first hits. | Streaming `rg --files`/`rg --json` covering unloaded dirs + ignore parity. |
| Watching | One handle/dir, 2000 cap, full rescan per burst. | Recursive, skips `node_modules`/objects. | Watch root + visible dirs + git only; targeted `loadDir` refresh. |
| Budgets | Global `-files` startup truncation. | Per-query streaming caps. | Per-operation caps + cancel, not startup truncation. |

Code gaps: sync I/O on page loop (`onToggleDir`+`walkTree`); global re-sort per expand; rescan defeats lazy; watcher handles unopened dirs; search/filter inconsistent with disk once lazy; ignored placeholders lack loading state; no cancellation (dir loads; search cancels, loads don't).

## 3. Design

### 3.1 Principles

* Startup `ReadDir(root)` only (keep `loadDiskWorkspaceLazy` default; reserve full walk for explicit `-files != Max` + tests + web snapshot).
* No disk I/O on render loop. Single background worker, per-path tokens; placeholder/spinner from state.
* Search/quick-open read from disk (streaming) when lazy. `ws.Files` = "seen so far", not search corpus.
* Respect `skipDir/skipExt`/dotfile/symlink/`maxTreeDepth`/git-ignore everywhere (streaming layer reproduces or delegates to `rg`/`git` flags).
* Budgets per-operation with notes, not silent truncation.

### 3.2 Expand-on-demand with placeholder + spinner

State: `fileNode += Loading bool + LoadErr string` (transient); `App += dirLoads map[path]int + dirLoadSeq + dirLoadResult chan dirLoadReply{Token,Path,Entries,Err,Millis}`; `treeRow += Loading/LoadErr`. `walkTree` never calls `loadNode`; unloaded-open emits synthetic spinner child (`path+"/…"`, non-focusable, excluded from counts); `LoadErr` emits retry row.

Flow: `onToggleDir` (and space/click equivalents) flips `folders`, calls `requestDirLoad(path)` if opening unloaded, returns immediately. Worker (one goroutine, started `NewWithLimit`/`switchProject`, stopped `Close`): serial `ReadDir`, filters (preserve ignored-descend rule), `entry.Info()` sizes for files, cap `dirChildCap` (propose 2000, first-N + "N more…" row). Page loop (`tickWatch` alongside `receiveSearch`): `receiveDirLoads` token-matches, `insertNodeChildren` (sort children only; binary-insert or batched `Files` append — no global re-sort), clears `Loading`, sets `Loaded=true` even on empty/error, `refresh()`. Collapse bumps token (late reply dropped); retry via `requestDirLoad`. Keep sync `LoadDir/loadNode/ensureFileLoaded` for by-path opens + tests, forbid from `walkTree`/`onToggleDir` (lint comment + no-syscall render test). `ensureOpenFoldersLoaded` becomes depth-sorted enqueue scheduler (concurrency 1).

### 3.3 Worker details

Single-flight per path; depth-first ordering reuse; `dirChildCap` per dir + in-flight cap 1 (or 2: explorer + ancestor loads); over-cap trailing pseudo-row (not a file). Errors: permission → `Cannot read folder`; deleted → drop node + prune `folders`; other → truncated `LoadErr`. Metrics per-load ms; slow (>500ms) to devtools Frame tab (`-perf` hook).

### 3.4 Quick-open / content search without full scan (Option A preferred: ripgrep streaming)

* Quick-open: streaming `rg --files --hidden --glob ...` pushing first `paletteMaxRows` incrementally (new `paletteBusy/paletteNote` mirroring search); fallback chain unchanged but streaming (`bufio.Scanner` + ctx, not `Output()`). `skipExt`/dotfile at merge; `--glob '!node_modules/**'` derived from `skipDir` so `rg` and walker agree. Debounce + cancel-on-keystroke (token pattern).
* Content: `searchRipgrep(ctx,root,query,case,limit)` via `rg --json` (or `--vimgrep`) with `--max-count 8`, hidden/ignore flags, parse streaming into `workspaceHit` (reuse preview slicing). Keep `searchWorkspace` in-memory loop as no-`rg`/test fallback. Merge: stream disk hits; if no `rg`, loaded-files pass + bounded walk fallback with honest note ("covered N loaded files; expand or install ripgrep for full-repo") — never claim full coverage silently.
* Excludes: `rg` respects `.gitignore` by default (do not pass `--no-ignore`); verify `rg --files` vs `loadIgnoredPaths/loadIgnoredDirs` on fixture (`node_modules/dist/.gitignore secret`). Binary/big: `rg` binary-aware; add `--max-filesize 512K` mirroring `maxFileBytes` (verify flag; else post-hoc `stat` filter).
* Option B (only if no-`rg` too slow on 25k): idle breadth-first path index (paths only, cap ~50k) for quick-open; content still per-query streaming. Implement A first.

`ws.Files` stops being search corpus; `TruncatedNote`/`ScanBudget` wording updated (lazy never file-budget; search/palette own notes).

### 3.5 Budget + cancellation

Retire startup truncation for lazy (`ScanBudget()==""` always; lock test). New budgets: `dirChildCap`, `paletteMaxRows`/`paletteDiskSearchTimeout`, `searchResultLimit=200`/`fileHits=8`, index cap if built. Every background unit: ctx timeout/cancel-on-new, token, `Busy`+`Note`, stale-drop (`receiveSearch` template). `-files` stays escape hatch: explicit N = full scan; default = lazy. Document in `--help` + README.

### 3.6 Watcher scope shrink

`addWatchTree`: root + loaded/expanded dirs + git metadata only. Newly loaded dir → `watcher.Add` best-effort; collapse/switch prunes under `maxWatchDirs` pressure (collapsed-first eviction). New-dir Create keeps background subtree-add. Events: loaded dir → `requestDirLoad(dir)` + `loadLiveChanges`; unloaded → mark parent stale (`Loaded=false`), no I/O; overflow/root-rename/`.gitignore` → full rescan (rare). `rescanDiskWorkspace` gains lazy-incremental mode (re-`ReadDir` root + each `Loaded` dir, not full walk); full-walk kept for explicit `-files`. Handles become backstop, not routine truncation.

## 4. Checklist

- [x] Worker + state: chan/tokens/`Loading`/`LoadErr`, spinner + retry rows, `requestDirLoad`/`receiveDirLoads` in `tickWatch`.
- [x] De-block render: `walkTree`/`onToggleDir` enqueue-only; sync loaders kept for by-path/tests, marked off-render-path.
- [x] `insertNodeChildren` (children-only sort, batched `Files`), `dirChildCap` + more-row, `watcher.Add` on load.
- [x] Cancellation: collapse token-drop, retry, slow metric.
- [x] Quick-open streaming: cancellable `rg --files` → `git ls-files` → walk, incremental render, `paletteBusy/Note`, exclude-parity test.
- [x] Content streaming: `searchRipgrep` caps + `max-filesize`, fallback note, keep `searchWorkspace` for no-`rg`/tests.
- [x] Watcher shrink: root+loaded-only, targeted refresh, lazy-preserving rescan, eviction.
- [x] Ignored placeholders: muted `IGNORED` kept; expand loads real children with spinner; empty stays placeholder (no spin loop).
- [x] Filter + `TreeCount`: exclude spinner/retry/more rows; filter declares scope in note.
- [x] Flags/docs: `-files` help (N = full; default lazy), README Limits + VS Code table (fix tokenization sentence), `ScanBudget` lock test.
- [x] Tests: async expand, cancel-collapse, retry-permission, 10k cap, search-unexpanded via rg (skip if absent), targeted-not-full watcher (counter hook), web no-watcher regression.
- [x] Perf gates (§5) measured + recorded.

## 5. Acceptance + perf gates

Functional: 10k + 25k synthetic repos (generated `many/dirXXX/fileYYY.go` + `node_modules/dist/.gitignore`): root <1s, no full walk (`len(Files)==root-only` + walk-counter); expand shows spinner-in-one-frame, no input freeze, collapse-during-load drops stale; quick-open finds unexpanded file; content search finds unexpanded string with scope-honest note; external create/delete under expanded dir targeted-reloads (no full walk); `go test -p 1 ./...` green (lazy/sticky/workspace/rescan/watch/palette/search updated not deleted).

| Gate | 10k | 25k | Notes |
|---|---|---|---|
| Startup to first frame | ≤800ms | ≤1200ms | `ReadDir(root)` + git root + paint; record `ScanMillis`. |
| Peak extra RSS vs empty | ≤60MB | ≤120MB | Root files only; no content pre-read. |
| Expand p50/p95 (500-file dir) | ≤100/300ms | same | Worker + insert + repaint; no dropped frames. |
| Quick-open first paint | ≤200ms | ≤300ms | Streaming `rg`; fallback must meet or trigger index work. |
| Content first hits | ≤500ms | ≤1000ms | Streaming; 200-hit completion may lag with note. |
| Watcher handles | ≤ expanded + ~8 git + root | same | Well under 2000; overflow never hit. |
| Full walks after startup | 0 | 0 | Counter hook fails on violation (except explicit `-files`/overflow). |

Record on Linux desktop + `-web`; gate failures block merge.

## 6. Risks

* **Consistency**: `rg` vs tree mid-flight (create after start, unloaded rename). Per-query snapshot note, token-drop, `openSearchHit` re-stats (`openDoc` size check) + "changed, reopen" over stale line.
* **Ignored placeholders**: unloaded ignored ≡ empty until expanded. Keep `Ignored` bit, `Loaded=true` on empty (no spin loop), badge + empty note, git-fixture test.
* **Web no watcher**: Refresh re-loads root + `Loaded` dirs (lazy-preserving rescan); expanded refresh affordance; `openDoc` re-stat truth. Test web path.
* **`rg` parity**: absent/differing flags. Version-tolerant flags + post-hoc size/`skipExt` filter; honest fallback note; parity test skips-without-`rg`, fails-on-mismatch-with-`rg`.
* **Sort regression**: missed global-sort call site reintroduces quadratic — add `BenchmarkWorkspace_ExpandMany`.
* **Windows deadlock**: `watcher.Add`-on-expand off-loop (Create-branch pattern); drain-before-Add; keep buffer sizing.
* **Doc drift**: fix "tokenizes every line" in same change.
