package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/edit"
	"github.com/chinmay-sawant/own-code/internal/merge"
)

const (
	// mergeReadLimit caps the text the merge editor reads and writes. It
	// matches conflict detection so a file that shows a marker count can
	// always be opened.
	mergeReadLimit = conflictReadLimit

	mergeRowHeader = "header"
	mergeRowSide   = "side"
	mergeRowAction = "action"
	mergeRowMeta   = "meta"
)

// mergeFileStamp is the size and modification time captured when the merge
// editor opened, or after its last save. It guards against another process
// rewriting the file between those points.
type mergeFileStamp struct {
	exists bool
	size   int64
	mod    int64
}

// openMergeConflict reads one unmerged path and puts the app in merge mode.
// It refuses paths with unsaved editor edits so a merge result never clobbers
// in-memory work.
func (a *App) openMergeConflict(ctx context.Context, path string) {
	if !validWorkspacePath(path) {
		return
	}

	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if findFile(a.changes.Conflicts, clean) == nil {
		a.showToast("info", "No merge conflict", clean+" is no longer unmerged. Refresh Source Control.")
		a.refresh()

		return
	}

	if a.dirty[clean] {
		a.showToast("info", "Unsaved edits", clean+" has unsaved edits. Save or discard them before resolving the conflict.")
		a.refresh()

		return
	}

	// Leave the plain editor without touching disk. Unsaved text stays in
	// the document and dirty map, so switching files never silently saves it.
	if a.editing {
		a.finishEdit()
	}

	a.ws.ensureFileLoaded(clean)

	full := filepath.Join(a.ws.Root, filepath.FromSlash(clean))

	var (
		raw        []byte
		stamp      mergeFileStamp
		note       string
		missing    bool
		unreadable bool
		directory  bool
		oversized  bool
	)

	info, statErr := os.Stat(full)
	switch {
	case statErr == nil && info.IsDir():
		directory = true
		note = "The conflict path is a directory. Resolve it outside the merge editor."
	case statErr == nil && info.Size() > mergeReadLimit:
		oversized = true
		stamp = mergeFileStamp{exists: true, size: info.Size(), mod: info.ModTime().UnixNano()}
		note = fmt.Sprintf("The file is larger than %d KB and cannot be parsed safely.", mergeReadLimit>>10)
	case statErr == nil:
		stamp = mergeFileStamp{exists: true, size: info.Size(), mod: info.ModTime().UnixNano()}

		raw, statErr = os.ReadFile(full) //nolint:gosec // path came from git status under the scanned root
		if statErr != nil {
			unreadable = true
			note = "The working file could not be read: " + statErr.Error()
		}
	default:
		missing = true
		raw, note = a.recoverConflictText(ctx, clean)
	}

	result := string(raw)
	doc := merge.Parse(result)
	base, baseSource, baseLines := a.loadMergeBase(ctx, clean, doc.Conflicts)

	a.mergeOpen = true
	a.mergePath = clean
	a.mergeResult = result
	a.mergeConflicts = doc.Conflicts
	a.mergeIndex = 0
	a.mergeCompare = false
	a.mergeDirty = false
	a.mergeStamp = stamp
	a.mergeBase = base
	a.mergeBaseSource = baseSource
	a.mergeBaseLines = baseLines
	a.mergeUnavailable = false
	a.mergeNote = note
	a.mergeRows = nil

	// A missing working file that has a recoverable index side is written
	// back through the workspace writer on Save, so register its metadata.
	if missing && len(raw) > 0 && !a.ws.Sample && a.ws.ByPath[clean] == nil {
		a.ws.addFile(clean, baseName(clean), int64(len(raw)))
	}

	switch {
	case isBinary(raw):
		a.mergeResult = ""
		a.mergeConflicts = nil
		a.mergeUnavailable = true
		a.mergeNote = "This is a binary conflict. Resolve it with Discard, or stage one side from the command line."
	case directory:
		a.mergeResult = ""
		a.mergeConflicts = nil
		a.mergeUnavailable = true
	case oversized:
		a.mergeResult = ""
		a.mergeConflicts = nil
		a.mergeUnavailable = true
	case unreadable:
		a.mergeResult = ""
		a.mergeConflicts = nil
		a.mergeUnavailable = true
	case missing && len(raw) == 0:
		a.mergeConflicts = nil
		a.mergeUnavailable = true
	}

	if !a.mergeUnavailable && note != "" {
		a.mergeNote = note
	}

	a.mode = "merge"
	a.diffSource = "working"
	a.revealChangePath(clean)
	a.resetScroll()
	a.refresh()
}

