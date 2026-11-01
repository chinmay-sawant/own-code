package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// Go to definition resolves the symbol under the caret without an LSP: the
// current file's outline first (exact name), then the workspace symbol index
// (recency, then shortest path), then a textual fallback that reports its
// match count and opens workspace search. F12 stays with the dev dock, so the
// chord is Ctrl+D (verified free: no other branch consumes it) with the
// keymap action id "goto-definition", the palette command of the same id,
// and Ctrl+Click on a code line.

// gotoDefinitionWordAt returns the identifier around a rune caret. A caret
// between a word and a boundary (or at the end of the buffer) resolves to
// the word on its left, the way a click lands.
func gotoDefinitionWordAt(text string, caret int) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}

	caret = edit.ClampPos(caret, len(runes))

	at := caret
	if at >= len(runes) || !isIdentRune(runes[at]) {
		if at > 0 && isIdentRune(runes[at-1]) {
			at--
		} else {
			return ""
		}
	}

	start := at
	for start > 0 && isIdentRune(runes[start-1]) {
		start--
	}

	end := at
	for end < len(runes) && isIdentRune(runes[end]) {
		end++
	}

	return string(runes[start:end])
}

// gotoIsIdent reports whether every rune of name continues an identifier.
func gotoIsIdent(name string) bool {
	if name == "" {
		return false
	}

	for _, r := range name {
		if !isIdentRune(r) {
			return false
		}
	}

	return true
}

// symbolAtCaret resolves the word under the edit caret. It prefers the
// syntax token at the caret when the highlighter marked it fn/type/var (a
// definition or a call), and falls back to the identifier runes around the
// caret. Tokens are only trusted when the buffer line still matches the
// document line they were built from, so unsaved edits never mis-resolve.
func (a *App) symbolAtCaret() (string, bool) {
	doc := a.activeDoc()
	if doc == nil || doc.Err != "" || doc.Binary {
		return "", false
	}

	if !a.editing || a.edit == nil || a.edit.Path != doc.Path {
		return "", false
	}

	text := a.edit.Text()
	caret := edit.ClampPos(a.editCaret, utf8.RuneCountInString(text))
	line, col := offsetLineCol(text, caret)

	if name, ok := gotoTokenSymbolAt(doc, lineTextAt(text, line), doc.Lines, doc.Tokens, line, col); ok {
		return name, true
	}

	if word := gotoDefinitionWordAt(text, caret); word != "" {
		return word, true
	}

	return "", false
}

// gotoTokenSymbolAt returns the syntax token covering a raw rune column when
// it carries a definition-like class. The buffer line must equal the document
// line the tokens were built from, and the segments must still partition it
// exactly; otherwise the lookup declines and the caller falls back to
// identifier runes.
func gotoTokenSymbolAt(doc *fileDoc, lineText string, docLines []string, tokens [][]segment, line, col int) (string, bool) {
	if doc == nil || line < 0 || line >= len(tokens) || line >= len(docLines) {
		return "", false
	}

	if lineText != docLines[line] {
		return "", false
	}

	segs := tokens[line]
	total := 0
	for _, seg := range segs {
		total += utf8.RuneCountInString(seg.Text)
	}

	runes := []rune(lineText)
	if total != len(runes) || total == 0 {
		return "", false
	}

	at := col
	if at < 0 {
		return "", false
	}

	if at >= total {
		at = total - 1
	}

	pos := 0
	for _, seg := range segs {
		width := utf8.RuneCountInString(seg.Text)
		if at >= pos && at < pos+width {
			switch seg.Class {
			case classFn, classType, classVar:
				if name := strings.TrimSpace(seg.Text); gotoIsIdent(name) {
					return name, true
				}
			}

			return "", false
		}

		pos += width
	}

	return "", false
}

// definitionCandidates returns every workspace symbol with exactly word's
// name outside skipPath, skipping vendor/ignored/generated files. The order
// is recency (open tabs, then recents) with unranked files last, then
// shortest path, then path, then line.
func (a *App) definitionCandidates(word, skipPath string) []workspaceSymbol {
	if word == "" || a.ws == nil {
		return nil
	}

	idx := a.ensureSymbolIndex()
	if idx == nil {
		return nil
	}

	rank := a.symbolRecency()

	var out []workspaceSymbol

	for _, sym := range idx.flat {
		if sym.Name != word || sym.Path == "" || sym.Path == skipPath {
			continue
		}

		if gotoSkipCandidate(a.ws, sym.Path) {
			continue
		}

		out = append(out, sym)
	}

	const unranked = 1 << 30

	sort.SliceStable(out, func(i, j int) bool {
		ri, ok := rank[out[i].Path]
		if !ok {
			ri = unranked
		}

		rj, ok := rank[out[j].Path]
		if !ok {
			rj = unranked
		}

		if ri != rj {
			return ri < rj
		}

		if len(out[i].Path) != len(out[j].Path) {
			return len(out[i].Path) < len(out[j].Path)
		}

		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}

		return out[i].Line < out[j].Line
	})

	return out
}

