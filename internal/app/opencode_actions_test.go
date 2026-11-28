package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func actionGitApp(t *testing.T) (*App, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	a := mentionTestApp(t, map[string]string{"a.go": "one\n"})
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
	return a, root
}

func TestOCActionReviewChangesOnlyDiffAndKeepsSession(t *testing.T) {
	a, root := actionGitApp(t)
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("two\n"), 0o600)
	_ = os.WriteFile(filepath.Join(root, "new.go"), []byte("package n\n"), 0o600)
	_ = os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=9\n"), 0o600)
	a.openCode.panelOpen = true
	a.openCode.selectedSessionID = "s1"
	a.openCode.composer.chips = []openCodeChip{{ID: "old", Kind: openCodeChipFile, Label: "x"}}
	if err := a.runOpenCodeAction(context.Background(), paletteCmdOCReviewChanges); err != nil {
		t.Fatal(err)
	}
	c := a.openCode.composer
	if len(c.chips) != 1 || c.chips[0].Kind != openCodeChipDiff {
		t.Fatalf("chips %+v", c.chips)
	}
	txt := c.chips[0].Text
	if !strings.Contains(txt, "+two") || !strings.Contains(txt, "+++ b/new.go") || !strings.Contains(txt, "+package n") || strings.Contains(txt, "TOKEN") {
		t.Fatalf("diff %q", txt)
	}
	if !strings.Contains(c.text, "Review the attached") || a.openCode.selectedSessionID != "s1" || len(a.openCode.runs) != 0 {
		t.Fatalf("composer %q session %q runs %d", c.text, a.openCode.selectedSessionID, len(a.openCode.runs))
	}
	// Deterministic: same state, same payload.
	again, _ := a.planOpenCodeAction(paletteCmdOCReviewChanges)
	if again.Prompt != c.text || again.Chips[0].Text != txt {
		t.Fatal("not deterministic")
	}
	// The diff is rechecked at send: a new edit makes it stale.
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("three\n"), 0o600)
	if _, err := a.composeOpenCodeUserText("go"); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale diff not blocked: %v", err)
	}
}

func TestOCActionExplainProblemOnlyThatProblem(t *testing.T) {
	a := mentionTestApp(t, map[string]string{"a.go": "x\n", "b.go": "y\n"})
	m := a.lspManager()
	m.diags["a.go"] = []lspDiag{{Line: 3, From: 1, Severity: 1, Message: "undefined: x"}}
	m.diags["b.go"] = []lspDiag{{Line: 0, From: 0, Severity: 2, Message: "unused"}}
	m.problemsDirty = true
	plan, err := a.planOpenCodeAction(paletteCmdOCExplainProblem)
	if err != nil || len(plan.Chips) != 1 || plan.Chips[0].Kind != openCodeChipProblems || plan.Chips[0].Label != "problem" {
		t.Fatalf("%+v %v", plan, err)
	}
	if !strings.Contains(plan.Chips[0].Text, "a.go:4:2 error: undefined: x") || strings.Contains(plan.Chips[0].Text, "unused") || strings.Contains(plan.Chips[0].Text, "x\n") {
		t.Fatalf("chip %q", plan.Chips[0].Text)
	}
	a.openCode.composer.chips = plan.Chips
	root, _ := a.openCodeChipRoot()
	if why := a.openCodeProblemsStale(root, plan.Chips[0]); why != "" {
		t.Fatalf("fresh: %s", why)
	}
	m.diags["a.go"] = nil
	m.problemsDirty = true
	if a.openCodeProblemsStale(root, plan.Chips[0]) == "" {
		t.Fatal("cleared problem not stale")
	}
	if _, err := a.planOpenCodeAction(paletteCmdOCExplainSelection); err == nil {
		t.Fatal("selection action without editor must fail, not widen context")
	}
}

func TestOCActionNoticesAndPaletteRows(t *testing.T) {
	a := mentionTestApp(t, map[string]string{"a.go": "x\n"})
	if n := a.openCodeActionNotice(); !strings.Contains(n, "Offline") {
		t.Fatalf("notice %q", n)
	}
	got := map[string]bool{}
	for _, r := range paletteExtraCatalog() {
		got[r.Cmd] = true
	}
	for _, r := range openCodeActionRows() {
		if !got[r.Cmd] || !strings.HasPrefix(r.Cmd, paletteCmdOCActionPrefix) {
			t.Fatalf("row %s missing", r.Cmd)
		}
	}
	for _, e := range keymapCatalog() {
		if strings.HasPrefix(e.Action, "opencode") {
			t.Fatalf("unexpected default binding %s", e.Action)
		}
	}
}

func TestOCMentionKeyboardAndClickAway(t *testing.T) {
	a := mentionTestApp(t, map[string]string{"main.go": "x"})
	a.openCode.panelOpen = true
	a.openCode.composer.text = "@"
	n := len(a.currentOpenCodeMentions())
	if n < 3 {
		t.Fatalf("%d candidates", n)
	}
	a.openCodeMentionKey("arrowdown")
	if a.openCode.composer.mentionIndex != 1 {
		t.Fatal("down did not move")
	}
	a.openCodeMentionKey("arrowup")
	a.openCodeMentionKey("arrowup")
	if a.openCode.composer.mentionIndex != n-1 {
		t.Fatal("up did not wrap")
	}
	if a.openCodeMentionKey("a") {
		t.Fatal("typed key consumed")
	}
	a.openCodeMentionKey("escape")
	if len(a.currentOpenCodeMentions()) != 0 {
		t.Fatal("escape did not close")
	}
	a.onOpenCodePromptChange("@m")
	if len(a.currentOpenCodeMentions()) == 0 {
		t.Fatal("new query must reopen")
	}
	a.dismissOpenCodeMentions("opencode-mention-0")
	if len(a.currentOpenCodeMentions()) == 0 {
		t.Fatal("click on popup closed it")
	}
	a.dismissOpenCodeMentions("tab-0")
	if len(a.currentOpenCodeMentions()) != 0 {
		t.Fatal("click away did not close")
	}
	// Enter attaches the highlighted row (a file) and clears the token.
	a.onOpenCodePromptChange("see @main")
	a.openCodeMentionKey("enter")
	if len(a.openCode.composer.chips) != 1 || a.openCode.composer.chips[0].Path != "main.go" || a.openCode.composer.text != "see " {
		t.Fatalf("%+v %q", a.openCode.composer.chips, a.openCode.composer.text)
	}
}

func TestOCMentionProblemPicksAndDirtyBuffer(t *testing.T) {
	a := mentionTestApp(t, map[string]string{"a.go": "x\n"})
	m := a.lspManager()
	m.diags["a.go"] = []lspDiag{{Line: 0, From: 0, Severity: 1, Message: "bad"}}
	m.problemsDirty = true
	var picks int
	for _, c := range a.openCodeMentionCandidates("problems") {
		if c.Problem != "" {
			picks++
		}
	}
	if picks != 1 {
		t.Fatalf("picks %d", picks)
	}
}

func TestOCTermLinkOnlyInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "in.go"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(outside, "out.go"), []byte("x"), 0o600)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlink unsupported")
	}
	a := &App{ws: &workspace{Root: root, ByPath: map[string]*fileMeta{}}}
	if !a.termLinkInsideWorkspace(filepath.Join(root, "in.go")) {
		t.Fatal("inside file rejected")
	}
	for _, p := range []string{filepath.Join(outside, "out.go"), filepath.Join(root, "link", "out.go"), filepath.Join(root, "..", "x"), root} {
		if a.termLinkInsideWorkspace(p) {
			t.Fatalf("%s accepted", p)
		}
	}
}
