package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// tabRef names one open tab by group and path. Indices shift as tabs close,
// so pending closes store paths and resolve the index at execution.
type tabRef struct {
	Group int
	Path  string
}

const (
	unsavedCloseOne  = "unsaved-close-one"
	unsavedCloseMany = "unsaved-close-many"
)

// savePath writes one dirty path to disk, whether or not it holds the live
// edit buffer. It is the per-path core extracted from saveEdit (live buffer)
// and saveDirtyActive (folded doc). It returns true on success.
func (a *App) savePath(path string) bool {
	if path == "" {
		return false
	}

	// Merge results save through the merge editor so marker checks and the
	// on-disk stamp guard stay in one place. Never write merge staging here.
	if a.mergeOpen && a.mergePath == path {
		return false
	}

	if a.edit != nil && a.edit.Path == path {
		data := a.edit.Save()

		if err := a.ws.writeFile(path, data); err != nil {
			a.showToast("error", "Save failed", err.Error())
			a.refresh()

			return false
		}

		a.edit.MarkSaved()
		a.setDirty(path, false)
		a.docs[path] = a.ws.openDoc(path)
		a.invalidateSymbols(path)
		a.cacheDocSymbols(path)
		a.commitEditBuffer()
		a.showToast("success", "File saved", path)
		a.refresh()

		return true
	}

	doc := a.docs[path]
	if doc == nil {
		return false
	}

	data := []byte(edit.JoinLines(doc.Lines))
	if err := a.ws.writeFile(path, data); err != nil {
		a.showToast("error", "Save failed", err.Error())
		a.refresh()

		return false
	}

	a.setDirty(path, false)
	a.docs[path] = a.ws.openDoc(path)
	a.invalidateSymbols(path)
	a.cacheDocSymbols(path)
	a.showToast("success", "File saved", path)
	a.refresh()

	return true
}

// savePathSilent is savePath without toasts, for autosave. Failures stay
// silent so a debounce does not spam the status card every second.
func (a *App) savePathSilent(path string) bool {
	if path == "" {
		return false
	}

	if a.mergeOpen && a.mergePath == path {
		return false
	}

	if a.edit != nil && a.edit.Path == path {
		data := a.edit.Save()

		if err := a.ws.writeFile(path, data); err != nil {
			return false
		}

		a.edit.MarkSaved()
		a.setDirty(path, false)
		a.docs[path] = a.ws.openDoc(path)
		a.invalidateSymbols(path)
		a.cacheDocSymbols(path)
		a.commitEditBuffer()
		a.refresh()

		return true
	}

	doc := a.docs[path]
	if doc == nil {
		return false
	}

	data := []byte(edit.JoinLines(doc.Lines))
	if err := a.ws.writeFile(path, data); err != nil {
		return false
	}

	a.setDirty(path, false)
	a.docs[path] = a.ws.openDoc(path)
	a.invalidateSymbols(path)
	a.cacheDocSymbols(path)
	a.refresh()

	return true
}

// openUnsavedConfirm opens the Save/Don't Save/Cancel dialog for pending
// dirty tabs. kind is unsaved-close-one or unsaved-close-many; after selects
// the continuation ("close-one", "bulk", "evict-open", "merge-leave",
// "close-split").
func (a *App) openUnsavedConfirm(kind string, pending []tabRef, after string) {
	seen := map[string]bool{}

	var unique []tabRef

	for _, ref := range pending {
		if ref.Path == "" || seen[ref.Path] {
			continue
		}

		seen[ref.Path] = true
		unique = append(unique, tabRef{Group: ref.Group, Path: ref.Path})
	}

	if len(unique) == 0 {
		return
	}

	// Re-check dirtiness at open time: a path autosaved between the collect
	// and the open drops out.
	var still []tabRef

	for _, ref := range unique {
		if a.isDirty(ref.Path) {
			still = append(still, ref)
		}
	}

	if len(still) == 0 {
		return
	}

	sort.Slice(still, func(i, j int) bool { return still[i].Path < still[j].Path })

	paths := make([]string, 0, len(still))
	for _, ref := range still {
		paths = append(paths, ref.Path)
	}

	a.confirmOpen = true
	a.confirmPending = still
	a.confirmAfter = after

	if len(still) == 1 && kind == unsavedCloseOne {
		a.confirmKind = unsavedCloseOne
		a.confirmTitle = fmt.Sprintf("Save changes to %s?", baseName(still[0].Path))
	} else {
		a.confirmKind = unsavedCloseMany
		a.confirmTitle = fmt.Sprintf("Save %d files?", len(still))
	}

	a.confirmMsg = "Your changes will be lost if you don't save them."
	a.confirmPaths = paths
	a.refresh()
}

