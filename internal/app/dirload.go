package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// dirChildCap caps one async directory expansion.
var dirChildCap = 2000

const slowDirLoadMillis = 500

// dirLoadRequest is one queued async directory expansion.
type dirLoadRequest struct {
	Token int
	Path  string
}

// dirLoadReply carries one finished directory read back to the page loop.
// Children are sorted (dirs first) and capped at dirChildCap; More counts
// the hidden remainder. Files are the new fileMetas for ByPath/Files.
type dirLoadReply struct {
	Token    int
	Path     string
	Children []*fileNode
	Files    []*fileMeta
	More     int
	Err      string
	Deleted  bool
	Millis   int64
}

// scanDirEntries reads one directory off the page loop. It mirrors the
// filters in loadNode (skipDir/skipExt/dotfile/symlink/depth/git-ignore) so
// the async path and the sync by-path path agree. It never touches the
// workspace; the page loop installs the result via insertNodeChildren.
func scanDirEntries(root, rel string, ignored map[string]bool, parentIgnored bool) ([]*fileNode, []*fileMeta, int, error) {
	started := time.Now()
	dir := filepath.Join(root, filepath.FromSlash(rel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, 0, err
	}
	_ = started

	var children []*fileNode
	var files []*fileMeta

	for _, entry := range entries {
		name := entry.Name()
		childRel := childRelPath(rel, name)

		if entry.IsDir() {
			if !parentIgnored && skipDir(name) {
				continue
			}
			if strings.Count(childRel, "/") >= maxTreeDepth {
				continue
			}
			// Never descend into symlinked directories.
			if info, infoErr := entry.Info(); infoErr == nil {
				if info.Mode()&os.ModeSymlink != 0 {
					continue
				}
			} else {
				continue
			}
			children = append(children, &fileNode{
				Path:    childRel,
				Name:    name,
				Dir:     true,
				Loaded:  false,
				Ignored: parentIgnored || pathIgnored(ignored, childRel),
			})
			continue
		}

		if strings.HasPrefix(name, ".") && name != ".gitignore" && name != ".env.example" {
			continue
		}
		if skipExt(name) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		meta := &fileMeta{
			Path: childRel,
			Name: name,
			Dir:  rel,
			Size: info.Size(),
			Lang: languageOf(childRel),
			Big:  info.Size() > maxFileBytes,
		}
		if parentIgnored || pathIgnored(ignored, childRel) {
			meta.Ignored = true
		}
		files = append(files, meta)
		children = append(children, &fileNode{
			Path:    childRel,
			Name:    name,
			Dir:     false,
			Loaded:  true,
			Ignored: meta.Ignored,
			Meta:    meta,
		})
	}

	sort.Slice(children, func(i, j int) bool {
		if children[i].Dir != children[j].Dir {
			return children[i].Dir
		}
		return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
	})

	more := 0
	if len(children) > dirChildCap {
		more = len(children) - dirChildCap
		children = children[:dirChildCap]
		// Drop file metas beyond the cap so Files stays in sync with rows.
		keep := map[string]bool{}
		for _, c := range children {
			keep[c.Path] = true
		}
		kept := files[:0]
		for _, m := range files {
			if keep[m.Path] {
				kept = append(kept, m)
			}
		}
		files = kept
	}

	// Sort new file metas for the batched merge in insertNodeChildren.
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].Path) < strings.ToLower(files[j].Path)
	})

	return children, files, more, nil
}

// startDirLoader launches the single background directory reader. It is
// started from NewWithLimit and switchProject and stopped by Close.
func (a *App) startDirLoader() {
	if a.dirLoadReq != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.dirLoadCtx = ctx
	a.dirLoadCancel = cancel
	a.dirLoads = map[string]int{}
	a.dirLoadReq = make(chan dirLoadRequest, 16)
	a.dirLoadResult = make(chan dirLoadReply, 16)
	root := ""
	ignored := map[string]bool{}
	if a.ws != nil {
		root = a.ws.Root
		ignored = a.ws.ignored
	}
	go dirLoadWorker(ctx, root, ignored, a.dirLoadReq, a.dirLoadResult, func() {
		if a.page != nil {
			a.page.WantFrameIn(0)
		}
	})
}

