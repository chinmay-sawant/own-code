package app

import "context"

// Settings content scroll. The engine scrolls the page as a whole and has
// no input path that moves an inner overflow:auto box (see
// browse_list_window.go), so the settings dialog owns its offset in Go the
// way the browse dialog and the ui-builder example do: .settings-scroll
// carries the pixel offset as a relative top inside the clipped
// .settings-content viewport, and an edge rail with a proportional thumb
// shows automatically while the tab content overflows.
const (
	// settingsWheelStep is the 1x pixels one wheel notch travels.
	settingsWheelStep = 48
	// settingsThumbMin is the smallest thumb paint, matching the browse rail.
	settingsThumbMin = 18
	// settingsRailPad is the 1x inset matching the rail off the viewport
	// edges; settingsRailW is the 1x rail width.
	settingsRailPad = 4
	settingsRailW   = 8
	// settingsViewportID is the clipped content box, settingsScrollID the
	// translated inner whose height measures the full tab content.
	settingsViewportID = "settings-viewport"
	settingsScrollID   = "settings-scroll"
)

// resetSettingsScroll parks the panel back at the top, used when the dialog
// opens or the tab changes.
func (a *App) resetSettingsScroll() {
	a.settingsScrollOffset = 0
	a.settingsWheelAccum = 0
	a.settingsViewH = 0
	a.settingsContentH = 0
	a.settingsViewX = 0
	a.settingsViewY = 0
	a.settingsViewW = 0
	a.settingsPanelX = 0
	a.settingsPanelY = 0
	a.settingsDrag = false
	a.settingsDragAt = 0
	a.settingsDragScale = 0
}

// settingsMaxOffset is the scrollable range from the last measurement. A
// tab that fits stays at zero.
func (a *App) settingsMaxOffset() int {
	return max(a.settingsContentH-a.settingsViewH, 0)
}

// updateSettingsScrollMetrics remeasures the viewport and the full tab
// content from the last layout and pins the offset to the scrollable range.
// It reports whether anything changed, so open/tab/resize passes can
// refresh once more to publish the rail. Missing boxes (dialog not yet
// painted) leave the stored metrics alone and report false.
func (a *App) updateSettingsScrollMetrics() bool {
	if !a.settingsOpen {
		return false
	}
	boxes := a.page.Boxes()
	vp, ok := findBoxID(boxes, settingsViewportID)
	if !ok {
		return false
	}
	inner, ok := findBoxID(boxes, settingsScrollID)
	if !ok {
		return false
	}
	viewH, contentH := int(vp.H), int(inner.H)
	panel, ok := findBoxID(boxes, "settings-panel")
	if !ok {
		return false
	}
	offset := min(max(a.settingsScrollOffset, 0), max(contentH-viewH, 0))
	if viewH == a.settingsViewH && contentH == a.settingsContentH && offset == a.settingsScrollOffset &&
		int(vp.X) == a.settingsViewX && int(vp.Y) == a.settingsViewY && int(vp.W) == a.settingsViewW &&
		int(panel.X) == a.settingsPanelX && int(panel.Y) == a.settingsPanelY {
		return false
	}
	a.settingsViewH, a.settingsContentH = viewH, contentH
	a.settingsViewX, a.settingsViewY, a.settingsViewW = int(vp.X), int(vp.Y), int(vp.W)
	a.settingsPanelX, a.settingsPanelY = int(panel.X), int(panel.Y)
	a.settingsScrollOffset = offset
	return true
}

