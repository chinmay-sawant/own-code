package merge

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

const (
	standardConflict  = "before\n<<<<<<< HEAD\nours line\n=======\ntheirs line\n>>>>>>> feature\nafter\n"
	diff3Conflict     = "a\n<<<<<<< HEAD\nours\n||||||| base commit\nbase\n=======\ntheirs\n>>>>>>> branch\nz\n"
	addAddConflict    = "<<<<<<< HEAD\nours add\n||||||| merged common ancestors\n=======\ntheirs add\n>>>>>>> branch\n"
	multipleConflicts = "top\n<<<<<<< one\nleft one\n=======\nright one\n>>>>>>> one\n" +
		"mid\n<<<<<<< two\nleft two\n=======\nright two\n>>>>>>> two\nend\n"
	longMarkersConflict = "x\n<<<<<<<<<< HEAD long\nside\n|||||||| base mark\nold\n" +
		"==========\nother\n>>>>>>>>>> feature/long\ny\n"
	emptyLabelsConflict = "<<<<<<<\na\n|||||||\n=======\nb\n>>>>>>>\n"
	emptySidesConflict  = "<<<<<<<\n=======\n>>>>>>>\n"
	lookalikeConflict   = "head\n<<<<<<< HEAD\n<<<<<< six\n <<<<<<< indented\n" +
		"<<<<<<<no-space label\n====== six\n=====five\n=======no-space label\n" +
		">>>>>>>no-space label\n>>>>>> six\n|||||| six\nreal ours\n=======\n" +
		"real theirs\n>>>>>>> branch\ntail\n"
	lookalikeOurs = "head\n<<<<<< six\n <<<<<<< indented\n<<<<<<<no-space label\n" +
		"====== six\n=====five\n=======no-space label\n>>>>>>>no-space label\n" +
		">>>>>> six\n|||||| six\nreal ours\ntail\n"
	crlfConflict       = "before\r\n<<<<<<< HEAD\r\nours\r\n=======\r\ntheirs\r\n>>>>>>> branch\r\nafter\r\n"
	mixedConflict      = "top\r\n<<<<<<< HEAD\nours mixed\r\n=======\r\ntheirs\n>>>>>>> branch\r\nend\n"
	noTrailingConflict = "head\n<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> branch"
	addAddNoTrailing   = "<<<<<<< HEAD\nours\n||||||| base\n=======\ntheirs\n>>>>>>> branch"

	malformedUnterminated     = "before\n<<<<<<< HEAD\nours\n=======\ntheirs\n"
	malformedNoSeparator      = "before\n<<<<<<< HEAD\nours\n>>>>>>> branch\nafter\n"
	malformedNoClose          = "<<<<<<< HEAD\na\n||||||| base\nb\n=======\nc\n"
	malformedDiff3NoSeparator = "<<<<<<< HEAD\na\n||||||| base\nb\n>>>>>>> branch\n"
)

