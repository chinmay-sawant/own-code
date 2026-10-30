// Named-string table for CSS string-set (css-content-3, GCPM running headers).
//
// Scope: parse the upstream declaration syntax, keep a per-document table
// where the first assignment wins, and expose the accessor the future page
// margin pipeline will call. That is all this file does.
//
// Boundary (honest): there is no page margin pipeline in this tree, so
// nothing renders running headers or footers; content: string() is not
// resolved anywhere; stylesheet rules are not consulted (the collector reads
// inline style attributes only, which needs no cascade change); and the table
// is per document, not per page (no first/start/last/first-except page
// selectors). Chrome 143.0.7499.40 has no string-set support, stated
// explicitly, so parsing follows the css-content-3 draft and the catalog
// upstream syntax: none | [ <custom-ident> <string>+ ]#
// (testdata/css/catalog/properties.json, testdata/css/catalog/upstream
// webref-ed-css-1f2ec8f.json). Functions such as counter(), content(), and
// running() are rejected: only quoted strings have used values here, matching
// how internal/css/page_margin.go:6 drops non-quoted margin content.
//
// Follow-up seam: when the margin pipeline lands, it should call
// CollectNamedStrings once per layout (or own one table on the engine) and
// resolve string() lookups against it. Wiring the cascade (a ResolvedStyle
// field plus a parse arm in the property dispatch) is the other half of that
// work; grep confirms string-set has zero Go references today, so no existing
// file is touched by this change.
package layout

import (
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
	"github.com/chinmay-sawant/blinkless/internal/html"
)

// StringSetAssignment is one parsed named-string assignment: the lower-cased
// identifier plus its used value (the concatenated decoded strings).
type StringSetAssignment struct {
	Name  string
	Value string
}

// stringSetRevertLayer is the CSS-wide revert-layer keyword, kept as a named
// constant so the two keyword switches share one spelling.
const stringSetRevertLayer = "revert-layer"

// NamedStringTable is a per-document named-string store. The first assignment
// for a name wins; later assignments for the same name are ignored. This is
// the lite single-document form of the GCPM rule that the first assignment on
// a page supplies string(name, first).
type NamedStringTable struct {
	values map[string]string
	order  []string
}

// NewNamedStringTable returns an empty table.
func NewNamedStringTable() *NamedStringTable {
	return &NamedStringTable{
		values: make(map[string]string),
		order:  nil,
	}
}

// Set records name=value unless name is already present. Names match
// ASCII case-insensitively, consistent with CSS ident handling elsewhere
// (internal/layout/style_properties.go lower-cases the page name).
func (table *NamedStringTable) Set(name, value string) {
	if table == nil {
		return
	}

	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return
	}

	if table.values == nil {
		table.values = make(map[string]string)
	}

	if _, present := table.values[key]; present {
		return
	}

	table.values[key] = value
	table.order = append(table.order, key)
}

// Lookup reports the stored used value for name.
func (table *NamedStringTable) Lookup(name string) (string, bool) {
	if table == nil {
		return "", false
	}

	value, ok := table.values[strings.ToLower(strings.TrimSpace(name))]

	return value, ok
}

// Names lists stored names in first-assignment order.
func (table *NamedStringTable) Names() []string {
	if table == nil {
		return nil
	}

	return append([]string(nil), table.order...)
}

// Len reports how many names are stored.
func (table *NamedStringTable) Len() int {
	if table == nil {
		return 0
	}

	return len(table.values)
}

// ParseStringSet parses a string-set declaration value into used-value
// assignments in source order. It reports false for anything outside the
// supported syntax, in which case the caller keeps the prior state and the
// table is untouched. none and initial are valid and yield zero assignments
// (initial is none). Functions, unquoted tokens, a bare identifier with no
// string, and CSS-wide keywords as names are invalid.
func ParseStringSet(raw string) ([]StringSetAssignment, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false
	}

	switch strings.ToLower(trimmed) {
	case "none", cssKeywordInitial:
		return nil, true
	case inheritKeyword, cssKeywordUnset, flowKeywordDefault, cssKeywordRevert, stringSetRevertLayer:
		return nil, false
	}

	items := splitStringSetList(trimmed)

	assignments := make([]StringSetAssignment, 0, len(items))

	for _, item := range items {
		assignment, ok := parseStringSetItem(item)
		if !ok {
			return nil, false
		}

		assignments = append(assignments, assignment)
	}

	return assignments, true
}

// CollectNamedStrings builds the per-document table from the inline style
// attributes in document order (html.Node.Walk is pre-order). When an element
// declares string-set twice in one attribute the last declaration wins, as in
// the cascade. Stylesheet rules are out of scope (see the file header); the
// future margin pipeline calls this once per layout and reads via Lookup.
func CollectNamedStrings(root *html.Node) *NamedStringTable {
	table := NewNamedStringTable()

	if root == nil {
		return table
	}

	root.Walk(func(node *html.Node) {
		if node.Type != html.ElementNode {
			return
		}

		styleAttr := node.Attribute("style")
		if styleAttr == "" {
			return
		}

		var latest []StringSetAssignment

		claimed := false

		for _, decl := range css.ParseInline(styleAttr) {
			if decl.Prop != "string-set" {
				continue
			}

			assignments, ok := ParseStringSet(decl.Value)
			if !ok {
				continue
			}

			latest = assignments
			claimed = true
		}

		if !claimed {
			return
		}

		for _, assignment := range latest {
			table.Set(assignment.Name, assignment.Value)
		}
	})

	return table
}

