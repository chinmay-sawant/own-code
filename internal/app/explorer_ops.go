package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Explorer file/folder operations: New, Rename, Delete, Duplicate.
//
// The disk primitives live in workspace_ops.go (createDir, renamePath,
// removePath) next to createFile. This file holds the UI-free backend: shared
// validation, the five ops (no refresh, no toasts inside — callers toast),
// doc/tab hygiene, the inline-input controller, delete confirmation, and the
// palette `>` command mode. It mirrors the paste backend in explorer_paste.go.

const (
	// Inline input modes.
	explorerInputNewFile   = "new-file"
	explorerInputNewFolder = "new-folder"
	explorerInputRename    = "rename"

	// Confirm kind for the explorer delete dialog. SCM discards keep their
	// own kinds in scm_ops.go.
	confirmExplorerDelete = "explorer-delete"

	// Palette command ids for the `>` command mode.
	paletteCmdNewFile   = "new-file"
	paletteCmdNewFolder = "new-folder"
	paletteCmdRename    = "rename"
	paletteCmdDelete    = "delete"
	paletteCmdDuplicate = "duplicate"
	// Tab command ids for the preview/pinned plan: pin, reopen, and the
	// bulk-close family act on the focused group.
	paletteCmdPinTab       = "pin-tab"
	paletteCmdReopenTab    = "reopen-tab"
	paletteCmdCloseOthers  = "close-others"
	paletteCmdCloseLeft    = "close-left"
	paletteCmdCloseRight   = "close-right"
	paletteCmdCloseAllTabs = "close-all-tabs"
	// Go command id: jumps to the definition of the symbol at the caret.
	paletteCmdGotoDefinition = "goto-definition"
)

// Context-menu ids for the explorer file operations, alongside the cut/copy
// ids in context_menu.go.
const (
	contextMenuNewFileID   = contextMenuIDPrefix + "new-file:"
	contextMenuNewFolderID = contextMenuIDPrefix + "new-folder:"
	contextMenuRenameID    = contextMenuIDPrefix + "rename:"
	contextMenuDeleteID    = contextMenuIDPrefix + "delete:"
	contextMenuDuplicateID = contextMenuIDPrefix + "duplicate:"
)

// contextMenuSep marks a separator row. The menu template renders it as an
// <hr>; it is disabled so it never dispatches.
func contextMenuSep() contextMenuRow {
	return contextMenuRow{Label: "-"}
}

// cleanExplorerRel normalizes one workspace-relative path for the explorer
// ops: "" is rejected (callers pass root as "" before joining), anything
// absolute or escaping with ".." is rejected, and NUL bytes are rejected.
// It is the single choke point before paths reach filepath.Join.
func cleanExplorerRel(path string) (string, bool) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" || strings.ContainsRune(trimmed, 0) {
		return "", false
	}

	if filepath.IsAbs(trimmed) {
		return "", false
	}

	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(trimmed)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}

	return clean, true
}

// validateExplorerName checks one file or folder name (no separators: the
// op joins it onto the target directory). It mirrors the traversal rules in
// cleanExplorerRel plus the reserved names and Windows-hostile suffixes.
func validateExplorerName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("name is empty")
	}

	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("name contains NUL")
	}

	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("name cannot contain path separators")
	}

	if name == "." || name == ".." {
		return fmt.Errorf("name %q is reserved", name)
	}

	if len(name) > 255 {
		return fmt.Errorf("name is too long")
	}

	if strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return fmt.Errorf("name cannot end with a space or dot")
	}

	if strings.HasPrefix(name, " ") {
		return fmt.Errorf("name cannot start with a space")
	}

	return nil
}

// explorerJoin joins a validated directory and a validated name. "" dir is
// the workspace root.
func explorerJoin(dir, name string) string {
	if dir == "" {
		return name
	}

	return dir + "/" + name
}

