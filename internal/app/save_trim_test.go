package app

import "testing"

func TestTrimEditForSave(t *testing.T) {
	app := newTestApp(t)
	enterEditAtFirstLine(t, app)

	app.trimMode = "all"
	app.edit.SetText("a  \nb\t\nc\n")
	app.editCaret, app.editAnchor = 2, 2

	app.trimEditForSave(app.edit.Path)

	if got := app.edit.Text(); got != "a\nb\nc\n" {
		t.Fatalf("trimmed = %q", got)
	}

	if app.editCaret != 1 {
		t.Fatalf("caret = %d, want clamped to 1", app.editCaret)
	}

	app.trimMode = ""
	app.edit.SetText("a  \n")
	app.trimEditForSave(app.edit.Path)

	if got := app.edit.Text(); got != "a  \n" {
		t.Fatalf("trim ran with mode off: %q", got)
	}
}
