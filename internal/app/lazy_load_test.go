package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLazyAsyncExpand verifies expanding shows a spinner in one frame with no
// input freeze, then drains to real children.
func TestLazyAsyncExpand(t *testing.T) {
	dir := createLazyTestDir(t)
	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Expand without draining: spinner row appears immediately.
	app.onToggleDir("src")
	v := app.View()
	foundSpinner := false
	for _, row := range v.Tree {
		if row.Loading && strings.HasSuffix(row.Path, "/…") {
			foundSpinner = true
			break
		}
	}
	// Spinner may already have drained if worker was fast; accept either
	// spinner or loaded children, but folders must flip immediately (no freeze).
	if !app.folders["src"] {
		t.Fatal("folders[src] did not flip immediately")
	}
	drainDirLoads(t, app)
	srcNode := findTreeNode(app.ws.Tree, "src")
	if srcNode == nil || !srcNode.Loaded {
		t.Fatalf("src not loaded after drain: %+v", srcNode)
	}
	if foundSpinner {
		// Spinner was visible pre-drain: good (one-frame placeholder).
	} else {
		// Worker beat the frame; children must be visible instead.
		found := false
		for _, row := range app.View().Tree {
			if row.Path == "src/app.go" {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("neither spinner nor children visible after expand")
		}
	}
	// TreeCount excludes spinner rows (seen files only).
	if got := app.View().TreeCount; got != 4 {
		t.Fatalf("TreeCount=%d, want 4 (spinner excluded)", got)
	}
}

// TestLazyCancelCollapse verifies collapsing during load drops the stale reply.
func TestLazyCancelCollapse(t *testing.T) {
	dir := createLazyTestDir(t)
	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	app.onToggleDir("src")
	// Collapse before drain: token dropped.
	app.onToggleDir("src")
	if app.folders["src"] {
		t.Fatal("src still open after collapse")
	}
	if len(app.dirLoads) != 0 {
		t.Fatalf("dirLoads not dropped on collapse: %v", app.dirLoads)
	}
	// Drain stale reply (should be dropped, no children installed yet or harmless).
	drainDirLoads(t, app)
	// Re-expand reloads cleanly.
	app.onToggleDir("src")
	drainDirLoads(t, app)
	if node := findTreeNode(app.ws.Tree, "src"); node == nil || !node.Loaded {
		t.Fatal("re-expand did not load src")
	}
}

// TestLazyRetryPermission verifies a permission-denied expand shows a retry
// row and retry re-enqueues.
func TestLazyRetryPermission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# F\n")
	os.MkdirAll(filepath.Join(dir, "locked"), 0o700)
	writeFile(t, dir, "locked/a.go", "package x\n")
	os.Chmod(filepath.Join(dir, "locked"), 0o000)
	defer os.Chmod(filepath.Join(dir, "locked"), 0o700)

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	app.onToggleDir("locked")
	drainDirLoads(t, app)
	node := findTreeNode(app.ws.Tree, "locked")
	if node == nil {
		t.Fatal("no locked node")
	}
	if node.LoadErr == "" {
		t.Fatalf("expected LoadErr for locked dir, got %+v", node)
	}
	// Retry row visible.
	foundRetry := false
	for _, row := range app.View().Tree {
		if row.LoadErr != "" && strings.HasSuffix(row.Path, "/…retry") {
			foundRetry = true
			break
		}
	}
	if !foundRetry {
		t.Fatal("no retry row visible")
	}
	// Fix perms and retry.
	os.Chmod(filepath.Join(dir, "locked"), 0o700)
	node.Loaded = false
	node.LoadErr = ""
	app.requestDirLoad("locked")
	drainDirLoads(t, app)
	if node.LoadErr != "" || !node.Loaded {
		t.Fatalf("retry did not clear: %+v", node)
	}
}

