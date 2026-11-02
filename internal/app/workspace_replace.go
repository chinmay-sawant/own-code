package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// replaceSkipped counts the files one workspace apply pass refused, by
// reason. Binary, oversized, gitignored, dirty, live-edit, unreadable, and
// never-loaded files are never written; the footer reports them instead.
type replaceSkipped struct {
	Binary   int
	Large    int
	Ignored  int
	Dirty    int
	ReadErr  int
	Unloaded int
}

// Total counts every skipped file.
func (s replaceSkipped) Total() int {
	return s.Binary + s.Large + s.Ignored + s.Dirty + s.ReadErr + s.Unloaded
}

// String renders the footer summary, or "" when nothing was skipped.
func (s replaceSkipped) String() string {
	var parts []string

	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}

	add(s.Binary, "binary")
	add(s.Large, "large")
	add(s.Ignored, "ignored")
	add(s.Dirty, "dirty")
	add(s.ReadErr, "unreadable")
	add(s.Unloaded, "unloaded")

	if len(parts) == 0 {
		return ""
	}

	return "Skipped: " + strings.Join(parts, ", ")
}

// appliedFile is one workspace-undo journal entry: the exact bytes before
// and after the apply, plus the replaced hit count.
type appliedFile struct {
	Path  string
	Old   []byte
	New   []byte
	Count int
}

// replacePlan is one confirmed replace-all: the checked files in order with
// their full hit lists from the uncapped rescan. ScanSkipped carries the
// scan-time eligibility counts the apply footer reports.
type replacePlan struct {
	Files       []string
	Hits        map[string][]workspaceHit
	Total       int
	Lookup      map[string][]workspaceHit
	ScanSkipped replaceSkipped
}

// fileChecked reports whether a path's file checkbox is on. A missing entry
// means checked; toggling a file off records false.
func (a *App) fileChecked(path string) bool {
	if a.replaceCheck == nil {
		return true
	}

	checked, ok := a.replaceCheck[path]
	return !ok || checked
}

// toggleReplaceFile flips one file's checkbox. Hits arrive sorted by path,
// so toggling by hit index always names a real file.
func (a *App) toggleReplaceFile(index int) {
	if index < 0 || index >= len(a.searchHits) {
		return
	}

	path := a.searchHits[index].Path

	if a.replaceCheck == nil {
		a.replaceCheck = map[string]bool{}
	}

	a.replaceCheck[path] = !a.fileChecked(path)
	a.refresh()
}

// replaceBlockedFiles lists the checked files that must stop an apply: files
// with unsaved edits or a live edit buffer. The apply refuses the whole
// gesture and names them instead of writing around them.
func (a *App) replaceBlockedFiles(paths []string) []string {
	var blocked []string

	for _, path := range paths {
		if a.dirty[path] || (a.editing && a.editPath() == path) {
			blocked = append(blocked, path)
		}
	}

	return blocked
}

// sweepWorkspace loads every unloaded directory up to the workspace file
// budget, so a lazy tree never silently misses files during a replace-all.
// Directories still unloaded afterwards count as skipped, never as clean.
func (a *App) sweepWorkspace() int {
	if a.ws == nil || a.ws.Sample || a.ws.Root == "" || !a.ws.Lazy {
		return 0
	}

	for len(a.ws.Files) < MaxWorkspaceFiles {
		var pending []*fileNode
		collectUnloadedDirs(a.ws.Tree, &pending)

		if len(pending) == 0 {
			break
		}

		loaded := false

		for _, node := range pending {
			if len(a.ws.Files) >= MaxWorkspaceFiles {
				break
			}

			if err := a.ws.loadNode(node); err != nil {
				continue
			}

			loaded = true
		}

		if !loaded {
			break
		}
	}

	var rest []*fileNode
	collectUnloadedDirs(a.ws.Tree, &rest)

	return len(rest)
}

// collectUnloadedDirs gathers the directory nodes a lazy tree never loaded.
func collectUnloadedDirs(nodes []*fileNode, out *[]*fileNode) {
	for _, node := range nodes {
		if node == nil || !node.Dir {
			continue
		}

		if !node.Loaded {
			*out = append(*out, node)
			continue
		}

		collectUnloadedDirs(node.Children, out)
	}
}

