// recovery conventions as html.go
//
//nolint:all // insertion-mode tree builder; same custom Node model and
package html

import "strings"

// maxElementDepth caps element nesting. Elements that would nest deeper are
// dropped by insertNode, so recursive walks stay bounded on adversarial input
// instead of exhausting the stack.
const maxElementDepth = 1024

// maxReprocess bounds mode-transition reprocessing so adversarial input cannot
// loop between insertion modes. It is a hard limit: appendToken runs the mode
// chain at most this many times.
const maxReprocess = 8

// maxForeignReparse caps how many times foreign raw text is tokenized again.
// A raw-text element inside that run can scan again. Past the cap the run is
// inserted as text, so nested <svg><style> cannot recurse without bound.
const maxForeignReparse = 32

// Attribute and selected-content caps keep adversarial input from driving
// unbounded allocation during parsing.
const (
	// maxAttributesPerElement caps the number of attributes stored on one
	// element; excess attributes are dropped during token scanning.
	maxAttributesPerElement = 1000

	// maxAttributeNameBytes caps one attribute name's size.
	maxAttributeNameBytes = 4096

	// maxAttributeValueBytes caps one attribute value's size.
	maxAttributeValueBytes = 64 * 1024

	// maxAttributeBytesPerElement caps the total attribute bytes stored on
	// one element.
	maxAttributeBytesPerElement = 64 * 1024

	// maxSelectedContentBytes bounds the deep clone performed for a
	// selectedcontent element, so a huge selected subtree cannot double the
	// tree's memory.
	maxSelectedContentBytes = 1 << 20
)

// depthExceeded reports whether a nesting depth has reached the element-depth
// cap. Callers pass the depth of the node they are about to process or
// create; the cap is applied consistently with insertNode.
func depthExceeded(depth int) bool {
	return depth >= maxElementDepth
}

// insertionMode is the standard's tree-construction mode.
type insertionMode uint8

const (
	modeInitial insertionMode = iota
	modeBeforeHTML
	modeBeforeHead
	modeInHead
	modeInHeadNoscript
	modeAfterHead
	modeInBody
	modeInText
	modeInTable
	modeInCaption
	modeInColumnGroup
	modeInTableBody
	modeInRow
	modeInCell
	modeInTemplate
	modeAfterBody
	modeInFrameset
	modeAfterFrameset
	modeAfterAfterBody
	modeAfterAfterFrameset
)

// treeBuilder accumulates parsed tokens into a node tree.
type treeBuilder struct {
	root             *Node
	stack            []*Node
	mode             insertionMode
	textReturn       insertionMode // mode to restore after a raw-text element
	head             *Node         // head element pointer
	form             *Node         // form element pointer
	docMode          DocumentMode
	docModeSet       bool
	ignoreNextLF     bool // swallow the LF after <pre>, <listing>, <textarea>
	fosterParenting  bool // route insertions around the open table
	pendingTableText []string
	activeFormatting []*Node         // nil entries are scope markers
	templateModes    []insertionMode // stack of template insertion modes
	framesetOK       bool            // whether a frameset start tag may replace body
	fragment         bool            // fragment parsing: the synthetic root holds the context
	fragmentContext  *Node           // context element for a fragment parse (nil for documents)
	reparseRawText   bool            // next text token holds foreign raw-text content; re-tokenize in Data state
	reparseDepth     int             // nested foreign raw-text reparses; capped by maxForeignReparse
	textAccum        *Node           // text node whose bytes are still in textBytes
	textBytes        []byte          // pending text for textAccum; flushed onto Node.Text
}

func newTreeBuilder() *treeBuilder {
	root := &Node{Type: ElementNode, Name: "#document"} //nolint:exhaustruct

	return &treeBuilder{
		root:       root,
		stack:      []*Node{root},
		framesetOK: true,
	}
}

// insertMarker pushes a scope marker onto the active formatting list.
func (b *treeBuilder) insertMarker() {
	b.activeFormatting = append(b.activeFormatting, nil)
}

// clearActiveFormattingToMarker removes active formatting entries up to and
// including the last scope marker, per the standard's clear-to-marker rule.
func (b *treeBuilder) clearActiveFormattingToMarker() {
	for idx := len(b.activeFormatting) - 1; idx >= 0; idx-- {
		if b.activeFormatting[idx] == nil {
			b.activeFormatting = b.activeFormatting[:idx]

			return
		}
	}

	b.activeFormatting = nil
}

func (b *treeBuilder) top() *Node {
	return b.stack[len(b.stack)-1]
}

// appendToken applies one scanned token, reprocessing it through insertion
// modes as the standard requires.
func (b *treeBuilder) appendToken(tokItem token) {
	// A raw-text element inserted in foreign content is not raw text there:
	// the tokenizer consumed its content by tag name, so re-tokenize the
	// captured run in the Data state. The flag clears on the next token
	// whatever its kind, so ordinary text is never reparsed.
	if b.reparseRawText {
		b.reparseRawText = false

		if tokItem.kind == tokText && b.reparseDepth < maxForeignReparse {
			b.reparseDepth++
			scanTokens(tokItem.data, b.appendToken, b.foreignCDATAAllowed)
			b.reparseDepth--

			return
		}
	}

	if tokItem.kind != tokText {
		b.flushPendingTableText()
	}

	for range maxReprocess {
		if !b.processToken(&tokItem) {
			return
		}
	}
}

// finish runs the end-of-file mode chain: every mode before "in body" creates
// the document structure the standard inserts at EOF.
func (b *treeBuilder) finish() {
	defer b.flushTextAccum()

	b.flushPendingTableText()

	for {
		// The in-body end-of-file rule defers to the in-template rules
		// whenever a template insertion mode is open, whatever the current
		// insertion mode is.
		if len(b.templateModes) > 0 {
			if !b.hasOpenHTMLTemplate() {
				b.root.Mode = b.docMode

				return
			}

			b.popUntilHTMLTemplate()
			b.clearActiveFormattingToMarker()
			b.popTemplateMode()
			b.resetInsertionMode()

			continue
		}

		switch b.mode {
		case modeInitial:
			if !b.docModeSet {
				b.docMode = Quirks
				b.docModeSet = true
			}

			b.mode = modeBeforeHTML
		case modeBeforeHTML:
			b.insertHTMLElement("html", nil)
			b.mode = modeBeforeHead
		case modeBeforeHead:
			b.head = b.insertHTMLElement("head", nil)
			b.mode = modeInHead
		case modeInHead:
			b.popCurrentIfName("head")
			b.mode = modeAfterHead
		case modeInHeadNoscript:
			// EOF inside noscript closes it and returns to "in head", whose
			// own EOF chain then builds the body.
			b.popCurrentIfName("noscript")
			b.mode = modeInHead
		case modeAfterHead:
			b.insertHTMLElement("body", nil)
			b.mode = modeInBody
		case modeInText:
			// EOF in a raw-text element pops it and reprocesses under the
			// original insertion mode, which then builds head/body as needed.
			if len(b.stack) > 1 {
				b.stack = b.stack[:len(b.stack)-1]
			}

			b.mode = b.textReturn
		case modeInTemplate:
			if !b.hasOpenHTMLTemplate() {
				// No open template: the standard stops parsing (fragment
				// case). Nothing left to pop.
				b.root.Mode = b.docMode

				return
			}

			b.popUntilHTMLTemplate()
			b.clearActiveFormattingToMarker()
			b.popTemplateMode()
			b.resetInsertionMode()
		case modeInFrameset, modeAfterFrameset, modeAfterAfterFrameset:
			b.root.Mode = b.docMode

			return
		default:
			b.root.Mode = b.docMode

			return
		}
	}
}

