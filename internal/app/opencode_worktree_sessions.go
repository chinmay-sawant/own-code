package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

const (
	openCodeWorkspaceSessionLimit = 50
	openCodeKnownWorktreeLimit    = 20
)

type openCodeSessionWorktree struct {
	worktree  *openCodeWorktree
	sessionID map[string]struct{}
}

func (a *App) openCodeWorktreeManager() (*openCodeWorktreeManager, error) {
	if a.openCode.worktrees != nil {
		return a.openCode.worktrees, nil
	}
	if a.settingsPath == "" {
		path, err := defaultSettingsPath()
		if err != nil {
			return nil, err
		}
		a.settingsPath = path
	}
	manager, err := newOpenCodeWorktreeManager(filepath.Dir(a.settingsPath))
	if err != nil {
		return nil, err
	}
	a.openCode.worktrees = manager
	return manager, nil
}

func verifyOpenCodeSessionWorktree(ctx context.Context, manager *openCodeWorktreeManager, workspaceRoot string, session agentstore.Session) (bool, string) {
	if session.WorktreeRoot == "" {
		return false, ""
	}
	if session.BaselineCommit == "" {
		return false, "Transcript loaded, but this saved session has no verified proposal baseline."
	}
	if manager == nil {
		return false, "Transcript loaded, but the isolated worktree could not be verified for proposal review."
	}
	if _, err := manager.OpenExisting(ctx, workspaceRoot, session.WorktreeRoot, session.BaselineCommit); err != nil {
		return false, "Transcript loaded, but the isolated worktree could not be verified for proposal review."
	}
	return true, ""
}

func isOpenCodeLoopbackServerURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func openCodeReadOnlyPermissions() json.RawMessage {
	return json.RawMessage(`[{"action":"*","resource":"*","effect":"deny"}]`)
}

func (h *openCodeSessionHistory) PersistIsolatedSession(ctx context.Context, remote opencode.Session, worktree *openCodeWorktree, status agentstore.SessionStatus) (agentstore.Session, error) {
	if h == nil || h.store == nil || worktree == nil || worktree.manager == nil {
		return agentstore.Session{}, errors.New("OpenCode isolated session history is unavailable")
	}
	if err := contextError(ctx); err != nil {
		return agentstore.Session{}, err
	}
	if strings.TrimSpace(remote.ID) == "" || len(remote.ID) > 256 {
		return agentstore.Session{}, errors.New("OpenCode session ID is missing or too long")
	}
	if !validOpenCodeCommitID(worktree.baseCommit) {
		return agentstore.Session{}, errors.New("OpenCode isolated session has no valid baseline commit")
	}
	workspaceRoot, err := canonicalWorkspaceRoot(h.workspaceRoot)
	if err != nil || !sameWorkspacePath(workspaceRoot, h.workspaceRoot) {
		return agentstore.Session{}, errOpenCodeHistoryScope
	}
	worktreePath, err := filepath.Abs(worktree.Path())
	if err != nil || !sameWorkspacePath(worktreePath, worktree.path) || !isOpenCodeWorktreeTarget(worktree.manager.root, worktreePath) {
		return agentstore.Session{}, errOpenCodeHistoryScope
	}
	selectedRoot, err := filepath.EvalSymlinks(worktree.WorkspaceRoot())
	if err != nil || !sameWorkspacePath(selectedRoot, worktree.workspaceRoot) {
		return agentstore.Session{}, errOpenCodeHistoryScope
	}
	if !sameWorkspacePath(selectedRoot, worktreePath) && !isContainedPath(worktreePath, selectedRoot) {
		return agentstore.Session{}, errOpenCodeHistoryScope
	}
	remoteRoot, err := canonicalWorkspaceRoot(remote.Location.Directory)
	if err != nil || !sameWorkspacePath(remoteRoot, selectedRoot) {
		return agentstore.Session{}, errOpenCodeHistoryScope
	}
	worktree.manager.mu.Lock()
	owned := worktree.manager.owned[worktreePath] == worktree
	worktree.manager.mu.Unlock()
	if !owned {
		return agentstore.Session{}, errOpenCodeHistoryScope
	}
	created := openCodeSessionTime(remote.Time.Created)
	updated := openCodeSessionTime(remote.Time.Updated)
	if created.IsZero() {
		created = time.Now().UTC()
	}
	if updated.IsZero() {
		updated = created
	}
	stored, err := h.store.CreateSession(ctx, agentstore.Session{
		ID:                stableHistoryID("session", h.serverID, remote.ID),
		WorkspaceRoot:     h.workspaceRoot,
		WorktreeRoot:      worktreePath,
		BaselineCommit:    worktree.baseCommit,
		ServerID:          h.serverID,
		OpenCodeSessionID: remote.ID,
		Title:             remote.Title,
		Status:            status,
		CreatedAt:         created,
		UpdatedAt:         updated,
	})
	if err != nil {
		return agentstore.Session{}, err
	}
	stored.Title = remote.Title
	stored.Status = status
	stored.UpdatedAt = updated
	return h.store.UpdateSessionMetadata(ctx, stored)
}

