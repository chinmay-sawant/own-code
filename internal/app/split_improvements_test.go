package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

// TestSplitHScrollIndependent checks per-group horizontal scrolling: wheel
// over a pane and arrows follow the hovered / focused group, and the other
// pane's offset stays byte-identical.
func TestSplitHScrollIndependent(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	app.toggleSplit()
	app.openFileIn(0, "backend/probe.go")
	app.openFileIn(1, "backend/probe.go")
	app.wrap = false
	app.scrollXGrp = [2]int{}
	app.refresh()

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	// Direct per-group scroll.
	app.scrollHorizontalFor(1, 50)
	if app.scrollXGrp[1] != 50 {
		t.Fatalf("group 1 scrollX = %d, want 50", app.scrollXGrp[1])
	}
	if app.scrollXGrp[0] != 0 {
		t.Fatalf("group 0 scrollX changed: %d", app.scrollXGrp[0])
	}
	if got := app.View().Second.ScrollX; got != 50 {
		t.Fatalf("Second.ScrollX = %d, want 50", got)
	}

	// ScrollX follows the focused group.
	app.setFocusGroup(1)
	if app.ScrollX() != 50 {
		t.Fatalf("ScrollX focused right = %d, want 50", app.ScrollX())
	}
	app.setFocusGroup(0)
	if app.ScrollX() != 0 {
		t.Fatalf("ScrollX focused left = %d, want 0", app.ScrollX())
	}

	// Arrows route to the focused group.
	app.setFocusGroup(1)
	if err := app.onKeyDown(ctx, "arrowright"); err != nil {
		t.Fatalf("arrowright: %v", err)
	}
	if app.scrollXGrp[1] != 50+app.zx(hscrollStep) {
		t.Fatalf("group 1 after arrow = %d, want %d", app.scrollXGrp[1], 50+app.zx(hscrollStep))
	}
	if app.scrollXGrp[0] != 0 {
		t.Fatalf("group 0 changed by focused arrow: %d", app.scrollXGrp[0])
	}

	// Shift+wheel over the right pane scrolls group 1 only. The right pane
	// is tested before the left so the shared gutter cannot steal it.
	boxes := app.Page().Boxes()
	box2, ok := boxByID(boxes, "editor-window-2")
	if !ok {
		t.Fatal("missing editor-window-2")
	}
	app.shift = true
	before := app.scrollXGrp[1]
	handled, err := app.onWheel(ctx, box2.X+box2.W/2, box2.Y+box2.H/2, 0, -1)
	app.shift = false
	if err != nil {
		t.Fatalf("onWheel: %v", err)
	}
	if !handled {
		t.Fatal("shift+wheel over right pane was not handled")
	}
	if app.scrollXGrp[1] <= before {
		t.Fatalf("group 1 shift+wheel did not move: %d -> %d", before, app.scrollXGrp[1])
	}
	if app.scrollXGrp[0] != 0 {
		t.Fatalf("group 0 changed by right-pane wheel: %d", app.scrollXGrp[0])
	}
}

// TestSplitOnScrollInert checks the page scroll callback leaves both group
// offsets alone in split mode while viewport resizes still rebuild.
func TestSplitOnScrollInert(t *testing.T) {
	app := newTestApp(t)

	app.toggleSplit()
	app.openFileIn(0, "backend/server.go")
	app.openFileIn(1, "backend/store.go")
	app.scrollGroupBy(0, 120)
	app.scrollGroupBy(1, 80)
	app.scrollPending = false

	if !app.onScroll(9999, app.viewportH) {
		t.Fatal("viewport/cols sync reported no change")
	}
	if app.scrollGroup[0] != 120 || app.scrollGroup[1] != 80 {
		t.Fatalf("split onScroll moved offsets: (%d, %d)", app.scrollGroup[0], app.scrollGroup[1])
	}
	if app.scrollY != 120 {
		t.Fatalf("split onScroll moved scrollY: %d", app.scrollY)
	}

	// Second call with a synced viewport is a no-op.
	if app.onScroll(9999, app.viewportH) {
		t.Fatal("synced split onScroll reported a change")
	}
	if app.scrollGroup[0] != 120 || app.scrollGroup[1] != 80 {
		t.Fatalf("offsets moved on no-op: (%d, %d)", app.scrollGroup[0], app.scrollGroup[1])
	}
}

