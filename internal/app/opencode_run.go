package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agent"
	"github.com/chinmay-sawant/own-code/internal/agentbridge"
	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/agenttools"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

const (
	openCodeApprovalTimeout = 5 * time.Minute
	openCodeApprovalNotice  = "This command runs with your user's filesystem and network access. The worktree directory is not a sandbox."
	openCodeSystemPrompt    = "You are a coding assistant working in an isolated Git worktree of the user's project. Use the provided tools to read and search files. Shell commands require the user's approval and may be denied."
	openCodeToolTextLimit   = 2000
)

type openCodeRunEventKind uint8

const (
	openCodeRunDelta openCodeRunEventKind = iota + 1
	openCodeRunApproval
	openCodeRunDone
)

// openCodeRunEvent is routed by session and turn ID. The UI thread drops any
// event whose run is no longer the live run for that session.
type openCodeRunEvent struct {
	sessionID, turnID string
	kind              openCodeRunEventKind
	text              string
	approval          *openCodeApproval
	messages          []openCodeTranscriptMessage
	retryText         string
	status            string
}

// openCodeRun is the per-session, per-turn run context owned by the UI thread.
type openCodeRun struct {
	sessionID, turnID string
	prompt            string
	cancel            context.CancelFunc
	text              string
	approval          *openCodeApproval
	stopping          bool
}

// openCodeApproval is one pending decision. The runner goroutine blocks on
// decide; the UI thread resolves it exactly once.
type openCodeApproval struct {
	req    agent.ApprovalRequest
	decide chan bool
}

func (ap *openCodeApproval) resolve(allow bool) {
	select {
	case ap.decide <- allow:
	default:
	}
}

func newOpenCodeTurnID() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("could not create a turn ID")
	}
	return "turn-" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (a *App) openCodeRunFor(sessionID string) *openCodeRun {
	if sessionID == "" {
		return nil
	}
	return a.openCode.runs[sessionID]
}

// cancelAllOpenCodeRuns stops every run; pending approvals are denied through
// the canceled context.
func (a *App) cancelAllOpenCodeRuns() {
	for _, run := range a.openCode.runs {
		run.stopping = true
		run.cancel()
	}
}

func (a *App) denyOpenCodeApprovals() {
	for _, run := range a.openCode.runs {
		if run.approval != nil {
			run.approval.resolve(false)
			run.approval = nil
		}
	}
}

func (a *App) resolveOpenCodeApproval(callID string, allow bool) error {
	run := a.openCodeRunFor(a.openCode.selectedSessionID)
	if run == nil || run.approval == nil || run.approval.req.CallID != callID {
		a.openCode.panelStatus = "That approval request is no longer pending."
		a.refresh()
		return nil
	}
	run.approval.resolve(allow)
	run.approval = nil
	if allow {
		a.openCode.panelStatus = "Approved. Running the command…"
	} else {
		a.openCode.panelStatus = "Denied. The command was not run."
	}
	a.refresh()
	return nil
}

func (a *App) interruptOpenCodeSession() error {
	run := a.openCodeRunFor(a.openCode.selectedSessionID)
	if run == nil {
		a.openCode.panelStatus = "There is no active agent turn to stop."
		a.refresh()
		return nil
	}
	run.stopping = true
	if run.approval != nil {
		run.approval.resolve(false)
		run.approval = nil
	}
	run.cancel()
	a.openCode.panelStatus = "Stopping…"
	a.refresh()
	return nil
}

func (a *App) retryOpenCodeTurn() error {
	if a.openCodeRunFor(a.openCode.selectedSessionID) != nil || strings.TrimSpace(a.openCode.retryText) == "" {
		a.openCode.panelStatus = "There is nothing to retry."
		a.refresh()
		return nil
	}
	// A retry is a brand-new turn: a fresh model request, with fresh approval
	// for any tool it asks for. Earlier calls are never re-dispatched.
	return a.submitOpenCodePrompt(a.openCode.retryText)
}

type openCodeTurnParams struct {
	store     *agentstore.Store
	manager   *openCodeWorktreeManager
	models    []opencode.ConsoleModel
	results   chan<- openCodeReply
	lifeCtx   context.Context
	sessionID string
	turnID    string
	directory string
	prompt    string
	// client, when set, replaces the Console client built from the saved key.
	client agent.Provider
}

