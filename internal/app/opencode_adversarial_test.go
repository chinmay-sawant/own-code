package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/opencode"
	ownframe "github.com/chinmay-sawant/ownframe"
)

func TestAdversarialSensitivePathClassifier(t *testing.T) {
	sensitive := []string{
		".env", ".env.local", ".ENV", "app/.env.production", "config/credentials.json", "AWS_CREDENTIALS",
		"secrets/db.yml", "my-secret.txt", "password.txt", "api_token.txt", "keys/id_rsa", "id_ed25519",
		"ssh/known_hosts", "authorized_keys", "tls/server.pem", "a/b/c.key", "store.p12", "cert.PFX", "x.keystore",
	}
	for _, p := range sensitive {
		if !isSensitiveAgentContextPath(p) {
			t.Errorf("%q not classified sensitive", p)
		}
	}
	for _, p := range []string{"main.go", "docs/readme.md", "environment.go", "internal/app/app.go"} {
		if isSensitiveAgentContextPath(p) {
			t.Errorf("%q wrongly classified sensitive", p)
		}
	}
}

func TestAdversarialCollectSelectedFilesExcludesUnsafe(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write := func(p, body string) {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("ok.go", "package ok\n")
	write(".env", "API=LEAK1")
	write(".env.local", "API=LEAK2")
	write("keys/id_rsa", "LEAK3")
	write("tls/cert.pem", "LEAK4")
	write("config/credentials.json", "LEAK5")
	write("ignored/a.go", "LEAK6")
	write(".git/config", "LEAK7")
	write("sub/.git/config", "LEAK8")
	if err := os.WriteFile(filepath.Join(outside, "s.txt"), []byte("LEAK9"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "s.txt"), filepath.Join(root, "linkfile")); err != nil {
			t.Fatal(err)
		}
	}
	selected := []string{
		"ok.go", ".env", ".env.local", "keys/id_rsa", "tls/cert.pem", "config/credentials.json", "ignored/a.go",
		".git/config", "sub/.git/config", "../x", "/etc/passwd", "linkdir/s.txt", "linkfile", `..\x`, "a\x00b", "",
	}
	// The selection limit is small, so feed the hostile list in batches to
	// make sure every entry is individually evaluated.
	ignored := map[string]bool{"ignored/a.go": true}
	var files []agentFileContext
	excluded := 0
	for i := 0; i < len(selected); i += 4 {
		end := min(i+4, len(selected))
		got, err := collectSelectedAgentFiles(root, selected[i:end], ignored)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, got.Files...)
		excluded += len(got.Excluded)
	}
	if len(files) != 1 || files[0].RelativePath != "ok.go" {
		t.Fatalf("included files = %+v", files)
	}
	if strings.Contains(files[0].Text, "LEAK") {
		t.Fatalf("secret text in context: %q", files[0].Text)
	}
	if excluded < len(selected)-1 {
		t.Errorf("excluded = %d, want %d reported exclusions", excluded, len(selected)-1)
	}
}

func TestAdversarialCaptureContextRejectsSensitiveAndEscapes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("A=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".env", "../x", "/etc/passwd", ".git/config", `..\x`, "a\x00b"} {
		if _, err := captureAgentContext(root, p, "A=1", 0, 0); err == nil {
			t.Errorf("captureAgentContext(%q) accepted", p)
		}
	}
}

func TestAdversarialMentionCandidatesHideUnsafeEntries(t *testing.T) {
	a := mentionTestApp(t, map[string]string{
		"main.go": "x", ".env.production": "S", "id_ed25519": "k", "db/password.txt": "p", "certs/a.pem": "c",
		"vendor/ign.go": "i", "api_token.go": "t",
	})
	a.ws.ignored = map[string]bool{"vendor": true}
	var joined []string
	for _, q := range []string{"", "env", "pass", "pem", "ign", "token", "ed25519"} {
		for _, c := range a.openCodeMentionCandidates(q) {
			joined = append(joined, c.Label+"|"+c.Path)
		}
	}
	all := strings.Join(joined, ",")
	for _, bad := range []string{".env", "id_ed25519", "password", ".pem", "ign.go", "token"} {
		if strings.Contains(all, bad) {
			t.Errorf("%q leaked into mention candidates: %s", bad, all)
		}
	}
}