// createExplorerFile writes an empty file into dir and registers it, without
// refreshing. dir "" is the root. It never overwrites: an existing path is
// an error for a toast.
func (a *App) createExplorerFile(dir, name string) (string, error) {
	ws := a.ws
	if ws == nil {
		return "", fmt.Errorf("no workspace")
	}

	if !ws.writable() {
		return "", errSampleReadOnly()
	}

	cleanDir := ""
	if strings.TrimSpace(dir) != "" {
		clean, ok := cleanExplorerRel(dir)
		if !ok {
			return "", fmt.Errorf("folder %q is not in this workspace", dir)
		}

		if isDir, found := a.lookupTreePath(clean); found {
			if !isDir {
				return "", fmt.Errorf("%s is a file, not a folder", clean)
			}
		} else if info, err := os.Stat(filepath.Join(ws.Root, filepath.FromSlash(clean))); err != nil || !info.IsDir() {
			return "", fmt.Errorf("folder %q is not in this workspace", dir)
		}

		cleanDir = clean
	}

	if err := validateExplorerName(name); err != nil {
		return "", err
	}

	rel := explorerJoin(cleanDir, name)

	if err := ws.createFile(rel, nil); err != nil {
		return "", err
	}

	if _, err := ws.ensureFile(rel); err != nil {
		return "", fmt.Errorf("create %s: %w", rel, err)
	}

	return rel, nil
}

// createExplorerDir makes one folder inside dir and returns its path,
// without refreshing. It never overwrites.
func (a *App) createExplorerDir(dir, name string) (string, error) {
	ws := a.ws
	if ws == nil {
		return "", fmt.Errorf("no workspace")
	}

	if !ws.writable() {
		return "", errSampleReadOnly()
	}

	cleanDir := ""
	if strings.TrimSpace(dir) != "" {
		clean, ok := cleanExplorerRel(dir)
		if !ok {
			return "", fmt.Errorf("folder %q is not in this workspace", dir)
		}

		if isDir, found := a.lookupTreePath(clean); found {
			if !isDir {
				return "", fmt.Errorf("%s is a file, not a folder", clean)
			}
		} else if info, err := os.Stat(filepath.Join(ws.Root, filepath.FromSlash(clean))); err != nil || !info.IsDir() {
			return "", fmt.Errorf("folder %q is not in this workspace", dir)
		}

		cleanDir = clean
	}

	if err := validateExplorerName(name); err != nil {
		return "", err
	}

	rel := explorerJoin(cleanDir, name)

	if err := ws.createDir(rel); err != nil {
		return "", err
	}

	return rel, nil
}

// renameExplorerPath renames oldRel within its own directory (same-dir only
// for v1; cross-dir move stays Cut/Paste) and returns the new path, without
// refreshing. It never overwrites.
func (a *App) renameExplorerPath(oldRel, newName string) (string, error) {
	ws := a.ws
	if ws == nil {
		return "", fmt.Errorf("no workspace")
	}

	if !ws.writable() {
		return "", errSampleReadOnly()
	}

	old, ok := cleanExplorerRel(oldRel)
	if !ok {
		return "", fmt.Errorf("%q is not in this workspace", oldRel)
	}

	if err := validateExplorerName(newName); err != nil {
		return "", err
	}

	next := explorerJoin(dirName(old), newName)

	if _, found := a.lookupTreePath(old); !found {
		if _, err := os.Stat(filepath.Join(ws.Root, filepath.FromSlash(old))); err != nil {
			return "", fmt.Errorf("no file %s", old)
		}
	}

	if err := ws.renamePath(old, next); err != nil {
		return "", err
	}

	return next, nil
}

// deleteExplorerPaths removes every listed workspace path (files and folders)
// and returns the deleted ones, without refreshing. Unknown paths are
// skipped; when nothing was deleted the error says so.
func (a *App) deleteExplorerPaths(rels []string) ([]string, error) {
	ws := a.ws
	if ws == nil {
		return nil, fmt.Errorf("no workspace")
	}

	if !ws.writable() {
		return nil, errSampleReadOnly()
	}

	var deleted []string

	var firstErr error

	for _, rel := range rels {
		clean, ok := cleanExplorerRel(rel)
		if !ok {
			continue
		}

		if _, found := a.lookupTreePath(clean); !found {
			if _, err := os.Stat(filepath.Join(ws.Root, filepath.FromSlash(clean))); err != nil {
				continue
			}
		}

		if err := ws.removePath(clean); err != nil {
			if firstErr == nil {
				firstErr = err
			}

			continue
		}

		deleted = append(deleted, clean)
	}

	if len(deleted) == 0 && firstErr == nil {
		firstErr = fmt.Errorf("nothing to delete")
	}

	return deleted, firstErr
}

