package app

import (
	"context"
	"strconv"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/merge"
	"github.com/chinmay-sawant/ownframe"
)

// contextMenuRow mirrors the window's context-menu row. The host row type
// lives in an internal package, so the page bridge converts this shape at the
// boundary.
type contextMenuRow struct {
	ID    string
	Label string
	// Hint is a right-aligned shortcut label such as Ctrl+C.
	Hint    string
	Enabled bool
}

const (
	menuWindowControlWidth = 196
	menuWindowControlRow   = 26
	menuWindowControlPad   = 8

	contextMenuIDPrefix     = "code-view:"
	contextMenuCloseID      = contextMenuIDPrefix + "tab-close:"
	contextMenuCloseOtherID = contextMenuIDPrefix + "tab-close-others:"
	contextMenuCloseAllID   = contextMenuIDPrefix + "tab-close-all:"
	contextMenuCloseLeftID  = contextMenuIDPrefix + "tab-close-left:"
	contextMenuCloseRightID = contextMenuIDPrefix + "tab-close-right:"
	contextMenuPinID        = contextMenuIDPrefix + "tab-pin:"
	contextMenuUnpinID      = contextMenuIDPrefix + "tab-unpin:"
	contextMenuReopenID     = contextMenuIDPrefix + "tab-reopen"

	contextMenuDiscardFileID = contextMenuIDPrefix + "discard-file:"
	contextMenuStageFileID   = contextMenuIDPrefix + "stage-file:"
	contextMenuUnstageFileID = contextMenuIDPrefix + "unstage-file:"
	contextMenuStageHunkID   = contextMenuIDPrefix + "stage-hunk:"
	contextMenuRevertHunkID  = contextMenuIDPrefix + "revert-hunk:"
	contextMenuDiscardAllID  = contextMenuIDPrefix + "discard-all"
	contextMenuStageAllID    = contextMenuIDPrefix + "stage-all"
	contextMenuUnstageAllID  = contextMenuIDPrefix + "unstage-all"
	contextMenuStageSelID    = contextMenuIDPrefix + "stage-selected"
	contextMenuUnstageSelID  = contextMenuIDPrefix + "unstage-selected"
	contextMenuDiscardSelID  = contextMenuIDPrefix + "discard-selected"
	contextMenuCopyMsgID     = contextMenuIDPrefix + "copy-message:"
	contextMenuCopyIDID      = contextMenuIDPrefix + "copy-id:"
	contextMenuCopyPathID    = contextMenuIDPrefix + "copy-path:"
	contextMenuCopyAbsID     = contextMenuIDPrefix + "copy-abs:"
	contextMenuCutID         = contextMenuIDPrefix + "cut:"
	contextMenuCopyID        = contextMenuIDPrefix + "copy:"
	contextMenuPasteID       = contextMenuIDPrefix + "paste:"
	contextMenuSplitCloseID  = contextMenuIDPrefix + "split-close:"

	// Merge conflict rows in Source Control and merge-editor actions.
	contextMenuMergeOpenID    = contextMenuIDPrefix + "merge-open:"
	contextMenuMergeStageID   = contextMenuIDPrefix + "merge-stage:"
	contextMenuMergeDiscardID = contextMenuIDPrefix + "merge-discard:"
	contextMenuMergeOursID    = contextMenuIDPrefix + "merge-accept-ours:"
	contextMenuMergeTheirsID  = contextMenuIDPrefix + "merge-accept-theirs:"
	contextMenuMergeBothID    = contextMenuIDPrefix + "merge-accept-both:"
	contextMenuMergeCompareID = contextMenuIDPrefix + "merge-compare"
)

// presentMenu paints the right-click rows in the page instead of the shell menu.
func (a *App) presentMenu(x, y int, items []ownframe.MenuItem) bool {
	if a == nil || a.page == nil || len(items) == 0 {
		return false
	}

	rows := make([]menuViewRow, 0, len(items))
	for i, item := range items {
		rows = append(rows, menuViewRow{
			Index:   i,
			ID:      item.ID,
			Label:   item.Label,
			Hint:    item.Hint,
			Enabled: item.Enabled,
		})
	}

	w, h := a.page.Size()
	menuH := a.zx(menuWindowControlPad) + len(rows)*a.zx(menuWindowControlRow)
	if x+a.zx(menuWindowControlWidth) > w {
		x = w - a.zx(menuWindowControlWidth)
	}

	if y+menuH > h {
		y = h - menuH
	}

	if x < 0 {
		x = 0
	}

	if y < 0 {
		y = 0
	}

	a.menuOpen = true
	a.menuX = x
	a.menuY = y
	a.menuItems = rows
	a.refresh()

	if err := a.page.Redraw(context.Background()); err != nil {
		return false
	}

	return true
}

