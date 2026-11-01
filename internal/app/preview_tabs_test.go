package app

import (
	"context"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// previewFiles are sample-workspace files used across the preview/pinned
// tests. The sample tree holds 17 files, so 8-tab cap scenarios fit.
var previewFiles = []string{
	"README.md",
	"backend/probe.go",
	"backend/server.go",
	"backend/store.go",
	"backend/main.go",
	"web/index.html",
	"web/theme.css",
	"docs/deployment.md",
	"docs/architecture.md",
}

func TestPreviewReusesSingleSlot(t *testing.T) {
	app := newTestApp(t)

	before := len(app.tabs)
	app.openFileAtPreview(0, "backend/probe.go", true)
	app.openFileAtPreview(0, "backend/server.go", true)

	if len(app.tabs) != before+1 {
		t.Fatalf("tabs = %v, want one reused preview slot", app.tabs)
	}

	if app.previewTab[0] != "backend/server.go" {
		t.Fatalf("preview slot = %q, want backend/server.go", app.previewTab[0])
	}

	rows := app.tabRowsFor(0)
	found := false

	for _, row := range rows {
		if row.Path == "backend/server.go" {
			found = true

			if !row.Preview {
				t.Error("server.go row is not marked Preview")
			}

			if row.Pinned {
				t.Error("server.go preview row is marked Pinned")
			}
		}
	}

	if !found {
		t.Fatal("server.go has no tab row")
	}
}

func TestPinProtectsFromReplaceAndEvict(t *testing.T) {
	app := newTestApp(t)

	app.openFileAtPreview(0, "backend/probe.go", true)
	app.pinTabByIndex(0, app.tabIndexIn(0, "backend/probe.go"))
	app.openFileAtPreview(0, "backend/server.go", true)

	if len(app.tabs) != 3 {
		t.Fatalf("tabs = %v, want pinned probe kept plus a new preview", app.tabs)
	}

	if !app.isPinned(0, "backend/probe.go") {
		t.Fatal("probe.go lost its pin")
	}

	// Tabs are unlimited: even an all-pinned group appends instead of
	// refusing. Pin everything open, then verify a preview appends.
	for _, path := range previewFiles {
		if app.tabIndexIn(0, path) < 0 {
			app.openFileAt(0, path, true)
		}
	}

	for i := range app.tabs {
		app.pinTabByIndex(0, i)
	}

	before := len(app.tabs)
	app.openFileAtPreview(0, "tools/report.py", true)

	if len(app.tabs) != before+1 {
		t.Fatalf("tabs = %v, want one appended preview on an all-pinned group", app.tabs)
	}

	if app.previewTab[0] != "tools/report.py" {
		t.Fatalf("preview slot = %q, want tools/report.py", app.previewTab[0])
	}

	_ = strings.Join(app.tabs, "\x00")
}

func TestDoubleClickOrCtrlKPin(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	// There is no press-count on window boxes, so Enter on a tree row and
	// Ctrl+K are the double-click fallbacks. pinPath is the Enter path.
	app.openFileAtPreview(0, "backend/probe.go", true)
	app.pinPath(0, "backend/probe.go")

	if !app.isPinned(0, "backend/probe.go") {
		t.Fatal("pinPath did not pin the preview")
	}

	if app.previewTab[0] == "backend/probe.go" {
		t.Fatal("preview slot was not cleared by pinning")
	}

	// Ctrl+K toggles the focused tab off and back on.
	app.focusGroup = 0
	app.active = app.tabIndexIn(0, "backend/probe.go")
	app.ctrl = true
	app.shift = false

	if err := app.onKeyDown(ctx, "k"); err != nil {
		t.Fatalf("Ctrl+K: %v", err)
	}

	if app.isPinned(0, "backend/probe.go") {
		t.Fatal("Ctrl+K did not unpin")
	}

	if err := app.onKeyDown(ctx, "k"); err != nil {
		t.Fatalf("Ctrl+K again: %v", err)
	}

	if !app.isPinned(0, "backend/probe.go") {
		t.Fatal("Ctrl+K did not re-pin")
	}

	app.ctrl = false
}

func TestEditAutoPinsPreview(t *testing.T) {
	app := newTestApp(t)

	app.openFileAtPreview(0, "backend/probe.go", true)

	if app.isPinned(0, "backend/probe.go") {
		t.Fatal("preview arrived pinned")
	}

	app.edit = edit.NewBuffer("backend/probe.go", []byte("package probe\n"))
	app.editing = true
	app.applyEditText("package probe\n\n// touched\n")

	if !app.isPinned(0, "backend/probe.go") {
		t.Fatal("editing a preview did not auto-pin it")
	}

	if app.previewTab[0] == "backend/probe.go" {
		t.Fatal("preview slot was not cleared by the auto-pin")
	}

	app.editing = false
	app.edit = nil
}

func TestCloseOthersSkipsPinned(t *testing.T) {
	app := newTestApp(t)

	app.tabs = []string{"a", "b", "c"}
	app.active = 1
	app.ensurePinnedMaps()
	app.pinned[0]["a"] = true

	kept, skipped := app.closeTabsIn(0, func(i int, _ string) bool { return i == 1 }, false)

	if kept != 2 || skipped != 1 {
		t.Fatalf("kept=%d skipped=%d, want 2 and 1", kept, skipped)
	}

	if len(app.tabs) != 2 || app.tabs[0] != "a" || app.tabs[1] != "b" {
		t.Fatalf("tabs = %v, want [a b]", app.tabs)
	}
}

func TestCloseRightPushesStackLIFO(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	// Real paths: reopen restores through the workspace, so fake names
	// cannot be used here.
	app.tabs = []string{"README.md", "backend/probe.go", "backend/server.go", "backend/store.go"}
	app.active = 1
	app.ensurePinnedMaps()
	app.closedTabs = nil

	_, _ = app.closeTabsIn(0, func(i int, _ string) bool { return i <= 1 }, false)

	if len(app.tabs) != 2 {
		t.Fatalf("tabs = %v, want [README probe]", app.tabs)
	}

	if len(app.closedTabs) != 2 {
		t.Fatalf("closed stack = %+v, want 2 entries", app.closedTabs)
	}

	app.reopenClosedTab()

	if app.tabs[len(app.tabs)-1] != "backend/store.go" {
		t.Fatalf("tabs = %v, want store.go reopened last-first (LIFO)", app.tabs)
	}

	// The Ctrl+Shift+T chord reopens too.
	app.closeTabIn(0, app.tabIndexIn(0, "backend/store.go"))
	app.ctrl = true
	app.shift = true

	if err := app.onKeyDown(ctx, "t"); err != nil {
		t.Fatalf("Ctrl+Shift+T: %v", err)
	}

	app.ctrl = false
	app.shift = false

	if !containsPath(app.tabs, "backend/store.go") {
		t.Fatalf("tabs = %v, want store.go reopened via Ctrl+Shift+T", app.tabs)
	}

	app.reopenClosedTab()

	if !containsPath(app.tabs, "backend/server.go") {
		t.Fatalf("tabs = %v, want server.go reopened second", app.tabs)
	}

	// The stack caps at closedStackCap, dropping the oldest.
	for i := 0; i < closedStackCap+5; i++ {
		app.pushClosed(0, 0, "backend/probe.go")
	}

	if len(app.closedTabs) != closedStackCap {
		t.Fatalf("closed stack = %d, want cap %d", len(app.closedTabs), closedStackCap)
	}
}

func TestReopenRestoresDirtySnapshot(t *testing.T) {
	app := newTestApp(t)

	app.openFileAt(0, "backend/probe.go", true)

	doc := app.docs["backend/probe.go"]
	doc.Lines = append(append([]string(nil), doc.Lines...), "// unsaved")
	app.setDirty("backend/probe.go", true)

	index := app.tabIndexIn(0, "backend/probe.go")
	app.closeTabIn(0, index)

	if len(app.closedTabs) == 0 || !app.closedTabs[len(app.closedTabs)-1].Dirty {
		t.Fatalf("closed stack = %+v, want a dirty entry", app.closedTabs)
	}

	// Simulate the docs/dirty leftover diverging: the snapshot must win.
	app.docs["backend/probe.go"] = app.ws.openDoc("backend/probe.go")
	app.setDirty("backend/probe.go", false)

	app.reopenClosedTab()

	got := app.docs["backend/probe.go"]
	if len(got.Lines) == 0 || got.Lines[len(got.Lines)-1] != "// unsaved" {
		t.Fatal("reopen did not restore the dirty snapshot text")
	}

	if !app.isDirty("backend/probe.go") {
		t.Fatal("reopen did not restore the dirty flag")
	}
}

func TestReopenClampsIndexAndFullGroup(t *testing.T) {
	app := newTestApp(t)

	// A stale far-past-the-end index clamps to the end.
	app.tabs = []string{"a"}
	app.active = 0
	app.ensurePinnedMaps()
	app.closedTabs = []closedTab{{Path: "backend/probe.go", Group: 0, Index: 99}}

	app.reopenClosedTab()

	if len(app.tabs) != 2 || app.tabs[1] != "backend/probe.go" {
		t.Fatalf("tabs = %v, want probe appended by clamp", app.tabs)
	}

	// Tabs are unlimited: reopening never evicts, pinned or not.
	app.tabs = nil
	app.closedTabs = nil

	for _, path := range previewFiles[:8] {
		app.openFileAt(0, path, true)
	}

	if len(app.tabs) != 8 {
		t.Fatalf("tabs = %d, want a full group", len(app.tabs))
	}

	kept := append([]string(nil), app.tabs...)

	app.pushClosed(0, 0, "tools/report.py")
	app.reopenClosedTab()

	for _, path := range kept {
		if app.tabIndexIn(0, path) < 0 {
			t.Fatalf("tabs = %v, want %s kept (no eviction)", app.tabs, path)
		}
	}

	if !containsPath(app.tabs, "tools/report.py") {
		t.Fatalf("tabs = %v, want report.py reopened", app.tabs)
	}
}

func TestSessionPersistsPinned(t *testing.T) {
	app := newTestApp(t)

	app.openFileAt(0, "backend/probe.go", true)
	app.openFileAtPreview(0, "backend/server.go", true)

	state := app.sessionState()

	if !containsPath(state.Pinned, "README.md") || !containsPath(state.Pinned, "backend/probe.go") {
		t.Fatalf("pinned = %v, want README + probe", state.Pinned)
	}

	if state.PreviewTab != "backend/server.go" {
		t.Fatalf("preview slot = %q, want server.go", state.PreviewTab)
	}

	for _, entry := range state.Pinned {
		if entry == "backend/server.go" {
			t.Fatal("preview tab leaked into the pin set")
		}
	}

	clean := state.sanitize("sample")
	if clean.PreviewTab != "backend/server.go" {
		t.Fatalf("sanitized slot = %q", clean.PreviewTab)
	}

	// Pins naming closed tabs drop; slots outside the group clear.
	if got := keepSessionPins([]string{"backend/probe.go", "gone.go"}, []string{"backend/probe.go"}); len(got) != 1 {
		t.Fatalf("keepSessionPins = %v", got)
	}

	if got := keepSessionSlot("backend/probe.go", []string{"backend/probe.go"}, []string{"backend/probe.go"}); got != "" {
		t.Fatalf("pinned slot = %q, want empty", got)
	}
}

func TestTabRowsExposePreviewPinned(t *testing.T) {
	app := newTestApp(t)

	app.openFileAtPreview(0, "backend/probe.go", true)
	app.pinPath(0, "backend/server.go")

	rows := app.tabRowsFor(0)
	byPath := map[string]tabRow{}

	for _, row := range rows {
		byPath[row.Path] = row
	}

	probe, ok := byPath["backend/probe.go"]
	if !ok || !probe.Preview || probe.Pinned {
		t.Fatalf("probe row = %+v, want Preview-only", probe)
	}

	server, ok := byPath["backend/server.go"]
	if !ok || !server.Pinned || server.Preview {
		t.Fatalf("server row = %+v, want Pinned-only", server)
	}

	readme, ok := byPath["README.md"]
	if !ok || !readme.Pinned {
		t.Fatalf("readme row = %+v, want pinned initial tab", readme)
	}
}

func containsPath(tabs []string, path string) bool {
	for _, p := range tabs {
		if p == path {
			return true
		}
	}

	return false
}
