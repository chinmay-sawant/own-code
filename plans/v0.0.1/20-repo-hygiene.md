# 20 — Repo hygiene and release

Small, high leverage. Do before the first real release.

## Release artifacts
- [ ] Stop committing binaries (`linux-release/`, `macos-release/`, `windows-release/`, about 350 MB; `.git` is 487 MB). Publish them as GitHub Release assets instead, built by CI on tag.
- [ ] Update `install.sh` / `install.bat` to download from `releases/latest/download/...` and verify a published SHA-256 checksum file.
- [ ] Decide whether to rewrite history to drop the old binaries (`git filter-repo`). It is optional, destructive, and needs a force-push, so only do it deliberately.
- [ ] Tag `v0.0.1`; add a `CHANGELOG.md` (the master checklist's "release notes stub").

## CI
- [ ] `.github/workflows/ci.yml` on PR/push: `gofmt -l`, `go vet ./...`, `go test -p 1 ./...` on linux, plus a windows and macos build job.
- [ ] Make `build-windows.yml` follow `go.mod` rather than a hard-coded ownframe commit (it still pins `abb99008f1dc`, while go.mod is on v0.0.5). Replace it with a matrix release workflow triggered on tags (linux/macos/windows x amd64/arm64).
- [ ] Cache the Go build; run tests under `xvfb-run` for the screenshot/e2e tests.
- [ ] Add `golangci-lint` (start with a small linter set so it passes on day one).

## Build
- [ ] Document or remove the need for sibling checkouts (`go.work`, `../blinkless`). Once `ownframe` is consumed purely as a tagged module, `go build ./cmd/own-code` should work from a clean clone. Add a "clean clone builds" CI job to guard that.
- [ ] `Makefile`: `make test`, `make lint`, `make release`.
- [ ] Remove the stray `own-code` binary and `tmp/` from the working tree root (both ignored, but easy to confuse).

## Code structure
- [ ] Split `internal/app` (120+ non-test files; `app.go` 4.2k lines, `actions.go` 3.3k). Candidate sub-packages: `internal/search`, `internal/scm` (git, blame, commit graph, merge), `internal/explorer`, `internal/settings`, `internal/palette`. Do it incrementally, one package per PR, behind the existing tests.
- [ ] Break `actions.go`'s key dispatcher into per-feature handlers registered in a table (keymap catalog already exists in `settings.go`).

## Docs
- [ ] Reconcile README and plans: theme count (README says ten, master checklist says six), "no horizontal scroll" claim, Limits section (some limits will change with `19`).
- [ ] Tick the boxes in `00-master-checklist.md` for shipped phases, or mark that file as historical and point to `CHANGELOG.md`.
- [ ] Add `CONTRIBUTING.md` and `docs/architecture.md` (data flow: tick, input, view, async workers).
- [ ] Add a `LICENSE` if one is missing (none is in the tracked file list).
