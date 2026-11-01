package app

import (
	"context"
	"path/filepath"
	"strings"
	"time"
)

const (
	paletteDefaultRows = 5
	paletteMaxRows     = 8
	// paletteDiskSearchTimeout bounds the on-disk fallback search. The
	// in-memory matches already paint, so disk only fills the remainder.
	paletteDiskSearchTimeout = 750 * time.Millisecond
)

// paletteView is the quick-open overlay data.
type paletteView struct {
	Open bool
	Rows []paletteRow
	// Mode is the overlay mode (files, symbols, recent, goto). Note scopes
	// the results for modes that filter beyond filenames. Busy marks an
	// in-flight disk fallback.
	Mode string
	Busy bool
	Note string
}

// paletteRow is one file suggestion. Cmd names a `>` command-mode row
// (one of the paletteCmd* ids); file rows leave it empty. Symbol rows set
// Symbol and carry the definition Kind, the 0-based Line, and the 1-based
// No the symbol-open:path:line action round-trips through.
type paletteRow struct {
	Path   string
	Name   string
	Dir    string
	Lang   string
	Badge  string
	Active bool
	Cmd    string
	Symbol bool
	Kind   string
	Line   int
	No     int
}

// paletteRows returns the open tabs followed by workspace files for an empty
// query, or path matches for a query. An absolute path resolves to that file,
// even when it lives outside the open folder. A query starting with `>`
// returns command rows instead of files.
func (a *App) paletteRows() []paletteRow {
	// Symbol and goto modes own the overlay: symbol rows filter the active
	// file's outline, goto has no rows (Enter parses the query).
	switch a.paletteMode {
	case paletteModeSymbols:
		return a.symbolPaletteRows()
	case paletteModeGoto:
		return nil
	}

	if a.isPaletteCommandMode() {
		return a.paletteCommandRows()
	}

	if row, ok := a.absolutePaletteRow(a.paletteQuery); ok {
		return []paletteRow{row}
	}

	query := strings.ToLower(strings.TrimSpace(a.paletteQuery))

	if query == "" {
		a.paletteBusy = false
		a.paletteNote = ""
		rows := make([]paletteRow, 0, paletteDefaultRows)
		seen := map[string]bool{}

		for _, path := range a.tabs {
			if seen[path] || len(rows) >= paletteDefaultRows {
				continue
			}

			if file := a.ws.ByPath[path]; file != nil {
				rows = append(rows, paletteRowOf(file))
				seen[path] = true
			}
		}

		if a.split {
			for _, path := range a.tabs2 {
				if seen[path] || len(rows) >= paletteDefaultRows {
					continue
				}

				if file := a.ws.ByPath[path]; file != nil {
					rows = append(rows, paletteRowOf(file))
					seen[path] = true
				}
			}
		}

		for _, fresh := range a.recentRows() {
			if len(rows) >= paletteDefaultRows {
				break
			}

			if seen[fresh.Path] {
				continue
			}

			if file := a.ws.ByPath[fresh.Path]; file != nil {
				rows = append(rows, paletteRowOf(file))
				seen[fresh.Path] = true
			}
		}

		if a.ws != nil {
			for _, file := range a.ws.Files {
				if len(rows) >= paletteDefaultRows {
					break
				}

				if seen[file.Path] {
					continue
				}

				rows = append(rows, paletteRowOf(file))
			}
		}

		return rows
	}

	seen := map[string]bool{}
	var inMemCandidates []*fileMeta

	for _, path := range a.tabs {
		if seen[path] {
			continue
		}
		if file := a.ws.ByPath[path]; file != nil {
			seen[path] = true
			inMemCandidates = append(inMemCandidates, file)
		}
	}

	if a.split {
		for _, path := range a.tabs2 {
			if seen[path] {
				continue
			}
			if file := a.ws.ByPath[path]; file != nil {
				seen[path] = true
				inMemCandidates = append(inMemCandidates, file)
			}
		}
	}

	for _, fresh := range a.recentRows() {
		if seen[fresh.Path] {
			continue
		}
		if file := a.ws.ByPath[fresh.Path]; file != nil {
			seen[fresh.Path] = true
			inMemCandidates = append(inMemCandidates, file)
		}
	}

	if a.ws != nil {
		for _, file := range a.ws.Files {
			if seen[file.Path] {
				continue
			}
			seen[file.Path] = true
			inMemCandidates = append(inMemCandidates, file)
		}
	}

	var inMemPrefix []paletteRow
	var inMemContains []paletteRow

	for _, file := range inMemCandidates {
		p := strings.ToLower(file.Path)
		name := strings.ToLower(file.Name)

		switch {
		case strings.HasPrefix(p, query) || strings.HasPrefix(name, query):
			inMemPrefix = append(inMemPrefix, paletteRowOf(file))
		case strings.Contains(p, query):
			inMemContains = append(inMemContains, paletteRowOf(file))
		}
	}

	rows := make([]paletteRow, 0, paletteMaxRows)
	for _, row := range inMemPrefix {
		if len(rows) >= paletteMaxRows {
			break
		}
		rows = append(rows, row)
	}
	for _, row := range inMemContains {
		if len(rows) >= paletteMaxRows {
			break
		}
		rows = append(rows, row)
	}

	if len(rows) < paletteMaxRows && a.ws != nil && (diskFileSearcher != nil || (!a.ws.Sample && a.ws.Root != "")) {
		a.paletteBusy = true
		a.paletteNote = "Searching disk…"
		ctx, cancel := context.WithTimeout(context.Background(), paletteDiskSearchTimeout)
		defer cancel()

		diskPaths, err := diskSearchFiles(ctx, a.ws.Root)
		a.paletteBusy = false
		if err == nil && len(diskPaths) > 0 {
			var diskPrefix []paletteRow
			var diskContains []paletteRow

			for _, p := range diskPaths {
				p = filepath.ToSlash(p)
				if seen[p] || skipExt(p) {
					continue
				}
				seen[p] = true

				lowerPath := strings.ToLower(p)
				lowerName := lowerPath
				if idx := strings.LastIndexByte(lowerPath, '/'); idx >= 0 {
					lowerName = lowerPath[idx+1:]
				}

				switch {
				case strings.HasPrefix(lowerPath, query) || strings.HasPrefix(lowerName, query):
					diskPrefix = append(diskPrefix, makePaletteRow(p))
				case strings.Contains(lowerPath, query):
					diskContains = append(diskContains, makePaletteRow(p))
				}
			}

			for _, row := range diskPrefix {
				if len(rows) >= paletteMaxRows {
					break
				}
				rows = append(rows, row)
			}
			for _, row := range diskContains {
				if len(rows) >= paletteMaxRows {
					break
				}
				rows = append(rows, row)
			}
			if a.ws != nil && a.ws.Lazy {
				a.paletteNote = "Disk results include unexpanded folders."
			} else {
				a.paletteNote = ""
			}
		} else {
			a.paletteNote = ""
		}
	} else if a.ws != nil && a.ws.Lazy && query != "" {
		a.paletteNote = "Covered seen files only; expand folders for full-repo."
	} else {
		a.paletteNote = ""
	}

	return rows
}

