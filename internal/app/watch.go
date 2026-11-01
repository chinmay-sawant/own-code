package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	watchDebounce   = 150 * time.Millisecond
	maxWatchDirs    = 2000
	rescanNoteParts = 3
	gitPollInterval = 2 * time.Second
	// watchEventBuffer sizes the userspace event queue. The Windows backend
	// deadlocks when its reader blocks sending to a full Events channel while
	// the main goroutine waits for an Add reply, so keep this large and keep a
	// reader draining Events before any Add runs.
	watchEventBuffer = 4096
)

// watchUpdate is one debounced background result. Workspace is non-nil after a
// rescan; Git marks a change that may have moved a ref or the index. Err
// carries the first background failure since the previous update so the UI
// can degrade to a status note instead of crashing or deadlocking. Dirs
// carries changed directories for targeted lazy refresh (no full walk).
type watchUpdate struct {
	Changes   changeSet
	Workspace *workspace
	Delta     workspaceDelta
	Git       bool
	Branch    string
	Branches  []branchRow
	Dirs      []string
	Err       *AppError
}

// fullWalkCounter counts full-tree walks after startup for the perf gate.
var fullWalkCounter int

func noteFullWalk() { fullWalkCounter++ }

// StartWatcher watches the live workspace and wakes the UI when files or git
// metadata change. It is desktop only; Serve does not start it.
//
// All failures are returned as *AppError (KindWatch) so callers can show
// UserMessage() in the status bar and keep running without live updates
// instead of hitting the Windows backend deadlock
// ("all goroutines are asleep - deadlock!").
func (a *App) StartWatcher(ctx context.Context) error {
	if a.ws.Sample || a.watch != nil {
		return nil
	}

	root := ""
	if a.ws != nil {
		root = a.ws.Root
	}

	// Use a large userspace queue so a burst of filesystem activity during
	// setup does not fill the queue before the loop drains it.
	watcher, err := fsnotify.NewBufferedWatcher(watchEventBuffer)
	if err != nil {
		return NewWatchError(WatchOpStartup, root, err)
	}

	// The fsnotify Windows backend serves Add from its reader goroutine and
	// that reader blocks sending to Events when the queue is full. If no one
	// drains Events while we Add, the Add reply never arrives and the runtime
	// reports "all goroutines are asleep - deadlock!". Start the consumer
	// first so Adds below always have a live reader.
	watchCtx, cancel := context.WithCancel(ctx)
	a.watch = watcher
	a.watchCancel = cancel
	a.watchUpdates = make(chan watchUpdate, 1)
	a.watchErr = nil

	wsRoot := a.ws.Root
	limit := a.ws.Limit
	start := a.ws
	startChanges := a.changes
	go a.watchLoop(watchCtx, watcher, wsRoot, limit, start, startChanges)

	watched, watchErr := a.addWatchTree(watcher, wsRoot)
	if watchErr != nil && watched == 0 {
		cancel()
		_ = watcher.Close()
		a.watch = nil
		a.watchCancel = nil
		a.watchErr = watchErr

		return watchErr
	}

	// A partial tree still gives live updates for most of the workspace.
	// Record the degradation and keep running instead of failing startup.
	if watchErr != nil {
		a.watchErr = watchErr
		a.watchNote = watchErr.UserMessage()
	}

	if gitErr := a.addGitWatches(watcher, wsRoot); gitErr != nil && a.watchErr == nil {
		a.watchErr = gitErr
		a.watchNote = gitErr.UserMessage()
	}

	return nil
}

// StopWatcher closes the fsnotify watcher. Close errors other than "already
// closed" are recorded on the app so the next StartWatcher starts clean
// instead of surfacing a stale failure.
func (a *App) StopWatcher() {
	if a.watch == nil {
		return
	}

	if a.watchCancel != nil {
		a.watchCancel()
		a.watchCancel = nil
	}

	if err := a.watch.Close(); err != nil {
		if appErr := AppErrorAt(err); appErr != nil {
			a.watchErr = appErr
		}
		// fsnotify.ErrClosed and double-close races are benign: the watcher
		// is gone either way.
	}

	a.watch = nil

	for {
		select {
		case <-a.watchUpdates:
		default:
			return
		}
	}
}

