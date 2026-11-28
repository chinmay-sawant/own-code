package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newPropReg(t *testing.T, opts ...Option) (r *registry, root, prop string) {
	t.Helper()
	root, prop = t.TempDir(), t.TempDir()
	r, err := newRegistry(root, append([]Option{WithProposalRoot(prop)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return r, r.root, r.proposalRoot
}

func runProposal(t *testing.T, r *registry, tool string, args map[string]any) (Output, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	p, err := r.Prepare(tool, raw)
	if err != nil {
		return Output{}, err
	}
	if !p.NeedsApproval {
		t.Fatal("write tools must need approval")
	}
	b := bind(r.root)
	return p.Execute(WithBinding(context.Background(), b), NewApproval(b, p, OutcomeGranted))
}

func TestWriteToolsOnlyWithProposalRoot(t *testing.T) {
	r, _ := newReg(t)
	if _, err := r.Prepare(ToolWriteFile, json.RawMessage(`{"path":"a","content":"b"}`)); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("err = %v", err)
	}
	if _, err := newRegistry(t.TempDir(), WithProposalRoot("")); err == nil {
		t.Fatal("empty proposal root accepted")
	}
	root := t.TempDir()
	if _, err := newRegistry(root, WithProposalRoot(root)); err == nil {
		t.Fatal("proposal root == root accepted")
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := newRegistry(root, WithProposalRoot(sub)); err == nil {
		t.Fatal("proposal root inside root accepted")
	}
}

func TestWriteFileStagesOnlyInProposalRoot(t *testing.T) {
	var sunk []Proposal
	r, root, prop := newPropReg(t, WithProposalSink(func(_ context.Context, p Proposal) error { sunk = append(sunk, p); return nil }))
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runProposal(t, r, ToolWriteFile, map[string]any{"path": "a.txt", "content": "one\nthree\n"})
	if err != nil || out.Proposal == nil {
		t.Fatalf("%v %+v", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(b) != "one\ntwo\n" {
		t.Fatalf("user checkout modified: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(prop, "a.txt")); string(b) != "one\nthree\n" {
		t.Fatalf("proposal file = %q", b)
	}
	p := out.Proposal
	if p.Path != "a.txt" || !p.BaselineExists || p.BaselineHash != hashBytes([]byte("one\ntwo\n")) || p.NewContent != "one\nthree\n" ||
		!strings.Contains(p.Diff, "-two") || !strings.Contains(p.Diff, "+three") || len(sunk) != 1 || sunk[0].NewHash != p.NewHash {
		t.Fatalf("proposal = %+v sunk=%d", p, len(sunk))
	}
	if !strings.Contains(out.Text, "not applied") {
		t.Fatalf("text = %q", out.Text)
	}
	// New file: no baseline.
	out, err = runProposal(t, r, ToolWriteFile, map[string]any{"path": "dir/new.txt", "content": "x"})
	if err != nil || out.Proposal.BaselineExists || out.Proposal.BaselineHash != "" {
		t.Fatalf("%v %+v", err, out.Proposal)
	}
	if _, err := os.Stat(filepath.Join(root, "dir")); !os.IsNotExist(err) {
		t.Fatal("directory created in user checkout")
	}
}

func TestEditFileAndCumulativeEdits(t *testing.T) {
	r, root, _ := newPropReg(t)
	if err := os.WriteFile(filepath.Join(root, "f.go"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runProposal(t, r, ToolEditFile, map[string]any{"path": "f.go", "old_string": "b", "new_string": "B"})
	if err != nil || out.Proposal.NewContent != "a\nB\nc\n" {
		t.Fatalf("%v %+v", err, out)
	}
	// Second edit builds on the staged file but keeps the original baseline.
	out2, err := runProposal(t, r, ToolEditFile, map[string]any{"path": "f.go", "old_string": "c", "new_string": "C"})
	if err != nil || out2.Proposal.NewContent != "a\nB\nC\n" || out2.Proposal.BaselineHash != out.Proposal.BaselineHash {
		t.Fatalf("%v %+v", err, out2)
	}
	for name, args := range map[string]map[string]any{
		"missing":   {"path": "f.go", "old_string": "zzz", "new_string": "y"},
		"ambiguous": {"path": "g.go", "old_string": "x", "new_string": "y"},
		"nofile":    {"path": "nope.go", "old_string": "x", "new_string": "y"},
	} {
		if err := os.WriteFile(filepath.Join(root, "g.go"), []byte("x x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := runProposal(t, r, ToolEditFile, args); !errors.Is(err, ErrInvalidArgs) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestProposalPathValidation(t *testing.T) {
	r, _, _ := newPropReg(t)
	for _, args := range []string{
		`{"path":"/etc/x","content":"a"}`, `{"path":"../x","content":"a"}`, `{"path":"","content":"a"}`,
		`{"path":".","content":"a"}`, `{"path":".git/config","content":"a"}`, `{"path":"a","content":"a","extra":1}`,
		`{"path":"a","content":"\u0000"}`,
	} {
		if p, err := r.Prepare(ToolWriteFile, json.RawMessage(args)); err == nil || p.Execute != nil {
			t.Errorf("accepted %s", args)
		}
	}
}

func TestWriteRefusesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	r, root, prop := newPropReg(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Symlinked directory in the proposal root.
	if err := os.Symlink(outside, filepath.Join(prop, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := runProposal(t, r, ToolWriteFile, map[string]any{"path": "link/x", "content": "a"}); !errors.Is(err, ErrOutside) {
		t.Fatalf("dir symlink err = %v", err)
	}
	// Symlinked file in the proposal root.
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(prop, "f")); err != nil {
		t.Fatal(err)
	}
	if _, err := runProposal(t, r, ToolWriteFile, map[string]any{"path": "f", "content": "a"}); !errors.Is(err, ErrOutside) {
		t.Fatalf("file symlink err = %v", err)
	}
	// Symlink in the user's checkout pointing outside: baseline read refused.
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "g")); err != nil {
		t.Fatal(err)
	}
	if _, err := runProposal(t, r, ToolWriteFile, map[string]any{"path": "g", "content": "a"}); !errors.Is(err, ErrOutside) {
		t.Fatalf("baseline symlink err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "secret")); string(b) != "s" {
		t.Fatal("outside file modified")
	}
}

func TestApplyProposal(t *testing.T) {
	r, root, _ := newPropReg(t)
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	out, err := runProposal(t, r, ToolWriteFile, map[string]any{"path": "a.txt", "content": "new"})
	if err != nil {
		t.Fatal(err)
	}
	// Baseline changed: refuse and leave the file alone.
	if err := os.WriteFile(file, []byte("user edit"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := ApplyProposal(*out.Proposal, root); !errors.Is(err, ErrBaselineChanged) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(file); string(b) != "user edit" {
		t.Fatalf("file = %q", b)
	}
	// Unchanged baseline: applies, keeps mode.
	if err := os.WriteFile(file, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := ApplyProposal(*out.Proposal, root); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != "new" {
		t.Fatalf("file = %q", b)
	}
	if st, _ := os.Stat(file); st.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", st.Mode())
	}
	// Applying again fails: the baseline is now the new content.
	if err := ApplyProposal(*out.Proposal, root); !errors.Is(err, ErrBaselineChanged) {
		t.Fatalf("replay err = %v", err)
	}
	// New-file proposal refuses if the file appeared meanwhile.
	out, _ = runProposal(t, r, ToolWriteFile, map[string]any{"path": "n.txt", "content": "n"})
	if err := os.WriteFile(filepath.Join(root, "n.txt"), []byte("appeared"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyProposal(*out.Proposal, root); !errors.Is(err, ErrBaselineChanged) {
		t.Fatalf("new-file err = %v", err)
	}
	if err := os.Remove(filepath.Join(root, "n.txt")); err != nil {
		t.Fatal(err)
	}
	if err := ApplyProposal(*out.Proposal, root); err != nil {
		t.Fatal(err)
	}
	// Tampered / escaping proposals.
	bad := *out.Proposal
	bad.NewContent = "tampered"
	if err := ApplyProposal(bad, root); !errors.Is(err, ErrBadProposal) {
		t.Fatalf("tamper err = %v", err)
	}
	esc := *out.Proposal
	esc.Path = "../x"
	if err := ApplyProposal(esc, root); !errors.Is(err, ErrOutside) {
		t.Fatalf("escape err = %v", err)
	}
}

func TestApplyProposalRefusesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "d")); err != nil {
		t.Fatal(err)
	}
	p := Proposal{Tool: ToolWriteFile, Path: "d/x", NewContent: "a", NewHash: hashBytes([]byte("a"))}
	if err := ApplyProposal(p, root); !errors.Is(err, ErrOutside) {
		t.Fatalf("err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "t"), []byte("t"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "t"), filepath.Join(root, "l")); err != nil {
		t.Fatal(err)
	}
	p.Path = "l"
	if err := ApplyProposal(p, root); !errors.Is(err, ErrOutside) {
		t.Fatalf("file link err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "x")); !os.IsNotExist(err) {
		t.Fatal("wrote through symlink")
	}
}

func TestWriteRequiresApprovalAndSinkFailure(t *testing.T) {
	r, _, _ := newPropReg(t, WithProposalSink(func(context.Context, Proposal) error { return errors.New("db down") }))
	raw := json.RawMessage(`{"path":"a","content":"b"}`)
	p, err := r.Prepare(ToolWriteFile, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Execute(context.Background(), nil); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("err = %v", err)
	}
	b := bind(r.root)
	if _, err := p.Execute(WithBinding(context.Background(), b), NewApproval(b, p, OutcomeDenied)); !errors.Is(err, ErrApprovalDenied) {
		t.Fatalf("err = %v", err)
	}
	p, _ = r.Prepare(ToolWriteFile, raw)
	if _, err := p.Execute(WithBinding(context.Background(), b), NewApproval(b, p, OutcomeGranted)); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("sink err = %v", err)
	}
}
