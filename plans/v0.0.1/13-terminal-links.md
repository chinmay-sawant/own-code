# 03 — Clickable file:line links in embedded terminal

> Branch: `wt/top5-termlinks` (worktree `/home/chinmay/ChinmayPersonalProjects/own-code-termlinks`)
> Scope: terminal output links only. No new dependencies.

## Spec

Visible terminal rows link `path:line`, `path:line:col`, absolute paths, and
workspace-relative paths resolved against the session cwd
(`App.terminalDir`, the workspace root). Also Python traceback
`File "x", line N` and Go/gcc `file.go:12(:3)` forms.

Parser (`internal/termui/links.go:ParseTerminalLinks`):

- Regex-free rune scan, single pass per visible row. Only visible rows are
  parsed (`termui.Render` → `applyRowLinks`), so scrollback costs stay bounded
  by the viewport.
- Path charset `[A-Za-z0-9_./-+~]`; file-like requires `.` or `/` plus one
  ASCII letter. Rejects timestamps (`12:34`, `12:34:56`), decimals
  (`12.34:56`), bare numbers/words, `<stdin>`.
- `://` spans are marked as URLs and skipped, so
  `https://example.com/foo.go:12` and `http://localhost:8080/path` stay plain
  text.
- Python span covers path through line digits; generic span covers path
  through line/col digits. Overlapping Python wins; output sorted and
  de-duplicated.

Render (`internal/termui/termui.go:applyRowLinks`, `internal/app/terminal_model.go`,
`components/html/terminal.html`, `components/css/terminal.css`):

- Links recomputed every `Render` from row text, not stored per frame, so
  scrollback windows relink on scroll.
- Link pieces keep ANSI style, add `underline`, and carry
  `LinkPath/LinkLine/LinkCol` through `termui.Segment` → `app.segment` →
  template button `term-link:row:seg` (`id="term-link-…"`, keeps `term` prefix
  so focus logic holds).
- `.term-link` button reset underlines and hovers with `var(--accent)`.

Click (`internal/app/terminal_links.go:openTerminalLink`,
`internal/app/actions.go:parseTermLinkAction`):

- Existing path only: `openFileAtPreview(activeGroup, wsPath, true)` then
  `gotoLine(line, col)`. Preview slot, dirty-preview auto-pin, merge guard,
  and `pushNavHistory` ride along. Edit auto-pin clears the preview slot,
  matching normal preview opens.
- Relative resolved against session cwd; absolute inside root relativized to
  workspace-relative; outside stays absolute for external opens.
- `os.Stat` missing/directory → `showToast("File not found")`, no open, no
  caret move. Sample-root fallback uses `ensureFile` then the same toast.
- Clicks only. No `handleTerminalKey` change; `terminalOwnsKeys` untouched.

## Checklist

- [x] Pattern table incl. false positives — `internal/app/terminal_links_test.go:TestTermLinkPatterns` — `go test -p 1 ./internal/app/ -run 'TestTermLinkPatterns' -count=1` PASS
- [x] Relative resolution against cwd — `internal/app/terminal_links_test.go:TestTermLinkRelativeResolution` — `go test -p 1 ./internal/app/ -run 'TestTermLinkRelativeResolution' -count=1` PASS
- [x] Absolute paths (inside relativized, outside external) — `internal/app/terminal_links_test.go:TestTermLinkAbsolutePaths` — `go test -p 1 ./internal/app/ -run 'TestTermLinkAbsolutePaths' -count=1` PASS
- [x] Click opens at line via preview+goto with underline + nav — `internal/app/terminal_links_test.go:TestTermLinkClickOpensAtLine` — `go test -p 1 ./internal/app/ -run 'TestTermLinkClickOpensAtLine' -count=1` PASS
- [x] Missing file toasts instead of failing — `internal/app/terminal_links_test.go:TestTermLinkMissingFileToast` — `go test -p 1 ./internal/app/ -run 'TestTermLinkMissingFileToast' -count=1` PASS
- [x] Scrollback render relinks per visible window — `internal/app/terminal_links_test.go:TestTermLinkScrollbackRender` — `go test -p 1 ./internal/app/ -run 'TestTermLinkScrollbackRender' -count=1` PASS
- [x] Focus key ownership (clicks only, no key grabs) — `internal/app/terminal_links_test.go:TestTermLinkFocusOwnership` — `go test -p 1 ./internal/app/ -run 'TestTermLinkFocusOwnership' -count=1` PASS
- [x] Dirty-preview guard rides preview path — `internal/app/terminal_links_test.go:TestTermLinkDirtyPreviewPreserved` — `go test -p 1 ./internal/app/ -run 'TestTermLinkDirtyPreviewPreserved' -count=1` PASS
- [x] Parse work capped (visible rows only, regex-free scan) — `internal/termui/links.go:ParseTerminalLinks`, `internal/termui/termui.go:applyRowLinks` — `go test -p 1 ./internal/termui/ -count=1` PASS + `go test -p 1 ./internal/terminal/ ./internal/termui/ ./internal/app/ -run 'TestTermLink|TestTerminal' -count=1` PASS
- [x] Suite green — `gofmt -l internal cmd` empty; `go vet ./internal/terminal/ ./internal/termui/ ./internal/app/` clean; `go test -p 1 ./internal/app/ -count=1` PASS (188s); `go test -p 1 ./internal/terminal/ -count=1` PASS; `go test -p 1 ./internal/termui/ -count=1` PASS