// closeConfirmUnsaved hides the unsaved dialog without acting.
func (a *App) closeConfirmUnsaved() {
	a.confirmOpen = false
	a.confirmKind = ""
	a.confirmPaths = nil
	a.confirmTitle = ""
	a.confirmMsg = ""
	a.confirmPending = nil
	a.confirmAfter = ""
	a.confirmEvictGroup = 0
	a.confirmEvictPath = ""
	a.confirmEvictPinned = false
	a.confirmBulkGroup = 0
	a.confirmBulkPaths = nil
	a.confirmMerge = false
	a.confirmProceed = nil
	a.refresh()
}

// confirmUnsavedSave saves every pending path, then performs the pending
// action. Merge-aware: when the dialog guards a dirty merge result the Save
// arm writes through saveMergeEditor.
func (a *App) confirmUnsavedSave(ctx context.Context) error {
	if !a.confirmOpen {
		return nil
	}

	pending := append([]tabRef(nil), a.confirmPending...)
	after := a.confirmAfter
	evictGroup, evictPath, evictPinned := a.confirmEvictGroup, a.confirmEvictPath, a.confirmEvictPinned
	bulkGroup, bulkPaths := a.confirmBulkGroup, append([]string(nil), a.confirmBulkPaths...)
	proceed := a.confirmProceed
	isMerge := a.confirmMerge

	if isMerge && a.mergeOpen && a.mergeDirty {
		if !a.saveMergeEditor(ctx) {
			return nil
		}

		a.closeConfirmUnsaved()
		a.finishUnsavedAfter(after, pending, bulkGroup, bulkPaths, evictGroup, evictPath, evictPinned, proceed, ctx)

		return nil
	}

	for _, ref := range pending {
		// A path autosaved while the dialog stood open drops out.
		if !a.isDirty(ref.Path) {
			continue
		}

		if !a.savePath(ref.Path) {
			return nil
		}
	}

	a.closeConfirmUnsaved()
	a.finishUnsavedAfter(after, pending, bulkGroup, bulkPaths, evictGroup, evictPath, evictPinned, proceed, ctx)

	return nil
}

// confirmUnsavedDontSave discards every pending path, then performs the
// pending action.
func (a *App) confirmUnsavedDontSave(ctx context.Context) error {
	if !a.confirmOpen {
		return nil
	}

	_ = ctx

	pending := append([]tabRef(nil), a.confirmPending...)
	after := a.confirmAfter
	evictGroup, evictPath, evictPinned := a.confirmEvictGroup, a.confirmEvictPath, a.confirmEvictPinned
	bulkGroup, bulkPaths := a.confirmBulkGroup, append([]string(nil), a.confirmBulkPaths...)
	proceed := a.confirmProceed
	isMerge := a.confirmMerge

	if isMerge && a.mergeOpen {
		a.closeConfirmUnsaved()
		a.leaveMergeMode()
		a.mode = "changes"
		a.resetScroll()
		a.finishUnsavedAfter(after, pending, bulkGroup, bulkPaths, evictGroup, evictPath, evictPinned, proceed, ctx)

		return nil
	}

	for _, ref := range pending {
		if a.isDirty(ref.Path) {
			a.discardDirty(ref.Path)
		} else {
			delete(a.dirty, ref.Path)
		}
	}

	a.closeConfirmUnsaved()
	a.finishUnsavedAfter(after, pending, bulkGroup, bulkPaths, evictGroup, evictPath, evictPinned, proceed, ctx)

	return nil
}

