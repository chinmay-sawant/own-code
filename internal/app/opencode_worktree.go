package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	openCodeWorktreeDirectory = "agent-workbench/worktrees"
	openCodeWorktreeMaxOutput = 1 << 20
)

var (
	errOpenCodeWorktreeContext = errors.New("OpenCode worktree requires an active context")
	errOpenCodeWorktreeScope   = errors.New("OpenCode worktree path is outside its private directory")
)

// openCodeWorktreeManager creates isolated checkouts for agent sessions. It
// deliberately starts each checkout from committed HEAD; it never copies,
// stages, or resets the user's index or working tree.
type openCodeWorktreeManager struct {
	root      string
	hooksPath string

	mu    sync.Mutex
	owned map[string]*openCodeWorktree
}

// openCodeWorktree is a manager-owned checkout. WorkspaceRoot is the selected
// project root mapped to the same relative path inside the worktree.
type openCodeWorktree struct {
	manager       *openCodeWorktreeManager
	path          string
	workspaceRoot string
	gitRoot       string
	workspaceRel  string
	baseCommit    string
}

// newOpenCodeWorktreeManager creates a private per-user worktree area below
// the application config directory passed by the caller.
func newOpenCodeWorktreeManager(configDir string) (*openCodeWorktreeManager, error) {
	if strings.TrimSpace(configDir) == "" {
		return nil, errors.New("OpenCode worktree config directory is required")
	}
	root, err := filepath.Abs(filepath.Join(configDir, filepath.FromSlash(openCodeWorktreeDirectory)))
	if err != nil {
		return nil, errors.New("could not resolve the OpenCode worktree config directory")
	}
	if err := ensurePrivateDirectory(root); err != nil {
		return nil, err
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, errors.New("could not resolve the private OpenCode worktree directory")
	}
	hooksPath := filepath.Join(canonicalRoot, ".empty-hooks")
	if err := ensurePrivateDirectory(hooksPath); err != nil {
		return nil, err
	}
	return &openCodeWorktreeManager{
		root:      canonicalRoot,
		hooksPath: hooksPath,
		owned:     make(map[string]*openCodeWorktree),
	}, nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return errors.New("could not create the private OpenCode worktree directory")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("OpenCode worktree directory must be a real directory")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return errors.New("could not restrict OpenCode worktree directory permissions")
	}
	return nil
}

// Create adds one detached worktree at committed HEAD. It refuses repositories
// with checkout filters or tracked symlinks because either can cause code to
// execute or redirect file access while the checkout is prepared.
func (m *openCodeWorktreeManager) Create(ctx context.Context, workspaceRoot string) (*openCodeWorktree, error) {
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
		return nil, errors.New("Open a real project directory before starting an isolated OpenCode session")
	}
	gitRootOutput, err := m.gitOutput(ctx, workspace, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errors.New("OpenCode edit sessions require a Git workspace")
	}
	gitRootText := string(gitRootOutput)
	if !strings.HasSuffix(gitRootText, "\n") || strings.Count(gitRootText, "\n") != 1 {
		return nil, errors.New("git returned an invalid worktree root")
	}
	gitRootText = strings.TrimSuffix(gitRootText, "\n")
	gitRootText = strings.TrimSuffix(gitRootText, "\r")
	if strings.TrimSpace(gitRootText) == "" {
		return nil, errors.New("git returned an invalid worktree root")
	}
	gitRoot, err := filepath.EvalSymlinks(gitRootText)
	if err != nil {
		return nil, errors.New("could not resolve the Git worktree root")
	}
	if pathsOverlap(gitRoot, m.root) {
		return nil, errors.New("private OpenCode worktrees must be outside the selected Git repository")
	}
	workspaceRel, err := filepath.Rel(gitRoot, workspace)
	if err != nil || workspaceRel == ".." || !filepath.IsLocal(workspaceRel) {
		return nil, errors.New("selected project directory is outside its Git worktree")
	}
	if workspaceRel == "." {
		workspaceRel = ""
	}
	if err := m.rejectCheckoutFilters(ctx, gitRoot); err != nil {
		return nil, err
	}
	if err := m.rejectTrackedSymlinks(ctx, gitRoot); err != nil {
		return nil, err
	}
	head, err := m.gitOutput(ctx, gitRoot, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, errors.New("OpenCode edit sessions require a committed Git revision")
	}
	commit := strings.TrimSpace(string(head))
	if commit == "" || strings.ContainsAny(commit, "\r\n") {
		return nil, errors.New("git did not return a valid committed revision")
	}
	name, err := randomWorktreeName()
	if err != nil {
		return nil, errors.New("could not allocate an isolated OpenCode worktree")
	}
	path := filepath.Join(m.root, name)
	if !isContainedPath(m.root, path) {
		return nil, errOpenCodeWorktreeScope
	}
	if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("OpenCode worktree target already exists")
	}
	if _, err := m.gitOutput(ctx, gitRoot, "-c", "core.hooksPath="+m.hooksPath, "-c", "core.fsmonitor=false", "worktree", "add", "--detach", "--", path, commit); err != nil {
		if !m.removeFailedAdd(gitRoot, path, workspace, workspaceRel) {
			return nil, errors.New("could not create the isolated OpenCode worktree; a partial checkout remains in the private worktree directory")
		}
		return nil, errors.New("could not create an isolated OpenCode worktree")
	}
	canonicalPath, err := filepath.EvalSymlinks(path)
	if err != nil || !isContainedPath(m.root, canonicalPath) || !sameWorkspacePath(path, canonicalPath) {
		return nil, errors.New("created OpenCode worktree did not resolve inside its private directory")
	}
	worktree := &openCodeWorktree{
		manager:       m,
		path:          canonicalPath,
		workspaceRoot: canonicalPath, // the isolated checkout, never the user's directory
		gitRoot:       gitRoot,
		workspaceRel:  workspaceRel,
		baseCommit:    commit,
	}
	if workspaceRel != "" {
		worktree.workspaceRoot = filepath.Join(canonicalPath, workspaceRel)
		info, statErr := os.Lstat(worktree.workspaceRoot)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			// The just-created worktree is registered by this manager. Remove it
			// only through Git's non-forcing cleanup path; dirty checkouts remain.
			m.mu.Lock()
			m.owned[canonicalPath] = worktree
			m.mu.Unlock()
			_ = m.Remove(ctx, worktree)
			return nil, errors.New("selected project subdirectory is missing from the committed revision")
		}
	}
	m.mu.Lock()
	m.owned[canonicalPath] = worktree
	m.mu.Unlock()
	return worktree, nil
}

