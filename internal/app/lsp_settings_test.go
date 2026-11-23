package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

func TestLSPSettingsRoundTripAndOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := `{"lsp":{"enabled":false,"servers":{
		"go":{"command":"/opt/gopls","args":["-v"]},
		"pyright":{"disabled":true},
		"zls":{"command":"zls","extensions":["zig"]},
		"bad":{"command":"x\ny"}}}}`

	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}

	if s.LSP == nil || s.LSP.Enabled == nil || *s.LSP.Enabled {
		t.Fatalf("lsp block = %+v", s.LSP)
	}

	t.Setenv(lspEnvOff, "")

	if lspEnabled(s.LSP) {
		t.Fatal("enabled=false was ignored")
	}

	reg := lspRegistry(s.LSP)

	cfg, ok := reg.ForPath("main.go")
	if !ok || cfg.Command != "/opt/gopls" || len(cfg.Args) != 1 || cfg.Args[0] != "-v" {
		t.Fatalf("go override = %+v %v", cfg, ok)
	}

	if _, ok := reg.ForPath("x.py"); ok {
		t.Fatal("disabled pyright still matches")
	}

	if cfg, ok := reg.ForPath("main.zig"); !ok || cfg.Command != "zls" {
		t.Fatalf("custom server = %+v %v", cfg, ok)
	}

	if srv := s.LSP.Servers["bad"]; srv.Command != "" {
		t.Fatalf("control characters survived: %q", srv.Command)
	}

	// Saving keeps the block.
	if err := saveSettings(path, s); err != nil {
		t.Fatal(err)
	}

	back, _ := os.ReadFile(path) //nolint:gosec // temp file
	if !strings.Contains(string(back), `"lsp"`) {
		t.Fatalf("saved settings lost lsp: %s", back)
	}
}

func TestLSPDefaultsAndEnvSwitch(t *testing.T) {
	t.Setenv(lspEnvOff, "")

	if !lspEnabled(nil) {
		t.Fatal("LSP should default to on")
	}

	t.Setenv(lspEnvOff, "off")

	if lspEnabled(nil) {
		t.Fatal("env switch ignored")
	}
}

func TestNewLSPDiagConvertsColumns(t *testing.T) {
	// Tab expands to the display tab width; the emoji is two UTF-16 units but
	// one rune, so LSP character 5 is rune column 3.
	lines := []string{"\tx😀y"}
	d := newLSPDiag(lines, lsp.Diagnostic{
		Range:    lsp.Range{Start: lsp.Position{Character: 4}, End: lsp.Position{Character: 5}},
		Severity: lsp.SeverityWarning, Message: "m",
	})

	tab := len([]rune(expandTabs("\t")))
	if d.From != tab+2 || d.To != tab+3 || d.Severity != lsp.SeverityWarning {
		t.Fatalf("diag = %+v, want from %d to %d", d, tab+2, tab+3)
	}

	// Zero-width and past-the-end ranges still cover one cell.
	z := newLSPDiag([]string{"ab"}, lsp.Diagnostic{Range: lsp.Range{Start: lsp.Position{Character: 9}, End: lsp.Position{Character: 9}}})
	if z.To <= z.From || z.Severity != lsp.SeverityError {
		t.Fatalf("zero-width diag = %+v", z)
	}
}

func TestLSPInitOptionsOverride(t *testing.T) {
	t.Setenv(lspEnvOff, "")

	reg := lspRegistry(&lspSettings{Servers: map[string]lspServerSettings{
		"css": {InitOptions: map[string]any{"provideFormatter": false}},
	}})

	cfg, ok := reg.ForPath("a.scss")
	if !ok || cfg.InitOptions["provideFormatter"] != false {
		t.Fatalf("css override = %+v %v", cfg, ok)
	}

	html, _ := lspRegistry(nil).ForPath("a.html")
	if html.InitOptions["provideFormatter"] != true {
		t.Fatalf("html default = %+v", html)
	}
}