func (a *App) submitOpenCodePrompt(prompt string) error {
	prompt = strings.TrimSpace(prompt)
	fail := func(msg string) error {
		a.openCode.panelStatus = msg
		a.refresh()
		return nil
	}
	switch {
	case a.ws == nil || a.ws.Sample:
		return fail("Open a project folder to use OpenCode sessions.")
	case a.openCode.selectedSessionID == "":
		return fail("Choose or create a local session first.")
	case !a.openCode.connected:
		return fail("Connect to OpenCode Console in Settings to send a message.")
	case prompt == "":
		return fail("Type a message first.")
	case len(prompt) > 12000:
		return fail("The message is too long.")
	case a.openCodeRunFor(a.openCode.selectedSessionID) != nil:
		return fail("This session already has a running turn.")
	}
	if err := a.ensureOpenCodeStore(); err != nil {
		return fail("Local session history is unavailable.")
	}
	directory, err := canonicalWorkspaceRoot(a.ws.Root)
	if err != nil {
		return fail("Could not resolve the selected workspace.")
	}
	manager, err := a.openCodeWorktreeManager()
	if err != nil {
		return fail("The private OpenCode worktree directory could not be opened.")
	}
	turnID, err := newOpenCodeTurnID()
	if err != nil {
		return fail(err.Error())
	}
	sessionID := a.openCode.selectedSessionID
	runCtx, cancel := context.WithCancel(a.openCode.lifeCtx)
	run := &openCodeRun{sessionID: sessionID, turnID: turnID, prompt: prompt, cancel: cancel}
	if a.openCode.runs == nil {
		a.openCode.runs = map[string]*openCodeRun{}
	}
	a.openCode.runs[sessionID] = run
	a.openCode.retryText = ""
	a.openCode.panelStatus = "Sending to OpenCode Console…"
	a.refresh()
	params := openCodeTurnParams{
		store: a.openCode.store, manager: manager,
		models:  append([]opencode.ConsoleModel(nil), a.openCode.consoleModels...),
		results: a.openCode.results, lifeCtx: a.openCode.lifeCtx,
		sessionID: sessionID, turnID: turnID, directory: directory, prompt: prompt,
		client: a.openCode.testProvider,
	}
	go executeOpenCodeTurn(runCtx, params)
	return nil
}

// executeOpenCodeTurn runs entirely off the render loop. It reports only
// through events keyed by session and turn ID.
func executeOpenCodeTurn(ctx context.Context, p openCodeTurnParams) {
	send := func(sendCtx context.Context, ev openCodeRunEvent) bool {
		ev.sessionID, ev.turnID = p.sessionID, p.turnID
		select {
		case p.results <- openCodeReply{run: &ev}:
			return true
		case <-sendCtx.Done():
			return false
		}
	}
	finish := func(status string) {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		rows, retry, _ := loadOpenCodeTranscript(wctx, p.store, p.sessionID)
		send(p.lifeCtx, openCodeRunEvent{kind: openCodeRunDone, messages: rows, retryText: retry, status: status})
	}

	// Request-time checks. Nothing is persisted if any of them fails.
	lctx, lcancel := context.WithTimeout(ctx, 5*time.Second)
	local, err := findOpenCodeLocalSession(lctx, p.store, p.directory, p.sessionID)
	lcancel()
	if err != nil {
		finish("Could not load the local session.")
		return
	}
	target, err := resolveOpenCodeSessionModel(p.models, local)
	if err != nil {
		finish("Model rejected: " + err.Error())
		return
	}
	if local.WorktreeRoot == "" {
		finish("This session has no isolated worktree. Create a new session in a Git repository to run the agent.")
		return
	}
	wctx, wcancel := context.WithTimeout(ctx, 30*time.Second)
	worktree, err := p.manager.OpenExisting(wctx, p.directory, local.WorktreeRoot, local.BaselineCommit)
	wcancel()
	if err != nil {
		finish("The isolated worktree could not be verified.")
		return
	}
	root, err := filepath.EvalSymlinks(worktree.WorkspaceRoot())
	if err != nil {
		finish("The isolated worktree could not be resolved.")
		return
	}
	registry, err := agentbridge.NewRegistry(root)
	if err != nil {
		finish("Agent tools could not be prepared for the worktree.")
		return
	}
	provider := p.client
	if provider == nil {
		key, kerr := loadOpenCodeConsoleAPIKey()
		if kerr != nil {
			finish("Could not read the OpenCode Console API key.")
			return
		}
		client, cerr := agent.NewClient(key, agent.Options{})
		if cerr != nil {
			finish("The OpenCode Console API key is invalid.")
			return
		}
		provider = client
	}
	ask := func(actx context.Context, req agent.ApprovalRequest) (bool, error) {
		ap := &openCodeApproval{req: req, decide: make(chan bool, 1)}
		tctx, tcancel := context.WithTimeout(context.WithoutCancel(actx), 3*time.Second)
		rows, _, _ := loadOpenCodeTranscript(tctx, p.store, p.sessionID)
		tcancel()
		if !send(actx, openCodeRunEvent{kind: openCodeRunApproval, approval: ap, messages: rows}) {
			return false, actx.Err()
		}
		timer := time.NewTimer(openCodeApprovalTimeout)
		defer timer.Stop()
		select {
		case allow := <-ap.decide:
			return allow, nil
		case <-actx.Done():
			return false, actx.Err()
		case <-timer.C:
			return false, errors.New("approval timed out")
		}
	}
	runner, err := agent.NewRunner(agent.Config{
		Store: p.store, Provider: provider, Tools: registry,
		Approver:    agentbridge.NewPolicyApprover(agenttools.DefaultPolicy(), ask),
		BindContext: agentbridge.BindContext,
		System:      openCodeSystemPrompt,
	})
	if err != nil {
		finish("The agent runner could not be created.")
		return
	}
	setSessionStatus := func(status agentstore.SessionStatus) {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		update := local
		update.Status = status
		_, _ = p.store.UpdateSessionMetadata(sctx, update)
	}
	setSessionStatus(agentstore.SessionRunning)
	outcome, runErr := runner.Run(ctx, agent.RunInput{
		SessionID: p.sessionID, TurnID: p.turnID, UserText: p.prompt,
		Model: target, WorktreeRoot: root,
		Events: func(e agent.Event) {
			if e.Kind != agent.EventTextDelta || e.Text == "" {
				return
			}
			send(ctx, openCodeRunEvent{kind: openCodeRunDelta, text: e.Text})
		},
	})
	status, session := "Turn completed.", agentstore.SessionCompleted
	switch {
	case runErr != nil && outcome.Status == "":
		status, session = "The turn could not be started.", agentstore.SessionFailed
	case outcome.Status == agentstore.TurnInterrupted:
		status, session = "Stopped. The turn was marked interrupted.", agentstore.SessionInterrupted
	case outcome.Status == agentstore.TurnFailed:
		status, session = "The turn failed: "+openCodeOutcomeReason(outcome.Reason), agentstore.SessionFailed
	}
	setSessionStatus(session)
	finish(status)
}

