package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/agentreview"
	"github.com/chinmay-sawant/own-code/internal/agenttools"
)

const (
	openCodeProposalStateDirectory = "agent-workbench/proposal-state"
	openCodeProposalStateVersion   = 2
	openCodeProposalStateMaxBytes  = 16 << 20
	openCodeProposalStateMaxItems  = 128
	openCodeProposalFileMaxBytes   = 1 << 20
)

type openCodePersistedProposals struct {
	Version   int                         `json:"version"`
	Proposals []openCodePersistedProposal `json:"proposals"`
}

type openCodePersistedProposal struct {
	Proposal   agenttools.Proposal `json:"proposal"`
	DiffHash   string              `json:"diff_hash"`
	Note       string              `json:"note,omitempty"`
	HasReview  bool                `json:"has_review,omitempty"`
	ReviewBase string              `json:"review_base,omitempty"`
	HunkStates map[string]string   `json:"hunk_states,omitempty"`
}

// openCodeProposalRestore contains all file-backed validation and review
// reconstruction prepared by the session-load worker. Applying it only
// replaces in-memory state on the UI loop.
type openCodeProposalRestore struct {
	sessionID string
	proposals []*openCodeAgentProposal
	reviews   []*agentreview.FileReview
	loaded    int
	skipped   int
	err       error
}

func openCodeProposalStatePath(configDir, sessionID string) (string, error) {
	if strings.TrimSpace(configDir) == "" || sessionID == "" || sessionID != filepath.Base(sessionID) || sessionID == "." || sessionID == ".." {
		return "", errors.New("OpenCode proposal state is unavailable")
	}
	base, err := filepath.Abs(filepath.Join(configDir, filepath.FromSlash(openCodeProposalStateDirectory)))
	if err != nil {
		return "", errors.New("could not resolve OpenCode proposal state directory")
	}
	dir := filepath.Join(base, sessionID)
	if err := ensurePrivateDirectory(dir); err != nil {
		return "", errors.New("could not open private OpenCode proposal state directory")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", errors.New("could not resolve private OpenCode proposal state directory")
	}
	return filepath.Join(resolved, "pending.json"), nil
}

func proposalDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validProposalRelativePath(rel string) bool {
	if rel == "" || strings.ContainsRune(rel, '\\') || strings.ContainsRune(rel, 0) || strings.HasPrefix(rel, "/") {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	return !filepath.IsAbs(clean) && clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator)) && filepath.ToSlash(clean) == rel
}

func proposalFilePath(root, rel string) (string, error) {
	if !validProposalRelativePath(rel) {
		return "", errors.New("invalid proposal path")
	}
	target := filepath.Join(root, filepath.FromSlash(rel))
	if !pathWithin(root, target) {
		return "", errors.New("proposal path leaves its private directory")
	}
	return target, nil
}

func pathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func writePrivateProposalFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".proposal-state-*")
	if err != nil {
		return errors.New("could not create private OpenCode proposal state")
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return errors.New("could not restrict OpenCode proposal state permissions")
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errors.New("could not write OpenCode proposal state")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return errors.New("could not sync OpenCode proposal state")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("could not close OpenCode proposal state")
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return errors.New("could not replace OpenCode proposal state")
	}
	return nil
}

func (a *App) verifyStagedOpenCodeProposal(sid string, p agenttools.Proposal) error {
	if a.settingsPath == "" {
		return errors.New("OpenCode settings path is unavailable")
	}
	if p.Deleted {
		return nil
	}
	root, err := openCodeProposalDir(filepath.Dir(a.settingsPath), sid)
	if err != nil {
		return err
	}
	if p.NewHash != proposalDigest([]byte(p.NewContent)) || len(p.NewContent) > openCodeProposalFileMaxBytes {
		return errors.New("proposal content did not match its recorded hash")
	}
	staged, err := readStagedProposal(root, p.Path, p.NewHash)
	if err != nil || staged != p.NewContent {
		return errors.New("staged proposal content is missing or changed")
	}
	return nil
}

