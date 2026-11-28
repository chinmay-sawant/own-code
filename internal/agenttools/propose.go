package agenttools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const maxProposalFile = 1 << 20 // largest baseline file a proposal may be built on

// Proposal is a staged change to one file. write_file and edit_file produce
// it; the user's checkout is only changed later, by ApplyProposal, after an
// explicit accept.
type Proposal struct {
	Tool           string `json:"tool"`
	Path           string `json:"path"` // slash-separated, worktree-relative
	BaselineExists bool   `json:"baseline_exists"`
	BaselineHash   string `json:"baseline_hash"` // sha256 hex of the baseline file; empty when it did not exist
	NewContent     string `json:"new_content"`
	NewHash        string `json:"new_hash"`
	Diff           string `json:"diff"`
	// Deleted marks a whole-file delete of Path. BaselineExists must be true;
	// NewContent is empty.
	Deleted bool `json:"deleted,omitempty"`
	// RenamedFrom marks a move: Path is the destination, RenamedFrom the
	// source. BaselineExists/BaselineHash then describe the source file, and
	// the destination must not exist when the proposal is applied.
	RenamedFrom string `json:"renamed_from,omitempty"`
}

// Errors from proposals.
var (
	ErrBaselineChanged = errors.New("agenttools: baseline changed since the proposal was made")
	ErrBadProposal     = errors.New("agenttools: invalid proposal")
	ErrDestExists      = errors.New("agenttools: rename destination already exists")
)

// JSON returns the persisted form of the proposal.
func (p Proposal) JSON() ([]byte, error) { return json.Marshal(p) }

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// safeTarget resolves rel under root without following a symlink at or above
// the target out of root. The final element may not exist yet. Symlinked final
// elements are refused outright so a write can never be redirected.
func safeTarget(root, rel string) (string, error) {
	target := filepath.Join(root, rel)
	// Resolve the deepest existing ancestor and recheck containment.
	probe := target
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if !within(root, real) {
				return "", fmt.Errorf("%w: %q resolves outside the worktree", ErrOutside, rel)
			}
			if probe == target && real != target {
				return "", fmt.Errorf("%w: %q is or crosses a symlink", ErrOutside, rel)
			}
			if probe != target {
				// Rebuild the path from the resolved ancestor.
				suffix, _ := filepath.Rel(probe, target)
				target = filepath.Join(real, suffix)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe || !within(root, parent) {
			return "", fmt.Errorf("%w: %q", ErrOutside, rel)
		}
		probe = parent
	}
	if !within(root, target) {
		return "", fmt.Errorf("%w: %q", ErrOutside, rel)
	}
	if st, err := os.Lstat(target); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: %q is a symlink", ErrOutside, rel)
		}
		if !st.Mode().IsRegular() {
			return "", fmt.Errorf("%w: %q is not a regular file", ErrInvalidArgs, rel)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return target, nil
}

