package app

import (
	"context"
	"fmt"
)

const searchWindowRows = 8

const searchRowHeight = 44

// searchReply carries one async workspace search back to the page loop.
type searchReply struct {
	Token  int
	Result workspaceSearchResult
}

// searchViewState builds the palette-like content search panel.
func (a *App) searchViewState() searchView {
	s := searchView{
		Open:           a.searchOpen,
		Query:          a.searchQuery,
		Case:           a.searchCase,
		Index:          a.searchIndex,
		Files:          countSearchFiles(a.searchHits),
		Total:          len(a.searchHits),
		Note:           a.searchNote,
		Truncated:      a.searchTruncated,
		Busy:           a.searchBusy,
		Replace:        a.searchReplace,
		Regex:          a.searchRegex,
		WholeWord:      a.searchWholeWord,
		Error:          a.searchError,
		TotalAll:       a.searchTotal,
		Capped:         a.searchCapped,
		Skipped:        a.replaceSkipped.String(),
		CanUndoReplace: a.replaceJournalOK && len(a.replaceJournal) > 0,
		UndoNote:       a.replaceJournalMsg,
	}
	if !s.Open {
		return s
	}

	if len(a.searchHits) > 0 {
		m := compileMatcher(a.searchQueryForScan(), a.workspaceSearchOpts())
		counts := map[string]int{}
		for _, hit := range a.searchHits {
			counts[hit.Path]++
		}

		seen := map[string]bool{}
		start, end := a.searchScroll.Rows(len(a.searchHits), searchWindowRows)
		s.ScrollTop, s.ScrollHeight, s.Scroll = a.searchScroll.Thumb(
			searchWindowRows*a.zx(searchRowHeight), len(a.searchHits), searchWindowRows, a.zx(paneThumbMin),
		)

		for i := start; i < end; i++ {
			hit := a.searchHits[i]
			row := searchRow{
				Index:    i,
				Path:     hit.Path,
				Name:     baseName(hit.Path),
				Dir:      dirName(hit.Path),
				Line:     hit.Line,
				Col:      hit.Col,
				Before:   hit.Before,
				Match:    hit.Match,
				After:    hit.After,
				Selected: i == a.searchIndex,
				Checked:  a.fileChecked(hit.Path),
			}
			if !seen[hit.Path] {
				seen[hit.Path] = true
				row.GroupFirst = true
				row.GroupCount = counts[hit.Path]
			}
			if m.valid && m.Err == "" && !m.empty && row.Checked {
				row.Replacement = m.expandReplacement(hit.Match, a.searchReplace)
			}
			s.Rows = append(s.Rows, row)

			if row.Checked {
				s.ReplaceTotal++
			}
		}

		files := map[string]bool{}
		for _, hit := range a.searchHits {
			if a.fileChecked(hit.Path) {
				files[hit.Path] = true
			}
		}
		s.ReplaceFiles = len(files)
	}

	s.CanReplace = s.Query != "" && s.Error == "" && !s.Busy && (s.TotalAll > 0 || len(a.searchHits) > 0)

	return s
}

func countSearchFiles(hits []workspaceHit) int {
	seen := map[string]struct{}{}

	for _, hit := range hits {
		seen[hit.Path] = struct{}{}
	}

	return len(seen)
}

// openWorkspaceSearch shows the workspace content search panel.
func (a *App) openWorkspaceSearch(ctx context.Context) error {
	if !a.searchOpen {
		a.searchReturn = a.page.Form().FocusID()
		a.searchOpen = true
	}

	a.refresh()

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	if err := a.focusAfterLayout(ctx, "workspace-search"); err != nil {
		return err
	}

	if a.searchQuery != "" {
		return a.page.Edit().SelectAll(ctx)
	}
	return nil
}

// openWorkspaceReplace shows the workspace panel with the replace field
// focused, for the Ctrl+Shift+H flow.
func (a *App) openWorkspaceReplace(ctx context.Context) error {
	if !a.searchOpen {
		a.searchReturn = a.page.Form().FocusID()
		a.searchOpen = true
	}

	a.refresh()

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	return a.page.Form().Focus(ctx, "workspace-replace")
}

