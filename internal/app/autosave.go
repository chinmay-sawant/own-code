package app

import (
	"context"
	"time"
)

const (
	autoSaveOff           = "off"
	autoSaveAfterDelay    = "afterDelay"
	autoSaveOnFocusChange = "onFocusChange"

	autoSaveDefaultDelay = 1000
	autoSaveMinDelay     = 250
	autoSaveMaxDelay     = 5000
)

// validAutoSave sanitizes the mode; unknown values fall back to off.
func validAutoSave(mode string) string {
	switch mode {
	case autoSaveAfterDelay, autoSaveOnFocusChange:
		return mode
	default:
		return autoSaveOff
	}
}

// clampAutoSaveDelay keeps the debounce inside 250-5000 ms.
func clampAutoSaveDelay(ms int) int {
	if ms < autoSaveMinDelay {
		return autoSaveMinDelay
	}

	if ms > autoSaveMaxDelay {
		return autoSaveMaxDelay
	}

	return ms
}

// firstNonZero returns v when set, or fallback for zero values such as an
// unrestored session delay.
func firstNonZero(v, fallback int) int {
	if v == 0 {
		return fallback
	}

	return v
}

// firstNonEmpty returns v when set, or fallback for empty session strings.
func firstNonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}

// autosaveDelay returns the effective debounce.
func (a *App) autosaveDelay() time.Duration {
	ms := a.autoSaveDelayMs
	if ms < autoSaveMinDelay || ms > autoSaveMaxDelay {
		ms = autoSaveDefaultDelay
	}

	return time.Duration(ms) * time.Millisecond
}

// scheduleAutoSave debounces an afterDelay save. It is called from
// replaceEditRange, restoreEdit, and applyEditText. The timer only requests
// a frame; the authoritative save runs on the app loop in tickWatch, and a
// generation counter drops stale fires.
func (a *App) scheduleAutoSave() {
	if a.autoSave != autoSaveAfterDelay {
		return
	}

	if a.ws == nil || !a.ws.writable() {
		return
	}

	path := a.editPath()
	if path == "" {
		if doc := a.activeDoc(); doc != nil && a.isDirty(doc.Path) {
			path = doc.Path
		}
	}

	if path == "" {
		return
	}

	// Never autosave merge staging, and skip Big/Truncated files on the
	// debounce path (focus-change saves still allow them on navigation).
	if a.mergeOpen && a.mergePath == path {
		return
	}

	if doc := a.docs[path]; doc != nil && doc.Truncated {
		return
	}

	if meta := a.ws.ByPath[path]; meta != nil && meta.Big {
		return
	}

	a.autosaveGen++
	gen := a.autosaveGen
	delay := a.autosaveDelay()

	if a.autosaveTimer != nil {
		a.autosaveTimer.Stop()
	}

	a.autosaveDue = time.Now().Add(delay).UnixNano()

	// Sample workspaces silently skip: writable() already returned false
	// above, so no timer runs and no toast fires.
	a.autosaveTimer = time.AfterFunc(delay, func() {
		_ = gen

		// Never write off-loop: just wake the loop. tickWatch checks the
		// generation and due time before saving.
		if a.page != nil {
			a.page.WantFrameIn(0)
		}
	})
}

// cancelAutoSave drops a pending debounce without saving.
func (a *App) cancelAutoSave() {
	a.autosaveGen++

	if a.autosaveTimer != nil {
		a.autosaveTimer.Stop()
		a.autosaveTimer = nil
	}

	a.autosaveDue = 0
}

// fireAutoSave runs on the loop when the debounce elapses. It saves the live
// buffer path, or the active dirty path when no buffer is live.
func (a *App) fireAutoSave() {
	if a.autoSave != autoSaveAfterDelay {
		return
	}

	if a.ws == nil || !a.ws.writable() {
		return
	}

	path := a.editPath()
	if path == "" {
		if doc := a.activeDoc(); doc != nil && a.isDirty(doc.Path) {
			path = doc.Path
		}
	}

	if path == "" || !a.isDirty(path) {
		return
	}

	if a.mergeOpen && a.mergePath == path {
		return
	}

	if doc := a.docs[path]; doc != nil && doc.Truncated {
		return
	}

	if meta := a.ws.ByPath[path]; meta != nil && meta.Big {
		return
	}

	a.savePathSilent(path)
	a.autosaveDue = 0
}

// autosaveDueNow reports whether a debounced save is ready on the loop.
func (a *App) autosaveDueNow() bool {
	if a.autoSave != autoSaveAfterDelay || a.autosaveDue == 0 {
		return false
	}

	return time.Now().UnixNano() >= a.autosaveDue
}

// autosaveFocusSave saves dirty plain files on navigation when the mode is
// onFocusChange. Sample workspaces silently skip; merge staging is never
// autosaved. Big/Truncated files still save here because navigation is an
// explicit user action.
func (a *App) autosaveFocusSave() {
	if a.autoSave != autoSaveOnFocusChange {
		return
	}

	if a.ws == nil || !a.ws.writable() {
		return
	}

	for _, path := range a.dirtyTabs(-1) {
		if a.mergeOpen && a.mergePath == path {
			continue
		}

		a.savePathSilent(path)
	}

	a.cancelAutoSave()
}

// setAutoSave applies a mode change from settings.
func (a *App) setAutoSave(mode string) {
	a.autoSave = validAutoSave(mode)

	if a.autoSave != autoSaveAfterDelay {
		a.cancelAutoSave()
	}

	a.refresh()
	a.persistSession()
}

// setAutoSaveDelay applies a clamped delay change from settings.
func (a *App) setAutoSaveDelay(ms int) {
	a.autoSaveDelayMs = clampAutoSaveDelay(ms)
	a.refresh()
	a.persistSession()
}

// pollAutoSave is called from tickWatch on the loop.
func (a *App) pollAutoSave(_ context.Context) {
	if a.autosaveDueNow() {
		a.fireAutoSave()
	}
}