// paletteRowOf builds one suggestion.
func paletteRowOf(file *fileMeta) paletteRow {
	return paletteRow{
		Path:  file.Path,
		Name:  file.Name,
		Dir:   file.Dir,
		Lang:  file.Lang,
		Badge: languageInfo(file.Lang).Badge,
	}
}

// paletteView marks the selected row.
func (a *App) paletteView() paletteView {
	out := paletteView{Open: a.paletteOpen, Mode: a.paletteMode, Busy: a.paletteBusy, Note: a.paletteNote}
	if !a.paletteOpen {
		return out
	}

	rows := a.paletteRows()
	out.Busy = a.paletteBusy
	out.Note = a.paletteNote

	switch {
	case len(rows) == 0:
		a.paletteIndex = 0
	case a.paletteIndex >= len(rows):
		a.paletteIndex = len(rows) - 1
	case a.paletteIndex < 0:
		a.paletteIndex = 0
	}

	for i := range rows {
		rows[i].Active = i == a.paletteIndex
	}

	out.Rows = rows

	return out
}

// openPalette shows the overlay and focuses its input.
func (a *App) openPalette(ctx context.Context) error {
	a.paletteOpen = true
	a.paletteMode = paletteModeFiles
	a.paletteQuery = ""
	a.paletteIndex = 0
	a.refresh()

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	return a.page.Form().Focus(ctx, "palette")
}

// closePalette hides the overlay.
func (a *App) closePalette(ctx context.Context) error {
	a.paletteOpen = false
	a.paletteMode = paletteModeFiles
	a.paletteQuery = ""
	a.symbolQuery = ""
	a.symbolScope = ""
	a.paletteIndex = 0
	a.refresh()

	return a.page.Form().Focus(ctx, "")
}

// movePalette moves the selected suggestion.
func (a *App) movePalette(delta int) {
	rows := a.paletteRows()
	if len(rows) == 0 {
		return
	}

	a.paletteIndex += delta
	if a.paletteIndex < 0 {
		a.paletteIndex = 0
	}

	if a.paletteIndex >= len(rows) {
		a.paletteIndex = len(rows) - 1
	}

	a.refresh()
}
