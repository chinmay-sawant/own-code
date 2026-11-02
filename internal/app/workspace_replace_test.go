package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// replaceTestApp opens a disk workspace with the given files and returns the
// app with the search query staged.
func replaceTestApp(t *testing.T, files map[string]string) *App {
	t.Helper()

	dir := t.TempDir()
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := app.Page().Redraw(context.Background()); err != nil {
		t.Fatal(err)
	}

	return app
}

func readDisk(t *testing.T, app *App, name string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(app.ws.Root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}

func TestWorkspaceReplaceApplyAndUndo(t *testing.T) {
	app := replaceTestApp(t, map[string]string{
		"a.txt": "hello world\nhello again\n",
		"b.txt": "say hello\n",
		"c.txt": "nothing here\n",
	})
	ctx := context.Background()

	app.searchQuery = "hello"
	app.searchReplace = "goodbye"

	plan, err := app.replaceRescan(ctx)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}

	if plan.Total != 3 {
		t.Fatalf("total = %d, want 3", plan.Total)
	}

	if err := app.applyWorkspaceReplace(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := readDisk(t, app, "a.txt"); got != "goodbye world\ngoodbye again\n" {
		t.Fatalf("a.txt = %q", got)
	}

	if got := readDisk(t, app, "b.txt"); got != "say goodbye\n" {
		t.Fatalf("b.txt = %q", got)
	}

	if got := readDisk(t, app, "c.txt"); got != "nothing here\n" {
		t.Fatalf("c.txt changed: %q", got)
	}

	if len(app.replaceJournal) != 2 {
		t.Fatalf("journal = %d entries, want 2", len(app.replaceJournal))
	}

	if !app.replaceJournalOK {
		t.Fatal("journal not undoable")
	}

	if err := app.undoWorkspaceReplace(ctx); err != nil {
		t.Fatalf("undo: %v", err)
	}

	if got := readDisk(t, app, "a.txt"); got != "hello world\nhello again\n" {
		t.Fatalf("a.txt after undo = %q", got)
	}

	if got := readDisk(t, app, "b.txt"); got != "say hello\n" {
		t.Fatalf("b.txt after undo = %q", got)
	}

	if len(app.replaceJournal) != 0 {
		t.Fatal("journal not cleared after undo")
	}
}

func TestWorkspaceReplaceSkips(t *testing.T) {
	dir := t.TempDir()
	writeSearchFile(t, dir, "ok.txt", "needle here\n")
	writeSearchFile(t, dir, "bin.dat", "needle\x00binary\n")
	writeSearchFile(t, dir, "skipme.txt", "needle here\n")

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := app.Page().Redraw(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Mark one file gitignored without needing a git repository.
	app.ws.ByPath["skipme.txt"].Ignored = true
	// And one file oversized via metadata.
	app.ws.ByPath["ok.txt"].Size = maxFileBytes + 1
	app.ws.ByPath["ok.txt"].Big = true

	ctx := context.Background()
	app.searchQuery = "needle"
	app.searchReplace = "thread"

	if err := app.applyWorkspaceReplace(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := readDisk(t, app, "ok.txt"); got != "needle here\n" {
		t.Fatalf("large file written: %q", got)
	}

	if got := readDisk(t, app, "skipme.txt"); got != "needle here\n" {
		t.Fatalf("ignored file written: %q", got)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "bin.dat"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(raw), "needle") {
		t.Fatal("binary file written")
	}

	skipped := app.replaceSkipped
	if skipped.Large != 1 || skipped.Ignored != 1 || skipped.Binary != 1 {
		t.Fatalf("skipped = %+v", skipped)
	}

	if skipped.String() == "" {
		t.Fatal("no skipped summary")
	}
}

func TestWorkspaceReplaceDirtyBlocks(t *testing.T) {
	app := replaceTestApp(t, map[string]string{
		"a.txt": "hello\n",
		"b.txt": "hello\n",
	})
	ctx := context.Background()

	app.searchQuery = "hello"
	app.searchReplace = "goodbye"
	app.dirty["b.txt"] = true

	if err := app.applyWorkspaceReplace(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The whole gesture refuses: no partial apply.
	if got := readDisk(t, app, "a.txt"); got != "hello\n" {
		t.Fatalf("partial apply wrote a.txt: %q", got)
	}

	if len(app.replaceJournal) != 0 {
		t.Fatal("journal written despite dirty block")
	}
}

func TestWorkspaceReplaceSampleBlocked(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	app.searchQuery = "Beacon"
	app.searchReplace = "Signal"

	if err := app.openReplaceConfirm(ctx); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	if app.confirmOpen {
		t.Fatal("confirm opened in the sample workspace")
	}

	if err := app.applyWorkspaceReplace(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if len(app.replaceJournal) != 0 {
		t.Fatal("journal written in the sample workspace")
	}
}

func TestWorkspaceReplaceCeiling(t *testing.T) {
	app := replaceTestApp(t, map[string]string{
		"big.txt": strings.Repeat("needle\n", workspaceReplaceCeil+1),
	})
	ctx := context.Background()

	app.searchQuery = "needle"
	app.searchReplace = "thread"

	if _, err := app.replaceRescan(ctx); err == nil {
		t.Fatal("rescan past the ceiling succeeded")
	}

	if err := app.applyWorkspaceReplace(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := readDisk(t, app, "big.txt"); strings.Contains(got, "thread") {
		t.Fatal("ceiling exceeded but file was written")
	}
}

func TestWorkspaceReplaceCRLF(t *testing.T) {
	app := replaceTestApp(t, map[string]string{
		"win.txt": "hello\r\nworld\r\n",
	})
	ctx := context.Background()

	app.searchQuery = "hello"
	app.searchReplace = "goodbye"

	if err := app.applyWorkspaceReplace(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := readDisk(t, app, "win.txt"); got != "goodbye\r\nworld\r\n" {
		t.Fatalf("CRLF not preserved: %q", got)
	}
}

func TestWorkspaceReplaceRegex(t *testing.T) {
	app := replaceTestApp(t, map[string]string{
		"r.txt": "ab cd\n",
	})
	ctx := context.Background()

	app.searchQuery = `(a)(b)`
	app.searchReplace = "$2$1"
	app.searchRegex = true

	if err := app.applyWorkspaceReplace(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := readDisk(t, app, "r.txt"); got != "ba cd\n" {
		t.Fatalf("r.txt = %q", got)
	}
}

func TestWorkspaceReplaceCheckbox(t *testing.T) {
	app := replaceTestApp(t, map[string]string{
		"a.txt": "hello\n",
		"b.txt": "hello\n",
	})
	ctx := context.Background()

	app.searchQuery = "hello"
	app.searchReplace = "goodbye"
	app.searchHits = []workspaceHit{
		{Path: "a.txt", Line: 1, Col: 1, Match: "hello"},
		{Path: "b.txt", Line: 1, Col: 1, Match: "hello"},
	}

	app.toggleReplaceFile(1)

	if app.fileChecked("b.txt") {
		t.Fatal("b.txt still checked")
	}

	if !app.fileChecked("a.txt") {
		t.Fatal("a.txt unchecked")
	}

	if err := app.applyWorkspaceReplace(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := readDisk(t, app, "a.txt"); got != "goodbye\n" {
		t.Fatalf("a.txt = %q", got)
	}

	if got := readDisk(t, app, "b.txt"); got != "hello\n" {
		t.Fatalf("b.txt = %q", got)
	}
}

func TestWorkspaceReplaceConfirm(t *testing.T) {
	app := replaceTestApp(t, map[string]string{
		"a.txt": "hello\n",
		"b.txt": "hello hello\n",
	})
	ctx := context.Background()

	app.searchQuery = "hello"
	app.searchReplace = "goodbye"

	if err := app.openReplaceConfirm(ctx); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	if !app.confirmOpen || app.confirmKind != confirmKindReplaceAll {
		t.Fatalf("confirm open=%v kind=%q", app.confirmOpen, app.confirmKind)
	}

	view := app.confirmViewState()
	if view.OkLabel != "Replace" {
		t.Fatalf("ok label = %q", view.OkLabel)
	}

	if !strings.Contains(app.confirmTitle, "3 in 2 files") {
		t.Fatalf("title = %q", app.confirmTitle)
	}

	if err := app.confirmOK(ctx); err != nil {
		t.Fatalf("confirm ok: %v", err)
	}

	if app.confirmOpen {
		t.Fatal("confirm still open")
	}

	if got := readDisk(t, app, "b.txt"); got != "goodbye goodbye\n" {
		t.Fatalf("b.txt = %q", got)
	}
}

func TestWorkspaceSearchInvalidRegex(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	app.searchQuery = "(unclosed"
	app.searchRegex = true
	app.runWorkspaceSearch(ctx)

	if app.searchError == "" {
		t.Fatal("no inline error for invalid regex")
	}

	if len(app.searchHits) != 0 || app.searchBusy {
		t.Fatal("search ran with an invalid pattern")
	}
}

func TestWorkspaceReplaceJournalCap(t *testing.T) {
	// Twelve ~400 KB files (under the 512 KB scan limit) with two matches
	// each push the journal past its 5 MB cap: the apply still writes, but
	// workspace-undo disables itself with a note.
	big := strings.Repeat("x\n", 200000) + "needle\nneedle\n"
	files := map[string]string{}
	for i := 0; i < 12; i++ {
		files["f"+string(rune('a'+i))+".txt"] = big
	}
	app := replaceTestApp(t, files)
	ctx := context.Background()

	app.searchQuery = "needle"
	app.searchReplace = "thread"

	if err := app.applyWorkspaceReplace(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := readDisk(t, app, "fa.txt"); !strings.Contains(got, "thread") {
		t.Fatal("fa.txt not replaced")
	}

	if app.replaceJournalOK {
		t.Fatal("journal still undoable past the cap")
	}

	if app.replaceJournalMsg == "" {
		t.Fatal("no note for the disabled undo")
	}

	if err := app.undoWorkspaceReplace(ctx); err != nil {
		t.Fatalf("undo: %v", err)
	}

	if got := readDisk(t, app, "fa.txt"); !strings.Contains(got, "thread") {
		t.Fatal("disabled undo changed the file")
	}
}

func TestWorkspaceReplaceLazySweep(t *testing.T) {
	// A lazy workspace starts with unloaded subdirectories. The apply
	// sweeps them instead of silently missing their matches.
	app := replaceTestApp(t, map[string]string{
		"top.txt":      "hello\n",
		"sub/deep.txt": "hello\n",
	})
	ctx := context.Background()

	if !app.ws.Lazy {
		t.Skip("workspace is not lazy")
	}

	loaded := false
	for _, meta := range app.ws.Files {
		if meta.Path == "sub/deep.txt" {
			loaded = true
		}
	}

	if loaded {
		t.Skip("subdirectory already loaded")
	}

	app.searchQuery = "hello"
	app.searchReplace = "goodbye"

	if err := app.applyWorkspaceReplace(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := readDisk(t, app, "sub/deep.txt"); got != "goodbye\n" {
		t.Fatalf("swept file not replaced: %q", got)
	}
}
