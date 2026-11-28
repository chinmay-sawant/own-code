package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/agent"
	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/opencode"
	"github.com/chinmay-sawant/ownframe"
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

func TestModelMenuSeparatesDefaultAndFreeGroups(t *testing.T) {
	freeByCost := menuModel("opencode", "Default / OpenCode", "big-pickle", true)
	freeByCost.Name = "Big Pickle"
	freeByCost.FreeKnown, freeByCost.Free = true, true

	freeByName := menuModel("opencode", "Default / OpenCode", "glm-4.7-free", true)
	freeByName.Name = "GLM 4.7 Free"

	paid := menuModel("opencode", "Default / OpenCode", "claude-sonnet-4", true)
	paid.Name = "Claude Sonnet 4"
	paid.FreeKnown, paid.Free = true, false

	groups := agent.GroupEligible("opencode", []opencode.ConsoleModel{paid, freeByCost, freeByName})
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups (Default and Free), got %d: %+v", len(groups), groups)
	}
	if groups[0].ProviderName != "Default / OpenCode" || len(groups[0].Models) != 1 || groups[0].Models[0].ModelID != "claude-sonnet-4" {
		t.Fatalf("default group = %+v", groups[0])
	}
	if groups[1].ProviderName != "Free" || len(groups[1].Models) != 2 {
		t.Fatalf("free group = %+v", groups[1])
	}
	if groups[1].Models[0].Key() != "opencode/big-pickle" || groups[1].Models[1].Key() != "opencode/glm-4.7-free" {
		t.Fatalf("free model keys = %+v", groups[1].Models)
	}
}

func TestModelMenuScrollbarAndNewChatPromptClickable(t *testing.T) {
	a := newRunTestApp(t)
	ctx := context.Background()
	a.openCode.panelOpen = true
	a.openCode.connected = true

	// Build enough default + free models to overflow the 240px dropdown.
	var models []opencode.ConsoleModel
	for i := 0; i < 8; i++ {
		m := menuModel("opencode", "Default / OpenCode", "paid-"+string(rune('a'+i)), true)
		m.Name = "Paid Model " + string(rune('A'+i))
		m.FreeKnown, m.Free = true, false
		models = append(models, m)
	}
	for i := 0; i < 6; i++ {
		m := menuModel("opencode", "Default / OpenCode", "model-"+string(rune('a'+i))+"-free", true)
		m.Name = "Free Model " + string(rune('A'+i)) + " Free"
		models = append(models, m)
	}
	a.openCode.consoleModels = models

	// In New Chat state (SelectedID == ""), the Ask the Agent box must be enabled.
	v := a.openCodePanelView()
	if !v.CanPrompt {
		t.Fatal("expected CanPrompt=true in New Chat state when connected")
	}

	// Clicking Review in New Chat state gives a clear status message.
	if err := a.onClick(ctx, ownframe.Box{Action: "opencode-session-review"}); err != nil {
		t.Fatal(err)
	}
	if a.openCode.panelStatus == "" {
		t.Fatal("expected Review click in New Chat to report status")
	}

	// Workspace switch cancel must route through onClick to opencodePanelAction.
	a.openCode.pendingSwitch = &openCodePendingSwitch{SessionID: "other", Title: "Other", Root: t.TempDir()}
	if err := a.onClick(ctx, ownframe.Box{Action: "opencode-switch-cancel"}); err != nil {
		t.Fatal(err)
	}
	if a.openCode.pendingSwitch != nil {
		t.Fatal("opencode-switch-cancel was not handled by onClick")
	}

	// Open the model picker and verify the visible vertical scrollbar renders and settles.
	if err := a.opencodePanelAction(ctx, "opencode-panel-model-toggle"); err != nil {
		t.Fatal(err)
	}
	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	if !a.openCodeMenuMetrics() {
		t.Fatal("expected initial model menu measurement to report changed")
	}
	a.refresh()
	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	// Second measurement must settle (return false) to avoid per-frame refresh loops.
	if a.openCodeMenuMetrics() {
		t.Fatal("model menu metrics did not settle after layout")
	}
	if !a.view.OpenCodePanel.ModelScroll {
		t.Fatalf("expected ModelScroll=true for overflowing model list, view=%+v", a.view.OpenCodePanel)
	}
	for _, id := range []string{openCodeModelRailID, openCodeModelThumbID} {
		if _, ok := findBoxID(a.page.Boxes(), id); !ok {
			t.Errorf("missing scrollbar element %q", id)
		}
	}

	// Wheel scrolling moves both the list and the thumb.
	beforeThumb := a.view.OpenCodePanel.ModelThumbTop
	vp, ok := findBoxID(a.page.Boxes(), openCodeModelMenuID)
	if !ok {
		t.Fatal("missing model menu box")
	}
	if !a.openCodeMenuWheel(vp.X+10, vp.Y+10, -3) {
		t.Fatal("expected wheel event on model menu to be handled")
	}
	if a.view.OpenCodePanel.ModelThumbTop <= beforeThumb {
		t.Fatalf("thumb did not move after scrolling: before=%d after=%d", beforeThumb, a.view.OpenCodePanel.ModelThumbTop)
	}

	// Picking a model sets a concise status using the friendly model name.
	if err := a.pickOpenCodeModel("opencode/paid-a"); err != nil {
		t.Fatal(err)
	}
	if a.openCode.panelStatus != "Model: Paid Model A" {
		t.Fatalf("panelStatus = %q, want %q", a.openCode.panelStatus, "Model: Paid Model A")
	}
}

