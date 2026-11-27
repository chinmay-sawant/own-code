package app

import (
	"context"
	"strings"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

func demoItems(n int) []popupItem {
	items := make([]popupItem, n)
	for i := range items {
		items[i] = popupItem{Label: "item" + string(rune('a'+i%26)) + string(rune('0'+i/26))}
	}

	return items
}

func TestPopupLayoutFlipAndClamp(t *testing.T) {
	// Fits below.
	b := popupLayout(100, 100, 120, 300, 100, 800, 600)
	if b.Above || b.Top != 120+popupGap || b.Left != 100 {
		t.Fatalf("below: %+v", b)
	}

	// Near the bottom: flips above.
	b = popupLayout(100, 500, 520, 300, 120, 800, 600)
	if !b.Above || b.Top+b.Height != 500-popupGap {
		t.Fatalf("flip: %+v", b)
	}

	// Neither side fits: larger side, shrunk, inside the window.
	b = popupLayout(100, 200, 220, 300, 900, 800, 600)
	if b.Height >= 900 || b.Top < 0 || b.Top+b.Height > 600 {
		t.Fatalf("shrink: %+v", b)
	}

	// Right edge and narrow window clamp horizontally.
	b = popupLayout(790, 100, 120, 300, 50, 800, 600)
	if b.Left+b.Width > 800-popupMargin || b.Left < popupMargin {
		t.Fatalf("clamp right: %+v", b)
	}

	b = popupLayout(-50, 100, 120, 900, 50, 400, 600)
	if b.Left != popupMargin || b.Width != 400-2*popupMargin {
		t.Fatalf("clamp narrow: %+v", b)
	}
}

func TestPopupKeyboardNavigation(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	var picked string

	app.showPopup(popupSpec{
		AnchorX: 50, AnchorTop: 100, AnchorBottom: 118,
		Items:    demoItems(20),
		OnSelect: func(it popupItem, _ int) { picked = it.Label },
	})

	if err := app.onKeyDown(ctx, "arrowdown"); err != nil {
		t.Fatal(err)
	}

	if app.popup.index != 1 {
		t.Fatalf("index = %d", app.popup.index)
	}

	for i := 0; i < 12; i++ {
		_ = app.onKeyDown(ctx, "arrowdown")
	}

	p := &app.popup
	if p.index != 13 || p.index < p.offset || p.index >= p.offset+popupMaxRows {
		t.Fatalf("selection not scrolled into view: index=%d offset=%d", p.index, p.offset)
	}

	if rows := app.View().Popup.Rows; len(rows) != popupMaxRows || !rows[len(rows)-1].Selected {
		t.Fatalf("rows window wrong: %+v", rows)
	}

	_ = app.onKeyDown(ctx, "arrowup")
	if app.popup.index != 12 {
		t.Fatalf("up index = %d", app.popup.index)
	}

	if err := app.onKeyDown(ctx, "tab"); err != nil {
		t.Fatal(err)
	}

	if picked != "itemm0" || app.popup.open {
		t.Fatalf("tab accept: picked=%q open=%v", picked, app.popup.open)
	}

	app.showPopup(popupSpec{AnchorBottom: 10, Items: demoItems(3), OnSelect: func(it popupItem, _ int) { picked = it.Label }})
	_ = app.onKeyDown(ctx, "enter")

	if picked != "itema0" {
		t.Fatalf("enter accept: %q", picked)
	}
}

func TestPopupFilter(t *testing.T) {
	app := newTestApp(t)

	app.showPopup(popupSpec{Items: []popupItem{{Label: "ReadFile"}, {Label: "Remove"}, {Label: "Rename", Filter: "rnm"}}})
	app.popupSetFilter("rem")

	if got := app.popup.matches; len(got) != 1 || got[0] != 1 {
		t.Fatalf("matches = %v", got)
	}

	app.popupSetFilter("rn")
	if len(app.popup.matches) != 1 || app.popup.matches[0] != 2 {
		t.Fatalf("custom filter text: %v", app.popup.matches)
	}

	app.popupSetFilter("zzz")
	if !app.View().Popup.Empty {
		t.Fatal("want empty state")
	}

	if app.popupKeyDown("enter") {
		t.Fatal("enter must pass through on empty list")
	}
}

func TestPopupDismissal(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	var reason popupDismiss

	open := func(content bool) {
		spec := popupSpec{Caret: 5, OnDismiss: func(r popupDismiss) { reason = r }}
		if content {
			spec.Content = "hello"
		} else {
			spec.Items = demoItems(2)
		}

		reason = ""
		app.showPopup(spec)
	}

	open(false)
	_ = app.onKeyDown(ctx, "escape")

	if app.popup.open || reason != popupDismissEscape {
		t.Fatalf("escape: %q", reason)
	}

	open(false)

	if app.popupCaretMoved(5) {
		t.Fatal("same caret must keep popup")
	}

	app.popupCaretMoved(6)

	if app.popup.open || reason != popupDismissCaret {
		t.Fatalf("caret: %q", reason)
	}

	open(true)
	_ = app.onKeyDown(ctx, "arrowleft")

	if app.popup.open || reason != popupDismissCaret {
		t.Fatalf("content nav key: %q", reason)
	}

	open(false)

	app.lastScroll = 0
	app.popupScrolled(40)

	if app.popup.open || reason != popupDismissScroll {
		t.Fatalf("scroll: %q", reason)
	}

	open(true)

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	box, ok := boxByID(app.Page().Boxes(), "popup")
	if !ok {
		t.Fatal("popup box missing")
	}

	// A click inside keeps it; a click elsewhere dismisses it.
	if !app.popupClick(box) || !app.popup.open {
		t.Fatal("inside click must be swallowed")
	}

	if app.popupClick(boxFor("somewhere", "mode-changes")) || app.popup.open || reason != popupDismissClick {
		t.Fatalf("click away: %q", reason)
	}
}

func boxFor(id, action string) ownframe.Box { return ownframe.Box{ID: id, Action: action} }

func TestPopupClickSelectsItem(t *testing.T) {
	app := newTestApp(t)

	var picked int

	app.showPopup(popupSpec{Items: demoItems(5), OnSelect: func(_ popupItem, i int) { picked = i }})

	if !app.popupClick(boxFor("popup-item-3", "popup-item:3")) || picked != 3 || app.popup.open {
		t.Fatalf("picked=%d open=%v", picked, app.popup.open)
	}
}

func TestPopupAccessibility(t *testing.T) {
	app := newTestApp(t)

	app.showPopup(popupSpec{Label: "Completions", AnchorBottom: 100, Items: demoItems(4)})

	tree := accessibilityTree(t, app)
	assertFocusableNames(t, tree)

	if _, ok := accessibilityNode(tree, "popup-item-0"); !ok {
		t.Error("popup item missing from the accessibility tree")
	}

	if !strings.Contains(buildHTML(), `aria-label="{{.Popup.Label}}"`) {
		t.Error("popup template lacks its label")
	}

	app.hidePopup(popupDismissCode)
	app.showPopup(popupSpec{Label: "Hover details", AnchorBottom: 100, Content: "## Title\n\ntext `code`", Trap: true})

	if !app.View().Modal {
		t.Error("trap popup must set Modal so Tab stays inside")
	}

	tree = accessibilityTree(t, app)
	assertFocusableNames(t, tree)
	assertAccessibilityName(t, tree, "popup-close", "Close Hover details")
}

func TestPopupPaletteDemo(t *testing.T) {
	app := newTestApp(t)

	if err := app.runPaletteCommand(context.Background(), paletteCmdPopupList); err != nil {
		t.Fatal(err)
	}

	if !app.View().Popup.Show || len(app.View().Popup.Rows) == 0 {
		t.Fatal("demo list popup not shown")
	}

	if err := app.runPaletteCommand(context.Background(), paletteCmdPopupCard); err != nil {
		t.Fatal(err)
	}

	if v := app.View().Popup; !v.Show || v.List || len(v.Lines) == 0 {
		t.Fatalf("demo card popup wrong: %+v", v)
	}
}
