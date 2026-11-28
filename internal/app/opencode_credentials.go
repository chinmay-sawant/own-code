package app

import (
	"errors"
	"os"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/secretstore"
)

// loadOpenCodeConsoleAPIKey checks the dedicated user-config secret first,
// then the environment names used by LazyCoder and existing OpenCode setups.
func loadOpenCodeConsoleAPIKey() (string, error) {
	store, err := secretstore.DefaultOpenCodeAPIKey()
	if err != nil {
		return "", err
	}
	apiKey, err := store.Load()
	if err == nil {
		return apiKey, nil
	}
	if !errors.Is(err, secretstore.ErrNotFound) {
		return "", err
	}
	for _, name := range []string{"OPENCODE_API_KEY", "OPENCODE_ZEN_API_KEY"} {
		if apiKey := strings.TrimSpace(os.Getenv(name)); apiKey != "" {
			return apiKey, nil
		}
	}
	return "", secretstore.ErrNotFound
}

func saveOpenCodeConsoleAPIKey(apiKey string) error {
	store, err := secretstore.DefaultOpenCodeAPIKey()
	if err != nil {
		return err
	}
	return store.Save(apiKey)
}

func deleteOpenCodeConsoleAPIKey() error {
	store, err := secretstore.DefaultOpenCodeAPIKey()
	if err != nil {
		return err
	}
	return store.Delete()
}
