package agentreview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxReviewBytes is the largest file the hunk model will review.
const MaxReviewBytes = 1 << 20

// ErrOutside means an agent path is not a plain file inside the workspace.
var ErrOutside = errors.New("agentreview: path is outside the workspace")

// MapPath maps an agent-produced path (slash or native, relative) to an
// absolute file under root. It rejects absolute paths, traversal, .git, NULs,
// symlinks at or above the target, and anything resolving outside root.
func MapPath(root, agentPath string) (abs, rel string, err error) {
	if agentPath == "" || strings.ContainsAny(agentPath, "\x00\r\n") {
		return "", "", fmt.Errorf("%w: empty or control characters", ErrOutside)
	}
	p := strings.ReplaceAll(agentPath, `\`, "/")
	if strings.HasPrefix(p, "/") || filepath.IsAbs(agentPath) || (len(p) > 1 && p[1] == ':') {
		return "", "", fmt.Errorf("%w: absolute path %q", ErrOutside, agentPath)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", "", fmt.Errorf("%w: traversal in %q", ErrOutside, agentPath)
		}
		if strings.EqualFold(seg, ".git") {
			return "", "", fmt.Errorf("%w: .git is not reviewable", ErrOutside)
		}
	}
	rel = path.Clean(p)
	if rel == "." {
		return "", "", fmt.Errorf("%w: no file", ErrOutside)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}
	// Walk each existing component; none may be a symlink or escape.
	cur := realRoot
	for _, seg := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, seg)
		st, lerr := os.Lstat(cur)
		if errors.Is(lerr, os.ErrNotExist) {
			continue
		}
		if lerr != nil {
			return "", "", lerr
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("%w: %q crosses a symlink", ErrOutside, agentPath)
		}
	}
	return cur, rel, nil
}

// Kind classifies a proposed file change.
type Kind int

const (
	Modify Kind = iota
	Create
	Delete
	Rename
	Binary
	Oversized
)

func (k Kind) String() string {
	return [...]string{"modify", "create", "delete", "rename", "binary", "oversized"}[k]
}

// Reviewable reports whether per-hunk review applies. Delete is reviewable as
// a whole-file change; rename, binary and oversized are shown but never
// applied hunk by hunk.
func (k Kind) Reviewable() bool { return k == Modify || k == Create || k == Delete }

// Classify decides how a change is handled. renamedFrom is non-empty when the
// agent moved the file.
func Classify(baseExists, newExists bool, base, proposed []byte, renamedFrom string) Kind {
	switch {
	case renamedFrom != "":
		return Rename
	case len(base) > MaxReviewBytes || len(proposed) > MaxReviewBytes:
		return Oversized
	case !isText(base) || !isText(proposed):
		return Binary
	case !baseExists && newExists:
		return Create
	case baseExists && !newExists:
		return Delete
	}
	return Modify
}

func isText(b []byte) bool { return bytes.IndexByte(b, 0) < 0 && utf8.Valid(b) }

// FileState is one file's pre-agent state.
type FileState struct {
	Exists bool
	Hash   string
	Dirty  bool   // unsaved editor buffer
	Git    string // porcelain XY status; empty when clean
}

// Baseline is what the user had before the agent ran. Anything it records is
// attributable to the user, never the agent.
type Baseline struct {
	Files map[string]FileState
}

// UserOwned reports whether the user already had changes at rel.
func (b *Baseline) UserOwned(rel string) bool {
	s, ok := b.Files[rel]
	return ok && (s.Dirty || strings.TrimSpace(s.Git) != "")
}

// StagedByUser reports index changes that existed before the agent.
func (b *Baseline) StagedByUser(rel string) bool {
	s := b.Files[rel]
	return len(s.Git) >= 1 && s.Git[0] != ' ' && s.Git[0] != '?'
}

// CaptureBaseline records worktree hashes, git index/worktree status and the
// open dirty buffers (workspace-relative, slash paths) before an agent turn.
func CaptureBaseline(ctx context.Context, root string, dirty []string) (*Baseline, error) {
	b := &Baseline{Files: map[string]FileState{}}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "git", "-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all").Output()
	if err == nil {
		recs := strings.Split(string(out), "\x00")
		for i := 0; i < len(recs); i++ {
			r := recs[i]
			if len(r) < 4 {
				continue
			}
			xy, name := r[:2], r[3:]
			if xy[0] == 'R' || xy[0] == 'C' {
				i++ // original name follows
			}
			b.Files[name] = FileState{Git: xy}
		}
	}
	for _, d := range dirty {
		s := b.Files[d]
		s.Dirty = true
		b.Files[d] = s
	}
	for rel, s := range b.Files {
		abs, _, merr := MapPath(root, rel)
		if merr != nil {
			continue
		}
		if data, rerr := os.ReadFile(abs); rerr == nil {
			sum := sha256.Sum256(data)
			s.Exists, s.Hash = true, hex.EncodeToString(sum[:])
		}
		b.Files[rel] = s
	}
	return b, nil
}

// RecordFile adds or refreshes one file's hash (used for files the agent is
// about to touch that were clean).
func (b *Baseline) RecordFile(root, rel string) error {
	abs, _, err := MapPath(root, rel)
	if err != nil {
		return err
	}
	s := b.Files[rel]
	if data, rerr := os.ReadFile(abs); rerr == nil {
		sum := sha256.Sum256(data)
		s.Exists, s.Hash = true, hex.EncodeToString(sum[:])
	} else {
		s.Exists, s.Hash = false, ""
	}
	b.Files[rel] = s
	return nil
}
