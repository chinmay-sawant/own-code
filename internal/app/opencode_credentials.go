package app

import (
	"errors"
	"os"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/secretstore"
)

// loadOpenCodeServerPassword checks the dedicated user-config secret first,
// then the documented process environment names for existing setups.
func loadOpenCodeServerPassword() (string, error) {
	store, err := secretstore.DefaultOpenCodePassword()
	if err != nil {
		return "", err
	}
	password, err := store.Load()
	if err == nil {
		return password, nil
	}
	if !errors.Is(err, secretstore.ErrNotFound) {
		return "", err
	}

	for _, name := range []string{"OPENCODE_PASSWORD", "OPENCODE_SERVER_PASSWORD"} {
		if password := os.Getenv(name); strings.TrimSpace(password) != "" {
			return password, nil
		}
	}
	return "", secretstore.ErrNotFound
}

func saveOpenCodeServerPassword(password string) error {
	store, err := secretstore.DefaultOpenCodePassword()
	if err != nil {
		return err
	}
	return store.Save(password)
}

func deleteOpenCodeServerPassword() error {
	store, err := secretstore.DefaultOpenCodePassword()
	if err != nil {
		return err
	}
	return store.Delete()
}
