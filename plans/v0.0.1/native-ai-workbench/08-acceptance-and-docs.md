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
- [x] Verify workspace path boundaries, tool permission decisions, and sensitive-context exclusions with adversarial request and file fixtures. Evidence (pure-function and temp-dir fixtures, no network): `internal/agenttools/adversarial_test.go` (`TestAdversarialReadPathsRejected`, `...SymlinkEscapeBlockedForReadTools`, `...SearchSkipsGitDirectory`, `...WriteToolsRefuseEveryNonGrant` with deny/dismiss/timeout/cancel/stale-args/wrong-root/no-binding/forged approvals and a check that nothing is staged and the checkout is unchanged, `...ProposalPathsRejected`); `internal/agentreview/adversarial_test.go` (`TestAdversarialMapPathRejects`, `...MapPathSymlinks` incl. dangling and nested links, `...BaselineIgnoresEscapingPaths`); `internal/app/opencode_adversarial_test.go` (`TestAdversarialSensitivePathClassifier`, `...CollectSelectedFilesExcludesUnsafe` for `.env`, keys, credentials, ignored, `.git`, traversal, absolute and symlinked paths, `...CaptureContextRejectsSensitiveAndEscapes`, `...MentionCandidatesHideUnsafeEntries`). Gap now closed: `internal/sensitive` is the single classifier (also used by `@` context and `captureAgentContext`); `read_file` refuses sensitive or git-ignored paths with `ErrSensitive`, and `list_files`/`search_files` skip them (`internal/agenttools/sensitive_test.go`: `TestSensitiveReadRefused`, `TestSensitiveListSearchSkip` over `.env`, `.env.local`, `id_rsa`, `*.pem`, `credentials.json`, `.npmrc`, `secret*`, ignored files and dirs). `run_shell` is not filtered (it is approval-gated per command).

