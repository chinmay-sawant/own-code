package layout

import (
	"fmt"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

const (
	contentNormal             = "normal"
	singleQuotedContentMinLen = 2
)

// pseudoContent cascades the CSS content property for ::before/::after on n.
// Supports string literals, attr(name), counter()/counters(), open-quote /
// close-quote, and none/normal (empty). Wiki hlist separators use
// li::after{content:"\a0 · "}; print external links use
// a.external::after{content:' (' attr(href) ')'}.
func contentNeedsEnv(value string) bool {
	if value == "" {
		return false
	}

	low := strings.ToLower(value)

	return strings.Contains(low, "counter(") ||
		strings.Contains(low, "counters(") ||
		strings.Contains(low, "quote")
}

func (e *engine) pseudoContent(node *html.Node, pseudoEl string) string {
	// content on regular elements (e.g. div with content:none or counter-increment)
	// intentionally has no effect – only ::before/::after generate content.
	// List markers for display:list-item are handled via OpBullet (see inline_paint.go).
	if e == nil || node == nil || (pseudoEl != pseudoBefore && pseudoEl != pseudoAfter) {
		return ""
	}

	if !e.mayHavePseudoContent(node, pseudoEl) {
		return ""
	}

	ctx := e.pseudoStyleContext()
	best := e.cachedContentDecl(ctx, node, pseudoEl)

	if best == nil {
		return ""
	}

	if !contentNeedsEnv(best.value) {
		return evalContentValue(best.value, node, nil)
	}

	return evalContentValue(best.value, node, e.contentEnvAt(node, pseudoEl))
}

func (e *engine) pseudoContentURL(node *html.Node, pseudoEl string) string {
	if e == nil || node == nil || (pseudoEl != pseudoBefore && pseudoEl != pseudoAfter) {
		return ""
	}

	if !e.mayHavePseudoContent(node, pseudoEl) {
		return ""
	}

	ctx := e.pseudoStyleContext()

	best := e.cachedContentDecl(ctx, node, pseudoEl)
	if best == nil {
		return ""
	}

	if url, ok := firstCSSUrl(best.value); ok {
		return url
	}

	return ""
}

// pseudoStyleContext is the cascade context used to re-walk sheets for
// generated content (media + @container gates). When e.styles came from a
// container pass, non-matching container content is suppressed here the same
// way colors are. The context is cached on the engine and refreshed per use:
// pseudo lookups run sequentially and consume each matchedRules result before
// the next lookup, so sharing the scratch buffers is exact and avoids one
// allocation plus fresh match buffers per ::before/::after probe.
func (e *engine) pseudoStyleContext() *styleContext {
	media := e.opts.Media
	if media == "" {
		media = "print"
	}

	ctx := e.pseudoCtx
	if ctx == nil {
		ctx = &styleContext{} //nolint:exhaustruct // refreshed below on every use
		e.pseudoCtx = ctx
	}

	ctx.sheets = e.opts.Sheets
	ctx.media = media
	ctx.viewportW = e.opts.Width
	ctx.viewportH = e.opts.Height
	ctx.containers = e.containers
	ctx.index = e.styleRuleIndex()

	return ctx
}

// styleRuleIndex returns the selector index for this layout's sheets, built
// once and shared by the cascade and every pseudo-content context. It is nil
// when the layout carries no sheets.
func (e *engine) styleRuleIndex() *ruleIndex {
	if e == nil || len(e.opts.Sheets) == 0 {
		return nil
	}

	if e.styleIndex == nil {
		e.styleIndex = newRuleIndex(e.opts.Sheets)
	}

	return e.styleIndex
}

// hasPseudoContent reports whether any rule can generate ::before/::after
// content. No such rule means every generated-content lookup can answer empty
// without walking selectors.
func (e *engine) hasPseudoContent() bool {
	index := e.styleRuleIndex()

	return index != nil && index.content
}

// mayHavePseudoContent reports whether node could generate content for one
// pseudo-element: some rule for that name declares content and some selector
// for that name could match node. False means the pseudo walk would find no
// rule, so callers skip the cascade without changing the result.
func (e *engine) mayHavePseudoContent(node *html.Node, pseudoEl string) bool {
	if e == nil || node == nil || (pseudoEl != pseudoBefore && pseudoEl != pseudoAfter) {
		return false
	}

	index := e.styleRuleIndex()
	if index == nil || !index.contentPseudo[pseudoEl] {
		return false
	}

	return index.mayMatchPseudo(node, pseudoEl)
}

// pseudoStyle resolves the used style of generated content against the host
// style. Generated content inherits from its host, then applies declarations
// from the matching pseudo-element rules through the same raw-to-used
// sequence as elements, so custom properties and var() references resolve
// here too.
func (e *engine) pseudoStyle(node *html.Node, pseudoEl string, host ResolvedStyle) *ResolvedStyle {
	if e == nil || node == nil {
		return &host
	}

	if !e.mayHavePseudoContent(node, pseudoEl) {
		return e.stylePtr(node)
	}

	ctx := e.pseudoStyleContext()
	raw := cascadePseudoRaw(ctx, node, pseudoEl)

	if len(raw) == 0 {
		return e.stylePtr(node)
	}

	var sty ResolvedStyle

	applyRawToUsed(nil, ctx, &host, &sty, raw)

	return &sty
}

// contentHit is one content: declaration with its cascade priority.
type contentHit struct {
	value          string
	a, b, c, order int
	important      bool
}

// pseudoContentKey identifies one cached content-declaration lookup: the
// element plus the before/after pass. The cascade is fixed for a Layout run,
// so the winning declaration cannot change between probes in the run.
type pseudoContentKey struct {
	node   *html.Node
	pseudo string
}

// pseudoContentEntry is one cached lookup: the winning declaration, or
// found false when no content rule wins for the element.
type pseudoContentEntry struct {
	hit   contentHit
	found bool
}

// cachedContentDecl returns the winning content declaration for the element's
// pseudo-element, running the selector match once per element per run and
// serving every later probe from the memo.
func (e *engine) cachedContentDecl(ctx *styleContext, node *html.Node, pseudoEl string) *contentHit {
	if e == nil || node == nil {
		return selectContentDecl(ctx, node, pseudoEl)
	}

	if e.pseudoContentCache == nil {
		e.pseudoContentCache = make(map[pseudoContentKey]pseudoContentEntry)
	}

	key := pseudoContentKey{node: node, pseudo: pseudoEl}

	if cached, ok := e.pseudoContentCache[key]; ok {
		if !cached.found {
			return nil
		}

		hit := cached.hit

		return &hit
	}

	best := selectContentDecl(ctx, node, pseudoEl)

	if best == nil {
		e.pseudoContentCache[key] = pseudoContentEntry{
			hit:   contentHit{value: "", a: 0, b: 0, c: 0, order: 0, important: false},
			found: false,
		}

		return nil
	}

	e.pseudoContentCache[key] = pseudoContentEntry{hit: *best, found: true}

	hit := *best

	return &hit
}

// selectContentDecl picks the winning content declaration for the pseudo
// element pe on node.
func selectContentDecl(ctx *styleContext, node *html.Node, pseudoEl string) *contentHit {
	var best *contentHit

	for _, rowH := range ctx.matchedRules(node, pseudoEl) {
		for _, d := range rowH.rule.Decls {
			if !strings.EqualFold(d.Prop, "content") {
				continue
			}

			h := contentHit{value: d.Value, a: rowH.a, b: rowH.b, c: rowH.c, order: rowH.rule.Order, important: d.Important}
			if betterContentHit(h, best) {
				hh := h
				best = &hh
			}
		}
	}

	return best
}

// betterContentHit reports whether candidate outranks best by importance,
// then specificity, then source order.
func betterContentHit(candidate contentHit, best *contentHit) bool {
	if best == nil {
		return true
	}

	if candidate.important != best.important {
		return candidate.important
	}

	if candidate.a != best.a {
		return candidate.a > best.a
	}

	if candidate.b != best.b {
		return candidate.b > best.b
	}

	if candidate.c != best.c {
		return candidate.c > best.c
	}

	return candidate.order >= best.order
}

// parseContentValue evaluates a CSS content list: quoted strings, attr(name),
// counter()/counters(), open-quote/close-quote, and none/normal. Unsupported
// tokens (url(), …) are skipped so we never paint the literal source text
// "attr(href)".
func parseContentValue(value string, node *html.Node) string {
	return evalContentValue(value, node, nil)
}

func evalContentValue(value string, node *html.Node, env *contentEnv) string {
	value = strings.TrimSpace(value)
	low := strings.ToLower(value)

	if low == displayNone || low == contentNormal || value == "" {
		return ""
	}
	// Fast path: single quoted string.
	if content, ok := singleQuotedContent(value); ok {
		return decodeCSSString(content)
	}

	if env == nil {
		env = defaultContentEnv()
	}

	var boxNode strings.Builder

	idx := 0
	for idx < len(value) {
		next, ok := scanContentToken(value, idx, node, env, &boxNode)
		if !ok {
			break
		}

		idx = next
	}

	return boxNode.String()
}

// scanContentToken consumes one token of a content list at idx, appending any
// text to boxNode, and returns the index just past it. ok is false when
// scanning must stop (unbalanced quote).
func scanContentToken(
	value string, idx int, node *html.Node, env *contentEnv, boxNode *strings.Builder,
) (int, bool) {
	idx = skipCSSWhitespace(value, idx)
	if idx >= len(value) {
		return idx, false
	}

	child := value[idx]
	if child == '"' || child == '\'' {
		if end, ok := scanQuotedContent(value, idx+1, child); ok {
			boxNode.WriteString(decodeCSSString(value[idx+1 : end]))

			return end + 1, true
		}

		return idx, false
	}

	if next, ok := scanContentFunction(value, idx, node, env, boxNode); ok {
		return next, true
	}

	if isIdentStart(value[idx]) {
		return scanContentIdent(value, idx, env, boxNode), true
	}

	return idx + 1, true
}

func scanContentFunction(
	value string, idx int, node *html.Node, env *contentEnv, boxNode *strings.Builder,
) (int, bool) {
	if !isIdentStart(value[idx]) {
		return 0, false
	}

	nameEnd := skipCSSIdent(value, idx)
	paren := skipCSSWhitespace(value, nameEnd)

	if paren >= len(value) || value[paren] != '(' {
		return 0, false
	}

	name := strings.ToLower(value[idx:nameEnd])

	switch name {
	case "attr":
		val, next := parseAttrToken(value, idx, node)
		boxNode.WriteString(val)

		return next, true
	case "counters":
		text, next := evalCountersFn(value, idx, env)
		boxNode.WriteString(text)

		return next, true
	case "counter":
		text, next := evalCounterFn(value, idx, env)
		boxNode.WriteString(text)

		return next, true
	case "url":
		// content:url() is an image replaced element (minimal: skip text, caller creates OpImage)
		return skipCSSFunction(value, paren+1), true
	default:
		return skipCSSFunction(value, paren+1), true
	}
}

func scanContentIdent(value string, idx int, env *contentEnv, boxNode *strings.Builder) int {
	end := skipCSSIdent(value, idx)
	ident := strings.ToLower(value[idx:end])

	switch ident {
	case cssContentOpenQuote:
		boxNode.WriteString(env.takeOpenQuote())
	case cssContentCloseQuote:
		boxNode.WriteString(env.takeCloseQuote())
	case cssContentNoOpenQuote:
		env.skipOpenQuote()
	case cssContentNoCloseQuote:
		env.skipCloseQuote()
	}

	return end
}

// singleQuotedContent returns the inner text when value is exactly one quoted
// string with no inner unescaped quote.
func singleQuotedContent(value string) (string, bool) {
	if len(value) < singleQuotedContentMinLen {
		return "", false
	}

	q := value[0]
	if (q != '"' && q != '\'') || value[len(value)-1] != q || strings.Contains(value[1:len(value)-1], string(q)) {
		return "", false
	}

	return value[1 : len(value)-1], true
}

// scanQuotedContent finds the closing quote of a string whose opening quote is
// value[open-1], honoring backslash escapes.
func scanQuotedContent(value string, open int, quote byte) (int, bool) {
	jdx := open
	for jdx < len(value) {
		if value[jdx] == '\\' && jdx+1 < len(value) {
			jdx += 2

			continue
		}

		if value[jdx] == quote {
			return jdx, true
		}

		jdx++
	}

	return 0, false
}

// parseAttrToken evaluates attr(...) starting at idx (value[idx:] begins with
// "attr("). Returns the attribute value ("" when absent) and the index just
// past the closing paren.
func parseAttrToken(value string, idx int, node *html.Node) (string, int) {
	arg, next := attrArg(value, idx)

	name := arg
	if sp := strings.IndexAny(arg, " \t,"); sp >= 0 {
		name = arg[:sp]
	}

	name = strings.Trim(name, `"'`)

	val := ""
	if node != nil && name != "" {
		val = node.Attribute(name)
	}

	if next < len(value) && value[next] == ')' {
		next++
	}

	return val, next
}

// attrArg returns the trimmed argument text of the attr(...) call starting at
// idx (value[idx:] begins with "attr(") plus the index just past the closing
// paren (len(value) when unbalanced).
func attrArg(value string, idx int) (string, int) {
	start := idx + len("attr(")

	end, depth := start, 1
	for end < len(value) && depth > 0 {
		if value[end] == '(' {
			depth++
		} else if value[end] == ')' {
			depth--
			if depth == 0 {
				break
			}
		}

		end++
	}

	return strings.TrimSpace(value[start:end]), end
}

// skipCSSFunction returns the index just past the closing paren of the
// function whose opening paren is at start (value[start-1] == '(').
func skipCSSFunction(value string, start int) int {
	depth := 1

	for start < len(value) && depth > 0 {
		switch value[start] {
		case '(':
			depth++
		case ')':
			depth--
		}

		start++
	}

	return start
}

// skipCSSIdent advances idx past a CSS identifier (one word).
func skipCSSIdent(v string, idx int) int {
	for idx < len(v) && isIdentCont(v[idx]) {
		idx++
	}

	return idx
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// isCSSWhitespace reports whether c is CSS whitespace.
func isCSSWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// skipCSSWhitespace advances idx past CSS whitespace.
func skipCSSWhitespace(v string, idx int) int {
	for idx < len(v) && isCSSWhitespace(v[idx]) {
		idx++
	}

	return idx
}

func decodeCSSString(value string) string {
	var boxNode strings.Builder

	for idx := 0; idx < len(value); idx++ {
		if value[idx] != '\\' || idx+1 >= len(value) {
			boxNode.WriteByte(value[idx])

			continue
		}

		idx++
		if isHex(value[idx]) {
			r, next := decodeHexEscape(value, idx)
			if r != 0 {
				boxNode.WriteRune(r)
			}

			idx = next

			continue
		}

		switch value[idx] {
		case 'n':
			boxNode.WriteByte('\n')
		case 'r':
			boxNode.WriteByte('\r')
		case 't':
			boxNode.WriteByte('\t')
		default:
			boxNode.WriteByte(value[idx])
		}
	}

	return boxNode.String()
}

// decodeHexEscape consumes up to six hex digits at value[start] (the digits
// after a backslash), honors the optional single-whitespace terminator, and
// returns the decoded rune plus the index to resume from.
func decodeHexEscape(value string, start int) (rune, int) {
	jdx := start
	for jdx < len(value) && jdx-start < 6 && isHex(value[jdx]) {
		jdx++
	}

	idx := jdx
	if idx >= len(value) || !isCSSWhitespace(value[idx]) {
		idx--
	}

	var code int

	if _, err := fmt.Sscanf(value[start:jdx], "%x", &code); err != nil {
		return 0, idx
	}

	return rune(code), idx
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
