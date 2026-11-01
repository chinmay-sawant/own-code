package app

import "context"

// splitDividerDrag tracks an in-progress split-divider drag.
type splitDividerDrag struct {
	active bool
	start  float64
	dx     float64
}

// splitDividerW is the divider hit width in page pixels.
const splitDividerW = 6

// splitRatioStep is one keyboard nudge of the split divider.
const splitRatioStep = 0.02

// splitCloseMode selects whether closing one split side keeps its tabs.
type splitCloseMode int

const (
	// splitCloseMerge folds the closing side's tabs into the survivor.
	splitCloseMerge splitCloseMode = iota
	// splitCloseDrop discards the closing side's tabs, blocked by dirty tabs.
	splitCloseDrop
)

// toggleSplit opens or closes the second editor group. Opening the split
// mirrors the focused file into the right pane so the new pane is never empty.
func (a *App) toggleSplit() {
	if a.mode != "explorer" {
		a.showToast("info", "Split editor", "Switch to Explorer to open a second code pane.")
		a.refresh()

		return
	}

	if a.split {
		a.finishEdit()
		closing := append([]string(nil), a.tabs2...)
		proceed := func(_ context.Context) error {
			a.split = false
			a.focusGroup = 0
			a.resetScroll()
			a.refresh()

			return nil
		}
		if a.gateBulkClose(1, closing, proceed) {
			return
		}
		_ = proceed(context.Background())

		return
	}

	a.split = true
	a.focusGroup = 1
	// The new right pane starts where the left one is: seed both groups
	// from the shared single-pane offset.
	a.scrollXGrp[0] = a.scrollX
	a.scrollXGrp[1] = a.scrollX

	if len(a.tabs2) == 0 {
		if path := a.leftActivePath(); path != "" {
			a.tabs2 = []string{path}
			a.active2 = 0

			if _, ok := a.docs[path]; !ok {
				a.docs[path] = a.ws.openDoc(path)
			}
		}
	}

	a.resetScroll()
	a.refresh()
}

// leftActivePath returns the first group's active path.
func (a *App) leftActivePath() string {
	if a.active < 0 || a.active >= len(a.tabs) {
		return ""
	}

	return a.tabs[a.active]
}

// normalizeGroup coerces a group for opens when the split is closed.
func (a *App) normalizeGroup(group int) int {
	if group == 1 && a.split {
		return 1
	}

	return 0
}

// setFocusGroup moves keyboard and open-file routing to one pane.
func (a *App) setFocusGroup(group int) {
	if !a.split {
		a.focusGroup = 0

		return
	}

	if group != 0 && group != 1 {
		return
	}

	if group != a.focusGroup {
		a.finishEdit()
		a.focusGroup = group
		a.preview = false
		a.resetFind()
	}

	a.ensureTabVisible(group)
	a.refresh()
}

// focusGroupForClick maps a clicked box id to the pane it belongs to.
// Right-pane ids end in -2 or belong to the second tab strip; left-pane ids
// are the un-suffixed editor boxes. Anything else (tree, drawers, overlays)
// keeps the current focus: ok is false.
func focusGroupForClick(id string) (int, bool) {
	switch id {
	case "editor-window-2", "code-body-2", "crumbs-2", "crumb-path-2",
		"tabstrip-2", "markdown-preview-2":
		return 1, true
	case "editor-window", "code-body", "crumbs", "crumb-path",
		"tabstrip", "edit-surface", "markdown-preview":
		return 0, true
	}

	if len(id) > 2 && id[len(id)-2:] == "-2" {
		return 1, true
	}

	if len(id) >= 5 && id[:5] == "tab2-" {
		return 1, true
	}

	if len(id) >= 4 && id[:4] == "tab-" {
		return 0, true
	}

	if len(id) >= 10 && id[:10] == "edit-line-" {
		return 0, true
	}

	return 0, false
}

// splitDividerGeometry centers the divider hit box on the 1 px gutter
// between the panes.
func (a *App) splitDividerGeometry() (int, int) {
	left, width := a.paneGeometry()
	mid := left + width

	divLeft := mid - splitDividerW/2
	if divLeft < left {
		divLeft = left
	}

	return divLeft, splitDividerW
}

// clampSplitRatio keeps a pane fraction inside the session limits and the
// minimum pane width on both sides.
func (a *App) clampSplitRatio(ratio float64, total int) float64 {
	if total > 2*paneMinWidth {
		if min := float64(paneMinWidth) / float64(total); ratio < min {
			ratio = min
		}
		if max := float64(total-paneMinWidth) / float64(total); ratio > max {
			ratio = max
		}
	}

	return clampSessionRatio(ratio, sessionSplitMin, sessionSplitMax)
}