// closeMenu hides the page menu. The caller redraws.
func (a *App) closeMenu() {
	a.menuOpen = false
	a.menuItems = nil
}

// dismissOpenMenu closes the menu when the pointer is outside its rows.
// A row still runs. The caller redraws.
func (a *App) dismissOpenMenu(id, action string) bool {
	if a == nil || !a.menuOpen {
		return false
	}

	if strings.HasPrefix(id, "ctx-") || strings.HasPrefix(action, "menu-item:") {
		return false
	}

	a.closeMenu()
	a.refresh()

	return true
}

// runMenuItem closes the menu and runs one row. Built-in edit rows use the
// same buffer methods the shell menu used.
func (a *App) runMenuItem(ctx context.Context, id string) error {
	a.closeMenu()

	switch id {
	case "cut":
		text, ok, err := a.onCut(ctx)
		if err != nil || !ok {
			return err
		}

		ownframe.Write(text)
		a.refresh()

		return a.page.Redraw(ctx)
	case "copy":
		text, ok, err := a.onCopy(ctx)
		if err != nil || !ok {
			return err
		}

		ownframe.Write(text)
		a.refresh()

		return a.page.Redraw(ctx)
	case "paste":
		text, err := ownframe.Read()
		if err != nil {
			a.refresh()

			return a.page.Redraw(ctx)
		}

		if err := a.onPaste(ctx, text); err != nil {
			return err
		}

		return a.page.Redraw(ctx)
	case "select-all":
		if err := a.onSelectAll(ctx); err != nil {
			return err
		}

		return a.page.Redraw(ctx)
	case "undo":
		if err := a.onUndo(ctx); err != nil {
			return err
		}

		return a.page.Redraw(ctx)
	case "redo":
		if err := a.onRedo(ctx); err != nil {
			return err
		}

		return a.page.Redraw(ctx)
	default:
		return a.MenuAction(ctx, id)
	}
}

// ContextMenuFor returns actions for the editor, the tab, the working tree,
// or the history rail under the pointer.
func (a *App) ContextMenuFor(id string) []contextMenuRow {
	if id == "code-body" && a.editing && a.edit != nil {
		selected := a.editCaret != a.editAnchor
		return []contextMenuRow{
			{ID: "cut", Label: "Cut", Enabled: selected},
			{ID: "copy", Label: "Copy", Enabled: selected},
			{ID: "paste", Label: "Paste", Enabled: true},
			{ID: "select-all", Label: "Select All", Enabled: true},
			{ID: "undo", Label: "Undo", Enabled: len(a.editUndo) > 0},
			{ID: "redo", Label: "Redo", Enabled: len(a.editRedo) > 0},
		}
	}
	if rows, ok := a.mergeContextMenu(id); ok {
		return rows
	}
	if rows, ok := a.splitContextMenu(id); ok {
		return rows
	}
	if rows, ok := a.contextMenuForExplorer(id); ok {
		return rows
	}
	if rows, ok := a.scmContextMenu(id); ok {
		return rows
	}
	if rows, ok := a.hunkContextMenu(id); ok {
		return rows
	}
	if rows, ok := a.historyContextMenu(id); ok {
		return rows
	}
	group, index, ok := contextMenuTabID(id)
	if !ok {
		return nil
	}

	tabs := a.tabs
	if group == 1 {
		tabs = a.tabs2
	}

	if index < 0 || index >= len(tabs) {
		return nil
	}

	path := tabs[index]
	pinned := a.isPinned(group, path)

	rows := make([]contextMenuRow, 0, 7)
	if pinned {
		rows = append(rows, contextMenuRow{ID: contextMenuUnpinID + contextMenuTabTarget(group, index), Label: "Unpin Tab", Hint: "Ctrl+K", Enabled: true})
	} else {
		rows = append(rows, contextMenuRow{ID: contextMenuPinID + contextMenuTabTarget(group, index), Label: "Pin Tab", Hint: "Ctrl+K", Enabled: true})
	}

	rows = append(rows,
		contextMenuRow{ID: contextMenuCloseID + contextMenuTabTarget(group, index), Label: "Close", Enabled: true},
		contextMenuRow{ID: contextMenuCloseOtherID + contextMenuTabTarget(group, index), Label: "Close Others", Enabled: len(tabs) > 1},
		contextMenuRow{ID: contextMenuCloseLeftID + contextMenuTabTarget(group, index), Label: "Close All to the Left", Enabled: index > 0},
		contextMenuRow{
			ID:      contextMenuCloseRightID + contextMenuTabTarget(group, index),
			Label:   "Close All to the Right",
			Enabled: index < len(tabs)-1,
		},
		contextMenuRow{ID: contextMenuCloseAllID + strconv.Itoa(group), Label: "Close All", Enabled: len(tabs) > 0},
		contextMenuRow{ID: contextMenuReopenID, Label: "Reopen Closed Tab", Hint: "Ctrl+Shift+T", Enabled: len(a.closedTabs) > 0},
	)

	return rows
}