// stopDirLoader cancels the background reader and drops in-flight tokens.
func (a *App) stopDirLoader() {
	if a.dirLoadCancel != nil {
		a.dirLoadCancel()
		a.dirLoadCancel = nil
	}
	a.dirLoadReq = nil
	a.dirLoads = map[string]int{}
	// Drain stale replies so a project switch never installs old children.
	for {
		select {
		case <-a.dirLoadResult:
		default:
			return
		}
	}
}

// dirLoadWorker serially reads directories off the page loop. Every unit
// carries its token; the page loop drops stale replies.
func dirLoadWorker(ctx context.Context, root string, ignored map[string]bool, reqs <-chan dirLoadRequest, replies chan dirLoadReply, wake func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-reqs:
			started := time.Now()
			// Find parentIgnored from the ignored map walk; the worker does
			// not touch the tree, so ignored-dir descent uses the map only.
			parentIgnored := pathIgnored(ignored, req.Path)
			children, files, more, err := scanDirEntries(root, req.Path, ignored, parentIgnored)
			millis := time.Since(started).Milliseconds()
			reply := dirLoadReply{Token: req.Token, Path: req.Path, Children: children, Files: files, More: more, Millis: millis}
			if err != nil {
				if os.IsNotExist(err) {
					reply.Deleted = true
					reply.Err = "deleted"
				} else if os.IsPermission(err) {
					reply.Err = "Cannot read folder (permission denied)"
				} else {
					msg := strings.TrimSpace(err.Error())
					if len(msg) > 120 {
						msg = msg[:120] + "…"
					}
					reply.Err = msg
				}
			}
			select {
			case replies <- reply:
			default:
				select {
				case <-replies:
				default:
				}
				select {
				case replies <- reply:
				default:
				}
			}
			if wake != nil {
				wake()
			}
		}
	}
}

// requestDirLoad enqueues one async expansion and marks the node Loading.
// It is the only render-path way to load a folder; sync LoadDir/loadNode
// stay for by-path opens and tests.
func (a *App) requestDirLoad(path string) {
	if a.ws == nil || a.ws.Sample || path == "" {
		return
	}
	node := findTreeNode(a.ws.Tree, path)
	if node == nil || !node.Dir {
		return
	}
	if node.Loaded && node.LoadErr == "" {
		return
	}
	if a.dirLoads == nil {
		a.dirLoads = map[string]int{}
	}
	if _, inFlight := a.dirLoads[path]; inFlight {
		return
	}
	// No background worker (unit tests without New): fall back to the sync
	// loader so by-path coverage stays deterministic.
	if a.dirLoadReq == nil {
		_ = a.ws.loadDir(path)
		if n := findTreeNode(a.ws.Tree, path); n != nil {
			n.Loading = false
		}
		a.refresh()
		return
	}
	a.dirLoadSeq++
	token := a.dirLoadSeq
	a.dirLoads[path] = token
	node.Loading = true
	node.LoadErr = ""
	// Collapse during load drops the token (see onToggleDir); the late
	// reply is discarded in receiveDirLoads.
	select {
	case a.dirLoadReq <- dirLoadRequest{Token: token, Path: path}:
	default:
		// Queue full: drop the mark so a retry can re-enqueue.
		delete(a.dirLoads, path)
		node.Loading = false
		node.LoadErr = "Folder load busy, retry."
		a.refresh()
	}
}

// receiveDirLoads installs finished directory reads on the page loop. It
// token-matches, drops stale replies from collapsed folders, and refreshes.
func (a *App) receiveDirLoads(ctx context.Context) bool {
	changed := false
	for {
		select {
		case reply := <-a.dirLoadResult:
			token, ok := a.dirLoads[reply.Path]
			if !ok || token != reply.Token {
				continue
			}
			delete(a.dirLoads, reply.Path)
			a.insertNodeChildren(reply)
			if reply.Millis > slowDirLoadMillis {
				a.dirLoadNote = fmt.Sprintf("Slow folder load %s: %dms", reply.Path, reply.Millis)
			}
			changed = true
		default:
			if changed {
				a.refresh()
				if a.page != nil {
					_ = a.page.Redraw(ctx)
				}
			}
			return changed
		}
	}
}