// processToken applies the current insertion mode. It reports whether the
// token must be reprocessed after a mode switch.
func (b *treeBuilder) processToken(tokItem *token) bool {
	if b.foreignToken(tokItem) {
		return false
	}

	switch b.mode {
	case modeInitial:
		return b.processInitial(tokItem)
	case modeBeforeHTML:
		return b.processBeforeHTML(tokItem)
	case modeBeforeHead:
		return b.processBeforeHead(tokItem)
	case modeInHead:
		return b.processInHead(tokItem)
	case modeInHeadNoscript:
		return b.processInHeadNoscript(tokItem)
	case modeAfterHead:
		return b.processAfterHead(tokItem)
	case modeInBody:
		return b.processInBody(tokItem)
	case modeInText:
		return b.processInText(tokItem)
	case modeInTable:
		return b.processInTable(tokItem)
	case modeInCaption:
		return b.processInCaption(tokItem)
	case modeInColumnGroup:
		return b.processInColumnGroup(tokItem)
	case modeInTableBody:
		return b.processInTableBody(tokItem)
	case modeInRow:
		return b.processInRow(tokItem)
	case modeInCell:
		return b.processInCell(tokItem)
	case modeInTemplate:
		return b.processInTemplate(tokItem)
	case modeAfterBody:
		return b.processAfterBody(tokItem)
	case modeInFrameset:
		return b.processInFrameset(tokItem)
	case modeAfterFrameset:
		return b.processAfterFrameset(tokItem)
	case modeAfterAfterBody:
		return b.processAfterAfterBody(tokItem)
	case modeAfterAfterFrameset:
		return b.processAfterAfterFrameset(tokItem)
	default:
		return false
	}
}

// --- insertion modes ---

func (b *treeBuilder) processInitial(tokItem *token) bool {
	switch tokItem.kind {
	case tokComment:
		b.appendCommentTo(b.root, tokItem.data)

		return false
	case tokDoctype:
		if !b.docModeSet {
			b.docMode = classifyDocumentMode(tokItem.doctype)
			b.docModeSet = true
			b.root.Children = append(b.root.Children, &Node{Type: DoctypeNode, Text: tokItem.data, Doctype: tokItem.doctype}) //nolint:exhaustruct
		}

		b.mode = modeBeforeHTML

		return false
	case tokText:
		_, rest := splitLeadingWhitespace(tokItem.data)
		if rest == "" {
			return false
		}

		tokItem.data = rest
	case tokStart, tokEnd:
	}

	if !b.docModeSet {
		b.docMode = Quirks
		b.docModeSet = true
	}

	b.mode = modeBeforeHTML

	return true
}

func (b *treeBuilder) processBeforeHTML(tokItem *token) bool {
	switch tokItem.kind {
	case tokDoctype:
		return false
	case tokComment:
		b.appendCommentTo(b.root, tokItem.data)

		return false
	case tokText:
		_, rest := splitLeadingWhitespace(tokItem.data)
		if rest == "" {
			return false
		}

		tokItem.data = rest
	case tokStart:
		if tokItem.data == "html" {
			b.insertHTMLElement("html", tokItem.attrs)
			b.mode = modeBeforeHead

			return false
		}
	case tokEnd:
		switch tokItem.data {
		case "head", "body", "html", "br":
		default:
			return false
		}
	}

	b.insertHTMLElement("html", nil)
	b.mode = modeBeforeHead

	return true
}

func (b *treeBuilder) processBeforeHead(tokItem *token) bool {
	switch tokItem.kind {
	case tokDoctype:
		return false
	case tokComment:
		b.appendCommentTo(b.top(), tokItem.data)

		return false
	case tokText:
		_, rest := splitLeadingWhitespace(tokItem.data)
		if rest == "" {
			return false
		}

		tokItem.data = rest
	case tokStart:
		switch tokItem.data {
		case "html":
			b.mergeIntoHTMLElement(tokItem.attrs)

			return false
		case "head":
			b.head = b.insertHTMLElement("head", tokItem.attrs)
			b.mode = modeInHead

			return false
		}
	case tokEnd:
		switch tokItem.data {
		case "head", "body", "html", "br":
		default:
			return false
		}
	}

	b.head = b.insertHTMLElement("head", nil)
	b.mode = modeInHead

	return true
}

func (b *treeBuilder) processInHead(tokItem *token) bool {
	switch tokItem.kind {
	case tokDoctype:
		return false
	case tokComment:
		b.appendCommentTo(b.top(), tokItem.data)

		return false
	case tokText:
		prefix, rest := splitLeadingWhitespace(tokItem.data)
		if rest == "" {
			b.appendTextToken(tokItem.data)

			return false
		}

		if prefix != "" {
			b.appendTextToken(prefix)
		}

		tokItem.data = rest
		b.popCurrentIfName("head")
		b.mode = modeAfterHead

		return true
	case tokStart:
		switch tokItem.data {
		case "html":
			b.mergeIntoHTMLElement(tokItem.attrs)

			return false
		case "base", "basefont", "bgsound", "link", "meta":
			b.insertHTMLElement(tokItem.data, tokItem.attrs)

			return false
		case "template":
			// The in-head template rule: insert, mark the active formatting
			// list, and push the template insertion mode.
			b.insertHTMLElement(tokItem.data, tokItem.attrs)
			b.insertMarker()
			b.framesetOK = false
			b.mode = modeInTemplate
			b.pushTemplateMode(modeInTemplate)

			return false
		case "noscript":
			// With scripting disabled (this engine's pinned mode) noscript is
			// an ordinary head element and switches to "in head noscript";
			// with scripting enabled it would be raw text.
			b.insertHTMLElement(tokItem.data, tokItem.attrs)
			b.mode = modeInHeadNoscript

			return false
		case "title", "style", "noframes", "script":
			b.insertHTMLElement(tokItem.data, tokItem.attrs)
			if textModeElement(tokItem.data) {
				b.textReturn = b.mode
				b.mode = modeInText
			}

			return false
		}
	case tokEnd:
		if tokItem.data == "head" {
			b.popCurrentIfName("head")
			b.mode = modeAfterHead

			return false
		}

		if tokItem.data == "template" {
			b.closeTemplate()

			return false
		}

		if tokItem.data != "body" && tokItem.data != "html" && tokItem.data != "br" {
			b.popCurrentIfName(tokItem.data)

			return false
		}
	}

	b.popCurrentIfName("head")
	b.mode = modeAfterHead

	return true
}

