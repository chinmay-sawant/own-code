//nolint:varnamelen,cyclop,wsl,funlen,mnd,goconst // anchor grammar
package layout

import (
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
	"github.com/chinmay-sawant/blinkless/internal/html"
)

// Anchor positioning, one-canvas subset (css-anchor-position-1).
//
// Pinned upstream inventory, webref-ed-css revision
// 1f2ec8f74a80c14066b4c7d6822cee59f69fa03b, spec css-anchor-position:
//   anchor-name: none | <anchor-name>#
//   anchor-scope: none | all | <anchor-name>#
//   position-anchor: normal | none | auto | <anchor-name> | match-parent
//   position-area: none | <position-area>
//   position-try-fallbacks: none | [ [<dashed-ident> || <try-tactic>] | <position-area> ]#
//   position-try-order: normal | <try-size>
//   position-try: <'position-try-order'>? <'position-try-fallbacks'>
//   position-visibility: always | [ anchor-valid || anchor-visible || no-overflow ]
//   <anchor-name> = <dashed-ident>
//   <try-tactic> = flip-block || flip-inline || flip-start || flip-x || flip-y
//   <try-size> = most-width | most-height | most-block-size | most-inline-size
//
// Placement puts an absolute or fixed box against an in-scope anchor box and
// moves its paint ops with it. Static boxes never move. The output stays
// Result.Ops; the viewport comes from Options.Width and Options.Height.
// The pass runs once in finalizeResult before finalizeChrome so anchors laid
// out anywhere in the document are visible and shifted boxes carry their
// backgrounds and borders. Maps are built locally per pass; no engine fields.
//
// Non-goal, scrollport: this tree has no scroll offset, so
// position-visibility anchor-visible is stored and treated exactly like
// anchor-valid on this canvas until a scroll offset exists.

const (
	anchorNameProp         = "anchor-name"
	anchorScopeProp        = "anchor-scope"
	positionAnchorProp     = "position-anchor"
	positionAreaProp       = "position-area"
	positionTryProp        = "position-try"
	positionTryFallbacks   = "position-try-fallbacks"
	positionTryOrderProp   = "position-try-order"
	positionVisibilityProp = "position-visibility"
)

// Try tactics from css-anchor-position-1 section 6.1.
var anchorTryTactics = map[string]struct{}{ //nolint:gochecknoglobals // static keyword set
	"flip-block":  {},
	"flip-inline": {},
	"flip-start":  {},
	"flip-x":      {},
	"flip-y":      {},
}

// Try sizes from css-anchor-position-1 section 6.2.
var anchorTrySizes = map[string]struct{}{ //nolint:gochecknoglobals // static keyword set
	"most-width":       {},
	"most-height":      {},
	"most-block-size":  {},
	"most-inline-size": {},
}

// Position-area keywords from css-anchor-position-1 section 3.1.2. Storage
// accepts one or two tokens from this set. Placement moves the common
// single-token areas and two-token combinations built from them; other valid
// areas are stored but keep absolute offsets.
var anchorPositionAreaTokens = map[string]struct{}{ //nolint:gochecknoglobals // static keyword set
	"left": {}, "center": {}, "right": {}, "span-left": {}, "span-right": {},
	"x-start": {}, "x-end": {}, "span-x-start": {}, "span-x-end": {},
	"self-x-start": {}, "self-x-end": {}, "span-self-x-start": {}, "span-self-x-end": {},
	"top": {}, "bottom": {}, "span-top": {}, "span-bottom": {},
	"y-start": {}, "y-end": {}, "span-y-start": {}, "span-y-end": {},
	"self-y-start": {}, "self-y-end": {}, "span-self-y-start": {}, "span-self-y-end": {},
	"block-start": {}, "block-end": {}, "span-block-start": {}, "span-block-end": {},
	"inline-start": {}, "inline-end": {}, "span-inline-start": {}, "span-inline-end": {},
	"self-block-start": {}, "self-block-end": {}, "span-self-block-start": {}, "span-self-block-end": {},
	"self-inline-start": {}, "self-inline-end": {}, "span-self-inline-start": {}, "span-self-inline-end": {},
	"start": {}, "end": {}, "span-start": {}, "span-end": {},
	"self-start": {}, "self-end": {}, "span-self-start": {}, "span-self-end": {},
	"span-all": {},
}

// isAnchorDashedIdent reports whether s is a dashed-ident (--name).
func isAnchorDashedIdent(s string) bool {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "--") || len(s) < 3 {
		return false
	}

	return css.IsIdentToken(s)
}

// isAnchorTryTactic reports whether s is a pinned try-tactic keyword.
func isAnchorTryTactic(s string) bool {
	_, ok := anchorTryTactics[strings.ToLower(strings.TrimSpace(s))]

	return ok
}

// isAnchorTrySize reports whether s is a pinned try-size keyword.
func isAnchorTrySize(s string) bool {
	_, ok := anchorTrySizes[strings.ToLower(strings.TrimSpace(s))]

	return ok
}

// parseAnchorPositionArea validates a position-area value (none excluded).
// It accepts one or two tokens from the pinned keyword set.
func parseAnchorPositionArea(value string) (string, bool) {
	norm := normalizeCSSValue(value)
	if norm == "" {
		return "", false
	}

	fields := strings.Fields(norm)
	if len(fields) < 1 || len(fields) > 2 {
		return "", false
	}

	for _, tok := range fields {
		if _, ok := anchorPositionAreaTokens[tok]; !ok {
			return "", false
		}
	}

	return norm, true
}

// parseAnchorFallbackEntry validates one comma-separated fallback entry:
// either a position-area value or a dashed-ident with optional try-tactics,
// or try-tactics alone (which flip the base area at use time).
func parseAnchorFallbackEntry(entry string) (string, bool) {
	norm := normalizeCSSValue(entry)
	if norm == "" {
		return "", false
	}

	if _, ok := parseAnchorPositionArea(norm); ok {
		return norm, true
	}

	var ident string

	seenTactic := make(map[string]struct{})

	for _, tok := range strings.Fields(norm) {
		switch {
		case isAnchorDashedIdent(tok):
			if ident != "" {
				return "", false
			}

			ident = tok
		case isAnchorTryTactic(tok):
			low := strings.ToLower(tok)
			if _, dup := seenTactic[low]; dup {
				return "", false
			}

			seenTactic[low] = struct{}{}
		default:
			return "", false
		}
	}

	if ident == "" && len(seenTactic) == 0 {
		return "", false
	}

	return norm, true
}

