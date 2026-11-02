package app

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// findHit is one match in source-line rune coordinates.
type findHit struct {
	Line  int
	Start int
	End   int
}

// findCurrent is the 1-based visible match number.
func (a *App) findCurrent() int {
	if !a.findOpen || len(a.findHits) == 0 {
		return 0
	}

	return a.findIndex + 1
}

// reindexFind rebuilds the match list for the active document with the shared
// matcher, so the find bar honors the case, regex, and whole-word toggles.
// An invalid regex leaves no hits and records the inline error; the caller
// surfaces a.findError and disables Replace until the pattern compiles.
func (a *App) reindexFind() {
	a.findHits = nil
	a.findIndex = 0
	a.findError = ""

	doc := a.activeDoc()
	if doc == nil || doc.Err != "" || doc.Binary {
		return
	}

	m := compileMatcher(a.findQuery, a.fileSearchOpts())
	if !m.valid || m.Err != "" {
		a.findError = m.Err
		return
	}

	if m.empty {
		return
	}

	for lineNo, line := range doc.Lines {
		for _, span := range m.spansInLine([]rune(line)) {
			a.findHits = append(a.findHits, findHit{Line: lineNo, Start: span.Start, End: span.End})
		}
	}
}

// openFind shows the find bar and focuses its field. The replace state is
// left alone, so Ctrl+F never collapses an open replace bar.
func (a *App) openFind(ctx context.Context) error {
	if a.mode != "explorer" {
		a.showToast("info", "Find", "Find searches the file view. Return to Explorer.")
		a.refresh()

		return nil
	}

	if a.activeDoc() == nil {
		return nil
	}

	if !a.findOpen {
		a.findReturn = a.page.Form().FocusID()
		a.findOpen = true
	}

	a.reindexFind()
	a.refresh()

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	if err := a.page.Form().Focus(ctx, "find"); err != nil {
		return err
	}

	return a.page.Edit().SelectAll(ctx)
}

// openReplace shows the find bar with the replace row and focuses the find
// field. A single-line edit selection seeds the query when the bar opens
// with an empty query.
func (a *App) openReplace(ctx context.Context) error {
	if a.mode != "explorer" {
		a.showToast("info", "Replace", "Replace edits the file view. Return to Explorer.")
		a.refresh()

		return nil
	}

	if a.activeDoc() == nil {
		return nil
	}

	if !a.findOpen {
		a.findReturn = a.page.Form().FocusID()
		a.findOpen = true
	}

	if strings.TrimSpace(a.findQuery) == "" {
		if seed, ok := a.replaceSeedQuery(); ok {
			seed = clampFieldLen(seed)
			a.findQuery = seed
			a.page.Form().SetFormValue("find", seed)
		}
	}

	a.replaceOpen = true
	a.reindexFind()
	a.refresh()

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	if err := a.page.Form().Focus(ctx, "find"); err != nil {
		return err
	}

	return a.page.Edit().SelectAll(ctx)
}

// replaceSeedQuery returns the single-line edit selection for seeding the
// find query, or false when there is no usable selection.
func (a *App) replaceSeedQuery() (string, bool) {
	if !a.editing || a.edit == nil {
		return "", false
	}

	text, start, end := a.editSelection()
	if start == end {
		return "", false
	}

	sel := string([]rune(text)[start:end])
	if strings.Contains(sel, "\n") {
		return "", false
	}

	if strings.TrimSpace(sel) == "" {
		return "", false
	}

	return sel, true
}

// closeFind hides the bar and the replace row and restores the previous focus
// when possible. The match toggles stay sticky across open and close and are
// persisted in the session; only the queries and hits are cleared.
func (a *App) closeFind(ctx context.Context) error {
	previous := a.findReturn
	a.findOpen = false
	a.replaceOpen = false
	a.findQuery = ""
	a.replaceQuery = ""
	a.findError = ""
	a.findHits = nil
	a.findIndex = 0
	a.findReturn = ""
	a.refresh()

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	if previous != "" && a.page.Form().FocusID() == "" {
		return a.page.Form().Focus(ctx, previous)
	}

	return nil
}

// findStep moves to the next or previous match and reveals it.
func (a *App) findStep(delta int) {
	if len(a.findHits) == 0 {
		a.refresh()

		return
	}

	a.findIndex = (a.findIndex + delta + len(a.findHits)) % len(a.findHits)
	hit := a.findHits[a.findIndex]
	a.refresh()
	a.scrollToSourceLine(hit.Line)
}

