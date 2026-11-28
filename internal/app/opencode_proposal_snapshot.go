package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	openCodeProposalMaxFiles      = 64
	openCodeProposalMaxFileBytes  = 256 << 10
	openCodeProposalMaxDiffBytes  = 96 << 10
	openCodeProposalMaxTotalBytes = 512 << 10
	openCodeProposalMaxGitOutput  = 1 << 20
)

var errOpenCodeProposalUnsupported = errors.New("OpenCode proposal contains a change the review panel cannot safely display")

type openCodeProposalSnapshot struct {
	BaseCommit string
	Files      []openCodeProposalFile
	TotalBytes int
}

type openCodeProposalFile struct {
	Path      string
	Change    string
	SizeBytes int64
	Diff      string
}

// snapshotProposal reads changes relative to the checkout's original commit.
// It never stages or applies files. The caller must persist the full commit ID
// returned when the worktree was created and pass it back as baseCommit.
func (m *openCodeWorktreeManager) snapshotProposal(ctx context.Context, worktree *openCodeWorktree, baseCommit string) (openCodeProposalSnapshot, error) {
	if m == nil || worktree == nil || worktree.manager != m || ctx == nil {
		return openCodeProposalSnapshot{}, errors.New("OpenCode proposal worktree is not manager-owned")
	}
	if err := ctx.Err(); err != nil {
		return openCodeProposalSnapshot{}, err
	}
	path, err := filepath.EvalSymlinks(worktree.path)
	if err != nil || !isContainedPath(m.root, path) || !sameWorkspacePath(path, worktree.path) {
		return openCodeProposalSnapshot{}, errOpenCodeWorktreeScope
	}
	m.mu.Lock()
	owned := m.owned[path] == worktree
	m.mu.Unlock()
	if !owned {
		return openCodeProposalSnapshot{}, errors.New("OpenCode proposal worktree is not registered as manager-owned")
	}
	registered, err := m.registeredWorktree(ctx, worktree.gitRoot, path)
	if err != nil || !registered {
		return openCodeProposalSnapshot{}, errors.New("could not verify OpenCode proposal worktree ownership")
	}
	if !validOpenCodeCommitID(baseCommit) {
		return openCodeProposalSnapshot{}, errors.New("OpenCode proposal base must be a full commit ID")
	}
	resolved, err := m.proposalGitOutput(ctx, path, 128, "rev-parse", "--verify", baseCommit+"^{commit}")
	if err != nil || strings.TrimSpace(string(resolved)) != baseCommit {
		return openCodeProposalSnapshot{}, errors.New("OpenCode proposal base commit is unavailable")
	}

	changed, err := m.proposalChangedPaths(ctx, path, baseCommit)
	if err != nil {
		return openCodeProposalSnapshot{}, err
	}
	if len(changed) > openCodeProposalMaxFiles {
		return openCodeProposalSnapshot{}, errors.New("OpenCode proposal contains too many changed files")
	}
	paths := make([]string, 0, len(changed))
	for relative := range changed {
		paths = append(paths, relative)
	}
	sort.Strings(paths)

	snapshot := openCodeProposalSnapshot{BaseCommit: baseCommit, Files: make([]openCodeProposalFile, 0, len(paths))}
	for _, relative := range paths {
		if err := ctx.Err(); err != nil {
			return openCodeProposalSnapshot{}, err
		}
		change := changed[relative]
		currentSize, exists, err := inspectOpenCodeProposalPath(path, relative)
		if err != nil {
			return openCodeProposalSnapshot{}, err
		}
		var size int64
		if exists {
			size = currentSize
		}
		if change == "deleted" {
			if exists {
				return openCodeProposalSnapshot{}, fmt.Errorf("%w: deleted path still exists: %s", errOpenCodeProposalUnsupported, relative)
			}
			mode, baseSize, err := m.proposalBaseFile(ctx, path, baseCommit, relative)
			if err != nil || !supportedProposalMode(mode) || baseSize > openCodeProposalMaxFileBytes {
				return openCodeProposalSnapshot{}, fmt.Errorf("%w: deleted path is not a bounded regular file: %s", errOpenCodeProposalUnsupported, relative)
			}
			size = baseSize
		} else {
			if !exists || size > openCodeProposalMaxFileBytes {
				return openCodeProposalSnapshot{}, fmt.Errorf("%w: changed path is missing or too large: %s", errOpenCodeProposalUnsupported, relative)
			}
			if change == "modified" {
				mode, _, err := m.proposalBaseFile(ctx, path, baseCommit, relative)
				if err != nil || !supportedProposalMode(mode) {
					return openCodeProposalSnapshot{}, fmt.Errorf("%w: original path is not a regular file: %s", errOpenCodeProposalUnsupported, relative)
				}
			}
		}
		diff, err := m.proposalFileDiff(ctx, path, baseCommit, relative, change == "added")
		if err != nil {
			return openCodeProposalSnapshot{}, err
		}
		if len(diff) == 0 || len(diff) > openCodeProposalMaxDiffBytes || bytes.IndexByte(diff, 0) >= 0 || !utf8.Valid(diff) || bytes.Contains(diff, []byte("Binary files ")) || bytes.Contains(diff, []byte("GIT binary patch")) {
			return openCodeProposalSnapshot{}, fmt.Errorf("%w: binary, empty, or oversized diff: %s", errOpenCodeProposalUnsupported, relative)
		}
		if snapshot.TotalBytes+len(diff) > openCodeProposalMaxTotalBytes {
			return openCodeProposalSnapshot{}, errors.New("OpenCode proposal diff exceeds the review size limit")
		}
		snapshot.TotalBytes += len(diff)
		snapshot.Files = append(snapshot.Files, openCodeProposalFile{
			Path: relative, Change: change, SizeBytes: size, Diff: string(diff),
		})
	}
	return snapshot, nil
}

