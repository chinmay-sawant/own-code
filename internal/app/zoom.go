package app

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// Layout zoom bounds for the code view. The window's own paint zoom covers
// 0.25 to 4; the layout zoom stays inside it and matches the editor feel of
// staying readable without exploding the window frame.
const (
	zoomMin  = 0.5
	zoomMax  = 3.0
	zoomStep = 1.1
	// zoomEpsilon treats a level within a rounding error of 1 as exactly
	// 100 percent, so the override stays byte-stable at the default.
	zoomEpsilon = 1e-9
	// zoomPercent scales a level to the status bar label.
	zoomPercent = 100
)

// uiZoom is the active layout multiplier, defaulting to 1 for a fresh app
// and clamping anything the session hands back into range.
func (a *App) uiZoom() float64 {
	if a.zoom <= 0 {
		return 1
	}

	if a.zoom < zoomMin {
		return zoomMin
	}

	if a.zoom > zoomMax {
		return zoomMax
	}

	return a.zoom
}

// zx scales an integer pixel value by the layout zoom, keeping at least one
// pixel. Pass every layout constant that feeds scroll math, hit testing, or
// a CSS var through here so Go and paint stay on the same grid.
func (a *App) zx(v int) int {
	if v <= 0 {
		return v
	}

	return max(1, int(math.Round(float64(v)*a.uiZoom())))
}

// zf scales a fractional pixel value such as a monospace cell advance.
func (a *App) zf(v float64) float64 {
	return v * a.uiZoom()
}

// zoomLabel renders the status bar chip, e.g. "110%".
func (a *App) zoomLabel() string {
	return fmt.Sprintf("%.0f%%", math.Round(a.uiZoom()*zoomPercent))
}

// zoomIn steps the layout zoom up and keeps the scroll position.
func (a *App) zoomIn(ctx context.Context) error {
	return a.setZoom(ctx, a.uiZoom()*zoomStep)
}

// zoomOut steps the layout zoom down and keeps the scroll position.
func (a *App) zoomOut(ctx context.Context) error {
	return a.setZoom(ctx, a.uiZoom()/zoomStep)
}

// resetZoom returns the layout to 100 percent.
func (a *App) resetZoom(ctx context.Context) error {
	return a.setZoom(ctx, 1)
}

// setZoom clamps the level, rescales the scroll offsets so the same first
// row stays on screen, persists the session and the global settings
// (gestures write session + global; the session wins on restore), and
// repaints with the zoom stylesheet layered over the theme.
func (a *App) setZoom(ctx context.Context, level float64) error {
	old := a.uiZoom()

	a.zoom = level
	now := a.uiZoom()

	if old > 0 && now != old {
		scale := func(v int) int { return int(math.Round(float64(v) * now / old)) }
		a.scrollY = scale(a.scrollY)
		a.scrollX = scale(a.scrollX)
		for group := 0; group < 2; group++ {
			a.scrollGroup[group] = scale(a.scrollGroup[group])
			a.scrollXGrp[group] = scale(a.scrollXGrp[group])
			for path, offset := range a.tabScroll[group] {
				a.tabScroll[group][path] = scale(offset)
			}
		}
	}

	a.persistSession()
	a.persistSettings()
	if a.split {
		// Reclamp both groups against their own documents instead of
		// funneling the shared single-pane offsets into the focused pane.
		for group := 0; group < 2; group++ {
			a.scrollGroup[group] = a.clampGroupScroll(group, a.scrollGroup[group])
			if maxW := a.maxLineWidthFor(group); a.scrollXGrp[group] > maxW {
				a.scrollXGrp[group] = maxW
			}
		}
		a.scrollTo(a.scrollY)
	} else {
		a.setScrollX(a.scrollX)
		a.scrollTo(a.scrollY)
	}

	if err := a.page.SetTheme(a.activeThemeCSS()); err != nil {
		return err
	}

	return a.page.Redraw(ctx)
}

// baseFontSize is the global base font in px, defaulting a missing value
// to 12 and clamping hand edits into 8..24.
func (a *App) baseFontSize() float64 {
	if a.fontSize <= 0 {
		return defaultFontSize
	}
	return clampFontSize(a.fontSize)
}

// fontLabel renders the base size, e.g. "12px".
func (a *App) fontLabel() string {
	return fmt.Sprintf("%.0fpx", math.Round(a.baseFontSize()))
}

// statusZoomLabel renders the status bar chip, e.g. "110% · 12px".
func (a *App) statusZoomLabel() string {
	return a.zoomLabel() + " · " + a.fontLabel()
}

// setFontSize clamps the base font, persists session + global, and repaints.
func (a *App) setFontSize(ctx context.Context, size float64) error {
	a.fontSize = clampFontSize(size)
	a.persistSession()
	a.persistSettings()
	if err := a.page.SetTheme(a.activeThemeCSS()); err != nil {
		return err
	}
	return a.page.Redraw(ctx)
}