// mergeContextMenu returns the actions for one conflict header or action row
// in the merge editor.
func (a *App) mergeContextMenu(id string) ([]contextMenuRow, bool) {
	if a.mode != "merge" || !a.mergeOpen {
		return nil, false
	}

	var rest string

	for _, prefix := range []string{
		"merge-c-", "merge-actions-", "merge-side-",
		"merge-ours-", "merge-theirs-", "merge-both-", "merge-compare-",
	} {
		if strings.HasPrefix(id, prefix) {
			rest = strings.TrimPrefix(id, prefix)

			break
		}
	}

	if rest == "" {
		return nil, false
	}

	if cut := strings.IndexByte(rest, '-'); cut >= 0 {
		rest = rest[:cut]
	}

	index, ok := contextMenuIndex(rest)
	if !ok || index >= len(a.mergeConflicts) {
		return nil, false
	}

	suffix := strconv.Itoa(index)

	return []contextMenuRow{
		{ID: contextMenuMergeOursID + suffix, Label: "Accept Current", Enabled: true},
		{ID: contextMenuMergeTheirsID + suffix, Label: "Accept Incoming", Enabled: true},
		{ID: contextMenuMergeBothID + suffix, Label: "Accept Both", Enabled: true},
		{ID: contextMenuMergeCompareID, Label: "Compare Base", Enabled: true},
	}, true
}

// splitContextMenu returns close-pane rows for the editor tab strips and
// panes while a split is open. Merge keeps the closing side's tabs in the
// survivor; drop discards them (blocked by dirty tabs with a toast). Tab ids
// keep the tab menu untouched, so this only matches pane and strip ids.
func (a *App) splitContextMenu(id string) ([]contextMenuRow, bool) {
	if !a.split || a.mode != "explorer" {
		return nil, false
	}

	group := -1

	switch id {
	case "tabstrip", "editor-window", "code-body", "crumbs":
		group = 0
	case "tabstrip-2", "editor-window-2", "code-body-2", "crumbs-2":
		group = 1
	default:
		return nil, false
	}

	other := 1 - group

	return []contextMenuRow{
		{ID: contextMenuSplitCloseID + strconv.Itoa(group) + ":merge", Label: "Close This Pane", Enabled: true},
		{ID: contextMenuSplitCloseID + strconv.Itoa(other) + ":merge", Label: "Close Other Pane", Enabled: true},
		{ID: contextMenuSplitCloseID + strconv.Itoa(group) + ":drop", Label: "Drop This Pane's Tabs", Enabled: true},
		{ID: contextMenuSplitCloseID + strconv.Itoa(other) + ":drop", Label: "Drop Other Pane's Tabs", Enabled: true},
	}, true
}

// parseSplitClose reads "<side>:<merge|drop>" from a split-close row.
func parseSplitClose(rest string) (int, splitCloseMode, bool) {
	sideText, modeText, ok := strings.Cut(rest, ":")
	if !ok {
		return 0, splitCloseMerge, false
	}

	side, ok := contextMenuGroup(sideText)
	if !ok {
		return 0, splitCloseMerge, false
	}

	switch modeText {
	case "merge":
		return side, splitCloseMerge, true
	case "drop":
		return side, splitCloseDrop, true
	}

	return 0, splitCloseMerge, false
}

