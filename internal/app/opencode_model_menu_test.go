package app

import (
	"context"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/agent"
	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

func menuModel(provider, providerName, id string, eligible bool) opencode.ConsoleModel {
	m := opencode.ConsoleModel{ID: id, Name: id + "-name", ProviderID: provider, ProviderName: providerName, Type: "chat",
		ToolsKnown: true, ToolsSupported: eligible,
		APIs: map[string]opencode.ConsoleModelAPI{"chat-completions": {BaseURL: "https://x.example/v1"}}}
	return m
}

func TestModelMenuRegistryAndGrouping(t *testing.T) {
	r := agent.NewProviderRegistry()
	if d, ok := r.Default(); !ok || d.DisplayName == "" || d.NewRequestProvider == nil || d.LoadCatalog == nil {
		t.Fatalf("default descriptor = %+v", d)
	}
	if err := r.Register(agent.OpenCodeConsoleDescriptor()); err == nil {
		t.Fatal("duplicate provider accepted")
	}
	if err := r.Register(agent.ProviderDescriptor{ID: "x"}); err == nil {
		t.Fatal("incomplete descriptor accepted")
	}
	groups := agent.GroupEligible("opencode", []opencode.ConsoleModel{
		menuModel("a", "Alpha", "m1", true), menuModel("b", "Beta", "m2", true),
		menuModel("a", "Alpha", "m3", true), menuModel("a", "Alpha", "bad", false),
	})
	if len(groups) != 2 || groups[0].ProviderName != "Alpha" || len(groups[0].Models) != 2 || groups[1].Models[0].Key() != "b/m2" {
		t.Fatalf("groups = %+v", groups)
	}
	if groups[0].Models[0].BackendID != "opencode" {
		t.Fatalf("backend id = %q", groups[0].Models[0].BackendID)
	}
}

func TestModelMenuStatesPersistAndValidate(t *testing.T) {
	a := newRunTestApp(t)
	ctx := context.Background()
	a.openCode.panelOpen = true

	if v := a.openCodePanelView(); v.ModelState != openCodeModelMissingKey || v.ModelLabel == "" {
		t.Fatalf("disconnected view = %+v", v)
	}
	a.openCode.connected = true
	if v := a.openCodePanelView(); v.ModelState != openCodeModelNoModels || v.ModelLabel != "No available model" {
		t.Fatalf("empty catalog view = %+v", v)
	}

	a.openCode.consoleModels = []opencode.ConsoleModel{
		menuModel("a", "Alpha", "m1", true), menuModel("b", "Beta", "m2", true), menuModel("a", "Alpha", "bad", false),
	}
	if v := a.openCodePanelView(); v.ModelState != openCodeModelNone || len(v.ModelGroups) != 2 {
		t.Fatalf("no selection view = %+v", v)
	}

	// An ineligible or unknown key is rejected at selection time.
	for _, key := range []string{"a/bad", "zz/nope", "malformed"} {
		if err := a.pickOpenCodeModel(key); err != nil {
			t.Fatal(err)
		}
		if st := openCodeSettingsValue(a.settings.OpenCode); st.ModelID != "" {
			t.Fatalf("rejected key %q was saved: %+v", key, st)
		}
	}

	// With a session selected the choice is stored on the session and remembered.
	if err := a.ensureOpenCodeStore(); err != nil {
		t.Fatal(err)
	}
	root, _ := canonicalWorkspaceRoot(a.ws.Root)
	s, err := a.openCode.store.CreateSession(ctx, agentstore.Session{ID: "s-model", WorkspaceRoot: root, Title: "t", Agent: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	a.openCode.allSessions = []agentstore.Session{s}
	a.openCode.selectedSessionID = "s-model"
	a.openCode.modelMenuOpen = true
	if err := a.pickOpenCodeModel("b/m2"); err != nil {
		t.Fatal(err)
	}
	if a.openCode.modelMenuOpen {
		t.Fatal("menu stayed open after a valid pick")
	}
	stored, err := findOpenCodeLocalSession(ctx, a.openCode.store, root, "s-model")
	if err != nil || stored.ProviderID != "b" || stored.ModelID != "m2" {
		t.Fatalf("stored = %+v err=%v", stored, err)
	}
	if st := openCodeSettingsValue(a.settings.OpenCode); st.ProviderID != "b" || st.ModelID != "m2" {
		t.Fatalf("default not remembered: %+v", st)
	}
	if v := a.openCodePanelView(); v.ModelLabel != "m2-name" || v.ModelState != "" {
		t.Fatalf("view after pick = %+v", v)
	}

	// Another session keeps its own model while the default stays remembered.
	a.openCode.allSessions = append(a.openCode.allSessions, agentstore.Session{ID: "s2", ProviderID: "a", ModelID: "m1"})
	a.openCode.selectedSessionID = "s2"
	if v := a.openCodePanelView(); v.ModelLabel != "m1-name" {
		t.Fatalf("per-session label = %q", v.ModelLabel)
	}
	// A model that left the catalog shows as unavailable.
	a.openCode.allSessions[1].ModelID = "gone"
	if v := a.openCodePanelView(); v.ModelState != openCodeModelUnavailable {
		t.Fatalf("stale model view = %+v", v)
	}

	// A running turn blocks switching.
	addRun(a, "s2", "t1", "hi")
	if err := a.pickOpenCodeModel("a/m1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.openCode.panelStatus, "Stop the running turn") {
		t.Fatalf("status = %q", a.openCode.panelStatus)
	}
}

func TestDockGeometryAndRender(t *testing.T) {
	a := newRunTestApp(t)
	ctx := context.Background()
	closedW := a.editorWidth()
	a.openCode.panelOpen = true
	w := a.agentDockWidth()
	if w <= 0 || a.editorWidth() != closedW-w {
		t.Fatalf("dock %d does not carve the editor: %d vs %d", w, a.editorWidth(), closedW)
	}
	if w > a.agentDockMax() {
		t.Fatalf("dock %d exceeds max %d", w, a.agentDockMax())
	}
	// Dragging the grip left widens it, bounded by the editor minimum.
	a.startOpenCodeDockDrag()
	a.moveOpenCodeDockDrag(-60)
	if a.agentDockWidth() <= w {
		t.Fatal("drag left did not widen the dock")
	}
	a.moveOpenCodeDockDrag(-100000)
	if a.agentDockWidth() > a.agentDockMax() {
		t.Fatal("dock exceeded max")
	}
	a.moveOpenCodeDockDrag(100000)
	if a.agentDockWidth() < min(a.zx(openCodeDockMin), a.agentDockMax()) {
		t.Fatal("dock below min")
	}
	// The templates render with both dropdowns open.
	a.openCode.connected = true
	a.openCode.consoleModels = []opencode.ConsoleModel{menuModel("a", "Alpha", "m1", true)}
	a.openCode.modelMenuOpen, a.openCode.historyOpen = true, true
	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	a.refresh()
	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"opencode-panel", "opencode-dock-resize", "opencode-model-btn", "opencode-model-menu", "opencode-history-menu", "opencode-transcript"} {
		if _, ok := findBoxID(a.page.Boxes(), id); !ok {
			t.Errorf("rendered page has no %q", id)
		}
	}
	if box, ok := findBoxID(a.page.Boxes(), "opencode-panel"); ok {
		if int(box.X+box.W) < int(float64(a.agentDockWidth())) || box.X <= float64(a.windowControlLeftPx()) {
			t.Errorf("panel box %+v is not right-docked (dock %d)", box, a.agentDockWidth())
		}
	}
	a.openCode.panelOpen = false
	if a.agentDockWidth() != 0 {
		t.Fatal("closed panel keeps a dock")
	}
}