// resetFontSize returns the base font to 12px.
func (a *App) resetFontSize(ctx context.Context) error {
	return a.setFontSize(ctx, defaultFontSize)
}

// effectiveZoom returns the layout zoom for one view. V1 knows editor and
// terminal overrides; every other view follows the global zoom. zx/zf stay
// global for scroll math and hit testing; per-view zoom is CSS-only v1.
func (a *App) effectiveZoom(view string) float64 {
	if a.zoomPerView != nil {
		if v, ok := a.zoomPerView[view]; ok && v >= zoomMin && v <= zoomMax {
			return v
		}
	}
	return a.uiZoom()
}

// setZoomPerView sets or clears one per-view override (0/1 clears), then
// persists and repaints. V1 supports editor and terminal only.
func (a *App) setZoomPerView(ctx context.Context, view string, level float64) error {
	if view != "editor" && view != "terminal" {
		return nil
	}
	if a.zoomPerView == nil {
		a.zoomPerView = map[string]float64{}
	}
	if level <= 0 || math.Abs(level-1) < zoomEpsilon {
		delete(a.zoomPerView, view)
	} else {
		a.zoomPerView[view] = clampSettingsZoom(level)
	}
	a.persistSession()
	a.persistSettings()
	if err := a.page.SetTheme(a.activeThemeCSS()); err != nil {
		return err
	}
	return a.page.Redraw(ctx)
}

// activeThemeCSS layers the zoom override after the theme so it wins the
// cascade without touching the theme catalog.
func (a *App) activeThemeCSS() string {
	return themeCSS(a.theme) + zoomOverrideCSSWithFont(a.uiZoom(), a.baseFontSize(), a.zoomPerView)
}

// zoomFont is one fixed font size the zoom override rescales.
type zoomFont struct {
	sel  string
	base float64
}

// zoomFontTable covers the fixed text the layout zoom rescales. Everything
// else inherits the scaled body size.
func zoomFontTable() []zoomFont {
	return []zoomFont{
		{"body", 12},
		{".cline", 12},
		{".srow", 12},
		{".sticky-kind", 10},
		{".ln", 10},
		{".ln.cont", 9},
		{".blame", 10},
		{".tab-name", 9.5},
		{".crumb-path, .diff-path, .file-path", 9.5},
		{".tname", 11},
		{".hist-subject", 9.5},
		{".hist-branch", 9},
		{".history-current", 9.5},
		{".history-current-count", 8.5},
		{".hist-kicker, .hist-pages", 8.5},
		{".hist-sample, .hist-file-stat", 8},
		{".hist-more, .hist-error", 8.5},
		{".history-current-arrow", 15},
		{".drow", 12},
		{".palette-close", 14},
		{".dnum", 9.5},
		{".sb-branch", 9.5},
		{".sb-path", 9},
		{".act-badge", 8.5},
		// Titlebar.
		{".file-btn", 12},
		{".brand-name", 12.5},
		{".brand-sub", 9.5},
		{".cmdbar", 10.5},
		{".cmd-key", 9},
		{".file-item, .project-input", 11},
		{".locations-label", 9},
		{".browse-path", 10},
		{".browse-filter-input", 11},
		{".browse-meta", 9},
		{".file-alert", 11},
		{".file-alert-x", 10},
		// Explorer tree and filter.
		{".filter-input", 10.5},
		{".filter-x", 13},
		{".tree-note", 7},
		{".fstat, .cstat", 9},
		{".tree-tag, .tree-more, .side-foot", 9.5},
		{".side-count", 9},
		// Changes list.
		{".changes-title", 9.5},
		{".side-title", 9.5},
		{".change-name", 10.5},
		{".change-dir", 9},
		{".change-num", 8.5},
		{".sc-stats", 9},
		{".changes-count", 8.5},
		{".side-tag", 7.5},
		// Editor window controls.
		{".tab-x", 13},
		{".tab-tool", 11},
		{".seg-btn", 9},
		{".crumb-btn", 9},
		{".crumb-meta", 9.5},
		{".crumbs", 10},
		{".lang-pill", 8.5},
		{".edit-flag", 8},
		{".find-input", 12},
		{".find-count", 8.5},
		{".edit-hint", 9},
		{".expand-btn", 10},
		{".keys em", 8.5},
		{".welcome-card h1", 21},
		{".welcome-card p", 11},
		{".empty h2", 15},
		{".empty p", 10.5},
		{".empty .button", 10},
		{".ctx-item", 11},
		{".keys", 9},
		// Diff.
		{".dhunk, .dmeta", 9.5},
		// History rail.
		{".hist-hash", 9},
		{".hist-when, .hist-stat", 8.5},
		{".hist-meta", 8.5},
		{".hist-file-name", 9.5},
		{".hist-file-dir", 8.5},
		{".hist-chev", 9},
		{".branch-name", 9},
		{".branch-input", 9.5},
		{".branch-mark", 8},
		{".branch-track", 8},
		{".branch-tag", 7.5},
		{".branch-more", 8.5},
		{".hist-btn", 8.5},
		{".commit-tip-float b", 10},
		{".commit-tip-float span", 9.5},
		{".commit-tip-float em", 8},
		// Search palette.
		{".palette-text b", 10.5},
		{".palette-text span", 9},
		{".palette-go", 10},
		{".search-meta", 9},
		{".search-loc", 8.5},
		{".search-line, .search-text", 9},
		{".palette-foot", 9},
		{".palette-empty", 10},
		// Details panel.
		{".settings-header", 14},
		{".settings-title", 14},
		{".settings-theme-text span", 11},
		{".settings-sub", 12},
		{".settings-key-text b", 11},
		{".settings-key-text span", 10},
		{".settings-key-fixed, .settings-capturing", 10}, {".settings-check", 14},
		{".det-kicker", 9.5},
		{".det-x", 14},
		{".det-file b", 11},
		{".det-file span", 9},
		{".det-grid", 9.5},
		{".okind", 8},
		{".oname", 9},
		{".olno", 8.5},
		{".det-empty", 9.5},
		{".det-sub", 8.5},
		{".ficon", 8.5},
		{".ficon.lg", 11},
		// Status bar.
		{".statusbar", 10},
		{".scan-badge", 7.5},
		// Preview.
		{".prow", 12},
		{".pmark", 10},
		{".prow-h1 .ptext", 14},
		{".prow-h2 .ptext", 13},
		{".prow-h3 .ptext", 12},
		{".prow-code .ptext, .prow-table .ptext, .prow-diagram .ptext", 11},
		// Toast and bottom floats.
		{".toast-text b", 11.5},
		{".toast-text span", 10.5},
		{".toast-x", 14},
		{".file-nav-arrow", 17},
		{".file-nav-change", 14},
		{".file-nav-pos, .file-nav-stats", 9.5},
		{".file-nav-current", 11},
		{".file-nav-dir", 9},
		// Embedded terminal.
		{".term-body", 12},
		{".term-tab-name", 9.5},
		{".term-tab-shell", 7.5},
		{".term-kicker", 8.5},
		{".term-foot", 8.5},
		{".term-note", 9},
		// Merge editor.
		{".mrow", 12},
		{".merge-head", 9.5},
		{".merge-btn", 8.5},
	}
}

