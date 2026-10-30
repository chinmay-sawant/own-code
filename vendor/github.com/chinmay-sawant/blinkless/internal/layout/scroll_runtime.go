package layout

import "sync"

// Scroller runtime for scroll-margin observability (CSS Scroll Snap 1,
// https://drafts.csswg.org/css-scroll-snap-1/#propdef-scroll-margin).
//
// Chrome 143.0.7499.40 uses scroll-margin as the scroll-snap offset of a snap
// target: the margin edges of the target align to the scroll container's
// snapport. This engine has no JavaScript and no user scroll, so static paint
// without a carried offset clips to the padding box exactly as before. The
// runtime below is the smallest step that makes scroll-margin observable:
// each scrollable overflow region carries a settable scroll offset, the
// viewport pairs the padding-box port with that offset, and overflow clipping
// reads the offset-aware port. Snap-target visibility then insets that port by
// the target's used scroll margins through scrollVisibleRect in scroll.go.
//
// Full snapping (choosing a snap offset from scroll-snap-align/type and
// scrolling the region to it) stays follow-up: there is no scrolling pass,
// only the carried offset and the translated clip.
//
// Background-attachment:fixed is intentionally not wired here. It still paints
// as scroll (see background_image.go); anchoring fixed layers to the initial
// viewport would be a separate positioning change, not a natural fall-out of
// carrying one scroller offset.

// testScrollOffsets carries one scroll offset per scrollable region box. The
// map is keyed by box pointer so parallel tests using distinct layouts never
// share keys. Production layout carries zero (absent key reads as zero), which
// keeps static paint identical. Tests set nonzero offsets through
// setTestScrollOffset and remove them with unsetTestScrollOffset.
//
//nolint:gochecknoglobals // test-only per-box store, keyed by pointer
var testScrollOffsets = make(map[*box]ScrollOffset)

// testScrollOffsetsMu guards testScrollOffsets. A plain package mutex keeps the
// store map readable without an embedding struct.
//
//nolint:gochecknoglobals // guards testScrollOffsets
var testScrollOffsetsMu sync.RWMutex

// setTestScrollOffset stores the scroll offset one scrollable region carries.
// It is test-only: production layout never calls it, so every region reads as
// zero unless a test set a value. A zero offset deletes the entry to keep the
// map small and the static path a single miss.
func setTestScrollOffset(boxNode *box, off ScrollOffset) {
	if boxNode == nil {
		return
	}

	testScrollOffsetsMu.Lock()
	defer testScrollOffsetsMu.Unlock()

	if off.X == 0 && off.Y == 0 {
		delete(testScrollOffsets, boxNode)

		return
	}

	testScrollOffsets[boxNode] = off
}

// unsetTestScrollOffset removes any carried offset for one region box. Tests
// defer it after setTestScrollOffset so a reused box address cannot leak an
// offset into a later layout.
func unsetTestScrollOffset(boxNode *box) {
	if boxNode == nil {
		return
	}

	testScrollOffsetsMu.Lock()
	defer testScrollOffsetsMu.Unlock()

	delete(testScrollOffsets, boxNode)
}

// testScrollOffsetOf returns the offset one region box carries, or zero when
// no test set one. Zero is the initial position this engine lays out.
func testScrollOffsetOf(boxNode *box) ScrollOffset {
	if boxNode == nil {
		return ScrollOffset{X: 0, Y: 0}
	}

	testScrollOffsetsMu.RLock()
	defer testScrollOffsetsMu.RUnlock()

	return testScrollOffsets[boxNode]
}

// scrollOffsetAwarePort translates a viewport port into content coordinates by
// the offset the region carries. With the zero offset production carries it
// equals the port, so static clipping is unchanged. It is the zero-margin
// form of scrollVisibleRect: the scroller clip without any one target's
// scroll-margin inset. Per-target margin insets apply through
// scrollVisibleRect in scroll.go.
func scrollOffsetAwarePort(view ScrollViewport) clipRect {
	return clipRect{
		x: view.Port.x + view.At.X,
		y: view.Port.y + view.At.Y,
		w: view.Port.w,
		h: view.Port.h,
	}
}
