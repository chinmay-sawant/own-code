package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/secretstore"
)

var openCodeEnvKeyNames = []string{"OPENCODE_API_KEY", "OPENCODE_ZEN_API_KEY"}

func isOpenCodeTextFocus(id string) bool {
	switch id {
	case "opencode-prompt", "opencode-inline-instruction", "opencode-rename":
		return true
	default:
		return false
	}
}

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
	for _, name := range openCodeEnvKeyNames {
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

// openCodeEnvFilePaths returns candidate .env paths (workspace root first, then
// current working directory) without duplicates.
func (a *App) openCodeEnvFilePaths() []string {
	var paths []string
	seen := map[string]struct{}{}
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		p := filepath.Join(dir, ".env")
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if _, dup := seen[p]; !dup {
			seen[p] = struct{}{}
			paths = append(paths, p)
		}
	}
	if a != nil && a.ws != nil && !a.ws.Sample && strings.TrimSpace(a.ws.Root) != "" {
		add(a.ws.Root)
	}
	if wd, err := os.Getwd(); err == nil {
		add(wd)
	}
	return paths
}

// openCodePrimaryEnvFilePath returns the .env path used when saving a key.
func (a *App) openCodePrimaryEnvFilePath() string {
	paths := a.openCodeEnvFilePaths()
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

// loadOpenCodeKey resolves the Console API key from .env (workspace or cwd),
// then the dedicated user-config secret store, then process environment vars.
func (a *App) loadOpenCodeKey() (string, error) {
	dir := ""
	if a != nil && a.ws != nil && !a.ws.Sample {
		dir = a.ws.Root
	}
	return loadOpenCodeKeyForDir(dir)
}

func loadOpenCodeKeyForDir(dir string) (string, error) {
	var paths []string
	if strings.TrimSpace(dir) != "" {
		paths = append(paths, filepath.Join(dir, ".env"))
	}
	if wd, err := os.Getwd(); err == nil {
		p := filepath.Join(wd, ".env")
		if len(paths) == 0 || paths[0] != p {
			paths = append(paths, p)
		}
	}
	for _, path := range paths {
		if key, ok := loadOpenCodeKeyFromEnvFile(path); ok {
			return key, nil
		}
	}
	return loadOpenCodeConsoleAPIKey()
}

// saveOpenCodeKey persists the API key to the local .env file and the
// user-config secret store so restarts reconnect automatically.
func (a *App) saveOpenCodeKey(apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	var envErr error
	if path := a.openCodePrimaryEnvFilePath(); path != "" {
		envErr = saveOpenCodeKeyToEnvFile(path, apiKey)
	}
	storeErr := saveOpenCodeConsoleAPIKey(apiKey)
	if envErr == nil || storeErr == nil {
		return nil
	}
	return storeErr
}

// deleteOpenCodeKey removes the API key from both .env and the secret store.
func (a *App) deleteOpenCodeKey() error {
	for _, path := range a.openCodeEnvFilePaths() {
		_ = deleteOpenCodeKeyFromEnvFile(path)
	}
	return deleteOpenCodeConsoleAPIKey()
}

func loadOpenCodeKeyFromEnvFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	values := map[string]string{}
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if v != "" {
			values[k] = v
		}
	}
	for _, name := range openCodeEnvKeyNames {
		if v := strings.TrimSpace(values[name]); v != "" {
			return v, true
		}
	}
	return "", false
}

func saveOpenCodeKeyToEnvFile(path, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" || strings.ContainsAny(apiKey, "\r\n") {
		return errors.New("invalid API key")
	}
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		text := strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		if text != "" {
			lines = strings.Split(text, "\n")
		}
	}
	updated := false
	for i, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		trimmed = strings.TrimPrefix(trimmed, "export ")
		if k, _, ok := strings.Cut(trimmed, "="); ok && strings.TrimSpace(k) == "OPENCODE_API_KEY" {
			lines[i] = "OPENCODE_API_KEY=" + apiKey
			updated = true
		}
	}
	if !updated {
		lines = append(lines, "OPENCODE_API_KEY="+apiKey)
	}
	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o600)
}

func deleteOpenCodeKeyFromEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	text := strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	changed := false
	for i, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		trimmed = strings.TrimPrefix(trimmed, "export ")
		if k, _, ok := strings.Cut(trimmed, "="); ok {
			keyName := strings.TrimSpace(k)
			for _, target := range openCodeEnvKeyNames {
				if keyName == target {
					lines[i] = target + "="
					changed = true
				}
			}
		}
	}
	if !changed {
		return nil
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}