// duplicateExplorerPath copies rel beside itself with the paste ` (copy)`
// suffix rule and returns the new path, without refreshing.
func (a *App) duplicateExplorerPath(rel string) (string, error) {
	ws := a.ws
	if ws == nil {
		return "", fmt.Errorf("no workspace")
	}

	if !ws.writable() {
		return "", errSampleReadOnly()
	}

	clean, ok := cleanExplorerRel(rel)
	if !ok {
		return "", fmt.Errorf("%q is not in this workspace", rel)
	}

	isDir := false
	if dir, found := a.lookupTreePath(clean); found {
		isDir = dir
	} else if info, err := os.Stat(filepath.Join(ws.Root, filepath.FromSlash(clean))); err == nil {
		isDir = info.IsDir()
	} else {
		return "", fmt.Errorf("no file %s", clean)
	}

	dir := dirName(clean)
	base := baseName(clean)

	next, err := uniquePasteCopyPath(ws, dir, base)
	if err != nil {
		return "", err
	}

	if isDir {
		dst := filepath.Join(ws.Root, filepath.FromSlash(next))
		from := filepath.Join(ws.Root, filepath.FromSlash(clean))

		if err := os.MkdirAll(dst, workspaceDirMode); err != nil {
			return "", fmt.Errorf("duplicate %s: %w", clean, err)
		}

		if err := os.CopyFS(dst, os.DirFS(from)); err != nil {
			return "", fmt.Errorf("duplicate %s: %w", clean, err)
		}

		return next, nil
	}

	raw, err := ws.readFile(clean)
	if err != nil {
		return "", fmt.Errorf("duplicate %s: %w", clean, err)
	}

	if err := ws.createFile(next, raw); err != nil {
		return "", err
	}

	if _, err := ws.ensureFile(next); err != nil {
		return "", fmt.Errorf("duplicate %s: %w", next, err)
	}

	return next, nil
}

// forgetExplorerDocs migrates or closes open documents for removed paths and
// rename pairs. Renames re-key docs, dirty flags, the live edit buffer, tabs,
// recents, selection, and folder expansion in place; deletes close docs
// (finishing an edit on a deleted path), drop tabs, and prune recents,
// selection, and expansion. Callers refresh after.
func (a *App) forgetExplorerDocs(removed []string, renames map[string]string) {
	gone := map[string]bool{}
	for _, rel := range removed {
		if clean, ok := cleanExplorerRel(rel); ok {
			gone[clean] = true
		} else if rel != "" {
			gone[rel] = true
		}
	}

	renameDir := func(path string) (string, bool) {
		for old, next := range renames {
			if path == old {
				return next, true
			}

			if strings.HasPrefix(path, old+"/") {
				return next + strings.TrimPrefix(path, old), true
			}
		}
		return path, false
	}

	dropped := func(path string) bool {
		if gone[path] {
			return true
		}

		for old := range gone {
			if strings.HasPrefix(path, old+"/") {
				// A renamed source already migrated above; only drop paths
				// that did not move.
				if _, moved := renameDir(path); !moved {
					return true
				}
			}
		}

		return false
	}

	for old, next := range renames {
		if doc, ok := a.docs[old]; ok {
			doc.Path = next
			a.docs[next] = doc
			delete(a.docs, old)
		}

		if a.isDirty(old) {
			a.setDirty(next, true)
			a.setDirty(old, false)
		}

		if a.editing && a.edit != nil && a.editPath() == old {
			a.edit.Path = next
		}

		if a.mergeOpen && a.mergePath == old {
			a.mergePath = next
		}
	}

	// Folder expansion follows renames so an expanded folder stays open.
	for old, next := range renames {
		for path, open := range a.folders {
			if !open {
				continue
			}

			if path == old {
				a.folders[next] = true
				delete(a.folders, old)
			} else if strings.HasPrefix(path, old+"/") {
				a.folders[next+strings.TrimPrefix(path, old)] = true
				delete(a.folders, path)
			}
		}

		for path := range a.explSel {
			if path == old {
				a.explSel[next] = true
				delete(a.explSel, old)
			} else if strings.HasPrefix(path, old+"/") {
				a.explSel[next+strings.TrimPrefix(path, old)] = true
				delete(a.explSel, path)
			}
		}
	}

	for _, path := range append(append([]string(nil), a.tabs...), a.tabs2...) {
		if !dropped(path) {
			continue
		}

		if a.editing && a.editPath() == path {
			a.finishEdit()
		}

		if a.mergeOpen && a.mergePath == path {
			a.leaveMergeMode()
			a.mode = "changes"
		}

		delete(a.docs, path)
		delete(a.dirty, path)
	}

	// Docs can outlive their tabs, so sweep any remaining document for a
	// deleted path. Rename sources already migrated above and are not
	// dropped.
	for path := range a.docs {
		if dropped(path) {
			if a.editing && a.editPath() == path {
				a.finishEdit()
			}

			delete(a.docs, path)
			a.setDirty(path, false)
		}
	}

	mapTabs := func(tabs []string) []string {
		out := tabs[:0]
		for _, path := range tabs {
			if next, ok := renameDir(path); ok && next != path {
				out = append(out, next)
				continue
			}

			if dropped(path) {
				continue
			}

			out = append(out, path)
		}
		return out
	}

	a.tabs = mapTabs(a.tabs)
	a.tabs2 = mapTabs(a.tabs2)
	a.active = clampSessionIndex(a.active, len(a.tabs))
	a.active2 = clampSessionIndex(a.active2, len(a.tabs2))

	if len(a.tabs2) == 0 && a.split {
		a.split = false
		a.focusGroup = 0
	}

	kept := a.recent[:0]
	for _, path := range a.recent {
		if next, ok := renameDir(path); ok && next != path {
			kept = append(kept, next)
			continue
		}

		if dropped(path) {
			continue
		}

		kept = append(kept, path)
	}
	a.recent = kept

	for path := range a.explSel {
		if dropped(path) {
			delete(a.explSel, path)
		}
	}

	for path := range a.folders {
		if dropped(path) {
			delete(a.folders, path)
		}
	}

	if a.diffPath != "" && dropped(a.diffPath) {
		a.selectFirstDiff()
	}

	a.syncFileNav()
}

