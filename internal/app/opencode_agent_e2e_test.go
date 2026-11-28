package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agent"
	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/agenttools"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

type e2eProvider struct {
	mu     sync.Mutex
	steps  []agent.Result
	block  bool
	nCalls int
}

func (p *e2eProvider) Complete(ctx context.Context, req agent.Request) (agent.Result, error) {
	return p.Stream(ctx, req, func(agent.Event) {})
}

func (p *e2eProvider) Stream(ctx context.Context, req agent.Request, emit func(agent.Event)) (agent.Result, error) {
	if p.block {
		<-ctx.Done()
		return agent.Result{Status: agent.StatusInterrupted}, ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	res := agent.Result{Status: agent.StatusCompleted, Text: "done"}
	if p.nCalls < len(p.steps) {
		res = p.steps[p.nCalls]
	}
	p.nCalls++
	if res.Text != "" {
		emit(agent.Event{Kind: agent.EventTextDelta, Text: res.Text})
	}
	return res, nil
}

func e2eCall(id, name, args string) agent.ToolCall {
	return agent.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

func e2eGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// drive runs one turn and answers approvals with decide (call order).
func e2eDrive(t *testing.T, recv <-chan openCodeReply, p openCodeTurnParams, ctx context.Context, cancelOn func(openCodeRunEvent) bool, cancel context.CancelFunc, decide func(n int, req agent.ApprovalRequest) bool) (done openCodeRunEvent, props []agenttools.Proposal) {
	t.Helper()
	go executeOpenCodeTurn(ctx, p)
	n := 0
	deadline := time.After(45 * time.Second)
	for {
		select {
		case r := <-recv:
			ev := r.run
			if ev == nil {
				continue
			}
			switch ev.kind {
			case openCodeRunApproval:
				ev.approval.resolve(decide(n, ev.approval.req))
				n++
			case openCodeRunProposal:
				props = append(props, *ev.proposal)
			case openCodeRunDelta:
				if cancelOn != nil && cancelOn(*ev) {
					cancel()
				}
			case openCodeRunDone:
				return *ev, props
			}
		case <-deadline:
			t.Fatal("turn did not finish")
		}
	}
}

func TestOpenCodeAgentE2EProposalsAndRecovery(t *testing.T) {
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", ".")
	e2eGit(t, repo, "commit", "-q", "-m", "init")
	canon, err := canonicalWorkspaceRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	configDir := t.TempDir()
	manager, err := newOpenCodeWorktreeManager(configDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	wt, err := manager.Create(ctx, canon)
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if _, err := manager.OpenExisting(ctx, canon, wt.Path(), wt.baseCommit); err != nil {
		t.Fatalf("open existing: %v", err)
	}
	dbPath := filepath.Join(configDir, "sessions.sqlite")
	store, err := agentstore.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = store.Close()
		}
	})
	const sid = "local-e2e"
	if _, err := store.CreateSession(ctx, agentstore.Session{
		ID: sid, WorkspaceRoot: canon, WorktreeRoot: wt.Path(), BaselineCommit: wt.baseCommit,
		ProviderID: "p", ModelID: "m", Model: "p/m", Title: "e2e", Agent: "opencode",
		Status: agentstore.SessionQueued,
	}); err != nil {
		t.Fatal(err)
	}
	models := []opencode.ConsoleModel{{ID: "m", ProviderID: "p", Type: "chat", ToolsKnown: true, ToolsSupported: true,
		APIs: map[string]opencode.ConsoleModelAPI{"chat-completions": {BaseURL: "https://x.example/v1"}}}}
	results := make(chan openCodeReply, 16)
	params := func(turn, prompt string, prov agent.Provider) openCodeTurnParams {
		return openCodeTurnParams{store: store, manager: manager, models: models, results: results, lifeCtx: ctx,
			sessionID: sid, turnID: turn, directory: canon, prompt: prompt, client: prov, configDir: configDir}
	}

	// Turn 1: one approved edit, one denied write, then a final answer.
	prov := &e2eProvider{steps: []agent.Result{
		{Status: agent.StatusCompleted, ToolCalls: []agent.ToolCall{e2eCall("c1", "edit_file", `{"path":"a.txt","old_string":"one","new_string":"ONE"}`)}},
		{Status: agent.StatusCompleted, ToolCalls: []agent.ToolCall{e2eCall("c2", "write_file", `{"path":"c.txt","content":"x\n"}`)}},
		{Status: agent.StatusCompleted, Text: "all done"},
	}}
	done, props := e2eDrive(t, results, params("turn-1", "edit a", prov), ctx, nil, nil, func(n int, req agent.ApprovalRequest) bool { return n == 0 })
	if !strings.Contains(done.status, "completed") {
		t.Fatalf("status = %q", done.status)
	}
	if len(props) != 1 || props[0].Path != "a.txt" || !strings.Contains(props[0].Diff, "+ONE") {
		t.Fatalf("proposals = %+v", props)
	}
	// Nothing is written into the checkout, the worktree, or for the denied call.
	if b, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(b) != "one\ntwo\n" {
		t.Fatalf("checkout modified before accept: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(wt.WorkspaceRoot(), "a.txt")); string(b) != "one\ntwo\n" {
		t.Fatalf("worktree modified: %q", b)
	}
	propDir, _ := openCodeProposalDir(configDir, sid)
	if _, err := os.Stat(filepath.Join(propDir, "c.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("denied write was staged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(propDir, "a.txt")); err != nil {
		t.Fatalf("approved edit not staged: %v", err)
	}
	var sawApproved, sawDenied bool
	for _, m := range done.messages {
		if m.Kind == "tool" && strings.Contains(m.Text, "edit_file [succeeded]") {
			sawApproved = true
		}
		if m.Kind == "tool" && strings.Contains(m.Text, "write_file [denied]") {
			sawDenied = true
		}
	}
	if !sawApproved || !sawDenied {
		t.Fatalf("transcript missing approved/denied rows: %+v", done.messages)
	}

	// Turn 2: a stop while the provider is blocked.
	sctx, scancel := context.WithCancel(ctx)
	go func() { time.Sleep(300 * time.Millisecond); scancel() }()
	done2, _ := e2eDrive(t, results, params("turn-2", "stall", &e2eProvider{block: true}), sctx, nil, scancel, nil)
	if !strings.Contains(done2.status, "Stopped") {
		t.Fatalf("stop status = %q", done2.status)
	}

	// An interrupted call: dispatch begun, then the process "dies".
	if _, err := store.BeginTurn(ctx, agentstore.Turn{SessionID: sid, ID: "turn-3", UserMessageID: "m3", ProviderID: "p", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTurnMessage(ctx, agentstore.TurnMessage{ID: "m3a", SessionID: sid, TurnID: "turn-3", Role: "assistant"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveToolCall(ctx, agentstore.ToolCall{SessionID: sid, ID: "c3", MessageID: "m3a", TurnID: "turn-3", Ordinal: -1, ToolName: "run_shell", Input: `{"command":"true"}`}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordToolApproval(ctx, sid, "c3", agentstore.ApprovalApproved, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginToolDispatch(ctx, sid, "c3", time.Now()); err != nil {
		t.Fatal(err)
	}

	// Reopen the store and restore.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	store2, err := agentstore.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store2.Close() }()
	report, err := store2.RecoverInterrupted(ctx, time.Now())
	if err != nil || report.CallsUnknown != 1 {
		t.Fatalf("recovery = %+v, %v", report, err)
	}
	rows, _, err := loadOpenCodeTranscript(ctx, store2, sid)
	if err != nil {
		t.Fatal(err)
	}
	var restoredEdit, inspect, stopped bool
	for _, m := range rows {
		restoredEdit = restoredEdit || (m.Kind == "tool" && strings.Contains(m.Text, "edit_file [succeeded]"))
		if m.ID == "c3" && strings.Contains(m.Note, "Needs inspection") {
			inspect = true
		}
		stopped = stopped || (m.Kind == "notice" && m.TurnID == "turn-2")
	}
	if !restoredEdit || !inspect || !stopped {
		t.Fatalf("restore edit=%v inspect=%v stopped=%v rows=%+v", restoredEdit, inspect, stopped, rows)
	}
}

func newProposalApp(t *testing.T, repo string) *App {
	t.Helper()
	a, err := New(repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.cancelAllOpenCodeRuns(); a.Close() })
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.openCode.selectedSessionID = "s1"
	return a
}

func e2eProposal(t *testing.T, repo, rel, content string) agenttools.Proposal {
	t.Helper()
	// Build through the real tool so hashes are authentic.
	root := t.TempDir()
	prop := t.TempDir()
	src, _ := os.ReadFile(filepath.Join(repo, rel))
	if err := os.WriteFile(filepath.Join(root, rel), src, 0o644); err != nil {
		t.Fatal(err)
	}
	var got agenttools.Proposal
	reg, err := agenttools.NewRegistry(root, agenttools.WithProposalRoot(prop), agenttools.WithProposalSink(func(_ context.Context, p agenttools.Proposal) error { got = p; return nil }))
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": rel, "content": content})
	pc, err := reg.Prepare(agenttools.ToolWriteFile, args)
	if err != nil {
		t.Fatal(err)
	}
	b := agenttools.Binding{SessionID: "s", TurnID: "t", CallID: "c", WorktreeRoot: root}
	_, err = pc.Execute(agenttools.WithBinding(context.Background(), b), &agenttools.Approval{SessionID: "s", TurnID: "t", CallID: "c", WorktreeRoot: root, ArgsHash: pc.ArgsHash, Granted: true})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestOpenCodeAgentProposalAcceptRefusalsAndReject(t *testing.T) {
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	for _, f := range []string{"a.txt", "b.txt", "d.txt"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte("base\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e2eGit(t, repo, "add", ".")
	e2eGit(t, repo, "commit", "-q", "-m", "init")
	a := newProposalApp(t, repo)

	read := func(f string) string { b, _ := os.ReadFile(filepath.Join(repo, f)); return string(b) }

	// Dirty open buffer: refused, file untouched, proposal stays pending.
	pa := e2eProposal(t, repo, "a.txt", "agent\n")
	a.addAgentProposal("s1", pa)
	a.dirty["a.txt"] = true
	_ = a.acceptOpenCodeProposal(openCodeAgentProposalID(pa))
	if read("a.txt") != "base\n" || !strings.Contains(a.openCode.panelStatus, "unsaved") || len(a.openCode.agentProposals["s1"]) != 1 {
		t.Fatalf("dirty buffer not refused: %q", a.openCode.panelStatus)
	}
	delete(a.dirty, "a.txt")

	// Unstaged user change: refused and preserved.
	pb := e2eProposal(t, repo, "b.txt", "agent\n")
	a.addAgentProposal("s1", pb)
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = a.acceptOpenCodeProposal(openCodeAgentProposalID(pb))
	if read("b.txt") != "mine\n" || !strings.Contains(a.openCode.panelStatus, "Refused") {
		t.Fatalf("unstaged change clobbered: %q %q", read("b.txt"), a.openCode.panelStatus)
	}
	// Staged-only change (worktree restored to HEAD content) is also refused.
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", "b.txt")
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = a.acceptOpenCodeProposal(openCodeAgentProposalID(pb))
	if read("b.txt") != "base\n" || !strings.Contains(a.openCode.panelStatus, "Refused") {
		t.Fatalf("staged change not refused: %q", a.openCode.panelStatus)
	}

	// Clean file: accept applies. Reject discards without writing.
	pd := e2eProposal(t, repo, "d.txt", "agent\n")
	a.addAgentProposal("s1", pd)
	_ = a.acceptOpenCodeProposal(openCodeAgentProposalID(pd))
	if read("d.txt") != "agent\n" {
		t.Fatalf("accept did not apply: %q", a.openCode.panelStatus)
	}
	_ = a.rejectOpenCodeProposal(openCodeAgentProposalID(pa))
	if read("a.txt") != "base\n" {
		t.Fatal("reject wrote to the checkout")
	}
	if rows := a.agentProposalRows("s1"); len(rows) != 1 || rows[0].Path != "b.txt" {
		t.Fatalf("rows after decisions = %+v", rows)
	}
}

func e2eWholeProposal(t *testing.T, repo, tool string, args map[string]string) agenttools.Proposal {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{args["path"], args["from"]} {
		if rel == "" {
			continue
		}
		if src, err := os.ReadFile(filepath.Join(repo, rel)); err == nil {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, rel), src, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	var got agenttools.Proposal
	reg, err := agenttools.NewRegistry(root, agenttools.WithProposalRoot(t.TempDir()), agenttools.WithProposalSink(func(_ context.Context, p agenttools.Proposal) error { got = p; return nil }))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(args)
	pc, err := reg.Prepare(tool, raw)
	if err != nil {
		t.Fatal(err)
	}
	b := agenttools.Binding{SessionID: "s", TurnID: "t", CallID: "c", WorktreeRoot: root}
	if _, err = pc.Execute(agenttools.WithBinding(context.Background(), b), &agenttools.Approval{SessionID: "s", TurnID: "t", CallID: "c", WorktreeRoot: root, ArgsHash: pc.ArgsHash, Granted: true}); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestOpenCodeAgentDeleteAndRenameProposals(t *testing.T) {
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	for _, f := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte("base\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e2eGit(t, repo, "add", ".")
	e2eGit(t, repo, "commit", "-q", "-m", "init")
	a := newProposalApp(t, repo)
	exists := func(f string) bool { _, err := os.Stat(filepath.Join(repo, f)); return err == nil }

	// Nothing is touched before accept; the unit is registered without hunks.
	pa := e2eWholeProposal(t, repo, agenttools.ToolDeleteFile, map[string]string{"path": "a.txt"})
	a.addAgentProposal("s1", pa)
	if !exists("a.txt") || a.agentReviewRegistry().Get("s1", "a.txt") == nil || len(a.agentReviewRegistry().Get("s1", "a.txt").Hunks) != 0 {
		t.Fatal("staging touched the checkout or created hunks")
	}
	if rows := a.agentProposalRows("s1"); rows[0].Change != "Delete" {
		t.Fatalf("rows %+v", rows)
	}

	// Dirty buffer refuses; then staged state refuses; then accept closes the open doc.
	a.dirty["a.txt"] = true
	_ = a.acceptOpenCodeProposal(openCodeAgentProposalID(pa))
	if !exists("a.txt") || !strings.Contains(a.openCode.panelStatus, "unsaved") {
		t.Fatalf("dirty not refused: %q", a.openCode.panelStatus)
	}
	delete(a.dirty, "a.txt")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", "a.txt")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = a.acceptOpenCodeProposal(openCodeAgentProposalID(pa))
	if !exists("a.txt") || !strings.Contains(a.openCode.panelStatus, "Refused") {
		t.Fatalf("staged not refused: %q", a.openCode.panelStatus)
	}
	e2eGit(t, repo, "reset", "-q", "a.txt")
	a.docs["a.txt"] = a.ws.openDoc("a.txt")
	a.tabs = append(a.tabs, "a.txt")
	_ = a.acceptOpenCodeProposal(openCodeAgentProposalID(pa))
	if exists("a.txt") {
		t.Fatalf("accept did not delete: %q", a.openCode.panelStatus)
	}
	if _, ok := a.docs["a.txt"]; ok {
		t.Fatal("open doc not closed")
	}
	for _, tab := range a.tabs {
		if tab == "a.txt" {
			t.Fatal("tab not closed")
		}
	}

	// Reject leaves the file alone.
	pb := e2eWholeProposal(t, repo, agenttools.ToolDeleteFile, map[string]string{"path": "b.txt"})
	a.addAgentProposal("s1", pb)
	_ = a.rejectOpenCodeProposal(openCodeAgentProposalID(pb))
	if !exists("b.txt") || len(a.agentProposalRows("s1")) != 0 {
		t.Fatal("reject touched the checkout")
	}

	// Baseline changed: refused.
	pc := e2eWholeProposal(t, repo, agenttools.ToolDeleteFile, map[string]string{"path": "c.txt"})
	a.addAgentProposal("s1", pc)
	e2eGit(t, repo, "add", "c.txt")
	if err := os.WriteFile(filepath.Join(repo, "c.txt"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", "c.txt")
	e2eGit(t, repo, "commit", "-q", "-m", "c")
	_ = a.acceptOpenCodeProposal(openCodeAgentProposalID(pc))
	if !exists("c.txt") || !strings.Contains(a.openCode.panelStatus, "changed") {
		t.Fatalf("baseline change not refused: %q", a.openCode.panelStatus)
	}

	// Rename: collision refused (untracked destination), then clean accept.
	pd := e2eWholeProposal(t, repo, agenttools.ToolMoveFile, map[string]string{"from": "d.txt", "to": "moved.txt"})
	a.addAgentProposal("s1", pd)
	if err := os.WriteFile(filepath.Join(repo, "moved.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = a.acceptOpenCodeProposal(openCodeAgentProposalID(pd))
	if !exists("d.txt") || !strings.Contains(a.openCode.panelStatus, "Refused") {
		t.Fatalf("collision not refused: %q", a.openCode.panelStatus)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "moved.txt")); string(b) != "mine\n" {
		t.Fatal("destination clobbered")
	}
	if err := os.Remove(filepath.Join(repo, "moved.txt")); err != nil {
		t.Fatal(err)
	}
	// Dirty source buffer refuses a rename too.
	a.dirty["d.txt"] = true
	_ = a.acceptOpenCodeProposal(openCodeAgentProposalID(pd))
	if !exists("d.txt") || !strings.Contains(a.openCode.panelStatus, "unsaved") {
		t.Fatalf("dirty source not refused: %q", a.openCode.panelStatus)
	}
	delete(a.dirty, "d.txt")
	_ = a.acceptOpenCodeProposal(openCodeAgentProposalID(pd))
	if exists("d.txt") || !exists("moved.txt") {
		t.Fatalf("rename not applied: %q", a.openCode.panelStatus)
	}

	// Symlink target: refused, outside file survives.
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pe := e2eWholeProposal(t, repo, agenttools.ToolDeleteFile, map[string]string{"path": "e.txt"})
	a.addAgentProposal("s1", pe)
	if err := os.Remove(filepath.Join(repo, "e.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "e.txt")); err == nil {
		a.openCode.panelStatus = ""
		_ = a.acceptOpenCodeProposal(openCodeAgentProposalID(pe))
		if _, err := os.Stat(outside); err != nil || !strings.Contains(a.openCode.panelStatus, "Refused") {
			t.Fatalf("symlink delete not refused: %q", a.openCode.panelStatus)
		}
	}
}
