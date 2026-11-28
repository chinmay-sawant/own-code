package agenttools

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeF(t *testing.T, p, c string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteProposalStagesWithoutTouchingCheckoutThenApplies(t *testing.T) {
	r, root, prop := newPropReg(t)
	writeF(t, filepath.Join(root, "a.txt"), "hi\n")
	writeF(t, filepath.Join(prop, "a.txt"), "staged\n")
	out, err := runProposal(t, r, ToolDeleteFile, map[string]any{"path": "a.txt"})
	if err != nil || out.Proposal == nil || !out.Proposal.Deleted {
		t.Fatalf("%v %+v", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(b) != "hi\n" {
		t.Fatal("checkout touched before accept")
	}
	if _, err := os.Stat(filepath.Join(prop, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("staged copy not removed")
	}
	if err := ApplyProposal(*out.Proposal, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("file not deleted on apply")
	}
}

func TestDeleteBaselineChangedAndMissingRefused(t *testing.T) {
	r, root, _ := newPropReg(t)
	writeF(t, filepath.Join(root, "a.txt"), "hi\n")
	out, err := runProposal(t, r, ToolDeleteFile, map[string]any{"path": "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	writeF(t, filepath.Join(root, "a.txt"), "changed\n")
	if err := ApplyProposal(*out.Proposal, root); !errors.Is(err, ErrBaselineChanged) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err != nil {
		t.Fatal("file must survive a refused delete")
	}
	if _, err := runProposal(t, r, ToolDeleteFile, map[string]any{"path": "nope.txt"}); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("missing file err = %v", err)
	}
	if _, err := runProposal(t, r, ToolDeleteFile, map[string]any{"path": "../x"}); err == nil {
		t.Fatal("traversal must be refused")
	}
}

func TestDeleteSymlinkAndOutsideRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	r, root, _ := newPropReg(t)
	outside := t.TempDir()
	writeF(t, filepath.Join(outside, "secret"), "s\n")
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := runProposal(t, r, ToolDeleteFile, map[string]any{"path": "link"}); !errors.Is(err, ErrOutside) {
		t.Fatalf("symlink delete err = %v", err)
	}
	// A forged proposal is rechecked at apply time too.
	writeF(t, filepath.Join(root, "real.txt"), "x\n")
	out, err := runProposal(t, r, ToolDeleteFile, map[string]any{"path": "real.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "real.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "real.txt")); err != nil {
		t.Fatal(err)
	}
	if err := ApplyProposal(*out.Proposal, root); !errors.Is(err, ErrOutside) {
		t.Fatalf("apply onto symlink err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Fatal("outside file must survive")
	}
	forged := *out.Proposal
	forged.Path = "../escape"
	if err := ApplyProposal(forged, root); err == nil {
		t.Fatal("out-of-root path must be refused")
	}
}

func TestMoveProposalAppliesOnlyWhenBaselineMatches(t *testing.T) {
	r, root, prop := newPropReg(t)
	writeF(t, filepath.Join(root, "a.txt"), "hi\n")
	out, err := runProposal(t, r, ToolMoveFile, map[string]any{"from": "a.txt", "to": "sub/b.txt"})
	if err != nil || out.Proposal.RenamedFrom != "a.txt" || out.Proposal.Path != "sub/b.txt" {
		t.Fatalf("%v %+v", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "sub")); !os.IsNotExist(err) {
		t.Fatal("checkout touched before accept")
	}
	if b, _ := os.ReadFile(filepath.Join(prop, "sub/b.txt")); string(b) != "hi\n" {
		t.Fatalf("staged dest = %q", b)
	}
	// Source edited since the proposal: refused, nothing moved.
	writeF(t, filepath.Join(root, "a.txt"), "edited\n")
	if err := ApplyProposal(*out.Proposal, root); !errors.Is(err, ErrBaselineChanged) {
		t.Fatalf("err = %v", err)
	}
	writeF(t, filepath.Join(root, "a.txt"), "hi\n")
	if err := ApplyProposal(*out.Proposal, root); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "sub/b.txt")); string(b) != "hi\n" {
		t.Fatal("destination missing")
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("source not removed")
	}
}

func TestMoveCollisionRefused(t *testing.T) {
	r, root, _ := newPropReg(t)
	writeF(t, filepath.Join(root, "a.txt"), "a\n")
	writeF(t, filepath.Join(root, "b.txt"), "b\n")
	if _, err := runProposal(t, r, ToolMoveFile, map[string]any{"from": "a.txt", "to": "b.txt"}); !errors.Is(err, ErrDestExists) {
		t.Fatalf("stage err = %v", err)
	}
	// Destination appears after staging: apply refuses and keeps both files.
	out, err := runProposal(t, r, ToolMoveFile, map[string]any{"from": "a.txt", "to": "c.txt"})
	if err != nil {
		t.Fatal(err)
	}
	writeF(t, filepath.Join(root, "c.txt"), "mine\n")
	if err := ApplyProposal(*out.Proposal, root); !errors.Is(err, ErrDestExists) {
		t.Fatalf("apply err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "c.txt")); string(b) != "mine\n" {
		t.Fatal("destination clobbered")
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err != nil {
		t.Fatal("source lost")
	}
	if _, err := runProposal(t, r, ToolMoveFile, map[string]any{"from": "a.txt", "to": "a.txt"}); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("same path err = %v", err)
	}
}

func TestDeleteMovePolicyAsk(t *testing.T) {
	p := DefaultPolicy()
	if p.Decide(ToolDeleteFile) != DecisionAsk || p.Decide(ToolMoveFile) != DecisionAsk {
		t.Fatal("delete/move must ask")
	}
}
