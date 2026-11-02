package app

import (
	"context"
	"strings"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

func TestKeymapCatalogCoversOnKeyDown(t *testing.T) {
	t.Parallel()

	catalog := keymapCatalog()
	if len(catalog) < 20 {
		t.Fatalf("catalog = %d entries, want the onKeyDown inventory", len(catalog))
	}

	seen := map[string]bool{}
	for _, e := range catalog {
		if e.Action == "" || e.Label == "" || e.Default == "" {
			t.Fatalf("catalog entry = %+v", e)
		}

		if seen[e.Action] {
			t.Fatalf("duplicate action %q", e.Action)
		}

		seen[e.Action] = true

		if _, ok := normalizeChord(e.Default); !ok {
			t.Fatalf("default chord %q does not parse", e.Default)
		}
	}

	for _, action := range []string{"zoom-in", "toggle-terminal", "quick-open", "workspace-search", "save-file", "history-back"} {
		if !seen[action] {
			t.Fatalf("catalog misses %q", action)
		}
	}
}

func TestKeymapResolveAndReserved(t *testing.T) {
	t.Parallel()

	if got := resolveChord(nil, "Ctrl+P"); len(got) != 1 || got[0] != "quick-open" {
		t.Fatalf("resolve Ctrl+P = %v", got)
	}

	custom := map[string]string{"quick-open": "Ctrl+O"}
	if got := resolveChord(custom, "Ctrl+O"); len(got) != 1 || got[0] != "quick-open" {
		t.Fatalf("resolve rebound = %v", got)
	}

	if got := resolveChord(custom, "Ctrl+P"); len(got) != 0 {
		t.Fatalf("old chord still resolves: %v", got)
	}

	for _, chord := range []string{"F12", "Ctrl+Shift+I", "f12", "ctrl+shift+i"} {
		if !isReservedChord(chord) {
			t.Fatalf("%q is not reserved", chord)
		}
	}

	if isReservedChord("Ctrl+P") {
		t.Fatal("Ctrl+P is wrongly reserved")
	}

	if !isTerminalRiskyChord("Ctrl+C") || !isSingleLetterChord("W") {
		t.Fatal("risky/single-letter detection wrong")
	}

	if isSingleLetterChord("Ctrl+W") {
		t.Fatal("Ctrl+W is wrongly single-letter")
	}
}

func TestKeymapDiffsOnly(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	a.applyKeymapChord("quick-open", "Ctrl+P")
	if len(a.settings.Keymap) != 0 {
		t.Fatalf("default-equal bind stored: %+v", a.settings.Keymap)
	}

	a.applyKeymapChord("quick-open", "Ctrl+O")
	if a.keymapChord("quick-open") != "Ctrl+O" {
		t.Fatalf("live chord = %q", a.keymapChord("quick-open"))
	}

	a.resetKeymapAction("quick-open")
	if a.keymapChord("quick-open") != "Ctrl+P" {
		t.Fatalf("after reset = %q", a.keymapChord("quick-open"))
	}
}

func TestKeymapRebindFlow(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	ctx := context.Background()

	a.applyKeymapChord("quick-open", "Ctrl+O")

	if err := a.onKeyDown(ctx, "control"); err != nil {
		t.Fatal(err)
	}

	if err := a.onKeyDown(ctx, "o"); err != nil {
		t.Fatal(err)
	}

	if !a.View().Palette.Open {
		t.Fatal("rebound Ctrl+O did not open the palette")
	}

	if err := a.onKeyDown(ctx, "escape"); err != nil {
		t.Fatal(err)
	}

	if err := a.onKeyUp(ctx, "control"); err != nil {
		t.Fatal(err)
	}

	// Old chord is dead after the rebind.
	if err := a.onKeyDown(ctx, "control"); err != nil {
		t.Fatal(err)
	}

	if err := a.onKeyDown(ctx, "p"); err != nil {
		t.Fatal(err)
	}

	if a.View().Palette.Open {
		t.Fatal("old Ctrl+P still opens the palette after the rebind")
	}

	if err := a.onKeyUp(ctx, "control"); err != nil {
		t.Fatal(err)
	}

	a.resetKeymapAll()
	if a.keymapChord("quick-open") != "Ctrl+P" {
		t.Fatal("reset-all did not restore Ctrl+P")
	}
}

func TestKeymapCaptureConflictAndReserved(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	ctx := context.Background()

	if err := a.onClick(ctx, ownBox("settings-open")); err != nil {
		t.Fatal(err)
	}

	if err := a.onClick(ctx, ownBox("keymap-rebind:quick-open")); err != nil {
		t.Fatal(err)
	}

	if a.keymapCapture != "quick-open" {
		t.Fatal("capture did not arm")
	}

	// F12 is read-only: disallowed with a note, capture cleared.
	a.processKeymapCapture("F12")
	if a.keymapCapture != "" {
		t.Fatal("reserved chord kept the capture armed")
	}

	// Colliding with the palette default parks a conflict.
	a.applyKeymapChord("workspace-search", "Ctrl+Shift+F")
	a.keymapCapture = "quick-open"
	a.processKeymapCapture("Ctrl+Shift+F")
	if a.keymapPendingAction != "quick-open" || len(a.keymapPendingOthers) == 0 {
		t.Fatalf("conflict = %+v", a.keymapPendingOthers)
	}

	a.swapKeymapPending()
	if a.keymapChord("quick-open") != "Ctrl+Shift+F" {
		t.Fatalf("swap quick-open = %q", a.keymapChord("quick-open"))
	}

	if a.keymapChord("workspace-search") != "Ctrl+P" {
		t.Fatalf("swap displaced = %q", a.keymapChord("workspace-search"))
	}
}

func TestWorkspaceScanExcludes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	writeFile(t, dir, "debug.log", "noise\n")
	writeFile(t, dir, "notes.txt", "keep\n")

	plain, err := loadDiskWorkspaceWithLimit(dir, 0)
	if err != nil {
		t.Fatal(err)
	}

	if plain.ByPath["debug.log"] == nil {
		t.Fatal("fixture missing without excludes")
	}

	filter := compileScanExcludes(excludeSettings{Scan: []string{"*.log"}})
	hidden, err := loadDiskWorkspaceWithLimitExcludes(dir, 0, filter)
	if err != nil {
		t.Fatal(err)
	}

	if hidden.ByPath["debug.log"] != nil {
		t.Fatal("*.log survived scan excludes")
	}

	if hidden.ByPath["main.go"] == nil || hidden.ByPath["notes.txt"] == nil {
		t.Fatal("scan excludes hid too much")
	}

	// Invalid globs never abort the scan.
	bad := compileScanExcludes(excludeSettings{Scan: []string{"[[bad", "!neg"}})
	survived, err := loadDiskWorkspaceWithLimitExcludes(dir, 0, bad)
	if err != nil {
		t.Fatal(err)
	}

	if survived.ByPath["debug.log"] == nil {
		t.Fatal("invalid glob aborted the scan")
	}
}

