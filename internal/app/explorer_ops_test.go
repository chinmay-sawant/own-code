package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanExplorerRel(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"a.go", "a.go", true},
		{"sub/dir", "sub/dir", true},
		{"sub/../a.go", "a.go", true},
		{"", "", false},
		{"   ", "", false},
		{".", "", false},
		{"..", "", false},
		{"../a.go", "", false},
		{"sub/../../a.go", "", false},
		{"/abs/a.go", "", false},
		{"a\x00.go", "", false},
	} {
		got, ok := cleanExplorerRel(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("cleanExplorerRel(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestValidateExplorerName(t *testing.T) {
	for _, name := range []string{"a.go", "my notes.md", "dir-name", ".gitignore", "a..b", "a.b.c"} {
		if err := validateExplorerName(name); err != nil {
			t.Errorf("validateExplorerName(%q) = %v, want nil", name, err)
		}
	}

	for _, name := range []string{
		"", "   ", ".", "..", "a/b", `a\b`, "a\x00b",
		"trailing ", "trailing.", " leading",
		strings.Repeat("x", 256),
	} {
		if err := validateExplorerName(name); err == nil {
			t.Errorf("validateExplorerName(%q) = nil, want an error", name)
		}
	}
}

func TestWorkspaceOpsTraversal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"createDir", func() error { return app.ws.createDir("../evil") }},
		{"rename", func() error { return app.ws.renamePath("a.go", "../evil.go") }},
		{"remove", func() error { return app.ws.removePath("../evil.go") }},
	} {
		if err := tc.run(); err == nil {
			t.Errorf("%s with traversal = nil, want an error", tc.name)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "evil")); !os.IsNotExist(err) {
		t.Fatal("traversal created a path outside the root")
	}
}

func TestWorkspaceOpsSampleReadOnly(t *testing.T) {
	app := newTestApp(t)

	if err := app.ws.createDir("newdir"); err == nil {
		t.Fatal("sample createDir = nil, want read-only")
	}

	if err := app.ws.renamePath("README.md", "other.md"); err == nil {
		t.Fatal("sample renamePath = nil, want read-only")
	}

	if err := app.ws.removePath("README.md"); err == nil {
		t.Fatal("sample removePath = nil, want read-only")
	}
}

func TestWorkspaceOpsNeverOverwrite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n")
	writeFile(t, dir, "b.go", "package b\n")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := app.ws.renamePath("a.go", "b.go"); err == nil {
		t.Fatal("rename onto an existing file = nil, want an error")
	}

	if err := app.ws.createDir("sub"); err != nil {
		t.Fatalf("createDir: %v", err)
	}

	if err := app.ws.createDir("sub"); err == nil {
		t.Fatal("second createDir = nil, want an error")
	}

	got, err := os.ReadFile(filepath.Join(dir, "b.go")) //nolint:gosec // path is a t.TempDir fixture
	if err != nil || string(got) != "package b\n" {
		t.Fatalf("b.go changed by the refused rename: %q (%v)", got, err)
	}
}

func TestCreateExplorerFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sub/b.go", "package sub\n")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	rel, err := app.createExplorerFile("sub", "fresh.go")
	if err != nil {
		t.Fatalf("createExplorerFile: %v", err)
	}

	if rel != "sub/fresh.go" {
		t.Fatalf("rel = %q, want sub/fresh.go", rel)
	}

	app.refreshExplorerAfterMutation([]string{rel}, nil, nil)

	if _, err := os.Stat(filepath.Join(dir, "sub", "fresh.go")); err != nil {
		t.Fatalf("fresh.go missing on disk: %v", err)
	}

	if app.ws.ByPath["sub/fresh.go"] == nil {
		t.Fatal("sub/fresh.go missing from the workspace index")
	}

	if findTreeNode(app.ws.Tree, "sub/fresh.go") == nil {
		t.Fatal("sub/fresh.go missing from the tree")
	}

	if _, err := app.createExplorerFile("sub", "fresh.go"); err == nil {
		t.Fatal("duplicate create = nil, want a file-exists error")
	}
}

func TestCreateExplorerDirRoundTrip(t *testing.T) {
	dir := t.TempDir()

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	rel, err := app.createExplorerDir("", "assets")
	if err != nil {
		t.Fatalf("createExplorerDir: %v", err)
	}

	app.refreshExplorerAfterMutation([]string{rel}, nil, nil)

	info, err := os.Stat(filepath.Join(dir, "assets"))
	if err != nil || !info.IsDir() {
		t.Fatalf("assets missing on disk: %v", err)
	}

	if node := findTreeNode(app.ws.Tree, "assets"); node == nil || !node.Dir {
		t.Fatal("assets missing from the tree")
	}
}