// recoverConflictText reads the ours or theirs index side when the working
// file is missing, as in a delete/modify conflict.
func (a *App) recoverConflictText(ctx context.Context, path string) ([]byte, string) {
	for _, stage := range []string{":2:", ":3:"} {
		raw, truncated, err := gitBytesCapped(ctx, a.ws.Root, mergeReadLimit, "show", stage+path)
		if err == nil && len(raw) > 0 {
			label := "current"
			if stage == ":3:" {
				label = "incoming"
			}

			note := "The working file is missing; recovered the " + label + " side from the index."
			if truncated {
				note += " The read stopped at the size cap."
			}

			return raw, note
		}
	}

	return nil, "The working file is missing and neither index side could be read. Resolve this delete conflict with Discard or Stage Conflict."
}

// loadMergeBase returns the base text, its source, and its line count. A diff3
// marker block carries its own base lines, so no git read is needed then. The
// stage-1 index entry is the fallback and is empty for add/add conflicts.
func (a *App) loadMergeBase(ctx context.Context, path string, conflicts []merge.Conflict) (string, string, int) {
	for _, conflict := range conflicts {
		if mergeConflictHasBase(conflict) {
			return "", "diff3", 0
		}
	}

	raw, truncated, err := gitBytesCapped(ctx, a.ws.Root, mergeReadLimit, "show", ":1:"+path)
	if err != nil || len(raw) == 0 {
		return "", "none", 0
	}

	if truncated || isBinary(raw) {
		return "", "unavailable", 0
	}

	text := string(raw)

	return text, ":1", len(strings.Split(strings.TrimSuffix(text, "\n"), "\n"))
}

// mergeConflictHasBase reports whether a parsed block carried a diff3 base
// section. A two-way block has three marker lines; a diff3 block has four.
func mergeConflictHasBase(conflict merge.Conflict) bool {
	total := conflict.EndLine - conflict.StartLine + 1
	content := len(conflict.Ours) + len(conflict.Base) + len(conflict.Theirs)

	return total-content >= 4
}

// fillMerge publishes the merge editor state and its visible rows.
func (a *App) fillMerge(v *View) {
	mv := mergeView{
		Open:        a.mergeOpen,
		Unavailable: a.mergeUnavailable,
		Path:        a.mergePath,
		Name:        baseName(a.mergePath),
		Dir:         dirName(a.mergePath),
		Total:       len(a.mergeConflicts),
		Compare:     a.mergeCompare,
		Dirty:       a.mergeDirty,
		Narrow:      v.PaneWidth < a.zx(700),
		HasMarkers:  merge.HasMarkers(a.mergeResult),
		BaseSource:  a.mergeBaseSource,
		Note:        a.mergeNote,
	}

	mv.CanStage = mv.Open && !mv.Unavailable && !mv.HasMarkers
	if mv.Total > 0 {
		mv.Index = min(a.mergeIndex, mv.Total-1) + 1
	}

	winW, _ := a.page.Size()
	mv.NameShort, mv.DirShort = clipDiffPath(v.PaneWidth, winW, mv.Name, mv.Dir)

	rows := a.mergeRowModels()
	a.mergeRows = rows

	start, end, track := a.windowSlice(len(rows))
	mv.TrackTop = track

	if end > start {
		mv.Rows = append([]mergeRow(nil), rows[start:end]...)
	}

	if mv.Note == "" {
		switch {
		case mv.Total == 0 && mv.HasMarkers:
			mv.Note = "Conflict markers are present but did not form complete blocks. Edit the result to fix them."
		case mv.Total == 0:
			mv.Note = "No conflict markers remain. Save, then Mark Resolved & Stage."
		default:
			mv.Note = "The result text is the source of truth. Accepting a side rewrites the block in place."
		}
	}

	v.Merge = mv
}

