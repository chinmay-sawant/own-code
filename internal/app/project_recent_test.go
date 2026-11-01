package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRecentFile(t *testing.T, roots []string) {
	t.Helper()

	path, err := recentProjectsPath()
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.MarshalIndent(roots, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRecentProjectsDropsMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	live := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")
	writeRecentFile(t, []string{missing, live, "sample", missing})

	roots := loadRecentProjects()
	if len(roots) != 2 || roots[0] != live || roots[1] != "sample" {
		t.Fatalf("loadRecentProjects = %+v, want [%q sample]", roots, live)
	}
}

func TestResolveStartupRoot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	live := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")

	if got := ResolveStartupRoot(true, missing); got != missing {
		t.Fatalf("explicit root = %q, want %q", got, missing)
	}

	writeRecentFile(t, []string{missing, live})
	if got := ResolveStartupRoot(false, missing); got != live {
		t.Fatalf("startup root = %q, want most recent live %q", got, live)
	}

	writeRecentFile(t, []string{"sample"})
	if got := ResolveStartupRoot(false, missing); got != "" {
		t.Fatalf("sample startup root = %q, want empty", got)
	}

	writeRecentFile(t, []string{missing})
	if got := ResolveStartupRoot(false, live); got != live {
		t.Fatalf("fallback startup root = %q, want %q", got, live)
	}
}

// TestCommitPatchErrorShowsNote checks a failed patch leaves a retryable
// error note instead of spinning on "Loading the patch for …" forever.
func TestCommitPatchErrorShowsNote(t *testing.T) {
	app, hashes := newHistoryApp(t)
	ctx := context.Background()

	app.patchLoader = &commitPatchLoader{results: make(chan commitPatchResult, 1)}
	app.selectCommitFile(ctx, hashes[0], "app.go")

	if diff := app.View().Diff; !diff.Loading || !strings.Contains(diff.Note, "Loading the patch for app.go") {
		t.Fatalf("pending diff = %+v", diff)
	}

	key := commitDetailKey(hashes[0], "app.go")
	app.patchLoader.results <- commitPatchResult{Key: key, Err: errors.New("test patch failure")}
	app.pollPatches(ctx)

	diff := app.View().Diff
	if diff.Loading {
		t.Fatalf("failed diff still loading: %+v", diff)
	}

	if !strings.Contains(diff.Note, "Couldn't load the patch for app.go") {
		t.Fatalf("failed diff note = %q", diff.Note)
	}

	if _, ok := app.commitErrors[key]; !ok {
		t.Fatal("commit error was not recorded for retry")
	}
}

// TestRefreshRetriesFailedCommitPatch checks the Refresh path re-asks the
// worker after a failure instead of leaving the error note up.
func TestRefreshRetriesFailedCommitPatch(t *testing.T) {
	app, hashes := newHistoryApp(t)
	ctx := context.Background()

	app.patchLoader = &commitPatchLoader{results: make(chan commitPatchResult, 1)}
	app.selectCommitFile(ctx, hashes[0], "app.go")

	key := commitDetailKey(hashes[0], "app.go")
	app.patchLoader.results <- commitPatchResult{Key: key, Err: errors.New("test patch failure")}
	app.pollPatches(ctx)

	if _, ok := app.commitErrors[key]; !ok {
		t.Fatal("commit error was not recorded")
	}

	app.patchLoader = NewCommitPatchLoader(app.ws.Root, maxCommitPatchCache)
	app.ReloadChanges(ctx, false)
	waitForCommitPatch(t, app)

	if diff := app.View().Diff; !diff.HasFiles || diff.Source != "commit" {
		t.Fatalf("retried diff = %+v", diff)
	}
}

// TestSwitchProjectResetsPatchLoader checks a project switch retires the
// worker tied to the old root: commit state resets and the next commit
// patch loads from the new repository instead of failing against the old.
func TestSwitchProjectResetsPatchLoader(t *testing.T) {
	dirA, _ := historyRepo(t)

	app, err := New(dirA)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	app.ReloadChanges(ctx, false)
	app.setMode(ctx, "changes")

	commitsA := app.commits.Commits
	if len(commitsA) == 0 {
		t.Fatal("first project has no history")
	}

	app.toggleCommit(ctx, commitsA[0].Hash)
	waitForCommitPatch(t, app)

	oldLoader := app.patchLoader
	if oldLoader == nil {
		t.Fatal("missing patch loader before switch")
	}

	if len(app.commitDetails) == 0 {
		t.Fatal("first project loaded no patches")
	}

	dirB, hashesB := historyRepo(t)
	if err := app.switchProject(ctx, dirB); err != nil {
		t.Fatal(err)
	}

	if app.patchLoader == nil || app.patchLoader == oldLoader {
		t.Fatal("project switch did not recreate the patch loader")
	}

	if len(app.commitDetails) != 0 || len(app.commitErrors) != 0 {
		t.Fatalf("stale patches survived the switch: %d details %d errors",
			len(app.commitDetails), len(app.commitErrors))
	}

	if app.commitOpen != "" || app.commitFile != "" || app.diffSource != "working" {
		t.Fatalf("stale commit selection survived: open=%q file=%q source=%q",
			app.commitOpen, app.commitFile, app.diffSource)
	}

	if app.historyRef != "" {
		t.Fatalf("stale history ref survived: ref=%q", app.historyRef)
	}

	// The switch loads the new root's history right away, so the rail shows
	// the second repository instead of the first (or nothing).
	commitsB := app.commits.Commits
	if len(commitsB) == 0 || commitsB[0].Hash != hashesB[0] {
		t.Fatalf("history after switch = %+v, want second repo head %q", app.commits, hashesB[0])
	}

	app.toggleCommit(ctx, commitsB[0].Hash)
	waitForCommitPatch(t, app)

	if diff := app.View().Diff; !diff.HasFiles || diff.Source != "commit" {
		t.Fatalf("second project commit diff = %+v", diff)
	}
}