// refreshExplorerAfterMutation makes minted/removed paths and rename pairs
// visible. Docs migrate first; then a full workspace rescans and reinstalls,
// while a lazy workspace updates incrementally (parent reloads, direct node
// inserts, stale-source drops) with a full rescan fallback. It ends in one
// refresh; the caller redraws.
func (a *App) refreshExplorerAfterMutation(minted, removed []string, renames map[string]string) {
	a.forgetExplorerDocs(removed, renames)

	ws := a.ws
	if ws == nil {
		a.refresh()
		return
	}

	if !ws.Lazy {
		if next, delta, err := rescanDiskWorkspace(ws.Root, ws, a.scanLimit); err == nil {
			a.installRescan(next, delta)
		}
	} else {
		parents := map[string]bool{}
		for _, rel := range minted {
			parents[dirName(rel)] = true
		}
		for _, rel := range removed {
			parents[dirName(rel)] = true
		}
		for old, next := range renames {
			parents[dirName(old)] = true
			parents[dirName(next)] = true
		}

		for parent := range parents {
			if parent != "" {
				_ = ws.loadDir(parent)
			}
		}

		for _, rel := range minted {
			if info, err := os.Stat(filepath.Join(ws.Root, filepath.FromSlash(rel))); err == nil && info.IsDir() {
				_ = ws.addDiskDir(rel)
				_ = ws.loadDir(rel)
			} else {
				_ = ws.addDiskFile(rel)
			}
		}

		for _, rel := range removed {
			ws.dropPath(rel)
		}
		for old := range renames {
			ws.dropPath(old)
		}

		sortPasteFiles(ws)
	}

	for _, rel := range minted {
		if dir := dirName(rel); dir != "" {
			a.folders[dir] = true
		}
	}

	a.refresh()
}

// explorerInputState is the inline new-file / new-folder / rename prompt.
// Mode is one of explorerInputNewFile, explorerInputNewFolder,
// explorerInputRename. Dir is the creation parent; Target is the rename
// source. Value is the live input; Error is the inline validation message.
type explorerInputState struct {
	Open   bool
	Mode   string
	Dir    string
	Target string
	Value  string
	Error  string
}