// readBaseline returns the file content and whether it exists.
func readBaseline(path string) ([]byte, bool, error) {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxProposalFile {
		return nil, false, fmt.Errorf("%w: not a regular file within %d bytes", ErrInvalidArgs, maxProposalFile)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// writeAtomic writes data to path via a temp file in the same directory.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".agent-tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(name)
		return errors.Join(werr, cerr)
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// stage builds a proposal for rel with the given new content, writes the new
// content into the proposal worktree only, and returns the proposal. base, if
// non-nil, is the content edit tools start from (the previously staged file).
func (r *registry) stage(ctx context.Context, tool, rel string, build func(current []byte, exists bool) (string, error)) (Output, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}
	// Baseline: the file as the user's worktree has it now.
	src, err := safeTarget(r.root, rel)
	if err != nil {
		return Output{}, err
	}
	baseline, exists, err := readBaseline(src)
	if err != nil {
		return Output{}, err
	}
	// Starting point: an earlier staged version of this file, else the baseline.
	dst, err := safeTarget(r.proposalRoot, rel)
	if err != nil {
		return Output{}, err
	}
	current, curExists := baseline, exists
	if staged, ok, err := readBaseline(dst); err != nil {
		return Output{}, err
	} else if ok {
		current, curExists = staged, true
	}
	if bytes.IndexByte(current, 0) >= 0 || !utf8.Valid(current) {
		return Output{}, fmt.Errorf("%w: binary or non-UTF-8 file", ErrInvalidArgs)
	}
	newContent, err := build(current, curExists)
	if err != nil {
		return Output{}, err
	}
	if len(newContent) > maxProposalFile {
		return Output{}, fmt.Errorf("%w: result exceeds %d bytes", ErrInvalidArgs, maxProposalFile)
	}
	p := Proposal{
		Tool: tool, Path: filepath.ToSlash(rel), BaselineExists: exists,
		NewContent: newContent, NewHash: hashBytes([]byte(newContent)),
		Diff: unifiedDiff(filepath.ToSlash(rel), string(baseline), newContent, exists),
	}
	if exists {
		p.BaselineHash = hashBytes(baseline)
	}
	// Recheck containment immediately before the write.
	if dst, err = safeTarget(r.proposalRoot, rel); err != nil {
		return Output{}, err
	}
	if err := writeAtomic(dst, []byte(newContent)); err != nil {
		return Output{}, err
	}
	if _, err := safeTarget(r.proposalRoot, rel); err != nil { // post-write recheck
		return Output{}, err
	}
	if r.sink != nil {
		if err := r.sink(ctx, p); err != nil {
			return Output{}, fmt.Errorf("agenttools: persist proposal: %w", err)
		}
	}
	text := fmt.Sprintf("Proposal staged for %s (not applied to your checkout; awaiting accept/reject review).\n%s", p.Path, p.Diff)
	text, trunc := truncateText(text, maxStreamBytes)
	return Output{Text: text, Truncated: trunc, Duration: time.Since(start), Proposal: &p}, nil
}

func truncateText(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n[diff truncated; the full change is in the proposal]", true
}

// unifiedDiff is a compact single-hunk diff: common prefix and suffix lines are
// trimmed, the middle is shown as removals and additions.
func unifiedDiff(path, old, new string, existed bool) string {
	a, b := splitLines(old), splitLines(new)
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	ja, jb := len(a), len(b)
	for ja > i && jb > i && a[ja-1] == b[jb-1] {
		ja--
		jb--
	}
	var sb strings.Builder
	from := "a/" + path
	if !existed {
		from = "/dev/null"
	}
	fmt.Fprintf(&sb, "--- %s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n", from, path, i+1, ja-i, i+1, jb-i)
	for _, l := range a[i:ja] {
		sb.WriteString("-" + l + "\n")
	}
	for _, l := range b[i:jb] {
		sb.WriteString("+" + l + "\n")
	}
	return sb.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// ApplyProposal writes an accepted proposal into targetRoot (the user's
// checkout). It refuses with ErrBaselineChanged unless the target file still
// matches the baseline the proposal was built on, and it rechecks containment
// and symlinks first. It does not know about dirty editor buffers or staged
// git changes: the caller must reject the apply if the file has unsaved edits.
// A narrow window remains between the final check and the rename; callers
// should serialize applies per worktree.
func ApplyProposal(p Proposal, targetRoot string) error {
	if p.Path == "" || p.NewHash != hashBytes([]byte(p.NewContent)) {
		return fmt.Errorf("%w: content does not match its hash", ErrBadProposal)
	}
	if p.BaselineExists != (p.BaselineHash != "") {
		return fmt.Errorf("%w: inconsistent baseline", ErrBadProposal)
	}
	rel, err := cleanRel(p.Path)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("%w: no file path", ErrBadProposal)
	}
	if p.Deleted && (p.RenamedFrom != "" || !p.BaselineExists || p.NewContent != "") {
		return fmt.Errorf("%w: inconsistent delete", ErrBadProposal)
	}
	if p.RenamedFrom != "" && !p.BaselineExists {
		return fmt.Errorf("%w: rename without a source baseline", ErrBadProposal)
	}
	abs, err := filepath.Abs(targetRoot)
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return err
	}
	if p.RenamedFrom != "" {
		return applyRename(p, root, rel)
	}
	target, err := safeTarget(root, rel)
	if err != nil {
		return err
	}
	cur, exists, err := readBaseline(target)
	if err != nil {
		return err
	}
	if exists != p.BaselineExists || (exists && hashBytes(cur) != p.BaselineHash) {
		return ErrBaselineChanged
	}
	if target, err = safeTarget(root, rel); err != nil { // recheck right before writing
		return err
	}
	if p.Deleted {
		return os.Remove(target)
	}
	return writeAtomic(target, []byte(p.NewContent))
}

