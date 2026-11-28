package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

const uniSample = "héllo 世界 😀 é ñ̃\r\nline2 日本語\r\n"

func TestUnicodeSelectionSource(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := "first\r\n" + uniSample + "last"
	runes := []rune(doc)
	from := strings.Index(doc, "世")
	from = len([]rune(doc[:from]))
	snap, err := captureAgentContext(root, "a.txt", doc, from, from+2)
	if err != nil {
		t.Fatal(err)
	}
	if snap.SelectionText != "世界" || !strings.Contains(snap.Text, "\r\n") || !utf8.ValidString(snap.Text) {
		t.Fatalf("snap %q sel %q", snap.Text, snap.SelectionText)
	}
	if got := []rune(snap.Text)[snap.SelectionFrom:snap.SelectionTo]; string(got) != "世界" {
		t.Fatalf("rune offsets wrong: %q", string(got))
	}
	if snap.StartLine > 2 || snap.EndLine < 2 || snap.EndLine > 4 {
		t.Fatalf("CRLF lines: %d-%d", snap.StartLine, snap.EndLine)
	}
	// Selection of a base letter + combining mark keeps both runes.
	i := len([]rune(doc[:strings.Index(doc, "é")]))
	snap, err = captureAgentContext(root, "a.txt", doc, i, i+2)
	if err != nil || snap.SelectionText != "é" {
		t.Fatalf("combining: %q %v", snap.SelectionText, err)
	}
	_ = runes
	// Truncation: one huge multibyte line with a selection in the middle.
	big := strings.Repeat("世😀é", 20000)
	br := []rune(big)
	snap, err = captureAgentContext(root, "a.txt", big, 30000, 30010)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Truncated || !utf8.ValidString(snap.Text) || strings.ContainsRune(snap.Text, utf8.RuneError) || len(snap.Text) > maxOpenCodeContextBytes {
		t.Fatalf("truncation broke runes: truncated=%v len=%d", snap.Truncated, len(snap.Text))
	}
	if string(br[30000:30010]) != snap.SelectionText {
		t.Fatal("selection changed")
	}
}

func TestUnicodeFileSource(t *testing.T) {
	big := strings.Repeat("世😀é́", 20000) // over the per-file byte limit, no newline
	a := mentionTestApp(t, map[string]string{"u.txt": uniSample, "big.txt": big, "crlf.txt": strings.Repeat("日本\r\n", 600)})
	root, _ := a.openCodeChipRoot()
	chip, err := a.captureOpenCodeFileChip(root, "u.txt")
	if err != nil || chip.Text != uniSample || chip.Truncated {
		t.Fatalf("u.txt: %q %v", chip.Text, err)
	}
	for _, name := range []string{"big.txt", "crlf.txt"} {
		chip, err = a.captureOpenCodeFileChip(root, name)
		if err != nil {
			t.Fatal(name, err)
		}
		if !chip.Truncated || !utf8.ValidString(chip.Text) || strings.ContainsRune(chip.Text, utf8.RuneError) || len(chip.Text) > maxOpenCodeChipBytes {
			t.Fatalf("%s: truncated=%v valid=%v len=%d", name, chip.Truncated, utf8.ValidString(chip.Text), len(chip.Text))
		}
		if blk := renderOpenCodeContextBlock([]openCodeChip{chip}); !utf8.ValidString(blk) || !strings.Contains(blk, "TRUNCATED") {
			t.Fatalf("%s: block invalid or missing marker", name)
		}
	}
}

func TestUnicodeDiffSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	a := mentionTestApp(t, map[string]string{"a.txt": "one\r\n"})
	root, _ := a.openCodeChipRoot()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "-c", "core.autocrlf=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", "-A")
	run("commit", "-q", "-m", "i")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte(uniSample), 0o600); err != nil {
		t.Fatal(err)
	}
	chip, err := a.captureOpenCodeDiffChip(root)
	if err != nil || !strings.Contains(chip.Text, "+héllo 世界 😀 é") || !strings.Contains(chip.Text, "\r") || !utf8.ValidString(chip.Text) {
		t.Fatalf("diff: %q %v", chip.Text, err)
	}
	// Oversized multibyte diff: cut must land on a rune boundary.
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte(strings.Repeat("世😀é́", 30000)), 0o600); err != nil {
		t.Fatal(err)
	}
	chip, err = a.captureOpenCodeDiffChip(root)
	if err != nil {
		t.Fatal(err)
	}
	if !chip.Truncated || !utf8.ValidString(chip.Text) || strings.ContainsRune(chip.Text, utf8.RuneError) || len(chip.Text) > maxOpenCodeDiffBytes {
		t.Fatalf("diff truncation: truncated=%v valid=%v len=%d", chip.Truncated, utf8.ValidString(chip.Text), len(chip.Text))
	}
}

func TestUnicodeProblemsSource(t *testing.T) {
	line := openCodeProblemLine(lspProblem{Path: "世界/é.go", Line: 0, Col: 0, Severity: 1, Message: "bad\r\nvalue 😀 é"})
	if line != "世界/é.go:1:1 error: bad value 😀 é" {
		t.Fatalf("line %q", line)
	}
	long := openCodeProblemLine(lspProblem{Path: "a.go", Message: strings.Repeat("世😀é́", 400)})
	if !utf8.ValidString(long) || strings.ContainsRune(long, utf8.RuneError) || strings.ContainsAny(long, "\r\n") {
		t.Fatalf("clipped problem broke runes or kept newline")
	}
}