// mergeRowModels builds the fixed-height rows for the merge editor. Rows are
// conflict headers, ours/theirs/base lines, and one action row per conflict.
func (a *App) mergeRowModels() []mergeRow {
	if a.mergeUnavailable {
		return []mergeRow{{
			Kind:  mergeRowMeta,
			Label: "Merge editor unavailable",
			Text:  a.mergeNote,
		}}
	}

	if len(a.mergeConflicts) == 0 {
		return []mergeRow{
			{
				Kind:  mergeRowMeta,
				Label: "Result",
				Text:  "No conflict markers remain. Save and stage the file when it is ready.",
			},
			{
				Kind:     mergeRowAction,
				Conflict: -1,
				Current:  true,
			},
		}
	}

	rows := make([]mergeRow, 0, len(a.mergeConflicts)*8)

	for i, conflict := range a.mergeConflicts {
		current := i == a.mergeIndex
		oursLabel := conflict.OursLabel
		if oursLabel == "" {
			oursLabel = "current"
		}

		theirsLabel := conflict.TheirsLabel
		if theirsLabel == "" {
			theirsLabel = "incoming"
		}

		rows = append(rows, mergeRow{
			Kind:     mergeRowHeader,
			Conflict: i,
			Current:  current,
			Label:    fmt.Sprintf("Conflict %d of %d", i+1, len(a.mergeConflicts)),
			Text:     fmt.Sprintf("lines %d–%d · %s → %s", conflict.StartLine, conflict.EndLine, oursLabel, theirsLabel),
		})

		oursStart := conflict.StartLine + 1
		for j, line := range conflict.Ours {
			rows = append(rows, mergeRow{
				Kind:      mergeRowSide,
				Side:      "ours",
				Conflict:  i,
				Current:   current,
				SideFirst: j == 0,
				No:        strconv.Itoa(oursStart + j),
				Text:      expandTabs(line),
			})
		}

		if len(conflict.Ours) == 0 {
			rows = append(rows, mergeRow{Kind: mergeRowMeta, Side: "ours", Conflict: i, Current: current, Text: "(no current-branch lines)"})
		}

		if a.mergeCompare {
			switch {
			case mergeConflictHasBase(conflict):
				baseStart := conflict.StartLine + 2 + len(conflict.Ours)
				for j, line := range conflict.Base {
					rows = append(rows, mergeRow{
						Kind:      mergeRowSide,
						Side:      "base",
						Conflict:  i,
						Current:   current,
						SideFirst: j == 0,
						No:        strconv.Itoa(baseStart + j),
						Text:      expandTabs(line),
					})
				}

				if len(conflict.Base) == 0 {
					rows = append(rows, mergeRow{Kind: mergeRowMeta, Side: "base", Conflict: i, Current: current, Text: "(empty base block)"})
				}
			case a.mergeBase != "":
				rows = append(rows, mergeRow{
					Kind:     mergeRowMeta,
					Side:     "base",
					Conflict: i,
					Current:  current,
					Text:     fmt.Sprintf("No diff3 base block for this conflict. The stage-1 base file has %d lines.", a.mergeBaseLines),
				})
			default:
				rows = append(rows, mergeRow{
					Kind:     mergeRowMeta,
					Side:     "base",
					Conflict: i,
					Current:  current,
					Text:     "Base unavailable for this conflict (add/add or a missing stage-1 entry).",
				})
			}
		}

		theirsStart := conflict.StartLine + 2 + len(conflict.Ours)
		if mergeConflictHasBase(conflict) {
			theirsStart = conflict.StartLine + 3 + len(conflict.Ours) + len(conflict.Base)
		}

		for j, line := range conflict.Theirs {
			rows = append(rows, mergeRow{
				Kind:      mergeRowSide,
				Side:      "theirs",
				Conflict:  i,
				Current:   current,
				SideFirst: j == 0,
				No:        strconv.Itoa(theirsStart + j),
				Text:      expandTabs(line),
			})
		}

		if len(conflict.Theirs) == 0 {
			rows = append(rows, mergeRow{Kind: mergeRowMeta, Side: "theirs", Conflict: i, Current: current, Text: "(no incoming lines)"})
		}

		rows = append(rows, mergeRow{
			Kind:     mergeRowAction,
			Conflict: i,
			Current:  current,
		})
	}

	return rows
}

// parseMergeChoice maps an action suffix to a merge choice.
func parseMergeChoice(name string) (merge.Choice, bool) {
	switch name {
	case "ours":
		return merge.Ours, true
	case "theirs":
		return merge.Theirs, true
	case "both":
		return merge.Both, true
	case "base":
		return merge.Base, true
	default:
		return merge.Keep, false
	}
}

