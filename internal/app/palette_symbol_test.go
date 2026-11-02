package app

import (
	"context"
	"testing"
)

func TestPaletteSymbolRows(t *testing.T) {
	app := newTestApp(t)
	app.openFile("backend/probe.go")

	rows := app.symbolRows("")
	if len(rows) == 0 {
		t.Fatal("no symbols for probe.go")
	}

	if len(rows) > paletteMaxRows {
		t.Fatalf("symbol rows = %d, want at most %d", len(rows), paletteMaxRows)
	}

	rows = app.symbolRows("prob")
	if len(rows) == 0 || rows[0].Name != "Prober" {
		t.Fatalf("prefix rows = %+v", rows)
	}

	foundProbe := false

	for _, row := range rows {
		if row.Name == "Probe" {
			foundProbe = true
		}
	}

	if !foundProbe {
		t.Fatalf("prefix rows miss Probe: %+v", rows)
	}

	// Prefix beats contains in the ranking.
	rows = app.symbolRows("robe")
	found := false

	for _, row := range rows {
		if row.Name == "Probe" {
			found = true
		}
	}

	if !found {
		t.Fatalf("contains rows = %+v", rows)
	}

	if rows := app.symbolRows("zzz-no-such-symbol"); len(rows) != 0 {
		t.Fatalf("no-match rows = %+v", rows)
	}
}

func TestPaletteSymbolSubmit(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.openFile("backend/probe.go")

	if err := app.openSymbolPalette(ctx); err != nil {
		t.Fatal(err)
	}

	if app.paletteMode != paletteModeSymbols || app.symbolScope != "backend/probe.go" {
		t.Fatalf("symbol palette = mode %q scope %q", app.paletteMode, app.symbolScope)
	}

	app.paletteQuery = "Probe"
	app.symbolQuery = "Probe"
	app.refresh()

	rows := app.paletteRows()
	if len(rows) == 0 || !rows[0].Symbol || rows[0].No == 0 {
		t.Fatalf("symbol palette rows = %+v", rows)
	}

	if err := app.onSubmit(ctx); err != nil {
		t.Fatal(err)
	}

	if app.paletteOpen {
		t.Fatal("symbol submit left the palette open")
	}

	if !app.editing || app.edit == nil {
		t.Fatal("symbol submit did not move the caret")
	}

	line, _ := offsetLineCol(app.edit.Text(), app.editCaret)
	if line+1 != rows[0].No {
		t.Fatalf("caret line = %d, want %d", line+1, rows[0].No)
	}
}

func TestPaletteSymbolEscape(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	enterEditAtFirstLine(t, app)

	if err := app.openSymbolPalette(ctx); err != nil {
		t.Fatal(err)
	}

	// While editing, Esc reaches the overlay through onKeyDown instead of
	// leaving edit mode.
	if err := app.onKeyDown(ctx, "escape"); err != nil {
		t.Fatal(err)
	}

	if app.paletteOpen {
		t.Fatal("escape did not close the symbol palette")
	}

	if !app.editing {
		t.Fatal("escape left edit mode instead of just closing the palette")
	}
}
