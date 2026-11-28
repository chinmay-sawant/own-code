package secretstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const openCodeAPIKeyFileName = "opencode-console-api-key"

// OpenCodeAPIKey stores the workspace-bound OpenCode Console key outside
// project files, settings.json, and the SQLite session database.
type OpenCodeAPIKey struct {
	path string
}

func DefaultOpenCodeAPIKey() (*OpenCodeAPIKey, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("secretstore: resolve user config directory: %w", err)
	}
	return NewOpenCodeAPIKey(filepath.Join(root, configDirName, openCodeAPIKeyFileName))
}

func NewOpenCodeAPIKey(path string) (*OpenCodeAPIKey, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("secretstore: empty OpenCode API key path")
	}
	return &OpenCodeAPIKey{path: path}, nil
}

func (s *OpenCodeAPIKey) Load() (string, error) {
	if s == nil || s.path == "" {
		return "", errors.New("secretstore: OpenCode API key store is not configured")
	}
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("secretstore: inspect OpenCode API key file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("secretstore: OpenCode API key path is not a regular file")
	}
	data, err := os.ReadFile(s.path) //nolint:gosec // path is under the per-user config directory
	if err != nil {
		return "", fmt.Errorf("secretstore: read OpenCode API key file: %w", err)
	}
	if len(data) == 0 {
		return "", ErrNotFound
	}
	return string(data), nil
}

func (s *OpenCodeAPIKey) Save(apiKey string) error {
	if s == nil || s.path == "" {
		return errors.New("secretstore: OpenCode API key store is not configured")
	}
	if strings.TrimSpace(apiKey) == "" {
		return errors.New("secretstore: OpenCode API key cannot be empty")
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("secretstore: create OpenCode API key directory: %w", err)
	}
	if err := os.Chmod(dir, dirMode); err != nil {
		return fmt.Errorf("secretstore: secure OpenCode API key directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".opencode-api-key-*.tmp")
	if err != nil {
		return fmt.Errorf("secretstore: create temporary OpenCode API key file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secretstore: secure temporary OpenCode API key file: %w", err)
	}
	if _, err := tmp.WriteString(strings.TrimSpace(apiKey)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secretstore: write OpenCode API key file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secretstore: sync OpenCode API key file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("secretstore: close OpenCode API key file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("secretstore: replace OpenCode API key file: %w", err)
	}
	return nil
}

func (s *OpenCodeAPIKey) Delete() error {
	if s == nil || s.path == "" {
		return errors.New("secretstore: OpenCode API key store is not configured")
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("secretstore: remove OpenCode API key file: %w", err)
	}
	return nil
}
