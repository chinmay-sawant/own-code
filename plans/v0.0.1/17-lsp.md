# 17 — Language Server Protocol client

**Goal:** diagnostics, hover, go to definition, completion, references, rename, signature help, document symbols, and formatting, for the languages the user already has servers for. No bundled servers; we spawn what is on `PATH`.

**Why now:** this is the largest gap versus VS Code. It also removes two hand-maintained pieces: the regex outline (`outline.go`) and the text-index definition lookup (`goto_definition.go`).

## Design
- New package `internal/lsp/` (no UI imports):
  - `transport.go`: stdio JSON-RPC 2.0 with `Content-Length` framing, request ids, cancellation (`$/cancelRequest`), notifications, and a reader goroutine.
  - `client.go`: lifecycle (`initialize`, `initialized`, `shutdown`, `exit`), capability negotiation, restart with backoff, stderr to the log.
  - `session.go`: one server per (language, workspace root). Lazy start on the first file of that language. Idle shutdown.
  - `position.go`: **rune offset <-> UTF-16 line/character conversion**. The edit buffer is rune-based; LSP is UTF-16 by default. Request `positionEncoding: utf-8/utf-32` when the server supports it. Property-test this one thoroughly.
  - `types.go`: hand-written subset of the protocol structs (avoid a huge generated dependency).
- Server registry in `settings.json`, with defaults:
  `gopls`, `typescript-language-server --stdio`, `pyright-langserver --stdio`, `rust-analyzer`, `clangd`, `vscode-json-languageserver`, `yaml-language-server`. User can override command, args, and init options per language. Missing binary gives a one-time toast with an install hint, never an error loop.
- `internal/app/lsp_*.go` glue, kept out of `app.go`: document sync, result routing back to the UI thread, stale-response drop (compare document version).

## Phases
**Phase 1: plumbing (no UI)**
- [x] Transport + client + fake-server tests (in-process pipe server). Evidence: `internal/lsp/{transport,client,registry}.go`, `client_test.go`; `go test -race ./internal/lsp/...` passes.
- [x] UTF-16 conversion with fuzz/property tests, including emoji and CRLF. Evidence: `internal/lsp/position.go`, `position_test.go` (round-trip property test, `FuzzPosition` ran 5s clean).
- [ ] `didOpen` / `didChange` (full sync first) / `didSave` / `didClose` from tabs. Debounce changes (about 150 ms).
- [ ] Settings schema, registry, "LSP: Show log" palette command.

**Phase 2: read-only intelligence**
- [ ] Diagnostics: squiggle spans in the editor, gutter marker, status-bar counts, and a Problems list in the Details drawer (click to jump). Clear on close.
- [ ] Hover popup (mouse dwell + `Ctrl+K Ctrl+I` equivalent). Render Markdown via existing `markdown_html.go`.
- [ ] Go to Definition / Declaration / Type Definition via LSP; **fall back to the current text-index path** when no server or no result (keep `goto_definition.go` as the fallback; do not delete).
- [ ] Find References as a results panel (reuse the workspace-search result list UI).
- [ ] Document symbols replace `outline.go` when a server is running; regex outline remains the fallback.
- [ ] Workspace symbols behind `Ctrl+T` (already mentioned in goto-def toasts).

**Phase 3: editing intelligence**
- [ ] Completion popup: trigger chars + `Ctrl+Space`, filter client-side as the user types, commit chars, snippet placeholders (basic `$1`/`${1:x}` only first). Needs a popup widget (see `18-ui-improvements.md`).
- [ ] Signature help.
- [ ] Rename symbol with a preview of `WorkspaceEdit` (reuse workspace-replace confirm UI; one undo for the whole edit).
- [ ] Format document / selection; format on save (opt-in).
- [ ] Code actions (quick fix lightbulb): apply `WorkspaceEdit`, run commands.

**Phase 4: polish**
- [ ] Incremental sync (needs range-based edits from the buffer; see `19`).
- [ ] Semantic tokens to improve highlighting where available.
- [ ] Inlay hints (opt-in), folding ranges from the server (feeds folding in `19`).
- [ ] Progress (`$/progress`) in the status bar: "gopls: indexing".

## Risks and decisions
- **Threading:** all server I/O off the UI loop. Results are delivered via the app's existing async/"stale-drop" pattern used by history and search.
- **Large and lazy workspaces:** pass the workspace root, not a file list. Do not `didOpen` unopened files. Honor the existing 512 KB cap for sync (skip LSP for larger files).
- **Memory goal:** servers are separate processes and cost memory. The README memory comparison must say "own-code process only" or list server cost. Do not quietly claim ~200 MB with gopls running.
- **WSL:** the app re-execs inside WSL, so spawn servers inside the same environment.
- **Security:** only run servers from the registry or user settings, never from workspace files (untrusted repos must not trigger execution). Add a workspace-trust prompt before honoring a per-workspace override.
- Tree-sitter would improve highlighting but needs cgo or a pure-Go port. It conflicts with the single static binary goal. Defer; semantic tokens cover most of the value.

## Proof
`go test -p 1 ./internal/lsp/...`, a gopls integration test gated on `gopls` being on `PATH`, and a manual pass on `testdata/sample`.