// addWatchTree adds watched directories. For lazy workspaces it watches
// root + loaded/expanded dirs + git metadata only, keeping handles at
// expanded+~8 instead of the 2000-dir cap. Full-tree walks stay for explicit
// -files scans. Collapsed dirs are evicted first under pressure.
// The caller must already be draining watcher.Events: on Windows each Add is
// served by the backend reader goroutine, which blocks when the Events queue
// is full, so adding without a live reader deadlocks the Add reply.
//
// It returns how many directories are watched. Per-directory Add failures
// (permission, handle limits, races with deletion) never abort the walk: they
// are counted and, when at least one directory was watched, reported as a
// degraded WatchOpAdd warning. A walk failure or zero watched directories is
// a *AppError the caller must treat as fatal.
func (a *App) addWatchTree(watcher *fsnotify.Watcher, root string) (int, *AppError) {
	if a.ws != nil && a.ws.Lazy {
		return a.addWatchTreeLazy(watcher, root)
	}
	count := 0
	addFailures := 0

	var firstAddErr error

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}

			return nil //nolint:nilerr // an unreadable subdirectory must not stop the walk
		}

		if !entry.IsDir() {
			return nil
		}

		// Never descend into symlinked directories: WalkDir does not follow
		// them, and watching the link itself adds noise without coverage.
		if entry.Type()&os.ModeSymlink != 0 {
			return filepath.SkipDir
		}

		if path != root && skipDir(entry.Name()) {
			return filepath.SkipDir
		}

		if path != root && skipWatchDir(entry.Name()) {
			return filepath.SkipDir
		}

		if strings.Contains(strings.ToLower(filepath.ToSlash(path)), "/.git") {
			return filepath.SkipDir
		}

		if count >= maxWatchDirs {
			return filepath.SkipAll
		}

		if watchErr := watcher.Add(path); watchErr != nil {
			addFailures++
			if firstAddErr == nil {
				firstAddErr = watchErr
			}

			// A closed watcher is fatal: further Adds cannot succeed.
			if errors.Is(watchErr, fsnotify.ErrClosed) {
				return NewWatchError(WatchOpClosed, path, watchErr)
			}

			return nil
		}

		count++

		return nil
	})
	if err != nil {
		if appErr := AppErrorAt(err); appErr != nil {
			return count, appErr
		}

		return count, NewWatchError(WatchOpWalk, root, err)
	}

	if count == 0 {
		cause := firstAddErr
		if cause == nil {
			cause = errors.New("no directories could be watched")
		}

		return 0, NewWatchError(WatchOpAdd, root, cause)
	}

	if addFailures > 0 {
		return count, NewWatchError(
			WatchOpAdd, root,
			fmt.Errorf("%d of %d directories skipped: %w", addFailures, count+addFailures, firstAddErr),
		)
	}

	return count, nil
}

// addWatchTreeLazy watches root plus every Loaded dir in the lazy tree.
// Newly loaded dirs get handles via watchDirLoaded (off-loop,
// drain-before-Add). Under maxWatchDirs pressure collapsed dirs are evicted
// first; handles stay a backstop, not routine truncation.
func (a *App) addWatchTreeLazy(watcher *fsnotify.Watcher, root string) (int, *AppError) {
	count := 0
	var firstAddErr error
	addOne := func(path string) bool {
		// Drain-before-Add for the Windows backend.
		select {
		case <-watcher.Events:
		default:
		}
		if err := watcher.Add(path); err != nil {
			if firstAddErr == nil {
				firstAddErr = err
			}
			if errors.Is(err, fsnotify.ErrClosed) {
				return false
			}
			return true
		}
		count++
		return true
	}
	if !addOne(root) {
		return 0, NewWatchError(WatchOpClosed, root, firstAddErr)
	}
	if a.ws != nil {
		var loaded []string
		var walk func([]*fileNode)
		walk = func(nodes []*fileNode) {
			for _, n := range nodes {
				if n.Dir && n.Loaded && len(n.Children) > 0 {
					loaded = append(loaded, n.Path)
				}
				walk(n.Children)
			}
		}
		walk(a.ws.Tree)
		// Collapsed-first eviction: expanded (open) dirs win.
		openFirst := loaded[:0]
		collapsed := []string{}
		for _, p := range loaded {
			if a.folders[p] {
				openFirst = append(openFirst, p)
			} else {
				collapsed = append(collapsed, p)
			}
		}
		ordered := append(openFirst, collapsed...)
		for _, rel := range ordered {
			if count >= maxWatchDirs {
				break
			}
			abs := filepath.Join(root, filepath.FromSlash(rel))
			_ = addOne(abs)
		}
	}
	if count == 0 {
		cause := firstAddErr
		if cause == nil {
			cause = errors.New("no directories could be watched")
		}
		return 0, NewWatchError(WatchOpAdd, root, cause)
	}
	return count, nil
}