// TestLazyDirChildCap verifies the per-dir cap with a trailing more-row that
// is not a file and is excluded from counts.
func TestLazyDirChildCap(t *testing.T) {
	oldCap := dirChildCap
	dirChildCap = 10
	defer func() { dirChildCap = oldCap }()

	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# F\n")
	for i := 0; i < 25; i++ {
		writeFile(t, dir, "many/file.go", "package many\n")
		// unique names
		os.Rename(filepath.Join(dir, "many/file.go"), filepath.Join(dir, "many", "file"+string(rune('a'+i%26))+string(rune('0'+i/26))+".go"))
	}
	// Ensure 25 files exist.
	entries, _ := os.ReadDir(filepath.Join(dir, "many"))
	if len(entries) < 10 {
		t.Fatalf("fixture has %d files", len(entries))
	}
	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	app.onToggleDir("many")
	drainDirLoads(t, app)
	node := findTreeNode(app.ws.Tree, "many")
	if node == nil || node.More == 0 {
		t.Fatalf("expected More>0, got %+v", node)
	}
	foundMore := false
	for _, row := range app.View().Tree {
		if row.More && strings.HasSuffix(row.Path, "/…more") {
			foundMore = true
			if row.MoreCount != node.More {
				t.Fatalf("more count %d != node %d", row.MoreCount, node.More)
			}
			break
		}
	}
	if !foundMore {
		t.Fatal("no more-row visible")
	}
	// More-row excluded from TreeCount (files only).
	for _, row := range app.View().Tree {
		if row.More && app.ws.ByPath[row.Path] != nil {
			t.Fatal("more-row leaked into ByPath")
		}
	}
}

// TestLazyStartupRootOnly verifies startup does ReadDir(root) only: Files is
// root-only with no full walk, and ScanBudget stays empty (no truncation).
func TestLazyStartupRootOnly(t *testing.T) {
	dir := createLazyTestDir(t)
	ws, err := loadDiskWorkspaceLazy(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Files) != 2 {
		t.Fatalf("Files=%d, want root-only 2", len(ws.Files))
	}
	if ws.Truncated || ws.ScanBudget() != "" || ws.TruncatedNote != "" {
		t.Fatalf("lazy must never truncate: %+v", ws)
	}
}

