# 18 — UI improvements

Grouped by effort. Items marked (LSP) are prerequisites for `17-lsp.md`.

## Foundations (do first)
- [x] **Popup/overlay widget**: anchored to the caret, keyboard navigable, clipped to the window. Needed by completion, hover, signature help, code actions (LSP).
- [x] **Command palette**: show keybindings next to commands, recent commands on top, fuzzy match with highlighted characters.
- [x] **Problems panel** and status-bar diagnostic counts (LSP). Evidence: `components/html/problems.html`, `internal/app/lsp_diag.go`, `lsp_sync_test.go` (workspace Problems in resizable bottom panel shared with terminal, cached counts, click-to-jump).
- [ ] **Toast/notification center**: keep history; long-running tasks (indexing, search, replace) show progress with cancel.

## Editor surface
- [ ] Minimap (optional toggle, off by default for the memory story). Render from cached line density, not full tokens.
- [ ] Code folding with gutter chevrons (indent-based first, LSP ranges later). Not started: the code view windows rows by source line index (`windowSlice*`, `visualRowAt`, `sourceLineAt`, wrap plan, sticky scroll, hover, caret visibility), so hiding rows needs a visible-line map threaded through all of them first. Plan: add a `foldMap` (visible row -> source line) on `fileDoc`, route those helpers through it, then add chevrons and fold/unfold commands.
- [x] Indent guides (one per indent level on leading spaces, both editor groups) and matching-bracket highlight at the caret while editing (`bracket_match.go`, `.indent-guide` / `.bracket-match` in `base.css`). Bracket-pair colorization and current-line highlight remain open.
- [ ] Whitespace rendering toggle (Phase 5 of the master checklist lists it; verify it shipped).
- [ ] Sticky scroll exists (`15-sticky-scroll.md`); extend it to Markdown headings.
- [ ] Breadcrumb dropdowns: click a symbol segment to list siblings.
- [ ] Tab context menu: Close Others / Close to Right / Copy Path / Reveal / Pin. Drag to reorder and drag between groups.
- [ ] Vertical split and grid layouts (today: one second group; scroll shared per limits).

## Explorer and Source Control
- [ ] Drag and drop move/copy in the explorer; multi-select; inline rename (F2) and new file/folder inline row.
- [ ] Outline and Timeline (file history) sections in the sidebar.
- [ ] SCM (partial: stage/unstage hunk and stash list/apply/push shipped via the palette; lines, amend, branch create/delete, discard confirm, graph filter remain): stage/unstage hunks and lines (stage_commit exists at file level), stash, amend, branch create/delete, discard confirmation, and a graph filter by author/path.
- [ ] Inline blame text at end of line (gutter blame exists).
- [ ] Open the diff of any two files / any two commits / HEAD vs working tree from the palette. (Two commits/branches shipped as `>compare a b`; files and HEAD vs worktree remain.)

## Accessibility and ergonomics
- [ ] High-contrast theme; honor OS reduced-motion for `motion.css`.
- [ ] Screen-reader labels already have a test (`accessibility_labels_test.go`); extend it to new popups.
- [ ] Focus rings on all interactive rows; verify every dialog traps Tab (the README says overlays do; add a test per new overlay).
- [ ] Keymap import (VS Code `keybindings.json`) to lower switching cost.
- [ ] First-run welcome: show 6 core shortcuts and the "Open Folder" flow; link the in-app Settings.
- [ ] Themes: ten exist; add a "match OS light/dark" mode and a theme preview in Settings.

## Visual polish
- [ ] Consistent icon set (file-type icons in explorer and tabs, not only language badges).
- [ ] Unified spacing/size tokens across `components/css/*.css` (they are separate files with likely duplicated values; extract shared variables in `base.css`).
- [ ] Smooth scroll and animation budget documented in `motion.css`; verify idle CPU stays ~0.
