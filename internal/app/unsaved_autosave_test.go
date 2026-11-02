package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chinmay-sawant/own-code/internal/merge"
)

// dirtyTestFile opens path in a temp-dir app, enters edit mode, and types
// one character so the buffer is dirty. It returns the app.
func dirtyTestFile(t *testing.T, dir, path string) *App {
	t.Helper()

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	app.openFile(path)
	app.toggleEdit()

	if !app.editing {
		t.Fatalf("did not enter edit mode for %s", path)
	}

	if err := app.Page().Edit().Type(ctx, "x"); err != nil {
		t.Fatal(err)
	}

	if !app.isDirty(path) {
		t.Fatalf("%s is not dirty after typing", path)
	}

	return app
}

func diskText(t *testing.T, dir, path string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path))) //nolint:gosec // test helper reads a temp-dir fixture
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}

// TestUnsavedCloseGuard_Save checks the Save arm writes disk bytes, clears
// the flag, and closes the tab.
func TestUnsavedCloseGuard_Save(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	app := dirtyTestFile(t, dir, "main.go")
	ctx := context.Background()

	app.requestCloseTabIn(0, app.active)

	if !app.confirmOpen || app.confirmKind != unsavedCloseOne {
		t.Fatalf("dialog = open %v kind %q, want one-file", app.confirmOpen, app.confirmKind)
	}

	if len(app.confirmPending) != 1 || app.confirmPending[0].Path != "main.go" {
		t.Fatalf("pending = %+v, want main.go", app.confirmPending)
	}

	if err := app.confirmUnsavedSave(ctx); err != nil {
		t.Fatal(err)
	}

	if app.confirmOpen {
		t.Fatal("dialog stayed open after Save")
	}

	if !strings.Contains(diskText(t, dir, "main.go"), "x") {
		t.Fatal("Save did not write the typed text to disk")
	}

	if app.isDirty("main.go") {
		t.Fatal("dirty flag survived Save")
	}

	for _, tab := range append(append([]string(nil), app.tabs...), app.tabs2...) {
		if tab == "main.go" {
			t.Fatalf("tab still open after Save: %v", app.tabs)
		}
	}
}

// TestUnsavedCloseGuard_DontSave checks Don't Save closes the tab, reverts
// the document to disk bytes, and drops the flag.
func TestUnsavedCloseGuard_DontSave(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	app := dirtyTestFile(t, dir, "main.go")
	ctx := context.Background()

	app.requestCloseTabIn(0, app.active)

	if !app.confirmOpen {
		t.Fatal("no dialog for a dirty close")
	}

	if err := app.confirmUnsavedDontSave(ctx); err != nil {
		t.Fatal(err)
	}

	if got := diskText(t, dir, "main.go"); strings.Contains(got, "x") {
		t.Fatalf("Don't Save wrote to disk: %q", got)
	}

	if app.isDirty("main.go") {
		t.Fatal("dirty flag survived Don't Save")
	}

	if doc := app.docs["main.go"]; doc != nil && strings.Contains(strings.Join(doc.Lines, "\n"), "x") {
		t.Fatal("document kept the discarded text")
	}

	for _, tab := range append(append([]string(nil), app.tabs...), app.tabs2...) {
		if tab == "main.go" {
			t.Fatalf("tab still open after Don't Save: %v", app.tabs)
		}
	}
}

// TestUnsavedCloseGuard_Cancel checks Cancel keeps the tab, the text, and
// the dirty flag.
func TestUnsavedCloseGuard_Cancel(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	app := dirtyTestFile(t, dir, "main.go")

	before := len(app.tabs)
	app.requestCloseTabIn(0, app.active)

	if !app.confirmOpen {
		t.Fatal("no dialog for a dirty close")
	}

	app.closeConfirm()

	if app.confirmOpen {
		t.Fatal("dialog stayed open after Cancel")
	}

	if len(app.tabs) != before {
		t.Fatalf("tabs = %d, want %d after Cancel", len(app.tabs), before)
	}

	if !app.isDirty("main.go") {
		t.Fatal("Cancel cleared the dirty flag")
	}

	if !strings.Contains(app.edit.Text(), "x") {
		t.Fatal("Cancel lost the typed text")
	}
}

