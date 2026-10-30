// Table insertion modes: "in table", "in caption", "in column group", "in
// table body", "in row", and "in cell", plus foster parenting. These states
// implement the standard's table tree construction on the HTML-02a mode
// skeleton in tree.go; the custom Node model and recovery conventions are
// shared.
//
//nolint:all // table insertion modes; same custom Node model and conventions as tree.go
package html

import "strings"

// tableScopeStops is the standard's table scope: html, table, and template.
var tableScopeStops = map[string]bool{"html": true, "table": true, "template": true}

// --- in table ---

func (b *treeBuilder) processInTable(tokItem *token) bool {
	switch tokItem.kind {
	case tokComment:
		b.appendCommentTo(b.top(), tokItem.data)

		return false
	case tokDoctype:
		return false
	case tokText:
		if b.currentIsTableSection() {
			b.pendingTableText = append(b.pendingTableText, tokItem.data)

			return false
		}
	case tokStart:
		if handled, reprocess := b.processInTableStart(tokItem); handled {
			return reprocess
		}
	case tokEnd:
		switch tokItem.data {
		case "table":
			if !b.hasInScope("table", tableScopeStops) {
				return false
			}

			b.popUntilName("table")
			b.resetInsertionMode()

			return false
		case "body", "caption", "col", "colgroup", "html", "tbody", "td", "tfoot", "th", "thead", "tr":
			return false
		case "template":
			b.processInHead(tokItem)

			return false
		}
	}

	return b.fosterInBody(tokItem)
}

// processInTableStart handles the table-structure start tags. handled reports
// whether the token was consumed; reprocess reports a mode switch that needs
// the token replayed.
func (b *treeBuilder) processInTableStart(tokItem *token) (handled, reprocess bool) {
	switch tokItem.data {
	case "caption":
		b.clearStackToTableContext()
		b.insertHTMLElement("caption", tokItem.attrs)
		b.insertMarker()
		b.mode = modeInCaption
	case "colgroup":
		b.clearStackToTableContext()
		b.insertHTMLElement("colgroup", tokItem.attrs)
		b.mode = modeInColumnGroup
	case "col":
		b.clearStackToTableContext()
		b.insertHTMLElement("colgroup", nil)
		b.mode = modeInColumnGroup

		return true, true
	case "tbody", "tfoot", "thead":
		b.clearStackToTableContext()
		b.insertHTMLElement(tokItem.data, tokItem.attrs)
		b.mode = modeInTableBody
	case "td", "th", "tr":
		b.clearStackToTableContext()
		b.insertHTMLElement("tbody", nil)
		b.mode = modeInTableBody

		return true, true
	case "table":
		if !b.hasInScope("table", tableScopeStops) {
			return true, false
		}

		b.popUntilName("table")
		b.resetInsertionMode()

		return true, true
	case "style", "script", "template":
		b.processInHead(tokItem)
	case "input":
		if !inputIsHidden(tokItem) {
			return false, false
		}

		b.insertHTMLElement("input", tokItem.attrs)
	case "form":
		if b.form == nil {
			b.insertHTMLElement("form", tokItem.attrs)
			b.form = b.top()
			b.stack = b.stack[:len(b.stack)-1]
		}
	default:
		return false, false
	}

	return true, false
}

// fosterInBody processes a token with the in-body rules while foster
// parenting is enabled, the standard's "anything else" in table mode.
func (b *treeBuilder) fosterInBody(tokItem *token) bool {
	b.fosterParenting = true
	reprocess := b.processInBody(tokItem)
	b.fosterParenting = false

	return reprocess
}

