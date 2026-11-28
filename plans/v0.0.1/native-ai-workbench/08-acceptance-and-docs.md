# End-to-end acceptance and documentation

**Goal:** Verify the complete native OpenCode Console workflow and document its setup, credential handling, and limits.

## Phase 1: End-to-end workflow

- [ ] Verify a fresh setup accepts a workspace-bound `oc_sk_` key, stores it outside settings JSON and SQLite, and fetches the catalog from `GET https://opencode.ai/console/api/v1/models`.
- [ ] Select a model from the returned provider/model catalog and send a request to the protocol endpoint advertised by that model's API metadata.
- [ ] Create an own-code local session, send a prompt with explicitly selected file or selection context, and verify the context preview matches the inference request.
- [ ] Run the own-code agent loop for a tool-capable model. Verify tool calls are validated, permission-gated, executed by own-code, and represented in the local transcript.
- [ ] Stream a response and tool activity into the native panel. Cancel a run and recover from an inference disconnect without losing or duplicating local transcript entries.
- [ ] Ask for an inline edit, inspect the proposal, accept one change, reject another, and undo an accepted change.
- [ ] Ask the agent to edit multiple files. Review changes in the existing diff view and accept or reject individual hunks.
- [ ] Restart own-code and restore local sessions, transcripts, tool/run state, and pending review state without restoring any credential into own-code settings, SQLite, or a workspace.
- [ ] Verify user changes staged or edited before a turn survive session creation, rejection, acceptance, interruption, and restart.
- [ ] Verify workspace path boundaries, tool permission decisions, and sensitive-context exclusions with adversarial request and file fixtures.

**Gate:** Record the Console key type and workspace scope, selected provider/model/protocol, commands run, and manual UI sequence. Document unsupported model protocols and relevant Console inference restrictions. Do not use an OpenCode Server process or remote Server session as an acceptance dependency.

## Phase 2: Repository gates and docs

- [ ] Add focused fake-HTTP, storage, editor, agent-loop, tool-permission, and UI checks with each implementation slice. Keep platform PTY checks separate from API protocol checks.
- [ ] Run the repository's current serial validation gate, vet, formatting, and build checks after implementation is complete.
- [ ] Update `README.md` with Console key setup, where the key is stored, local session-history location, direct model inference, local tool permissions, privacy/context controls, and known limits.
- [ ] Update `docs/architecture.md` with the Console catalog/inference client, local SQLite store, own-code agent/tool loop, and edit-review boundaries.
- [ ] Reconcile issue #9's acceptance list against the shipped UI. Leave unimplemented prompt, local agent loop, tool permission, review, and end-to-end requirements open.

**Gate:** The documented setup works from a clean profile. The checklist status matches implemented behavior and recorded verification.
