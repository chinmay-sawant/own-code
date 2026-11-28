package agentbridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/agent"
	"github.com/chinmay-sawant/own-code/internal/agenttools"
)

func TestRegistrySpecsAndReadFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]agent.ToolDefinition{}
	for _, s := range reg.Specs() {
		names[s.Name] = s
	}
	if len(names) != 4 || !names["run_shell"].NeedsApproval || names["read_file"].NeedsApproval || len(names["read_file"].Schema) == 0 {
		t.Fatalf("specs = %+v", names)
	}
	p, err := reg.Prepare("read_file", json.RawMessage(`{"path":"a.txt"}`))
	if err != nil || p.ArgsHash == "" || p.NeedsApproval {
		t.Fatalf("%+v %v", p, err)
	}
	out, err := p.Execute(context.Background(), agent.ToolApproval{})
	if err != nil || out.Text != "hello" {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := reg.Prepare("nope", nil); !errors.Is(err, agenttools.ErrUnknownTool) {
		t.Fatalf("err = %v", err)
	}
	if _, err := NewRegistry(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing root accepted")
	}
}

func TestShellRequiresBoundApproval(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistry(dir)
	root, _ := filepath.EvalSymlinks(dir)
	p, err := reg.Prepare("run_shell", json.RawMessage(`{"command":"echo hi"}`))
	if err != nil || !p.NeedsApproval {
		t.Fatalf("%+v %v", p, err)
	}
	appr := agent.ToolApproval{SessionID: "s", TurnID: "t", CallID: "c", WorktreeRoot: root, ArgsHash: p.ArgsHash, Granted: true}
	// No binding in ctx: refused.
	if _, err := p.Execute(context.Background(), appr); !errors.Is(err, agenttools.ErrNoBinding) {
		t.Fatalf("unbound err = %v", err)
	}
	// Binding for another call: refused.
	other := appr
	other.CallID = "other"
	if _, err := p.Execute(BindContext(context.Background(), other), appr); !errors.Is(err, agenttools.ErrApprovalStale) {
		t.Fatalf("foreign binding err = %v", err)
	}
	// Not granted: refused.
	denied := appr
	denied.Granted = false
	if _, err := p.Execute(BindContext(context.Background(), denied), denied); !errors.Is(err, agenttools.ErrApprovalDenied) {
		t.Fatalf("denied err = %v", err)
	}
	// Exact approval: runs.
	p, _ = reg.Prepare("run_shell", json.RawMessage(`{"command":"echo hi"}`))
	out, err := p.Execute(BindContext(context.Background(), appr), appr)
	if err != nil || !strings.Contains(out.Text, "hi") || out.ExitCode == nil || *out.ExitCode != 0 {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestRegistryWithProposalRoot(t *testing.T) {
	root, prop := t.TempDir(), t.TempDir()
	reg, err := NewRegistryWith(root, agenttools.WithProposalRoot(prop))
	if err != nil {
		t.Fatal(err)
	}
	var have []string
	for _, s := range reg.Specs() {
		have = append(have, s.Name)
	}
	if !strings.Contains(strings.Join(have, ","), "write_file") || !strings.Contains(strings.Join(have, ","), "edit_file") {
		t.Fatalf("specs = %v", have)
	}
}

func req(name string) agent.ApprovalRequest {
	return agent.ApprovalRequest{SessionID: "s", TurnID: "t", CallID: "c", WorktreeRoot: "/w", Name: name, ArgsHash: "h"}
}

func TestPolicyApprover(t *testing.T) {
	asked := 0
	grant := func(context.Context, agent.ApprovalRequest) (bool, error) { asked++; return true, nil }
	ap := NewPolicyApprover(agenttools.DefaultPolicy(), grant)

	got, err := ap.Decide(context.Background(), req("run_shell"))
	if err != nil || !got.Granted || got.CallID != "c" || got.ArgsHash != "h" || got.WorktreeRoot != "/w" || got.SessionID != "s" || got.TurnID != "t" {
		t.Fatalf("%+v %v", got, err)
	}
	// "allow" tools that nonetheless reach the approver are still asked.
	if got, err = ap.Decide(context.Background(), req("read_file")); err != nil || !got.Granted || asked != 2 {
		t.Fatalf("%+v %v asked=%d", got, err, asked)
	}
	// Denied or unknown tool: not asked, error.
	asked = 0
	custom := NewPolicyApprover(agenttools.NewPolicy(map[string]agenttools.Decision{"x": agenttools.DecisionDeny}), grant)
	for _, name := range []string{"x", "unlisted"} {
		if got, err := custom.Decide(context.Background(), req(name)); err == nil || got.Granted {
			t.Fatalf("%s: %+v %v", name, got, err)
		}
	}
	if got, err := NewPolicyApprover(nil, grant).Decide(context.Background(), req("run_shell")); err == nil || got.Granted {
		t.Fatalf("nil policy: %+v %v", got, err)
	}
	if asked != 0 {
		t.Fatalf("asked %d times for denied tools", asked)
	}
	// User refusal: not granted, no error.
	no := NewPolicyApprover(agenttools.DefaultPolicy(), func(context.Context, agent.ApprovalRequest) (bool, error) { return false, nil })
	if got, err := no.Decide(context.Background(), req("run_shell")); err != nil || got.Granted {
		t.Fatalf("%+v %v", got, err)
	}
	// ask error and nil ask: denied.
	boom := NewPolicyApprover(agenttools.DefaultPolicy(), func(context.Context, agent.ApprovalRequest) (bool, error) { return true, errors.New("ui gone") })
	if got, err := boom.Decide(context.Background(), req("run_shell")); err == nil || got.Granted {
		t.Fatalf("%+v %v", got, err)
	}
	if got, err := NewPolicyApprover(agenttools.DefaultPolicy(), nil).Decide(context.Background(), req("run_shell")); err == nil || got.Granted {
		t.Fatalf("%+v %v", got, err)
	}
	// Context canceled before or during ask: denied even if ask says yes.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := ap.Decide(ctx, req("run_shell")); err == nil || got.Granted {
		t.Fatalf("pre-canceled: %+v %v", got, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	late := NewPolicyApprover(agenttools.DefaultPolicy(), func(context.Context, agent.ApprovalRequest) (bool, error) { cancel(); return true, nil })
	if got, err := late.Decide(ctx, req("run_shell")); err == nil || got.Granted {
		t.Fatalf("canceled during ask: %+v %v", got, err)
	}
}
