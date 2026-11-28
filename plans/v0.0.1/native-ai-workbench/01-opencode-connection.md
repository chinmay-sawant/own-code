# OpenCode Console API and credentials

**Goal:** Use OpenCode's hosted Console catalog and inference APIs directly. Keep the API key private and keep agent-session behavior inside own-code.

## Phase 1: Record the Console contract

- [x] Record the current authentication boundary: Console API keys use the `oc_sk_` prefix, are bearer tokens, and are bound to one workspace when created; requests do not need a workspace header. Sources: [Console API overview](https://opencode.ai/docs/console/api).
- [x] Record the catalog route: `GET https://opencode.ai/console/api/v1/models` returns providers and the models available to the key's workspace. Model entries include their type, limits, capabilities, and protocol-specific `api` metadata. Sources: [Model list API](https://opencode.ai/docs/console/api/models).
- [x] Extend `internal/opencode/console.go` model decoding to retain protocol families and their `base_url` values from `api`; select the request route from that metadata instead of guessing from the model name. Catalog metadata is retained; inference route selection is still open.
- [ ] Implement direct inference requests using the selected model's advertised API family. Current documented routes include OpenAI Chat Completions (`/inference/openai/v1/chat/completions`), OpenAI Responses (`/inference/openai/v1/responses`), Anthropic Messages (`/inference/anthropic/v1/messages`), and Gemini (`/inference/google/v1beta/models/<model>:generateContent`). Send the Console key as a bearer token. Sources: [Inference API](https://opencode.ai/docs/console/api/inference), [Model list API](https://opencode.ai/docs/console/api/models).
- [ ] Support streaming response parsing and bounded request/response handling for the protocol families selected by the catalog. Keep error bodies and provider response data bounded and redact credentials.
- [x] Handle authentication, permission, quota, unavailable-model, and unsupported-protocol errors without including the key or raw authorization headers in logs or UI messages. Evidence: `agent/client.go` httpError classifies 401/403/429/5xx with redaction; `TestHTTPErrorsNoKeyLeak`; unsupported protocol rejected in `agent/model.go`.
- [x] Document the Console limitation that free-tier chat models only accept requests from OpenCode; own-code must expose eligible paid or Go models from the catalog and communicate access failures clearly. Source: [Inference API](https://opencode.ai/docs/console/api/inference). Evidence: 403 message in `agent/client.go` says catalog membership does not guarantee inference access (free-tier models only accept OpenCode requests); `TestHTTPErrorsNoKeyLeak`. The eligible-model filter is `agent.FilterEligible`.
- [x] Do not use OpenCode Server routes, server passwords, `opencode serve`, remote session IDs, or server event streams in this integration. Evidence: `internal/app` has no references to `opencode.NewClient`, `ServerProcess`, or remote session IDs after the server-history removal; `TestConsolePathHasNoOpenCodeServerRoutes` covers the agent packages. The unused server client/process code in `internal/opencode` is still compiled for its shared types and tests. Console inference supplies model calls; own-code owns agent state and execution.

**Current implementation:** `internal/opencode/console.go` contains a bounded, redirect-rejecting client for the Console model-list endpoint. It returns provider/model identifiers, display names, model types, context/output limits, tool support, and protocol/base-URL metadata. Settings accepts a masked workspace key, loads the catalog asynchronously, saves a typed key only after a successful catalog response, supports clearing the saved key, and saves the selected provider/model IDs. New local sessions and transcript reads use SQLite. Chat Completions and Responses inference with streaming and the local agent/tool loop are implemented in `internal/agent`; Anthropic Messages and Gemini routes are not (such models are filtered out as ineligible). Legacy OpenCode Server code remains in `internal/app/opencode_worktree_sessions.go` and `opencode_session_store.go`. The app's active settings and session paths do not connect to `opencode serve`.

**Gate:** Catalog fixtures cover provider/model decoding, omitted fields, malformed and oversized responses, deterministic ordering, and redacted errors. Inference fixtures cover each supported protocol, streaming, cancellation, response limits, non-success responses, and secret redaction. No agent loop or tool execution is considered complete by the HTTP client alone.

## Phase 2: Add a Console key setting

- [x] Add an OpenCode Console section to the native Settings UI with a masked key field, save/replace/remove actions, and connection status. Explain that the key must be created for the intended workspace in OpenCode Console.
- [x] Validate the input shape enough to catch empty or obviously malformed keys without displaying or logging the supplied value. A key is not valid merely because it starts with `oc_sk_`; confirm it through the catalog request. Evidence: `opencode.ValidateConsoleAPIKeyShape` (empty, length, whitespace/control/non-ASCII; error never echoes the value) called from `NewConsoleClient`; `TestValidateConsoleAPIKeyShape`. Catalog request still confirms validity.
- [x] Store the key outside `settings.json`, editor session files, SQLite, workspace files, and rendered data. The current file store uses the per-user config directory with owner-only directory/file permissions; OS keychain support and cross-platform permission verification remain open.
- [ ] Load the credential only for a bounded API request. Clear transient input and intermediate buffers where practical. Redact the key from `String`/`GoString`, wrapped errors, logs, and diagnostics.
- [x] Replace or remove saved credentials without exposing their value. Do not attempt to create, rotate, or revoke Console keys through the own-code API unless the official API explicitly supports it.
- [x] Do not use a repository-local `.env` or ask the user to put a workspace key in a project file.

**Gate:** UI states cover missing, checking, valid, invalid, unavailable, replacing, and removed credentials. A clean-profile pass proves the saved key survives restart but is absent from settings JSON, SQLite, workspace files, view data, logs, and error output. Permission behavior is verified on each supported desktop platform.

## Phase 3: Connect settings to the model catalog

- [x] Fetch and refresh the workspace catalog asynchronously after credential save or an explicit refresh. Do not block the UI render loop.
- [x] Show provider, model name, model type, context/output limits, and tool-call capability from the catalog. Preserve provider ID and model ID as request identifiers.
- [x] Store the chosen provider/model identifiers as non-secret settings, and validate them against the current catalog before sending inference. Evidence: `resolveOpenCodeSessionModel` re-checks the saved provider/model against the live catalog at request time; `TestResolveSessionModelRejectsIneligible`.
- [x] Report catalog failures with actionable status while retaining the last known model list only if its age and workspace association are clear. Failed catalog requests clear the active catalog and report status; no stale catalog is shown.

**Gate:** Settings display a real workspace catalog and never mix model choices from different API keys/workspaces. Refresh, key replacement, empty catalog, and network failure are covered.
