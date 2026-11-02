package app

import (
	"context"
	"testing"
	"time"

	"github.com/chinmay-sawant/ownframe"
)

// clickBox drives one onClick for tests, like a pointer press + release.
func clickBox(t *testing.T, app *App, action string) {
	t.Helper()

	if err := app.onClick(context.Background(), ownframe.Box{Action: action}); err != nil {
		t.Fatalf("click %s: %v", action, err)
	}
}

// TestDoubleClickFilePins checks the VS Code flow: single-click previews in
// the shared slot, and a fast second click on the same file pins it.
func TestDoubleClickFilePins(t *testing.T) {
	app := newTestApp(t)

	clickBox(t, app, "open:backend/probe.go")

	if app.isPinned(0, "backend/probe.go") {
		t.Fatal("single-click pinned the file")
	}

	if app.previewTab[0] != "backend/probe.go" {
		t.Fatalf("preview slot = %q, want backend/probe.go", app.previewTab[0])
	}

	// Fast second click on the same file: pins instead of previewing.
	clickBox(t, app, "open:backend/probe.go")

	if !app.isPinned(0, "backend/probe.go") {
		t.Fatal("double-click did not pin the file")
	}

	if app.previewTab[0] == "backend/probe.go" {
		t.Fatal("preview slot was not cleared by the pin")
	}
}

// TestDoubleClickFileSlowStaysPreview checks two slow clicks behave like
// two single clicks: the second preview replaces the first, nothing pins.
func TestDoubleClickFileSlowStaysPreview(t *testing.T) {
	app := newTestApp(t)

	clickBox(t, app, "open:backend/probe.go")

	// Age the first click past the double-click window.
	app.lastClickAt = app.lastClickAt.Add(-time.Hour)

	clickBox(t, app, "open:backend/probe.go")

	if app.isPinned(0, "backend/probe.go") {
		t.Fatal("slow second click pinned the file")
	}

	if app.previewTab[0] != "backend/probe.go" {
		t.Fatalf("preview slot = %q, want backend/probe.go", app.previewTab[0])
	}
}

// TestDoubleClickDifferentFilesStaysPreview checks fast clicks on two
// different files preview both without pinning either.
func TestDoubleClickDifferentFilesStaysPreview(t *testing.T) {
	app := newTestApp(t)

	clickBox(t, app, "open:backend/probe.go")
	clickBox(t, app, "open:backend/server.go")

	if app.isPinned(0, "backend/probe.go") || app.isPinned(0, "backend/server.go") {
		t.Fatalf("fast clicks on different files pinned: %v", app.tabs)
	}

	if app.previewTab[0] != "backend/server.go" {
		t.Fatalf("preview slot = %q, want backend/server.go", app.previewTab[0])
	}
}

// TestDoubleClickInterruptedStaysPreview checks a click elsewhere between
// two clicks on one file breaks the double-click: no pin.
func TestDoubleClickInterruptedStaysPreview(t *testing.T) {
	app := newTestApp(t)

	clickBox(t, app, "open:backend/probe.go")
	clickBox(t, app, "toast-close")
	clickBox(t, app, "open:backend/probe.go")

	if app.isPinned(0, "backend/probe.go") {
		t.Fatal("interrupted clicks pinned the file")
	}
}

// TestDoubleClickTabPins checks double-clicking an open preview tab pins
// it in place, and a third fast click stays a harmless single click.
func TestDoubleClickTabPins(t *testing.T) {
	app := newTestApp(t)

	app.openFileAtPreview(0, "backend/probe.go", true)

	index := app.tabIndexIn(0, "backend/probe.go")
	if index < 0 {
		t.Fatal("probe.go has no tab")
	}

	action := "tab:" + itoa(index)
	clickBox(t, app, action)

	if app.isPinned(0, "backend/probe.go") {
		t.Fatal("single-click on the tab pinned it")
	}

	clickBox(t, app, action)

	if !app.isPinned(0, "backend/probe.go") {
		t.Fatal("double-click on the tab did not pin it")
	}

	// Third fast click: fresh single click on a pinned tab, stays pinned.
	clickBox(t, app, action)

	if !app.isPinned(0, "backend/probe.go") {
		t.Fatal("third click unpinned the tab")
	}
}

// TestDoubleClickPinnedTabKeepsPin checks double-clicking an already-pinned
// tab keeps the pin instead of toggling it off.
func TestDoubleClickPinnedTabKeepsPin(t *testing.T) {
	app := newTestApp(t)

	clickBox(t, app, "open:backend/probe.go")
	clickBox(t, app, "open:backend/probe.go")

	if !app.isPinned(0, "backend/probe.go") {
		t.Fatal("setup did not pin probe.go")
	}

	index := app.tabIndexIn(0, "backend/probe.go")
	action := "tab:" + itoa(index)
	clickBox(t, app, action)
	clickBox(t, app, action)

	if !app.isPinned(0, "backend/probe.go") {
		t.Fatal("double-click on a pinned tab dropped the pin")
	}
}

// TestPreviewOpensAtBack checks a single-click always opens at the back of
// the strip: a previous preview leaves and the new file appends at the end,
// like a pinned open. Pinned tabs never move.
func TestPreviewOpensAtBack(t *testing.T) {
	app := newTestApp(t)

	app.openFileAt(0, "backend/probe.go", true)
	app.openFileAtPreview(0, "backend/server.go", true)
	app.openFileAtPreview(0, "backend/store.go", true)

	want := []string{"README.md", "backend/probe.go", "backend/store.go"}
	if len(app.tabs) != len(want) {
		t.Fatalf("tabs = %v, want %v", app.tabs, want)
	}

	for i, path := range want {
		if app.tabs[i] != path {
			t.Fatalf("tabs = %v, want %v", app.tabs, want)
		}
	}

	if app.previewTab[0] != "backend/store.go" {
		t.Fatalf("preview slot = %q, want backend/store.go", app.previewTab[0])
	}

	if active := app.groupActive(0); active != len(app.tabs)-1 {
		t.Fatalf("active = %d, want the back tab %d", active, len(app.tabs)-1)
	}

	// The replaced preview is on the closed stack, so Ctrl+Shift+T
	// restores it.
	app.reopenClosedTab()

	if !containsPath(app.tabs, "backend/server.go") {
		t.Fatalf("tabs = %v, want replaced server.go reopenable", app.tabs)
	}
}

// TestPreviewTabShowsBadgeNameStatus checks a preview tab row carries the
// same content as a pinned one: language badge, file name, and status.
func TestPreviewTabShowsBadgeNameStatus(t *testing.T) {
	app := newTestApp(t)

	app.openFileAtPreview(0, "backend/probe.go", true)

	rows := app.tabRowsFor(0)

	var found *tabRow
	for i := range rows {
		if rows[i].Path == "backend/probe.go" {
			found = &rows[i]
		}
	}

	if found == nil {
		t.Fatal("probe.go has no tab row")
	}

	if !found.Preview || found.Pinned {
		t.Fatalf("probe row = %+v, want Preview-only", *found)
	}

	if found.Name != "probe.go" {
		t.Fatalf("preview name = %q, want probe.go", found.Name)
	}

	if found.Badge == "" || found.Lang != languageOf("backend/probe.go") {
		t.Fatalf("preview badge/lang = %q/%q, want the Go badge", found.Badge, found.Lang)
	}
}
