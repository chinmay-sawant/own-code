package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// OpenExisting attaches a persisted OpenCode worktree to a fresh manager after an
// application restart. It accepts only a direct child created by Create and
// verifies that Git still registers it against the original workspace's repo.
func (m *openCodeWorktreeManager) OpenExisting(ctx context.Context, workspaceRoot, worktreePath string, baseline ...string) (*openCodeWorktree, error) {
	if m == nil || strings.TrimSpace(m.root) == "" {
		return nil, errors.New("OpenCode worktree manager is unavailable")
	}
	if ctx == nil {
		return nil, errOpenCodeWorktreeContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspace, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, errors.New("OpenCode worktree workspace is unavailable")
	}

	path, err := filepath.Abs(worktreePath)
	if err != nil || !isOpenCodeWorktreeTarget(m.root, path) {
		return nil, errOpenCodeWorktreeScope
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("persisted OpenCode worktree must be a real directory")
	}
	canonicalPath, err := filepath.EvalSymlinks(path)
	if err != nil || !isContainedPath(m.root, canonicalPath) || !sameWorkspacePath(path, canonicalPath) {
		return nil, errOpenCodeWorktreeScope
	}

	workspaceGitRoot, err := openCodeWorktreeRoot(ctx, m, workspace)
	if err != nil {
		return nil, errors.New("could not resolve the original Git workspace")
	}
	if pathsOverlap(workspaceGitRoot, m.root) {
		return nil, errors.New("private OpenCode worktrees must be outside the selected Git repository")
	}
	workspaceRel, err := filepath.Rel(workspaceGitRoot, workspace)
	if err != nil || workspaceRel == ".." || !filepath.IsLocal(workspaceRel) {
		return nil, errors.New("selected project directory is outside its Git worktree")
	}
	if workspaceRel == "." {
		workspaceRel = ""
	}

	worktreeGitRoot, err := openCodeWorktreeRoot(ctx, m, canonicalPath)
	if err != nil || !sameWorkspacePath(worktreeGitRoot, canonicalPath) {
		return nil, errors.New("persisted path is not the root of its Git worktree")
	}
	workspaceCommonDir, err := openCodeWorktreeCommonDir(ctx, m, workspaceGitRoot)
	if err != nil {
		return nil, errors.New("could not resolve the original Git repository identity")
	}
	worktreeCommonDir, err := openCodeWorktreeCommonDir(ctx, m, worktreeGitRoot)
	if err != nil || !sameWorkspacePath(workspaceCommonDir, worktreeCommonDir) {
		return nil, errors.New("persisted OpenCode worktree belongs to a different Git repository")
	}
	registered, err := m.registeredWorktree(ctx, workspaceGitRoot, canonicalPath)
	if err != nil {
		return nil, err
	}
	if !registered {
		return nil, errors.New("persisted OpenCode worktree is no longer registered with Git")
	}
	baseCommit := ""
	if len(baseline) > 1 {
		return nil, errors.New("persisted OpenCode worktree has ambiguous baseline metadata")
	}
	if len(baseline) == 1 && baseline[0] != "" {
		baseCommit = baseline[0]
		if !validOpenCodeCommitID(baseCommit) {
			return nil, errors.New("persisted OpenCode worktree has an invalid baseline commit")
		}
		resolved, resolveErr := m.gitOutput(ctx, canonicalPath, "rev-parse", "--verify", baseCommit+"^{commit}")
		if resolveErr != nil || strings.TrimSpace(string(resolved)) != baseCommit {
			return nil, errors.New("persisted OpenCode worktree baseline commit is unavailable")
		}
	}

	selectedRoot := canonicalPath
	if workspaceRel != "" {
		selectedRoot = filepath.Join(canonicalPath, workspaceRel)
		selectedInfo, statErr := os.Lstat(selectedRoot)
		if statErr != nil || !selectedInfo.IsDir() || selectedInfo.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("selected project subdirectory is missing from the OpenCode worktree")
		}
		resolvedSelectedRoot, resolveErr := filepath.EvalSymlinks(selectedRoot)
		if resolveErr != nil || !isContainedPath(canonicalPath, resolvedSelectedRoot) || !sameWorkspacePath(selectedRoot, resolvedSelectedRoot) {
			return nil, errors.New("selected project subdirectory escapes the OpenCode worktree")
		}
	}

	worktree := &openCodeWorktree{
		manager:       m,
		path:          canonicalPath,
		workspaceRoot: selectedRoot,
		gitRoot:       workspaceGitRoot,
		workspaceRel:  workspaceRel,
		baseCommit:    baseCommit,
	}
	m.mu.Lock()
	if existing := m.owned[canonicalPath]; existing != nil {
		m.mu.Unlock()
		if sameWorkspacePath(existing.workspaceRoot, selectedRoot) {
			return existing, nil
		}
		return nil, errors.New("persisted OpenCode worktree is already attached to another workspace")
	}
	m.owned[canonicalPath] = worktree
	m.mu.Unlock()
	return worktree, nil
}

func openCodeWorktreeRoot(ctx context.Context, manager *openCodeWorktreeManager, directory string) (string, error) {
	output, err := manager.gitOutput(ctx, directory, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root, err := singleGitPathOutput(output)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	return root, nil
}

func openCodeWorktreeCommonDir(ctx context.Context, manager *openCodeWorktreeManager, gitRoot string) (string, error) {
	output, err := manager.gitOutput(ctx, gitRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	commonDir, err := singleGitPathOutput(output)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitRoot, commonDir)
	}
	commonDir, err = filepath.Abs(commonDir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(commonDir)
}

func singleGitPathOutput(output []byte) (string, error) {
	value := string(output)
	if !strings.HasSuffix(value, "\n") || strings.Count(value, "\n") != 1 {
		return "", errors.New("Git returned an invalid path")
	}
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) {
		return "", errors.New("Git returned an invalid path")
	}
	return value, nil
}
