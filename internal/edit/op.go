package edit

// Edit is one replacement over rune offsets: [Start, End) of the old text
// becomes Text. It is the unit for operation-based undo (an Edit plus its
// Invert) and for incremental LSP didChange, in place of whole-text
// snapshots.
type Edit struct {
	Start, End int
	Text       string
}

// Diff returns the single smallest Edit turning old into updated, found by
// trimming the common prefix and suffix. ok is false when the texts are
// equal.
func Diff(old, updated string) (Edit, bool) {
	a, b := []rune(old), []rune(updated)

	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}

	if prefix == len(a) && prefix == len(b) {
		return Edit{}, false
	}

	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}

	return Edit{Start: prefix, End: len(a) - suffix, Text: string(b[prefix : len(b)-suffix])}, true
}

// Apply returns text with e applied. Offsets clamp to the text.
func (e Edit) Apply(text string) string {
	r := []rune(text)
	start := ClampPos(e.Start, len(r))
	end := ClampPos(e.End, len(r))

	if end < start {
		end = start
	}

	return string(r[:start]) + e.Text + string(r[end:])
}

// Invert returns the Edit that undoes e when applied to the text e
// produced. old is the text e was applied to.
func (e Edit) Invert(old string) Edit {
	r := []rune(old)
	start := ClampPos(e.Start, len(r))
	end := ClampPos(e.End, len(r))

	if end < start {
		end = start
	}

	return Edit{Start: start, End: start + len([]rune(e.Text)), Text: string(r[start:end])}
}

// MapOffset moves a caret or selection offset across e: offsets before the
// edit stay, offsets inside the replaced range snap to its end, and offsets
// after shift by the length change.
func (e Edit) MapOffset(pos int) int {
	switch {
	case pos <= e.Start:
		return pos
	case pos < e.End:
		return e.Start + len([]rune(e.Text))
	default:
		return pos - (e.End - e.Start) + len([]rune(e.Text))
	}
}
