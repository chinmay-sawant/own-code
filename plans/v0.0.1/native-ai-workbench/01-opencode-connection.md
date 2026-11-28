# OpenCode connection and settings

**Goal:** Connect own-code to a supported OpenCode Server version and onboard a model provider without keeping provider secrets in own-code.

## Phase 1: Record the V2 contract

- [ ] Pin the minimum supported OpenCode version and reject incompatible servers. `GET /api/info` returns a version, but the app currently displays it without enforcing a minimum. The V2 migration guide names the API generation but does not establish a minimum compatible release for this integration. Sources: [OpenCode V2 migration guide](https://opencode.ai/docs/migrate-v1/#server-api-and-clients), [API reference](https://opencode.ai/docs/api).
- [x] Record the current endpoint inventory in the OpenCode transport: `GET /api/info`; `GET /api/integration`; `POST /api/integration/{integrationID}/connect/key`; `GET` and `POST /api/session`; `GET /api/session/active`; `POST /api/session/{sessionID}/prompt`; `GET /api/session/{sessionID}/message`; `GET /api/event`; and `POST /api/session/{sessionID}/interrupt`. See `internal/opencode/client.go` and the [OpenCode API reference](https://opencode.ai/docs/api). The generated [OpenAPI document](https://opencode.ai/docs/openapi.json) describes its selected-instance HTTP surface as experimental; its Session and Event sections also label those route groups experimental. It also exposes the experimental durable `GET /api/experimental/session/{sessionID}/log` route, which this client does not implement. The implemented `/api/event` stream remains volatile and spans server locations. This records current routes and gaps, not a compatibility promise.
- [x] Record workspace scoping: generic location-scoped routes encode the directory as the `location[directory]` deep-object query parameter. The integration list and key-connect requests use that parameter. Session listing instead has a `directory` query filter, and session creation accepts `location.directory` in its body. Prompt, message, active-session, and interrupt routes address a session ID. `GET /api/event` is an exception: the reference says it streams across all server locations and does not expose a directory filter. Sources: [Location and Session API reference](https://opencode.ai/docs/api), [OpenAPI document](https://opencode.ai/docs/openapi.json). Event consumers must still filter by workspace/session before using event data; the app does not currently subscribe to the event stream.
- [x] Record and implement the V2 server authentication contract: HTTP Basic auth, fixed username `opencode`, and `OPENCODE_PASSWORD`; `OPENCODE_SERVER_PASSWORD` remains supported. See `internal/opencode/client.go`, `internal/opencode/server_process.go`, `internal/opencode/client_test.go`, and `internal/opencode/server_process_test.go`. Sources: [V2 migration guide: authentication](https://opencode.ai/docs/migrate-v1/#server-authentication), [OpenCode Web authentication](https://opencode.ai/docs/cli/web/#authentication).
- [ ] Distinguish local server password, provider API key, and OpenCode Console inference key in the UI copy and architecture notes. The Settings copy labels the server password separately from the provider API key; the plan overview distinguishes hosted Console inference from the local Server API. The Settings copy does not yet explicitly name the Console inference key.
- [x] Inspect `GET /api/integration` and its authentication-method data. The app discovers integrations from the selected workspace, exposes key-auth methods without fixed provider IDs, and parses the server-advertised key form. `Integration.Method` can advertise OAuth, command, key, external, or environment authentication; own-code currently submits only the key method. See `internal/opencode/models.go`, `internal/opencode/client.go`, `internal/app/opencode_workbench.go`, `internal/app/opencode_provider_fields.go`, `internal/opencode/client_test.go`, and `internal/app/opencode_provider_fields_test.go`. The current key method's optional `form` is `Form.Fields` (an array); `connect/key` accepts required `key`, optional `answer`, and optional `label`; `answer` maps field names to string, number, boolean, or string-array values. Source: [OpenCode Integration and Form schemas](https://opencode.ai/docs/openapi.json).
- [ ] Capture request and response fixtures that match the selected V2 OpenAPI contract. The existing integration fixture models the response's `data` array and `method.form` as `Form.Fields` with `key` and `type`; the prompt fixture models `{data: Session.Inbox.User}`. The transport models the direct `{interrupted}` response and exposes optional nullable session-create `permissions` and prompt `delivery` request properties. The current OpenAPI defines `POST /api/session` as `{data: Session.Info}`, session and message list responses as `{data, cursor}`, and `GET /api/session/active` as `{data: {<sessionID>: {type: "running"}}}`. Existing fixtures do not cover session creation/listing, active-session decoding, message-list pagination, or interruption. Source: [OpenCode OpenAPI document](https://opencode.ai/docs/openapi.json), schemas and operations `session.create`, `session.list`, `session.active`, `session.prompt`, `session.message.list`, and `session.interrupt`. Keep API-specific types in `internal/opencode` rather than `internal/app`.

### Illustrative Go boundary

The server-advertised method defines the optional form fields. OpenCode's current key method accepts a required key plus an optional answer object and label. This sketch represents the ownership boundary; the actual `Form.Fields` schema is defined by the server.

```go
type ConnectKeyInput struct {
	Key    string
	Answer json.RawMessage // map of Form.Value primitives; transient only
	Label  *string
}

// ConnectKey sends one-time provider authentication input to OpenCode.
// The provider credential and answer values must not be logged or persisted.
func (c *Client) ConnectKey(ctx context.Context, integrationID string, input ConnectKeyInput, directory string) error
```

**Gate:** Still open. The source-linked endpoint/auth/location contract is recorded, but the minimum supported server version is not selected and OpenAPI-aligned fixtures do not yet cover all request and response shapes. The current integration form fixture matches the documented `Form.Fields` array. The durable session log is not yet implemented or version-gated. Session and event operations are explicitly marked experimental in the OpenAPI document's `session` and `event` tags.

## Phase 2: Add settings and provider onboarding

- [ ] Add an OpenCode section to `components/html/settings.html` and `components/css/settings.css`. The section shows mode, server address, and connection status; it does not yet show the selected workspace path.
- [x] Support an owned local server and attaching to an existing server. The Settings section shows the active mode; see `components/html/settings.html`, `internal/app/opencode_settings.go`, and `internal/app/opencode_workbench.go`.
- [x] Discover available provider integrations and authentication methods from OpenCode. The user can choose a discovered key-auth integration before entering a key; see `internal/app/opencode_workbench.go` and `components/html/settings.html`.
- [x] Use a masked, transient input for a provider API key. Submit it to the authenticated server's `POST /api/integration/{integrationID}/connect/key` route; see `components/html/settings.html`, `internal/app/opencode_settings.go`, `internal/opencode/client.go`, and `internal/opencode/client_test.go`.
- [x] Clear provider key and advertised form inputs before the async submission starts. `internal/app/opencode_settings.go` clears the base credentials and all discovered key-form field values before launching the asynchronous request; the form controls have no `data-bind` and their values are absent from the view model.
- [ ] Support replacing and disconnecting a provider credential through the server API. The current [Credential API reference](https://opencode.ai/docs/api) defines `DELETE /api/credential/{credentialID}` to remove a stored credential. `GET /api/integration` returns connection metadata with credential IDs; avoid `GET /api/credential`, whose response includes secret values. Own-code has not implemented removal, tested multiple/active-credential effects, or added a confirmation flow.
- [ ] Keep the server password out of `settings.json`, `session-*.json`, workspace files, SQLite, view data, error messages, and logs. It is stored in a separate owner-only user-config file (`internal/secretstore/opencode_password.go`); however, end-to-end checks for all listed surfaces and cross-platform permission guarantees are still incomplete. Provider keys are submitted directly to OpenCode.
- [x] No repository-local `.env` is used for OpenCode credentials. The server password store is under the per-user config directory; no project credential file is created.
- [x] Keep hosted Console inference setup out of this local-server form. The Settings form is for a local/attached OpenCode Server and provider authentication; the architecture boundary is recorded in `plans/v0.0.1/native-ai-workbench/README.md`.

**Gate:** UI states cover missing, connecting, connected, replacing, and failed credentials. A server confirms the provider is connected, while own-code settings, session files, SQLite, rendered views, and logs contain no provider key.

## Phase 3: Launch, attach, and recover

- [ ] Detect the installed `opencode` CLI and report a useful setup state when it is missing or too old.
- [x] Start an owned `opencode serve` process on loopback. Pass the server password through the child environment, not through command-line arguments. Fake-process coverage is in `internal/opencode/server_process_test.go`.
- [ ] Wait for `GET /api/info` before enabling session actions. Apply request deadlines and surface unauthorized, unavailable, and incompatible-version errors without printing credentials. Readiness checks and request deadlines exist, but minimum-version rejection and the full settings-to-session gate have not been proven.
- [x] Attach to a user-configured server URL. Require HTTPS for remote endpoints unless a deliberate local-network policy is documented. See `internal/app/opencode_settings.go`, `internal/opencode/client.go`, `internal/app/opencode_settings_test.go`, and `internal/opencode/client_test.go`.
- [x] Track whether own-code owns the child process. On close, stop only a process own-code started; never terminate a shared OpenCode service. The process wrapper owns its child, and fake-process coverage checks shutdown and that a service already bound to the requested port remains alive. See `internal/opencode/server_process.go` and `internal/opencode/server_process_test.go`.
- [ ] Handle server restarts, child exits, timeouts, and settings changes without blocking the UI loop.
- [x] Keep an owned server bound to `127.0.0.1` by default. The launcher passes `--hostname 127.0.0.1`; the fake-process test checks the loopback URL. See `internal/opencode/server_process.go` and `internal/opencode/server_process_test.go`.

**Gate:** Fake CLI/server coverage proves readiness checks, auth failure, timeout, child exit, attach behavior, and cleanup. A manual pass connects to an installed OpenCode server and confirms own-code leaves unrelated server processes running.
