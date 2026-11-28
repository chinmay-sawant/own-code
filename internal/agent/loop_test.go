package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agentstore"
)

// scriptProvider returns scripted results per session and records requests.
type scriptProvider struct {
	mu       sync.Mutex
	script   map[string][]Result // by session
	requests []Request
	block    chan struct{} // if set, Stream waits for ctx or this
	started  chan string
}

func (p *scriptProvider) Complete(ctx context.Context, req Request) (Result, error) {
	return p.Stream(ctx, req, func(Event) {})
}

func (p *scriptProvider) Stream(ctx context.Context, req Request, emit func(Event)) (Result, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	q := p.script[req.SessionID]
	var res Result
	if len(q) > 0 {
		res, p.script[req.SessionID] = q[0], q[1:]
	} else {
		res = Result{Status: StatusCompleted, Text: "done"}
	}
	p.mu.Unlock()
	if p.started != nil {
		p.started <- req.SessionID
	}
	if p.block != nil {
		select {
		case <-ctx.Done():
			emit(Event{Kind: EventTextDelta, Text: "partial"})
			return Result{Status: StatusInterrupted, Text: "partial"}, ctx.Err()
		case <-p.block:
		}
	}
	if res.Text != "" {
		emit(Event{Kind: EventTextDelta, SessionID: req.SessionID, TurnID: req.TurnID, Text: res.Text})
	}
	return res, nil
}

func (p *scriptProvider) reqs() []Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Request(nil), p.requests...)
}

func tc(name, args string) ToolCall {
	return ToolCall{ID: "prov-id", Name: name, Arguments: json.RawMessage(args)}
}

func toolStep(calls ...ToolCall) Result {
	return Result{Status: StatusCompleted, ToolCalls: calls}
}

type fakeRegistry struct {
	needsApproval bool
	execs         atomic.Int32
	run           func(ctx context.Context, appr ToolApproval) (ToolOutput, error)
}

func (f *fakeRegistry) Specs() []ToolDefinition {
	return []ToolDefinition{{Name: "echo", Description: "echo", Schema: json.RawMessage(`{"type":"object"}`), NeedsApproval: f.needsApproval}}
}

func (f *fakeRegistry) Prepare(name string, raw json.RawMessage) (PreparedCall, error) {
	var a struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.Msg == "" {
		return PreparedCall{}, errors.New("msg is required")
	}
	sum := sha256.Sum256(raw)
	return PreparedCall{
		Name: name, Args: raw, ArgsHash: hex.EncodeToString(sum[:]), NeedsApproval: f.needsApproval, Summary: "echo " + a.Msg,
		Execute: func(ctx context.Context, appr ToolApproval) (ToolOutput, error) {
			f.execs.Add(1)
			if f.run != nil {
				return f.run(ctx, appr)
			}
			return ToolOutput{Text: "echoed " + a.Msg}, nil
		},
	}, nil
}

type fakeApprover struct {
	grant bool
	err   error
	tweak func(*ToolApproval)
	calls atomic.Int32
	store *agentstore.Store
	check func(req ApprovalRequest)
}

func (a *fakeApprover) Decide(ctx context.Context, req ApprovalRequest) (ToolApproval, error) {
	a.calls.Add(1)
	if a.check != nil {
		a.check(req)
	}
	if a.err != nil {
		return ToolApproval{}, a.err
	}
	ap := ToolApproval{SessionID: req.SessionID, TurnID: req.TurnID, CallID: req.CallID, WorktreeRoot: req.WorktreeRoot, ArgsHash: req.ArgsHash, Granted: a.grant}
	if a.tweak != nil {
		a.tweak(&ap)
	}
	return ap, nil
}

type env struct {
	store *agentstore.Store
	prov  *scriptProvider
	reg   *fakeRegistry
	appr  *fakeApprover
	run   *Runner
}

