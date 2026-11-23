package app

import (
	"context"
	"os"
	"testing"
)

// TestScreenshotCommandPalette captures the command palette and the
// Language servers settings tab (gated like the other screenshot tests).
func TestScreenshotCommandPalette(t *testing.T) {
	if os.Getenv("OWNFRAME_CODE_VIEW_SCREENSHOT") != "1" {
		t.Skip("set OWNFRAME_CODE_VIEW_SCREENSHOT=1 to capture the frames")
	}

	ctx := context.Background()
	app := newTestApp(t)
	app.Page().SetSize(1400, 900)
	app.notePaletteCommand(paletteCmdBlame)

	app.paletteOpen = true
	app.paletteQuery = ">"
	app.refresh()

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	writeShot(t, app, "palette-commands")

	app.paletteQuery = ">tgl"
	app.refresh()

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	writeShot(t, app, "palette-commands-fuzzy")

	app.paletteOpen = false
	app.settingsOpen, app.settingsTab = true, "lsp"
	app.refresh()

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	writeShot(t, app, "settings-lsp")
}