func (a *App) persistAgentProposalState(sid string) error {
	if a.settingsPath == "" {
		return errors.New("OpenCode settings path is unavailable")
	}
	path, err := openCodeProposalStatePath(filepath.Dir(a.settingsPath), sid)
	if err != nil {
		return err
	}
	entries := a.openCode.agentProposals[sid]
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("could not clear OpenCode proposal state")
		}
		return nil
	}
	if len(entries) > openCodeProposalStateMaxItems {
		return errors.New("too many pending OpenCode proposals to persist")
	}
	state := openCodePersistedProposals{Version: openCodeProposalStateVersion, Proposals: make([]openCodePersistedProposal, 0, len(entries))}
	registry := a.agentReviewRegistry()
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		p := entry.Proposal
		p.NewContent = "" // Proposed bytes are stored in the private proposal directory.
		persisted := openCodePersistedProposal{Proposal: p, DiffHash: proposalDigest([]byte(p.Diff)), Note: entry.Note}
		if review := registry.Get(sid, entry.Proposal.Path); review != nil && !review.Kind.Whole() {
			persisted.HasReview = true
			persisted.ReviewBase = review.Base
			persisted.HunkStates = make(map[string]string, len(review.Hunks))
			for i, hunk := range review.Hunks {
				if i < len(review.States) {
					persisted.HunkStates[hunk.ID] = review.States[i].String()
				}
			}
		}
		state.Proposals = append(state.Proposals, persisted)
	}
	data, err := json.Marshal(state)
	if err != nil || len(data) > openCodeProposalStateMaxBytes {
		return errors.New("OpenCode proposal state exceeds its size limit")
	}
	return writePrivateProposalFile(path, data)
}

func readPrivateProposalState(path string) ([]byte, error) {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 || st.Size() > openCodeProposalStateMaxBytes {
		return nil, errors.New("OpenCode proposal state is not a bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("could not read OpenCode proposal state")
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, openCodeProposalStateMaxBytes+1))
	if err != nil || len(data) > openCodeProposalStateMaxBytes {
		return nil, errors.New("OpenCode proposal state exceeds its size limit")
	}
	return data, nil
}

func readStagedProposal(root, rel, wantHash string) (string, error) {
	path, err := proposalFilePath(root, rel)
	if err != nil {
		return "", err
	}
	cur := root
	relPath, _ := filepath.Rel(root, path)
	parts := strings.Split(relPath, string(filepath.Separator))
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if err != nil || st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("staged proposal is missing or contains a symlink")
		}
		if i < len(parts)-1 && !st.IsDir() {
			return "", errors.New("staged proposal parent is not a directory")
		}
		if i == len(parts)-1 && (!st.Mode().IsRegular() || st.Size() > openCodeProposalFileMaxBytes) {
			return "", errors.New("staged proposal is not a bounded regular file")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || proposalDigest(data) != wantHash {
		return "", errors.New("staged proposal content is missing or changed")
	}
	return string(data), nil
}

func decodeReviewState(state string) (agentreview.State, bool) {
	switch state {
	case agentreview.Pending.String():
		return agentreview.Pending, true
	case agentreview.Accepted.String():
		return agentreview.Accepted, true
	case agentreview.Rejected.String():
		return agentreview.Rejected, true
	default:
		return agentreview.Pending, false
	}
}

