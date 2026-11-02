package app

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// searchRipgrep streams content hits from disk via ripgrep, covering
// unexpanded (lazy) directories. It respects .gitignore by default (never
// --no-ignore), caps at limit total / workspaceSearchFileHits per file, and
// skips files over maxFileBytes (via --max-filesize when available, else
// post-hoc stat). rg flags are version-tolerant with post-hoc size/skipExt
// filtering. Every unit carries ctx+token+Busy/Note+stale-drop via the
// searchOps wrapper.
func searchRipgrep(ctx context.Context, root, query string, caseSensitive bool, limit int) (workspaceSearchResult, bool) {
	result := workspaceSearchResult{Query: query}
	if root == "" || root == "sample" || query == "" {
		return result, false
	}
	if limit <= 0 {
		limit = workspaceSearchLimit
	}
	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return result, false
	}
	// Try --json first (structured, streaming), fall back to --vimgrep.
	if hits, ok := searchRipgrepJSON(ctx, rgPath, root, query, caseSensitive, limit); ok {
		return hits, true
	}
	return searchRipgrepVimgrep(ctx, rgPath, root, query, caseSensitive, limit)
}

func rgBaseArgs(query string, caseSensitive bool) []string {
	args := []string{"--hidden", "--glob", "!node_modules/**", "--glob", "!dist/**", "--glob", "!build/**", "--glob", "!.git/**"}
	if !caseSensitive {
		args = append(args, "--ignore-case")
	}
	// Binary-aware + size cap; old rg ignores unknown flags via fallback below.
	return args
}

