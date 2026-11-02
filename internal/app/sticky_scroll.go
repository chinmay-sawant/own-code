package app

// Sticky scroll pins the enclosing symbol chain above the editor while the
// body scrolls underneath. Up to maxStickyRows header rows show the symbols
// enclosing the current scroll-top source line (e.g. a class plus the method
// the viewport is inside), derived from the outline: the last symbol with
// Line <= top line, plus its parents via Depth. Clicking a header jumps to
// the symbol through the group-aware scroll path.
//
// The headers are disabled in diff (changes), preview, and merge modes, and
// files with no symbols show nothing (no placeholder rows).

// maxStickyRows caps the pinned header block so it never eats the viewport.
const maxStickyRows = 3

// stickyRow is one pinned header: the enclosing symbol plus its 1-based
// display line. It mirrors symbolRow so templates read the same fields.
type stickyRow struct {
	Name  string
	Kind  string
	Line  int
	No    int
	Depth int
}

// stickyChain returns the enclosing symbol chain for topLine, ordered
// outer to inner. rows must be in line order (the outline extractors append
// in line order). It returns nil when no symbol starts at or above topLine.
func stickyChain(rows []symbolRow, topLine int) []stickyRow {
	idx := -1

	for i, row := range rows {
		if row.Line <= topLine {
			idx = i
		} else {
			break
		}
	}

	if idx < 0 {
		return nil
	}

	chain := []stickyRow{{
		Name:  rows[idx].Name,
		Kind:  rows[idx].Kind,
		Line:  rows[idx].Line,
		No:    rows[idx].No,
		Depth: rows[idx].Depth,
	}}

	want := rows[idx].Depth

	for j := idx - 1; j >= 0; j-- {
		if rows[j].Depth >= want {
			continue
		}

		chain = append(chain, stickyRow{
			Name:  rows[j].Name,
			Kind:  rows[j].Kind,
			Line:  rows[j].Line,
			No:    rows[j].No,
			Depth: rows[j].Depth,
		})
		want = rows[j].Depth

		if want <= 0 {
			break
		}
	}

	// Reverse to outer-first.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	if len(chain) > maxStickyRows {
		chain = chain[len(chain)-maxStickyRows:]
	}

	return chain
}

// stickyTopLine is the source line at the top of one editor group's view,
// following the wrapped plan when soft wrap is on.
func (a *App) stickyTopLine(group int) int {
	doc := a.groupDoc(group)
	if doc == nil || len(doc.Lines) == 0 {
		return 0
	}

	offset := a.scrollY
	if a.split && (group == 0 || group == 1) {
		offset = a.scrollGroup[group]
	}

	visual := offset / a.zx(rowH)
	if visual < 0 {
		visual = 0
	}

	if a.wrap {
		plan := doc.wrapped(a.codeColsForGroup(group))
		if len(plan.Rows) == 0 {
			return 0
		}

		if visual >= len(plan.Rows) {
			visual = len(plan.Rows) - 1
		}

		return plan.Rows[visual].Line
	}

	if visual >= len(doc.Lines) {
		visual = len(doc.Lines) - 1
	}

	return visual
}

// stickyEnabledForGroup reports whether pinned headers may show in one
// group. Diff (changes), preview, and merge modes are excluded; empty,
// binary, and symbol-free files show nothing.
func (a *App) stickyEnabledForGroup(group int) bool {
	if a.stickyOff {
		return false
	}

	if a.mode != "explorer" {
		return false
	}

	doc := a.groupDoc(group)
	if doc == nil || doc.Err != "" || doc.Binary || len(doc.Outline) == 0 {
		return false
	}

	if a.preview && a.activeGroup() == group && doc.Lang == langMarkdown {
		return false
	}

	return true
}

// stickyForGroup returns the pinned headers for one editor group.
func (a *App) stickyForGroup(group int) []stickyRow {
	if !a.stickyEnabledForGroup(group) {
		return nil
	}

	doc := a.groupDoc(group)

	return stickyChain(doc.Outline, a.stickyTopLine(group))
}

// scrollToSticky jumps one editor group to a sticky header's symbol line,
// keeping the wrapped plan's first visual row on screen.
func (a *App) scrollToSticky(group, line int) {
	doc := a.groupDoc(group)
	if doc == nil || len(doc.Lines) == 0 {
		return
	}

	if line < 0 {
		line = 0
	}

	if line >= len(doc.Lines) {
		line = len(doc.Lines) - 1
	}

	target := line
	if a.wrap {
		target = doc.wrapped(a.codeColsForGroup(group)).firstRow(line)
	}

	y := target * a.zx(rowH)
	if !a.split && group == 0 {
		a.scrollTo(y)

		return
	}

	a.scrollGroupTo(group, y)
}

// toggleStickyScroll flips the pinned symbol headers and persists the
// session. The headers stay off until toggled back on.
func (a *App) toggleStickyScroll() {
	a.stickyOff = !a.stickyOff
	a.refresh()
	a.persistSession()
}

// stickyTopPx is the pixel headroom the pinned headers take above one
// group's windowed rows. Paint and hit-testing offsets add it to the code
// body's top edge so rows, presses, and floats agree on row positions.
func (a *App) stickyTopPx(group int) int {
	return len(a.stickyForGroup(group)) * a.zx(rowH)
}

// windowSliceReserve is windowSliceForGroup with headroom for pinned UI: the
// visible code capacity shrinks by reserve rows so the sticky block plus the
// code rows stay exactly full.
func (a *App) windowSliceReserve(group, total, reserve int) (int, int, int) {
	capacity := a.editorClearHeight()/a.zx(rowH) - reserve
	if capacity < 1 {
		capacity = 1
	}

	offset := a.scrollY
	if a.split && (group == 0 || group == 1) {
		offset = a.scrollGroup[group]
	}

	if offset < 0 {
		offset = 0
	}

	first := offset / a.zx(rowH)
	tail := 0
	if total > capacity {
		tail = total - capacity
	}

	if first > tail {
		first = tail
	}

	// Diff rows do not go through paneRowCount, so their spacer stops at the
	// status bar. At that limit, show the tail in the clear area.
	if limit := a.pageScrollLimit(); limit > 0 && offset >= limit && first < tail {
		first = tail
	}

	end := first + capacity
	if end > total {
		end = total
	}

	return first, end, 0
}
