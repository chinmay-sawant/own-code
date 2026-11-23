package app

import (
	"context"
	"os"
	"testing"
)

// TestScreenshotNewFeatures renders the LSP diagnostics UI and the popup
// widget at 1920x1080. It is a development aid: set
// OWNFRAME_CODE_VIEW_SCREENSHOT=1 and read the PNGs from shotDir.
func TestScreenshotNewFeatures(t *testing.T) {
	if os.Getenv("OWNFRAME_CODE_VIEW_SCREENSHOT") != "1" {
		t.Skip("set OWNFRAME_CODE_VIEW_SCREENSHOT=1 to capture the frames")
	}

	ctx := context.Background()
	src := "bad := compute(1)\nfunc compute(n int) int {\n\treturn n * 2\n}\n\nfunc main() {\n\tprintln(compute(21))\n}\n"
	app, _, _ := lspTestApp(t, map[string]string{"main.go": src})
	app.Page().SetSize(1920, 1080)

	app.openFile("main.go")
	pump(t, app, "diagnostic", func() bool { return len(app.lspm.diags["main.go"]) == 1 })

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	writeShot(t, app, "feat-diagnostics")

	app.toggleDetails()

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	writeShot(t, app, "feat-problems-drawer")
	app.toggleDetails()

	if err := app.runPopupDemo(ctx, paletteCmdPopupList); err != nil {
		t.Fatal(err)
	}

	writeShot(t, app, "feat-popup-list")

	if err := app.runPopupDemo(ctx, paletteCmdPopupCard); err != nil {
		t.Fatal(err)
	}

	writeShot(t, app, "feat-popup-card")
}
