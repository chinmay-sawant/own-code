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
}

// Errors from proposals.
var (
	ErrBaselineChanged = errors.New("agenttools: baseline changed since the proposal was made")
	ErrBadProposal     = errors.New("agenttools: invalid proposal")
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
	abs, err := filepath.Abs(targetRoot)
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return err
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
	return writeAtomic(target, []byte(p.NewContent))
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
