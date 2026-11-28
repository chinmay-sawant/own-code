// Package secretstore keeps small application credentials outside project
// settings and workspace data.
package secretstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	fileName = "opencode-server-password"
)

var ErrNotFound = errors.New("OpenCode credential is not configured")

// OpenCodePassword stores the Basic-auth password used to attach to an
// OpenCode Server. It is deliberately separate from settings.json and the
// workbench SQLite database.
type OpenCodePassword struct {
	path string
}

// DefaultOpenCodePassword returns a store under the user's application config
// directory.
func DefaultOpenCodePassword() (*OpenCodePassword, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("secretstore: resolve user config directory: %w", err)
	}
	return NewOpenCodePassword(filepath.Join(root, configDirName, fileName))
}

// NewOpenCodePassword creates a store at path. This constructor is useful for
// tests and alternate application config locations.
func NewOpenCodePassword(path string) (*OpenCodePassword, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("secretstore: empty OpenCode password path")
	}
	return &OpenCodePassword{path: path}, nil
}

// Load returns the stored server password. It refuses symlinks and non-regular
// files so a config-path substitution cannot redirect secret reads.
func (s *OpenCodePassword) Load() (string, error) {
	if s == nil || s.path == "" {
		return "", errors.New("secretstore: OpenCode password store is not configured")
	}

	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("secretstore: inspect OpenCode password file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("secretstore: OpenCode password path is not a regular file")
	}

	data, err := os.ReadFile(s.path) //nolint:gosec // path is built from the per-user config directory
	if err != nil {
		return "", fmt.Errorf("secretstore: read OpenCode password file: %w", err)
	}
	if len(data) == 0 {
		return "", ErrNotFound
	}
	return string(data), nil
}

// Save atomically stores password with owner-only directory and file modes.
func (s *OpenCodePassword) Save(password string) error {
	if s == nil || s.path == "" {
		return errors.New("secretstore: OpenCode password store is not configured")
	}
	if password == "" {
		return errors.New("secretstore: OpenCode password cannot be empty")
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("secretstore: create OpenCode password directory: %w", err)
	}
	if err := os.Chmod(dir, dirMode); err != nil {
		return fmt.Errorf("secretstore: secure OpenCode password directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".opencode-password-*.tmp")
	if err != nil {
		return fmt.Errorf("secretstore: create temporary OpenCode password file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secretstore: secure temporary OpenCode password file: %w", err)
	}
	if _, err := tmp.WriteString(password); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secretstore: write OpenCode password file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secretstore: sync OpenCode password file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("secretstore: close OpenCode password file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("secretstore: replace OpenCode password file: %w", err)
	}
	return nil
}

// Delete removes the stored server password. A missing password is already
// the desired state.
func (s *OpenCodePassword) Delete() error {
	if s == nil || s.path == "" {
		return errors.New("secretstore: OpenCode password store is not configured")
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("secretstore: remove OpenCode password file: %w", err)
	}
	return nil
}