// skipWatchDir hides Windows system and profile churn from the watcher. The
// workspace scan keeps its own skipDir list; this is watcher-only so opening a
// home directory does not install thousands of volatile handles.
func skipWatchDir(name string) bool {
	switch strings.ToLower(name) {
	case "appdata", "$recycle.bin", "system volume information":
		return true
	}

	return false
}

// addGitWatches watches the metadata files that move HEAD, refs, or the index.
// .git/objects is deliberately left out: object churn is not a branch change.
// Missing git metadata is not an error; a git dir that exists but cannot be
// watched is a degraded WatchOpGit warning.
func (a *App) addGitWatches(watcher *fsnotify.Watcher, root string) *AppError {
	gitDir := filepath.Join(root, ".git")

	if _, err := os.Stat(gitDir); err != nil {
		return nil
	}

	added := 0

	var firstErr error

	for _, path := range []string{
		gitDir,
		filepath.Join(gitDir, "refs"),
		filepath.Join(gitDir, "refs", "heads"),
		filepath.Join(gitDir, "refs", "tags"),
		filepath.Join(gitDir, "refs", "remotes"),
		filepath.Join(gitDir, "logs"),
	} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}

		if err := watcher.Add(path); err != nil {
			if firstErr == nil {
				firstErr = err
			}

			if errors.Is(err, fsnotify.ErrClosed) {
				return NewWatchError(WatchOpClosed, path, err)
			}

			continue
		}

		added++
	}

	if added == 0 && firstErr != nil {
		return NewWatchError(WatchOpGit, gitDir, firstErr)
	}

	return nil
}

