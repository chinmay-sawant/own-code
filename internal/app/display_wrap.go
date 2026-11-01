package app

import "sort"

// Per-file word-wrap overrides. The global wrap flag stays the default and
// the diff view keeps it: diff rows span several files, so an override must
// not leak into currentDiffRows, diffColumns, or wrapDiffRows.

const wrapOverrideCap = sessionMaxPaths

// sortedKeys lists a map's keys in order so sanitize output is stable.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return keys
}

// wrapFor is the effective wrap flag for one file path: the per-file
// override when present, else the global flag.
func (a *App) wrapFor(path string) bool {
	if a.wrapByFile != nil {
		if override, ok := a.wrapByFile[path]; ok {
			return override
		}
	}

	return a.wrap
}

// wrapForGroup is the effective wrap flag for one editor group, resolved
// from the group's active document.
func (a *App) wrapForGroup(group int) bool {
	if doc := a.groupDoc(group); doc != nil {
		return a.wrapFor(doc.Path)
	}

	return a.wrap
}

// wrapScrollXForGroup is the horizontal scroll offset for one editor group.
// Wrap resets it to zero; unwrapped groups share the single scrollX.
func (a *App) wrapScrollXForGroup(group int) int {
	if a.wrapForGroup(group) {
		return 0
	}

	return a.scrollX
}

// setWrapOverride records a per-file override, capped at 32 entries. It
// returns false when the path is rejected.
func (a *App) setWrapOverride(path string, wrap bool) bool {
	clean, ok := cleanSessionPath(path)
	if !ok {
		return false
	}

	if a.wrapByFile == nil {
		a.wrapByFile = map[string]bool{}
	}

	a.wrapByFile[clean] = wrap
	a.pruneWrapOverrides()

	return true
}

// clearWrapOverride drops the per-file override so the file follows the
// global flag again.
func (a *App) clearWrapOverride(path string) {
	if a.wrapByFile == nil {
		return
	}

	delete(a.wrapByFile, path)

	if len(a.wrapByFile) == 0 {
		a.wrapByFile = nil
	}
}

// pruneWrapOverrides keeps the override map inside the persistence budget,
// preferring open files.
func (a *App) pruneWrapOverrides() {
	if len(a.wrapByFile) <= wrapOverrideCap {
		return
	}

	keep := map[string]bool{}
	for _, path := range a.groupPaths() {
		keep[path] = true
	}

	for path := range a.wrapByFile {
		if len(a.wrapByFile) <= wrapOverrideCap {
			break
		}

		if !keep[path] {
			delete(a.wrapByFile, path)
		}
	}

	for path := range a.wrapByFile {
		if len(a.wrapByFile) <= wrapOverrideCap {
			break
		}

		delete(a.wrapByFile, path)
	}
}

// sanitizeWrapOverrides drops bad keys and caps the map for persistence.
func sanitizeWrapOverrides(overrides map[string]bool) map[string]bool {
	if len(overrides) == 0 {
		return nil
	}

	out := make(map[string]bool, min(len(overrides), wrapOverrideCap))

	for _, path := range sortedKeys(overrides) {
		if len(out) >= wrapOverrideCap {
			break
		}

		if clean, ok := cleanSessionPath(path); ok {
			out[clean] = overrides[path]
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// wrapFileLabel is the crumb indicator for the active file: Wrap* when a
// per-file override is present, else Wrap.
func (a *App) wrapFileLabel() string {
	if doc := a.activeDoc(); doc != nil && a.wrapByFile != nil {
		if _, ok := a.wrapByFile[doc.Path]; ok {
			return "Wrap*"
		}
	}

	return "Wrap"
}

// toggleWrapFile cycles the active file's override: On to Off to
// FollowGlobal, with a toast naming each state. Enabling resets the shared
// horizontal scroll; the top source line stays in view.
func (a *App) toggleWrapFile() {
	doc := a.activeDoc()
	if doc == nil {
		a.showToast("info", "Wrap", "Open a file before overriding wrap.")
		a.refresh()

		return
	}

	path := doc.Path
	line := a.topSourceLineFor(path)

	var msg string

	if a.wrapByFile != nil {
		if override, ok := a.wrapByFile[path]; ok {
			if override {
				a.wrapByFile[path] = false
				msg = "Wrap off for this file"
			} else {
				a.clearWrapOverride(path)
				msg = "Following global wrap"
			}
		} else {
			a.setWrapOverride(path, !a.wrap)
			if a.wrapFor(path) {
				a.scrollX = 0
				msg = "Wrap on for this file"
			} else {
				msg = "Wrap off for this file"
			}
		}
	} else {
		a.setWrapOverride(path, !a.wrap)
		if a.wrapFor(path) {
			a.scrollX = 0
			msg = "Wrap on for this file"
		} else {
			msg = "Wrap off for this file"
		}
	}

	a.viewportCols = 0
	a.snapScrollToLine(doc, line)
	a.showToast("info", "Wrap", msg+" ("+baseName(path)+").")
	a.refresh()
}

// topSourceLineFor is the source line at the top of the view for one file
// path, following that file's wrap state.
func (a *App) topSourceLineFor(path string) int {
	doc := a.docs[path]
	if doc == nil {
		return a.scrollY / a.zx(rowH)
	}

	if a.wrapFor(path) {
		return doc.wrapped(a.codeCols()).lineAt(a.scrollY / a.zx(rowH))
	}

	return a.scrollY / a.zx(rowH)
}

// snapScrollToLine keeps one source line on screen after a wrap flip.
func (a *App) snapScrollToLine(doc *fileDoc, line int) {
	if doc != nil && a.wrapFor(doc.Path) {
		a.scrollTo(doc.wrapped(a.codeCols()).firstRow(line) * a.zx(rowH))

		return
	}

	a.scrollTo(line * a.zx(rowH))
}