// splitAnchorCommaList splits a comma-separated list, trimming each part.
func splitAnchorCommaList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}

		out = append(out, trimmed)
	}

	return out
}

// parseAnchorNameList validates an anchor-name value. None clears the list.
// An invalid entry fails so the cascade keeps the previous value.
func parseAnchorNameList(value string) ([]string, bool) {
	if strings.EqualFold(strings.TrimSpace(value), "none") {
		return nil, true
	}

	entries := splitAnchorCommaList(value)
	if len(entries) == 0 {
		return nil, false
	}

	out := make([]string, 0, len(entries))

	for _, entry := range entries {
		fields := strings.Fields(entry)
		if len(fields) != 1 || !isAnchorDashedIdent(fields[0]) {
			return nil, false
		}

		out = append(out, strings.TrimSpace(fields[0]))
	}

	return out, true
}

// parseAnchorScope validates an anchor-scope value: none, all, or a name
// list. Keywords compare case-insensitively; names keep their case.
func parseAnchorScope(value string) (string, bool) {
	norm := normalizeCSSValue(value)
	if norm == "none" || norm == "all" {
		return norm, true
	}

	entries := splitAnchorCommaList(value)
	if len(entries) == 0 {
		return "", false
	}

	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		fields := strings.Fields(entry)
		if len(fields) != 1 || !isAnchorDashedIdent(fields[0]) {
			return "", false
		}

		names = append(names, strings.TrimSpace(fields[0]))
	}

	return strings.Join(names, " "), true
}

// anchorScopeList splits a stored scope value into names. Storage joins with
// spaces but raw values may use commas, so both separators are accepted.
func anchorScopeList(scope string) []string {
	var out []string

	for _, part := range strings.Split(scope, ",") {
		out = append(out, strings.Fields(part)...)
	}

	return out
}

// parseAnchorPositionAnchor validates a position-anchor value: auto or one
// dashed-ident. Anything else (including normal, none, and match-parent)
// fails so the cascade keeps the previous value.
func parseAnchorPositionAnchor(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, "auto") {
		return "auto", true
	}

	if isAnchorDashedIdent(trimmed) {
		return trimmed, true
	}

	return "", false
}

// parseAnchorTryFallbacks validates a position-try-fallbacks value. None
// clears the list. An invalid entry fails so the cascade keeps the previous
// value.
func parseAnchorTryFallbacks(value string) ([]string, bool) {
	norm := strings.TrimSpace(value)
	if strings.EqualFold(norm, "none") {
		return nil, true
	}

	entries := splitAnchorCommaList(norm)
	if len(entries) == 0 {
		return nil, false
	}

	out := make([]string, 0, len(entries))

	for _, entry := range entries {
		parsed, ok := parseAnchorFallbackEntry(entry)
		if !ok {
			return nil, false
		}

		out = append(out, parsed)
	}

	return out, true
}

// parseAnchorTryOrder validates a position-try-order value.
func parseAnchorTryOrder(value string) (string, bool) {
	norm := normalizeCSSValue(value)
	if norm == "normal" || isAnchorTrySize(norm) {
		return norm, true
	}

	return "", false
}

// parseAnchorVisibility validates a position-visibility value: always alone
// or one to three of anchor-valid, anchor-visible, no-overflow.
func parseAnchorVisibility(value string) (string, bool) {
	norm := normalizeCSSValue(value)
	if norm == "always" {
		return norm, true
	}

	fields := strings.Fields(norm)
	if len(fields) < 1 || len(fields) > 3 {
		return "", false
	}

	seen := make(map[string]struct{}, len(fields))

	for _, tok := range fields {
		switch tok {
		case "anchor-valid", "anchor-visible", "no-overflow":
		default:
			return "", false
		}

		if _, dup := seen[tok]; dup {
			return "", false
		}

		seen[tok] = struct{}{}
	}

	return norm, true
}

// applyAnchorWideKeywordString resolves a CSS-wide keyword for anchor
// longhands. Inherit copies the parent used value; initial, unset, revert,
// and revert-layer reset to the property initial (zero value).
func applyAnchorWideKeywordString(parentVal, keyword string, hasParent bool) string {
	if keyword == inheritKeyword {
		if hasParent {
			return parentVal
		}

		return ""
	}

	return ""
}

// applyAnchorProps owns the eight anchor longhands plus the position-try
// shorthand fallback (cascade expansion normally removes the shorthand
// before this runs; the direct call keeps var() and @supports probes
// honest). Invalid values keep the previous used value: the cascade gate
// drops them before this runs, and a parse failure here (var() indirection)
// also keeps the old value.
func applyAnchorProps(
	style *ResolvedStyle, prop, value string, _ float64, _ *styleContext,
	parent *ResolvedStyle, hasParent bool,
) bool {
	switch prop {
	case anchorNameProp:
		return applyAnchorNameProp(style, value, parent, hasParent)
	case anchorScopeProp:
		return applyAnchorScopeProp(style, value, parent, hasParent)
	case positionAnchorProp:
		return applyPositionAnchorProp(style, value, parent, hasParent)
	case positionAreaProp:
		return applyPositionAreaProp(style, value, parent, hasParent)
	case positionTryFallbacks:
		return applyPositionTryFallbacksProp(style, value, parent, hasParent)
	case positionTryOrderProp:
		return applyPositionTryOrderProp(style, value, parent, hasParent)
	case positionVisibilityProp:
		return applyPositionVisibilityProp(style, value, parent, hasParent)
	case positionTryProp:
		return applyPositionTryShorthandProp(style, value, parent, hasParent)
	default:
		return false
	}
}

func applyAnchorNameProp(style *ResolvedStyle, value string, parent *ResolvedStyle, hasParent bool) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		kw := strings.ToLower(trimmed)
		if kw == inheritKeyword && hasParent && parent != nil {
			style.AnchorNames = append([]string(nil), parent.AnchorNames...)
		} else {
			style.AnchorNames = nil
		}

		return true
	}

	names, ok := parseAnchorNameList(value)
	if !ok {
		return true
	}

	style.AnchorNames = names

	return true
}

func applyAnchorScopeProp(style *ResolvedStyle, value string, parent *ResolvedStyle, hasParent bool) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		kw := strings.ToLower(trimmed)
		if kw == inheritKeyword && hasParent && parent != nil {
			style.AnchorScope = parent.AnchorScope
		} else {
			style.AnchorScope = ""
		}

		return true
	}

	scope, ok := parseAnchorScope(value)
	if !ok {
		return true
	}

	style.AnchorScope = scope

	return true
}

