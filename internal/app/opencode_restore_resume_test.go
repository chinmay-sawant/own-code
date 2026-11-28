package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agent"
	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

// recordingProvider captures each request and replays scripted results.
type recordingProvider struct {
	e2eProvider
	rmu  sync.Mutex
	reqs []agent.Request
}

func (p *recordingProvider) Stream(ctx context.Context, req agent.Request, emit func(agent.Event)) (agent.Result, error) {
	p.rmu.Lock()
	p.reqs = append(p.reqs, req)
	p.rmu.Unlock()
	return p.e2eProvider.Stream(ctx, req, emit)
}

// TestOfflineRestoreAndResume closes and reopens the store with no key and no
// network, renders the session list and transcript from SQLite, then resumes
// the session: the request is rebuilt from persisted history after the model
// check, and a call that was in flight at "crash" time is never re-run and
// needs a fresh approval for anything new.
func TestOfflineRestoreAndResume(t *testing.T) {
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644); err != nil {
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
		t.Fatal(err)
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
	const sid = "restore-1"
	if _, err := store.CreateSession(ctx, agentstore.Session{
		ID: sid, WorkspaceRoot: canon, WorktreeRoot: wt.Path(), BaselineCommit: wt.baseCommit,
		ProviderID: "p", ModelID: "m", Model: "p/m", Title: "restore me", Agent: "opencode",
		Status: agentstore.SessionQueued,
	}); err != nil {
		t.Fatal(err)
	}
	models := []opencode.ConsoleModel{{ID: "m", ProviderID: "p", Type: "chat", ToolsKnown: true, ToolsSupported: true,
		APIs: map[string]opencode.ConsoleModelAPI{"chat-completions": {BaseURL: "https://x.example/v1"}}}}
	results := make(chan openCodeReply, 16)
	params := func(st *agentstore.Store, ms []opencode.ConsoleModel, turn, prompt string, prov agent.Provider) openCodeTurnParams {
		return openCodeTurnParams{store: st, manager: manager, models: ms, results: results, lifeCtx: ctx,
			sessionID: sid, turnID: turn, directory: canon, prompt: prompt, client: prov, configDir: configDir}
	}

	// Turn 1 completes with a final answer.
	first := &e2eProvider{steps: []agent.Result{{Status: agent.StatusCompleted, Text: "first answer"}}}
	done, _ := e2eDrive(t, results, params(store, models, "turn-1", "first question", first), ctx, nil, nil, nil)
	if !strings.Contains(done.status, "completed") {
		t.Fatalf("turn 1 status = %q", done.status)
	}
	// Turn 2 dies mid-dispatch of a shell call ("process killed").
	if _, err := store.BeginTurn(ctx, agentstore.Turn{SessionID: sid, ID: "turn-2", UserMessageID: "m2", ProviderID: "p", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTurnMessage(ctx, agentstore.TurnMessage{ID: "m2a", SessionID: sid, TurnID: "turn-2", Role: "assistant"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveToolCall(ctx, agentstore.ToolCall{SessionID: sid, ID: "dead", MessageID: "m2a", TurnID: "turn-2", Ordinal: -1, ToolName: "run_shell", Input: `{"command":"true"}`}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordToolApproval(ctx, sid, "dead", agentstore.ApprovalApproved, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginToolDispatch(ctx, sid, "dead", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true

	// Reopen offline: no credential, no catalog, no provider, no network.
	store2, err := agentstore.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store2.Close() }()
	if rep, err := store2.RecoverInterrupted(ctx, time.Now()); err != nil || rep.CallsUnknown != 1 {
		t.Fatalf("recovery = %+v, %v", rep, err)
	}
	list, err := listLocalOpenCodeSessions(ctx, store2, canon)
	if err != nil || len(list) != 1 || list[0].ID != sid || list[0].Title != "restore me" {
		t.Fatalf("session list = %+v, %v", list, err)
	}
	rows, _, err := loadOpenCodeTranscript(ctx, store2, sid)
	if err != nil {
		t.Fatal(err)
	}
	var sawQ, sawA, sawInspect bool
	for _, m := range rows {
		sawQ = sawQ || m.Text == "first question"
		sawA = sawA || m.Text == "first answer"
		sawInspect = sawInspect || (m.ID == "dead" && strings.Contains(m.Note, "Needs inspection"))
	}
	if !sawQ || !sawA || !sawInspect {
		t.Fatalf("offline transcript q=%v a=%v inspect=%v rows=%+v", sawQ, sawA, sawInspect, rows)
	}
	// The offline panel gates new requests instead of failing late.
	a := newRunTestApp(t)
	a.openCode.connected = false
	if msg := a.openCodeSessionContinue(sid); !strings.Contains(msg, "no API key") {
		t.Fatalf("offline continue = %q", msg)
	}

	// A model missing from the catalog rejects the resume before any request.
	stale := &recordingProvider{}
	done, _ = e2eDrive(t, results, params(store2, nil, "turn-3", "hello", stale), ctx, nil, nil, nil)
	if !strings.Contains(done.status, "Model rejected") || len(stale.reqs) != 0 {
		t.Fatalf("stale model: status=%q requests=%d", done.status, len(stale.reqs))
	}

	// Resume with the model available: history is rebuilt, the dead call does
	// not run again, and a new write needs a fresh approval.
	prov := &recordingProvider{e2eProvider: e2eProvider{steps: []agent.Result{
		{Status: agent.StatusCompleted, ToolCalls: []agent.ToolCall{e2eCall("n1", "write_file", `{"path":"new.txt","content":"x\n"}`)}},
		{Status: agent.StatusCompleted, Text: "ok"},
	}}}
	approvals := 0
	done, props := e2eDrive(t, results, params(store2, models, "turn-4", "second question", prov), ctx, nil, nil,
		func(int, agent.ApprovalRequest) bool { approvals++; return false })
	if !strings.Contains(done.status, "completed") {
		t.Fatalf("resume status = %q", done.status)
	}
	if approvals != 1 || len(props) != 0 {
		t.Fatalf("approvals=%d proposals=%d; a new write must ask and a denial must stage nothing", approvals, len(props))
	}
	if len(prov.reqs) == 0 {
		t.Fatal("provider received no request")
	}
	body, _ := json.Marshal(prov.reqs[0].Messages)
	for _, want := range []string{"first question", "first answer", "second question"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("rebuilt request lacks %q: %s", want, body)
		}
	}
	calls, err := store2.ListCallsNeedingInspection(ctx, sid)
	if err != nil || len(calls) != 1 || calls[0].ID != "dead" {
		t.Fatalf("interrupted call must stay unresolved: %+v, %v", calls, err)
	}
}

// TestSessionStateLabels asserts every lifecycle label from the local run
// state. There is no separate queued label: a created session that has not run
// reads as Ready (connected) or Disconnected.
func TestSessionStateLabels(t *testing.T) {
	a := newRunTestApp(t)
	set := func(status agentstore.SessionStatus) {
		a.openCode.allSessions = []agentstore.Session{{ID: "s", Status: status}}
	}
	cases := []struct {
		status    agentstore.SessionStatus
		connected bool
		want      string
	}{
		{agentstore.SessionQueued, true, "Ready"},
		{agentstore.SessionQueued, false, "Disconnected"},
		{agentstore.SessionCompleted, true, "Completed"},
		{agentstore.SessionFailed, true, "Failed"},
		{agentstore.SessionInterrupted, true, "Interrupted"},
		{agentstore.SessionRunning, true, "Interrupted"}, // stale row, no live run
		{agentstore.SessionCompleted, false, "Completed"},
	}
	for _, c := range cases {
		set(c.status)
		if got := a.openCodeSessionState("s", c.connected); got != c.want {
			t.Errorf("status %q connected=%v: got %q want %q", c.status, c.connected, got, c.want)
		}
	}
	run := addRun(a, "s", "t", "hi")
	if got := a.openCodeSessionState("s", true); got != "Running" {
		t.Errorf("live run = %q", got)
	}
	run.stopping = true
	if got := a.openCodeSessionState("s", true); got != "Stopping" {
		t.Errorf("stopping = %q", got)
	}
	run.stopping = false
	run.approval = &openCodeApproval{}
	if got := a.openCodeSessionState("s", true); got != "Waiting for approval" {
		t.Errorf("approval = %q", got)
	}
}

// TestDockMinimumWindowGeometry checks the dock and editor widths over small
// window sizes: the editor keeps its minimum pane width whenever the window
// can hold it, and the dock never drops below its floor or exceeds its max.
func TestDockMinimumWindowGeometry(t *testing.T) {
	a := newRunTestApp(t)
	a.openCode.panelOpen = true
	for _, details := range []bool{false, true} {
		a.details = details
		for _, w := range []int{480, 640, 800, 960, 1100, 1280} {
			a.page.SetSize(w, 600)
			dock := a.agentDockWidth()
			if dock < a.zx(openCodeDockFloor) && dock != a.agentDockMax() {
				t.Errorf("w=%d details=%v: dock %d below floor", w, details, dock)
			}
			if dock > a.agentDockMax() && dock > a.zx(openCodeDockMin) {
				t.Errorf("w=%d details=%v: dock %d above max %d", w, details, dock, a.agentDockMax())
			}
			ed := a.editorWidth()
			if ed < a.zx(paneMinWidth) {
				t.Errorf("w=%d details=%v: editor %d below minimum %d", w, details, ed, a.zx(paneMinWidth))
			}
			// When the window is wide enough the dock plus editor fit exactly.
			avail := w - a.windowControlLeftPx()
			if details {
				avail -= a.zx(detailsWidth)
			}
			if avail >= dock+a.zx(paneMinWidth) && ed != avail-dock {
				t.Errorf("w=%d details=%v: editor %d != avail %d - dock %d", w, details, ed, avail, dock)
			}
		}
	}
}
