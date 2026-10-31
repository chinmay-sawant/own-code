package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPaletteAbsolutePathOpensExternalFile verifies that pasting an absolute
// path opens that file in a new tab without adding it to the workspace scan.
func TestPaletteAbsolutePathOpensExternalFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Workspace\n")

	external := t.TempDir()
	target := filepath.Join(external, "notes.txt")
	if err := os.WriteFile(target, []byte("outside line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	abs := filepath.ToSlash(target)
	filesBefore := len(app.ws.Files)
	ctx := context.Background()

	if err := app.openPalette(ctx); err != nil {
		t.Fatal(err)
	}

	app.paletteQuery = `"` + abs + `"`
	app.refresh()

	rows := app.paletteRows()
	if len(rows) != 1 || rows[0].Path != abs {
		t.Fatalf("rows = %+v, want one row for %s", rows, abs)
	}

	if err := app.onSubmit(ctx); err != nil {
		t.Fatal(err)
	}

	if app.View().Active != abs || len(app.tabs) != 2 || app.tabs[1] != abs {
		t.Fatalf("active = %q tabs = %v, want %s open", app.View().Active, app.tabs, abs)
	}

	doc := app.docs[abs]
	if doc == nil || doc.Err != "" {
		t.Fatalf("doc = %+v", doc)
	}
	if got := strings.Join(doc.Lines, "\n"); got != "outside line" {
		t.Fatalf("doc text = %q", got)
	}
	if app.ws.ByPath[abs] == nil {
		t.Fatal("external file was not registered in ByPath")
	}
	if got := len(app.ws.Files); got != filesBefore {
		t.Fatalf("workspace file count = %d, want %d", got, filesBefore)
	}
}

// TestPaletteAbsolutePathUnderRootStaysRelative verifies that an absolute path
// inside the workspace resolves to the normal relative path.
func TestPaletteAbsolutePathUnderRootStaysRelative(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Workspace\n")
	writeFile(t, dir, "pkg/inside.go", "package pkg\n")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	abs := filepath.ToSlash(filepath.Join(dir, "pkg", "inside.go"))

	app.paletteQuery = abs
	rows := app.paletteRows()

	if len(rows) != 1 || rows[0].Path != "pkg/inside.go" {
		t.Fatalf("rows = %+v, want pkg/inside.go", rows)
	}

	app.openFile(rows[0].Path)

	if app.View().Active != "pkg/inside.go" {
		t.Fatalf("active = %q, want pkg/inside.go", app.View().Active)
	}
	if doc := app.docs["pkg/inside.go"]; doc == nil || doc.Err != "" {
		t.Fatalf("doc = %+v", doc)
	}
}

// TestPaletteAbsolutePathRejectsNonEditable verifies that directories, missing
// paths, and binary extensions do not resolve to a palette row.
func TestPaletteAbsolutePathRejectsNonEditable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Workspace\n")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	external := t.TempDir()
	png := filepath.Join(external, "image.png")
	if err := os.WriteFile(png, []byte("not really a png"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{external, png, filepath.Join(external, "missing.txt")} {
		if row, ok := app.absolutePaletteRow(path); ok {
			t.Fatalf("%s resolved to %+v", path, row)
		}
	}
}

// TestExternalFileSaveWritesOutsideRoot verifies that editing an external file
// saves back to its absolute path.
func TestExternalFileSaveWritesOutsideRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Workspace\n")

	external := t.TempDir()
	target := filepath.Join(external, "notes.txt")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	abs := filepath.ToSlash(target)
	app.openFile(abs)
	app.toggleEdit()

	ctx := context.Background()
	if err := app.onSelectAll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := app.onPaste(ctx, "after\n"); err != nil {
		t.Fatal(err)
	}

	app.saveEdit()

	raw, err := os.ReadFile(target) //nolint:gosec // target is a t.TempDir fixture
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != "after\n" {
		t.Fatalf("saved text = %q", got)
	}
}
