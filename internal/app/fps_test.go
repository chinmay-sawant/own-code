package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

func fpsTempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "settings.json")
}

func TestNormalizeFPS(t *testing.T) {
	if NormalizeFPS(0) != 0 {
		t.Fatal("missing must be uncapped")
	}
	if NormalizeFPS(-1) != FPSUncapped {
		t.Fatal("uncapped must stay uncapped")
	}
	for _, v := range []int{60, 120, 144, 240} {
		if NormalizeFPS(v) != v {
			t.Fatalf("valid %d must pass", v)
		}
	}
	if NormalizeFPS(90) != 0 || NormalizeFPS(1000) != 0 {
		t.Fatal("invalid must fall back to uncapped")
	}
}

func TestUncappedDefaultActiveRow(t *testing.T) {
	rows := fpsRows(0)
	active := 0
	for _, r := range rows {
		if r.Active {
			active++
			if r.Value != FPSUncapped {
				t.Fatalf("default active = %d, want uncapped", r.Value)
			}
		}
	}
	if active != 1 {
		t.Fatalf("want exactly one active FPS row, got %d", len(rows))
	}
	if FPSLabel(0) != "Uncapped (default)" {
		t.Fatalf("default label = %q", FPSLabel(0))
	}
}

func TestEffectiveFPSPrecedence(t *testing.T) {
	if EffectiveFPS(false, 0, 0) != 0 {
		t.Fatal("nothing set must be default")
	}
	if EffectiveFPS(false, 0, 144) != 144 {
		t.Fatal("settings must win when no CLI")
	}
	if EffectiveFPS(true, 120, 144) != 120 {
		t.Fatal("CLI must win over settings")
	}
	if EffectiveFPS(true, -1, 144) != FPSUncapped {
		t.Fatal("CLI uncapped must win")
	}
}

func TestWindowMaxFPSMapping(t *testing.T) {
	if WindowMaxFPS(0) != 0 || WindowMaxFPS(-1) != -1 || WindowMaxFPS(144) != 144 {
		t.Fatal("window mapping must preserve default/uncapped/cap")
	}
}

func TestFPSActionPersistsAndNotesRestart(t *testing.T) {
	a, err := NewWithLimit("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.sessionReady = false // keep persistSettings hermetic (no config write)
	if err := a.onClick(context.Background(), ownframe.Box{Action: "fps:144"}); err != nil {
		t.Fatal(err)
	}
	if a.settings.MaxFPS != 144 {
		t.Fatalf("settings MaxFPS = %d, want 144", a.settings.MaxFPS)
	}
	if a.settingsNote == "" {
		t.Fatal("FPS change must note restart")
	}
	if got := a.EffectiveMaxFPS(); got != 144 {
		t.Fatalf("effective = %d, want 144", got)
	}
	rows := fpsRows(a.EffectiveMaxFPS())
	active := 0
	for _, r := range rows {
		if r.Active {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("want exactly one active FPS row, got %d", len(rows))
	}
}

func TestFPSUncappedAction(t *testing.T) {
	a, err := NewWithLimit("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.sessionReady = false
	if err := a.onClick(context.Background(), ownframe.Box{Action: "fps:-1"}); err != nil {
		t.Fatal(err)
	}
	if a.settings.MaxFPS != FPSUncapped {
		t.Fatalf("uncapped = %d", a.settings.MaxFPS)
	}
	if got := a.WindowMaxFPSValue(); got != -1 {
		t.Fatalf("window uncapped = %d", got)
	}
}

func TestSettingsMaxFPSRoundTrip(t *testing.T) {
	t.Parallel()
	path := fpsTempPath(t)
	want := zeroSettings()
	want.MaxFPS = 144
	if err := saveSettings(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.MaxFPS != 144 {
		t.Fatalf("round trip MaxFPS = %d", got.MaxFPS)
	}
	// Invalid hand-edits fall back to default.
	want.MaxFPS = 90
	if err := saveSettings(path, want); err != nil {
		t.Fatalf("save invalid: %v", err)
	}
	// saveSettings sanitizes, so the file already holds the default.
	got, err = loadSettings(path)
	if err != nil {
		t.Fatalf("load invalid: %v", err)
	}
	if got.MaxFPS != 0 {
		t.Fatalf("invalid MaxFPS = %d, want default 0", got.MaxFPS)
	}
}