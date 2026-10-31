## Summary

<!-- What user-visible problem does this PR solve? Keep it to a short paragraph. -->

## Why

<!-- Link the issue and describe the workflow this changes. -->

## Changes

### App and backend

<!-- Session management, Git operations, process handling, data model. -->

### UI and components

<!-- Panels, editor, Source Control, context menus, keys, themes, accessibility. -->

### Docs and fixtures

<!-- New docs, README links, test fixtures. -->

## Impact

| Area | Impact | Notes |
|---|---|---|
| Editor and diff | None / Extended / Changed | |
| Source Control | None / Extended / Changed | |
| Terminal | None / New / Changed | |
| Keys and focus | None / Extended / Changed | |
| Platform support | None / Changed | Linux, macOS, Windows, WSL |
| Build and dependencies | None / Changed | |
| Docs | None / New / Changed | |

## Screenshots or recordings

<!-- Desktop app only. Show the affected panel at 1500x900 and at the 840x700 minimum window. Include light and dark themes when colors or layout changed. -->

| Before | After |
|---|---|
| | |

## Test plan

- [ ] `go test -p 1 ./...`
- [ ] `go build ./cmd/own-code`
- [ ] `git diff --check`
- [ ] Manual check at the default window size and the 840x700 minimum
- [ ] Keyboard path checked: Tab order, Esc, focus return, and no trapped focus
- [ ] Screenshots checked in light and dark themes when colors changed
- [ ] Terminal: open, hide, resize, run an interactive command, copy and paste, close a tab, quit the app
- [ ] Merge: conflict badges, accept actions, compare, previous and next, manual edit, save, explicit stage, refresh
- [ ] Windows pipe fallback and WSL path checked when terminal or path code changed
- [ ] External file change and unsaved edit behavior checked when an editor or Git write path changed

## Related issues

Use `Closes #123` only when this PR fully resolves the issue. Use `Relates #123` for partial work or follow-ups. Do not close an issue that still needs platform or documentation work.

Closes #
Relates #

## Behavior and security checks

- [ ] Git writes happen only after an explicit user action. No automatic stage, commit, merge continue, or rebase continue.
- [ ] Terminal commands start only from an explicit terminal action. Workspace files are never executed on their own.
- [ ] Terminal subprocesses and pipes are closed on tab, panel, and app exit.
- [ ] Workspace paths are validated before reads, writes, and Git operations.
- [ ] No new runtime dependency on Chromium, a browser, or a network service.
- [ ] No secrets, tokens, private paths, or user data in code, fixtures, screenshots, or logs.
- [ ] Existing editor, diff, search, history, and Source Control behavior is unchanged unless described above.

## Breaking changes

None. <!-- Or describe the change and migration path. -->

## Checklist

- [ ] Tests added or updated, or the reason for no test is explained
- [ ] README, `docs/`, or both updated for user-facing changes
- [ ] New strings are clear and use sentence case
- [ ] Screenshots use the real app, not a mockup
- [ ] Release binaries and vendored changes are intentional
- [ ] `git diff --check` is clean