func openCodeOutcomeReason(reason string) string {
	switch reason {
	case "provider_error":
		return "the provider returned an error. Retry sends a new request."
	case "max_steps":
		return "the step limit was reached."
	case "persist_error":
		return "the local history could not be written."
	}
	return "see the transcript."
}

func findOpenCodeLocalSession(ctx context.Context, store *agentstore.Store, directory, sessionID string) (agentstore.Session, error) {
	stored, err := store.ListSessions(ctx, directory)
	if err != nil {
		return agentstore.Session{}, err
	}
	for _, s := range stored {
		if s.ID == sessionID && sameWorkspacePath(s.WorkspaceRoot, directory) {
			return s, nil
		}
	}
	return agentstore.Session{}, agentstore.ErrNotFound
}

// resolveOpenCodeSessionModel re-checks eligibility against the live catalog
// at request time. Routing uses catalog metadata, never the model name.
func resolveOpenCodeSessionModel(models []opencode.ConsoleModel, local agentstore.Session) (agent.ModelTarget, error) {
	providerID, modelID := local.ProviderID, local.ModelID
	if providerID == "" || modelID == "" {
		providerID, modelID, _ = strings.Cut(local.Model, "/")
	}
	if providerID == "" || modelID == "" {
		return agent.ModelTarget{}, errors.New("the session has no selected model")
	}
	for _, m := range agent.FilterEligible(models) {
		if m.ProviderID == providerID && m.ID == modelID {
			return agent.ResolveModel(m)
		}
	}
	return agent.ModelTarget{}, errors.New("the selected model is not eligible for agent runs in the current catalog")
}

// handleOpenCodeRunEvent applies one routed event on the UI thread and reports
// whether the panel changed. Events for a replaced or finished turn are stale.
func (a *App) handleOpenCodeRunEvent(ev *openCodeRunEvent) bool {
	run := a.openCode.runs[ev.sessionID]
	if run == nil || run.turnID != ev.turnID {
		return false
	}
	selected := ev.sessionID == a.openCode.selectedSessionID
	switch ev.kind {
	case openCodeRunDelta:
		run.text += ev.text
		if selected {
			a.openCode.panelStatus = "Streaming response…"
		}
	case openCodeRunApproval:
		run.approval = ev.approval
		if selected {
			if ev.messages != nil {
				a.openCode.messages = ev.messages
			}
			a.openCode.panelStatus = "Approval required before a command runs."
		}
	case openCodeRunDone:
		run.cancel()
		delete(a.openCode.runs, ev.sessionID)
		if selected {
			if ev.messages != nil {
				a.openCode.messages = ev.messages
			}
			a.openCode.retryText = ev.retryText
			a.openCode.panelStatus = ev.status
		}
	default:
		return false
	}
	return true
}

