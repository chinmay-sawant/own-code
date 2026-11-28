package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agent"
	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

func newRunTestApp(t *testing.T) *App {
	t.Helper()
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.cancelAllOpenCodeRuns(); a.Close() })
	return a
}

func addRun(a *App, session, turn, prompt string) *openCodeRun {
	ctx, cancel := context.WithCancel(context.Background())
	_ = ctx
	r := &openCodeRun{sessionID: session, turnID: turn, prompt: prompt, cancel: cancel}
	if a.openCode.runs == nil {
		a.openCode.runs = map[string]*openCodeRun{}
	}
	a.openCode.runs[session] = r
	return r
}

func TestRunEventStreamingAndStale(t *testing.T) {
	a := newRunTestApp(t)
	a.openCode.selectedSessionID = "s1"
	run := addRun(a, "s1", "t1", "hi")
	if !a.handleOpenCodeRunEvent(&openCodeRunEvent{sessionID: "s1", turnID: "t1", kind: openCodeRunDelta, text: "he"}) ||
		!a.handleOpenCodeRunEvent(&openCodeRunEvent{sessionID: "s1", turnID: "t1", kind: openCodeRunDelta, text: "llo"}) {
		t.Fatal("live deltas ignored")
	}
	if run.text != "hello" {
		t.Fatalf("text = %q", run.text)
	}
	vis := openCodeVisibleMessages(nil, run)
	if len(vis) != 2 || vis[0].Text != "hi" || vis[1].Text != "hello" {
		t.Fatalf("visible = %+v", vis)
	}
	// Stale turn and unknown session are ignored.
	if a.handleOpenCodeRunEvent(&openCodeRunEvent{sessionID: "s1", turnID: "old", kind: openCodeRunDelta, text: "X"}) ||
		a.handleOpenCodeRunEvent(&openCodeRunEvent{sessionID: "other", turnID: "t1", kind: openCodeRunDelta, text: "X"}) {
		t.Fatal("stale event accepted")
	}
	if run.text != "hello" {
		t.Fatalf("stale event mutated run: %q", run.text)
	}
	// Done clears the run; later events for it are stale.
	a.handleOpenCodeRunEvent(&openCodeRunEvent{sessionID: "s1", turnID: "t1", kind: openCodeRunDone, status: "Turn completed.", retryText: "hi"})
	if a.openCodeRunFor("s1") != nil || a.openCode.panelStatus != "Turn completed." {
		t.Fatal("done not applied")
	}
	if a.handleOpenCodeRunEvent(&openCodeRunEvent{sessionID: "s1", turnID: "t1", kind: openCodeRunDelta, text: "late"}) {
		t.Fatal("late delta accepted")
	}
}

func TestRunEventsDoNotCrossSessions(t *testing.T) {
	a := newRunTestApp(t)
	a.openCode.selectedSessionID = "s2"
	r1 := addRun(a, "s1", "t1", "a")
	addRun(a, "s2", "t2", "b")
	a.handleOpenCodeRunEvent(&openCodeRunEvent{sessionID: "s1", turnID: "t1", kind: openCodeRunDelta, text: "one"})
	if r1.text != "one" {
		t.Fatal("background delta lost")
	}
	if a.openCode.runs["s2"].text != "" {
		t.Fatal("delta leaked to another session")
	}
	a.openCode.panelOpen = true
	if v := a.openCodePanelView(); v.Running || v.Messages != nil && strings.Contains(v.Messages[len(v.Messages)-1].Text, "one") {
		// s2 selected with a (non-workspace) panel: just ensure no leak of s1 text
		for _, m := range v.Messages {
			if m.Text == "one" {
				t.Fatal("s1 text shown in s2 view")
			}
		}
	}
}

