package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClipProblemText(t *testing.T) {
	long := "cannot use compute(21) (value of type int) as string value in variable declaration"

	got := clipProblemText(long, 36)
	if n := len([]rune(got)); n != 36 || !strings.HasSuffix(got, "…") {
		t.Fatalf("clip = %q (%d runes), want %d ending in an ellipsis", got, n, 36)
	}

	if got := clipProblemText("short\nmessage\twith   gaps", 36); got != "short message with gaps" {
		t.Fatalf("flatten = %q", got)
	}

	if got := clipProblemText("exactly", 7); got != "exactly" {
		t.Fatalf("fit = %q", got)
	}
}

func TestProblemRowKeepsFullMessageAndFile(t *testing.T) {
	app, _, _ := lspTestApp(t, map[string]string{"a.go": "bad\n"})

	app.openFile("a.go")
	pump(t, app, "diagnostic", func() bool { return len(app.lspm.diags["a.go"]) == 1 })

	m := app.lspm
	d := m.diags["a.go"][0]
	d.Message = strings.Repeat("long message ", 12)
	m.setDiagnostics("a.go", m.docs["a.go"].server, []lspDiag{d})
	app.showProblems()
	rows := app.lspViewData().Problems

	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}

	if n := len([]rune(rows[0].Text)); n >= len([]rune(rows[0].Full)) {
		t.Fatalf("display text has %d runes, want it clipped", n)
	}

	if !strings.HasPrefix(rows[0].Full, "long message") || rows[0].Name != "a.go" {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestPopupHeadingHidesMarkers(t *testing.T) {
	lines := popupContentLines("## func Open\n\nbody text")
	if len(lines) == 0 || len(lines[0].Segments) != 1 {
		t.Fatalf("lines = %+v", lines)
	}

	seg := lines[0].Segments[0]
	if seg.Text != "func Open" || seg.Class != headingClass(2) {
		t.Fatalf("heading segment = %+v", seg)
	}

	// Non-heading Markdown still goes through the highlighter.
	if got := popupMarkdownSegments("plain `code`"); len(got) < 2 {
		t.Fatalf("inline code not styled: %+v", got)
	}
}

func TestGitUnavailablePathWrapsInSourceControl(t *testing.T) {
	root := filepath.Join(t.TempDir(), strings.Repeat("longfolder", 17))
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	app, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)

	ctx := context.Background()
	app.ReloadChanges(ctx, false)
	app.setMode(ctx, "changes")
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	boxes := app.Page().Boxes()
	sidebar, sideOK := boxByID(boxes, "sidebar")
	path, pathOK := boxByID(boxes, "git-unavailable-path")
	if !sideOK || !pathOK || path.H < 35 || path.X < sidebar.X || path.X+path.W > sidebar.X+sidebar.W {
		t.Fatalf("wrapped path = %+v (present %v), sidebar = %+v (present %v)", path, pathOK, sidebar, sideOK)
	}
}

func TestRecentLongProjectNameFitsMenu(t *testing.T) {
	app := newTestApp(t)
	root := filepath.Join(t.TempDir(), "workspace-"+strings.Repeat("W", 90)+"-tail")
	app.recentRoots = []string{root}

	clickID(t, app, "file-menu")
	clickID(t, app, "recent-toggle")
	view := app.View()
	if !view.RecentOpen || len(view.RecentRoots) != 1 {
		t.Fatalf("recent menu = %+v", view.RecentRoots)
	}

	row := view.RecentRoots[0]
	if row.Path != root || !strings.HasPrefix(row.Name, "workspace-") ||
		!strings.Contains(row.Name, "…") || !strings.HasSuffix(row.Name, "-tail") {
		t.Fatalf("recent row = %+v", row)
	}
	boxes := app.Page().Boxes()
	menu, menuOK := boxByID(boxes, "recent-flyout")
	item, itemOK := boxByID(boxes, "recent-"+root)
	if !menuOK || !itemOK || item.X+item.W > menu.X+menu.W ||
		item.Action != "project-open-path:"+root {
		t.Fatalf("recent item = %+v (present %v), menu = %+v (present %v)", item, itemOK, menu, menuOK)
	}
}
