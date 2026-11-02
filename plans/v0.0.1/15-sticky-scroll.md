# Plan: Sticky scroll (pinned enclosing-symbol headers)

## 1. Spec

- Up to `maxStickyRows` (3) pinned header rows sit above the code while its
  body scrolls. They show the enclosing symbol chain at the current scroll
  top: the last outline symbol with `Line <=` top source line, plus its
  parents via `Depth` (e.g. `Row` + `uptime` in `tools/report.py`).
  `internal/app/sticky_scroll.go:stickyChain`.
- Derived from the document outline, so it updates on scroll (every
  `buildView`) and on edit (`commitEditBuffer` rebuilds `doc.Outline`).
- Clicking a header jumps to the symbol through the group-aware scroll path
  (`scrollToSticky`, reusing the `outlineLine` target math), not just group 0.
- Toggle: palette command `View: Toggle Sticky Scroll` (`toggle-sticky`)
  plus key `T` (`toggle-sticky` keymap entry, default `T`; verified free).
  Persisted per workspace in the session (`NoSticky`, missing = enabled).
- Works wrapped and unwrapped (top line resolves through the wrap plan via
  `firstRow`/`lineAt`), in both split panes independently (per-group scroll
  offsets, per-group `Sticky` in `View`/`secondaryView`), with the
  gutter/line-number toggle and zoom scaling (headers reuse
  `var(--row-h)`, `.gutter-mark`/`.ln` spans, and the zoom font table).
- Disabled in diff (`changes`), preview, and merge modes; empty,
  binary, error, and symbol-free files show nothing (no placeholder rows).
- No new dependencies.

## 2. Geometry contract

- The sticky block is absolutely positioned at the top of each `code-body`
  (`#code-sticky`, `#code-sticky-2`, `z-index: 5`) and rendered AFTER the
  track/`edit-surface` siblings so header buttons win hit-testing over the
  full-body edit layer (`editor.html`, verified by `TestStickyClickJumps`).
- Go shifts the windowed track down by the header height
  (`TrackTop = len(Sticky) * zx(rowH)`) and shrinks the slice by the same
  rows (`windowSliceReserve`), so headers + rows stay exactly full.
- Paint-adjacent math follows the shift:
  `editPointAt` maps presses past the header block (`selection_ops.go`),
  the four inline-card anchors add `stickyTopPx(0)` (`inline_ops.go`).

## 3. Checklist (checked = has test evidence)

- [x] Scroll deep shows the enclosing header — `TestStickyShowsEnclosingHeader`
- [x] Nested chain depth (`[Row uptime]`) — `TestStickyNestedChainDepth`
- [x] Edit updates the header (rename via the outline rebuild path) — `TestStickyEditUpdatesHeader`
- [x] Clicking a header jumps to the symbol (real `Page().Click` on `sticky-<line>`) — `TestStickyClickJumps`
- [x] Wrapped and unwrapped modes — `TestStickyWrappedAndUnwrapped`
- [x] Split panes scroll independently with per-pane headers — `TestStickySplitIndependent`
- [x] Diff, Markdown preview, and merge modes show none — `TestStickyHiddenInDiffPreviewMerge`
- [x] Symbol-free files and above-first-symbol show nothing — `TestStickyEmptyAndUnsupported`
- [x] Toggle flips headers, persists, palette command dispatches — `TestStickyToggle`
- [x] Zoom keeps headers on the enclosing symbols with scaled track — `TestStickyZoomScaling`
- [x] Gutter/line-number toggle leaves headers unchanged — `TestStickyGutterToggle`
- [x] `T` chord is free, wired, and toggles — `TestStickyKeymapChord`
- [x] Existing scroll/caret geometry follows the shift — `TestScrollWindow` (updated invariant), `TestEditCaretFirstClickAndVerticalKeys` (updated coordinates)

## 4. Validation

- `gofmt -l internal/app/` clean.
- `go vet ./internal/app/` clean.
- `go test -p 1 ./internal/app/ -run 'TestSticky|TestOutline|TestScroll|TestSplit|TestWrap' -count=1` green.
- Full `go test -p 1 ./internal/app/ -count=1` green.
- Note: `go.mod`/`go.sum` carry a one-line indirect bump (`blinkless`
  v0.0.4-… → v0.1.1-…) that the toolchain required before `go vet`/`go test`
  would run at all; unrelated to the feature.
