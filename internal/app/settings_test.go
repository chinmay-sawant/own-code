package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

func TestSettingsRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")
	want := globalSettings{
		Version:     settingsVersion,
		Theme:       "dracula",
		FontSize:    14,
		Zoom:        1.1,
		ZoomPerView: map[string]float64{"editor": 1.1},
		Keymap:      map[string]string{"quick-open": "Ctrl+O"},
		Excludes:    excludeSettings{Scan: []string{"*.log"}, Search: []string{"vendor/**"}, UseGitignore: true},
	}

	if err := saveSettings(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := loadSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got.Theme != "dracula" || got.FontSize != 14 || got.Zoom != 1.1 {
		t.Fatalf("round trip appearance = %+v", got)
	}

	if got.ZoomPerView["editor"] != 1.1 {
		t.Fatalf("round trip per-view = %+v", got.ZoomPerView)
	}

	if got.Keymap["quick-open"] != "Ctrl+O" {
		t.Fatalf("round trip keymap = %+v", got.Keymap)
	}

	if len(got.Excludes.Scan) != 1 || got.Excludes.Scan[0] != "*.log" {
		t.Fatalf("round trip excludes = %+v", got.Excludes)
	}

	if _, err := loadSettings(""); err == nil {
		t.Fatal("load accepted an empty path")
	}

	if err := saveSettings("", want); err == nil {
		t.Fatal("save accepted an empty path")
	}
}

func TestSettingsMissingIsZero(t *testing.T) {
	t.Parallel()

	got, err := loadSettings(filepath.Join(t.TempDir(), "missing", "settings.json"))
	if err != nil {
		t.Fatalf("missing file returned an error: %v", err)
	}

	if got.Theme != "midnight" || got.FontSize != defaultFontSize || got.Zoom != 1 {
		t.Fatalf("missing file settings = %+v, want defaults", got)
	}
}

func TestSettingsCorruptIsZeroWithError(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{"broken": "{not json", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}

			got, err := loadSettings(path)
			if err == nil {
				t.Fatal("corrupt file loaded without an error")
			}

			if !strings.Contains(err.Error(), "settings") || !strings.Contains(err.Error(), "decode") {
				t.Fatalf("error is not descriptive: %v", err)
			}

			if got.Theme != "midnight" || got.Version != settingsVersion {
				t.Fatalf("corrupt file settings = %+v, want zero", got)
			}
		})
	}
}

