package app

import (
	"context"
	"path/filepath"

	"github.com/chinmay-sawant/own-code/internal/agentstore"
)

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