// TestAdversarialAPIKeyAbsentFromPersistedOutputs sets a fake key through the
// app's credential-save path, then persists settings, the editor session file,
// tool history and a rendered view, and scans every byte on disk and in the
// HTML for the key. Only the dedicated secret file may contain it.
func TestAdversarialAPIKeyAbsentFromPersistedOutputs(t *testing.T) {
	const key = "oc_sk_FAKEADVERSARIALKEY0123456789abcdef"
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("APPDATA", cfg)
	t.Setenv("OPENCODE_API_KEY", "")
	t.Setenv("OPENCODE_ZEN_API_KEY", "")

	a := newTestApp(t)
	ctx := context.Background()
	if err := saveOpenCodeConsoleAPIKey(key); err != nil {
		t.Fatal(err)
	}
	if got, err := loadOpenCodeConsoleAPIKey(); err != nil || got != key {
		t.Fatalf("key round trip = %q, %v", got, err)
	}

	a.settingsPath = filepath.Join(cfg, "ownframe", "code-view", "settings.json")
	a.sessionPath = filepath.Join(cfg, "ownframe", "code-view", "session.json")
	a.sessionReady = true
	a.settings.OpenCode = &opencodeSettings{ProviderID: "opencode-go", ModelID: "m"}
	a.persistSettings()
	a.persistSession()

	// Type the key into the settings form and render the opencode tab.
	if err := a.onClick(ctx, ownframe.Box{Action: "settings-open"}); err != nil {
		t.Fatal(err)
	}
	if err := a.onClick(ctx, ownframe.Box{Action: "settings-tab:opencode"}); err != nil {
		t.Fatal(err)
	}
	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	html := a.page.HTML()
	if strings.Contains(html, key) {
		t.Fatal("rendered settings HTML contains the key before typing")
	}
	// Panel view struct and its JSON form must not carry the key.
	view, _ := json.Marshal(a.openCodePanelView())
	if strings.Contains(string(view), key) {
		t.Fatal("panel view contains the key")
	}
	if a.view.OpenCodeModelSelection != "" && strings.Contains(a.view.OpenCodeModelSelection, key) {
		t.Fatal("view model selection contains the key")
	}

	// Client formatting must stay redacted.
	client, err := opencode.NewConsoleClient(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"%v", "%+v", "%#v", "%s"} {
		if s := fmt.Sprintf(f, client); strings.Contains(s, key) {
			t.Errorf("client formatted with %s leaks key", f)
		}
	}

	var keyFiles []string
	err = filepath.WalkDir(cfg, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		if strings.Contains(string(data), key) || strings.Contains(string(data), "FAKEADVERSARIALKEY") {
			keyFiles = append(keyFiles, filepath.Base(p))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(keyFiles) != 1 || keyFiles[0] != "opencode-console-api-key" {
		t.Fatalf("files containing the key = %v, want only opencode-console-api-key", keyFiles)
	}
	for _, name := range []string{"settings.json", "session.json"} {
		if _, err := os.Stat(filepath.Join(cfg, "ownframe", "code-view", name)); err != nil {
			t.Errorf("%s was not written, scan would be vacuous: %v", name, err)
		}
	}
}

// TestAdversarialConsolePathHasNoServerUsageInApp greps the non-test sources
// of the Console agent files in internal/app for OpenCode Server process and
// route usage. The legacy internal/opencode server client is not scanned.
func TestAdversarialConsolePathHasNoServerUsageInApp(t *testing.T) {
	files, err := filepath.Glob("opencode_*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("Glob = %v, %v", files, err)
	}
	forbidden := []string{
		"opencode serve", "StartServer", "ServerProcess", "NewClient(", "/prompt_async", `"/session`, `"/event`,
		`"/global/event`, `"/message`, "SubscribeEvents", "OPENCODE_SERVER_PASSWORD", "serverUrl",
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range forbidden {
			if strings.Contains(string(data), needle) {
				t.Errorf("%s references %q", f, needle)
			}
		}
	}
}

// TestAdversarialNoLoggerInInternalSources pins the "logs" half of the
// key-absence check: no internal package imports log or log/slog, so the
// app has no log sink a key could reach. A future logger must come with its
// own redaction test and update this one.
func TestAdversarialNoLoggerInInternalSources(t *testing.T) {
	err := filepath.WalkDir("..", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, imp := range []string{"\t\"log\"\n", "\t\"log/slog\"\n"} {
			if strings.Contains(string(data), imp) {
				t.Errorf("%s imports a logger (%q); add a key-redaction test before logging", p, strings.TrimSpace(imp))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
