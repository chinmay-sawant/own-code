package app

import (
	"context"
	"strings"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agent"
	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/ownframe"
)

// Right-docked chat chrome: the per-session model dropdown, the session
// history dropdown, and the dock width. Both dropdowns own their scroll
// offset because the engine has no input path for inner overflow boxes.
const (
	openCodeDockDefault   = 400 // base px, scaled by UI zoom
	openCodeDockMin       = 280
	openCodeDockSavedMax  = 4000 // sanity bound for a persisted width
	openCodeDockFloor     = 240  // never narrower, even in a tiny window
	openCodeMenuWheelStep = 40

	openCodeModelMenuID        = "opencode-model-menu"
	openCodeModelMenuInnerID   = "opencode-model-menu-inner"
	openCodeHistoryMenuID      = "opencode-history-menu"
	openCodeHistoryMenuInnerID = "opencode-history-menu-inner"
	openCodeDockGripID         = "opencode-dock-resize"
)

// Model states shown instead of a model name.
const (
	openCodeModelOK          = ""
	openCodeModelMissingKey  = "missing-key"
	openCodeModelNoModels    = "no-models"
	openCodeModelNone        = "none"
	openCodeModelUnavailable = "unavailable"
)

type openCodeModelItem struct {
	Key    string
	Name   string
	Active bool
}

type openCodeModelGroupRow struct {
	Name   string
	Models []openCodeModelItem
}

// openCodeBackend is the registered provider serving agent runs. The registry
// is the only place a backend is named; the UI reads DisplayName from it.
func (a *App) openCodeBackend() agent.ProviderDescriptor {
	if a.openCode.registry == nil {
		a.openCode.registry = agent.NewProviderRegistry()
	}
	d, _ := a.openCode.registry.Default()
	return d
}

// openCodeSelectedModel is the model the composer will use: the selected
// session's own model, else the remembered default.
func (a *App) openCodeSelectedModel() (providerID, modelID string) {
	if id := a.openCode.selectedSessionID; id != "" {
		for _, s := range a.openCode.allSessions {
			if s.ID != id {
				continue
			}
			p, m := s.ProviderID, s.ModelID
			if p == "" || m == "" {
				p, m, _ = strings.Cut(s.Model, "/")
			}
			if p != "" && m != "" {
				return p, m
			}
			break
		}
	}
	st := a.openCodeSettingsView()
	return st.ProviderID, st.ModelID
}

// fillOpenCodeChrome fills the dock, model, and history view fields.
func (a *App) fillOpenCodeChrome(v *openCodePanelView) {
	backend := a.openCodeBackend()
	v.ProviderName = backend.DisplayName
	v.DockWidth = a.agentDockWidth()
	v.HistoryOpen = a.openCode.historyOpen
	v.HistoryTop = -a.openCode.historyScroll.offset()
	v.ModelMenuOpen = a.openCode.modelMenuOpen
	v.ModelMenuTop = -a.openCode.modelScroll.offset()
	v.SessionTitle = "New chat"
	for _, s := range v.Sessions {
		if s.Active {
			v.SessionTitle = s.Title
		}
	}

	curP, curM := a.openCodeSelectedModel()
	if a.openCode.connected {
		for _, g := range agent.GroupEligible(backend.ID, a.openCode.consoleModels) {
			row := openCodeModelGroupRow{Name: g.ProviderName}
			for _, m := range g.Models {
				active := m.ProviderID == curP && m.ModelID == curM
				row.Models = append(row.Models, openCodeModelItem{Key: m.Key(), Name: m.Name, Active: active})
				if active {
					v.ModelLabel = m.Name
				}
			}
			v.ModelGroups = append(v.ModelGroups, row)
		}
	}
	switch {
	case !a.openCode.connected:
		v.ModelState, v.ModelLabel = openCodeModelMissingKey, "No API key"
		v.ModelNote = "Add a " + backend.DisplayName + " key in Settings to choose a model."
	case len(v.ModelGroups) == 0:
		v.ModelState, v.ModelLabel = openCodeModelNoModels, "No available model"
		v.ModelNote = "The catalog has no chat model with tool support for this key."
	case curP == "" || curM == "":
		v.ModelState, v.ModelLabel = openCodeModelNone, "Select a model"
	case v.ModelLabel == "":
		v.ModelState, v.ModelLabel = openCodeModelUnavailable, curM+" (unavailable)"
		v.ModelNote = "This model is no longer in the catalog. Pick another."
	}
}