// TestSplitFocusClick checks click-to-focus: body presses focus their pane,
// chrome elsewhere keeps focus, and caret actions never reach the fallback.
func TestSplitFocusClick(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	for id, want := range map[string]int{
		"editor-window-2": 1, "code-body-2": 1, "crumbs-2": 1,
		"tabstrip-2": 1, "tab2-3": 1, "tab2-close-0": 1,
		"editor-window": 0, "code-body": 0, "tabstrip": 0,
		"edit-surface": 0, "tab-1": 0, "edit-line-4": 0,
	} {
		if got, ok := focusGroupForClick(id); !ok || got != want {
			t.Fatalf("focusGroupForClick(%q) = (%d, %v), want (%d, true)", id, got, ok, want)
		}
	}

	for _, id := range []string{"tree-pane", "filter", "palette", "split-divider", "", "commit-abc123"} {
		if _, ok := focusGroupForClick(id); ok {
			t.Fatalf("focusGroupForClick(%q) matched, want no match", id)
		}
	}

	app.toggleSplit()
	app.setFocusGroup(0)

	if err := app.onClick(ctx, ownframe.Box{ID: "code-body-2"}); err != nil {
		t.Fatalf("click right body: %v", err)
	}
	if app.focusGroup != 1 {
		t.Fatalf("focus after right click = %d, want 1", app.focusGroup)
	}

	if err := app.onClick(ctx, ownframe.Box{ID: "code-body"}); err != nil {
		t.Fatalf("click left body: %v", err)
	}
	if app.focusGroup != 0 {
		t.Fatalf("focus after left click = %d, want 0", app.focusGroup)
	}
}

// TestSplitFocusKeys checks Ctrl+1 / Ctrl+2 focus switching, F6 cycling,
// and that plain 1/2 still switch modes.
func TestSplitFocusKeys(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	app.toggleSplit()
	app.setFocusGroup(0)

	app.ctrl = true
	if err := app.onKeyDown(ctx, "2"); err != nil {
		t.Fatalf("ctrl+2: %v", err)
	}
	app.ctrl = false
	if app.focusGroup != 1 {
		t.Fatalf("focus after ctrl+2 = %d, want 1", app.focusGroup)
	}

	app.ctrl = true
	if err := app.onKeyDown(ctx, "1"); err != nil {
		t.Fatalf("ctrl+1: %v", err)
	}
	app.ctrl = false
	if app.focusGroup != 0 {
		t.Fatalf("focus after ctrl+1 = %d, want 0", app.focusGroup)
	}

	if err := app.onKeyDown(ctx, "f6"); err != nil {
		t.Fatalf("f6: %v", err)
	}
	if app.focusGroup != 1 {
		t.Fatalf("focus after f6 = %d, want 1", app.focusGroup)
	}
	if err := app.onKeyDown(ctx, "f6"); err != nil {
		t.Fatalf("f6 again: %v", err)
	}
	if app.focusGroup != 0 {
		t.Fatalf("focus after second f6 = %d, want 0", app.focusGroup)
	}

	// Plain 1/2 keep switching modes, untouched by the focus chords.
	if err := app.onKeyDown(ctx, "2"); err != nil {
		t.Fatalf("plain 2: %v", err)
	}
	if app.View().Mode != "changes" {
		t.Fatalf("mode after plain 2 = %q, want changes", app.View().Mode)
	}
	if err := app.onKeyDown(ctx, "1"); err != nil {
		t.Fatalf("plain 1: %v", err)
	}
	if app.View().Mode != "explorer" {
		t.Fatalf("mode after plain 1 = %q, want explorer", app.View().Mode)
	}
}

// TestSplitActiveHighlight checks the focused pane carries the focus class
// in the rendered template and the divider box exists between the panes.
func TestSplitActiveHighlight(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	app.toggleSplit()
	app.setFocusGroup(1)

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	rendered := renderAppHTML(t, app)
	if !strings.Contains(rendered, `class="editor editor-second focus"`) {
		t.Fatal("right pane is missing the focus class")
	}
	if strings.Contains(rendered, `class="editor split focus"`) {
		t.Fatal("left pane wrongly carries the focus class")
	}

	app.setFocusGroup(0)
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	rendered = renderAppHTML(t, app)
	if !strings.Contains(rendered, `class="editor split focus"`) {
		t.Fatal("left pane is missing the focus class")
	}

	div, ok := boxByID(app.Page().Boxes(), "split-divider")
	if !ok {
		t.Fatal("missing split-divider box")
	}
	if div.W < 5 || div.W > 7 {
		t.Fatalf("divider width = %.1f, want ~6px", div.W)
	}

	view := app.View()
	if view.FocusedPane != 0 || view.Second.Focused {
		t.Fatalf("view focus = pane %d second %v, want 0/false", view.FocusedPane, view.Second.Focused)
	}
}

