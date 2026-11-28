package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agenttools"
)

const openCodeProposalDirectory = "agent-workbench/proposals"

// openCodeAgentProposal is one write_file/edit_file/delete_file/move_file proposal awaiting a native
// Accept or Reject. It lives in memory only; the staged copy sits in a private
// per-session proposal directory that is never the worktree or the checkout.
type openCodeAgentProposal struct {
	ID       string
	Proposal agenttools.Proposal
	Note     string
}

type openCodePanelAgentProposalRow struct {
	ID     string
	Path   string
	Change string
	Diff   string
	Note   string
}

func openCodeAgentProposalID(p agenttools.Proposal) string {
	sum := sha256.Sum256([]byte(p.Path + "\x00" + p.NewHash))
	return "ap-" + hex.EncodeToString(sum[:6])
}

// openCodeProposalDir returns (creating) the private proposal directory for a
// session, outside every worktree and checkout.
func openCodeProposalDir(configDir, sessionID string) (string, error) {
	if strings.TrimSpace(configDir) == "" || sessionID == "" || sessionID != filepath.Base(sessionID) || sessionID == "." || sessionID == ".." {
		return "", errors.New("OpenCode proposal directory is unavailable")
	}
	base, err := filepath.Abs(filepath.Join(configDir, filepath.FromSlash(openCodeProposalDirectory)))
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, sessionID)
	if err := ensurePrivateDirectory(dir); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(dir)
}

// addAgentProposal records a proposal on the UI thread. A newer proposal for
// the same path replaces the older one (it was built on the staged copy).
func (a *App) addAgentProposal(sessionID string, p agenttools.Proposal) {
	if a.openCode.agentProposals == nil {
		a.openCode.agentProposals = map[string][]*openCodeAgentProposal{}
	}
	note := a.recordAgentReview(sessionID, p)
	list := a.openCode.agentProposals[sessionID]
	entry := &openCodeAgentProposal{ID: openCodeAgentProposalID(p), Proposal: p, Note: note}
	for i, e := range list {
		if e.Proposal.Path == p.Path {
			list[i] = entry
			return
		}
	}
	a.openCode.agentProposals[sessionID] = append(list, entry)
}

func (a *App) agentProposalRows(sessionID string) []openCodePanelAgentProposalRow {
	var rows []openCodePanelAgentProposalRow
	for _, e := range a.openCode.agentProposals[sessionID] {
		change := "Edit"
		switch {
		case e.Proposal.Deleted:
			change = "Delete"
		case e.Proposal.RenamedFrom != "":
			change = "Rename"
		case !e.Proposal.BaselineExists:
			change = "Create"
		}
		rows = append(rows, openCodePanelAgentProposalRow{ID: e.ID, Path: e.Proposal.Path, Change: change, Diff: e.Proposal.Diff, Note: e.Note})
	}
	return rows
}

func (a *App) findAgentProposal(id string) (string, int, *openCodeAgentProposal) {
	sid := a.openCode.selectedSessionID
	for i, e := range a.openCode.agentProposals[sid] {
		if e.ID == id {
			return sid, i, e
		}
	}
	return sid, -1, nil
}

func (a *App) dropAgentProposal(sid string, i int) {
	list := a.openCode.agentProposals[sid]
	e := list[i]
	a.openCode.agentProposals[sid] = append(list[:i:i], list[i+1:]...)
	a.discardStagedProposal(sid, e.Proposal.Path)
	a.forgetAgentReview(sid, e.Proposal.Path)
}

// discardStagedProposal removes the staged copy so later edits start from the
// real baseline again.
func (a *App) discardStagedProposal(sid, rel string) {
	if a.settingsPath == "" {
		return
	}
	dir, err := openCodeProposalDir(filepath.Dir(a.settingsPath), sid)
	if err != nil {
		return
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return
	}
	if st, err := os.Lstat(filepath.Join(dir, clean)); err == nil && st.Mode().IsRegular() {
		_ = os.Remove(filepath.Join(dir, clean))
	}
}

func (a *App) rejectOpenCodeProposal(id string) error {
	sid, i, e := a.findAgentProposal(id)
	if e == nil {
		a.openCode.panelStatus = "That proposal is no longer pending."
		a.refresh()
		return nil
	}
	a.dropAgentProposal(sid, i)
	a.openCode.panelStatus = "Rejected " + e.Proposal.Path + ". Your checkout was not changed."
	a.refresh()
	return nil
}