// explorerInputTitle is the prompt label the sidebar paints.
func (a *App) explorerInputTitle() string {
	switch a.explorerInput.Mode {
	case explorerInputNewFolder:
		if a.explorerInput.Dir == "" {
			return "New folder"
		}
		return "New folder in " + a.explorerInput.Dir
	case explorerInputRename:
		return "Rename " + a.explorerInput.Target
	default:
		if a.explorerInput.Dir == "" {
			return "New file"
		}
		return "New file in " + a.explorerInput.Dir
	}
}

// beginExplorerInput opens the inline name prompt. An empty dir is the root.
// The sample workspace gets a read-only toast and no prompt.
func (a *App) beginExplorerInput(ctx context.Context, mode, dir, target string) error {
	if a.ws == nil || a.ws.Sample {
		a.showToast("error", "Read-only sample", sampleReadOnlyMessage)
		a.refresh()

		if a.page == nil {
			return nil
		}

		return a.page.Redraw(ctx)
	}

	switch mode {
	case explorerInputNewFile, explorerInputNewFolder:
		if strings.TrimSpace(dir) != "" {
			clean, ok := cleanExplorerRel(dir)
			if !ok {
				a.showToast("error", "Cannot create", "The destination folder is not in the workspace.")
				a.refresh()

				if a.page == nil {
					return nil
				}

				return a.page.Redraw(ctx)
			}

			dir = clean
		}
	case explorerInputRename:
		clean, ok := cleanExplorerRel(target)
		if !ok {
			a.showToast("error", "Cannot rename", "Select a file in the explorer first.")
			a.refresh()

			if a.page == nil {
				return nil
			}

			return a.page.Redraw(ctx)
		}

		target = clean
	default:
		return nil
	}

	value := ""
	if mode == explorerInputRename {
		value = baseName(target)
	}

	a.explorerInput = explorerInputState{Open: true, Mode: mode, Dir: dir, Target: target, Value: value}
	a.refresh()

	if a.page == nil {
		return nil
	}

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	return a.page.Form().Focus(ctx, "explorer-input")
}

// commitExplorerInput validates the prompt and runs the op. Validation and
// op errors stay inline (message + error toast) without touching the disk;
// success toasts, refreshes the tree, and focuses the new row.
func (a *App) commitExplorerInput(ctx context.Context) error {
	if !a.explorerInput.Open {
		return nil
	}

	mode := a.explorerInput.Mode
	dir := a.explorerInput.Dir
	target := a.explorerInput.Target
	name := a.explorerInput.Value

	if err := validateExplorerName(name); err != nil {
		a.explorerInput.Error = err.Error()
		a.showToast("error", "Invalid name", err.Error())
		a.refresh()

		if a.page == nil {
			return nil
		}

		if err := a.page.Redraw(ctx); err != nil {
			return err
		}

		return a.page.Form().Focus(ctx, "explorer-input")
	}

	fail := func(title string, err error) error {
		a.explorerInput.Error = err.Error()
		a.showToast("error", title, err.Error())
		a.refresh()

		if a.page == nil {
			return nil
		}

		if err := a.page.Redraw(ctx); err != nil {
			return err
		}

		return a.page.Form().Focus(ctx, "explorer-input")
	}

	switch mode {
	case explorerInputNewFile:
		rel, err := a.createExplorerFile(dir, name)
		if err != nil {
			return fail("Cannot create file", err)
		}

		a.explorerInput = explorerInputState{}
		a.refreshExplorerAfterMutation([]string{rel}, nil, nil)
		a.showToast("success", "File created", rel)
		a.refresh()

		if a.page == nil {
			return nil
		}

		if err := a.page.Redraw(ctx); err != nil {
			return err
		}

		return a.page.Form().Focus(ctx, "tree-"+rel)
	case explorerInputNewFolder:
		rel, err := a.createExplorerDir(dir, name)
		if err != nil {
			return fail("Cannot create folder", err)
		}

		a.folders[rel] = true
		a.explorerInput = explorerInputState{}
		a.refreshExplorerAfterMutation([]string{rel}, nil, nil)
		a.showToast("success", "Folder created", rel)
		a.refresh()

		if a.page == nil {
			return nil
		}

		if err := a.page.Redraw(ctx); err != nil {
			return err
		}

		return a.page.Form().Focus(ctx, "tree-"+rel)
	case explorerInputRename:
		next, err := a.renameExplorerPath(target, name)
		if err != nil {
			return fail("Cannot rename", err)
		}

		a.explorerInput = explorerInputState{}
		a.refreshExplorerAfterMutation([]string{next}, nil, map[string]string{target: next})
		a.showToast("success", "Renamed", next)
		a.refresh()

		if a.page == nil {
			return nil
		}

		if err := a.page.Redraw(ctx); err != nil {
			return err
		}

		return a.page.Form().Focus(ctx, "tree-"+next)
	}

	return nil
}

