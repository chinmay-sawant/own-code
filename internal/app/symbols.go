package app

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// maxWorkspaceSymbols caps the cross-file symbol index. The per-file
	// outline cap (60) times MaxWorkspaceFiles would reach 600k rows, so
	// the index stops at 20k with recency-biased ordering: open tabs and
	// recent files index first.
	maxWorkspaceSymbols = 20000
	// symbolIndexTimeout bounds one index build. The palette paints file
	// results immediately; the build resumes where it stopped on the next
	// symbol query instead of blocking the UI.
	symbolIndexTimeout = time.Second
)

// workspaceSymbol is one outline entry with the file it came from. Line is
// 0-based; No (Line+1) is the display line.
type workspaceSymbol struct {
	Name string
	Kind string
	Path string
	Line int
}

// No is the 1-based display line.
func (s workspaceSymbol) No() int { return s.Line + 1 }

// symbolIndex is the lazy cross-file symbol cache. It is never built on
// startup: the first symbol query (or an idle pass) builds it, and later
// queries extend it until every outline-supported file is covered.
type symbolIndex struct {
	byPath   map[string][]workspaceSymbol
	sizes    map[string]int64
	flat     []workspaceSymbol
	order    []string
	next     int
	complete bool
	// Truncated reports that the flat list stopped at maxWorkspaceSymbols.
	// Note explains the cap the way workspaceSearchNote does.
	Truncated bool
	Note      string
	Millis    int64
}

// outlineLangs are the languages outline() covers. Files in any other
// language are skipped silently: they count toward the coverage gap in the
// footer note, not an error.
func outlineLangs(lang string) bool {
	switch lang {
	case langGo, langPython, langJS, langMarkdown, langCSS,
		langJSON, langYAML, langHTML, langShell:
		return true
	default:
		return false
	}
}

// symbolFilesOrdered returns the indexable files with recency bias: open
// tabs first, then recents, then the remaining files by shortest path.
func (a *App) symbolFilesOrdered() []*fileMeta {
	if a.ws == nil {
		return nil
	}

	rank := map[string]int{}
	order := []*fileMeta{}

	push := func(path string) {
		if _, seen := rank[path]; seen {
			return
		}

		if meta := a.ws.ByPath[path]; meta != nil {
			rank[path] = len(order)
			order = append(order, meta)
		}
	}

	for _, path := range a.tabs {
		push(path)
	}

	if a.split {
		for _, path := range a.tabs2 {
			push(path)
		}
	}

	for _, path := range a.recent {
		push(path)
	}

	rest := make([]*fileMeta, 0, len(a.ws.Files))

	for _, meta := range a.ws.Files {
		if _, seen := rank[meta.Path]; !seen {
			rest = append(rest, meta)
		}
	}

	sort.Slice(rest, func(i, j int) bool {
		if len(rest[i].Path) != len(rest[j].Path) {
			return len(rest[i].Path) < len(rest[j].Path)
		}

		return rest[i].Path < rest[j].Path
	})

	return append(order, rest...)
}

// ensureSymbolIndex builds (or resumes) the workspace symbol index under a
// 1s context. It never runs on startup: callers invoke it on the first
// symbol query, so a session that never uses Ctrl+T pays nothing.
func (a *App) ensureSymbolIndex() *symbolIndex {
	if a.symIndex != nil && (a.symIndex.complete || len(a.symIndex.flat) >= maxWorkspaceSymbols) {
		return a.symIndex
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), symbolIndexTimeout)
	defer cancel()

	if a.symIndex == nil {
		a.symIndex = &symbolIndex{
			byPath: map[string][]workspaceSymbol{},
			sizes:  map[string]int64{},
		}
		a.symIndex.order = nil
	}

	idx := a.symIndex

	if idx.order == nil {
		for _, meta := range a.symbolFilesOrdered() {
			idx.order = append(idx.order, meta.Path)
		}
	}

	for idx.next < len(idx.order) {
		if ctx.Err() != nil {
			break
		}

		path := idx.order[idx.next]
		idx.next++

		if syms, ok := idx.byPath[path]; ok && len(syms) > 0 {
			if meta := a.ws.ByPath[path]; meta != nil && idx.sizes[path] == meta.Size {
				continue
			}
		}

		syms := a.symbolsForFile(path)
		if syms == nil {
			continue
		}

		if meta := a.ws.ByPath[path]; meta != nil {
			idx.sizes[path] = meta.Size
		}

		if len(idx.flat)+len(syms) > maxWorkspaceSymbols {
			idx.Truncated = true
			idx.Note = "Showing first 20,000 symbols; open a file and use Ctrl+Shift+O for its full outline."
			idx.complete = false
			idx.Millis = time.Since(started).Milliseconds()

			return idx
		}

		idx.byPath[path] = syms
		idx.flat = append(idx.flat, syms...)
	}

	idx.Millis = time.Since(started).Milliseconds()

	if idx.next >= len(idx.order) {
		idx.complete = true

		if !idx.Truncated {
			idx.Note = ""
		}
	}

	return idx
}

