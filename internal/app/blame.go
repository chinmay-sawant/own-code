package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// blameGutterW is the 1x blame column width in px: a 7-char short hash
	// in the 10 px mono face plus headroom. Uncommitted lines read
	// "Not committed" and clip with an ellipsis; the full text stays in the
	// title. blameEditLeft is the code hit-test left edge while the column
	// shows (the 60 px mark+number gutter plus the blame column).
	blameGutterW  = 72
	blameEditLeft = 132
	// blameQueue/blameReady mirror the commit patch worker: clicks never
	// block on git and a full queue drops the newest request.
	blameQueue = 64
	blameReady = 64
	// maxBlameCache bounds the blame worker cache like the patch cache.
	maxBlameCache = 24
)

// blameLine is the last-commit annotation for one file line.
type blameLine struct {
	Hash        string
	Short       string
	Author      string
	Date        string
	Summary     string
	Uncommitted bool
}

// blameEntry is the cached blame result for one path at one stat key.
type blameEntry struct {
	Key   string
	Lines []blameLine
	Note  string
}

// blameResult is one finished blame load. Key is the stat key the request
// named so the page can match the result; Path names the file to install it
// under when the file has not changed since the request.
type blameResult struct {
	Key   string
	Path  string
	Lines []blameLine
	Err   error
}

// blameRequest names one file blame load at one stat key.
type blameRequest struct {
	Key  string
	Path string
}

// blameJob carries the request context alongside the request.
type blameJob struct {
	ctx context.Context
	req blameRequest
}

// blameLoader reads git blame on one worker goroutine, so toggling blame
// never waits for git. Request queues work, Poll drains finished results on
// the page loop, and the loader keeps the newest maxBlameCache results.
type blameLoader struct {
	load    func(ctx context.Context, path string) ([]blameLine, error)
	reqs    chan blameJob
	results chan blameResult
	quit    chan struct{}
	done    chan struct{}
	once    sync.Once
	ctx     context.Context
	cancel  context.CancelFunc
	cache   map[string][]blameLine
	order   []string
}

// NewBlameLoader starts the blame worker for root. Close must be called to
// stop the worker.
func NewBlameLoader(root string) *blameLoader {
	ctx, cancel := context.WithCancel(context.Background())

	loader := &blameLoader{
		load: func(ctx context.Context, path string) ([]blameLine, error) {
			return loadBlame(ctx, root, path)
		},
		reqs:    make(chan blameJob, blameQueue),
		results: make(chan blameResult, blameReady),
		quit:    make(chan struct{}),
		done:    make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
		cache:   make(map[string][]blameLine, maxBlameCache),
	}

	go loader.run()

	return loader
}

// Request queues one blame load. It never blocks: a full queue or a closed
// loader drops the request, and the caller can ask again on a later frame.
func (l *blameLoader) Request(ctx context.Context, key, path string) {
	if l == nil || key == "" || path == "" || ctx == nil {
		return
	}

	select {
	case <-l.quit:
		return
	default:
	}

	select {
	case l.reqs <- blameJob{ctx: ctx, req: blameRequest{Key: key, Path: path}}:
	default:
	}
}

// Poll returns one finished blame load, or false when none is ready. It
// never waits.
func (l *blameLoader) Poll() (blameResult, bool) {
	if l == nil {
		return blameResult{}, false
	}

	select {
	case result := <-l.results:
		return result, true
	default:
		return blameResult{}, false
	}
}

// Close cancels the in-flight load, drops queued work, and waits for the
// worker to exit. It is safe to call more than once and after a request.
func (l *blameLoader) Close() {
	if l == nil {
		return
	}

	l.once.Do(func() {
		close(l.quit)
		l.cancel()
	})

	<-l.done

	for {
		select {
		case <-l.results:
		default:
			return
		}
	}
}

// run serializes the blocking blame reads until Close.
func (l *blameLoader) run() {
	defer close(l.done)

	for {
		select {
		case job := <-l.reqs:
			l.loadJob(job)
		case <-l.quit:
			return
		}
	}
}

