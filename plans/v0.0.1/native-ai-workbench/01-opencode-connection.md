# OpenCode connection and settings

**Goal:** Connect own-code to a supported OpenCode Server version and onboard a model provider without keeping provider secrets in own-code.

## Phase 1: Record the V2 contract

- [ ] Record the minimum supported OpenCode version and the exact V2 endpoints used for server info, integrations, credentials, sessions, messages, events, and interruption. Source: [OpenCode API reference](https://opencode.ai/docs/api) and [V1 migration guide](https://opencode.ai/docs/migrate-v1/).
- [ ] Record how `location[directory]` scopes API requests to the selected workspace.
- [ ] Record the server authentication contract: Basic auth, username `opencode`, password `OPENCODE_PASSWORD`, with `OPENCODE_SERVER_PASSWORD` as a supported compatibility name. Source: [OpenCode server authentication](https://opencode.ai/docs/cli/web/).
- [ ] Distinguish local server password, provider API key, and OpenCode Console inference key in the UI copy and architecture notes.
- [ ] Inspect `GET /api/integration` and its authentication-method data. Do not hardcode provider IDs or assume every provider uses a plain API key.
- [ ] Capture request and response fixtures from the selected V2 OpenAPI contract. Keep API-specific types in an OpenCode package rather than in `internal/app`.

### Illustrative Go boundary

The provider form and request body must come from the selected integration's server-advertised method. Treat this snippet as an ownership sketch, not a fixed OpenCode payload shape.

```go
type ProviderKeyInput struct {
	Fields map[string]string
}

// ConnectProviderKey sends the one-time input to OpenCode.
// The input must not be logged or written to own-code storage.
func (c *Client) ConnectProviderKey(ctx context.Context, integrationID string, input ProviderKeyInput) error
```

**Gate:** The source-linked contract names the supported version, authentication boundary, workspace scoping, and endpoints. Fake-server fixtures cover the request shapes before UI wiring begins.

## Phase 2: Add settings and provider onboarding

- [ ] Add an OpenCode section to `components/html/settings.html` and `components/css/settings.css`. Show connection mode, server address, connection status, and the selected workspace.
- [ ] Support an owned local server and attaching to an existing server. Make the active mode visible.
- [ ] Discover available provider integrations and authentication methods from OpenCode. Let the user choose an integration before entering a credential.
- [ ] Use a masked, transient input for a provider API key. Submit it only to the authenticated server's `POST /api/integration/{integrationID}/connect/key` endpoint.
- [ ] Clear the input after submission. Show connection status without reading the saved secret back into own-code.
- [ ] Support replacing and disconnecting a provider credential through the server API. Confirm the API's actual disconnect semantics before adding a destructive control.
- [ ] Keep the server password out of `settings.json`, `session-*.json`, workspace files, SQLite, view data, error messages, and logs. Prefer the OS credential store; otherwise use a separate owner-only file under the user config directory.
- [ ] If implementation uses a repository-local `.env`, first add `.env`, `.env.*`, and `!.env.example` patterns to `.gitignore`. Do not place per-project credentials in the opened workspace.
- [ ] Keep hosted Console inference setup out of this local-server form. If direct Console inference becomes a separate feature, plan its bearer key and API routes separately.

**Gate:** UI states cover missing, connecting, connected, replacing, and failed credentials. A server confirms the provider is connected, while own-code settings, session files, SQLite, rendered views, and logs contain no provider key.

## Phase 3: Launch, attach, and recover

- [ ] Detect the installed `opencode` CLI and report a useful setup state when it is missing or too old.
- [ ] Start an owned `opencode serve` process on loopback. Pass the server password through the child environment, not through command-line arguments.
- [ ] Wait for `GET /api/info` before enabling session actions. Apply request deadlines and surface unauthorized, unavailable, and incompatible-version errors without printing credentials.
- [ ] Attach to a user-configured server URL. Require HTTPS for remote endpoints unless a deliberate local-network policy is documented.
- [ ] Track whether own-code owns the child process. On close, stop only a process own-code started; never terminate a shared OpenCode service.
- [ ] Handle server restarts, child exits, timeouts, and settings changes without blocking the UI loop.
- [ ] Keep an owned server bound to loopback by default. Do not enable remote exposure automatically.

**Gate:** Fake CLI/server coverage proves readiness checks, auth failure, timeout, child exit, attach behavior, and cleanup. A manual pass connects to an installed OpenCode server and confirms own-code leaves unrelated server processes running.