// scmContextMenu returns working-tree rows. Per-file ids offer stage or
// unstage based on the section, plus discard; multi-select adds bulk rows.
func (a *App) scmContextMenu(id string) ([]contextMenuRow, bool) {
	if a.mode != "changes" && a.mode != "merge" {
		return nil, false
	}

	for _, prefix := range []string{"merge-conflict-", "merge-open-", "merge-stage-"} {
		if !strings.HasPrefix(id, prefix) {
			continue
		}

		path := strings.TrimPrefix(id, prefix)
		if path == "" || findFile(a.changes.Conflicts, path) == nil {
			return nil, false
		}

		return []contextMenuRow{
			{ID: contextMenuMergeOpenID + path, Label: "Resolve Conflicts", Enabled: true},
			{ID: contextMenuMergeStageID + path, Label: "Stage Conflict", Enabled: true},
			{ID: contextMenuMergeDiscardID + path, Label: "Discard Changes", Enabled: true},
		}, true
	}

	for _, prefix := range []string{"discard-staged-", "wrap-staged-", "change-", "staged-", "stage-", "discard-", "unstage-", "wrap-"} {
		if strings.HasPrefix(id, prefix) {
			path := strings.TrimPrefix(id, prefix)
			if path == "" {
				return nil, false
			}

			if _, ok := a.scmChangeFile(path); !ok {
				return nil, false
			}

			staged := a.isStaged(path)
			selCount := a.selectedCount()
			selActive := selCount > 1 && a.selected[path]

			var rows []contextMenuRow
			if staged {
				rows = append(rows, contextMenuRow{ID: contextMenuUnstageFileID + path, Label: "Unstage Changes", Enabled: true})
			} else {
				rows = append(rows, contextMenuRow{ID: contextMenuStageFileID + path, Label: "Stage Changes", Enabled: true})
			}

			rows = append(rows, contextMenuRow{ID: contextMenuDiscardFileID + path, Label: "Discard Changes", Enabled: true})

			if selActive {
				rows = append(rows,
					contextMenuRow{ID: contextMenuStageSelID, Label: "Stage Selected", Enabled: true},
					contextMenuRow{ID: contextMenuUnstageSelID, Label: "Unstage Selected", Enabled: true},
					contextMenuRow{ID: contextMenuDiscardSelID, Label: "Discard Selected", Enabled: true},
				)
			}

			hasChanges := len(a.changes.Files) > 0
			rows = append(rows,
				contextMenuRow{ID: contextMenuStageAllID, Label: "Stage All", Enabled: hasChanges},
			)

			if len(a.changes.Staged) > 0 {
				rows = append(rows, contextMenuRow{ID: contextMenuUnstageAllID, Label: "Unstage All", Enabled: true})
			}

			rows = append(rows, contextMenuRow{ID: contextMenuDiscardAllID, Label: "Discard All", Enabled: hasChanges})

			return rows, true
		}
	}

	switch id {
	case "changes-list", "changes-head", "changes-staged-head", "changes-unstaged-head", "history-working", "changes-stage-all", "changes-discard-all", "changes-unstage-all", "sidebar":
		hasChanges := len(a.changes.Files) > 0
		if !hasChanges {
			return nil, false
		}

		var rows []contextMenuRow
		if a.selectedCount() > 1 {
			rows = append(rows,
				contextMenuRow{ID: contextMenuStageSelID, Label: "Stage Selected", Enabled: true},
				contextMenuRow{ID: contextMenuUnstageSelID, Label: "Unstage Selected", Enabled: true},
				contextMenuRow{ID: contextMenuDiscardSelID, Label: "Discard Selected", Enabled: true},
			)
		}

		rows = append(rows, contextMenuRow{ID: contextMenuStageAllID, Label: "Stage All", Enabled: true})

		if len(a.changes.Staged) > 0 {
			rows = append(rows, contextMenuRow{ID: contextMenuUnstageAllID, Label: "Unstage All", Enabled: true})
		}

		rows = append(rows, contextMenuRow{ID: contextMenuDiscardAllID, Label: "Discard All", Enabled: true})

		return rows, true
	}

	return nil, false
}

// hunkContextMenu returns the per-hunk rows for a diff revert button: stage
// the block next to discarding it. Binary, oversized, and renamed files
// carry no revert button, so they never reach this menu; untracked and
// conflicted files reach stageHunk through the row and get its explicit
// note instead of a guessed stage.
func (a *App) hunkContextMenu(id string) ([]contextMenuRow, bool) {
	if a.mode != "changes" || a.diffSource != "working" {
		return nil, false
	}

	rest, ok := strings.CutPrefix(id, "revert-line-")
	if !ok {
		return nil, false
	}

	index, _, _, ok := parseHunkMenuRest(rest)
	if !ok {
		return nil, false
	}

	file := findFile(a.changes.Files, a.diffPath)
	if file == nil || index < 0 || index >= len(file.Hunks) {
		return nil, false
	}

	if file.Binary || file.Oversized || file.Status == "R" {
		return nil, false
	}

	return []contextMenuRow{
		{ID: contextMenuStageHunkID + rest, Label: "Stage Hunk", Enabled: true},
		{ID: contextMenuRevertHunkID + rest, Label: "Discard Hunk", Enabled: true},
	}, true
}

