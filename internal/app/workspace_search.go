package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const (
	workspaceSearchLimit      = 200
	workspaceSearchFileHits   = 8
	workspaceSearchTextRunes  = 240
	workspaceSearchCancelNote = "Search cancelled."
	// workspaceReplaceCeil is the hard ceiling for one workspace replace-all.
	// Above it the apply pass refuses and asks for a narrower query. The
	// preview counter runs one past the ceiling so a capped total reads as a
	// lower bound (2000+).
	workspaceReplaceCeil = 2000
	// replaceJournalCapBytes bounds the in-memory undo journal for one
	// workspace replace. Past it the journal drops and the UI disables
	// workspace-undo with a note instead of growing without bound.
	replaceJournalCapBytes = 5 << 20
)

// workspaceHit is one occurrence of a workspace query. Line and Col are
// 1-based, and Col counts runes in the display line, so a tab is four spaces.
// Before, Match, and After split Text around the occurrence. Replacement is
// the replace-mode preview for this hit and is empty in search-only passes.
type workspaceHit struct {
	Path        string
	Line        int
	Col         int
	Text        string
	Before      string
	Match       string
	After       string
	Replacement string
}

// workspaceSearchResult is one pass over the workspace. Files counts the
// distinct paths in Hits, not every file that was scanned. Note explains a
// truncated or cancelled pass. Total counts every match the scan saw, which
// can exceed len(Hits) when the preview caps stopped storing; Capped reports
// that Total itself stopped at the count ceiling and is a lower bound.
type workspaceSearchResult struct {
	Query     string
	Hits      []workspaceHit
	Files     int
	Truncated bool
	Note      string
	Total     int
	Capped    bool
	// Skipped counts the files the scan refused without reading fully:
	// gitignored, oversized, binary, and unreadable. The apply pass adds
	// its own recheck counts on top.
	Skipped replaceSkipped
}

// searchEligibleFile is the single size predicate shared by the preview
// scan and the replace apply pass: oversized files never match. Callers that
// already read the bytes additionally refuse binary content and recheck the
// size, since metadata can be stale.
func searchEligibleFile(meta *fileMeta) bool {
	if meta == nil {
		return false
	}

	return !meta.Big && meta.Size <= maxFileBytes
}

// searchWorkspace scans the text files of ws for query. An empty query has no
// hits. A file larger than maxFileBytes or one that looks binary is skipped
// before its bytes are read. Matches are non-overlapping, at most
// workspaceSearchFileHits per file, and the total is capped at limit, or
// workspaceSearchLimit when limit is not positive.
func searchWorkspace(ctx context.Context, ws *workspace, query string, caseSensitive bool, limit int) workspaceSearchResult {
	return searchWorkspaceFiltered(ctx, ws, query, caseSensitive, limit, nil)
}

// searchWorkspaceFiltered scans with a custom-excludes filter. Excluded
// files are skipped before their bytes are read and counted in the result
// Skipped tally, which the replace footer reports. Custom scan/search
// patterns are additionally counted in the Note ("N files skipped by
// excludes"); the cancel note stays pure so callers can match it exactly.
func searchWorkspaceFiltered(ctx context.Context, ws *workspace, query string, caseSensitive bool, limit int, filter *scanExcludes) workspaceSearchResult {
	result := searchWorkspaceOptsFiltered(ctx, ws, query, searchOpts{Case: caseSensitive}, limit, workspaceSearchFileHits, filter)
	if filter != nil && result.Note != workspaceSearchCancelNote {
		if n := countCustomSearchExcluded(ws, filter); n > 0 {
			if result.Note != "" {
				result.Note += " "
			}
			result.Note += excludesSkippedNote(n)
		}
	}
	return result
}

// searchWorkspaceOpts scans ws with the shared matcher. Stored previews stop
// at limit total and perFile per file, but Total keeps counting every match
// up to the replace ceiling so the footer can say what the preview hides.
// An invalid regex matches nothing; the caller surfaces m.Err inline.
func searchWorkspaceOpts(ctx context.Context, ws *workspace, query string, o searchOpts, limit, perFile int) workspaceSearchResult {
	return searchWorkspaceOptsFiltered(ctx, ws, query, o, limit, perFile, nil)
}

