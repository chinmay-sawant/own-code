// Document outline model for the CSS bookmark properties.
//
// Spec: CSS Generated Content for Paged Media (GCPM) bookmark model
// (https://www.w3.org/TR/css-gcpm-3/#bookmarks); the three longhands are
// defined normatively in CSS Content 3:
// https://drafts.csswg.org/css-content-3/#propdef-bookmark-label,
// https://drafts.csswg.org/css-content-3/#propdef-bookmark-level and
// https://drafts.csswg.org/css-content-3/#propdef-bookmark-state.
//
// Browser status: Chrome has no support for bookmark-label, bookmark-level or
// bookmark-state (no outline entries are produced from them). The catalog rows
// stay unsupported at testdata/css/catalog/properties.json, the compatibility
// matrix records "No handler" for all three at
// documentation/compatibility-matrix.md:532-534, and the fixture-57 probes
// carry data-status="unsupported". There is no Chrome reference to compare
// against, so the drafts above are the only oracle.
//
// Display-list gap: Op kinds run OpUnknown through OpBullet plus OpGridRun
// (internal/layout/layout.go:357-373) and none can express an outline, so
// this model exposes the built tree through BuildBookmarkOutline and
// ResolveBookmarkProps instead of ops. Rendering the tree into PDF bookmarks
// is out of scope: this tree has no PDF writer.
package layout

