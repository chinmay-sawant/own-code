package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mentionTestApp(t *testing.T, files map[string]string) *App {
	t.Helper()
	root := t.TempDir()
	ws := &workspace{Root: root, ByPath: map[string]*fileMeta{}}
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		m := &fileMeta{Path: p, Name: filepath.Base(p), Dir: filepath.Dir(p)}
		ws.Files = append(ws.Files, m)
	}
	return &App{ws: ws}
}

func TestMentionQueryParsing(t *testing.T) {
	for _, c := range []struct {
		in, q string
		ok    bool
	}{{"@", "", true}, {"hi @ma", "ma", true}, {"a@b", "", false}, {"@x done", "", false}, {"none", "", false}} {
		q, _, ok := openCodeMentionQuery(c.in)
		if q != c.q || ok != c.ok {
			t.Fatalf("%q => %q,%v", c.in, q, ok)
		}
	}
}

func TestMentionCandidatesExcludeSensitiveAndIgnored(t *testing.T) {
	a := mentionTestApp(t, map[string]string{"main.go": "x", ".env": "S=1", "keys/server.pem": "k", "ignored/a.go": "x", "src/credentials.json": "{}"})
	a.ws.ignored = map[string]bool{"ignored": true}
	var labels []string
	for _, c := range a.openCodeMentionCandidates("") {
		labels = append(labels, c.Label+"|"+c.Path)
	}
	joined := strings.Join(labels, ",")
	if !strings.Contains(joined, "main.go") || !strings.Contains(joined, "@diff") || !strings.Contains(joined, "@selection") {
		t.Fatalf("missing entries: %s", joined)
	}
	for _, bad := range []string{".env", "server.pem", "ignored", "credentials"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("%s leaked: %s", bad, joined)
		}
	}
	if got := a.openCodeMentionCandidates("zzzz"); len(got) != 0 {
		t.Fatalf("fuzzy matched garbage: %v", got)
	}
}

func TestMentionCandidatesAreBounded(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 40; i++ {
		files["pkg/f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".go"] = "x"
	}
	a := mentionTestApp(t, files)
	n := 0
	for _, c := range a.openCodeMentionCandidates("f") {
		if c.Path != "" {
			n++
		}
	}
	if n != maxOpenCodeMentionFiles {
		t.Fatalf("%d file candidates", n)
	}
}

func TestMentionFileChipStaleAndExclusion(t *testing.T) {
	a := mentionTestApp(t, map[string]string{"a.go": "package a\nfunc A() {}\n", ".env": "SECRET=1"})
	root, _ := a.openCodeChipRoot()
	chip, err := a.captureOpenCodeFileChip(root, "a.go")
	if err != nil || chip.Path != "a.go" || chip.Detail != "lines 1-3" || chip.Root != root || chip.Version == "" {
		t.Fatalf("chip %+v err %v", chip, err)
	}
	if why := a.openCodeChipStale(chip); why != "" {
		t.Fatalf("fresh chip stale: %s", why)
	}
	if _, err := a.captureOpenCodeFileChip(root, ".env"); err == nil {
		t.Fatal(".env attached")
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if a.openCodeChipStale(chip) == "" {
		t.Fatal("changed file not stale")
	}
	chip.Root = "/elsewhere"
	if a.openCodeChipStale(chip) == "" {
		t.Fatal("root mismatch not stale")
	}
}

func TestMentionChipLimitsAndTruncation(t *testing.T) {
	a := &App{}
	for i := 0; i < maxOpenCodeChips; i++ {
		if err := a.addOpenCodeChip(openCodeChip{Kind: openCodeChipFile, Label: "f", Path: string(rune('a' + i)), Text: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.addOpenCodeChip(openCodeChip{Kind: openCodeChipFile, Path: "z"}); err == nil {
		t.Fatal("count limit not enforced")
	}
	b := &App{}
	big := strings.Repeat("€", maxOpenCodeChipBytes)
	for i := 0; i < 5; i++ {
		_ = b.addOpenCodeChip(openCodeChip{Kind: openCodeChipFile, Path: string(rune('a' + i)), Text: big[:maxOpenCodeChipBytes-1]})
	}
	total := 0
	for _, c := range b.openCode.composer.chips {
		total += len(c.Text)
		if !strings.HasPrefix(c.Text, "") || len(c.Text) > maxOpenCodeChipTotalBytes {
			t.Fatal("bad chip")
		}
	}
	if total > maxOpenCodeChipTotalBytes {
		t.Fatalf("total %d", total)
	}
	last := b.openCode.composer.chips[len(b.openCode.composer.chips)-1]
	if !last.Truncated || last.Note == "" {
		t.Fatalf("truncation not reported: %+v", last)
	}
}

func TestMentionComposeUserTextAndRetry(t *testing.T) {
	a := mentionTestApp(t, map[string]string{"a.go": "package a\n"})
	root, _ := a.openCodeChipRoot()
	chip, _ := a.captureOpenCodeFileChip(root, "a.go")
	if err := a.addOpenCodeChip(chip); err != nil {
		t.Fatal(err)
	}
	want := renderOpenCodeContextBlock(a.openCode.composer.chips)
	got, err := a.composeOpenCodeUserText("explain")
	if err != nil || got != "explain"+want || !strings.Contains(got, "package a") || !strings.Contains(got, "version ") {
		t.Fatalf("%q %v", got, err)
	}
	if again, _ := a.composeOpenCodeUserText(got); again != got {
		t.Fatal("retry text recomposed")
	}
	if openCodeTypedPart(got) != "explain" {
		t.Fatalf("typed part %q", openCodeTypedPart(got))
	}
	a.removeChipForTest("c1")
	if out, _ := a.composeOpenCodeUserText("x"); out != "x" {
		t.Fatalf("removed chip still sent: %q", out)
	}
}

func (a *App) removeChipForTest(id string) {
	c := &a.openCode.composer
	for i, ch := range c.chips {
		if ch.ID == id {
			c.chips = append(c.chips[:i], c.chips[i+1:]...)
			return
		}
	}
}

func TestMentionStaleChipBlocksCompose(t *testing.T) {
	a := mentionTestApp(t, map[string]string{"a.go": "one\n"})
	root, _ := a.openCodeChipRoot()
	chip, _ := a.captureOpenCodeFileChip(root, "a.go")
	_ = a.addOpenCodeChip(chip)
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("two two\n"), 0o600)
	if _, err := a.composeOpenCodeUserText("go"); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("err %v", err)
	}
}

func TestMentionDiffChipExcludesSecrets(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	a := mentionTestApp(t, map[string]string{"a.go": "one\n", ".env": "TOKEN=1\n"})
	root, _ := a.openCodeChipRoot()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", "-A", "-f")
	run("commit", "-q", "-m", "i")
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("two\n"), 0o600)
	_ = os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=2\n"), 0o600)
	chip, err := a.captureOpenCodeDiffChip(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(chip.Text, "+two") || strings.Contains(chip.Text, "TOKEN") || chip.Detail != "1 file(s), "+chip.Detail[len("1 file(s), "):] {
		t.Fatalf("diff chip: %q / %s", chip.Text, chip.Detail)
	}
}
