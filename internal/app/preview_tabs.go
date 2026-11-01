package app

import "context"

// Preview-tab vs pinned-tab state and the closed-tab stack.
//
// Per-group pin/preview: the same path can be pinned in one pane and a
// preview in the other, so both `pinned` and `previewTab` are indexed by
// group. The Markdown rendered-view flag `App.preview` is unrelated and
// keeps its name; the preview-tab slot is always `previewTab`.
//
// `W` closes the focused group's active tab (not always group 0); see the
// `w` branch in actions.go.
//
// Double-click: the window's Box carries no press count, so there is no
// reliable double-click event to hook. Enter on a tree row, Ctrl+K, and the
// tab context menu are the pin paths.

const (
	// closedStackCap bounds the LIFO closed-tab stack.
	closedStackCap = 20
	// closedSnapshotCapBytes bounds one dirty-text snapshot. A larger dirty
	// file still pushes its path (for reopen) but without text, plus a toast.
	closedSnapshotCapBytes = 256 * 1024
)

// closedTab is one entry on the closed-tab stack. Lines holds a deep copy of
// the document text iff Dirty at close time and the text fits the byte cap.
// The stack is never persisted: session files carry pins, never text.
type closedTab struct {
	Path  string
	Group int
	Index int
	Lines []string
	Dirty bool
}

// ensurePinnedMaps lazily initializes the per-group pin sets so tests that
// assign tabs directly still get safe helpers.
func (a *App) ensurePinnedMaps() {
	for g := 0; g < 2; g++ {
		if a.pinned[g] == nil {
			a.pinned[g] = map[string]bool{}
		}
	}
}

// isPinned reports whether path is pinned in group.
func (a *App) isPinned(group int, path string) bool {
	if group < 0 || group > 1 {
		return false
	}

	return a.pinned[group][path]
}

// isPreviewTab reports whether path is the group's live preview tab: it is
// the preview slot, it is open, and it is not pinned.
func (a *App) isPreviewTab(group int, path string) bool {
	if group < 0 || group > 1 {
		return false
	}

	if a.previewTab[group] == "" || a.previewTab[group] != path {
		return false
	}

	if a.isPinned(group, path) {
		return false
	}

	for _, open := range a.groupTabs(group) {
		if open == path {
			return true
		}
	}

	return false
}

// tabIndexIn returns the strip index of path in group, or -1.
func (a *App) tabIndexIn(group int, path string) int {
	for i, open := range a.groupTabs(group) {
		if open == path {
			return i
		}
	}

	return -1
}

// pinKeyReady reports whether the Ctrl+K / Ctrl+Shift+T chords may run: the
// terminal does not own the keys, no overlay owns the keyboard, and no text
// field owns focus. Verified free: onKeyDown has no Ctrl+K or Ctrl+Shift+T
// branch today, and handleTerminalKey swallows app shortcuts while the shell
// has focus.
func (a *App) pinKeyReady() bool {
	if a == nil || a.page == nil {
		return false
	}

	if a.terminalOwnsKeys() {
		return false
	}

	if a.editing {
		return false
	}

	if a.confirmOpen || a.settingsOpen || a.paletteOpen || a.branchOpen || a.searchOpen || a.fileMenu || a.browseOpen {
		return false
	}

	switch a.page.Form().FocusID() {
	case "palette", "workspace-search", "find", "filter", "branch-filter", "project-path", "drawer-path", "browse-filter", "editor", "edit-surface":
		return false
	}

	return true
}

// pinTabByIndex pins one open tab and clears the preview slot when the tab
// was the preview. Unpin never closes.
func (a *App) pinTabByIndex(group, index int) {
	tabs := a.groupTabs(group)
	if index < 0 || index >= len(tabs) {
		return
	}

	a.ensurePinnedMaps()

	path := tabs[index]
	a.pinned[group][path] = true

	if a.previewTab[group] == path {
		a.previewTab[group] = ""
	}

	a.ensureTabVisible(group)
	a.refresh()
}

