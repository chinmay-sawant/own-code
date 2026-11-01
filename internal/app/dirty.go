package app

import (
	"sort"
	"strings"
)

// isDirty reports whether path has unsaved edits. The dirty map is the
// single source of truth per path; a missing key reads as clean.
func (a *App) isDirty(path string) bool {
	if path == "" {
		return false
	}

	return a.dirty[path]
}

// setDirty marks path dirty or clean. Clean entries are deleted so the map
// never leaks false entries for closed tabs.
func (a *App) setDirty(path string, d bool) {
	if path == "" {
		return
	}

	if d {
		if a.dirty == nil {
			a.dirty = map[string]bool{}
		}

		a.dirty[path] = true

		return
	}

	delete(a.dirty, path)
}

// dirtyTabs returns the dirty paths in one group's tab order. A negative
// group returns every dirty open path, ordered by group 0 then group 1 and
// de-duplicated, for bulk-close dialogs.
func (a *App) dirtyTabs(group int) []string {
	if group < 0 {
		seen := map[string]bool{}

		var out []string

		for _, path := range append(append([]string(nil), a.tabs...), a.tabs2...) {
			if seen[path] || !a.isDirty(path) {
				seen[path] = true

				continue
			}

			seen[path] = true
			out = append(out, path)
		}

		return out
	}

	var out []string

	for _, path := range a.groupTabs(group) {
		if a.isDirty(path) {
			dup := false

			for _, existing := range out {
				if existing == path {
					dup = true

					break
				}
			}

			if !dup {
				out = append(out, path)
			}
		}
	}

	return out
}

// discardDirty reverts path to the bytes on disk and drops its dirty flag.
// A live edit buffer for path is dropped without folding, so no keystroke
// survives. Paths the workspace no longer lists are forgotten entirely.
func (a *App) discardDirty(path string) {
	if path == "" {
		return
	}

	if a.editing && a.editPath() == path {
		a.editing = false
		a.edit = nil
		a.editCaret = 0
		a.editAnchor = 0
		a.editUndo = nil
		a.editRedo = nil
	}

	if a.ws == nil || a.ws.ByPath[path] == nil {
		delete(a.docs, path)
		delete(a.dirty, path)

		return
	}

	a.docs[path] = a.ws.openDoc(path)
	delete(a.dirty, path)
}

// dirtyPathsSorted lists every dirty path in stable order for status copy.
func (a *App) dirtyPathsSorted() []string {
	var out []string

	for path, d := range a.dirty {
		if d {
			out = append(out, path)
		}
	}

	sort.Strings(out)

	return out
}

// pathOpenInTabs reports whether path is still open in any group.
func (a *App) pathOpenInTabs(path string) bool {
	if path == "" {
		return false
	}

	for _, open := range a.tabs {
		if open == path {
			return true
		}
	}

	for _, open := range a.tabs2 {
		if open == path {
			return true
		}
	}

	return false
}

// cleanStaleDirty drops a dirty entry whose path left every tab. Callers
// that closed the last holder of a clean path use this so false entries
// never linger.
func (a *App) cleanStaleDirty(path string) {
	if !a.pathOpenInTabs(path) {
		// A folded-but-dirty doc whose tab just closed keeps its text in
		// a.docs until the dialog arm decides; only drop clean entries
		// here. The unsaved gate handles dirty holders explicitly.
		if !a.isDirty(path) {
			delete(a.dirty, path)
		}
	}
}

// dirtyGroupPaths is the ordered dirty subset of an explicit close set,
// de-duplicated across groups.
func dirtyGroupPaths(paths []string, isDirty func(string) bool) []string {
	seen := map[string]bool{}

	var out []string

	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] || !isDirty(path) {
			seen[path] = true

			continue
		}

		seen[path] = true
		out = append(out, path)
	}

	return out
}
