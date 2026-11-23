package app

import (
	"strings"

	"github.com/chinmay-sawant/ownframe"
)

// popupKeyDown routes one key to the open popup and reports whether it was
// consumed. List mode takes Up/Down/PageUp/PageDown, Enter and Tab (accept),
// and Esc; typed text and Left/Right stay with the editor so the caller can
// feed the filter. Content mode takes Esc and dismisses on caret-moving keys,
// which still reach the editor.
func (a *App) popupKeyDown(key string) bool {
	p := &a.popup
	if !p.open {
		return false
	}

	if key == "escape" {
		a.hidePopup(popupDismissEscape)

		return true
	}

	if p.isList() {
		switch key {
		case "arrowdown":
			a.popupMove(1)

			return true
		case "arrowup":
			a.popupMove(-1)

			return true
		case "pagedown":
			a.popupMove(popupMaxRows)

			return true
		case "pageup":
			a.popupMove(-popupMaxRows)

			return true
		case "enter", "tab":
			return a.popupAccept()
		}

		return false
	}

	switch key {
	case "arrowdown", "arrowup", "arrowleft", "arrowright", "home", "end", "pagedown", "pageup":
		a.hidePopup(popupDismissCaret)
	}

	return false
}

// popupClick handles a click while a popup may be open. It reports whether
// the click is fully handled. A press inside the popup is swallowed so the
// editor keeps its caret; a press anywhere else dismisses the popup and the
// click carries on to its own target.
func (a *App) popupClick(box ownframe.Box) bool {
	if !a.popup.open {
		return false
	}

	action := box.Action

	if index, ok := popupItemAction(action); ok {
		if index < len(a.popup.matches) {
			a.popup.index = index
			a.popupAccept()
		}

		return true
	}

	if action == "popup-close" {
		a.hidePopup(popupDismissClick)

		return true
	}

	if box.ID == "popup" || strings.HasPrefix(box.ID, "popup-") {
		return true
	}

	a.hidePopup(popupDismissClick)

	return false
}

// popupScrolled dismisses the popup when the page scroll offset changes. The
// scroll handler rebuilds the view itself, so this does not.
func (a *App) popupScrolled(offsetY int) {
	if a.popup.open && offsetY != a.lastScroll {
		a.clearPopup(popupDismissScroll)
	}
}
