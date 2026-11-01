package app

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// indentLines indents or outdents every non-empty line. Empty lines stay
// empty so the operation never sprinkles trailing whitespace. Outdent strips
// one tab, one tabSpaces run, or up to tabWidth spaces. The operation is
// pure: callers apply it in one buffer change for a single undo point.
func indentLines(lines []string, outdent bool) []string {
	out := append([]string(nil), lines...)

	for i, line := range lines {
		if line == "" {
			continue
		}

		if !outdent {
			out[i] = tabSpaces + line

			continue
		}

		out[i] = stripOneIndent(line)
	}

	return out
}

// stripOneIndent removes one indent level from a line's leading whitespace.
func stripOneIndent(line string) string {
	if strings.HasPrefix(line, "\t") {
		return strings.TrimPrefix(line, "\t")
	}

	if strings.HasPrefix(line, tabSpaces) {
		return strings.TrimPrefix(line, tabSpaces)
	}

	stripped := strings.TrimLeft(line, " ")
	removed := len(line) - len(stripped)

	if removed > tabWidth {
		removed = tabWidth
	}

	return strings.Repeat(" ", len(line)-removed-len(stripped)) + stripped
}

// indentSelection indents or outdents the edit selection in one undo point.
// A collapsed caret indents at the caret (Tab) or strips one level from the
// caret line (Shift+Tab). A multi-line selection indents every line in the
// range, excluding the last line when the selection ends at column 0, and
// expands the selection to the touched block.
func (a *App) indentSelection(ctx context.Context, outdent bool) error {
	if !a.editing || a.edit == nil {
		return nil
	}

	text, start, end := a.editSelection()

	if start == end {
		if !outdent {
			return a.insertEditText(ctx, tabSpaces)
		}

		return a.outdentCaretLine(ctx, text, start)
	}

	lines := edit.SelectionLines(text)
	startLine, _ := offsetLineCol(text, start)
	endLine, endCol := offsetLineCol(text, end)

	if endLine > startLine && endCol == 0 {
		endLine--
	}

	raw := make([]string, 0, endLine-startLine+1)
	for i := startLine; i <= endLine; i++ {
		raw = append(raw, lines[i].Text)
	}

	next := indentLines(raw, outdent)
	spanStart := lines[startLine].Start
	spanEnd := lines[endLine].Start + len([]rune(lines[endLine].Text))
	insert := strings.Join(next, "\n")

	if err := a.replaceEditRange(ctx, text, spanStart, spanEnd, insert); err != nil {
		return err
	}

	// Dirty-range correctness: every touched line retokenizes instead of
	// only the caret line.
	if doc := a.docs[a.edit.Path]; doc != nil && doc.Syntax != nil {
		for line := startLine; line <= endLine; line++ {
			doc.Syntax.markDirty(line)
		}
	}

	// Expand the selection to the touched block.
	a.editAnchor = spanStart
	a.editCaret = spanStart + utf8.RuneCountInString(insert)
	a.ensureEditCaretVisible()
	a.refresh()

	return a.page.Redraw(ctx)
}

// outdentCaretLine strips one indent level from the caret's line and keeps
// the caret glued to its text.
func (a *App) outdentCaretLine(ctx context.Context, text string, caret int) error {
	lines := edit.SelectionLines(text)
	line, _ := offsetLineCol(text, caret)

	spanStart := lines[line].Start
	spanEnd := spanStart + len([]rune(lines[line].Text))
	next := stripOneIndent(lines[line].Text)

	if next == lines[line].Text {
		return nil
	}

	removed := len([]rune(lines[line].Text)) - len([]rune(next))

	if err := a.replaceEditRange(ctx, text, spanStart, spanEnd, next); err != nil {
		return err
	}

	a.editAnchor = max(spanStart, caret-removed)
	a.editCaret = a.editAnchor
	a.ensureEditCaretVisible()
	a.refresh()

	return a.page.Redraw(ctx)
}

// leadingWhitespace returns the space/tab prefix of a line, verbatim.
func leadingWhitespace(line string) string {
	i := 0

	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}

	return line[:i]
}

// markdownListMarker returns the list marker a newline should repeat:
// "- " for bullets, or the next number for ordered lists.
func markdownListMarker(trimmed string) string {
	for _, bullet := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(trimmed, bullet) {
			return bullet
		}
	}

	if dot := strings.IndexByte(trimmed, '.'); dot > 0 && dot+1 < len(trimmed) && trimmed[dot+1] == ' ' {
		if n, err := strconv.Atoi(trimmed[:dot]); err == nil {
			return strconv.Itoa(n+1) + ". "
		}
	}

	return ""
}

// indentForNewline computes the indent for a newline inserted on curLine:
// the current leading whitespace, plus one level after an opener, or the
// repeated Markdown list marker.
func indentForNewline(lang, curLine string) string {
	base := leadingWhitespace(curLine)
	trimmed := strings.TrimSpace(curLine)

	if trimmed == "" {
		return base
	}

	if lang == langMarkdown {
		if marker := markdownListMarker(trimmed); marker != "" {
			return base + marker
		}
	}

	last := trimmed[len(trimmed)-1]

	switch lang {
	case langGo, langJS, langCSS, langJSON, langHTML:
		if last == '{' || last == '(' || last == '[' {
			return base + tabSpaces
		}
	case langPython, langShell, langYAML:
		if last == ':' {
			return base + tabSpaces
		}
	}

	return base
}

// insertNewlineWithIndent replaces the selection with a newline carrying the
// language indent, in one undo point. It replaces the bare newline insert in
// onSubmit.
func (a *App) insertNewlineWithIndent(ctx context.Context) error {
	if !a.editing || a.edit == nil {
		return nil
	}

	text, start, end := a.editSelection()
	lines := edit.SelectionLines(text)
	line, _ := offsetLineCol(text, start)

	curLine := ""
	if line < len(lines) {
		curLine = lines[line].Text
	}

	return a.replaceEditRange(ctx, text, start, end, "\n"+indentForNewline(a.editLang(), curLine))
}