func newEnv(t *testing.T, mutate func(*Config)) *env {
	t.Helper()
	ctx := context.Background()
	store, err := agentstore.Open(ctx, filepath.Join(t.TempDir(), "s.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	e := &env{store: store, prov: &scriptProvider{script: map[string][]Result{}}, reg: &fakeRegistry{needsApproval: true}, appr: &fakeApprover{grant: true}}
	cfg := Config{Store: store, Provider: e.prov, Tools: e.reg, Approver: e.appr, MaxSteps: 5}
	if mutate != nil {
		mutate(&cfg)
	}
	e.run, err = NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) session(t *testing.T, id string) {
	t.Helper()
	_, err := e.store.CreateSession(context.Background(), agentstore.Session{
		ID: id, WorkspaceRoot: "/work", WorktreeRoot: "/work/wt-" + id, ServerID: "console", OpenCodeSessionID: id,
		Title: "t", Agent: "build", Model: "m", ProviderID: "p", ModelID: "m", Status: agentstore.SessionRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (e *env) do(t *testing.T, session, turn string) Outcome {
	t.Helper()
	e.session(t, session)
	return e.again(t, session, turn)
}

func (e *env) again(t *testing.T, session, turn string) Outcome {
	t.Helper()
	out, err := e.run.Run(context.Background(), RunInput{
		SessionID: session, TurnID: turn, UserText: "hi " + turn, WorktreeRoot: "/work/wt-" + session,
		Model: ModelTarget{ProviderID: "p", ModelID: "m", ToolsSupported: true},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return out
}

func (e *env) calls(t *testing.T, session string) []agentstore.ToolCall {
	t.Helper()
	h, err := e.store.LoadHistory(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	var out []agentstore.ToolCall
	for _, m := range h {
		for _, p := range m.Parts {
			if p.ToolCall != nil {
				out = append(out, *p.ToolCall)
			}
		}
	}
	return out
}

func (e *env) turnStatus(t *testing.T, session, turn string) agentstore.TurnStatus {
	t.Helper()
	turns, err := e.store.ListTurns(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range turns {
		if tr.ID == turn {
			return tr.Status
		}
	}
	t.Fatalf("turn %s missing", turn)
	return ""
}

func lastToolMessage(req Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == RoleTool {
			return req.Messages[i].Content
		}
	}
	return ""
}

func TestFinalResponse(t *testing.T) {
	e := newEnv(t, nil)
	e.prov.script["s1"] = []Result{{Status: StatusCompleted, Text: "hello there"}}
	out := e.do(t, "s1", "t1")
	if out.Status != agentstore.TurnCompleted || out.Reason != "final" || out.Text != "hello there" {
		t.Fatalf("outcome = %+v", out)
	}
	if e.turnStatus(t, "s1", "t1") != agentstore.TurnCompleted {
		t.Fatal("turn not completed in store")
	}
	h, _ := e.store.LoadHistory(context.Background(), "s1")
	if len(h) != 2 || h[0].Role != "user" || h[1].Parts[0].Text != "hello there" || h[1].Parts[0].Status != agentstore.PartComplete {
		t.Fatalf("history = %+v", h)
	}
	req := e.prov.reqs()[0]
	if len(req.Tools) != 1 || req.Tools[0].Name != "echo" || req.Messages[0].Content != "hi t1" {
		t.Fatalf("request = %+v", req)
	}
}

func TestMultipleToolStepsRebuildHistory(t *testing.T) {
	e := newEnv(t, nil)
	e.prov.script["s1"] = []Result{
		toolStep(tc("echo", `{"msg":"a"}`)),
		toolStep(tc("echo", `{"msg":"b"}`), tc("echo", `{"msg":"c"}`)),
		{Status: StatusCompleted, Text: "all done"},
	}
	out := e.do(t, "s1", "t1")
	if out.Status != agentstore.TurnCompleted || out.Steps != 3 || out.ToolCalls != 3 {
		t.Fatalf("outcome = %+v", out)
	}
	if e.reg.execs.Load() != 3 || e.appr.calls.Load() != 3 {
		t.Fatalf("execs=%d approvals=%d", e.reg.execs.Load(), e.appr.calls.Load())
	}
	reqs := e.prov.reqs()
	last := reqs[2].Messages
	var roles []Role
	for _, m := range last {
		roles = append(roles, m.Role)
	}
	want := []Role{RoleUser, RoleAssistant, RoleTool, RoleAssistant, RoleTool, RoleTool}
	if fmt.Sprint(roles) != fmt.Sprint(want) {
		t.Fatalf("roles = %v want %v", roles, want)
	}
	if last[2].Content != "echoed a" || last[2].ToolCallID != last[1].ToolCalls[0].ID {
		t.Fatalf("tool message = %+v / %+v", last[1], last[2])
	}
	for _, c := range e.calls(t, "s1") {
		if c.Status != agentstore.ToolSucceeded || c.Approval != agentstore.ApprovalApproved || c.DispatchStartedAt.IsZero() {
			t.Fatalf("call = %+v", c)
		}
	}
}

func TestUnknownInvalidAndOversizedNeverExecute(t *testing.T) {
	big := `{"msg":"` + strings.Repeat("x", DefaultMaxArgsBytes) + `"}`
	cases := map[string]ToolCall{
		"unknown":   tc("rm_rf", `{"msg":"x"}`),
		"invalid":   tc("echo", `{"nope":1}`),
		"oversized": tc("echo", big),
		"notjson":   tc("echo", `{"msg":`),
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.prov.script["s1"] = []Result{toolStep(call), {Status: StatusCompleted, Text: "ok"}}
			out := e.do(t, "s1", "t1")
			if out.Status != agentstore.TurnCompleted {
				t.Fatalf("outcome = %+v", out)
			}
			if e.reg.execs.Load() != 0 || e.appr.calls.Load() != 0 {
				t.Fatalf("executed or asked: %d %d", e.reg.execs.Load(), e.appr.calls.Load())
			}
			msg := lastToolMessage(e.prov.reqs()[1])
			if !strings.HasPrefix(msg, "Rejected:") {
				t.Fatalf("model saw %q", msg)
			}
			c := e.calls(t, "s1")[0]
			if c.Status != agentstore.ToolDenied || len(c.Input) > 1024 {
				t.Fatalf("call = %+v", c)
			}
		})
	}
}

func TestDenialPersistedAndNotRun(t *testing.T) {
	for name, a := range map[string]*fakeApprover{
		"denied":  {grant: false},
		"error":   {err: errors.New("ui gone")},
		"stale":   {grant: true, tweak: func(ap *ToolApproval) { ap.ArgsHash = "other" }},
		"foreign": {grant: true, tweak: func(ap *ToolApproval) { ap.TurnID = "old-turn" }},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.run.cfg.Approver = a
			e.prov.script["s1"] = []Result{toolStep(tc("echo", `{"msg":"a"}`)), {Status: StatusCompleted, Text: "ok"}}
			out := e.do(t, "s1", "t1")
			if out.Status != agentstore.TurnCompleted || e.reg.execs.Load() != 0 {
				t.Fatalf("outcome=%+v execs=%d", out, e.reg.execs.Load())
			}
			c := e.calls(t, "s1")[0]
			if c.Status != agentstore.ToolDenied || !c.DispatchStartedAt.IsZero() {
				t.Fatalf("call = %+v", c)
			}
			if !strings.HasPrefix(lastToolMessage(e.prov.reqs()[1]), "Denied") {
				t.Fatalf("model saw %q", lastToolMessage(e.prov.reqs()[1]))
			}
		})
	}
}

func TestNilApproverDeniesApprovalTools(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.Approver = nil })
	e.prov.script["s1"] = []Result{toolStep(tc("echo", `{"msg":"a"}`))}
	e.do(t, "s1", "t1")
	if e.reg.execs.Load() != 0 {
		t.Fatal("executed without approver")
	}
}

func TestApprovedExecutionPersistsApprovalBeforeDispatch(t *testing.T) {
	e := newEnv(t, nil)
	e.reg.run = func(ctx context.Context, appr ToolApproval) (ToolOutput, error) {
		c, err := e.store.GetToolCall(context.Background(), appr.SessionID, appr.CallID)
		if err != nil {
			return ToolOutput{}, err
		}
		if c.Status != agentstore.ToolRunning || c.Approval != agentstore.ApprovalApproved || c.ApprovalAt.IsZero() || c.DispatchStartedAt.IsZero() {
			return ToolOutput{}, fmt.Errorf("claim not durable before execute: %+v", c)
		}
		if !appr.Granted || appr.WorktreeRoot != "/work/wt-s1" {
			return ToolOutput{}, fmt.Errorf("bad approval %+v", appr)
		}
		code := 0
		return ToolOutput{Text: "ok", Truncated: true, ExitCode: &code}, nil
	}
	e.appr.check = func(req ApprovalRequest) {
		if c, _ := e.store.GetToolCall(context.Background(), req.SessionID, req.CallID); c.Status != agentstore.ToolRequested {
			t.Errorf("call at approval time = %s", c.Status)
		}
		if req.Summary != "echo a" || req.Name != "echo" || req.ArgsHash == "" {
			t.Errorf("req = %+v", req)
		}
	}
	e.prov.script["s1"] = []Result{toolStep(tc("echo", `{"msg":"a"}`)), {Status: StatusCompleted, Text: "ok"}}
	e.do(t, "s1", "t1")
	c := e.calls(t, "s1")[0]
	if c.Status != agentstore.ToolSucceeded || c.Result != "ok\n[output truncated]\n[exit code 0]" {
		t.Fatalf("call = %+v", c)
	}
}

func TestNoApprovalNeededStillRecordsApproval(t *testing.T) {
	e := newEnv(t, nil)
	e.reg.needsApproval = false
	e.prov.script["s1"] = []Result{toolStep(tc("echo", `{"msg":"a"}`))}
	e.do(t, "s1", "t1")
	c := e.calls(t, "s1")[0]
	if e.appr.calls.Load() != 0 || c.Approval != agentstore.ApprovalApproved || c.Status != agentstore.ToolSucceeded {
		t.Fatalf("call = %+v asks=%d", c, e.appr.calls.Load())
	}
}

func TestToolErrorAndPanicFedBack(t *testing.T) {
	for name, fn := range map[string]func(context.Context, ToolApproval) (ToolOutput, error){
		"error": func(context.Context, ToolApproval) (ToolOutput, error) {
			return ToolOutput{Text: "stderr"}, errors.New("boom")
		},
		"panic": func(context.Context, ToolApproval) (ToolOutput, error) { panic("kaboom") },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.reg.run = fn
			e.prov.script["s1"] = []Result{toolStep(tc("echo", `{"msg":"a"}`)), {Status: StatusCompleted, Text: "ok"}}
			out := e.do(t, "s1", "t1")
			c := e.calls(t, "s1")[0]
			if out.Status != agentstore.TurnCompleted || c.Status != agentstore.ToolFailed || !c.IsError {
				t.Fatalf("out=%+v call=%+v", out, c)
			}
			if !strings.HasPrefix(lastToolMessage(e.prov.reqs()[1]), "error:") {
				t.Fatalf("model saw %q", lastToolMessage(e.prov.reqs()[1]))
			}
		})
	}
}

