# 21 — Extensibility and later

Decisions, not commitments.

| Feature | Recommendation | Notes |
|---|---|---|
| Extension host / VS Code extensions | **No** | It would erase the memory and single-binary story. Offer LSP, formatter hooks, and user snippets instead. |
| Debugger (DAP) | Later, after LSP | Same JSON-RPC transport (`internal/lsp/transport.go` is reusable). Start with Go (delve) and Python (debugpy); breakpoints gutter, variables, call stack. |
| Tasks (`tasks.json`-style) | Soon | A "Run Task" palette entry that runs a command in the terminal. Cheap, because the terminal exists. Auto-detect `Makefile`, `package.json` scripts, `go test`. |
| Git extras | Soon | Stash, rebase-continue UI, submodule awareness, `.gitattributes` aware diffs. Pull/push/fetch with the user's credential helper. |
| Remote (SSH) | Later | Largest effort. WSL handoff already exists; SSH would run a headless own-code server remotely (`-web` mode is a head start). |
| Settings sync, accounts | No | Explicitly out of scope per the README positioning. |
| Notebook / Jupyter | No | |
| Integrated browser preview | Maybe | `-web` mode exists; HTML preview pane could reuse it. |
| Telemetry | Off by default if ever added | |
| Plugin API (Go plugins/WASM) | Revisit after v0.1 | WASM (e.g. wazero) keeps the static binary. Only if there is real demand. |