func applyPositionAnchorProp(style *ResolvedStyle, value string, parent *ResolvedStyle, hasParent bool) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		kw := strings.ToLower(trimmed)
		var parentVal string

		if hasParent && parent != nil {
			parentVal = parent.PositionAnchor
		}

		style.PositionAnchor = applyAnchorWideKeywordString(parentVal, kw, hasParent)

		return true
	}

	anchor, ok := parseAnchorPositionAnchor(value)
	if !ok {
		return true
	}

	style.PositionAnchor = anchor

	return true
}

func applyPositionAreaProp(style *ResolvedStyle, value string, parent *ResolvedStyle, hasParent bool) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		kw := strings.ToLower(trimmed)
		var parentVal string

		if hasParent && parent != nil {
			parentVal = parent.PositionArea
		}

		style.PositionArea = applyAnchorWideKeywordString(parentVal, kw, hasParent)

		return true
	}

	if strings.EqualFold(trimmed, "none") {
		style.PositionArea = "none"

		return true
	}

	area, ok := parseAnchorPositionArea(value)
	if !ok {
		return true
	}

	style.PositionArea = area

	return true
}

func applyPositionTryFallbacksProp(style *ResolvedStyle, value string, parent *ResolvedStyle, hasParent bool) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		kw := strings.ToLower(trimmed)
		if kw == inheritKeyword && hasParent && parent != nil {
			style.PositionTryFallbacks = append([]string(nil), parent.PositionTryFallbacks...)
		} else {
			style.PositionTryFallbacks = nil
		}

		return true
	}

	fallbacks, ok := parseAnchorTryFallbacks(value)
	if !ok {
		return true
	}

	style.PositionTryFallbacks = fallbacks

	return true
}

func applyPositionTryOrderProp(style *ResolvedStyle, value string, parent *ResolvedStyle, hasParent bool) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		kw := strings.ToLower(trimmed)
		var parentVal string

		if hasParent && parent != nil {
			parentVal = parent.PositionTryOrder
		}

		style.PositionTryOrder = applyAnchorWideKeywordString(parentVal, kw, hasParent)

		return true
	}

	order, ok := parseAnchorTryOrder(value)
	if !ok {
		return true
	}

	style.PositionTryOrder = order

	return true
}

func applyPositionVisibilityProp(style *ResolvedStyle, value string, parent *ResolvedStyle, hasParent bool) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		kw := strings.ToLower(trimmed)
		var parentVal string

		if hasParent && parent != nil {
			parentVal = parent.PositionVisibility
		}

		style.PositionVisibility = applyAnchorWideKeywordString(parentVal, kw, hasParent)

		return true
	}

	vis, ok := parseAnchorVisibility(value)
	if !ok {
		return true
	}

	style.PositionVisibility = vis

	return true
}

// applyPositionTryShorthandProp applies position-try by expanding it into
// order plus fallbacks. CSS-wide keywords reset both longhands.
func applyPositionTryShorthandProp(style *ResolvedStyle, value string, parent *ResolvedStyle, hasParent bool) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		kw := strings.ToLower(trimmed)
		applyPositionTryFallbacksProp(style, kw, parent, hasParent)
		applyPositionTryOrderProp(style, kw, parent, hasParent)

		return true
	}

	decls, ok := expandPositionTryDeclaration(positionTryProp, value)
	if !ok {
		return true
	}

	for _, decl := range decls {
		applyAnchorProps(style, decl.prop, decl.val, 0, nil, parent, hasParent)
	}

	return true
}

// expandPositionTryDeclaration expands the position-try shorthand into its
// order and fallbacks longhands. It returns false for other properties and
// for invalid values so the declaration keeps the previous value. A leading
// order keyword followed by fallbacks sets both; otherwise the whole value
// must be fallbacks-only and order resets to normal. A lone order keyword
// sets order and resets fallbacks to none.
func expandPositionTryDeclaration(prop, value string) ([]logicalPropDecl, bool) {
	if prop != positionTryProp {
		return nil, false
	}

	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		kw := strings.TrimSpace(value)

		return []logicalPropDecl{
			{prop: positionTryOrderProp, val: kw},
			{prop: positionTryFallbacks, val: kw},
		}, true
	}

	fields := strings.Fields(trimmed)
	if len(fields) >= 2 {
		if _, ok := parseAnchorTryOrder(fields[0]); ok {
			rest := strings.TrimSpace(trimmed[len(fields[0]):])
			if fallbacks, ok := parseAnchorTryFallbacks(rest); ok {
				_ = fallbacks

				return []logicalPropDecl{
					{prop: positionTryOrderProp, val: fields[0]},
					{prop: positionTryFallbacks, val: rest},
				}, true
			}

			return nil, false
		}
	}

	if len(fields) == 1 {
		if order, ok := parseAnchorTryOrder(trimmed); ok {
			if _, isFallback := parseAnchorTryFallbacks(trimmed); !isFallback {
				return []logicalPropDecl{
					{prop: positionTryOrderProp, val: order},
					{prop: positionTryFallbacks, val: "none"},
				}, true
			}
		}
	}

	fallbacks, ok := parseAnchorTryFallbacks(trimmed)
	if !ok {
		return nil, false
	}

	_ = fallbacks

	return []logicalPropDecl{
		{prop: positionTryOrderProp, val: "normal"},
		{prop: positionTryFallbacks, val: trimmed},
	}, true
}

// Anchor shorthand acceptance for the cascade gate (supportedDeclaration).
// CSS-wide keywords always pass; anything else must parse.
func anchorNameValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		return true
	}

	_, ok := parseAnchorNameList(value)

	return ok
}

func anchorScopeValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		return true
	}

	_, ok := parseAnchorScope(value)

	return ok
}

func anchorPositionAnchorValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		return true
	}

	_, ok := parseAnchorPositionAnchor(value)

	return ok
}

func anchorPositionAreaValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) || strings.EqualFold(trimmed, "none") {
		return true
	}

	_, ok := parseAnchorPositionArea(value)

	return ok
}

func anchorFallbacksValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		return true
	}

	_, ok := parseAnchorTryFallbacks(value)

	return ok
}

func anchorOrderValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		return true
	}

	_, ok := parseAnchorTryOrder(value)

	return ok
}

func anchorTryValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		return true
	}

	_, ok := expandPositionTryDeclaration(positionTryProp, value)

	return ok
}

func anchorVisibilityValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		return true
	}

	_, ok := parseAnchorVisibility(value)

	return ok
}