// replaceRescan runs the uncapped apply scan over the swept workspace and
// groups the hits by file in sorted order. Invalid patterns yield an error
// for the inline message; a total past the ceiling aborts with a narrowing
// error instead of a partial plan.
func (a *App) replaceRescan(ctx context.Context) (*replacePlan, error) {
	m := compileMatcher(a.searchQueryForScan(), a.workspaceSearchOpts())
	if !m.valid || m.Err != "" {
		a.searchError = m.Err
		return nil, fmt.Errorf("%s", m.Err)
	}

	if m.empty {
		return &replacePlan{Hits: map[string][]workspaceHit{}, Lookup: map[string][]workspaceHit{}}, nil
	}

	result := searchWorkspaceOpts(ctx, a.ws, a.searchQueryForScan(), a.workspaceSearchOpts(), workspaceReplaceCeil+1, workspaceReplaceCeil+1)
	if result.Capped {
		return nil, fmt.Errorf("more than %d matches; narrow the query first", workspaceReplaceCeil)
	}

	plan := &replacePlan{Hits: map[string][]workspaceHit{}, Lookup: map[string][]workspaceHit{}, ScanSkipped: result.Skipped}

	for _, hit := range result.Hits {
		if _, ok := plan.Hits[hit.Path]; !ok {
			plan.Files = append(plan.Files, hit.Path)
		}

		plan.Hits[hit.Path] = append(plan.Hits[hit.Path], hit)
		plan.Total++
	}

	plan.Lookup = plan.Hits

	return plan, nil
}

// searchQueryForScan returns the raw query for regex mode and the trimmed
// query for literal mode.
func (a *App) searchQueryForScan() string {
	if a.searchRegex {
		return a.searchQuery
	}

	return strings.TrimSpace(a.searchQuery)
}

