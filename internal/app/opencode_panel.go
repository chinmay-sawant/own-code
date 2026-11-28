package app

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

type openCodePanelView struct {
	Open               bool
	Busy               bool
	CanInterrupt       bool
	CanPrompt          bool
	WorkspaceAvailable bool
	Connected          bool
	Status             string
	Sessions           []openCodePanelSessionRow
	Messages           []openCodePanelMessageRow
	SelectedID         string
	// Composer attachments (opencode_mentions.go).
	Mentions        []openCodeMentionRow
	Chips           []openCodeChipRow
	ComposerPreview string
	CanReview       bool
	Proposals       []openCodePanelProposalRow
	AgentProposals  []openCodePanelAgentProposalRow
	Inline          *openCodeInlineCard
	InlineChips     []openCodeChipRow
	Running         bool
	CanRetry        bool
	Approval        *openCodePanelApproval
	// Notice is the offline / missing credential or model indicator for new
	// requests; cached history stays visible regardless.
	Notice        string
	Offline       bool
	OtherSessions []openCodePanelOtherRow
	Archived      []openCodePanelSessionRow
	PendingSwitch *openCodePanelSwitch
	TranscriptTop int
	// Docked chat chrome.
	ProviderName  string
	SessionTitle  string
	DockWidth     int
	HistoryOpen   bool
	HistoryTop    int
	ModelMenuOpen bool
	ModelMenuTop  int
	ModelScroll   bool
	ModelRailLeft int
	ModelRailTop  int
	ModelTrackH   int
	ModelThumbTop int
	ModelThumbH   int
	ModelLabel    string
	ModelState    string
	ModelNote     string
	ModelGroups   []openCodeModelGroupRow
}

// openCodePanelOtherRow is a saved session from a different workspace.
type openCodePanelOtherRow struct {
	ID, Title, Root string
}

// openCodePanelSwitch is the visible workspace-switch confirmation.
type openCodePanelSwitch struct {
	SessionID, Title, Root string
}

// openCodePanelApproval is the native approval view for one pending call.
type openCodePanelApproval struct {
	CallID    string
	Tool      string
	Command   string
	Directory string
	Notice    string
}

type openCodePanelSessionRow struct {
	ID     string
	Title  string
	Active bool
	Status string
	// State is the lifecycle label (Ready, Running, Completed, Failed,
	// Interrupted, Disconnected). Renaming shows the inline title editor.
	State    string
	Renaming bool
	// Continue says whether a new turn can use the current key and model.
	Continue    string
	CanContinue bool
}

type openCodePanelMessageRow struct {
	ID   string
	Role string
	Text string
	Note string
	// Body is the escaped HTML for the message (Markdown subset for the
	// assistant, preformatted text otherwise). Produced by this package only.
	Body template.HTML
}

type openCodePanelProposalRow struct {
	Path   string
	Change string
	Diff   string
}

