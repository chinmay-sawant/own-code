# Architecture

A short map of how own-code is put together and how a frame gets from an
event to pixels. File names are accurate as of v0.0.1; grep them for detail.

## Layout

| Path | Role |
|------|------|
| `cmd/own-code/main.go` | Flag parsing, WSL re-exec, detach, then builds the `App` and hands its `Page` to ownframe. |
| `assets.go` (package `owncode`) | `go:embed` of `components/html`, `components/css`, `fixtures/sample.diff`, and `testdata/sample`. |
| `components/` | The HTML fragments and CSS the UI renders. |
| `internal/app/` | The application: state, handlers, git, search, editor glue, session. Everything else is a helper for it. |
| `internal/edit/` | Text buffer and selection model (`edit_buffer.go`, `selection_model.go`). |
| `internal/merge/` | Three-way merge conflict model for the resolver. |
| `internal/terminal/`, `internal/termui/` | PTY manager, screen model, link detection and rendering for the embedded terminal. |
| `internal/wsl/` | WSL detection, distro list, path translation, and re-exec handoff on Windows. |

The UI toolkit is the module `ownframe` (and its dependency `blinkless`),
fetched through the Go module proxy at the versions `go.mod` records.

## The `App` object

`internal/app/app.go` defines `App`, which owns the workspace, open
documents, tabs and editor groups, git state, and the `*ownframe.Page`.
`New`/`NewWithLimit` builds the workspace, creates the page from the embedded
HTML (`buildHTML`), registers handlers, and calls `openInitial` and `refresh`.

## Data flow

```
 input (ownframe)                 handlers on App               publish
 click, key, wheel, type  --->  onClick / onKeyDown / onWheel   --->  refresh()
 drag, paste, menu              onType / onPaste / MenuAction        |
                                (actions.go, edit_ops.go, ...)       v
                                  mutate App state            buildView() -> View
                                                                     |
 tick (every frame)  --->  tickWatch (watch.go)                      v
   drains worker channels        may call refresh()          page.SetData(&view)
                                                                     |
                                              ownframe lays out the HTML template
                                              with View and paints the frame
```

1. **Input.** `page.Handle(ownframe.Handlers{...})` in `New` wires the
   ownframe callbacks (`Click`, `KeyDown`, `Wheel`, `Type`, `Paste`, `Undo`,
   drag and context-menu hooks) to `App` methods. Key dispatch lives in
   `actions.go`; the keymap catalog is in `settings.go`.
2. **State change.** A handler mutates `App` fields (tabs, scroll offsets,
   edit buffer, selection, mode) and then ends in `refresh()`.
3. **View.** `refresh()` (`app.go`) calls `buildView()`, which flattens state
   into the `View` struct (`view.go`). Every field is a plain value or a
   prebuilt row, so one `page.SetData(&a.view)` publishes one frame. It also
   calls `persistSession()` so layout state is saved as you go.
4. **Windowed rows.** `page.Scroll().SetWindowing(true)` plus
   `page.SetScrollWindow(a.onScroll)` make the editor and diff render only the
   rows around the viewport; `onScroll` slices rows and reports whether a new
   layout is needed.
   The v0.0.8 `Config.CullWindow` option stays off: the large variable-length
   collections are already window-sliced, and no app-level measurement shows
   additional benefit from layout culling. `Config.Render` also stays off.
   `buildHTML` composes embedded Go-template fragments; passing a template
   executor through `Render` would keep the same reflection work, while a
   direct writer requires replacing that renderer and proving output parity.
5. **Tick.** `page.SetTick(a.tickWatch)` runs each frame on the page loop.
   It applies watcher updates and drains the results of every background
   worker (below), then refreshes if anything changed.

Handlers end in one `refresh`. The doc comment on `App` says no background
goroutine is involved; that is out of date, since the workers below exist.

## Async workers

Anything slow runs in a goroutine or loader and reports back on a channel
that `tickWatch` drains on the page loop, so UI state is only touched from
one goroutine. A worker that finishes calls `page.WantFrameIn(0)` to wake the
loop.

| Worker | Files | Drained by |
|--------|-------|-----------|
| File watcher (fsnotify, debounced 150 ms, plus a 2 s git poll) | `watch.go` | `applyWatchUpdate` |
| Commit patch loader | `commit_loader.go`, `commits.go` | `pollPatches` |
| Blame loader | `blame.go` | `pollBlame` |
| Workspace content search | `search_ops.go`, `workspace_search.go`, `search_ripgrep.go` | `receiveSearch` |
| History paging | `history_load.go` | `receiveHistoryLoad` |
| Lazy directory loads | `dirload.go` | `receiveDirLoads` |
| Terminal PTY output | `terminal.go`, `internal/terminal` | `pollTerminal` |
| Auto-save | `autosave.go` | `pollAutoSave` |

The watcher is desktop only; `-web` does not start it. On Windows the
watcher keeps its event reader draining before any `Add` to avoid a backend
deadlock (see the comment in `watch.go`).

## Workspace and git

`workspace.go` scans the root (lazy by default: the root only, folders load
on expand; an explicit `-files N` runs a full scan up to N). `rescan.go`
reinstalls a fresh scan while preserving lazy state. Git access shells out
(`git.go`, `scm_ops.go`, `stage_commit.go`, `branches.go`, `commit_graph.go`).
With no repository the bundled sample workspace and `fixtures/sample.diff`
stand in.