// finishUnsavedAfter performs the original action once Save or Don't Save
// resolves. A stored proceed closure wins so callers with bespoke close
// behavior (pinned skips, split collapse) keep it.
func (a *App) finishUnsavedAfter(after string, pending []tabRef, bulkGroup int, bulkPaths []string, evictGroup int, evictPath string, evictPinned bool, proceed func(context.Context) error, ctx context.Context) {
	if proceed != nil {
		_ = proceed(ctx)

		return
	}
	switch after {
	case "evict-open":
		// Save/Discard cleared the evicted tab's dirtiness, so reopening
		// now evicts it without a second prompt. Pinned re-opens stay
		// pinned; preview re-opens reuse the preview slot (openFileAt is
		// the pinned matrix, so previews route explicitly).
		if evictPath != "" {
			a.openFileAt(evictGroup, evictPath, true)
		} else {
			a.refresh()
		}
	case "merge-leave":
		a.resetScroll()
		a.refresh()
	case "close-split":
		a.split = false
		a.focusGroup = 0
		a.tabs2 = nil
		a.active2 = 0
		a.resetScroll()
		a.ensureTabVisible(0)
		a.refresh()
	case "bulk":
		a.closeTabsByPaths(bulkGroup, bulkPaths)
	default:
		// "close-one": resolve each pending path to its live index and
		// close descending so indices never shift the wrong tab.
		type indexed struct {
			group, index int
		}

		var targets []indexed

		for _, ref := range pending {
			tabs := a.groupTabs(ref.Group)

			for i, p := range tabs {
				if p == ref.Path {
					targets = append(targets, indexed{group: ref.Group, index: i})

					break
				}
			}
		}

		sort.Slice(targets, func(i, j int) bool {
			if targets[i].group != targets[j].group {
				return targets[i].group > targets[j].group
			}

			return targets[i].index > targets[j].index
		})

		for _, t := range targets {
			a.closeTabIn(t.group, t.index)
		}
	}
}

// requestCloseTabIn gates one tab close on dirtiness. Clean tabs close
// directly; the last holder of a dirty path opens the one-file dialog. A
// path still open in the other group just closes without prompting.
func (a *App) requestCloseTabIn(group, index int) {
	tabs := a.groupTabs(group)
	if index < 0 || index >= len(tabs) {
		return
	}

	path := tabs[index]

	if a.isDirty(path) && !a.pathOpenElsewhere(path, group, index) {
		// On-focus-change autosave makes Save automatic: an autosaved path
		// drops out and never prompts.
		if a.autoSave == autoSaveOnFocusChange {
			a.autosaveFocusSave()
			if !a.isDirty(path) {
				a.closeTabIn(group, index)

				return
			}
		}

		a.confirmMerge = false
		a.confirmBulkGroup = 0
		a.confirmBulkPaths = nil
		a.confirmEvictGroup = 0
		a.confirmEvictPath = ""
		a.openUnsavedConfirm(unsavedCloseOne, []tabRef{{Group: group, Path: path}}, "close-one")

		return
	}

	a.closeTabIn(group, index)
}

// requestCloseTabs gates a bulk close (others/all/left/right). The full close
// set is computed upfront; clean tabs close only after the dialog resolves so
// Cancel restores nothing. Only last holders prompt, de-duplicated.
func (a *App) requestCloseTabs(group int, paths []string) {
	closeSet := map[string]bool{}
	for _, p := range paths {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			closeSet[trimmed] = true
		}
	}

	if len(closeSet) == 0 {
		return
	}

	dirty := dirtyGroupPaths(paths, a.isDirty)

	var lastHolders []tabRef

	for _, p := range dirty {
		holdersOutside := false

		for _, open := range a.tabs {
			if open == p && !closeSet[open] {
				holdersOutside = true

				break
			}
		}

		if !holdersOutside {
			for _, open := range a.tabs2 {
				if open == p && !closeSet[open] {
					holdersOutside = true

					break
				}
			}
		}

		if !holdersOutside {
			lastHolders = append(lastHolders, tabRef{Group: group, Path: p})
		}
	}

	if len(lastHolders) > 0 && a.autoSave == autoSaveOnFocusChange {
		a.autosaveFocusSave()

		var still []tabRef

		for _, ref := range lastHolders {
			if a.isDirty(ref.Path) {
				still = append(still, ref)
			}
		}

		lastHolders = still
	}

	if len(lastHolders) == 0 {
		a.closeTabsByPaths(group, paths)

		return
	}

	kind := unsavedCloseOne
	if len(lastHolders) > 1 {
		kind = unsavedCloseMany
	}

	a.confirmMerge = false
	a.confirmEvictGroup = 0
	a.confirmEvictPath = ""
	a.openUnsavedConfirm(kind, lastHolders, "bulk")
	a.confirmBulkGroup = group
	a.confirmBulkPaths = append([]string(nil), paths...)
}

// pathOpenElsewhere reports whether path is open in any tab other than
// group:index.
func (a *App) pathOpenElsewhere(path string, group, index int) bool {
	for i, open := range a.tabs {
		if open != path {
			continue
		}

		if !(group == 0 && i == index) {
			return true
		}
	}

	for i, open := range a.tabs2 {
		if open != path {
			continue
		}

		if !(group == 1 && i == index) {
			return true
		}
	}

	return false
}