// closeWorkspaceSearch hides the panel and clears the query. The match
// toggles stay sticky across open and close and persist in the session;
// queries, hits, checks, and errors are cleared.
func (a *App) closeWorkspaceSearch(ctx context.Context) error {
	previous := a.searchReturn
	a.cancelSearch()
	a.searchOpen = false
	a.searchQuery = ""
	a.searchReplace = ""
	a.searchHits = nil
	a.searchIndex = 0
	a.searchScroll = historyScroll{}
	a.searchDrag = false
	a.searchNote = ""
	a.searchTruncated = false
	a.searchBusy = false
	a.searchTotal = 0
	a.searchCapped = false
	a.searchError = ""
	a.replaceCheck = nil
	a.searchReturn = ""
	a.refresh()

	if err := a.page.Redraw(ctx); err != nil {
		return err
	}

	if previous != "" && a.page.Form().FocusID() == "" {
		return a.page.Form().Focus(ctx, previous)
	}

	return nil
}

// runWorkspaceSearch starts one search. Results come back on the page loop
// through receiveSearch so the workspace pointer is never read concurrently
// by the renderer and the scanner. Literal queries trim surrounding
// whitespace; regex queries scan exactly what was typed. An invalid regex
// surfaces inline and never starts a scan.
func (a *App) runWorkspaceSearch(ctx context.Context) {
	a.cancelSearch()
	a.drainSearchResults()

	query := a.searchQueryForScan()
	if query == "" {
		a.searchHits = nil
		a.searchIndex = 0
		a.searchScroll = historyScroll{}
		a.searchNote = ""
		a.searchTruncated = false
		a.searchBusy = false
		a.searchTotal = 0
		a.searchCapped = false
		a.searchError = ""
		a.refresh()

		return
	}

	if m := compileMatcher(query, a.workspaceSearchOpts()); !m.valid || m.Err != "" {
		a.searchHits = nil
		a.searchIndex = 0
		a.searchScroll = historyScroll{}
		a.searchNote = ""
		a.searchTruncated = false
		a.searchBusy = false
		a.searchTotal = 0
		a.searchCapped = false
		a.searchError = m.Err
		a.refresh()

		return
	}

	a.searchError = ""
	a.searchToken++
	token := a.searchToken
	a.searchBusy = true
	a.searchNote = "Searching…"
	opts := a.workspaceSearchOpts()

	ctx, cancel := context.WithCancel(ctx)
	a.searchCancel = cancel
	ws := a.ws
	result := a.searchResult
	filter := a.scanFilter

	go func() {
		var found workspaceSearchResult
		// Lazy workspaces stream from disk (ripgrep) so unexpanded folders
		// are covered; ws.Files is seen-so-far, not the search corpus.
		if ws != nil && ws.Lazy && !ws.Sample && ws.Root != "" && !opts.Regex && !opts.WholeWord {
			if rgResult, ok := searchRipgrep(ctx, ws.Root, query, opts.Case, workspaceSearchLimit); ok {
				found = rgResult
			} else {
				// No rg: loaded-files filtered pass with an honest scope note.
				found = searchWorkspaceOptsFiltered(ctx, ws, query, opts, workspaceSearchLimit, workspaceSearchFileHits, filter)
				if found.Note != "" {
					found.Note += " "
				}
				found.Note += "Covered loaded files only; expand folders or install ripgrep for full-repo."
			}
		} else {
			found = searchWorkspaceOptsFiltered(ctx, ws, query, opts, workspaceSearchLimit, workspaceSearchFileHits, filter)
		}

		select {
		case result <- searchReply{Token: token, Result: found}:
		default:
		}

		a.page.WantFrameIn(0)
	}()

	a.refresh()
}

