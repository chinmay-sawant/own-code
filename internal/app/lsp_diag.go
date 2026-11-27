package app

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

// lspDiag is one diagnostic in editor display coordinates: columns count
// runes after tab expansion, the same space the code rows use.
type lspDiag struct {
	Line     int // zero based
	From     int
	To       int
	Severity int
	Message  string
	Source   string
	Unopened bool
}

// newLSPDiag converts a protocol diagnostic against the exact lines the
// server saw. UTF-16 offsets become rune columns, then display columns.
func newLSPDiag(lines []string, d lsp.Diagnostic) lspDiag {
	// An unopened file has no synced text. Keep the reported source
	// position for the list and jump. Editor squiggles use synced text.
	if lines == nil {
		return lspDiag{
			Line: max(0, d.Range.Start.Line), From: max(0, d.Range.Start.Character),
			Severity: diagnosticSeverity(d.Severity), Message: strings.TrimSpace(d.Message), Source: d.Source, Unopened: true,
		}
	}

	line := max(0, min(d.Range.Start.Line, len(lines)-1))
	raw := ""

	if line >= 0 && line < len(lines) {
		raw = lines[line]
	}

	runes := []rune(raw)
	from := lsp.UTF16ToRuneCol(raw, d.Range.Start.Character)
	to := len(runes)

	if d.Range.End.Line == d.Range.Start.Line {
		to = lsp.UTF16ToRuneCol(raw, d.Range.End.Character)
	}

	if to <= from {
		to = from + 1
	}

	disp := func(col int) int {
		col = min(col, len(runes))

		return len([]rune(expandTabs(string(runes[:col]))))
	}

	fromD := disp(from)
	toD := disp(to)

	if to > len(runes) {
		toD += to - len(runes)
	}

	return lspDiag{
		Line: line, From: fromD, To: toD, Severity: diagnosticSeverity(d.Severity),
		Message: strings.TrimSpace(d.Message), Source: d.Source,
	}
}

func diagnosticSeverity(sev int) int {
	if sev < lsp.SeverityError || sev > lsp.SeverityHint {
		return lsp.SeverityError
	}
	return sev
}

func diagClass(sev int) string {
	switch sev {
	case lsp.SeverityError:
		return "diag-err"
	case lsp.SeverityWarning:
		return "diag-warn"
	default:
		return "diag-info"
	}
}

// decorateDiagnostics paints squiggles and the gutter marker on the visible
// rows of doc. Rows must be owned by the caller (the render path clones them).
func (a *App) decorateDiagnostics(doc *fileDoc, rows []codeRow) {
	if a.lspm == nil || doc == nil || len(rows) == 0 {
		return
	}

	diags := a.lspm.diags[doc.Path]
	if len(diags) == 0 {
		return
	}

	byLine := map[int][]lspDiag{}
	for _, d := range diags {
		if !d.Unopened {
			byLine[d.Line] = append(byLine[d.Line], d)
		}
	}

	for i := range rows {
		row := &rows[i]

		list := byLine[row.Line]
		if len(list) == 0 {
			continue
		}

		length := segmentRuneLen(row.Segments)

		for _, d := range list {
			from := max(d.From, row.Start)
			to := min(d.To, row.Start+length)

			if row.Segments != nil && from < to && d.Severity <= lsp.SeverityInfo {
				row.Segments = markRange(row.Segments, from-row.Start, to-row.Start, diagClass(d.Severity))
			}
		}

		if row.No == 0 {
			continue
		}

		worst := list[0]
		for _, d := range list {
			if d.Severity < worst.Severity {
				worst = d
			}
		}

		row.DiagClass = "gutter-" + diagClass(worst.Severity)
		row.DiagMark = "!"

		if worst.Severity >= lsp.SeverityInfo {
			row.DiagMark = "i"
		}

		row.DiagHint = clipGutterHint(worst.Message)
	}
}

// lspProblem is one row in the Problems list.
type lspProblem struct {
	Path     string
	Line     int // zero based
	Col      int
	Severity int
	Message  string
}

// problemRow is one Problems entry shown in the bottom panel.
type problemRow struct {
	Index int
	Name  string
	Where string
	Text  string // clipped for display
	Path  string
	Full  string // complete message, for the tooltip and aria-label
	Class string
	Mark  string
}

const (
	problemRowHeight   = 32
	problemPagerHeight = 34
)

// clipProblemText flattens a diagnostic message to one line of at most max
// runes, marking a cut with an ellipsis. The full text stays in the row's
// title and aria-label.
func clipProblemText(msg string, max int) string {
	msg = strings.Join(strings.Fields(msg), " ")

	runes := []rune(msg)
	if len(runes) <= max {
		return msg
	}

	return string(runes[:max-1]) + "…"
}