// TestSplitWClosesFocused checks W closes the focused group's active tab.
func TestSplitWClosesFocused(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	app.toggleSplit()
	app.openFileIn(1, "backend/probe.go")
	if len(app.tabs2) != 2 || app.focusGroup != 1 {
		t.Fatalf("setup: tabs2=%v focus=%d", app.tabs2, app.focusGroup)
	}
	leftTabs := append([]string(nil), app.tabs...)

	if err := app.onKeyDown(ctx, "w"); err != nil {
		t.Fatalf("w: %v", err)
	}

	if len(app.tabs2) != 1 {
		t.Fatalf("tabs2 after w = %v, want one tab closed", app.tabs2)
	}
	if strings.Join(app.tabs, ",") != strings.Join(leftTabs, ",") {
		t.Fatalf("left tabs changed by focused w: %v", app.tabs)
	}

	// Close the last right tab: the split collapses and focus returns left.
	if err := app.onKeyDown(ctx, "w"); err != nil {
		t.Fatalf("w again: %v", err)
	}
	if app.split {
		t.Fatal("split still open after closing the last right tab")
	}
	if app.focusGroup != 0 {
		t.Fatalf("focus = %d, want 0", app.focusGroup)
	}

	app.openFile("backend/server.go")
	before := append([]string(nil), app.tabs...)
	if err := app.onKeyDown(ctx, "w"); err != nil {
		t.Fatalf("w on left: %v", err)
	}
	if len(app.tabs) != len(before)-1 {
		t.Fatalf("w on left closed nothing: tabs=%v", app.tabs)
	}
}

// TestSplitCloseSideMerge checks closing the right side merges its tabs
// into the survivor, keeps unedited tabs, and lands focus on the survivor.
func TestSplitCloseSideMerge(t *testing.T) {
	app := newTestApp(t)

	app.toggleSplit()
	app.openFileIn(0, "backend/server.go")
	app.openFileIn(1, "backend/store.go")
	app.openFileIn(1, "backend/probe.go")

	app.closeSplitSide(1, splitCloseMerge)

	if app.split {
		t.Fatal("split still open after merge close")
	}
	if app.focusGroup != 0 {
		t.Fatalf("focus = %d, want survivor 0", app.focusGroup)
	}

	want := []string{"README.md", "backend/server.go", "backend/store.go", "backend/probe.go"}
	if strings.Join(app.tabs, ",") != strings.Join(want, ",") {
		t.Fatalf("merged tabs = %v, want %v", app.tabs, want)
	}
	if len(app.tabs2) != 0 {
		t.Fatalf("tabs2 = %v, want empty", app.tabs2)
	}
}

// TestSplitCloseLeftRenumber checks closing side 0 renumbers the survivor
// to group 0 with its tabs, active tab, and offsets intact.
func TestSplitCloseLeftRenumber(t *testing.T) {
	app := newTestApp(t)

	app.toggleSplit()
	app.openFileIn(0, "backend/server.go")
	app.openFileIn(1, "backend/store.go")
	app.scrollGroupBy(1, 120)
	app.scrollHorizontalFor(1, 0)

	app.closeSplitSide(0, splitCloseMerge)

	if app.split {
		t.Fatal("split still open after left close")
	}

	want := []string{"README.md", "backend/store.go", "backend/server.go"}
	if strings.Join(app.tabs, ",") != strings.Join(want, ",") {
		t.Fatalf("renumbered tabs = %v, want %v", app.tabs, want)
	}
	if app.active != 1 || app.tabs[app.active] != "backend/store.go" {
		t.Fatalf("active = %d (%v), want store.go at 1", app.active, app.tabs)
	}
	if app.scrollGroup[0] != 120 {
		t.Fatalf("renumbered scroll = %d, want survivor offset 120", app.scrollGroup[0])
	}
	if app.focusGroup != 0 {
		t.Fatalf("focus = %d, want 0", app.focusGroup)
	}
}

