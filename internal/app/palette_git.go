package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// runExtraPaletteCommand handles the commands defined in palette_commands.go.
// It reports false for ids it does not own.
func (a *App) runExtraPaletteCommand(ctx context.Context, id string) (bool, error) {
	id = strings.TrimSpace(id)

	owned := false

	switch {
	case id == paletteCmdTaskPick:
		return true, a.setPaletteQuery(ctx, ">Task: ")
	case id == paletteCmdStashPick:
		return true, a.setPaletteQuery(ctx, ">Git: Apply Stash ")
	case id == paletteCmdComparePick:
		return true, a.setPaletteQuery(ctx, ">compare ")
	case id == paletteCmdStageHunk, id == paletteCmdUnstageHunk, id == paletteCmdStashPush, id == paletteCmdLSPToggle,
		strings.HasPrefix(id, paletteCmdTaskPrefix), strings.HasPrefix(id, paletteCmdStashApplyPfx),
		strings.HasPrefix(id, paletteCmdComparePfx):
		owned = true
	}

	if !owned {
		return false, nil
	}

	a.paletteOpen = false
	a.paletteQuery = ""
	a.paletteIndex = 0
	a.palx.dynAt = a.palx.dynAt.Add(-2 * paletteDynTTL)

	switch {
	case id == paletteCmdStageHunk:
		a.setSCMMode()

		if err := a.stageHunkAtCursor(ctx); err != nil {
			return true, err
		}
	case id == paletteCmdUnstageHunk:
		if err := a.unstageHunkAtCursor(ctx); err != nil {
			return true, err
		}
	case id == paletteCmdStashPush:
		a.stashPush(ctx)
	case id == paletteCmdLSPToggle:
		on := !lspEnabled(a.settings.LSP)
		a.setLSPEnabled(on)

		if on {
			a.showToast("success", "Language servers on", "Servers start when you open a source file.")
		} else {
			a.showToast("info", "Language servers off", "Diagnostics, hover and go-to-definition fall back to the built-in index.")
		}
	case strings.HasPrefix(id, paletteCmdTaskPrefix):
		a.runTask(ctx, strings.TrimPrefix(id, paletteCmdTaskPrefix))
	case strings.HasPrefix(id, paletteCmdStashApplyPfx):
		n, err := strconv.Atoi(strings.TrimPrefix(id, paletteCmdStashApplyPfx))
		if err == nil && n >= 0 {
			a.stashApply(ctx, n)
		}
	case strings.HasPrefix(id, paletteCmdComparePfx):
		left, right, _ := strings.Cut(strings.TrimPrefix(id, paletteCmdComparePfx), ":")
		a.compareRefs(ctx, left, right)
	}

	a.refresh()

	if a.page == nil {
		return true, nil
	}

	return true, a.page.Redraw(ctx)
}

// setPaletteQuery rewrites the palette input and keeps it open and focused.
func (a *App) setPaletteQuery(ctx context.Context, query string) error {
	a.paletteQuery = query
	a.paletteIndex = 0
	a.refresh()

	if a.page == nil {
		return nil
	}

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	return a.focusAfterLayout(ctx, "palette")
}

// setSCMMode switches to the Source Control view so hunk commands have a
// diff to act on.
func (a *App) setSCMMode() {
	if a.mode != "changes" {
		a.mode = "changes"
	}
}

// unstageHunk moves one staged hunk (or the selected block of it) back out
// of the index with `git apply --cached --reverse`. It mirrors stageHunk.
func (a *App) unstageHunk(ctx context.Context, hunkIdx int, selection ...hunk) error {
	if a.scmGuard() {
		return nil
	}

	if a.diffSource != "working" {
		a.showToast("info", "Working tree only", "Hunk unstage is available for working-tree changes.")
		a.refresh()

		return nil
	}

	file := findFile(a.changes.Files, a.diffPath)
	if file == nil || file.Binary || file.Oversized || file.Status == "U" {
		a.showToast("info", "File-level unstage only", "Use the file - button for this file.")
		a.refresh()

		return nil
	}

	if hunkIdx < 0 || hunkIdx >= len(file.Hunks) {
		return nil
	}

	uiHunk := file.Hunks[hunkIdx]
	if len(selection) > 0 {
		uiHunk = selection[0]
	}

	patch, ok := a.stagedHunkPatch(ctx, file.Path, uiHunk.OldStart, uiHunk.OldCount)
	if !ok {
		a.showToast("info", "Hunk not staged", "The index holds no staged change for this hunk.")
		a.refresh()

		return nil
	}

	if _, err := gitBytesStdin(ctx, a.ws.Root, patch, "apply", "--cached", "--reverse", "--whitespace=nowarn", "--unidiff-zero"); err != nil {
		a.showToast("error", "Unstage failed", err.Error())
		a.refreshAfterSCMMutation(ctx)
		a.refresh()

		return nil
	}

	a.refreshAfterSCMMutation(ctx)
	a.showToast("success", "Hunk unstaged", file.Path)
	a.refresh()

	return nil
}

