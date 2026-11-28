package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/agentreview"
	"github.com/chinmay-sawant/own-code/internal/agenttools"
)

func (a *App) agentReviewRegistry() *agentreview.Registry {
	if a.openCode.agentReview == nil {
		a.openCode.agentReview = agentreview.NewRegistry()
	}
	return a.openCode.agentReview
}

// recordAgentReview builds the per-hunk model for a proposal against the
// baseline file it was made on and returns a user-facing note: why hunk review
// is unavailable (binary, oversized, delete, rename, changed baseline), or an
// attribution warning when the user already had changes at that path before
// the turn. Whole-proposal accept (which re-checks the hash) still rules.
func (a *App) recordAgentReview(sid string, p agenttools.Proposal) string {
	if a.ws == nil || a.ws.Sample {
		return ""
	}
	root, err := canonicalWorkspaceRoot(a.ws.Root)
	if err != nil {
		return ""
	}
	abs, rel, err := agentreview.MapPath(root, p.Path)
	if err != nil {
		return "Hunk review unavailable: path is outside the workspace."
	}
	if p.Deleted || p.RenamedFrom != "" {
		return a.recordWholeFileReview(sid, root, rel, p)
	}
	var data []byte
	if p.BaselineExists {
		data, err = os.ReadFile(abs)
		sum := sha256.Sum256(data)
		if err != nil || hex.EncodeToString(sum[:]) != p.BaselineHash {
			return "Hunk review unavailable: the file changed since the proposal was made."
		}
	}
	kind := agentreview.Classify(p.BaselineExists, true, data, []byte(p.NewContent), "")
	want := agentreview.Create
	if p.BaselineExists {
		want = agentreview.Modify
	}
	if kind != want {
		a.agentReviewRegistry().Forget(sid, rel)
		return unsupportedChangeNote(kind)
	}
	a.agentReviewRegistry().Put(sid, rel, string(data), p.NewContent)
	return a.userOwnedNote(sid, rel)
}

// recordWholeFileReview registers a delete or rename as one reviewable unit
// (accept or reject the whole proposal; there are no hunks).
func (a *App) recordWholeFileReview(sid, root, rel string, p agenttools.Proposal) string {
	kind, src := agentreview.Delete, rel
	if p.RenamedFrom != "" {
		kind = agentreview.Rename
		_, srel, err := agentreview.MapPath(root, p.RenamedFrom)
		if err != nil {
			return "Review unavailable: the rename source is outside the workspace."
		}
		src = srel
	}
	data, err := os.ReadFile(filepath.Join(root, src))
	sum := sha256.Sum256(data)
	if err != nil || hex.EncodeToString(sum[:]) != p.BaselineHash {
		a.agentReviewRegistry().Forget(sid, rel)
		return "Review unavailable: the file changed since the proposal was made."
	}
	if _, err := a.agentReviewRegistry().PutWhole(sid, rel, kind, filepath.ToSlash(src)); err != nil {
		return "Review unavailable for this change."
	}
	if kind == agentreview.Delete {
		return "Whole-file delete: accept or reject it as one unit (no per-hunk review)."
	}
	return "Whole-file rename from " + p.RenamedFrom + ": accept or reject it as one unit (no per-hunk review)."
}

// unsupportedChangeNote explains why a change kind has no hunk review. Delete
// and rename are reviewed as one whole-file unit instead.
func unsupportedChangeNote(k agentreview.Kind) string {
	switch k {
	case agentreview.Binary:
		return "Hunk review unavailable: binary or non-UTF-8 content. Accept or reject the whole proposal."
	case agentreview.Oversized:
		return "Hunk review unavailable: the file is over the review size limit. Accept or reject the whole proposal."
	case agentreview.Delete:
		return "Deletes are reviewed as one whole-file unit, not per hunk."
	case agentreview.Rename:
		return "Renames are reviewed as one whole-file unit, not per hunk."
	}
	return "Hunk review unavailable for this change."
}

// userOwnedNote tells the user when they already had changes at rel before the
// session's latest turn, so agent and user lines stay distinguishable.
func (a *App) userOwnedNote(sid, rel string) string {
	b := a.openCode.agentBaseline[sid]
	if b == nil || !b.UserOwned(rel) {
		return ""
	}
	if b.StagedByUser(rel) {
		return "You had staged changes here before this turn. Hunk actions apply only agent lines and never touch the index."
	}
	return "You had unsaved or uncommitted changes here before this turn. Hunk actions apply only agent lines."
}

// dirtyRelPaths lists open dirty editor buffers as workspace-relative slash paths.
func (a *App) dirtyRelPaths(root string) []string {
	var out []string
	for key, d := range a.dirty {
		if !d {
			continue
		}
		k := filepath.Clean(filepath.FromSlash(key))
		if filepath.IsAbs(k) {
			r, err := filepath.Rel(root, k)
			if err != nil || strings.HasPrefix(r, "..") {
				continue
			}
			k = r
		}
		out = append(out, filepath.ToSlash(k))
	}
	return out
}

func (a *App) forgetAgentReview(sid, rel string) {
	a.agentReviewRegistry().Forget(sid, rel)
}