// watchLoop debounces events and sends one update back to the UI loop.
func (a *App) watchLoop(ctx context.Context, watcher *fsnotify.Watcher, root string, limit int, start *workspace, startChanges changeSet) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}

	pollTicker := time.NewTicker(gitPollInterval)
	defer pollTicker.Stop()

	pending := false
	rescan := false
	gitChange := false
	overflow := false
	gitignoreChange := false
	changedDirs := map[string]bool{}
	last := start
	lastChanges := startChanges

	pushUpdate := func(update watchUpdate) {
		select {
		case a.watchUpdates <- update:
		default:
			select {
			case <-a.watchUpdates:
			default:
			}

			select {
			case a.watchUpdates <- update:
			default:
			}
		}

		if a.page != nil {
			a.page.WantFrameIn(0)
		}
	}

	flush := func() {
		pending = false

		update := watchUpdate{Git: gitChange}

		if rescan {
			if last != nil && last.Lazy && !overflow {
				if gitignoreChange {
					// .gitignore moved: lazy-preserving rescan (no full walk).
					next, delta, err := rescanDiskWorkspace(root, last, limit)
					if err != nil {
						update.Err = NewAppError(KindWorkspace, WatchOpRescan, root, err)
					} else {
						update.Workspace = next
						update.Delta = delta
						last = next
					}
				} else {
					// Targeted: no full walk, page loop reloads loaded dirs.
					for d := range changedDirs {
						update.Dirs = append(update.Dirs, d)
					}
				}
			} else {
				next, delta, err := rescanDiskWorkspace(root, last, limit)
				if err != nil {
					update.Err = NewAppError(KindWorkspace, WatchOpRescan, root, err)
				} else {
					update.Workspace = next
					update.Delta = delta
					last = next
				}
			}
		}

		changes, err := loadLiveChanges(ctx, root)
		if err != nil {
			if update.Err == nil {
				update.Err = NewAppError(KindGit, WatchOpChanges, root, err)
			}
		} else {
			update.Changes = changes
			update.Branch = changes.Branch
			lastChanges = changes
		}

		if branches, head, branchErr := loadBranches(ctx, root); branchErr != nil {
			if update.Err == nil {
				update.Err = NewAppError(KindGit, WatchOpChanges, root, branchErr)
			}
		} else {
			update.Branches = branches
			update.Branch = head
		}

		pushUpdate(update)

		rescan = false
		gitChange = false
		overflow = false
		gitignoreChange = false
		changedDirs = map[string]bool{}
	}

	for {
		select {
		case <-ctx.Done():
			timer.Stop()

			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if event.Op&fsnotify.Create != 0 {
				// Adding a subtree blocks on the backend reader reply, so
				// never do it inline: while this loop waits for Add, nobody
				// drains Events and the Windows backend deadlocks. Queue the
				// subtree in the background; the debounce below already
				// coalesces the storm into one rescan.
				if info, err := os.Lstat(event.Name); err == nil && info.IsDir() &&
					info.Mode()&os.ModeSymlink == 0 &&
					!skipDir(filepath.Base(event.Name)) &&
					!skipWatchDir(filepath.Base(event.Name)) {
					dir := event.Name
					go func() {
						defer func() {
							_ = recover()
						}()

						if _, addErr := a.addWatchTree(watcher, dir); addErr != nil {
							pushUpdate(watchUpdate{Err: addErr})
						}
					}()
				}
			}

			if isWatchIgnored(event.Name) {
				continue
			}

			if isGitMetadataChange(event.Name) {
				gitChange = true
			} else {
				rescan = true
				gitChange = true
				if filepath.Base(event.Name) == ".gitignore" {
					gitignoreChange = true
				}
				// Record the parent dir for targeted lazy reload.
				if rel, relErr := filepath.Rel(root, event.Name); relErr == nil && rel != "." && rel != ".." {
					slash := filepath.ToSlash(rel)
					// If the event names a dir, reload it; else reload its parent.
					dir := slash
					if info, statErr := os.Lstat(event.Name); statErr != nil || !info.IsDir() {
						if i := len(slash) - len(filepath.Base(slash)) - 1; i >= 0 {
							dir = slash[:i]
						} else {
							dir = ""
						}
					}
					if dir == "" {
						dir = "."
					}
					if dir == "." {
						// Root-level change: parent is root; mark all loaded
						// as stale via empty dir (page loop re-reads root).
						changedDirs["."] = true
					} else {
						changedDirs[dir] = true
					}
				}
			}

			if !pending {
				pending = true
				timer.Reset(watchDebounce)
			}
		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return
			}

			// An overflow means the kernel dropped events: force a full
			// rescan instead of trusting the debounced delta. Any other
			// backend error is recorded and surfaced as a status note.
			if errors.Is(watchErr, fsnotify.ErrEventOverflow) {
				rescan = true
				overflow = true
				gitChange = true
				if !pending {
					pending = true
					timer.Reset(watchDebounce)
				}

				pushUpdate(watchUpdate{Err: NewWatchError(WatchOpOverflow, root, watchErr)})
				continue
			}

			if watchErr != nil {
				pushUpdate(watchUpdate{Err: NewWatchError(WatchOpEvents, root, watchErr)})
			}
		case <-timer.C:
			flush()
		case <-pollTicker.C:
			if pending {
				continue
			}

			changes, err := loadLiveChanges(ctx, root)
			if err != nil {
				continue
			}

			if !changeSetDelta(lastChanges, changes) {
				continue
			}

			lastChanges = changes
			pushUpdate(watchUpdate{
				Changes: changes,
				Branch:  changes.Branch,
			})
		}
	}
}

