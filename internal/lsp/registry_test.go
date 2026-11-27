package lsp

import "testing"

func TestRegistryWebDefaults(t *testing.T) {
	reg := NewRegistry()

	cases := []struct {
		path, server, lang string
	}{
		{"a/index.html", "vscode-html-language-server", "html"},
		{"a/index.HTM", "vscode-html-language-server", "html"},
		{"s.css", "vscode-css-language-server", "css"},
		{"s.scss", "vscode-css-language-server", "scss"},
		{"s.less", "vscode-css-language-server", "less"},
		{"package.json", "vscode-json-language-server", "json"},
		{".eslintrc.jsonc", "vscode-json-language-server", "jsonc"},
		{"README.md", "marksman", "markdown"},
		{"x.markdown", "marksman", "markdown"},
		{"ci.yaml", "yaml-language-server", "yaml"},
		{"ci.yml", "yaml-language-server", "yaml"},
		{"a.ts", "typescript-language-server", "typescript"},
		{"a.tsx", "typescript-language-server", "typescriptreact"},
		{"a.js", "typescript-language-server", "javascript"},
		{"a.jsx", "typescript-language-server", "javascriptreact"},
		{"a.mjs", "typescript-language-server", "javascript"},
		{"a.cjs", "typescript-language-server", "javascript"},
		{"main.go", "gopls", "go"},
	}

	for _, c := range cases {
		cfg, ok := reg.ForPath(c.path)
		if !ok || cfg.Name != c.server {
			t.Errorf("ForPath(%q) = %q, %v; want %q", c.path, cfg.Name, ok, c.server)

			continue
		}

		if got := cfg.LanguageID(c.path); got != c.lang {
			t.Errorf("LanguageID(%q) = %q, want %q", c.path, got, c.lang)
		}
	}
}

func TestRegistryWebServerCommands(t *testing.T) {
	reg := NewRegistry()

	for _, name := range []string{"vscode-html-language-server", "vscode-css-language-server", "vscode-json-language-server"} {
		var found ServerConfig

		for _, s := range reg.Servers() {
			if s.Name == name {
				found = s
			}
		}

		if found.Command != name || len(found.Args) != 1 || found.Args[0] != "--stdio" {
			t.Errorf("%s = %+v", name, found)
		}

		if v, _ := found.InitOptions["provideFormatter"].(bool); !v {
			t.Errorf("%s lacks provideFormatter init option", name)
		}
	}

	for _, s := range reg.Servers() {
		switch s.Name {
		case "marksman":
			if s.Command != "marksman" || len(s.Args) != 1 || s.Args[0] != "server" {
				t.Errorf("marksman = %+v", s)
			}
		case "yaml-language-server":
			if len(s.Args) != 1 || s.Args[0] != "--stdio" {
				t.Errorf("yaml = %+v", s)
			}
		}
	}
}

func TestRegistryNoExtensionCollisions(t *testing.T) {
	seen := map[string]string{}

	for _, s := range DefaultServers() {
		for _, e := range s.Extensions {
			if prev, dup := seen[e]; dup {
				t.Errorf("extension %s claimed by %s and %s", e, prev, s.Name)
			}

			seen[e] = s.Name
		}
	}
}

func TestFindRootFallsBackWithoutMarker(t *testing.T) {
	got := FindRoot("/ws/docs", []string{".marksman.toml"}, "/ws", func(string) bool { return false })
	if got != "/ws" {
		t.Fatalf("root = %q, want fallback /ws", got)
	}
}
