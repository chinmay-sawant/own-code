package lsp

import (
	"path/filepath"
	"strings"
	"sync"
)

// ServerConfig describes how to launch a language server.
type ServerConfig struct {
	Name        string   `json:"name"`
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	LanguageIDs []string `json:"languages"`
	Extensions  []string `json:"extensions"` // with leading dot, lower case
	RootMarkers []string `json:"rootMarkers,omitempty"`
	Disabled    bool     `json:"disabled,omitempty"`
}

// DefaultServers returns the built-in server table.
func DefaultServers() []ServerConfig {
	return []ServerConfig{
		{Name: "gopls", Command: "gopls", LanguageIDs: []string{"go"}, Extensions: []string{".go"}, RootMarkers: []string{"go.work", "go.mod"}},
		{Name: "typescript-language-server", Command: "typescript-language-server", Args: []string{"--stdio"},
			LanguageIDs: []string{"typescript", "typescriptreact", "javascript", "javascriptreact"},
			Extensions:  []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}, RootMarkers: []string{"tsconfig.json", "package.json"}},
		{Name: "pyright", Command: "pyright-langserver", Args: []string{"--stdio"}, LanguageIDs: []string{"python"},
			Extensions: []string{".py", ".pyi"}, RootMarkers: []string{"pyproject.toml", "setup.py", "pyrightconfig.json"}},
		{Name: "rust-analyzer", Command: "rust-analyzer", LanguageIDs: []string{"rust"}, Extensions: []string{".rs"}, RootMarkers: []string{"Cargo.toml"}},
		{Name: "clangd", Command: "clangd", LanguageIDs: []string{"c", "cpp"},
			Extensions: []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh"}, RootMarkers: []string{"compile_commands.json", ".clangd"}},
	}
}

// Registry maps files to server configs. Safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	servers []ServerConfig
}

// NewRegistry returns a registry holding the default servers.
func NewRegistry() *Registry { return &Registry{servers: DefaultServers()} }

// Set adds a config, replacing any with the same Name. Extensions are normalized.
func (r *Registry) Set(c ServerConfig) {
	for i, e := range c.Extensions {
		e = strings.ToLower(e)
		if e != "" && !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		c.Extensions[i] = e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.servers {
		if r.servers[i].Name == c.Name {
			r.servers[i] = c
			return
		}
	}
	r.servers = append(r.servers, c)
}

// Servers returns a copy of all configs.
func (r *Registry) Servers() []ServerConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]ServerConfig(nil), r.servers...)
}

// ForPath returns the first enabled config whose extensions match path.
func (r *Registry) ForPath(path string) (ServerConfig, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return ServerConfig{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.servers {
		if s.Disabled {
			continue
		}
		for _, e := range s.Extensions {
			if e == ext {
				return s, true
			}
		}
	}
	return ServerConfig{}, false
}

// LanguageID returns the LSP languageId for path under config s.
func (s ServerConfig) LanguageID(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".ts", ".mjs", ".cjs":
		if s.Name == "typescript-language-server" {
			if ext == ".ts" {
				return "typescript"
			}
			return "javascript"
		}
	case ".tsx":
		return "typescriptreact"
	case ".js":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".c", ".h":
		if s.Name == "clangd" {
			return "c"
		}
	case ".cc", ".cpp", ".cxx", ".hpp", ".hh":
		return "cpp"
	}
	if len(s.LanguageIDs) > 0 {
		return s.LanguageIDs[0]
	}
	return ""
}

// FindRoot walks up from dir looking for any root marker, using exists to
// test for a path. It returns fallback when none is found.
func FindRoot(dir string, markers []string, fallback string, exists func(string) bool) string {
	for d := dir; ; {
		for _, m := range markers {
			if exists(filepath.Join(d, m)) {
				return d
			}
		}
		p := filepath.Dir(d)
		if p == d {
			return fallback
		}
		d = p
	}
}