// loadJob answers from the cache or calls the loader. The caller context and
// Close both cancel the read, so a shutdown never waits out a git timeout.
func (l *blameLoader) loadJob(job blameJob) {
	if lines, ok := l.cache[job.req.Key]; ok {
		l.emit(blameResult{Key: job.req.Key, Path: job.req.Path, Lines: lines})

		return
	}

	ctx, cancel := context.WithCancel(l.ctx)
	defer cancel()

	stop := context.AfterFunc(job.ctx, cancel)
	defer stop()

	lines, err := l.load(ctx, job.req.Path)
	if err != nil {
		l.emit(blameResult{Key: job.req.Key, Path: job.req.Path, Err: err})

		return
	}

	l.store(job.req.Key, lines)
	l.emit(blameResult{Key: job.req.Key, Path: job.req.Path, Lines: lines})
}

// store adds one blame result and evicts the oldest once over capacity.
func (l *blameLoader) store(key string, lines []blameLine) {
	if _, seen := l.cache[key]; !seen {
		l.order = append(l.order, key)
	}

	l.cache[key] = lines

	for len(l.order) > maxBlameCache {
		oldest := l.order[0]
		l.order = l.order[1:]
		delete(l.cache, oldest)
	}
}

// emit offers one result without blocking the worker. A result that finishes
// during or after Close is stale and dropped.
func (l *blameLoader) emit(result blameResult) {
	select {
	case <-l.quit:
		return
	default:
	}

	select {
	case l.results <- result:
	default:
	}
}

// loadBlame runs git blame --porcelain for one workspace-relative path and
// parses the per-line commits. The caller skips binary, big, and truncated
// files first, so the output stays bounded by the open limits.
func loadBlame(ctx context.Context, root, path string) ([]blameLine, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	if path == "" {
		return nil, fmt.Errorf("empty blame path")
	}

	raw, err := gitBytes(ctx, root, "blame", "--porcelain", "--", path)
	if err != nil {
		return nil, err
	}

	return parseBlamePorcelain(string(raw)), nil
}

// parseBlamePorcelain turns git blame --porcelain output into one entry per
// file line. Each "\t<content>" line closes the current block header: the
// author, date, and summary seen since that header belong to the line. A
// block whose hash is all zeros is an uncommitted line. Unknown lines are
// ignored so a newer git never breaks the gutter.
func parseBlamePorcelain(out string) []blameLine {
	var lines []blameLine

	cur := blameLine{}
	have := false

	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "\t") {
			if !have {
				continue
			}

			lines = append(lines, cur)

			continue
		}

		if isBlameHeader(ln) {
			hash := ln[:commitHashLen]
			cur = blameLine{Hash: hash, Short: blameShort(hash)}
			if isZeroHash(hash) {
				cur.Uncommitted = true
			}
			have = true

			continue
		}

		if !have {
			continue
		}

		switch {
		case strings.HasPrefix(ln, "author "):
			cur.Author = strings.TrimPrefix(ln, "author ")
		case strings.HasPrefix(ln, "author-time "):
			cur.Date = blameDate(strings.TrimPrefix(ln, "author-time "))
		case strings.HasPrefix(ln, "summary "):
			cur.Summary = strings.TrimPrefix(ln, "summary ")
		}
	}

	return lines
}

// isBlameHeader reports whether a porcelain line opens a blame block: a
// 40-char hex hash followed by a space and the line numbers.
func isBlameHeader(ln string) bool {
	if len(ln) < commitHashLen+1 || ln[commitHashLen] != ' ' {
		return false
	}

	for i := 0; i < commitHashLen; i++ {
		c := ln[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}

	return true
}

// isZeroHash reports the all-zeros hash git uses for uncommitted lines.
func isZeroHash(hash string) bool {
	for i := 0; i < len(hash); i++ {
		if hash[i] != '0' {
			return false
		}
	}

	return len(hash) > 0
}

// blameShort is the 7-char gutter hash, or "" for uncommitted lines.
func blameShort(hash string) string {
	if isZeroHash(hash) || len(hash) < 7 {
		return ""
	}

	return hash[:7]
}

// blameDate formats a porcelain author-time stamp as YYYY-MM-DD. Bad input
// stays blank instead of breaking the hover title.
func blameDate(raw string) string {
	secs, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return ""
	}

	return time.Unix(secs, 0).Format("2006-01-02")
}

// blameDisplay is the gutter text for one line: the short hash, or
// "Not committed" for uncommitted lines.
func blameDisplay(line blameLine) string {
	if line.Uncommitted {
		return "Not committed"
	}

	return line.Short
}