func TestMaxStepsTerminal(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxSteps = 2 })
	e.prov.script["s1"] = []Result{toolStep(tc("echo", `{"msg":"a"}`)), toolStep(tc("echo", `{"msg":"b"}`)), toolStep(tc("echo", `{"msg":"c"}`))}
	out := e.do(t, "s1", "t1")
	if out.Status != agentstore.TurnFailed || out.Reason != "max_steps" || out.Steps != 2 {
		t.Fatalf("outcome = %+v", out)
	}
	if len(e.prov.reqs()) != 2 || e.reg.execs.Load() != 2 {
		t.Fatalf("requests=%d execs=%d", len(e.prov.reqs()), e.reg.execs.Load())
	}
	turns, _ := e.store.ListTurns(context.Background(), "s1")
	if turns[0].Status != agentstore.TurnFailed || !strings.Contains(turns[0].Error, "step limit") {
		t.Fatalf("turn = %+v", turns[0])
	}
}

func TestProviderFailure(t *testing.T) {
	e := newEnv(t, nil)
	e.prov.script["s1"] = []Result{{Status: StatusFailed, Text: "part"}}
	out := e.do(t, "s1", "t1")
	if out.Status != agentstore.TurnFailed || out.Reason != "provider_error" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestCancelDuringStreamInterruptsTurn(t *testing.T) {
	e := newEnv(t, nil)
	e.prov.block = make(chan struct{})
	e.prov.started = make(chan string, 1)
	e.session(t, "s1")
	done := make(chan Outcome, 1)
	go func() {
		out, _ := e.run.Run(context.Background(), RunInput{SessionID: "s1", TurnID: "t1", UserText: "go", Model: ModelTarget{ToolsSupported: true}})
		done <- out
	}()
	<-e.prov.started
	if e.run.Cancel("s1", "other-turn") || e.run.Cancel("other-session", "t1") {
		t.Fatal("stale cancel accepted")
	}
	if !e.run.Cancel("s1", "t1") {
		t.Fatal("cancel rejected")
	}
	out := <-done
	if out.Status != agentstore.TurnInterrupted || e.turnStatus(t, "s1", "t1") != agentstore.TurnInterrupted {
		t.Fatalf("outcome = %+v", out)
	}
	h, _ := e.store.LoadHistory(context.Background(), "s1")
	if p := h[len(h)-1].Parts[0]; p.Text != "partial" || p.Status != agentstore.PartInterrupted {
		t.Fatalf("partial part = %+v", p)
	}
	if _, active := e.run.Active("s1"); active {
		t.Fatal("run still registered")
	}
}

func TestCancelDuringToolExecution(t *testing.T) {
	e := newEnv(t, nil)
	started := make(chan struct{})
	e.reg.run = func(ctx context.Context, _ ToolApproval) (ToolOutput, error) {
		close(started)
		<-ctx.Done()
		return ToolOutput{Text: "killed"}, ctx.Err()
	}
	e.prov.script["s1"] = []Result{toolStep(tc("echo", `{"msg":"a"}`), tc("echo", `{"msg":"b"}`))}
	e.session(t, "s1")
	done := make(chan Outcome, 1)
	go func() {
		out, _ := e.run.Run(context.Background(), RunInput{SessionID: "s1", TurnID: "t1", UserText: "go", Model: ModelTarget{ToolsSupported: true}})
		done <- out
	}()
	<-started
	e.run.Cancel("s1", "t1")
	out := <-done
	if out.Status != agentstore.TurnInterrupted || e.reg.execs.Load() != 1 {
		t.Fatalf("outcome=%+v execs=%d", out, e.reg.execs.Load())
	}
	calls := e.calls(t, "s1")
	if calls[0].Status != agentstore.ToolFailed || calls[1].Status != agentstore.ToolDenied || calls[1].Result == "" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestCancelDuringApproval(t *testing.T) {
	e := newEnv(t, nil)
	asked := make(chan struct{})
	e.run.cfg.Approver = approverFunc(func(ctx context.Context, req ApprovalRequest) (ToolApproval, error) {
		close(asked)
		<-ctx.Done()
		return ToolApproval{}, ctx.Err()
	})
	e.prov.script["s1"] = []Result{toolStep(tc("echo", `{"msg":"a"}`))}
	e.session(t, "s1")
	done := make(chan Outcome, 1)
	go func() {
		out, _ := e.run.Run(context.Background(), RunInput{SessionID: "s1", TurnID: "t1", UserText: "go", Model: ModelTarget{ToolsSupported: true}})
		done <- out
	}()
	<-asked
	e.run.Cancel("s1", "t1")
	out := <-done
	c := e.calls(t, "s1")[0]
	if out.Status != agentstore.TurnInterrupted || e.reg.execs.Load() != 0 || c.Approval != agentstore.ApprovalCanceled {
		t.Fatalf("outcome=%+v call=%+v", out, c)
	}
}

type approverFunc func(context.Context, ApprovalRequest) (ToolApproval, error)

func (f approverFunc) Decide(ctx context.Context, r ApprovalRequest) (ToolApproval, error) {
	return f(ctx, r)
}

func TestRecoveryNeverRedispatches(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	e.session(t, "s1")
	st := e.store
	// Simulate a crashed process: one running, one approved, one requested call.
	if _, err := st.BeginTurn(ctx, agentstore.Turn{SessionID: "s1", ID: "old"}); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.SaveTurnMessage(ctx, agentstore.TurnMessage{ID: "ou", SessionID: "s1", TurnID: "old", Role: "user"}))
	must(st.SaveMessagePart(ctx, agentstore.MessagePart{SessionID: "s1", MessageID: "ou", ID: "text", Kind: agentstore.PartText, Text: "earlier"}))
	must(st.SaveTurnMessage(ctx, agentstore.TurnMessage{ID: "oa", SessionID: "s1", TurnID: "old", Role: "assistant"}))
	for _, id := range []string{"running", "approved", "requested"} {
		_, err := st.SaveToolCall(ctx, agentstore.ToolCall{SessionID: "s1", ID: id, MessageID: "oa", TurnID: "old", Ordinal: -1, ToolName: "echo", Input: `{"msg":"x"}`})
		must(err)
	}
	must(st.RecordToolApproval(ctx, "s1", "running", agentstore.ApprovalApproved, time.Now()))
	must(st.BeginToolDispatch(ctx, "s1", "running", time.Now()))
	must(st.RecordToolApproval(ctx, "s1", "approved", agentstore.ApprovalApproved, time.Now()))
	if _, err := st.RecoverInterrupted(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	e.prov.script["s1"] = []Result{{Status: StatusCompleted, Text: "I see"}}
	out := e.again(t, "s1", "new")
	if out.Status != agentstore.TurnCompleted || e.reg.execs.Load() != 0 || e.appr.calls.Load() != 0 {
		t.Fatalf("outcome=%+v execs=%d asks=%d", out, e.reg.execs.Load(), e.appr.calls.Load())
	}
	want := map[string]agentstore.ToolCallStatus{"running": agentstore.ToolUnknown, "approved": agentstore.ToolInterrupted, "requested": agentstore.ToolInterrupted}
	for _, c := range e.calls(t, "s1") {
		if c.Status != want[c.ID] {
			t.Fatalf("call %s status %s", c.ID, c.Status)
		}
	}
	// The model sees the unknown outcome as a result and every call is answered.
	var results int
	for _, m := range e.prov.reqs()[0].Messages {
		if m.Role == RoleTool {
			results++
			if m.Content == "" {
				t.Fatal("empty tool result")
			}
		}
	}
	if results != 3 {
		t.Fatalf("tool results = %d", results)
	}
	// Re-requesting the same call needs a fresh ID and fresh approval.
	e.prov.script["s1"] = []Result{toolStep(tc("echo", `{"msg":"x"}`)), {Status: StatusCompleted, Text: "ok"}}
	e.again(t, "s1", "newer")
	if e.appr.calls.Load() != 1 || e.reg.execs.Load() != 1 {
		t.Fatalf("asks=%d execs=%d", e.appr.calls.Load(), e.reg.execs.Load())
	}
	// A claimed call cannot be claimed again.
	if err := st.BeginToolDispatch(ctx, "s1", "running", time.Now()); !errors.Is(err, agentstore.ErrAlreadyDispatched) {
		t.Fatalf("second claim err = %v", err)
	}
}

func TestTurnIDReuseAndBusySession(t *testing.T) {
	e := newEnv(t, nil)
	e.do(t, "s1", "t1")
	if _, err := e.run.Run(context.Background(), RunInput{SessionID: "s1", TurnID: "t1"}); !errors.Is(err, ErrTurnExists) {
		t.Fatalf("err = %v", err)
	}
	e.prov.block = make(chan struct{})
	e.prov.started = make(chan string, 2)
	done := make(chan struct{})
	go func() {
		e.run.Run(context.Background(), RunInput{SessionID: "s1", TurnID: "t2", UserText: "x"})
		close(done)
	}()
	<-e.prov.started
	if _, err := e.run.Run(context.Background(), RunInput{SessionID: "s1", TurnID: "t3"}); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("err = %v", err)
	}
	close(e.prov.block)
	<-done
}

func TestConcurrentSessionsDoNotMix(t *testing.T) {
	e := newEnv(t, nil)
	e.reg.needsApproval = false
	const n = 8
	for i := 0; i < n; i++ {
		s := fmt.Sprintf("s%d", i)
		e.session(t, s)
		e.prov.script[s] = []Result{
			toolStep(tc("echo", fmt.Sprintf(`{"msg":"m%d"}`, i))),
			{Status: StatusCompleted, Text: "final " + s},
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := fmt.Sprintf("s%d", i)
			out := e.again(t, s, "turn-"+s)
			if out.Text != "final "+s || out.Status != agentstore.TurnCompleted {
				t.Errorf("%s outcome = %+v", s, out)
			}
		}()
	}
	wg.Wait()
	for _, req := range e.prov.reqs() {
		want := "hi turn-" + req.SessionID
		if req.Messages[0].Content != want || req.TurnID != "turn-"+req.SessionID {
			t.Fatalf("session %s saw %+v", req.SessionID, req.Messages)
		}
		for _, m := range req.Messages {
			if m.Role == RoleTool && m.Content != "echoed m"+strings.TrimPrefix(req.SessionID, "s") {
				t.Fatalf("session %s saw foreign tool result %q", req.SessionID, m.Content)
			}
		}
	}
	if e.reg.execs.Load() != n {
		t.Fatalf("execs = %d", e.reg.execs.Load())
	}
}

func TestToolsOmittedWhenModelDoesNotSupportThem(t *testing.T) {
	e := newEnv(t, nil)
	e.session(t, "s1")
	if _, err := e.run.Run(context.Background(), RunInput{SessionID: "s1", TurnID: "t1", UserText: "x"}); err != nil {
		t.Fatal(err)
	}
	if len(e.prov.reqs()[0].Tools) != 0 {
		t.Fatal("tools sent to unsupported model")
	}
}

func TestBindContextWrapsExecute(t *testing.T) {
	type key struct{}
	e := newEnv(t, func(c *Config) {
		c.BindContext = func(ctx context.Context, a ToolApproval) context.Context {
			return context.WithValue(ctx, key{}, a.SessionID+"/"+a.TurnID+"/"+a.CallID+"/"+a.WorktreeRoot)
		}
	})
	var got string
	e.reg.run = func(ctx context.Context, _ ToolApproval) (ToolOutput, error) {
		got, _ = ctx.Value(key{}).(string)
		return ToolOutput{Text: "ok"}, nil
	}
	e.prov.script["s1"] = []Result{toolStep(tc("echo", `{"msg":"a"}`))}
	e.do(t, "s1", "t1")
	if got != "s1/t1/t1-s0-c0//work/wt-s1" {
		t.Fatalf("binding = %q", got)
	}
}
