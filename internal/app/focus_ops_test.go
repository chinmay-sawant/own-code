package app

import (
	"context"
	"testing"
)

// clickInRedrawBatch clicks id the way the window does: press, caret
// placement, and click inside one redraw batch.
func clickInRedrawBatch(t *testing.T, app *App, id string) {
	t.Helper()

	box, ok := boxByID(app.Page().Boxes(), id)
	if !ok {
		t.Fatalf("no box %s", id)
	}

	ctx := context.Background()
	x, y := box.X+box.W/2, box.Y+box.H/2

	app.Page().Probe().BeginRedrawBatch()

	if err := app.Page().Edit().Press(ctx, x, y); err != nil {
		t.Fatalf("press %s: %v", id, err)
	}

	if err := app.Page().Testing().SelectAt(ctx, x, y); err != nil {
		t.Fatalf("select %s: %v", id, err)
	}

	if err := app.Page().Click(ctx, x, y); err != nil {
		t.Fatalf("click %s: %v", id, err)
	}

	if err := app.Page().Probe().EndRedrawBatch(ctx); err != nil {
		t.Fatalf("end batch %s: %v", id, err)
	}
}

// TestSearchButtonFocusesInput checks the search panel takes the keyboard
// after a click on the activity-bar button. The click batch defers the
// panel's redraw, so focus is queued for the next tick.
func TestSearchButtonFocusesInput(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	clickInRedrawBatch(t, app, "act-search")

	if !app.searchOpen {
		t.Fatal("search did not open")
	}

	if err := app.tickWatch(ctx); err != nil {
		t.Fatal(err)
	}

	if got := app.Page().Form().FocusID(); got != "workspace-search" {
		t.Fatalf("focus = %q, want workspace-search", got)
	}

	if err := app.Page().Edit().Type(ctx, "beacon"); err != nil {
		t.Fatal(err)
	}

	if app.searchQuery != "beacon" {
		t.Fatalf("typed query = %q, want beacon", app.searchQuery)
	}
}

// TestPaletteButtonFocusesInput covers the same batched-open path for quick
// open, opened from the titlebar search bar.
func TestPaletteButtonFocusesInput(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	clickInRedrawBatch(t, app, "cmdbar")

	if !app.paletteOpen {
		t.Fatal("palette did not open")
	}

	if err := app.tickWatch(ctx); err != nil {
		t.Fatal(err)
	}

	if got := app.Page().Form().FocusID(); got != "palette" {
		t.Fatalf("focus = %q, want palette", got)
	}

	if err := app.Page().Edit().Type(ctx, "probe"); err != nil {
		t.Fatal(err)
	}

	if app.paletteQuery != "probe" {
		t.Fatalf("typed query = %q, want probe", app.paletteQuery)
	}
}
