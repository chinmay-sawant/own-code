package app

import (
	"encoding/json"
	"strings"
)

// EnableSession restores the state saved for this workspace, if any, and
// starts persisting later changes. The tests call New directly, so they never
// read or write the user config directory.
//
// Precedence is session > settings > builtin: the global settings file
// loads first as the roaming defaults, then a restored session overrides
// theme, zoom, and font size only for fields it actually stored. Old
// sessions without fontSize load as today (missing → global → builtin).
func (a *App) EnableSession() {
	if a.settingsPath == "" {
		if path, err := defaultSettingsPath(); err == nil {
			a.settingsPath = path
		}
	}
	if a.settingsPath != "" {
		if loaded, err := loadSettings(a.settingsPath); err != nil {
			a.showToast("info", "Settings not restored", err.Error())
			a.applySettings(zeroSettings())
		} else {
			a.applySettings(loaded)
		}
		if !a.ws.Sample {
			a.applyScanExcludesToWorkspace()
		}
		_ = a.page.SetTheme(a.activeThemeCSS())
	}

	path, err := defaultSessionPath(a.ws.Root)
	if err != nil {
		return
	}

	a.sessionPath = path

	state, err := loadSession(path)
	if err != nil {
		a.showToast("info", "Session not restored", err.Error())

		return
	}

	raw := state
	state = state.sanitize(a.ws.Root)
	if state.Root == "" || !sameWorkspace(state.Root, a.ws.Root) {
		a.sessionReady = true
		a.persistSession()
		a.persistSettings()

		return
	}

	a.restoreStateWithFallback(state, raw)
	a.sessionReady = true
	a.sessionRestored = true
	a.persistSession()
}

// restoreStateWithFallback applies one saved snapshot. Fields the session
// stored win; missing fields keep the settings defaults applied above.
func (a *App) restoreStateWithFallback(state, raw sessionState) {
	if strings.TrimSpace(raw.Theme) != "" {
		a.theme = validTheme(state.Theme)
	}
	if raw.Zoom != 0 {
		a.zoom = state.Zoom
	}
	if raw.FontSize != 0 {
		a.fontSize = state.FontSize
		if a.fontSize == 0 {
			a.fontSize = a.settings.FontSize
		}
	}
	_ = a.page.SetTheme(a.activeThemeCSS())
	a.restoreState(state)
}

// restoreState applies one saved snapshot.
func (a *App) restoreState(state sessionState) {
	a.autoSave = validAutoSave(state.AutoSave)
	a.autoSaveDelayMs = firstNonZero(state.AutoSaveDelay, autoSaveDefaultDelay)
	a.autoSaveDelayMs = clampAutoSaveDelay(a.autoSaveDelayMs)
	a.mode = "explorer"
	if state.Mode == "changes" {
		a.mode = "changes"
	}

	a.recent = cleanSessionPaths(state.Recents)
	a.tabs = nil
	a.active = 0
	a.tabs2 = nil
	a.active2 = 0
	a.previewTab = [2]string{}
	a.ensurePinnedMaps()
	a.pinned[0] = map[string]bool{}
	a.pinned[1] = map[string]bool{}
	a.closedTabs = nil

	if state.SplitRatio > 0 {
		a.splitRatio = clampSessionRatio(state.SplitRatio, sessionSplitMin, sessionSplitMax)
	}

	if len(state.BranchLogs) > 0 {
		a.branchLogs.Restore(state.BranchLogs)
	}

	for _, path := range cleanSessionPaths(state.Tabs) {
		if a.ws.ByPath[path] == nil {
			a.ws.ensureFileLoaded(path)
		}
		if a.ws.ByPath[path] == nil {
			continue
		}

		if _, ok := a.docs[path]; !ok {
			a.docs[path] = a.ws.openDoc(path)
		}

		a.tabs = append(a.tabs, path)
	}

	if len(a.tabs) > maxTabs {
		a.tabs = a.tabs[:maxTabs]
	}

	if len(a.tabs) > 0 {
		a.active = clampSessionIndex(state.Active, len(a.tabs))
	}

	for _, path := range cleanSessionPaths(state.Tabs2) {
		if a.ws.ByPath[path] == nil {
			a.ws.ensureFileLoaded(path)
		}
		if a.ws.ByPath[path] == nil {
			continue
		}

		if _, ok := a.docs[path]; !ok {
			a.docs[path] = a.ws.openDoc(path)
		}

		a.tabs2 = append(a.tabs2, path)
	}

	if len(a.tabs2) > maxTabs {
		a.tabs2 = a.tabs2[:maxTabs]
	}

	if len(a.tabs2) > 0 {
		a.active2 = clampSessionIndex(state.Active2, len(a.tabs2))
	}

	// Old sessions default every restored tab to pinned so the first
	// single-click appends instead of replacing restored work.
	// New sessions restore the persisted pin sets and preview slots.
	if len(state.Pinned)+len(state.Pinned2) == 0 && state.PreviewTab == "" && state.PreviewTab2 == "" {
		for _, path := range a.tabs {
			a.pinned[0][path] = true
		}

		for _, path := range a.tabs2 {
			a.pinned[1][path] = true
		}
	} else {
		for _, path := range keepSessionPins(state.Pinned, a.tabs) {
			a.pinned[0][path] = true
		}

		for _, path := range keepSessionPins(state.Pinned2, a.tabs2) {
			a.pinned[1][path] = true
		}

		a.previewTab[0] = keepSessionSlot(state.PreviewTab, a.tabs, state.Pinned)
		a.previewTab[1] = keepSessionSlot(state.PreviewTab2, a.tabs2, state.Pinned2)
	}

	a.split = state.Split && len(a.tabs2) > 0
	a.focusGroup = 0
	if a.split && state.FocusGroup == 1 {
		a.focusGroup = 1
	}

	a.details = state.Details
	a.findCase = state.FindCase
	a.replaceRegex = state.FindRegex
	a.replaceWholeWord = state.FindWholeWord
	a.searchCase = state.SearchCase
	a.searchRegex = state.SearchRegex
	a.searchWholeWord = state.SearchWholeWord
	a.wrap = state.Wrap
	a.stickyOff = state.NoSticky
	a.preview = state.Preview
	a.diffSplit = state.DiffSplit
	a.historyManual = state.HistoryManual
	if state.HistoryRatio > 0 {
		a.historyRatio = state.HistoryRatio
	}
	a.drawerExtra = clampDrawerExtra(state.DrawerExtra)

	a.devOn = state.DevTools
	a.page.Window().SetDevTools(state.DevTools)

	if len(a.tabs) == 0 {
		a.openInitial()
	} else {
		a.nav = cleanNavPaths(state.Nav)
		a.navAt = clampHistoryIndex(state.NavAt, len(a.nav))
		if len(a.nav) == 0 {
			a.resetNavHistory(a.activeDocPath())
		} else {
			a.pruneNavHistory()
			if len(a.nav) == 0 {
				a.resetNavHistory(a.activeDocPath())
			}
		}
	}

	if path := a.activeDocPath(); path != "" {
		a.addRecent(path)
	}

	a.ensureTabVisible(0)
	a.ensureTabVisible(1)
	a.resetScroll()
	a.refresh()
}