## Editing

The in-place editor wraps `internal/edit` buffers (`edit_ops.go`,
`text_edit.go`, `selection_ops.go`). The `dirty` map and `View().EditDirty`
drive tab and explorer dots; `unsaved.go` implements the Save / Don't Save /
Cancel flow. Display wrapping and horizontal scrolling live in `wrap.go` and
`hscroll.go`; two editor groups (`editor_group.go`, `split_ops.go`,
`split_scroll.go`) keep independent scroll offsets.

## Persistence

All under `os.UserConfigDir()` (an `ownframe/code-view` folder):

- Per-workspace session files (`session.go`, `app_session.go`): open tabs per
  group, focus, split and history ratios, wrap, zoom, theme. Loading drops
  missing paths and clamps ratios and indexes (`session.go`).
- `settings.go`: global `settings.json` (theme, font, zoom, keymap, exclude
  globs, frame rate). Precedence is session, then settings, then built-in.
- `project_recent.go`: the recent-projects list used by `ResolveStartupRoot`.

## OpenCode Console agent

The agent runtime is local and talks to the Console directly. Nothing starts `opencode serve`.

| Package | Role |
|---------|------|
| `internal/opencode` (`console.go`, `models.go`) | Console client for the model catalog (`GET /console/api/v1/models`) and its `ConsoleModel` metadata. |
| `internal/secretstore` | The Console API key file (`opencode-console-api-key`, `0600`) under the user config directory. Kept out of settings and SQLite. |
| `internal/agent` | Provider-neutral types, `ResolveModel` (chat + tool support + chat-completions/responses + `base_url`, validated again per request), the inference client (bounded bodies, SSE frames, timeouts, key redaction, `x-opencode-session` header), and the bounded step loop (`loop.go`, default 12 steps). |
| `internal/agenttools` | Tool registry, static allow/ask/deny `Policy`, approvals bound to session/turn/call/worktree/args hash, worktree-scoped file tools, `run_shell` (process-tree kill, timeout, bounded output), and proposal-only `write_file`/`edit_file`. No dependency on `internal/app`, `internal/agent`, or storage. |
| `internal/agentreview` | Pure per-hunk review model: path mapping (`MapPath` rejects absolute, traversal, `.git`, and symlinked paths), pre-turn baseline capture, hunk diff, apply and revert that touch only agent-owned lines, and per-session/file hunk state (in memory). |
| `internal/agentbridge` | Adapts `agenttools` to the interfaces `agent` defines, adding no policy. |
| `internal/agentstore` | The only runtime database: SQLite at `<config>/agent-workbench/sessions.sqlite`, versioned migrations, a profile lock, and startup recovery. |

Provider registry: `agent.ProviderRegistry` holds `ProviderDescriptor`s (ID, display name, catalog loader, request builder, resolver). OpenCode Console is the only registered backend; the UI asks the registry rather than hard-coding a vendor, and a session stores its backend ID.

Chat panel and context: the panel is a right dock that carves the editor area (`opencode_model_menu.go` owns the dock width, the model dropdown, and the history dropdown). `opencode_mentions.go` drives the `@` popup and composer chips, built on `opencode_context*.go` (bounded snapshots with versions; sensitive, ignored, `.git`, traversal, and symlink-escaping paths are excluded). `opencode_inline.go` runs tool-less single-selection edits against an immutable snapshot and applies an accepted patch as one undoable buffer operation. `opencode_hunks.go` and `opencode_diffview.go` connect proposals to `internal/agentreview` and the existing diff views.

Loop: each step rebuilds history from SQLite, requests a model response, and surfaces only complete tool calls. The loop validates each call, applies policy, asks the approver when needed, persists the approval, writes a dispatch claim (`BeginToolDispatch`), runs the executor, and stores a redacted, bounded result. A model can request a tool but cannot approve it. Cancellation is scoped to session and turn IDs, so sessions run concurrently without sharing state. On startup, `RecoverInterrupted` marks unfinished turns interrupted and uncertain calls unknown; they are never re-run without a fresh approval.

Store: sessions, messages, message parts, turns, and tool calls are keyed by local IDs and written idempotently, with separate provider and model IDs. Text, tool input, and tool output are size-bounded and redacted for bearer tokens and API keys. Older server-backed transcripts migrate in place and stay readable.

Proposal review: write tools stage files in a private proposal directory (`agent-workbench/proposals`), never the worktree or checkout. The app (`internal/app/opencode_proposals.go`) holds pending proposals in memory and shows them in the panel for Accept or Reject. Applying rechecks the baseline, paths, symlinks, and dirty editor buffers. Inference, SQLite writes, and tool execution run off the render loop and report back through the tick-drained result channels.

## Themes

`themes.go` holds `themeCatalog()` (ten themes today); each is a row of
colors turned into CSS by `themeCSS` and applied with `page.SetTheme`
(`activeThemeCSS`). Terminal palettes derive from the same theme id
(`termPaletteFor`).

## Testing

Tests sit beside the code and drive `App` directly: build an app on
`testdata/sample`, call handlers, assert on `View()` or the rendered HTML
(`renderAppHTML` in `hscroll_test.go`). `main_test.go` redirects
`os.UserConfigDir` so tests never touch real user state. Run them serially,
`go test -p 1 ./...`, under `xvfb-run -a` on headless Linux.
