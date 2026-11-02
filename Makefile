.PHONY: run dev install

PREFIX ?= $(HOME)/.local

# Run the app straight from source.
run:
	go run ./cmd/own-code

# Restart automatically on source or template changes via air.
dev:
	air -c .air.toml

# Build the current checkout and install it to $(PREFIX)/bin.
install:
	./install-local.sh --prefix "$(PREFIX)"
