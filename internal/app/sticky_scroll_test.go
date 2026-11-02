package app

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// scrollGroupToLine scrolls one group so line is the top source line,
// following the wrapped plan when soft wrap is on.
func scrollGroupToLine(t *testing.T, app *App, group, line int) {
	t.Helper()

	y := line * app.zx(rowH)
	if app.wrap {
		if doc := app.groupDoc(group); doc != nil {
			y = doc.wrapped(app.codeColsForGroup(group)).firstRow(line) * app.zx(rowH)
		}
	}

	app.scrollGroupTo(group, y)
	if err := app.Page().Redraw(context.Background()); err != nil {
		t.Fatalf("redraw: %v", err)
	}
}

// outlineLineOf returns the 0-based outline line for the named symbol.
func outlineLineOf(t *testing.T, app *App, path, name string) int {
	t.Helper()

	doc := app.docs[path]
	if doc == nil {
		t.Fatalf("no doc for %s", path)
	}

	for _, row := range doc.Outline {
		if row.Name == name {
			return row.Line
		}
	}

	t.Fatalf("symbol %q not in outline of %s", name, path)

	return -1
}

func TestStickyShowsEnclosingHeader(t *testing.T) {
	app := newTestApp(t)
	app.openFile("backend/server.go")

	line := outlineLineOf(t, app, "backend/server.go", "checks")
	scrollGroupToLine(t, app, 0, line+3)

	top := app.stickyTopLine(0)
	if top < line {
		t.Fatalf("top source line = %d, want inside checks (starts %d)", top, line)
	}

	sticky := app.View().Sticky
	if len(sticky) == 0 {
		t.Fatal("expected a sticky header deep inside checks")
	}

	last := sticky[len(sticky)-1]
	if last.Name != "checks" || last.Line != line {
		t.Fatalf("sticky = %+v, want enclosing checks at line %d", sticky, line)
	}
}

func TestStickyNestedChainDepth(t *testing.T) {
	app := newTestApp(t)
	app.openFile("tools/report.py")

	classLine := outlineLineOf(t, app, "tools/report.py", "Row")
	methodLine := outlineLineOf(t, app, "tools/report.py", "uptime")
	if methodLine <= classLine {
		t.Fatalf("uptime line %d not inside class Row at %d", methodLine, classLine)
	}

	scrollGroupToLine(t, app, 0, methodLine+2)

	sticky := app.View().Sticky
	if len(sticky) != 2 {
		t.Fatalf("sticky = %+v, want [Row uptime]", sticky)
	}

	if sticky[0].Name != "Row" || sticky[1].Name != "uptime" {
		t.Fatalf("sticky = %+v, want [Row uptime]", sticky)
	}

	if sticky[0].Line != classLine || sticky[1].Line != methodLine {
		t.Fatalf("sticky lines = [%d %d], want [%d %d]", sticky[0].Line, sticky[1].Line, classLine, methodLine)
	}
}

func TestStickyEditUpdatesHeader(t *testing.T) {
	app := newTestApp(t)
	app.openFile("backend/server.go")
	app.toggleEdit()

	line := outlineLineOf(t, app, "backend/server.go", "checks")
	scrollGroupToLine(t, app, 0, line+3)

	before := app.View().Sticky
	if len(before) == 0 {
		t.Fatal("expected a sticky header before the edit")
	}
	oldName := before[len(before)-1].Name

	text := app.edit.Text()
	renamed := oldName + "Renamed"
	next := strings.Replace(text, ") "+oldName+"(", ") "+renamed+"(", 1)
	if next == text {
		t.Fatalf("could not rename func %s in edit buffer", oldName)
	}

	app.edit.SetText(next)
	app.commitEditBuffer()
	app.refresh()

	after := app.View().Sticky
	if len(after) == 0 {
		t.Fatal("expected a sticky header after the edit")
	}

	if got := after[len(after)-1].Name; got != renamed {
		t.Fatalf("sticky after edit = %+v, want renamed %q", after, renamed)
	}
}

