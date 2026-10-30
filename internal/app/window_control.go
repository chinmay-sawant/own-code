package app

import (
	"github.com/hajimehoshi/ebiten/v2"
)

// SetWindowControl enables or disables the drawn titlebar controls. Custom window controls
// is a desktop window only. The web preview keeps the native frame and
// hides the in-page controls.
func (a *App) SetWindowControl(on bool) {
	a.windowControl = on
}

// WindowControl reports whether the drawn titlebar controls are enabled.
func (a *App) WindowControl() bool {
	return a.windowControl
}

// WindowControlDrag moves a borderless window by its titlebar for the desktop
// window's draggable strip. A press on a titlebar button still reaches the
// page as a click when it is released without moving.
func (a *App) WindowControlDrag(x, y int) bool {
	return a.windowControlDrag(x, y)
}

// windowControlDrag moves a borderless own-code window by its titlebar. Ebiten
// hands the cursor in device pixels, so the strip scales with the display
// and the layout zoom. A press on a titlebar button still reaches the page
// as a click when it is released without moving.
func (a *App) windowControlDrag(x, y int) bool {
	return float64(y) < float64(a.zx(windowControlTop))*windowControlDragScale()
}

// windowControlDragScale reads the monitor scale for the drag strip. Without a
// monitor the strip falls back to one CSS pixel per device pixel.
func windowControlDragScale() float64 {
	if monitor := ebiten.Monitor(); monitor != nil {
		return monitor.DeviceScaleFactor()
	}

	return 1
}
