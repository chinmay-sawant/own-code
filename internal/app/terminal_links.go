package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// resolveTermLink maps a raw terminal path to the workspace path the opener
// wants plus the absolute filesystem path for existence checks. Relative
// paths resolve against the session cwd (the workspace root); absolute paths
// inside the root become workspace-relative so tabs stay consistent, while
// outside paths stay absolute for external-file opens.
func (a *App) resolveTermLink(raw string) (openPath, absPath string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if raw == "~" {
				raw = home
			} else {
				raw = filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(raw, "~/")))
			}
		}
	}
	cleanRaw := filepath.Clean(filepath.FromSlash(raw))
	if filepath.IsAbs(cleanRaw) {
		abs := cleanRaw
		if a.ws != nil && !a.ws.Sample && a.ws.Root != "" {
			if rel, err := filepath.Rel(a.ws.Root, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "." {
				return filepath.ToSlash(rel), abs
			}
		}
		return filepath.ToSlash(abs), abs
	}
	cwd := a.terminalDir()
	if cwd == "" && a.ws != nil {
		cwd = a.ws.Root
	}
	if cwd == "" {
		clean := filepath.ToSlash(cleanRaw)
		clean = strings.TrimPrefix(clean, "./")
		return clean, ""
	}
	abs := filepath.Clean(filepath.Join(cwd, cleanRaw))
	if a.ws != nil && !a.ws.Sample && a.ws.Root != "" {
		if rel, err := filepath.Rel(a.ws.Root, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "." {
			return filepath.ToSlash(rel), abs
		}
		return filepath.ToSlash(abs), abs
	}
	clean := filepath.ToSlash(cleanRaw)
	clean = strings.TrimPrefix(clean, "./")
	return clean, ""
}

// openTerminalLink opens the file behind visible row/segment as a preview
// with nav history, then jumps to the line via the shared goto path. Missing files toast instead of failing; dirty preview and merge
// guards ride along with the normal open path.
func (a *App) openTerminalLink(ctx context.Context, row, seg int) error {
	if row < 0 || seg < 0 || row >= len(a.view.Terminal.Rows) {
		return nil
	}
	segs := a.view.Terminal.Rows[row].Segments
	if seg >= len(segs) {
		return nil
	}
	target := segs[seg]
	if strings.TrimSpace(target.LinkPath) == "" {
		return nil
	}
	raw := strings.TrimSpace(target.LinkPath)
	line := target.LinkLine
	col := target.LinkCol
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	openPath, abs := a.resolveTermLink(raw)
	if openPath == "" {
		a.showToast("info", "File not found", raw+" is not on disk.")
		a.refresh()
		return nil
	}
	if abs != "" && !a.termLinkInsideWorkspace(abs) {
		a.showToast("info", "Link not opened", raw+" is outside the active workspace.")
		a.refresh()
		return nil
	}
	if abs != "" {
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			a.showToast("info", "File not found", raw+" is not on disk.")
			a.refresh()
			return nil
		}
	} else if a.ws != nil && a.ws.ByPath[openPath] == nil {
		// Sample or rootless workspace: let the opener attempt the load so
		// embedded files still resolve, but toast first when the path is
		// definitely unknown and no loader can provide it.
		if _, err := a.ws.ensureFile(openPath); err != nil {
			a.showToast("info", "File not found", raw+" is not on disk.")
			a.refresh()
			return nil
		}
	}
	a.openFileAtPreview(a.activeGroup(), openPath, true)
	if a.ws == nil || a.ws.ByPath[openPath] == nil {
		return nil
	}
	return a.gotoLine(ctx, line, col)
}

// termLinkInsideWorkspace reports whether abs resolves, after symlinks, to a
// regular file under the active workspace root. Terminal output is untrusted
// text, so a link may never open a file outside the project (absolute paths,
// "..", or a symlink that leaves the root).
func (a *App) termLinkInsideWorkspace(abs string) bool {
	if a.ws == nil || a.ws.Sample || strings.TrimSpace(a.ws.Root) == "" {
		return false
	}
	root, err := filepath.EvalSymlinks(a.ws.Root)
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(abs)
	if errors.Is(err, os.ErrNotExist) {
		// Missing: the caller reports "not on disk"; only a lexical escape
		// is refused here.
		rel, rerr := filepath.Rel(filepath.Clean(a.ws.Root), filepath.Clean(abs))
		return rerr == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	info, err := os.Stat(real)
	return err == nil && info.Mode().IsRegular()
}
