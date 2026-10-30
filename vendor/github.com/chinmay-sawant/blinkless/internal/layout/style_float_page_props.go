//nolint:mnd,varnamelen // page-float property parsers + place nudge
package layout

import (
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
	"github.com/chinmay-sawant/blinkless/internal/html"
)

// CSS Page Floats apply group (css-page-floats-3 lite):
//   - float-offset: <length-percentage> — length nudge on placeFloat
//   - float-reference: inline | column | region | page
//       inline  = current BFC (CSS2 floats)
//       page    = page content box (x=0, width=viewport)
//       column  = parent BFC when nested inside a multicol ancestor
//       region  = stored; treated as inline (no CSS Regions)
//
// float-defer stays Unsupported (no apply arm, no defer model).

const (
	floatRefInline = "inline"
	floatRefColumn = "column"
	floatRefRegion = "region"
	floatRefPage   = "page"
)

// Cascade storage for the GCPM footnote longhands (CSS Generated Content for
// Paged Media 3, section 2, https://drafts.csswg.org/css-gcpm-3/#footnotes):
// canonical used values in CustomProps, following the ruby precedent in
// ruby.go. Chrome implements no footnote area, so the GCPM draft is the
// oracle. footnote-display stores block, inline or compact (initial block);
// footnote-policy stores auto, line or block (initial auto). An absent or
// invalid declaration leaves no key, which reads as the initial. The style
// store interns the CustomProps map without regenerating style_intern_gen.go.
const (
	footnoteDisplayCustomKey = "__footnote_display"
	footnotePolicyCustomKey  = "__footnote_policy"
)

// applyFloatPageProps owns float-offset, float-reference and the GCPM
// footnote-display and footnote-policy longhands.
func applyFloatPageProps(
	style *ResolvedStyle, prop, value string, fsize float64, ctx *styleContext, parent *ResolvedStyle, _ bool,
) bool {
	switch prop {
	case "float-offset":
		if pt, pct, ok := parseFloatOffset(value, fsize, ctx); ok {
			style.FloatOffset = pt
			style.FloatOffsetPercent = pct
		}
	case "float-reference":
		if ref, ok := parseFloatReference(value); ok {
			style.FloatReference = ref
		}
	case footnotePropDisplay:
		setFootnoteCustom(style, parent, footnoteDisplayCustomKey, value, parseFootnoteDisplay)
	case footnotePropPolicy:
		setFootnoteCustom(style, parent, footnotePolicyCustomKey, value, parseFootnotePolicy)
	default:
		return false
	}

	return true
}

// setFootnoteCustom validates value with parse and stores the canonical used
// value. CSS-wide keywords resolve first: inherit keeps the already-inherited
// map entry, everything else resets to the initial (deletes the key). An
// invalid value drops the declaration to the initial too, so a bogus
// footnote-display never revives an earlier valid one.
func setFootnoteCustom(
	style *ResolvedStyle, parent *ResolvedStyle, key, value string, parse func(string) (string, bool),
) {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		applyFootnoteWideKeyword(style, parent, key, strings.ToLower(trimmed))

		return
	}

	used, ok := parse(value)
	if !ok {
		if style.CustomProps != nil {
			delete(style.CustomProps, key)
		}

		return
	}

	ensureFootnoteMap(style)
	style.CustomProps[key] = used
}

// applyFootnoteWideKeyword resolves a CSS-wide keyword for a footnote key.
// Inherit keeps the parent entry that mergeCustomProps already folded in
// (deleting only when the parent carries nothing, which is the initial
// anyway). Initial, unset, revert, and revert-layer all reset to the initial.
func applyFootnoteWideKeyword(style, parent *ResolvedStyle, key, keyword string) {
	if keyword == inheritKeyword {
		if parent == nil || parent.CustomProps[key] == "" {
			if style.CustomProps != nil {
				delete(style.CustomProps, key)
			}
		}

		return
	}

	if style.CustomProps != nil {
		delete(style.CustomProps, key)
	}
}