func TestStickyClickJumps(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.openFile("backend/server.go")

	line := outlineLineOf(t, app, "backend/server.go", "services")
	scrollGroupToLine(t, app, 0, line+4)

	sticky := app.View().Sticky
	if len(sticky) == 0 {
		t.Fatal("expected a sticky header before clicking")
	}
	// Whatever the top line sits in, clicking its header jumps there.
	target := sticky[len(sticky)-1].Line

	id := "sticky-" + strconv.Itoa(target)
	box, ok := boxByID(app.Page().Boxes(), id)
	if !ok {
		t.Fatalf("no box %s in rendered page", id)
	}

	if err := app.Page().Click(ctx, box.X+box.W/2, box.Y+box.H/2); err != nil {
		t.Fatalf("click %s: %v", id, err)
	}

	want := target * app.zx(rowH)
	if app.wrap {
		if doc := app.groupDoc(0); doc != nil {
			want = doc.wrapped(app.codeColsForGroup(0)).firstRow(target) * app.zx(rowH)
		}
	}

	if app.scrollY != want {
		t.Fatalf("scroll after sticky click = %d, want %d", app.scrollY, want)
	}
}

func TestStickyWrappedAndUnwrapped(t *testing.T) {
	app := newTestApp(t)
	app.openFile("backend/server.go")

	line := outlineLineOf(t, app, "backend/server.go", "history")

	// Wrapped (the default).
	scrollGroupToLine(t, app, 0, line+3)
	wrapped := app.View().Sticky
	if len(wrapped) == 0 || wrapped[len(wrapped)-1].Name != "history" {
		t.Fatalf("wrapped sticky = %+v, want enclosing history", wrapped)
	}

	// Unwrapped keeps the same enclosing symbol at the same top line.
	app.toggleWrap()
	if app.wrap {
		t.Fatal("expected wrap to be off")
	}
	scrollGroupToLine(t, app, 0, line+3)
	unwrapped := app.View().Sticky
	if len(unwrapped) == 0 || unwrapped[len(unwrapped)-1].Name != "history" {
		t.Fatalf("unwrapped sticky = %+v, want enclosing history", unwrapped)
	}
}

func TestStickySplitIndependent(t *testing.T) {
	app := newTestApp(t)
	app.toggleSplit()
	app.openFileIn(0, "backend/server.go")
	app.openFileIn(1, "tools/report.py")

	serverLine := outlineLineOf(t, app, "backend/server.go", "checks")
	methodLine := outlineLineOf(t, app, "tools/report.py", "uptime")

	scrollGroupToLine(t, app, 0, serverLine+3)
	scrollGroupToLine(t, app, 1, methodLine+2)

	v := app.View()
	if len(v.Sticky) == 0 || v.Sticky[len(v.Sticky)-1].Name != "checks" {
		t.Fatalf("group 0 sticky = %+v, want enclosing checks", v.Sticky)
	}

	if len(v.Second.Sticky) != 2 || v.Second.Sticky[1].Name != "uptime" {
		t.Fatalf("group 1 sticky = %+v, want [Row uptime]", v.Second.Sticky)
	}

	// Moving group 1 to the top of its file must not disturb group 0.
	scrollGroupToLine(t, app, 1, 0)
	v = app.View()
	if len(v.Sticky) == 0 || v.Sticky[len(v.Sticky)-1].Name != "checks" {
		t.Fatalf("group 0 sticky after group 1 scroll = %+v", v.Sticky)
	}
	if len(v.Second.Sticky) != 0 {
		t.Fatalf("group 1 sticky at top = %+v, want none", v.Second.Sticky)
	}
}

func TestStickyHiddenInDiffPreviewMerge(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.openFile("backend/server.go")

	line := outlineLineOf(t, app, "backend/server.go", "checks")
	scrollGroupToLine(t, app, 0, line+3)
	if len(app.View().Sticky) == 0 {
		t.Fatal("expected a sticky header in explorer mode")
	}

	// Diff mode shows none.
	app.setMode(ctx, "changes")
	if len(app.View().Sticky) != 0 {
		t.Fatalf("diff sticky = %+v, want none", app.View().Sticky)
	}
	app.setMode(ctx, "explorer")

	// Markdown preview shows none.
	app.openFile("README.md")
	app.togglePreview()
	if !app.View().Preview {
		t.Fatal("expected markdown preview to be on")
	}
	if len(app.View().Sticky) != 0 {
		t.Fatalf("preview sticky = %+v, want none", app.View().Sticky)
	}
	app.togglePreview()

	// Merge mode shows none.
	app.mode = "merge"
	if got := app.stickyForGroup(0); len(got) != 0 {
		t.Fatalf("merge sticky = %+v, want none", got)
	}
	app.mode = "explorer"
}

