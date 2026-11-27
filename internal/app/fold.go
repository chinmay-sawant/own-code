package app

import (
	"sort"
	"strings"
)

// Code folding. A fold hides the lines under an indent-based region while
// its first line stays visible with a chevron in the gutter. Every consumer
// that turns scroll rows into source lines (and back) goes through the row
// plan (wrapPlan): when folds are active, or soft wrap is on, a plan maps
// rows to lines and skips hidden lines; otherwise rows equal lines. The
// helpers rowPlan, rowOfLine, and lineOfRow are the single mapping layer.

// noWrapCols plans one row per source line while folds are active and soft
// wrap is off. It is far wider than any file line.
const noWrapCols = 1 << 20

// foldHidden is the immutable hidden-line view for one fold generation. The
// wrap plan keeps a pointer to it so a changed view rebuilds the plan.
type foldHidden struct {
	mask []bool // mask[line] is true for a line hidden inside a folded region
	ends map[int]int
}

// foldRegions returns the indent regions of the document: first line to last
// line (inclusive). The result is cached against the current Lines slice.
func (d *fileDoc) foldRegions() map[int]int {
	if d == nil || len(d.Lines) == 0 {
		return nil
	}

	if d.regsKeyLines == len(d.Lines) && d.regsKeyPtr == &d.Lines[0] {
		return d.regs
	}

	oldLen := d.regsKeyLines
	d.regs = computeFoldRegions(d.Lines)
	d.regsKeyLines = len(d.Lines)
	d.regsKeyPtr = &d.Lines[0]
	d.reconcileFolds(oldLen)

	return d.regs
}

// reconcileFolds keeps fold state across an edit. A fold stays when its
// start line still opens a region with the same text; otherwise it follows
// the line-count delta once, and is dropped when neither fits.
func (d *fileDoc) reconcileFolds(oldLen int) {
	if len(d.Folded) == 0 {
		return
	}

	delta := len(d.Lines) - oldLen
	next := make(map[int]string, len(d.Folded))

	for start, text := range d.Folded {
		for _, cand := range []int{start, start + delta} {
			if cand < 0 || cand >= len(d.Lines) {
				continue
			}

			if _, ok := d.regs[cand]; ok && d.Lines[cand] == text {
				next[cand] = text

				break
			}
		}
	}

	d.Folded = next
	d.foldGen++
}

// computeFoldRegions finds indent regions with one stack pass. A region
// opens on a non-blank line followed by a deeper line and runs to the last
// non-blank line that is deeper than the opener.
func computeFoldRegions(lines []string) map[int]int {
	type open struct{ line, indent int }

	var (
		stack []open
		last  = -1
		regs  = map[int]int{}
	)

	closeTo := func(indent int) {
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]

			if last > top.line {
				regs[top.line] = last
			}
		}
	}

	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		closeTo(foldIndent(line))
		stack = append(stack, open{i, foldIndent(line)})
		last = i
	}

	closeTo(-1)

	return regs
}

// foldIndent counts indent columns (tabs count as four).
func foldIndent(line string) int {
	n := 0

	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}

	return n
}

// foldMask returns the hidden-line view, or nil when no fold hides a line.
func (d *fileDoc) foldMask() *foldHidden {
	if d == nil {
		return nil
	}

	if len(d.Folded) == 0 || len(d.Lines) == 0 {
		d.foldView = nil

		return nil
	}

	regs := d.foldRegions() // may prune Folded
	if len(d.Folded) == 0 {
		d.foldView = nil

		return nil
	}

	if d.foldView != nil && d.foldViewGen == d.foldGen && len(d.foldView.mask) == len(d.Lines) {
		return d.foldView
	}

	view := &foldHidden{mask: make([]bool, len(d.Lines)), ends: map[int]int{}}

	for start := range d.Folded {
		end := regs[start]
		view.ends[start] = end

		for l := start + 1; l <= end && l < len(view.mask); l++ {
			view.mask[l] = true
		}
	}

	d.foldView = view
	d.foldViewGen = d.foldGen

	return view
}

// foldedAt reports whether line is the start of a collapsed fold.
func (d *fileDoc) foldedAt(line int) bool {
	if d == nil {
		return false
	}

	d.foldRegions() // reconcile fold state with the current lines first

	_, ok := d.Folded[line]

	return ok
}

// foldable reports whether line opens a region.
func (d *fileDoc) foldable(line int) bool {
	_, ok := d.foldRegions()[line]

	return ok
}

