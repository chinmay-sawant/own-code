package layout

import (
	"slices"
	"strconv"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
	"github.com/chinmay-sawant/blinkless/internal/html"
)

// ruleHit is one selector match from the shared cascade rule walk. rule
// points into its stylesheet, so pointer identity is the exact rule identity
// the style memo keys on; ruleHit therefore stays comparable and comparable
// field-for-field.
type ruleHit struct {
	rule    *css.Rule
	a, b, c int
}

// matchCacheKey identifies elements whose element-local selector outcomes are
// identical: the namespace-qualified tag, the full attribute set, and the
// pseudo-element pass. Classes and ids ride along inside attrs because the
// matcher reads them from the attribute map; the inline style attribute rides
// along too because attribute selectors can test for it. Ancestors, siblings,
// and measured container sizes are deliberately absent: selectors that read
// them are contextual and evaluate fresh on every lookup.
type matchCacheKey struct {
	tag    string
	attrs  string
	pseudo string
}

// matchCacheEntry is one cached signature: the sorted source-order candidates
// for the signature, which candidate positions are reusable from the cache
// (see selectorReusable), and the reusable hits among them in candidate
// order. A replay walks cands in order, resolves reusable positions from
// hits, and evaluates the rest fresh, so the merged sequence keeps the order
// a fresh walk produced.
type matchCacheEntry struct {
	cands    []ruleSel
	reusable []bool
	hits     []ruleSel
}

// matchSignature builds the cache key for node in the pseudoElem pass. The
// attribute encoding is length-prefixed over sorted names so distinct maps
// never share a key; the tag is lowercased for HTML (whose type selectors
// match ASCII case-insensitively) and namespace-qualified so foreign elements
// never alias HTML ones.
func (ctx *styleContext) matchSignature(node *html.Node, pseudoElem string) matchCacheKey {
	return matchCacheKey{tag: ctx.matchTag(node), attrs: canonicalAttrKey(node.Attrs), pseudo: pseudoElem}
}

// matchTag interns the namespaced tag for one cascade pass. Element probes
// run two to three times per element (element plus ::before/::after, plus
// counter and content probes), and the matchCacheKey holds the tag string,
// so sharing one copy per distinct tag removes a concatenation per probe.
// Tags are fixed at parse time, so a pass-local cache cannot go stale.
func (ctx *styleContext) matchTag(node *html.Node) string {
	if node.Namespace != html.NamespaceHTML {
		return "foreign\x00" + strconv.Itoa(int(node.Namespace)) + "\x00" + node.Name
	}

	lowered := strings.ToLower(node.Name)

	if interned, ok := ctx.matchTags[lowered]; ok {
		return interned
	}

	if ctx.matchTags == nil {
		ctx.matchTags = make(map[string]string)
	}

	tag := "html\x00" + lowered
	ctx.matchTags[lowered] = tag

	return tag
}

// canonicalAttrKey encodes the full attribute map with length-prefixed,
// sorted names so the key is exact and map-iteration order never leaks in.
// Single-attribute elements skip the name slice and the sort; the builder is
// pre-sized so the common shapes allocate exactly the result string.
func canonicalAttrKey(attrs map[string]string) string {
	switch len(attrs) {
	case 0:
		return ""
	case 1:
		for name, value := range attrs {
			var out strings.Builder

			out.Grow(len(name) + len(value) + attrKeyOverhead)
			writeAttrKeyInt(&out, len(name))
			out.WriteByte(':')
			out.WriteString(name)
			out.WriteByte('=')
			writeAttrKeyInt(&out, len(value))
			out.WriteByte(':')
			out.WriteString(value)
			out.WriteByte(';')

			return out.String()
		}
	}

	names := make([]string, 0, len(attrs))

	sizes := 0

	for name, value := range attrs {
		names = append(names, name)
		sizes += len(name) + len(value)
	}

	slices.Sort(names)

	var out strings.Builder

	out.Grow(sizes + len(names)*attrKeyOverhead)

	for _, name := range names {
		value := attrs[name]
		writeAttrKeyInt(&out, len(name))
		out.WriteByte(':')
		out.WriteString(name)
		out.WriteByte('=')
		writeAttrKeyInt(&out, len(value))
		out.WriteByte(':')
		out.WriteString(value)
		out.WriteByte(';')
	}

	return out.String()
}

// attrKeyOverhead is the separator and length-prefix budget per attribute in
// a canonical attribute key (two length fields plus ":…=…:;" framing).
const attrKeyOverhead = 8

// attrKeyBase is the decimal base for the length prefixes in a canonical
// attribute key.
const attrKeyBase = 10

// writeAttrKeyInt appends n in decimal without the strconv.Itoa allocation
// on the per-element match-signature path.
func writeAttrKeyInt(out *strings.Builder, n int) {
	var buf [20]byte

	out.Write(strconv.AppendInt(buf[:0], int64(n), attrKeyBase))
}