// TestSplitCloseDirtyBlock checks drop mode aborts on dirty tabs with a
// toast, and merge mode keeps dirty tabs.
func TestSplitCloseDirtyBlock(t *testing.T) {
	app := newTestApp(t)

	app.toggleSplit()
	app.openFileIn(1, "backend/store.go")
	app.dirty["backend/store.go"] = true

	app.closeSplitSide(1, splitCloseDrop)

	if !app.split {
		t.Fatal("dirty drop collapsed the split")
	}
	if len(app.tabs2) == 0 {
		t.Fatal("dirty drop discarded tabs")
	}
	if !app.View().Toast.Show {
		t.Fatal("dirty drop showed no toast")
	}

	app.closeSplitSide(1, splitCloseMerge)

	if app.split {
		t.Fatal("merge close left the split open")
	}
	if !app.dirty["backend/store.go"] {
		t.Fatal("merge close lost the dirty flag")
	}
	found := false
	for _, path := range app.tabs {
		if path == "backend/store.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("merge close lost the dirty tab: %v", app.tabs)
	}
}

// TestSplitCloseSideDrop checks drop mode discards clean tabs and the pane.
func TestSplitCloseSideDrop(t *testing.T) {
	app := newTestApp(t)

	app.toggleSplit()
	app.openFileIn(0, "backend/server.go")
	app.openFileIn(1, "backend/store.go")

	left := append([]string(nil), app.tabs...)
	app.closeSplitSide(1, splitCloseDrop)

	if app.split || len(app.tabs2) != 0 {
		t.Fatalf("split=%v tabs2=%v after drop", app.split, app.tabs2)
	}
	if strings.Join(app.tabs, ",") != strings.Join(left, ",") {
		t.Fatalf("left tabs changed by drop: %v", app.tabs)
	}
}

// TestSplitCloseMergeCap checks merging keeps every tab: tabs are
// unlimited, so no cap toast applies.
func TestSplitCloseMergeCap(t *testing.T) {
	app := newTestApp(t)

	const n = 8
	left := make([]string, 0, n)
	right := make([]string, 0, n)
	for i := 0; i < n; i++ {
		left = append(left, fmt.Sprintf("left-file-%02d.go", i))
		right = append(right, fmt.Sprintf("right-file-%02d.go", i))
	}

	app.tabs = append([]string(nil), left...)
	app.active = 0
	app.tabs2 = append([]string(nil), right...)
	app.active2 = 0
	app.split = true
	app.focusGroup = 1
	app.refresh()

	app.closeSplitSide(1, splitCloseMerge)

	if app.split {
		t.Fatal("split still open after merge")
	}
	if len(app.tabs) != 2*n {
		t.Fatalf("merged tabs = %d, want %d", len(app.tabs), 2*n)
	}
}

// TestSplitCloseLastLeftAdoptsRight checks closing the last left tab while
// split collapses onto the renumbered right instead of an empty left.
func TestSplitCloseLastLeftAdoptsRight(t *testing.T) {
	app := newTestApp(t)

	app.toggleSplit()
	// Leave exactly one left tab (README.md) and a scrolled right tab.
	app.openFileIn(1, "backend/store.go")
	app.scrollGroupBy(1, 100)

	app.closeTabIn(0, 0)

	if app.split {
		t.Fatal("split still open after last-left close")
	}
	if len(app.tabs) != 2 || app.tabs[0] != "README.md" || app.tabs[1] != "backend/store.go" {
		t.Fatalf("adopted tabs = %v", app.tabs)
	}
	if app.scrollGroup[0] != 100 {
		t.Fatalf("adopted scroll = %d, want 100", app.scrollGroup[0])
	}
	if app.focusGroup != 0 {
		t.Fatalf("focus = %d, want 0", app.focusGroup)
	}
}