// unpinTabByIndex unpins one open tab. It never closes the tab.
func (a *App) unpinTabByIndex(group, index int) {
	tabs := a.groupTabs(group)
	if index < 0 || index >= len(tabs) {
		return
	}

	a.ensurePinnedMaps()
	delete(a.pinned[group], tabs[index])
	a.refresh()
}

// togglePinFocused pins/unpins the focused group's active tab.
func (a *App) togglePinFocused() {
	group := a.focusGroup
	if !a.split {
		group = 0
	}

	tabs := a.groupTabs(group)
	active := a.groupActive(group)
	if len(tabs) == 0 || active < 0 || active >= len(tabs) {
		a.showToast("info", "No tab to pin", "Open a file before pinning it.")
		a.refresh()

		return
	}

	a.ensurePinnedMaps()

	path := tabs[active]
	if a.pinned[group][path] {
		delete(a.pinned[group], path)
		a.showToast("info", "Tab unpinned", path)
	} else {
		a.pinned[group][path] = true
		if a.previewTab[group] == path {
			a.previewTab[group] = ""
		}

		a.showToast("success", "Tab pinned", path)
	}

	a.ensureTabVisible(group)
	a.refresh()
}

// pinPath opens path in group when it is not open, then pins it. This is the
// Enter-on-tree / double-click-fallback / menu Pin path.
func (a *App) pinPath(group int, path string) {
	group = a.normalizeGroup(group)

	if a.tabIndexIn(group, path) < 0 {
		a.openFileAt(group, path, true)

		group = a.normalizeGroup(group)
	}

	if index := a.tabIndexIn(group, path); index >= 0 {
		a.pinTabByIndex(group, index)
	}
}

// autoPinForEdit pins path in every group holding it. Edits auto-pin the
// preview they land in (VS Code behavior), so a dirty preview is never
// silently replaced.
func (a *App) autoPinForEdit(path string) {
	if path == "" {
		return
	}

	a.ensurePinnedMaps()

	for group := 0; group < 2; group++ {
		if group == 1 && !a.split {
			continue
		}

		if a.tabIndexIn(group, path) < 0 {
			continue
		}

		if !a.pinned[group][path] {
			a.pinned[group][path] = true
		}

		if a.previewTab[group] == path {
			a.previewTab[group] = ""
		}
	}
}

// evictIndexForAppend returns the strip index to evict when group is full, or
// -1 when there is nothing to evict (all pinned). Eviction skips pinned tabs
// and takes the oldest unpinned tab.
func (a *App) evictIndexForAppend(group int) int {
	tabs := a.groupTabs(group)
	if len(tabs) < maxTabs {
		return -1
	}

	for i, path := range tabs {
		if !a.isPinned(group, path) {
			return i
		}
	}

	return -1
}

// pushClosed captures one closed tab on the LIFO stack. Dirty tabs deep-copy
// their document lines iff the text fits the byte cap; oversized dirty tabs
// push path-only plus a toast. Clean tabs push path-only. The stack caps at
// closedStackCap (oldest dropped).
func (a *App) pushClosed(group, index int, path string) {
	if path == "" {
		return
	}

	entry := closedTab{Path: path, Group: group, Index: index}

	if a.isDirty(path) {
		entry.Dirty = true

		if doc := a.docs[path]; doc != nil && len(doc.Lines) > 0 {
			size := 0
			for _, line := range doc.Lines {
				size += len(line) + 1
				if size > closedSnapshotCapBytes {
					break
				}
			}

			if size <= closedSnapshotCapBytes {
				lines := make([]string, len(doc.Lines))
				copy(lines, doc.Lines)
				entry.Lines = lines
			} else {
				a.showToast("info", "Unsaved changes too large", path+" closed without its unsaved text; reopen restores the saved file.")
			}
		}
	}

	a.closedTabs = append(a.closedTabs, entry)
	if len(a.closedTabs) > closedStackCap {
		a.closedTabs = append([]closedTab(nil), a.closedTabs[len(a.closedTabs)-closedStackCap:]...)
	}
}

