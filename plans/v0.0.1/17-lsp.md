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
- [x] `didOpen` / `didChange` (full sync first) / `didSave` / `didClose` from tabs. Debounce changes (about 150 ms). Evidence: `internal/app/lsp_sync.go`, `lsp_sync_test.go` (fake server over `io.Pipe`).
- [x] Settings schema, registry, "LSP: Show log" palette command. Evidence: `internal/app/lsp_settings.go`, `lsp_log.go`, `lsp_settings_test.go`. Not done: a toggle in the Settings panel UI (edit `settings.json` `lsp` block).

**Phase 2: read-only intelligence**
- [x] Diagnostics: squiggle spans in the editor, gutter marker, status-bar counts, and a Problems list in the Details drawer (click to jump). Clear on close. Evidence: `internal/app/lsp_diag.go`, `lsp_sync_test.go`. Problems list shows the first 8 entries.
- [x] Hover popup on mouse dwell (about 400 ms), rendered with the popup widget's Markdown card. Evidence: `internal/lsp/hover.go`, `internal/app/lsp_hover.go`, `lsp_hover_test.go` (fake server, UTF-16 position with a non-BMP rune, stale drop, dismissal rules, gopls-gated test). Not done: the `Ctrl+K Ctrl+I` keyboard chord.
- [x] Server registry defaults for web languages: HTML, CSS/SCSS/Less, JSON/JSONC (`initializationOptions: {provideFormatter: true}`), Markdown (`marksman server`), YAML; per-server `initializationOptions` in settings. Evidence: `internal/lsp/registry.go`, `registry_test.go`, `internal/app/lsp_settings.go`.
- [x] Go to Definition / Declaration / Type Definition via LSP; **fall back to the current text-index path** when no server or no result (keep `goto_definition.go` as the fallback; do not delete). Evidence: `internal/lsp/definition.go`, `definition_test.go`, `internal/app/lsp_definition.go`, `lsp_definition_test.go`, `goto_definition.go`, `explorer_ops.go` (`textDocument/definition`, `declaration`, `typeDefinition`, `LocationLink`, UTF-16 col conversion, external-file jump, stale-reply drop, automatic fallback to text index).
- [x] Find References as a results panel (reuse the workspace-search result list UI). Evidence: `internal/app/lsp_refs.go`, `lsp_features_test.go` (`Shift+F12`, palette `LSP: Find References`; hits install into the search panel).
- [x] Document symbols replace `outline.go` when a server is running; regex outline remains the fallback. Evidence: `internal/app/lsp_symbols.go`, `lsp_features_test.go` (drawer outline and `Ctrl+Shift+O`; fallback on error, empty reply, or stale version).
- [x] Workspace symbols behind `Ctrl+T`. Evidence: `lsp_symbols.go` (`workspace/symbol`, debounced; falls back to the local symbol index with no server).

**Phase 3: editing intelligence**
- [x] Completion popup: trigger chars + `Ctrl+Space`, client-side filtering while typing, basic snippets (`$1`/`${1:x}`/`$0`, caret at first stop), `textEdit`/`additionalTextEdits`. Evidence: `internal/lsp/features.go`, `internal/app/lsp_completion.go`, `lsp_features_test.go`, gated screenshot `TestScreenshotLSPFeatures`. Commit characters (item `commitCharacters`, else server `allCommitCharacters`) accept the selected item as you type; the first placeholder is selected and `Tab` cycles the remaining stops (`lsp_snippet.go`, `lsp_extras_test.go`). `completionItem/resolve` has a client method (`Client.ResolveCompletion`) but is not wired into accept.
- [x] Signature help: popup card on `(`, `,` and server trigger characters, closed by `)`/`Esc`; palette `LSP: Signature Help`. Evidence: `internal/app/lsp_signature.go`, `internal/lsp/extras.go`, `lsp_extras_test.go`.
- [x] Rename symbol with a preview of `WorkspaceEdit` (shared confirm dialog). Evidence: `lsp_refs.go`, `internal/lsp/edits.go`, tests. `F2` or palette. The open buffer changes in place (one `Ctrl+Z`); other files are written to disk and undone with the existing Undo replace. Refuses dirty files, files outside the workspace, and resource operations. `textDocument/prepareRename` runs first when the server advertises it: a refusal shows a toast, a placeholder pre-fills the name.
- [ ] Format document / selection; format on save (opt-in).
- [x] Code actions: `Ctrl+.` or palette `LSP: Code Actions (Quick Fix)` lists `textDocument/codeAction` results (with the caret line's diagnostics); the edit goes through the rename confirm dialog, commands run via `workspace/executeCommand`, lazy edits via `codeAction/resolve`. Evidence: `internal/app/lsp_codeaction.go`, `lsp_extras_test.go`. Not done: the gutter lightbulb; no `workspace/applyEdit` server requests.

**Phase 4: polish**
- [ ] Incremental sync (needs range-based edits from the buffer; see `19`).
- [ ] Semantic tokens to improve highlighting where available.
- [ ] Inlay hints (opt-in), folding ranges from the server (feeds folding in `19`).
- [x] Progress (`$/progress`) in the status bar: "gopls: Indexing 42%". Evidence: `internal/app/lsp_progress.go`, `lsp_features_test.go`.

## Risks and decisions
- **Threading:** all server I/O off the UI loop. Results are delivered via the app's existing async/"stale-drop" pattern used by history and search.
- **Large and lazy workspaces:** pass the workspace root, not a file list. Do not `didOpen` unopened files. Honor the existing 512 KB cap for sync (skip LSP for larger files).
- **Memory goal:** servers are separate processes and cost memory. The README memory comparison must say "own-code process only" or list server cost. Do not quietly claim ~200 MB with gopls running.
- **WSL:** the app re-execs inside WSL, so spawn servers inside the same environment.
- **Security:** only run servers from the registry or user settings, never from workspace files (untrusted repos must not trigger execution). Add a workspace-trust prompt before honoring a per-workspace override.
- Tree-sitter would improve highlighting but needs cgo or a pure-Go port. It conflicts with the single static binary goal. Defer; semantic tokens cover most of the value.

## Proof
`go test -p 1 ./internal/lsp/...`, a gopls integration test gated on `gopls` being on `PATH`, and a manual pass on `testdata/sample`.