func validOpenCodeCommitID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' && r < 'a' || r > 'f' {
			return false
		}
	}
	return true
}

func (m *openCodeWorktreeManager) proposalChangedPaths(ctx context.Context, directory, baseCommit string) (map[string]string, error) {
	tracked, err := m.proposalGitOutput(ctx, directory, openCodeProposalMaxGitOutput, "diff", "--name-status", "-z", "--find-renames", baseCommit, "--")
	if err != nil {
		return nil, errors.New("could not inspect OpenCode proposal changes")
	}
	changed, err := parseOpenCodeProposalNameStatus(tracked)
	if err != nil {
		return nil, err
	}
	ignored, err := m.proposalGitOutput(ctx, directory, openCodeProposalMaxGitOutput, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return nil, errors.New("could not inspect ignored OpenCode proposal files; snapshot is incomplete")
	}
	if len(ignored) != 0 {
		return nil, errors.New("OpenCode proposal snapshot is incomplete: ignored untracked files are present and were not included")
	}
	untracked, err := m.proposalGitOutput(ctx, directory, openCodeProposalMaxGitOutput, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, errors.New("could not inspect untracked OpenCode proposal files")
	}
	paths, err := parseOpenCodeUntrackedPaths(untracked)
	if err != nil {
		return nil, err
	}
	if len(changed)+len(paths) > openCodeProposalMaxFiles {
		return nil, errors.New("OpenCode proposal contains too many changed files")
	}
	for _, relative := range paths {
		if _, exists := changed[relative]; exists {
			return nil, fmt.Errorf("%w: path is both tracked and untracked: %s", errOpenCodeProposalUnsupported, relative)
		}
		changed[relative] = "added"
	}
	return changed, nil
}

func parseOpenCodeProposalNameStatus(data []byte) (map[string]string, error) {
	changed := make(map[string]string)
	if len(data) == 0 {
		return changed, nil
	}
	if data[len(data)-1] != 0 {
		return nil, errors.New("Git returned malformed OpenCode proposal status")
	}
	parts := bytes.Split(data[:len(data)-1], []byte{0})
	if len(parts)%2 != 0 {
		return nil, errors.New("Git returned malformed OpenCode proposal status")
	}
	for index := 0; index < len(parts); index += 2 {
		status, relative := string(parts[index]), string(parts[index+1])
		if status != "A" && status != "M" && status != "D" {
			return nil, fmt.Errorf("%w: Git status %q", errOpenCodeProposalUnsupported, status)
		}
		if err := validateOpenCodeProposalPath(relative); err != nil {
			return nil, err
		}
		change := map[string]string{"A": "added", "M": "modified", "D": "deleted"}[status]
		if _, exists := changed[relative]; exists {
			return nil, fmt.Errorf("%w: duplicate path: %s", errOpenCodeProposalUnsupported, relative)
		}
		changed[relative] = change
	}
	return changed, nil
}

func parseOpenCodeUntrackedPaths(data []byte) ([]string, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if data[len(data)-1] != 0 {
		return nil, errors.New("Git returned malformed untracked OpenCode proposal paths")
	}
	parts := bytes.Split(data[:len(data)-1], []byte{0})
	if len(parts) > openCodeProposalMaxFiles {
		return nil, errors.New("OpenCode proposal contains too many untracked files")
	}
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		relative := string(part)
		if err := validateOpenCodeProposalPath(relative); err != nil {
			return nil, err
		}
		paths = append(paths, relative)
	}
	return paths, nil
}