func TestOpenCodePromptTypingDoesNotTriggerExplorerFilter(t *testing.T) {
	a := newRunTestApp(t)
	ctx := context.Background()
	a.openCode.panelOpen = true
	a.openCode.connected = true
	a.refresh()
	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.page.Form().Focus(ctx, "opencode-prompt"); err != nil {
		t.Fatal(err)
	}
	// Typing bare shortcut letters like f (focus-filter), w (close-tab), 2 (changes)
	// must stay inside the Ask the agent box and never steal focus to the Explorer filter.
	for _, key := range []string{"f", "i", "x", "w", "2", "r", "z"} {
		if err := a.onKeyDown(ctx, key); err != nil {
			t.Fatal(err)
		}
		if got := a.page.Form().FocusID(); got != "opencode-prompt" {
			t.Fatalf("key %q stole focus from opencode-prompt to %q", key, got)
		}
	}
	if a.mode != "explorer" {
		t.Fatalf("typing '2' in opencode-prompt switched mode to %q", a.mode)
	}
}

func TestOpenCodeEnvFileKeyPersistence(t *testing.T) {
	dir := t.TempDir()
	envPath := dir + "/.env"
	if err := os.WriteFile(envPath, []byte("# existing comment\nOTHER_VAR=keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const sampleKey = "oc_sk_test_1234567890abcdef"
	if err := saveOpenCodeKeyToEnvFile(envPath, sampleKey); err != nil {
		t.Fatal(err)
	}
	got, ok := loadOpenCodeKeyFromEnvFile(envPath)
	if !ok || got != sampleKey {
		t.Fatalf("loadOpenCodeKeyFromEnvFile() = (%q, %t), want (%q, true)", got, ok, sampleKey)
	}
	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "OTHER_VAR=keep") {
		t.Fatalf("saving key clobbered unrelated env vars: %s", string(raw))
	}
	if err := deleteOpenCodeKeyFromEnvFile(envPath); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadOpenCodeKeyFromEnvFile(envPath); ok {
		t.Fatal("expected key to be cleared after deleteOpenCodeKeyFromEnvFile")
	}
}

func TestScreenshotOpenCodePanelUI(t *testing.T) {
	if os.Getenv("OWNFRAME_CODE_VIEW_SCREENSHOT") != "1" {
		t.Skip("set OWNFRAME_CODE_VIEW_SCREENSHOT=1 to capture screenshots")
	}
	a := newRunTestApp(t)
	ctx := context.Background()
	a.Page().SetSize(1440, 900)
	a.openCode.panelOpen = true
	a.openCode.connected = true
	a.settings.OpenCode = &opencodeSettings{ProviderID: "opencode", ModelID: "claude-sonnet-4"}

	var models []opencode.ConsoleModel
	for _, pair := range [][2]string{
		{"claude-sonnet-4", "Claude Sonnet 4"},
		{"claude-opus-4", "Claude Opus 4"},
		{"gpt-5", "GPT-5"},
		{"gpt-5-mini", "GPT-5 Mini"},
		{"gemini-2.5-pro", "Gemini 2.5 Pro"},
		{"deepseek-v4", "DeepSeek V4"},
	} {
		m := menuModel("opencode", "Default / OpenCode", pair[0], true)
		m.Name = pair[1]
		m.FreeKnown, m.Free = true, false
		models = append(models, m)
	}
	for _, pair := range [][2]string{
		{"big-pickle", "Big Pickle"},
		{"glm-4.7-free", "GLM 4.7 Free"},
		{"minimax-m2.5-free", "MiniMax M2.5 Free"},
		{"kimi-k2.5-free", "Kimi K2.5 Free"},
		{"trinity-large-free", "Trinity Large Free"},
	} {
		m := menuModel("opencode", "Default / OpenCode", pair[0], true)
		m.Name = pair[1]
		m.FreeKnown, m.Free = true, true
		models = append(models, m)
	}
	a.openCode.consoleModels = models
	a.refresh()
	if err := a.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	writeShot(t, a, "opencode-new-chat")

	// Open the model picker and measure for scrollbar.
	if err := a.opencodePanelAction(ctx, "opencode-panel-model-toggle"); err != nil {
		t.Fatal(err)
	}
	if err := a.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	a.openCodeMenuMetrics()
	a.refresh()
	if err := a.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	writeShot(t, a, "opencode-model-picker-top")

	// Scroll down into the Free group.
	if vp, ok := findBoxID(a.Page().Boxes(), openCodeModelMenuID); ok {
		a.openCodeMenuWheel(vp.X+20, vp.Y+20, -4)
		if err := a.Page().Redraw(ctx); err != nil {
			t.Fatal(err)
		}
	}
	writeShot(t, a, "opencode-model-picker-scrolled")
}
