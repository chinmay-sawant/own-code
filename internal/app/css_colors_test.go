package app

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

var (
	cssComment   = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssTokenDecl = regexp.MustCompile(`--[a-zA-Z0-9-]+\s*:[^;{}]*;`)
	cssColorLit  = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|rgba?\([^)]*\)`)
)

// colorLiteralBudget caps the hard-coded color literals each component file
// may hold outside custom-property declarations (token definitions such as
// base.css :root are exempt). Remaining literals are intentional: language
// badges and theme swatches are fixed brand colors, shadows are removed
// under glass, the close button red and the mid-gray SVG rail must stay
// theme independent. Lowering a number is always fine; raising one needs a
// reason, so prefer a var(--...) token that glassTokenCSS can retint.
var colorLiteralBudget = map[string]int{
	"base.css":           24, // .lang-* file badges (themes.go overrides them for light themes)
	"settings.css":       18, // .swatch-* theme previews are fixed theme colors
	"editor.css":         4,  // soft shadows (glass sets box-shadow none) + empty-mark borders
	"sidebar.css":        9,  // shadow, scrim (glass overrides), multi-select tint, status tints
	"window-control.css": 5,  // shadow, browse scrim, 0.01 hit area, win-close red
	"merge.css":          4,  // stage/compare outlines
	"inline_card.css":    3,  // shadow + old/new line washes
	"history.css":        1,  // shadow
	"palette.css":        2,  // scrim (glass overrides) + search-match highlight
	"scrollrails.css":    0,
	"popup.css":          1, // soft shadow (glass sets box-shadow none)
	"file_nav.css":       1, // soft shadow
	"diff.css":           0,
}

// TestComponentCSSColorBudget fails when a component stylesheet gains a new
// hard-coded color literal. Literals ignore the active theme, so each one is
// a spot where Liquid Glass (or any theme) cannot restyle consistently.
func TestComponentCSSColorBudget(t *testing.T) {
	files, err := filepath.Glob("../../components/css/*.css")
	if err != nil || len(files) == 0 {
		t.Fatalf("no component CSS found: %v", err)
	}

	sort.Strings(files)

	for _, path := range files {
		raw, err := os.ReadFile(path) //nolint:gosec // fixed repo path from Glob
		if err != nil {
			t.Fatal(err)
		}

		text := cssComment.ReplaceAllString(string(raw), "")
		text = cssTokenDecl.ReplaceAllString(text, "")
		got := len(cssColorLit.FindAllString(text, -1))
		name := filepath.Base(path)

		if budget, ok := colorLiteralBudget[name]; !ok && got > 0 {
			t.Errorf("%s has %d color literals and no budget; use var(--...) tokens", name, got)
		} else if got > budget {
			t.Errorf("%s has %d color literals, budget %d; use var(--...) tokens instead of new literals", name, got, budget)
		}
	}
}