// anchorCandidate is one position option: its area plus the geometry that
// area produces for the target box. cbW and cbH are the containing sizes the
// try-size order keys on.
type anchorCandidate struct {
	area string
	x, y float64
	cbW  float64
	cbH  float64
	rx0  float64
	ry0  float64
	rx1  float64
	ry1  float64
}

// clampRectStart shifts one axis of a box so the box stays inside the
// containing block span when it can fit there at all.
func clampRectStart(start, size, cbStart, cbSize float64) float64 {
	if size >= cbSize {
		return cbStart
	}

	if start < cbStart {
		return cbStart
	}

	if start+size > cbStart+cbSize {
		return cbStart + cbSize - size
	}

	return start
}

// rectInside reports whether the box lies inside the rect within tolerance.
func rectInside(x, y, w, h, rx0, ry0, rx1, ry1 float64) bool {
	if w < 0 || h < 0 {
		return false
	}

	if x < rx0-0.01 || y < ry0-0.01 {
		return false
	}

	if x+w > rx1+0.01 || y+h > ry1+0.01 {
		return false
	}

	return true
}

// buildAnchorMap indexes every built box carrying anchor-name by its names in
// document order. Boxes are walked pre-order so the last entry is the last
// in tree order, matching the spec fallback when no ancestor qualifies.
func buildAnchorMap(root *box, styles map[*html.Node]*ResolvedStyle) map[string][]*box {
	out := make(map[string][]*box)

	var walk func(b *box)
	walk = func(b *box) {
		if b == nil {
			return
		}

		if b.node != nil {
			if sty := styles[b.node]; sty != nil {
				for _, name := range sty.AnchorNames {
					out[name] = append(out[name], b)
				}
			}
		}

		for _, child := range b.children {
			walk(child)
		}
	}
	walk(root)

	return out
}

// findTargetAnchor selects the anchor box for target. An explicit
// dashed-ident prefers the nearest ancestor with that name, else the last
// in-scope box in tree order. Auto (or the empty initial) uses the anchor
// only when exactly one in-scope name exists; otherwise it keeps the
// ordinary offsets.
func findTargetAnchor(
	target *box, anchorMap map[string][]*box, styles map[*html.Node]*ResolvedStyle,
) *box {
	if target == nil || target.style == nil {
		return nil
	}

	name := strings.TrimSpace(target.style.PositionAnchor)

	if isAnchorDashedIdent(name) {
		candidates := inScopeAnchorBoxes(anchorMap[name], target.node, name, styles)
		if len(candidates) == 0 {
			return nil
		}

		if box := nearestAncestorAnchorBox(candidates, target.node); box != nil {
			return box
		}

		return candidates[len(candidates)-1]
	}

	if name != "" && !strings.EqualFold(name, "auto") {
		return nil
	}

	var single *box

	count := 0

	for anchorName, boxes := range anchorMap {
		for _, b := range inScopeAnchorBoxes(boxes, target.node, anchorName, styles) {
			count++

			single = b

			if count > 1 {
				return nil
			}
		}
	}

	return single
}

// inScopeAnchorBoxes keeps only boxes whose anchor name is visible to target.
func inScopeAnchorBoxes(
	boxes []*box, target *html.Node, name string, styles map[*html.Node]*ResolvedStyle,
) []*box {
	var out []*box

	for _, b := range boxes {
		if b == nil || b.node == nil {
			continue
		}

		if isAnchorInScope(b.node, target, name, styles) {
			out = append(out, b)
		}
	}

	return out
}

// nearestAncestorAnchorBox returns the nearest ancestor of target among
// candidates, or nil when none qualifies.
func nearestAncestorAnchorBox(candidates []*box, target *html.Node) *box {
	byNode := make(map[*html.Node]*box, len(candidates))
	for _, b := range candidates {
		byNode[b.node] = b
	}

	for n := target.Parent; n != nil; n = n.Parent {
		if b, ok := byNode[n]; ok {
			return b
		}
	}

	return nil
}

// isAnchorInScope reports whether anchorNode's name is visible to target. An
// anchor-scope on the anchor or its ancestors that lists the name (or all)
// hides it from targets outside that scope's subtree. No scope (empty or
// none) hides nothing. The check runs both directions: a scope between the
// anchor and the target hides an outer same-named anchor from an inner
// target just as it hides an inner anchor from an outer target.
func isAnchorInScope(
	anchorNode, target *html.Node, name string, styles map[*html.Node]*ResolvedStyle,
) bool {
	if anchorNode == nil || target == nil {
		return false
	}

	for scopeNode := anchorNode; scopeNode != nil; scopeNode = scopeNode.Parent {
		sty := styles[scopeNode]
		if sty == nil {
			continue
		}

		scope := strings.TrimSpace(sty.AnchorScope)
		if scope == "" || strings.EqualFold(scope, "none") {
			continue
		}

		scoped := false

		if strings.EqualFold(scope, "all") {
			scoped = true
		} else {
			for _, part := range anchorScopeList(scope) {
				if part == name {
					scoped = true

					break
				}
			}
		}

		if !scoped {
			continue
		}

		return isDescendantOf(target, scopeNode)
	}

	return true
}

// isDescendantOf reports whether node is scope or inside its subtree.
func isDescendantOf(node, scope *html.Node) bool {
	for n := node; n != nil; n = n.Parent {
		if n == scope {
			return true
		}
	}

	return false
}

// lookupPositionTry finds the last @position-try rule with the given name
// across sheets in order. Later sheets win, matching cascade order. Unknown
// idents return nil so the fallback entry is skipped.
func lookupPositionTry(sheets []*css.Stylesheet, name string) *css.PositionTryRule {
	var found *css.PositionTryRule

	for _, sheet := range sheets {
		if sheet == nil {
			continue
		}

		if rule := sheet.PositionTry(name); rule != nil {
			found = rule
		}
	}

	return found
}

