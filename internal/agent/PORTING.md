# Porting record

No LazyCoder source files were copied into `internal/agent`. The adapters were
written from the OpenCode Console protocol shapes, using LazyCoder
(`internal/provider/opencode`, MIT) only as a pattern reference at
`816e2113a54f659f779f2c8592b266011829492c`. No MIT notice is required for
this package; add one here if substantial code is copied later.

| LazyCoder source (reference only) | Destination |
| --- | --- |
| `internal/provider/opencode/client.go` | `client.go` (transport, errors, limits) |
| `internal/provider/opencode/stream.go` | `client.go` (SSE reader), `chat.go` |
| `internal/provider/opencode/responses.go` | `responses.go` |
| (own-code specific) | `types.go`, `model.go` (eligibility from `internal/opencode.ConsoleModel`) |

Deliberate differences: Console routing uses the catalog `api` key and
`base_url` (never model names); `x-opencode-session` carries the local session
ID; tool calls are surfaced only when complete.