// gotoSkipCandidate reports whether a workspace candidate stays out of the
// definition search: gitignored files, anything under a skipped directory
// (vendor, node_modules, dist, dot dirs), binary/archive extensions, and
// generated sources.
func gotoSkipCandidate(ws *workspace, path string) bool {
	if ws == nil {
		return true
	}

	if meta := ws.ByPath[path]; meta != nil && meta.Ignored {
		return true
	}

	for _, seg := range strings.Split(path, "/") {
		if skipDir(seg) {
			return true
		}
	}

	if skipExt(baseName(path)) {
		return true
	}

	return gotoGeneratedFile(ws, path)
}

// gotoGeneratedFile reports generated sources by name (*.pb.go, *.gen.go, or
// "generated" anywhere in the name) or by a generator marker in the first
// lines ("code generated", "@generated", "auto-generated"). An unreadable
// file is not generated: the caller already decided it is readable enough to
// index.
func gotoGeneratedFile(ws *workspace, path string) bool {
	lower := strings.ToLower(baseName(path))
	if strings.HasSuffix(lower, ".pb.go") || strings.HasSuffix(lower, ".gen.go") ||
		strings.Contains(lower, "generated") {
		return true
	}

	raw, err := ws.readFile(path)
	if err != nil {
		return false
	}

	if len(raw) > 64<<10 {
		raw = raw[:64<<10]
	}

	lines := strings.Split(strings.ToLower(string(raw)), "\n")
	if len(lines) > 8 {
		lines = lines[:8]
	}

	for _, line := range lines {
		if strings.Contains(line, "code generated") ||
			strings.Contains(line, "@generated") ||
			strings.Contains(line, "auto-generated") {
			return true
		}
	}

	return false
}

// gotoDefinitionAtCaret jumps to the definition of the symbol under the edit
// caret: the current file's outline first, then the workspace index, then a
// textual fallback that opens workspace search. Same-file jumps move the
// caret in place; cross-file jumps open the file (recorded nav history and
// dirty guards flow through openFile) and land on the definition line.
func (a *App) gotoDefinitionAtCaret(ctx context.Context) error {
	doc := a.activeDoc()
	if doc == nil || doc.Err != "" || doc.Binary {
		a.showToast("info", "Go to definition", "Open a text file before jumping to a definition.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	word, ok := a.symbolAtCaret()
	if !ok || word == "" {
		a.showToast("info", "Go to definition", "Place the caret on a symbol, then press Ctrl+D.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	var lines []string
	if a.editing && a.edit != nil && a.edit.Path == doc.Path {
		lines = strings.Split(a.edit.Text(), "\n")
	} else {
		lines = doc.Lines
	}

	for _, row := range outlineFull(doc.Lang, lines) {
		if row.Name == word {
			return a.jumpToDefinition(ctx, doc.Path, row.Line)
		}
	}

	cands := a.definitionCandidates(word, doc.Path)
	if len(cands) > 0 {
		best := cands[0]
		if len(cands) > 1 {
			a.showToast("info", "Go to definition",
				fmt.Sprintf("%d definitions for %q; jumped to %s. Ctrl+T lists every match.", len(cands), word, best.Path))
		}

		return a.jumpToDefinition(ctx, best.Path, best.Line)
	}

	res := searchWorkspaceOptsFiltered(context.Background(), a.ws, word, searchOpts{}, workspaceSearchLimit, workspaceSearchFileHits, a.scanFilter)
	if res.Total == 0 {
		a.showToast("info", "Go to definition", fmt.Sprintf("Definition not found for %q.", word))
		a.refresh()

		return a.page.Redraw(ctx)
	}

	a.searchQuery = clampFieldLen(word)
	if err := a.openWorkspaceSearch(ctx); err != nil {
		return err
	}

	a.runWorkspaceSearch(ctx)
	a.showToast("info", "Go to definition",
		fmt.Sprintf("Definition not found for %q; %d textual matches \u2014 see workspace search.", word, res.Total))
	a.refresh()

	return a.page.Redraw(ctx)
}

// jumpToDefinition opens path (recording nav history) and lands the caret on
// the 0-based definition line. The outline scroll and the caret move reuse
// outlineLine and gotoLine, so preview tabs, dirty guards, and nav history
// behave like normal opens.
func (a *App) jumpToDefinition(ctx context.Context, path string, line int) error {
	if a.activeDocPath() != path {
		a.openFilePreview(path)
	}

	a.outlineLine(line)

	return a.gotoLine(ctx, line+1, 1)
}