// zoomOverrideCSS rescales the layout vars, the fixed row heights, and the
// reader fonts. An empty string at 100 percent keeps the theme byte-stable.
// It is the font-agnostic wrapper; activeThemeCSS uses the font-aware form
// below so a non-default base font still paints.
func zoomOverrideCSS(z float64) string {
	return zoomOverrideCSSWithFont(z, defaultFontSize, nil)
}

// zoomOverrideCSSWithFont rescales by the layout zoom times the base-font
// ratio (fontSize/12). override(1,12) with no per-view overrides is empty,
// keeping the theme byte-stable at the default.
func zoomOverrideCSSWithFont(z, fontSize float64, perView map[string]float64) string {
	if z <= 0 {
		z = 1
	}
	if fontSize <= 0 {
		fontSize = defaultFontSize
	} else {
		fontSize = clampFontSize(fontSize)
	}
	fontRatio := fontSize / defaultFontSize
	hasPerView := false
	for _, view := range []string{"editor", "terminal"} {
		if v, ok := perView[view]; ok && v > 0 && math.Abs(v-1) >= zoomEpsilon {
			hasPerView = true
			break
		}
	}
	if math.Abs(z-1) < zoomEpsilon && math.Abs(fontSize-defaultFontSize) < zoomEpsilon && !hasPerView {
		return ""
	}

	var b strings.Builder

	// Identity globals emit nothing: at 100% with the default font the
	// theme paints alone, and a lone per-view override scopes only fonts.
	if math.Abs(z-1) >= zoomEpsilon || math.Abs(fontRatio-1) >= zoomEpsilon {
		px := func(v int) string {
			return fmt.Sprintf("%dpx", max(1, int(math.Round(float64(v)*z))))
		}

		b.WriteString(":root {")
		fmt.Fprintf(&b, " --row-h:%s !important;", px(rowH))
		fmt.Fprintf(&b, " --titlebar-h:%s !important;", px(windowControlTop))
		fmt.Fprintf(&b, " --tab-h:%s !important;", px(tabStripH))
		fmt.Fprintf(&b, " --crumb-h:%s !important;", px(crumbH))
		fmt.Fprintf(&b, " --side-w:%s !important;", px(sideWidth))
		fmt.Fprintf(&b, " --activity-w:%s !important;", px(activityW))
		fmt.Fprintf(&b, " --status-h:%s !important;", px(windowControlBottom))
		b.WriteString(" }")

		// Core rows.
		fmt.Fprintf(&b, ".trow { height:%s !important; }", px(treeRowH))

		// The terminal grid uses the same rounded row height as the Go geometry,
		// so windowing and the painted rows stay aligned at every zoom.
		fmt.Fprintf(&b, ".term-resize { height:%s !important; flex-basis:%s !important; }", px(termResizeH), px(termResizeH))
		fmt.Fprintf(&b, ".term-head { height:%s !important; flex-basis:%s !important; }", px(termHeadH), px(termHeadH))
		fmt.Fprintf(&b, ".term-foot { height:%s !important; flex-basis:%s !important; }", px(termFootH), px(termFootH))
		fmt.Fprintf(&b, ".term-note { height:%s !important; flex-basis:%s !important; line-height:%s !important; }", px(termNoteH), px(termNoteH), px(termNoteH))
		fmt.Fprintf(&b, ".term-body { padding:%s %s !important; }", px(termBodyPad/2), px(termWidthPad/2))
		fmt.Fprintf(&b, ".term-row { height:%s !important; min-height:%s !important; max-height:%s !important; flex-basis:%s !important; line-height:%s !important; }",
			px(termRowH), px(termRowH), px(termRowH), px(termRowH), px(termRowH))
		fmt.Fprintf(&b, ".term-tab, .term-tab .term-pick { height:%s !important; }", px(termHeadH))

		histH := px(histRowH)
		fmt.Fprintf(&b, ".hist-row { height:%s !important; min-height:%s !important; max-height:%s !important; }", histH, histH, histH)
		fmt.Fprintf(&b, ".change { min-height:%s !important; }", px(changeRowH))
		fmt.Fprintf(&b, ".orow { height:%s !important; }", px(treeRowH))
		fmt.Fprintf(&b, ".edit-selection { height:%s !important; }", px(rowH))
		fmt.Fprintf(&b, ".hsplit { height:%s !important; flex-basis:%s !important; }", px(histSplitH), px(histSplitH))

		for _, f := range zoomFontTable() {
			// Integral sizes only: the replay shapes text on an integer ppem
			// grid, so a fractional size would paint wider than the frame the
			// layout measured and push each run into the next.
			fmt.Fprintf(&b, "%s { font-size:%dpx !important; }", f.sel, max(1, int(math.Round(f.base*z*fontRatio))))
		}

		zoomWindowControlCSS(&b, px)
		zoomTreeCSS(&b, px)
		zoomEditorCSS(&b, px)
		zoomRailsCSS(&b, px)
		zoomHistoryCSS(&b, px)
		zoomDiffCSS(&b, px)
		zoomFloatsCSS(&b, px)
		zoomOverlayCSS(&b, px)
		zoomDetailsCSS(&b, px)
		zoomPreviewCSS(&b, px)
		zoomActivityCSS(&b, px)
	}

	zoomPerViewCSS(&b, fontSize, perView)

	return b.String()
}

