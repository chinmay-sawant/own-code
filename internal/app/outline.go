package app

import (
	"strings"
)

const (
	maxOutlineRows  = 60
	maxHeadingLevel = 6
	maxSymbolRunes  = 64
	pythonIndent    = 4
)

// outline returns the symbols the details panel shows. It is a lightweight
// scan, not a parser: function, type, class, heading, and selector lines are
// enough to navigate a file. The drawer keeps the first maxOutlineRows; the
// symbol palette uses outlineFull for the uncapped list.
func outline(lang string, lines []string) []symbolRow {
	rows := outlineFull(lang, lines)

	if len(rows) > maxOutlineRows {
		rows = rows[:maxOutlineRows]
	}

	return rows
}

// outlineFull returns every symbol without the drawer row cap.
func outlineFull(lang string, lines []string) []symbolRow {
	rows := make([]symbolRow, 0, maxOutlineRows)

	switch lang {
	case langGo:
		rows = outlineGo(lines, rows)
	case langPython:
		rows = outlinePython(lines, rows)
	case langJS:
		rows = outlineJS(lines, rows)
	case langMarkdown:
		rows = outlineMarkdown(lines, rows)
	case langCSS:
		rows = outlineCSS(lines, rows)
	case langJSON:
		rows = outlineJSON(lines, rows)
	case langYAML:
		rows = outlineYAML(lines, rows)
	case langHTML:
		rows = outlineHTML(lines, rows)
	case langShell:
		rows = outlineShell(lines, rows)
	}

	return rows
}

// addSymbol appends one symbol unless the row cap is reached.
func addSymbol(rows []symbolRow, name, kind string, line, depth int) []symbolRow {
	if len(rows) >= maxOutlineRows {
		return rows
	}

	return appendSymbol(rows, name, kind, line, depth)
}

// appendSymbol appends one symbol without the drawer row cap.
func appendSymbol(rows []symbolRow, name, kind string, line, depth int) []symbolRow {
	name = strings.TrimSpace(name)
	if name == "" {
		return rows
	}

	if runes := []rune(name); len(runes) > maxSymbolRunes {
		name = string(runes[:63]) + "…"
	}

	return append(rows, symbolRow{Name: name, Kind: kind, Line: line, No: line + 1, Depth: depth})
}

// isCommentLine reports whether a trimmed line is a full-line comment for the
// given prefixes, so the outline skips it instead of listing comment text.
func isCommentLine(trimmed string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}

	return false
}

// indentDepth counts the leading indent in units of width spaces. A tab counts
// as tabWidth so tab-indented files depth like their expanded form.
func indentDepth(line string, width int) int {
	if width < 1 {
		width = pythonIndent
	}

	cells := 0

	for _, r := range line {
		switch r {
		case ' ':
			cells++
		case '\t':
			cells += tabWidth
		default:
			return cells / width
		}
	}

	return cells / width
}

// outlineGo collects func, type, const, and var declarations.
func outlineGo(lines []string, rows []symbolRow) []symbolRow {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isCommentLine(trimmed, "//") {
			continue
		}

		switch {
		case strings.HasPrefix(trimmed, "func "):
			rest := strings.TrimPrefix(trimmed, "func ")
			if strings.HasPrefix(rest, "(") {
				if end := strings.Index(rest, ") "); end >= 0 {
					rest = rest[end+2:]
				}
			}

			rows = appendSymbol(rows, cutName(rest), "fn", i, 0)
		case strings.HasPrefix(trimmed, "type "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "type ")), "type", i, 0)
		case strings.HasPrefix(trimmed, "const "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "const ")), "const", i, 0)
		case strings.HasPrefix(trimmed, "var "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "var ")), "var", i, 0)
		}
	}

	return rows
}

// outlinePython collects def and class lines with their indentation.
func outlinePython(lines []string, rows []symbolRow) []symbolRow {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isCommentLine(trimmed, "#") {
			continue
		}

		depth := indentDepth(line, pythonIndent)

		switch {
		case strings.HasPrefix(trimmed, "def "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "def ")), "fn", i, depth)
		case strings.HasPrefix(trimmed, "async def "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "async def ")), "fn", i, depth)
		case strings.HasPrefix(trimmed, "class "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "class ")), "class", i, depth)
		}
	}

	return rows
}

