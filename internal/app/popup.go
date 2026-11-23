package app

import (
	"strconv"
	"strings"
)

// The popup is the reusable floating widget behind completion lists, hover
// cards, and signature help. It is anchored to a window point (usually the
// caret), flips above or below that point, clamps inside the window, and has
// two modes: a keyboard-navigable item list and a read-only content card. The
// model here is pure; popup_view.go paints it and popup_keys.go routes input.
//
// Callers use showPopup / hidePopup and the helpers popupSetItems,
// popupSetFilter, popupMove, popupAccept, and popupCaretMoved.
const (
	popupRowH       = 22  // one list row
	popupPad        = 4   // list padding top and bottom
	popupHeadH      = 20  // filter header line
	popupLineH      = 16  // one content line
	popupContentPad = 18  // content card vertical padding plus border
	popupMaxRows    = 8   // list rows shown before the list scrolls
	popupMaxH       = 280 // tallest content card
	popupMinW       = 180
	popupDefaultW   = 360
	popupMargin     = 8 // clearance from the window edge
	popupGap        = 4 // clearance from the anchor
	popupCharW      = 7 // monospace cell used to estimate wrapping
	popupDefaultWin = 800
)

// popupDismiss names why a popup closed. It is passed to OnDismiss.
type popupDismiss string

const (
	popupDismissEscape popupDismiss = "escape"
	popupDismissClick  popupDismiss = "click"
	popupDismissScroll popupDismiss = "scroll"
	popupDismissCaret  popupDismiss = "caret"
	popupDismissCode   popupDismiss = "program"
)

// popupItem is one list row. Filter is the text the typed filter matches;
// it defaults to Label. Data carries the caller's payload back to OnSelect.
type popupItem struct {
	Label  string
	Detail string
	Kind   string
	Filter string
	Data   any
}

// popupSpec describes one popup. A spec with Items is list mode; otherwise
// Content (Markdown) is shown as a read-only card. AnchorX, AnchorTop, and
// AnchorBottom are window pixels: the popup opens below AnchorBottom when it
// fits, else above AnchorTop. Caret is any token the caller compares with
// popupCaretMoved to dismiss the popup when the caret moves.
type popupSpec struct {
	Label        string
	AnchorX      int
	AnchorTop    int
	AnchorBottom int
	Width        int
	Caret        int
	Trap         bool
	Items        []popupItem
	Filter       string
	Content      string
	OnSelect     func(item popupItem, index int)
	OnDismiss    func(reason popupDismiss)
}

// popupState is the live popup. matches indexes spec.Items after the filter,
// index is the selection within matches, and offset is the first visible row.
type popupState struct {
	open    bool
	spec    popupSpec
	matches []int
	index   int
	offset  int
}

// popupBox is the resolved placement in window pixels.
type popupBox struct {
	Left, Top, Width, Height int
	Above                    bool
}

// popupLayout places a w x h popup against an anchor span inside a winW x
// winH window. It prefers below the anchor, flips above when below is too
// short, and when neither side fits it takes the larger side and shrinks the
// height. The result is always clamped inside the window margins.
func popupLayout(anchorX, anchorTop, anchorBottom, w, h, winW, winH int) popupBox {
	w = min(w, winW-2*popupMargin)
	w = max(w, 1)
	h = max(h, 1)
	left := max(popupMargin, min(anchorX, winW-w-popupMargin))

	below := winH - anchorBottom - popupGap - popupMargin
	above := anchorTop - popupGap - popupMargin

	box := popupBox{Left: left, Width: w}

	switch {
	case h <= below:
		box.Top = anchorBottom + popupGap
		box.Height = h
	case h <= above:
		box.Above = true
		box.Height = h
		box.Top = anchorTop - popupGap - h
	case below >= above:
		box.Height = max(below, 1)
		box.Top = anchorBottom + popupGap
	default:
		box.Above = true
		box.Height = max(above, 1)
		box.Top = anchorTop - popupGap - box.Height
	}

	// A degenerate window still keeps the box on screen.
	if box.Top < popupMargin {
		box.Top = popupMargin
	}

	return box
}

// popupMatch reports whether text contains every rune of filter in order,
// ignoring case. An empty filter matches everything.
func popupMatch(text, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}

	want := []rune(filter)
	i := 0

	for _, r := range strings.ToLower(text) {
		if r == want[i] {
			i++
			if i == len(want) {
				return true
			}
		}
	}

	return false
}

// refilter recomputes matches for the current filter and clamps the
// selection. keep preserves the selected item when it still matches.
func (p *popupState) refilter(keep bool) {
	prev := -1
	if keep && p.index >= 0 && p.index < len(p.matches) {
		prev = p.matches[p.index]
	}

	p.matches = p.matches[:0]

	for i, item := range p.spec.Items {
		text := item.Filter
		if text == "" {
			text = item.Label
		}

		if popupMatch(text, p.spec.Filter) {
			p.matches = append(p.matches, i)
		}
	}

	p.index, p.offset = 0, 0

	for i, m := range p.matches {
		if m == prev {
			p.index = i
		}
	}

	p.reveal()
}

