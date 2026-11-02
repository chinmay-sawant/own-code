package app

import (
	"context"
	"testing"
)

// The crumbs bar is a fixed 30 px header: editorBodyTop bakes crumbH into
// every row offset, window slice, and hit-test mapping, and the stylesheet
// keeps every crumbs state (plain, find, find+replace) on that single row.
// Opening find or replace must not move the code body, so headers never
// shift when the code scrolls: TrackTop, the first visible row, and both
// pane geometries stay identical at the same scroll offset.
func TestFindbarLayoutKeepsBodyStatic(t *testing.T) {	ctx := context.Background()

	for _, split := range []bool{false, true} {
		app := newTestApp(t)
		app.openFile("README.md")

		if split {
			app.toggleSplit()
		}

		// Scroll both groups (group 1 only exists in split mode) so the
		// snapshots below compare windowed bodies, not just offset zero.
		app.scrollTo(400)
		if split {
			app.scrollGroupTo(1, 400)
		}

		snap := func() (track, firstNo, paneLeft, paneWidth, secondLeft, secondWidth int) {
			v := app.View()
			track = v.TrackTop
			if len(v.Rows) > 0 {
				firstNo = v.Rows[0].No
			}
			paneLeft, paneWidth = v.PaneLeft, v.PaneWidth
			secondLeft, secondWidth = v.Second.Left, v.Second.Width

			return track, firstNo, paneLeft, paneWidth, secondLeft, secondWidth
		}

		plain := [6]int{}
		{
			track, firstNo, paneLeft, paneWidth, secondLeft, secondWidth := snap()
			plain = [6]int{track, firstNo, paneLeft, paneWidth, secondLeft, secondWidth}
		}

		if err := app.openReplace(ctx); err != nil {
			t.Fatalf("split=%v: open replace: %v", split, err)
		}

		if !app.View().ReplaceOpen {
			t.Fatalf("split=%v: replace bar did not open", split)
		}

		{
			track, firstNo, paneLeft, paneWidth, secondLeft, secondWidth := snap()
			got := [6]int{track, firstNo, paneLeft, paneWidth, secondLeft, secondWidth}
			if got != plain {
				t.Fatalf("split=%v: opening replace moved the body: was %v, now %v", split, plain, got)
			}
		}

		if err := app.closeFind(ctx); err != nil {
			t.Fatalf("split=%v: close find: %v", split, err)
		}

		{
			track, firstNo, paneLeft, paneWidth, secondLeft, secondWidth := snap()
			got := [6]int{track, firstNo, paneLeft, paneWidth, secondLeft, secondWidth}
			if got != plain {
				t.Fatalf("split=%v: closing find moved the body: was %v, now %v", split, plain, got)
			}
		}
	}
}

// The find bar (including the replace row and the validation error) must
// stay inside the 30 px crumbs bar: with wrap enabled the rows spill onto
// the code below, painting header controls over body rows and stealing
// their clicks. Every findbar control box must sit within the crumbs band,
// at 100% and at a scaled zoom (the zoom sheet re-fixes input widths with
// !important, which the single-row rules must beat).
func TestFindbarLayoutStaysInCrumbsBand(t *testing.T) {
	ctx := context.Background()

	for _, zoom := range []float64{1, 1.25} {
		app := newTestApp(t)
		app.openFile("README.md")

		if zoom != 1 {
			if err := app.setZoom(ctx, zoom); err != nil {
				t.Fatalf("zoom %v: setZoom: %v", zoom, err)
			}
		}

		if err := app.openReplace(ctx); err != nil {
			t.Fatalf("zoom %v: open replace: %v", zoom, err)
		}

		if err := app.Page().Redraw(ctx); err != nil {
			t.Fatalf("zoom %v: redraw: %v", zoom, err)
		}

		crumbs, ok := boxByID(app.Page().Boxes(), "crumbs")
		if !ok {
			t.Fatalf("zoom %v: missing crumbs box", zoom)
		}

		bandTop := crumbs.Y
		bandBottom := crumbs.Y + crumbs.H

		ids := []string{
			"find", "find-count",
			"find-case", "find-regex", "find-word",
			"find-prev", "find-next", "find-close",
			"find-replace", "replace-one", "replace-all",
		}

		for _, id := range ids {
			box, ok := boxByID(app.Page().Boxes(), id)
			if !ok {
				t.Fatalf("zoom %v: missing box %s", zoom, id)
			}

			if box.Y < bandTop || box.Y+box.H > bandBottom {
				t.Fatalf("zoom %v: box %s at y %.1f..%.1f escapes crumbs band %.1f..%.1f",
					zoom, id, box.Y, box.Y+box.H, bandTop, bandBottom)
			}
		}
	}
}