// resolveAnchorFallbackItem resolves one position-try-fallbacks entry to an
// area. Pure areas return as written. A dashed-ident reads its @position-try
// rule (unknown idents fail so the entry is skipped) and try-tactics flip
// the resulting area after the area is known. It reports false when the
// entry is invalid or its ident is unknown.
func resolveAnchorFallbackItem(item, base string, sheets []*css.Stylesheet) (string, bool, bool, bool) {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(item)))
	if len(tokens) == 0 {
		return "", false, false, false
	}

	var ident string

	var tactics []string

	for _, tok := range tokens {
		switch {
		case isAnchorDashedIdent(tok):
			if ident != "" {
				return "", false, false, false
			}

			ident = tok
		case isAnchorTryTactic(tok):
			tactics = append(tactics, tok)
		case isPositionAreaKeyword(tok):
			// Area keywords must not mix with ident/tactic in one entry.
			if ident != "" || len(tactics) > 0 {
				return "", false, false, false
			}
		default:
			return "", false, false, false
		}
	}

	// Pure area fallback: one or two area keywords.
	if ident == "" && len(tactics) == 0 {
		if !isValidPositionArea(item) {
			return "", false, false, false
		}

		return normalizeCSSValue(item), true, false, false
	}

	// Ident and/or tactic fallback.
	candidate := base

	if ident != "" {
		rule := lookupPositionTry(sheets, ident)
		if rule == nil {
			return "", false, false, false
		}

		candidate = strings.ToLower(strings.TrimSpace(rule.Area))

		if candidate == "" || candidate == "none" {
			return "", false, false, false
		}
	}

	if candidate == "" || strings.EqualFold(candidate, "none") {
		return "", false, false, false
	}

	flipV, flipH := false, false

	for _, tactic := range tactics {
		candidate = flipPositionArea(candidate, tactic)

		switch tactic {
		case "flip-block", "flip-y":
			flipV = !flipV
		case "flip-inline", "flip-x":
			flipH = !flipH
		case "flip-start":
			flipV = !flipV

			flipH = !flipH
		}
	}

	if !isValidPositionArea(candidate) {
		return "", false, false, false
	}

	return candidate, true, flipV, flipH
}

// collectAnchorCandidates builds the position options list: the base area
// followed by resolved fallbacks in document order. Unknown dashed-idents
// are skipped; tactic entries flip the base area. Each option carries the
// position-area region rect its box must land inside to be accepted, and its
// point is first shifted to stay inside the containing block when it can.
func collectAnchorCandidates(
	sty *ResolvedStyle, anchor, target *box, cbX, cbY, cbW, cbH, viewportW, viewportH float64,
	sheets []*css.Stylesheet,
) []anchorCandidate {
	base := make([]anchorCandidate, 0, 1)
	fallbacks := make([]anchorCandidate, 0, len(sty.PositionTryFallbacks))

	add := func(area string, flipV, flipH bool, into *[]anchorCandidate) {
		if anchor == nil || target == nil {
			return
		}

		if normalizeCSSValue(area) == "none" || strings.TrimSpace(area) == "" {
			return
		}

		leftAuto := sty.LeftAuto
		topAuto := sty.TopAuto
		fallbackX := target.x
		fallbackY := target.y

		if flipV {
			topAuto = sty.BottomAuto
		}

		if flipH {
			leftAuto = sty.RightAuto
		}

		x, y, trackW, trackH, ok := anchorAreaGeometry(
			area, anchor, target.w, target.height, viewportW, viewportH,
			fallbackX, fallbackY, leftAuto, topAuto,
		)
		if !ok {
			return
		}

		x = clampRectStart(x, target.w, cbX, cbW)
		y = clampRectStart(y, target.height, cbY, cbH)

		rx0, ry0, rx1, ry1 := anchorRegionRect(area, anchor, cbX, cbY, cbW, cbH)

		*into = append(*into, anchorCandidate{
			area: normalizeCSSValue(area),
			x:    x, y: y, cbW: trackW, cbH: trackH,
			rx0: rx0, ry0: ry0, rx1: rx1, ry1: ry1,
		})
	}

	add(sty.PositionArea, false, false, &base)

	for _, entry := range sty.PositionTryFallbacks {
		area, ok, flipV, flipH := resolveAnchorFallbackItem(entry, sty.PositionArea, sheets)
		if !ok {
			continue
		}

		add(area, flipV, flipH, &fallbacks)
	}

	fallbacks = orderAnchorCandidates(fallbacks, sty.PositionTryOrder)

	return append(base, fallbacks...)
}

// orderAnchorCandidates stably sorts a copy of the candidates by the
// try-size axis, largest first. Normal keeps document order.
func orderAnchorCandidates(cands []anchorCandidate, order string) []anchorCandidate {
	low := strings.ToLower(strings.TrimSpace(order))
	if low == "" || low == "normal" {
		return cands
	}

	if !isAnchorTrySize(low) {
		return cands
	}

	sorted := append([]anchorCandidate(nil), cands...)

	for i := 1; i < len(sorted); i++ {
		cur := sorted[i]
		curKey := anchorTrySizeValue(low, cur.cbW, cur.cbH)
		j := i - 1

		for j >= 0 && anchorTrySizeValue(low, sorted[j].cbW, sorted[j].cbH) < curKey {
			sorted[j+1] = sorted[j]
			j--
		}

		sorted[j+1] = cur
	}

	return sorted
}

// anchorTrySizeValue returns the sort key for a try-size keyword.
func anchorTrySizeValue(order string, cbW, cbH float64) float64 {
	switch strings.ToLower(strings.TrimSpace(order)) {
	case "most-width", "most-inline-size":
		return cbW
	case "most-height", "most-block-size":
		return cbH
	default:
		return 0
	}
}

// anchorRegionRect returns the position-area region for one area: the
// inset-modified containing block an option's box must fit inside. Single
// tracks span from the containing block edge to the anchor edge; spans cover
// their two tracks; a missing track is the full containing block axis.
func anchorRegionRect(area string, anchor *box, cbX, cbY, cbW, cbH float64) (float64, float64, float64, float64) {
	vert, horiz, _, ok := splitPositionArea(area)
	if !ok || anchor == nil {
		return cbX, cbY, cbX + cbW, cbY + cbH
	}

	ry0, ry1 := verticalRegionFor(vert, anchor.y, anchor.height, cbY, cbY+cbH)
	rx0, rx1 := horizontalRegionFor(horiz, anchor.x, anchor.w, cbX, cbX+cbW)

	return rx0, ry0, rx1, ry1
}

func verticalRegionFor(vert string, anchorY, anchorH, cbY0, cbY1 float64) (float64, float64) {
	switch vert {
	case "center":
		return anchorY, anchorY + anchorH
	case "span-all":
		return min(cbY0, anchorY), max(cbY1, anchorY+anchorH)
	case "span-top", "span-y-start", "span-self-y-start", "span-block-start",
		"span-self-block-start", "span-start", "span-self-start":
		return min(cbY0, anchorY), anchorY + anchorH
	case "span-bottom", "span-y-end", "span-self-y-end", "span-block-end",
		"span-self-block-end", "span-end", "span-self-end":
		return anchorY, max(cbY1, anchorY+anchorH)
	case "":
		return cbY0, cbY1
	default:
		if anchorTrackSide(vert) > 0 {
			return anchorY + anchorH, max(cbY1, anchorY+anchorH)
		}

		if anchorTrackSide(vert) < 0 {
			return min(cbY0, anchorY), anchorY
		}

		return cbY0, cbY1
	}
}

