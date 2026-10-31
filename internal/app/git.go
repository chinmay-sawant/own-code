package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chinmay-sawant/own-code/internal/merge"
)

const (
	gitTimeout = 3 * time.Second
	// untrackedLimit is the largest untracked file the diff view synthesizes.
	untrackedLimit = 256 << 10
	// conflictReadLimit caps the working file read while counting conflict
	// markers. A larger file is reported unmerged without a marker count.
	conflictReadLimit = 1 << 20
)

// loadLiveChanges reads the working tree diff for -root. It keeps the
// combined HEAD diff in Files and splits Staged (`git diff --cached`) from
// Unstaged (`git diff` plus untracked files). Untracked files are added as
// synthetic all-added hunks. Writes live in scm_ops.go.
func loadLiveChanges(ctx context.Context, root string) (changeSet, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	branch, _ := gitText(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	cs := changeSet{Branch: branch}
	if cs.Branch == "" {
		cs.Branch = "detached"
	}

	raw, err := gitBytes(ctx, root, "diff", "--no-color", "--no-ext-diff", "HEAD")
	if err != nil {
		// A repository without a first commit has no HEAD. Show the two sides
		// git can still produce instead of failing the whole panel.
		work, workErr := gitBytes(ctx, root, "diff", "--no-color", "--no-ext-diff")
		cached, cachedErr := gitBytes(ctx, root, "diff", "--cached", "--no-color", "--no-ext-diff")
		if workErr != nil && cachedErr != nil {
			return cs, err
		}

		cs.Staged = parseDiff(string(cached))
		cs.Unstaged = parseDiff(string(work))
	} else {
		cached, cachedErr := gitBytes(ctx, root, "diff", "--cached", "--no-color", "--no-ext-diff")
		if cachedErr == nil {
			cs.Staged = parseDiff(string(cached))
		}

		work, workErr := gitBytes(ctx, root, "diff", "--no-color", "--no-ext-diff")
		if workErr == nil {
			cs.Unstaged = parseDiff(string(work))
		} else {
			cs.Unstaged = parseDiff(string(raw))
			cs.Staged = nil
		}

		cs.Files = parseDiff(string(raw))
	}

	untracked, err := gitBytes(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
	if err == nil {
		for _, path := range strings.Split(string(untracked), "\x00") {
			if path == "" {
				continue
			}

			if file, ok := untrackedChange(root, path); ok {
				cs.Files = append(cs.Files, file)
				cs.Unstaged = append(cs.Unstaged, file)
			}
		}
	}

	// Conflict detection is best-effort: a failed `git status` or an
	// unreadable file must never hide the ordinary working-tree diff.
	cs.Conflicts = scanConflicts(ctx, root)
	cs.Files = withoutUnmerged(cs.Files, cs.Conflicts)
	cs.Staged = withoutUnmerged(cs.Staged, cs.Conflicts)
	cs.Unstaged = withoutUnmerged(cs.Unstaged, cs.Conflicts)

	sortChangeFiles(cs.Files)
	sortChangeFiles(cs.Staged)
	sortChangeFiles(cs.Unstaged)
	sortChangeFiles(cs.Conflicts)

	switch {
	case len(cs.Files) == 0 && len(cs.Conflicts) > 0:
		cs.Note = "Resolve the merge conflicts before committing."
	case len(cs.Files) == 0 && len(cs.Conflicts) == 0:
		cs.Note = "The working tree is clean. Make a change and refresh."
	}

	return cs, nil
}

// unmergedCodes are the two-letter `git status --porcelain` states that mark
// a path as unmerged.
var unmergedCodes = map[string]bool{
	"UU": true, "AA": true, "DD": true,
	"AU": true, "UA": true, "DU": true, "UD": true,
}

// scanConflicts lists unmerged paths and counts the conflict blocks in each
// readable text file. Paths are relative to the git root. Failures are silent
// so Source Control still loads when conflict inspection is unavailable.
func scanConflicts(ctx context.Context, root string) []changedFile {
	raw, err := gitBytes(ctx, root, "status", "--porcelain", "-z")
	if err != nil {
		return nil
	}

	var out []changedFile
	entries := strings.Split(string(raw), "\x00")

	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}

		code := entry[:2]
		path := entry[3:]

		// Rename and copy entries carry the original path as the next
		// NUL-separated field. Unmerged codes never use R or C, but the
		// field must still be consumed to keep the walk aligned.
		if code[0] == 'R' || code[0] == 'C' {
			i++

			continue
		}

		if !unmergedCodes[code] || !validWorkspacePath(path) {
			continue
		}

		file := changedFile{
			Path:         path,
			Status:       code,
			Unmerged:     true,
			ConflictCode: code,
		}

		full := filepath.Join(root, filepath.FromSlash(path))

		info, statErr := os.Stat(full)
		if statErr != nil {
			out = append(out, file)

			continue
		}

		if info.Size() > conflictReadLimit {
			file.Oversized = true
			out = append(out, file)

			continue
		}

		text, readErr := os.ReadFile(full) //nolint:gosec // path came from git status under the scanned root
		if readErr != nil {
			out = append(out, file)

			continue
		}

		if isBinary(text) {
			file.Binary = true
			out = append(out, file)

			continue
		}

		file.Conflicts = merge.Parse(string(text)).Count()
		out = append(out, file)
	}

	return out
}

