package app

import (
	"strings"
	"testing"
)

func TestClipProblemText(t *testing.T) {
	long := "cannot use compute(21) (value of type int) as string value in variable declaration"

	got := clipProblemText(long, problemTextMax)
	if n := len([]rune(got)); n != problemTextMax || !strings.HasSuffix(got, "…") {
		t.Fatalf("clip = %q (%d runes), want %d ending in an ellipsis", got, n, problemTextMax)
	}

	if got := clipProblemText("short\nmessage\twith   gaps", problemTextMax); got != "short message with gaps" {
		t.Fatalf("flatten = %q", got)
	}

	if got := clipProblemText("exactly", 7); got != "exactly" {
		t.Fatalf("fit = %q", got)
	}
}

func TestProblemRowKeepsFullMessageAndFile(t *testing.T) {
	app, _, _ := lspTestApp(t, map[string]string{"a.go": "bad\n"})

	app.openFile("a.go")
	pump(t, app, "diagnostic", func() bool { return len(app.lspm.diags["a.go"]) == 1 })

	m := app.lspm
	m.diags["a.go"][0].Message = strings.Repeat("long message ", 12)
	rows := app.lspViewData().Problems

	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}

	if n := len([]rune(rows[0].Text)); n > problemTextMax {
		t.Fatalf("display text has %d runes, want <= %d", n, problemTextMax)
	}

	if !strings.HasPrefix(rows[0].Full, "long message") || rows[0].Name != "a.go" {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestPopupHeadingHidesMarkers(t *testing.T) {
	lines := popupContentLines("## func Open\n\nbody text")
	if len(lines) == 0 || len(lines[0].Segments) != 1 {
		t.Fatalf("lines = %+v", lines)
	}

	seg := lines[0].Segments[0]
	if seg.Text != "func Open" || seg.Class != headingClass(2) {
		t.Fatalf("heading segment = %+v", seg)
	}

	// Non-heading Markdown still goes through the highlighter.
	if got := popupMarkdownSegments("plain `code`"); len(got) < 2 {
		t.Fatalf("inline code not styled: %+v", got)
	}
}