// historyContextMenu returns commit rows. Each commit offers its message and
// ID on the clipboard. It never returns the working-tree rows.
func (a *App) historyContextMenu(id string) ([]contextMenuRow, bool) {
	if a.mode != "changes" {
		return nil, false
	}

	if !strings.HasPrefix(id, "commit-") ||
		strings.HasPrefix(id, "commit-file-") ||
		strings.HasPrefix(id, "commit-open-") {
		return nil, false
	}

	short := strings.TrimPrefix(id, "commit-")
	if short == "" {
		return nil, false
	}

	hash := ""
	for i := range a.commits.Commits {
		if a.commits.Commits[i].Short == short {
			hash = a.commits.Commits[i].Hash

			break
		}
	}

	if hash == "" {
		return nil, false
	}

	return []contextMenuRow{
		{ID: contextMenuCopyMsgID + hash, Label: "Copy Message", Enabled: true},
		{ID: contextMenuCopyIDID + hash, Label: "Copy Commit ID", Enabled: true},
	}, true
}

// MenuAction handles tab-closing, working-tree, and history actions.
func (a *App) MenuAction(ctx context.Context, id string) error {
	switch {
	case strings.HasPrefix(id, contextMenuMergeOpenID):
		a.openMergeConflict(ctx, strings.TrimPrefix(id, contextMenuMergeOpenID))
		return a.redrawMenuAction(ctx)
	case strings.HasPrefix(id, contextMenuMergeStageID):
		a.stageConflictPath(ctx, strings.TrimPrefix(id, contextMenuMergeStageID))
		return a.redrawMenuAction(ctx)
	case strings.HasPrefix(id, contextMenuMergeDiscardID):
		a.openDiscardConfirm("discard-file", []string{strings.TrimPrefix(id, contextMenuMergeDiscardID)})
		return a.redrawMenuAction(ctx)
	case strings.HasPrefix(id, contextMenuMergeOursID):
		if index, ok := contextMenuIndex(strings.TrimPrefix(id, contextMenuMergeOursID)); ok {
			a.mergeAccept(merge.Ours, index)
		}
		return a.redrawMenuAction(ctx)
	case strings.HasPrefix(id, contextMenuMergeTheirsID):
		if index, ok := contextMenuIndex(strings.TrimPrefix(id, contextMenuMergeTheirsID)); ok {
			a.mergeAccept(merge.Theirs, index)
		}
		return a.redrawMenuAction(ctx)
	case strings.HasPrefix(id, contextMenuMergeBothID):
		if index, ok := contextMenuIndex(strings.TrimPrefix(id, contextMenuMergeBothID)); ok {
			a.mergeAccept(merge.Both, index)
		}
		return a.redrawMenuAction(ctx)
	case id == contextMenuMergeCompareID:
		a.toggleMergeCompare()
		return a.redrawMenuAction(ctx)
	case strings.HasPrefix(id, contextMenuDiscardFileID):
		path := strings.TrimPrefix(id, contextMenuDiscardFileID)
		a.openDiscardConfirm("discard-file", []string{path})

		if a.page == nil {
			return nil
		}

		return a.page.Redraw(ctx)
	case strings.HasPrefix(id, contextMenuStageHunkID):
		if index, oldNo, newNo, ok := parseHunkMenuRest(strings.TrimPrefix(id, contextMenuStageHunkID)); ok {
			if sel, found := a.hunkBlockSelection(index, oldNo, newNo); found {
				if err := a.stageHunk(ctx, index, sel); err != nil {
					return err
				}
			}
		}

		return a.redrawMenuAction(ctx)
	case strings.HasPrefix(id, contextMenuRevertHunkID):
		if index, oldNo, newNo, ok := parseHunkMenuRest(strings.TrimPrefix(id, contextMenuRevertHunkID)); ok {
			if sel, found := a.hunkBlockSelection(index, oldNo, newNo); found {
				if err := a.revertHunk(ctx, index, sel); err != nil {
					return err
				}
			}
		}

		return a.redrawMenuAction(ctx)
	case strings.HasPrefix(id, contextMenuStageFileID):
		path := strings.TrimPrefix(id, contextMenuStageFileID)
		if err := a.stageFile(ctx, path); err != nil {
			return err
		}

		if a.page == nil {
			return nil
		}

		return a.page.Redraw(ctx)
	case strings.HasPrefix(id, contextMenuUnstageFileID):
		path := strings.TrimPrefix(id, contextMenuUnstageFileID)
		if err := a.unstageFile(ctx, path); err != nil {
			return err
		}

		if a.page == nil {
			return nil
		}

		return a.page.Redraw(ctx)
	case id == contextMenuDiscardAllID:
		a.openDiscardConfirm("discard-all", a.workingAllPaths())

		if a.page == nil {
			return nil
		}

		return a.page.Redraw(ctx)
	case id == contextMenuStageAllID:
		if err := a.stageAll(ctx); err != nil {
			return err
		}

		if a.page == nil {
			return nil
		}

		return a.page.Redraw(ctx)
	case id == contextMenuUnstageAllID:
		if err := a.unstageAll(ctx); err != nil {
			return err
		}

		if a.page == nil {
			return nil
		}

		return a.page.Redraw(ctx)
	case id == contextMenuStageSelID:
		if err := a.stageSelected(ctx); err != nil {
			return err
		}

		if a.page == nil {
			return nil
		}

		return a.page.Redraw(ctx)
	case id == contextMenuUnstageSelID:
		if err := a.unstageSelected(ctx); err != nil {
			return err
		}

		if a.page == nil {
			return nil
		}

		return a.page.Redraw(ctx)
	case id == contextMenuDiscardSelID:
		a.openDiscardConfirm("discard-selected", a.selectedList())

		if a.page == nil {
			return nil
		}

		return a.page.Redraw(ctx)
	case strings.HasPrefix(id, contextMenuCopyMsgID):
		a.copyCommitMessage(strings.TrimPrefix(id, contextMenuCopyMsgID))
		if a.page == nil {
			return nil
		}

		return a.page.Redraw(ctx)
	case strings.HasPrefix(id, contextMenuCopyIDID):
		a.copyCommitID(strings.TrimPrefix(id, contextMenuCopyIDID))
		if a.page == nil {
			return nil
		}

		return a.page.Redraw(ctx)
	case strings.HasPrefix(id, contextMenuCopyPathID):
		a.copyTreePath(strings.TrimPrefix(id, contextMenuCopyPathID))

		return a.redrawTreeMenu(ctx)
	case strings.HasPrefix(id, contextMenuCopyAbsID):
		a.copyTreeAbsPath(strings.TrimPrefix(id, contextMenuCopyAbsID))

		return a.redrawTreeMenu(ctx)
	case strings.HasPrefix(id, contextMenuCutID):
		a.stageTreeCopy(a.copyTargets(strings.TrimPrefix(id, contextMenuCutID)), true)

		return a.redrawTreeMenu(ctx)
	case strings.HasPrefix(id, contextMenuCopyID):
		a.stageTreeCopy(a.copyTargets(strings.TrimPrefix(id, contextMenuCopyID)), false)

		return a.redrawTreeMenu(ctx)
	case strings.HasPrefix(id, contextMenuPasteID):
		return a.pasteTreeCopy(ctx, strings.TrimPrefix(id, contextMenuPasteID))
	case strings.HasPrefix(id, contextMenuNewFileID):
		if err := a.beginExplorerInput(ctx, explorerInputNewFile, strings.TrimPrefix(id, contextMenuNewFileID), ""); err != nil {
			return err
		}

		return a.redrawTreeMenu(ctx)
	case strings.HasPrefix(id, contextMenuNewFolderID):
		if err := a.beginExplorerInput(ctx, explorerInputNewFolder, strings.TrimPrefix(id, contextMenuNewFolderID), ""); err != nil {
			return err
		}

		return a.redrawTreeMenu(ctx)
	case strings.HasPrefix(id, contextMenuDuplicateID):
		rel := strings.TrimPrefix(id, contextMenuDuplicateID)

		next, err := a.duplicateExplorerPath(rel)
		if err != nil {
			a.showToast("error", "Cannot duplicate", err.Error())

			return a.redrawTreeMenu(ctx)
		}

		a.refreshExplorerAfterMutation([]string{next}, nil, nil)
		a.showToast("success", "Duplicated", next)

		return a.redrawTreeMenu(ctx)
	case strings.HasPrefix(id, contextMenuRenameID):
		if err := a.beginExplorerInput(ctx, explorerInputRename, "", strings.TrimPrefix(id, contextMenuRenameID)); err != nil {
			return err
		}

		return a.redrawTreeMenu(ctx)
	case strings.HasPrefix(id, contextMenuDeleteID):
		a.openExplorerDeleteConfirm(a.copyTargets(strings.TrimPrefix(id, contextMenuDeleteID)))

		return a.redrawTreeMenu(ctx)
	case strings.HasPrefix(id, contextMenuSplitCloseID):
		if side, mode, ok := parseSplitClose(strings.TrimPrefix(id, contextMenuSplitCloseID)); ok {
			a.closeSplitSide(side, mode)
			if a.page == nil {
				return nil
			}

			return a.page.Redraw(ctx)
		}

		return nil
	case id == contextMenuReopenID:
		a.reopenClosedTab()
		return a.redrawMenuAction(ctx)
	case strings.HasPrefix(id, contextMenuPinID):
		if group, index, ok := contextMenuTarget(strings.TrimPrefix(id, contextMenuPinID)); ok {
			if tabs := a.groupTabs(group); index >= 0 && index < len(tabs) {
				a.pinTabByIndex(group, index)
			}
		}
		return a.redrawMenuAction(ctx)
	case strings.HasPrefix(id, contextMenuUnpinID):
		if group, index, ok := contextMenuTarget(strings.TrimPrefix(id, contextMenuUnpinID)); ok {
			a.unpinTabByIndex(group, index)
		}
		return a.redrawMenuAction(ctx)
	case strings.HasPrefix(id, contextMenuCloseLeftID):
		if group, index, ok := contextMenuTarget(strings.TrimPrefix(id, contextMenuCloseLeftID)); ok {
			return a.contextMenuCloseSide(ctx, group, index, true)
		}
	case strings.HasPrefix(id, contextMenuCloseRightID):
		if group, index, ok := contextMenuTarget(strings.TrimPrefix(id, contextMenuCloseRightID)); ok {
			return a.contextMenuCloseSide(ctx, group, index, false)
		}
	case strings.HasPrefix(id, contextMenuCloseOtherID):
		if group, index, ok := contextMenuTarget(strings.TrimPrefix(id, contextMenuCloseOtherID)); ok {
			return a.contextMenuCloseOtherTabs(ctx, group, index)
		}
	case strings.HasPrefix(id, contextMenuCloseAllID):
		if group, ok := contextMenuGroup(strings.TrimPrefix(id, contextMenuCloseAllID)); ok {
			return a.contextMenuCloseAllTabs(ctx, group)
		}
	case strings.HasPrefix(id, contextMenuCloseID):
		if group, index, ok := contextMenuTarget(strings.TrimPrefix(id, contextMenuCloseID)); ok {
			return a.contextMenuCloseTab(ctx, group, index)
		}
	}

	return nil
}