// processInHeadNoscript implements the "in head noscript" insertion mode:
// noscript in head with scripting disabled. Comments and whitespace stay
// inside the noscript; head-content tags run the "in head" rules; anything
// else closes the noscript and is reprocessed in "in head". Any other end
// tag is ignored.
func (b *treeBuilder) processInHeadNoscript(tokItem *token) bool {
	switch tokItem.kind {
	case tokDoctype:
		return false
	case tokComment:
		b.appendCommentTo(b.top(), tokItem.data)

		return false
	case tokText:
		prefix, rest := splitLeadingWhitespace(tokItem.data)
		if prefix != "" {
			b.appendTextToken(prefix)
		}

		if rest == "" {
			return false
		}

		tokItem.data = rest
	case tokStart:
		switch tokItem.data {
		case "html":
			return b.processInBody(tokItem)
		case "basefont", "bgsound", "link", "meta", "noframes", "style":
			return b.processInHead(tokItem)
		case "head", "noscript":
			return false
		}
	case tokEnd:
		switch tokItem.data {
		case "noscript":
			b.popCurrentIfName("noscript")
			b.mode = modeInHead

			return false
		case "br":
			// Act as described in "anything else".
		default:
			// Any other end tag is a parse error and is ignored.
			return false
		}
	}

	b.popCurrentIfName("noscript")
	b.mode = modeInHead

	return true
}

func (b *treeBuilder) processAfterHead(tokItem *token) bool {
	switch tokItem.kind {
	case tokDoctype:
		return false
	case tokComment:
		b.appendCommentTo(b.top(), tokItem.data)

		return false
	case tokText:
		prefix, rest := splitLeadingWhitespace(tokItem.data)
		if rest == "" {
			b.appendTextToken(tokItem.data)

			return false
		}

		if prefix != "" {
			b.appendTextToken(prefix)
		}

		tokItem.data = rest
		b.insertHTMLElement("body", nil)
		b.framesetOK = true
		b.mode = modeInBody

		return true
	case tokStart:
		switch tokItem.data {
		case "html":
			b.mergeIntoHTMLElement(tokItem.attrs)

			return false
		case "body":
			b.insertHTMLElement("body", tokItem.attrs)
			b.framesetOK = false
			b.mode = modeInBody

			return false
		case "frameset":
			b.insertHTMLElement("frameset", tokItem.attrs)
			b.mode = modeInFrameset

			return false
		case "base", "basefont", "bgsound", "link", "meta", "noframes", "script", "style", "template", "title":
			b.processHeadContent(tokItem)

			return false
		case "head":
			// A second head start tag after head is a parse error; ignore it.
			return false
		}
	case tokEnd:
		switch tokItem.data {
		case "template":
			b.processInHead(tokItem)

			return false
		case "body", "html", "br":
		default:
			return false
		}
	}

	b.insertHTMLElement("body", nil)
	b.framesetOK = true
	b.mode = modeInBody

	return true
}

func (b *treeBuilder) processInText(tokItem *token) bool {
	switch tokItem.kind {
	case tokText:
		// A textarea drops one leading LF: the spec ignores it as an
		// authoring convenience. pre/listing share the flag but consume it
		// in the in-body text rule.
		data := tokItem.data
		if b.ignoreNextLF {
			b.ignoreNextLF = false

			data = strings.TrimPrefix(data, "\n")
		}

		if data != "" {
			b.appendTextToken(data)
		}
	case tokEnd:
		b.ignoreNextLF = false

		if len(b.stack) > 1 {
			b.stack = b.stack[:len(b.stack)-1]
		}

		b.mode = b.textReturn
	case tokStart, tokComment, tokDoctype:
		// Raw text is consumed by the tokenizer; these cannot occur here.
		b.ignoreNextLF = false
	}

	return false
}

func (b *treeBuilder) processInBody(tokItem *token) bool {
	switch tokItem.kind {
	case tokComment:
		b.appendCommentTo(b.top(), tokItem.data)

		return false
	case tokDoctype:
		return false
	case tokText:
		data := tokItem.data
		if b.ignoreNextLF {
			b.ignoreNextLF = false
			data = strings.TrimPrefix(data, "\n")
		}

		// U+0000 character tokens are ignored in body: they neither insert
		// text nor unset the frameset-ok flag.
		if strings.IndexByte(data, 0) >= 0 {
			data = strings.ReplaceAll(data, "\x00", "")
		}

		if data != "" {
			b.reconstructActiveFormatting()
			b.appendTextToken(data)

			if !isAllWhitespace(data) {
				b.framesetOK = false
			}
		}

		return false
	case tokStart:
		return b.processInBodyStart(tokItem)
	case tokEnd:
		return b.processInBodyEnd(tokItem)
	default:
		return false
	}
}