// tickWatch drains one background update and any finished patch, search,
// or directory load.
func (a *App) tickWatch(ctx context.Context) error {
	if err := a.applyPendingFocus(ctx); err != nil {
		return err
	}

	select {
	case update := <-a.watchUpdates:
		a.applyWatchUpdate(ctx, update)
	default:
	}

	a.pollTerminal(ctx)
	a.pollPatches(ctx)
	a.pollBlame(ctx)
	a.receiveSearch(ctx)
	a.receiveHistoryLoad(ctx)
	a.pollAutoSave(ctx)
	a.scheduleOpenFoldersLoaded()
	a.receiveDirLoads(ctx)

	return nil
}

// setWatchError records a watcher startup failure using the global AppError
// envelope. Plain errors are wrapped as KindWatch/WatchOpStartup so every
// failure path ends in one handled shape: a status note plus a refresh,
// never a fatal crash or a deadlock report.
func (a *App) setWatchError(err error) {
	if err == nil {
		a.watchErr = nil
		return
	}

	if appErr := AppErrorAt(err); appErr != nil {
		a.watchErr = appErr
		a.watchNote = appErr.UserMessage()
		return
	}

	wrapped := NewWatchError(WatchOpStartup, "", err)
	a.watchErr = wrapped
	a.watchNote = wrapped.UserMessage()
}

// applyWatchUpdate installs a rescan and then the refreshed git state. A
// background Err never aborts the update: the last good state stays on
// screen and the failure is recorded as a status note via the global
// AppError.
func (a *App) applyWatchUpdate(ctx context.Context, update watchUpdate) {
	if update.Err != nil {
		a.watchErr = update.Err
		a.watchNote = update.Err.UserMessage()
	}

	if update.Workspace != nil {
		a.installRescan(update.Workspace, update.Delta)
		// A successful rescan clears a stale overflow/rescan note; a
		// concurrent failure note from this same update wins.
		if update.Err == nil {
			a.watchErr = nil
		}
	}

	if len(update.Dirs) > 0 {
		a.applyTargetedDirs(update.Dirs)
	}

	if update.Changes.Branch != "" || len(update.Changes.Files) > 0 || update.Changes.Note != "" {
		a.applyWatchChanges(ctx, update.Changes)
	}

	if update.Git {
		a.applyGitRefresh(ctx, update)
	}
}

// applyTargetedDirs reloads loaded dirs without a full walk. Loaded dirs
// re-enqueue (requestDirLoad); unloaded dirs mark their parent stale
// (Loaded=false) with no I/O so the next expand re-reads.
func (a *App) applyTargetedDirs(dirs []string) {
	if a.ws == nil || a.ws.Sample {
		return
	}
	a.watchNote = "Workspace rescan: updated"
	for _, dir := range dirs {
		if dir == "." || dir == "" {
			// Root-level change: stale-mark root children? Re-read root
			// via lazy rescan of root only is cheap; for now mark loaded
			// root files stale by re-requesting open loaded dirs.
			for path, open := range a.folders {
				if !open {
					continue
				}
				if node := findTreeNode(a.ws.Tree, path); node != nil && node.Dir && node.Loaded {
					node.Loaded = false
					node.Loading = false
					node.LoadErr = ""
					a.requestDirLoad(path)
				}
			}
			continue
		}
		node := findTreeNode(a.ws.Tree, dir)
		if node != nil && node.Dir && node.Loaded {
			node.Loaded = false
			node.Loading = false
			node.LoadErr = ""
			a.requestDirLoad(dir)
			continue
		}
		if node == nil {
			// New directory (e.g. pkg/ created after startup): refresh root
			// to discover it, then load it if it was meant to be open.
			a.refreshRoot()
			if fresh := findTreeNode(a.ws.Tree, dir); fresh != nil && fresh.Dir {
				if a.folders[dir] {
					a.requestDirLoad(dir)
				}
				continue
			}
		}
		// Unloaded dir: mark parent stale, no I/O.
		if parent := dirName(dir); parent != "" {
			if pnode := findTreeNode(a.ws.Tree, parent); pnode != nil && pnode.Dir && pnode.Loaded {
				pnode.Loaded = false
				pnode.Loading = false
			}
		} else {
			// Top-level unloaded change: refresh root to pick up new files.
			a.refreshRoot()
		}
	}
	a.refresh()
}