func horizontalRegionFor(horiz string, anchorX, anchorW, cbX0, cbX1 float64) (float64, float64) {
	switch horiz {
	case "center":
		return anchorX, anchorX + anchorW
	case "span-all":
		return min(cbX0, anchorX), max(cbX1, anchorX+anchorW)
	case "span-left", "span-x-start", "span-self-x-start", "span-inline-start",
		"span-self-inline-start", "span-start", "span-self-start":
		return min(cbX0, anchorX), anchorX + anchorW
	case "span-right", "span-x-end", "span-self-x-end", "span-inline-end",
		"span-self-inline-end", "span-end", "span-self-end":
		return anchorX, max(cbX1, anchorX+anchorW)
	case "":
		return cbX0, cbX1
	default:
		if anchorTrackSide(horiz) > 0 {
			return anchorX + anchorW, max(cbX1, anchorX+anchorW)
		}

		if anchorTrackSide(horiz) < 0 {
			return min(cbX0, anchorX), anchorX
		}

		return cbX0, cbX1
	}
}

// pickAnchorCandidate selects the winning option: the base first, then the
// fallbacks in the try order. An option wins when its box fits that option's
// position-area region (per Chrome 155 print). When no option fits, the box
// keeps the base option (its current styles) and is only hidden under
// no-overflow. hidden reports that case.
func pickAnchorCandidate(
	cands []anchorCandidate, sty *ResolvedStyle, target *box,
) (*anchorCandidate, bool) {
	if len(cands) == 0 || sty == nil || target == nil {
		return nil, false
	}

	fits := func(cand anchorCandidate) bool {
		return rectInside(cand.x, cand.y, target.w, target.height, cand.rx0, cand.ry0, cand.rx1, cand.ry1)
	}

	for i := range cands {
		if i == 0 {
			if fits(cands[0]) {
				return &cands[0], false
			}

			continue
		}

		base := &cands[i]

		if fits(*base) {
			out := *base

			return &out, false
		}
	}

	if strings.Contains(normalizeCSSValue(sty.PositionVisibility), "no-overflow") {
		return nil, true
	}

	out := cands[0]

	return &out, false
}

// resolveAnchorPositions moves absolute and fixed boxes with an in-scope
// anchor and a position-area. It runs once after the box tree is built (see
// finalizeResult) so anchors laid out anywhere in the document are visible.
// Boxes without an anchor, without an area, or with visibility that rejects
// every candidate keep their absolute offsets and still paint. The anchor
// box itself never moves.
func (e *engine) resolveAnchorPositions(root *box) {
	if e == nil || root == nil {
		return
	}

	anchorMap := buildAnchorMap(root, e.styles)
	if len(anchorMap) == 0 {
		return
	}

	hidden := map[*box]struct{}{}

	var walk func(b, cb *box)
	walk = func(b, cb *box) {
		if b == nil {
			return
		}

		if e.placeAnchorTarget(b, anchorMap, cb) {
			hidden[b] = struct{}{}
		}

		next := cb
		if b.style != nil && b.style.Position != positionStatic {
			next = b
		}

		for _, child := range b.children {
			walk(child, next)
		}
	}

	walk(root, nil)

	if len(hidden) > 0 {
		skip := map[*box]struct{}{}
		for b := range hidden {
			for s := range markBoxSubtree(b) {
				skip[s] = struct{}{}
			}
		}

		kept := make([]chromeEntry, 0, len(e.deferredChrome))
		for _, entry := range e.deferredChrome {
			if _, ok := skip[entry.b]; !ok {
				kept = append(kept, entry)
			}
		}

		e.deferredChrome = kept
	}
}

func (e *engine) placeAnchorTarget(target *box, anchorMap map[string][]*box, cb *box) bool {
	if e == nil || target == nil || target.style == nil {
		return false
	}

	sty := target.style
	if sty.Position != positionAbsolute && sty.Position != positionFixed {
		return false
	}

	anchor := findTargetAnchor(target, anchorMap, e.styles)
	if anchor == nil {
		return false
	}

	viewportW := e.opts.Width
	viewportH := e.opts.Height
	if viewportH <= 0 {
		viewportH = 1e9
	}

	cbX, cbY, cbW, cbH := anchorCBRect(target, cb, viewportW, viewportH)

	cands := collectAnchorCandidates(sty, anchor, target, cbX, cbY, cbW, cbH, viewportW, viewportH, e.opts.Sheets)
	if len(cands) == 0 {
		return false
	}

	win, hidden := pickAnchorCandidate(cands, sty, target)
	if hidden {
		hideAnchorBox(e, target)

		return true
	}

	if win == nil {
		return false
	}

	dx := win.x - target.x
	dy := win.y - target.y
	if dx == 0 && dy == 0 {
		return false
	}

	target.x = win.x
	target.y = win.y
	e.shiftBoxOps(target, dx, dy)

	return false
}

func anchorCBRect(target, cb *box, viewportW, viewportH float64) (float64, float64, float64, float64) {
	if target != nil && target.style != nil && target.style.Position == positionFixed {
		return 0, 0, viewportW, viewportH
	}

	if cb != nil {
		return cb.x, cb.y, cb.w, cb.height
	}

	return 0, 0, viewportW, viewportH
}

func hideAnchorBox(e *engine, target *box) {
	if e == nil || target == nil {
		return
	}

	if target.opEnd >= target.opStart {
		for k := target.opStart; k <= target.opEnd && k < len(e.ops); k++ {
			e.ops[k].Kind = opKindNoop
		}
	}
}