// fillSettingsScroll publishes the settings edge-rail geometry. The rail
// shows only while the tab content overflows the viewport.
func (a *App) fillSettingsScroll(v *View) {
	v.SettingsScrollTop = -a.settingsScrollOffset
	if !a.settingsOpen {
		return
	}
	viewH, contentH := a.settingsViewH, a.settingsContentH
	maxOffset := max(contentH-viewH, 0)
	if a.settingsScrollOffset > maxOffset {
		a.settingsScrollOffset = maxOffset
	}
	v.SettingsScrollTop = -a.settingsScrollOffset
	if contentH <= viewH || viewH <= 0 {
		return
	}
	track := viewH - 2*a.zx(settingsRailPad)
	if track <= 0 {
		return
	}
	thumb := track * viewH / contentH
	if thumb < a.zx(settingsThumbMin) {
		thumb = a.zx(settingsThumbMin)
	}
	if thumb > track {
		thumb = track
	}
	top := 0
	if room := track - thumb; maxOffset > 0 && room > 0 {
		top = a.settingsScrollOffset * room / maxOffset
	}
	v.SettingsScroll = true
	v.SettingsTrackH = track
	v.SettingsThumbTop = top
	v.SettingsThumbH = thumb
	// The rail is window-level fixed like the sidebar rails, pinned in
	// viewport coords: the stored viewport box is in page coords, hence
	// the page scroll offset comes off. It lives outside the dialog
	// layer so the fixed overlay cannot swallow its paint.
	sx, sy := a.page.Scroll().ScrollOffset()
	railW := a.zx(settingsRailW)
	pad := a.zx(settingsRailPad)
	v.SettingsRailLeft = a.settingsViewX - sx + a.settingsViewW - railW - pad
	v.SettingsRailTop = a.settingsViewY - sy + pad
}

// redrawSettings refreshes, lays out, then remeasures the settings scroll
// metrics from the fresh boxes: the rail geometry needs a layout that
// already contains the dialog, which the pre-layout buildView cannot see.
// A second pass publishes the rail only when the metrics changed it.
func (a *App) redrawSettings(ctx context.Context) error {
	a.refresh()
	if err := a.page.Redraw(ctx); err != nil {
		return err
	}
	if a.updateSettingsScrollMetrics() {
		a.refresh()
		return a.page.Redraw(ctx)
	}
	return nil
}

// settingsWheelAt scrolls the settings tab when a vertical wheel lands on
// the content viewport. x and y are window coordinates; the dialog is
// modal so the wheel is always swallowed. It returns an error only.
func (a *App) settingsWheelAt(ctx context.Context, x, y, dy float64) error {
	if dy == 0 {
		return a.page.Redraw(ctx)
	}
	sx, sy := a.page.Scroll().ScrollOffset()
	x -= float64(sx)
	y -= float64(sy)
	if vp, ok := findBoxID(a.page.Boxes(), settingsViewportID); !ok ||
		x < vp.X || x > vp.X+vp.W || y < vp.Y || y > vp.Y+vp.H {
		return a.page.Redraw(ctx)
	}
	a.updateSettingsScrollMetrics()
	if maxOffset := a.settingsMaxOffset(); maxOffset > 0 {
		a.settingsWheelAccum += -dy * float64(a.zx(settingsWheelStep))
		step := int(a.settingsWheelAccum)
		a.settingsWheelAccum -= float64(step)
		if step != 0 {
			if next := min(max(a.settingsScrollOffset+step, 0), maxOffset); next != a.settingsScrollOffset {
				a.settingsScrollOffset = next
				a.refresh()
			}
		}
	}
	return a.page.Redraw(ctx)
}

// startSettingsThumbDrag claims a drag on the settings rail thumb. It
// mirrors the browse-thumb claim: dragging the rail starts from the
// current offset.
func (a *App) startSettingsThumbDrag() bool {
	if !a.settingsOpen || !a.view.SettingsScroll {
		return false
	}
	a.updateSettingsScrollMetrics()
	maxOffset := a.settingsMaxOffset()
	track := a.view.SettingsTrackH
	travel := track - a.view.SettingsThumbH
	if maxOffset <= 0 || travel <= 0 {
		return false
	}
	a.settingsDrag = true
	a.settingsDragAt = a.settingsScrollOffset
	a.settingsDragScale = float64(maxOffset) / float64(travel)
	return true
}

// moveSettingsThumbDrag applies a thumb drag delta to the offset.
func (a *App) moveSettingsThumbDrag(dy float64) {
	maxOffset := a.settingsMaxOffset()
	offset := a.settingsDragAt + int(dy*a.settingsDragScale+roundHalf)
	a.settingsScrollOffset = min(max(offset, 0), maxOffset)
}
