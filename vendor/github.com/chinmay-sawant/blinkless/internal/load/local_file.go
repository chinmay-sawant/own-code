package load

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chinmay-sawant/blinkless/internal/settings"
)

// openPinnedFile uses a directory handle to keep ACL-limited reads beneath
// their allowed root, including when a descendant becomes a symlink during
// the open. The configured roots themselves must remain operator-controlled.
func (l *Loader) openPinnedFile(filePath string, pageLoad settings.LoadPage) (*os.File, error) {
	if l.EnableLocalFileAccess && !pageLoad.BlockLocalFileAccess {
		file, err := os.OpenFile(filePath, localFileOpenFlags, 0)
		if err != nil {
			return nil, fmt.Errorf("open local file: %w", err)
		}

		return file, nil
	}

	candidate := resolvePath(filePath)

	for _, prefix := range l.Allow {
		if prefix == "" {
			continue
		}

		allowed := resolvePath(prefix)
		if pathWithinPrefix(candidate, allowed) {
			return openAllowedFile(allowed, candidate)
		}
	}

	return nil, ErrAccessDenied
}

func openAllowedFile(allowed, candidate string) (*os.File, error) {
	info, err := os.Stat(allowed)
	if err != nil {
		return nil, fmt.Errorf("stat allowed path: %w", err)
	}

	rootPath := allowed

	if !info.IsDir() {
		if candidate != allowed {
			return nil, ErrAccessDenied
		}

		rootPath = filepath.Dir(allowed)
	}

	root, err := openAllowedRoot(rootPath, info)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	relative, err := filepath.Rel(rootPath, candidate)
	if err != nil {
		return nil, ErrAccessDenied
	}

	file, err := root.OpenFile(relative, localFileOpenFlags, 0)
	if err != nil {
		return nil, ErrAccessDenied
	}

	if !info.IsDir() {
		openedInfo, statErr := file.Stat()
		if statErr != nil || !os.SameFile(info, openedInfo) {
			_ = file.Close()

			return nil, ErrAccessDenied
		}
	}

	return file, nil
}

func openAllowedRoot(path string, allowedInfo os.FileInfo) (*os.Root, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open allowed root: %w", err)
	}

	if allowedInfo.IsDir() {
		openedInfo, statErr := root.Stat(".")
		if statErr != nil || !os.SameFile(allowedInfo, openedInfo) {
			_ = root.Close()

			return nil, ErrAccessDenied
		}
	}

	return root, nil
}