// installRescan swaps the workspace, preserves open documents that still
// exist, and drops tabs for files that disappeared.
func (a *App) installRescan(next *workspace, delta workspaceDelta) {
	if next == nil {
		return
	}

	a.ws = next
	a.scanLimit = next.Limit
	// A rescan retires in-flight directory loads; stale replies drop.
	a.dirLoads = map[string]int{}
	// Paths can collide across workspaces, so a rescan retires every cached
	// blame line; the next frame re-requests the visible files.
	a.invalidateAllBlame()

	a.tabs = keepExistingTabs(a.tabs, next)
	a.tabs2 = keepExistingTabs(a.tabs2, next)
	a.active = clampSessionIndex(a.active, len(a.tabs))
	a.active2 = clampSessionIndex(a.active2, len(a.tabs2))

	for path := range a.docs {
		if next.ByPath[path] == nil {
			delete(a.docs, path)
			delete(a.dirty, path)

			continue
		}
	}

	for _, path := range a.groupPaths() {
		if next.ByPath[path] == nil {
			continue
		}

		if a.dirty[path] || (a.editing && a.editPath() == path) {
			continue
		}

		a.docs[path] = a.ws.openDoc(path)
	}

	a.recent = keepExistingTabs(a.recent, next)
	a.statuses = statusMap(a.changes)
	a.syncFileNav()

	if a.diffPath != "" && findFile(a.changes.Files, a.diffPath) == nil {
		a.selectFirstDiff()
	}

	a.watchNote = rescanNote(delta)
	if a.split && len(a.tabs2) == 0 {
		a.split = false
		a.focusGroup = 0
	}
}

// keepExistingTabs drops paths the rescan no longer lists.
func keepExistingTabs(paths []string, ws *workspace) []string {
	out := make([]string, 0, len(paths))

	for _, path := range paths {
		if ws.ByPath[path] != nil {
			out = append(out, path)
		}
	}

	return out
}

// rescanNote is the short status line for added, removed, or changed files.
func rescanNote(delta workspaceDelta) string {
	parts := make([]string, 0, rescanNoteParts)

	if len(delta.Added) > 0 {
		parts = append(parts, fmt.Sprintf("%d added", len(delta.Added)))
	}

	if len(delta.Removed) > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", len(delta.Removed)))
	}

	if len(delta.Changed) > 0 {
		parts = append(parts, fmt.Sprintf("%d changed", len(delta.Changed)))
	}

	if len(parts) == 0 {
		return ""
	}

	return "Workspace rescan: " + strings.Join(parts, ", ")
}

// applyWatchChanges updates the diff without moving the user's selection.
func (a *App) applyWatchChanges(ctx context.Context, changes changeSet) {
	a.changes = changes
	a.pruneSelected()
	if changes.Branch != "" {
		a.headBranch = changes.Branch
	}
	a.statuses = statusMap(a.changes)
	a.syncFileNav()

	if a.diffSource == "working" {
		if findFile(a.changes.Files, a.diffPath) == nil {
			if len(a.changes.Files) > 0 {
				a.diffPath = a.changes.Files[0].Path
			} else {
				a.diffPath = ""
			}
		}

		for i, file := range a.changes.Files {
			if file.Path == a.diffPath {
				a.revealChangeIndex(i)

				break
			}
		}
	}

	for _, path := range a.groupPaths() {
		if a.ws.ByPath[path] == nil {
			continue
		}

		if a.dirty[path] || (a.editing && a.editPath() == path) {
			continue
		}

		a.docs[path] = a.ws.openDoc(path)
		// The rescan reopened the file, so its stat key may have moved:
		// drop the cached blame and let the next frame re-request it.
		a.invalidateBlame(path)
	}

	if a.findOpen {
		a.reindexFind()
	}

	a.rebuildDiffRows()
	a.scrollY = a.clampScroll(a.scrollY)
	a.reconcileMergeAfterReload()
	a.view = a.buildView()
	a.page.SetData(&a.view)
	_ = a.page.Redraw(ctx)
}

