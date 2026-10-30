package app

import (
	"github.com/hajimehoshi/ebiten/v2"
)

// SetChrome enables or disables the drawn titlebar controls. Custom chrome
// is a desktop window only. The web preview keeps the native frame and
// hides the in-page controls.
func (a *App) SetChrome(on bool) {
	a.chrome = on
}

// Chrome reports whether the drawn titlebar controls are enabled.
func (a *App) Chrome() bool {
	return a.chrome
}

// ChromeDrag moves a borderless window by its titlebar for the desktop
// window's draggable strip. A press on a titlebar button still reaches the
// page as a click when it is released without moving.
func (a *App) ChromeDrag(x, y int) bool {
	return a.chromeDrag(x, y)
}

// chromeDrag moves a borderless code-view window by its titlebar. Ebiten
// hands the cursor in device pixels, so the strip scales with the display
// and the layout zoom. A press on a titlebar button still reaches the page
// as a click when it is released without moving.
func (a *App) chromeDrag(x, y int) bool {
	return float64(y) < float64(a.zx(chromeTop))*chromeDragScale()
}

// chromeDragScale reads the monitor scale for the drag strip. Without a
// monitor the strip falls back to one CSS pixel per device pixel.
func chromeDragScale() float64 {
	if monitor := ebiten.Monitor(); monitor != nil {
		return monitor.DeviceScaleFactor()
	}

	return 1
}