func TestStickyEmptyAndUnsupported(t *testing.T) {
	app := newTestApp(t)

	// The Makefile has no outline symbols: nothing pins.
	app.openFile("Makefile")
	scrollGroupToLine(t, app, 0, 10)
	if len(app.View().Sticky) != 0 {
		t.Fatalf("makefile sticky = %+v, want none", app.View().Sticky)
	}

	// Before the first symbol there is no enclosing chain either.
	app.openFile("backend/server.go")
	scrollGroupToLine(t, app, 0, 0)
	if len(app.View().Sticky) != 0 {
		t.Fatalf("top-of-file sticky = %+v, want none", app.View().Sticky)
	}
}

func TestStickyToggle(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.openFile("backend/server.go")

	line := outlineLineOf(t, app, "backend/server.go", "checks")
	scrollGroupToLine(t, app, 0, line+3)
	if len(app.View().Sticky) == 0 {
		t.Fatal("expected a sticky header before toggling")
	}

	app.toggleStickyScroll()
	if len(app.View().Sticky) != 0 {
		t.Fatalf("sticky after toggle off = %+v, want none", app.View().Sticky)
	}

	app.toggleStickyScroll()
	if len(app.View().Sticky) == 0 {
		t.Fatal("expected the sticky header back after toggling on")
	}

	// The palette command carries the same toggle.
	found := false
	for _, row := range paletteCommandCatalog() {
		if row.Cmd == paletteCmdStickyScroll {
			found = true
		}
	}
	if !found {
		t.Fatal("palette command catalog has no sticky-scroll toggle")
	}
	if err := app.runPaletteCommand(ctx, paletteCmdStickyScroll); err != nil {
		t.Fatalf("palette sticky toggle: %v", err)
	}
	if len(app.View().Sticky) != 0 {
		t.Fatalf("sticky after palette toggle = %+v, want none", app.View().Sticky)
	}
}

func TestStickyZoomScaling(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.openFile("backend/server.go")

	line := outlineLineOf(t, app, "backend/server.go", "checks")
	scrollGroupToLine(t, app, 0, line+3)

	before := app.View().Sticky
	if len(before) == 0 {
		t.Fatal("expected a sticky header before zooming")
	}

	if err := app.setZoom(ctx, 2); err != nil {
		t.Fatalf("setZoom: %v", err)
	}

	// Zoom re-wraps, so the top line may move; the headers must still
	// match the spec definition for the live top line, with the track
	// shifted by exactly the header block height.
	after := app.View().Sticky
	if len(after) == 0 {
		t.Fatal("expected sticky headers after zooming")
	}

	top := app.stickyTopLine(0)
	doc := app.docs["backend/server.go"]
	want := -1
	for _, row := range doc.Outline {
		if row.Line <= top {
			want = row.Line
		} else {
			break
		}
	}
	if got := after[len(after)-1].Line; got != want {
		t.Fatalf("sticky after zoom ends at line %d, want enclosing %d (top %d)", got, want, top)
	}

	if wantPx := len(after) * app.zx(rowH); app.View().TrackTop != wantPx {
		t.Fatalf("track after zoom = %d, want %d", app.View().TrackTop, wantPx)
	}
}

func TestStickyGutterToggle(t *testing.T) {
	app := newTestApp(t)
	app.openFile("backend/server.go")

	line := outlineLineOf(t, app, "backend/server.go", "checks")
	scrollGroupToLine(t, app, 0, line+3)

	before := app.View().Sticky
	if len(before) == 0 {
		t.Fatal("expected a sticky header with the gutter on")
	}

	app.lineNumbersSet = true
	app.showLineNumbers = false
	app.refresh()

	after := app.View().Sticky
	if len(after) != len(before) {
		t.Fatalf("sticky with gutter off = %+v, want %+v", after, before)
	}
	for i := range before {
		if after[i] != before[i] {
			t.Fatalf("sticky with gutter off = %+v, want %+v", after, before)
		}
	}
}

func TestStickyKeymapChord(t *testing.T) {
	def, ok := defaultChordFor("toggle-sticky")
	if !ok || def == "" {
		t.Fatal("toggle-sticky has no keymap default")
	}

	// T must be free: no other catalog entry may claim it.
	for _, e := range keymapCatalog() {
		if e.Action == "toggle-sticky" {
			continue
		}
		if norm, valid := normalizeChord(e.Default); valid && norm == def {
			t.Fatalf("chord %q also claimed by %s", def, e.Action)
		}
	}

	app := newTestApp(t)
	ctx := context.Background()
	off := app.stickyOff
	if err := app.onKeyDown(ctx, "t"); err != nil {
		t.Fatalf("onKeyDown t: %v", err)
	}
	if app.stickyOff == off {
		t.Fatal("T did not toggle sticky scroll")
	}
}
