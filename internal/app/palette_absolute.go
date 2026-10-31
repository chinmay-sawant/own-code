package app

import (
	"os"
	"path/filepath"
	"strings"
)

// absolutePaletteRow resolves a typed absolute path to one palette row. The
// path may carry the quotes a file manager adds. It returns false unless the
// path is absolute, exists, and names a regular file the editor can display.
func (a *App) absolutePaletteRow(raw string) (paletteRow, bool) {
	query := strings.Trim(strings.TrimSpace(raw), `"'`)
	if query == "" {
		return paletteRow{}, false
	}

	clean := filepath.Clean(filepath.FromSlash(query))
	if !filepath.IsAbs(clean) {
		return paletteRow{}, false
	}

	info, err := os.Stat(clean)
	if err != nil || !info.Mode().IsRegular() || skipExt(clean) {
		return paletteRow{}, false
	}

	path := filepath.ToSlash(clean)
	if rel, ok := a.workspaceRelativePath(clean); ok {
		path = rel
	}

	return makePaletteRow(path), true
}

// workspaceRelativePath maps an absolute path under the open root back to its
// workspace-relative form, so the file keeps its tree, status, and change state.
func (a *App) workspaceRelativePath(abs string) (string, bool) {
	if a.ws == nil || a.ws.Sample || a.ws.Root == "" {
		return "", false
	}

	root, err := filepath.Abs(a.ws.Root)
	if err != nil {
		return "", false
	}

	rel, err := filepath.Rel(root, filepath.Clean(abs))
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	return filepath.ToSlash(rel), true
}
