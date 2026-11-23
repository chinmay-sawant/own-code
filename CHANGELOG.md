# Changelog

All notable changes are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased] - v0.0.1

First tagged release. The feature set below is what already exists on
`master`; it has not yet been verified end to end against the v0.0.1 plans
(`plans/v0.0.1/`).

### Added
- Command palette: fuzzy matching with highlighted characters, right-aligned
  keybindings, recently used commands pinned first, `Task: Run Task…`
  (Makefile, package.json, go, cargo) running in the integrated terminal.
- Git review commands: unstage hunk, stash push and apply, and a two-ref
  diff from the palette.
- Settings: Language servers tab with a master switch, per-server toggles
  and live status; the `lsp` block now survives settings saves.
- Explorer with lazy directory loading, filterable project browser, recent
  projects, and WSL distro handoff.
- In-place editor: save, dirty markers, unsaved-close prompt, auto-save,
  find and replace (in file and workspace), goto line, indent, comment
  toggle, sticky scroll headers, go to definition via a workspace symbol
  index, and an outline in the Details drawer.
- Tabs with single-click preview and double-click pin, plus a split second
  editor group with independent scroll.
- Unified and split diffs, git history with commit graph, async blame gutter,
  per-hunk stage and commit dialog, and a three-way merge conflict resolver.
- Markdown preview with tables and basic Mermaid diagrams; image preview.
- Embedded terminal with themed colors and clickable file:line links.
- Ten color themes, including Liquid Glass (dark and light) and Ember (dark
  and light).
- Settings: theme, font, zoom, keymap, exclude globs, desktop frame rate cap.
- Reveal-in-file-manager, detached launch (`-d`), local install script.
- CI (`ci.yml`) and a tag-triggered release workflow that publishes binaries
  and `SHA256SUMS` as GitHub Release assets.
- `CONTRIBUTING.md` and `docs/architecture.md`.

### Changed
- `build-windows.yml` now checks out `ownframe` and `blinkless` at the
  versions in `go.mod` instead of a hard-coded commit.
- README: corrected the horizontal-scroll limit (scrolling works when wrap is
  off).

### Known gaps
- The committed release binaries are removed and the install scripts download
  from GitHub Release assets with SHA-256 verification. **No release exists
  until `v0.0.1` is tagged**, so the installers fail until then. 32-bit
  builds are no longer published. Old binaries remain in git history.
- Added the MIT `LICENSE`.

### Also in this release
- Removed the `ownframe` `replace` in `go.mod` and `go.work`; the module now
  builds from a clean clone with no sibling checkouts.
- LSP groundwork: `internal/lsp` client, document sync, diagnostics
  (squiggles, gutter marks, status counts, Problems list), `lsp` settings
  block, and "LSP: Show log". Hover, completion and other features are not
  wired yet.
- Reusable popup widget for completion lists and hover cards.