// setSplitRatio applies one divider ratio. Pixel scroll offsets are already
// in row units, so the top visual row survives the wrap recompute; offsets
// only reclamp when the narrower pane shortens the scroll range. It reports
// whether the ratio moved, so drag ticks can skip refreshes when clamped.
func (a *App) setSplitRatio(ratio float64) bool {
	total := a.editorWidth()
	ratio = a.clampSplitRatio(ratio, total)
	if ratio == a.splitRatio {
		return false
	}

	a.splitRatio = ratio

	for group := 0; group < 2; group++ {
		a.scrollGroup[group] = a.clampGroupScroll(group, a.scrollGroup[group])
		if group == 0 {
			a.scrollY = a.scrollGroup[0]
		}
	}

	a.ensureTabVisible(0)
	a.ensureTabVisible(1)
	a.refresh()
	a.persistSession()

	return true
}

// adoptRightGroup collapses the split by renumbering the right group to
// group 0: tabs, active index, scroll offsets, tab strip window, pin and
// preview-tab state, and per-tab scroll memory all move over, so the
// survivor reads exactly as it did on the right. Single-pane invariants
// hold afterwards.
func (a *App) adoptRightGroup() {
	if active := a.groupActive(1); active >= 0 && active < len(a.tabs2) {
		if a.tabScroll[1] == nil {
			a.tabScroll[1] = map[string]int{}
		}

		a.tabScroll[1][a.tabs2[active]] = a.scrollGroup[1]
	}

	a.tabs = append([]string(nil), a.tabs2...)
	a.active = a.groupActive(1)
	if a.active < 0 || a.active >= len(a.tabs) {
		a.active = 0
	}

	a.scrollGroup[0] = a.scrollGroup[1]
	a.scrollXGrp[0] = a.scrollXGrp[1]
	a.tabStart[0] = a.tabStart[1]

	if a.tabScroll[0] == nil {
		a.tabScroll[0] = map[string]int{}
	}

	for path, offset := range a.tabScroll[1] {
		a.tabScroll[0][path] = offset
	}

	a.ensurePinnedMaps()
	a.pinned[0] = map[string]bool{}
	for path := range a.pinned[1] {
		a.pinned[0][path] = true
	}

	a.previewTab[0] = a.previewTab[1]

	a.tabs2 = nil
	a.active2 = 0
	a.tabStart[1] = 0
	a.scrollGroup[1] = 0
	a.scrollXGrp[1] = 0
	a.splitWheelAccum[1] = 0
	a.tabScroll[1] = map[string]int{}
	a.pinned[1] = map[string]bool{}
	a.previewTab[1] = ""
	a.split = false
	a.focusGroup = 0
}

// closeSplitSide closes one split side. Merge mode folds the closing side's
// tabs into the survivor (deduped, capped at maxTabs with a toast); drop
// mode discards them but aborts with a toast when a closing tab is dirty.
// Closing side 0 renumbers the survivor to group 0 so single-pane
// invariants hold, and focus lands on the survivor.
func (a *App) closeSplitSide(side int, mode splitCloseMode) {
	if !a.split || (side != 0 && side != 1) {
		return
	}

	a.finishEdit()

	closing := a.groupTabs(side)
	if mode == splitCloseDrop {
		for _, path := range closing {
			if a.dirty[path] {
				a.showToast("error", "Unsaved changes", "Save or revert "+baseName(path)+" before dropping this pane.")
				a.refresh()

				return
			}
		}
	}

	survivor := 1 - side
	kept := append([]string(nil), a.groupTabs(survivor)...)
	if mode == splitCloseMerge {
		seen := map[string]bool{}
		for _, path := range kept {
			seen[path] = true
		}

		for _, path := range closing {
			if !seen[path] {
				seen[path] = true
				kept = append(kept, path)
			}
		}
	}

	dropped := 0
	if len(kept) > maxTabs {
		dropped = len(kept) - maxTabs
		kept = kept[:maxTabs]
	}

	survivorActivePath := ""
	if active := a.groupActive(survivor); active >= 0 && active < len(a.groupTabs(survivor)) {
		survivorActivePath = a.groupTabs(survivor)[active]
	}

	survivorActive := 0
	for i, path := range kept {
		if path == survivorActivePath {
			survivorActive = i

			break
		}
	}

	if side == 0 {
		// Merge into the right pane first, then renumber the survivor to
		// group 0 so single-pane invariants hold.
		a.tabs2 = kept
		a.active2 = survivorActive
		a.tabStart[1] = 0
		a.ensureTabVisible(1)
		a.adoptRightGroup()
	} else {
		a.tabs = kept
		if survivorActive >= len(a.tabs) {
			survivorActive = len(a.tabs) - 1
		}

		if survivorActive < 0 {
			survivorActive = 0
		}

		a.active = survivorActive
		a.tabs2 = nil
		a.active2 = 0
		a.tabStart[1] = 0
		a.scrollGroup[1] = 0
		a.scrollXGrp[1] = 0
		a.splitWheelAccum[1] = 0
		a.tabScroll[1] = map[string]int{}
		a.previewTab[1] = ""
		a.split = false
		a.focusGroup = 0
	}

	if len(a.tabs) == 0 {
		a.openInitial()
	}

	if dropped > 0 {
		a.showToast("info", "Pane closed", "Tabs merged; oldest tabs dropped at the tab cap.")
	}

	a.ensureTabVisible(0)
	a.refresh()
}
