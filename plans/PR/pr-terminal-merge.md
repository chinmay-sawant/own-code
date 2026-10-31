# feat(app): add embedded terminal and merge conflict resolver

Suggested title: `feat(app): add embedded terminal and merge conflict resolver`

## Summary

This branch adds two workflows to own-code: an embedded terminal panel and a Git merge conflict resolver. Both run inside the existing ownframe desktop window. Neither needs Chromium, a browser, or an external terminal.

The terminal runs a shell in the active workspace, streams output live, and supports tabs, a resizable split, interactive input, ANSI output, and clipboard actions. The merge resolver lists unmerged paths in Source Control, opens a merge editor with Current, Incoming, and Result, and stages only when the user explicitly marks a file resolved.

## Why

own-code is a review app, but reviewing a change often means running a test, checking a Git command, or resolving a conflict without leaving the window. Issue #1 covers the terminal. Issue #2 covers the conflict resolver.

The terminal should stay inside the app instead of opening another process window. The conflict resolver should reuse the existing Source Control and editor surfaces instead of sending users to an external merge tool.

## Changes

### App and backend

- Add a terminal session manager with streamed input and output, per-session working directories, resizing, and process cleanup.
- Use a PTY on Unix. Use a pipe-backed fallback on Windows where a PTY is unavailable.
- Read `OWN_CODE_SHELL` for shell selection, falling back to the platform default.
- Add Git conflict detection from unmerged index stages. This covers merge, rebase, cherry-pick, and revert conflicts.
- Add merge operations for reading base, current, and incoming stages, saving the result, and staging one resolved path.
- Handle diff3 base data, binary files, add/add, modify/delete, rename, and missing-base cases with explicit fallbacks.
- Guard saves against external file changes and keep staging and committing as separate actions.
- Pin ownframe to the latest master commit (`dbcfeccf`) and add `github.com/creack/pty` for Unix PTY sessions.
- The branch also carries the pre-existing `Open absolute paths from quick open` commit (`7ce219e`) that was already on the remote branch when this work started. It is included for branch continuity, not as part of either terminal or merge behavior.

### UI and components

- Add a terminal panel below the editor with an open and hide toggle, resizable split, and session tabs.
- Render ANSI colors, cursor movement, scrolling, keyboard input, and copy/paste in the terminal.
- Add conflict badges and counts to the Source Control conflicts list.
- Add a merge editor with Current, Incoming, and editable Result, plus a three-way Compare view when a base is available.
- Add Accept Current, Accept Incoming, Accept Both, and Compare actions in the editor and context menus.
- Add previous and next conflict controls with a position counter and scroll to the active range.
- Add Save and explicit Mark Resolved or Stage, then refresh Source Control immediately.

### Docs

- Add [docs/terminal.md](docs/terminal.md) and [docs/merge-conflicts.md](docs/merge-conflicts.md).
- Link both guides from the feature list in [README.md](README.md).
- Add this repository's pull request template.

## Impact

| Area | Impact | Notes |
|---|---|---|
| Terminal | New | Embedded panel, sessions, PTY on Unix, pipe fallback on Windows |
| Source Control | Extended | Conflicted paths, badges, counts, explicit stage refresh |
| Editor | Extended | Merge editor reuses the existing buffer, undo, and context menu |
| Keys and focus | Extended | Terminal keys belong to the shell while it has focus; app shortcuts work elsewhere |
| Platform support | Extended | Unix PTY support; Windows pipe fallback; WSL paths keep the Linux behavior |
| Build and dependencies | Changed | ownframe master pin and `creack/pty`; Windows build still passes |
| Docs | New | Terminal and merge conflict guides, README links, PR template |
| Existing review flow | Unchanged | Explorer, diff, search, history, and normal editing keep their current behavior |

## Breaking changes

None. The terminal and merge resolver are additive. Existing editor, diff, search, history, and Source Control defaults do not change.

## Test plan

### Automated

