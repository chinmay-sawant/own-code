package app

import (
	"sort"
	"strconv"
	"unicode/utf8"
)

// LSP snippets: a completion item in snippet format is expanded to plain text
// and its tab stops ($1, ${2:name}, $0) are remembered so Tab can walk them.

// snippetStop is one tab stop as rune offsets into the expanded text.
type snippetStop struct{ start, end int }

// snippetSession is the Tab walk through the stops of the last snippet.
type snippetSession struct {
	path    string
	stops   []snippetStop // absolute rune offsets, in visiting order
	idx     int
	baseLen int // buffer rune count when stops were last adjusted
}

// parseSnippet expands a snippet to text and returns its tab stops in visiting
// order: ascending number, with $0 last. The first occurrence of a number wins
// (mirrors are not tracked); choices keep their first entry; variables are
// left as typed.
func parseSnippet(s string) (string, []snippetStop) {
	p := &snippetParser{in: []rune(s), spans: map[int]snippetStop{}}
	p.run(false)

	nums := make([]int, 0, len(p.spans))

	for n := range p.spans {
		if n > 0 {
			nums = append(nums, n)
		}
	}

	sort.Ints(nums)

	stops := make([]snippetStop, 0, len(p.spans))
	for _, n := range nums {
		stops = append(stops, p.spans[n])
	}

	if z, ok := p.spans[0]; ok {
		stops = append(stops, z)
	}

	return string(p.out), stops
}

type snippetParser struct {
	in    []rune
	i     int
	out   []rune
	spans map[int]snippetStop
}

func (p *snippetParser) number() (int, bool) {
	j := p.i
	for j < len(p.in) && p.in[j] >= '0' && p.in[j] <= '9' {
		j++
	}

	if j == p.i {
		return 0, false
	}

	n, err := strconv.Atoi(string(p.in[p.i:j]))
	if err != nil {
		return 0, false
	}

	p.i = j

	return n, true
}

func (p *snippetParser) record(n, start int) {
	if _, ok := p.spans[n]; !ok {
		p.spans[n] = snippetStop{start, len(p.out)}
	}
}

// run consumes input until the end, or until the closing brace of a
// placeholder when nested.
func (p *snippetParser) run(nested bool) {
	for p.i < len(p.in) {
		r := p.in[p.i]

		switch {
		case nested && r == '}':
			return
		case r == '\\' && p.i+1 < len(p.in):
			p.out = append(p.out, p.in[p.i+1])
			p.i += 2
		case r == '$' && p.i+1 < len(p.in) && p.in[p.i+1] >= '0' && p.in[p.i+1] <= '9':
			p.i++
			n, _ := p.number()
			p.record(n, len(p.out))
		case r == '$' && p.i+1 < len(p.in) && p.in[p.i+1] == '{':
			save := p.i
			p.i += 2

			n, ok := p.number()
			if !ok || p.i >= len(p.in) {
				p.i = save + 1
				p.out = append(p.out, '$')

				continue
			}

			start := len(p.out)

			switch p.in[p.i] {
			case ':':
				p.i++
				p.run(true)
			case '|':
				p.i++
				for p.i < len(p.in) && p.in[p.i] != ',' && p.in[p.i] != '|' {
					p.out = append(p.out, p.in[p.i])
					p.i++
				}

				for p.i < len(p.in) && p.in[p.i] != '}' {
					p.i++
				}
			}

			if p.i < len(p.in) && p.in[p.i] == '}' {
				p.i++
			}

			p.record(n, start)
		default:
			p.out = append(p.out, r)
			p.i++
		}
	}
}

// expandSnippet reduces a snippet to plain text and the rune offset where the
// caret goes: the first tab stop, else the end.
func expandSnippet(s string) (string, int) {
	text, stops := parseSnippet(s)
	if len(stops) == 0 {
		return text, utf8.RuneCountInString(text)
	}

	return text, stops[0].start
}

// beginSnippet selects the first stop of a snippet inserted at base and, when
// more stops follow, starts the Tab session.
func (a *App) beginSnippet(base int, stops []snippetStop) {
	if a.edit == nil || a.lspm == nil || len(stops) == 0 {
		return
	}

	abs := make([]snippetStop, len(stops))
	for i, s := range stops {
		abs[i] = snippetStop{base + s.start, base + s.end}
	}

	sess := &a.lspm.ext.snip
	*sess = snippetSession{}

	if len(abs) > 1 {
		*sess = snippetSession{path: a.edit.Path, stops: abs, baseLen: utf8.RuneCountInString(a.edit.Text())}
	}

	a.selectSnippetStop(abs[0])
}

func (a *App) selectSnippetStop(s snippetStop) {
	n := utf8.RuneCountInString(a.edit.Text())
	a.editAnchor, a.editCaret = lspClamp(s.start, n), lspClamp(s.end, n)
	a.ensureEditCaretVisible()
	a.refresh()
}

// snippetTab moves to the next tab stop of the active snippet. It reports
// whether Tab was consumed; a caret outside the current stop ends the session.
func (a *App) snippetTab() bool {
	if a.lspm == nil || a.edit == nil || !a.editing {
		return false
	}

	sess := &a.lspm.ext.snip
	if len(sess.stops) == 0 || sess.path != a.edit.Path || sess.idx+1 >= len(sess.stops) {
		*sess = snippetSession{}

		return false
	}

	now := utf8.RuneCountInString(a.edit.Text())
	delta := now - sess.baseLen
	cur := sess.stops[sess.idx]

	if a.editCaret < cur.start || a.editCaret > cur.end+delta || a.editAnchor < cur.start || a.editAnchor > cur.end+delta {
		*sess = snippetSession{}

		return false
	}

	sess.stops[sess.idx].end += delta

	for j := sess.idx + 1; j < len(sess.stops); j++ {
		sess.stops[j].start += delta
		sess.stops[j].end += delta
	}

	sess.baseLen = now
	sess.idx++
	next := sess.stops[sess.idx]

	if sess.idx == len(sess.stops)-1 {
		*sess = snippetSession{}
	}

	a.selectSnippetStop(next)

	return true
}