func (h *openCodeSessionHistory) FindSession(ctx context.Context, openCodeSessionID string) (agentstore.Session, error) {
	if h == nil || h.store == nil || strings.TrimSpace(openCodeSessionID) == "" {
		return agentstore.Session{}, errOpenCodeHistoryScope
	}
	sessions, err := h.ListSessions(ctx)
	if err != nil {
		return agentstore.Session{}, err
	}
	for _, session := range sessions {
		if session.OpenCodeSessionID == openCodeSessionID {
			return session, nil
		}
	}
	return agentstore.Session{}, agentstore.ErrNotFound
}

func openCodeKnownWorktrees(ctx context.Context, manager *openCodeWorktreeManager, history *openCodeSessionHistory) ([]openCodeSessionWorktree, map[string]agentstore.Session, error) {
	if history == nil {
		return nil, nil, nil
	}
	sessions, err := history.ListSessions(ctx)
	if err != nil {
		return nil, nil, err
	}
	byRemoteID := make(map[string]agentstore.Session, len(sessions))
	for _, session := range sessions {
		byRemoteID[session.OpenCodeSessionID] = session
	}
	if manager == nil {
		return nil, byRemoteID, errors.New("OpenCode worktree manager is unavailable")
	}
	type knownPath struct {
		path       string
		baseCommit string
		sessionIDs map[string]struct{}
	}
	byPath := make(map[string]*knownPath)
	invalidPaths := make(map[string]struct{})
	for _, session := range sessions {
		if session.WorktreeRoot == "" || session.OpenCodeSessionID == "" {
			continue
		}
		path, err := filepath.Abs(session.WorktreeRoot)
		if err != nil {
			invalidPaths[session.WorktreeRoot] = struct{}{}
			continue
		}
		if !isOpenCodeWorktreeTarget(manager.root, path) {
			invalidPaths[filepath.Clean(path)] = struct{}{}
			continue
		}
		key := filepath.Clean(path)
		if _, invalid := invalidPaths[key]; invalid {
			continue
		}
		known := byPath[key]
		if known == nil {
			known = &knownPath{path: key, baseCommit: session.BaselineCommit, sessionIDs: make(map[string]struct{})}
			byPath[key] = known
		}
		if known.baseCommit != session.BaselineCommit || session.BaselineCommit != "" && !validOpenCodeCommitID(session.BaselineCommit) {
			delete(byPath, key)
			invalidPaths[key] = struct{}{}
			continue
		}
		known.sessionIDs[session.OpenCodeSessionID] = struct{}{}
	}
	paths := make([]string, 0, len(byPath))
	seenPaths := make(map[string]struct{}, len(byPath))
	for _, session := range sessions {
		if session.WorktreeRoot == "" {
			continue
		}
		path, err := filepath.Abs(session.WorktreeRoot)
		if err != nil {
			continue
		}
		key := filepath.Clean(path)
		if _, known := byPath[key]; !known {
			continue
		}
		if _, seen := seenPaths[key]; seen {
			continue
		}
		seenPaths[key] = struct{}{}
		paths = append(paths, key)
	}
	tooMany := len(paths) > openCodeKnownWorktreeLimit
	if tooMany {
		paths = paths[:openCodeKnownWorktreeLimit]
	}
	worktrees := make([]openCodeSessionWorktree, 0, len(paths))
	for _, path := range paths {
		known := byPath[path]
		var worktree *openCodeWorktree
		var err error
		if known.baseCommit == "" {
			worktree, err = manager.OpenExisting(ctx, history.workspaceRoot, known.path)
		} else {
			worktree, err = manager.OpenExisting(ctx, history.workspaceRoot, known.path, known.baseCommit)
		}
		if err != nil {
			invalidPaths[path] = struct{}{}
			continue
		}
		worktrees = append(worktrees, openCodeSessionWorktree{worktree: worktree, sessionID: known.sessionIDs})
	}
	if tooMany || len(invalidPaths) > 0 {
		return worktrees, byRemoteID, errors.New("some persisted OpenCode worktrees could not be verified")
	}
	return worktrees, byRemoteID, nil
}

