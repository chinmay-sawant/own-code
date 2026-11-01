package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/termui"
	"github.com/chinmay-sawant/ownframe"
)

// TestTermLinkPatterns covers path:line, path:line:col, absolute paths, and
// Python traceback forms plus the false positives that must stay plain text.
func TestTermLinkPatterns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		text     string
		wantPath string
		wantLine int
		wantCol  int
		wantNil  bool
	}{
		{name: "relative line", text: "internal/app/foo.go:42", wantPath: "internal/app/foo.go", wantLine: 42},
		{name: "relative line col", text: "src/main.py:10:3", wantPath: "src/main.py", wantLine: 10, wantCol: 3},
		{name: "absolute", text: "/home/user/project/main.go:12", wantPath: "/home/user/project/main.go", wantLine: 12},
		{name: "absolute col", text: "/tmp/x.go:7:2: error", wantPath: "/tmp/x.go", wantLine: 7, wantCol: 2},
		{name: "dot slash", text: "./rel/path.go:5", wantPath: "./rel/path.go", wantLine: 5},
		{name: "dotdot", text: "../up/file.go:9:1", wantPath: "../up/file.go", wantLine: 9, wantCol: 1},
		{name: "gcc trailing", text: "main.go:12:3: undefined: x", wantPath: "main.go", wantLine: 12, wantCol: 3},
		{name: "go vet", text: "pkg/foo.go:123:2: vet error", wantPath: "pkg/foo.go", wantLine: 123, wantCol: 2},
		{name: "python double", text: `File "main.py", line 12`, wantPath: "main.py", wantLine: 12},
		{name: "python nested", text: `File "src/app.py", line 7, in <module>`, wantPath: "src/app.py", wantLine: 7},
		{name: "python abs", text: `  File "/tmp/x.py", line 3, in foo`, wantPath: "/tmp/x.py", wantLine: 3},
		{name: "parens", text: "(internal/app/foo.go:42)", wantPath: "internal/app/foo.go", wantLine: 42},
		{name: "quotes", text: `"src/a.go:11"`, wantPath: "src/a.go", wantLine: 11},
		{name: "timestamp", text: "12:34", wantNil: true},
		{name: "timestamp full", text: "2024-01-01 12:34:56", wantNil: true},
		{name: "bare number", text: "123", wantNil: true},
		{name: "bare word colon", text: "error: something broke", wantNil: true},
		{name: "no digits", text: "foo:bar", wantNil: true},
		{name: "url", text: "https://example.com/foo.go:12", wantNil: true},
		{name: "url port", text: "http://localhost:8080/path", wantNil: true},
		{name: "decimal", text: "12.34:56", wantNil: true},
		{name: "meeting time", text: "Meeting at 12:30 tomorrow", wantNil: true},
		{name: "stdin", text: `File "<stdin>", line 1`, wantNil: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := termui.ParseTerminalLinks(tc.text)
			if tc.wantNil {
				if len(got) != 0 {
					t.Fatalf("ParseTerminalLinks(%q) = %+v, want nil", tc.text, got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("ParseTerminalLinks(%q) = %+v, want one link", tc.text, got)
			}
			l := got[0]
			if l.Path != tc.wantPath || l.Line != tc.wantLine || l.Col != tc.wantCol {
				t.Fatalf("ParseTerminalLinks(%q) = %+v, want %q:%d:%d", tc.text, l, tc.wantPath, tc.wantLine, tc.wantCol)
			}
		})
	}
}

// TestTermLinkRelativeResolution checks workspace-relative links resolve
// against the session cwd (the workspace root).
func TestTermLinkRelativeResolution(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# R\n")
	writeFile(t, dir, "sub/dir/file.go", "package p\n")

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	open, abs := app.resolveTermLink("sub/dir/file.go")
	if open != "sub/dir/file.go" {
		t.Fatalf("open = %q, want %q", open, "sub/dir/file.go")
	}
	if want := filepath.Join(dir, "sub", "dir", "file.go"); abs != want {
		t.Fatalf("abs = %q, want %q", abs, want)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("stat abs: %v", err)
	}

	openDot, _ := app.resolveTermLink("./sub/dir/file.go")
	if openDot != "sub/dir/file.go" {
		t.Fatalf("dot open = %q, want sub/dir/file.go", openDot)
	}
}