func (b *treeBuilder) processInBodyStart(tokItem *token) bool {
	name := tokItem.data

	switch name {
	case "html":
		if b.hasOpenHTMLTemplate() {
			return false
		}

		b.mergeIntoHTMLElement(tokItem.attrs)

		return false
	case "base", "basefont", "bgsound", "link", "meta", "noframes", "script", "style", "template", "title":
		b.processInHead(tokItem)

		return false
	case "body":
		if body := b.findInScope("body", defaultScopeStops); body != nil {
			applyAttributes(body, tokItem.attrs)
			b.framesetOK = false
		}

		return false
	case "address", "article", "aside", "blockquote", "center", "details", "dialog",
		"dir", "div", "dl", "fieldset", "figcaption", "figure", "footer", "header",
		"hgroup", "main", "menu", "nav", "ol", "p", "search", "section", "summary", "ul":
		b.closePElementIfOpen()
		b.insertHTMLElement(name, tokItem.attrs)

		return false
	case "h1", "h2", "h3", "h4", "h5", "h6":
		b.closePElementIfOpen()

		if isHeadingElement(b.top()) {
			b.stack = b.stack[:len(b.stack)-1]
		}

		b.insertHTMLElement(name, tokItem.attrs)

		return false
	case "pre", "listing":
		b.closePElementIfOpen()
		b.insertHTMLElement(name, tokItem.attrs)

		b.ignoreNextLF = true
		b.framesetOK = false

		return false
	case "form":
		if b.form != nil {
			return false
		}

		b.closePElementIfOpen()
		b.form = b.insertHTMLElement(name, tokItem.attrs)

		return false
	case "li":
		b.closeOpenListItem()
		b.closePElementIfOpen()
		b.insertHTMLElement(name, tokItem.attrs)
		b.framesetOK = false

		return false
	case "dd", "dt":
		b.closeOpenDefinitionItem()
		b.closePElementIfOpen()
		b.insertHTMLElement(name, tokItem.attrs)
		b.framesetOK = false

		return false
	case "plaintext":
		b.closePElementIfOpen()
		b.insertHTMLElement(name, tokItem.attrs)
		b.framesetOK = false

		return false
	case "button":
		if b.hasInScope("button", defaultScopeStops) {
			b.generateImpliedEndTags("")
			b.popUntilName("button")
		}

		b.reconstructActiveFormatting()
		b.insertHTMLElement(name, tokItem.attrs)
		b.framesetOK = false

		return false
	case "a":
		if existing := b.lastActiveFormatting("a"); existing != nil {
			b.adoptionAgency(tokItem)

			if b.activeFormattingContains(existing) {
				b.activeFormattingRemove(existing)
			}

			b.removeFromStack(existing)
		}

		b.reconstructActiveFormatting()
		b.pushActiveFormatting(b.insertHTMLElement(name, tokItem.attrs))

		return false
	case "b", "big", "code", "em", "font", "i", "s", "small", "strike", "strong", "tt", "u":
		b.reconstructActiveFormatting()
		b.pushActiveFormatting(b.insertHTMLElement(name, tokItem.attrs))

		return false
	case "nobr":
		b.reconstructActiveFormatting()

		if b.hasInScope("nobr", defaultScopeStops) {
			b.adoptionAgency(tokItem)
			b.reconstructActiveFormatting()
		}

		b.pushActiveFormatting(b.insertHTMLElement(name, tokItem.attrs))

		return false
	case "applet", "marquee", "object":
		b.reconstructActiveFormatting()
		b.insertHTMLElement(name, tokItem.attrs)
		b.insertMarker()
		b.framesetOK = false

		return false
	case "area", "br", "embed", "img", "keygen", "wbr":
		b.reconstructActiveFormatting()
		b.insertHTMLElement(name, tokItem.attrs)
		b.framesetOK = false

		return false
	case "input":
		// A fragment whose context is select ignores input start tags.
		if b.fragmentContextIsSelect() {
			return false
		}

		// A select element in scope is closed before the input is inserted;
		// the input then lands outside the select, per the current standard.
		if b.selectInScope() {
			b.popUntilSelectPopped()
		}

		b.reconstructActiveFormatting()
		b.insertHTMLElement(name, tokItem.attrs)

		if !inputIsHidden(tokItem) {
			b.framesetOK = false
		}

		return false
	case "param", "source", "track":
		// Unlike the current standard, the pinned corpus keeps frameset-ok
		// set here: <param>/<source>/<track> do not block a later <frameset>.
		b.insertHTMLElement(name, tokItem.attrs)

		return false
	case "hr":
		b.closePElementIfOpen()

		if b.selectInScope() {
			b.generateImpliedEndTags("")
		}

		b.insertHTMLElement(name, tokItem.attrs)
		b.framesetOK = false

		return false
	case "image":
		tokItem.data = "img"

		return true
	case "textarea":
		b.insertHTMLElement(name, tokItem.attrs)

		b.ignoreNextLF = true
		b.framesetOK = false
		b.textReturn = b.mode
		b.mode = modeInText

		return false
	case "xmp":
		b.closePElementIfOpen()
		b.reconstructActiveFormatting()
		b.insertHTMLElement(name, tokItem.attrs)

		b.framesetOK = false
		b.textReturn = b.mode
		b.mode = modeInText

		return false
	case "iframe", "noembed":
		b.insertHTMLElement(name, tokItem.attrs)

		b.framesetOK = false
		b.textReturn = b.mode
		b.mode = modeInText

		return false
	case "select":
		// A fragment whose context is select ignores select start tags
		// without closing anything.
		if b.fragmentContextIsSelect() {
			return false
		}

		// A nested select start tag closes the outer select and is ignored.
		if b.selectInScope() {
			b.popUntilSelectPopped()

			return false
		}

		b.reconstructActiveFormatting()
		b.insertHTMLElement(name, tokItem.attrs)
		b.framesetOK = false

		return false
	case "option":
		if b.selectInScope() {
			b.generateImpliedEndTags("optgroup")
		} else if b.top().Namespace == NamespaceHTML && b.top().Name == "option" {
			b.stack = b.stack[:len(b.stack)-1]
		}

		b.reconstructActiveFormatting()
		b.insertHTMLElement(name, tokItem.attrs)

		return false
	case "optgroup":
		if b.selectInScope() {
			b.generateImpliedEndTags("")
		} else if b.top().Namespace == NamespaceHTML && b.top().Name == "option" {
			b.stack = b.stack[:len(b.stack)-1]
		}

		b.reconstructActiveFormatting()
		b.insertHTMLElement(name, tokItem.attrs)

		return false
	case "math":
		b.insertForeignElement(name, NamespaceMathML, tokItem)

		return false
	case "svg":
		b.insertForeignElement(name, NamespaceSVG, tokItem)

		return false
	case "rb", "rtc":
		// The ruby base/annotation-container rules: close implied end tags
		// (including any open rb/rt/rp/rtc) when a ruby element is in scope.
		if b.hasInScope("ruby", defaultScopeStops) {
			b.generateImpliedEndTags("")
		}

		b.insertHTMLElement(name, tokItem.attrs)

		return false
	case "rp", "rt":
		// Ruby annotation rules: close implied end tags except an open rtc,
		// so annotations stay inside their annotation container.
		if b.hasInScope("ruby", defaultScopeStops) {
			b.generateImpliedEndTags("rtc")
		}

		b.insertHTMLElement(name, tokItem.attrs)

		return false
	case "table":
		if b.docMode != Quirks {
			b.closePElementIfOpen()
		}

		b.insertHTMLElement(name, tokItem.attrs)
		b.framesetOK = false
		b.mode = modeInTable

		return false
	case "frameset":
		// A frameset start tag replaces body only when body is the second
		// element on the stack and no content has made the frameset-ok flag
		// fail. stack[0] is the synthetic document root, so body is stack[2];
		// a fragment has no body and ignores the token.
		if b.fragment || len(b.stack) < 3 ||
			b.stack[2].Namespace != NamespaceHTML || b.stack[2].Name != "body" {
			return false
		}

		if !b.framesetOK {
			return false
		}

		detachNode(b.stack[2])
		b.stack = b.stack[:2]
		b.insertHTMLElement(name, tokItem.attrs)
		b.mode = modeInFrameset

		return false
	case "caption", "col", "colgroup", "frame", "head", "tbody", "td", "tfoot", "th", "thead", "tr":
		// A table-structure tag outside its table, frame, and head are parse
		// errors in body; the table insertion modes own the table tags.
		return false
	default:
		if !defaultNoReconstruct[name] {
			b.reconstructActiveFormatting()
		}

		b.insertHTMLElement(name, tokItem.attrs)

		return false
	}
}