// flushPendingTableText resolves accumulated table text: pure whitespace is
// inserted in place, text mixed with non-whitespace is foster-parented around
// the table.
func (b *treeBuilder) flushPendingTableText() {
	if len(b.pendingTableText) == 0 {
		return
	}

	pending := b.pendingTableText
	b.pendingTableText = nil

	allWhitespace := true

	for _, text := range pending {
		if !isAllWhitespace(text) {
			allWhitespace = false

			break
		}
	}

	for _, text := range pending {
		if allWhitespace {
			b.appendTextToken(text)

			continue
		}

		// Reprocess under the in-body rules with foster parenting enabled,
		// as the in-table "anything else" entry requires: this also
		// reconstructs active formatting elements.
		b.fosterParenting = true
		b.processInBody(&token{kind: tokText, data: text})
		b.fosterParenting = false
	}
}

// currentIsTableSection reports whether the current node is one of the
// elements whose character tokens are collected by the in-table-text rules.
func (b *treeBuilder) currentIsTableSection() bool {
	if b.top().Namespace != NamespaceHTML {
		return false
	}

	switch b.top().Name {
	case "table", "tbody", "template", "tfoot", "thead", "tr":
		return true
	default:
		return false
	}
}

// inputIsHidden reports whether an input token carries type=hidden.
func inputIsHidden(tokItem *token) bool {
	for i := 0; i+1 < len(tokItem.attrs); i += 2 {
		if tokItem.attrs[i] == "type" && strings.EqualFold(tokItem.attrs[i+1], "hidden") {
			return true
		}
	}

	return false
}

// --- in caption ---

func (b *treeBuilder) processInCaption(tokItem *token) bool {
	switch tokItem.kind {
	case tokEnd:
		switch tokItem.data {
		case "caption":
			if !b.hasInScope("caption", tableScopeStops) {
				return false
			}

			b.closeCaption()

			return false
		case "table":
			if !b.hasInScope("caption", tableScopeStops) {
				return false
			}

			b.closeCaption()

			return true
		case "body", "col", "colgroup", "html", "tbody", "td", "tfoot", "th", "thead", "tr":
			return false
		}
	case tokStart:
		switch tokItem.data {
		case "caption", "col", "colgroup", "tbody", "td", "tfoot", "th", "thead", "tr":
			if !b.hasInScope("caption", tableScopeStops) {
				return false
			}

			b.closeCaption()

			return true
		}
	}

	return b.processInBody(tokItem)
}

// closeCaption generates implied end tags, pops through the open caption, and
// clears active formatting entries added since the caption's marker.
func (b *treeBuilder) closeCaption() {
	b.generateImpliedEndTags("")
	b.popUntilName("caption")
	b.clearActiveFormattingToMarker()
	b.mode = modeInTable
}

// --- in column group ---

func (b *treeBuilder) processInColumnGroup(tokItem *token) bool {
	switch tokItem.kind {
	case tokComment:
		b.appendCommentTo(b.top(), tokItem.data)

		return false
	case tokDoctype:
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
		case "col":
			b.insertHTMLElement("col", tokItem.attrs)

			return false
		case "template":
			b.processInHead(tokItem)

			return false
		}
	case tokEnd:
		switch tokItem.data {
		case "colgroup":
			if b.top().Namespace == NamespaceHTML && b.top().Name == "colgroup" {
				b.stack = b.stack[:len(b.stack)-1]
				b.mode = modeInTable
			}

			return false
		case "col":
			return false
		case "template":
			b.processInHead(tokItem)

			return false
		}
	}

	// Anything else: leave the column group if one is open, then reprocess
	// under the table rules.
	if b.top().Namespace == NamespaceHTML && b.top().Name == "colgroup" {
		b.stack = b.stack[:len(b.stack)-1]
		b.mode = modeInTable

		return true
	}

	return false
}

// --- in table body ---

