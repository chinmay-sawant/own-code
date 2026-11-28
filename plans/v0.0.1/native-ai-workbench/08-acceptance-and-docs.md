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

**Status of the unticked rows above: needs manual run.** They require a real Console key and a live window. Fake-HTTP and fake-provider tests cover parts of the workflow, but they do not replace live inference, UI, or restart checks. Proposal and hunk restoration have focused persistence tests; the live restart remains open. Follow the [manual acceptance script](#manual-acceptance-needs-manual-run) and tick a row only with its recorded result.

**Gate:** Record the Console key type and workspace scope, selected provider/model/protocol, commands run, and manual UI sequence. Document unsupported model protocols and relevant Console inference restrictions. Do not use an OpenCode Server process or remote Server session as an acceptance dependency.

## Phase 2: Repository gates and docs

- [x] Add focused fake-HTTP, storage, editor, agent-loop, tool-permission, and UI checks with each implementation slice. Keep platform PTY checks separate from API protocol checks. Evidence: fake-HTTP and loop tests in `internal/agent` (`agent_test.go`, `loop_test.go`) and `internal/opencode/console_test.go`; storage in `internal/agentstore/*_test.go`; tool permission in `internal/agenttools/*_test.go`; editor/UI/e2e in `internal/app/opencode_*_test.go` (for example `opencode_agent_e2e_test.go`, `opencode_gate_test.go`, `opencode_inline_test.go`, `opencode_hunks_test.go`). PTY checks live in `internal/terminal` and are not mixed into API tests.
- [ ] Run the repository's current serial validation gate, vet, formatting, and build checks after implementation is complete. **Partly run, left open:** build, vet and formatting were run and the named tests below passed, but the full serial test gate was not run in this pass because the WSL host crashes on whole-package runs. Run the full gate on a host that can take it; commands and results are in [Validation gate record](#validation-gate-record).
- [x] Update `README.md` with Console key setup, where the key is stored, local session-history location, direct model inference, local tool permissions, privacy/context controls, and known limits.
- [x] Update `docs/architecture.md` with the Console catalog/inference client, local SQLite store, own-code agent/tool loop, and edit-review boundaries.
- [x] Reconcile issue #9's acceptance list against the implemented features and current evidence. Keep live or full-gate checks open until they pass. Done in [Issue #9 reconciliation](#issue-9-reconciliation) below.

**Gate:** The documented setup works from a clean profile. The checklist status matches implemented behavior and recorded verification.

## Validation gate record

Run from the repository root. The latest changes have focused package tests and formatting checks below. The full serial test gate and `-race` were not run because the WSL host crashed on whole-package runs; run them, plus vet and lint, on a host that can take them.

| Command | Result |
|---|---|
| `gofmt` on changed Go files | Clean after the proposal, ordered-part, and signature changes. |
| `git diff --check` | Clean. |
| Focused current tests: `go test ./internal/agentstore ./internal/agent ./internal/app ./internal/agentreview -count=1 -run 'Test(ThoughtSignaturesAndPartOrderSurviveStoreReopen|TurnRoundTripWithToolCallsAndRecovery|MigrationFromV3PreservesLegacyTranscript|GeminiThoughtSignatureRoundTripsOnFunctionCallPart|MultipleToolStepsRebuildHistory|GeminiResolveAndEndpoint|OpenCodeProposalPersistence.*|ParseOpenCodeProposalNameStatus|OpenCodeProposalSnapshot.*|RestoreStatesRequiresExactHunkIDsAndIsAtomic)'` | All four packages `ok`. |
| Earlier baseline `go build ./...`, targeted `go vet`, and adversarial package tests | Passed before the latest persistence and Gemini ordered-part changes; rerun in the final gate. |

Still to run before merge (not run here): `go build ./...`, `go test -count=1 -p 1 ./...`, the same tests with `-race` on a host that tolerates it, `go vet ./...`, and the repository lint gate.

Earlier dead-code removal deleted the unused OpenCode Server client, process launcher, and event stream from the active integration. The earlier `go build ./...` and targeted `go vet` passed before the latest persistence and Gemini changes; rerun them in the final gate. Unused server-session request/response types remain in `internal/opencode/models.go` and can be pruned separately; `Session`, `SessionTime`, `LocationRef`, `IntegrationMethod`, and `ConnectKeyInput` are still used by `internal/app`.

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
- Supported model protocols: `chat-completions`, `responses`, `messages` (Anthropic), and `generate-content` (Gemini). Models with none are hidden; free-tier chat models accept requests only from OpenCode and are not offered.
- Sensitive-name and git-ignored exclusion for model-initiated `read_file`, `list_files`, and `search_files`; `run_shell` remains approval-gated and can read files only when the user approves the command.

Live or repository validation still open:

- Live Console turn with a real key and an eligible model: not run. Needs manual run (script below).
- Restart in a live window restoring sessions and pending review state: not run live. Proposal metadata and hunk state now persist with staged bytes in private per-session files; focused tests cover restore, while the native-window restart remains open.
- 03/07: minimum-window dock and streamed Markdown presentation; terminal focus, keyboard, resize and scroll with the dock open; a real `file:line` click; and live exercise of `Ctrl+Shift+L`, dropdowns, and diff-hunk styling.
- 06: delete and rename proposals are reviewed as whole-file units. They have focused tests, but no real model has used either tool in a live run.
- 01: the current public model list returns Gemini `base_url` as `https://opencode.ai/inference/google/v1beta`, and official docs show Bearer authentication for Anthropic Messages. The authenticated workspace catalog and live Gemini/Anthropic requests have not been checked.
- 09: full serial tests, `-race`, vet, and lint remain to run on a stable host.

## Manual acceptance (needs manual run)

Record with the run: key type (`oc_sk_`, workspace scope), selected provider, model and protocol, commands, and the UI sequence. Do not start or attach to `opencode serve`.

1. Start from a clean profile: remove `ownframe/code-view` under the user config directory, launch `go run ./cmd/own-code <repo>`.
2. Settings, OpenCode: paste a workspace `oc_sk_` key, Connect. Expect "Loaded N models". Confirm `settings.json` has only `providerId` and `modelId`, and `opencode-console-api-key` is the only file containing the key (`grep -r oc_sk_ <config dir>`).
3. Open the right dock at minimum supported window size. Pick an eligible model in the footer menu, open/navigate/close the model and history dropdowns, create a session, and verify the `Ctrl+Shift+L` chord.
4. Type `@`, attach the active file and the diff, check the preview and that `.env` is not offered, send "explain this", and inspect streamed Markdown as it arrives.
5. Ask for a change that needs `read_file` (no prompt), then one that needs `run_shell` (approve), then another `run_shell` (deny). Check the transcript order and states.
6. Start a long answer and press Stop. Confirm the turn shows interrupted with no duplicate entries; retry.
7. Select code, run the inline edit command, accept one result and reject another, then undo the accepted one.
8. Ask for edits to two files. Review in the diff view, accept one hunk, reject another, revert the accepted hunk. Check diff-hunk styling and confirm your own unsaved edits and staged changes are intact.
9. Ask the model to use `delete_file` on a disposable file and `move_file` on another. Confirm each appears as one Accept or Reject unit with no hunks, then exercise reject and accept. Verify source/destination paths, staged state, and open tabs.
10. Leave one pending proposal and a mixed hunk review, then quit during a `run_shell` approval. Relaunch: confirm proposals and hunk decisions restore, the call is interrupted and marked for inspection, and nothing re-runs without a new approval.
11. With the dock open, verify terminal focus, typing, resize, and scroll; click a real `file:line` link. Confirm no process named `opencode` was started during the run.