func TestRenameExplorerPathMigratesTabs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "old.go", "package main\n")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	app.openFile("old.go")

	next, err := app.renameExplorerPath("old.go", "new.go")
	if err != nil {
		t.Fatalf("renameExplorerPath: %v", err)
	}

	if next != "new.go" {
		t.Fatalf("next = %q, want new.go", next)
	}

	app.refreshExplorerAfterMutation([]string{next}, nil, map[string]string{"old.go": "new.go"})

	if _, err := os.Stat(filepath.Join(dir, "new.go")); err != nil {
		t.Fatalf("new.go missing on disk: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "old.go")); !os.IsNotExist(err) {
		t.Fatal("old.go still on disk after the rename")
	}

	if len(app.tabs) != 1 || app.tabs[0] != "new.go" {
		t.Fatalf("tabs = %v, want [new.go]", app.tabs)
	}

	if app.docs["new.go"] == nil || app.docs["old.go"] != nil {
		t.Fatal("open document did not migrate to the new path")
	}

	if findTreeNode(app.ws.Tree, "new.go") == nil || findTreeNode(app.ws.Tree, "old.go") != nil {
		t.Fatal("tree did not migrate to the new path")
	}
}

func TestDuplicateExplorerPathSuffix(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.md", "hello\n")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	next, err := app.duplicateExplorerPath("notes.md")
	if err != nil {
		t.Fatalf("duplicateExplorerPath: %v", err)
	}

	if next != "notes (copy).md" {
		t.Fatalf("next = %q, want notes (copy).md", next)
	}

	app.refreshExplorerAfterMutation([]string{next}, nil, nil)

	got, err := os.ReadFile(filepath.Join(dir, next)) //nolint:gosec // path is a t.TempDir fixture
	if err != nil || string(got) != "hello\n" {
		t.Fatalf("duplicate bytes = %q (%v)", got, err)
	}

	again, err := app.duplicateExplorerPath("notes.md")
	if err != nil {
		t.Fatalf("second duplicate: %v", err)
	}

	if again != "notes (copy 2).md" {
		t.Fatalf("again = %q, want notes (copy 2).md", again)
	}
}

func TestDeleteExplorerConfirmFlow(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "gone.go", "package main\n")
	writeFile(t, dir, "stay.go", "package stay\n")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	app.openFile("gone.go")
	app.openFile("stay.go")

	app.openExplorerDeleteConfirm([]string{"gone.go"})

	if !app.confirmOpen || app.confirmKind != confirmExplorerDelete {
		t.Fatalf("confirm = open %v kind %q, want the explorer-delete dialog", app.confirmOpen, app.confirmKind)
	}

	view := app.confirmViewState()
	if view.OkLabel != "Delete" {
		t.Fatalf("OkLabel = %q, want Delete", view.OkLabel)
	}

	if err := app.confirmDiscard(ctx); err != nil {
		t.Fatalf("confirmDiscard: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "gone.go")); !os.IsNotExist(err) {
		t.Fatal("gone.go still on disk after the confirmed delete")
	}

	for _, tab := range app.tabs {
		if tab == "gone.go" {
			t.Fatalf("tabs = %v, want gone.go closed", app.tabs)
		}
	}

	if app.docs["gone.go"] != nil {
		t.Fatal("gone.go document survived the delete")
	}

	if toast := app.View().Toast; !toast.Show || toast.Kind != "success" {
		t.Fatalf("toast = %+v, want a visible success", toast)
	}
}

func TestExplorerDeleteSampleGuard(t *testing.T) {
	app := newTestApp(t)
	before := len(app.ws.Files)

	app.openExplorerDeleteConfirm([]string{"README.md"})

	if app.confirmOpen {
		t.Fatal("sample workspace opened the delete dialog")
	}

	if toast := app.View().Toast; !toast.Show || toast.Kind != "error" {
		t.Fatalf("toast = %+v, want a visible error", toast)
	}

	if len(app.ws.Files) != before {
		t.Fatal("sample workspace changed after a guarded delete")
	}
}

func TestExplorerMenuFileOpRows(t *testing.T) {
	clearStagedCopy()
	defer clearStagedCopy()

	app := newTestApp(t)

	labels := []string{"New File", "New Folder", "-", "Cut", "Copy", "Copy Path", "Copy Absolute Path", "Paste", "-", "Duplicate", "Rename", "Delete"}
	rows := wantExplorerRows(t, app, "tree-backend/probe.go", labels)

	for i, row := range rows {
		if row.Label == "-" {
			if row.ID != "" || row.Enabled {
				t.Fatalf("separator row %d = %+v, want an empty disabled id", i, row)
			}

			continue
		}

		if !strings.HasPrefix(row.ID, "code-view:") {
			t.Fatalf("row %d = %+v, want the code-view: prefix", i, row)
		}
	}

	if rows[9].ID != "code-view:duplicate:backend/probe.go" {
		t.Fatalf("duplicate id = %q", rows[9].ID)
	}

	if rows[10].ID != "code-view:rename:backend/probe.go" || rows[10].Hint != "F2" {
		t.Fatalf("rename row = %+v, want the rename id with the F2 hint", rows[10])
	}

	if rows[11].ID != "code-view:delete:backend/probe.go" || rows[11].Hint != "Del" {
		t.Fatalf("delete row = %+v, want the delete id with the Del hint", rows[11])
	}
}

