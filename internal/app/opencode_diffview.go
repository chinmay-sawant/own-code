package app

import (
	"sort"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/agentreview"
)

// This file shows agent proposals in the same unified and split diff views the
// Source Control pane uses (diffSource "agent"), with per-hunk controls.

func agentHunkNoteKey(sid, path, hunk string) string { return sid + "\x00" + path + "\x00" + hunk }

// setAgentHunkNote keeps the last refusal for a hunk; a success clears it.
func (a *App) setAgentHunkNote(sid, path, hunk, msg string) {
	if a.openCode.agentHunkNotes == nil {
		a.openCode.agentHunkNotes = map[string]string{}
	}
	key := agentHunkNoteKey(sid, path, hunk)
	if strings.HasPrefix(msg, "Refused") || strings.HasPrefix(msg, "Could not") || strings.HasPrefix(msg, "That hunk") {
		a.openCode.agentHunkNotes[key] = msg
		return
	}
	delete(a.openCode.agentHunkNotes, key)
}

// agentChangeSet renders every reviewed file of a session as parsed diffs.
func (a *App) agentChangeSet(sid string) changeSet {
	reg := a.agentReviewRegistry()
	cs := changeSet{EmptyTitle: "No agent proposals", Note: "This session has no proposals to review."}
	for _, p := range reg.Paths(sid) {
		fr := reg.Get(sid, p)
		if fr == nil || len(fr.Hunks) == 0 {
			continue
		}
		files := parseDiff(fr.Unified(true))
		if len(files) == 0 {
			continue
		}
		f := files[0]
		f.Path = p
		countHunkLines(&f)
		cs.Files = append(cs.Files, f)
	}
	sort.Slice(cs.Files, func(i, j int) bool { return cs.Files[i].Path < cs.Files[j].Path })
	return cs
}

// openAgentDiff switches the Changes view to the selected session's proposals.
func (a *App) openAgentDiff(path string) {
	sid := a.openCode.selectedSessionID
	cs := a.agentChangeSet(sid)
	if len(cs.Files) == 0 {
		a.openCode.panelStatus = "No hunk-level review is available for this proposal. Deletes and renames are accepted or rejected as one unit."
		a.refresh()
		return
	}
	if a.mode == "merge" {
		a.leaveMergeMode()
	}
	a.mode = "changes"
	a.agentSession, a.agentChanges, a.agentPath = sid, cs, cs.Files[0].Path
	if findFile(cs.Files, path) != nil {
		a.agentPath = path
	}
	a.diffSource = "agent"
	a.resetScroll()
	a.rebuildDiffRows()
	a.refresh()
}

func (a *App) selectAgentFile(path string) {
	if findFile(a.agentChanges.Files, path) == nil {
		return
	}
	a.agentPath = path
	a.resetScroll()
	a.rebuildDiffRows()
	a.refresh()
}

func shortSession(sid string) string {
	if len(sid) > 8 {
		return sid[:8]
	}
	return sid
}

// rebuildAgentDiffRows renders the selected proposal file and tags each hunk
// header with its session, file, state, and the controls that apply.
func (a *App) rebuildAgentDiffRows() {
	a.agentChanges = a.agentChangeSet(a.agentSession)
	file := findFile(a.agentChanges.Files, a.agentPath)
	if file == nil && len(a.agentChanges.Files) > 0 {
		a.agentPath = a.agentChanges.Files[0].Path
		file = &a.agentChanges.Files[0]
	}
	if file == nil {
		a.diffSource = "working"
		a.rebuildDiffRows()
		return
	}
	lang := languageOf(file.Path)
	if a.diffSplit {
		a.diffRows = diffRowsSplit(*file, lang)
	} else {
		a.diffRows = diffRowsUnified(*file, lang)
	}
	fr := a.agentReviewRegistry().Get(a.agentSession, a.agentPath)
	if fr == nil {
		return
	}
	rows := a.diffRows[:0]
	n := 0
	for _, row := range a.diffRows {
		if row.Kind == rowExpand {
			continue
		}
		if row.Kind == rowHunk && n < len(fr.Hunks) {
			h := fr.Hunks[n]
			st := fr.States[n]
			row.AgentHunk, row.AgentFile, row.AgentState = h.ID, a.agentPath, st.String()
			row.AgentAccept = st == agentreview.Pending
			row.AgentReject = st == agentreview.Pending
			row.AgentRevert = st == agentreview.Accepted
			row.AgentNote = a.openCode.agentHunkNotes[agentHunkNoteKey(a.agentSession, a.agentPath, h.ID)]
			row.Hunk = "Agent " + shortSession(a.agentSession) + " · " + a.agentPath + " · " + row.Hunk + " · " + row.AgentState
			n++
		}
		rows = append(rows, row)
	}
	a.diffRows = rows
}

// agentHunkAction handles "agent-hunk:<accept|reject|revert>:<id>" from the diff.
func (a *App) agentHunkAction(rest string) error {
	op, id, ok := strings.Cut(rest, ":")
	if !ok || a.diffSource != "agent" {
		return nil
	}
	var kind agentHunkOp
	switch op {
	case "accept":
		kind = agentHunkAccept
	case "reject":
		kind = agentHunkReject
	case "revert":
		kind = agentHunkRevert
	default:
		return nil
	}
	// The diff belongs to agentSession; review that session, not whatever
	// the chat panel has selected since.
	prev := a.openCode.selectedSessionID
	a.openCode.selectedSessionID = a.agentSession
	defer func() { a.openCode.selectedSessionID = prev }()
	return a.reviewOpenCodeHunk(kind, a.agentPath, id)
}
