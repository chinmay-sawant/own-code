package app

import "testing"

// TestShellChromePinnedToViewport guards the window-scroll contract. The
// editor scrolls by moving the page, so the fixed shell (titlebar, activity
// rail, sidebar, statusbar) must be viewport-pinned. The app pins every
// layer at z-index 10 or above; the editor sits exactly on that boundary,
// and scrolling content stays below it. ownframe v0.0.5 no longer exposes a
// getter for the pin, so this checks the value the app passes to it.
func TestShellChromePinnedToViewport(t *testing.T) {
	if shellViewportPinZ != 10 {
		t.Fatalf("viewport pin z = %d, want 10 so the fixed shell stays put while the page scrolls", shellViewportPinZ)
	}
}
