package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chinmay-sawant/ownframe"
)

// blamePorcelainFixture is two committed blocks plus one uncommitted line.
// Hashes a/b are full 40-char hex; the zeros block is "not committed yet".
func blamePorcelainFixture() string {
	a := strings.Repeat("a", commitHashLen)
	b := strings.Repeat("b", commitHashLen)
	z := strings.Repeat("0", commitHashLen)

	return a + " 1 1 2\nauthor Alice\n" +
		"author-mail <alice@example.com>\n" +
		"author-time 1700000000\n" +
		"author-tz +0000\n" +
		"summary First commit\n" +
		"filename app.go\n" +
		"\tpackage main\n" +
		"\t\n" +
		b + " 3 3 1\nauthor Bob\n" +
		"author-mail <bob@example.com>\n" +
		"author-time 1700000100\n" +
		"author-tz +0000\n" +
		"summary Second commit\n" +
		"filename app.go\n" +
		"\tprintln()\n" +
		z + " 4 4 1\nauthor Not Committed Yet\n" +
		"author-mail <not.committed.yet>\n" +
		"author-time 0\n" +
		"author-tz +0000\n" +
		"summary Version of app.go from app.go\n" +
		"filename app.go\n" +
		"\t// scratch\n"
}

func TestParseBlamePorcelain(t *testing.T) {
	t.Parallel()

	lines := parseBlamePorcelain(blamePorcelainFixture())
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want 4", len(lines))
	}

	first := lines[0]
	if first.Hash != strings.Repeat("a", commitHashLen) || first.Short != "aaaaaaa" {
		t.Fatalf("first = %+v", first)
	}
	if first.Author != "Alice" || first.Summary != "First commit" || first.Date == "" {
		t.Fatalf("first meta = %+v", first)
	}
	if first.Uncommitted {
		t.Fatalf("first unexpectedly uncommitted: %+v", first)
	}

	if lines[1].Hash != first.Hash || lines[1].Short != first.Short {
		t.Fatalf("block lines split the commit: %+v", lines[1])
	}

	third := lines[2]
	if third.Hash != strings.Repeat("b", commitHashLen) || third.Author != "Bob" {
		t.Fatalf("third = %+v", third)
	}

	fourth := lines[3]
	if !fourth.Uncommitted {
		t.Fatalf("fourth should be uncommitted: %+v", fourth)
	}
	if got := blameDisplay(fourth); got != "Not committed" {
		t.Fatalf("uncommitted display = %q", got)
	}
	if got := blameDisplay(first); got != "aaaaaaa" {
		t.Fatalf("committed display = %q", got)
	}

	title := blameTitle(first)
	for _, want := range []string{"aaaaaaa", "Alice", "First commit"} {
		if !strings.Contains(title, want) {
			t.Fatalf("title %q misses %q", title, want)
		}
	}
	if got := blameTitle(fourth); got != "Not committed" {
		t.Fatalf("uncommitted title = %q", got)
	}
}

func TestParseBlamePorcelainIgnoresNoise(t *testing.T) {
	t.Parallel()

	a := strings.Repeat("c", commitHashLen)
	out := "boundary\n" + a + " 1 1 1\n" +
		"author Carol\n" +
		"author-time bogus\n" +
		"previous 0123456789012345678901234567890123456789 app.go\n" +
		"filename app.go\n" +
		"\thello\n"

	lines := parseBlamePorcelain(out)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	if lines[0].Author != "Carol" || lines[0].Date != "" {
		t.Fatalf("line = %+v", lines[0])
	}
	if lines[0].Short != "ccccccc" {
		t.Fatalf("short = %q", lines[0].Short)
	}
}

// waitForBlame drains the async blame worker until the gutter shows.
func waitForBlame(t *testing.T, app *App) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		if err := app.tickWatch(context.Background()); err != nil {
			t.Fatal(err)
		}

		if app.View().BlameShow {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("blame did not arrive: note=%q rows=%d", app.View().BlameNote, len(app.View().Rows))
}

func newBlameApp(t *testing.T) (*App, context.Context) {
	t.Helper()

	dir, _ := historyRepo(t)
	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	app.ReloadChanges(ctx, false)
	app.openFile("app.go")

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	return app, ctx
}