func (b *treeBuilder) processInBodyEnd(tokItem *token) bool {
	name := tokItem.data

	if isFormattingTag(name) {
		b.adoptionAgency(tokItem)

		return false
	}

	switch name {
	case "body":
		if !b.hasInScope("body", defaultScopeStops) {
			return false
		}

		b.mode = modeAfterBody

		return false
	case "html":
		if !b.hasInScope("body", defaultScopeStops) {
			return false
		}

		// Reprocess in "after body" so a document advances to "after after
		// body": comments there belong to the Document, and the corpus pins
		// that placement.
		b.mode = modeAfterBody

		return true
	case "p":
		if !b.hasInButtonScope("p") {
			b.insertHTMLElement("p", nil)
		}

		b.closePElement()

		return false
	case "li":
		if !b.hasInListScope("li") {
			return false
		}

		b.generateImpliedEndTags("li")
		b.popUntilName("li")

		return false
	case "dd", "dt":
		if !b.hasInScope(name, defaultScopeStops) {
			return false
		}

		b.generateImpliedEndTags(name)
		b.popUntilName(name)

		return false
	case "h1", "h2", "h3", "h4", "h5", "h6":
		if !b.hasInScopeAny(headingNames, defaultScopeStops) {
			return false
		}

		b.generateImpliedEndTags("")
		b.popUntilAnyName(headingNames...)

		return false
	case "br":
		b.reconstructActiveFormatting()
		b.insertHTMLElement("br", nil)

		return false
	case "applet", "marquee", "object":
		if !b.hasInScope(name, defaultScopeStops) {
			return false
		}

		b.generateImpliedEndTags("")
		b.popUntilName(name)
		b.clearActiveFormattingToMarker()

		return false
	case "form":
		node := b.form
		if node == nil {
			node = b.findInScope("form", defaultScopeStops)
		}

		b.form = nil

		if node == nil {
			return false
		}

		b.generateImpliedEndTags("")
		b.removeFromStack(node)

		return false
	case "template":
		b.processInHead(tokItem)

		return false
	}

	if bodyEndTagsWithScope[name] {
		if !b.hasInScope(name, defaultScopeStops) {
			return false
		}

		b.generateImpliedEndTags("")
		b.popUntilName(name)

		return false
	}

	b.closeHTMLElement(name)

	return false
}

func (b *treeBuilder) processAfterBody(tokItem *token) bool {
	switch tokItem.kind {
	case tokComment:
		b.appendCommentTo(b.firstStackElement(), tokItem.data)

		return false
	case tokDoctype:
		return false
	case tokText:
		if isAllWhitespace(tokItem.data) {
			return b.processInBody(tokItem)
		}
	case tokStart:
		if tokItem.data == "html" {
			return b.processInBody(tokItem)
		}
	case tokEnd:
		if tokItem.data == "html" {
			// A fragment never leaves through the document root: ignore.
			if b.fragment {
				return false
			}

			b.mode = modeAfterAfterBody

			return false
		}
	}

	b.mode = modeInBody

	return true
}

func (b *treeBuilder) processAfterAfterBody(tokItem *token) bool {
	switch tokItem.kind {
	case tokComment:
		b.appendCommentTo(b.root, tokItem.data)

		return false
	case tokDoctype:
		return false
	case tokText:
		if isAllWhitespace(tokItem.data) {
			return b.processInBody(tokItem)
		}
	case tokStart:
		if tokItem.data == "html" {
			return b.processInBody(tokItem)
		}
	}

	b.mode = modeInBody

	return true
}

// processInTemplate implements the "in template" insertion mode.
// --- insertion helpers ---

// insertHTMLElement inserts an HTML element at the current position.
func (b *treeBuilder) insertHTMLElement(name string, attrs []string) *Node {
	return b.insertNode(name, NamespaceHTML, attrs, false)
}

// insertionPoint is where the next node goes: a parent plus an optional
// reference child to insert before (nil appends at the end). contents routes
// the insertion into the parent template's Contents instead of its Children.
type insertionPoint struct {
	parent   *Node
	before   *Node
	contents bool
}

// appropriatePlace implements the standard's "appropriate place for inserting
// a node" for the current node: the top of the open-element stack, or, with
// foster parenting enabled over a table section, the position just before the
// open table (or the open template's contents when no table is open).
func (b *treeBuilder) appropriatePlace() insertionPoint {
	return b.appropriatePlaceFor(b.top())
}

// appropriatePlaceFor is appropriatePlace relative to target, for the
// adoption agency's adjusted insertion location.
func (b *treeBuilder) appropriatePlaceFor(target *Node) insertionPoint {
	if !b.fosterParenting || !isFosterTarget(target) {
		return templateRedirect(insertionPoint{parent: target})
	}

	lastTemplateIdx, lastTableIdx := -1, -1

	for i := len(b.stack) - 1; i > 0; i-- {
		node := b.stack[i]

		if lastTemplateIdx < 0 && isTemplateElement(node) {
			lastTemplateIdx = i
		}

		if lastTableIdx < 0 && node.Namespace == NamespaceHTML && node.Name == "table" {
			lastTableIdx = i
		}

		if lastTemplateIdx >= 0 && lastTableIdx >= 0 {
			break
		}
	}

	// A template opened after the table (or with no table open) owns the
	// foster-parented content.
	if lastTemplateIdx >= 0 && (lastTableIdx < 0 || lastTemplateIdx > lastTableIdx) {
		return insertionPoint{parent: b.stack[lastTemplateIdx], contents: true}
	}

	if lastTableIdx >= 0 {
		table := b.stack[lastTableIdx]
		if table.Parent != nil {
			return templateRedirect(insertionPoint{parent: table.Parent, before: table})
		}

		if lastTableIdx > 0 {
			return templateRedirect(insertionPoint{parent: b.stack[lastTableIdx-1]})
		}
	}

	// No table: the standard inserts into the first element in the stack.
	if len(b.stack) > 1 {
		return templateRedirect(insertionPoint{parent: b.stack[1]})
	}

	return insertionPoint{parent: b.stack[0]}
}

