package app

import (
	"context"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

// TestSettingsCardsShrunk checks the theme cards dropped 5% from 58px.
func TestSettingsCardsShrunk(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	a.page.SetSize(minWidth, minHeight)
	clickID(t, a, "act-settings")
	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	theme, ok := boxByID(a.page.Boxes(), "theme-midnight")
	if !ok {
		t.Fatal("theme-midnight box is missing")
	}
	if theme.H != 55 {
		t.Fatalf("theme card height = %.0f, want 55", theme.H)
	}
}

// openScrolledSettings opens the dialog at a small window and returns the
// viewport box once the auto rail has been measured.
func openScrolledSettings(t *testing.T, a *App, ctx context.Context) (viewport ownframe.Box) {
	t.Helper()
	clickID(t, a, "act-settings")
	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	if !a.view.SettingsScroll {
		t.Fatal("settings rail is missing on an overflowing tab")
	}
	vp, ok := boxByID(a.page.Boxes(), "settings-viewport")
	if !ok {
		t.Fatal("settings-viewport box is missing")
	}
	return vp
}

// TestSettingsWheelScrollsOverflow checks a wheel over the settings content
// moves the offset to the clamped ends and translates the inner.
func TestSettingsWheelScrollsOverflow(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	a.page.SetSize(minWidth, minHeight)
	vp := openScrolledSettings(t, a, ctx)
	maxOffset := a.settingsContentH - a.settingsViewH
	if maxOffset <= 0 {
		t.Fatalf("nothing overflows: view=%d content=%d", a.settingsViewH, a.settingsContentH)
	}
	before, _ := boxByID(a.page.Boxes(), "settings-scroll")
	cx, cy := vp.X+vp.W/2, vp.Y+vp.H/2
	for i := 0; i < 30 && a.settingsScrollOffset < maxOffset; i++ {
		handled, err := a.onWheel(ctx, cx, cy, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		if !handled {
			t.Fatal("wheel over settings was not swallowed")
		}
	}
	if a.settingsScrollOffset != maxOffset {
		t.Fatalf("wheel offset = %d, want clamped %d", a.settingsScrollOffset, maxOffset)
	}
	after, _ := boxByID(a.page.Boxes(), "settings-scroll")
	if after.Y >= before.Y {
		t.Fatalf("inner did not translate up: %.0f -> %.0f", before.Y, after.Y)
	}
	for i := 0; i < 30 && a.settingsScrollOffset > 0; i++ {
		if _, err := a.onWheel(ctx, cx, cy, 0, 1); err != nil {
			t.Fatal(err)
		}
	}
	if a.settingsScrollOffset != 0 {
		t.Fatalf("wheel back offset = %d, want 0", a.settingsScrollOffset)
	}
}

// TestSettingsThumbDrag checks the edge rail thumb drags to the ends.
func TestSettingsThumbDrag(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	a.page.SetSize(minWidth, minHeight)
	openScrolledSettings(t, a, ctx)
	boxes := a.page.Boxes()
	thumb, ok := boxByID(boxes, "settings-thumb")
	if !ok {
		t.Fatal("settings-thumb box is missing")
	}
	rail, ok := boxByID(boxes, "settings-rail")
	if !ok {
		t.Fatal("settings-rail box is missing")
	}
	if thumb.H >= rail.H {
		t.Fatalf("thumb %.0f is not shorter than rail %.0f", thumb.H, rail.H)
	}
	if thumb.Y < rail.Y || thumb.Y+thumb.H > rail.Y+rail.H {
		t.Fatalf("thumb y=%.0f..%.0f escapes rail y=%.0f..%.0f", thumb.Y, thumb.Y+thumb.H, rail.Y, rail.Y+rail.H)
	}
	claimed, err := a.onDragStart(ctx, thumb)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("settings-thumb drag was not claimed")
	}
	maxOffset := a.settingsContentH - a.settingsViewH
	if err := a.onDragMove(ctx, 0, 100000); err != nil {
		t.Fatal(err)
	}
	if a.settingsScrollOffset != maxOffset {
		t.Fatalf("drag past end = %d, want %d", a.settingsScrollOffset, maxOffset)
	}
	if err := a.onDragMove(ctx, 0, -100000); err != nil {
		t.Fatal(err)
	}
	if a.settingsScrollOffset != 0 {
		t.Fatalf("drag past start = %d, want 0", a.settingsScrollOffset)
	}
	if err := a.onDragEnd(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestSettingsRailAutoHides checks a tab that fits the viewport renders
// no rail, and switching tabs resets the offset.
func TestSettingsRailAutoHides(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	a.page.SetSize(1920, 1200)
	clickID(t, a, "act-settings")
	if err := a.onClick(ctx, ownframe.Box{Action: "settings-tab:files"}); err != nil {
		t.Fatal(err)
	}
	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	if a.view.SettingsScroll {
		t.Fatal("settings rail shows when the tab fits")
	}
	if _, ok := boxByID(a.page.Boxes(), "settings-thumb"); ok {
		t.Fatal("settings-thumb rendered without overflow")
	}
	// Switching tabs parks the offset back at the top.
	if err := a.onClick(ctx, ownframe.Box{Action: "settings-tab:keyboard"}); err != nil {
		t.Fatal(err)
	}
	if a.settingsScrollOffset != 0 {
		t.Fatalf("tab switch offset = %d, want 0", a.settingsScrollOffset)
	}
}