func TestSettingsUnknownFieldsIgnored(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")
	body := `{"version":99,"theme":"nord","fontSize":13,"future":{"x":1},"unknownList":[1,2]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := loadSettings(path)
	if err != nil {
		t.Fatalf("forward-compat load: %v", err)
	}

	if got.Theme != "nord" || got.FontSize != 13 {
		t.Fatalf("known fields = %+v", got)
	}
}

func TestSettingsSanitizeClamps(t *testing.T) {
	t.Parallel()

	s := globalSettings{
		Theme:    "bogus",
		FontSize: 99,
		Zoom:     9,
		ZoomPerView: map[string]float64{
			"editor":   0.1,
			"terminal": 2,
			"explorer": 2,
		},
		Keymap: map[string]string{
			"quick-open": "Ctrl+O",
			"bogus":      "Ctrl+Q",
			"zoom-in":    "Ctrl+=",
			"broken":     "Ctrl++",
		},
		Excludes: excludeSettings{
			Scan:   []string{"", "  ", "!negated", "[[bad", "*.log", strings.Repeat("x", 300)},
			Search: []string{"vendor/**"},
		},
	}.sanitize()

	if s.Theme != "midnight" {
		t.Fatalf("theme = %q", s.Theme)
	}

	if s.FontSize != fontMaxSize {
		t.Fatalf("font = %v", s.FontSize)
	}

	if s.Zoom != zoomMax {
		t.Fatalf("zoom = %v", s.Zoom)
	}

	if s.ZoomPerView["editor"] != zoomMin {
		t.Fatalf("editor zoom = %v", s.ZoomPerView["editor"])
	}

	if _, ok := s.ZoomPerView["explorer"]; ok {
		t.Fatal("explorer per-view survived v1 sanitize")
	}

	if s.Keymap["quick-open"] != "Ctrl+O" {
		t.Fatalf("keymap = %+v", s.Keymap)
	}

	if _, ok := s.Keymap["bogus"]; ok {
		t.Fatal("unknown action survived sanitize")
	}

	if _, ok := s.Keymap["zoom-in"]; ok {
		t.Fatal("default-equal diff survived sanitize")
	}

	if len(s.Excludes.Scan) != 1 || s.Excludes.Scan[0] != "*.log" {
		t.Fatalf("scan globs = %+v", s.Excludes.Scan)
	}
}

func TestSettingsDefaultPathBesideSessions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	settingsPath, err := defaultSettingsPath()
	if err != nil {
		t.Fatal(err)
	}

	sessionPath, err := defaultSessionPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if filepath.Dir(settingsPath) != filepath.Dir(sessionPath) {
		t.Fatalf("settings dir %q is not beside sessions %q", filepath.Dir(settingsPath), filepath.Dir(sessionPath))
	}

	if filepath.Base(settingsPath) != "settings.json" {
		t.Fatalf("settings name = %q", filepath.Base(settingsPath))
	}
}

func TestFontSizeClampAndChip(t *testing.T) {
	t.Parallel()

	if clampFontSize(4) != fontMinSize || clampFontSize(99) != fontMaxSize {
		t.Fatal("font clamps wrong")
	}

	a := newTestApp(t)
	if got := a.fontLabel(); got != "12px" {
		t.Fatalf("font label = %q", got)
	}

	if got := a.statusZoomLabel(); got != "100% · 12px" {
		t.Fatalf("status chip = %q", got)
	}

	if err := a.setFontSize(context.Background(), 99); err != nil {
		t.Fatal(err)
	}

	if a.baseFontSize() != fontMaxSize {
		t.Fatalf("font = %v", a.baseFontSize())
	}

	// zoomLabel stays percent-only for its callers; the chip carries both.
	if got := a.zoomLabel(); got != "100%" {
		t.Fatalf("zoom label = %q", got)
	}

	if got := a.statusZoomLabel(); !strings.Contains(got, "24px") {
		t.Fatalf("status chip = %q", got)
	}
}

func TestZoomPerViewEffectiveAndCSS(t *testing.T) {
	t.Parallel()

	if got := zoomOverrideCSSWithFont(1, 12, nil); got != "" {
		t.Fatalf("override(1,12) = %q, want empty", got)
	}

	a := newTestApp(t)
	if got := a.effectiveZoom("editor"); got != 1 {
		t.Fatalf("effective editor zoom = %v", got)
	}

	a.zoomPerView = map[string]float64{"editor": 1.1}
	if got := a.effectiveZoom("editor"); got != 1.1 {
		t.Fatalf("effective editor zoom = %v", got)
	}

	if got := a.effectiveZoom("explorer"); got != 1 {
		t.Fatalf("effective explorer zoom = %v", got)
	}

	css := zoomOverrideCSSWithFont(1, 12, map[string]float64{"editor": 1.1})
	if !strings.Contains(css, ".cline") || strings.Contains(css, "--row-h") {
		t.Fatalf("per-view css should scope fonts without layout vars: %q", css[:min(200, len(css))])
	}

	// zx/zf stay global for scroll math and hit testing.
	if a.zx(100) != 100 || a.zf(7.5) != 7.5 {
		t.Fatal("global geometry moved under a per-view override")
	}
}

func TestThemeFontZoomSessionPrecedence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")

	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	a.EnableSession()
	a.theme = "dracula"
	a.fontSize = 14
	a.zoom = 1.1
	a.sessionReady = true
	a.persistSession()
	a.persistSettings()

	b, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	b.EnableSession()
	if b.theme != "dracula" || b.baseFontSize() != 14 || b.uiZoom() != 1.1 {
		t.Fatalf("restored = %q %.0f %.2f", b.theme, b.baseFontSize(), b.uiZoom())
	}

	// Roaming default: a fresh workspace inherits the global settings.
	other := t.TempDir()
	writeFile(t, other, "main.go", "package main\n")

	c, err := New(other)
	if err != nil {
		t.Fatal(err)
	}

	c.EnableSession()
	if c.theme != "dracula" || c.baseFontSize() != 14 {
		t.Fatalf("roamed = %q %.0f", c.theme, c.baseFontSize())
	}
}

func TestSettingsPanelTabsRender(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	ctx := context.Background()

	if err := a.onClick(ctx, ownframe.Box{Action: "settings-open"}); err != nil {
		t.Fatal(err)
	}

	for _, tab := range []string{"keyboard", "files", "appearance"} {
		if err := a.onClick(ctx, ownframe.Box{Action: "settings-tab:" + tab}); err != nil {
			t.Fatal(err)
		}

		if err := a.page.Redraw(ctx); err != nil {
			t.Fatalf("tab %s: %v", tab, err)
		}
	}
}

func TestSettingsExcludesOverbroadConfirm(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	ctx := context.Background()

	if err := a.onClick(ctx, ownframe.Box{Action: "settings-open"}); err != nil {
		t.Fatal(err)
	}

	// A blanket pattern would empty the tree: park it behind a confirm
	// instead of applying.
	a.scanDraft = "**"
	a.scanTouched = true

	if err := a.applyExcludesRescan(ctx); err != nil {
		t.Fatal(err)
	}

	if !a.confirmOpen || a.confirmKind != "excludes-overbroad" {
		t.Fatalf("confirm = open %v kind %q", a.confirmOpen, a.confirmKind)
	}

	if len(a.settings.Excludes.Scan) != 0 {
		t.Fatal("overbroad patterns applied without confirm")
	}

	if err := a.confirmDiscard(ctx); err != nil {
		t.Fatal(err)
	}

	if a.confirmOpen {
		t.Fatal("confirm did not close")
	}

	if len(a.settings.Excludes.Scan) != 1 || a.settings.Excludes.Scan[0] != "**" {
		t.Fatalf("applied scan = %+v", a.settings.Excludes.Scan)
	}
}