// templateRedirect routes an insertion whose parent is a template element into
// that template's Contents, per the standard's adjusted-insertion-location
// step.
func templateRedirect(point insertionPoint) insertionPoint {
	if isTemplateElement(point.parent) {
		point.contents = true
	}

	return point
}

// isFosterTarget reports whether inserting into n must be rerouted around the
// table: table, tbody, tfoot, thead, and tr are the foster-parenting targets.
func isFosterTarget(n *Node) bool {
	if n.Namespace != NamespaceHTML {
		return false
	}

	switch n.Name {
	case "table", "tbody", "tfoot", "thead", "tr":
		return true
	default:
		return false
	}
}

// insertNode appends an element node at the appropriate place and pushes it
// unless it is void or, in foreign content, self-closing. Elements deeper than
// the depth cap are dropped so recursive walks stay bounded.
func (b *treeBuilder) insertNode(name string, ns Namespace, attrs []string, selfClosing bool) *Node {
	if len(b.stack)-1 >= maxElementDepth {
		return nil // deeper than the cap: drop the element, content flattens up
	}

	point := b.appropriatePlace()

	node := &Node{Type: ElementNode, Name: name, Namespace: ns} //nolint:exhaustruct
	applyAttributes(node, attrs)

	insertChildAt(point, node)
	node.Parent = point.parent

	// The self-closing flag is a parse error and is ignored on ordinary HTML
	// elements; void HTML elements never take content, and foreign elements
	// honor the flag.
	if (ns == NamespaceHTML && isVoidElement(name)) || (ns != NamespaceHTML && selfClosing) {
		return node // no child content
	}

	b.stack = append(b.stack, node)

	return node
}

// insertChildAt places node at point, either before the reference child, at
// the end of the parent's children, or in the parent template's contents.
func insertChildAt(point insertionPoint, node *Node) {
	children := &point.parent.Children
	if point.contents {
		children = &point.parent.Contents
	}

	if point.before != nil {
		for i, child := range *children {
			if child == point.before {
				*children = append(*children, nil)
				copy((*children)[i+1:], (*children)[i:])
				(*children)[i] = node

				return
			}
		}
	}

	*children = append(*children, node)
}

// insertForeignElement inserts an element in ns, applying the namespace's
// element-name adjustment (the standard's SVG tag-name table).
func (b *treeBuilder) insertForeignElement(name string, ns Namespace, tokItem *token) {
	b.insertNode(adjustForeignElementName(ns, name), ns, tokItem.attrs, tokItem.selfClosing)
}

// appendCommentTo attaches a comment node to parent. A comment targeting a
// template element goes into its Contents, like any other insertion.
func (b *treeBuilder) appendCommentTo(parent *Node, data string) {
	node := &Node{Type: CommentNode, Text: data} //nolint:exhaustruct

	if isTemplateElement(parent) {
		parent.Contents = append(parent.Contents, node)

		return
	}

	parent.Children = append(parent.Children, node)
}

// appendTextToken attaches text at the appropriate place, merging into an
// adjacent text node when present. Token data is already decoded by the
// tokenizer; U+0000 characters are dropped in HTML content and replaced with
// U+FFFD in foreign content.
func (b *treeBuilder) appendTextToken(data string) {
	if b.currentIsForeignText() {
		data = replaceNUL(data)
	} else {
		data = strings.ReplaceAll(data, "\x00", "")
	}

	if data == "" {
		return
	}

	point := b.appropriatePlace()

	children := &point.parent.Children
	if point.contents {
		children = &point.parent.Contents
	}

	if point.before != nil {
		for i, child := range *children {
			if child != point.before {
				continue
			}

			if i > 0 {
				if prev := (*children)[i-1]; prev.Type == TextNode {
					b.accumulateText(prev, data)

					return
				}
			}

			b.flushTextAccum()

			node := &Node{Type: TextNode, Text: data, Parent: point.parent} //nolint:exhaustruct
			insertChildAt(point, node)

			return
		}
	}

	top := point.parent

	if len(*children) > 0 {
		last := (*children)[len(*children)-1]
		if last.Type == TextNode {
			b.accumulateText(last, data)

			return
		}
	}

	b.flushTextAccum()

	node := &Node{Type: TextNode, Text: data} //nolint:exhaustruct
	node.Parent = top
	*children = append(*children, node)
}

// accumulateText appends data onto a text node. Repeated appends stay in
// textBytes and are copied onto the node once, in flushTextAccum, so a long
// run of one-character tokens does not copy the whole string each time.
func (b *treeBuilder) accumulateText(node *Node, data string) {
	if b.textAccum != node {
		b.flushTextAccum()
		b.textAccum = node
		b.textBytes = append(b.textBytes[:0], node.Text...)
	}

	b.textBytes = append(b.textBytes, data...)
}

// flushTextAccum writes any pending text bytes onto their text node.
func (b *treeBuilder) flushTextAccum() {
	if b.textAccum == nil {
		return
	}

	b.textAccum.Text = string(b.textBytes)
	b.textAccum = nil
	b.textBytes = b.textBytes[:0]
}

// applyAttributes stores the interleaved name/value pairs on node, keeping
// the first value of a duplicated attribute. Names are already lowercased and
// values already character-reference decoded by the tokenizer; foreign
// elements additionally adjust names per the standard's tables.
func applyAttributes(node *Node, attrs []string) {
	if len(attrs) == 0 {
		return
	}

	if node.Attrs == nil {
		const attrPairSize = 2 // attrs slice interleaves name and value

		node.Attrs = make(map[string]string, len(attrs)/attrPairSize)
	}

	totalBytes := 0

	for i := 0; i+1 < len(attrs); i += 2 {
		if len(node.AttrList) >= maxAttributesPerElement {
			break
		}

		key, attr := adjustAttributeName(node.Namespace, attrs[i], attrs[i+1])

		pairBytes := len(key) + len(attr.Value)
		if totalBytes+pairBytes > maxAttributeBytesPerElement {
			break
		}

		if _, dup := node.Attrs[key]; dup {
			continue
		}

		node.Attrs[key] = attr.Value
		node.AttrList = append(node.AttrList, attr)
		totalBytes += pairBytes
	}
}

