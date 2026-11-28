# Terminal and quick actions

**Goal:** Connect agent sessions to the embedded PTY and make common agent actions easy to start.

## Phase 1: PTY integration

- [x] Decide whether an agent session shares a user terminal or starts a dedicated PTY. Show the relationship in the UI. **Decision: neither.** Agent sessions own no PTY. Approved agent shell commands run as child processes in the private worktree; the user terminal (`internal/terminal` manager) is user-owned and never written to by the agent. The panel footer says "Agent shell commands ask for approval and run in a private worktree, not in your terminal."
- [ ] Reuse `internal/terminal/manager.go` where it fits. Keep process ownership and close behavior explicit.
- [ ] Preserve terminal focus, keyboard ownership, resize, and scroll behavior while the agent panel is open.
- [x] Make terminal `file:line` links open only validated files inside the active workspace. (`termLinkInsideWorkspace` in `terminal_links.go`: symlink-resolved, regular file, under the root; test `TestOCTermLinkOnlyInsideWorkspace`. The manual click pass is still outstanding.)
- [x] Define what happens to PTYs when the user switches session, workspace, or closes own-code. Switching an agent session leaves terminals untouched (they are not tied to sessions). Switching workspace calls `closeTerminalSessions` in `switchProject`. Closing own-code calls `App.Close`, which closes all PTYs. This is documented from the code; no lifecycle tests were added or run.

**Gate:** Lifecycle checks cover new, switch, exit, close, and app shutdown. A manual pass clicks a real `file:line` link from the agent terminal.

## Phase 2: Palette and keybindings

- [x] Add command palette actions to start an agent task, open a saved session, explain a selection, explain or fix a Problem, review pending changes, and interrupt a run. (`openCodeActionRows` in `opencode_actions.go`, dispatched from `runExtraPaletteCommand` to `runOpenCodeAction`; test `TestOCActionNoticesAndPaletteRows`. Start Task creates a session when connected and none is selected, Open Saved Session opens the history menu, Interrupt reuses `interruptOpenCodeSession`.)
- [x] (Update: `agent-chat`, default Ctrl+Shift+L, is now in `keymapCatalog` as rebindable, with a home branch in `onKeyDown` placed after `handleTerminalKey` so a focused terminal swallows the chord first, and a `runKeymapAction` case so rebinding works. It opens or closes the chat and focuses the composer. `TestAgentChatChordCataloguedAndUnique`; not exercised in a live window. Earlier note follows.) Add keybindings only after checking the existing `keymapCatalog` and terminal precedence. Keep new bindings rebindable where possible. Checked: catalog entries need a non-empty default chord plus an `onKeyDown` home branch, and the focused terminal takes most chords before the dispatcher. No default chord was safe on both counts, so none was added; the actions are palette-only for now.
- [x] Route each action through the same session, context, and review contracts as the visible controls. (Chips use `addOpenCodeChip`, the `composeOpenCodeUserText` stale checks, and the normal Send and proposal review path.)
- [x] Show clear missing-key, no-available-model, no-session, permission-needed, and offline states. (`openCodeActionNotice` sets the panel status after each action; offline and no-model text comes from `openCodeRequestNotice`. Only the offline branch is tested.)

**Implementation note:** The palette has `OpenCode: Open Session Panel` and `OpenCode: New Blank Session` entry points. Direct Console model selection, local prompt submission, and the own-code agent loop are not wired through these commands yet. Prompt and interrupt commands remain open while local tool permissions and edit review are incomplete. This covers only two palette entry points; the remaining actions and the phase gate are still pending, so all checklist rows remain open. No tests were run for this partial change.

**Gate:** Palette filtering, dispatch, keymap conflicts, terminal focus, and empty-state tests pass. Each command produces the intended session action without bypassing the review flow.