// blameTitle is the hover text for one line: short hash, author, date, and
// summary, or the uncommitted note.
func blameTitle(line blameLine) string {
	if line.Uncommitted {
		return "Not committed"
	}

	title := line.Short
	if line.Author != "" {
		title += " · " + line.Author
	}
	if line.Date != "" {
		title += " · " + line.Date
	}
	if line.Summary != "" {
		title += " · " + line.Summary
	}

	return title
}

// blameStatKey keys the blame cache by path plus size and mtime, so a save
// (or any outside write) retires the cached lines. It reports false when the
// file cannot be stated.
func (a *App) blameStatKey(path string) (string, bool) {
	if a.ws == nil || a.ws.Sample || path == "" {
		return "", false
	}

	full := filepath.Join(a.ws.Root, filepath.FromSlash(path))
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return "", false
	}

	return path + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" +
		strconv.FormatInt(info.ModTime().UnixNano(), 10), true
}

// blameSkipNote explains why blame stays off one document. "" means blame
// may show: a text file inside a git workspace with no open errors.
func (a *App) blameSkipNote(doc *fileDoc) string {
	if doc == nil {
		return ""
	}

	if a.ws != nil && a.ws.Sample {
		return "Blame is unavailable in the sample workspace."
	}

	if doc.Binary {
		return "Blame is unavailable for binary files."
	}

	if doc.Truncated {
		return "Blame is unavailable for large files."
	}

	if a.ws != nil {
		if meta := a.ws.ByPath[doc.Path]; meta != nil && meta.Big {
			return "Blame is unavailable for large files."
		}
	}

	if doc.Err != "" {
		return "Blame is unavailable: " + doc.Err
	}

	return ""
}

// blameShows reports whether the blame column paints for one document.
func (a *App) blameShows(doc *fileDoc) bool {
	return a.blameOn && doc != nil && a.blameSkipNote(doc) == ""
}

// blameStatus is the crumbs state for one document: the skip or busy note
// and whether the column shows. A cache miss queues the async load and
// reports the busy note; the column appears when pollBlame installs the
// result. It never blocks the page loop.
func (a *App) blameStatus(doc *fileDoc) (note string, show bool) {
	if !a.blameOn || doc == nil {
		return "", false
	}

	if skip := a.blameSkipNote(doc); skip != "" {
		return skip, false
	}

	key, ok := a.blameStatKey(doc.Path)
	if !ok {
		return "Blame is unavailable for this file.", false
	}

	if entry, hit := a.blameCache[doc.Path]; hit && entry.Key == key {
		if entry.Note != "" {
			return entry.Note, false
		}

		return "", true
	}

	a.ensureBlame(doc.Path, key)

	return "Loading blame…", false
}

// ensureBlame queues one async blame load unless the result is already
// cached or in flight. Queues never block: a full queue drops the request
// and the next frame asks again.
func (a *App) ensureBlame(path, key string) {
	if path == "" || key == "" || a.ws == nil || a.ws.Sample {
		return
	}

	if entry, hit := a.blameCache[path]; hit && entry.Key == key {
		return
	}

	if a.blamePending == nil {
		a.blamePending = map[string]bool{}
	}

	if a.blamePending[key] {
		return
	}

	if a.blameLoader == nil {
		// Fallback for an App built by a test without New: the worker lives
		// for the app and Close cancels its in-flight read.
		a.blameLoader = NewBlameLoader(a.ws.Root) //nolint:contextcheck // app-lifetime blame worker
	}

	a.blamePending[key] = true

	// The loader owns a process-lifetime context; this call is not tied to a
	// request, and Close cancels an in-flight read.
	a.blameLoader.Request(context.Background(), key, path) //nolint:contextcheck // app-lifetime blame worker
}