// removeFailedAdd only force-removes the random path attempted by the current
// Create call, after proving Git registered that exact path below the private
// root. The worktree has not been returned to a caller, so no agent could have
// written proposals there yet. Public Remove remains non-forcing.
func (m *openCodeWorktreeManager) removeFailedAdd(gitRoot, target, workspaceRoot, workspaceRel string) bool {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if !isOpenCodeWorktreeTarget(m.root, target) {
		return false
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	canonicalPath, err := filepath.EvalSymlinks(target)
	if err != nil || !sameWorkspacePath(canonicalPath, target) || !isContainedPath(m.root, canonicalPath) {
		return false
	}
	registered, err := m.registeredWorktree(cleanupCtx, gitRoot, canonicalPath)
	if err != nil || !registered {
		return false
	}
	worktree := &openCodeWorktree{
		manager:       m,
		path:          canonicalPath,
		workspaceRoot: workspaceRoot,
		gitRoot:       gitRoot,
		workspaceRel:  workspaceRel,
	}
	m.mu.Lock()
	m.owned[canonicalPath] = worktree
	m.mu.Unlock()
	if _, err := m.gitOutput(cleanupCtx, gitRoot, "worktree", "remove", "--force", "--", canonicalPath); err != nil {
		return false
	}
	m.mu.Lock()
	delete(m.owned, canonicalPath)
	m.mu.Unlock()
	return true
}

// WorkspaceRoot returns the selected project directory inside the isolated
// checkout, preserving a selected subdirectory when the Git root is higher.
func (w *openCodeWorktree) WorkspaceRoot() string {
	if w == nil {
		return ""
	}
	return w.workspaceRoot
}

// Path returns the root of the isolated Git checkout.
func (w *openCodeWorktree) Path() string {
	if w == nil {
		return ""
	}
	return w.path
}

// Remove unregisters and removes a manager-owned worktree only when Git can
// remove it without force. Pending agent edits or untracked files are kept.
func (m *openCodeWorktreeManager) Remove(ctx context.Context, worktree *openCodeWorktree) error {
	if m == nil || worktree == nil || worktree.manager != m || ctx == nil {
		return errors.New("OpenCode worktree is not owned by this manager")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := filepath.EvalSymlinks(worktree.path)
	if err != nil || !isContainedPath(m.root, path) || !sameWorkspacePath(path, worktree.path) {
		return errOpenCodeWorktreeScope
	}
	m.mu.Lock()
	owned := m.owned[path] == worktree
	m.mu.Unlock()
	if !owned {
		return errors.New("OpenCode worktree is not registered as manager-owned")
	}
	registered, err := m.registeredWorktree(ctx, worktree.gitRoot, path)
	if err != nil {
		return err
	}
	if !registered {
		return errors.New("OpenCode worktree is no longer registered with Git")
	}
	if _, err := m.gitOutput(ctx, worktree.gitRoot, "worktree", "remove", "--", path); err != nil {
		return errors.New("OpenCode worktree contains pending changes and was left in place")
	}
	m.mu.Lock()
	delete(m.owned, path)
	m.mu.Unlock()
	return nil
}

func (m *openCodeWorktreeManager) registeredWorktree(ctx context.Context, gitRoot, target string) (bool, error) {
	sep := []byte{0}
	data, err := m.gitOutput(ctx, gitRoot, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		// Git before 2.36 has no -z for worktree list; fall back to lines.
		sep = []byte{'\n'}
		data, err = m.gitOutput(ctx, gitRoot, "worktree", "list", "--porcelain")
	}
	if err != nil {
		return false, errors.New("could not verify OpenCode worktree ownership")
	}
	for _, record := range bytes.Split(data, sep) {
		if !bytes.HasPrefix(record, []byte("worktree ")) {
			continue
		}
		registered := string(bytes.TrimPrefix(record, []byte("worktree ")))
		absolute, absErr := filepath.Abs(registered)
		if absErr != nil {
			continue
		}
		if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
			absolute = resolved
		} else {
			absolute = filepath.Clean(absolute)
		}
		if sameWorkspacePath(absolute, target) {
			return true, nil
		}
	}
	return false, nil
}

func (m *openCodeWorktreeManager) rejectCheckoutFilters(ctx context.Context, gitRoot string) error {
	_, err := m.gitOutput(ctx, gitRoot, "config", "--local", "--name-only", "--get-regexp", `^filter\..*\.(smudge|process)$`)
	if err == nil {
		return errors.New("OpenCode isolated sessions are disabled for repositories with Git checkout filters")
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil
	}
	return errors.New("could not inspect Git checkout filters")
}

func (m *openCodeWorktreeManager) rejectTrackedSymlinks(ctx context.Context, gitRoot string) error {
	entries, err := m.gitOutput(ctx, gitRoot, "ls-files", "--stage", "-z")
	if err != nil {
		return errors.New("could not inspect tracked paths before creating an OpenCode worktree")
	}
	for _, entry := range bytes.Split(entries, []byte{0}) {
		metadata, _, ok := bytes.Cut(entry, []byte{'\t'})
		if !ok {
			continue
		}
		fields := bytes.Fields(metadata)
		if len(fields) > 0 && bytes.Equal(fields[0], []byte("120000")) {
			return errors.New("OpenCode isolated sessions are disabled for repositories with tracked symlinks")
		}
	}
	return nil
}

func (m *openCodeWorktreeManager) gitOutput(ctx context.Context, directory string, args ...string) ([]byte, error) {
	if ctx == nil {
		return nil, errOpenCodeWorktreeContext
	}
	full := append([]string{"-C", directory}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) //nolint:gosec // fixed executable and subcommands; paths are passed as arguments
	cmd.Env = openCodeGitEnvironment()
	hideConsoleWindow(cmd)
	stdout := &worktreeOutputBuffer{limit: openCodeWorktreeMaxOutput}
	cmd.Stdout = stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if stdout.truncated {
		return nil, errors.New("git worktree command output exceeds its limit")
	}
	return stdout.data, nil
}

func openCodeGitEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+8)
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found && strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment,
		"GIT_PAGER=cat",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_NO_LAZY_FETCH=1",
		"GIT_ATTR_NOSYSTEM=1",
		"LC_ALL=C",
	)
}