// loadOpenCodeTranscript projects the persisted history into panel rows,
// including tool activity, interrupted turns, and calls needing inspection.
func loadOpenCodeTranscript(ctx context.Context, store *agentstore.Store, sessionID string) ([]openCodeTranscriptMessage, string, error) {
	if store == nil {
		return nil, "", errors.New("OpenCode local session store is unavailable")
	}
	history, err := store.LoadHistory(ctx, sessionID)
	if err != nil {
		return nil, "", err
	}
	turns, err := store.ListTurns(ctx, sessionID)
	if err != nil {
		return nil, "", err
	}
	inspect := map[string]bool{}
	if calls, err := store.ListCallsNeedingInspection(ctx, sessionID); err == nil {
		for _, c := range calls {
			inspect[c.ID] = true
		}
	}
	rows := make([]openCodeTranscriptMessage, 0, len(history))
	for _, m := range history {
		var text strings.Builder
		interrupted := false
		var toolRows []openCodeTranscriptMessage
		for _, part := range m.Parts {
			switch part.Kind {
			case agentstore.PartText:
				text.WriteString(part.Text)
				interrupted = interrupted || part.Status == agentstore.PartInterrupted
			case agentstore.PartToolCall:
				if part.ToolCall != nil {
					toolRows = append(toolRows, openCodeToolRow(*part.ToolCall, inspect[part.ToolCall.ID], m.CreatedAt))
				}
			}
		}
		if strings.TrimSpace(text.String()) != "" {
			row := openCodeTranscriptMessage{ID: m.ID, Role: m.Role, Text: text.String(), CreatedAt: m.CreatedAt, TurnID: m.TurnID, Kind: "text"}
			if interrupted {
				row.Note = "Interrupted. This output is partial."
			}
			rows = append(rows, row)
		}
		rows = append(rows, toolRows...)
	}
	retry := ""
	for i, turn := range turns {
		var notice string
		switch turn.Status {
		case agentstore.TurnInterrupted:
			notice = "Turn interrupted. Nothing was replayed automatically."
		case agentstore.TurnFailed:
			notice = "Turn failed."
			if e := strings.TrimSpace(turn.Error); e != "" {
				notice += " " + truncateOpenCodeText(e, 300)
			}
		}
		if i == len(turns)-1 && notice != "" {
			for _, m := range history {
				if m.TurnID == turn.ID && m.Role == "user" {
					retry = openCodeHistoryText(m)
				}
			}
		}
		if notice == "" {
			continue
		}
		insert := len(rows)
		for j := len(rows) - 1; j >= 0; j-- {
			if rows[j].TurnID == turn.ID {
				insert = j + 1
				break
			}
		}
		row := openCodeTranscriptMessage{ID: "notice-" + turn.ID, Role: "system", Text: notice, TurnID: turn.ID, Kind: "notice"}
		rows = append(rows, openCodeTranscriptMessage{})
		copy(rows[insert+1:], rows[insert:])
		rows[insert] = row
	}
	return rows, retry, nil
}

func openCodeHistoryText(m agentstore.HistoryMessage) string {
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Kind == agentstore.PartText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func openCodeToolRow(call agentstore.ToolCall, needsInspection bool, at time.Time) openCodeTranscriptMessage {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s]", call.ToolName, call.Status)
	if call.Approval != "" {
		fmt.Fprintf(&b, " approval: %s", call.Approval)
	}
	if call.Input != "" {
		b.WriteString("\n" + truncateOpenCodeText(call.Input, openCodeToolTextLimit))
	}
	if call.Result != "" {
		b.WriteString("\n---\n" + truncateOpenCodeText(call.Result, openCodeToolTextLimit))
	}
	row := openCodeTranscriptMessage{ID: call.ID, Role: "tool", Text: b.String(), CreatedAt: at, TurnID: call.TurnID, Kind: "tool"}
	if needsInspection {
		row.Note = "Needs inspection: the outcome is not certain. Check the worktree before trusting it. It will not be retried; a retry needs a new request and fresh approval."
	}
	return row
}

func truncateOpenCodeText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// openCodePanelApprovalFor builds the native approval view for a session.
func openCodePanelApprovalFor(run *openCodeRun) *openCodePanelApproval {
	if run == nil || run.approval == nil {
		return nil
	}
	req := run.approval.req
	return &openCodePanelApproval{
		CallID: req.CallID, Tool: req.Name, Command: req.Summary,
		Directory: req.WorktreeRoot, Notice: openCodeApprovalNotice,
	}
}