func searchWorkspaceOptsFiltered(ctx context.Context, ws *workspace, query string, o searchOpts, limit, perFile int, filter *scanExcludes) workspaceSearchResult {
	result := workspaceSearchResult{Query: query}
	if ws == nil || query == "" {
		return result
	}

	if limit <= 0 {
		limit = workspaceSearchLimit
	}

	if perFile <= 0 {
		perFile = workspaceSearchFileHits
	}

	m := compileMatcher(query, o)
	if !m.valid || m.Err != "" {
		return result
	}

	if m.empty {
		return result
	}

	limitStopped := false
	perFileStopped := false

search:
	for _, meta := range ws.Files {
		if meta == nil {
			continue
		}

		if ctx.Err() != nil {
			result.Truncated = perFileStopped
			result.Note = workspaceSearchCancelNote

			return finishWorkspaceSearch(result)
		}

		// Custom search excludes (plus gitignored files when
		// searchIgnored=false) skip before bytes are read. Without a
		// filter the gitignored flag excludes by default.
		if filter != nil {
			if filter.excludedBySearch(meta.Path, meta.Ignored) {
				result.Skipped.Ignored++
				continue
			}
		} else if meta.Ignored {
			result.Skipped.Ignored++
			continue
		}

		if !searchEligibleFile(meta) {
			result.Skipped.Large++
			continue
		}

		raw, err := ws.readFile(meta.Path)
		if err != nil {
			result.Skipped.ReadErr++
			continue
		}

		if len(raw) > maxFileBytes {
			result.Skipped.Large++
			continue
		}

		if isBinary(raw) {
			result.Skipped.Binary++
			continue
		}

		text := strings.TrimPrefix(string(raw), "\ufeff")
		fileHits := 0

		for lineIndex, rawLine := range strings.Split(text, "\n") {
			if ctx.Err() != nil {
				result.Truncated = perFileStopped
				result.Note = workspaceSearchCancelNote

				return finishWorkspaceSearch(result)
			}

			line := []rune(expandTabs(rawLine))
			spans := m.spansInLine(line)
			if len(spans) == 0 {
				continue
			}

			for _, span := range spans {
				if result.Total >= workspaceReplaceCeil {
					result.Capped = true

					break search
				}

				result.Total++

				if len(result.Hits) >= limit {
					limitStopped = true
					continue
				}

				if fileHits >= perFile {
					perFileStopped = true
					continue
				}

				start, end := span.Start, span.End
				previewStart := max(0, start-(workspaceSearchTextRunes-min(end-start, workspaceSearchTextRunes))/2)
				previewStart = min(previewStart, max(0, len(line)-workspaceSearchTextRunes))
				previewEnd := min(len(line), previewStart+workspaceSearchTextRunes)
				matchEnd := min(end, previewEnd)
				result.Hits = append(result.Hits, workspaceHit{
					Path:   meta.Path,
					Line:   lineIndex + 1,
					Col:    start + 1,
					Text:   string(line[previewStart:previewEnd]),
					Before: string(line[previewStart:start]),
					Match:  string(line[start:matchEnd]),
					After:  string(line[matchEnd:previewEnd]),
				})
				fileHits++
			}
		}
	}

	result.Truncated = limitStopped || perFileStopped
	result.Note = workspaceSearchNote(limitStopped, perFileStopped, limit)

	return finishWorkspaceSearch(result)
}

// workspaceMatchAt reports whether candidate starts with query. It delegates
// to the shared matcher helper so the workspace scan and the find bar fold
// case the same way.
func workspaceMatchAt(candidate, query []rune, caseSensitive bool) bool {
	return matchRunesAt(candidate, query, caseSensitive)
}

// workspaceRuneEqualFold compares one rune under Unicode simple folding. It
// delegates to the shared matcher helper.
func workspaceRuneEqualFold(a, b rune) bool {
	return runeEqualFold(a, b)
}

// workspaceSearchNote describes why a pass stopped before its last match.
func workspaceSearchNote(limitStopped, perFileStopped bool, limit int) string {
	var notes []string

	if limitStopped {
		notes = append(notes, fmt.Sprintf("Showing the first %d matches; more were found.", limit))
	}

	if perFileStopped {
		notes = append(notes, fmt.Sprintf("Some files have more than %d matches.", workspaceSearchFileHits))
	}

	return strings.Join(notes, " ")
}

// finishWorkspaceSearch orders the hits and counts the files they came from.
func finishWorkspaceSearch(result workspaceSearchResult) workspaceSearchResult {
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

	for i, hit := range result.Hits {
		if i == 0 || hit.Path != result.Hits[i-1].Path {
			result.Files++
		}
	}

	return result
}