// applyGitRefresh installs a new branch list and reloads the visible history.
func (a *App) applyGitRefresh(ctx context.Context, update watchUpdate) {
	if len(update.Branches) > 0 {
		a.branches = update.Branches
	}

	if update.Branch != "" {
		a.headBranch = update.Branch
	}

	if a.historyRef != "" {
		if _, ok := branchByRef(a.branches, a.historyRef); !ok {
			a.historyRef = ""
		}
	}

	history, err := loadCommitLogLimit(ctx, a.ws.Root, a.historyRef, a.historyLimit)
	if err != nil {
		return
	}

	if a.historyRef != "" {
		a.branchLogs.Put(a.historyRef, history)
	}

	a.commits = history
	a.historyLabel = history.Branch
	a.historyScroll.Limit = len(history.Commits)
	a.historyDone = len(history.Commits) < a.historyLimit || a.historyLimit >= maxHistoryCommits
	// A new head moves every file's blame, so the cached lines retire and
	// the next frame re-requests them.
	a.invalidateAllBlame()
	if a.diffSource == "branch" {
		a.refreshBranchChanges(ctx)
	}

	if a.commitOpen != "" && a.findCommit(a.commitOpen) == nil {
		a.commitOpen = ""
		a.commitFile = ""
		a.commitRows = nil
		a.commitPreview = nil

		if a.diffSource == "commit" {
			a.diffSource = "working"
			a.rebuildDiffRows()
		}
	}

	a.refresh()
}

// isWatchIgnored filters editor churn and git object writes. HEAD, refs, and
// the index stay visible because isGitMetadataChange accepts them first.
func isWatchIgnored(path string) bool {
	slash := filepath.ToSlash(path)

	if isGitMetadataChange(path) {
		return false
	}

	if strings.Contains(slash, "/.git/") {
		return true
	}

	return strings.HasSuffix(slash, ".swp") ||
		strings.HasSuffix(slash, ".swx") ||
		strings.HasSuffix(slash, "~") ||
		strings.HasSuffix(slash, ".tmp")
}

// changeSetDelta reports whether next differs from prev in branch, note,
// or list of files and their statuses/diffs.
func changeSetDelta(prev, next changeSet) bool {
	if prev.Branch != next.Branch || prev.Note != next.Note || len(prev.Files) != len(next.Files) {
		return true
	}

	for i := range prev.Files {
		p := &prev.Files[i]
		n := &next.Files[i]

		if p.Path != n.Path || p.Status != n.Status || p.OldPath != n.OldPath ||
			p.Add != n.Add || p.Del != n.Del || p.Binary != n.Binary ||
			!hunksEqual(p.Hunks, n.Hunks) {
			return true
		}
	}

	if len(prev.Conflicts) != len(next.Conflicts) {
		return true
	}

	for i := range prev.Conflicts {
		p := &prev.Conflicts[i]
		n := &next.Conflicts[i]

		if p.Path != n.Path || p.Status != n.Status ||
			p.Conflicts != n.Conflicts || p.Binary != n.Binary {
			return true
		}
	}

	return false
}

func hunksEqual(a, b []hunk) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		ha := &a[i]
		hb := &b[i]

		if ha.OldStart != hb.OldStart || ha.OldCount != hb.OldCount ||
			ha.NewStart != hb.NewStart || ha.NewCount != hb.NewCount ||
			ha.Header != hb.Header || len(ha.Lines) != len(hb.Lines) {
			return false
		}

		for j := range ha.Lines {
			la := &ha.Lines[j]
			lb := &hb.Lines[j]

			if la.Kind != lb.Kind || la.OldNo != lb.OldNo || la.NewNo != lb.NewNo || la.Text != lb.Text {
				return false
			}
		}
	}

	return true
}