func listOpenCodeWorkspaceSessions(ctx context.Context, client *opencode.Client, workspaceRoot, serverID, serverURL string, store *agentstore.Store, manager *openCodeWorktreeManager) ([]opencode.Session, string, error) {
	if client == nil || strings.TrimSpace(workspaceRoot) == "" {
		return nil, "", errors.New("OpenCode workspace session request is incomplete")
	}
	var history *openCodeSessionHistory
	var err error
	if store != nil {
		history, err = newOpenCodeSessionHistory(store, workspaceRoot, serverID)
		if err != nil {
			history = nil
		}
	}
	worktrees, localByID, worktreeErr := openCodeKnownWorktrees(ctx, manager, history)
	page, err := client.ListSessions(ctx, opencode.SessionListOptions{
		Limit: openCodeWorkspaceSessionLimit, Order: "desc", Archived: boolPointer(false), Directory: workspaceRoot,
	})
	if err != nil {
		return nil, "", err
	}
	active, activeErr := client.ListActiveSessions(ctx)
	if ctxErr := contextError(ctx); ctxErr != nil {
		return nil, "", ctxErr
	}
	activeIDs := make(map[string]struct{}, len(active))
	for _, session := range active {
		activeIDs[session.ID] = struct{}{}
	}
	statusFor := func(sessionID string) agentstore.SessionStatus {
		if activeErr != nil {
			return ""
		}
		if _, running := activeIDs[sessionID]; running {
			return agentstore.SessionRunning
		}
		// The active endpoint reports only whether a session is running. An
		// idle session may be new or queued, so only close a session that our
		// own store previously observed running.
		if local, ok := localByID[sessionID]; ok && local.Status == agentstore.SessionRunning {
			return agentstore.SessionCompleted
		}
		return ""
	}
	sessions := openCodeWorkspaceSessions(page.Data, workspaceRoot)
	partial := worktreeErr != nil || history == nil || activeErr != nil
	seen := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		seen[session.ID] = struct{}{}
		if history != nil {
			if _, persistErr := history.PersistSession(ctx, session, statusFor(session.ID)); persistErr != nil {
				partial = true
			}
		}
	}
	if isOpenCodeLoopbackServerURL(serverURL) {
		for _, worktree := range worktrees {
			if len(sessions) >= openCodeWorkspaceSessionLimit {
				break
			}
			if err := contextError(ctx); err != nil {
				return nil, "", err
			}
			worktreePage, listErr := client.ListSessions(ctx, opencode.SessionListOptions{
				Limit: openCodeWorkspaceSessionLimit, Order: "desc", Archived: boolPointer(false), Directory: worktree.worktree.WorkspaceRoot(),
			})
			if listErr != nil {
				partial = true
				continue
			}
			for _, remote := range worktreePage.Data {
				if remote.ID == "" || !sameOpenCodeWorkspace(remote.Location.Directory, worktree.worktree.WorkspaceRoot()) {
					continue
				}
				if _, known := worktree.sessionID[remote.ID]; !known {
					continue
				}
				local, known := localByID[remote.ID]
				if !known || !sameWorkspacePath(local.WorktreeRoot, worktree.worktree.Path()) {
					partial = true
					continue
				}
				if _, duplicate := seen[remote.ID]; duplicate {
					continue
				}
				if history != nil {
					if _, persistErr := history.PersistIsolatedSession(ctx, remote, worktree.worktree, statusFor(remote.ID)); persistErr != nil {
						partial = true
					}
				}
				remote.Location.Directory = workspaceRoot
				sessions = append(sessions, remote)
				seen[remote.ID] = struct{}{}
				if !sameWorkspacePath(local.WorktreeRoot, worktree.worktree.Path()) {
					partial = true
				}
			}
		}
	} else if len(worktrees) > 0 {
		partial = true
	}
	status := "Choose a session or create one for this workspace."
	if partial {
		status = "Sessions loaded; local history or some isolated worktrees could not be verified."
	}
	return sessions, status, nil
}