**Status of the unticked rows above: needs manual run.** Each needs a real Console key and a live window (rows 1 to 2, 5, 8) or a manual UI sequence (rows 3, 4, 6, 7, 9). Fake-HTTP and fake-provider tests cover parts of rows 3, 4, 6, 7 and 9 (see the Phase 2 evidence list), but they do not replace the live run. Follow the [manual acceptance script](#manual-acceptance-needs-manual-run) and tick a row only with its recorded result.

**Gate:** Record the Console key type and workspace scope, selected provider/model/protocol, commands run, and manual UI sequence. Document unsupported model protocols and relevant Console inference restrictions. Do not use an OpenCode Server process or remote Server session as an acceptance dependency.

## Phase 2: Repository gates and docs

- [x] Add focused fake-HTTP, storage, editor, agent-loop, tool-permission, and UI checks with each implementation slice. Keep platform PTY checks separate from API protocol checks. Evidence: fake-HTTP and loop tests in `internal/agent` (`agent_test.go`, `loop_test.go`) and `internal/opencode/console_test.go`; storage in `internal/agentstore/*_test.go`; tool permission in `internal/agenttools/*_test.go`; editor/UI/e2e in `internal/app/opencode_*_test.go` (for example `opencode_agent_e2e_test.go`, `opencode_gate_test.go`, `opencode_inline_test.go`, `opencode_hunks_test.go`). PTY checks live in `internal/terminal` and are not mixed into API tests.
- [ ] Run the repository's current serial validation gate, vet, formatting, and build checks after implementation is complete. **Partly run, left open:** build, vet and formatting were run and the named tests below passed, but the full serial test gate was not run in this pass because the WSL host crashes on whole-package runs. Run the full gate on a host that can take it; commands and results are in [Validation gate record](#validation-gate-record).
- [x] Update `README.md` with Console key setup, where the key is stored, local session-history location, direct model inference, local tool permissions, privacy/context controls, and known limits.
- [x] Update `docs/architecture.md` with the Console catalog/inference client, local SQLite store, own-code agent/tool loop, and edit-review boundaries.
- [x] Reconcile issue #9's acceptance list against the shipped UI. Leave unimplemented prompt, local agent loop, tool permission, review, and end-to-end requirements open. Done in [Issue #9 reconciliation](#issue-9-reconciliation) below; the unimplemented and unverified rows stay open there and in their own checklists.

**Gate:** The documented setup works from a clean profile. The checklist status matches implemented behavior and recorded verification.

## Validation gate record

Run from the repository root. Serial only (`-p 1`); no `-race` and no whole-package runs in this pass because of the WSL crash constraint.

| Command | Result |
|---|---|
| `gofmt -l .` | Clean for everything touched here. Reported `internal/app/opencode_model_menu.go` (another slice's file, being edited concurrently) and `testdata/sample/backend/*.go` (intentional sample fixtures, not formatted). |
| `go build ./...` | OK |
| `go vet ./internal/opencode ./internal/agenttools ./internal/agentreview ./internal/app` | OK, no findings |
| `go test -count=1 -p 1 -timeout 60s -run 'TestAdversarial\|TestConsolePathHasNoOpenCodeServerRoutes\|TestAPIKeyNeverReachesDatabaseBytes\|TestMigrationFromV3PreservesLegacyTranscript' ./internal/agenttools ./internal/agentreview ./internal/agent ./internal/agentstore ./internal/app` | All five packages `ok` |

Still to run before merge (not run here): `go test -count=1 -p 1 ./...`, then the same with `-race` on a host that tolerates it, plus the repository lint gate.

Dead code removed: the legacy OpenCode Server client, process launcher and event stream (`internal/opencode/client.go`, `server_process.go`, `events.go` and their tests) had no importer outside their own package, so they were deleted; `go build ./...` and `go vet` pass without them. The unused server-session request/response types in `internal/opencode/models.go` (`CreateSessionInput`, `SessionPage`, `PromptInput`, `InboxItem`, `MessagePage`, and similar) are now unreferenced and can be pruned in a follow-up; `Session`, `SessionTime`, `LocationRef`, `IntegrationMethod`, and `ConnectKeyInput` are still used by `internal/app` and were kept.

Key-absence evidence (fake `oc_sk_` key): `TestAdversarialAPIKeyAbsentFromPersistedOutputs` saves the key through `saveOpenCodeConsoleAPIKey` under an isolated config dir, persists `settings.json` and the editor `session.json` via the app's own persist paths, renders the settings tab, serializes the panel view, formats the Console client with `%v %+v %#v %s`, then scans every file under the config dir: only `opencode-console-api-key` may contain the key. SQLite and tool history are covered by `TestAPIKeyNeverReachesDatabaseBytes`. Logs: `TestAdversarialNoLoggerInInternalSources` shows no internal package imports `log` or `log/slog`, so there is no log sink. Console-path route audit: `TestConsolePathHasNoOpenCodeServerRoutes` (agent packages) and `TestAdversarialConsolePathHasNoServerUsageInApp` (`internal/app/opencode_*.go` non-test sources).

## Issue #9 reconciliation

Source of the acceptance list: the issue #9 plan [README](README.md) goal and shared acceptance rules, plus the checklists 01 to 07. "Shipped" means implemented and covered by the evidence in that checklist; "live" means it needs a real Console key and a running window.

Shipped (code and automated evidence):

- Console key settings, private key file, catalog fetch and eligible-model selection, per-session model menu, provider registry.
- Local SQLite sessions, transcripts, tool calls, approvals, turn recovery; migration of older transcripts; offline history.
- Right-docked chat panel with session history dropdown, rename and archive, streaming output, stop and retry, tool activity and approval prompts.
- Bounded step loop with allow/ask/deny policy and approvals bound to session, turn, call, worktree, and arguments.
- `@` mentions, context chips with previews and staleness, context actions, palette commands.
- Inline edit with snapshot, undoable accept, stale refusal.
- Proposal-only writes, per-hunk accept, reject and revert in the diff views, user changes preserved.

Unimplemented or unverified (kept open):

- Live Console turn with a real key and an eligible model: not run. Needs manual run (script below).
- Restart in a live window restoring sessions and pending review state: not run live. Pending proposals and hunk state live in memory, so a restart drops them (documented in the README).
- (Closed) Sensitive-name and git-ignored exclusion for model-initiated `read_file`, `list_files` and `search_files`; see `internal/sensitive`. `run_shell` can still read such files if the user approves the command.
- 01: direct-inference and streaming rows and the transient credential-buffer row are still unchecked in that file.
- 02: keeping transcripts and credentials out of editor `session.json` has a test here, but the row in 02 is unchecked; "can this session continue with the current key and model" indicator is open.
- 03: session state label assertions, dock layout item, and streamed Markdown row are unchecked there.
- 05: not reading arbitrary files named in agent output is unchecked.
- 06: delete and rename proposals are reported unsupported rather than reviewable.
- 07: terminal manager reuse, terminal focus preservation while the panel is open, and new keybindings are unchecked.
- Supported model protocols: `chat-completions`, `responses`, `messages` (Anthropic) and `generate-content` (Gemini); models with none are hidden. Free-tier chat models accept requests only from OpenCode, so they are not offered.

## Manual acceptance (needs manual run)

Record with the run: key type (`oc_sk_`, workspace scope), selected provider, model and protocol, commands, and the UI sequence. Do not start or attach to `opencode serve`.

1. Start from a clean profile: remove `ownframe/code-view` under the user config directory, launch `go run ./cmd/own-code <repo>`.
2. Settings, OpenCode: paste a workspace `oc_sk_` key, Connect. Expect "Loaded N models". Confirm `settings.json` has only `providerId` and `modelId`, and `opencode-console-api-key` is the only file containing the key (`grep -r oc_sk_ <config dir>`).
3. Open the right dock, pick an eligible model in the footer menu, create a session.
4. Type `@`, attach the active file and the diff, check the preview and that `.env` is not offered, send "explain this".
5. Ask for a change that needs `read_file` (no prompt), then one that needs `run_shell` (approve), then another `run_shell` (deny). Check the transcript order and states.
6. Start a long answer and press Stop. Confirm the turn shows interrupted with no duplicate entries; retry.
7. Select code, run the inline edit command, accept one result and reject another, then undo the accepted one.
8. Ask for edits to two files. Review in the diff view, accept one hunk, reject another, revert the accepted hunk. Confirm your own unsaved edits and staged changes are intact.
9. During a `run_shell` approval, quit own-code. Relaunch: the session list and transcript load offline, the interrupted call is marked for inspection, and nothing re-runs without a new approval.
10. Confirm no process named `opencode` was started during the run.