// TestSplitDividerDrag checks ratio moves, clamping, scroll preservation,
// and session persistence across a drag.
func TestSplitDividerDrag(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	app.toggleSplit()
	app.openFileIn(0, "backend/server.go")
	app.openFileIn(1, "backend/store.go")
	app.scrollGroupBy(0, 100)
	app.scrollGroupBy(1, 60)

	if !app.setSplitRatio(0.6) {
		t.Fatal("setSplitRatio(0.6) reported no move")
	}
	if app.splitRatio != 0.6 {
		t.Fatalf("ratio = %v, want 0.6", app.splitRatio)
	}
	if app.scrollGroup[0] != 100 || app.scrollGroup[1] != 60 {
		t.Fatalf("drag moved offsets: (%d, %d)", app.scrollGroup[0], app.scrollGroup[1])
	}

	// Clamp: past the rails pins at the session limits and reports no move
	// once pinned.
	app.setSplitRatio(0.99)
	if app.splitRatio != sessionSplitMax {
		t.Fatalf("clamped ratio = %v, want %v", app.splitRatio, sessionSplitMax)
	}
	if app.setSplitRatio(0.99) {
		t.Fatal("clamped setSplitRatio reported a move")
	}
	app.setSplitRatio(0.01)
	if app.splitRatio != sessionSplitMin {
		t.Fatalf("clamped ratio = %v, want %v", app.splitRatio, sessionSplitMin)
	}

	// Pane-minimum clamp wins over the session minimum on narrow totals.
	if got := app.clampSplitRatio(0, 1000); got != float64(paneMinWidth)/1000 {
		t.Fatalf("narrow min clamp = %v, want %v", got, float64(paneMinWidth)/1000)
	}
	if got := app.clampSplitRatio(1, 1000); got != float64(1000-paneMinWidth)/1000 {
		t.Fatalf("narrow max clamp = %v, want %v", got, float64(1000-paneMinWidth)/1000)
	}

	// Full drag flow claims, moves, and persists.
	app.setSplitRatio(0.5)
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	div, ok := boxByID(app.Page().Boxes(), "split-divider")
	if !ok {
		t.Fatal("missing split-divider")
	}

	claimed, err := app.onDragStart(ctx, div)
	if err != nil || !claimed {
		t.Fatalf("drag start: claimed=%v err=%v", claimed, err)
	}
	if !app.splitDrag.active {
		t.Fatal("splitDrag not active after start")
	}

	total := app.editorWidth()
	before := app.splitRatio
	if err := app.onDragMove(ctx, float64(total)*0.1, 0); err != nil {
		t.Fatalf("drag move: %v", err)
	}
	if app.splitRatio == before {
		t.Fatal("drag move did not change the ratio")
	}
	if app.scrollGroup[0] != 100 || app.scrollGroup[1] != 60 {
		t.Fatalf("drag moved offsets: (%d, %d)", app.scrollGroup[0], app.scrollGroup[1])
	}

	if err := app.onDragEnd(ctx); err != nil {
		t.Fatalf("drag end: %v", err)
	}
	if app.splitDrag.active {
		t.Fatal("splitDrag still active after end")
	}

	state := app.sessionState()
	if state.SplitRatio != app.splitRatio || !state.Split {
		t.Fatalf("session did not persist the split: %+v", state)
	}
}

// TestSplitDividerKeyboard checks arrows nudge and Home resets the ratio.
func TestSplitDividerKeyboard(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	app.toggleSplit()
	app.setSplitRatio(0.5)

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}
	if err := app.page.Form().Focus(ctx, "split-divider"); err != nil {
		t.Fatalf("focus divider: %v", err)
	}

	if err := app.onKeyDown(ctx, "arrowright"); err != nil {
		t.Fatalf("divider arrowright: %v", err)
	}
	if app.splitRatio != 0.5+splitRatioStep {
		t.Fatalf("ratio = %v, want %v", app.splitRatio, 0.5+splitRatioStep)
	}

	if err := app.onKeyDown(ctx, "arrowleft"); err != nil {
		t.Fatalf("divider arrowleft: %v", err)
	}
	if app.splitRatio != 0.5 {
		t.Fatalf("ratio = %v, want 0.5", app.splitRatio)
	}

	if err := app.onKeyDown(ctx, "home"); err != nil {
		t.Fatalf("divider home: %v", err)
	}
	if app.splitRatio != defaultSplitRatio {
		t.Fatalf("ratio = %v, want default %v", app.splitRatio, defaultSplitRatio)
	}
}

// hasSplitCloseRow reports whether a menu carries split-close rows.
func hasSplitCloseRow(rows []contextMenuRow) bool {
	for _, row := range rows {
		if strings.HasPrefix(row.ID, contextMenuSplitCloseID) {
			return true
		}
	}

	return false
}