// applyRename moves RenamedFrom to rel (the destination) only if the source
// still matches its baseline and the destination does not exist.
func applyRename(p Proposal, root, rel string) error {
	srcRel, err := cleanProposalPath(p.RenamedFrom)
	if err != nil {
		return err
	}
	if srcRel == rel {
		return fmt.Errorf("%w: source and destination are the same", ErrBadProposal)
	}
	check := func() (src, dst string, err error) {
		if src, err = safeTarget(root, srcRel); err != nil {
			return
		}
		if dst, err = safeTarget(root, rel); err != nil {
			return
		}
		cur, exists, rerr := readBaseline(src)
		if rerr != nil {
			return "", "", rerr
		}
		if !exists || hashBytes(cur) != p.BaselineHash {
			return "", "", ErrBaselineChanged
		}
		if _, lerr := os.Lstat(dst); lerr == nil {
			return "", "", ErrDestExists
		} else if !errors.Is(lerr, os.ErrNotExist) {
			return "", "", lerr
		}
		return src, dst, nil
	}
	if _, _, err := check(); err != nil {
		return err
	}
	src, dst, err := check() // recheck right before writing
	if err != nil {
		return err
	}
	if err := writeAtomic(dst, []byte(p.NewContent)); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func writeFileTool() tool {
	return tool{
		spec: Spec{
			Name:          ToolWriteFile,
			Description:   "Propose creating or replacing a UTF-8 text file (relative path). The change is staged for user review, not applied.",
			Schema:        schema(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
			NeedsApproval: true,
		},
		prepare: func(r *registry, raw json.RawMessage) (any, string, func(context.Context) (Output, error), error) {
			var a writeArgs
			if err := decodeStrict(raw, &a); err != nil {
				return nil, "", nil, err
			}
			rel, err := cleanProposalPath(a.Path)
			if err != nil {
				return nil, "", nil, err
			}
			if !utf8.ValidString(a.Content) || strings.ContainsRune(a.Content, 0) {
				return nil, "", nil, fmt.Errorf("%w: content must be UTF-8 text", ErrInvalidArgs)
			}
			a.Path = filepath.ToSlash(rel)
			run := func(ctx context.Context) (Output, error) {
				return r.stage(ctx, ToolWriteFile, rel, func([]byte, bool) (string, error) { return a.Content, nil })
			}
			return a, fmt.Sprintf("Propose writing %s (%d bytes) in the proposal worktree; your checkout is not changed until you accept.", a.Path, len(a.Content)), run, nil
		},
	}
}

type editArgs struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

func editFileTool() tool {
	return tool{
		spec: Spec{
			Name:          ToolEditFile,
			Description:   "Propose replacing one exact occurrence of old_string with new_string in an existing file (relative path). Staged for user review, not applied.",
			Schema:        schema(`{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}},"required":["path","old_string","new_string"],"additionalProperties":false}`),
			NeedsApproval: true,
		},
		prepare: func(r *registry, raw json.RawMessage) (any, string, func(context.Context) (Output, error), error) {
			var a editArgs
			if err := decodeStrict(raw, &a); err != nil {
				return nil, "", nil, err
			}
			rel, err := cleanProposalPath(a.Path)
			if err != nil {
				return nil, "", nil, err
			}
			if a.OldString == "" || a.OldString == a.NewString || !utf8.ValidString(a.NewString) || strings.ContainsRune(a.NewString, 0) {
				return nil, "", nil, fmt.Errorf("%w: old_string must be non-empty and differ from new_string", ErrInvalidArgs)
			}
			a.Path = filepath.ToSlash(rel)
			run := func(ctx context.Context) (Output, error) {
				return r.stage(ctx, ToolEditFile, rel, func(cur []byte, exists bool) (string, error) {
					if !exists {
						return "", fmt.Errorf("%w: %s does not exist", ErrInvalidArgs, a.Path)
					}
					s := string(cur)
					switch n := strings.Count(s, a.OldString); {
					case n == 0:
						return "", fmt.Errorf("%w: old_string not found", ErrInvalidArgs)
					case n > 1:
						return "", fmt.Errorf("%w: old_string matches %d places; add context to make it unique", ErrInvalidArgs, n)
					}
					return strings.Replace(s, a.OldString, a.NewString, 1), nil
				})
			}
			return a, fmt.Sprintf("Propose editing %s in the proposal worktree; your checkout is not changed until you accept.", a.Path), run, nil
		},
	}
}

func cleanProposalPath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("%w: path required", ErrInvalidArgs)
	}
	rel, err := cleanRel(p)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "", fmt.Errorf("%w: path must name a file", ErrInvalidArgs)
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == ".git" {
			return "", fmt.Errorf("%w: .git is not writable", ErrOutside)
		}
	}
	return rel, nil
}

