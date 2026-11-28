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

// Adversarial fixtures for the workspace boundary and approval contract.

func prepareErrOrExec(t *testing.T, r *registry, tool string, args any) error {
	t.Helper()
	raw, _ := json.Marshal(args)
	p, err := r.Prepare(tool, raw)
	if err != nil {
		return err
	}
	_, err = p.Execute(context.Background(), nil)
	return err
}

func TestAdversarialReadPathsRejected(t *testing.T) {
	r, _ := newReg(t)
	for _, p := range []string{
		"../x", "a/../../x", "..", "../../etc/passwd", "/etc/passwd", `\etc\passwd`, `..\x`, `a\..\..\x`,
		"C:/Windows/win.ini", "C:\\Windows\\win.ini", "a\x00b", strings.Repeat("a/", 600),
	} {
		for _, tool := range []string{ToolReadFile, ToolListFiles} {
			if err := prepareErrOrExec(t, r, tool, map[string]any{"path": p}); err == nil {
				t.Errorf("%s accepted path %q", tool, p)
			}
		}
		if err := prepareErrOrExec(t, r, ToolSearchFiles, map[string]any{"pattern": "x", "path": p}); err == nil {
			t.Errorf("search_files accepted path %q", p)
		}
	}
}

func TestAdversarialSymlinkEscapeBlockedForReadTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	r, root := newReg(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dirlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "filelink")); err != nil {
		t.Fatal(err)
	}
	// Nested: a link inside a real subdirectory.
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "sub", "up")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"dirlink/secret.txt", "filelink", "sub/up/secret.txt"} {
		raw, _ := json.Marshal(map[string]any{"path": p})
		prep, err := r.Prepare(ToolReadFile, raw)
		if err != nil {
			t.Fatal(err)
		}
		out, err := prep.Execute(context.Background(), nil)
		if !errors.Is(err, ErrOutside) || strings.Contains(out.Text, "TOPSECRET") {
			t.Errorf("read %s: err=%v out=%q", p, err, out.Text)
		}
	}
	for _, p := range []string{"dirlink", "sub/up"} {
		if err := prepareErrOrExec(t, r, ToolListFiles, map[string]any{"path": p}); !errors.Is(err, ErrOutside) {
			t.Errorf("list %s: %v", p, err)
		}
	}
	// A recursive search from the root must not follow links out.
	raw, _ := json.Marshal(map[string]any{"pattern": "TOPSECRET"})
	prep, err := r.Prepare(ToolSearchFiles, raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := prep.Execute(context.Background(), nil)
	if err != nil || strings.Contains(out.Text, "TOPSECRET") {
		t.Errorf("search followed a symlink: err=%v out=%q", err, out.Text)
	}
}

func TestAdversarialSearchSkipsGitDirectory(t *testing.T) {
	r, root := newReg(t)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("GITSECRETTOKEN"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("GITSECRETTOKEN visible"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"pattern": "GITSECRETTOKEN"})
	prep, err := r.Prepare(ToolSearchFiles, raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := prep.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Text, ".git") || !strings.Contains(out.Text, "ok.txt") {
		t.Errorf("search output = %q", out.Text)
	}
}

func TestAdversarialWriteToolsRefuseEveryNonGrant(t *testing.T) {
	r, root, prop := newPropReg(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := map[string]map[string]any{
		ToolWriteFile: {"path": "a.txt", "content": "owned\n"},
		ToolEditFile:  {"path": "a.txt", "old_string": "one", "new_string": "owned"},
	}
	for tool, args := range calls {
		raw, _ := json.Marshal(args)
		p, err := r.Prepare(tool, raw)
		if err != nil {
			t.Fatal(err)
		}
		otherRaw, _ := json.Marshal(map[string]any{"path": "b.txt", "content": "x"})
		other, err := r.Prepare(ToolWriteFile, otherRaw)
		if err != nil {
			t.Fatal(err)
		}
		bctx := WithBinding(context.Background(), bind(root))
		forgedWrongRoot := *NewApproval(bind(root), p, OutcomeGranted)
		forgedWrongRoot.WorktreeRoot = prop
		forgedNoHash := *NewApproval(bind(root), p, OutcomeGranted)
		forgedNoHash.ArgsHash = ""
		cases := map[string]struct {
			ctx context.Context
			ap  *Approval
		}{
			"nil":          {bctx, nil},
			"denied":       {bctx, NewApproval(bind(root), p, OutcomeDenied)},
			"dismissed":    {bctx, NewApproval(bind(root), p, OutcomeDismissed)},
			"timeout":      {bctx, NewApproval(bind(root), p, OutcomeTimeout)},
			"canceled":     {bctx, NewApproval(bind(root), p, OutcomeCanceled)},
			"stale-args":   {bctx, NewApproval(bind(root), other, OutcomeGranted)},
			"no-binding":   {context.Background(), NewApproval(bind(root), p, OutcomeGranted)},
			"wrong-root":   {bctx, &forgedWrongRoot},
			"empty-hash":   {bctx, &forgedNoHash},
			"other-call":   {WithBinding(context.Background(), Binding{"s1", "t1", "c9", root}), NewApproval(bind(root), p, OutcomeGranted)},
			"hand-forged":  {bctx, &Approval{SessionID: "s1", TurnID: "t1", CallID: "c1", WorktreeRoot: root, ArgsHash: "guess", Granted: true}},
			"not-granted+": {bctx, &Approval{SessionID: "s1", TurnID: "t1", CallID: "c1", WorktreeRoot: root, ArgsHash: p.ArgsHash}},
		}
		for name, c := range cases {
			if _, err := p.Execute(c.ctx, c.ap); err == nil {
				t.Errorf("%s/%s: executed", tool, name)
			}
		}
	}
	// Nothing was staged and the checkout is untouched.
	if b, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(b) != "one\n" {
		t.Fatalf("checkout modified: %q", b)
	}
	var staged []string
	if err := filepath.WalkDir(prop, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			staged = append(staged, p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(staged) != 0 {
		t.Fatalf("denied calls staged files: %v", staged)
	}
}

func TestAdversarialProposalPathsRejected(t *testing.T) {
	r, root, _ := newPropReg(t)
	for _, p := range []string{
		"../x", "a/../../x", "/abs", `..\x`, "C:/x", ".git/hooks/pre-commit", "sub/.git/config", "a\x00b",
	} {
		raw, _ := json.Marshal(map[string]any{"path": p, "content": "x"})
		if pr, err := r.Prepare(ToolWriteFile, raw); err == nil {
			// Some spellings may be accepted lexically (case-folded .GIT on
			// case-sensitive systems); they must still not run unapproved.
			if _, xerr := pr.Execute(context.Background(), nil); xerr == nil {
				t.Errorf("write %q executed without approval", p)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "x")); err == nil {
		t.Fatal("traversal wrote outside the workspace")
	}
}