// cancelExplorerInput closes the prompt without acting and returns focus to
// the rename target when there is one.
func (a *App) cancelExplorerInput(ctx context.Context) error {
	if !a.explorerInput.Open {
		return nil
	}

	target := a.explorerInput.Target
	a.explorerInput = explorerInputState{}
	a.refresh()

	if a.page == nil {
		return nil
	}

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	if target != "" {
		return a.page.Form().Focus(ctx, "tree-"+target)
	}

	return a.page.Form().Focus(ctx, "")
}

// explorerOpReady reports whether explorer file ops may run: no overlay or
// text field owns the keys, no edit or prompt is live, and the focus is a
// tree row or rows are already selected.
func (a *App) explorerOpReady() bool {
	if a.explorerInput.Open || a.confirmOpen || a.editing {
		return false
	}

	if !a.pasteTargetReady() {
		return false
	}

	if len(a.explSel) > 0 {
		return true
	}

	if a.page == nil {
		return false
	}

	return isTreeRowID(a.page.Form().FocusID())
}

// explorerNewDir maps a menu row to the folder a new file lands in: a
// directory stays, a file maps to its parent, and "" is the root.
func (a *App) explorerNewDir(rel string) string {
	if strings.TrimSpace(rel) == "" {
		return ""
	}

	clean, ok := cleanExplorerRel(rel)
	if !ok {
		return ""
	}

	if isDir, found := a.lookupTreePath(clean); found {
		if isDir {
			return clean
		}

		return dirName(clean)
	}

	return ""
}

// explorerOpTarget is the single row rename and duplicate act on: the focused
// tree row, else the active document when it is in the workspace.
func (a *App) explorerOpTarget() string {
	if a.page != nil {
		if id := a.page.Form().FocusID(); isTreeRowID(id) {
			if rel := strings.TrimPrefix(id, "tree-"); rel != "" {
				if clean, ok := cleanExplorerRel(rel); ok {
					if _, found := a.lookupTreePath(clean); found {
						return clean
					}
				}
			}
		}
	}

	if doc := strings.TrimSpace(a.activeDocPath()); doc != "" {
		if clean, ok := cleanExplorerRel(doc); ok {
			if _, found := a.lookupTreePath(clean); found {
				return clean
			}
		}
	}

	return ""
}

// explorerDeleteTargets resolves what Delete acts on: the menu row (with the
// multi-select when the row belongs to it), else the multi-select, else the
// single op target.
func (a *App) explorerDeleteTargets(rel string) []string {
	if strings.TrimSpace(rel) != "" {
		if clean, ok := cleanExplorerRel(rel); ok {
			return a.copyTargets(clean)
		}

		return nil
	}

	if len(a.explSel) > 0 {
		targets := make([]string, 0, len(a.explSel))
		for path := range a.explSel {
			targets = append(targets, path)
		}

		sort.Strings(targets)

		return targets
	}

	if target := a.explorerOpTarget(); target != "" {
		return []string{target}
	}

	return nil
}

