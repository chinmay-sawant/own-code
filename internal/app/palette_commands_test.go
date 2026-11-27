package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFuzzyMatchPositionsAndRejects(t *testing.T) {
	score, pos, ok := fuzzyMatch("gtd", "Go: Go to Definition")
	if !ok || score <= 0 || len(pos) != 3 {
		t.Fatalf("fuzzyMatch = %d %v %v", score, pos, ok)
	}

	if _, _, ok := fuzzyMatch("zzz", "Go: Go to Definition"); ok {
		t.Fatal("zzz must not match")
	}

	if _, _, ok := fuzzyMatch("ba", "ab"); ok {
		t.Fatal("order must matter")
	}

	tight, _, _ := fuzzyMatch("tab", "Tab: Pin")
	loose, _, _ := fuzzyMatch("tab", "Toggle Terminal and Blame")
	if tight <= loose {
		t.Fatalf("consecutive match %d should beat scattered %d", tight, loose)
	}

	runs := nameRuns("Tab", []int{0, 1})
	if len(runs) != 2 || runs[0].Text != "Ta" || !runs[0].Hit || runs[1].Text != "b" || runs[1].Hit {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestCommandRowsKeysHighlightAndRecent(t *testing.T) {
	app := newTestApp(t)

	rows := app.rankedCommandRows("")
	if rows[0].Cmd != paletteCmdNewFile || rows[0].Recent {
		t.Fatalf("first row = %+v, want catalog order without recents", rows[0])
	}

	var def paletteRow

	for _, r := range rows {
		if r.Cmd == paletteCmdGotoDefinition {
			def = r
		}
	}

	if def.Keys != "Ctrl+D" {
		t.Fatalf("goto definition keys = %q, want Ctrl+D", def.Keys)
	}

	app.settings.Keymap = map[string]string{"goto-definition": "Ctrl+Alt+G"}
	if got := app.paletteKeysFor(paletteCmdGotoDefinition); got != "Ctrl+Alt+G" {
		t.Fatalf("custom chord = %q", got)
	}

	app.notePaletteCommand(paletteCmdBlame)
	app.notePaletteCommand(paletteCmdStickyScroll)
	app.notePaletteCommand(paletteCmdBlame)

	rows = app.rankedCommandRows("")
	if rows[0].Cmd != paletteCmdBlame || rows[1].Cmd != paletteCmdStickyScroll || !rows[0].Recent {
		t.Fatalf("recent order = %s, %s", rows[0].Cmd, rows[1].Cmd)
	}

	rows = app.rankedCommandRows("tgl")
	if len(rows) == 0 || len(rows[0].NameParts) == 0 {
		t.Fatalf("fuzzy rows = %+v", rows)
	}

	hits := ""

	for _, run := range rows[0].NameParts {
		if run.Hit {
			hits += run.Text
		}
	}

	if !strings.EqualFold(hits, "tgl") && len(hits) < 3 {
		t.Fatalf("highlighted = %q", hits)
	}
}

func TestDetectTasksAndRunTask(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("Makefile", ".PHONY: build\nVAR := 1\nbuild: deps\n\tgo build\n%.o: %.c\n\ttrue\nevil;rm:\ntest:\n")
	write("package.json", `{"scripts":{"dev":"vite","bad name":"x","x;rm -rf":"y","build":"z"}}`)
	write("yarn.lock", "")
	write("go.mod", "module x\n")

	ids := map[string]string{}
	for _, task := range detectTasks(dir) {
		ids[task.ID] = task.Line
	}

	for id, line := range map[string]string{
		"make:build": "make build", "make:test": "make test",
		"yarn:dev": "yarn run dev", "yarn:build": "yarn run build", "go:test": "go test ./...",
	} {
		if ids[id] != line {
			t.Errorf("task %s = %q, want %q (all: %v)", id, ids[id], line, ids)
		}
	}

	for id := range ids {
		if strings.ContainsAny(id, ";% ") {
			t.Errorf("unsafe task id %q", id)
		}
	}
}

func TestRunTaskTypesIntoTerminal(t *testing.T) {
	app := newTerminalTestApp(t)
	defer app.Close()

	writeFile(t, app.ws.Root, "Makefile", "hello:\n\t@echo task-ran-ok\n")

	ctx := context.Background()
	app.runTask(ctx, "make:hello")

	if !app.termOpen {
		t.Fatal("terminal did not open")
	}

	// make may be absent on minimal systems; the typed line still echoes.
	waitTerminalContains(t, app, "make hello")
}

func TestParseCompareQuery(t *testing.T) {
	cases := []struct {
		in   string
		a, b string
		ok   bool
	}{
		{"compare main feature/x", "main", "feature/x", true},
		{"diff v1..v2", "v1", "v2", true},
		{"compare abc123", "abc123", "HEAD", true},
		{"compare --output=x main", "", "", false},
		{"compare a b c", "", "", false},
		{"compare a;b c", "", "", false},
		{"nothing a b", "", "", false},
	}

	for _, c := range cases {
		a, b, ok := parseCompareQuery(c.in)
		if ok != c.ok || a != c.a || b != c.b {
			t.Errorf("parseCompareQuery(%q) = %q %q %v", c.in, a, b, ok)
		}
	}
}

func TestUnstageHunkRoundTrip(t *testing.T) {
	dir := twoHunkRepo(t)
	app := newTwoHunkApp(t, dir)
	ctx := context.Background()

	if err := app.stageHunk(ctx, 0); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stagedDiff(t, dir), "TWO") {
		t.Fatal("setup: TWO hunk not staged")
	}

	app.selectDiff("notes.txt")

	if err := app.unstageHunk(ctx, 0); err != nil {
		t.Fatal(err)
	}

	if staged := stagedDiff(t, dir); strings.TrimSpace(staged) != "" {
		t.Fatalf("staged after unstage = %q", staged)
	}

	if !strings.Contains(diskText(t, dir, "notes.txt"), "TWO") {
		t.Fatal("unstage must keep the worktree edit")
	}
}

func TestStashListApplyAndCompareRefs(t *testing.T) {
	dir := twoHunkRepo(t)
	app := newTwoHunkApp(t, dir)
	ctx := context.Background()

	app.stashPush(ctx)

	if strings.TrimSpace(unstagedDiff(t, dir)) != "" {
		t.Fatal("stash push left changes behind")
	}

	stashes := listStashes(ctx, dir)
	if len(stashes) != 1 || stashes[0].Index != 0 {
		t.Fatalf("stashes = %+v", stashes)
	}

	app.stashApply(ctx, 0)

	if !strings.Contains(diskText(t, dir, "notes.txt"), "ELEVEN") {
		t.Fatal("apply did not restore the edit")
	}

	if len(listStashes(ctx, dir)) != 1 {
		t.Fatal("apply must keep the stash entry")
	}

	runGit(t, dir, "commit", "-qam", "second")

	changes, err := loadRefsChanges(ctx, dir, "HEAD~1", "HEAD")
	if err != nil || len(changes.Files) != 1 {
		t.Fatalf("loadRefsChanges = %+v, %v", changes, err)
	}

	app.compareRefs(ctx, "HEAD~1", "HEAD")

	if app.diffSource != "branch" || len(app.branchChanges.Files) != 1 {
		t.Fatalf("source=%q files=%d", app.diffSource, len(app.branchChanges.Files))
	}

	// A refresh must keep the comparison, not fall back to the default branch.
	app.refreshBranchChanges(ctx)

	if len(app.branchChanges.Files) != 1 || !strings.Contains(app.branchChanges.Note, "HEAD~1") {
		t.Fatalf("refresh lost the comparison: %+v", app.branchChanges)
	}

	app.compareRefs(ctx, "nope", "HEAD")

	if app.toast.Kind != "error" {
		t.Fatalf("toast = %+v, want error", app.toast)
	}
}

func TestRunPaletteCommandRoutesExtras(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	app.paletteOpen = true
	if err := app.runPaletteCommand(ctx, paletteCmdComparePick); err != nil {
		t.Fatal(err)
	}

	if app.paletteQuery != ">compare " || !app.paletteOpen {
		t.Fatalf("query = %q open = %v", app.paletteQuery, app.paletteOpen)
	}

	if len(app.palx.recent) == 0 || app.palx.recent[0] != paletteCmdComparePick {
		t.Fatalf("recent = %v", app.palx.recent)
	}
}

func TestLSPSettingsToggles(t *testing.T) {
	t.Setenv(lspEnvOff, "")

	app := newTestApp(t)

	rows := app.lspSettingsRows()
	if len(rows) == 0 {
		t.Fatal("no language servers listed")
	}

	name := rows[0].Name
	if !rows[0].Enabled {
		t.Fatalf("%s should start enabled", name)
	}

	app.setLSPServerDisabled(name, true)

	for _, r := range app.lspSettingsRows() {
		if r.Name == name && (r.Enabled || r.Status != "Disabled") {
			t.Fatalf("row after disable = %+v", r)
		}
	}

	snap := app.settingsSnapshot()
	if snap.LSP == nil || !snap.LSP.Servers[name].Disabled {
		t.Fatalf("snapshot lost lsp override: %+v", snap.LSP)
	}

	app.setLSPEnabled(false)

	if lspEnabled(app.settings.LSP) || app.lspSettingsRows()[0].StatusCls != "off" {
		t.Fatal("master switch off not reflected")
	}

	if _, err := app.lspSettingsAction(context.Background(), "lsp-master-toggle"); err != nil {
		t.Fatal(err)
	}

	if !lspEnabled(app.settings.LSP) {
		t.Fatal("toggle should re-enable")
	}
}