func sameOpenCodeModelGroups(a, b []openCodeModelGroupRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || len(a[i].Models) != len(b[i].Models) {
			return false
		}
		for j := range a[i].Models {
			if a[i].Models[j] != b[i].Models[j] {
				return false
			}
		}
	}
	return true
}

// dismissOpenCodeDropdowns closes the history and model dropdowns when a click
// lands outside them. Their own toggles, rows, and anything geometrically
// inside an open menu are left to the normal action handlers.
func (a *App) dismissOpenCodeDropdowns(box ownframe.Box) {
	if a == nil || a.page == nil || !(a.openCode.historyOpen || a.openCode.modelMenuOpen) {
		return
	}
	if strings.HasPrefix(box.Action, "opencode-panel-model-") || strings.HasPrefix(box.Action, "opencode-panel-history-") ||
		strings.HasPrefix(box.Action, "opencode-session-pick:") {
		return
	}
	cx, cy := box.X+box.W/2, box.Y+box.H/2
	for _, m := range []struct {
		open bool
		id   string
	}{{a.openCode.modelMenuOpen, openCodeModelMenuID}, {a.openCode.historyOpen, openCodeHistoryMenuID}} {
		if !m.open {
			continue
		}
		if vp, ok := findBoxID(a.page.Boxes(), m.id); ok && cx >= vp.X && cx <= vp.X+vp.W && cy >= vp.Y && cy <= vp.Y+vp.H {
			return
		}
	}
	a.openCode.historyOpen, a.openCode.modelMenuOpen = false, false
	a.refresh()
}

// openCodeChromeAction handles the model and history dropdown actions.
func (a *App) openCodeChromeAction(action string) error {
	switch {
	case action == "opencode-panel-model-toggle":
		a.openCode.modelMenuOpen = !a.openCode.modelMenuOpen
		a.openCode.historyOpen = false
		a.openCode.modelScroll = openCodeTranscriptScroll{detached: true}
		a.refresh()
		return nil
	case action == "opencode-panel-history-toggle":
		a.openCode.historyOpen = !a.openCode.historyOpen
		a.openCode.modelMenuOpen = false
		a.openCode.historyScroll = openCodeTranscriptScroll{detached: true}
		a.refresh()
		return nil
	case strings.HasPrefix(action, "opencode-panel-model-pick:"):
		return a.pickOpenCodeModel(strings.TrimPrefix(action, "opencode-panel-model-pick:"))
	}
	return nil
}

// pickOpenCodeModel validates the choice against the live catalog at selection
// time, stores it on the selected session, and remembers it as the default for
// new sessions. Request time still re-validates (resolveOpenCodeSessionModel).
func (a *App) pickOpenCodeModel(key string) error {
	sid := a.openCode.selectedSessionID
	if sid != "" && a.openCodeRunFor(sid) != nil {
		a.openCode.panelStatus = "Stop the running turn before changing the model."
		a.refresh()
		return nil
	}
	providerID, modelID, ok := a.findOpenCodeModel(key)
	if ok {
		for _, m := range a.openCode.consoleModels {
			if m.ProviderID == providerID && m.ID == modelID {
				_, err := a.openCodeBackend().Resolve(m)
				ok = err == nil
			}
		}
	}
	if !ok {
		a.openCode.panelStatus = "That model is not available in the current catalog. Pick another."
		a.refresh()
		return nil
	}
	st := openCodeSettingsValue(a.settings.OpenCode)
	st.ProviderID, st.ModelID = providerID, modelID
	a.settings.OpenCode = &st
	a.persistSettings()
	a.openCode.modelMenuOpen = false
	a.openCode.panelStatus = "Model set to " + providerID + "/" + modelID + "."
	if sid != "" {
		a.storeOpenCodeSessionModel(sid, providerID, modelID)
	}
	a.refresh()
	return nil
}