func TestWorkspaceSearchExcludesNote(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n// secret\n")
	writeFile(t, dir, "debug.log", "secret\n")

	ws, err := loadDiskWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}

	full := searchWorkspace(context.Background(), ws, "secret", true, 0)
	if len(full.Hits) == 0 {
		t.Fatal("no baseline hits")
	}

	filter := compileScanExcludes(excludeSettings{Search: []string{"*.log"}})
	trimmed := searchWorkspaceFiltered(context.Background(), ws, "secret", true, 0, filter)

	for _, hit := range trimmed.Hits {
		if strings.HasSuffix(hit.Path, ".log") {
			t.Fatalf("excluded hit in %s", hit.Path)
		}
	}

	if !strings.Contains(trimmed.Note, "1 file skipped") {
		t.Fatalf("note = %q", trimmed.Note)
	}

	// Search stays cancellable with excludes on.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := searchWorkspaceFiltered(cancelled, ws, "secret", true, 0, filter)
	if stopped.Note != workspaceSearchCancelNote {
		t.Fatalf("cancel note = %q", stopped.Note)
	}
}

func TestRescanKeepsExcludes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")

	filter := compileScanExcludes(excludeSettings{Scan: []string{"*.log"}})
	first, err := loadDiskWorkspaceWithLimitExcludes(dir, 0, filter)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "debug.log", "noise\n")

	next, _, err := rescanDiskWorkspace(dir, first, 0)
	if err != nil {
		t.Fatal(err)
	}

	if next.ByPath["debug.log"] != nil {
		t.Fatal("rescan dropped the scan filter")
	}

	if next.ByPath["main.go"] == nil {
		t.Fatal("rescan lost main.go")
	}
}

func TestGitignoreToggleSkipsGit(t *testing.T) {
	t.Parallel()

	// Offline/fast: with useGitignore=false no git subprocess runs and the
	// scan still lists files.
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")

	filter := compileScanExcludes(excludeSettings{UseGitignore: false})
	if filter.useGitignore {
		t.Fatal("toggle did not stick")
	}

	ws, err := loadDiskWorkspaceWithLimitExcludes(dir, 0, filter)
	if err != nil {
		t.Fatal(err)
	}

	if ws.ByPath["main.go"] == nil {
		t.Fatal("scan with git disabled lost files")
	}

	if got := loadIgnoredDirsFiltered(dir, []string{"x"}, filter); len(got) != 0 {
		t.Fatal("git ran with useGitignore=false")
	}
}

func ownBox(action string) ownframe.Box {
	return ownframe.Box{Action: action}
}