// anchorAreaGeometry returns the target top-left and the containing-block
// size for an area. Single vertical areas align X to the anchor start edge
// and single horizontal areas align Y the same way; center centers that
// axis. It returns false for none or invalid areas, which keep absolute
// offsets.
func anchorAreaGeometry(
	area string, anchor *box, targetW, targetH, viewportW, viewportH float64,
	fallbackX, fallbackY float64, leftAuto, topAuto bool,
) (float64, float64, float64, float64, bool) {
	vert, horiz, single, ok := splitPositionArea(area)
	if !ok {
		return 0, 0, 0, 0, false
	}

	var x, y float64

	switch single {
	case "vertical":
		x = fallbackX
		switch {
		case anchorTrackSide(vert) > 0:
			y = anchor.y + anchor.height
		case topAuto:
			y = verticalAreaY(vert, anchor.y, anchor.height, targetH)
		default:
			y = fallbackY
		}
	case "horizontal":
		y = fallbackY
		switch {
		case anchorTrackSide(horiz) > 0:
			x = anchor.x + anchor.w
		case leftAuto:
			x = horizontalAreaX(horiz, anchor.x, anchor.w, targetW)
		default:
			x = fallbackX
		}
	case "single-center":
		x = anchor.x + (anchor.w-targetW)/2
		y = anchor.y + (anchor.height-targetH)/2
	case "single-span-all":
		x = anchor.x
		y = anchor.y
	case "double":
		switch anchorTrackSide(vert) {
		case 1:
			y = anchor.y + anchor.height
		case -1:
			if topAuto {
				y = verticalAreaY(vert, anchor.y, anchor.height, targetH)
			} else {
				y = fallbackY
			}
		default:
			y = verticalAreaY(vert, anchor.y, anchor.height, targetH)
		}
		switch anchorTrackSide(horiz) {
		case 1:
			x = anchor.x + anchor.w
		case -1:
			if leftAuto {
				x = horizontalAreaX(horiz, anchor.x, anchor.w, targetW)
			} else {
				x = fallbackX
			}
		default:
			x = horizontalAreaX(horiz, anchor.x, anchor.w, targetW)
		}
	default:
		return 0, 0, 0, 0, false
	}

	cbW := anchor.w
	switch anchorTrackSide(horiz) {
	case -1:
		cbW = anchor.x
	case 1:
		cbW = viewportW - (anchor.x + anchor.w)
	}

	cbH := anchor.height
	switch anchorTrackSide(vert) {
	case -1:
		cbH = anchor.y
	case 1:
		cbH = viewportH - (anchor.y + anchor.height)
	}

	return x, y, cbW, cbH, true
}

// anchorTrackSide classifies one area track: -1 sits before the anchor,
// +1 sits after it, 0 aligns with it.
func anchorTrackSide(tok string) int {
	switch tok {
	case "top", "y-start", "self-y-start", "block-start", "self-block-start",
		"start", "self-start", "span-top", "span-y-start", "span-self-y-start",
		"span-block-start", "span-self-block-start", "span-start", "span-self-start",
		"left", "x-start", "self-x-start", "inline-start", "self-inline-start",
		"span-left", "span-x-start", "span-self-x-start", "span-inline-start",
		"span-self-inline-start":
		return -1
	case "bottom", "y-end", "self-y-end", "block-end", "self-block-end",
		"end", "self-end", "span-bottom", "span-y-end", "span-self-y-end",
		"span-block-end", "span-self-block-end", "span-end", "span-self-end",
		"right", "x-end", "self-x-end", "inline-end", "self-inline-end",
		"span-right", "span-x-end", "span-self-x-end", "span-inline-end",
		"span-self-inline-end":
		return 1
	default:
		return 0
	}
}

// splitPositionArea divides an area into its vertical and horizontal tracks.
// single reports vertical for one vertical keyword, horizontal for one
// horizontal keyword, single-center for a lone center, single-span-all for a
// lone span-all, and double for two-keyword areas.
func splitPositionArea(area string) (string, string, string, bool) {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(area)))

	switch len(tokens) {
	case 1:
		tok := tokens[0]
		switch {
		case isVerticalAreaKeyword(tok):
			return tok, "span-all", "vertical", true
		case isHorizontalAreaKeyword(tok):
			return "span-all", tok, "horizontal", true
		case tok == "center":
			return tok, tok, "single-center", true
		case tok == "span-all":
			return tok, tok, "single-span-all", true
		case isAmbiguousAreaKeyword(tok):
			// Lone start/end/self-start/self-end repeats per the grammar.
			return tok, tok, "double", true
		default:
			return "", "", "", false
		}
	case 2:
		first, second := tokens[0], tokens[1]
		firstVert := isVerticalAreaKeyword(first)
		firstHoriz := isHorizontalAreaKeyword(first)
		secondVert := isVerticalAreaKeyword(second)
		secondHoriz := isHorizontalAreaKeyword(second)

		switch {
		case firstVert && secondHoriz:
			return first, second, "double", true
		case firstHoriz && secondVert:
			return second, first, "double", true
		case firstVert && !secondVert && !secondHoriz:
			return first, second, "double", true
		case !firstVert && !firstHoriz && secondHoriz:
			return first, second, "double", true
		case firstHoriz && !secondVert && !secondHoriz:
			return second, first, "double", true
		case !firstVert && !firstHoriz && secondVert:
			return second, first, "double", true
		case !firstVert && !firstHoriz && !secondVert && !secondHoriz:
			// Both ambiguous: first is block (vertical), second inline.
			return first, second, "double", true
		default:
			return "", "", "", false
		}
	default:
		return "", "", "", false
	}
}

// verticalAreaY returns the target top for one vertical track.
func verticalAreaY(vert string, anchorY, anchorH, targetH float64) float64 {
	switch vert {
	case "bottom", "y-end", "self-y-end", "block-end", "self-block-end",
		"end", "self-end", "span-bottom", "span-y-end", "span-self-y-end",
		"span-block-end", "span-self-block-end", "span-end", "span-self-end":
		return anchorY + anchorH
	case "center":
		return anchorY + (anchorH-targetH)/2
	case "span-all":
		return anchorY
	default:
		// top, y-start, block-start, start, self variants, and their spans.
		return anchorY - targetH
	}
}

// horizontalAreaX returns the target left for one horizontal track.
func horizontalAreaX(horiz string, anchorX, anchorW, targetW float64) float64 {
	switch horiz {
	case "right", "x-end", "self-x-end", "inline-end", "self-inline-end",
		"end", "self-end", "span-right", "span-x-end", "span-self-x-end",
		"span-inline-end", "span-self-inline-end", "span-end", "span-self-end":
		return anchorX + anchorW
	case "center":
		return anchorX + (anchorW-targetW)/2
	case "span-all":
		return anchorX
	default:
		// left, x-start, inline-start, start, self variants, and their spans.
		return anchorX - targetW
	}
}