func (a *App) openCodePanelView() openCodePanelView {
	v := openCodePanelView{
		Open:               a.openCode.panelOpen,
		Busy:               a.openCode.operation >= openCodeOperationSessions,
		WorkspaceAvailable: a.ws != nil && !a.ws.Sample,
		Connected:          a.openCode.connected,
		Status:             a.openCode.panelStatus,
		SelectedID:         a.openCode.selectedSessionID,
		CanReview:          a.openCode.reviewable && a.openCode.selectedSessionID != "" && a.openCode.operation == openCodeOperationNone,
	}
	run := a.openCodeRunFor(v.SelectedID)
	v.Running = run != nil
	v.CanInterrupt = v.Running
	v.CanPrompt = v.WorkspaceAvailable && v.Connected && !v.Running
	v.CanRetry = !v.Running && v.SelectedID != "" && v.Connected && a.openCode.retryText != "" && !v.Busy
	v.Approval = openCodePanelApprovalFor(run)
	v.Offline = !v.Connected
	v.Notice = a.openCodeRequestNotice()
	if sw := a.openCode.pendingSwitch; sw != nil {
		v.PendingSwitch = &openCodePanelSwitch{SessionID: sw.SessionID, Title: sw.Title, Root: sw.Root}
	}
	v.TranscriptTop = -a.openCode.scroll.offset()
	if !v.WorkspaceAvailable {
		v.Status = "Open a project folder to use OpenCode sessions."
	} else if v.Status == "" {
		if v.SelectedID != "" {
			v.Status = "Ready"
			if !v.Connected {
				v.Status = "Saved history · offline"
			}
		} else if !v.Connected {
			v.Status = "Connect in Settings to chat"
		} else {
			v.Status = "Ready to chat"
		}
	}
	for _, session := range a.openCode.sessions {
		if a.ws.Sample || !sameOpenCodeWorkspace(session.Location.Directory, a.ws.Root) {
			continue
		}
		title := strings.TrimSpace(session.Title)
		if title == "" {
			title = "Untitled session"
		}
		state := a.openCodeSessionState(session.ID, v.Connected)
		cont := a.openCodeSessionContinue(session.ID)
		status := state
		if !v.Connected {
			status = "Disconnected · saved history"
		}
		v.Sessions = append(v.Sessions, openCodePanelSessionRow{
			ID:       session.ID,
			Title:    title,
			Active:   session.ID == v.SelectedID,
			Status:   status,
			State:    state,
			Renaming: session.ID == a.openCode.renamingID,
			Continue: cont, CanContinue: strings.HasPrefix(cont, "Can "),
		})
	}
	v.OtherSessions = a.openCodeOtherWorkspaceSessions()
	v.Archived = a.openCodeArchivedSessions()
	for _, message := range openCodeVisibleMessages(a.openCode.messages, run) {
		if strings.TrimSpace(message.Text) == "" || (message.Role == "turn" && message.Text == "") {
			continue
		}
		displayText := message.Text
		note := message.Note
		if message.Role == "user" {
			if summary := openCodeAttachmentSummary(displayText); summary != "" {
				displayText = strings.TrimSpace(openCodeTypedPart(displayText))
				if note != "" {
					note = summary + " · " + note
				} else {
					note = summary
				}
			}
		}
		body := openCodePlainBody(displayText)
		if message.Role == "assistant" {
			body = renderOpenCodeMarkdown(displayText)
		}
		v.Messages = append(v.Messages, openCodePanelMessageRow{
			ID:   message.ID,
			Role: message.Role,
			Text: displayText,
			Note: note,
			Body: body,
		})
	}
	for _, proposal := range a.openCode.proposals {
		v.Proposals = append(v.Proposals, openCodePanelProposalRow{
			Path: proposal.Path, Change: proposal.Change, Diff: proposal.Diff,
		})
	}
	v.AgentProposals = a.agentProposalRows(v.SelectedID)
	v.Inline, v.InlineChips = a.openCodeInlineView()
	a.fillOpenCodeChrome(&v)
	a.openCodeComposerView(&v)
	return v
}

// openCodeVisibleMessages overlays the live turn on the persisted transcript:
// the turn's persisted assistant text, tool rows, and summary are replaced by
// the live segments (text and tool rows in the order they happened), and the
// user message is shown immediately.
func openCodeVisibleMessages(persisted []openCodeTranscriptMessage, run *openCodeRun) []openCodeTranscriptMessage {
	if run == nil {
		return persisted
	}
	out := make([]openCodeTranscriptMessage, 0, len(persisted)+len(run.segs)+2)
	hasUser := false
	for _, m := range persisted {
		if m.TurnID == run.turnID {
			if m.Role == "user" {
				hasUser = true
			} else {
				continue
			}
		}
		out = append(out, m)
	}
	if !hasUser {
		out = append(out, openCodeTranscriptMessage{ID: "live-user-" + run.turnID, Role: "user", Text: run.prompt, TurnID: run.turnID, Kind: "text"})
	}
	for i, seg := range run.segs {
		if seg.toolID != "" {
			out = append(out, openCodeTranscriptMessage{ID: "live-tool-" + seg.toolID, Role: "tool", Text: seg.toolName + " [" + seg.status + "]", TurnID: run.turnID, Kind: "tool"})
			continue
		}
		out = append(out, openCodeTranscriptMessage{ID: fmt.Sprintf("live-%s-%d", run.turnID, i), Role: "assistant", Text: seg.text, TurnID: run.turnID, Kind: "text"})
	}
	if !run.startedAt.IsZero() {
		state := "Running"
		if run.approval != nil {
			state = "Waiting for approval"
		} else if run.stopping {
			state = "Stopping"
		}
		out = append(out, openCodeTranscriptMessage{ID: "live-meta-" + run.turnID, Role: "turn", Kind: "turn", TurnID: run.turnID,
			Text: openCodeTurnLine(run.provider, run.model, state, run.startedAt, time.Now())})
	}
	return out
}

