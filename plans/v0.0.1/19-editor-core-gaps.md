# 19 — Editor core gaps

These sit under the LSP work; several are prerequisites.

## Buffer model (do before incremental LSP sync)
- [x] Replace snapshot-undo with an operation/edit log. Done: undo/redo records in `internal/app/text_edit.go` (`editRecord`) hold an `edit.Edit` (resolved lazily by `Diff`/`Invert` on the next push or undo), so history costs the size of each change instead of a buffer copy; caret/anchor restore unchanged. Tests: `internal/app/undo_ops_test.go`. Incremental `didChange` is done (see below). Multi-cursor-safe edits remain with the multi-cursor item.
- [ ] Choose the data structure: piece table or rope for files over about 1 MB. Keep the current `[]string` lines path for small files if it benchmarks better (`perf_editor_bench_test.go` is the gate).
- [ ] Raise or remove the 512 KB / 20,000-line caps once the above lands. Keep a read-only "large file mode" (no highlighting, no LSP) above a new, documented cap (say 50 MB).
- [ ] Add `internal/edit` property tests: random edit sequences, undo/redo round-trip, selection mapping. Done for `Edit` diff/apply/invert and buffer text round trips; undo/redo stacks and selection mapping still live in `internal/app`.

## Features
- [ ] Multi-cursor: `Ctrl+Alt+Up/Down`, `Alt+Click`, `Ctrl+D` is taken by go-to-definition, so use `Ctrl+Shift+L` "select all occurrences" and a rebindable "add next occurrence".
- [ ] Column (box) selection.
- [x] Move line up/down, duplicate line, delete line, join lines (transpose not done).
- [x] Auto-close brackets/quotes and surround-selection.
- [ ] Word-wise movement/deletion (`Ctrl+Left/Right/Backspace`) if not complete; verify against a test matrix.
- [ ] Emmet and snippets (user-defined JSON snippets first).
- [ ] Encoding and EOL: detect UTF-8 BOM/UTF-16/Latin-1, show in status bar, convert. Preserve original EOL on save (workspace replace already preserves CRLF).
- [x] `.editorconfig` support (tab width, indent style, trim trailing whitespace on save; final newline is always written by the buffer).
- [x] Format-on-save hook for external formatters (`gofmt`, `prettier`) before LSP formatting exists. `internal/app/format_save.go`; `format` block in `settings.json` (`onSave`, per-extension `formatters`); missing tool, non-zero exit, or timeout skips silently. LSP `textDocument/formatting` not wired.
- [ ] Per-file undo history survives tab switch (verify) and optionally restarts (session).

## Highlighting
- [ ] Add languages: Rust, C/C++, Java, C#, Ruby, PHP, SQL, TOML, Dockerfile, Makefile, Lua. Keep each lexer small and table-driven so `syntax.go` (853 lines) stops growing; split into `internal/syntax/<lang>.go` with a shared tokenizer test harness.
- [ ] Incremental re-highlight (only changed lines plus a carry state) for edit latency on large files.
- [x] Incremental `textDocument/didChange`: `internal/lsp/sync.go` (`SyncKind`, UTF-16 `OffsetPosition`, `ChangeFor`) and `lsp_sync.go` send one range edit from `edit.Diff` against the last sent text when the server advertises incremental sync, else full text (the first sync after a disk-backed open is full). Tests: `internal/lsp/sync_test.go`, `TestLSPIncrementalDidChange`.