// LookupNamedString is the single-query accessor: collect the document table
// and return the used value stored for name.
func LookupNamedString(root *html.Node, name string) (string, bool) {
	return CollectNamedStrings(root).Lookup(name)
}

// splitStringSetList splits a declaration value on top-level commas.
// Commas inside quoted strings do not split. ok is false only when called
// with an empty value, which the caller already rejects.
func splitStringSetList(value string) []string {
	var items []string

	start := 0

	for idx := 0; idx < len(value); idx++ {
		switch value[idx] {
		case '"', '\'':
			end, ok := scanStringSetQuote(value, idx)
			if !ok {
				items = append(items, value[start:])

				return items
			}

			idx = end
		case '\\':
			idx++
		case ',':
			items = append(items, value[start:idx])
			start = idx + 1
		}
	}

	items = append(items, value[start:])

	return items
}

// parseStringSetItem parses one comma item: an identifier followed by one or
// more quoted strings whose decoded contents concatenate to the used value.
func parseStringSetItem(item string) (StringSetAssignment, bool) {
	empty := StringSetAssignment{Name: "", Value: ""}

	name, rest, ok := splitStringSetName(item)
	if !ok {
		return empty, false
	}

	var built strings.Builder

	count := 0

	for rest != "" {
		decoded, next, ok := scanStringSetString(rest, 0)
		if !ok {
			return empty, false
		}

		built.WriteString(decoded)

		count++

		rest = strings.TrimLeft(rest[next:], " \t\n\r\f")
	}

	if count == 0 {
		return empty, false
	}

	return StringSetAssignment{Name: strings.ToLower(name), Value: built.String()}, true
}

// splitStringSetName cuts one comma item into its identifier and the
// remaining string list. ok is false for an empty item, a non-identifier, or
// a reserved keyword.
func splitStringSetName(item string) (string, string, bool) {
	rest := strings.TrimLeft(item, " \t\n\r\f")
	if rest == "" {
		return "", "", false
	}

	identEnd := 0

	for identEnd < len(rest) && !isStringSetSpace(rest[identEnd]) {
		identEnd++
	}

	name := rest[:identEnd]

	if !css.IsIdentToken(name) || isStringSetReserved(name) {
		return "", "", false
	}

	return name, rest[identEnd:], true
}

// isStringSetReserved reports CSS-wide keywords and none, which cannot be
// custom identifiers.
func isStringSetReserved(name string) bool {
	switch strings.ToLower(name) {
	case "none", cssKeywordInitial, inheritKeyword, cssKeywordUnset, "default", cssKeywordRevert, stringSetRevertLayer:
		return true
	default:
		return false
	}
}

// scanStringSetString scans one quoted string at the start of rest (after
// leading whitespace) and returns its decoded used value plus the offset just
// past the closing quote.
func scanStringSetString(rest string, start int) (string, int, bool) {
	idx := start

	for idx < len(rest) && isStringSetSpace(rest[idx]) {
		idx++
	}

	if idx >= len(rest) || (rest[idx] != '"' && rest[idx] != '\'') {
		return "", start, false
	}

	end, ok := scanStringSetQuote(rest, idx)
	if !ok {
		return "", start, false
	}

	return decodeStringSetString(rest[idx+1 : end]), end + 1, true
}

// scanStringSetQuote returns the index of the closing quote matching the
// quote at open. Backslash escapes skip the next byte.
func scanStringSetQuote(value string, open int) (int, bool) {
	quote := value[open]

	for idx := open + 1; idx < len(value); idx++ {
		if value[idx] == '\\' {
			idx++

			if idx >= len(value) {
				return 0, false
			}

			continue
		}

		if value[idx] == quote {
			return idx, true
		}
	}

	return 0, false
}

// decodeStringSetString resolves backslash escapes the way quoted page-box
// content does (internal/css/page_margin.go): a backslash quotes the next
// byte, so \' inside a single-quoted string is a literal quote.
func decodeStringSetString(src string) string {
	if !strings.ContainsRune(src, '\\') {
		return src
	}

	var built strings.Builder

	built.Grow(len(src))

	for idx := 0; idx < len(src); idx++ {
		if src[idx] != '\\' || idx+1 >= len(src) {
			built.WriteByte(src[idx])

			continue
		}

		idx++

		built.WriteByte(src[idx])
	}

	return built.String()
}

// isStringSetSpace reports CSS whitespace between string-set tokens.
func isStringSetSpace(char byte) bool {
	switch char {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	default:
		return false
	}
}
