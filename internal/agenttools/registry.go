package agenttools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	ToolReadFile    = "read_file"
	ToolListFiles   = "list_files"
	ToolSearchFiles = "search_files"
	ToolRunShell    = "run_shell"

	// MaxArgsBytes bounds raw tool arguments before any parsing.
	MaxArgsBytes = 16 * 1024
)

// Spec advertises a tool to the model.
type Spec struct {
	Name          string
	Description   string
	Schema        json.RawMessage
	NeedsApproval bool
}

// Registry validates calls and prepares them for execution.
type Registry interface {
	Specs() []Spec
	Prepare(name string, rawArgs json.RawMessage) (Prepared, error)
}

// Output is a tool result.
type Output struct {
	Text      string
	Truncated bool
	ExitCode  *int
	Duration  time.Duration
}

// Prepared is a fully validated call. Execute is the only way to run it.
type Prepared struct {
	Name          string
	Args          json.RawMessage // canonical
	ArgsHash      string
	NeedsApproval bool
	Summary       string
	Execute       func(ctx context.Context, approval *Approval) (Output, error)
}

// Validation errors.
var (
	ErrUnknownTool = errors.New("agenttools: unknown tool")
	ErrInvalidArgs = errors.New("agenttools: invalid arguments")
	ErrOutside     = errors.New("agenttools: path outside workspace")
)

// HashArgs is the canonical args hash for a tool call.
func HashArgs(name string, canonical []byte) string {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte{0})
	h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil))
}

type tool struct {
	spec Spec
	// prepare decodes and validates args, returning the canonical value, a
	// summary and the executor body.
	prepare func(r *registry, raw json.RawMessage) (canonical any, summary string, run func(ctx context.Context) (Output, error), err error)
}

type registry struct {
	root  string // symlink-resolved absolute worktree root, captured once
	tools map[string]tool
}

// NewRegistry captures the worktree root (absolute, symlinks resolved) and
// returns the default tool set: read_file, list_files, search_files, run_shell.
func NewRegistry(worktreeRoot string) (Registry, error) {
	return newRegistry(worktreeRoot)
}

func newRegistry(worktreeRoot string) (*registry, error) {
	if worktreeRoot == "" {
		return nil, errors.New("agenttools: empty worktree root")
	}
	abs, err := filepath.Abs(worktreeRoot)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("agenttools: worktree root: %w", err)
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return nil, errors.New("agenttools: worktree root is not a directory")
	}
	r := &registry{root: real, tools: map[string]tool{}}
	for _, t := range []tool{readFileTool(), listFilesTool(), searchFilesTool(), runShellTool()} {
		r.tools[t.spec.Name] = t
	}
	return r, nil
}

func (r *registry) Specs() []Spec {
	out := make([]Spec, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *registry) Prepare(name string, rawArgs json.RawMessage) (Prepared, error) {
	t, ok := r.tools[name]
	if !ok {
		return Prepared{}, fmt.Errorf("%w: %q", ErrUnknownTool, name)
	}
	if len(rawArgs) > MaxArgsBytes {
		return Prepared{}, fmt.Errorf("%w: arguments exceed %d bytes", ErrInvalidArgs, MaxArgsBytes)
	}
	if len(bytes.TrimSpace(rawArgs)) == 0 {
		rawArgs = json.RawMessage("{}")
	}
	if !json.Valid(rawArgs) {
		return Prepared{}, fmt.Errorf("%w: not valid JSON", ErrInvalidArgs)
	}
	canon, summary, run, err := t.prepare(r, rawArgs)
	if err != nil {
		return Prepared{}, err
	}
	cb, err := json.Marshal(canon)
	if err != nil {
		return Prepared{}, fmt.Errorf("%w: %v", ErrInvalidArgs, err)
	}
	p := Prepared{
		Name:          name,
		Args:          cb,
		ArgsHash:      HashArgs(name, cb),
		NeedsApproval: t.spec.NeedsApproval,
		Summary:       summary,
	}
	guard := &onceGuard{}
	p.Execute = func(ctx context.Context, approval *Approval) (Output, error) {
		if err := ctx.Err(); err != nil {
			return Output{}, err
		}
		if p.NeedsApproval {
			if err := approval.verify(ctx, p.ArgsHash, r.root); err != nil {
				return Output{}, err
			}
			if !guard.take() {
				return Output{}, ErrApprovalUsed
			}
		}
		return run(ctx)
	}
	return p, nil
}

// decodeStrict decodes raw into v, rejecting unknown fields and trailing data.
func decodeStrict(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgs, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing data", ErrInvalidArgs)
	}
	return nil
}
