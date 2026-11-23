package app

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestScreenshotGuides renders indent guides and the matching-bracket
// highlight. Set OWNFRAME_CODE_VIEW_SCREENSHOT=1 to capture.
func TestScreenshotGuides(t *testing.T) {
	if os.Getenv("OWNFRAME_CODE_VIEW_SCREENSHOT") != "1" {
		t.Skip("set OWNFRAME_CODE_VIEW_SCREENSHOT=1 to capture the frames")
	}

	src := "package main\n\nfunc main() {\n    if true {\n        println(compute(21))\n    }\n}\n"
	app := newDiskGotoTestApp(t, map[string]string{"main.go": src})
	app.Page().SetSize(1280, 720)
	app.openFile("main.go")
	app.toggleEdit()

	text := app.edit.Text()
	app.editCaret = strings.Index(text, "compute(") + len("compute(")
	app.editAnchor = app.editCaret
	app.refresh()

	if err := app.Page().Redraw(context.Background()); err != nil {
		t.Fatal(err)
	}

	writeShot(t, app, "guides-brackets")
}