// processHeadContent handles a head-content start tag seen after the head
// element was closed: the head is pushed back on the stack, the in-head rules
// run, and the head is removed again.
func (b *treeBuilder) processHeadContent(tokItem *token) {
	pushed := false

	if b.head != nil && !openInStack(b.stack, "head") {
		b.stack = append(b.stack, b.head)
		pushed = true
	}

	b.processInHead(tokItem)

	if pushed {
		b.removeFromStack(b.head)
	}
}

// mergeIntoHTMLElement merges attributes into the existing html element.
func (b *treeBuilder) mergeIntoHTMLElement(attrs []string) {
	if len(b.stack) < 2 {
		return
	}

	html := b.stack[1]
	if html.Namespace == NamespaceHTML && html.Name == "html" {
		applyAttributes(html, attrs)
	}
}

func (b *treeBuilder) removeFromStack(node *Node) {
	for i := len(b.stack) - 1; i > 0; i-- {
		if b.stack[i] == node {
			b.stack = append(b.stack[:i], b.stack[i+1:]...)

			return
		}
	}
}

func (b *treeBuilder) popCurrentIfName(name string) {
	if b.top().Namespace == NamespaceHTML && b.top().Name == name {
		b.stack = b.stack[:len(b.stack)-1]
	}
}

func (b *treeBuilder) firstStackElement() *Node {
	if b.fragment {
		// The synthetic root is the html element in a fragment parse.
		return b.root
	}

	for _, node := range b.stack {
		if node != b.root && node.Type == ElementNode {
			return node
		}
	}

	return b.root
}

// closeHTMLElement applies the in-body "any other end tag" rule: walk the
// stack from the current node; a matching HTML element is popped together
// with everything above it, but a special element that does not match makes
// the end tag a no-op.
func (b *treeBuilder) closeHTMLElement(data string) {
	for i := len(b.stack) - 1; i > 0; i-- {
		node := b.stack[i]
		if node.Namespace == NamespaceHTML && node.Name == data {
			b.generateImpliedEndTags(data)
			b.stack = b.stack[:i]

			return
		}

		if isSpecialElement(node) {
			return
		}
	}
}

// closeForeignElement applies the foreign-content end-tag rules: a matching
// element name anywhere in the foreign run pops through it. It reports
// whether the token was consumed; reaching an HTML element first returns
// false so the token is reprocessed under the HTML insertion mode, whose
// scope rules decide whether the end tag applies.
func (b *treeBuilder) closeForeignElement(data string) bool {
	if data == "script" && b.top().Namespace == NamespaceSVG && strings.EqualFold(b.top().Name, "script") {
		b.stack = b.stack[:len(b.stack)-1]

		return true
	}

	// The topmost element is stack[1] for documents (stack[0] is the
	// synthetic #document root) and stack[0] for fragments (the synthetic
	// html root).
	floor := 1
	if b.fragment {
		floor = 0
	}

	i := len(b.stack) - 1
	for {
		if i <= floor {
			return true // topmost element reached: ignore the stray end tag
		}

		node := b.stack[i]
		if strings.EqualFold(node.Name, data) {
			b.stack = b.stack[:i]

			return true
		}

		i--
		if b.stack[i].Namespace == NamespaceHTML {
			return false // process the token under the HTML rules
		}
	}
}

// --- scope and implied end tags ---

// integrationPointStops is the sentinel key that adds the foreign scope
// boundaries to a stops map: the default-derived scopes (default, button,
// list item) also end at MathML text integration points and HTML integration
// points. Table scope omits it, because only HTML html, table, and template
// elements end a table-scope search.
const integrationPointStops = "#integration-points"

var (
	defaultScopeStops = map[string]bool{
		"applet": true, "caption": true, "html": true, "table": true, "td": true,
		"th": true, "marquee": true, "object": true, "select": true, "template": true,
		integrationPointStops: true,
	}

	buttonScopeStops = map[string]bool{
		"applet": true, "caption": true, "html": true, "table": true, "td": true,
		"th": true, "marquee": true, "object": true, "select": true, "template": true,
		"button":              true,
		integrationPointStops: true,
	}

	listItemScopeStops = map[string]bool{
		"applet": true, "caption": true, "html": true, "table": true, "td": true,
		"th": true, "marquee": true, "object": true, "select": true, "template": true,
		"ol": true, "ul": true,
		integrationPointStops: true,
	}

	impliedEndTags = map[string]bool{
		"dd": true, "dt": true, "li": true, "optgroup": true, "option": true,
		"p": true, "rb": true, "rp": true, "rt": true, "rtc": true,
	}

	headingNames = []string{"h1", "h2", "h3", "h4", "h5", "h6"}

	bodyEndTagsWithScope = map[string]bool{
		"address": true, "article": true, "aside": true, "blockquote": true,
		"button": true, "center": true, "details": true, "dialog": true,
		"dir": true, "div": true, "dl": true, "fieldset": true, "figcaption": true,
		"figure": true, "footer": true, "header": true, "hgroup": true,
		"listing": true, "main": true, "menu": true, "nav": true, "ol": true,
		"pre": true, "search": true, "section": true, "summary": true, "ul": true,
	}

	specialTags = map[string]bool{
		"address": true, "applet": true, "area": true, "article": true, "aside": true,
		"base": true, "basefont": true, "bgsound": true, "blockquote": true, "body": true,
		"br": true, "button": true, "caption": true, "center": true, "col": true,
		"colgroup": true, "dd": true, "details": true, "dir": true, "div": true,
		"dl": true, "dt": true, "embed": true, "fieldset": true, "figcaption": true,
		"figure": true, "footer": true, "form": true, "frame": true, "frameset": true,
		"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
		"head": true, "header": true, "hgroup": true, "hr": true, "html": true,
		"iframe": true, "img": true, "input": true, "keygen": true, "li": true,
		"link": true, "listing": true, "main": true, "marquee": true, "menu": true,
		"meta": true, "nav": true, "noembed": true, "noframes": true, "noscript": true,
		"object": true, "ol": true, "p": true, "param": true, "plaintext": true,
		"pre": true, "script": true, "search": true, "section": true, "select": true,
		"source": true, "style": true, "summary": true, "table": true, "tbody": true,
		"td": true, "template": true, "textarea": true, "tfoot": true, "th": true,
		"thead": true, "title": true, "tr": true, "track": true, "ul": true,
		"wbr": true, "xmp": true,
	}
)