// insertNodeChildren installs one worker reply: children-only sort is
// already done by the worker, Files merges without a global re-sort, the
// dirChildCap remainder becomes a "more…" row, and the new dir gets a
// best-effort watcher handle off the page loop.
func (a *App) insertNodeChildren(reply dirLoadReply) {
	if a.ws == nil {
		return
	}
	if reply.Deleted {
		a.pruneDeletedDir(reply.Path)
		return
	}
	node := findTreeNode(a.ws.Tree, reply.Path)
	if node == nil || !node.Dir {
		return
	}
	node.Loading = false
	if reply.Err != "" {
		node.LoadErr = reply.Err
		// Errors still mark Loaded so an empty/errored folder never spins.
		node.Loaded = true
		return
	}
	node.LoadErr = ""
	// Prune deleted files under this dir: drop Files/ByPath entries that were
	// in old children but are gone now.
	oldPaths := map[string]bool{}
	for _, c := range node.Children {
		if !c.Dir {
			oldPaths[c.Path] = true
		}
	}
	newPaths := map[string]bool{}
	for _, c := range reply.Children {
		newPaths[c.Path] = true
	}
	for path := range oldPaths {
		if !newPaths[path] {
			delete(a.ws.ByPath, path)
		}
	}
	if len(oldPaths) > 0 {
		kept := a.ws.Files[:0]
		for _, m := range a.ws.Files {
			if oldPaths[m.Path] && !newPaths[m.Path] {
				continue
			}
			kept = append(kept, m)
		}
		a.ws.Files = kept
	}
	node.Children = reply.Children
	node.More = reply.More
	// Children arrive sorted; keep the invariant without recursing.
	// (Worker sorts; no global re-sort here.)
	node.Loaded = true
	if len(reply.Files) > 0 {
		if a.ws.ByPath == nil {
			a.ws.ByPath = map[string]*fileMeta{}
		}
		for _, m := range reply.Files {
			if a.ws.ByPath[m.Path] == nil {
				a.ws.ByPath[m.Path] = m
			} else {
				// Keep the existing pointer but refresh ignored/size bits.
				existing := a.ws.ByPath[m.Path]
				existing.Ignored = m.Ignored
				existing.Size = m.Size
				existing.Big = m.Big
				// Re-link child metas to the canonical entry.
				for _, c := range node.Children {
					if !c.Dir && c.Path == m.Path {
						c.Meta = existing
						c.Ignored = existing.Ignored
					}
				}
			}
		}
		a.mergeFilesBatched(reply.Files)
	}
	a.watchDirLoaded(reply.Path)
}

// mergeFilesBatched appends one sorted batch into the sorted Files slice via
// a linear merge, avoiding the O(E·N log N) global re-sort per expand that
// loadNode used to pay.
func (a *App) mergeFilesBatched(batch []*fileMeta) {
	if len(batch) == 0 {
		return
	}
	seen := map[string]bool{}
	for _, m := range a.ws.Files {
		seen[m.Path] = true
	}
	var fresh []*fileMeta
	for _, m := range batch {
		if !seen[m.Path] {
			fresh = append(fresh, m)
			seen[m.Path] = true
		}
	}
	if len(fresh) == 0 {
		return
	}
	// Batch arrives sorted; merge with the sorted base.
	merged := make([]*fileMeta, 0, len(a.ws.Files)+len(fresh))
	i, j := 0, 0
	for i < len(a.ws.Files) && j < len(fresh) {
		if strings.ToLower(a.ws.Files[i].Path) < strings.ToLower(fresh[j].Path) {
			merged = append(merged, a.ws.Files[i])
			i++
		} else {
			merged = append(merged, fresh[j])
			j++
		}
	}
	merged = append(merged, a.ws.Files[i:]...)
	merged = append(merged, fresh[j:]...)
	a.ws.Files = merged
}