// TestTermLinkAbsolutePaths checks absolute inside the root relativize while
// outside paths stay absolute for external opens.
func TestTermLinkAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# R\n")
	writeFile(t, dir, "main.go", "package main\n")

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	inside := filepath.Join(dir, "main.go")
	open, abs := app.resolveTermLink(inside)
	if open != "main.go" {
		t.Fatalf("inside open = %q, want main.go", open)
	}
	if abs != inside {
		t.Fatalf("inside abs = %q, want %q", abs, inside)
	}

	outside := filepath.Join(t.TempDir(), "other.go")
	if err := os.WriteFile(outside, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	openOut, absOut := app.resolveTermLink(outside)
	if absOut != outside {
		t.Fatalf("outside abs = %q, want %q", absOut, outside)
	}
	if !filepath.IsAbs(openOut) {
		t.Fatalf("outside open = %q, want absolute", openOut)
	}
}

// termLinkTestView installs a one-row terminal view carrying raw text.
func termLinkTestView(app *App, raw string) (row, seg int, ok bool) {
	snap := terminalManualSnapshot(raw)
	view := terminalViewState(true, false, 100, 0, nil, snap, true, 4, 0)
	if len(view.Rows) == 0 {
		return 0, 0, false
	}
	app.view.Terminal = view
	for r, tr := range view.Rows {
		for s, part := range tr.Segments {
			if part.LinkPath != "" {
				return r, s, true
			}
		}
	}
	return 0, 0, false
}

// TestTermLinkClickOpensAtLine checks the click path opens the file at the
// parsed line through the preview opener with nav history.
func TestTermLinkClickOpensAtLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# R\n")
	writeFile(t, dir, "main.go", "one\ntwo\nthree\nfour\nfive\n")

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	ctx := context.Background()
	row, seg, ok := termLinkTestView(app, "main.go:3")
	if !ok {
		t.Fatal("link not rendered for main.go:3")
	}
	segView := app.view.Terminal.Rows[row].Segments[seg]
	if !strings.Contains(segView.Class, "underline") {
		t.Fatalf("link class = %q, want underline", segView.Class)
	}

	beforeNav := len(app.nav)
	if err := app.onClick(ctx, ownframe.Box{ID: "term-link-0-0", Action: "term-link:0:0"}); err != nil {
		t.Fatalf("onClick: %v", err)
	}
	_ = beforeNav

	if app.View().Active != "main.go" {
		t.Fatalf("active = %q, want main.go", app.View().Active)
	}
	// The open rides the preview path; goto enters edit, which auto-pins the
	// preview (VS Code behavior), so the slot clears and the file pins.
	if !app.isPinned(app.activeGroup(), "main.go") {
		t.Fatalf("main.go not pinned after edit; pinned=%v preview=%q", app.pinned[app.activeGroup()], app.previewTab[app.activeGroup()])
	}
	if !app.canNavBack() {
		t.Fatal("link open did not record nav history")
	}
	if !app.editing || app.edit == nil {
		t.Fatal("link open did not enter edit for goto")
	}
	line, _ := offsetLineCol(app.edit.Text(), app.editCaret)
	if line != 2 {
		t.Fatalf("caret line = %d, want 2 (file line 3)", line)
	}
}

// TestTermLinkMissingFileToast checks missing files toast instead of failing.
func TestTermLinkMissingFileToast(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# R\n")

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	ctx := context.Background()
	activeBefore := app.View().Active
	_, _, ok := termLinkTestView(app, "missing.go:12")
	if !ok {
		t.Fatal("link not rendered for missing.go:12")
	}
	if err := app.onClick(ctx, ownframe.Box{ID: "term-link-0-0", Action: "term-link:0:0"}); err != nil {
		t.Fatalf("onClick: %v", err)
	}
	toast := app.View().Toast
	if !toast.Show {
		t.Fatal("missing file did not toast")
	}
	if !strings.Contains(toast.Title+toast.Message, "not") && !strings.Contains(toast.Message, "not on disk") {
		t.Fatalf("toast = %+v, want file-not-found", toast)
	}
	if app.View().Active != activeBefore {
		t.Fatalf("active changed to %q on missing file", app.View().Active)
	}
}