// mergeAccept resolves one conflict in the result text and re-parses it, so
// manual edits elsewhere survive and choices live only in the text.
func (a *App) mergeAccept(choice merge.Choice, index int) {
	if !a.mergeOpen || a.mergeUnavailable {
		return
	}

	next, err := merge.ResolveConflict(a.mergeResult, index, choice)
	if err != nil {
		a.showToast("error", "Conflict not resolved", err.Error())
		a.refresh()

		return
	}

	doc := merge.Parse(next)
	a.mergeResult = next
	a.mergeConflicts = doc.Conflicts
	a.mergeDirty = true

	if len(a.mergeConflicts) == 0 {
		a.mergeIndex = 0
	} else {
		a.mergeIndex = min(index, len(a.mergeConflicts)-1)
	}

	a.mergeRows = a.mergeRowModels()
	a.scrollY = a.clampScroll(a.scrollY)
	a.refresh()
}

// mergeStep moves to the previous or next conflict and scrolls its header
// row to the top of the editor.
func (a *App) mergeStep(delta int) {
	if !a.mergeOpen || len(a.mergeConflicts) == 0 {
		return
	}

	next := a.mergeIndex + delta
	if next < 0 {
		next = 0
	}

	if next >= len(a.mergeConflicts) {
		next = len(a.mergeConflicts) - 1
	}

	if next == a.mergeIndex {
		return
	}

	a.mergeIndex = next
	rows := a.mergeRowModels()
	a.mergeRows = rows

	for i, row := range rows {
		if row.Kind == mergeRowHeader && row.Conflict == next {
			a.scrollTo(i * a.zx(rowH))

			return
		}
	}

	a.refresh()
}

// toggleMergeCompare shows or hides the base lines under each conflict.
func (a *App) toggleMergeCompare() {
	a.mergeCompare = !a.mergeCompare
	a.mergeRows = a.mergeRowModels()
	a.scrollY = a.clampScroll(a.scrollY)
	a.refresh()
}

// mergeChangedOnDisk reports whether the working file moved since the merge
// editor captured its stamp.
func (a *App) mergeChangedOnDisk(path string) bool {
	info, err := os.Stat(filepath.Join(a.ws.Root, filepath.FromSlash(path)))
	if err != nil {
		return a.mergeStamp.exists
	}

	current := mergeFileStamp{
		exists: true,
		size:   info.Size(),
		mod:    info.ModTime().UnixNano(),
	}

	return current != a.mergeStamp
}

// saveMergeEditor writes the result text without staging it. It refuses when
// another process changed the file after the editor opened it.
func (a *App) saveMergeEditor(ctx context.Context) bool {
	if !a.mergeOpen || a.mergeUnavailable {
		return false
	}

	path := a.mergePath
	if a.mergeChangedOnDisk(path) {
		a.showToast("error", "File changed on disk", "Reopen the conflict before saving, so the newer file is not overwritten.")
		a.refresh()

		return false
	}

	if err := a.ws.writeFile(path, []byte(a.mergeResult)); err != nil {
		a.showToast("error", "Save failed", err.Error())
		a.refresh()

		return false
	}

	if info, err := os.Stat(filepath.Join(a.ws.Root, filepath.FromSlash(path))); err == nil {
		a.mergeStamp = mergeFileStamp{exists: true, size: info.Size(), mod: info.ModTime().UnixNano()}
	}

	a.mergeDirty = false
	a.refreshMergeAfterWrite(ctx, path)
	a.showToast("success", "Merge result saved", path)
	a.refresh()

	return true
}

// refreshMergeAfterWrite updates the open document and Source Control after
// a merge save without resetting the merge editor's scroll position.
func (a *App) refreshMergeAfterWrite(ctx context.Context, path string) {
	if !a.ws.Sample && a.ws.Root != "" {
		if next, err := loadLiveChanges(ctx, a.ws.Root); err == nil {
			a.changes = next
			a.statuses = statusMap(next)
			a.syncFileNav()
		}
	}

	if a.ws.ByPath[path] != nil {
		a.docs[path] = a.ws.openDoc(path)
	} else {
		delete(a.docs, path)
	}
}