// toggleFold folds or unfolds the region starting at line.
func (d *fileDoc) toggleFold(line int) bool {
	if d == nil || !d.foldable(line) {
		return false
	}

	if d.Folded == nil {
		d.Folded = map[int]string{}
	}

	if _, ok := d.Folded[line]; ok {
		delete(d.Folded, line)
	} else {
		d.Folded[line] = d.Lines[line]
	}

	d.foldGen++
	d.Wrap = nil

	return true
}

// foldAround toggles the fold that covers line: the region opening on the
// line, else the innermost enclosing one. fold selects fold versus unfold.
func (d *fileDoc) foldAround(line int, fold bool) bool {
	if d == nil {
		return false
	}

	target := -1

	if fold {
		// Innermost region that is not folded yet.
		for start, end := range d.foldRegions() {
			if line >= start && line <= end && !d.foldedAt(start) && start > target {
				target = start
			}
		}
	} else {
		// A fold on the line wins, else the innermost folded ancestor.
		if d.foldedAt(line) {
			target = line
		} else {
			for start, end := range d.foldRegions() {
				if line > start && line <= end && d.foldedAt(start) && start > target {
					target = start
				}
			}
		}
	}

	if target < 0 {
		return false
	}

	return d.toggleFold(target)
}

// foldAll collapses every top-level region; unfoldAll clears all folds.
func (d *fileDoc) foldAll() bool {
	if d == nil {
		return false
	}

	regs := d.foldRegions()
	if len(regs) == 0 {
		return false
	}

	d.Folded = map[int]string{}
	starts := make([]int, 0, len(regs))

	for start := range regs {
		starts = append(starts, start)
	}

	sort.Ints(starts)

	for _, start := range starts {
		d.Folded[start] = d.Lines[start]
	}

	d.foldGen++
	d.Wrap = nil

	return true
}

func (d *fileDoc) unfoldAll() bool {
	if d == nil || len(d.Folded) == 0 {
		return false
	}

	d.Folded = nil
	d.foldGen++
	d.Wrap = nil

	return true
}

// revealLine unfolds every fold hiding line.
func (d *fileDoc) revealLine(line int) bool {
	if d == nil || len(d.Folded) == 0 {
		return false
	}

	changed := false

	for start, end := range d.foldRegions() {
		if d.foldedAt(start) && line > start && line <= end {
			delete(d.Folded, start)

			changed = true
		}
	}

	if changed {
		d.foldGen++
		d.Wrap = nil
	}

	return changed
}

// foldStartFor returns the visible fold start that hides line, or -1.
func (d *fileDoc) foldStartFor(line int) int {
	best := -1

	for start, end := range d.foldRegions() {
		if d.foldedAt(start) && line > start && line <= end && (best < 0 || start < best) {
			best = start
		}
	}

	return best
}

// ---- the single row <-> source line mapping layer ----

// rowPlan returns the row plan for one document in one group, or nil when
// rows equal source lines (soft wrap off and nothing folded).
func (a *App) rowPlan(doc *fileDoc, group int) *wrapPlan {
	if doc == nil {
		return nil
	}

	if a.wrap {
		return doc.wrapped(a.codeColsForGroup(group))
	}

	if doc.foldMask() != nil {
		return doc.wrapped(noWrapCols)
	}

	return nil
}

// rowOfLine is the first visual row of a source line.
func (a *App) rowOfLine(doc *fileDoc, group, line int) int {
	if plan := a.rowPlan(doc, group); plan != nil {
		return plan.firstRow(line)
	}

	return line
}

// lineOfRow is the source line that owns one visual row.
func (a *App) lineOfRow(doc *fileDoc, group, row int) int {
	if plan := a.rowPlan(doc, group); plan != nil {
		return plan.lineAt(row)
	}

	return row
}

// decorateFolds paints the chevron state and the folded-tail marker.
func (a *App) decorateFolds(doc *fileDoc, rows []codeRow) {
	if doc == nil || len(rows) == 0 {
		return
	}

	regs := doc.foldRegions()
	if len(regs) == 0 {
		return
	}

	for i := range rows {
		row := &rows[i]
		if row.No <= 0 {
			continue
		}

		if _, ok := regs[row.Line]; !ok {
			continue
		}

		if doc.foldedAt(row.Line) {
			row.Fold = "closed"
			row.Segments = append(append([]segment(nil), row.Segments...), segment{Text: " ⋯", Class: "fold"})
		} else {
			row.Fold = "open"
		}
	}
}