// symbolsForFile reads one file through the outline extractor. Open
// documents reuse their lines so the disk is not re-read; Big, binary, and
// unsupported-language files return nil. External absolute files are
// file-scope only and never join the workspace index.
func (a *App) symbolsForFile(path string) []workspaceSymbol {
	if a.ws == nil {
		return nil
	}

	if filepath.IsAbs(filepath.FromSlash(path)) {
		return nil
	}

	meta := a.ws.ByPath[path]
	if meta == nil {
		return nil
	}

	if meta.Big || !outlineLangs(meta.Lang) {
		return nil
	}

	var lines []string

	if doc := a.docs[path]; doc != nil && doc.Err == "" && !doc.Binary && len(doc.Lines) > 0 {
		lines = doc.Lines
	} else {
		raw, err := a.ws.readFile(path)
		if err != nil || isBinary(raw) {
			return nil
		}

		text := strings.ReplaceAll(string(raw), "\r\n", "\n")
		text = strings.TrimSuffix(text, "\n")

		if text == "" {
			return nil
		}

		lines = strings.Split(text, "\n")
	}

	rows := outline(meta.Lang, lines)
	if len(rows) == 0 {
		return nil
	}

	out := make([]workspaceSymbol, 0, len(rows))

	for _, row := range rows {
		out = append(out, workspaceSymbol{Name: row.Name, Kind: row.Kind, Path: path, Line: row.Line})
	}

	return out
}

// cacheDocSymbols writes one open document's outline into the index. Open
// paths call it after openDoc so the index stays fresh without re-reading
// the file.
func (a *App) cacheDocSymbols(path string) {
	doc := a.docs[path]
	if doc == nil || doc.Err != "" || doc.Binary || len(doc.Outline) == 0 {
		return
	}

	if a.symIndex == nil {
		a.symIndex = &symbolIndex{
			byPath: map[string][]workspaceSymbol{},
			sizes:  map[string]int64{},
		}
	}

	fresh := make([]workspaceSymbol, 0, len(doc.Outline))
	for _, row := range doc.Outline {
		fresh = append(fresh, workspaceSymbol{Name: row.Name, Kind: row.Kind, Path: path, Line: row.Line})
	}

	a.symIndex.byPath[path] = fresh

	if meta := a.ws.ByPath[path]; meta != nil {
		a.symIndex.sizes[path] = meta.Size
	}

	// Keep the flat list consistent: drop the stale rows, then append the
	// fresh ones unless the cap is already reached.
	kept := a.symIndex.flat[:0]

	for _, sym := range a.symIndex.flat {
		if sym.Path != path {
			kept = append(kept, sym)
		}
	}

	a.symIndex.flat = kept

	if len(a.symIndex.flat)+len(fresh) <= maxWorkspaceSymbols {
		a.symIndex.flat = append(a.symIndex.flat, fresh...)
	} else if !a.symIndex.Truncated {
		a.symIndex.Truncated = true
		a.symIndex.Note = "Showing first 20,000 symbols; open a file and use Ctrl+Shift+O for its full outline."
	}
}

// invalidateSymbols drops per-path entries after a save or a watcher event.
// Unknown paths are ignored; with no paths the whole index is dropped.
func (a *App) invalidateSymbols(paths ...string) {
	if a.symIndex == nil {
		return
	}

	if len(paths) == 0 {
		a.symIndex = nil

		return
	}

	drop := map[string]bool{}
	for _, path := range paths {
		if path == "" {
			continue
		}

		drop[path] = true
		delete(a.symIndex.byPath, path)
		delete(a.symIndex.sizes, path)
	}

	if len(drop) == 0 {
		return
	}

	kept := a.symIndex.flat[:0]

	for _, sym := range a.symIndex.flat {
		if !drop[sym.Path] {
			kept = append(kept, sym)
		}
	}

	a.symIndex.flat = kept

	// A partial invalidation restarts the ordering so the changed files are
	// re-read on the next symbol query.
	a.symIndex.order = nil
	a.symIndex.next = 0
	a.symIndex.complete = false

	if len(a.symIndex.flat) < maxWorkspaceSymbols && a.symIndex.Truncated {
		a.symIndex.Truncated = false
		a.symIndex.Note = ""
	}
}

