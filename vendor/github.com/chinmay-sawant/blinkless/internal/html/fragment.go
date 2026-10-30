// Context-aware fragment parsing. The entry point is internal: parseFragment
// takes the context element's local name, namespace, and attributes and
// returns the synthetic root whose Children are the fragment output. The
// public Parse/ParseDocument signatures stay unchanged until the fragment
// contract has proven table, select, raw-text, and foreign contexts against
// the pinned corpus.
//
// Tokenizer seeding follows the standard's "parsing HTML fragments" step 12.
// RCDATA contexts (title, textarea) are tokenized as one text run with
// character references decoded. RAWTEXT and script-data contexts use the
// spec-sanctioned PLAINTEXT substitution: no appropriate end tag token exists
// in a fragment, so the whole input is one raw text run. Data contexts run the
// ordinary tokenizer.
//
//nolint:all // fragment entry point; same custom Node model and conventions as tree.go
package html

// fragmentContextMode returns the tokenizer state a context element selects.
// Data is the fallback, including noscript under scripting disabled.
func fragmentContextMode(context *Node) textMode {
	if context == nil || context.Namespace != NamespaceHTML {
		return textData
	}

	switch context.Name {
	case "title", "textarea":
		return textRCDATA
	case "style", "xmp", "iframe", "noembed", "noframes", "script", "plaintext":
		// RAWTEXT and script data share the substitution: one raw text run.
		return textRAWTEXT
	default:
		return textData
	}
}

// newFragmentTreeBuilder builds the fragment parser state: the stack holds
// only the synthetic root html element, and the context element is remembered
// for the adjusted current node and the reset-insertion-mode substitution.
func newFragmentTreeBuilder(context *Node) *treeBuilder {
	root := &Node{Type: ElementNode, Name: "html"} //nolint:exhaustruct

	return &treeBuilder{
		root:            root,
		stack:           []*Node{root},
		framesetOK:      true,
		fragment:        true,
		fragmentContext: context,
	}
}

// parseFragment parses src with the given context element. The returned root
// is the synthetic html element; its Children are the fragment output, which
// is what the standard returns as the fragment (the context element itself is
// never part of the output).
func parseFragment(contextName string, contextNS Namespace, contextAttrs []string, src string) *Node {
	context := &Node{Type: ElementNode, Name: contextName, Namespace: contextNS} //nolint:exhaustruct
	applyAttributes(context, contextAttrs)

	builder := newFragmentTreeBuilder(context)

	if isTemplateElement(context) {
		builder.pushTemplateMode(modeInTemplate)
	}

	builder.resetInsertionMode()

	scanFragmentTokens(src, context, builder)
	builder.finish()
	applySelectedContent(builder.root)

	return builder.root
}

// scanFragmentTokens tokenizes fragment input under the context's tokenizer
// state. preprocessInput runs once up front for the raw-text paths. The
// builder answers the CDATA policy so an SVG or MathML context element
// accepts CDATA sections, mirroring the adjusted-current-node rule.
func scanFragmentTokens(src string, context *Node, builder *treeBuilder) {
	emit := builder.appendToken

	switch fragmentContextMode(context) {
	case textData:
		scanTokens(src, emit, builder.foreignCDATAAllowed)
	case textRCDATA:
		text, _, _ := scanRawText(preprocessInput(src), 0, "")
		if text != "" {
			emit(token{kind: tokText, data: UnescapeEntities(replaceNUL(text))}) //nolint:exhaustruct
		}
	default:
		// RAWTEXT and script data: PLAINTEXT substitution, sanctioned by the
		// standard for fragments because no end tag can match.
		text := preprocessInput(src)
		if text != "" {
			emit(token{kind: tokText, data: replaceNUL(text)}) //nolint:exhaustruct
		}
	}
}

// fragmentContextIsSelect reports whether this is a fragment parse whose
// context element is an HTML select element.
func (b *treeBuilder) fragmentContextIsSelect() bool {
	return b.fragment && b.fragmentContext != nil &&
		b.fragmentContext.Namespace == NamespaceHTML && b.fragmentContext.Name == "select"
}

// adjustedCurrent returns the standard's adjusted current node: the fragment
// context element while the stack holds only the synthetic root, otherwise
// the current node.
func (b *treeBuilder) adjustedCurrent() *Node {
	if b.fragment && b.fragmentContext != nil && len(b.stack) == 1 {
		return b.fragmentContext
	}

	return b.top()
}
