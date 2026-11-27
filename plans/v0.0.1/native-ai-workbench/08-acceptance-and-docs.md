# End-to-end acceptance and documentation

**Goal:** Verify the complete native OpenCode workflow and document its supported setup and limits.

## Phase 1: End-to-end workflow

- [ ] Verify a fresh setup can detect or launch OpenCode, connect a model provider, create a session, and save its local history to SQLite.
- [ ] Send a prompt with explicitly selected file or selection context. Verify the context preview matches the request.
- [ ] Stream a response and tool activity into the native panel. Interrupt a run and recover after a server disconnect.
- [ ] Ask for an inline edit, inspect the proposal, accept one change, reject another, and undo an accepted change.
- [ ] Ask OpenCode to edit multiple files. Review the changes in the existing diff view and accept or reject individual hunks.
- [ ] Restart own-code and restore the session list, transcript, and pending review state without restoring any credential into own-code.
- [ ] Verify user changes staged or edited before the agent turn survive session creation, rejection, acceptance, interruption, and restart.
- [ ] Verify workspace path boundaries and sensitive-context exclusions with adversarial event and file fixtures.

**Gate:** Record the OpenCode version, server mode, provider auth method, commands run, and manual UI sequence. Document any unsupported API route or experimental dependency.

## Phase 2: Repository gates and docs

- [ ] Add focused fake-server, storage, editor, and UI tests with each implementation slice. Keep platform PTY checks separate from server protocol tests.
- [ ] Run the repository's current serial test gate, vet, formatting, and build checks after implementation is complete.
- [ ] Update `README.md` with setup, session history location, provider-key ownership, server password behavior, privacy/context controls, and known limits.
- [ ] Update `docs/architecture.md` with the OpenCode client, SQLite store, event delivery, and edit-review boundaries.
- [ ] Record the supported OpenCode version and whether the implementation relies on experimental session/event routes.
- [ ] Reconcile issue #9's acceptance list against the shipped UI. Leave unimplemented issue requirements open.

**Gate:** The documented setup works from a clean profile. The checklist status matches the implemented behavior and recorded verification.