func TestParse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		crlf bool
		want []Conflict
	}{
		{
			name: "plain text",
			text: "plain\ntext\n",
		},
		{
			name: "standard two-way",
			text: standardConflict,
			want: []Conflict{{
				Index: 0, OursLabel: "HEAD", TheirsLabel: "feature",
				Ours: []string{"ours line"}, Theirs: []string{"theirs line"},
				StartLine: 2, EndLine: 6,
			}},
		},
		{
			name: "diff3",
			text: diff3Conflict,
			want: []Conflict{{
				Index: 0, OursLabel: "HEAD", TheirsLabel: "branch", BaseLabel: "base commit",
				Ours: []string{"ours"}, Base: []string{"base"}, Theirs: []string{"theirs"},
				StartLine: 2, EndLine: 8,
			}},
		},
		{
			name: "add/add with empty base",
			text: addAddConflict,
			want: []Conflict{{
				Index: 0, OursLabel: "HEAD", TheirsLabel: "branch",
				BaseLabel: "merged common ancestors",
				Ours:      []string{"ours add"}, Theirs: []string{"theirs add"},
				StartLine: 1, EndLine: 6,
			}},
		},
		{
			name: "multiple conflicts",
			text: multipleConflicts,
			want: []Conflict{
				{
					Index: 0, OursLabel: "one", TheirsLabel: "one",
					Ours: []string{"left one"}, Theirs: []string{"right one"},
					StartLine: 2, EndLine: 6,
				},
				{
					Index: 1, OursLabel: "two", TheirsLabel: "two",
					Ours: []string{"left two"}, Theirs: []string{"right two"},
					StartLine: 8, EndLine: 12,
				},
			},
		},
		{
			name: "long markers and labels",
			text: longMarkersConflict,
			want: []Conflict{{
				Index: 0, OursLabel: "HEAD long", TheirsLabel: "feature/long",
				BaseLabel: "base mark",
				Ours:      []string{"side"}, Base: []string{"old"}, Theirs: []string{"other"},
				StartLine: 2, EndLine: 8,
			}},
		},
		{
			name: "empty labels",
			text: emptyLabelsConflict,
			want: []Conflict{{
				Index: 0, Ours: []string{"a"}, Theirs: []string{"b"},
				StartLine: 1, EndLine: 6,
			}},
		},
		{
			name: "empty sides",
			text: emptySidesConflict,
			want: []Conflict{{
				Index: 0, StartLine: 1, EndLine: 3,
			}},
		},
		{
			name: "lookalike content",
			text: lookalikeConflict,
			want: []Conflict{{
				Index: 0, OursLabel: "HEAD", TheirsLabel: "branch",
				Ours: []string{
					"<<<<<< six", " <<<<<<< indented", "<<<<<<<no-space label",
					"====== six", "=====five", "=======no-space label",
					">>>>>>>no-space label", ">>>>>> six", "|||||| six", "real ours",
				},
				Theirs:    []string{"real theirs"},
				StartLine: 2, EndLine: 15,
			}},
		},
		{
			name: "crlf",
			text: crlfConflict,
			crlf: true,
			want: []Conflict{{
				Index: 0, OursLabel: "HEAD", TheirsLabel: "branch",
				Ours: []string{"ours"}, Theirs: []string{"theirs"},
				StartLine: 2, EndLine: 6,
			}},
		},
		{
			name: "mixed line endings",
			text: mixedConflict,
			crlf: true,
			want: []Conflict{{
				Index: 0, OursLabel: "HEAD", TheirsLabel: "branch",
				Ours: []string{"ours mixed"}, Theirs: []string{"theirs"},
				StartLine: 2, EndLine: 6,
			}},
		},
		{
			name: "no trailing newline",
			text: noTrailingConflict,
			want: []Conflict{{
				Index: 0, OursLabel: "HEAD", TheirsLabel: "branch",
				Ours: []string{"ours"}, Theirs: []string{"theirs"},
				StartLine: 2, EndLine: 6,
			}},
		},
		{
			name: "unterminated conflict",
			text: malformedUnterminated,
		},
		{
			name: "missing separator",
			text: malformedNoSeparator,
		},
		{
			name: "missing closing marker",
			text: malformedNoClose,
		},
		{
			name: "diff3 missing separator",
			text: malformedDiff3NoSeparator,
		},
		{
			name: "short markers",
			text: "<<<<<<\na\n======\nb\n>>>>>>\n",
		},
		{
			name: "indented start marker",
			text: " <<<<<<< HEAD\na\n=======\nb\n>>>>>>> branch\n",
		},
		{
			name: "labels without a space",
			text: "<<<<<<<HEAD\na\n=======\nb\n>>>>>>>branch\n",
		},
		{
			name: "start marker only",
			text: "<<<<<<<\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc := Parse(tc.text)
			if doc.Text != tc.text {
				t.Errorf("Text = %q, want input", doc.Text)
			}
			if doc.CRLF != tc.crlf {
				t.Errorf("CRLF = %v, want %v", doc.CRLF, tc.crlf)
			}
			if got := doc.Count(); got != len(tc.want) {
				t.Errorf("Count() = %d, want %d", got, len(tc.want))
			}
			if !reflect.DeepEqual(doc.Conflicts, tc.want) {
				t.Errorf("Conflicts = %#v, want %#v", doc.Conflicts, tc.want)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		text   string
		index  int
		choice Choice
		want   string
	}{
		{"standard keep", standardConflict, 0, Keep, standardConflict},
		{"standard ours", standardConflict, 0, Ours, "before\nours line\nafter\n"},
		{"standard theirs", standardConflict, 0, Theirs, "before\ntheirs line\nafter\n"},
		{"standard both", standardConflict, 0, Both, "before\nours line\ntheirs line\nafter\n"},
		{"standard base", standardConflict, 0, Base, "before\nafter\n"},

		{"diff3 keep", diff3Conflict, 0, Keep, diff3Conflict},
		{"diff3 ours", diff3Conflict, 0, Ours, "a\nours\nz\n"},
		{"diff3 theirs", diff3Conflict, 0, Theirs, "a\ntheirs\nz\n"},
		{"diff3 both", diff3Conflict, 0, Both, "a\nours\ntheirs\nz\n"},
		{"diff3 base", diff3Conflict, 0, Base, "a\nbase\nz\n"},

		{"add/add keep", addAddConflict, 0, Keep, addAddConflict},
		{"add/add ours", addAddConflict, 0, Ours, "ours add\n"},
		{"add/add theirs", addAddConflict, 0, Theirs, "theirs add\n"},
		{"add/add both", addAddConflict, 0, Both, "ours add\ntheirs add\n"},
		{"add/add empty base", addAddConflict, 0, Base, ""},

		{"multiple first ours", multipleConflicts, 0, Ours,
			"top\nleft one\nmid\n<<<<<<< two\nleft two\n=======\nright two\n>>>>>>> two\nend\n"},
		{"multiple first base", multipleConflicts, 0, Base,
			"top\nmid\n<<<<<<< two\nleft two\n=======\nright two\n>>>>>>> two\nend\n"},
		{"multiple first both", multipleConflicts, 0, Both,
			"top\nleft one\nright one\nmid\n<<<<<<< two\nleft two\n=======\nright two\n>>>>>>> two\nend\n"},
		{"multiple second theirs", multipleConflicts, 1, Theirs,
			"top\n<<<<<<< one\nleft one\n=======\nright one\n>>>>>>> one\nmid\nright two\nend\n"},
		{"multiple second base", multipleConflicts, 1, Base,
			"top\n<<<<<<< one\nleft one\n=======\nright one\n>>>>>>> one\nmid\nend\n"},
		{"multiple second both", multipleConflicts, 1, Both,
			"top\n<<<<<<< one\nleft one\n=======\nright one\n>>>>>>> one\nmid\nleft two\nright two\nend\n"},

		{"long markers ours", longMarkersConflict, 0, Ours, "x\nside\ny\n"},
		{"long markers base", longMarkersConflict, 0, Base, "x\nold\ny\n"},

		{"lookalike content ours", lookalikeConflict, 0, Ours, lookalikeOurs},

		{"empty labels ours", emptyLabelsConflict, 0, Ours, "a\n"},
		{"empty labels theirs", emptyLabelsConflict, 0, Theirs, "b\n"},
		{"empty sides keep", emptySidesConflict, 0, Keep, emptySidesConflict},
		{"empty sides ours", emptySidesConflict, 0, Ours, ""},
		{"empty sides theirs", emptySidesConflict, 0, Theirs, ""},
		{"empty sides both", emptySidesConflict, 0, Both, ""},
		{"empty sides base", emptySidesConflict, 0, Base, ""},

		{"crlf keep", crlfConflict, 0, Keep, crlfConflict},
		{"crlf ours", crlfConflict, 0, Ours, "before\r\nours\r\nafter\r\n"},
		{"crlf theirs", crlfConflict, 0, Theirs, "before\r\ntheirs\r\nafter\r\n"},
		{"crlf both", crlfConflict, 0, Both, "before\r\nours\r\ntheirs\r\nafter\r\n"},
		{"crlf base", crlfConflict, 0, Base, "before\r\nafter\r\n"},

		{"mixed ours", mixedConflict, 0, Ours, "top\r\nours mixed\r\nend\n"},
		{"mixed theirs", mixedConflict, 0, Theirs, "top\r\ntheirs\nend\n"},
		{"mixed both", mixedConflict, 0, Both, "top\r\nours mixed\r\ntheirs\nend\n"},

		{"no trailing keep", noTrailingConflict, 0, Keep, noTrailingConflict},
		{"no trailing ours", noTrailingConflict, 0, Ours, "head\nours"},
		{"no trailing theirs", noTrailingConflict, 0, Theirs, "head\ntheirs"},
		{"no trailing both", noTrailingConflict, 0, Both, "head\nours\ntheirs"},
		{"no trailing base", noTrailingConflict, 0, Base, "head\n"},

		{"empty base no trailing ours", addAddNoTrailing, 0, Ours, "ours"},
		{"empty base no trailing theirs", addAddNoTrailing, 0, Theirs, "theirs"},
		{"empty base no trailing both", addAddNoTrailing, 0, Both, "ours\ntheirs"},
		{"empty base no trailing base", addAddNoTrailing, 0, Base, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc := Parse(tc.text)
			got, err := doc.Resolve(tc.index, tc.choice)
			if err != nil {
				t.Fatalf("Resolve(%d, %v) error: %v", tc.index, tc.choice, err)
			}
			if got != tc.want {
				t.Errorf("Resolve(%d, %v) = %q, want %q", tc.index, tc.choice, got, tc.want)
			}
			if doc.Text != tc.text {
				t.Errorf("Resolve changed Document.Text to %q", doc.Text)
			}

			got, err = ResolveConflict(tc.text, tc.index, tc.choice)
			if err != nil {
				t.Fatalf("ResolveConflict(%d, %v) error: %v", tc.index, tc.choice, err)
			}
			if got != tc.want {
				t.Errorf("ResolveConflict(%d, %v) = %q, want %q", tc.index, tc.choice, got, tc.want)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		text    string
		index   int
		choice  Choice
		wantSub string
	}{
		{"negative index", standardConflict, -1, Ours, "out of range"},
		{"index at count", standardConflict, 1, Ours, "out of range"},
		{"no conflicts", "plain\n", 0, Ours, "out of range"},
		{"unterminated block", malformedUnterminated, 0, Ours, "out of range"},
		{"unknown choice", standardConflict, 0, Choice(99), "unknown choice"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc := Parse(tc.text)
			got, err := doc.Resolve(tc.index, tc.choice)
			if err == nil {
				t.Fatalf("Resolve(%d, %v) succeeded", tc.index, tc.choice)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantSub)
			}
			if got != tc.text {
				t.Errorf("text after error = %q, want original %q", got, tc.text)
			}

			got, err = ResolveConflict(tc.text, tc.index, tc.choice)
			if err == nil {
				t.Fatalf("ResolveConflict(%d, %v) succeeded", tc.index, tc.choice)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("ResolveConflict error = %q, want it to contain %q", err, tc.wantSub)
			}
			if got != tc.text {
				t.Errorf("ResolveConflict text after error = %q, want original %q", got, tc.text)
			}
		})
	}
}

func TestMalformedBlocksAreText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
	}{
		{"unterminated", malformedUnterminated},
		{"missing separator", malformedNoSeparator},
		{"missing closing marker", malformedNoClose},
		{"diff3 missing separator", malformedDiff3NoSeparator},
		{"short markers", "<<<<<<\na\n======\nb\n>>>>>>\n"},
		{"indented start", " <<<<<<< HEAD\na\n=======\nb\n>>>>>>> branch\n"},
		{"labels without a space", "<<<<<<<HEAD\na\n=======\nb\n>>>>>>>branch\n"},
		{"no start marker", "a\n=======\nb\n>>>>>>> branch\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc := Parse(tc.text)
			if doc.Count() != 0 {
				t.Fatalf("Count() = %d, want 0", doc.Count())
			}

			got, err := doc.Resolve(0, Keep)
			if err == nil {
				t.Fatal("Resolve(0, Keep) succeeded on a document without conflicts")
			}
			if got != tc.text {
				t.Errorf("text after error = %q, want original %q", got, tc.text)
			}
		})
	}
}

