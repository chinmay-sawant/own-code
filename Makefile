.PHONY: run dev install test vet fmt lint check release

PREFIX ?= $(HOME)/.local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DIST ?= dist

# Run the app straight from source.
run:
	go run ./cmd/own-code

# Restart automatically on source or template changes via air.
dev:
	air -c .air.toml

# Build the current checkout and install it to $(PREFIX)/bin.
install:
	./install-local.sh --prefix "$(PREFIX)"

# Serial tests are the gate; parallel runs flake. Uses xvfb-run when present
# and no display is set.
test:
	@if [ -z "$$DISPLAY" ] && command -v xvfb-run >/dev/null 2>&1; then \
		xvfb-run -a go test -p 1 ./...; \
	else \
		go test -p 1 ./...; \
	fi

vet:
	go vet ./...

fmt:
	gofmt -w assets.go cmd internal

# gofmt must be clean, go vet must pass, and golangci-lint runs when installed.
lint: vet
	@out="$$(gofmt -l assets.go cmd internal)"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; \
	else echo "golangci-lint not installed; skipped"; fi

check: lint test

# Build a binary for the host platform into $(DIST)/ plus a checksum file.
# Cross-platform release binaries are built by .github/workflows/release.yml
# on a version tag (ebiten needs cgo on Linux, so each OS builds natively).
release:
	mkdir -p "$(DIST)"
	go build -trimpath -ldflags "-s -w" -o "$(DIST)/own-code-$$(go env GOOS)-$$(go env GOARCH)" ./cmd/own-code
	cd "$(DIST)" && sha256sum own-code-* > SHA256SUMS
	@echo "built $(VERSION) into $(DIST)/"
