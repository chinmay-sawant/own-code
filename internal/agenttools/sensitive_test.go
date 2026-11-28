package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sensRun(t *testing.T, r *registry, tool string, args map[string]any) (Output, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	p, err := r.Prepare(tool, raw)
	if err != nil {
		return Output{}, err
	}
	return p.Execute(context.Background(), nil)
}

func sensFixture(t *testing.T) *registry {
	r, root := newReg(t)
	if err := exec.Command("git", "-C", root, "init", "-q").Run(); err != nil {
		t.Skip("git unavailable")
	}
	files := map[string]string{
		".gitignore": "ignored.txt\nbuild/\n", "ok.txt": "needle ok", ".env": "needle", ".env.local": "needle",
		"id_rsa": "needle", "k.pem": "needle", "credentials.json": "needle", ".npmrc": "needle",
		"secret_notes.txt": "needle", "ignored.txt": "needle", "build/out.txt": "needle", "sub/.env": "needle",
	}
	for n, c := range files {
		p := filepath.Join(root, n)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	return r
}

func TestSensitiveReadRefused(t *testing.T) {
	r := sensFixture(t)
	for _, n := range []string{".env", ".env.local", "id_rsa", "k.pem", "credentials.json", ".npmrc", "secret_notes.txt", "ignored.txt", "build/out.txt", "sub/.env"} {
		if _, err := sensRun(t, r, ToolReadFile, map[string]any{"path": n}); !errors.Is(err, ErrSensitive) {
			t.Errorf("%s: want ErrSensitive, got %v", n, err)
		}
	}
	if out, err := sensRun(t, r, ToolReadFile, map[string]any{"path": "ok.txt"}); err != nil || !strings.Contains(out.Text, "needle ok") {
		t.Errorf("ok.txt: %v %q", err, out.Text)
	}
}

func TestSensitiveListSearchSkip(t *testing.T) {
	r := sensFixture(t)
	out, err := sensRun(t, r, ToolListFiles, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{".env", "id_rsa", "k.pem", "credentials", ".npmrc", "secret", "ignored.txt", "build"} {
		if strings.Contains(out.Text, bad) {
			t.Errorf("list leaks %s: %q", bad, out.Text)
		}
	}
	if !strings.Contains(out.Text, "ok.txt") {
		t.Errorf("list missing ok.txt: %q", out.Text)
	}
	out, err = sensRun(t, r, ToolSearchFiles, map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.Text) != "ok.txt:1: needle ok" {
		t.Errorf("search leaks: %q", out.Text)
	}
	if _, err := sensRun(t, r, ToolListFiles, map[string]any{"path": "build"}); !errors.Is(err, ErrSensitive) {
		t.Errorf("list ignored dir: %v", err)
	}
}