// openExplorerDeleteConfirm shows the delete dialog for validated explorer
// paths. The confirm button reads Delete; the dialog closes without acting
// on cancel.
func (a *App) openExplorerDeleteConfirm(rels []string) {
	if a.ws == nil || a.ws.Sample {
		a.showToast("error", "Cannot delete", sampleReadOnlyMessage)
		a.refresh()

		return
	}

	unique := make([]string, 0, len(rels))
	seen := map[string]bool{}

	for _, rel := range rels {
		clean, ok := cleanExplorerRel(rel)
		if !ok || seen[clean] {
			continue
		}

		seen[clean] = true

		if _, found := a.lookupTreePath(clean); !found {
			if _, err := os.Stat(filepath.Join(a.ws.Root, filepath.FromSlash(clean))); err != nil {
				continue
			}
		}

		unique = append(unique, clean)
	}

	if len(unique) == 0 {
		return
	}

	sort.Strings(unique)

	a.confirmOpen = true
	a.confirmKind = confirmExplorerDelete
	a.confirmPaths = unique
	a.confirmTitle = fmt.Sprintf("Delete %d file(s)?", len(unique))
	a.confirmMsg = "This deletes from disk and cannot be undone."
	a.refresh()
}

// deleteExplorerConfirmed removes the confirmed paths, refreshes the tree,
// and toasts the result. The dialog already closed.
func (a *App) deleteExplorerConfirmed(ctx context.Context, paths []string) error {
	deleted, err := a.deleteExplorerPaths(paths)
	if len(deleted) > 0 {
		a.refreshExplorerAfterMutation(nil, deleted, nil)
	}

	switch {
	case len(deleted) == 1:
		a.showToast("success", "Deleted", deleted[0])
	case len(deleted) > 1:
		a.showToast("success", fmt.Sprintf("%d files deleted", len(deleted)), strings.Join(deleted, ", "))
	case err != nil:
		a.showToast("error", "Cannot delete", err.Error())
	default:
		a.showToast("info", "Nothing deleted", "The files were already gone.")
	}

	if err != nil && len(deleted) == 0 {
		a.refresh()

		return nil
	}

	a.refresh()

	if a.page == nil {
		return nil
	}

	return a.page.Form().Focus(ctx, "")
}

// paletteCommandCatalog is the `>` command mode list.
func paletteCommandCatalog() []paletteRow {
	return []paletteRow{
		{Cmd: paletteCmdNewFile, Name: "File: New File", Dir: "Create an empty file"},
		{Cmd: paletteCmdNewFolder, Name: "File: New Folder", Dir: "Create a folder"},
		{Cmd: paletteCmdRename, Name: "File: Rename", Dir: "Rename the selected file"},
		{Cmd: paletteCmdDelete, Name: "File: Delete", Dir: "Delete the selected files"},
		{Cmd: paletteCmdDuplicate, Name: "File: Duplicate", Dir: "Duplicate the selected file"},
		{Cmd: paletteCmdPinTab, Name: "Tab: Pin/Unpin", Dir: "Pin or unpin the focused tab (Ctrl+K)"},
		{Cmd: paletteCmdReopenTab, Name: "Tab: Reopen Closed Tab", Dir: "Reopen the last closed tab (Ctrl+Shift+T)"},
		{Cmd: paletteCmdCloseOthers, Name: "Tab: Close Others", Dir: "Close other tabs, keeping pinned"},
		{Cmd: paletteCmdCloseLeft, Name: "Tab: Close to the Left", Dir: "Close tabs left of the active one"},
		{Cmd: paletteCmdCloseRight, Name: "Tab: Close to the Right", Dir: "Close tabs right of the active one"},
		{Cmd: paletteCmdCloseAllTabs, Name: "Tab: Close All", Dir: "Close every tab in the focused group"},
		{Cmd: paletteCmdGotoDefinition, Name: "Go: Go to Definition", Dir: "Jump to the definition at the caret (Ctrl+D)"},
	}
}

// paletteCommandRows filters the command catalog by the text after `>`.
func (a *App) paletteCommandRows() []paletteRow {
	query := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(a.paletteQuery), ">")))

	rows := make([]paletteRow, 0, len(paletteCommandCatalog()))

	for _, row := range paletteCommandCatalog() {
		if query != "" && !strings.Contains(strings.ToLower(row.Name), query) {
			continue
		}

		rows = append(rows, row)
	}

	return rows
}

// isPaletteCommandMode reports whether the palette query opens command mode.
func (a *App) isPaletteCommandMode() bool {
	return strings.HasPrefix(strings.TrimSpace(a.paletteQuery), ">")
}