func TestHasMarkers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want bool
	}{
		{"empty", "", false},
		{"plain text", "plain text\n", false},
		{"indented start", " <<<<<<< HEAD\n", false},
		{"short markers", "<<<<<<\n======\n>>>>>>\n", false},
		{"start only", "<<<<<<< HEAD\n", true},
		{"separator only", "x\n=======\ny\n", true},
		{"base only", "||||||| base\n", true},
		{"closing only", ">>>>>>> branch\n", true},
		{"not at line start", "a <<<<<<< HEAD\n", false},
		{"label without a space", "<<<<<<<HEAD\n", false},
		{"standard conflict", standardConflict, true},
		{"malformed block", malformedUnterminated, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := HasMarkers(tc.text); got != tc.want {
				t.Errorf("HasMarkers(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestConflictLineSlices(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		text   string
		index  int
		ours   []string
		base   []string
		theirs []string
	}{
		{
			name: "diff3 empty base", text: addAddConflict, index: 0,
			ours: []string{"ours add"}, base: nil, theirs: []string{"theirs add"},
		},
		{
			name: "two-way empty base", text: standardConflict, index: 0,
			ours: []string{"ours line"}, base: nil, theirs: []string{"theirs line"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := Parse(tc.text).Conflicts[tc.index]
			if !slices.Equal(c.Ours, tc.ours) {
				t.Errorf("Ours = %q, want %q", c.Ours, tc.ours)
			}
			if !slices.Equal(c.Base, tc.base) {
				t.Errorf("Base = %q, want %q", c.Base, tc.base)
			}
			if !slices.Equal(c.Theirs, tc.theirs) {
				t.Errorf("Theirs = %q, want %q", c.Theirs, tc.theirs)
			}
		})
	}
}
