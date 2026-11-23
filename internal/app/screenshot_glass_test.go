package app

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

var glassShotThemes = []string{"glass", "glass-light", "midnight", "light"}

// glassWorkspace builds a git repo parked in a merge conflict with extra
// modified, staged, image and Markdown files so every surface has content.
func glassWorkspace(t *testing.T) string {
	t.Helper()

	dir := textConflictRepo(t)

	entries, err := os.ReadDir("../../testdata/sample/backend")
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join("../../testdata/sample/backend", e.Name())) //nolint:gosec // fixed test fixture path
		if err != nil {
			t.Fatal(err)
		}

		writeFile(t, dir, "backend/"+e.Name(), string(raw))
	}

	readme, err := os.ReadFile("../../testdata/sample/README.md")
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "README.md", string(readme))
	writeFile(t, dir, "docs/notes.md", "# Notes\n\nSome **bold** text and `code`.\n\n- one\n- two\n")

	img := image.NewRGBA(image.Rect(0, 0, 160, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 160; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y * 2), 180, 255})
		}
	}

	f, err := os.Create(filepath.Join(dir, "pic.png")) //nolint:gosec // temp dir
	if err != nil {
		t.Fatal(err)
	}

	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}

	_ = f.Close()

	runGit(t, dir, "add", "README.md", "backend", "pic.png", "docs")
	cmd := exec.CommandContext(t.Context(), "git", "-C", dir, "-c", "user.name=T", "-c", "user.email=t@e.x", "commit", "-q", "--no-verify", "-m", "scratch")
	_ = cmd.Run() // the merge in progress may refuse a partial commit; staged state is enough
	writeFile(t, dir, "backend/main.go", "package main\n\n// changed\nfunc main() { println(\"hi\") }\n")
	runGit(t, dir, "add", "backend/main.go")
	writeFile(t, dir, "README.md", string(readme)+"\nmore text\n")

	return dir
}

func glassRedraw(t *testing.T, app *App) {
	t.Helper()

	if err := app.Page().Redraw(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestScreenshotGlass renders every major surface under the glass themes
// and two flat themes for comparison.
func TestScreenshotGlass(t *testing.T) {
	if os.Getenv("OWNFRAME_CODE_VIEW_SCREENSHOT") != "1" {
		t.Skip("set OWNFRAME_CODE_VIEW_SCREENSHOT=1 to capture the frames")
	}

	requireGit(t)

	ctx := context.Background()
	dir := glassWorkspace(t)

	for _, theme := range glassShotThemes {
		app := newMergeTestApp(t, dir)
		app.Page().SetSize(1600, 900)
		app.theme = theme

		if err := app.Page().SetTheme(app.activeThemeCSS()); err != nil {
			t.Fatal(err)
		}

		shot := func(name string) {
			t.Helper()
			glassRedraw(t, app)
			writeShot(t, app, "glass-"+theme+"-"+name)
		}
		click := func(action string) {
			t.Helper()

			if err := app.onClick(ctx, ownframe.Box{Action: action}); err != nil {
				t.Fatal(action, err)
			}
		}

		app.openFile("backend/main.go")
		app.openFile("backend/probe.go")
		shot("01-explorer-editor-tabs")

		app.toggleEdit()
		if err := app.openFind(ctx); err != nil {
			t.Fatal(err)
		}
		shot("02-edit-find")
		app.finishEdit()

		app.toggleSplit()
		app.openFileIn(1, "backend/server.go")
		shot("03-split-editor")
		app.toggleSplit()

		app.toggleDetails()
		shot("04-details-drawer")
		app.toggleDetails()

		app.openFile("docs/notes.md")
		app.togglePreview()
		shot("05-markdown-preview")
		app.togglePreview()

		app.openFile("pic.png")
		shot("06-image-preview")

		click("mode-changes")
		shot("07-source-control-history")

		app.selectDiff("backend/main.go")
		app.setSplit(false)
		shot("08-diff-unified")
		app.setSplit(true)
		shot("09-diff-split")
		app.setSplit(false)

		app.openMergeConflict(ctx, mergeTestPath)
		shot("10-merge-conflict")

		click("mode-explorer")

		if err := app.openCommitDialog(ctx); err != nil {
			t.Fatal(err)
		}
		shot("11-commit-dialog")
		app.commitDialogOpen = false
		app.refresh()

		app.openExplorerDeleteConfirm([]string{"pic.png"})
		shot("12-confirm-dialog")
		app.confirmOpen = false
		app.refresh()

		app.showToast("success", "Saved", "backend/main.go written to disk.")
		app.refresh()
		shot("13-toast")
		app.toast = toastView{}

		app.presentMenu(500, 300, []ownframe.MenuItem{
			{ID: "a", Label: "Open", Enabled: true},
			{ID: "b", Label: "Rename", Hint: "F2", Enabled: true},
			{ID: "c", Label: "Delete", Enabled: false},
		})
		shot("14-context-menu")
		app.closeMenu()

		if err := app.openPalette(ctx); err != nil {
			t.Fatal(err)
		}
		shot("15-command-palette")
		click("palette-close")

		if err := app.openWorkspaceSearch(ctx); err != nil {
			t.Fatal(err)
		}

		app.searchQuery = "main"
		app.runWorkspaceSearch(ctx)
		pump(t, app, "search", func() bool { return !app.View().Search.Busy })
		shot("16-workspace-search")
		_ = app.closeWorkspaceSearch(ctx)

		click("settings-open")

		for _, tab := range []string{"appearance", "keyboard", "files", "help", "about"} {
			click("settings-tab:" + tab)
			shot("17-settings-" + tab)
		}

		click("settings-close")

		app.toggleTerminal(ctx)
		shot("18-terminal")
		app.toggleTerminal(ctx)

		app.Close()
	}
}

// TestScreenshotGlassPopup renders diagnostics, problems and popups.
func TestScreenshotGlassPopup(t *testing.T) {
	if os.Getenv("OWNFRAME_CODE_VIEW_SCREENSHOT") != "1" {
		t.Skip("set OWNFRAME_CODE_VIEW_SCREENSHOT=1 to capture the frames")
	}

	ctx := context.Background()

	for _, theme := range glassShotThemes {
		src := "bad := compute(1)\nfunc compute(n int) int {\n\treturn n * 2\n}\n"
		app, _, _ := lspTestApp(t, map[string]string{"main.go": src})
		app.Page().SetSize(1600, 900)
		app.theme = theme

		if err := app.Page().SetTheme(app.activeThemeCSS()); err != nil {
			t.Fatal(err)
		}

		app.openFile("main.go")
		pump(t, app, "diagnostic", func() bool { return len(app.lspm.diags["main.go"]) == 1 })
		glassRedraw(t, app)
		writeShot(t, app, "glass-"+theme+"-20-diagnostics")

		app.toggleDetails()
		glassRedraw(t, app)
		writeShot(t, app, "glass-"+theme+"-21-problems")
		app.toggleDetails()

		if err := app.runPopupDemo(ctx, paletteCmdPopupList); err != nil {
			t.Fatal(err)
		}

		writeShot(t, app, "glass-"+theme+"-22-popup-list")

		if err := app.runPopupDemo(ctx, paletteCmdPopupCard); err != nil {
			t.Fatal(err)
		}

		writeShot(t, app, "glass-"+theme+"-23-popup-card")
	}
}