// matchedRules walks sheets with the cascade's gates (media, @container,
// selector match, specificity). pseudoElem != "" matches ::before/::after
// shapes instead of the element (pseudo-content path).
func (ctx *styleContext) matchedRules(node *html.Node, pseudoElem string) []ruleHit {
	if ctx == nil {
		return nil
	}

	if ctx.pollContext() {
		return nil
	}

	index := ctx.ruleIndex()
	if index == nil {
		return nil
	}

	if node == nil {
		ctx.ruleCands = index.candidates(ctx.ruleCands[:0], node, pseudoElem)
		sortCandidates(ctx.ruleCands)

		//nolint:exhaustruct // zero key: nil nodes never cache
		empty := matchCacheKey{}

		return ctx.scanMatches(node, pseudoElem, empty, false)
	}

	key := ctx.matchSignature(node, pseudoElem)
	if entry := ctx.matchCache[key]; entry != nil {
		return ctx.replayMatches(entry, node, pseudoElem)
	}

	ctx.ruleCands = index.candidates(ctx.ruleCands[:0], node, pseudoElem)
	sortCandidates(ctx.ruleCands)

	return ctx.scanMatches(node, pseudoElem, key, true)
}

// scanMatches evaluates the sorted ctx.ruleCands with the cascade's gates and
// selector matching, in source order. On a cacheable lookup it also records
// the reusable selector hits for the signature entry, so a replay can skip
// their gates and matching while keeping cascade order and specificity
// identical.
func (ctx *styleContext) scanMatches(
	node *html.Node, pseudoElem string, key matchCacheKey, cache bool,
) []ruleHit {
	hits := ctx.ruleHits[:0]

	var entry *matchCacheEntry

	if cache {
		entry = &matchCacheEntry{ //nolint:exhaustruct // reusable/hits grow during the scan
			cands: slices.Clone(ctx.ruleCands),
		}
		if len(entry.cands) > 0 {
			entry.reusable = make([]bool, len(entry.cands))
		}
	}

	for idx, cand := range ctx.ruleCands {
		if ctx.pollContext() {
			return nil
		}

		rule := cand.rule
		if !ctx.ruleGatesMatch(rule, node) {
			continue
		}

		sel := rule.Selectors[cand.sel]
		if !ctx.selectorMatches(sel, node, pseudoElem) {
			continue
		}

		a, b, c := css.Specificity(sel)
		hits = append(hits, ruleHit{rule: rule, a: a, b: b, c: c})

		if entry != nil && ctx.selectorReusable(rule, sel) {
			entry.reusable[idx] = true
			entry.hits = append(entry.hits, cand)
		}
	}

	ctx.ruleHits = hits

	if entry != nil {
		ctx.storeMatchEntry(key, entry)
	}

	return hits
}

// storeMatchEntry records a finished scan under key, allocating the pass
// cache on first use. Split from scanMatches so the hot scan stays flat.
func (ctx *styleContext) storeMatchEntry(key matchCacheKey, entry *matchCacheEntry) {
	if ctx.matchCache == nil {
		ctx.matchCache = make(map[matchCacheKey]*matchCacheEntry)
	}

	ctx.matchCache[key] = entry
}

// replayMatches replays a cached signature: reusable candidates resolve from
// the stored hits and contextual candidates evaluate fresh. Both sides walk
// the stored source-order candidates, so the merged sequence (and therefore
// the cascade) is identical to a fresh scan.
func (ctx *styleContext) replayMatches(
	entry *matchCacheEntry, node *html.Node, pseudoElem string,
) []ruleHit {
	hits := ctx.ruleHits[:0]
	next := 0

	for i, cand := range entry.cands {
		if ctx.pollContext() {
			return nil
		}

		if entry.reusable[i] {
			if next < len(entry.hits) && entry.hits[next] == cand {
				sel := cand.rule.Selectors[cand.sel]
				a, b, c := css.Specificity(sel)
				hits = append(hits, ruleHit{rule: cand.rule, a: a, b: b, c: c})
				next++
			}

			continue
		}

		if !ctx.ruleGatesMatch(cand.rule, node) {
			continue
		}

		sel := cand.rule.Selectors[cand.sel]
		if !ctx.selectorMatches(sel, node, pseudoElem) {
			continue
		}

		a, b, c := css.Specificity(sel)
		hits = append(hits, ruleHit{rule: cand.rule, a: a, b: b, c: c})
	}

	ctx.ruleHits = hits

	return hits
}

// selectorReusable reports whether one candidate's outcome is a pure function
// of the element signature for this pass: an element-local selector (see
// css.SelectorIsElementLocal) whose container gate cannot vary per element.
// Media and @supports gates are pass-constant and fold into every cached
// outcome. A container-gated rule is reusable only on passes without measured
// containers, where its gate fails uniformly.
func (ctx *styleContext) selectorReusable(rule *css.Rule, sel css.Selector) bool {
	if rule.Container != nil && ctx.containers != nil {
		return false
	}

	return css.SelectorIsElementLocal(sel)
}

// ruleGatesMatch reports whether one rule passes the non-selector gates the
// cascade applies before matching a selector.
func (ctx *styleContext) ruleGatesMatch(rule *css.Rule, node *html.Node) bool {
	if !ctx.staticGateMatches(rule) {
		return false
	}

	return ctx.containerGateMatches(node, rule)
}

// staticGateMatches reports whether one rule passes the node-independent
// gates: the media query and the @supports condition. Both read only
// pass-constant state, so the outcome is memoized per rule for the pass and
// shared by every element the pass resolves.
func (ctx *styleContext) staticGateMatches(rule *css.Rule) bool {
	if ok, found := ctx.gateCache[rule]; found {
		return ok
	}

	pass := css.MediaMatches(rule.Media, ctx.media, ctx.viewportW, ctx.viewportH) &&
		(rule.Supports == nil || css.SupportsMatches(rule.Supports, engineSupportsProperty))

	if ctx.gateCache == nil {
		ctx.gateCache = make(map[*css.Rule]bool)
	}

	ctx.gateCache[rule] = pass

	return pass
}
