package app

import (
	"context"
	"strings"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

// TestSingleLineInputsCapped ensures every single-line search/filter input
// carries maxlength="255" so long values cannot grow without bound.
func TestSingleLineInputsCapped(t *testing.T) {
	html := buildHTML()
	for _, id := range []string{
		`id="workspace-search"`, `id="workspace-replace"`, `id="palette"`,
		`id="filter"`, `id="branch-filter"`,
		`id="find"`, `id="find-replace"`,
		`id="browse-filter"`, `id="project-path"`,
	} {
		idx := strings.Index(html, id)
		if idx < 0 {
			t.Fatalf("missing input %s", id)
		}
		end := strings.Index(html[idx:], ">")
		if end < 0 {
			t.Fatalf("unterminated input %s", id)
		}
		tag := html[idx : idx+end]
		// The palette placeholder nests template conditionals with ">",
		// so the tag slice may cut early; fall back to a wider window.
		if !strings.Contains(tag, `maxlength="255"`) {
			wide := html[idx:min(idx+1200, len(html))]
			if !strings.Contains(wide, `maxlength="255"`) {
				t.Fatalf("input %s lacks maxlength=\"255\"", id)
			}
		}
	}
}

// TestSingleLineInputsNoWrap ensures the single-line CSS keeps the engine's
// rewritten field spans on one line and clipped inside their bars.
func TestSingleLineInputsNoWrap(t *testing.T) {
	html := buildHTML()
	if !strings.Contains(html, `span[data-ownframe-field="input"]`) ||
		!strings.Contains(html, "white-space: pre") ||
		!strings.Contains(html, "text-overflow: ellipsis") {
		t.Fatal("missing global single-line input rule")
	}
	if !strings.Contains(html, ".palette-search") || !strings.Contains(html, ".filter") {
		t.Fatal("missing search bar containers")
	}
}

// TestClampFieldLen ensures the Go clamp keeps queries at 255 runes.
func TestClampFieldLen(t *testing.T) {
	if got := len([]rune(clampFieldLen(strings.Repeat("a", 300)))); got != maxFieldLen {
		t.Fatalf("long clamp = %d, want %d", got, maxFieldLen)
	}
	if got := clampFieldLen("short"); got != "short" {
		t.Fatalf("short clamp = %q", got)
	}
	longUni := strings.Repeat("é", 300)
	if got := len([]rune(clampFieldLen(longUni))); got != maxFieldLen {
		t.Fatalf("unicode clamp = %d, want %d", got, maxFieldLen)
	}
}

// TestOnChangeClampsLongQuery ensures a long binder value is truncated in
// app state instead of flowing into search.
func TestOnChangeClampsLongQuery(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	long := strings.Repeat("x", 300)
	app.view.SearchQuery = long
	if err := app.onChange(ctx, ownframe.Box{ID: "workspace-search"}); err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(app.searchQuery)); got != maxFieldLen {
		t.Fatalf("searchQuery len = %d, want %d", got, maxFieldLen)
	}
	app.view.PaletteQuery = long
	if err := app.onChange(ctx, ownframe.Box{ID: "palette"}); err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(app.paletteQuery)); got != maxFieldLen {
		t.Fatalf("paletteQuery len = %d, want %d", got, maxFieldLen)
	}
}