// reopenClosedTab pops the last closed tab and restores it. An already-open
// path focuses instead and discards the snapshot. Otherwise the tab inserts
// at min(index,len) (evicting the oldest unpinned tab when full, never
// pinned); a dirty snapshot wins over any docs/dirty leftover. A group-1
// reopen with no split lands in group 0 plus a toast. The reopened tab is
// pinned so it survives the preview slot.
func (a *App) reopenClosedTab() {
	if len(a.closedTabs) == 0 {
		a.showToast("info", "Nothing to reopen", "No closed tabs in this session.")
		a.refresh()

		return
	}

	entry := a.closedTabs[len(a.closedTabs)-1]
	a.closedTabs = a.closedTabs[:len(a.closedTabs)-1]

	group := entry.Group
	if group == 1 && !a.split {
		group = 0
		a.showToast("info", "Reopened in the first group", entry.Path+" was in the second group, which is closed.")
	}

	if group != 0 && group != 1 {
		group = 0
	}

	a.finishEdit()

	// Already open: focus and discard the snapshot.
	if index := a.tabIndexIn(group, entry.Path); index >= 0 {
		a.ensurePinnedMaps()
		a.pinned[group][entry.Path] = true

		if a.previewTab[group] == entry.Path {
			a.previewTab[group] = ""
		}

		if group == 1 && a.split {
			a.active2 = index
		} else {
			group = 0
			a.active = index
		}

		a.focusGroup = group
		a.mode = "explorer"
		a.ensureTabVisible(group)
		a.refresh()

		return
	}

	tabs := a.groupTabs(group)

	index := entry.Index
	if index < 0 {
		index = len(tabs)
	}

	if index > len(tabs) {
		index = len(tabs)
	}

	if len(tabs) >= maxTabs {
		evict := -1
		for i, path := range tabs {
			if !a.isPinned(group, path) {
				evict = i

				break
			}
		}

		if evict < 0 {
			a.showToast("info", "All tabs pinned", "Unpin a tab before reopening "+entry.Path+".")
			a.refresh()

			return
		}

		a.pushClosed(group, evict, tabs[evict])
		a.clearTabState(group, tabs[evict])
		tabs = append(append([]string(nil), tabs[:evict]...), tabs[evict+1:]...)

		if index > evict {
			index--
		}

		if index > len(tabs) {
			index = len(tabs)
		}
	}

	if a.ws != nil && a.ws.ByPath[entry.Path] == nil {
		a.ws.ensureFileLoaded(entry.Path)
	}

	if a.ws != nil && a.ws.ByPath[entry.Path] == nil {
		if _, err := a.ws.ensureFile(entry.Path); err != nil {
			a.showToast("error", "Cannot reopen", entry.Path+" is not part of this workspace.")
			a.refresh()

			return
		}
	}

	if _, ok := a.docs[entry.Path]; !ok {
		a.docs[entry.Path] = a.ws.openDoc(entry.Path)
	}

	// Snapshot wins over any docs/dirty leftover.
	if entry.Dirty && len(entry.Lines) > 0 {
		if doc := a.docs[entry.Path]; doc != nil {
			lines := make([]string, len(entry.Lines))
			copy(lines, entry.Lines)
			doc.Lines = lines
			doc.Tokens = doc.tokenizeLines(lines)
			doc.Outline = outline(doc.Lang, lines)
			doc.Preview = nil
			doc.PreviewLine = nil
			doc.Wrap = nil
		}

		a.setDirty(entry.Path, true)
	}

	next := make([]string, 0, len(tabs)+1)
	next = append(next, tabs[:index]...)
	next = append(next, entry.Path)
	next = append(next, tabs[index:]...)

	if group == 1 && a.split {
		a.tabs2 = next
		a.active2 = index
	} else {
		group = 0
		a.tabs = next
		a.active = index
	}

	a.ensurePinnedMaps()
	a.pinned[group][entry.Path] = true

	if a.previewTab[group] == entry.Path {
		a.previewTab[group] = ""
	}

	a.focusGroup = group
	a.mode = "explorer"
	a.firstChangePending = ""

	if a.fileNav != nil {
		a.fileNav.Select(entry.Path)
	}

	a.addRecent(entry.Path)
	a.pushNavHistory(entry.Path)

	if a.split {
		a.scrollGroup[group] = 0
		a.splitWheelAccum[group] = 0
		if group == 0 {
			a.scrollY = 0
			if a.page != nil {
				a.page.ScrollTo(0, 0)
			}
		}
	} else {
		a.resetScroll()
	}

	a.ensureTabVisible(group)
	a.refresh()
	a.jumpToFirstChange(entry.Path)
}

