package app

import (
	"context"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// symbolRows returns the active file's full (uncapped) outline filtered by
// query, prefix matches before contains matches, capped at paletteMaxRows
// for display. The drawer keeps the 60-row cap; the palette filters the
// full list. An empty query lists the head of the outline.
func (a *App) symbolRows(query string) []symbolRow {
	doc := a.activeDoc()
	if doc == nil || doc.Err != "" || doc.Binary {
		return nil
	}

	var lines []string

	if a.editing && a.edit != nil && a.edit.Path == doc.Path {
		sel := edit.SelectionLines(a.edit.Text())
		lines = make([]string, len(sel))

		for i, l := range sel {
			lines[i] = l.Text
		}
	} else {
		lines = doc.Lines
	}

	full := outlineFull(doc.Lang, lines)
	q := strings.ToLower(strings.TrimSpace(query))

	if q == "" {
		if len(full) > paletteMaxRows {
			full = full[:paletteMaxRows]
		}

		return full
	}

	var prefix, contains []symbolRow

	for _, sym := range full {
		name := strings.ToLower(sym.Name)

		switch {
		case strings.HasPrefix(name, q):
			prefix = append(prefix, sym)
		case strings.Contains(name, q):
			contains = append(contains, sym)
		}
	}

	out := make([]symbolRow, 0, paletteMaxRows)
	for _, sym := range prefix {
		if len(out) >= paletteMaxRows {
			return out
		}

		out = append(out, sym)
	}

	for _, sym := range contains {
		if len(out) >= paletteMaxRows {
			return out
		}

		out = append(out, sym)
	}

	return out
}

// symbolPaletteRows renders symbolRows as palette rows for the shared
// overlay. No is 1-based so the sibling symbol-open:path:line action (and
// its template) round-trips through cutSymbolAction.
func (a *App) symbolPaletteRows() []paletteRow {
	syms := a.symbolRows(a.paletteQuery)
	rows := make([]paletteRow, 0, len(syms))

	for _, sym := range syms {
		rows = append(rows, symbolPaletteRow(a, sym))
	}

	return rows
}

// symbolPaletteRow builds one symbol suggestion.
func symbolPaletteRow(a *App, sym symbolRow) paletteRow {
	lang := ""
	path := ""

	if doc := a.activeDoc(); doc != nil {
		lang = doc.Lang
		path = doc.Path
	}

	if lang == "" {
		lang = languageOf(path)
	}

	return paletteRow{
		Path:   path,
		Name:   sym.Name,
		Dir:    dirName(path),
		Lang:   lang,
		Badge:  languageInfo(lang).Badge,
		Symbol: true,
		Kind:   sym.Kind,
		Line:   sym.Line,
		No:     sym.No,
	}
}

// openSymbolPalette shows the palette overlay in file-scoped symbol mode:
// the active file's outline, filtered as you type. Enter jumps to the
// selected symbol and moves the caret; Esc closes.
func (a *App) openSymbolPalette(ctx context.Context) error {
	if a.settingsOpen || a.confirmOpen || a.terminalOwnsKeys() {
		return nil
	}

	doc := a.activeDoc()
	if doc == nil || doc.Err != "" || doc.Binary {
		a.showToast("info", "Go to symbol", "Open a text file before jumping to a symbol.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	// A repeat chord keeps the typed query; a fresh open starts empty.
	fresh := !a.paletteOpen || a.paletteMode != paletteModeSymbols

	a.paletteOpen = true
	a.paletteMode = paletteModeSymbols
	a.symbolScope = doc.Path
	a.paletteIndex = 0

	if fresh {
		a.paletteQuery = ""
		a.symbolQuery = ""
	}

	a.refresh()

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	return a.page.Form().Focus(ctx, "palette")
}

// submitSymbol jumps to the selected symbol row: scroll plus caret move,
// exiting preview like gotoLine.
func (a *App) submitSymbol(ctx context.Context) error {
	rows := a.symbolRows(a.paletteQuery)
	if len(rows) == 0 {
		return nil
	}

	index := a.paletteIndex
	if index < 0 {
		index = 0
	}

	if index >= len(rows) {
		index = len(rows) - 1
	}

	if err := a.closePalette(ctx); err != nil {
		return err
	}

	return a.gotoLine(ctx, rows[index].No, 1)
}