func (b *treeBuilder) findInScope(name string, stops map[string]bool) *Node {
	for i := len(b.stack) - 1; i > 0; i-- {
		node := b.stack[i]
		if node.Namespace == NamespaceHTML && node.Name == name {
			return node
		}

		if isScopeBoundary(node, stops) {
			return nil
		}
	}

	return nil
}

func (b *treeBuilder) hasInScope(name string, stops map[string]bool) bool {
	return b.findInScope(name, stops) != nil
}

func (b *treeBuilder) hasInScopeAny(names []string, stops map[string]bool) bool {
	for i := len(b.stack) - 1; i > 0; i-- {
		node := b.stack[i]
		if node.Namespace == NamespaceHTML {
			for _, name := range names {
				if node.Name == name {
					return true
				}
			}
		}

		if isScopeBoundary(node, stops) {
			return false
		}
	}

	return false
}

func isScopeBoundary(node *Node, stops map[string]bool) bool {
	if node.Namespace == NamespaceHTML {
		return stops[node.Name]
	}

	return stops[integrationPointStops] &&
		(isMathMLTextIntegrationPoint(node) || isHTMLIntegrationPoint(node))
}

func (b *treeBuilder) hasInButtonScope(name string) bool {
	return b.hasInScope(name, buttonScopeStops)
}

func (b *treeBuilder) hasInListScope(name string) bool {
	return b.hasInScope(name, listItemScopeStops)
}

func (b *treeBuilder) generateImpliedEndTags(except string) {
	for len(b.stack) > 1 {
		top := b.top()
		if top.Namespace != NamespaceHTML || !impliedEndTags[top.Name] || top.Name == except {
			return
		}

		b.stack = b.stack[:len(b.stack)-1]
	}
}

func (b *treeBuilder) popUntilName(name string) {
	for len(b.stack) > 1 {
		top := b.top()
		b.stack = b.stack[:len(b.stack)-1]

		if top.Namespace == NamespaceHTML && top.Name == name {
			return
		}
	}
}

func (b *treeBuilder) popUntilAnyName(names ...string) {
	for len(b.stack) > 1 {
		top := b.top()
		b.stack = b.stack[:len(b.stack)-1]

		if top.Namespace != NamespaceHTML {
			continue
		}

		for _, name := range names {
			if top.Name == name {
				return
			}
		}
	}
}

// closePElement closes the p element in button scope: implied end tags except
// p, then pop until p has been popped.
func (b *treeBuilder) closePElement() {
	b.generateImpliedEndTags("p")
	b.popUntilName("p")
}

func (b *treeBuilder) closePElementIfOpen() {
	if !b.hasInButtonScope("p") {
		return
	}

	b.closePElement()
}

// closeOpenListItem implements the standard's list-item loop: a new li closes
// the nearest open li, continuing past address, div, and p elements.
func (b *treeBuilder) closeOpenListItem() {
	for i := len(b.stack) - 1; i > 0; i-- {
		node := b.stack[i]
		if node.Namespace == NamespaceHTML && node.Name == "li" {
			b.generateImpliedEndTags("li")
			b.popUntilName("li")

			return
		}

		if isSpecialElement(node) && !isListContinuationElement(node) {
			return
		}
	}
}

// closeOpenDefinitionItem is closeOpenListItem for dd and dt.
func (b *treeBuilder) closeOpenDefinitionItem() {
	for i := len(b.stack) - 1; i > 0; i-- {
		node := b.stack[i]
		if node.Namespace == NamespaceHTML && (node.Name == "dd" || node.Name == "dt") {
			b.generateImpliedEndTags(node.Name)
			b.popUntilAnyName("dd", "dt")

			return
		}

		if isSpecialElement(node) && !isListContinuationElement(node) {
			return
		}
	}
}

func isSpecialElement(node *Node) bool {
	if node.Namespace == NamespaceHTML {
		return specialTags[node.Name]
	}

	return isMathMLTextIntegrationPoint(node) || isHTMLIntegrationPoint(node)
}

// isListContinuationElement reports whether the li/dd/dt search continues past
// this element: address, div, and p do not stop the list-item loop.
func isListContinuationElement(node *Node) bool {
	if node.Namespace != NamespaceHTML {
		return false
	}

	switch node.Name {
	case "address", "div", "p":
		return true
	default:
		return false
	}
}

// --- small predicates ---

func isAllWhitespace(data string) bool {
	for i := 0; i < len(data); i++ {
		if !isWhitespace(data[i]) {
			return false
		}
	}

	return true
}

// keepWhitespace returns data with every non-whitespace byte removed. The
// frameset insertion modes insert whitespace characters and ignore all other
// characters individually; text arrives coalesced, so the run is filtered.
func keepWhitespace(data string) string {
	if isAllWhitespace(data) {
		return data
	}

	var b strings.Builder

	b.Grow(len(data))

	for i := 0; i < len(data); i++ {
		if isWhitespace(data[i]) {
			b.WriteByte(data[i])
		}
	}

	return b.String()
}

// splitLeadingWhitespace splits data into its leading ASCII whitespace and the
// rest, so modes that ignore or relocate leading whitespace can process the
// remainder correctly.
func splitLeadingWhitespace(data string) (string, string) {
	i := 0
	for i < len(data) && isWhitespace(data[i]) {
		i++
	}

	return data[:i], data[i:]
}

func isHeadingElement(node *Node) bool {
	if node.Namespace != NamespaceHTML {
		return false
	}

	for _, name := range headingNames {
		if node.Name == name {
			return true
		}
	}

	return false
}

// textModeElement reports whether a start tag's content is consumed by the
// tokenizer as raw text, so the tree builder enters the "text" mode until the
// matching end tag.
func textModeElement(name string) bool {
	_, raw := rawTextMode(name)

	return raw
}

// openInStack reports whether an element with name is currently open.
func openInStack(stack []*Node, name string) bool {
	for i := len(stack) - 1; i > 0; i-- {
		if stack[i].Name == name {
			return true
		}
	}

	return false
}

// isVoidElement reports whether name never takes content. basefont and
// bgsound join the true void elements because the in-head and in-body rules
// insert them and immediately pop them.
func isVoidElement(name string) bool {
	switch name {
	case "area", "base", "basefont", "bgsound", "br", "col", "embed", "hr",
		"img", "input", "keygen", "link", "meta", "param", "source", "track",
		"wbr":
		return true
	}

	return false
}
