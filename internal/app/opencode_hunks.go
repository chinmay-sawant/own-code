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
// baseline file it was made on. If the file already changed, no hunk review
// is offered and whole-proposal accept (which re-checks the hash) still rules.
func (a *App) recordAgentReview(sid string, p agenttools.Proposal) {
	if a.ws == nil || a.ws.Sample {
		return
	}
	root, err := canonicalWorkspaceRoot(a.ws.Root)
	if err != nil {
		return
	}
	abs, rel, err := agentreview.MapPath(root, p.Path)
	if err != nil {
		return
	}
	base := ""
	if p.BaselineExists {
		data, err := os.ReadFile(abs)
		sum := sha256.Sum256(data)
		if err != nil || hex.EncodeToString(sum[:]) != p.BaselineHash {
			return
		}
		if agentreview.Classify(true, true, data, []byte(p.NewContent), "") != agentreview.Modify {
			return
		}
		base = string(data)
	} else if agentreview.Classify(false, true, nil, []byte(p.NewContent), "") != agentreview.Create {
		return
	}
	a.agentReviewRegistry().Put(sid, rel, base, p.NewContent)
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
		os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	_ = os.Chmod(tmp.Name(), mode)
	if err := os.Rename(tmp.Name(), abs); err != nil {
		os.Remove(tmp.Name())
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
	if op == agentHunkAccept {
		return status("Applied a hunk to " + path + ".")
	}
	return status("Reverted a hunk in " + path + ".")
}