// withoutUnmerged drops conflict paths from an ordinary change list.
func withoutUnmerged(files, conflicts []changedFile) []changedFile {
	if len(files) == 0 || len(conflicts) == 0 {
		return files
	}

	blocked := make(map[string]bool, len(conflicts))
	for _, file := range conflicts {
		blocked[file.Path] = true
	}

	out := files[:0]
	for _, file := range files {
		if !blocked[file.Path] {
			out = append(out, file)
		}
	}

	return out
}

// validWorkspacePath rejects absolute paths and paths that escape the root.
func validWorkspacePath(path string) bool {
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "" || clean == "." || filepath.IsAbs(clean) {
		return false
	}

	return clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// sortChangeFiles orders files case-insensitively by path.
func sortChangeFiles(files []changedFile) {
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].Path) < strings.ToLower(files[j].Path)
	})
}

// untrackedChange turns one untracked file into an all-added change.
func untrackedChange(root, path string) (changedFile, bool) {
	full := filepath.Join(root, filepath.FromSlash(path))

	info, err := os.Stat(full)
	if err != nil || info.Size() > untrackedLimit {
		return changedFile{}, false
	}

	raw, err := os.ReadFile(full) //nolint:gosec // path came from git ls-files under the scanned root
	if err != nil {
		return changedFile{}, false
	}

	file := changedFile{Path: path, Status: "U"}

	if isBinary(raw) {
		file.Binary = true

		return file, true
	}

	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	lines := strings.Split(text, "\n")

	h := hunk{NewStart: 1, NewCount: len(lines), oldNext: 1, newNext: 1}
	h.OldStart = 0

	for i, line := range lines {
		h.Lines = append(h.Lines, diffLine{Kind: '+', NewNo: i + 1, Text: expandTabs(line)})
	}

	file.Add = len(lines)
	file.Hunks = []hunk{h}
	measureDiff(&file)

	return file, true
}

// gitText runs one git command and returns its trimmed stdout.
func gitText(ctx context.Context, root string, args ...string) (string, error) {
	out, err := gitBytes(ctx, root, args...)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

// cappedBuffer keeps at most limit bytes and drains the rest without an EPIPE.
type cappedBuffer struct {
	buf       []byte
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - len(b.buf); room > 0 {
		if len(p) <= room {
			b.buf = append(b.buf, p...)
		} else {
			b.buf = append(b.buf, p[:room]...)
			b.truncated = true
		}
	} else if len(p) > 0 {
		b.truncated = true
	}

	return len(p), nil
}

// gitBytesCapped runs one git command and caps its stdout.
func gitBytesCapped(ctx context.Context, root string, limit int, args ...string) ([]byte, bool, error) {
	full := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) //nolint:gosec // fixed binary, fixed subcommand, operator-selected -C
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	hideConsoleWindow(cmd)

	buf := &cappedBuffer{limit: limit}
	cmd.Stdout = buf

	if err := cmd.Run(); err != nil {
		return nil, false, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	return buf.buf, buf.truncated, nil
}

// gitBytes runs one git command. The arguments are fixed by the caller and
// -C carries the operator-selected workspace root.
func gitBytes(ctx context.Context, root string, args ...string) ([]byte, error) {
	full := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) //nolint:gosec // fixed binary, fixed subcommand, operator-selected -C
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	hideConsoleWindow(cmd)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	return out, nil
}

// gitBytesStdin runs one git command with input on stdin. It is used for
// check-ignore batches, where one process answers for every scanned directory.
func gitBytesStdin(ctx context.Context, root, input string, args ...string) ([]byte, error) {
	full := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) //nolint:gosec // fixed binary, fixed subcommand, operator-selected -C
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	cmd.Stdin = strings.NewReader(input)
	hideConsoleWindow(cmd)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	return out, nil
}