// outlineJS collects function, class, arrow-assignment, and TypeScript
// declaration lines. .ts/.tsx map to langJS in languageOf, so interface,
// enum, and type aliases are handled here rather than in a separate branch.
func outlineJS(lines []string, rows []symbolRow) []symbolRow {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isCommentLine(trimmed, "//") {
			continue
		}

		switch {
		case strings.HasPrefix(trimmed, "export default function "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "export default function ")), "fn", i, 0)
		case strings.HasPrefix(trimmed, "export function "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "export function ")), "fn", i, 0)
		case strings.HasPrefix(trimmed, "function "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "function ")), "fn", i, 0)
		case strings.HasPrefix(trimmed, "export interface "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "export interface ")), "interface", i, 0)
		case strings.HasPrefix(trimmed, "interface "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "interface ")), "interface", i, 0)
		case strings.HasPrefix(trimmed, "export enum "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "export enum ")), "enum", i, 0)
		case strings.HasPrefix(trimmed, "enum "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "enum ")), "enum", i, 0)
		case strings.HasPrefix(trimmed, "export type "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "export type ")), "type", i, 0)
		case strings.HasPrefix(trimmed, "type "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "type ")), "type", i, 0)
		case strings.HasPrefix(trimmed, "export class "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "export class ")), "class", i, 0)
		case strings.HasPrefix(trimmed, "class "):
			rows = appendSymbol(rows, cutName(strings.TrimPrefix(trimmed, "class ")), "class", i, 0)
		case strings.HasPrefix(trimmed, "const ") || strings.HasPrefix(trimmed, "let ") || strings.HasPrefix(trimmed, "var "):
			if strings.Contains(trimmed, "= (") || strings.Contains(trimmed, "= async") ||
				strings.Contains(trimmed, "= function") || strings.Contains(trimmed, "=>") {
				rows = appendSymbol(rows, cutName(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(trimmed, "const "), "let "), "var ")), "fn", i, 0)
			}
		default:
			// Object-method shorthand: name(...) { — excluding control
			// keywords that share the shape.
			if name, ok := jsMethodName(trimmed); ok {
				rows = appendSymbol(rows, name, "fn", i, 0)
			}
		}
	}

	return rows
}

// jsMethodName matches the object-method shorthand name(args) { and reports
// the method name. Control keywords and calls with a receiver are rejected.
func jsMethodName(trimmed string) (string, bool) {
	if !strings.HasSuffix(trimmed, "{") {
		return "", false
	}

	open := strings.IndexByte(trimmed, '(')
	if open <= 0 {
		return "", false
	}

	name := strings.TrimSpace(trimmed[:open])
	if name == "" || strings.ContainsAny(name, " .[\"'`") {
		return "", false
	}

	for _, r := range name {
		if !isIdentRune(r) && r != '$' {
			return "", false
		}
	}

	switch name {
	case "if", "for", "while", "switch", "catch", "function", "return", "class":
		return "", false
	}

	return name, true
}

// outlineMarkdown collects ATX headings.
func outlineMarkdown(lines []string, rows []symbolRow) []symbolRow {
	inFence := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence

			continue
		}

		if inFence {
			continue
		}

		r := []rune(strings.TrimLeft(line, " "))
		level, marker := headingLevel(r)
		if level == 0 || marker > len(r) {
			continue
		}

		rows = appendSymbol(rows, string(r[marker:]), headingKind(level), i, level-1)
	}

	return rows
}

// headingKind maps a level to the outline label.
func headingKind(level int) string {
	kinds := [...]string{"h1", "h2", "h3", "h4", "h5", "h6"}
	if level < 1 || level > len(kinds) {
		return "h1"
	}

	return kinds[level-1]
}

// outlineCSS collects selectors and at-rules.
func outlineCSS(lines []string, rows []symbolRow) []symbolRow {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasSuffix(trimmed, "{") {
			continue
		}

		name := strings.TrimSpace(strings.TrimSuffix(trimmed, "{"))
		if name == "" || strings.HasPrefix(name, "/*") {
			continue
		}

		kind := "rule"
		if strings.HasPrefix(name, "@") {
			kind = "at"
		}

		rows = appendSymbol(rows, name, kind, i, 0)
	}

	return rows
}

// outlineJSON collects top-level and nested "key": lines with depth from the
// indent (two spaces per level).
func outlineJSON(lines []string, rows []symbolRow) []symbolRow {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, `"`) {
			continue
		}

		rest := trimmed[1:]
		end := strings.IndexByte(rest, '"')
		if end <= 0 {
			continue
		}

		after := strings.TrimSpace(rest[end+1:])
		if !strings.HasPrefix(after, ":") {
			continue
		}

		rows = appendSymbol(rows, rest[:end], "key", i, indentDepth(line, 2))
	}

	return rows
}

