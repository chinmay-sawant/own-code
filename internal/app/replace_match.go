package app

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// matcherQueryCap bounds a regex query in runes. RE2 itself is safe, but a
// huge pattern typed into the find or search box should fail fast with an
// inline error instead of scanning the workspace.
const matcherQueryCap = 500

// searchOpts is the shared match configuration for the in-file find bar and
// the workspace content search. Both surfaces build one from their toggles
// and delegate to compileMatcher, so literal, regex, and whole-word matching
// behave the same everywhere.
type searchOpts struct {
	Case      bool
	Regex     bool
	WholeWord bool
}

// matchSpan is one non-overlapping match in line-rune coordinates.
type matchSpan struct {
	Start int
	End   int
}

// matcher is one compiled query. A matcher with Err set matches nothing; the
// caller surfaces Err inline and disables Replace until the query compiles.
type matcher struct {
	opts    searchOpts
	literal []rune
	re      *regexp.Regexp
	valid   bool
	Err     string
	empty   bool
}

// compileMatcher builds a matcher for query. Literal queries keep the
// historical semantics: surrounding whitespace is trimmed and an empty query
// matches nothing. Regex queries are Go RE2 and are never trimmed; a
// case-insensitive pattern compiles with a (?i) prefix.
func compileMatcher(query string, o searchOpts) *matcher {
	m := &matcher{opts: o}

	if o.Regex {
		if len([]rune(query)) > matcherQueryCap {
			m.Err = "Pattern is too long (over 500 characters)."
			return m
		}

		if query == "" {
			m.empty = true
			m.valid = true
			return m
		}

		pattern := query
		if !o.Case {
			pattern = "(?i)" + pattern
		}

		re, err := regexp.Compile(pattern)
		if err != nil {
			m.Err = "Invalid pattern: " + err.Error()
			return m
		}

		m.re = re
		m.valid = true
		return m
	}

	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		m.empty = true
		m.valid = true
		return m
	}

	m.literal = []rune(trimmed)
	m.valid = true
	return m
}

// fileSearchOpts builds the in-file find bar options from the app toggles.
func (a *App) fileSearchOpts() searchOpts {
	return searchOpts{Case: a.findCase, Regex: a.replaceRegex, WholeWord: a.replaceWholeWord}
}

// workspaceSearchOpts builds the workspace search options from the app toggles.
func (a *App) workspaceSearchOpts() searchOpts {
	return searchOpts{Case: a.searchCase, Regex: a.searchRegex, WholeWord: a.searchWholeWord}
}

// spansInLine returns the non-overlapping matches in one line's runes.
func (m *matcher) spansInLine(line []rune) []matchSpan {
	if m == nil || !m.valid || m.empty {
		return nil
	}

	if m.re != nil {
		return m.regexSpans(line)
	}

	return m.literalSpans(line)
}

// literalSpans scans for the literal query. Case-insensitive matching folds
// one rune at a time under Unicode simple folding, the same rule the old
// workspace scan used. Matches never overlap.
func (m *matcher) literalSpans(line []rune) []matchSpan {
	needle := m.literal
	if len(needle) == 0 || len(needle) > len(line) {
		return nil
	}

	var out []matchSpan

	for start := 0; start+len(needle) <= len(line); {
		if !matchRunesAt(line[start:start+len(needle)], needle, m.opts.Case) {
			start++
			continue
		}

		end := start + len(needle)
		if m.opts.WholeWord && !wordBoundary(line, start, end) {
			start++
			continue
		}

		out = append(out, matchSpan{Start: start, End: end})
		start = end
	}

	return out
}

// regexSpans runs the compiled RE2 over one line and converts the byte
// offsets to rune offsets. Whole-word mode post-filters by word boundaries.
func (m *matcher) regexSpans(line []rune) []matchSpan {
	if len(line) == 0 {
		// A pattern like ^$ or x* can match an empty line.
		if loc := m.re.FindStringIndex(""); loc != nil && loc[0] == 0 && loc[1] == 0 {
			if !m.opts.WholeWord || wordBoundary(line, 0, 0) {
				return []matchSpan{{Start: 0, End: 0}}
			}
		}
		return nil
	}

	text := string(line)
	byteToRune := make([]int, len(text)+1)
	runeIndex := 0
	for i := range text {
		byteToRune[i] = runeIndex
		runeIndex++
	}
	byteToRune[len(text)] = runeIndex

	var out []matchSpan
	for _, loc := range m.re.FindAllStringIndex(text, -1) {
		start, end := byteToRune[loc[0]], byteToRune[loc[1]]
		if m.opts.WholeWord && !wordBoundary(line, start, end) {
			continue
		}
		out = append(out, matchSpan{Start: start, End: end})
		// Guard against a zero-width loop: FindAllStringIndex already
		// advances past empty matches, so no extra handling is needed.
	}

	return out
}

// expandReplacement renders the replacement for one matched substring.
// Literal mode inserts the replacement verbatim; regex mode expands $1 and
// $name references against the matched substring only. A lone $ that names
// no group expands to "" under RE2 rules; use $$ for a literal dollar.
func (m *matcher) expandReplacement(matched, replacement string) string {
	if m == nil || !m.valid || m.re == nil {
		return replacement
	}

	return m.re.ReplaceAllString(matched, replacement)
}

// matchRunesAt reports whether candidate starts with the needle.
func matchRunesAt(candidate, needle []rune, caseSensitive bool) bool {
	for i, r := range needle {
		if caseSensitive {
			if candidate[i] != r {
				return false
			}
			continue
		}
		if !runeEqualFold(candidate[i], r) {
			return false
		}
	}
	return true
}

// runeEqualFold compares one rune under Unicode simple folding.
func runeEqualFold(a, b rune) bool {
	if a == b {
		return true
	}

	for r := unicode.SimpleFold(a); r != a; r = unicode.SimpleFold(r) {
		if r == b {
			return true
		}
	}

	return false
}

// isWordChar reports whether r joins a whole-word token: a letter, digit,
// or underscore, including Unicode letters and digits.
func isWordChar(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// wordBoundary reports whether [start,end) in line is delimited by word
// boundaries on both sides.
func wordBoundary(line []rune, start, end int) bool {
	if start > 0 && isWordChar(line[start-1]) {
		return false
	}
	if end < len(line) && isWordChar(line[end]) {
		return false
	}
	// A zero-width match is a word only when it sits between a word char
	// and a non-word char.
	if start == end {
		before := start > 0 && isWordChar(line[start-1])
		after := end < len(line) && isWordChar(line[end])
		return before != after
	}
	return true
}

// runeLen counts the runes in s without importing utf8 at call sites that
// already import this file's helpers.
func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}
