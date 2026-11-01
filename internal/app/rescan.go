package app

import (
	"path/filepath"
	"sort"
	"strings"
)

// workspaceDelta is what a full rescan found: paths that appeared, disappeared,
// or kept their path but changed size or language. Same counts the files that
// did not move.
type workspaceDelta struct {
	Added   []string
	Removed []string
	Changed []string
	Same    int
}

// diffWorkspace compares two scans by path. A file counts as changed when its
// size or language moved; contents are not read, so the check stays cheap.
func diffWorkspace(old, next *workspace) workspaceDelta {
	var delta workspaceDelta

	oldFiles := workspaceFilesByPath(old)
	nextFiles := workspaceFilesByPath(next)

	for path, meta := range nextFiles {
		prev, ok := oldFiles[path]
		switch {
		case !ok:
			delta.Added = append(delta.Added, path)
		case prev.Size != meta.Size || prev.Lang != meta.Lang:
			delta.Changed = append(delta.Changed, path)
		default:
			delta.Same++
		}
	}

	for path := range oldFiles {
		if _, ok := nextFiles[path]; !ok {
			delta.Removed = append(delta.Removed, path)
		}
	}

	sort.Strings(delta.Added)
	sort.Strings(delta.Removed)
	sort.Strings(delta.Changed)

	return delta
}

// workspaceFilesByPath returns the scan's file map, rebuilding it from Files
// when a caller handed over a workspace without one. A nil workspace is empty.
func workspaceFilesByPath(w *workspace) map[string]*fileMeta {
	files := map[string]*fileMeta{}

	if w == nil {
		return files
	}

	if len(w.ByPath) > 0 {
		for path, meta := range w.ByPath {
			if meta != nil {
				files[path] = meta
			}
		}

		return files
	}

	for _, meta := range w.Files {
		if meta != nil {
			files[meta.Path] = meta
		}
	}

	return files
}

// rescanDiskWorkspace scans root again and reports how it moved from old. A
// limit of zero or less means MaxWorkspaceFiles. The previous workspace's
// scan filter carries over so excludes survive rescans. Lazy workspaces use
// the incremental mode (re-ReadDir root + each Loaded dir) so a watcher burst
// never pays a full walk; full walks increment fullWalkCounter.
func rescanDiskWorkspace(root string, old *workspace, limit int) (*workspace, workspaceDelta, error) {
	if old != nil && old.Lazy && (limit <= 0 || limit == MaxWorkspaceFiles) {
		return rescanLazyWorkspace(root, old)
	}
	var filter *scanExcludes
	if old != nil {
		filter = old.scanFilter
	}
	return rescanDiskWorkspaceExcludes(root, old, limit, filter)
}

// rescanDiskWorkspaceExcludes rescans with an explicit custom-excludes
// filter, replacing the carried-over one. A nil filter drops excludes.
func rescanDiskWorkspaceExcludes(root string, old *workspace, limit int, filter *scanExcludes) (*workspace, workspaceDelta, error) {
	noteFullWalk()
	next, err := loadDiskWorkspaceWithLimitExcludes(root, limit, filter)
	if err != nil {
		return nil, workspaceDelta{}, err
	}
	if old != nil && old.Lazy {
		next.Lazy = true
		next.Truncated = false
		next.TruncatedNote = ""
	}

	return next, diffWorkspace(old, next), nil
}

// rescanLazyWorkspace re-reads root and each Loaded dir, preserving folders
// and Loaded bits. Collapsed dirs stay unloaded; new files under loaded dirs
// appear; deleted dirs prune. No full walk runs.
func rescanLazyWorkspace(root string, old *workspace) (*workspace, workspaceDelta, error) {
	if old == nil {
		noteFullWalk()
		next, err := loadDiskWorkspaceLazy(root)
		if err != nil {
			return nil, workspaceDelta{}, err
		}
		return next, diffWorkspace(nil, next), nil
	}
	// Snapshot expand state and loaded paths.
	abs, err := filepathAbs(root)
	if err != nil {
		return nil, workspaceDelta{}, err
	}
	_ = abs
	next, err := loadDiskWorkspaceLazy(root)
	if err != nil {
		return nil, workspaceDelta{}, err
	}
	// Preserve folders; re-load each previously Loaded dir depth-first.
	next.Lazy = true
	// Carry over the old expand set; installRescan keeps folders from App.
	// Reload loaded dirs in depth order so ancestors exist first.
	var loaded []string
	var collect func([]*fileNode)
	collect = func(nodes []*fileNode) {
		for _, n := range nodes {
			if n.Dir && n.Loaded {
				loaded = append(loaded, n.Path)
			}
			collect(n.Children)
		}
	}
	collect(old.Tree)
	// Sort shallow-first.
	sort.Slice(loaded, func(i, j int) bool {
		di := strings.Count(loaded[i], "/")
		dj := strings.Count(loaded[j], "/")
		if di != dj {
			return di < dj
		}
		return loaded[i] < loaded[j]
	})
	for _, path := range loaded {
		// Ensure ancestors exist in next (loadDir creates them), then load.
		_ = next.loadDir(path)
	}
	next.Truncated = false
	next.TruncatedNote = ""
	return next, diffWorkspace(old, next), nil
}

func filepathAbs(root string) (string, error) {
	return filepath.Abs(root)
}

// isGitMetadataChange reports whether path is a git ref, index, or log file
// that can move the branch name or working-tree diff. Object churn under
// .git/objects is not a branch change and stays out.
func isGitMetadataChange(path string) bool {
	slash := "/" + strings.TrimPrefix(filepath.ToSlash(path), "/")

	const marker = "/.git/"

	i := strings.Index(slash, marker)
	if i < 0 {
		return false
	}

	rest := slash[i+len(marker):]

	switch {
	case rest == "HEAD", rest == "packed-refs", rest == "index":
		return true
	case strings.HasPrefix(rest, "refs/"), strings.HasPrefix(rest, "logs/"):
		return true
	}

	return false
}
