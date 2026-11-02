package app

import (
	"bytes"
	"context"
	"image/png"
	"testing"
)

// TestSelectedCommitUsesRowHighlightNotLeftBorder guards the history rail
// styling: selecting a commit highlights the whole row instead of painting a
// stray accent border down the sidebar edge.
func TestSelectedCommitUsesRowHighlightNotLeftBorder(t *testing.T) {
	app, hashes := newHistoryApp(t)
	ctx := context.Background()
	short := app.findCommit(hashes[1]).Short

	clickID(t, app, "commit-"+short)
	waitForCommitPatch(t, app)

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	box, ok := boxByID(app.Page().Boxes(), "commit-"+short)
	if !ok {
		t.Fatal("missing selected commit row")
	}

	img, err := png.Decode(bytes.NewReader(app.Page().Probe().PNG()))
	if err != nil {
		t.Fatal(err)
	}

	// The empty strip above the subject avoids the graph and text.
	y := int(box.Y) + 3
	left := img.At(int(box.X)-1, y)
	row := img.At(int(box.X)+int(box.W/2), y)

	lr, lg, lb, _ := left.RGBA()
	rr, rg, rb, _ := row.RGBA()
	if lr>>8 != rr>>8 || lg>>8 != rg>>8 || lb>>8 != rb>>8 {
		t.Fatalf("selected commit left edge = %d,%d,%d, row = %d,%d,%d; want the same highlight",
			lr>>8, lg>>8, lb>>8, rr>>8, rg>>8, rb>>8)
	}
}