// clearTabState drops the preview slot and pin entry for a path leaving its
// group. Callers push the closed stack first.
func (a *App) clearTabState(group int, path string) {
	if a.previewTab[group] == path {
		a.previewTab[group] = ""
	}

	if a.pinned[group] != nil {
		delete(a.pinned[group], path)
	}
}

// closeTabsIn closes every tab in group where keep returns false. Pinned tabs
// are always kept unless includePinned is set (Close All and single closes
// pass true). Every removed tab pushes the closed stack in strip order. It
// returns the kept count and the pinned-skipped count.
func (a *App) closeTabsIn(group int, keep func(index int, path string) bool, includePinned bool) (kept, skippedPinned int) {
	group = a.normalizeGroup(group)

	tabs := append([]string(nil), a.groupTabs(group)...)
	if len(tabs) == 0 {
		return 0, 0
	}

	oldActive := a.groupActive(group)
	oldActivePath := ""
	if oldActive >= 0 && oldActive < len(tabs) {
		oldActivePath = tabs[oldActive]
	}

	a.finishEdit()

	keptTabs := make([]string, 0, len(tabs))
	removed := 0
	unsaved := 0

	for i, path := range tabs {
		wantKeep := keep != nil && keep(i, path)

		if !includePinned && a.isPinned(group, path) {
			keptTabs = append(keptTabs, path)
			kept++

			if !wantKeep {
				skippedPinned++
			}

			continue
		}

		if wantKeep {
			keptTabs = append(keptTabs, path)
			kept++

			continue
		}

		a.pushClosed(group, i, path)
		if a.isDirty(path) {
			unsaved++
		}

		a.clearTabState(group, path)
		removed++
	}

	if group == 1 && a.split {
		a.tabs2 = keptTabs
	} else {
		group = 0
		a.tabs = keptTabs
	}

	newActive := 0
	if len(keptTabs) > 0 {
		newActive = 0

		if oldActivePath != "" {
			for i, path := range keptTabs {
				if path == oldActivePath {
					newActive = i

					break
				}
			}

			if newActive == 0 && oldActivePath != keptTabs[0] {
				newActive = min(oldActive, len(keptTabs)-1)
				if newActive < 0 {
					newActive = 0
				}
			}
		}
	}

	if group == 1 {
		a.active2 = newActive
		if len(a.tabs2) == 0 {
			a.split = false
			a.focusGroup = 0
			a.resetGroupScroll(1)
			group = 0
		}
	} else {
		a.active = newActive
	}

	if skippedPinned > 0 {
		a.showToast("info", "Kept pinned tabs", pluralTabs(skippedPinned)+" stayed open.")
	} else if removed > 0 && unsaved > 0 {
		a.showToast("info", "Tabs closed", pluralTabs(removed)+" closed ("+pluralUnsaved(unsaved)+" — Ctrl+Shift+T to reopen).")
	}

	if a.split {
		switch {
		case group == 1 && len(a.tabs2) == 0:
			a.split = false
			a.focusGroup = 0
			group = 0
			a.resetGroupScroll(1)
		case group == 0 && len(a.tabs) == 0 && len(a.tabs2) > 0:
			// Last tabs of the left while split: collapse onto the
			// renumbered right instead of showing an empty left.
			a.adoptRightGroup()
			group = 0
		case group == 0 && len(a.tabs) == 0:
			a.split = false
			a.focusGroup = 0
			group = 0
			a.resetScroll()
		default:
			// The bulk close retargets the active tab: the live offset
			// belonged to the old active tab, and the new active tab
			// restores its own. The other pane is untouched.
			newActivePath := ""
			if tabs := a.groupTabs(group); newActive >= 0 && newActive < len(tabs) {
				newActivePath = tabs[newActive]
			}

			a.saveRestoreGroupScroll(group, oldActivePath, newActivePath)
		}
	} else {
		a.resetScroll()
	}

	a.ensureTabVisible(group)
	if a.split {
		a.ensureTabVisible(1 - group)
	} else {
		a.ensureTabVisible(0)
	}

	a.refresh()

	return kept, skippedPinned
}

