package app

import (
	"os"
	"reflect"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

// lspSettings is the "lsp" block of settings.json. Servers are launched only
// from this block and the built-in registry, never from workspace files, so
// opening an untrusted repository cannot run anything.
//
//	"lsp": {
//	  "enabled": true,
//	  "servers": { "gopls": { "command": "/opt/gopls", "args": ["-remote=auto"] } }
//	}
//
// A server key is a registry name ("gopls") or a language id ("go").
type lspSettings struct {
	// Enabled is nil (default on), or an explicit true/false.
	Enabled *bool                        `json:"enabled,omitempty"`
	Servers map[string]lspServerSettings `json:"servers,omitempty"`
}

// lspServerSettings overrides one registry entry. A key that names no
// registry entry defines a new server when Command and Extensions are set.
type lspServerSettings struct {
	Command    string   `json:"command,omitempty"`
	Args       []string `json:"args,omitempty"`
	Disabled   bool     `json:"disabled,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
	Languages  []string `json:"languages,omitempty"`
	// InitOptions replaces the server's initializationOptions.
	InitOptions map[string]any `json:"initializationOptions,omitempty"`
}

const (
	lspMaxServerOverrides = 32
	lspMaxArgs            = 32
	lspMaxArgLen          = 512
)

// lspEnvOff names the environment switch that disables LSP regardless of
// settings. Tests set it so a gopls on PATH is never spawned.
const lspEnvOff = "OWN_CODE_LSP"

// sanitize bounds the block: control characters and empty commands drop.
func (s *lspSettings) sanitize() *lspSettings {
	if s == nil {
		return nil
	}

	out := &lspSettings{Enabled: s.Enabled}

	for name, srv := range s.Servers {
		if len(out.Servers) >= lspMaxServerOverrides {
			break
		}

		name = strings.TrimSpace(name)
		if name == "" || len(name) > 64 {
			continue
		}

		srv.Command = strings.TrimSpace(srv.Command)
		if strings.ContainsAny(srv.Command, "\x00\r\n") {
			srv.Command = ""
		}

		if len(srv.Args) > lspMaxArgs {
			srv.Args = srv.Args[:lspMaxArgs]
		}

		args := make([]string, 0, len(srv.Args))

		for _, arg := range srv.Args {
			if len(arg) <= lspMaxArgLen && !strings.ContainsRune(arg, 0) {
				args = append(args, arg)
			}
		}

		srv.Args = args

		if out.Servers == nil {
			out.Servers = map[string]lspServerSettings{}
		}

		out.Servers[name] = srv
	}

	return out
}

// lspEnabled resolves the master switch: env off, then settings, default on.
func lspEnabled(s *lspSettings) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(lspEnvOff))) {
	case "0", "off", "false", "no":
		return false
	}

	if s == nil || s.Enabled == nil {
		return true
	}

	return *s.Enabled
}

// lspRegistry builds the server table: built-in defaults plus user overrides.
func lspRegistry(s *lspSettings) *lsp.Registry {
	reg := lsp.NewRegistry()
	if s == nil {
		return reg
	}

	for key, over := range s.Servers {
		matched := false

		for _, cfg := range reg.Servers() {
			if !lspServerMatches(cfg, key) {
				continue
			}

			matched = true

			if over.Command != "" {
				cfg.Command = over.Command
				// A new command without args drops the default args, which
				// belong to the default binary.
				if over.Args == nil {
					cfg.Args = nil
				}
			}

			if over.Args != nil {
				cfg.Args = over.Args
			}

			if over.InitOptions != nil {
				cfg.InitOptions = over.InitOptions
			}

			cfg.Disabled = over.Disabled
			reg.Set(cfg)
		}

		if !matched && over.Command != "" && len(over.Extensions) > 0 && !over.Disabled {
			reg.Set(lsp.ServerConfig{
				Name:        key,
				Command:     over.Command,
				Args:        over.Args,
				LanguageIDs: over.Languages,
				Extensions:  append([]string(nil), over.Extensions...),
				InitOptions: over.InitOptions,
			})
		}
	}

	return reg
}

func lspServerMatches(cfg lsp.ServerConfig, key string) bool {
	if strings.EqualFold(cfg.Name, key) {
		return true
	}

	for _, id := range cfg.LanguageIDs {
		if strings.EqualFold(id, key) {
			return true
		}
	}

	return false
}

func lspSettingsEqual(a, b *lspSettings) bool { return reflect.DeepEqual(a, b) }

// lspInstallHint is the one-line advice shown when a binary is missing.
func lspInstallHint(name string) string {
	switch name {
	case "gopls":
		return "go install golang.org/x/tools/gopls@latest"
	case "typescript-language-server":
		return "npm i -g typescript-language-server typescript"
	case "pyright":
		return "npm i -g pyright"
	case "rust-analyzer":
		return "rustup component add rust-analyzer"
	case "vscode-html-language-server", "vscode-css-language-server", "vscode-json-language-server", "yaml-language-server":
		return "npm i -g typescript typescript-language-server vscode-langservers-extracted yaml-language-server"
	case "marksman":
		return "install marksman from https://github.com/artempyanykh/marksman"
	case "clangd":
		return "install clangd with your package manager"
	}

	return "install it and make sure it is on PATH"
}
