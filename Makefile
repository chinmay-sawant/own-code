.PHONY: run dev

# Run the app straight from source.
run:
	go run ./cmd/own-code

# Restart automatically on source or template changes via air.
dev:
	air -c .air.toml