func validateOpenCodeProposalPath(relative string) error {
	if relative == "" || !utf8.ValidString(relative) || strings.ContainsAny(relative, "\\\r\n\x00") || !filepath.IsLocal(filepath.FromSlash(relative)) {
		return fmt.Errorf("%w: invalid proposal path", errOpenCodeProposalUnsupported)
	}
	for _, r := range relative {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: path contains control characters", errOpenCodeProposalUnsupported)
		}
	}
	for _, segment := range strings.Split(relative, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.EqualFold(segment, ".git") {
			return fmt.Errorf("%w: unsafe proposal path", errOpenCodeProposalUnsupported)
		}
	}
	return nil
}

func inspectOpenCodeProposalPath(root, relative string) (int64, bool, error) {
	if err := validateOpenCodeProposalPath(relative); err != nil {
		return 0, false, err
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return 0, false, errOpenCodeWorktreeScope
	}
	fullPath := filepath.Join(root, filepath.FromSlash(relative))
	if !isContainedPath(root, fullPath) {
		return 0, false, errOpenCodeWorktreeScope
	}
	segments := strings.Split(relative, "/")
	current := root
	for index, segment := range segments {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return 0, false, fmt.Errorf("%w: path is inaccessible or contains a symlink: %s", errOpenCodeProposalUnsupported, relative)
		}
		if index < len(segments)-1 {
			if !info.IsDir() {
				return 0, false, fmt.Errorf("%w: path parent is not a directory: %s", errOpenCodeProposalUnsupported, relative)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return 0, false, fmt.Errorf("%w: path is not a regular file: %s", errOpenCodeProposalUnsupported, relative)
		}
		return info.Size(), true, nil
	}
	return 0, false, nil
}

func (m *openCodeWorktreeManager) proposalBaseFile(ctx context.Context, directory, baseCommit, relative string) (string, int64, error) {
	pathspec := ":(literal)" + relative
	data, err := m.proposalGitOutput(ctx, directory, 4096, "ls-tree", "-z", baseCommit, "--", pathspec)
	if err != nil || len(data) == 0 || data[len(data)-1] != 0 {
		return "", 0, errors.New("OpenCode proposal base file is unavailable")
	}
	records := bytes.Split(data[:len(data)-1], []byte{0})
	if len(records) != 1 {
		return "", 0, errors.New("OpenCode proposal base path is ambiguous")
	}
	metadata, name, ok := bytes.Cut(records[0], []byte{'\t'})
	if !ok || string(name) != relative {
		return "", 0, errors.New("OpenCode proposal base path did not match")
	}
	fields := bytes.Fields(metadata)
	if len(fields) != 3 || string(fields[1]) != "blob" || !validGitObjectID(string(fields[2])) {
		return "", 0, errors.New("OpenCode proposal base entry is not a file")
	}
	mode := string(fields[0])
	sizeData, err := m.proposalGitOutput(ctx, directory, 128, "cat-file", "-s", string(fields[2]))
	if err != nil {
		return "", 0, errors.New("could not inspect OpenCode proposal base file size")
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeData)), 10, 64)
	if err != nil || size < 0 {
		return "", 0, errors.New("Git returned an invalid OpenCode proposal base size")
	}
	return mode, size, nil
}

func validGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' && r < 'a' || r > 'f' {
			return false
		}
	}
	return true
}

func supportedProposalMode(mode string) bool { return mode == "100644" || mode == "100755" }

func (m *openCodeWorktreeManager) proposalFileDiff(ctx context.Context, directory, baseCommit, relative string, untracked bool) ([]byte, error) {
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--unified=3"}
	if untracked {
		args = append(args, "--no-index", "--", os.DevNull, relative)
	} else {
		args = append(args, baseCommit, "--", ":(literal)"+relative)
	}
	data, err := m.proposalGitOutput(ctx, directory, openCodeProposalMaxDiffBytes, args...)
	if err != nil && untracked {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return data, nil
		}
	}
	if err != nil {
		return nil, errors.New("could not create OpenCode proposal diff")
	}
	return data, nil
}

func (m *openCodeWorktreeManager) proposalGitOutput(ctx context.Context, directory string, limit int, args ...string) ([]byte, error) {
	if ctx == nil {
		return nil, errOpenCodeWorktreeContext
	}
	if limit <= 0 || limit > openCodeProposalMaxGitOutput {
		limit = openCodeProposalMaxGitOutput
	}
	full := append([]string{"-C", directory, "-c", "core.hooksPath=" + m.hooksPath, "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) //nolint:gosec // fixed executable and subcommands; file paths remain isolated arguments
	cmd.Env = openCodeGitEnvironment()
	hideConsoleWindow(cmd)
	stdout := &worktreeOutputBuffer{limit: limit}
	cmd.Stdout = stdout
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if stdout.truncated {
		return stdout.data, errors.New("Git proposal output exceeds its limit")
	}
	return stdout.data, err
}
