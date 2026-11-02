package app

import (
	"strings"
	"testing"
)

func spansOf(t *testing.T, query string, o searchOpts, line string) []matchSpan {
	t.Helper()

	m := compileMatcher(query, o)
	if !m.valid || m.Err != "" {
		t.Fatalf("compile %q: valid=%v err=%q", query, m.valid, m.Err)
	}

	return m.spansInLine([]rune(line))
}

func assertSpans(t *testing.T, query string, o searchOpts, line string, want []matchSpan) {
	t.Helper()

	got := spansOf(t, query, o, line)
	if len(got) != len(want) {
		t.Fatalf("%q in %q: spans=%v want=%v", query, line, got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%q in %q: spans=%v want=%v", query, line, got, want)
		}
	}
}

func TestMatcherLiteral(t *testing.T) {
	t.Parallel()

	insensitive := searchOpts{}
	assertSpans(t, "func", insensitive, "func main() {", []matchSpan{{0, 4}})
	assertSpans(t, "FUNC", insensitive, "func main() {", []matchSpan{{0, 4}})
	assertSpans(t, "  func  ", insensitive, "func func", []matchSpan{{0, 4}, {5, 9}})

	sensitive := searchOpts{Case: true}
	assertSpans(t, "FUNC", sensitive, "func main() {", nil)
	assertSpans(t, "func", sensitive, "func main() {", []matchSpan{{0, 4}})

	// Non-overlapping: "aa" in "aaa" matches once at 0.
	assertSpans(t, "aa", insensitive, "aaa", []matchSpan{{0, 2}})

	// Unicode folding and multibyte offsets.
	assertSpans(t, "strasse", insensitive, "STRASSE", []matchSpan{{0, 7}})
	assertSpans(t, "b", insensitive, "aöb", []matchSpan{{2, 3}})

	// Empty and blank queries match nothing.
	for _, q := range []string{"", "   "} {
		m := compileMatcher(q, insensitive)
		if !m.valid || !m.empty || m.Err != "" {
			t.Fatalf("%q: valid=%v empty=%v err=%q", q, m.valid, m.empty, m.Err)
		}
		if spans := m.spansInLine([]rune("func")); len(spans) != 0 {
			t.Fatalf("%q matched %v", q, spans)
		}
	}
}

func TestMatcherWholeWord(t *testing.T) {
	t.Parallel()

	word := searchOpts{WholeWord: true}
	assertSpans(t, "he", word, "he she", []matchSpan{{0, 2}})
	assertSpans(t, "he", word, "he she he'd", []matchSpan{{0, 2}, {7, 9}})
	assertSpans(t, "he", word, "the sheath", nil)
	assertSpans(t, "he", word, "he-he", []matchSpan{{0, 2}, {3, 5}})
	assertSpans(t, "_x", word, "_x", []matchSpan{{0, 2}})
	assertSpans(t, "x", word, "_x", nil)
	assertSpans(t, "naïve", word, "naïve naïveté", []matchSpan{{0, 5}})

	// Whole word without the flag still matches inside words.
	assertSpans(t, "he", searchOpts{}, "the", []matchSpan{{1, 3}})
}

func TestMatcherRegex(t *testing.T) {
	t.Parallel()

	re := searchOpts{Regex: true}
	assertSpans(t, `f.n`, re, "afunc", []matchSpan{{1, 4}})
	assertSpans(t, `(\w+)@(\w+)`, re, "a@b c", []matchSpan{{0, 3}})
	// Case-insensitive by default.
	assertSpans(t, `FUNC`, re, "func", []matchSpan{{0, 4}})
	assertSpans(t, `FUNC`, searchOpts{Regex: true, Case: true}, "func", nil)
	assertSpans(t, `^func`, re, "  func", nil)
	assertSpans(t, `^func`, re, "func x", []matchSpan{{0, 4}})

	// Whole word post-filters regex matches.
	assertSpans(t, `\w+`, searchOpts{Regex: true, WholeWord: true}, "a b", []matchSpan{{0, 1}, {2, 3}})
	assertSpans(t, `b`, searchOpts{Regex: true, WholeWord: true}, "abc", nil)

	// Regex queries are never trimmed.
	m := compileMatcher(` foo`, re)
	if !m.valid || m.Err != "" {
		t.Fatalf("leading space: valid=%v err=%q", m.valid, m.Err)
	}
	if spans := m.spansInLine([]rune("x foo")); len(spans) != 1 || spans[0] != (matchSpan{1, 5}) {
		t.Fatalf("leading space spans=%v", spans)
	}

	// Invalid patterns carry an inline error and match nothing.
	bad := compileMatcher(`(unclosed`, re)
	if bad.valid || bad.Err == "" {
		t.Fatalf("invalid: valid=%v err=%q", bad.valid, bad.Err)
	}
	if spans := bad.spansInLine([]rune("(unclosed")); len(spans) != 0 {
		t.Fatalf("invalid matched %v", spans)
	}

	// Over-long patterns fail fast.
	long := compileMatcher(strings.Repeat("a", matcherQueryCap+1), re)
	if long.Err == "" {
		t.Fatal("over-long pattern has no error")
	}
}

func TestMatcherExpansion(t *testing.T) {
	t.Parallel()

	literal := compileMatcher("a+b", searchOpts{})
	if got := literal.expandReplacement("a+b", "$1-$2"); got != "$1-$2" {
		t.Fatalf("literal verbatim = %q", got)
	}

	re := compileMatcher(`(\w+)@(\w+)`, searchOpts{Regex: true})
	if got := re.expandReplacement("a@b", "$2@$1"); got != "b@a" {
		t.Fatalf("groups = %q", got)
	}
	if got := re.expandReplacement("a@b", "[$0]"); got != "[a@b]" {
		t.Fatalf("$0 = %q", got)
	}

	named := compileMatcher(`(?P<user>\w+)@\w+`, searchOpts{Regex: true})
	if got := named.expandReplacement("amy@x", "<$user>"); got != "<amy>" {
		t.Fatalf("named = %q", got)
	}

	dollar := compileMatcher(`x`, searchOpts{Regex: true})
	if got := dollar.expandReplacement("x", "$$"); got != "$" {
		t.Fatalf("escape = %q", got)
	}
}