// pruneDeletedDir drops a directory that vanished mid-load and prunes the
// fold state and file index under it.
func (a *App) pruneDeletedDir(path string) {
	delete(a.folders, path)
	for folder := range a.folders {
		if folder == path || strings.HasPrefix(folder, path+"/") {
			delete(a.folders, folder)
		}
	}
	if a.ws == nil {
		return
	}
	// Remove the node from its parent.
	if dirName(path) == "" {
		kept := a.ws.Tree[:0]
		for _, n := range a.ws.Tree {
			if n.Path != path {
				kept = append(kept, n)
			}
		}
		a.ws.Tree = kept
	} else if parent := findTreeNode(a.ws.Tree, dirName(path)); parent != nil {
		kept := parent.Children[:0]
		for _, n := range parent.Children {
			if n.Path != path {
				kept = append(kept, n)
			}
		}
		parent.Children = kept
	}
	// Prune files under the deleted dir.
	kept := a.ws.Files[:0]
	for _, m := range a.ws.Files {
		if m.Path != path && !strings.HasPrefix(m.Path, path+"/") {
			kept = append(kept, m)
		}
	}
	a.ws.Files = kept
	for p := range a.ws.ByPath {
		if p == path || strings.HasPrefix(p, path+"/") {
			delete(a.ws.ByPath, p)
		}
	}
}

// watchDirLoaded adds a best-effort watcher handle for a newly loaded dir.
// It runs off the page loop in a background goroutine with a recover, using
// the same drain-before-Add discipline as the Create branch in watchLoop, so
// the Windows backend never deadlocks on a full Events queue.
func (a *App) watchDirLoaded(path string) {
	if a.watch == nil || a.ws == nil || a.ws.Sample {
		return
	}
	abs := filepath.Join(a.ws.Root, filepath.FromSlash(path))
	w := a.watch
	go func() {
		defer func() {
			_ = recover()
		}()
		// Drain-before-Add: keep the reader moving while Add waits.
		select {
		case <-w.Events:
		default:
		}
		_ = w.Add(abs)
		_ = w.Add(filepath.Join(a.ws.Root, filepath.FromSlash(path)))
	}()
}

// refreshRoot re-reads the root directory to discover new top-level entries
// without a full walk. It preserves expand state and loaded subtrees.
func (a *App) refreshRoot() {
	if a.ws == nil || a.ws.Sample || a.ws.Root == "" {
		return
	}
	entries, err := os.ReadDir(a.ws.Root)
	if err != nil {
		return
	}
	existing := map[string]*fileNode{}
	for _, n := range a.ws.Tree {
		existing[n.Path] = n
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			if skipDir(name) {
				continue
			}
			if existing[name] != nil {
				continue
			}
			// New top-level dir: unloaded node, ignored bit from map.
			a.ws.Tree = append(a.ws.Tree, &fileNode{
				Path:    name,
				Name:    name,
				Dir:     true,
				Loaded:  false,
				Ignored: pathIgnored(a.ws.ignored, name),
			})
		} else {
			if len(name) > 0 && name[0] == '.' && name != ".gitignore" && name != ".env.example" {
				continue
			}
			if skipExt(name) {
				continue
			}
			if existing[name] != nil {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if a.ws.ByPath[name] == nil {
				a.ws.addFile(name, name, info.Size())
			}
			meta := a.ws.ByPath[name]
			if pathIgnored(a.ws.ignored, name) {
				meta.Ignored = true
			}
			a.ws.Tree = append(a.ws.Tree, &fileNode{
				Path:    name,
				Name:    name,
				Dir:     false,
				Loaded:  true,
				Ignored: pathIgnored(a.ws.ignored, name),
				Meta:    meta,
			})
		}
	}
	sortTree(a.ws.Tree)
	// Re-merge root files to keep Files sorted without a global re-sort?
	// Root files are few; use batched merge for consistency.
}

// ensureOpenFoldersLoaded is now a depth-sorted enqueue scheduler. It never
// touches disk; the worker loads and receiveDirLoads installs.
func (a *App) scheduleOpenFoldersLoaded() {
	if a.ws == nil || a.ws.Sample || !a.ws.Lazy {
		return
	}
	var paths []string
	for path, open := range a.folders {
		if !open {
			continue
		}
		node := findTreeNode(a.ws.Tree, path)
		if node != nil && node.Dir && !node.Loaded && !node.Loading && node.LoadErr == "" {
			paths = append(paths, path)
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		di := strings.Count(paths[i], "/")
		dj := strings.Count(paths[j], "/")
		if di != dj {
			return di < dj
		}
		return paths[i] < paths[j]
	})
	for _, p := range paths {
		a.requestDirLoad(p)
	}
}
