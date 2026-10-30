// Template and frameset insertion modes: "in template", "in frameset",
// "after frameset", and "after after frameset", plus the template insertion
// mode stack and contents helpers. These states extend the mode skeleton in
// tree.go; the custom Node model and recovery conventions are shared.
//
//nolint:all // template/frameset insertion modes; same conventions as tree.go
package html

func (b *treeBuilder) processInTemplate(tokItem *token) bool {
	switch tokItem.kind {
	case tokText, tokComment, tokDoctype:
		return b.processInBody(tokItem)
	case tokStart:
		switch tokItem.data {
		case "base", "basefont", "bgsound", "link", "meta", "noframes", "script", "style", "template", "title":
			return b.processInHead(tokItem)
		case "caption", "colgroup", "tbody", "tfoot", "thead":
			b.popTemplateMode()
			b.pushTemplateMode(modeInTable)
			b.mode = modeInTable

			return true
		case "col":
			b.popTemplateMode()
			b.pushTemplateMode(modeInColumnGroup)
			b.mode = modeInColumnGroup

			return true
		case "tr":
			b.popTemplateMode()
			b.pushTemplateMode(modeInTableBody)
			b.mode = modeInTableBody

			return true
		case "td", "th":
			b.popTemplateMode()
			b.pushTemplateMode(modeInRow)
			b.mode = modeInRow

			return true
		default:
			b.popTemplateMode()
			b.pushTemplateMode(modeInBody)
			b.mode = modeInBody

			return true
		}
	case tokEnd:
		if tokItem.data == "template" {
			return b.processInHead(tokItem)
		}

		// Any other end tag in a template is ignored.
		return false
	}

	return false
}

// processInFrameset implements the "in frameset" insertion mode.
func (b *treeBuilder) processInFrameset(tokItem *token) bool {
	switch tokItem.kind {
	case tokText:
		if ws := keepWhitespace(tokItem.data); ws != "" {
			b.appendTextToken(ws)
		}

		return false
	case tokComment:
		b.appendCommentTo(b.top(), tokItem.data)

		return false
	case tokDoctype:
		return false
	case tokStart:
		switch tokItem.data {
		case "html":
			return b.processInBody(tokItem)
		case "frameset":
			b.insertHTMLElement(tokItem.data, tokItem.attrs)

			return false
		case "frame":
			b.insertHTMLElement(tokItem.data, tokItem.attrs)

			// A frame element never takes content: pop it immediately.
			if len(b.stack) > 1 {
				b.stack = b.stack[:len(b.stack)-1]
			}

			return false
		case "noframes":
			return b.processInHead(tokItem)
		}
	case tokEnd:
		if tokItem.data == "frameset" {
			if b.top().Namespace == NamespaceHTML && b.top().Name == "html" {
				return false // the root html element: ignore (fragment case)
			}

			b.stack = b.stack[:len(b.stack)-1]

			if b.top().Namespace == NamespaceHTML && b.top().Name == "frameset" {
				return false
			}

			if !b.fragment {
				b.mode = modeAfterFrameset
			}

			return false
		}
	}

	// Anything else is ignored.
	return false
}

// processAfterFrameset implements the "after frameset" insertion mode.
func (b *treeBuilder) processAfterFrameset(tokItem *token) bool {
	switch tokItem.kind {
	case tokText:
		if ws := keepWhitespace(tokItem.data); ws != "" {
			b.appendTextToken(ws)
		}

		return false
	case tokComment:
		b.appendCommentTo(b.top(), tokItem.data)

		return false
	case tokDoctype:
		return false
	case tokStart:
		switch tokItem.data {
		case "html":
			return b.processInBody(tokItem)
		case "noframes":
			return b.processInHead(tokItem)
		}
	case tokEnd:
		if tokItem.data == "html" {
			b.mode = modeAfterAfterFrameset
		}
	}

	return false
}

// processAfterAfterFrameset implements the "after after frameset" insertion
// mode.
func (b *treeBuilder) processAfterAfterFrameset(tokItem *token) bool {
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

		if tokItem.data == "noframes" {
			return b.processInHead(tokItem)
		}
	}

	return false
}

func isTemplateElement(node *Node) bool {
	return node.Namespace == NamespaceHTML && node.Name == "template"
}

// hasOpenHTMLTemplate reports whether an HTML template element is open.
func (b *treeBuilder) hasOpenHTMLTemplate() bool {
	for i := len(b.stack) - 1; i > 0; i-- {
		if isTemplateElement(b.stack[i]) {
			return true
		}
	}

	return false
}

// popUntilHTMLTemplate pops elements until an HTML template element has been
// popped, skipping foreign elements that share the name.
func (b *treeBuilder) popUntilHTMLTemplate() {
	for len(b.stack) > 1 {
		top := b.top()
		b.stack = b.stack[:len(b.stack)-1]

		if isTemplateElement(top) {
			return
		}
	}
}

// pushTemplateMode pushes a template insertion mode.
func (b *treeBuilder) pushTemplateMode(mode insertionMode) {
	b.templateModes = append(b.templateModes, mode)
}

// popTemplateMode pops the current template insertion mode.
func (b *treeBuilder) popTemplateMode() {
	if len(b.templateModes) > 0 {
		b.templateModes = b.templateModes[:len(b.templateModes)-1]
	}
}

// currentTemplateMode returns the current template insertion mode.
func (b *treeBuilder) currentTemplateMode() insertionMode {
	if len(b.templateModes) == 0 {
		return modeInBody
	}

	return b.templateModes[len(b.templateModes)-1]
}

// closeTemplate implements the in-head template end tag: ignore when no
// template is open, otherwise generate all implied end tags thoroughly, pop
// through the last HTML template, clear formatting to the marker, drop the
// template mode, and reset the insertion mode.
func (b *treeBuilder) closeTemplate() {
	if !b.hasOpenHTMLTemplate() {
		return
	}

	b.generateAllImpliedEndTagsThoroughly()
	b.popUntilHTMLTemplate()
	b.clearActiveFormattingToMarker()
	b.popTemplateMode()
	b.resetInsertionMode()
}

// generateAllImpliedEndTagsThoroughly pops the standard's full implied
// end-tag list, including table parts.
func (b *treeBuilder) generateAllImpliedEndTagsThoroughly() {
	for len(b.stack) > 1 {
		top := b.top()
		if top.Namespace != NamespaceHTML || !thoroughImpliedEndTags[top.Name] {
			return
		}

		b.stack = b.stack[:len(b.stack)-1]
	}
}

var thoroughImpliedEndTags = map[string]bool{
	"caption": true, "colgroup": true, "dd": true, "dt": true, "li": true,
	"optgroup": true, "option": true, "p": true, "rb": true, "rp": true,
	"rt": true, "rtc": true, "tbody": true, "td": true, "tfoot": true,
	"th": true, "thead": true, "tr": true,
}