// zoomPerViewCSS scopes editor/terminal-only font scaling after the global
// rules so per-view overrides win the cascade. V1 touches font size only
// and keeps row heights global, so caret and terminal-row geometry (which
// zx/zf compute globally) cannot desync at extremes.
func zoomPerViewCSS(b *strings.Builder, fontSize float64, perView map[string]float64) {
	if perView == nil {
		return
	}
	if v, ok := perView["editor"]; ok && v > 0 && math.Abs(v-1) >= zoomEpsilon {
		v = clampSettingsZoom(v)
		ed := func(base float64) string {
			return fmt.Sprintf("%dpx", max(1, int(math.Round(base*(fontSize/defaultFontSize)*v))))
		}
		fmt.Fprintf(b, ".cline { font-size:%s !important; }", ed(12))
		fmt.Fprintf(b, ".drow { font-size:%s !important; }", ed(12))
		fmt.Fprintf(b, ".ln { font-size:%s !important; }", ed(10))
		fmt.Fprintf(b, ".blame { font-size:%s !important; }", ed(10))
		fmt.Fprintf(b, ".mrow { font-size:%s !important; }", ed(12))
	}
	if v, ok := perView["terminal"]; ok && v > 0 && math.Abs(v-1) >= zoomEpsilon {
		v = clampSettingsZoom(v)
		tm := func(base float64) string {
			return fmt.Sprintf("%dpx", max(1, int(math.Round(base*(fontSize/defaultFontSize)*v))))
		}
		fmt.Fprintf(b, ".term-body { font-size:%s !important; }", tm(12))
		fmt.Fprintf(b, ".term-note { font-size:%s !important; }", tm(9))
	}
}