func openCodeWorkspaceSessions(sessions []opencode.Session, root string) []opencode.Session {
	if strings.TrimSpace(root) == "" || root == "sample" {
		return nil
	}
	rows := make([]opencode.Session, 0, len(sessions))
	for _, session := range sessions {
		if session.ID == "" || strings.TrimSpace(session.Location.Directory) == "" {
			continue
		}
		if !sameOpenCodeWorkspace(session.Location.Directory, root) {
			continue
		}
		rows = append(rows, session)
	}
	return rows
}

func sameOpenCodeWorkspace(left, right string) bool {
	leftRoot, leftErr := canonicalWorkspaceRoot(left)
	rightRoot, rightErr := canonicalWorkspaceRoot(right)
	return leftErr == nil && rightErr == nil && sameWorkspacePath(leftRoot, rightRoot)
}

func openCodeLineSpan(start, end int) string {
	if start <= 0 {
		start = 1
	}
	if end < start {
		end = start
	}
	if start == end {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "-" + strconv.Itoa(end)
}

func (a *App) currentOpenCodeContext() (agentContextSnapshot, error) {
	if a == nil || a.ws == nil || a.ws.Sample || !a.editing || a.edit == nil {
		return agentContextSnapshot{}, errors.New("Enter edit mode and select text or place the caret in a workspace file first.")
	}
	path := a.editPath()
	if path == "" {
		return agentContextSnapshot{}, errors.New("There is no active editor buffer to use as context.")
	}
	return captureAgentContext(a.ws.Root, path, a.edit.Text(), a.editAnchor, a.editCaret)
}

func (a *App) opencodePanelAction(ctx context.Context, action string) error {
	if strings.HasPrefix(action, "opencode-session-pick:") || action == "opencode-session-new" || action == "opencode-panel-hide" {
		a.openCode.historyOpen, a.openCode.modelMenuOpen = false, false
	}
	switch {
	case strings.HasPrefix(action, "opencode-panel-model-") || strings.HasPrefix(action, "opencode-panel-history-"):
		return a.openCodeChromeAction(action)
	case action == "opencode-panel-toggle":
		if a.openCode.panelOpen {
			a.denyOpenCodeApprovals()
			a.openCode.panelOpen = false
			a.refresh()
			return nil
		}
		return a.openOpenCodePanel(ctx)
	case action == "opencode-panel-open":
		return a.openOpenCodePanel(ctx)
	case action == "opencode-panel-hide":
		a.denyOpenCodeApprovals() // dismissal is denial
		a.openCode.panelOpen = false
		a.refresh()
		return nil
	case strings.HasPrefix(action, "opencode-panel-inline-"):
		return a.openCodeInlineAction(ctx, action)
	case strings.HasPrefix(action, "opencode-panel-mention:"):
		if i, err := strconv.Atoi(strings.TrimPrefix(action, "opencode-panel-mention:")); err == nil {
			return a.pickOpenCodeMention(i)
		}
		return nil
	case strings.HasPrefix(action, "opencode-panel-chip-remove:"):
		a.removeOpenCodeChip(strings.TrimPrefix(action, "opencode-panel-chip-remove:"))
		return nil
	case action == "opencode-panel-refresh":
		return a.loadOpenCodeSessions()
	case action == "opencode-session-new":
		return a.createOpenCodeSession()
	case action == "opencode-session-review":
		return a.reviewOpenCodeProposal()
	case strings.HasPrefix(action, "opencode-session-pick:"):
		return a.selectOpenCodeSession(strings.TrimPrefix(action, "opencode-session-pick:"))
	case strings.HasPrefix(action, "opencode-session-other:"):
		return a.requestOpenCodeWorkspaceSwitch(strings.TrimPrefix(action, "opencode-session-other:"))
	case action == "opencode-switch-confirm":
		return a.confirmOpenCodeWorkspaceSwitch(ctx)
	case action == "opencode-switch-cancel":
		a.cancelOpenCodeWorkspaceSwitch()
		return nil
	case strings.HasPrefix(action, "opencode-session-rename:"):
		a.startOpenCodeRename(strings.TrimPrefix(action, "opencode-session-rename:"))
		return nil
	case strings.HasPrefix(action, "opencode-session-rename-save:"):
		return a.saveOpenCodeRename(strings.TrimPrefix(action, "opencode-session-rename-save:"))
	case action == "opencode-session-rename-cancel":
		a.openCode.renamingID = ""
		a.refresh()
		return nil
	case strings.HasPrefix(action, "opencode-session-restore:"):
		return a.unarchiveOpenCodeSession(strings.TrimPrefix(action, "opencode-session-restore:"))
	case strings.HasPrefix(action, "opencode-session-archive:"):
		return a.archiveOpenCodeSession(strings.TrimPrefix(action, "opencode-session-archive:"))
	case action == "opencode-session-prompt":
		prompt := strings.TrimSpace(a.opencodeCredentialInput("opencode-prompt"))
		a.page.Form().SetFormValue("opencode-prompt", "")
		a.openCode.composer.text = ""
		return a.submitOpenCodePrompt(prompt)
	case action == "opencode-session-interrupt":
		return a.interruptOpenCodeSession()
	case action == "opencode-session-retry":
		return a.retryOpenCodeTurn()
	case strings.HasPrefix(action, "opencode-session-approve:"):
		return a.resolveOpenCodeApproval(strings.TrimPrefix(action, "opencode-session-approve:"), true)
	case strings.HasPrefix(action, "opencode-proposal-accept:"):
		return a.acceptOpenCodeProposal(strings.TrimPrefix(action, "opencode-proposal-accept:"))
	case strings.HasPrefix(action, "opencode-proposal-review:"):
		_, _, e := a.findAgentProposal(strings.TrimPrefix(action, "opencode-proposal-review:"))
		if e == nil {
			a.openCode.panelStatus = "That proposal is no longer pending."
			a.refresh()
			return nil
		}
		a.openAgentDiff(e.Proposal.Path)
		return nil
	case strings.HasPrefix(action, "opencode-proposal-reject:"):
		return a.rejectOpenCodeProposal(strings.TrimPrefix(action, "opencode-proposal-reject:"))
	case strings.HasPrefix(action, "opencode-session-deny:"):
		return a.resolveOpenCodeApproval(strings.TrimPrefix(action, "opencode-session-deny:"), false)
	default:
		return nil
	}
}

// actionAgentChat is the keymap action that opens or closes the agent chat.
const actionAgentChat = "agent-chat"

// toggleAgentChat closes the docked chat, or opens it and focuses the composer.
func (a *App) toggleAgentChat(ctx context.Context) error {
	if a.openCode.panelOpen {
		if err := a.opencodePanelAction(ctx, "opencode-panel-hide"); err != nil {
			return err
		}
		return a.page.Redraw(ctx)
	}
	if err := a.openOpenCodePanel(ctx); err != nil {
		return err
	}
	if a.ws != nil && !a.ws.Sample {
		if err := a.focusOpenCodePrompt(ctx); err != nil {
			return err
		}
	}
	return a.page.Redraw(ctx)
}

func (a *App) openOpenCodePanel(ctx context.Context) error {
	a.openCode.panelOpen = true
	a.termFocus = false
	a.openCode.panelStatus = ""
	if a.ws == nil || a.ws.Sample {
		a.openCode.panelStatus = "Open a project folder to use OpenCode sessions."
		a.refresh()
		return nil
	}
	if err := a.ensureOpenCodeStore(); err != nil {
		a.openCode.panelStatus = "Local session history is unavailable."
		a.refresh()
		return nil
	}
	return a.loadOpenCodeSessions()
}

func (a *App) loadOpenCodeCachedSessions() error {
	return a.loadOpenCodeSessions()
}

func (a *App) ensureOpenCodeStore() error {
	if a.openCode.store != nil {
		return nil
	}
	if a.settingsPath == "" {
		path, err := defaultSettingsPath()
		if err != nil {
			return err
		}
		a.settingsPath = path
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	store, err := agentstore.OpenUserConfig(ctx, filepath.Dir(a.settingsPath))
	if err != nil {
		return err
	}
	// This process now owns the profile lock, so unfinished turns from a
	// previous process are safe to finalize. Nothing is replayed.
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	report, err := store.RecoverInterrupted(rctx, time.Now().UTC())
	if err != nil {
		_ = store.Close()
		return err
	}
	a.openCode.recoveryNote = openCodeRecoveryNote(report)
	a.openCode.store = store
	return nil
}

func openCodeRecoveryNote(r agentstore.RecoveryReport) string {
	if r.TurnsInterrupted == 0 && r.CallsInterrupted == 0 && r.CallsUnknown == 0 {
		return ""
	}
	note := fmt.Sprintf("Recovered %d interrupted turn(s).", r.TurnsInterrupted)
	if r.CallsUnknown > 0 {
		note += fmt.Sprintf(" %d tool call(s) have an unknown outcome and need inspection.", r.CallsUnknown)
	}
	return note
}

func (a *App) loadOpenCodeSessions() error {
	if a.ws == nil || a.ws.Sample {
		a.openCode.sessions = nil
		a.openCode.selectedSessionID = ""
		a.openCode.messages = nil
		a.openCode.panelStatus = "Open a project folder to use OpenCode sessions."
		a.refresh()
		return nil
	}
	if err := a.ensureOpenCodeStore(); err != nil {
		a.openCode.panelStatus = "Local session history is unavailable."
		a.refresh()
		return nil
	}
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationSessions)
	store, directory := a.openCode.store, a.ws.Root
	connected, result := a.openCode.connected, a.openCode.results
	recovery := a.openCode.recoveryNote
	a.openCode.panelStatus = "Loading local sessions…"
	a.refresh()
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, 3*time.Second)
		defer cancel()
		sessions, all, err := fetchOpenCodePanelSessions(ctx, store, directory)
		status := "Showing sessions saved in this workspace."
		if !connected {
			status = "OpenCode Console is disconnected. Showing saved local sessions."
		}
		if err != nil {
			status = "Local session history could not be loaded."
		} else if len(sessions) == 0 {
			status = "No local sessions are saved for this workspace."
		}
		if recovery != "" && err == nil {
			status += " " + recovery
		}
		a.sendOpenCodeReply(baseCtx, result, openCodeReply{
			token: token, operation: openCodeOperationSessions,
			sessions: sessions, allSessions: all, setAllSessions: err == nil,
			panelStatus: status, err: err,
		})
	}()
	return nil
}