// unstageHunkAtCursor unstages the hunk under the highlighted change block.
func (a *App) unstageHunkAtCursor(ctx context.Context) error {
	if a.scmGuard() {
		return nil
	}

	if a.mode != "changes" {
		a.showToast("info", "Open Source Control", "Select a changed file in Source Control first.")
		a.refresh()

		return nil
	}

	file := findFile(a.changes.Files, a.diffPath)
	index := a.hunkIndexAtCursor()

	if file == nil || index < 0 || index >= len(file.Hunks) {
		a.showToast("info", "No hunk here", "Move to a changed line before unstaging a hunk.")
		a.refresh()

		return nil
	}

	return a.unstageHunk(ctx, index)
}

// stashPush stashes tracked changes. Untracked files are left alone so the
// command can never sweep up files git does not track.
func (a *App) stashPush(ctx context.Context) {
	if a.scmGuard() {
		return
	}

	if _, err := gitBytes(ctx, a.ws.Root, "stash", "push", "--message", "own-code stash"); err != nil {
		a.showToast("error", "Stash failed", err.Error())

		return
	}

	a.refreshAfterSCMMutation(ctx)
	a.showToast("success", "Changes stashed", "Apply them again from Git: Apply Stash.")
}

// stashApply applies one stash entry and keeps it (never drops).
func (a *App) stashApply(ctx context.Context, index int) {
	if a.scmGuard() {
		return
	}

	ref := fmt.Sprintf("stash@{%d}", index)

	if _, err := gitBytes(ctx, a.ws.Root, "stash", "apply", ref); err != nil {
		a.showToast("error", "Apply stash failed", err.Error())
		a.refreshAfterSCMMutation(ctx)

		return
	}

	a.refreshAfterSCMMutation(ctx)
	a.showToast("success", "Stash applied", ref+" kept in the stash list")
}

// loadRefsChanges diffs two refs (left to right) for the review view.
func loadRefsChanges(ctx context.Context, root, left, right string) (changeSet, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	var tips [2]string

	for i, ref := range []string{left, right} {
		tip, err := gitText(ctx, root, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
		if err != nil {
			return changeSet{}, fmt.Errorf("resolve %s: %w", ref, err)
		}

		tips[i] = tip
	}

	raw, err := gitBytes(ctx, root, "diff", "--no-color", "--no-ext-diff", tips[0], tips[1], "--")
	if err != nil {
		return changeSet{}, err
	}

	changes := changeSet{Branch: branchLabel(right), Files: parseDiff(string(raw))}
	if len(changes.Files) == 0 {
		changes.EmptyTitle = "No differences"
	}

	changes.Note = fmt.Sprintf("Changes from %s to %s.", left, right)

	return changes, nil
}

// compareRefs opens the two-ref diff in the Source Control branch view.
func (a *App) compareRefs(ctx context.Context, left, right string) {
	if a.ws == nil || a.ws.Sample || a.ws.Root == "" {
		a.showToast("info", "Sample workspace", "Open a git folder to compare refs.")

		return
	}

	if !validRefArg(left) || !validRefArg(right) {
		a.showToast("error", "Invalid ref", "Use branch, tag or commit names.")

		return
	}

	if _, err := loadRefsChanges(ctx, a.ws.Root, left, right); err != nil {
		a.showToast("error", "Cannot compare", err.Error())

		return
	}

	a.mode = "changes"
	a.presentBranchChanges(ctx, [2]string{left, right})
}