// flipPositionArea flips one area across the axis the pinned tactic names.
// flip-block and flip-y mirror the vertical track, flip-inline and flip-x
// mirror the horizontal track, and flip-start mirrors both so the flipped Y
// lands opposite the unflipped Y. The flip runs after the area is known and
// before the final shift.
func flipPositionArea(area, tactic string) string {
	vert, horiz, single, ok := splitPositionArea(area)
	if !ok {
		return area
	}

	switch strings.ToLower(strings.TrimSpace(tactic)) {
	case "flip-block", "flip-y":
		vert = flipVerticalToken(vert)
	case "flip-inline", "flip-x":
		horiz = flipHorizontalToken(horiz)
	case "flip-start":
		vert = flipVerticalToken(vert)
		horiz = flipHorizontalToken(horiz)
	default:
		return area
	}

	switch single {
	case "vertical", "horizontal", "single-center", "single-span-all":
		// Rebuild the lone-keyword form so top stays top-shaped.
		if single == "vertical" {
			return vert
		}

		if single == "horizontal" {
			return horiz
		}

		if vert == horiz {
			return vert
		}

		return vert + " " + horiz
	default:
		return vert + " " + horiz
	}
}

// flipVerticalToken mirrors one vertical track token.
func flipVerticalToken(tok string) string {
	switch tok {
	case "top":
		return "bottom"
	case "bottom":
		return "top"
	case "y-start":
		return "y-end"
	case "y-end":
		return "y-start"
	case "self-y-start":
		return "self-y-end"
	case "self-y-end":
		return "self-y-start"
	case "block-start":
		return "block-end"
	case "block-end":
		return "block-start"
	case "self-block-start":
		return "self-block-end"
	case "self-block-end":
		return "self-block-start"
	case "start":
		return "end"
	case "end":
		return "start"
	case "self-start":
		return "self-end"
	case "self-end":
		return "self-start"
	case "span-top":
		return "span-bottom"
	case "span-bottom":
		return "span-top"
	case "span-y-start":
		return "span-y-end"
	case "span-y-end":
		return "span-y-start"
	case "span-self-y-start":
		return "span-self-y-end"
	case "span-self-y-end":
		return "span-self-y-start"
	case "span-block-start":
		return "span-block-end"
	case "span-block-end":
		return "span-block-start"
	case "span-self-block-start":
		return "span-self-block-end"
	case "span-self-block-end":
		return "span-self-block-start"
	case "span-start":
		return "span-end"
	case "span-end":
		return "span-start"
	case "span-self-start":
		return "span-self-end"
	case "span-self-end":
		return "span-self-start"
	default:
		return tok
	}
}

// flipHorizontalToken mirrors one horizontal track token.
func flipHorizontalToken(tok string) string {
	switch tok {
	case "left":
		return "right"
	case "right":
		return "left"
	case "x-start":
		return "x-end"
	case "x-end":
		return "x-start"
	case "self-x-start":
		return "self-x-end"
	case "self-x-end":
		return "self-x-start"
	case "inline-start":
		return "inline-end"
	case "inline-end":
		return "inline-start"
	case "self-inline-start":
		return "self-inline-end"
	case "self-inline-end":
		return "self-inline-start"
	case "start":
		return "end"
	case "end":
		return "start"
	case "self-start":
		return "self-end"
	case "self-end":
		return "self-start"
	case "span-left":
		return "span-right"
	case "span-right":
		return "span-left"
	case "span-x-start":
		return "span-x-end"
	case "span-x-end":
		return "span-x-start"
	case "span-self-x-start":
		return "span-self-x-end"
	case "span-self-x-end":
		return "span-self-x-start"
	case "span-inline-start":
		return "span-inline-end"
	case "span-inline-end":
		return "span-inline-start"
	case "span-self-inline-start":
		return "span-self-inline-end"
	case "span-self-inline-end":
		return "span-self-inline-start"
	case "span-start":
		return "span-end"
	case "span-end":
		return "span-start"
	case "span-self-start":
		return "span-self-end"
	case "span-self-end":
		return "span-self-start"
	default:
		return tok
	}
}

// isValidPositionArea reports whether value is one to two position-area
// keywords with at most one track per axis.
func isValidPositionArea(value string) bool {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	if len(tokens) == 0 || len(tokens) > 2 {
		return false
	}

	for _, tok := range tokens {
		if !isPositionAreaKeyword(tok) {
			return false
		}
	}

	if len(tokens) == 2 {
		firstVert := isVerticalAreaKeyword(tokens[0])
		firstHoriz := isHorizontalAreaKeyword(tokens[0])
		secondVert := isVerticalAreaKeyword(tokens[1])
		secondHoriz := isHorizontalAreaKeyword(tokens[1])

		// Two explicit tracks on one axis never pair (top bottom is invalid).
		if (firstVert && secondVert) || (firstHoriz && secondHoriz) {
			return false
		}
	}

	return true
}

// isPositionAreaKeyword reports whether tok appears in the pinned
// <position-area> production.
func isPositionAreaKeyword(tok string) bool {
	return isVerticalAreaKeyword(tok) || isHorizontalAreaKeyword(tok) || isAmbiguousAreaKeyword(tok)
}

// isVerticalAreaKeyword reports whether tok can only name a vertical track.
func isVerticalAreaKeyword(tok string) bool {
	switch tok {
	case "top", "bottom", "y-start", "y-end", "self-y-start", "self-y-end",
		"block-start", "block-end", "self-block-start", "self-block-end",
		"span-top", "span-bottom", "span-y-start", "span-y-end",
		"span-self-y-start", "span-self-y-end", "span-block-start",
		"span-block-end", "span-self-block-start", "span-self-block-end":
		return true
	default:
		return false
	}
}

// isHorizontalAreaKeyword reports whether tok can only name a horizontal track.
func isHorizontalAreaKeyword(tok string) bool {
	switch tok {
	case "left", "right", "x-start", "x-end", "self-x-start", "self-x-end",
		"inline-start", "inline-end", "self-inline-start", "self-inline-end",
		"span-left", "span-right", "span-x-start", "span-x-end",
		"span-self-x-start", "span-self-x-end", "span-inline-start",
		"span-inline-end", "span-self-inline-start", "span-self-inline-end":
		return true
	default:
		return false
	}
}

// isAmbiguousAreaKeyword reports whether tok leaves its axis to the other
// keyword: center, span-all, and the axis-free start/end family.
func isAmbiguousAreaKeyword(tok string) bool {
	switch tok {
	case "center", "span-all", "start", "end", "self-start", "self-end",
		"span-start", "span-end", "span-self-start", "span-self-end":
		return true
	default:
		return false
	}
}