// pollBlame drains finished blame loads on the page loop. Results refresh
// the gutter when blame is on; a stale key (saved while in flight) stays
// cached under its old key and the next frame re-requests the new one.
func (a *App) pollBlame(ctx context.Context) bool {
	if a.blameLoader == nil {
		return false
	}

	changed := false

	for {
		result, ok := a.blameLoader.Poll()
		if !ok {
			break
		}

		delete(a.blamePending, result.Key)

		if a.blameCache == nil {
			a.blameCache = map[string]blameEntry{}
		}

		if result.Err != nil {
			a.blameCache[result.Path] = blameEntry{Key: result.Key, Note: "Blame unavailable: " + result.Err.Error()}
		} else {
			a.blameCache[result.Path] = blameEntry{Key: result.Key, Lines: result.Lines}
		}

		changed = true
	}

	if changed && a.blameOn {
		a.refresh()
		_ = a.page.Redraw(ctx)
	}

	return changed
}

// blameLinesFor returns the cached blame lines for one path, or nil while
// the load is in flight or skipped.
func (a *App) blameLinesFor(path string) []blameLine {
	entry, ok := a.blameCache[path]
	if !ok || entry.Note != "" {
		return nil
	}

	key, statOK := a.blameStatKey(path)
	if !statOK || entry.Key != key {
		return nil
	}

	return entry.Lines
}

// decorateBlame adds the per-line blame annotation to the visible code rows.
// Only the first visual row of a wrapped line carries the annotation
// (continuation rows have No zero); gutter marks and line numbers are
// untouched so all three compose.
func (a *App) decorateBlame(doc *fileDoc, rows []codeRow, group int) {
	_ = group

	if !a.blameShows(doc) || len(rows) == 0 {
		return
	}

	lines := a.blameLinesFor(doc.Path)
	if len(lines) == 0 {
		return
	}

	for i := range rows {
		row := &rows[i]
		if row.No == 0 {
			continue
		}

		line := row.Line + 1
		if line < 1 || line > len(lines) {
			continue
		}

		entry := lines[line-1]
		if entry.Hash == "" && !entry.Uncommitted {
			continue
		}

		row.Blame = blameDisplay(entry)
		row.BlameTitle = blameTitle(entry)
		if !entry.Uncommitted {
			row.BlameHash = entry.Hash
		}
	}
}

// toggleBlame flips the blame gutter and keeps the top source line in view,
// mirroring toggleWrap. The column paints once the async load arrives; the
// crumbs carry the busy note until then.
func (a *App) toggleBlame() {
	line := a.topSourceLine()
	a.blameOn = !a.blameOn
	a.viewportCols = 0

	if doc := a.activeDoc(); doc != nil && a.wrap {
		a.scrollTo(doc.wrapped(a.codeCols()).firstRow(line) * a.zx(rowH))

		return
	}

	a.scrollTo(line * a.zx(rowH))
}

// openBlameCommit opens the clicked blame hash in the commit diff view,
// reusing the selectCommitFile path (which fetches the patch through the
// commit worker). Commits outside the loaded history cannot open.
func (a *App) openBlameCommit(ctx context.Context, hash string) {
	if len(hash) != commitHashLen || !isHexHash(hash) {
		a.showToast("info", "Blame", "No commit is attached to this line.")
		a.refresh()

		return
	}

	commit := a.findCommit(hash)
	if commit == nil {
		a.showToast("info", "Blame", "That commit is not in the loaded history. Open Source Control to load it.")
		a.refresh()

		return
	}

	// The commit diff lives in the changes mode; mirror the toggleCommit
	// entry so a click from the explorer lands on the patch.
	if a.mode == "merge" {
		a.leaveMergeMode()
	}
	a.mode = "changes"

	path := ""
	if doc := a.activeDoc(); doc != nil {
		for _, file := range commit.Files {
			if file.Path == doc.Path {
				path = doc.Path

				break
			}
		}
	}

	if path == "" && len(commit.Files) > 0 {
		path = commit.Files[0].Path
	}

	a.selectCommitFile(ctx, hash, path)
}

// isHexHash reports whether s is a full lowercase or uppercase hex commit
// hash.
func isHexHash(s string) bool {
	if len(s) != commitHashLen {
		return false
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}

	return true
}

// invalidateBlame drops the cached blame for one path, so a save (or any
// write) re-requests it under the new stat key on the next frame.
func (a *App) invalidateBlame(path string) {
	if path == "" {
		return
	}

	delete(a.blameCache, path)
}

// invalidateAllBlame drops every cached blame line, for workspace switches
// and git refreshes that can move every file's blame.
func (a *App) invalidateAllBlame() {
	a.blameCache = map[string]blameEntry{}
	a.blamePending = map[string]bool{}
}