// workspaceReplaceNote labels the preview caps as preview-only: the stored
// rows stop at the preview limits while Replace All runs its own uncapped
// rescan.
func workspaceReplaceNote(result workspaceSearchResult) string {
	if result.Capped {
		return fmt.Sprintf("Showing the first %d of %d+ matches — Replace All scans everything.", len(result.Hits), workspaceReplaceCeil)
	}

	if result.Total > len(result.Hits) {
		return fmt.Sprintf("Showing the first %d of %d matches — Replace All scans everything.", len(result.Hits), result.Total)
	}

	return result.Note
}

// receiveSearch drains one finished search on the page loop.
func (a *App) receiveSearch(ctx context.Context) bool {
	for {
		select {
		case reply := <-a.searchResult:
			if reply.Token != a.searchToken {
				continue
			}

			a.searchHits = reply.Result.Hits
			a.searchNote = workspaceReplaceNote(reply.Result)
			a.searchTruncated = reply.Result.Truncated
			a.searchBusy = false
			a.searchTotal = reply.Result.Total
			a.searchCapped = reply.Result.Capped
			a.searchIndex = 0
			a.searchScroll = historyScroll{}
			a.refresh()

			if err := a.page.Redraw(ctx); err != nil {
				return false
			}

			return true
		default:
			return false
		}
	}
}

// drainSearchResults drops a result the next query replaced.
func (a *App) drainSearchResults() {
	for {
		select {
		case <-a.searchResult:
		default:
			return
		}
	}
}

// cancelSearch stops the running scan.
func (a *App) cancelSearch() {
	if a.searchCancel != nil {
		a.searchCancel()
		a.searchCancel = nil
	}
}

// toggleSearchCase flips the case switch and reruns the query.
func (a *App) toggleSearchCase(ctx context.Context) {
	a.searchCase = !a.searchCase
	a.searchIndex = 0
	a.runWorkspaceSearch(ctx)
}

// toggleSearchRegex flips the regex switch and reruns the query.
func (a *App) toggleSearchRegex(ctx context.Context) {
	a.searchRegex = !a.searchRegex
	a.searchIndex = 0
	a.runWorkspaceSearch(ctx)
}

// toggleSearchWholeWord flips the whole-word switch and reruns the query.
func (a *App) toggleSearchWholeWord(ctx context.Context) {
	a.searchWholeWord = !a.searchWholeWord
	a.searchIndex = 0
	a.runWorkspaceSearch(ctx)
}

// searchStep moves the selected result.
func (a *App) searchStep(delta int) {
	if len(a.searchHits) == 0 {
		return
	}

	a.searchIndex = (a.searchIndex + delta + len(a.searchHits)) % len(a.searchHits)
	if a.searchIndex < a.searchScroll.Offset {
		a.searchScroll.SetOffset(a.searchIndex, len(a.searchHits), searchWindowRows)
	} else if a.searchIndex >= a.searchScroll.Offset+searchWindowRows {
		a.searchScroll.SetOffset(a.searchIndex-searchWindowRows+1, len(a.searchHits), searchWindowRows)
	}
	a.refresh()
}

func (a *App) scrollSearchTo(offset int) {
	a.searchScroll.SetOffset(offset, len(a.searchHits), searchWindowRows)
	start, end := a.searchScroll.Rows(len(a.searchHits), searchWindowRows)
	if end > start {
		a.searchIndex = max(start, min(a.searchIndex, end-1))
	}
	a.refresh()
}

// openSearchHit opens one result at its source line as a preview tab.
func (a *App) openSearchHit(index int) {
	if index < 0 || index >= len(a.searchHits) {
		return
	}

	hit := a.searchHits[index]
	a.searchIndex = index
	a.cancelSearch()
	a.searchOpen = false
	a.searchQuery = ""
	a.searchReplace = ""
	a.searchHits = nil
	a.searchError = ""
	a.searchTotal = 0
	a.searchCapped = false
	a.replaceCheck = nil
	a.openFilePreview(hit.Path)
	a.scrollToSourceLine(hit.Line - 1)
}
