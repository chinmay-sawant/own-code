package app

import (
	"fmt"
	"sort"
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
}

// newLSPDiag converts a protocol diagnostic against the exact lines the
// server saw. UTF-16 offsets become rune columns, then display columns.
func newLSPDiag(lines []string, d lsp.Diagnostic) lspDiag {
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

	sev := d.Severity
	if sev < lsp.SeverityError || sev > lsp.SeverityHint {
		sev = lsp.SeverityError
	}

	return lspDiag{
		Line: line, From: fromD, To: toD, Severity: sev,
		Message: strings.TrimSpace(d.Message), Source: d.Source,
	}
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
		byLine[d.Line] = append(byLine[d.Line], d)
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

// problemRow is one Problems entry as the Details drawer prints it.
type problemRow struct {
	Index int
	Name  string
	Where string
	Text  string
	Class string
	Mark  string
}

const problemRowsShown = 8

// lspView is the diagnostics data for the status bar and Details drawer.
type lspView struct {
	Show     bool
	Errors   int
	Warnings int
	Total    int
	More     int
	Problems []problemRow
}

// lspViewData builds the view and refreshes the click index.
func (a *App) lspViewData() lspView {
	m := a.lspm
	if m == nil {
		return lspView{}
	}

	var all []lspProblem

	v := lspView{}

	for path, list := range m.diags {
		for _, d := range list {
			switch d.Severity {
			case lsp.SeverityError:
				v.Errors++
			case lsp.SeverityWarning:
				v.Warnings++
			}

			if d.Severity <= lsp.SeverityInfo {
				all = append(all, lspProblem{Path: path, Line: d.Line, Col: d.From, Severity: d.Severity, Message: d.Message})
			}
		}
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Severity != all[j].Severity {
			return all[i].Severity < all[j].Severity
		}

		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}

		return all[i].Line < all[j].Line
	})

	m.problems = all
	v.Total = len(all)
	v.Show = len(m.servers) > 0 && len(m.docs) > 0 || len(all) > 0

	for i, p := range all {
		if i >= problemRowsShown {
			v.More = len(all) - problemRowsShown

			break
		}

		mark := "!"
		if p.Severity >= lsp.SeverityInfo {
			mark = "i"
		}

		v.Problems = append(v.Problems, problemRow{
			Index: i,
			Name:  baseName(p.Path),
			Where: fmt.Sprintf("%d:%d", p.Line+1, p.Col+1),
			Text:  clipGutterHint(p.Message),
			Class: diagClass(p.Severity),
			Mark:  mark,
		})
	}

	return v
}

// openProblem jumps to one Problems entry.
func (a *App) openProblem(index int) {
	m := a.lspm
	if m == nil || index < 0 || index >= len(m.problems) {
		return
	}

	p := m.problems[index]
	if a.activeDocPath() != p.Path {
		a.openFile(p.Path)
	}

	a.scrollToSourceLine(p.Line)
	a.refresh()
}