// TestTermLinkScrollbackRender checks links survive scrollback: recomputed per
// render from visible row text, so scrolled windows still link.
func TestTermLinkScrollbackRender(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# R\n")
	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	lines := []string{"intro", "sub/dir/file.go:42", "middle", "other.go:7:2", "tail", "extra1", "extra2", "extra3"}
	snap := terminalManualSnapshot(lines...)

	live := terminalViewState(true, false, 100, 0, nil, snap, true, 4, 0)
	foundLive := false
	for _, r := range live.Rows {
		for _, s := range r.Segments {
			if s.LinkPath != "" {
				foundLive = true
			}
		}
	}
	if foundLive {
		t.Fatal("live tail should not show the old links")
	}

	oldest := terminalViewState(true, false, 100, 0, nil, snap, true, 4, 4)
	paths := map[string]bool{}
	for _, r := range oldest.Rows {
		for _, s := range r.Segments {
			if s.LinkPath != "" {
				paths[s.LinkPath] = true
				if !strings.Contains(s.Class, "underline") {
					t.Fatalf("scrolled link class = %q, want underline", s.Class)
				}
			}
		}
	}
	if !paths["sub/dir/file.go"] {
		t.Fatalf("expected sub/dir/file.go link in scrolled view, got %v", paths)
	}
	if !paths["other.go"] {
		t.Fatalf("expected other.go link in scrolled view, got %v", paths)
	}

	mid := terminalViewState(true, false, 100, 0, nil, snap, true, 4, 2)
	foundMid := false
	for _, r := range mid.Rows {
		for _, s := range r.Segments {
			if s.LinkPath == "other.go" {
				foundMid = true
			}
		}
	}
	if !foundMid {
		t.Fatalf("middle window missing other.go link: %+v", mid.Rows)
	}
}

// TestTermLinkFocusOwnership checks links are clicks only: terminal keeps key
// ownership and no key grab opens files.
func TestTermLinkFocusOwnership(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# R\n")
	writeFile(t, dir, "main.go", "one\ntwo\n")

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	ctx := context.Background()
	app.termOpen = true
	app.termFocus = true
	app.refresh()

	if !app.terminalOwnsKeys() {
		t.Fatal("terminal should own keys while focused")
	}
	activeBefore := app.View().Active
	if _, err := app.handleTerminalKey(ctx, "a"); err != nil {
		t.Fatalf("handleTerminalKey: %v", err)
	}
	if app.View().Active != activeBefore {
		t.Fatal("key press opened a file; links must be clicks only")
	}

	_, _, ok := termLinkTestView(app, "main.go:2")
	if !ok {
		t.Fatal("link not rendered")
	}
	app.termFocus = true
	if err := app.onClick(ctx, ownframe.Box{ID: "term-link-0-0", Action: "term-link:0:0"}); err != nil {
		t.Fatalf("onClick: %v", err)
	}
	if !app.termFocus {
		t.Fatal("link click dropped terminal focus")
	}
}

// TestTermLinkDirtyPreviewPreserved checks a dirty file is never lost when a
// terminal link opens: each link opens a new tab, so the dirty file stays
// open alongside the link target with its dirty flag intact.
func TestTermLinkDirtyPreviewPreserved(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# R\n")
	writeFile(t, dir, "a.go", "one\ntwo\n")
	writeFile(t, dir, "b.go", "one\ntwo\nthree\n")

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	app.openFileAtPreview(app.activeGroup(), "a.go", true)
	if app.previewTab[app.activeGroup()] != "a.go" {
		t.Fatalf("preview slot = %q, want a.go", app.previewTab[app.activeGroup()])
	}
	app.setDirty("a.go", true)

	ctx := context.Background()
	_, _, ok := termLinkTestView(app, "b.go:2")
	if !ok {
		t.Fatal("link not rendered for b.go:2")
	}
	if err := app.onClick(ctx, ownframe.Box{ID: "term-link-0-0", Action: "term-link:0:0"}); err != nil {
		t.Fatalf("onClick: %v", err)
	}
	if !app.pathOpenInTabs("a.go") {
		t.Fatal("dirty preview a.go was replaced by the link open")
	}
	if !app.pathOpenInTabs("b.go") {
		t.Fatal("link target b.go did not open")
	}
	if !app.isDirty("a.go") {
		t.Fatal("dirty flag for a.go was lost")
	}
}
