//nolint:cyclop,wsl,mnd // fallback rule parser
package css

import "strings"

// PositionTryRule is one @position-try block. Name includes the leading --
// (for example "--above"). Area is the raw position-area value stored on the
// block, lowercased ("", when absent). Top, Right, Bottom, and Left keep raw
// inset overrides from the same block ("", when absent). Only position-area
// and inset overrides are stored; every other descriptor is ignored. Timeline
// at-rules (@scroll-timeline and friends) are never parsed here and stay
// skipped by skipAtRule.
//
// Pinned spec: css-anchor-position-1, @position-try rule
// (https://drafts.csswg.org/css-anchor-position-1/#fallback-rule).
type PositionTryRule struct {
	Name   string
	Area   string
	Top    string
	Right  string
	Bottom string
	Left   string
}

// PositionTry returns the last @position-try rule with the given dashed-ident
// name, or nil when no rule declares it. Later rules win, matching the
// cascade order of same-name registrations.
func (s *Stylesheet) PositionTry(name string) *PositionTryRule {
	if s == nil {
		return nil
	}

	var found *PositionTryRule

	for i := range s.PositionTries {
		if s.PositionTries[i].Name == name {
			found = &s.PositionTries[i]
		}
	}

	return found
}

// parsePositionTryRule consumes one @position-try block and stores its
// position-area or inset overrides. A prelude that is not a single
// dashed-ident drops the block without storing. An unbalanced block returns
// the parse error from takeBlock.
func parsePositionTryRule(src string, str *Stylesheet) (string, error) {
	open := strings.IndexByte(src, '{')
	if open < 0 {
		return skipAtRule(src)
	}

	prelude := strings.TrimSpace(src[len("@position-try"):open])
	fields := strings.Fields(prelude)

	if len(fields) != 1 || !isDashedIdent(fields[0]) {
		block, rest, err := takeBlock(src, open)
		if err != nil {
			return "", err
		}
		_ = block

		return rest, nil
	}

	name := strings.ToLower(fields[0])

	block, rest, err := takeBlock(src, open)
	if err != nil {
		return "", err
	}

	rule := PositionTryRule{Name: name} //nolint:exhaustruct // absent descriptors stay empty

	for _, decl := range parseDeclarations(block) {
		switch decl.Prop {
		case "position-area":
			if v := strings.ToLower(strings.TrimSpace(decl.Value)); v != "" {
				rule.Area = v
			}
		case "top":
			rule.Top = strings.TrimSpace(decl.Value)
		case "right":
			rule.Right = strings.TrimSpace(decl.Value)
		case "bottom":
			rule.Bottom = strings.TrimSpace(decl.Value)
		case "left":
			rule.Left = strings.TrimSpace(decl.Value)
		case "inset":
			applyPositionTryInset(&rule, decl.Value)
		}
	}

	str.PositionTries = append(str.PositionTries, rule)

	return rest, nil
}

// applyPositionTryInset expands the inset shorthand into the rule's side
// overrides. One value sets all sides, two set vertical then horizontal,
// three set top, horizontal, then bottom, and four set each side clockwise
// from the top. Garbage keeps the sides it had.
func applyPositionTryInset(rule *PositionTryRule, value string) {
	fields := strings.Fields(strings.TrimSpace(value))

	switch len(fields) {
	case 1:
		rule.Top, rule.Right, rule.Bottom, rule.Left = fields[0], fields[0], fields[0], fields[0]
	case 2:
		rule.Top, rule.Right, rule.Bottom, rule.Left = fields[0], fields[1], fields[0], fields[1]
	case 3:
		rule.Top, rule.Right, rule.Bottom, rule.Left = fields[0], fields[1], fields[2], fields[1]
	case 4:
		rule.Top, rule.Right, rule.Bottom, rule.Left = fields[0], fields[1], fields[2], fields[3]
	}
}

// isDashedIdent reports whether s is a single dashed-ident token: it starts
// with --, is longer than the prefix, and carries no value separators.
func isDashedIdent(s string) bool {
	if !strings.HasPrefix(s, "--") || len(s) <= 2 {
		return false
	}

	return !strings.ContainsAny(s, " \t\r\n,;{}()")
}
