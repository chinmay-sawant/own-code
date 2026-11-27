package app

import (
	"context"
	"strings"
	"testing"
)

func TestUndoRedoOperationBased(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	enterEditAtFirstLine(t, app)

	app.edit.SetText("alpha\nbeta\n")
	app.editCaret, app.editAnchor = 0, 0
	app.commitEditBuffer()

	texts := []string{app.edit.Text()}

	steps := []struct {
		start, end int
		ins        string
	}{{5, 5, "!"}, {0, 0, "start "}, {6, 11, ""}, {1, 3, "XYZ"}}
	for _, s := range steps {
		if err := app.replaceEditRange(ctx, app.edit.Text(), s.start, s.end, s.ins); err != nil {
			t.Fatal(err)
		}

		texts = append(texts, app.edit.Text())
	}

	// No record may keep a full buffer copy except the open newest one.
	for i, r := range app.editUndo {
		if r.open && i != len(app.editUndo)-1 {
			t.Fatalf("record %d still open", i)
		}
	}

	for i := len(texts) - 2; i >= 0; i-- {
		if err := app.onUndo(ctx); err != nil {
			t.Fatal(err)
		}

		if got := app.edit.Text(); got != texts[i] {
			t.Fatalf("undo to %d: got %q want %q", i, got, texts[i])
		}
	}

	if err := app.onUndo(ctx); err != nil || app.edit.Text() != texts[0] {
		t.Fatalf("extra undo changed text: %q", app.edit.Text())
	}

	for i := 1; i < len(texts); i++ {
		if err := app.onRedo(ctx); err != nil {
			t.Fatal(err)
		}

		if got := app.edit.Text(); got != texts[i] {
			t.Fatalf("redo to %d: got %q want %q", i, got, texts[i])
		}
	}

	// Undo then a fresh edit drops redo; undo still walks back correctly.
	_ = app.onUndo(ctx)
	_ = app.onUndo(ctx)

	if err := app.replaceEditRange(ctx, app.edit.Text(), 0, 0, ">"); err != nil {
		t.Fatal(err)
	}

	if len(app.editRedo) != 0 {
		t.Fatal("redo not cleared by new edit")
	}

	_ = app.onUndo(ctx)

	if got := app.edit.Text(); got != texts[2] {
		t.Fatalf("after branch undo: %q want %q", got, texts[2])
	}
}

func TestUndoRestoresCaretAndStaysSmall(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	enterEditAtFirstLine(t, app)

	big := strings.Repeat("0123456789\n", 5000)
	app.edit.SetText(big)
	app.editCaret, app.editAnchor = 3, 3
	app.commitEditBuffer()

	for i := 0; i < 20; i++ {
		if err := app.replaceEditRange(ctx, app.edit.Text(), 3, 3, "x"); err != nil {
			t.Fatal(err)
		}
	}

	held := 0

	for _, r := range app.editUndo {
		held += len(r.before) + len(r.Apply.Text)
	}

	if held > 2*len(big) {
		t.Fatalf("undo history holds %d bytes for a %d byte buffer", held, len(big))
	}

	if err := app.onUndo(ctx); err != nil {
		t.Fatal(err)
	}

	if app.editCaret != 4 || app.editAnchor != 4 {
		t.Fatalf("caret %d anchor %d after undo, want 4/4 (caret before the last edit)", app.editCaret, app.editAnchor)
	}
}