// activeDocPath is the focused group's active path.
func (a *App) activeDocPath() string {
	if doc := a.activeDoc(); doc != nil {
		return doc.Path
	}

	return ""
}

// pinnedSessionList serializes one group's pin set in strip order so the
// session restores the same pins without snapshot text.
func pinnedSessionList(pins map[string]bool, tabs []string) []string {
	if len(pins) == 0 || len(tabs) == 0 {
		return nil
	}

	var out []string

	for _, path := range tabs {
		if pins[path] {
			out = append(out, path)
		}
	}

	return out
}

// sameWorkspace compares two workspace roots after cleaning.
func sameWorkspace(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

// persistSession writes the state when it differs from the last snapshot.
func (a *App) persistSession() {
	if !a.sessionReady || a.sessionPath == "" {
		return
	}

	state := a.sessionState()
	data, err := json.Marshal(state)
	if err != nil {
		return
	}

	if string(data) == a.sessionLast {
		return
	}

	if err := saveSession(a.sessionPath, state); err != nil {
		return
	}

	a.sessionLast = string(data)
}

// sessionState snapshots the fields that survive a restart.
func (a *App) sessionState() sessionState {
	state := sessionState{
		Root:            a.ws.Root,
		Mode:            a.mode,
		Theme:           a.theme,
		Recents:         append([]string(nil), a.recent...),
		Tabs:            append([]string(nil), a.tabs...),
		Active:          a.active,
		Tabs2:           append([]string(nil), a.tabs2...),
		Active2:         a.active2,
		FocusGroup:      a.focusGroup,
		Split:           a.split,
		SplitRatio:      a.splitRatio,
		Details:         a.details,
		DevTools:        a.devOn,
		Wrap:            a.wrap,
		NoSticky:        a.stickyOff,
		FindCase:        a.findCase,
		FindRegex:       a.replaceRegex,
		FindWholeWord:   a.replaceWholeWord,
		SearchCase:      a.searchCase,
		SearchRegex:     a.searchRegex,
		SearchWholeWord: a.searchWholeWord,
		Preview:         a.preview,
		DiffSplit:       a.diffSplit,
		HistoryRatio:    a.historyRatio,
		HistoryManual:   a.historyManual,
		DrawerExtra:     a.drawerExtra,
		Zoom:            a.uiZoom(),
		FontSize:        a.baseFontSize(),
		Nav:             append([]string(nil), a.nav...),
		NavAt:           a.navAt,
		AutoSave:        validAutoSave(firstNonEmpty(a.autoSave, autoSaveOff)),
		AutoSaveDelay:   clampAutoSaveDelay(firstNonZero(a.autoSaveDelayMs, autoSaveDefaultDelay)),
		Pinned:          pinnedSessionList(a.pinned[0], a.tabs),
		Pinned2:         pinnedSessionList(a.pinned[1], a.tabs2),
		PreviewTab:      a.previewTab[0],
		PreviewTab2:     a.previewTab[1],
	}

	branches := a.branchLogs.Snapshot()
	if len(branches) > 0 {
		state.BranchLogs = branches
	}

	return state
}