// dirtyBufferFor reports an open editor buffer with unsaved edits for abs.
func (a *App) dirtyBufferFor(root, rel string) bool {
	want := filepath.Join(root, rel)
	for key, d := range a.dirty {
		if !d {
			continue
		}
		k := filepath.Clean(filepath.FromSlash(key))
		if !filepath.IsAbs(k) {
			if a.ws != nil {
				if k == rel || filepath.Join(a.ws.Root, k) == want {
					return true
				}
			}
			k = filepath.Join(root, k)
		}
		if k == want {
			return true
		}
		if real, err := filepath.EvalSymlinks(k); err == nil && real == want {
			return true
		}
	}
	return false
}

// userChangesAt reports staged, unstaged, or untracked user state for rel.
func userChangesAt(ctx context.Context, root, rel string) (bool, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", "-C", root, "status", "--porcelain", "--untracked-files=all", "--", rel)
	hideConsoleWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// acceptOpenCodeProposal applies one proposal to the user's checkout. It
// refuses (leaving the proposal pending) rather than overwrite anything.
func (a *App) acceptOpenCodeProposal(id string) error {
	sid, i, e := a.findAgentProposal(id)
	fail := func(msg string) error {
		if e != nil {
			e.Note = msg
		}
		a.openCode.panelStatus = msg
		a.refresh()
		return nil
	}
	if e == nil {
		return fail("That proposal is no longer pending.")
	}
	if a.ws == nil || a.ws.Sample {
		return fail("Open a project folder before accepting a proposal.")
	}
	if a.openCodeRunFor(sid) != nil {
		return fail("Wait for the agent turn to finish before accepting.")
	}
	root, err := canonicalWorkspaceRoot(a.ws.Root)
	if err != nil {
		return fail("Refused: could not resolve the workspace.")
	}
	// Every path the apply touches gets the same dirty-buffer and Git-state
	// refusals: the target, plus the source of a rename.
	paths := []string{e.Proposal.Path}
	if e.Proposal.RenamedFrom != "" {
		paths = append(paths, e.Proposal.RenamedFrom)
	}
	ctx, cancel := context.WithTimeout(a.openCode.lifeCtx, 15*time.Second)
	defer cancel()
	for _, sp := range paths {
		rel := filepath.FromSlash(sp)
		if a.dirtyBufferFor(root, rel) {
			return fail("Refused: " + sp + " has unsaved edits in an open editor. Save or discard them, then accept again.")
		}
		changed, gerr := userChangesAt(ctx, root, rel)
		if gerr != nil {
			return fail("Refused: could not check Git state for " + sp + ".")
		}
		if changed {
			return fail("Refused: " + sp + " has staged, unstaged, or untracked changes that would be overwritten. They were preserved.")
		}
	}
	if err := agenttools.ApplyProposal(e.Proposal, root); err != nil {
		switch {
		case errors.Is(err, agenttools.ErrBaselineChanged):
			return fail("Refused: " + e.Proposal.Path + " changed since the proposal was made. Your file was not modified.")
		case errors.Is(err, agenttools.ErrDestExists):
			return fail("Refused: " + e.Proposal.Path + " already exists. Nothing was moved.")
		case errors.Is(err, agenttools.ErrOutside):
			return fail("Refused: " + e.Proposal.Path + " is a symlink or leaves the workspace.")
		}
		return fail("Refused: the proposal could not be applied safely.")
	}
	a.dropAgentProposal(sid, i)
	switch {
	case e.Proposal.Deleted:
		a.forgetExplorerDocs([]string{e.Proposal.Path}, nil) // closes any open tab/buffer (never dirty here)
		a.openCode.panelStatus = "Deleted " + e.Proposal.Path + " from your checkout."
		a.refresh()
		return nil
	case e.Proposal.RenamedFrom != "":
		a.forgetExplorerDocs(nil, map[string]string{e.Proposal.RenamedFrom: e.Proposal.Path})
		a.openCode.panelStatus = "Moved " + e.Proposal.RenamedFrom + " to " + e.Proposal.Path + "."
		a.refresh()
		return nil
	}
	if doc := a.docs[e.Proposal.Path]; doc != nil {
		a.docs[e.Proposal.Path] = a.ws.openDoc(e.Proposal.Path)
	}
	a.openCode.panelStatus = "Applied " + e.Proposal.Path + " to your checkout."
	a.refresh()
	return nil
}