// TestSplitPaneMenu checks the pane context menu offers close rows and the
// merge row collapses the split through MenuAction.
func TestSplitPaneMenu(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	app.toggleSplit()
	app.openFileIn(1, "backend/store.go")

	rows := app.ContextMenuFor("tabstrip-2")
	if len(rows) != 4 {
		t.Fatalf("pane menu rows = %+v, want 4 close rows", rows)
	}
	if rows[0].Label != "Close This Pane" || rows[1].Label != "Close Other Pane" {
		t.Fatalf("pane menu labels = %q %q", rows[0].Label, rows[1].Label)
	}

	if rows := app.ContextMenuFor("tree-pane"); hasSplitCloseRow(rows) {
		t.Fatalf("tree menu leaked split rows: %+v", rows)
	}

	if err := app.MenuAction(ctx, rows[0].ID); err != nil {
		t.Fatalf("menu merge: %v", err)
	}
	if app.split {
		t.Fatal("menu merge left the split open")
	}

	found := false
	for _, path := range app.tabs {
		if path == "backend/store.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("menu merge lost the right tab: %v", app.tabs)
	}
}

// TestSplitSessionSnapshot checks the session carries the split fields and
// never per-tab scroll memory.
func TestSplitSessionSnapshot(t *testing.T) {
	app := newTestApp(t)

	app.toggleSplit()
	app.openFileIn(1, "backend/store.go")
	app.setSplitRatio(0.6)

	state := app.sessionState()
	if !state.Split || state.FocusGroup != 1 || state.SplitRatio != 0.6 {
		t.Fatalf("session split = %+v", state)
	}
	if len(state.Tabs2) == 0 {
		t.Fatalf("session tabs2 = %v", state.Tabs2)
	}
}

// TestSplitZoomRescalesGroups checks layout zoom rescales both group
// offsets, both horizontal offsets, and remembered per-tab offsets.
func TestSplitZoomRescalesGroups(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	app.toggleSplit()
	app.openFileIn(0, "backend/server.go")
	app.openFileIn(0, "backend/store.go")
	app.openFileIn(1, "backend/store.go")
	app.wrap = false
	app.scrollGroupBy(0, 200) // store.go live at 200
	app.scrollGroupBy(1, 100)
	app.scrollHorizontalFor(0, 40)
	app.scrollHorizontalFor(1, 60)
	app.selectTabIn(0, 0) // parks store.go at 200 in memory, README.md live

	before := app.uiZoom()
	if err := app.setZoom(ctx, before*zoomStep); err != nil {
		t.Fatalf("setZoom: %v", err)
	}
	now := app.uiZoom()

	scale := func(v int) int { return int(float64(v)*now/before + 0.5) }
	if app.scrollGroup[0] != 0 {
		t.Fatalf("group 0 = %d, want rescaled 0", app.scrollGroup[0])
	}
	if app.scrollGroup[1] != scale(100) {
		t.Fatalf("group 1 = %d, want %d", app.scrollGroup[1], scale(100))
	}
	if app.scrollXGrp[0] != scale(40) || app.scrollXGrp[1] != scale(60) {
		t.Fatalf("scrollXGrp = %v, want [%d %d]", app.scrollXGrp, scale(40), scale(60))
	}
	if app.tabScroll[0]["backend/store.go"] != scale(200) {
		t.Fatalf("tab memory = %d, want %d", app.tabScroll[0]["backend/store.go"], scale(200))
	}
}

// TestSplitThumbDragIsolation checks dragging the second rail moves only
// group 1.
func TestSplitThumbDragIsolation(t *testing.T) {
	app := newTestApp(t)

	app.toggleSplit()
	app.openFileIn(0, "backend/server.go")
	app.openFileIn(1, "backend/probe.go")
	app.scrollGroupBy(0, 120)

	if !app.startEditorScrollDrag(1) {
		t.Fatal("second rail did not start")
	}
	app.moveEditorScrollDrag(40)

	if app.scrollGroup[0] != 120 {
		t.Fatalf("group 0 moved by rail-2 drag: %d", app.scrollGroup[0])
	}
	if app.scrollGroup[1] == 0 {
		t.Fatal("rail-2 drag did not move group 1")
	}
}