// reveal scrolls the visible window so the selected row is inside it.
func (p *popupState) reveal() {
	if p.index < p.offset {
		p.offset = p.index
	}

	if p.index >= p.offset+popupMaxRows {
		p.offset = p.index - popupMaxRows + 1
	}

	p.offset = max(0, min(p.offset, max(0, len(p.matches)-popupMaxRows)))
}

// isList reports whether the popup is in item-list mode.
func (p *popupState) isList() bool { return len(p.spec.Items) > 0 }

// size returns the unclamped popup size for the current mode.
func (p *popupState) size() (int, int) {
	w := p.spec.Width
	if w <= 0 {
		w = popupDefaultW
	}

	w = max(w, popupMinW)

	if p.isList() {
		rows := max(1, min(len(p.matches), popupMaxRows))
		h := rows*popupRowH + 2*popupPad
		if strings.TrimSpace(p.spec.Filter) != "" {
			h += popupHeadH
		}

		return w, h
	}

	cols := max(8, (w-24)/popupCharW)
	lines := 0

	for _, line := range strings.Split(strings.TrimRight(p.spec.Content, "\n"), "\n") {
		n := len([]rune(line))
		lines += max(1, (n+cols-1)/cols)
	}

	return w, min(popupMaxH, lines*popupLineH+popupContentPad)
}

// box resolves the popup placement in a winW x winH window.
func (p *popupState) box(winW, winH int) popupBox {
	w, h := p.size()

	return popupLayout(p.spec.AnchorX, p.spec.AnchorTop, p.spec.AnchorBottom, w, h, winW, winH)
}

// showPopup opens a popup, replacing any open one without calling its
// OnDismiss. Items start filtered by spec.Filter with the first row selected.
func (a *App) showPopup(spec popupSpec) {
	a.popup = popupState{open: true, spec: spec}
	a.popup.refilter(false)
	a.refreshPopup()
}

// hidePopup closes the popup, calls OnDismiss with the reason, and redraws.
// It reports whether a popup was open.
func (a *App) hidePopup(reason popupDismiss) bool {
	if !a.clearPopup(reason) {
		return false
	}

	a.refreshPopup()

	return true
}

// clearPopup closes the popup and fires OnDismiss but does not rebuild the
// view, for callers that rebuild right after.
func (a *App) clearPopup(reason popupDismiss) bool {
	if !a.popup.open {
		return false
	}

	done := a.popup.spec.OnDismiss
	a.popup = popupState{}

	if done != nil {
		done(reason)
	}

	return true
}

// popupSetItems swaps the list items, keeping the selection when it survives.
func (a *App) popupSetItems(items []popupItem) {
	if !a.popup.open {
		return
	}

	a.popup.spec.Items = items
	a.popup.refilter(true)
	a.refreshPopup()
}

// popupSetFilter sets the item filter text and refilters the list.
func (a *App) popupSetFilter(text string) {
	if !a.popup.open {
		return
	}

	a.popup.spec.Filter = text
	a.popup.refilter(true)
	a.refreshPopup()
}

// popupMove moves the list selection by delta, clamped to the list ends, and
// scrolls it into view. It reports whether the selection changed.
func (a *App) popupMove(delta int) bool {
	p := &a.popup
	if !p.open || !p.isList() || len(p.matches) == 0 {
		return false
	}

	next := max(0, min(p.index+delta, len(p.matches)-1))
	if next == p.index {
		return false
	}

	p.index = next
	p.reveal()
	a.refreshPopup()

	return true
}

// popupAccept closes the popup and calls OnSelect with the selected item. It
// reports whether an item was selected.
func (a *App) popupAccept() bool {
	p := &a.popup
	if !p.open || !p.isList() || p.index < 0 || p.index >= len(p.matches) {
		return false
	}

	index := p.matches[p.index]
	item := p.spec.Items[index]
	pick := p.spec.OnSelect
	a.popup = popupState{}

	if pick != nil {
		pick(item, index)
	}

	a.refreshPopup()

	return true
}

// popupCaretMoved dismisses the popup when the caret token differs from the
// one it opened with.
func (a *App) popupCaretMoved(caret int) bool {
	if !a.popup.open || a.popup.spec.Caret == caret {
		return false
	}

	return a.hidePopup(popupDismissCaret)
}

// popupTrapsFocus reports whether the open popup keeps Tab inside itself.
func (a *App) popupTrapsFocus() bool {
	return a.popup.open && a.popup.spec.Trap
}

// refreshPopup rebuilds the view when the page exists.
func (a *App) refreshPopup() {
	if a.page != nil {
		a.refresh()
	}
}

// popupItemAction parses a popup-item:N action into a filtered row index.
func popupItemAction(action string) (int, bool) {
	rest, ok := strings.CutPrefix(action, "popup-item:")
	if !ok {
		return 0, false
	}

	n, err := strconv.Atoi(rest)

	return n, err == nil && n >= 0
}