// TestUnsavedCloseMany checks a bulk close lists every dirty file and one
// Save writes them all.
func TestUnsavedCloseMany(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package a\n")
	writeFile(t, dir, "b.go", "package b\n")

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"a.go", "b.go"} {
		app.openFile(path)
		app.toggleEdit()

		if err := app.Page().Edit().Type(ctx, "x"); err != nil {
			t.Fatal(err)
		}

		app.finishEdit()
	}

	if len(app.dirtyTabs(-1)) != 2 {
		t.Fatalf("dirty = %v, want both files", app.dirtyTabs(-1))
	}

	app.requestCloseTabs(0, append([]string(nil), app.tabs...))

	if !app.confirmOpen || app.confirmKind != unsavedCloseMany {
		t.Fatalf("dialog = open %v kind %q, want many", app.confirmOpen, app.confirmKind)
	}

	if len(app.confirmPending) != 2 {
		t.Fatalf("pending = %+v, want 2", app.confirmPending)
	}

	if app.View().Confirm.Count != 2 {
		t.Fatalf("dialog count = %d, want 2", app.View().Confirm.Count)
	}

	if err := app.confirmUnsavedSave(ctx); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"a.go", "b.go"} {
		if !strings.Contains(diskText(t, dir, path), "x") {
			t.Fatalf("Save did not write %s", path)
		}

		if app.isDirty(path) {
			t.Fatalf("%s still dirty after bulk Save", path)
		}
	}

	if len(app.tabs) != 0 {
		t.Fatalf("tabs = %v, want all closed", app.tabs)
	}
}

// TestEvictDirtyTabPrompts checks tabs are unlimited: opening beyond the
// old cap appends without evicting or prompting, keeping dirty tabs intact.
func TestEvictDirtyTabPrompts(t *testing.T) {
	dir := t.TempDir()

	var names []string

	const n = 9
	for i := 0; i < n; i++ {
		name := string(rune('a'+i)) + ".go"
		names = append(names, name)
		writeFile(t, dir, name, "package p\n")
	}

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	for _, name := range names[:n-1] {
		app.openFile(name)
	}

	if len(app.tabs) != n-1 {
		t.Fatalf("tabs = %d, want %d", len(app.tabs), n-1)
	}

	oldest := app.tabs[0]

	app.openFile(oldest)
	app.toggleEdit()

	if err := app.Page().Edit().Type(ctx, "x"); err != nil {
		t.Fatal(err)
	}

	app.openFile(names[n-1])

	if app.confirmOpen {
		t.Fatal("unlimited tabs opened a dirty-evict prompt")
	}

	if !app.isDirty(oldest) {
		t.Fatal("dirty oldest lost its flag after an unlimited open")
	}

	foundOld, foundNew := false, false

	for _, tab := range app.tabs {
		if tab == names[n-1] {
			foundNew = true
		}

		if tab == oldest {
			foundOld = true
		}
	}

	if !foundNew {
		t.Fatalf("new file not open: %v", app.tabs)
	}

	if !foundOld {
		t.Fatalf("oldest tab evicted despite unlimited tabs: %v", app.tabs)
	}
}

// TestExplorerDirtyDot checks a dirty file dots its explorer row and a save
// clears both the row flag and the tab dot.
func TestExplorerDirtyDot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	app := dirtyTestFile(t, dir, "main.go")
	ctx := context.Background()
	app.refresh()

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	dirtyRow := false

	for _, row := range app.View().Tree {
		if row.Path == "main.go" {
			if !row.Dirty {
				t.Fatal("explorer row is not marked dirty")
			}

			dirtyRow = true
		}
	}

	if !dirtyRow {
		t.Fatal("main.go not visible in the explorer")
	}

	if _, ok := boxByID(app.Page().Boxes(), "tree-dot-main.go"); !ok {
		t.Fatal("explorer dirty dot box is missing")
	}

	app.saveDirtyActive()

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	for _, row := range app.View().Tree {
		if row.Path == "main.go" && row.Dirty {
			t.Fatal("explorer row stayed dirty after save")
		}
	}

	if _, ok := boxByID(app.Page().Boxes(), "tree-dot-main.go"); ok {
		t.Fatal("explorer dirty dot survived the save")
	}
}

