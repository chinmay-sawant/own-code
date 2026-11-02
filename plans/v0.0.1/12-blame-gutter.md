# Plan: Blame gutter (per-line last-commit annotation)

## 1. Spec

Toggle (off by default) that paints a per-line blame gutter for the open file.
Each code row shows the short hash of the line's last commit; the hover carries
author, date, and subject. Uncommitted lines read "Not committed" with no click
target. Clicking a hash opens that commit in the existing commit diff view.

- Data: `git blame --porcelain -- <path>`, parsed per `\t<content>` line into
  `blameLine{Hash, Short, Author, Date, Summary, Uncommitted}` (`blame.go`).
  All-zero hashes are uncommitted lines. Unknown porcelain lines are ignored.
- Async: `blameLoader` mirrors `commitPatchLoader` (one worker, non-blocking
  `Request`, `Poll` drained in `tickWatch`). While loading, the crumbs carry
  "Loading blame…"; rows populate when `pollBlame` installs the result and
  refreshes. The page loop never blocks on git.
- Cache: keyed by path plus size and mtime (`blameStatKey`). `ensureBlame`
  dedups in-flight keys in `blamePending`. Invalidated on save (`saveEdit`
  drops the path), on watcher rescan (`installRescan` / `applyWatchChanges`
  drop reloaded paths), and on git refresh (`applyGitRefresh` drops all).
- Toggle: palette command `View: Toggle Blame` plus the `B` key. `B` is free:
  no home branch in `onKeyDown` names it and no keymap default uses it; it
  fires through `keymapAllows("toggle-blame")` like `Z` for wrap, and is
  rebindable via the `toggle-blame` keymap entry.
- Composition: annotation only on the first visual row (`No == 0`
  continuations stay blank); per-group state (`View.BlameShow/BlameNote` and
  `secondaryView`) so split panes differ; gutter-mark column untouched;
  line-number gutter untouched (blame is its own 72 px column, so `ln` boxes
  keep painting). Wrap budget shrinks by the blame width while shown
  (`codeColsForGroup`); click-to-edit surfaces shift right via
  `.code-body.blame` (`left: 132px`).
- Skip with a crumbs note instead of blame: sample workspace, binary files,
  big (`meta.Big`) and truncated files, and files `openDoc` rejects.
- Zoom: `.blame` width/padding rule plus the shifted hit-test rule in
  `zoomEditorCSS`, `.blame` in the zoom font table and the per-view editor
  override — the same sections pattern as `.ln`.
- Session: runtime only, resets to off on project switch; not persisted.

## 2. Files

- `internal/app/blame.go` (new): parser, loader, `toggleBlame`,
  `ensureBlame`/`pollBlame`, `decorateBlame`, `openBlameCommit`, invalidation.
- `internal/app/app.go`: `blameOn/blameLoader/blameCache/blamePending`
  fields, loader lifecycle (`New`/`switchProject`), `fillCode` wiring.
- `internal/app/view.go`: `codeRow.Blame/BlameTitle/BlameHash`,
  `View.BlameOn/BlameShow/BlameNote`, `secondaryView.BlameShow/BlameNote`.
- `internal/app/pane.go`: `secondView` wiring, `codeColsForGroup` budget.
- `internal/app/actions.go`: `blame-toggle` / `blame-commit:<hash>` clicks,
  `B` key.
- `internal/app/settings.go`: `toggle-blame` keymap entry + dispatcher.
- `internal/app/explorer_ops.go`: `View: Toggle Blame` palette command.
- `internal/app/edit_ops.go`, `watch.go`, `terminal.go`: invalidation + poll.
- `components/html/editor.html`, `components/css/editor.css`,
  `internal/app/zoom_sections.go`, `zoom.go`: column, toggle, zoom rules.
- `internal/app/blame_test.go` (new): tests below.

## 3. Checklist (boxes checked only with test evidence)

- [x] Porcelain parse: two committed blocks share one entry; hash/short/
  author/date/summary land on the right lines (`TestParseBlamePorcelain`).
- [x] Parser ignores noise (`boundary`, `previous`, bad `author-time`)
  (`TestParseBlamePorcelainIgnoresNoise`).
- [x] Uncommitted lines display "Not committed" with no hash, hover included
  (`TestParseBlamePorcelain`; live new line in `TestBlameCacheInvalidatedOnSave`).
- [x] Off by default: no flag, no note, no column (`TestBlameOffByDefault`).
- [x] `B` toggles on with the "Loading blame…" busy note, then the column
  applies on arrival (`TestBlameToggleOnKeyAndPalette`).
- [x] Palette `View: Toggle Blame` toggles back off and clears every row
  (`TestBlameToggleOnKeyAndPalette`).
- [x] Rows carry short hash + author hover; hashes resolve in history; the
  `ln` and `blame` boxes paint side by side (`TestBlameToggleOnKeyAndPalette`).
- [x] Cache invalidated on save: key moves, line count grows, new line reads
  "Not committed" (`TestBlameCacheInvalidatedOnSave`).
- [x] Rescan retires the cache and the next frame re-requests
  (`TestBlameRescanInvalidates`).
- [x] Clicking a hash opens the commit diff (`mode=changes`,
  `diffSource=commit`, `commitOpen=hash`) (`TestBlameClickOpensCommit`).
- [x] Big files show the "large" note, no blame (`TestBlameBigFileSkipped`).
- [x] Truncated files show the "large" note, no blame
  (`TestBlameTruncatedSkipped`).
- [x] Binary files show the "binary" note, no blame (`TestBlameBinarySkipped`).
- [x] Sample workspace shows the "sample" note, no blame
  (`TestBlameSampleWorkspaceNote`).
- [x] Wrapped continuation rows stay blank; first visual rows carry blame
  (`TestBlameWrappedContinuation`).
- [x] Changed rows carry both gutter mark and blame
  (`TestBlameComposesWithGutterMarks`).
- [x] Split panes paint per-group blame (`TestBlameSplitPerGroup`).
- [x] Zoom override scales the column, hit shift, and font
  (`TestBlameZoomOverride`).
- [x] Loader caches by stat key; queue drops nothing silently
  (`TestBlameLoaderCachesByKey`).

## 4. Validation

- `gofmt -l internal/app/` clean.
- `go vet -mod=mod ./internal/app/` clean (plain `go vet` asks for a
  `go.mod` tidy from the sibling `ownframe` replace — pre-existing env
  drift, reverted after the run).
- `go test -mod=mod -p 1 ./internal/app/ -run
  'TestBlame|TestGutter|TestCommit|TestHistory' -count=1` green.
- Full `go test -mod=mod -p 1 -count=1 ./internal/app/` green (188s).