// ensureFootnoteMap allocates the CustomProps map for one footnote write.
func ensureFootnoteMap(style *ResolvedStyle) {
	if style.CustomProps == nil {
		style.CustomProps = make(map[string]string)
	}
}

func parseFloatOffset(raw string, fsize float64, ctx *styleContext) (float64, float64, bool) {
	value := normalizeCSSValue(raw)
	if value == "" {
		return 0, -1, false
	}

	val, unit, ok := css.ParseLength(value)
	if !ok {
		return 0, -1, false
	}

	if unit == "%" {
		_ = ctx

		return 0, val, true
	}

	pt, converted := lengthToPt(val, unit, fsize)
	if !converted {
		return 0, -1, false
	}

	return pt, -1, true
}

func parseFloatReference(raw string) (string, bool) {
	switch normalizeCSSValue(raw) {
	case floatRefInline, floatRefColumn, floatRefRegion, floatRefPage:
		return normalizeCSSValue(raw), true
	default:
		return "", false
	}
}

// nudgeFloatOffset shifts a just-built float box by float-offset along the
// block axis (positive moves down). Percent resolves against the float's own
// border-box height (lite). Extracted so placeFloat stays a thin caller.
func nudgeFloatOffset(e *engine, fbox *box, sty ResolvedStyle) {
	if fbox == nil {
		return
	}

	var dy float64

	switch {
	case sty.FloatOffsetPercent >= 0:
		dy = fbox.height * sty.FloatOffsetPercent / 100
	case sty.FloatOffset != 0:
		dy = e.scalePt(sty.FloatOffset)
	default:
		return
	}

	if dy == 0 {
		return
	}

	fbox.y += dy
	e.shiftBoxOps(fbox, 0, dy)
}

// floatReferenceBox returns the containing block used to place a float.
// inline / region: current BFC. page: page content box. column: parent BFC
// when the float sits in a nested BFC inside a multicol ancestor.
func (e *engine) floatReferenceBox(
	node *html.Node, sty ResolvedStyle, contentX, contentW, flowY float64,
) (float64, float64, float64) {
	switch sty.FloatReference {
	case floatRefPage:
		refW := 0.0
		if e != nil {
			refW = e.opts.Width
		}

		if refW <= 0 {
			refW = contentW
		}

		return 0, refW, flowY
	case floatRefColumn:
		if refX, refW, ok := e.columnReferenceBox(node, contentX, contentW); ok {
			return refX, refW, flowY
		}
	}

	return contentX, contentW, flowY
}

// columnReferenceBox reports the parent BFC (the column box) when the float
// is nested inside a multicol ancestor. Direct-in-column floats already use
// the column BFC, so this is a no-op unless a nested formatting context
// pushed a tighter box.
func (e *engine) columnReferenceBox(
	node *html.Node, contentX, contentW float64,
) (float64, float64, bool) {
	if e == nil || !hasMulticolAncestor(e, node) {
		return 0, 0, false
	}

	if n := len(e.bfcStack); n > 0 {
		parent := e.bfcStack[n-1]
		if parent != nil && parent.contentW > 0 &&
			(parent.contentX != contentX || parent.contentW != contentW) {
			return parent.contentX, parent.contentW, true
		}
	}

	return 0, 0, false
}

func hasMulticolAncestor(e *engine, n *html.Node) bool {
	if e == nil || n == nil {
		return false
	}

	for p := n.Parent; p != nil; p = p.Parent {
		st := e.stylePtr(p)
		if st == nil {
			continue
		}

		if st.ColumnCount > 1 || st.ColumnWidth >= 0 || st.ColumnHeight >= 0 {
			return true
		}
	}

	return false
}

// pinFloatToReference reports whether placeFloat should pack against the
// resolved reference box instead of the current BFC edges.
func pinFloatToReference(sty ResolvedStyle, refX, refW, contentX, contentW float64) bool {
	switch sty.FloatReference {
	case floatRefPage:
		return true
	case floatRefColumn:
		return refX != contentX || refW != contentW
	default:
		return false
	}
}