func (a *App) restorePendingOpenCodePrompt() {
	if a.openCode.pendingPrompt == "" {
		return
	}
	a.openCode.composer.text = a.openCode.pendingPrompt
	if a.page != nil {
		a.page.Form().SetFormValue("opencode-prompt", a.openCode.pendingPrompt)
	}
	a.openCode.pendingPrompt = ""
}

func (a *App) createOpenCodeSession() error {
	if a.ws == nil || a.ws.Sample {
		a.restorePendingOpenCodePrompt()
		a.openCode.panelStatus = "Open a project folder to create an OpenCode session."
		a.refresh()
		return nil
	}
	if !a.openCode.connected || a.openCode.consoleClient == nil {
		a.restorePendingOpenCodePrompt()
		a.openCode.panelStatus = "Connect to OpenCode Console in Settings before creating a session."
		a.refresh()
		return nil
	}
	settings := openCodeSettingsValue(a.settings.OpenCode)
	if _, _, ok := a.findOpenCodeModel(openCodeModelSelection(settings.ProviderID, settings.ModelID)); !ok {
		a.restorePendingOpenCodePrompt()
		a.openCode.panelStatus = "Choose an available model before starting a chat."
		a.refresh()
		return nil
	}
	if err := a.ensureOpenCodeStore(); err != nil {
		a.restorePendingOpenCodePrompt()
		a.openCode.panelStatus = "Local session history is unavailable; a session cannot be saved."
		a.refresh()
		return nil
	}
	directory, err := canonicalWorkspaceRoot(a.ws.Root)
	if err != nil {
		a.restorePendingOpenCodePrompt()
		a.openCode.panelStatus = "Could not resolve the selected workspace for a local session."
		a.refresh()
		return nil
	}
	sessionID, err := newOpenCodeLocalSessionID()
	if err != nil {
		a.restorePendingOpenCodePrompt()
		a.openCode.panelStatus = "Could not create a local OpenCode session ID."
		a.refresh()
		return nil
	}
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationCreateSession)
	store, result := a.openCode.store, a.openCode.results
	manager, _ := a.openCodeWorktreeManager()
	a.openCode.retryText = ""
	a.openCode.reviewable = false
	a.openCode.proposals = nil
	a.openCode.messages = nil
	a.openCode.selectedSessionID = ""
	a.openCode.panelStatus = "Saving a local session to SQLite…"
	a.refresh()
	go func() {
		// Each session gets its own isolated worktree; agent tools never touch
		// the user's checkout. Non-Git folders still get a (history-only) session.
		worktreePath, baseline, worktreeNote := "", "", ""
		if manager != nil {
			wctx, wcancel := context.WithTimeout(baseCtx, 60*time.Second)
			if wt, werr := manager.Create(wctx, directory); werr == nil {
				worktreePath, baseline = wt.Path(), wt.baseCommit
			} else {
				worktreeNote = " No isolated worktree could be created, so the agent cannot run in this session."
			}
			wcancel()
		}
		ctx, cancel := context.WithTimeout(baseCtx, 3*time.Second)
		defer cancel()
		local, err := store.CreateSession(ctx, agentstore.Session{
			ID:             sessionID,
			WorkspaceRoot:  directory,
			WorktreeRoot:   worktreePath,
			BaselineCommit: baseline,
			ProviderID:     settings.ProviderID,
			ModelID:        settings.ModelID,
			Title:          "New OpenCode session",
			Agent:          "opencode",
			Model:          settings.ProviderID + "/" + settings.ModelID,
			Status:         agentstore.SessionQueued,
		})
		var sessions []opencode.Session
		status := "Local session saved. Send a message to start." + worktreeNote
		selectedID := ""
		if err == nil {
			selectedID = local.ID
			sessions, err = listLocalOpenCodeSessions(ctx, store, directory)
			if err != nil {
				sessions = []opencode.Session{{
					ID: local.ID, Title: local.Title,
					Time:     opencode.SessionTime{Created: float64(local.CreatedAt.UnixMilli()), Updated: float64(local.UpdatedAt.UnixMilli())},
					Location: opencode.LocationRef{Directory: directory},
				}}
				status = "Local session was saved, but the session list could not be refreshed."
				err = nil
			}
		} else {
			status = "Could not save the OpenCode session to SQLite."
		}
		a.sendOpenCodeReply(baseCtx, result, openCodeReply{
			token: token, operation: openCodeOperationCreateSession,
			sessions: sessions, selectedSessionID: selectedID,
			panelStatus: status, err: err,
		})
	}()
	return nil
}