// redrawMenuAction repaints after a menu row that already refreshed the view.
func (a *App) redrawMenuAction(ctx context.Context) error {
	if a.page == nil {
		return nil
	}

	return a.page.Redraw(ctx)
}

func (a *App) contextMenuCloseSide(ctx context.Context, group, index int, left bool) error {
	tabs := a.groupTabs(group)
	if index < 0 || index >= len(tabs) {
		return nil
	}
	a.finishEdit()
	// Bulk closes skip pinned tabs; the gate only prompts for unpinned
	// dirty tabs, and the shared closeTabsIn pushes every removed tab.
	var closing []string
	for i, p := range tabs {
		if left && i >= index {
			continue
		}
		if !left && i <= index {
			continue
		}
		if a.isPinned(group, p) {
			continue
		}
		closing = append(closing, p)
	}
	proceed := func(ctx context.Context) error {
		if left {
			_, _ = a.closeTabsIn(group, func(i int, _ string) bool { return i >= index }, false)
		} else {
			_, _ = a.closeTabsIn(group, func(i int, _ string) bool { return i <= index }, false)
		}
		return a.contextMenuFinish(ctx)
	}
	if a.gateBulkClose(group, closing, proceed) {
		return a.redrawMenuAction(ctx)
	}
	return proceed(ctx)
}