// closeTabsByPaths closes every listed path in group, resolving indices at
// execution and closing descending.
func (a *App) closeTabsByPaths(group int, paths []string) {
	set := map[string]bool{}
	for _, p := range paths {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			set[trimmed] = true
		}
	}

	tabs := a.groupTabs(group)

	var indices []int

	for i, p := range tabs {
		if set[p] {
			indices = append(indices, i)
		}
	}

	sort.Sort(sort.Reverse(sort.IntSlice(indices)))

	for _, idx := range indices {
		a.closeTabIn(group, idx)
	}
}

// unsavedKind reports whether kind is an unsaved-close dialog.
func unsavedKind(kind string) bool {
	return kind == unsavedCloseOne || kind == unsavedCloseMany
}

// gateUnsavedDirty opens the unsaved dialog for pending last-holders and
// stores proceed for the Save/Don't Save arms. It reports whether the
// original action must stop (dialog opened). Callers compute the dirty
// subset; clean paths proceed untouched.
func (a *App) gateUnsavedDirty(pending []tabRef, proceed func(context.Context) error) bool {
	seen := map[string]bool{}

	var still []tabRef

	for _, ref := range pending {
		if ref.Path == "" || seen[ref.Path] || !a.isDirty(ref.Path) {
			seen[ref.Path] = true

			continue
		}

		seen[ref.Path] = true
		still = append(still, ref)
	}

	if len(still) == 0 {
		return false
	}

	if a.autoSave == autoSaveOnFocusChange {
		a.autosaveFocusSave()

		still = still[:0]

		for _, ref := range pending {
			if a.isDirty(ref.Path) {
				still = append(still, ref)
			}
		}

		if len(still) == 0 {
			return false
		}
	}

	kind := unsavedCloseOne
	if len(still) > 1 {
		kind = unsavedCloseMany
	}

	a.confirmMerge = false
	a.confirmEvictGroup = 0
	a.confirmEvictPath = ""
	a.confirmBulkGroup = 0
	a.confirmBulkPaths = nil
	a.confirmProceed = proceed
	a.openUnsavedConfirm(kind, still, "proceed")

	return true
}

// gateBulkClose gates a bulk close over closing (paths in group) on the
// dirty last-holders inside it. proceed performs the original close. It
// reports whether a dialog opened and the caller must stop.
func (a *App) gateBulkClose(group int, closing []string, proceed func(context.Context) error) bool {
	closeSet := map[string]bool{}
	for _, p := range closing {
		if p != "" {
			closeSet[p] = true
		}
	}

	var pending []tabRef

	for path := range closeSet {
		if !a.isDirty(path) {
			continue
		}

		holdersOutside := false

		for _, open := range a.tabs {
			if open == path && !closeSet[open] {
				holdersOutside = true

				break
			}
		}

		if !holdersOutside {
			for _, open := range a.tabs2 {
				if open == path && !closeSet[open] {
					holdersOutside = true

					break
				}
			}
		}

		if !holdersOutside {
			pending = append(pending, tabRef{Group: group, Path: path})
		}
	}

	return a.gateUnsavedDirty(pending, proceed)
}

// requestMergeLeave gates leaving the merge editor when the result is dirty.
// proceed runs when the editor is clean or after the dialog resolves.
// It reports whether the original action must stop.
func (a *App) requestMergeLeave(proceed func(context.Context) error) bool {
	if !a.mergeOpen || !a.mergeDirty {
		return false
	}

	a.confirmMerge = true
	a.confirmBulkGroup = 0
	a.confirmBulkPaths = nil
	a.confirmEvictGroup = 0
	a.confirmEvictPath = ""
	a.confirmProceed = proceed
	a.confirmOpen = true
	a.confirmKind = unsavedCloseOne
	a.confirmPending = []tabRef{{Group: -1, Path: a.mergePath}}
	a.confirmAfter = "proceed"
	a.confirmTitle = fmt.Sprintf("Save changes to %s?", baseName(a.mergePath))
	a.confirmMsg = "Your changes will be lost if you don't save them."
	a.confirmPaths = []string{a.mergePath}
	a.refresh()

	return true
}

// mergeLeaveRequested reports whether a merge guard opened a dialog.
func (a *App) mergeLeaveRequested() bool {
	return a.confirmOpen && a.confirmMerge
}

// requestCloseMergeEditor gates the merge-editor close on a dirty result.
func (a *App) requestCloseMergeEditor() {
	if a.mergeOpen && a.mergeDirty {
		if a.requestMergeLeave(func(ctx context.Context) error {
			a.closeMergeEditor()

			return nil
		}) {
			return
		}
	}

	a.closeMergeEditor()
}