func (b *treeBuilder) processInTableBody(tokItem *token) bool {
	switch tokItem.kind {
	case tokStart:
		switch tokItem.data {
		case "tr":
			b.clearStackToTableBodyContext()
			b.insertHTMLElement("tr", tokItem.attrs)
			b.mode = modeInRow

			return false
		case "th", "td":
			b.clearStackToTableBodyContext()
			b.insertHTMLElement("tr", nil)
			b.mode = modeInRow

			return true
		case "caption", "col", "colgroup", "tbody", "tfoot", "thead":
			if !b.hasTableBodyInScope() {
				return false
			}

			b.clearStackToTableBodyContext()
			b.stack = b.stack[:len(b.stack)-1]
			b.mode = modeInTable

			return true
		}
	case tokEnd:
		switch tokItem.data {
		case "tbody", "tfoot", "thead":
			if !b.hasInScope(tokItem.data, tableScopeStops) {
				return false
			}

			b.clearStackToTableBodyContext()
			b.stack = b.stack[:len(b.stack)-1]
			b.mode = modeInTable

			return false
		case "table":
			if !b.hasTableBodyInScope() {
				return false
			}

			b.clearStackToTableBodyContext()
			b.stack = b.stack[:len(b.stack)-1]
			b.mode = modeInTable

			return true
		case "body", "caption", "col", "colgroup", "html", "td", "th", "tr":
			return false
		}
	}

	return b.processInTable(tokItem)
}

// --- in row ---

func (b *treeBuilder) processInRow(tokItem *token) bool {
	switch tokItem.kind {
	case tokStart:
		switch tokItem.data {
		case "th", "td":
			b.clearStackToTableRowContext()
			b.insertHTMLElement(tokItem.data, tokItem.attrs)
			b.mode = modeInCell
			b.insertMarker()

			return false
		case "caption", "col", "colgroup", "tbody", "tfoot", "thead", "tr":
			if !b.hasInScope("tr", tableScopeStops) {
				return false
			}

			b.clearStackToTableRowContext()
			b.stack = b.stack[:len(b.stack)-1]
			b.mode = modeInTableBody

			return true
		}
	case tokEnd:
		switch tokItem.data {
		case "tr":
			if !b.hasInScope("tr", tableScopeStops) {
				return false
			}

			b.clearStackToTableRowContext()
			b.stack = b.stack[:len(b.stack)-1]
			b.mode = modeInTableBody

			return false
		case "table":
			if !b.hasInScope("tr", tableScopeStops) {
				return false
			}

			b.clearStackToTableRowContext()
			b.stack = b.stack[:len(b.stack)-1]
			b.mode = modeInTableBody

			return true
		case "tbody", "tfoot", "thead":
			if !b.hasInScope(tokItem.data, tableScopeStops) {
				return false
			}

			if !b.hasInScope("tr", tableScopeStops) {
				return false
			}

			b.clearStackToTableRowContext()
			b.stack = b.stack[:len(b.stack)-1]
			b.mode = modeInTableBody

			return true
		case "body", "caption", "col", "colgroup", "html", "td", "th":
			return false
		}
	}

	return b.processInTable(tokItem)
}

// --- in cell ---

func (b *treeBuilder) processInCell(tokItem *token) bool {
	switch tokItem.kind {
	case tokEnd:
		switch tokItem.data {
		case "td", "th":
			if !b.hasInScope(tokItem.data, tableScopeStops) {
				return false
			}

			b.closeCell()

			return false
		case "body", "caption", "col", "colgroup", "html":
			return false
		case "table", "tbody", "tfoot", "thead", "tr":
			if !b.hasInScope(tokItem.data, tableScopeStops) {
				return false
			}

			b.closeCell()

			return true
		}
	case tokStart:
		switch tokItem.data {
		case "caption", "col", "colgroup", "tbody", "td", "tfoot", "th", "thead", "tr":
			if !b.hasCellInTableScope() {
				return false
			}

			b.closeCell()

			return true
		}
	}

	return b.processInBody(tokItem)
}

// closeCell generates implied end tags, pops through the open cell, clears
// active formatting entries added since the cell's marker, and returns the
// insertion mode to "in row" (the standard's close-the-cell step).
func (b *treeBuilder) closeCell() {
	b.generateImpliedEndTags("")

	for len(b.stack) > 1 {
		top := b.top()
		b.stack = b.stack[:len(b.stack)-1]

		if top.Namespace == NamespaceHTML && (top.Name == "td" || top.Name == "th") {
			break
		}
	}

	b.clearActiveFormattingToMarker()
	b.mode = modeInRow
}