type problemCounts struct {
	Errors, Warnings, Info, Files, Total int
}

func (c *problemCounts) add(diags []lspDiag, delta int) {
	total := 0
	for _, d := range diags {
		switch d.Severity {
		case lsp.SeverityError:
			c.Errors += delta
		case lsp.SeverityWarning:
			c.Warnings += delta
		case lsp.SeverityInfo:
			c.Info += delta
		default:
			continue
		}
		total++
	}
	if total > 0 {
		c.Files += delta
		c.Total += total * delta
	}
}

// lspView is the diagnostics data for the status bar and bottom panel.
type lspView struct {
	problemCounts
	Show, Open  bool
	First, Last int
	Prev, Next  bool
	Problems    []problemRow
	// Progress is the active server work-done text for the status bar.
	Progress string
}

// rebuildProblems updates the ordered index once for each diagnostics batch.
func (m *lspManager) rebuildProblems() {
	if !m.problemsDirty {
		return
	}

	all := m.problems[:0]
	for path, list := range m.diags {
		for _, d := range list {
			if d.Severity >= lsp.SeverityError && d.Severity <= lsp.SeverityInfo {
				all = append(all, lspProblem{Path: path, Line: d.Line, Col: d.From, Severity: d.Severity, Message: d.Message})
			}
		}
	}
	slices.SortFunc(all, func(x, y lspProblem) int {
		if x.Severity != y.Severity {
			return cmp.Compare(x.Severity, y.Severity)
		}
		if x.Path != y.Path {
			return strings.Compare(x.Path, y.Path)
		}
		if x.Line != y.Line {
			return cmp.Compare(x.Line, y.Line)
		}
		if x.Col != y.Col {
			return cmp.Compare(x.Col, y.Col)
		}
		return strings.Compare(x.Message, y.Message)
	})
	if len(all) < len(m.problems) {
		clear(m.problems[len(all):])
	}
	m.problems = all
	m.problemsDirty = false
}

func (a *App) problemPageSize() int {
	h := a.termPanelHeight() - a.zx(termResizeH) - a.zx(termHeadH) - a.zx(problemPagerHeight) - 1
	return max(1, h/a.zx(problemRowHeight))
}

func (a *App) lspViewData() lspView {
	m := a.lspm
	if m == nil {
		return lspView{Open: a.problemsOpen}
	}
	v := lspView{problemCounts: m.problemSummary}
	v.Open = a.problemsOpen
	v.Progress = m.progressText()
	v.Show = len(m.servers) > 0 || v.Total > 0
	if !v.Open {
		return v
	}
	m.rebuildProblems()
	start, end := a.problemScroll.Rows(v.Total, a.problemPageSize())
	v.Prev, v.Next = start > 0, end < v.Total
	if end > start {
		v.First, v.Last = start+1, end
	}
	width := a.editorWidth()
	pathLimit := max(8, min(54, width/a.zx(16)))
	messageLimit := max(8, min(140, (width-a.zx(56))/a.zx(8)))
	v.Problems = make([]problemRow, 0, end-start)
	for i := start; i < end; i++ {
		p := m.problems[i]
		mark := "!"
		if p.Severity == lsp.SeverityInfo {
			mark = "i"
		}
		v.Problems = append(v.Problems, problemRow{
			Index: i,
			Name:  clipProblemText(p.Path, pathLimit),
			Path:  p.Path,
			Where: fmt.Sprintf("%d:%d", p.Line+1, p.Col+1),
			Text:  clipProblemText(p.Message, messageLimit),
			Full:  p.Message,
			Class: diagClass(p.Severity),
			Mark:  mark,
		})
	}

	return v
}

func (a *App) showProblems() {
	if !a.problemsOpen {
		a.problemScroll = historyScroll{}
	}
	a.termOpen, a.termFocus, a.problemsOpen = false, false, true
	a.refresh()
}

func (a *App) scrollProblems(rows int) {
	a.problemScroll.ScrollBy(rows, a.view.LSP.Total, a.problemPageSize())
	a.refresh()
}

// openProblem jumps to one Problems entry.
func (a *App) openProblem(index int) {
	m := a.lspm
	if m == nil || index < 0 || index >= len(m.problems) {
		return
	}

	p := m.problems[index]
	if a.activeDocPath() != p.Path || a.mode != "explorer" {
		a.openFile(p.Path)
	}

	if a.activeDocPath() == p.Path && a.mode == "explorer" {
		a.scrollToSourceLine(p.Line)
		a.refresh()
	}
}