func pluralTabs(n int) string {
	if n == 1 {
		return "1 tab"
	}

	return itoa(n) + " tabs"
}

func pluralUnsaved(n int) string {
	if n == 1 {
		return "1 unsaved"
	}

	return itoa(n) + " unsaved"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	neg := n < 0
	if neg {
		n = -n
	}

	var buf [20]byte
	pos := len(buf)

	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}

	if neg {
		pos--
		buf[pos] = '-'
	}

	return string(buf[pos:])
}

// paletteBulkClose gates a palette bulk close on dirty last-holders, then
// runs the same closeTabsIn the ungated path used. Pinned tabs skipped by
// closeTabsIn never prompt.
func (a *App) paletteBulkClose(group int, keep func(int, string) bool, includePinned bool) error {
	tabs := a.groupTabs(group)

	var closing []string

	for i, p := range tabs {
		if keep != nil && keep(i, p) {
			continue
		}

		if !includePinned && a.isPinned(group, p) {
			continue
		}

		closing = append(closing, p)
	}

	proceed := func(_ context.Context) error {
		_, _ = a.closeTabsIn(group, keep, includePinned)

		return nil
	}

	if a.gateBulkClose(group, closing, proceed) {
		return nil
	}

	return proceed(context.Background())
}

// openFilePreview opens or focuses one file as a preview in the focused
// group: a single-click / palette / search / outline entry point.
func (a *App) openFilePreview(path string) {
	a.openFileAtPreview(a.activeGroup(), path, true)
}

// openFilePreviewIn opens or focuses one file as a preview in a group.
func (a *App) openFilePreviewIn(group int, path string) {
	a.openFileAtPreview(group, path, true)
}

// openFileAtPreview is the preview-tab opener. Recorded opens push nav
// history like the pinned path.
func (a *App) openFileAtPreview(group int, path string, record bool) {
	a.openFileAtMode(group, path, record, false)
}