// stageMergeConflict saves a dirty result, refuses remaining markers, and
// stages the file only on this explicit action.
func (a *App) stageMergeConflict(ctx context.Context) {
	if !a.mergeOpen {
		return
	}

	if a.mergeUnavailable {
		a.showToast("error", "Cannot stage", "The merge editor could not prepare this conflict for staging.")
		a.refresh()

		return
	}

	if merge.HasMarkers(a.mergeResult) {
		a.showToast("error", "Unresolved conflicts", "Every conflict marker must be resolved before staging.")
		a.refresh()

		return
	}

	if (a.mergeDirty || !a.mergeStamp.exists) && !a.saveMergeEditor(ctx) {
		return
	}

	path := a.mergePath
	if _, err := gitBytes(ctx, a.ws.Root, "add", "--", path); err != nil {
		a.showToast("error", "Stage failed", err.Error())
		a.refresh()

		return
	}

	a.leaveMergeMode()
	a.mode = "changes"
	a.refreshAfterSCMMutation(ctx)
	a.showToast("success", "Conflict staged", path)
	a.refresh()
}

// stageConflictPath stages one conflict from the Source Control context menu.
// It refuses files that still contain conflict markers.
func (a *App) stageConflictPath(ctx context.Context, path string) {
	if !validWorkspacePath(path) {
		return
	}

	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	file := findFile(a.changes.Conflicts, clean)
	if file == nil {
		return
	}

	full := filepath.Join(a.ws.Root, filepath.FromSlash(clean))
	if info, err := os.Stat(full); err == nil && info.Size() <= mergeReadLimit {
		if raw, err := os.ReadFile(full); err == nil && !isBinary(raw) && merge.HasMarkers(string(raw)) { //nolint:gosec // path came from git status under the scanned root
			a.showToast("error", "Unresolved conflicts", clean+" still contains conflict markers. Resolve it in the merge editor first.")
			a.refresh()

			return
		}
	}

	if _, err := gitBytes(ctx, a.ws.Root, "add", "--", clean); err != nil {
		a.showToast("error", "Stage failed", err.Error())
		a.refresh()

		return
	}

	if a.mergeOpen && a.mergePath == clean {
		a.leaveMergeMode()
		a.mode = "changes"
	}

	a.refreshAfterSCMMutation(ctx)
	a.showToast("success", "Conflict staged", clean)
	a.refresh()
}

// editMergeResult leaves merge mode and loads the current result into the
// ordinary editor buffer. Nothing is written until the user saves.
func (a *App) editMergeResult() {
	if !a.mergeOpen || a.mergeUnavailable {
		a.showToast("info", "Merge editor", "The result cannot be edited inline for this conflict.")
		a.refresh()

		return
	}

	path := a.mergePath
	if a.dirty[path] {
		a.showToast("info", "Unsaved edits", path+" already has unsaved edits.")
		a.refresh()

		return
	}

	result := a.mergeResult
	a.leaveMergeMode()
	a.mode = "changes"
	a.openFile(path)

	raw, err := a.ws.readFile(path)
	if err != nil {
		raw = []byte(result)
	}

	doc := a.ws.openDoc(path)
	a.docs[path] = doc

	buffer := edit.NewBuffer(path, raw)
	buffer.SetText(result)
	a.edit = buffer
	a.editing = true
	a.editCaret = 0
	a.editAnchor = 0
	a.editUndo = nil
	a.editRedo = nil
	a.preview = false
	a.findOpen = false
	a.dirty[path] = buffer.Dirty
	a.commitEditBuffer()
	a.resetScroll()
	a.showToast("info", "Editing merge result", "Ctrl+S saves. Reopening the conflict re-parses the file from disk.")
	a.refresh()
}

// leaveMergeMode drops the merge editor state without touching disk.
func (a *App) leaveMergeMode() {
	a.mergeOpen = false
	a.mergePath = ""
	a.mergeResult = ""
	a.mergeConflicts = nil
	a.mergeIndex = 0
	a.mergeCompare = false
	a.mergeDirty = false
	a.mergeStamp = mergeFileStamp{}
	a.mergeBase = ""
	a.mergeBaseSource = ""
	a.mergeBaseLines = 0
	a.mergeUnavailable = false
	a.mergeNote = ""
	a.mergeRows = nil
}

// closeMergeEditor leaves merge mode and returns to Source Control.
func (a *App) closeMergeEditor() {
	a.leaveMergeMode()
	a.mode = "changes"
	a.resetScroll()
	a.refresh()
}

// reconcileMergeAfterReload closes a stale merge editor when the conflict was
// resolved elsewhere. Unsaved results stay open so work is not lost.
func (a *App) reconcileMergeAfterReload() {
	if a.mode != "merge" || !a.mergeOpen || a.mergeDirty {
		return
	}

	if findFile(a.changes.Conflicts, a.mergePath) == nil {
		a.leaveMergeMode()
		a.mode = "changes"
	}
}
