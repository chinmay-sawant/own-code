package app

import (
	"context"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// commentStyle is one line-comment form: prefix after the indent, plus an
// optional suffix at the line end for block-flavored styles (/* */,
// <!-- -->). JSON and plain text have no style and refuse with a toast.
type commentStyle struct {
	prefix string
	suffix string
}

// commentStyleFor returns the toggle-comment style for a language.
func commentStyleFor(lang string) (commentStyle, bool) {
	switch lang {
	case langGo, langJS:
		return commentStyle{prefix: "//"}, true
	case langPython, langShell, langYAML:
		return commentStyle{prefix: "#"}, true
	case langCSS:
		return commentStyle{prefix: "/*", suffix: "*/"}, true
	case langHTML, langMarkdown:
		return commentStyle{prefix: "<!--", suffix: "-->"}, true
	default:
		return commentStyle{}, false
	}
}

// toggleCommentLines toggles a line range between commented and uncommented.
// Blank lines are never touched. When every non-blank line is already
// commented the prefixes strip (idempotent re-toggle); otherwise every
// non-blank line gains the prefix after its indent. The operation is pure:
// callers apply it in one buffer change for a single undo point.
func toggleCommentLines(lines []string, style commentStyle) []string {
	out := append([]string(nil), lines...)

	nonBlank := make([]int, 0, len(lines))
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonBlank = append(nonBlank, i)
		}
	}

	if len(nonBlank) == 0 {
		return out
	}

	allCommented := true

	for _, i := range nonBlank {
		if !isCommentedLine(lines[i], style) {
			allCommented = false

			break
		}
	}

	for _, i := range nonBlank {
		if allCommented {
			out[i] = uncommentLine(lines[i], style)
		} else {
			out[i] = commentLine(lines[i], style)
		}
	}

	return out
}

// isCommentedLine reports whether a line carries the style's prefix (and
// suffix when the style has one).
func isCommentedLine(line string, style commentStyle) bool {
	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	rest := line[indent:]

	if !strings.HasPrefix(rest, style.prefix) {
		return false
	}

	if style.suffix == "" {
		return true
	}

	trimmed := strings.TrimRight(line, " \t")

	return strings.HasSuffix(trimmed, style.suffix) && len(trimmed) > indent+len(style.prefix)
}

// commentLine adds the prefix after the indent (plus the spaced suffix when
// the style has one).
func commentLine(line string, style commentStyle) string {
	indent := len(line) - len(strings.TrimLeft(line, " \t"))

	next := line[:indent] + style.prefix + " " + line[indent:]
	if style.suffix != "" {
		next += " " + style.suffix
	}

	return next
}

// uncommentLine strips one prefix (plus one optional space) and, for
// suffixed styles, one trailing suffix (plus one optional space).
func uncommentLine(line string, style commentStyle) string {
	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	rest := strings.TrimPrefix(line[indent:], style.prefix)
	rest = strings.TrimPrefix(rest, " ")

	next := line[:indent] + rest

	if style.suffix != "" {
		trimmed := strings.TrimRight(next, " \t")
		trimmed = strings.TrimSuffix(trimmed, style.suffix)
		next = strings.TrimRight(trimmed, " \t")
	}

	return next
}

// toggleLineComment toggles comments over the edit selection in one undo
// point. The buffer must be open; JSON and plain text toast their missing
// style, and preview (which never has a buffer) no-ops with a toast.
func (a *App) toggleLineComment(ctx context.Context) error {
	if !a.editing || a.edit == nil {
		if doc := a.activeDoc(); doc != nil {
			if _, ok := commentStyleFor(doc.Lang); !ok {
				a.noCommentStyleToast(doc.Lang)
				a.refresh()

				return a.page.Redraw(ctx)
			}
		}

		a.showToast("info", "Toggle comment", "Press Ctrl+E to edit, then Ctrl+/ toggles comments.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	style, ok := commentStyleFor(a.editLang())
	if !ok {
		a.noCommentStyleToast(a.editLang())
		a.refresh()

		return a.page.Redraw(ctx)
	}

	text, start, end := a.editSelection()
	lines := edit.SelectionLines(text)

	startLine, _ := offsetLineCol(text, start)
	endLine, endCol := offsetLineCol(text, end)

	// A selection ending at column 0 excludes its last line.
	if endLine > startLine && endCol == 0 {
		endLine--
	}

	raw := make([]string, 0, endLine-startLine+1)
	for i := startLine; i <= endLine; i++ {
		raw = append(raw, lines[i].Text)
	}

	next := toggleCommentLines(raw, style)
	spanStart := lines[startLine].Start
	spanEnd := lines[endLine].Start + len([]rune(lines[endLine].Text))

	if err := a.replaceEditRange(ctx, text, spanStart, spanEnd, strings.Join(next, "\n")); err != nil {
		return err
	}

	// Dirty-range correctness: every touched line retokenizes instead of
	// only the caret line.
	if doc := a.docs[a.edit.Path]; doc != nil && doc.Syntax != nil {
		for line := startLine; line <= endLine; line++ {
			doc.Syntax.markDirty(line)
		}
	}

	return nil
}

// noCommentStyleToast explains why a language refuses the toggle.
func (a *App) noCommentStyleToast(lang string) {
	switch lang {
	case langJSON:
		a.showToast("info", "Toggle comment", "JSON has no comment style.")
	case langPlain:
		a.showToast("info", "Toggle comment", "Plain text has no comment style.")
	default:
		a.showToast("info", "Toggle comment", "This language has no comment style.")
	}
}