type deleteArgs struct {
	Path string `json:"path"`
}

func deleteFileTool() tool {
	return tool{
		spec: Spec{
			Name:          ToolDeleteFile,
			Description:   "Propose deleting an existing file (relative path). Staged for user review, not applied.",
			Schema:        schema(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
			NeedsApproval: true,
		},
		prepare: func(r *registry, raw json.RawMessage) (any, string, func(context.Context) (Output, error), error) {
			var a deleteArgs
			if err := decodeStrict(raw, &a); err != nil {
				return nil, "", nil, err
			}
			rel, err := cleanProposalPath(a.Path)
			if err != nil {
				return nil, "", nil, err
			}
			a.Path = filepath.ToSlash(rel)
			run := func(ctx context.Context) (Output, error) { return r.stageDelete(ctx, rel) }
			return a, fmt.Sprintf("Propose deleting %s; your checkout is not changed until you accept.", a.Path), run, nil
		},
	}
}

type moveArgs struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func moveFileTool() tool {
	return tool{
		spec: Spec{
			Name:          ToolMoveFile,
			Description:   "Propose moving or renaming an existing UTF-8 text file to a new, non-existent path (relative paths). Staged for user review, not applied.",
			Schema:        schema(`{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"}},"required":["from","to"],"additionalProperties":false}`),
			NeedsApproval: true,
		},
		prepare: func(r *registry, raw json.RawMessage) (any, string, func(context.Context) (Output, error), error) {
			var a moveArgs
			if err := decodeStrict(raw, &a); err != nil {
				return nil, "", nil, err
			}
			from, err := cleanProposalPath(a.From)
			if err != nil {
				return nil, "", nil, err
			}
			to, err := cleanProposalPath(a.To)
			if err != nil {
				return nil, "", nil, err
			}
			if from == to {
				return nil, "", nil, fmt.Errorf("%w: from and to are the same", ErrInvalidArgs)
			}
			a.From, a.To = filepath.ToSlash(from), filepath.ToSlash(to)
			run := func(ctx context.Context) (Output, error) { return r.stageMove(ctx, from, to) }
			return a, fmt.Sprintf("Propose moving %s to %s; your checkout is not changed until you accept.", a.From, a.To), run, nil
		},
	}
}

// removeStaged deletes the staged copy of rel in the proposal root, if any.
func (r *registry) removeStaged(rel string) error {
	dst, err := safeTarget(r.proposalRoot, rel)
	if err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (r *registry) finishProposal(ctx context.Context, start time.Time, p Proposal, what string) (Output, error) {
	if r.sink != nil {
		if err := r.sink(ctx, p); err != nil {
			return Output{}, fmt.Errorf("agenttools: persist proposal: %w", err)
		}
	}
	text := fmt.Sprintf("%s staged for %s (not applied to your checkout; awaiting accept/reject review).\n%s", what, p.Path, p.Diff)
	text, trunc := truncateText(text, maxStreamBytes)
	return Output{Text: text, Truncated: trunc, Duration: time.Since(start), Proposal: &p}, nil
}

func (r *registry) stageDelete(ctx context.Context, rel string) (Output, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}
	src, err := safeTarget(r.root, rel)
	if err != nil {
		return Output{}, err
	}
	baseline, exists, err := readBaseline(src)
	if err != nil {
		return Output{}, err
	}
	if !exists {
		return Output{}, fmt.Errorf("%w: %s does not exist", ErrInvalidArgs, filepath.ToSlash(rel))
	}
	slash := filepath.ToSlash(rel)
	diff := fmt.Sprintf("Delete %s (binary or non-UTF-8, %d bytes)\n", slash, len(baseline))
	if bytes.IndexByte(baseline, 0) < 0 && utf8.Valid(baseline) {
		diff = unifiedDiff(slash, string(baseline), "", true)
	}
	p := Proposal{
		Tool: ToolDeleteFile, Path: slash, BaselineExists: true, BaselineHash: hashBytes(baseline),
		NewHash: hashBytes(nil), Diff: diff, Deleted: true,
	}
	if err := r.removeStaged(rel); err != nil { // proposal root only
		return Output{}, err
	}
	return r.finishProposal(ctx, start, p, "Delete proposal")
}

func (r *registry) stageMove(ctx context.Context, from, to string) (Output, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}
	src, err := safeTarget(r.root, from)
	if err != nil {
		return Output{}, err
	}
	baseline, exists, err := readBaseline(src)
	if err != nil {
		return Output{}, err
	}
	if !exists {
		return Output{}, fmt.Errorf("%w: %s does not exist", ErrInvalidArgs, filepath.ToSlash(from))
	}
	if bytes.IndexByte(baseline, 0) >= 0 || !utf8.Valid(baseline) {
		return Output{}, fmt.Errorf("%w: binary or non-UTF-8 file", ErrInvalidArgs)
	}
	dstReal, err := safeTarget(r.root, to)
	if err != nil {
		return Output{}, err
	}
	if _, err := os.Lstat(dstReal); err == nil {
		return Output{}, fmt.Errorf("%w: %s", ErrDestExists, filepath.ToSlash(to))
	} else if !errors.Is(err, os.ErrNotExist) {
		return Output{}, err
	}
	content := baseline
	if staged, ok, err := readBaseline(mustStaged(r, from)); err == nil && ok {
		content = staged
	}
	fromS, toS := filepath.ToSlash(from), filepath.ToSlash(to)
	diff := fmt.Sprintf("rename from %s\nrename to %s\n", fromS, toS)
	if string(content) != string(baseline) {
		diff += unifiedDiff(toS, string(baseline), string(content), true)
	}
	p := Proposal{
		Tool: ToolMoveFile, Path: toS, RenamedFrom: fromS, BaselineExists: true, BaselineHash: hashBytes(baseline),
		NewContent: string(content), NewHash: hashBytes(content), Diff: diff,
	}
	stagedDst, err := safeTarget(r.proposalRoot, to)
	if err != nil {
		return Output{}, err
	}
	if err := writeAtomic(stagedDst, content); err != nil {
		return Output{}, err
	}
	if _, err := safeTarget(r.proposalRoot, to); err != nil {
		return Output{}, err
	}
	if err := r.removeStaged(from); err != nil {
		return Output{}, err
	}
	return r.finishProposal(ctx, start, p, "Move proposal")
}

// mustStaged returns the staged path for rel, or a path that does not exist
// when containment fails (the caller then falls back to the baseline).
func mustStaged(r *registry, rel string) string {
	p, err := safeTarget(r.proposalRoot, rel)
	if err != nil {
		return filepath.Join(r.proposalRoot, ".agent-none")
	}
	return p
}
