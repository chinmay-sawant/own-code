# Changelog

All notable changes are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.0.1] - 2026-11-27

First release of own-code. The release checklist records the checks run for
this cut and any remaining manual checks; the older phase plan is retained as
a historical planning draft, not as a list of release blockers.

### Added
- LSP support: diagnostics, hover, completion, definition/declaration/type
  definition, references, rename, document and workspace symbols, formatting,
  signature help, code actions, snippets, and progress reporting.
- Diff hunk context menu: Unstage Hunk. Welcome card lists six core
  shortcuts.
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
- The MIT license.

### Changed
- Upgrade `ownframe` to `v0.0.8`, which pins Blinkless `v0.1.10`.
- Install scripts download release assets, verify `SHA256SUMS`, and replace
  the installed binary only after verification succeeds.
- Source builds use tagged Go modules and no longer need sibling checkouts.
- README: corrected the horizontal-scroll limit (scrolling works when wrap is
  off).

### Known limitations
- Release binaries target Linux, macOS, and Windows on amd64 and arm64; 32-bit
  builds are not published. Older binaries remain in Git history.
- Language-server features require the corresponding server to be installed
  and available on `PATH`.
