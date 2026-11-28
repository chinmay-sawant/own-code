package agentreview

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAdversarialMapPathRejects(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{
		"", "../x", "a/../../x", "..", "/etc/passwd", `\etc\passwd`, `..\x`, "C:/x", `C:\x`,
		".git/config", "sub/.git/hooks/pre-commit", ".GIT/config", "a\x00b", "a\nb", "a\rb", ".",
	} {
		if abs, rel, err := MapPath(root, p); err == nil || !errors.Is(err, ErrOutside) {
			t.Errorf("MapPath(%q) = %q, %q, %v", p, abs, rel, err)
		}
	}
	// A normal nested path (not yet existing) is accepted and stays inside.
	abs, rel, err := MapPath(root, "pkg/new.go")
	if err != nil || rel != "pkg/new.go" || !filepath.IsAbs(abs) {
		t.Fatalf("MapPath ok = %q %q %v", abs, rel, err)
	}
}

func TestAdversarialMapPathSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "s"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "s"), filepath.Join(root, "file")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "real", "up")); err != nil {
		t.Fatal(err)
	}
	// Dangling link: target does not exist but the link itself is refused.
	if err := os.Symlink(filepath.Join(outside, "missing"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"dir/s", "dir/new", "file", "real/up/s", "dangling"} {
		if _, _, err := MapPath(root, p); !errors.Is(err, ErrOutside) {
			t.Errorf("MapPath(%q) err = %v, want ErrOutside", p, err)
		}
	}
}

func TestAdversarialBaselineIgnoresEscapingPaths(t *testing.T) {
	root := t.TempDir()
	b := &Baseline{Files: map[string]FileState{}}
	for _, p := range []string{"../x", "/etc/passwd", ".git/config"} {
		if err := b.RecordFile(root, p); err == nil {
			t.Errorf("RecordFile(%q) accepted", p)
		}
		if _, ok := b.Files[p]; ok {
			t.Errorf("baseline recorded %q", p)
		}
	}
}