// contextMenuTabID reads tab-N, tab-close-N, tab2-N, or tab2-close-N. Other
// synthetic ids, such as tab-working or tab-details, do not match.
func contextMenuTabID(id string) (int, int, bool) {
	for _, candidate := range []struct {
		prefix string
		group  int
	}{
		{"tab2-close-", 1},
		{"tab-close-", 0},
		{"tab2-", 1},
		{"tab-", 0},
	} {
		if !strings.HasPrefix(id, candidate.prefix) {
			continue
		}

		index, ok := contextMenuIndex(strings.TrimPrefix(id, candidate.prefix))
		if ok {
			return candidate.group, index, true
		}
	}

	return 0, 0, false
}

// contextMenuTarget reads group:index from a custom close row.
func contextMenuTarget(rest string) (int, int, bool) {
	groupText, indexText, ok := strings.Cut(rest, ":")
	if !ok {
		return 0, 0, false
	}

	group, ok := contextMenuGroup(groupText)
	if !ok {
		return 0, 0, false
	}

	index, ok := contextMenuIndex(indexText)
	if !ok {
		return 0, 0, false
	}

	return group, index, true
}

// contextMenuGroup accepts only the two editor groups.
func contextMenuGroup(text string) (int, bool) {
	group, err := strconv.Atoi(text)
	if err != nil || (group != 0 && group != 1) {
		return 0, false
	}

	return group, true
}

