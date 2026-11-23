package app

import (
	"os"
	"path/filepath"
	"testing"
)

func newFormatTestApp(t *testing.T) *App {
	t.Helper()

	app := newTestApp(t)
	app.openFile("backend/probe.go")
	app.toggleEdit()

	if app.edit == nil {
		t.Fatal("no edit buffer")
	}

	return app
}

func TestFormatOnSaveGofmt(t *testing.T) {
	if _, err := os.Stat("/usr/local/go/bin/gofmt"); err != nil {
		t.Skip("gofmt not installed")
	}

	app := newFormatTestApp(t)
	app.edit.SetText("package main\nfunc  main( ) {\nx:=1\n_=x\n}\n")
	app.editCaret, app.editAnchor = 0, 0

	app.formatEditForSave(app.edit.Path)

	want := "package main\n\nfunc main() {\n\tx := 1\n\t_ = x\n}\n"
	if got := app.edit.Text(); got != want {
		t.Fatalf("formatted = %q, want %q", got, want)
	}
}

func TestFormatOnSaveSyntaxErrorKeepsText(t *testing.T) {
	app := newFormatTestApp(t)
	src := "package main\nfunc ( {\n"
	app.edit.SetText(src)
	app.formatEditForSave(app.edit.Path)

	if got := app.edit.Text(); got != src {
		t.Fatalf("text changed on formatter failure: %q", got)
	}
}

func TestFormatOnSaveDisabledAndMissingTool(t *testing.T) {
	app := newFormatTestApp(t)
	src := "package main\nfunc  main( ) {}\n"
	app.edit.SetText(src)

	off := false
	app.settings.Format = &formatSettings{OnSave: &off}
	app.formatEditForSave(app.edit.Path)

	if app.edit.Text() != src {
		t.Fatal("formatted while disabled")
	}

	app.settings.Format = (&formatSettings{Formatters: map[string]formatterSpec{"go": {Command: "definitely-not-a-real-formatter"}}}).sanitize()
	app.formatEditForSave(app.edit.Path)

	if app.edit.Text() != src {
		t.Fatal("missing tool changed the text")
	}
}

func TestFormatterConfiguredCommand(t *testing.T) {
	app := newFormatTestApp(t)

	script := filepath.Join(t.TempDir(), "upper.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntr a-z A-Z\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	app.settings.Format = (&formatSettings{Formatters: map[string]formatterSpec{"GO": {Command: script}}}).sanitize()
	app.edit.SetText("abc\n")
	app.formatEditForSave(app.edit.Path)

	if got := app.edit.Text(); got != "ABC\n" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatterForDefaults(t *testing.T) {
	app := newTestApp(t)

	if s, ok := app.formatterFor("a/b.go"); !ok || s.Command != "gofmt" {
		t.Fatalf("go = %+v %v", s, ok)
	}

	if s, ok := app.formatterFor("a/b.tsx"); !ok || s.Command != "prettier" {
		t.Fatalf("tsx = %+v %v", s, ok)
	}

	if _, ok := app.formatterFor("a/b.txt"); ok {
		t.Fatal("txt should have no formatter")
	}
}