func (a *App) selectOpenCodeSession(sessionID string) error {
	if a.ws == nil || a.ws.Sample {
		return nil
	}
	var selected opencode.Session
	for _, session := range a.openCode.sessions {
		if session.ID == sessionID {
			selected = session
			break
		}
	}
	if selected.ID == "" || !sameOpenCodeWorkspace(selected.Location.Directory, a.ws.Root) {
		return nil
	}
	if err := a.ensureOpenCodeStore(); err != nil {
		a.openCode.panelStatus = "Local session history is unavailable."
		a.refresh()
		return nil
	}
	directory, err := canonicalWorkspaceRoot(a.ws.Root)
	if err != nil {
		a.openCode.panelStatus = "Could not resolve the selected workspace for this local session."
		a.refresh()
		return nil
	}
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationLoadMessages)
	store := a.openCode.store
	configDir := ""
	if a.settingsPath != "" {
		configDir = filepath.Dir(a.settingsPath)
	}
	var worktreeManager *openCodeWorktreeManager
	var worktreeManagerErr error
	worktreeManager, worktreeManagerErr = a.openCodeWorktreeManager()
	a.openCode.selectedSessionID = sessionID
	a.openCode.scroll.follow()
	a.openCode.pendingSwitch = nil
	a.openCode.messages = nil
	a.openCode.reviewable = false
	a.openCode.proposals = nil
	a.openCode.panelStatus = "Loading session transcript…"
	a.refresh()
	result := a.openCode.results
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, 3*time.Second)
		defer cancel()
		var local agentstore.Session
		var err error
		stored, err := store.ListSessions(ctx, directory)
		if err == nil {
			for _, session := range stored {
				if session.ID == sessionID && sameWorkspacePath(session.WorkspaceRoot, directory) {
					local = session
					break
				}
			}
			if local.ID == "" {
				err = agentstore.ErrNotFound
			}
		}
		var messages []openCodeTranscriptMessage
		retry := ""
		reviewStatus := ""
		reviewable := false
		if err == nil && local.WorktreeRoot != "" {
			reviewable, reviewStatus = verifyOpenCodeSessionWorktree(ctx, worktreeManager, directory, local)
			if worktreeManagerErr != nil {
				reviewable = false
				reviewStatus = "The isolated worktree could not be verified for proposal review."
			}
		}
		if err == nil {
			messages, retry, err = loadOpenCodeTranscript(ctx, store, local.ID)
		}
		var proposalRestore *openCodeProposalRestore
		if err == nil {
			proposalRestore = loadOpenCodeProposalRestore(configDir, directory, sessionID)
		}
		if messages == nil {
			messages = make([]openCodeTranscriptMessage, 0)
		}
		status := "Local session ready."
		if err != nil {
			status = "Could not load the local session transcript."
		} else if reviewStatus != "" {
			status = reviewStatus
		}
		a.sendOpenCodeReply(baseCtx, result, openCodeReply{
			token: token, operation: openCodeOperationLoadMessages,
			selectedSessionID: sessionID, reviewable: reviewable,
			messages: messages, proposalRestore: proposalRestore,
			retryText: retry, panelStatus: status, err: err,
		})
	}()
	return nil
}