func randomWorktreeName() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "opencode-" + hex.EncodeToString(random[:]), nil
}

func isContainedPath(root, path string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return false
	}
	return runtime.GOOS != "windows" || strings.EqualFold(filepath.Clean(filepath.Join(root, rel)), path)
}

func isOpenCodeWorktreeTarget(root, path string) bool {
	if !isContainedPath(root, path) || !sameWorkspacePath(filepath.Dir(filepath.Clean(path)), filepath.Clean(root)) {
		return false
	}
	name := filepath.Base(path)
	if !strings.HasPrefix(name, "opencode-") {
		return false
	}
	identity, err := hex.DecodeString(strings.TrimPrefix(name, "opencode-"))
	return err == nil && len(identity) == 16
}

type worktreeOutputBuffer struct {
	data      []byte
	limit     int
	truncated bool
}

func (b *worktreeOutputBuffer) Write(data []byte) (int, error) {
	if b.limit <= 0 {
		b.limit = openCodeWorktreeMaxOutput
	}
	room := b.limit - len(b.data)
	if room > 0 {
		if len(data) <= room {
			b.data = append(b.data, data...)
		} else {
			b.data = append(b.data, data[:room]...)
			b.truncated = true
		}
	} else if len(data) > 0 {
		b.truncated = true
	}
	return len(data), nil
}

func pathsOverlap(left, right string) bool {
	return sameWorkspacePath(left, right) || isContainedPath(left, right) || isContainedPath(right, left)
}