// runPaletteCommand dispatches one `>` palette command. The target follows
// the paste precedence: the focused tree row, else the active document's
// directory, else the root.
func (a *App) runPaletteCommand(ctx context.Context, id string) error {
	closePalette := func() {
		a.paletteOpen = false
		a.paletteQuery = ""
		a.paletteIndex = 0
	}

	redraw := func() error {
		a.refresh()

		if a.page == nil {
			return nil
		}

		return a.page.Redraw(ctx)
	}

	switch strings.TrimSpace(id) {
	case paletteCmdNewFile, paletteCmdNewFolder:
		mode := explorerInputNewFile
		if strings.TrimSpace(id) == paletteCmdNewFolder {
			mode = explorerInputNewFolder
		}

		closePalette()

		return a.beginExplorerInput(ctx, mode, a.ResolvePasteDir(""), "")
	case paletteCmdRename:
		target := a.explorerOpTarget()
		if target == "" {
			closePalette()
			a.showToast("info", "Nothing to rename", "Select a file in the explorer first.")

			return redraw()
		}

		closePalette()

		return a.beginExplorerInput(ctx, explorerInputRename, "", target)
	case paletteCmdDelete:
		targets := a.explorerDeleteTargets("")
		if len(targets) == 0 {
			closePalette()
			a.showToast("info", "Nothing to delete", "Select a file in the explorer first.")

			return redraw()
		}

		closePalette()
		a.openExplorerDeleteConfirm(targets)

		return redraw()
	case paletteCmdDuplicate:
		target := a.explorerOpTarget()
		if target == "" {
			closePalette()
			a.showToast("info", "Nothing to duplicate", "Select a file in the explorer first.")

			return redraw()
		}

		next, err := a.duplicateExplorerPath(target)
		if err != nil {
			closePalette()
			a.showToast("error", "Cannot duplicate", err.Error())

			return redraw()
		}

		closePalette()
		a.refreshExplorerAfterMutation([]string{next}, nil, nil)
		a.showToast("success", "Duplicated", next)

		return redraw()
	case paletteCmdPinTab:
		closePalette()
		a.togglePinFocused()

		return redraw()
	case paletteCmdGotoDefinition:
		closePalette()

		return a.gotoDefinitionAtCaret(ctx)
	case paletteCmdReopenTab:
		closePalette()
		a.reopenClosedTab()

		return redraw()
	case paletteCmdCloseOthers, paletteCmdCloseLeft, paletteCmdCloseRight, paletteCmdCloseAllTabs:
		group := a.focusGroup
		if !a.split {
			group = 0
		}

		tabs := a.groupTabs(group)
		active := a.groupActive(group)
		if active < 0 || active >= len(tabs) {
			closePalette()
			a.showToast("info", "No tabs", "Open a file before closing tabs.")

			return redraw()
		}

		var keep func(int, string) bool

		includePinned := false

		switch strings.TrimSpace(id) {
		case paletteCmdCloseOthers:
			keep = func(i int, _ string) bool { return i == active }
		case paletteCmdCloseLeft:
			keep = func(i int, _ string) bool { return i >= active }
		case paletteCmdCloseRight:
			keep = func(i int, _ string) bool { return i <= active }
		default:
			keep = func(int, string) bool { return false }
			includePinned = true
		}

		closePalette()

		// paletteBulkClose gates dirty last-holders on the unsaved dialog,
		// then runs closeTabsIn (pinned tabs stay skipped without prompting).
		if err := a.paletteBulkClose(group, keep, includePinned); err != nil {
			return err
		}

		return redraw()
	}

	return nil
}

// duplicateExplorerTarget duplicates one row for the context menu, with
// toasts and a tree refresh.
func (a *App) duplicateExplorerTarget(ctx context.Context, rel string) error {
	clean, ok := cleanExplorerRel(rel)
	if !ok {
		return a.redrawTreeMenu(ctx)
	}

	next, err := a.duplicateExplorerPath(clean)
	if err != nil {
		a.showToast("error", "Cannot duplicate", err.Error())
		a.refresh()

		return a.redrawTreeMenu(ctx)
	}

	a.refreshExplorerAfterMutation([]string{next}, nil, nil)
	a.showToast("success", "Duplicated", next)
	a.refresh()

	return a.redrawTreeMenu(ctx)
}