// symbolsFor returns the cached outline symbols for one path, computing them
// from the open document (write-through) or the extractor when missing.
func (a *App) symbolsFor(path string) []workspaceSymbol {
	if a.symIndex != nil {
		if syms, ok := a.symIndex.byPath[path]; ok {
			return syms
		}
	}

	if doc := a.docs[path]; doc != nil && doc.Err == "" && !doc.Binary {
		a.cacheDocSymbols(path)

		if a.symIndex != nil {
			return a.symIndex.byPath[path]
		}

		return nil
	}

	syms := a.symbolsForFile(path)
	if syms == nil {
		return nil
	}

	if a.symIndex == nil {
		a.symIndex = &symbolIndex{
			byPath: map[string][]workspaceSymbol{},
			sizes:  map[string]int64{},
		}
	}

	a.symIndex.byPath[path] = syms

	return syms
}

// parseSymbolQuery splits a symbol query into its name part, an optional
// current-file scope (@ prefix, VS Code compat), and an optional :line
// suffix that jumps to an explicit line.
func parseSymbolQuery(raw string) (name, scope string, line int) {
	query := strings.TrimSpace(raw)

	if strings.HasPrefix(query, "@") {
		scope = "@"
		query = strings.TrimSpace(strings.TrimPrefix(query, "@"))
	}

	if i := strings.LastIndexByte(query, ':'); i >= 0 {
		if n := parseLineSuffix(strings.TrimSpace(query[i+1:])); n > 0 {
			line = n - 1
			query = strings.TrimSpace(query[:i])
		}
	}

	return query, scope, line
}

// openSymbolAction opens a symbol palette row: the file, then the
// definition line encoded as path:line (0-based).
func (a *App) openSymbolAction(rest string) {
	path, line := cutSymbolAction(rest)
	if path == "" {
		return
	}

	a.openFilePreview(path)
	a.outlineLine(line)
}

// cutSymbolAction splits a path:line action suffix. A non-numeric suffix
// keeps the whole rest as the path at line 0.
func cutSymbolAction(rest string) (string, int) {
	if i := strings.LastIndexByte(rest, ':'); i >= 0 {
		if n := parseLineSuffix(rest[i+1:]); n > 0 {
			return rest[:i], n - 1
		}
	}

	return rest, 0
}

// parseLineSuffix parses a 1-based :line suffix.
func parseLineSuffix(s string) int {
	if s == "" {
		return 0
	}

	n := 0

	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}

		n = n*10 + int(r-'0')
	}

	return n
}

// matchSymbols scores the index against the current symbol query. Scope is
// "" for the whole workspace or a path for file scope (Ctrl+Shift+O and the
// @ prefix). Tiers: name prefix, then name contains, then path contains.
func (a *App) matchSymbols() []workspaceSymbol {
	name, atScope, _ := parseSymbolQuery(a.symbolQuery)

	scope := a.symbolScope
	if atScope != "" {
		scope = a.activeDocPath()
	}

	lower := strings.ToLower(name)

	idx := a.ensureSymbolIndex()

	var pool []workspaceSymbol

	if scope != "" {
		pool = a.symbolsFor(scope)
	} else if idx != nil {
		pool = idx.flat
	}

	if lower == "" {
		out := make([]workspaceSymbol, 0, paletteMaxRows)
		for _, sym := range pool {
			if len(out) >= paletteMaxRows {
				break
			}

			out = append(out, sym)
		}

		return out
	}

	rank := a.symbolRecency()

	var prefix, contains, pathHit []workspaceSymbol

	for _, sym := range pool {
		if scope == "" && sym.Path == "" {
			continue
		}

		n := strings.ToLower(sym.Name)

		switch {
		case strings.HasPrefix(n, lower):
			prefix = append(prefix, sym)
		case strings.Contains(n, lower):
			contains = append(contains, sym)
		case strings.Contains(strings.ToLower(sym.Path), lower):
			pathHit = append(pathHit, sym)
		}
	}

	byRecency := func(list []workspaceSymbol) {
		sort.SliceStable(list, func(i, j int) bool {
			ri, rj := rank[list[i].Path], rank[list[j].Path]
			if ri != rj {
				return ri < rj
			}

			if list[i].Name != list[j].Name {
				return list[i].Name < list[j].Name
			}

			return list[i].Line < list[j].Line
		})
	}

	byRecency(prefix)
	byRecency(contains)
	byRecency(pathHit)

	out := make([]workspaceSymbol, 0, paletteMaxRows)

	for _, tier := range [][]workspaceSymbol{prefix, contains, pathHit} {
		for _, sym := range tier {
			if len(out) >= paletteMaxRows {
				return out
			}

			out = append(out, sym)
		}
	}

	return out
}

// symbolRecency ranks paths with open tabs and recents first so symbol
// matches prefer the files the user already touches.
func (a *App) symbolRecency() map[string]int {
	rank := map[string]int{}
	next := 0

	consider := func(path string) {
		if _, seen := rank[path]; !seen {
			rank[path] = next
			next++
		}
	}

	for _, path := range a.tabs {
		consider(path)
	}

	if a.split {
		for _, path := range a.tabs2 {
			consider(path)
		}
	}

	for _, path := range a.recent {
		consider(path)
	}

	return rank
}