func searchRipgrepJSON(ctx context.Context, rgPath, root, query string, caseSensitive bool, limit int) (workspaceSearchResult, bool) {
	result := workspaceSearchResult{Query: query}
	args := append(rgBaseArgs(query, caseSensitive), "--json", "--max-count", "8", "--max-filesize", "512K", "--", query, ".")
	cmd := exec.CommandContext(ctx, rgPath, args...)
	cmd.Dir = root
	hideConsoleWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return result, false
	}
	if err := cmd.Start(); err != nil {
		return result, false
	}
	perFile := map[string]int{}
	limitStopped := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 256*1024), 2*1024*1024)
	sawMatch := false
	for scanner.Scan() {
		if ctx.Err() != nil {
			_ = cmd.Process.Kill()
			result.Truncated = false
			result.Note = workspaceSearchCancelNote
			_ = cmd.Wait()
			return finishWorkspaceSearch(result), true
		}
		var msg struct {
			Type string `json:"type"`
			Data struct {
				Path struct {
					Text string `json:"text"`
				} `json:"path"`
				LineNumber int `json:"line_number"`
				Submatches []struct {
					Match struct {
						Text string `json:"text"`
					} `json:"match"`
					Start int `json:"start"`
					End   int `json:"end"`
				} `json:"submatches"`
				Lines struct {
					Text string `json:"text"`
				} `json:"lines"`
			} `json:"data"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Type != "match" {
			continue
		}
		sawMatch = true
		rel := filepath.ToSlash(msg.Data.Path.Text)
		rel = strings.TrimPrefix(rel, "./")
		if skipExt(rel) {
			continue
		}
		base := rel
		if i := strings.LastIndexByte(rel, '/'); i >= 0 {
			base = rel[i+1:]
		}
		if strings.HasPrefix(base, ".") && base != ".gitignore" && base != ".env.example" {
			continue
		}
		if perFile[rel] >= workspaceSearchFileHits {
			continue
		}
		// Post-hoc size guard for old rg without --max-filesize.
		if info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); statErr == nil {
			if info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxFileBytes {
				continue
			}
		}
		if len(result.Hits) >= limit {
			limitStopped = true
			break
		}
		lineText := strings.TrimRight(msg.Data.Lines.Text, "\r\n")
		expanded := expandTabs(lineText)
		runes := []rune(expanded)
		for _, sm := range msg.Data.Submatches {
			if perFile[rel] >= workspaceSearchFileHits || len(result.Hits) >= limit {
				if len(result.Hits) >= limit {
					limitStopped = true
				}
				break
			}
			// rg --json reports byte offsets; convert approximately via rune scan.
			// For ASCII (common) bytes==runes; for others, locate first occurrence.
			col := sm.Start + 1
			matchText := sm.Match.Text
			if matchText == "" {
				continue
			}
			// Recompute col by searching runes for robustness.
			if idx := strings.Index(expanded, matchText); idx >= 0 {
				col = len([]rune(expanded[:idx])) + 1
			}
			start := col - 1
			end := start + len([]rune(matchText))
			if start < 0 {
				start = 0
			}
			if end > len(runes) {
				end = len(runes)
			}
			previewStart := max(0, start-(workspaceSearchTextRunes-min(len([]rune(matchText)), workspaceSearchTextRunes))/2)
			previewStart = min(previewStart, max(0, len(runes)-workspaceSearchTextRunes))
			previewEnd := min(len(runes), previewStart+workspaceSearchTextRunes)
			matchEnd := min(end, previewEnd)
			if start > previewEnd || matchEnd < previewStart {
				continue
			}
			result.Hits = append(result.Hits, workspaceHit{
				Path:   rel,
				Line:   msg.Data.LineNumber,
				Col:    start + 1,
				Text:   string(runes[previewStart:previewEnd]),
				Before: string(runes[previewStart:start]),
				Match:  string(runes[start:matchEnd]),
				After:  string(runes[matchEnd:previewEnd]),
			})
			perFile[rel]++
		}
	}
	_ = cmd.Wait()
	if !sawMatch && len(result.Hits) == 0 {
		// No JSON matches: let caller try --vimgrep (old rg without --json).
		return result, false
	}
	result.Truncated = limitStopped
	if limitStopped {
		result.Note = workspaceSearchNote(true, false, limit)
	}
	// Scope-honest note for lazy: disk covers unexpanded too.
	if result.Note != "" {
		result.Note += " Searched disk (includes unexpanded folders)."
	} else {
		result.Note = "Searched disk (includes unexpanded folders)."
	}
	sort.Slice(result.Hits, func(i, j int) bool {
		a, b := result.Hits[i], result.Hits[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
	return finishWorkspaceSearch(result), true
}

func searchRipgrepVimgrep(ctx context.Context, rgPath, root, query string, caseSensitive bool, limit int) (workspaceSearchResult, bool) {
	result := workspaceSearchResult{Query: query}
	args := append(rgBaseArgs(query, caseSensitive), "--vimgrep", "--max-count", "8", "--max-filesize", "512K", "--", query, ".")
	cmd := exec.CommandContext(ctx, rgPath, args...)
	cmd.Dir = root
	hideConsoleWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return result, false
	}
	if err := cmd.Start(); err != nil {
		return result, false
	}
	perFile := map[string]int{}
	limitStopped := false
	sawLine := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 256*1024), 2*1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			_ = cmd.Process.Kill()
			result.Note = workspaceSearchCancelNote
			_ = cmd.Wait()
			return finishWorkspaceSearch(result), true
		}
		line := scanner.Text()
		// path:line:col:text
		parts := strings.SplitN(line, ":", 4)
		if len(parts) < 4 {
			continue
		}
		sawLine = true
		rel := filepath.ToSlash(strings.TrimPrefix(parts[0], "./"))
		if skipExt(rel) {
			continue
		}
		var lineNo, colNo int
		if _, err := parseVimgrepInts(parts[1], parts[2], &lineNo, &colNo); err != nil {
			continue
		}
		if perFile[rel] >= workspaceSearchFileHits {
			continue
		}
		if info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); statErr == nil {
			if info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxFileBytes {
				continue
			}
		}
		if len(result.Hits) >= limit {
			limitStopped = true
			break
		}
		text := expandTabs(parts[3])
		runes := []rune(text)
		// Find match at col (rg col is 1-based bytes; approximate with runes).
		start := colNo - 1
		if start < 0 {
			start = 0
		}
		if start > len(runes) {
			start = len(runes)
		}
		// Match length: use query rune length for literal; for regex, take to next space? Use query len as approximation.
		end := start + len([]rune(query))
		if end > len(runes) {
			end = len(runes)
		}
		previewStart := max(0, start-(workspaceSearchTextRunes-min(len([]rune(query)), workspaceSearchTextRunes))/2)
		previewStart = min(previewStart, max(0, len(runes)-workspaceSearchTextRunes))
		previewEnd := min(len(runes), previewStart+workspaceSearchTextRunes)
		matchEnd := min(end, previewEnd)
		result.Hits = append(result.Hits, workspaceHit{
			Path:   rel,
			Line:   lineNo,
			Col:    start + 1,
			Text:   string(runes[previewStart:previewEnd]),
			Before: string(runes[previewStart:start]),
			Match:  string(runes[start:matchEnd]),
			After:  string(runes[matchEnd:previewEnd]),
		})
		perFile[rel]++
	}
	_ = cmd.Wait()
	if !sawLine {
		return result, false
	}
	result.Truncated = limitStopped
	if limitStopped {
		result.Note = workspaceSearchNote(true, false, limit)
	}
	if result.Note != "" {
		result.Note += " Searched disk (includes unexpanded folders)."
	} else {
		result.Note = "Searched disk (includes unexpanded folders)."
	}
	return finishWorkspaceSearch(result), true
}

func parseVimgrepInts(lineS, colS string, lineNo, colNo *int) (int, error) {
	var l, c int
	for _, r := range lineS {
		if r < '0' || r > '9' {
			return 0, errVimgrep
		}
		l = l*10 + int(r-'0')
	}
	for _, r := range colS {
		if r < '0' || r > '9' {
			return 0, errVimgrep
		}
		c = c*10 + int(r-'0')
	}
	*lineNo, *colNo = l, c
	return 0, nil
}

var errVimgrep = errVimgrepSentinel()

func errVimgrepSentinel() error { return &vimgrepError{} }

type vimgrepError struct{}

func (e *vimgrepError) Error() string { return "vimgrep parse" }