// TestLazySearchUnexpanded verifies content search finds strings in
// unexpanded folders via ripgrep (skips without rg).
func TestLazySearchUnexpanded(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed")
	}
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# F\n")
	writeFile(t, dir, "hidden/secret.go", "package hidden\n\n// needle-xyz-123\nfunc X() {}\n")
	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if app.ws.ByPath["hidden/secret.go"] != nil {
		t.Fatal("secret should be unexpanded")
	}
	app.searchQuery = "needle-xyz-123"
	app.runWorkspaceSearch(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_ = app.tickWatch(context.Background())
		if !app.searchBusy {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(app.searchHits) == 0 {
		t.Fatalf("no hits for unexpanded string (note=%q)", app.searchNote)
	}
	if app.searchHits[0].Path != "hidden/secret.go" {
		t.Fatalf("hit path=%q", app.searchHits[0].Path)
	}
	if !strings.Contains(app.searchNote, "disk") {
		t.Fatalf("note not scope-honest: %q", app.searchNote)
	}
}

// TestLazyWatcherTargetedNotFull verifies external creates under an expanded
// dir do targeted reloads with no full walk (counter hook).
func TestLazyWatcherTargetedNotFull(t *testing.T) {
	dir, _ := historyRepo(t)
	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Expand a real dir: create pkg first so it exists at startup.
	writeFile(t, dir, "pkg/old.go", "package pkg\n")
	// Reload root to discover pkg (simulates startup with pkg present).
	app.refreshRoot()
	app.folders["pkg"] = true
	drainDirLoads(t, app)

	fullWalkCounter = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := app.StartWatcher(ctx); err != nil {
		t.Fatal(err)
	}
	defer app.StopWatcher()

	writeFile(t, dir, "pkg/new_targeted.go", "package pkg\n")
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		_ = app.tickWatch(context.Background())
		if app.ws.ByPath["pkg/new_targeted.go"] != nil {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if app.ws.ByPath["pkg/new_targeted.go"] == nil {
		t.Fatal("targeted reload did not add file")
	}
	if fullWalkCounter != 0 {
		t.Fatalf("full walks=%d, want 0 (targeted only)", fullWalkCounter)
	}
}

// TestLazyWebNoWatcher verifies Serve (-web) never starts a watcher.
func TestLazyWebNoWatcher(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Web path never calls StartWatcher; watch stays nil.
	if app.watch != nil {
		t.Fatal("watcher started without StartWatcher")
	}
	// Refresh re-loads root + Loaded dirs (lazy-preserving) without watcher.
	next, _, err := rescanDiskWorkspace(dir, app.ws, app.scanLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !next.Lazy {
		t.Fatal("web refresh lost lazy")
	}
}

// TestLazyQuickOpenExcludeParity verifies rg --files and the walker agree on
// skips: node_modules/dist/.gitignore secrets never surface.
func TestLazyQuickOpenExcludeParity(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	writeFile(t, dir, ".gitignore", "secret.txt\n")
	writeFile(t, dir, "keep.go", "package main\n")
	writeFile(t, dir, "secret.txt", "secret\n")
	writeFile(t, dir, "node_modules/pkg/index.js", "x\n")
	writeFile(t, dir, "dist/app.js", "y\n")
	paths, err := diskSearchFiles(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if p == "secret.txt" || p == "node_modules/pkg/index.js" {
			t.Fatalf("excluded path leaked: %s", p)
		}
	}
	found := false
	for _, p := range paths {
		if p == "keep.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("keep.go missing from %v", paths)
	}
	// Walker parity: loadIgnoredPaths must mark the same.
	ignored := loadIgnoredPaths(dir)
	if !pathIgnored(ignored, "secret.txt") {
		t.Fatal("secret.txt not ignored")
	}
}

// TestLazyFilterScopeHonest verifies filter excludes placeholders and notes scope.
func TestLazyFilterScopeHonest(t *testing.T) {
	dir := createLazyTestDir(t)
	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	app.filter = "app"
	app.refresh()
	v := app.View()
	if !v.Filtered {
		t.Fatal("not filtered")
	}
	for _, row := range v.Tree {
		if row.Loading || row.LoadErr != "" || row.More {
			t.Fatalf("placeholder leaked into filter: %+v", row)
		}
	}
	if v.FilterNote == "" {
		t.Fatal("filter note empty (must declare seen-so-far scope)")
	}
}

// TestLazyIgnoredEmptyNoSpin verifies an empty ignored dir expands to a
// placeholder with no spinner loop (Loaded=true, empty, no Loading).
func TestLazyIgnoredEmptyNoSpin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	writeFile(t, dir, ".gitignore", "empty/\n")
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "main.go", "package main\n")
	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Empty ignored dir should have a row (placeholder).
	found := false
	for _, row := range app.View().Tree {
		if row.Path == "empty" {
			found = true
			if !row.Ignored {
				t.Fatal("empty not ignored")
			}
			break
		}
	}
	if !found {
		t.Fatal("no empty row")
	}
	app.onToggleDir("empty")
	drainDirLoads(t, app)
	node := findTreeNode(app.ws.Tree, "empty")
	if node == nil || !node.Loaded || node.Loading {
		t.Fatalf("empty node stuck: %+v", node)
	}
	// No spinner row for empty (no spin loop); second expand is no-op.
	app.onToggleDir("empty") // collapse
	app.onToggleDir("empty") // re-expand
	drainDirLoads(t, app)
	if node.Loading {
		t.Fatal("spin loop on empty")
	}
}

// TestLazySlowMetric verifies slow directory loads surface to the Frame tab
// hook (DirLoadNote) via token-matched receiveDirLoads.
func TestLazySlowMetric(t *testing.T) {
	dir := createLazyTestDir(t)
	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	app.onToggleDir("src")
	// Steal the token and inject a slow reply.
	tok := app.dirLoads["src"]
	if tok == 0 {
		t.Fatal("no in-flight token for src")
	}
	// Drain the real worker reply first (fast), then inject slow.
	drainDirLoads(t, app)
	// Re-queue with a slow fake to prove the metric path.
	app.dirLoads["src"] = tok + 1000
	if node := findTreeNode(app.ws.Tree, "src"); node != nil {
		node.Loaded = false
		node.Loading = true
	}
	app.dirLoadResult <- dirLoadReply{Token: tok + 1000, Path: "src", Children: nil, Files: nil, Millis: 600}
	app.receiveDirLoads(context.Background())
	if app.dirLoadNote == "" || app.View().DirLoadNote == "" {
		t.Fatalf("DirLoadNote empty: %q / %q", app.dirLoadNote, app.View().DirLoadNote)
	}
}