// outlineYAML collects mapping keys (name: ...) with depth from the indent
// (two spaces per level). Comments and list items are skipped.
func outlineYAML(lines []string, rows []symbolRow) []symbolRow {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isCommentLine(trimmed, "#") || strings.HasPrefix(trimmed, "---") {
			continue
		}

		name, ok := yamlKeyName(trimmed)
		if !ok {
			continue
		}

		rows = appendSymbol(rows, name, "key", i, indentDepth(line, 2))
	}

	return rows
}

// yamlKeyName matches a mapping key (plain, quoted, or list-item key) and
// reports the key name without quotes.
func yamlKeyName(trimmed string) (string, bool) {
	rest := trimmed
	if strings.HasPrefix(rest, "- ") {
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "- "))
	}

	colon := strings.IndexByte(rest, ':')
	if colon <= 0 {
		return "", false
	}

	after := rest[colon+1:]
	if after != "" && !strings.HasPrefix(after, " ") && !strings.HasPrefix(after, "\t") {
		return "", false
	}

	name := strings.TrimSpace(rest[:colon])
	name = strings.Trim(name, `"'`)
	if name == "" || strings.ContainsAny(name, " \t{[") {
		return "", false
	}

	return name, true
}

// outlineHTML collects opening tags, naming id attributes (div#main) so
// repeated tags stay distinguishable. Closing tags and comments are skipped.
func outlineHTML(lines []string, rows []symbolRow) []symbolRow {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, "</") ||
			strings.HasPrefix(trimmed, "<!--") || strings.HasPrefix(trimmed, "<!") {
			continue
		}

		inner := strings.TrimPrefix(trimmed, "<")
		end := strings.IndexAny(inner, " \t>/")
		if end < 0 {
			continue
		}

		tag := inner[:end]
		if tag == "" {
			continue
		}

		name := tag
		if id := htmlIDAttr(inner); id != "" {
			name = tag + "#" + id
		}

		rows = appendSymbol(rows, name, "tag", i, 0)
	}

	return rows
}

// htmlIDAttr returns the first id="..." value in a tag's attribute text.
func htmlIDAttr(inner string) string {
	for _, quote := range []string{`"`, "'"} {
		marker := "id=" + quote
		if at := strings.Index(inner, marker); at >= 0 {
			rest := inner[at+len(marker):]
			if end := strings.Index(rest, quote); end > 0 {
				return rest[:end]
			}
		}
	}

	return ""
}

// outlineShell collects function definitions (function name and name()) with
// depth from the indent. Comments are skipped.
func outlineShell(lines []string, rows []symbolRow) []symbolRow {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isCommentLine(trimmed, "#") {
			continue
		}

		if name, ok := shellFuncName(trimmed); ok {
			rows = appendSymbol(rows, name, "fn", i, indentDepth(line, 2))
		}
	}

	return rows
}

// shellFuncName matches shell function definitions: "function name" with
// optional trailing () and {, or the "name() [{" form.
func shellFuncName(trimmed string) (string, bool) {
	rest := trimmed
	keyword := false

	if strings.HasPrefix(rest, "function ") {
		keyword = true
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "function "))
	}

	rest = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "{"))
	if rest == "" {
		return "", false
	}

	if i := strings.IndexByte(rest, '('); i >= 0 {
		// Parens must wrap to the end: name(...).
		if !strings.HasSuffix(rest, ")") {
			return "", false
		}

		name := strings.TrimSpace(rest[:i])
		if name == "" || strings.ContainsAny(name, " \t=|;&\"'`$") {
			return "", false
		}

		return name, true
	}

	if !keyword {
		return "", false
	}

	name := cutName(rest)
	if name == "" || (rest != name && rest != name+"()") || strings.ContainsAny(name, "=|;&\"'`$") {
		return "", false
	}

	return name, true
}

// cutName keeps the leading identifier of a declaration.
func cutName(rest string) string {
	rest = strings.TrimSpace(rest)

	for i, r := range rest {
		switch r {
		case '(', ' ', '[', '{', '=', ':', '<':
			return strings.TrimSpace(rest[:i])
		}
	}

	return rest
}