- [x] `go test ./...`
- [x] `go test -race ./internal/terminal ./internal/termui ./internal/merge`
- [x] `go test -race ./internal/app -run 'Terminal|Merge'`
- [x] `go vet ./...`
- [x] `go build ./...`
- [x] `GOOS=windows GOARCH=amd64 go build ./...`
- [x] `gofmt -l internal/app internal/merge internal/terminal internal/termui`
- [x] `git diff --check`

### Terminal

- [x] Open and hide the panel, resize it, and confirm the editor stays responsive
- [x] Run `pwd`/`printf` and confirm the workspace directory and streamed output
- [x] Open several sessions, switch between them, close one tab, and confirm its process exits
- [x] Route `Ctrl+C` to the shell without copying, and `Ctrl+Shift+C`/`Ctrl+Shift+V` to copy/paste
- [x] Confirm app shortcuts are swallowed while the shell has focus and restored after an outside click
- [x] Quit own-code and confirm no shell remains (`App.Close` closes all sessions)
- [ ] Run an interactive program such as `vim` or `less`, send input, and resize under a desktop session
- [ ] Set an invalid `OWN_CODE_SHELL` and confirm the panel note in a desktop session

### Merge

- [x] Create multi-hunk conflicts and confirm the Source Control badge and count
- [x] Accept Current, Incoming, and Both in separate conflicts
- [x] Use Compare with a diff3 base and without a base
- [x] Step with previous and next, confirm the counter and scroll position
- [x] Edit Result manually, save, and confirm the working tree content
- [x] Run Mark Resolved or Stage and confirm Source Control refreshes
- [x] Confirm save and accept do not stage, and no command commits
- [x] Test diff3, binary, add/add, and missing-file conflicts
- [x] Change the file on disk after opening it and confirm the save guard
- [ ] Test a live rebase or cherry-pick conflict (the same unmerged-index path is covered by the merge tests)

### UI and platform

- [x] Render the terminal and merge editor at 1500x900 and 840x700
- [x] Dark theme at both sizes, including the compact merge header
- [ ] Light theme visual check
- [ ] macOS PTY and Windows pipe fallback runtime checks (Windows compile passes)

## Docs

- [Terminal](docs/terminal.md)
- [Merge conflicts](docs/merge-conflicts.md)

## Related issues

Closes #1
Closes #2

## Diff stat by extension

Generated from `git diff master...HEAD`. Rebuilt release binaries are not included. The total includes the pre-existing quick-open commit already on the remote branch (see the note below).

| Extension | Files | Added | Removed |
|---|---|---|---|
| `.go` | 33 | 8313 | 47 |
| `.html` | 6 | 150 | 16 |
| `.css` | 2 | 546 | 0 |
| `.md` | 5 | 408 | 1 |
| `.mod`/`.sum` | 2 | 5 | 2 |
| `.yml` | 1 | 2 | 2 |
| Total | 49 | 9424 | 68 |

## Behavior and security checks

- [x] Terminal commands start only from an explicit terminal action. Workspace files are never executed on their own.
- [x] Terminal subprocesses and pipes are closed on tab, panel, and app exit.
- [x] Commands run with the user's privileges, and the docs say so.
- [x] Save writes the result; Mark Resolved or Stage stages one path; nothing commits or continues a merge automatically.
- [x] External file changes are detected before a save overwrites the working tree file.
- [x] Binary and path-level conflicts get an explicit fallback, not a guessed resolution.
- [x] No Chromium, browser, or network service dependency is added.
- [x] Workspace paths are validated before reads, writes, and Git commands.
- [x] No secrets, tokens, private paths, or user data appear in fixtures, screenshots, or logs.

## Checklist

- [x] Tests added or updated, or the reason for no test is explained
- [x] README and both guides reviewed against the final UI
- [ ] Screenshots or a recording added for the terminal and merge editor
- [x] `git diff --check` is clean
- [ ] Every test plan box is checked before merge