// contextMenuIndex accepts a non-negative decimal index.
func contextMenuIndex(text string) (int, bool) {
	if text == "" {
		return 0, false
	}

	for _, r := range text {
		if r < '0' || r > '9' {
			return 0, false
		}
	}

	index, err := strconv.Atoi(text)
	if err != nil {
		return 0, false
	}

	return index, true
}

// contextMenuTabTarget joins the group and index into the custom row suffix.
func contextMenuTabTarget(group, index int) string {
	return strconv.Itoa(group) + ":" + strconv.Itoa(index)
}

// contextMenuCloseTab drops one tab and keeps the group's active index on a
// real tab.
func (a *App) contextMenuCloseTab(ctx context.Context, group, index int) error {
	tabs := a.groupTabs(group)
	if index < 0 || index >= len(tabs) {
		return nil
	}
	a.finishEdit()
	path := tabs[index]
	// An explicit single close removes its tab even when pinned; the shared
	// closeTabIn pushes the closed stack.
	proceed := func(ctx context.Context) error {
		live := a.groupTabs(group)
		at := index
		if at < 0 || at >= len(live) || live[at] != path {
			at = -1
			for i, p := range live {
				if p == path {
					at = i

					break
				}
			}
		}
		if at < 0 {
			return a.contextMenuFinish(ctx)
		}
		a.closeTabIn(group, at)

		return a.contextMenuFinish(ctx)
	}
	if a.gateBulkClose(group, []string{path}, proceed) {
		return a.redrawMenuAction(ctx)
	}
	return proceed(ctx)
}

// contextMenuCloseOtherTabs keeps one tab in the group and drops the rest,
// skipping pinned tabs and pushing the rest onto the closed stack.
func (a *App) contextMenuCloseOtherTabs(ctx context.Context, group, index int) error {
	var tabs []string
	if group == 1 {
		tabs = a.tabs2
	} else {
		tabs = a.tabs
	}
	if index < 0 || index >= len(tabs) {
		return nil
	}
	a.finishEdit()
	keep := tabs[index]
	var closing []string
	for i, p := range tabs {
		if i != index && !a.isPinned(group, p) {
			closing = append(closing, p)
		}
	}
	proceed := func(ctx context.Context) error {
		live := a.groupTabs(group)
		at := -1
		for i, p := range live {
			if p == keep {
				at = i

				break
			}
		}
		if at < 0 {
			return a.contextMenuFinish(ctx)
		}
		_, _ = a.closeTabsIn(group, func(i int, _ string) bool { return live[i] == keep }, false)

		return a.contextMenuFinish(ctx)
	}
	if a.gateBulkClose(group, closing, proceed) {
		return a.redrawMenuAction(ctx)
	}
	return proceed(ctx)
}

// contextMenuCloseAllTabs clears one group. An empty second group ends the
// split. Close All is explicit, so it closes pinned tabs too.
func (a *App) contextMenuCloseAllTabs(ctx context.Context, group int) error {
	a.finishEdit()
	var closing []string
	if group == 1 {
		closing = append([]string(nil), a.tabs2...)
	} else {
		closing = append([]string(nil), a.tabs...)
	}
	proceed := func(ctx context.Context) error {
		_, _ = a.closeTabsIn(group, func(int, string) bool { return false }, true)

		return a.contextMenuFinish(ctx)
	}
	if a.gateBulkClose(group, closing, proceed) {
		return a.redrawMenuAction(ctx)
	}
	return proceed(ctx)
}

// contextMenuFinish settles and paints the page after a tab action.
func (a *App) contextMenuFinish(ctx context.Context) error {
	a.preview = false

	if a.page == nil {
		return nil
	}

	// The tab helpers already settled per-group scroll; only single-pane
	// mode resets here so split panes keep their offsets.
	if !a.split {
		a.resetScroll()
	}

	a.ensureTabVisible(0)
	a.ensureTabVisible(1)
	a.refresh()

	return a.page.Redraw(ctx)
}

// contextMenuClampIndex keeps an active index on a real tab, with 0 for an
// empty group.
func contextMenuClampIndex(index, count int) int {
	if index < 0 || count == 0 {
		return 0
	}

	if index >= count {
		return count - 1
	}

	return index
}
