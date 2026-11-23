package app

import (
	"context"
	"testing"
)

func TestPlanLineOps(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		action, text  string
		anchor, caret int
		want          string
		wantCaret     int
		noop          bool
	}{
		{"up", actionMoveLineUp, "a\nb\nc\n", 3, 3, "b\na\nc\n", 1, false},
		{"up top", actionMoveLineUp, "a\nb\n", 0, 0, "", 0, true},
		{"down", actionMoveLineDown, "a\nb\nc\n", 1, 1, "b\na\nc\n", 3, false},
		{"down bottom", actionMoveLineDown, "a\nb\n", 2, 2, "", 0, true},
		{"down block", actionMoveLineDown, "a\nb\nc\nd\n", 0, 3, "c\na\nb\nd\n", 5, false},
		{"dup", actionDuplicateLn, "ab\ncd\n", 1, 1, "ab\nab\ncd\n", 4, false},
		{"dup block", actionDuplicateLn, "a\nb\nc\n", 0, 3, "a\nb\na\nb\nc\n", 7, false},
		{"delete mid", actionDeleteLine, "a\nb\nc\n", 3, 3, "a\nc\n", 3, false},
		{"delete last", actionDeleteLine, "a\nb\n", 2, 2, "a\n", 0, false},
		{"delete only", actionDeleteLine, "a\n", 0, 0, "", 0, false},
		{"join", actionJoinLines, "a  \n   b\nc\n", 0, 0, "a b\nc\n", 1, false},
		{"join empty side", actionJoinLines, "a\n\nc\n", 0, 0, "a\nc\n", 1, false},
		{"join last", actionJoinLines, "a\nb\n", 2, 2, "", 0, true},
		{"join selection", actionJoinLines, "a\nb\nc\nd\n", 0, 5, "a b c\nd\n", 3, false},
	}

	for _, tc := range cases {
		start, end := tc.anchor, tc.caret
		if start > end {
			start, end = end, start
		}

		le, ok := planLineOp(tc.action, tc.text, start, end, tc.anchor, tc.caret)
		if ok == tc.noop {
			t.Fatalf("%s: ok=%v", tc.name, ok)
		}

		if tc.noop {
			continue
		}

		lines, _ := splitBufferLines(tc.text)
		got := spliceLines(lines, le.lo, le.hi, le.repl)
		gotText := ""

		for _, l := range got {
			gotText += l + "\n"
		}

		if gotText != tc.want || le.caret != tc.wantCaret {
			t.Fatalf("%s: text=%q caret=%d, want %q %d", tc.name, gotText, le.caret, tc.want, tc.wantCaret)
		}
	}
}

func TestLineOpsUndoAndKeymap(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	enterEditAtFirstLine(t, app)

	app.edit.SetText("one\ntwo\nthree\n")
	app.editCaret, app.editAnchor = 0, 0

	before := len(app.editUndo)
	if err := app.runLineOp(ctx, actionMoveLineDown); err != nil {
		t.Fatal(err)
	}

	if got := app.edit.Text(); got != "two\none\nthree\n" {
		t.Fatalf("text = %q", got)
	}

	if len(app.editUndo) != before+1 {
		t.Fatalf("undo points grew by %d, want 1", len(app.editUndo)-before)
	}

	// The default chord reaches the action through editKey.
	if err := app.editKey(ctx, "alt+arrowup"); err != nil {
		t.Fatal(err)
	}

	if got := app.edit.Text(); got != "one\ntwo\nthree\n" {
		t.Fatalf("after Alt+Up text = %q", got)
	}

	app.ctrl, app.shift = true, true
	if err := app.editKey(ctx, "d"); err != nil {
		t.Fatal(err)
	}

	app.ctrl, app.shift = false, false

	if got := app.edit.Text(); got != "one\none\ntwo\nthree\n" {
		t.Fatalf("after Ctrl+Shift+D text = %q", got)
	}
}

func TestLineOpChordsUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]string{}

	for _, e := range keymapCatalog() {
		norm, _ := normalizeChord(e.Default)
		if prev, dup := seen[norm]; dup && e.Action != prev {
			// Single-letter modes legitimately share keys; Ctrl/Alt chords must not.
			if len(norm) > 1 {
				t.Fatalf("chord %s used by %s and %s", norm, prev, e.Action)
			}
		}

		seen[norm] = e.Action
	}
}