func (a *App) reviewOpenCodeProposal() error {
	if a.ws == nil || a.ws.Sample || a.openCode.selectedSessionID == "" {
		a.openCode.panelStatus = "Start or select a chat to review changes."
		a.refresh()
		return nil
	}
	if !a.openCode.reviewable {
		if rows := a.agentProposalRows(a.openCode.selectedSessionID); len(rows) > 0 {
			a.openAgentDiff(rows[0].Path)
			return nil
		}
		a.openCode.panelStatus = "No isolated changes to review in this session."
		a.refresh()
		return nil
	}
	if a.openCode.store == nil {
		a.openCode.panelStatus = "Local session history is unavailable; proposal review cannot be loaded."
		a.refresh()
		return nil
	}
	manager, err := a.openCodeWorktreeManager()
	if err != nil {
		a.openCode.panelStatus = "The private OpenCode worktree directory could not be opened."
		a.refresh()
		return nil
	}
	directory, err := canonicalWorkspaceRoot(a.ws.Root)
	if err != nil {
		a.openCode.panelStatus = "Could not resolve the selected workspace for proposal review."
		a.refresh()
		return nil
	}
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationReview)
	store := a.openCode.store
	sessionID, result := a.openCode.selectedSessionID, a.openCode.results
	a.openCode.panelStatus = "Loading isolated proposal changes…"
	a.openCode.proposals = nil
	a.refresh()
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, 30*time.Second)
		defer cancel()
		stored, err := store.ListSessions(ctx, directory)
		var proposals []openCodeProposalFile
		var local agentstore.Session
		if err == nil {
			for _, session := range stored {
				if session.ID == sessionID && sameWorkspacePath(session.WorkspaceRoot, directory) {
					local = session
					break
				}
			}
			if local.ID == "" || local.WorktreeRoot == "" || !validOpenCodeCommitID(local.BaselineCommit) {
				err = errors.New("OpenCode session does not have a verified proposal baseline")
			}
			if err == nil {
				var worktree *openCodeWorktree
				worktree, err = manager.OpenExisting(ctx, directory, local.WorktreeRoot, local.BaselineCommit)
				if err == nil {
					var snapshot openCodeProposalSnapshot
					snapshot, err = manager.snapshotProposal(ctx, worktree, local.BaselineCommit)
					proposals = snapshot.Files
				}
			}
		}
		status := fmt.Sprintf("Loaded %d isolated changed files. The snapshot is read-only.", len(proposals))
		if err == nil && len(proposals) == 0 {
			status = "No isolated changes to review."
		}
		if err != nil {
			status = "The isolated proposal could not be loaded safely."
		}
		if proposals == nil {
			proposals = make([]openCodeProposalFile, 0)
		}
		a.sendOpenCodeReply(baseCtx, result, openCodeReply{
			token: token, operation: openCodeOperationReview,
			selectedSessionID: sessionID, reviewable: err == nil,
			proposals: proposals, panelStatus: status, err: err,
		})
	}()
	return nil
}

