package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// stageHunk stages a hunk, or the selected block within it, from the
// worktree into the index via `git apply --cached`. The patch is shaped
// from the unstaged diff (`git diff`, index vs worktree) with zero context
// so only the addressed hunk moves; the rest stays unstaged. It mirrors
// revertHunk addressing (selectRawHunks overlap) but never touches worktree
// bytes and never stages whole files by guessing.
func (a *App) stageHunk(ctx context.Context, hunkIdx int, selection ...hunk) error {
	if a.scmGuard() {
		return nil
	}

	if a.diffSource != "working" {
		a.showToast("info", "Working tree only", "Hunk stage is available for working-tree changes.")
		a.refresh()

		return nil
	}

	if findFile(a.changes.Conflicts, a.diffPath) != nil {
		a.showToast("info", "Conflicted file", "Resolve the merge conflict before staging hunks.")
		a.refresh()

		return nil
	}

	file := findFile(a.changes.Files, a.diffPath)
	if file == nil {
		return nil
	}

	if file.Binary || file.Oversized {
		a.showToast("info", "File-level stage only", "Binary and oversized files stage whole: use the file + button.")
		a.refresh()

		return nil
	}

	if file.Status == "U" {
		a.showToast("info", "Untracked file", "Untracked files stage whole: use the file + button.")
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

	patch, ok := a.unstagedHunkPatch(ctx, file.Path, uiHunk.OldStart, uiHunk.OldCount)
	if !ok {
		a.showToast("info", "Hunk already staged", "The working tree no longer has this unstaged hunk.")
		a.refresh()

		return nil
	}

	if _, err := gitBytesStdin(ctx, a.ws.Root, patch, "apply", "--cached", "--whitespace=nowarn", "--unidiff-zero"); err != nil {
		a.showToast("error", "Stage failed", err.Error())
		a.refreshAfterSCMMutation(ctx)
		a.refresh()

		return nil
	}

	a.refreshAfterSCMMutation(ctx)
	a.showToast("success", "Hunk staged", file.Path)
	a.refresh()

	return nil
}

// unstagedHunkPatch builds a cached-apply patch for the unstaged hunks whose
// index range overlaps one displayed hunk. It returns false when the index
// already holds the hunk (nothing unstaged overlaps it).
func (a *App) unstagedHunkPatch(ctx context.Context, path string, oldStart, oldCount int) (string, bool) {
	raw, err := gitBytes(ctx, a.ws.Root, "diff", "--no-color", "--no-ext-diff", "-U0", "--", path)
	if err != nil || len(raw) == 0 {
		return "", false
	}

	patch := selectRawHunks(string(raw), oldStart, oldCount)
	if patch == "" {
		return "", false
	}

	return patch, true
}

// stageClickedBlock resolves the line control back to its contiguous source
// block and stages it, mirroring revertClickedBlock.
func (a *App) stageClickedBlock(ctx context.Context, index int, id string) error {
	_, coordinates, ok := strings.Cut(strings.TrimPrefix(id, "revert-line-"), "-")
	if !ok {
		return nil
	}

	oldText, newText, ok := strings.Cut(coordinates, "-")
	if !ok {
		return nil
	}

	oldNo, _ := strconv.Atoi(oldText)
	newNo, _ := strconv.Atoi(newText)

	sel, found := a.hunkBlockSelection(index, oldNo, newNo)
	if !found {
		return nil
	}

	return a.stageHunk(ctx, index, sel)
}

// hunkBlockSelection finds the contiguous changed block holding one of two
// source line numbers. It mirrors the walk in revertClickedBlock so the
// stage and discard menu rows address the same block.
func (a *App) hunkBlockSelection(index, oldNo, newNo int) (hunk, bool) {
	file := findFile(a.changes.Files, a.diffPath)
	if file == nil || index < 0 || index >= len(file.Hunks) {
		return hunk{}, false
	}

	source := file.Hunks[index]
	oldCursor := source.OldStart

	for i := 0; i < len(source.Lines); {
		if source.Lines[i].Kind == ' ' {
			oldCursor++
			i++

			continue
		}

		start := oldCursor
		count := 0
		selected := false

		for i < len(source.Lines) && source.Lines[i].Kind != ' ' {
			line := source.Lines[i]
			selected = selected || (oldNo > 0 && line.OldNo == oldNo) || (newNo > 0 && line.NewNo == newNo)

			if line.Kind == '-' {
				count++
				oldCursor++
			}

			i++
		}

		if selected {
			if count == 0 {
				start--
			}

			return hunk{OldStart: start, OldCount: count}, true
		}
	}

	return hunk{}, false
}

// parseHunkMenuRest reads "<hunkIdx>-<oldNo>-<newNo>" from a hunk menu row.
// The coordinates come from the revert button id, so empty numbers parse
// leniently to zero exactly like revertClickedBlock does.
func parseHunkMenuRest(rest string) (index, oldNo, newNo int, ok bool) {
	idxText, coordinates, cut := strings.Cut(rest, "-")
	if !cut {
		return 0, 0, 0, false
	}

	index, err := strconv.Atoi(idxText)
	if err != nil || index < 0 {
		return 0, 0, 0, false
	}

	oldText, newText, cut := strings.Cut(coordinates, "-")
	if !cut {
		return 0, 0, 0, false
	}

	oldNo, _ = strconv.Atoi(oldText)
	newNo, _ = strconv.Atoi(newText)

	return index, oldNo, newNo, true
}

// hunkIndexAtCursor returns the file hunk under the highlighted change
// block, using the same current-row rule the pane highlight uses. Hunk
// header continuations from word wrap never count, so wrapped labels keep
// the mapping stable.
func (a *App) hunkIndexAtCursor() int {
	if a.mode != "changes" || a.diffSource != "working" || a.diffPath == "" {
		return -1
	}

	rows := a.diffDisplayRows()
	if len(rows) == 0 {
		return -1
	}

	current := a.scrollY / a.zx(rowH)
	if a.changeNav.valid && a.changeNav.path == a.diffPath &&
		a.changeNav.mode == a.mode && a.changeNav.scroll == a.scrollY {
		current = a.changeNav.row
	}

	starts := diffChangeStarts(rows)
	if len(starts) == 0 {
		return -1
	}

	selected := starts[0]
	for _, row := range starts {
		if row > current {
			break
		}

		selected = row
	}

	hunkIdx := -1
	for i := 0; i <= selected && i < len(rows); i++ {
		if rows[i].Kind == rowHunk && !rows[i].Cont {
			hunkIdx++
		}
	}

	return hunkIdx
}

// stageHunkAtCursor stages the hunk under the highlighted change block for
// the keyboard chord. Binary, oversized, untracked, and conflicted files
// route through stageHunk so they get the same explicit note instead of a
// guessed stage.
func (a *App) stageHunkAtCursor(ctx context.Context) error {
	if a.scmGuard() {
		return nil
	}

	if a.diffSource != "working" {
		a.showToast("info", "Working tree only", "Hunk stage is available for working-tree changes.")
		a.refresh()

		return nil
	}

	file := findFile(a.changes.Files, a.diffPath)
	if file == nil {
		if findFile(a.changes.Conflicts, a.diffPath) != nil {
			a.showToast("info", "Conflicted file", "Resolve the merge conflict before staging hunks.")
		} else {
			a.showToast("info", "No file selected", "Select a changed file before staging a hunk.")
		}

		a.refresh()

		return nil
	}

	if file.Binary || file.Oversized || file.Status == "U" {
		return a.stageHunk(ctx, 0)
	}

	index := a.hunkIndexAtCursor()
	if index < 0 || index >= len(file.Hunks) {
		a.showToast("info", "No hunk here", "Move to a changed line before staging a hunk.")
		a.refresh()

		return nil
	}

	return a.stageHunk(ctx, index)
}

// openCommitDialog shows the commit dialog when staged changes exist. The
// message draft survives cancel and failure; only a successful commit
// clears it.
func (a *App) openCommitDialog(ctx context.Context) error {
	if a.scmGuard() {
		return nil
	}

	if len(a.changes.Staged) == 0 {
		a.showToast("info", "Nothing staged", "Stage a file or hunk before committing.")
		a.refresh()

		return nil
	}

	a.commitDialogOpen = true
	a.commitDialogErr = ""
	a.refresh()

	return a.page.Form().Focus(ctx, "commit-message")
}

// closeCommitDialog hides the dialog and keeps the message draft so a
// reopen restores it.
func (a *App) closeCommitDialog() {
	a.commitDialogOpen = false
	a.commitDialogErr = ""
	a.refresh()
}

// submitCommitDialog validates the message, guards dirty buffers covering
// staged paths through the unsaved dialog, and commits.
func (a *App) submitCommitDialog(ctx context.Context) error {
	if !a.commitDialogOpen {
		return nil
	}

	if strings.TrimSpace(a.commitDialogMsg) == "" {
		a.commitDialogErr = "Write a commit message before committing."
		a.refresh()

		return a.page.Form().Focus(ctx, "commit-message")
	}

	if a.scmGuard() {
		return nil
	}

	staged := map[string]bool{}
	for _, file := range a.changes.Staged {
		staged[file.Path] = true
	}

	seen := map[string]bool{}

	var pending []tabRef

	for _, group := range []int{0, 1} {
		for _, path := range a.groupTabs(group) {
			if seen[path] || !staged[path] || !a.isDirty(path) {
				seen[path] = true

				continue
			}

			seen[path] = true
			pending = append(pending, tabRef{Group: group, Path: path})
		}
	}

	if a.gateUnsavedDirty(pending, func(ctx context.Context) error { return a.doCommit(ctx) }) {
		return a.page.Redraw(ctx)
	}

	return a.doCommit(ctx)
}

// doCommit runs git commit with the dialog message. An empty message is
// blocked with an inline error, a git failure keeps the draft with the
// error shown, and only success clears the draft and closes the dialog. It
// never pushes and never amends.
func (a *App) doCommit(ctx context.Context) error {
	msg := strings.TrimSpace(a.commitDialogMsg)
	if msg == "" {
		a.commitDialogErr = "Write a commit message before committing."
		a.refresh()

		return nil
	}

	if a.scmGuard() {
		return nil
	}

	if len(a.changes.Staged) == 0 {
		a.commitDialogErr = "Nothing staged to commit."
		a.refresh()

		return nil
	}

	subject, body := splitCommitMessage(msg)

	args := []string{"commit", "-m", subject}
	if body != "" {
		args = append(args, "-m", body)
	}

	if _, err := gitBytes(ctx, a.ws.Root, args...); err != nil {
		a.commitDialogErr = err.Error()
		a.refreshAfterSCMMutation(ctx)
		a.refresh()

		return nil
	}

	a.commitDialogOpen = false
	a.commitDialogMsg = ""
	a.commitDialogErr = ""
	a.refreshAfterSCMMutation(ctx)
	a.showToast("success", "Committed", subject)
	a.refresh()

	return nil
}

// splitCommitMessage treats the first line as the subject and the rest as
// the body, trimming blank edges on both.
func splitCommitMessage(msg string) (string, string) {
	msg = strings.ReplaceAll(msg, "\r\n", "\n")

	cut := strings.Index(msg, "\n")
	if cut < 0 {
		return strings.TrimSpace(msg), ""
	}

	return strings.TrimSpace(msg[:cut]), strings.TrimSpace(msg[cut+1:])
}

// stagedShortstat summarizes the staged set git-shortstat style.
func stagedShortstat(files []changedFile) string {
	if len(files) == 0 {
		return "Nothing staged"
	}

	add, del := 0, 0
	for _, file := range files {
		add += file.Add
		del += file.Del
	}

	var b strings.Builder

	if len(files) == 1 {
		b.WriteString("1 file changed")
	} else {
		fmt.Fprintf(&b, "%d files changed", len(files))
	}

	if add > 0 {
		fmt.Fprintf(&b, ", %d insertion", add)
		if add != 1 {
			b.WriteString("s")
		}

		b.WriteString("(+)")
	}

	if del > 0 {
		fmt.Fprintf(&b, ", %d deletion", del)
		if del != 1 {
			b.WriteString("s")
		}

		b.WriteString("(-)")
	}

	return b.String()
}

// commitDialogViewState publishes the commit dialog: staged files with
// stats, the shortstat line, the draft, and the inline error.
func (a *App) commitDialogViewState() commitDialogView {
	if !a.commitDialogOpen {
		return commitDialogView{}
	}

	rows := make([]diffFileRow, 0, len(a.changes.Staged))
	for _, file := range a.changes.Staged {
		rows = append(rows, diffFileRow{
			Path:        file.Path,
			Name:        baseName(file.Path),
			Dir:         dirName(file.Path),
			Status:      file.Status,
			StatusClass: "st-" + strings.ToLower(file.Status),
			Lang:        languageOf(file.Path),
			Badge:       languageInfo(languageOf(file.Path)).Badge,
			Add:         file.Add,
			Del:         file.Del,
		})
	}

	return commitDialogView{
		Show:      true,
		Message:   a.commitDialogMsg,
		Files:     rows,
		FileCount: len(rows),
		Shortstat: stagedShortstat(a.changes.Staged),
		Error:     a.commitDialogErr,
	}
}
