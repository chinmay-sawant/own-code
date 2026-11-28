package agentreview

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func lines(n ...string) string { return strings.Join(n, "\n") + "\n" }

func TestReviewDiffSplitsIndependentHunks(t *testing.T) {
	base := lines("a", "b", "c", "d", "e", "f", "g", "h", "i", "j")
	prop := lines("a", "B", "c", "d", "e", "f", "g", "h", "I", "j", "k")
	hs := Diff(base, prop)
	if len(hs) != 3 {
		t.Fatalf("want 3 hunks, got %d: %+v", len(hs), hs)
	}
	got := base
	var err error
	for _, h := range hs {
		if got, err = Apply(got, h); err != nil {
			t.Fatal(err)
		}
	}
	if got != prop {
		t.Fatalf("applying all hunks = %q", got)
	}
}

func TestReviewApplyOnlyAgentLinesPreservesUserEdits(t *testing.T) {
	base := lines("one", "two", "three", "four", "five", "six", "seven", "eight")
	prop := lines("one", "TWO", "three", "four", "five", "six", "seven", "eight")
	user := lines("USER TOP", "one", "two", "three", "four", "five", "six", "seven", "eight", "USER END")
	hs := Diff(base, prop)
	out, err := Apply(user, hs[0])
	if err != nil {
		t.Fatal(err)
	}
	want := lines("USER TOP", "one", "TWO", "three", "four", "five", "six", "seven", "eight", "USER END")
	if out != want {
		t.Fatalf("got %q", out)
	}
}

func TestReviewRevertKeepsAdjacentUserChange(t *testing.T) {
	base := lines("a", "b", "c", "d")
	prop := lines("a", "B", "c", "d")
	h := Diff(base, prop)[0]
	applied, _ := Apply(base, h)
	userEdited := strings.Replace(applied, "c\n", "c user\n", 1)
	out, err := Revert(userEdited, h)
	if err != nil {
		t.Fatal(err)
	}
	if out != lines("a", "b", "c user", "d") {
		t.Fatalf("got %q", out)
	}
}

func TestReviewApplyConflictWhenUserEditedSameLine(t *testing.T) {
	base := lines("a", "b", "c")
	h := Diff(base, lines("a", "B", "c"))[0]
	if _, err := Apply(lines("a", "b user", "c"), h); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
}

func TestReviewInsertionAndDeletionHunks(t *testing.T) {
	base := lines("a", "b", "c", "d", "e")
	prop := lines("a", "b", "NEW", "c", "d")
	hs := Diff(base, prop)
	if len(hs) != 2 {
		t.Fatalf("hunks %d", len(hs))
	}
	got := base
	for _, h := range hs {
		var err error
		if got, err = Apply(got, h); err != nil {
			t.Fatal(err)
		}
	}
	if got != prop {
		t.Fatalf("got %q", got)
	}
	// Revert insertion only.
	back, err := Revert(got, hs[0])
	if err != nil || strings.Contains(back, "NEW") {
		t.Fatalf("revert insert: %q %v", back, err)
	}
}