func TestApprovalAndStop(t *testing.T) {
	a := newRunTestApp(t)
	a.openCode.selectedSessionID = "s1"
	run := addRun(a, "s1", "t1", "run it")
	ap := &openCodeApproval{req: agent.ApprovalRequest{SessionID: "s1", TurnID: "t1", CallID: "c1", Name: "run_shell", Summary: "Run shell command:\nls", WorktreeRoot: "/wt"}, decide: make(chan bool, 1)}
	a.handleOpenCodeRunEvent(&openCodeRunEvent{sessionID: "s1", turnID: "t1", kind: openCodeRunApproval, approval: ap})
	v := openCodePanelApprovalFor(run)
	if v == nil || v.Directory != "/wt" || !strings.Contains(v.Command, "ls") || !strings.Contains(v.Notice, "user's filesystem and network access") {
		t.Fatalf("approval view = %+v", v)
	}
	// Stale call ID does not resolve.
	_ = a.resolveOpenCodeApproval("other", true)
	select {
	case <-ap.decide:
		t.Fatal("stale approval resolved")
	default:
	}
	_ = a.resolveOpenCodeApproval("c1", true)
	if got := <-ap.decide; !got || run.approval != nil {
		t.Fatal("approve failed")
	}
	// Stop denies a pending approval and cancels the run.
	ap2 := &openCodeApproval{req: agent.ApprovalRequest{CallID: "c2"}, decide: make(chan bool, 1)}
	run.approval = ap2
	_ = a.interruptOpenCodeSession()
	if got := <-ap2.decide; got {
		t.Fatal("stop must deny")
	}
	if !run.stopping {
		t.Fatal("run not stopping")
	}
	// Dismissal (hide) denies.
	ap3 := &openCodeApproval{req: agent.ApprovalRequest{CallID: "c3"}, decide: make(chan bool, 1)}
	run.approval = ap3
	a.denyOpenCodeApprovals()
	if got := <-ap3.decide; got {
		t.Fatal("dismiss must deny")
	}
}

func TestResolveSessionModelRejectsIneligible(t *testing.T) {
	good := opencode.ConsoleModel{ID: "m", ProviderID: "p", Type: "chat", ToolsKnown: true, ToolsSupported: true,
		APIs: map[string]opencode.ConsoleModelAPI{"chat-completions": {BaseURL: "https://x.example/v1"}}}
	bad := good
	bad.ID, bad.ToolsSupported = "n", false
	if _, err := resolveOpenCodeSessionModel([]opencode.ConsoleModel{good, bad}, agentstore.Session{ProviderID: "p", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveOpenCodeSessionModel([]opencode.ConsoleModel{good, bad}, agentstore.Session{Model: "p/n"}); err == nil {
		t.Fatal("ineligible model accepted")
	}
	if _, err := resolveOpenCodeSessionModel([]opencode.ConsoleModel{good}, agentstore.Session{}); err == nil {
		t.Fatal("missing model accepted")
	}
}

func TestTranscriptProjectionInterruptedAndInspection(t *testing.T) {
	ctx := context.Background()
	store, err := agentstore.Open(ctx, filepath.Join(t.TempDir(), "s.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if _, err := store.CreateSession(ctx, agentstore.Session{ID: "s1", WorkspaceRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginTurn(ctx, agentstore.Turn{SessionID: "s1", ID: "t1", UserMessageID: "t1-user", ProviderID: "p", ModelID: "m", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	_ = store.SaveTurnMessage(ctx, agentstore.TurnMessage{ID: "t1-user", SessionID: "s1", TurnID: "t1", Role: "user", CreatedAt: now})
	_ = store.SaveMessagePart(ctx, agentstore.MessagePart{SessionID: "s1", MessageID: "t1-user", ID: "text", Kind: agentstore.PartText, Text: "do it", Status: agentstore.PartComplete, UpdatedAt: now})
	_ = store.SaveTurnMessage(ctx, agentstore.TurnMessage{ID: "t1-a0", SessionID: "s1", TurnID: "t1", Role: "assistant", CreatedAt: now})
	if _, err := store.SaveToolCall(ctx, agentstore.ToolCall{SessionID: "s1", ID: "c1", MessageID: "t1-a0", TurnID: "t1", Ordinal: -1, ToolName: "run_shell", Input: `{"command":"touch x"}`, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	_ = store.RecordToolApproval(ctx, "s1", "c1", agentstore.ApprovalApproved, now)
	_ = store.BeginToolDispatch(ctx, "s1", "c1", now)
	// Simulated restart.
	report, err := store.RecoverInterrupted(ctx, now)
	if err != nil || report.CallsUnknown != 1 || report.TurnsInterrupted != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	rows, retry, err := loadOpenCodeTranscript(ctx, store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	var tool, notice bool
	for _, r := range rows {
		if r.Kind == "tool" && strings.Contains(r.Note, "Needs inspection") {
			tool = true
		}
		if r.Kind == "notice" && strings.Contains(r.Text, "interrupted") {
			notice = true
		}
	}
	if !tool || !notice || retry != "do it" {
		t.Fatalf("rows=%+v retry=%q", rows, retry)
	}
	if !strings.Contains(openCodeRecoveryNote(report), "unknown outcome") {
		t.Fatal("recovery note missing")
	}
}

func TestRetryNeedsRetryText(t *testing.T) {
	a := newRunTestApp(t)
	a.openCode.selectedSessionID = "s1"
	_ = a.retryOpenCodeTurn()
	if a.openCode.panelStatus != "There is nothing to retry." {
		t.Fatalf("status=%q", a.openCode.panelStatus)
	}
}