func loadOpenCodeProposalRestore(configDir, workspaceRoot, sid string) *openCodeProposalRestore {
	restored := &openCodeProposalRestore{sessionID: sid}
	if configDir == "" {
		return nil
	}
	path, err := openCodeProposalStatePath(configDir, sid)
	if err != nil {
		restored.err = err
		return restored
	}
	data, err := readPrivateProposalState(path)
	if err != nil {
		restored.err = err
		return restored
	}
	if len(data) == 0 {
		return restored
	}
	var saved openCodePersistedProposals
	if err := json.Unmarshal(data, &saved); err != nil || saved.Version != openCodeProposalStateVersion || len(saved.Proposals) > openCodeProposalStateMaxItems {
		restored.err = errors.New("OpenCode proposal state is invalid")
		return restored
	}
	proposalRoot, err := openCodeProposalDir(configDir, sid)
	if err != nil {
		restored.err = err
		return restored
	}
	seen := make(map[string]bool, len(saved.Proposals))
	for _, item := range saved.Proposals {
		p := item.Proposal
		if !validPersistedProposal(p) || item.DiffHash != proposalDigest([]byte(p.Diff)) || seen[p.Path] {
			restored.skipped++
			continue
		}
		seen[p.Path] = true
		if !p.Deleted {
			p.NewContent, err = readStagedProposal(proposalRoot, p.Path, p.NewHash)
			if err != nil {
				restored.skipped++
				continue
			}
		}
		entry := &openCodeAgentProposal{ID: openCodeAgentProposalID(p), Proposal: p, Note: item.Note}
		if item.HasReview && !p.Deleted && p.RenamedFrom == "" {
			if !validPersistedReview(item, p) {
				restored.skipped++
				continue
			}
			review := restoredHunkReview(sid, p.Path, item.ReviewBase, p.NewContent)
			if !restoreHunkStates(review, item.HunkStates) {
				// Keep the proposal visible, but reset undecodable review state to pending.
				restored.skipped++
			}
			restored.reviews = append(restored.reviews, review)
		} else {
			review, reviewNote := reconstructOpenCodeProposalReview(workspaceRoot, sid, p)
			if review != nil {
				restored.reviews = append(restored.reviews, review)
			}
			if item.Note == "" || reviewNote != "" {
				entry.Note = reviewNote
			}
		}
		restored.proposals = append(restored.proposals, entry)
		restored.loaded++
	}
	return restored
}

func restoredHunkReview(sid, path, base, proposed string) *agentreview.FileReview {
	hunks := agentreview.Diff(base, proposed)
	return &agentreview.FileReview{
		Session: sid, Path: path, Base: base, Proposed: proposed,
		Hunks: hunks, States: make([]agentreview.State, len(hunks)),
	}
}

func restoreHunkStates(review *agentreview.FileReview, persisted map[string]string) bool {
	if review == nil || len(persisted) != len(review.Hunks) {
		return false
	}
	states := make([]agentreview.State, len(review.Hunks))
	hunkIndexes := make(map[string]int, len(review.Hunks))
	for i, hunk := range review.Hunks {
		hunkIndexes[hunk.ID] = i
		decoded, ok := decodeReviewState(persisted[hunk.ID])
		if !ok {
			return false
		}
		states[i] = decoded
	}
	for id := range persisted {
		if _, found := hunkIndexes[id]; !found {
			return false
		}
	}
	copy(review.States, states)
	return true
}

func reconstructOpenCodeProposalReview(root, sid string, p agenttools.Proposal) (*agentreview.FileReview, string) {
	if root == "" {
		return nil, ""
	}
	_, rel, err := agentreview.MapPath(root, p.Path)
	if err != nil {
		return nil, "Hunk review unavailable: path is outside the workspace."
	}
	if p.Deleted || p.RenamedFrom != "" {
		kind, src := agentreview.Delete, rel
		if p.RenamedFrom != "" {
			kind = agentreview.Rename
			_, src, err = agentreview.MapPath(root, p.RenamedFrom)
			if err != nil {
				return nil, "Review unavailable: the rename source is outside the workspace."
			}
		}
		if err := verifyWorkspaceProposalBaseline(filepath.Join(root, filepath.FromSlash(src)), p.BaselineHash); err != nil {
			return nil, "Review unavailable: the file changed since the proposal was made."
		}
		return &agentreview.FileReview{Session: sid, Path: rel, Kind: kind, RenamedFrom: filepath.ToSlash(src)}, ""
	}
	var data []byte
	if p.BaselineExists {
		abs, _, mapErr := agentreview.MapPath(root, p.Path)
		if mapErr != nil {
			return nil, "Hunk review unavailable: path is outside the workspace."
		}
		data, err = readWorkspaceProposalBaseline(abs, p.BaselineHash)
		if err != nil {
			return nil, "Hunk review unavailable: the file changed since the proposal was made."
		}
	}
	kind := agentreview.Classify(p.BaselineExists, true, data, []byte(p.NewContent), "")
	want := agentreview.Create
	if p.BaselineExists {
		want = agentreview.Modify
	}
	if kind != want {
		return nil, unsupportedChangeNote(kind)
	}
	return restoredHunkReview(sid, rel, string(data), p.NewContent), ""
}