// --- stack and scope helpers ---

// clearStackToTableContext pops until the current node is a table, template,
// or html element.
func (b *treeBuilder) clearStackToTableContext() {
	for len(b.stack) > 1 {
		top := b.top()
		if top.Namespace == NamespaceHTML &&
			(top.Name == "table" || top.Name == "template" || top.Name == "html") {
			return
		}

		b.stack = b.stack[:len(b.stack)-1]
	}
}

// clearStackToTableBodyContext pops until the current node is a tbody, tfoot,
// thead, template, or html element.
func (b *treeBuilder) clearStackToTableBodyContext() {
	for len(b.stack) > 1 {
		top := b.top()
		if top.Namespace == NamespaceHTML &&
			(top.Name == "tbody" || top.Name == "tfoot" || top.Name == "thead" ||
				top.Name == "template" || top.Name == "html") {
			return
		}

		b.stack = b.stack[:len(b.stack)-1]
	}
}

// clearStackToTableRowContext pops until the current node is a tr, template,
// or html element.
func (b *treeBuilder) clearStackToTableRowContext() {
	for len(b.stack) > 1 {
		top := b.top()
		if top.Namespace == NamespaceHTML &&
			(top.Name == "tr" || top.Name == "template" || top.Name == "html") {
			return
		}

		b.stack = b.stack[:len(b.stack)-1]
	}
}

// hasTableBodyInScope reports whether a tbody, tfoot, or thead is in table
// scope.
func (b *treeBuilder) hasTableBodyInScope() bool {
	for _, name := range []string{"tbody", "tfoot", "thead"} {
		if b.hasInScope(name, tableScopeStops) {
			return true
		}
	}

	return false
}

// hasCellInTableScope reports whether a td or th is in table scope.
func (b *treeBuilder) hasCellInTableScope() bool {
	return b.hasInScope("td", tableScopeStops) || b.hasInScope("th", tableScopeStops)
}

// resetInsertionMode implements the standard's "reset the insertion mode
// appropriately", including the template and frameset branches. A fragment
// parse substitutes its context element at the bottom of the stack.
func (b *treeBuilder) resetInsertionMode() {
	last := false
	first := 1 // stack[0] is the synthetic #document root for documents

	if b.fragment {
		first = 0 // stack[0] is the synthetic html root for fragments
	}

	for i := len(b.stack) - 1; i >= first; i-- {
		node := b.stack[i]
		if i == first {
			last = true

			if b.fragmentContext != nil {
				node = b.fragmentContext
			}
		}

		if node.Namespace == NamespaceHTML {
			switch {
			case (node.Name == "td" || node.Name == "th") && !last:
				b.mode = modeInCell

				return
			case node.Name == "tr":
				b.mode = modeInRow

				return
			case node.Name == "tbody" || node.Name == "thead" || node.Name == "tfoot":
				b.mode = modeInTableBody

				return
			case node.Name == "caption":
				b.mode = modeInCaption

				return
			case node.Name == "colgroup":
				b.mode = modeInColumnGroup

				return
			case node.Name == "table":
				b.mode = modeInTable

				return
			case node.Name == "template":
				b.mode = b.currentTemplateMode()

				return
			case node.Name == "head" && !last:
				b.mode = modeInHead

				return
			case node.Name == "body":
				b.mode = modeInBody

				return
			case node.Name == "frameset":
				b.mode = modeInFrameset

				return
			case node.Name == "html":
				if b.head == nil {
					b.mode = modeBeforeHead
				} else {
					b.mode = modeAfterHead
				}

				return
			}
		}

		if last {
			b.mode = modeInBody

			return
		}
	}

	b.mode = modeInBody
}
