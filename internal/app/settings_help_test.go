package app

import (
	"context"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

// TestSettingsHelpAndAboutTabs checks the split Help and About sections:
// Help renders a read-only table over the full keymap catalog with the
// live bindings, About renders the app name/version/scope line.
func TestSettingsHelpAndAboutTabs(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	ctx := context.Background()

	if err := a.onClick(ctx, ownframe.Box{Action: "settings-open"}); err != nil {
		t.Fatal(err)
	}

	// Help tab: full catalog with live bindings.
	if err := a.onClick(ctx, ownframe.Box{Action: "settings-tab:help"}); err != nil {
		t.Fatal(err)
	}

	if got := a.settingsTabName(); got != "help" {
		t.Fatalf("settings tab = %q, want help", got)
	}

	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	v := a.View()
	if v.SettingsTab != "help" {
		t.Fatalf("view tab = %q, want help", v.SettingsTab)
	}

	if len(v.HelpRows) != len(keymapCatalog()) {
		t.Fatalf("help rows = %d, want full catalog %d", len(v.HelpRows), len(keymapCatalog()))
	}

	seen := map[string]bool{}
	for _, r := range v.HelpRows {
		if r.Action == "" || r.Label == "" || r.Chord == "" {
			t.Fatalf("help row = %+v, want action/description/binding", r)
		}
		if seen[r.Action] {
			t.Fatalf("duplicate help action %q", r.Action)
		}
		seen[r.Action] = true
	}

	for _, action := range []string{"quick-open", "save-file", "history-back", "devtools"} {
		if !seen[action] {
			t.Fatalf("help misses %q", action)
		}
	}

	for _, id := range []string{"settings-tab-help", "settings-help-list", "help-quick-open", "help-save-file"} {
		if _, ok := boxByID(a.page.Boxes(), id); !ok {
			t.Fatalf("box %q is missing on the help tab", id)
		}
	}

	// About tab: app name/version/scope only.
	if err := a.onClick(ctx, ownframe.Box{Action: "settings-tab:about"}); err != nil {
		t.Fatal(err)
	}

	if got := a.settingsTabName(); got != "about" {
		t.Fatalf("settings tab = %q, want about", got)
	}

	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	v = a.View()
	if v.SettingsTab != "about" {
		t.Fatalf("view tab = %q, want about", v.SettingsTab)
	}

	if v.AppName == "" || v.AppVersion == "" || v.AppScope == "" {
		t.Fatalf("about = %q %q %q, want name/version/scope", v.AppName, v.AppVersion, v.AppScope)
	}

	for _, id := range []string{"settings-tab-about", "settings-about"} {
		if _, ok := boxByID(a.page.Boxes(), id); !ok {
			t.Fatalf("box %q is missing on the about tab", id)
		}
	}
}

// TestSettingsHelpReflectsRebinds checks the Help table shows the effective
// binding after a Keyboard rebind and falls back to the default on reset.
func TestSettingsHelpReflectsRebinds(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	ctx := context.Background()

	a.applyKeymapChord("quick-open", "Ctrl+O")

	if err := a.onClick(ctx, ownframe.Box{Action: "settings-open"}); err != nil {
		t.Fatal(err)
	}

	if err := a.onClick(ctx, ownframe.Box{Action: "settings-tab:help"}); err != nil {
		t.Fatal(err)
	}

	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	find := func(rows []keymapRow, action string) keymapRow {
		for _, r := range rows {
			if r.Action == action {
				return r
			}
		}
		return keymapRow{}
	}

	row := find(a.View().HelpRows, "quick-open")
	if row.Chord != "Ctrl+O" {
		t.Fatalf("help binding = %q, want effective Ctrl+O", row.Chord)
	}

	if !row.Custom || row.Default != "Ctrl+P" {
		t.Fatalf("help row = %+v, want custom with default Ctrl+P", row)
	}

	row = find(a.View().KeymapRows, "quick-open")
	if row.Chord != "Ctrl+O" {
		t.Fatalf("keyboard binding = %q, want effective Ctrl+O", row.Chord)
	}

	a.resetKeymapAction("quick-open")
	a.refresh()

	row = find(a.View().HelpRows, "quick-open")
	if row.Chord != "Ctrl+P" || row.Custom {
		t.Fatalf("help row after reset = %+v, want default Ctrl+P", row)
	}
}