func verifyWorkspaceProposalBaseline(path, wantHash string) error {
	_, err := readWorkspaceProposalBaseline(path, wantHash)
	return err
}

func readWorkspaceProposalBaseline(path, wantHash string) ([]byte, error) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 || st.Size() > openCodeProposalFileMaxBytes {
		return nil, errors.New("proposal baseline is not a bounded regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil || proposalDigest(data) != wantHash {
		return nil, errors.New("proposal baseline changed")
	}
	return data, nil
}

func (a *App) applyOpenCodeProposalRestore(restored *openCodeProposalRestore) {
	if restored == nil || restored.sessionID == "" {
		return
	}
	if a.openCode.agentProposals == nil {
		a.openCode.agentProposals = make(map[string][]*openCodeAgentProposal)
	}
	a.openCode.agentProposals[restored.sessionID] = restored.proposals
	if err := a.agentReviewRegistry().RestoreSession(restored.sessionID, restored.reviews); err != nil {
		// The worker constructs this bounded state from validated records. If it
		// is rejected anyway, leave no stale review entries installed.
		a.agentReviewRegistry().Drop(restored.sessionID)
		restored.err = errors.New("OpenCode proposal review state is invalid")
	}
}

func validPersistedProposal(p agenttools.Proposal) bool {
	if !validProposalRelativePath(p.Path) || p.NewContent != "" || len(p.Diff) > openCodeProposalStateMaxBytes {
		return false
	}
	if len(p.NewHash) != sha256.Size*2 {
		return false
	}
	if _, err := hex.DecodeString(p.NewHash); err != nil || (p.BaselineExists != (p.BaselineHash != "")) {
		return false
	}
	if p.BaselineExists {
		if len(p.BaselineHash) != sha256.Size*2 {
			return false
		}
		if _, err := hex.DecodeString(p.BaselineHash); err != nil {
			return false
		}
	}
	switch p.Tool {
	case agenttools.ToolWriteFile, agenttools.ToolEditFile:
		return !p.Deleted && p.RenamedFrom == ""
	case agenttools.ToolDeleteFile:
		return p.Deleted && p.RenamedFrom == "" && p.BaselineExists && p.NewHash == proposalDigest(nil)
	case agenttools.ToolMoveFile:
		return !p.Deleted && p.RenamedFrom != "" && validProposalRelativePath(p.RenamedFrom) && p.RenamedFrom != p.Path && p.BaselineExists
	default:
		return false
	}
}

func validPersistedReview(item openCodePersistedProposal, p agenttools.Proposal) bool {
	if p.BaselineExists && proposalDigest([]byte(item.ReviewBase)) != p.BaselineHash {
		return false
	}
	if !p.BaselineExists && item.ReviewBase != "" {
		return false
	}
	kind := agentreview.Classify(p.BaselineExists, true, []byte(item.ReviewBase), []byte(p.NewContent), "")
	want := agentreview.Create
	if p.BaselineExists {
		want = agentreview.Modify
	}
	return kind == want
}
