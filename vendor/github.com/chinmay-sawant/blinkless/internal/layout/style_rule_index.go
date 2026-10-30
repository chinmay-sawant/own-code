package layout

import (
	"cmp"
	"slices"
	"strings"
	"unicode"

	"github.com/chinmay-sawant/blinkless/internal/css"
	"github.com/chinmay-sawant/blinkless/internal/html"
)

// ruleSel names one selector of one rule. The selector index places it in the
// bucket of its rightmost compound, so a node never sees the same selector
// twice and never walks a rule that cannot match. sheet keeps the candidate
// list sortable back into the source order the linear walk used, which keeps
// equal-specificity, equal-order ties (orders restart per sheet) unchanged.
type ruleSel struct {
	sheet int
	rule  *css.Rule
	sel   int
}

// ruleIndex narrows the rules one style pass must test. host holds selectors
// that end on the element itself; pseudo holds selectors that end on
// ::before or ::after, keyed by that name. content records that a pseudo rule
// declares content, which lets the generated-content walk skip pseudo
// matching when no rule can generate content at all. contentPseudo records
// the same per pseudo-element name so a ::before lookup skips when only
// ::after rules declare content (and vice versa).
type ruleIndex struct {
	host          ruleBucket
	pseudo        map[string]*ruleBucket
	content       bool
	contentPseudo map[string]bool
}

// ruleBucket groups selectors by the rightmost compound's strongest key: an
// id, else the first class, else the tag, else the universal bucket. Grouping
// by one key is enough: every key a selector lists must be present for it to
// match, so the bucket of any one of them finds it.
type ruleBucket struct {
	byID      map[string][]ruleSel
	byClass   map[string][]ruleSel
	byTag     map[string][]ruleSel
	universal []ruleSel
}

// newRuleIndex walks every sheet once and buckets each selector.
func newRuleIndex(sheets []*css.Stylesheet) *ruleIndex {
	index := new(ruleIndex)

	for sheetIndex, sheet := range sheets {
		if sheet == nil {
			continue
		}

		for ruleIndex := range sheet.Rules {
			rule := &sheet.Rules[ruleIndex]

			for selectorIndex := range rule.Selectors {
				sel := rule.Selectors[selectorIndex]
				if len(sel.Parts) == 0 {
					continue
				}

				last := sel.Parts[len(sel.Parts)-1]
				bucket := index.bucket(last.PseudoElement)
				bucket.add(ruleSel{sheet: sheetIndex, rule: rule, sel: selectorIndex}, last)

				if last.PseudoElement != "" && declaresContent(rule) {
					index.content = true

					if index.contentPseudo == nil {
						index.contentPseudo = map[string]bool{}
					}

					index.contentPseudo[last.PseudoElement] = true
				}
			}
		}
	}

	return index
}

// bucket returns the selector bucket for one pseudo-element name, creating
// the map on first use.
func (idx *ruleIndex) bucket(pseudo string) *ruleBucket {
	if pseudo == "" {
		return &idx.host
	}

	if idx.pseudo == nil {
		idx.pseudo = map[string]*ruleBucket{}
	}

	bucket := idx.pseudo[pseudo]
	if bucket == nil {
		bucket = new(ruleBucket)
		idx.pseudo[pseudo] = bucket
	}

	return bucket
}

// add places one selector in the bucket its rightmost compound names.
func (b *ruleBucket) add(entry ruleSel, part css.SelectorPart) {
	switch {
	case part.ID != "":
		if b.byID == nil {
			b.byID = map[string][]ruleSel{}
		}

		b.byID[part.ID] = append(b.byID[part.ID], entry)
	case len(part.Classes) > 0:
		if b.byClass == nil {
			b.byClass = map[string][]ruleSel{}
		}

		name := part.Classes[0]
		b.byClass[name] = append(b.byClass[name], entry)
	case part.Tag != "" && part.Tag != "*":
		if b.byTag == nil {
			b.byTag = map[string][]ruleSel{}
		}

		name := strings.ToLower(part.Tag)
		b.byTag[name] = append(b.byTag[name], entry)
	default:
		b.universal = append(b.universal, entry)
	}
}