func TestBlameOffByDefault(t *testing.T) {
	app, _ := newBlameApp(t)

	if app.blameOn || app.View().BlameOn || app.View().BlameShow {
		t.Fatalf("blame should default off: on=%v view=%+v", app.blameOn, app.View())
	}

	if app.View().BlameNote != "" {
		t.Fatalf("note while off = %q", app.View().BlameNote)
	}
}

func TestBlameToggleOnKeyAndPalette(t *testing.T) {
	app, ctx := newBlameApp(t)

	// B is the documented free chord: no home branch names it.
	if err := app.onKeyDown(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if !app.blameOn || !app.View().BlameOn {
		t.Fatalf("B did not toggle blame on: on=%v", app.blameOn)
	}
	if got := app.View().BlameNote; got != "Loading blame…" {
		t.Fatalf("busy note = %q, want the loading note", got)
	}

	waitForBlame(t, app)
	if !app.View().BlameShow {
		t.Fatal("gutter not showing after load")
	}

	found := false
	for _, row := range app.View().Rows {
		if row.No == 0 {
			if row.Blame != "" {
				t.Fatalf("continuation row carries blame: %+v", row)
			}

			continue
		}
		if row.Blame == "" {
			t.Fatalf("first visual row missing blame: %+v", row)
		}
		if row.BlameHash != "" && app.findCommit(row.BlameHash) == nil {
			t.Fatalf("blame hash %q is not in history", row.BlameHash)
		}
		if row.BlameTitle == "" || !strings.Contains(row.BlameTitle, row.Blame) && row.Blame != "Not committed" {
			t.Fatalf("row title = %q for %q", row.BlameTitle, row.Blame)
		}
		found = true
	}
	if !found {
		t.Fatal("no code rows to check")
	}

	// The blame column composes with the line-number gutter: both paint.
	boxes := app.Page().Boxes()
	seenLn, seenBlame := false, false
	for _, box := range boxes {
		if strings.HasPrefix(box.ID, "ln-") && box.Text != "" {
			seenLn = true
		}
		if strings.HasPrefix(box.ID, "blame-") && box.Text != "" {
			seenBlame = true
		}
	}
	if !seenLn || !seenBlame {
		t.Fatalf("gutter boxes: ln=%v blame=%v", seenLn, seenBlame)
	}

	// The palette command toggles it back off.
	if err := app.runPaletteCommand(ctx, paletteCmdBlame); err != nil {
		t.Fatal(err)
	}
	if app.blameOn || app.View().BlameOn || app.View().BlameShow {
		t.Fatalf("palette did not toggle blame off: on=%v", app.blameOn)
	}
	for _, row := range app.View().Rows {
		if row.Blame != "" || row.BlameHash != "" {
			t.Fatalf("row keeps blame while off: %+v", row)
		}
	}
}

func TestBlameCacheInvalidatedOnSave(t *testing.T) {
	app, _ := newBlameApp(t)

	app.toggleBlame()
	waitForBlame(t, app)

	old, ok := app.blameCache["app.go"]
	if !ok || len(old.Lines) == 0 {
		t.Fatalf("no cached blame: %+v", app.blameCache["app.go"])
	}

	doc := app.docs["app.go"]
	next := strings.Join(doc.Lines, "\n") + "\n// blame-note\n"
	app.toggleEdit()
	app.applyEditText(next)
	app.saveEdit()

	if _, ok := app.blameCache["app.go"]; ok {
		t.Fatal("save did not invalidate the cached blame")
	}

	waitForBlame(t, app)
	fresh := app.blameCache["app.go"]
	if fresh.Key == old.Key {
		t.Fatal("blame key did not move after save")
	}
	if len(fresh.Lines) != len(old.Lines)+1 {
		t.Fatalf("lines = %d, want %d", len(fresh.Lines), len(old.Lines)+1)
	}

	last := app.View().Rows[len(app.View().Rows)-1]
	if last.Blame != "Not committed" || last.BlameHash != "" {
		t.Fatalf("new line blame = %+v", last)
	}
}

func TestBlameClickOpensCommit(t *testing.T) {
	app, ctx := newBlameApp(t)

	app.toggleBlame()
	waitForBlame(t, app)

	hash := ""
	for _, row := range app.View().Rows {
		if row.BlameHash != "" {
			hash = row.BlameHash

			break
		}
	}
	if hash == "" {
		t.Fatal("no committed blame row to click")
	}

	if err := app.onClick(ctx, ownframe.Box{Action: "blame-commit:" + hash}); err != nil {
		t.Fatal(err)
	}
	if app.mode != "changes" || app.diffSource != "commit" || app.commitOpen != hash {
		t.Fatalf("click opened mode=%q source=%q commit=%q, want the commit diff",
			app.mode, app.diffSource, app.commitOpen)
	}
}

func TestBlameBigFileSkipped(t *testing.T) {
	dir, _ := historyRepo(t)
	writeFile(t, dir, "big.txt", strings.Repeat("x\n", (maxFileBytes/2)+10))

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	app.ReloadChanges(ctx, false)
	app.openFile("big.txt")
	app.toggleBlame()

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	view := app.View()
	if view.BlameShow {
		t.Fatal("blame shows for a big file")
	}
	if !strings.Contains(view.BlameNote, "large") {
		t.Fatalf("big-file note = %q", view.BlameNote)
	}
	for _, row := range view.Rows {
		if row.Blame != "" {
			t.Fatalf("big-file row carries blame: %+v", row)
		}
	}
}

func TestBlameSampleWorkspaceNote(t *testing.T) {
	app := newTestApp(t)
	app.openFile("README.md")
	app.toggleBlame()

	if err := app.Page().Redraw(context.Background()); err != nil {
		t.Fatal(err)
	}

	view := app.View()
	if view.BlameShow {
		t.Fatal("blame shows in the sample workspace")
	}
	if !strings.Contains(strings.ToLower(view.BlameNote), "sample") {
		t.Fatalf("sample note = %q", view.BlameNote)
	}
}

func TestBlameLoaderCachesByKey(t *testing.T) {
	t.Parallel()

	loader := NewBlameLoader(t.TempDir())
	t.Cleanup(loader.Close)

	calls := 0
	loader.load = func(_ context.Context, path string) ([]blameLine, error) {
		calls++

		return []blameLine{{Hash: strings.Repeat("d", commitHashLen), Short: "ddddddd", Author: "Dev"}}, nil
	}

	ctx := context.Background()
	loader.Request(ctx, "app.go\x00k1", "app.go")
	loader.Request(ctx, "app.go\x00k1", "app.go")
	loader.Request(ctx, "app.go\x00k2", "app.go")

	seen := map[string]int{}
	total := 0
	deadline := time.Now().Add(5 * time.Second)
	for total < 3 && time.Now().Before(deadline) {
		if result, ok := loader.Poll(); ok {
			seen[result.Key]++
			total++
		} else {
			time.Sleep(time.Millisecond)
		}
	}

	if total != 3 {
		t.Fatalf("results = %v, want 3", seen)
	}
	if calls != 2 {
		t.Fatalf("load calls = %d, want 2 (one per key)", calls)
	}
}

// TestBlameWrappedContinuation pins the annotation to the first visual row:
// wrapped continuation rows (No zero) never carry blame.
func TestBlameWrappedContinuation(t *testing.T) {
	app, _ := newBlameApp(t)

	key, ok := app.blameStatKey("app.go")
	if !ok {
		t.Fatal("no stat key for app.go")
	}

	hash := strings.Repeat("e", commitHashLen)
	app.blameOn = true
	app.blameCache["app.go"] = blameEntry{Key: key, Lines: []blameLine{
		{Hash: hash, Short: "eeeeeee", Author: "Eve"},
		{Hash: hash, Short: "eeeeeee", Author: "Eve"},
	}}

	doc := app.docs["app.go"]
	rows := []codeRow{
		{No: 1, Line: 0},
		{No: 0, Line: 0},
		{No: 2, Line: 1},
	}
	app.decorateBlame(doc, rows, 0)

	if rows[0].Blame != "eeeeeee" || rows[0].BlameHash != hash {
		t.Fatalf("first row = %+v", rows[0])
	}
	if rows[1].Blame != "" || rows[1].BlameHash != "" {
		t.Fatalf("continuation row carries blame: %+v", rows[1])
	}
	if rows[2].Blame != "eeeeeee" {
		t.Fatalf("second line = %+v", rows[2])
	}
}

// TestBlameComposesWithGutterMarks opens the file with an unstaged change:
// changed rows carry both the diff mark and the blame hash.
func TestBlameComposesWithGutterMarks(t *testing.T) {
	app, _ := newBlameApp(t)
	app.openFile("README.md")
	app.toggleBlame()
	waitForBlame(t, app)

	composed := false
	for _, row := range app.View().Rows {
		if row.Mark != "" && row.Blame != "" {
			composed = true

			break
		}
	}
	if !composed {
		t.Fatal("no row carries both a gutter mark and blame")
	}
}

// TestBlameSplitPerGroup mirrors the file into the right pane: both panes
// paint their own blame column from the shared cache.
func TestBlameSplitPerGroup(t *testing.T) {
	app, _ := newBlameApp(t)
	app.toggleBlame()
	waitForBlame(t, app)

	app.toggleSplit()

	view := app.View()
	if !view.Split || !view.Second.BlameShow {
		t.Fatalf("split blame: split=%v second show=%v", view.Split, view.Second.BlameShow)
	}

	found := false
	for _, row := range view.Second.Rows {
		if row.No > 0 && row.Blame != "" {
			found = true

			break
		}
	}
	if !found {
		t.Fatal("right pane has no blame annotations")
	}
}

// TestBlameZoomOverride keeps the zoom contract: the blame column and its
// hit-test shift scale with the layout zoom.
func TestBlameZoomOverride(t *testing.T) {
	t.Parallel()

	css := zoomOverrideCSS(1.5)
	for _, want := range []string{".blame", "108px", "198px", "font-size:15px"} {
		if !strings.Contains(css, want) {
			t.Fatalf("override missing %q", want)
		}
	}
}

func openBlameFile(t *testing.T, dir, name string) (*App, context.Context) {
	t.Helper()

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	app.ReloadChanges(ctx, false)
	app.openFile(name)
	app.toggleBlame()

	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	return app, ctx
}

// TestBlameBinarySkipped shows the unavailable note instead of per-line
// blame for binary files.
func TestBlameBinarySkipped(t *testing.T) {
	dir, _ := historyRepo(t)
	writeFile(t, dir, "blob.bin", "text\x00binary\n")

	app, _ := openBlameFile(t, dir, "blob.bin")

	view := app.View()
	if view.BlameShow {
		t.Fatal("blame shows for a binary file")
	}
	if !strings.Contains(strings.ToLower(view.BlameNote), "binary") {
		t.Fatalf("binary note = %q", view.BlameNote)
	}
}

// TestBlameTruncatedSkipped shows the unavailable note instead of per-line
// blame for files cut at the line limit.
func TestBlameTruncatedSkipped(t *testing.T) {
	dir, _ := historyRepo(t)
	writeFile(t, dir, "long.txt", strings.Repeat("y\n", maxFileLines+1))

	app, _ := openBlameFile(t, dir, "long.txt")

	view := app.View()
	if view.BlameShow {
		t.Fatal("blame shows for a truncated file")
	}
	if !strings.Contains(view.BlameNote, "large") {
		t.Fatalf("truncated note = %q", view.BlameNote)
	}
}

// TestBlameRescanInvalidates pins the watcher contract: a workspace rescan
// retires cached blame lines and the next frame re-requests them.
func TestBlameRescanInvalidates(t *testing.T) {
	app, _ := newBlameApp(t)
	app.toggleBlame()
	waitForBlame(t, app)

	if _, ok := app.blameCache["app.go"]; !ok {
		t.Fatal("no cached blame before rescan")
	}

	next, delta, err := rescanDiskWorkspace(app.ws.Root, app.ws, app.scanLimit)
	if err != nil {
		t.Fatal(err)
	}
	app.installRescan(next, delta)

	if len(app.blameCache) != 0 {
		t.Fatalf("rescan kept %d cached blame entries", len(app.blameCache))
	}

	waitForBlame(t, app)
}