func TestExplorerMenuEmptySpaceFileOpRows(t *testing.T) {
	clearStagedCopy()
	defer clearStagedCopy()

	app := newTestApp(t)

	labels := []string{"New File", "New Folder", "-", "Paste"}
	rows := wantExplorerRows(t, app, "tree-pane", labels)

	if rows[0].ID != "code-view:new-file:" || rows[1].ID != "code-view:new-folder:" {
		t.Fatalf("new rows = %q %q", rows[0].ID, rows[1].ID)
	}

	if rows[3].ID != "code-view:paste:" || rows[3].Enabled {
		t.Fatalf("paste row = %+v, want disabled", rows[3])
	}
}

func TestExplorerMenuDeleteConfirm(t *testing.T) {
	clearStagedCopy()
	defer clearStagedCopy()

	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	if err := app.MenuAction(ctx, "code-view:delete:a.go"); err != nil {
		t.Fatalf("MenuAction: %v", err)
	}

	if !app.confirmOpen || app.confirmKind != confirmExplorerDelete {
		t.Fatalf("confirm = open %v kind %q, want the explorer-delete dialog", app.confirmOpen, app.confirmKind)
	}

	if err := app.MenuAction(ctx, "code-view:duplicate:a.go"); err != nil {
		t.Fatalf("duplicate MenuAction: %v", err)
	}

	if app.ws.ByPath["a (copy).go"] == nil {
		t.Fatal("a (copy).go missing after the duplicate menu row")
	}
}

func TestPaletteCommandMode(t *testing.T) {
	app := newTestApp(t)

	app.paletteQuery = ">"
	rows := app.paletteRows()
	if len(rows) != len(paletteCommandCatalog()) {
		t.Fatalf("command rows = %d, want %d", len(rows), len(paletteCommandCatalog()))
	}

	for _, row := range rows {
		if row.Cmd == "" {
			t.Fatalf("row %+v has no command id", row)
		}
	}

	app.paletteQuery = ">fold"
	rows = app.paletteRows()
	if len(rows) != 1 || rows[0].Cmd != "new-folder" {
		t.Fatalf("filtered rows = %+v, want the new-folder command", rows)
	}

	app.paletteQuery = "probe"
	rows = app.paletteRows()
	for _, row := range rows {
		if row.Cmd != "" {
			t.Fatalf("file row %+v carries a command id", row)
		}
	}
}

func TestPaletteNewFileCommandOpensInput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	app.paletteOpen = true
	if err := app.runPaletteCommand(ctx, "new-file"); err != nil {
		t.Fatalf("runPaletteCommand: %v", err)
	}

	if !app.explorerInput.Open || app.explorerInput.Mode != explorerInputNewFile {
		t.Fatalf("input = %+v, want an open new-file prompt", app.explorerInput)
	}

	if app.paletteOpen {
		t.Fatal("palette stayed open after the command ran")
	}

	view := app.View()
	if !view.ExplorerInputOpen || view.ExplorerInputTitle != "New file" {
		t.Fatalf("view input = open %v title %q", view.ExplorerInputOpen, view.ExplorerInputTitle)
	}
}

func TestExplorerInlineInputCommitAndCancel(t *testing.T) {
	dir := t.TempDir()

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	if err := app.beginExplorerInput(ctx, explorerInputNewFile, "", ""); err != nil {
		t.Fatalf("begin: %v", err)
	}

	app.explorerInput.Value = "bad/name"
	if err := app.commitExplorerInput(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if !app.explorerInput.Open || app.explorerInput.Error == "" {
		t.Fatalf("input = %+v, want it open with an inline error", app.explorerInput)
	}

	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("invalid name wrote to disk: %v", entries)
	}

	app.explorerInput.Value = "good.go"
	if err := app.commitExplorerInput(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if app.explorerInput.Open {
		t.Fatal("input stayed open after a successful commit")
	}

	if _, err := os.Stat(filepath.Join(dir, "good.go")); err != nil {
		t.Fatalf("good.go missing on disk: %v", err)
	}

	if toast := app.View().Toast; !toast.Show || toast.Title != "File created" {
		t.Fatalf("toast = %+v, want the File created success", toast)
	}

	if err := app.beginExplorerInput(ctx, explorerInputNewFile, "", ""); err != nil {
		t.Fatalf("begin: %v", err)
	}

	if err := app.cancelExplorerInput(ctx); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if app.explorerInput.Open {
		t.Fatal("input stayed open after cancel")
	}
}

func TestExplorerOpReadyGate(t *testing.T) {
	clearStagedCopy()
	defer clearStagedCopy()

	app := newTestApp(t)
	ctx := context.Background()

	if err := app.Page().Form().Focus(ctx, "tree-backend/probe.go"); err != nil {
		t.Fatalf("focus: %v", err)
	}

	if !app.explorerOpReady() {
		t.Fatal("explorerOpReady is false on a focused tree row")
	}

	if err := app.Page().Form().Focus(ctx, "filter"); err != nil {
		t.Fatalf("focus: %v", err)
	}

	if app.explorerOpReady() {
		t.Fatal("explorerOpReady is true with the filter focused")
	}
}