// agentReviewText returns the user's current text for rel: the open editor
// buffer when one exists (so unsaved edits are respected), else the file.
func (a *App) agentReviewText(abs, rel string) (string, bool, error) {
	if a.edit != nil && a.editing && (a.edit.Path == rel || filepath.Clean(a.edit.Path) == filepath.Clean(filepath.FromSlash(rel))) {
		return a.edit.Text(), true, nil
	}
	data, err := os.ReadFile(abs)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	return string(data), false, err
}

// commitAgentHunkText writes next either into the open editor buffer through
// its undo and dirty model, or to disk. It never stages anything in Git.
func (a *App) commitAgentHunkText(root, abs, rel, cur, next string, inBuffer bool) error {
	if inBuffer {
		start, end, insert := minimalRuneSplice(cur, next)
		return a.replaceEditRanges(a.openCode.lifeCtx, []bufferRep{{Start: start, End: end, Insert: insert}})
	}
	if a.dirtyBufferFor(root, filepath.FromSlash(rel)) {
		return errors.New("file has unsaved edits in a background tab")
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(abs); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".agent-hunk-*")
	if err != nil {
		return err
	}
	_, werr := tmp.WriteString(next)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	_ = os.Chmod(tmp.Name(), mode)
	if err := os.Rename(tmp.Name(), abs); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if a.ws != nil {
		if _, ok := a.docs[rel]; ok {
			a.docs[rel] = a.ws.openDoc(rel)
		}
	}
	return nil
}

// minimalRuneSplice returns the rune range of cur to replace with insert to
// produce next, trimming the shared prefix and suffix so the caret and undo
// entry stay local to the hunk.
func minimalRuneSplice(cur, next string) (int, int, string) {
	c, n := []rune(cur), []rune(next)
	p := 0
	for p < len(c) && p < len(n) && c[p] == n[p] {
		p++
	}
	s := 0
	for s < len(c)-p && s < len(n)-p && c[len(c)-1-s] == n[len(n)-1-s] {
		s++
	}
	return p, len(c) - s, string(n[p : len(n)-s])
}

type agentHunkOp int

const (
	agentHunkAccept agentHunkOp = iota
	agentHunkReject
	agentHunkRevert
)

// reviewOpenCodeHunk accepts, rejects, or reverts one hunk of the selected
// session's proposal for path. Accept and revert touch only the hunk's lines.
func (a *App) reviewOpenCodeHunk(op agentHunkOp, path, hunkID string) error {
	sid := a.openCode.selectedSessionID
	status := func(msg string) error {
		a.openCode.panelStatus = msg
		a.setAgentHunkNote(sid, path, hunkID, msg)
		if a.diffSource == "agent" {
			a.rebuildDiffRows()
		}
		a.refresh()
		return nil
	}
	reg := a.agentReviewRegistry()
	if reg.Get(sid, path) == nil {
		return status("That hunk is no longer available for review.")
	}
	if op == agentHunkReject {
		if err := reg.Reject(sid, path, hunkID); err != nil {
			return status("Could not reject: " + err.Error())
		}
		if err := a.persistAgentProposalState(sid); err != nil {
			return status("Rejected a hunk in " + path + ", but its review state could not be saved for restart.")
		}
		return status("Rejected a hunk in " + path + ". Your files were not changed.")
	}
	if a.ws == nil || a.ws.Sample {
		return status("Open a project folder before reviewing hunks.")
	}
	root, err := canonicalWorkspaceRoot(a.ws.Root)
	if err != nil {
		return status("Refused: could not resolve the workspace.")
	}
	abs, rel, err := agentreview.MapPath(root, path)
	if err != nil {
		return status("Refused: " + path + " is outside the workspace.")
	}
	cur, inBuffer, err := a.agentReviewText(abs, rel)
	if err != nil {
		return status("Refused: could not read " + path + ".")
	}
	var next string
	if op == agentHunkAccept {
		next, err = reg.Accept(sid, path, hunkID, cur)
	} else {
		next, err = reg.Revert(sid, path, hunkID, cur)
	}
	if err != nil {
		if errors.Is(err, agentreview.ErrConflict) || errors.Is(err, agentreview.ErrAmbiguous) {
			return status("Refused: your changes overlap this hunk in " + path + ". Nothing was changed.")
		}
		return status("Could not update hunk: " + err.Error())
	}
	if err := a.commitAgentHunkText(root, abs, rel, cur, next, inBuffer); err != nil {
		// Roll the state back so the hunk can be retried.
		if op == agentHunkAccept {
			_, _ = reg.Revert(sid, path, hunkID, next)
		} else {
			_, _ = reg.Accept(sid, path, hunkID, next)
		}
		return status("Refused: " + strings.TrimSpace(err.Error()))
	}
	if err := a.persistAgentProposalState(sid); err != nil {
		if op == agentHunkAccept {
			return status("Applied a hunk to " + path + ", but its review state could not be saved for restart.")
		}
		return status("Reverted a hunk in " + path + ", but its review state could not be saved for restart.")
	}
	if op == agentHunkAccept {
		return status("Applied a hunk to " + path + ".")
	}
	return status("Reverted a hunk in " + path + ".")
}