// openFileAtMode is the shared opener. pinnedOpen selects the pinned matrix
// (append + pin + eviction skipping pinned); preview reuses the group's
// single preview slot in place.
func (a *App) openFileAtMode(group int, path string, record, pinnedOpen bool) {
	if a.ws == nil {
		a.showToast("error", "Not in workspace", path+" is not part of this workspace.")
		a.refresh()

		return
	}

	if a.mergeOpen && a.mergeDirty {
		grp, pth, rec, pin := group, path, record, pinnedOpen
		if a.requestMergeLeave(func(ctx context.Context) error {
			a.openFileAtMode(grp, pth, rec, pin)

			return nil
		}) {
			return
		}
	}

	if a.ws.ByPath[path] == nil {
		a.ws.ensureFileLoaded(path)
	}

	if a.ws.ByPath[path] == nil {
		if _, err := a.ws.ensureFile(path); err != nil {
			a.showToast("error", "Not in workspace", path+" is not part of this workspace.")
			a.refresh()

			return
		}
	}

	if a.mode == "merge" {
		a.leaveMergeMode()
	}

	a.finishEdit()
	a.firstChangePending = ""

	if _, ok := a.docs[path]; !ok {
		a.docs[path] = a.ws.openDoc(path)
	}

	a.cacheDocSymbols(path)

	a.ensurePinnedMaps()

	if group == 1 && a.split {
		group = 1
	} else {
		group = 0
	}

	var tabs []string
	if group == 1 {
		tabs = a.tabs2
	} else {
		tabs = a.tabs
	}

	found := -1
	for i, open := range tabs {
		if open == path {
			found = i

			break
		}
	}

	if found >= 0 {
		if pinnedOpen {
			a.pinned[group][path] = true
			if a.previewTab[group] == path {
				a.previewTab[group] = ""
			}
		} else if !a.isPinned(group, path) {
			// Focusing another unpinned tab moves the preview slot to it.
			a.previewTab[group] = path
		}

		a.finishOpenCommon(group, path, found, record)

		return
	}

	if !pinnedOpen {
		// Preview-replace: a live unpinned preview slot is reused in place.
		if slot := a.previewTab[group]; slot != "" && slot != path {
			if slotIndex := a.tabIndexIn(group, slot); slotIndex >= 0 && !a.isPinned(group, slot) {
				if a.isDirty(slot) {
					// A dirty preview auto-pins instead of being replaced,
					// so its unsaved text is never silently dropped.
					a.pinned[group][slot] = true
					a.previewTab[group] = ""
				} else {
					a.pushClosed(group, slotIndex, slot)
					a.clearTabState(group, slot)

					updated := append([]string(nil), a.groupTabs(group)...)
					updated[slotIndex] = path
					if group == 1 {
						a.tabs2 = updated
					} else {
						a.tabs = updated
					}

					a.previewTab[group] = path
					a.finishOpenCommon(group, path, slotIndex, record)

					return
				}
			} else if slotIndex < 0 {
				a.previewTab[group] = ""
			}
		}
	}

	// Append path (both matrices). Eviction skips pinned tabs.
	current := a.groupTabs(group)
	if len(current) >= maxTabs {
		evict := -1
		for i, open := range current {
			if !a.isPinned(group, open) {
				evict = i

				break
			}
		}

		if evict < 0 {
			a.showToast("info", "All tabs pinned", "Unpin a tab before opening "+path+".")
			a.refresh()

			return
		}

		if a.autoSave == autoSaveOnFocusChange {
			a.autosaveFocusSave()
		}

		if a.isDirty(current[evict]) {
			a.confirmMerge = false
			a.confirmBulkGroup = 0
			a.confirmBulkPaths = nil
			a.confirmProceed = nil
			a.openUnsavedConfirm(unsavedCloseOne, []tabRef{{Group: group, Path: current[evict]}}, "evict-open")
			a.confirmEvictGroup = group
			a.confirmEvictPath = path
			a.confirmEvictPinned = pinnedOpen
			grp, pth, pin := group, path, pinnedOpen
			a.confirmProceed = func(ctx context.Context) error {
				a.openFileAtMode(grp, pth, true, pin)

				return nil
			}

			return
		}

		a.pushClosed(group, evict, current[evict])
		a.clearTabState(group, current[evict])
		current = append(append([]string(nil), current[:evict]...), current[evict+1:]...)
	}

	current = append(current, path)
	found = len(current) - 1

	if group == 1 {
		a.tabs2 = current
	} else {
		a.tabs = current
	}

	if pinnedOpen {
		a.pinned[group][path] = true
		if a.previewTab[group] == path {
			a.previewTab[group] = ""
		}
	} else {
		a.previewTab[group] = path
	}

	a.finishOpenCommon(group, path, found, record)
}

// finishOpenCommon settles focus, nav, scroll, and paint after an open.
func (a *App) finishOpenCommon(group int, path string, index int, record bool) {
	group = a.normalizeGroup(group)

	oldPath := ""
	if tabs := a.groupTabs(group); len(tabs) > 0 {
		if active := a.groupActive(group); active >= 0 && active < len(tabs) {
			oldPath = tabs[active]
		}
	}

	if group == 1 {
		a.active2 = index
	} else {
		a.active = index
	}

	a.focusGroup = group
	a.mode = "explorer"
	a.ensureTabVisible(group)
	// Preview stays on across files; fillCode gates it to Markdown.
	a.inlineCard = inlineCard{}
	a.paletteOpen = false
	a.paletteMode = paletteModeFiles
	a.paletteQuery = ""
	a.symbolQuery = ""
	a.symbolScope = ""
	a.paletteIndex = 0
	if a.fileNav != nil {
		a.fileNav.Select(path)
	}
	a.addRecent(path)
	if record {
		a.pushNavHistory(path)
	}
	if a.split {
		// Per-tab memory: the previously active tab keeps its offset and
		// the opened tab restores its own (or 0). The other pane is
		// untouched.
		a.saveRestoreGroupScroll(group, oldPath, path)
	} else {
		a.resetScroll()
	}
	a.refresh()
	a.jumpToFirstChange(path)
}
