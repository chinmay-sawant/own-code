# 16 — Assessment and priorities (2026-11-23)

Scope: review of the tree at `056c541`. The `replace` line in `go.mod` is ignored on purpose.

## Rating

| Lens | Score | Why |
|---|---|---|
| Reader / diff-review tool | **7.5 / 10** | Strong git surface (unified and split diff, history graph, per-hunk revert, merge resolver, blame, stage and commit). Fast, one binary, and lazy-loaded. |
| Lightweight VS Code alternative | **4 / 10** | No language intelligence, no multi-cursor, no folding, no extensions, no debugger, no remote. Editing is "good enough", not daily-driver. |
| Engineering quality | **7 / 10** | `go vet` is clean. About 790 test funcs across 140 test files. Features are planned and checked off with evidence. See "Weak spots" for what drags it down. |

## What is good
- Real feature depth for a v0.0.x: terminal with PTY, merge resolver, lazy tree, WSL handoff, workspace replace with undo, rebindable keymap, session restore.
- Test culture: e2e scroll tests, screenshot tests, bench tests. Plans record evidence per box.
- Honest README with explicit limits.

## Weak spots (ordered by impact)
1. **No language intelligence.** Highlighting and outline are hand-written per language (`syntax.go`, `outline.go`). Go to Definition is a text index (`goto_definition.go`), so it guesses on overloaded names.
2. **Editor ceiling.** The edit buffer is a full-text snapshot (`editSnapshot` in `text_edit.go`). The README limits files to 512 KB and 20,000 lines. There is no multi-cursor, folding, minimap, bracket colorization, or column select.
3. **Giant files.** `app.go` is 4.2k lines and `actions.go` is 3.3k, in one `app` package of 120+ files. Every new feature raises the cost of the next one.
4. **Repo hygiene.** About 350 MB of release binaries are committed (`.git` is 487 MB). Every release adds roughly 350 MB of history, permanently. The Windows CI workflow is `workflow_dispatch` only and pins a stale ownframe commit. There is no CI running `go test`/`go vet` on PRs.
5. **Build friction (resolved 2026-11-27).** This assessment found that builds needed sibling `ownframe` and `blinkless` checkouts plus `go.work`. The v0.0.1 release now consumes tagged modules from the Go module proxy, and the README documents a clean-clone build.
6. **README drift.** The README says ten themes, but `plans/v0.0.1/00-master-checklist.md` still says six. The README also says "no horizontal scroll", yet `hscroll.go` exists. The plans checklist (`00-master-checklist.md`) is entirely unchecked even though most of it shipped.

## Recommended order
1. `20-repo-hygiene.md` (small, unblocks everything, stops repo growth)
2. `17-lsp.md` (the biggest gap; also replaces the regex outline and heuristic goto-def)
3. `19-editor-core-gaps.md` (multi-cursor, folding, large files; LSP needs the buffer work first)
4. `18-ui-improvements.md` (can interleave anywhere)
5. `21-extensibility-and-later.md` (debugger, remote, extensions; decide after LSP lands)

## Positioning (decision needed)
"Lightweight VS Code alternative" and "reader/review tool" pull in different directions. My recommendation: **review-first, with an LSP-powered editor** for small to medium edits. Skip an extension host (it would cancel the memory advantage). Keep the ~200 MB, single-binary identity as the headline.