// storeOpenCodeSessionModel persists provider_id/model_id for one session. It
// is a single local SQLite row update.
func (a *App) storeOpenCodeSessionModel(sessionID, providerID, modelID string) {
	if a.ensureOpenCodeStore() != nil || a.openCode.store == nil {
		a.openCode.panelStatus = "Model chosen, but local history is unavailable so it was not saved to the session."
		return
	}
	for i, s := range a.openCode.allSessions {
		if s.ID != sessionID {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		updated, err := a.openCode.store.UpdateSessionMetadata(ctx, agentstore.Session{
			ID: s.ID, WorkspaceRoot: s.WorkspaceRoot,
			ProviderID: providerID, ModelID: modelID, Model: providerID + "/" + modelID,
		})
		if err != nil {
			a.openCode.panelStatus = "Model chosen, but it could not be saved to the session."
			return
		}
		a.openCode.allSessions[i] = updated
		return
	}
}

// --- dock geometry -----------------------------------------------------------

// agentDockWidth is the docked chat width in page pixels (0 when closed).
func (a *App) agentDockWidth() int {
	if a == nil || !a.openCode.panelOpen {
		return 0
	}
	maxW := a.agentDockMax()
	base := a.openCode.dockWidth
	if base <= 0 && a.settings.OpenCode != nil {
		base = a.settings.OpenCode.DockWidth
	}
	if base <= 0 {
		base = openCodeDockDefault
	}
	w := min(a.zx(base), maxW)
	return max(w, min(a.zx(openCodeDockMin), maxW))
}

// agentDockMax leaves the editor its minimum width beside the dock.
func (a *App) agentDockMax() int {
	pageW := defaultWidth
	if a.page != nil {
		pageW, _ = a.page.Size()
	}
	if a.details {
		pageW -= a.zx(detailsWidth)
	}
	return max(pageW-a.windowControlLeftPx()-a.zx(paneMinWidth), a.zx(openCodeDockFloor))
}

func (a *App) startOpenCodeDockDrag() {
	a.openCode.dockDrag = true
	a.openCode.dockDragStart = int(float64(a.agentDockWidth()) / a.uiZoom())
	a.openCode.dockDragDx = 0
}

// moveOpenCodeDockDrag widens the dock as the grip moves left.
func (a *App) moveOpenCodeDockDrag(dx float64) {
	a.openCode.dockDragDx += dx
	maxBase := max(openCodeDockFloor, int(float64(a.agentDockMax())/a.uiZoom()))
	w := a.openCode.dockDragStart - int(a.openCode.dockDragDx/a.uiZoom())
	a.openCode.dockWidth = max(openCodeDockMin, min(w, maxBase))
}

// --- dropdown scrolling ------------------------------------------------------

func (a *App) openCodeMenuMetrics() bool {
	boxes := a.page.Boxes()
	changed := false
	if a.openCode.modelMenuOpen {
		changed = measureOpenCodeMenu(boxes, openCodeModelMenuID, openCodeModelMenuInnerID, &a.openCode.modelScroll) || changed
	}
	if a.openCode.historyOpen {
		changed = measureOpenCodeMenu(boxes, openCodeHistoryMenuID, openCodeHistoryMenuInnerID, &a.openCode.historyScroll) || changed
	}
	return changed
}

func measureOpenCodeMenu(boxes []ownframe.Box, viewportID, innerID string, s *openCodeTranscriptScroll) bool {
	vp, ok := findBoxID(boxes, viewportID)
	if !ok {
		return false
	}
	inner, ok := findBoxID(boxes, innerID)
	if !ok {
		return false
	}
	return s.measure(int(vp.H), int(inner.H))
}

// openCodeMenuWheel scrolls an open dropdown when the wheel lands on it.
func (a *App) openCodeMenuWheel(x, y, dy float64) bool {
	type menu struct {
		open bool
		id   string
		s    *openCodeTranscriptScroll
	}
	for _, m := range []menu{
		{a.openCode.modelMenuOpen, openCodeModelMenuID, &a.openCode.modelScroll},
		{a.openCode.historyOpen, openCodeHistoryMenuID, &a.openCode.historyScroll},
	} {
		if !m.open {
			continue
		}
		vp, ok := findBoxID(a.page.Boxes(), m.id)
		if !ok || x < vp.X || x > vp.X+vp.W || y < vp.Y || y > vp.Y+vp.H {
			continue
		}
		a.openCodeMenuMetrics()
		m.s.by(int(-dy * float64(a.zx(openCodeMenuWheelStep))))
		a.refresh()
		return true
	}
	return false
}

// persistOpenCodeDockWidth stores the dragged width in the existing settings.
func (a *App) persistOpenCodeDockWidth() {
	if a.openCode.dockWidth <= 0 {
		return
	}
	st := openCodeSettingsValue(a.settings.OpenCode)
	st.DockWidth = a.openCode.dockWidth
	a.settings.OpenCode = st.sanitize()
	a.persistSettings()
}
