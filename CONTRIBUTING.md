# Contributing

## Setup

You need Go 1.26 (see `go.mod`) and sibling checkouts of two repos next to
this one:

```
parent/
  own-code/
  ownframe/    github.com/chinmay-sawant/ownframe, at the tag go.mod requires
  blinkless/   github.com/chinmay-sawant/blinkless, at the commit go.mod lists
```

`go.mod` has `replace github.com/chinmay-sawant/ownframe => ../ownframe` and
`go.work` uses `../ownframe` and `../blinkless`. To fetch both at the right
versions, run `.github/scripts/checkout-siblings.sh` from the repo root (CI
does the same).

On Linux you also need the ebiten dependencies (`libgl1-mesa-dev`,
`xorg-dev`, `libasound2-dev`) and `xvfb` for tests on a headless machine.

## Everyday commands

| Command | What it does |
|---------|--------------|
| `make run` | Run the app from source (`go run ./cmd/own-code`). |
| `make test` | `go test -p 1 ./...`, under `xvfb-run` when there is no display. |
| `make lint` | `go vet`, a `gofmt -l` check, and `golangci-lint` if installed. |
| `make check` | `lint` then `test`. |
| `make fmt` | `gofmt -w` the Go sources. |
| `make release` | Build a host-platform binary and `SHA256SUMS` into `dist/`. |

Tests must run serially (`-p 1`); parallel runs flake. Screenshot frames:
`OWNFRAME_CODE_VIEW_SCREENSHOT=1 go test -run '^TestScreenshot$' ./internal/app`.

## Pull requests

- Branch from `master`; keep a PR to one change.
- Before pushing: `make check`. CI runs gofmt, `go vet`, and the serial tests
  on Linux, and builds on Windows and macOS.
- Add or update tests next to the code you change. Handler tests drive `App`
  directly and assert on `View()`; see `docs/architecture.md`.
- If you change a documented key, limit, or theme, update `README.md` in the
  same PR (the Keys and Limits sections are load-bearing).
- Commit messages follow `type(scope): summary`, e.g. `fix(split): ...`.
- Do not commit binaries. Releases are built by CI from a `v*` tag.

## Releasing

Update `CHANGELOG.md`, then tag and push: `git tag v0.0.1 && git push origin v0.0.1`.
`.github/workflows/release.yml` builds each platform and attaches the
binaries and `SHA256SUMS` to a GitHub Release.

See `docs/architecture.md` for how the code is organized.