import (
	"strconv"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

// Cascade property names for the CSS bookmark longhands.
const (
	bookmarkLabelProp = "bookmark-label"
	bookmarkLevelProp = "bookmark-level"
	bookmarkStateProp = "bookmark-state"
)

// bookmarkContentKeyword is the content keyword accepted inside a
// bookmark-label content list: it resolves to the element text.
const bookmarkContentKeyword = "content"

// bookmarkPropCount is the capacity of the per-element bookmark raw map.
const bookmarkPropCount = 3

// BookmarkNode is one entry in the document outline tree built from the CSS
// bookmark properties. Level is the 1-based nesting depth from the used
// bookmark-level, Label is the used title text from the used bookmark-label
// and Open reports the used bookmark-state (true for open, false for closed).
// Children holds deeper levels in document order.
type BookmarkNode struct {
	Label    string
	Level    int
	Open     bool
	Children []*BookmarkNode
}

// BookmarkProps holds the used bookmark values of one element. HasBookmark is
// true only when bookmark-level resolves to an integer >= 1 and
// bookmark-label resolves to non-blank text; only such elements become outline
// entries. Level is the used nesting depth, Label the used title text and Open
// the used bookmark-state.
type BookmarkProps struct {
	HasBookmark bool
	Level       int
	Label       string
	Open        bool
}

// ResolveBookmarkProps returns the used bookmark values of every element in
// the tree, keyed by element node. Text, comment and doctype nodes have no
// entry. Values come from the cascade (author sheets in opts plus inline
// style attributes, with var() substitution), never from stored strings.
func ResolveBookmarkProps(root *html.Node, opts Options) map[*html.Node]*BookmarkProps {
	props, _ := collectBookmarks(root, opts)

	return props
}

// BuildBookmarkOutline builds the document outline tree in document order:
// bookmark-level sets the nesting depth, the used bookmark-label sets the
// title text and the used bookmark-state sets the open/closed flag. Elements
// without a usable level and label are skipped. Rendering the tree into PDF
// bookmarks is out of scope: the returned tree is the observable surface.
func BuildBookmarkOutline(root *html.Node, opts Options) []*BookmarkNode {
	_, flat := collectBookmarks(root, opts)

	return nestBookmarkNodes(flat)
}

// collectBookmarks walks the tree in document order, resolves the used
// bookmark values of every element and records the flat outline entries.
// Raw declarations are consumed before descending because cascadeRaw reuses
// the context buffers across elements.
func collectBookmarks(
	root *html.Node, opts Options,
) (map[*html.Node]*BookmarkProps, []*BookmarkNode) {
	props := make(map[*html.Node]*BookmarkProps)

	var flat []*BookmarkNode

	if root == nil {
		return props, flat
	}

	bookmarks := &styleContext{ //nolint:exhaustruct // bookmark pass needs only cascade inputs
		sheets:     opts.Sheets,
		properties: registeredProperties(opts.Sheets),
		media:      opts.Media,
		viewportW:  opts.Width,
		viewportH:  opts.Height,
		state:      opts.State,
	}

	var walk func(node *html.Node, parentCustom map[string]string, parent *BookmarkProps)

	walk = func(node *html.Node, parentCustom map[string]string, parent *BookmarkProps) {
		if node == nil {
			return
		}

		custom := parentCustom
		used := parent

		if node.Type == html.ElementNode {
			stored, elementCustom := resolveBookmarkElement(bookmarks, node, parentCustom, parent)
			props[node] = stored
			custom = elementCustom
			used = stored

			if stored.HasBookmark {
				flat = append(flat, &BookmarkNode{
					Label:    stored.Label,
					Level:    stored.Level,
					Open:     stored.Open,
					Children: nil,
				})
			}
		}

		for _, child := range node.Children {
			walk(child, custom, used)
		}
	}

	walk(root, nil, nil)

	return props, flat
}

// resolveBookmarkElement resolves one element's used bookmark values from its
// winning cascaded declarations plus the element text for the content keyword.
func resolveBookmarkElement(
	bookmarks *styleContext,
	node *html.Node,
	parentCustom map[string]string,
	parent *BookmarkProps,
) (*BookmarkProps, map[string]string) {
	raw := cascadeRaw(bookmarks, node, bookmarks.matchedRules(node, ""))

	levelRaw := raw[bookmarkLevelProp]
	labelRaw := raw[bookmarkLabelProp]
	stateRaw := raw[bookmarkStateProp]

	custom := mergeCustomProps(parentCustom, raw, bookmarks.properties)

	small := make(map[string]string, bookmarkPropCount)

	if levelRaw != "" {
		small[bookmarkLevelProp] = levelRaw
	}

	if labelRaw != "" {
		small[bookmarkLabelProp] = labelRaw
	}

	if stateRaw != "" {
		small[bookmarkStateProp] = stateRaw
	}

	resolved := resolveRawVars(small, custom)
	text := bookmarkElementText(node)
	level, hasLevel := resolveBookmarkLevel(resolved[bookmarkLevelProp], parent)
	open := resolveBookmarkOpen(resolved[bookmarkStateProp], parent)
	label, hasLabel := resolveBookmarkLabel(resolved[bookmarkLabelProp], text, parent)

	stored := &BookmarkProps{
		HasBookmark: hasLevel && hasLabel,
		Level:       level,
		Label:       label,
		Open:        open,
	}

	return stored, custom
}

// resolveBookmarkLevel applies CSS-wide keywords to a cascaded bookmark-level
// value. The property is not inherited, so every keyword except inherit
// resolves to none (no bookmark).
func resolveBookmarkLevel(raw string, parent *BookmarkProps) (int, bool) {
	trimmed := strings.TrimSpace(raw)

	if trimmed == "" {
		return 0, false
	}

	if cssWideKeyword(strings.ToLower(trimmed)) {
		if strings.EqualFold(trimmed, inheritKeyword) && parent != nil && parent.HasBookmark {
			return parent.Level, true
		}

		return 0, false
	}

	return parseBookmarkLevelValue(trimmed)
}

// resolveBookmarkOpen applies CSS-wide keywords to a cascaded bookmark-state
// value. The property is not inherited and its initial value is open.
func resolveBookmarkOpen(raw string, parent *BookmarkProps) bool {
	trimmed := strings.TrimSpace(raw)

	if trimmed == "" {
		return true
	}

	if cssWideKeyword(strings.ToLower(trimmed)) {
		if strings.EqualFold(trimmed, inheritKeyword) && parent != nil {
			return parent.Open
		}

		return true
	}

	return parseBookmarkOpenValue(trimmed)
}

// resolveBookmarkLabel applies CSS-wide keywords to a cascaded bookmark-label
// value. The property is not inherited and its initial value is the element
// content, so an absent declaration resolves to the element text.
func resolveBookmarkLabel(raw, text string, parent *BookmarkProps) (string, bool) {
	trimmed := strings.TrimSpace(raw)

	if trimmed == "" {
		return text, text != ""
	}

	if cssWideKeyword(strings.ToLower(trimmed)) {
		if strings.EqualFold(trimmed, inheritKeyword) && parent != nil {
			return parent.Label, parent.Label != ""
		}

		return text, text != ""
	}

	return parseBookmarkLabelValue(trimmed, text)
}

// parseBookmarkLevelValue parses a cascaded bookmark-level value into the used
// integer depth. Only none and integers >= 1 are valid
// (testdata/css/catalog/properties.json records "none | <integer [1,∞]>").
func parseBookmarkLevelValue(value string) (int, bool) {
	trimmed := strings.TrimSpace(value)

	if trimmed == "" {
		return 0, false
	}

	if strings.EqualFold(trimmed, "none") {
		return 0, false
	}

	level, err := strconv.Atoi(trimmed)
	if err != nil || level < 1 {
		return 0, false
	}

	return level, true
}

// parseBookmarkOpenValue resolves the used bookmark-state. The initial value
// is open, so empty, open and any invalid value resolve to true; only closed
// resolves to false.
func parseBookmarkOpenValue(value string) bool {
	return !strings.EqualFold(strings.TrimSpace(value), "closed")
}

// parseBookmarkLabelValue resolves the used bookmark label from a cascaded
// bookmark-label value and the element text used for the content keyword. The
// grammar is a content list
// (testdata/css/catalog/properties.json records "<content-list>"): quoted
// strings contribute literal text, the content keyword (with or without
// arguments, for example content() or content(text)) contributes the element
// text, and unsupported tokens such as attr() or counter() are dropped. A
// blank result means no usable title.
func parseBookmarkLabelValue(value, text string) (string, bool) {
	trimmed := strings.TrimSpace(value)

	if trimmed == "" {
		return text, text != ""
	}

	if strings.EqualFold(trimmed, "none") {
		return "", false
	}

	var builder strings.Builder

	wrote := false
	rest := skipBookmarkSpaces(trimmed)

	for len(rest) > 0 {
		next, wroteToken, ok := stepBookmarkLabel(&builder, rest, text)
		if !ok {
			return text, text != ""
		}

		wrote = wrote || wroteToken
		rest = skipBookmarkSpaces(next)
	}

	if !wrote {
		return "", false
	}

	label := builder.String()

	if strings.TrimSpace(label) == "" {
		return "", false
	}

	return label, true
}

// stepBookmarkLabel consumes one content-list token at the start of rest,
// appending its used text to builder. It returns the remainder, whether the
// token contributed text, and whether scanning stays valid (an unbalanced
// quote is invalid and falls back to the element content).
func stepBookmarkLabel(builder *strings.Builder, rest, text string) (string, bool, bool) {
	switch {
	case rest[0] == '"' || rest[0] == '\'':
		literal, next, ok := scanBookmarkQuoted(rest)
		if !ok {
			return "", false, false
		}

		builder.WriteString(literal)

		return next, true, true
	case isBookmarkContentHead(rest):
		builder.WriteString(text)

		return skipBookmarkContentHead(rest), true, true
	default:
		return skipBookmarkToken(rest), false, true
	}
}

// scanBookmarkQuoted reads one quoted string at the start of rest and returns
// its unescaped content plus the remainder after the closing quote.
func scanBookmarkQuoted(rest string) (string, string, bool) {
	quote := rest[0]

	var builder strings.Builder

	for pos := 1; pos < len(rest); pos++ {
		switch rest[pos] {
		case '\\':
			if pos+1 >= len(rest) {
				return "", "", false
			}

			pos++

			builder.WriteByte(rest[pos])
		case '"', '\'':
			if rest[pos] == quote {
				return builder.String(), rest[pos+1:], true
			}

			builder.WriteByte(rest[pos])
		default:
			builder.WriteByte(rest[pos])
		}
	}

	return "", "", false
}

// isBookmarkContentHead reports whether rest starts with the content keyword
// (case-insensitive) bounded from a longer identifier.
func isBookmarkContentHead(rest string) bool {
	if len(rest) < len(bookmarkContentKeyword) {
		return false
	}

	if !strings.EqualFold(rest[:len(bookmarkContentKeyword)], bookmarkContentKeyword) {
		return false
	}

	if len(rest) == len(bookmarkContentKeyword) {
		return true
	}

	return !isBookmarkNameChar(rest[len(bookmarkContentKeyword)])
}

// isBookmarkNameChar reports identifier bytes that extend a keyword match.
func isBookmarkNameChar(char byte) bool {
	switch {
	case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		return true
	default:
		return char == '-' || char == '_'
	}
}

// skipBookmarkContentHead drops the content keyword plus one optional
// parenthesized argument group such as () or (text).
func skipBookmarkContentHead(rest string) string {
	rest = rest[len(bookmarkContentKeyword):]
	rest = skipBookmarkSpaces(rest)

	if len(rest) == 0 || rest[0] != '(' {
		return rest
	}

	return skipBalancedParens(rest, 0)
}

// skipBookmarkToken drops one unsupported content-list token: an identifier or
// a function call with balanced arguments.
func skipBookmarkToken(rest string) string {
	pos := 0

	for pos < len(rest) && !isBookmarkTokenEnd(rest[pos]) {
		pos++
	}

	if pos < len(rest) && rest[pos] == '(' {
		return skipBalancedParens(rest, pos)
	}

	return rest[pos:]
}

// isBookmarkTokenEnd reports delimiter bytes that end a content-list token.
func isBookmarkTokenEnd(char byte) bool {
	switch char {
	case ' ', '\t', '\n', '\r', '\f', '"', '\'':
		return true
	default:
		return false
	}
}

// skipBalancedParens drops one parenthesized group starting at open, which
// must hold '('. Quoted strings inside the group are skipped intact; an
// unbalanced group drops the rest of the value.
func skipBalancedParens(value string, open int) string {
	depth := 0

	for pos := open; pos < len(value); pos++ {
		switch value[pos] {
		case '(':
			depth++
		case ')':
			depth--

			if depth == 0 {
				return value[pos+1:]
			}
		case '"', '\'':
			_, next, ok := scanBookmarkQuoted(value[pos:])
			if !ok {
				return ""
			}

			pos += len(value[pos:]) - len(next) - 1
		default:
		}
	}

	return ""
}

// skipBookmarkSpaces drops leading CSS whitespace.
func skipBookmarkSpaces(value string) string {
	for len(value) > 0 {
		switch value[0] {
		case ' ', '\t', '\n', '\r', '\f':
			value = value[1:]
		default:
			return value
		}
	}

	return value
}

// bookmarkElementText returns the element text used when bookmark-label
// resolves to the content keyword: descendant text with script and style
// subtrees skipped, whitespace collapsed to single spaces.
func bookmarkElementText(node *html.Node) string {
	var builder strings.Builder

	var collect func(current *html.Node)

	collect = func(current *html.Node) {
		if current == nil {
			return
		}

		switch current.Type {
		case html.TextNode:
			builder.WriteString(current.Text)
			builder.WriteByte(' ')
		case html.ElementNode:
			if strings.EqualFold(current.Name, "script") || strings.EqualFold(current.Name, "style") {
				return
			}

			for _, child := range current.Children {
				collect(child)
			}
		case html.NodeUnknown, html.CommentNode, html.DoctypeNode:
			return
		}
	}

	for _, child := range node.Children {
		collect(child)
	}

	return strings.Join(strings.Fields(builder.String()), " ")
}

// nestBookmarkNodes folds flat document-order entries into the outline tree:
// each entry becomes a child of the nearest preceding entry with a smaller
// level, or a root when there is none. Skipped levels nest under the nearest
// ancestor instead of inventing placeholder nodes.
func nestBookmarkNodes(flat []*BookmarkNode) []*BookmarkNode {
	var roots []*BookmarkNode

	stack := make([]*BookmarkNode, 0, len(flat))

	for _, entry := range flat {
		for len(stack) > 0 && stack[len(stack)-1].Level >= entry.Level {
			stack = stack[:len(stack)-1]
		}

		if len(stack) == 0 {
			roots = append(roots, entry)
		} else {
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, entry)
		}

		stack = append(stack, entry)
	}

	return roots
}
