package secretstore

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenCodePasswordSaveLoadDelete(t *testing.T) {
	store, err := NewOpenCodePassword(filepath.Join(t.TempDir(), "config", fileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load() before Save() error = %v, want ErrNotFound", err)
	}

	const password = "server-only-secret"
	if err := store.Save(password); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != password {
		t.Fatalf("Load() = %q, want saved password", got)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(store.path)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := info.Mode().Perm(), os.FileMode(fileMode); got != want {
			t.Fatalf("password file mode = %#o, want %#o", got, want)
		}
		dirInfo, err := os.Stat(filepath.Dir(store.path))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := dirInfo.Mode().Perm(), os.FileMode(dirMode); got != want {
			t.Fatalf("password directory mode = %#o, want %#o", got, want)
		}
	}

	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load() after Delete() error = %v, want ErrNotFound", err)
	}
}

func TestOpenCodePasswordRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated privileges on Windows")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, fileName)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	store, err := NewOpenCodePassword(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("Load() followed a symlink")
	}
}