// openReplaceConfirm stages the replace-all confirm dialog: title with the
// checked hit and file counts, the first paths for review, and the skipped
// estimate. The apply pass rescans and rechecks everything.
func (a *App) openReplaceConfirm(ctx context.Context) error {
	if a.ws.Sample {
		a.showToast("info", "Sample workspace is read-only", "Open a disk folder with -root to replace across files.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.searchError != "" {
		a.showToast("error", "Invalid pattern", a.searchError)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	a.sweepWorkspace()

	plan, err := a.replaceRescan(ctx)
	if err != nil {
		a.showToast("error", "Replace", err.Error())
		a.refresh()

		return a.page.Redraw(ctx)
	}

	var files []string
	total := 0

	for _, path := range plan.Files {
		if !a.fileChecked(path) {
			continue
		}

		files = append(files, path)
		total += len(plan.Hits[path])
	}

	if total == 0 {
		a.showToast("info", "Replace", "No checked matches to replace.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if blocked := a.replaceBlockedFiles(files); len(blocked) > 0 {
		a.showToast("error", "Save first", "Unsaved edits in: "+strings.Join(blocked, ", "))
		a.refresh()

		return a.page.Redraw(ctx)
	}

	sort.Strings(files)

	a.confirmOpen = true
	a.confirmKind = confirmKindReplaceAll
	a.confirmPaths = files
	a.confirmTitle = fmt.Sprintf("Replace %d in %d files?", total, len(files))
	a.confirmMsg = fmt.Sprintf("This replaces %s with %q across %d files. Skipped files are listed after the apply. Undo restores every file with one button, not Ctrl+Z.",
		pluralOccurrences(total), a.searchReplace, len(files))
	a.refresh()

	return a.page.Redraw(ctx)
}

// applyWorkspaceReplace runs the confirmed replace-all: dirty/sample guards,
// eligibility recheck per file, raw-line replace preserving CRLF, writeFile,
// doc refresh, journaling, and skipped reporting. Failures leave earlier
// files applied and report the rest.
func (a *App) applyWorkspaceReplace(ctx context.Context) error {
	if a.ws.Sample {
		a.showToast("info", "Sample workspace is read-only", "Open a disk folder with -root to replace across files.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	unloaded := a.sweepWorkspace()

	plan, err := a.replaceRescan(ctx)
	if err != nil {
		a.showToast("error", "Replace", err.Error())
		a.refresh()

		return a.page.Redraw(ctx)
	}

	skipped := plan.ScanSkipped
	skipped.Unloaded += unloaded

	var files []string

	for _, path := range plan.Files {
		if a.fileChecked(path) {
			files = append(files, path)
		}
	}

	if len(files) == 0 {
		a.replaceSkipped = skipped
		msg := "No checked matches to replace."
		if skipped.Total() > 0 {
			msg += " (" + skipped.String() + ")"
		}
		a.showToast("info", "Replace", msg)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if blocked := a.replaceBlockedFiles(files); len(blocked) > 0 {
		a.showToast("error", "Save first", "Unsaved edits in: "+strings.Join(blocked, ", "))
		a.refresh()

		return a.page.Redraw(ctx)
	}

	m := compileMatcher(a.searchQueryForScan(), a.workspaceSearchOpts())
	journal := make([]appliedFile, 0, len(files))
	journalBytes := 0
	journalOK := true
	applied := 0
	replaced := 0

	for _, path := range files {
		meta := a.ws.ByPath[path]
		if meta == nil {
			skipped.ReadErr++
			continue
		}

		if meta.Ignored {
			skipped.Ignored++
			continue
		}

		if !searchEligibleFile(meta) {
			skipped.Large++
			continue
		}

		raw, err := a.ws.readFile(path)
		if err != nil {
			skipped.ReadErr++
			continue
		}

		if len(raw) > maxFileBytes {
			skipped.Large++
			continue
		}

		if isBinary(raw) {
			skipped.Binary++
			continue
		}

		next, count := replaceRawBytes(raw, m, a.searchReplace)
		if count == 0 {
			continue
		}

		if err := a.ws.writeFile(path, next); err != nil {
			skipped.ReadErr++
			continue
		}

		if journalBytes+len(raw)+len(next) > replaceJournalCapBytes {
			journalOK = false
		} else {
			journal = append(journal, appliedFile{Path: path, Old: raw, New: next, Count: count})
			journalBytes += len(raw) + len(next)
		}

		if _, open := a.docs[path]; open {
			a.docs[path] = a.ws.openDoc(path)
		}

		applied++
		replaced += count
	}

	a.replaceJournal = journal
	a.replaceJournalOK = journalOK && len(journal) > 0
	a.replaceJournalMsg = ""
	if len(journal) > 0 && !journalOK {
		a.replaceJournalMsg = "Undo unavailable: the change is over 5 MB."
	}
	a.replaceSkipped = skipped

	a.runWorkspaceSearch(ctx)

	msg := fmt.Sprintf("%s in %d files", pluralOccurrences(replaced), applied)
	if skipped.Total() > 0 {
		msg += " (" + skipped.String() + ")"
	}
	if len(journal) > 0 && !journalOK {
		msg += ". " + a.replaceJournalMsg
	}

	a.showToast("success", "Replaced", msg)
	a.refresh()

	return a.page.Redraw(ctx)
}

// undoWorkspaceReplace restores every journaled file to its pre-apply bytes.
// Files edited since the apply are left alone and reported.
func (a *App) undoWorkspaceReplace(ctx context.Context) error {
	if len(a.replaceJournal) == 0 || !a.replaceJournalOK {
		note := a.replaceJournalMsg
		if note == "" {
			note = "There is no workspace replace to undo."
		}

		a.showToast("info", "Undo replace", note)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.ws.Sample {
		a.showToast("info", "Sample workspace is read-only", "Undo is unavailable in the sample workspace.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	restored := 0
	var kept []string

	for i := len(a.replaceJournal) - 1; i >= 0; i-- {
		entry := a.replaceJournal[i]

		if a.dirty[entry.Path] || (a.editing && a.editPath() == entry.Path) {
			kept = append(kept, entry.Path)
			continue
		}

		if err := a.ws.writeFile(entry.Path, entry.Old); err != nil {
			kept = append(kept, entry.Path)
			continue
		}

		if _, open := a.docs[entry.Path]; open {
			a.docs[entry.Path] = a.ws.openDoc(entry.Path)
		}

		restored++
	}

	a.replaceJournal = nil
	a.replaceJournalOK = false
	a.replaceJournalMsg = ""
	a.runWorkspaceSearch(ctx)

	if len(kept) > 0 {
		a.showToast("success", "Undo replace", fmt.Sprintf("Restored %d files; kept edits in: %s", restored, strings.Join(kept, ", ")))
	} else {
		a.showToast("success", "Undo replace", fmt.Sprintf("Restored %d files", restored))
	}

	a.refresh()

	return a.page.Redraw(ctx)
}

// replaceRawBytes applies the matcher to raw file bytes line by line. Match
// lines strip a trailing CR for matching and reattach it, so CRLF files keep
// their endings. A leading BOM is preserved and kept out of the match. It
// returns the new bytes and the replaced hit count.
func replaceRawBytes(raw []byte, m *matcher, replacement string) ([]byte, int) {
	if len(raw) == 0 || m == nil || !m.valid || m.empty {
		return raw, 0
	}

	text := string(raw)
	trailingNL := strings.HasSuffix(text, "\n")
	parts := strings.Split(text, "\n")
	total := 0

	for i, part := range parts {
		if trailingNL && i == len(parts)-1 && part == "" && len(parts) > 1 {
			continue
		}

		line := part
		bom := ""

		if i == 0 && strings.HasPrefix(line, "\ufeff") {
			bom = "\ufeff"
			line = strings.TrimPrefix(line, "\ufeff")
		}

		core := strings.TrimSuffix(line, "\r")
		cr := line[len(core):]

		spans := m.spansInLine([]rune(core))
		if len(spans) == 0 {
			continue
		}

		runes := []rune(core)

		for j := len(spans) - 1; j >= 0; j-- {
			span := spans[j]
			matched := string(runes[span.Start:span.End])
			insert := []rune(m.expandReplacement(matched, replacement))
			next := make([]rune, 0, len(runes)-(span.End-span.Start)+len(insert))
			next = append(next, runes[:span.Start]...)
			next = append(next, insert...)
			next = append(next, runes[span.End:]...)
			runes = next
		}

		parts[i] = bom + string(runes) + cr
		total += len(spans)
	}

	return []byte(strings.Join(parts, "\n")), total
}
