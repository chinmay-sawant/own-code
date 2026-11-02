package app

import (
	"bytes"
	"context"
	"image/png"
	"strings"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

func TestSettingsThemesAndSelection(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	a.page.SetSize(minWidth, minHeight)
	clickID(t, a, "act-settings")
	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	panel, ok := boxByID(a.page.Boxes(), "settings-panel")
	if !ok || panel.X < 0 || panel.Y < 0 || panel.X+panel.W > minWidth || panel.Y+panel.H > minHeight {
		t.Fatalf("settings does not fit: %+v", panel)
	}
	for _, theme := range themeCatalog() {
		clickID(t, a, "theme-"+theme.ID)
		if err := a.page.Redraw(ctx); err != nil {
			t.Fatal(err)
		}
		currentPanel, ok := boxByID(a.page.Boxes(), "settings-panel")
		if !ok || currentPanel.X != panel.X || currentPanel.Y != panel.Y ||
			currentPanel.W != panel.W || currentPanel.H != panel.H {
			t.Fatalf("theme %s changed settings geometry from %+v to %+v", theme.ID, panel, currentPanel)
		}
		if a.theme != theme.ID || !a.View().SettingsOpen {
			t.Fatalf("theme %s did not apply", theme.ID)
		}
		if _, err := png.Decode(bytes.NewReader(a.page.Probe().PNG())); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.onKeyDown(ctx, "escape"); err != nil {
		t.Fatal(err)
	}
	if a.settingsOpen {
		t.Fatal("escape did not close settings")
	}
}

func TestThemeSessionRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.EnableSession()
	if err := a.onClick(context.Background(), ownframe.Box{Action: "theme:dracula"}); err != nil {
		t.Fatal(err)
	}
	b, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	b.EnableSession()
	if b.theme != "dracula" {
		t.Fatalf("restored theme = %q", b.theme)
	}
	if validTheme("unknown") != "midnight" {
		t.Fatal("unknown theme did not fall back")
	}
}

func TestThemeSelectionPaint(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	brightness := func(id string) uint32 {
		t.Helper()
		if err := a.onClick(ctx, ownframe.Box{Action: "theme:" + id}); err != nil {
			t.Fatal(err)
		}
		if err := a.page.Redraw(ctx); err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(themePNG(a)))
		if err != nil {
			t.Fatal(err)
		}
		r, g, b, _ := img.At(800, 500).RGBA()
		return r + g + b
	}
	dark, light := brightness("dracula"), brightness("light")
	if light <= dark {
		t.Fatalf("editor background did not change from dark to light: %d, %d", dark, light)
	}
	a.toggleEdit()
	if err := a.onSelectAll(ctx); err != nil {
		t.Fatal(err)
	}
	if a.edit == nil || a.editAnchor != 0 || a.editCaret == 0 {
		t.Fatal("select all did not select the editor text")
	}
}

func themePNG(a *App) []byte { return a.page.Probe().PNG() }

func TestTermPaletteCoversCatalog(t *testing.T) {
	for _, theme := range themeCatalog() {
		css := themeCSS(theme.ID)
		for _, want := range []string{"--term-bg", "--term0", "--term7", "--term8", "--term15"} {
			if !strings.Contains(css, want) {
				t.Fatalf("theme %s css misses %s", theme.ID, want)
			}
		}
	}
}

func TestGlassThemeUsesTranslucentBlurredSurfaces(t *testing.T) {
	for _, id := range []string{"glass", "glass-light"} {
		css := glassCSS(id)
		for selector, filter := range map[string]string{
			".sidebar, .tabstrip {":   "backdrop-filter:blur(16px)",
			".titlebar, .statusbar {": "backdrop-filter:blur(12px)",
		} {
			start := strings.Index(css, selector)
			if start < 0 {
				t.Fatalf("theme %s is missing structural surface %s", id, selector)
			}
			end := strings.Index(css[start:], "}")
			rule := css[start:]
			if end >= 0 {
				rule = rule[:end]
			}
			if !strings.Contains(rule, filter) || !strings.Contains(rule, "refract(") {
				t.Fatalf("theme %s must blur structural surface %s", id, selector)
			}
		}
		for _, selector := range []string{
			".settings-header {",
			".settings-nav {",
			".settings-content {",
			".settings-key, .settings-perview, .settings-area {",
			".settings-btn:not(.primary) {",
		} {
			start := strings.Index(css, selector)
			if start < 0 {
				t.Fatalf("theme %s is missing glass treatment for %s", id, selector)
			}
			end := strings.Index(css[start:], "}")
			if end < 0 || !strings.Contains(css[start:start+end], "background-image:") {
				t.Fatalf("theme %s needs a light-gradient surface for %s", id, selector)
			}
		}
		if !strings.Contains(css, "#settings-layer .palette-back {") {
			t.Fatalf("theme %s must preserve a visible backdrop behind Settings glass", id)
		}
		if !strings.Contains(css, ".settings-panel") || !strings.Contains(css, "backdrop-filter:blur(34px) saturate(") ||
			!strings.Contains(css, "refract(8px)") {
			t.Fatalf("theme %s lost the refractive glass filter on floating panels", id)
		}
		if id == "glass" && !strings.Contains(css, "rgba(18,27,42,0.15)") {
			t.Fatal("dark glass panels became too opaque")
		}
		if id == "glass-light" && !strings.Contains(css, "rgba(245,248,252,0.17)") {
			t.Fatal("light glass panels became too opaque")
		}
		if !strings.Contains(css, "* { box-shadow:none !important; }") {
			t.Fatalf("theme %s must keep shadows disabled", id)
		}

	}
}

func TestSettingsGlassOnlyAppliesToGlassThemes(t *testing.T) {
	for _, theme := range themeCatalog() {
		css := themeCSS(theme.ID)
		glass := theme.ID == "glass" || theme.ID == "glass-light"
		if strings.Contains(css, "backdrop-filter:") != glass {
			t.Fatalf("theme %s has incorrect glass filter scope", theme.ID)
		}
		if glass && !strings.Contains(css, "backdrop-filter:blur(6px) saturate(1)") {
			t.Fatalf("theme %s is missing the Settings backdrop blur", theme.ID)
		}
	}
}