// TestAutoSaveAfterDelay checks the debounce writes the file without any
// explicit save.
func TestAutoSaveAfterDelay(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	app.setAutoSave(autoSaveAfterDelay)
	app.setAutoSaveDelay(autoSaveMinDelay)
	app.openFile("main.go")
	app.toggleEdit()

	if err := app.Page().Edit().Type(ctx, "x"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(6 * time.Second)

	for {
		_ = app.tickWatch(ctx)

		if !app.isDirty("main.go") {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("debounced auto-save did not fire")
		}

		time.Sleep(50 * time.Millisecond)
	}

	if !strings.Contains(diskText(t, dir, "main.go"), "x") {
		t.Fatal("auto-save did not write the typed text")
	}
}

// TestAutoSaveFocusChange checks switching tabs saves the dirty file when
// the mode is onFocusChange, and never saves when off.
func TestAutoSaveFocusChange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package a\n")
	writeFile(t, dir, "b.go", "package b\n")

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	app.setAutoSave(autoSaveOnFocusChange)
	app.openFile("a.go")
	app.toggleEdit()

	if err := app.Page().Edit().Type(ctx, "x"); err != nil {
		t.Fatal(err)
	}

	app.openFile("b.go")

	if app.isDirty("a.go") {
		t.Fatal("focus change did not auto-save a.go")
	}

	if !strings.Contains(diskText(t, dir, "a.go"), "x") {
		t.Fatal("focus save did not write a.go")
	}

	// Off never autosaves on navigation.
	app.setAutoSave(autoSaveOff)
	app.openFile("a.go")
	app.toggleEdit()

	if err := app.Page().Edit().Type(ctx, "y"); err != nil {
		t.Fatal(err)
	}

	app.openFile("b.go")

	if !app.isDirty("a.go") {
		t.Fatal("off mode autosaved unexpectedly")
	}
}

// TestAutoSaveSessionRoundTrip checks the mode and delay survive a session
// snapshot, sanitize, and restore.
func TestAutoSaveSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	app.setAutoSave(autoSaveAfterDelay)
	app.setAutoSaveDelay(2000)

	state := app.sessionState().sanitize(dir)

	if state.AutoSave != autoSaveAfterDelay || state.AutoSaveDelay != 2000 {
		t.Fatalf("snapshot = %+v", state)
	}

	// Unknown modes fall back to off; wild delays clamp.
	weird := sessionState{Root: dir, AutoSave: "sometimes", AutoSaveDelay: 1}.sanitize(dir)
	if weird.AutoSave != autoSaveOff || weird.AutoSaveDelay != autoSaveMinDelay {
		t.Fatalf("sanitize = %+v", weird)
	}

	fresh, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	fresh.restoreState(state)

	if fresh.autoSave != autoSaveAfterDelay || fresh.autoSaveDelayMs != 2000 {
		t.Fatalf("restored = %q/%d", fresh.autoSave, fresh.autoSaveDelayMs)
	}
}

// TestMergeCloseGuard checks leaving a dirty merge result prompts, Save
// writes through the merge editor, and Don't Save leaves without writing.
func TestMergeCloseGuard(t *testing.T) {
	dir := textConflictRepo(t)
	app := newMergeTestApp(t, dir)
	ctx := context.Background()

	app.openMergeConflict(ctx, mergeTestPath)
	app.mergeAccept(merge.Ours, 0)

	if !app.mergeDirty {
		t.Fatal("accepting a side did not dirty the merge result")
	}

	app.requestCloseMergeEditor()

	if !app.confirmOpen || !app.confirmMerge {
		t.Fatalf("no merge guard dialog: open %v merge %v", app.confirmOpen, app.confirmMerge)
	}

	if len(app.confirmPending) != 1 || app.confirmPending[0].Path != mergeTestPath {
		t.Fatalf("pending = %+v", app.confirmPending)
	}

	if err := app.confirmUnsavedSave(ctx); err != nil {
		t.Fatal(err)
	}

	if app.mergeOpen {
		t.Fatal("merge editor stayed open after Save")
	}

	after, err := os.ReadFile(filepath.Join(dir, mergeTestPath)) //nolint:gosec // test helper reads a temp-dir fixture
	if err != nil {
		t.Fatal(err)
	}

	if string(after) == "" || !strings.Contains(string(after), "ours one") {
		t.Fatalf("merge save wrote unexpected bytes: %q", after)
	}

	// Don't Save leaves without writing.
	app.openMergeConflict(ctx, mergeTestPath)
	app.mergeAccept(merge.Theirs, 0)
	app.requestCloseMergeEditor()

	if !app.confirmOpen {
		t.Fatal("no merge guard dialog the second time")
	}

	if err := app.confirmUnsavedDontSave(ctx); err != nil {
		t.Fatal(err)
	}

	if app.mergeOpen {
		t.Fatal("merge editor stayed open after Don't Save")
	}
}