func TestReviewNoTrailingNewlineAndEmptyBase(t *testing.T) {
	hs := Diff("", "x\ny")
	if len(hs) != 1 {
		t.Fatal(len(hs))
	}
	out, err := Apply("", hs[0])
	if err != nil || out != "x\ny" {
		t.Fatalf("%q %v", out, err)
	}
	hs = Diff("a\nb", "a\nb\n")
	out, err = Apply("a\nb", hs[0])
	if err != nil || out != "a\nb\n" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestReviewOverlappingSessionsOnOneFile(t *testing.T) {
	base := lines("a", "b", "c", "d", "e", "f", "g", "h")
	r := NewRegistry()
	r.Put("s1", "f.txt", base, lines("a", "B1", "c", "d", "e", "f", "g", "h"))
	r.Put("s2", "f.txt", base, lines("a", "B2", "c", "d", "e", "f", "g", "H2"))
	cur := base
	var err error
	id1 := r.Get("s1", "f.txt").Hunks[0].ID
	if cur, err = r.Accept("s1", "f.txt", id1, cur); err != nil {
		t.Fatal(err)
	}
	// s2's overlapping hunk now conflicts; its disjoint hunk still applies.
	var overlap, disjoint string
	for _, h := range r.Get("s2", "f.txt").Hunks {
		if h.OldStart == 1 {
			overlap = h.ID
		} else {
			disjoint = h.ID
		}
	}
	if _, err = r.Accept("s2", "f.txt", overlap, cur); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if st := r.Get("s2", "f.txt").States; st[0] != Pending {
		t.Fatal("conflict must leave hunk pending")
	}
	if cur, err = r.Accept("s2", "f.txt", disjoint, cur); err != nil {
		t.Fatal(err)
	}
	if cur != lines("a", "B1", "c", "d", "e", "f", "g", "H2") {
		t.Fatalf("got %q", cur)
	}
}

func TestReviewStateTransitions(t *testing.T) {
	base := lines("a", "b", "c", "d", "e", "f", "g", "h", "i")
	prop := lines("A", "b", "c", "d", "e", "f", "g", "h", "I")
	r := NewRegistry()
	fr := r.Put("s", "x", base, prop)
	if len(fr.Hunks) != 2 {
		t.Fatal(len(fr.Hunks))
	}
	cur, err := r.Accept("s", "x", fr.Hunks[0].ID, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Reject("s", "x", fr.Hunks[1].ID); err != nil {
		t.Fatal(err)
	}
	if p, a, rj := fr.Counts(); p != 0 || a != 1 || rj != 1 {
		t.Fatalf("%d %d %d", p, a, rj)
	}
	if cur != lines("A", "b", "c", "d", "e", "f", "g", "h", "i") {
		t.Fatalf("rejected hunk changed text: %q", cur)
	}
	if _, err := r.Accept("s", "x", fr.Hunks[0].ID, cur); err == nil {
		t.Fatal("double accept must fail")
	}
	if cur, err = r.Revert("s", "x", fr.Hunks[0].ID, cur); err != nil || cur != base {
		t.Fatalf("%q %v", cur, err)
	}
	if err := r.Reject("s", "x", fr.Hunks[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Revert("s", "x", fr.Hunks[0].ID, cur); err == nil {
		t.Fatal("revert of non-accepted must fail")
	}
	r.Drop("s") // cancellation forgets state, touches no file
	if r.Get("s", "x") != nil {
		t.Fatal("drop failed")
	}
}

func TestReviewUnifiedOmitsDecidedHunks(t *testing.T) {
	base := lines("a", "b", "c", "d", "e", "f", "g", "h", "i")
	r := NewRegistry()
	fr := r.Put("s", "x", base, lines("A", "b", "c", "d", "e", "f", "g", "h", "I"))
	_ = r.Reject("s", "x", fr.Hunks[0].ID)
	u := fr.Unified(false)
	if strings.Contains(u, "+A") || !strings.Contains(u, "+I") || !strings.Contains(u, "--- a/x") {
		t.Fatalf("%s", u)
	}
}

func TestReviewMapPathRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	_ = os.WriteFile(filepath.Join(outside, "f"), []byte("x"), 0o644)
	for _, bad := range []string{"", "../x", "a/../../x", "/etc/passwd", "link/f", "link", ".git/config", "a\x00b", `..\x`, "C:/x"} {
		if _, _, err := MapPath(root, bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	abs, rel, err := MapPath(root, "sub/new.go")
	if err != nil || rel != "sub/new.go" || !strings.HasSuffix(abs, filepath.FromSlash("sub/new.go")) {
		t.Fatalf("%q %q %v", abs, rel, err)
	}
}

func TestReviewClassify(t *testing.T) {
	big := make([]byte, MaxReviewBytes+1)
	cases := []struct {
		be, ne     bool
		b, n       []byte
		from       string
		want       Kind
		reviewable bool
	}{
		{true, true, []byte("a"), []byte("b"), "", Modify, true},
		{false, true, nil, []byte("b"), "", Create, true},
		{true, false, []byte("a"), nil, "", Delete, true},
		{true, true, []byte("a"), []byte("a"), "old.go", Rename, true},
		{true, true, []byte("a"), []byte{0, 1}, "", Binary, false},
		{true, true, []byte("a"), []byte{0xff, 0xfe}, "", Binary, false},
		{true, true, big, []byte("a"), "", Oversized, false},
	}
	for i, c := range cases {
		k := Classify(c.be, c.ne, c.b, c.n, c.from)
		if k != c.want || k.Reviewable() != c.reviewable {
			t.Errorf("case %d: %v", i, k)
		}
	}
}

func TestReviewCaptureBaselineAttributesUserChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.email=a@b", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "-q")
	for _, n := range []string{"staged.txt", "unstaged.txt", "clean.txt"} {
		_ = os.WriteFile(filepath.Join(root, n), []byte("v1\n"), 0o644)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	_ = os.WriteFile(filepath.Join(root, "staged.txt"), []byte("v2\n"), 0o644)
	run("add", "staged.txt")
	_ = os.WriteFile(filepath.Join(root, "unstaged.txt"), []byte("v2\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("u\n"), 0o644)
	b, err := CaptureBaseline(context.Background(), root, []string{"clean.txt"})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"staged.txt", "unstaged.txt", "untracked.txt", "clean.txt"} {
		if !b.UserOwned(n) {
			t.Errorf("%s should be user owned", n)
		}
	}
	if !b.StagedByUser("staged.txt") || b.StagedByUser("unstaged.txt") {
		t.Error("staged attribution wrong")
	}
	if b.Files["staged.txt"].Hash == "" {
		t.Error("hash missing")
	}
	if err := b.RecordFile(root, "../x"); err == nil {
		t.Error("RecordFile must reject traversal")
	}
}

func TestWholeFileUnitHasNoHunksAndResolvesOnce(t *testing.T) {
	r := NewRegistry()
	fr, err := r.PutWhole("s", "a.go", Delete, "")
	if err != nil || len(fr.Hunks) != 0 || !Delete.Whole() || !Rename.Whole() || Modify.Whole() {
		t.Fatalf("%v %+v", err, fr)
	}
	if _, err := r.PutWhole("s", "b.go", Modify, ""); err == nil {
		t.Fatal("modify is not a whole-file unit")
	}
	if _, err := r.Accept("s", "a.go", "h", ""); err == nil {
		t.Fatal("per-hunk accept must not apply to a whole-file unit")
	}
	if err := r.ResolveWhole("s", "a.go", Rejected); err != nil {
		t.Fatal(err)
	}
	if err := r.ResolveWhole("s", "a.go", Accepted); err == nil {
		t.Fatal("already resolved")
	}
}
