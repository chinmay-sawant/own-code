package app

import (
	"strings"
)

// mruRows returns the most-recently-used files that still exist in the
// workspace, newest first, capped at limit. The welcome panel calls it with
// maxRecent; the Ctrl+E / Ctrl+Tab switcher calls it with mruMax. The limit
// never exceeds mruMax and never changes maxRecent globally.
func (a *App) mruRows(limit int) []recentRow {
	if limit <= 0 {
		limit = maxRecent
	}

	if limit > mruMax {
		limit = mruMax
	}

	rows := make([]recentRow, 0, limit)

	for _, path := range a.recent {
		if a.ws == nil || a.ws.ByPath[path] == nil {
			continue
		}

		lang := languageOf(path)
		rows = append(rows, recentRow{
			Path:  path,
			Name:  baseName(path),
			Dir:   dirName(path),
			Lang:  lang,
			Badge: languageInfo(lang).Badge,
		})

		if len(rows) >= limit {
			break
		}
	}

	return rows
}

// mruFilterRows filters the MRU list as the user types, with the same
// prefix-then-contains scoring the file palette uses.
func (a *App) mruFilterRows(query string) []recentRow {
	pool := a.mruRows(mruMax)

	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		if len(pool) > paletteMaxRows {
			pool = pool[:paletteMaxRows]
		}

		return pool
	}

	var prefix, contains []recentRow

	for _, row := range pool {
		p := strings.ToLower(row.Path)
		name := strings.ToLower(row.Name)

		switch {
		case strings.HasPrefix(p, lower) || strings.HasPrefix(name, lower):
			prefix = append(prefix, row)
		case strings.Contains(p, lower):
			contains = append(contains, row)
		}
	}

	out := make([]recentRow, 0, paletteMaxRows)
	out = append(out, prefix...)
	out = append(out, contains...)

	if len(out) > paletteMaxRows {
		out = out[:paletteMaxRows]
	}

	return out
}

// mruToggleLastTwo opens the previously used file: the second MRU entry.
// The framework is keydown-only, so a bare Ctrl+Tab cannot detect the key
// release VS Code uses for tap-to-toggle. Instead the first Ctrl+Tab press
// opens the recent palette preselected on that entry (Enter toggles), and
// repeated presses cycle; this helper is the no-UI equivalent.
func (a *App) mruToggleLastTwo() bool {
	rows := a.mruRows(mruMax)
	if len(rows) < 2 {
		return false
	}

	a.openFile(rows[1].Path)

	return true
}