func boolPointer(value bool) *bool { return &value }

func (a *App) sendOpenCodeReply(ctx context.Context, result chan<- openCodeReply, reply openCodeReply) {
	select {
	case result <- reply:
		if a.page != nil {
			a.page.WantFrameIn(0)
		}
	case <-ctx.Done():
	}
}

func (a *App) receiveOpenCodePanelReply(reply openCodeReply) {
	restoreNote := ""
	if reply.operation == openCodeOperationLoadMessages && reply.proposalRestore != nil {
		a.applyOpenCodeProposalRestore(reply.proposalRestore)
		loaded, skipped, err := reply.proposalRestore.loaded, reply.proposalRestore.skipped, reply.proposalRestore.err
		switch {
		case err != nil:
			restoreNote = " Pending proposal review could not be restored safely."
		case skipped > 0:
			restoreNote = fmt.Sprintf(" Restored %d pending proposal(s); %d incomplete or invalid proposal record(s) were skipped.", loaded, skipped)
		case loaded > 0:
			restoreNote = fmt.Sprintf(" Restored %d pending proposal(s).", loaded)
		}
	}
	if reply.operation == openCodeOperationReview {
		a.openCode.reviewable = reply.reviewable
	}
	if reply.operation == openCodeOperationLoadMessages || reply.operation == openCodeOperationCreateSession {
		a.openCode.retryText = reply.retryText
		a.openCode.reviewable = reply.reviewable
		a.openCode.proposals = nil
	}
	if reply.proposals != nil {
		a.openCode.proposals = reply.proposals
	}
	if reply.err != nil && !reply.inProgress {
		if reply.operation == openCodeOperationCreateSession {
			a.restorePendingOpenCodePrompt()
		}
		a.openCode.panelStatus = reply.panelStatus
		if a.openCode.panelStatus == "" {
			a.openCode.panelStatus = openCodeFailureStatus(reply.err)
		}
		a.openCode.operation = openCodeOperationNone
		a.cancelOpenCodeOperation()
		if reply.messages != nil {
			a.openCode.messages = reply.messages
		}
		return
	}
	if reply.setAllSessions {
		a.openCode.allSessions = reply.allSessions
	}
	if reply.sessions != nil {
		a.openCode.sessions = openCodeWorkspaceSessions(reply.sessions, a.ws.Root)
		selected := false
		for _, session := range a.openCode.sessions {
			if session.ID == a.openCode.selectedSessionID {
				selected = true
				break
			}
		}
		if !selected {
			a.openCode.selectedSessionID = ""
			a.openCode.messages = nil
			a.openCode.reviewable = false
			a.openCode.proposals = nil
		}
	}
	if reply.selectedSessionID != "" {
		a.openCode.selectedSessionID = reply.selectedSessionID
		a.openCode.scroll.follow()
	}
	if want := a.openCode.pendingSelect; want != "" && reply.sessions != nil {
		a.openCode.pendingSelect = ""
		for _, s := range a.openCode.sessions {
			if s.ID == want {
				defer func() { _ = a.selectOpenCodeSession(want) }()
			}
		}
	}
	if reply.messages != nil {
		a.openCode.messages = reply.messages
	}
	if reply.panelStatus != "" {
		a.openCode.panelStatus = reply.panelStatus + restoreNote
	} else if restoreNote != "" {
		a.openCode.panelStatus += restoreNote
	}
	if !reply.inProgress {
		a.openCode.operation = openCodeOperationNone
		a.cancelOpenCodeOperation()
	}
	if reply.operation == openCodeOperationCreateSession && reply.selectedSessionID != "" && a.openCode.pendingPrompt != "" {
		pending := a.openCode.pendingPrompt
		a.openCode.pendingPrompt = ""
		_ = a.submitOpenCodePrompt(pending)
	}
}
