package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agent"
	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

// editThenBlockProvider issues one edit_file call, then blocks until canceled.
type editThenBlockProvider struct {
	mu sync.Mutex
	n  int
}

func (p *editThenBlockProvider) Complete(ctx context.Context, req agent.Request) (agent.Result, error) {
	return p.Stream(ctx, req, func(agent.Event) {})
}

func (p *editThenBlockProvider) Stream(ctx context.Context, _ agent.Request, _ func(agent.Event)) (agent.Result, error) {
	p.mu.Lock()
	p.n++
	n := p.n
	p.mu.Unlock()
	if n == 1 {
		return agent.Result{Status: agent.StatusCompleted, ToolCalls: []agent.ToolCall{
			e2eCall("c1", "edit_file", `{"path":"a.txt","old_string":"one","new_string":"ONE"}`)}}, nil
	}
	<-ctx.Done()
	return agent.Result{Status: agent.StatusInterrupted}, ctx.Err()
}

// Plan 06 gate: a turn canceled after an approved edit never touches the
// checkout; the staged proposal stays reviewable, rejecting it (hunk and whole
// proposal) leaves the file byte-identical, and Drop forgets review state
// without writing any file.
func TestAgentCancelAndRejectLeaveCheckoutUnchanged(t *testing.T) {
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	const base = "one\ntwo\n"
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte(base), 0o644); err != nil {
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
	store, err := agentstore.Open(ctx, filepath.Join(configDir, "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const sid = "s1"
	if _, err := store.CreateSession(ctx, agentstore.Session{
		ID: sid, WorkspaceRoot: canon, WorktreeRoot: wt.Path(), BaselineCommit: wt.baseCommit,
		ProviderID: "p", ModelID: "m", Model: "p/m", Title: "gate", Agent: "opencode", Status: agentstore.SessionQueued,
	}); err != nil {
		t.Fatal(err)
	}
	models := []opencode.ConsoleModel{{ID: "m", ProviderID: "p", Type: "chat", ToolsKnown: true, ToolsSupported: true,
		APIs: map[string]opencode.ConsoleModelAPI{"chat-completions": {BaseURL: "https://x.example/v1"}}}}
	results := make(chan openCodeReply, 16)
	params := openCodeTurnParams{store: store, manager: manager, models: models, results: results, lifeCtx: ctx,
		sessionID: sid, turnID: "t1", directory: canon, prompt: "edit", client: &editThenBlockProvider{}, configDir: configDir}

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go executeOpenCodeTurn(cctx, params)
	var staged bool
	var done openCodeRunEvent
	deadline := time.After(45 * time.Second)
loop:
	for {
		select {
		case r := <-results:
			ev := r.run
			if ev == nil {
				continue
			}
			switch ev.kind {
			case openCodeRunApproval:
				ev.approval.resolve(true)
			case openCodeRunProposal:
				staged = true
				cancel() // stop while the provider is blocked on its next step
			case openCodeRunDone:
				done = *ev
				break loop
			}
		case <-deadline:
			t.Fatal("turn did not finish")
		}
	}
	if !staged {
		t.Fatal("no proposal was staged before cancel")
	}
	if !strings.Contains(done.status, "Stopped") {
		t.Fatalf("status = %q", done.status)
	}
	read := func() string { b, _ := os.ReadFile(filepath.Join(repo, "a.txt")); return string(b) }
	if read() != base {
		t.Fatalf("cancel modified the checkout: %q", read())
	}

	// The staged proposal survives cancel and can be reviewed in the app.
	a := newProposalApp(t, repo)
	p := e2eProposal(t, repo, "a.txt", "ONE\ntwo\n")
	a.addAgentProposal("s1", p)
	fr := a.agentReviewRegistry().Get("s1", "a.txt")
	if fr == nil || len(fr.Hunks) == 0 {
		t.Fatal("review state missing")
	}
	if err := a.reviewOpenCodeHunk(agentHunkReject, "a.txt", fr.Hunks[0].ID); err != nil {
		t.Fatal(err)
	}
	if read() != base {
		t.Fatalf("hunk reject modified the file: %q", read())
	}
	_ = a.rejectOpenCodeProposal(openCodeAgentProposalID(p))
	if read() != base {
		t.Fatalf("proposal reject modified the file: %q", read())
	}

	// Drop (cancel/archive) forgets review state and writes nothing.
	a.addAgentProposal("s1", p)
	a.agentReviewRegistry().Drop("s1")
	if a.agentReviewRegistry().Get("s1", "a.txt") != nil || read() != base {
		t.Fatal("drop must forget state and leave the file alone")
	}
}

// Plan 07: the agent chat chord is catalogued, unique among defaults, and
// rebindable.
func TestAgentChatChordCataloguedAndUnique(t *testing.T) {
	def, ok := defaultChordFor(actionAgentChat)
	if !ok || def != "Ctrl+Shift+L" || !keymapActionKnown(actionAgentChat) {
		t.Fatalf("catalog entry missing or fixed: %q %v", def, ok)
	}
	if got := resolveChord(nil, def); len(got) != 1 || got[0] != actionAgentChat {
		t.Fatalf("default chord is shared: %v", got)
	}
	if got := resolveChord(map[string]string{actionAgentChat: "Ctrl+Shift+J"}, def); len(got) != 0 {
		t.Fatalf("old chord still resolves after rebind: %v", got)
	}
}

// Plan 02: the editor session file holds only layout/navigation state. No
// field may be able to carry a transcript, credential, or chat content.
func TestSessionStateHasNoTranscriptOrCredentialFields(t *testing.T) {
	typ := reflect.TypeOf(sessionState{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := strings.ToLower(f.Tag.Get("json") + " " + f.Name)
		for _, bad := range []string{"transcript", "message", "chat", "prompt", "agent", "opencode", "apikey", "api_key", "secret", "token", "credential", "password", "key"} {
			if strings.Contains(tag, bad) {
				t.Errorf("sessionState field %s (%s) looks like chat/credential data", f.Name, tag)
			}
		}
	}
	// A populated session round-trips without any chat text appearing.
	data, err := json.Marshal(sessionState{Root: "/w", Tabs: []string{"a.go"}})
	if err != nil || strings.Contains(strings.ToLower(string(data)), "bearer") {
		t.Fatalf("unexpected session JSON %s %v", data, err)
	}
}

// The dragged dock width persists through the existing settings file and
// survives a reload; out-of-range stored values fall back to the default.
func TestDockWidthPersistsInSettings(t *testing.T) {
	a := newProposalApp(t, t.TempDir())
	a.sessionReady = true
	a.openCode.dockWidth = 512
	a.persistOpenCodeDockWidth()
	got, err := loadSettings(a.settingsPath)
	if err != nil || got.OpenCode == nil || got.OpenCode.DockWidth != 512 {
		t.Fatalf("not persisted: %+v %v", got.OpenCode, err)
	}
	a.openCode.dockWidth = 0 // a fresh start reads the stored value
	a.openCode.panelOpen = true
	if w, want := a.agentDockWidth(), min(a.zx(512), a.agentDockMax()); w != want {
		t.Fatalf("dock width after restart = %d, want %d", w, want)
	}
	if bad := (&opencodeSettings{DockWidth: 99999}).sanitize(); bad.DockWidth != 0 {
		t.Fatalf("absurd width kept: %d", bad.DockWidth)
	}
}

// Per-session continue state reflects the current key and model.
func TestSessionContinueState(t *testing.T) {
	a := newProposalApp(t, t.TempDir())
	a.openCode.connected = false
	if got := a.openCodeSessionContinue("s1"); !strings.HasPrefix(got, "Cannot continue: no API key") {
		t.Fatalf("offline: %q", got)
	}
	a.openCode.connected = true
	if got := a.openCodeSessionContinue("s1"); !strings.Contains(got, "choose a model") {
		t.Fatalf("no model: %q", got)
	}
	a.settings.OpenCode = &opencodeSettings{ProviderID: "p", ModelID: "m"}
	if got := a.openCodeSessionContinue("s1"); !strings.Contains(got, "unavailable") {
		t.Fatalf("missing from catalog: %q", got)
	}
	a.openCode.consoleModels = []opencode.ConsoleModel{{ID: "m", ProviderID: "p", Type: "chat", ToolsKnown: true, ToolsSupported: true,
		APIs: map[string]opencode.ConsoleModelAPI{"chat-completions": {BaseURL: "https://x.example/v1"}}}}
	if got := a.openCodeSessionContinue("s1"); !strings.HasPrefix(got, "Can continue") {
		t.Fatalf("available: %q", got)
	}
}

// Forget drops the key reference so a finished client cannot send it again.
func TestClientForgetDropsKey(t *testing.T) {
	c, err := agent.NewClient("sk-test-1234567890", agent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c.Forget()
	if _, err := c.Complete(context.Background(), agent.Request{}); err == nil {
		t.Fatal("forgotten client must refuse to send")
	}
	cc, err := opencode.NewConsoleClient("sk-test-1234567890")
	if err != nil {
		t.Fatal(err)
	}
	cc.Forget()
	if _, err := cc.Models(context.Background()); err == nil {
		t.Fatal("forgotten console client must refuse to send")
	}
}

// Plan 05: the Git diff staleness check is deferred off the UI thread. Compose
// no longer runs git; the returned recheck does, and notices a changed tree.
func TestDiffRecheckDeferredOffUIThread(t *testing.T) {
	a := mentionTestApp(t, map[string]string{"a.go": "one\n"})
	root, _ := a.openCodeChipRoot()
	e2eGit(t, root, "init", "-q")
	e2eGit(t, root, "add", "-A", "-f")
	e2eGit(t, root, "commit", "-q", "-m", "i")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chip, err := a.captureOpenCodeDiffChip(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.addOpenCodeChip(chip); err != nil {
		t.Fatal(err)
	}
	// Compose must succeed without spawning git; the recheck does that work.
	text, recheck, err := a.composeOpenCodeUserTextAsync("go")
	if err != nil || recheck == nil || !strings.Contains(text, "+two") {
		t.Fatalf("compose: %v recheck=%v", err, recheck != nil)
	}
	if why := recheck(); why != "" {
		t.Fatalf("unchanged tree reported stale: %s", why)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("three\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if why := recheck(); !strings.Contains(why, "stale") {
		t.Fatalf("changed tree not detected: %q", why)
	}
}
