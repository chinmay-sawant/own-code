# Terminal and quick actions

**Goal:** Connect agent sessions to the embedded PTY and make common agent actions easy to start.

## Phase 1: PTY integration

- [ ] Decide whether an agent session shares a user terminal or starts a dedicated PTY. Show the relationship in the UI.
- [ ] Reuse `internal/terminal/manager.go` where it fits. Keep process ownership and close behavior explicit.
- [ ] Preserve terminal focus, keyboard ownership, resize, and scroll behavior while the agent panel is open.
- [ ] Make terminal `file:line` links open only validated files inside the active workspace.
- [ ] Define what happens to PTYs when the user switches session, workspace, or closes own-code.

**Gate:** Lifecycle checks cover new, switch, exit, close, and app shutdown. A manual pass clicks a real `file:line` link from the agent terminal.

## Phase 2: Palette and keybindings

- [ ] Add command palette actions to start an agent task, open a saved session, explain a selection, explain or fix a Problem, review pending changes, and interrupt a run.
- [ ] Add keybindings only after checking the existing `keymapCatalog` and terminal precedence. Keep new bindings rebindable where possible.
- [ ] Route each action through the same session, context, and review contracts as the visible controls.
- [ ] Show clear no-server, no-provider, no-session, permission-needed, and offline states.

**Implementation note:** The palette now has `OpenCode: Open Session Panel`, which opens the existing session panel, and `OpenCode: New Blank Session`, which opens the panel and requests an empty session through the existing connected-server guard. Prompt and interrupt commands were not added while isolated edit review remains incomplete. This covers only two palette entry points; the remaining actions and the phase gate are still pending, so all checklist rows remain open. No tests were run for this partial change.

**Gate:** Palette filtering, dispatch, keymap conflicts, terminal focus, and empty-state tests pass. Each command produces the intended session action without bypassing the review flow.
