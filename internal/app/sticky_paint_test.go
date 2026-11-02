package app

import (
	"context"
	"strconv"
	"testing"
)

// TestStickyHeaderPaints checks the pinned sticky header actually paints its
// text. The .code-sticky rule once carried z-index 5, which dropped the
// header out of the painted viewport band while hit-testing still worked, so
// the slot rendered blank. Box presence alone cannot catch that: only pixels
// can. The header must show text ink in both wrap modes.
func TestStickyHeaderPaints(t *testing.T) {
	for _, wrap := range []bool{true, false} {
		app := newTestApp(t)
		app.openFile("backend/server.go")

		if app.wrap != wrap {
			app.toggleWrap()
		}

		// Scroll deep, the way the blank-header repro does: a shallow offset
		// keeps the header inside the painted band even with the bug.
		app.scrollTo(2520)
		app.Page().Scroll().SetScrollOffset(0, 2520)

		if err := app.Page().Redraw(context.Background()); err != nil {
			t.Fatal(err)
		}

		if len(app.View().Sticky) == 0 {
			t.Fatalf("wrap=%v: no sticky header scrolled deep", wrap)
		}

		id := "sticky-" + strconv.Itoa(app.View().Sticky[len(app.View().Sticky)-1].Line)
		target, ok := boxByID(app.Page().Boxes(), id)
		if !ok {
			t.Fatalf("wrap=%v: no box %s in rendered page", wrap, id)
		}

		img := mustDecodePNG(t, app.Page().Probe().PNG())

		// The header face and the code body behind it are a few grey steps
		// apart, so a corner sample cannot tell paint from cover-up. Take
		// the most common pixel as the local ground, then count rows with
		// real ink: the 1 px bottom border paints one row on its own, while
		// a 12 px header face paints text across many rows.
		counts := map[[3]int]int{}
		var mode [3]int

		for y := int(target.Y); y < int(target.Y+target.H); y++ {
			for x := int(target.X); x < int(target.X+target.W); x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				px := [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
				counts[px]++

				if counts[px] > counts[mode] {
					mode = px
				}
			}
		}

		rows := 0

		for y := int(target.Y); y < int(target.Y+target.H); y++ {
			line := 0

			for x := int(target.X); x < int(target.X+target.W); x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				dr := int(r>>8) - mode[0]
				dg := int(g>>8) - mode[1]
				db := int(b>>8) - mode[2]

				if dr < 0 {
					dr = -dr
				}

				if dg < 0 {
					dg = -dg
				}

				if db < 0 {
					db = -db
				}

				if dr+dg+db > 30 {
					line++
				}
			}

			if line >= 8 {
				rows++
			}
		}

		if rows < 3 {
			t.Fatalf("wrap=%v sticky %s shows ink on %d rows, want header text", wrap, id, rows)
		}
	}
}