// replaceEnabled reports whether a replace gesture may run: the bar is open,
// the query compiled, and at least one hit exists.
func (a *App) replaceEnabled() bool {
	return a.findOpen && a.replaceOpen && a.findError == "" && len(a.findHits) > 0
}

// toggleFindCase flips case sensitivity and rebuilds the matches.
func (a *App) toggleFindCase() {
	a.findCase = !a.findCase
	a.reindexFind()
	a.refresh()
}

// toggleFindRegex flips regex matching and rebuilds the matches.
func (a *App) toggleFindRegex() {
	a.replaceRegex = !a.replaceRegex
	a.reindexFind()
	a.refresh()
}

// toggleFindWholeWord flips whole-word matching and rebuilds the matches.
func (a *App) toggleFindWholeWord() {
	a.replaceWholeWord = !a.replaceWholeWord
	a.reindexFind()
	a.refresh()
}

// toggleMatchKeys flips one shared match toggle for the bar that owns the
// keyboard: Alt+C for case, Alt+R for regex, Alt+W for whole word. The
// palette and branch picker keep their keys; the confirm dialog and settings
// return before this runs. Focus stays where it was.
func (a *App) toggleMatchKeys(ctx context.Context, key string) error {
	if a.paletteOpen || a.branchOpen {
		return nil
	}

	focus := a.page.Form().FocusID()

	switch focus {
	case "find", "find-replace":
		a.toggleFileMatchKey(key)
		a.refresh()

		if err := a.page.Redraw(ctx); err != nil {
			return err
		}

		_ = a.page.Form().Focus(ctx, focus)

		return nil
	case "workspace-search", "workspace-replace":
		a.toggleWorkspaceMatchKey(ctx, key)
		a.refresh()

		if err := a.page.Redraw(ctx); err != nil {
			return err
		}

		_ = a.page.Form().Focus(ctx, focus)

		return nil
	}

	if a.searchOpen {
		a.toggleWorkspaceMatchKey(ctx, key)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.findOpen {
		a.toggleFileMatchKey(key)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	return nil
}

// toggleFileMatchKey flips one in-file matcher toggle and reindexes.
func (a *App) toggleFileMatchKey(key string) {
	switch key {
	case "alt+c":
		a.findCase = !a.findCase
	case "alt+r":
		a.replaceRegex = !a.replaceRegex
	case "alt+w":
		a.replaceWholeWord = !a.replaceWholeWord
	default:
		return
	}

	a.reindexFind()
}

// toggleWorkspaceMatchKey flips one workspace matcher toggle and reruns.
func (a *App) toggleWorkspaceMatchKey(ctx context.Context, key string) {
	switch key {
	case "alt+c":
		a.searchCase = !a.searchCase
	case "alt+r":
		a.searchRegex = !a.searchRegex
	case "alt+w":
		a.searchWholeWord = !a.searchWholeWord
	default:
		return
	}

	a.searchIndex = 0
	a.runWorkspaceSearch(ctx)
}

// bufferRep is one rune-offset replacement in buffer text.
type bufferRep struct {
	Start  int
	End    int
	Insert string
}

// replaceOne replaces the current hit with the replace query and advances.
// The replacement expands $1/$name references in regex mode. Exactly one
// undo point is recorded on the live-buffer path; the document path folds
// the change into the dirty document.
func (a *App) replaceOne(ctx context.Context) error {
	if !a.findOpen || !a.replaceOpen {
		return nil
	}

	if a.findError != "" {
		a.showToast("error", "Invalid pattern", a.findError)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if len(a.findHits) == 0 {
		return nil
	}

	doc := a.activeDoc()
	if doc == nil || doc.Err != "" || doc.Binary {
		a.showToast("info", "Replace", "Open a text file before replacing.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	m := compileMatcher(a.findQuery, a.fileSearchOpts())
	if !m.valid || m.Err != "" {
		a.findError = m.Err
		a.showToast("error", "Invalid pattern", m.Err)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.editing && a.edit != nil && a.edit.Path == doc.Path {
		hit := a.findHits[min(a.findIndex, len(a.findHits)-1)]
		start, end := a.hitBufferRange(a.edit.Text(), hit)
		matched := string([]rune(doc.Lines[hit.Line])[hit.Start:hit.End])
		insert := m.expandReplacement(matched, a.replaceQuery)

		if err := a.replaceEditRanges(ctx, []bufferRep{{Start: start, End: end, Insert: insert}}); err != nil {
			return err
		}

		a.reindexFind()
		a.clampFindIndex()
		a.refresh()

		return a.page.Redraw(ctx)
	}

	hit := a.findHits[min(a.findIndex, len(a.findHits)-1)]
	lineRunes := []rune(doc.Lines[hit.Line])
	matched := string(lineRunes[hit.Start:hit.End])
	insert := m.expandReplacement(matched, a.replaceQuery)

	next := make([]rune, 0, len(lineRunes)-(hit.End-hit.Start)+len([]rune(insert)))
	next = append(next, lineRunes[:hit.Start]...)
	next = append(next, []rune(insert)...)
	next = append(next, lineRunes[hit.End:]...)

	lines := append([]string(nil), doc.Lines...)
	lines[hit.Line] = string(next)
	refreshDocLines(doc, lines)
	a.dirty[doc.Path] = true

	a.reindexFind()
	a.clampFindIndex()
	a.showToast("success", "Replaced", "1 occurrence in "+doc.Path)
	a.refresh()

	return a.page.Redraw(ctx)
}

// replaceAllInFile replaces every hit in the active file. The live-buffer
// path records exactly one undo point for the whole gesture; the document
// path applies one grouped change to the dirty document.
func (a *App) replaceAllInFile(ctx context.Context) error {
	if !a.findOpen || !a.replaceOpen {
		return nil
	}

	if a.findError != "" {
		a.showToast("error", "Invalid pattern", a.findError)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if len(a.findHits) == 0 {
		return nil
	}

	doc := a.activeDoc()
	if doc == nil || doc.Err != "" || doc.Binary {
		a.showToast("info", "Replace", "Open a text file before replacing.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	m := compileMatcher(a.findQuery, a.fileSearchOpts())
	if !m.valid || m.Err != "" {
		a.findError = m.Err
		a.showToast("error", "Invalid pattern", m.Err)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.editing && a.edit != nil && a.edit.Path == doc.Path {
		text := a.edit.Text()
		reps := make([]bufferRep, 0, len(a.findHits))

		for _, hit := range a.findHits {
			start, end := a.hitBufferRange(text, hit)
			matched := string([]rune(doc.Lines[hit.Line])[hit.Start:hit.End])
			reps = append(reps, bufferRep{Start: start, End: end, Insert: m.expandReplacement(matched, a.replaceQuery)})
		}

		count := len(reps)
		if err := a.replaceEditRanges(ctx, reps); err != nil {
			return err
		}

		a.reindexFind()
		a.showToast("success", "Replaced", pluralOccurrences(count)+" in "+doc.Path)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	byLine := map[int][]findHit{}
	for _, hit := range a.findHits {
		byLine[hit.Line] = append(byLine[hit.Line], hit)
	}

	lines := append([]string(nil), doc.Lines...)
	count := 0

	for lineNo, hits := range byLine {
		lineRunes := []rune(lines[lineNo])
		out := lineRunes

		for i := len(hits) - 1; i >= 0; i-- {
			hit := hits[i]
			matched := string(lineRunes[hit.Start:hit.End])
			insert := []rune(m.expandReplacement(matched, a.replaceQuery))
			next := make([]rune, 0, len(out)-(hit.End-hit.Start)+len(insert))
			next = append(next, out[:hit.Start]...)
			next = append(next, insert...)
			next = append(next, out[hit.End:]...)
			out = next
			count++
		}

		lines[lineNo] = string(out)
	}

	refreshDocLines(doc, lines)
	a.dirty[doc.Path] = true

	a.reindexFind()
	a.showToast("success", "Replaced", pluralOccurrences(count)+" in "+doc.Path)
	a.refresh()

	return a.page.Redraw(ctx)
}

// clampFindIndex keeps the current hit on screen after a replace removed it.
// The index now points at the next match, so Replace keeps advancing.
func (a *App) clampFindIndex() {
	if len(a.findHits) == 0 {
		a.refresh()
		return
	}

	if a.findIndex >= len(a.findHits) {
		a.findIndex = len(a.findHits) - 1
	}

	a.refresh()
	a.scrollToSourceLine(a.findHits[a.findIndex].Line)
}

// hitBufferRange maps one display-coordinate hit (doc.Lines carry
// tab-expanded text) back to rune offsets in the live buffer text.
func (a *App) hitBufferRange(text string, hit findHit) (int, int) {
	rawLine := lineTextAt(text, hit.Line)
	rawStart := rawColumnAt(rawLine, hit.Start)
	rawEnd := rawColumnAt(rawLine, hit.End)
	lines := edit.SelectionLines(text)

	return edit.OffsetAtPoint(lines, hit.Line, rawStart), edit.OffsetAtPoint(lines, hit.Line, rawEnd)
}

// replaceEditRanges applies several rune-offset replacements to the live
// buffer with exactly one undo point for the whole gesture. Offsets refer to
// the text before any replacement; they are applied end-to-start so earlier
// offsets stay valid.
func (a *App) replaceEditRanges(ctx context.Context, reps []bufferRep) error {
	if a.edit == nil || len(reps) == 0 {
		return nil
	}

	text := a.edit.Text()
	n := utf8.RuneCountInString(text)

	ordered := append([]bufferRep(nil), reps...)
	sortBufferReps(ordered, n)
	if len(ordered) == 0 {
		return nil
	}

	a.pushUndo()
	a.editRedo = nil

	out := []rune(text)
	for _, r := range ordered {
		insert := []rune(r.Insert)
		next := make([]rune, 0, len(out)-(r.End-r.Start)+len(insert))
		next = append(next, out[:r.Start]...)
		next = append(next, insert...)
		next = append(next, out[r.End:]...)
		out = next
	}

	a.edit.SetText(string(out))
	a.dirty[a.edit.Path] = a.edit.Dirty

	last := ordered[len(ordered)-1]
	a.editCaret = last.Start + utf8.RuneCountInString(last.Insert)
	a.editAnchor = a.editCaret
	line, _ := a.editDisplayLineCol()
	if doc := a.docs[a.edit.Path]; doc != nil && doc.Syntax != nil {
		doc.Syntax.markDirty(line)
	}
	a.commitEditBuffer()
	a.ensureEditCaretVisible()
	a.refresh()

	return a.page.Redraw(ctx)
}

// sortBufferReps clamps replacements to the buffer and orders them
// end-to-start. Empty or inverted ranges are dropped.
func sortBufferReps(reps []bufferRep, n int) []bufferRep {
	kept := reps[:0]

	for _, r := range reps {
		start := edit.ClampPos(r.Start, n)
		end := edit.ClampPos(r.End, n)
		if start >= end {
			continue
		}

		kept = append(kept, bufferRep{Start: start, End: end, Insert: r.Insert})
	}

	sort.Slice(kept, func(i, j int) bool { return kept[i].Start > kept[j].Start })

	return kept
}

// refreshDocLines installs new display lines into a document without touching
// disk, so the source view shows the replaced text and the file reads dirty.
func refreshDocLines(doc *fileDoc, lines []string) {
	doc.Lines = lines
	doc.Tokens = doc.tokenizeLines(lines)
	doc.Outline = outline(doc.Lang, lines)

	var size int64
	for _, line := range lines {
		size += int64(len(line)) + 1
	}

	doc.ByteSize = size
	doc.Truncated = len(lines) > maxFileLines
	doc.Preview = nil
	doc.PreviewLine = nil
	doc.Wrap = nil
}

// pluralOccurrences renders "1 occurrence" or "N occurrences".
func pluralOccurrences(n int) string {
	if n == 1 {
		return "1 occurrence"
	}

	return strconv.Itoa(n) + " occurrences"
}

// markFindRows adds the match classes to a copy of the visible rows.
func (a *App) markFindRows(rows []codeRow) {
	if !a.findOpen || len(a.findHits) == 0 {
		return
	}

	for i := range rows {
		row := &rows[i]
		if row.Segments == nil {
			continue
		}

		start := row.Start
		length := 0
		for _, seg := range row.Segments {
			length += utf8.RuneCountInString(seg.Text)
		}

		end := start + length

		for hitIndex, hit := range a.findHits {
			if hit.Line != row.Line {
				continue
			}

			from := max(hit.Start, start)
			to := min(hit.End, end)
			if from >= to {
				continue
			}

			extra := "find-hit"
			if hitIndex == a.findIndex {
				extra += " find-cur"
			}

			row.Segments = markRange(row.Segments, from-start, to-start, extra)
		}
	}
}

// copyFindRows copies the visible rows before a find mark mutates them.
func (a *App) copyFindRows(rows []codeRow) []codeRow {
	if !a.findOpen || len(a.findHits) == 0 || len(rows) == 0 {
		return rows
	}

	out := append([]codeRow(nil), rows...)
	a.markFindRows(out)

	return out
}
