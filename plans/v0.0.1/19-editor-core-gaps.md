# 19 — Editor core gaps

These sit under the LSP work; several are prerequisites.

## Buffer model (do before incremental LSP sync)
- [ ] Replace snapshot-undo (`editSnapshot` stores full text) with an operation/edit log: `Edit{rangeStart, rangeEnd, text}`. Gains: cheap undo for big files, incremental `didChange`, multi-cursor-safe edits.
- [ ] Choose the data structure: piece table or rope for files over about 1 MB. Keep the current `[]string` lines path for small files if it benchmarks better (`perf_editor_bench_test.go` is the gate).
- [ ] Raise or remove the 512 KB / 20,000-line caps once the above lands. Keep a read-only "large file mode" (no highlighting, no LSP) above a new, documented cap (say 50 MB).
- [ ] Add `internal/edit` property tests: random edit sequences, undo/redo round-trip, selection mapping.

## Features
- [ ] Multi-cursor: `Ctrl+Alt+Up/Down`, `Alt+Click`, `Ctrl+D` is taken by go-to-definition, so use `Ctrl+Shift+L` "select all occurrences" and a rebindable "add next occurrence".
- [ ] Column (box) selection.
- [ ] Move line up/down, duplicate line, delete line, join lines, transpose.
- [ ] Auto-close brackets/quotes and surround-selection.
- [ ] Word-wise movement/deletion (`Ctrl+Left/Right/Backspace`) if not complete; verify against a test matrix.
- [ ] Emmet and snippets (user-defined JSON snippets first).
- [ ] Encoding and EOL: detect UTF-8 BOM/UTF-16/Latin-1, show in status bar, convert. Preserve original EOL on save (workspace replace already preserves CRLF).
- [ ] `.editorconfig` support (tab width, indent style, trim trailing whitespace, final newline).
- [ ] Format-on-save hook for external formatters (`gofmt`, `prettier`) before LSP formatting exists.
- [ ] Per-file undo history survives tab switch (verify) and optionally restarts (session).

## Highlighting
- [ ] Add languages: Rust, C/C++, Java, C#, Ruby, PHP, SQL, TOML, Dockerfile, Makefile, Lua. Keep each lexer small and table-driven so `syntax.go` (853 lines) stops growing; split into `internal/syntax/<lang>.go` with a shared tokenizer test harness.
- [ ] Incremental re-highlight (only changed lines plus a carry state) for edit latency on large files.
