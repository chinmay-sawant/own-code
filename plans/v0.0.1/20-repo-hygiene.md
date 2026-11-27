# 20 — Repo hygiene and release

Small, high leverage. Do before the first real release.

## Release artifacts
- [x] Stop committing binaries (`linux-release/`, `macos-release/`, `windows-release/`, about 350 MB; `.git` is 487 MB). Publish them as GitHub Release assets instead, built by CI on tag.
- [x] Update `install.sh` / `install.bat` to download from `releases/latest/download/...` and verify a published SHA-256 checksum file.
- [ ] Decide whether to rewrite history to drop the old binaries (`git filter-repo`). It is optional, destructive, and needs a force-push, so only do it deliberately.
- [ ] Tag and publish `v0.0.1`; verify the six release assets and `SHA256SUMS` after the release workflow completes.

## CI
- [x] `.github/workflows/ci.yml` on PR/push: Linux formatting, vet, and serial tests plus macOS and Windows builds (evidence: `.github/workflows/ci.yml`; PR #12 checks passed on 2026-11-27; lint is advisory and currently reports findings).
- [ ] Verify the tag-triggered release workflow on the real `v0.0.1` tag (six Linux/macOS/Windows amd64/arm64 builds, followed by assets and `SHA256SUMS`).
- [x] Cache the Go build; run tests under `xvfb-run` for the screenshot/e2e tests.  (setup-go cache enabled; tests run under `xvfb-run -a`.)
- [x] Add `golangci-lint` (start with a small linter set so it passes on day one).  (Added as an advisory, `continue-on-error` CI job with default linters; no `.golangci.yml` yet. Tighten once a baseline run is clean.)

## Build
- [x] Document or remove the need for sibling checkouts (evidence: no `go.work` or local `replace`; README uses a clean clone and the Go module proxy).
- [x] `Makefile`: `make test`, `make lint`, `make release`.
- [ ] Remove the stray `own-code` binary and `tmp/` from the working tree root (both ignored, but easy to confuse).

## Code structure
- [ ] Split `internal/app` (120+ non-test files; `app.go` 4.2k lines, `actions.go` 3.3k). Candidate sub-packages: `internal/search`, `internal/scm` (git, blame, commit graph, merge), `internal/explorer`, `internal/settings`, `internal/palette`. Do it incrementally, one package per PR, behind the existing tests.
- [ ] Break `actions.go`'s key dispatcher into per-feature handlers registered in a table (keymap catalog already exists in `settings.go`).

## Docs
- [x] Reconcile README and plans: theme count (README says ten, master checklist says six), "no horizontal scroll" claim, Limits section (some limits will change with `19`).  (Done for theme count (ten, per `themeCatalog`) and the horizontal-scroll claim (`hscroll.go`). The Limits section will still need a pass after plan 19 lands.)
- [x] Mark the initial `00-master-checklist.md` as a historical planning draft and point to `CHANGELOG.md`; keep current release checks in its Phase 11.3 record.
- [x] Add `CONTRIBUTING.md` and `docs/architecture.md` (data flow: tick, input, view, async workers).
- [x] Add a `LICENSE` (MIT) if one is missing (none is in the tracked file list).