// declaresContent reports whether one rule can generate pseudo content.
func declaresContent(rule *css.Rule) bool {
	for _, d := range rule.Decls {
		if strings.EqualFold(d.Prop, "content") {
			return true
		}
	}

	return false
}

// candidates appends the selectors that can match node with pseudo. The
// result may hold selectors whose other parts do not match; the caller still
// runs the full matcher. Order follows the id, class, tag, universal buckets,
// not source order; cascade priority comes from specificity and rule order
// fields, never from slice position.
func (idx *ruleIndex) candidates(dst []ruleSel, node *html.Node, pseudo string) []ruleSel {
	if idx == nil || node == nil {
		return dst
	}

	bucket := &idx.host
	if pseudo != "" {
		bucket = idx.pseudo[pseudo]
		if bucket == nil {
			return dst
		}
	}

	if id := node.Attribute("id"); id != "" {
		dst = append(dst, bucket.byID[id]...)
	}

	forEachClass(node.Attribute("class"), func(name string) {
		dst = append(dst, bucket.byClass[name]...)
	})

	if len(bucket.byTag) > 0 {
		dst = append(dst, bucket.byTag[strings.ToLower(node.Name)]...)
	}

	return append(dst, bucket.universal...)
}

// mayMatchPseudo reports whether any indexed ::before/::after selector for
// pseudo could match node. It mirrors the bucket lookups of candidates
// without building, sorting, or matching the candidate list, so callers can
// skip the generated-content cascade when the walk would find no rule. A
// true result still needs the full matcher (gates and combinators may
// reject); a false result means matchedRules would return no hits.
func (idx *ruleIndex) mayMatchPseudo(node *html.Node, pseudo string) bool {
	if idx == nil || node == nil || pseudo == "" {
		return false
	}

	bucket := idx.pseudo[pseudo]
	if bucket == nil {
		return false
	}

	if id := node.Attribute("id"); id != "" && len(bucket.byID[id]) > 0 {
		return true
	}

	if pseudoBucketMatchesClass(bucket, node) {
		return true
	}

	if len(bucket.byTag) > 0 && len(bucket.byTag[strings.ToLower(node.Name)]) > 0 {
		return true
	}

	return len(bucket.universal) > 0
}

// pseudoBucketMatchesClass reports whether any class on node selects into
// the pseudo bucket. Split from mayMatchPseudo so the bucket probe stays a
// flat sequence of early returns.
func pseudoBucketMatchesClass(bucket *ruleBucket, node *html.Node) bool {
	matched := false

	forEachClass(node.Attribute("class"), func(name string) {
		if !matched && len(bucket.byClass[name]) > 0 {
			matched = true
		}
	})

	return matched
}

// sortCandidates restores the sheet, rule, and selector order the linear walk
// used. Rule orders restart in every sheet, so the sheet ordinal comes first;
// without it a cross-sheet equal-order tie could flip.
func sortCandidates(cands []ruleSel) {
	slices.SortFunc(cands, func(left, right ruleSel) int {
		if left.sheet != right.sheet {
			return cmp.Compare(left.sheet, right.sheet)
		}

		if left.rule.Order != right.rule.Order {
			return cmp.Compare(left.rule.Order, right.rule.Order)
		}

		return cmp.Compare(left.sel, right.sel)
	})
}

// forEachClass calls visit for every class token in an HTML class attribute.
// It splits on the same Unicode whitespace the selector matcher uses, so the
// index never skips a class the matcher would accept.
func forEachClass(value string, visit func(string)) {
	for value != "" {
		value = strings.TrimLeftFunc(value, unicode.IsSpace)

		if value == "" {
			return
		}

		end := strings.IndexFunc(value, unicode.IsSpace)
		if end < 0 {
			visit(value)

			return
		}

		visit(value[:end])

		value = value[end:]
	}
}
