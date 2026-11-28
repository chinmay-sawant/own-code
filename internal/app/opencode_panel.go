package app

import (
	"context"
	"errors"
	"fmt"
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
	IncludeSelection   bool
	ContextPreview     string
	ContextAvailable   bool
	CanReview          bool
	Proposals          []openCodePanelProposalRow
}

type openCodePanelSessionRow struct {
	ID     string
	Title  string
	Active bool
	Status string
}

type openCodePanelMessageRow struct {
	ID   string
	Role string
	Text string
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
		CanInterrupt:       a.openCode.operation == openCodeOperationPrompt && a.openCode.connected && a.openCode.selectedSessionID != "",
		WorkspaceAvailable: a.ws != nil && !a.ws.Sample,
		Connected:          a.openCode.connected,
		Status:             a.openCode.panelStatus,
		SelectedID:         a.openCode.selectedSessionID,
		IncludeSelection:   a.openCode.includeSelection,
		CanReview:          a.openCode.reviewable && a.openCode.selectedSessionID != "" && a.openCode.operation == openCodeOperationNone,
	}
	if !v.WorkspaceAvailable {
		v.Status = "Open a project folder to use OpenCode sessions."
	} else if v.Status == "" {
		if v.SelectedID != "" {
			v.Status = "Local session is read-only until agent execution is implemented."
		} else if !v.Connected {
			v.Status = "Connect OpenCode Console in Settings to create a session."
		} else {
			v.Status = "Choose a local session or create one for this workspace."
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
		status := "Local SQLite session"
		if !v.Connected {
			status = "Saved local history"
		}
		v.Sessions = append(v.Sessions, openCodePanelSessionRow{
			ID:     session.ID,
			Title:  title,
			Active: session.ID == v.SelectedID,
			Status: status,
		})
	}
	for _, message := range a.openCode.messages {
		if strings.TrimSpace(message.Text) == "" {
			continue
		}
		v.Messages = append(v.Messages, openCodePanelMessageRow{
			ID:   message.ID,
			Role: message.Role,
			Text: message.Text,
		})
	}
	for _, proposal := range a.openCode.proposals {
		v.Proposals = append(v.Proposals, openCodePanelProposalRow{
			Path: proposal.Path, Change: proposal.Change, Diff: proposal.Diff,
		})
	}
	if v.IncludeSelection {
		if snapshot, err := a.currentOpenCodeContext(); err == nil {
			v.ContextAvailable = true
			v.ContextPreview = formatOpenCodeContext(snapshot)
		} else {
			v.ContextPreview = err.Error()
		}
	}
	return v
}

func formatOpenCodeContext(snapshot agentContextSnapshot) string {
	return "Selected editor context (" + snapshot.RelativePath + ", lines " + openCodeLineSpan(snapshot.StartLine, snapshot.EndLine) + ", version " + snapshot.Version[:16] + "):\n```\n" + snapshot.Text + "\n```"
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
	switch {
	case action == "opencode-panel-toggle":
		if a.openCode.panelOpen {
			a.openCode.panelOpen = false
			a.refresh()
			return nil
		}
		return a.openOpenCodePanel(ctx)
	case action == "opencode-panel-open":
		return a.openOpenCodePanel(ctx)
	case action == "opencode-panel-hide":
		a.openCode.panelOpen = false
		a.refresh()
		return nil
	case action == "opencode-context-toggle":
		a.openCode.includeSelection = !a.openCode.includeSelection
		a.refresh()
		return nil
	case action == "opencode-panel-refresh":
		return a.loadOpenCodeSessions()
	case action == "opencode-session-new":
		return a.createOpenCodeSession()
	case action == "opencode-session-review":
		return a.reviewOpenCodeProposal()
	case strings.HasPrefix(action, "opencode-session-pick:"):
		return a.selectOpenCodeSession(strings.TrimPrefix(action, "opencode-session-pick:"))
	case action == "opencode-session-prompt":
		prompt := strings.TrimSpace(a.opencodeCredentialInput("opencode-prompt"))
		a.page.Form().SetFormValue("opencode-prompt", "")
		return a.submitOpenCodePrompt(prompt)
	case action == "opencode-session-interrupt":
		return a.interruptOpenCodeSession()
	default:
		return nil
	}
}

func (a *App) openOpenCodePanel(ctx context.Context) error {
	a.openCode.panelOpen = true
	a.termOpen = false
	a.termFocus = false
	a.problemsOpen = false
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
	a.openCode.store = store
	return nil
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
	a.openCode.panelStatus = "Loading local sessions…"
	a.refresh()
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, 3*time.Second)
		defer cancel()
		sessions, err := listLocalOpenCodeSessions(ctx, store, directory)
		status := "Showing sessions saved in this workspace."
		if !connected {
			status = "OpenCode Console is disconnected. Showing saved local sessions."
		}
		if err != nil {
			status = "Local session history could not be loaded."
		} else if len(sessions) == 0 {
			status = "No local sessions are saved for this workspace."
		}
		a.sendOpenCodeReply(baseCtx, result, openCodeReply{
			token: token, operation: openCodeOperationSessions,
			sessions: sessions, panelStatus: status, err: err,
		})
	}()
	return nil
}

func (a *App) createOpenCodeSession() error {
	if a.ws == nil || a.ws.Sample {
		a.openCode.panelStatus = "Open a project folder to create an OpenCode session."
		a.refresh()
		return nil
	}
	if !a.openCode.connected || a.openCode.consoleClient == nil {
		a.openCode.panelStatus = "Connect to OpenCode Console in Settings before creating a session."
		a.refresh()
		return nil
	}
	settings := openCodeSettingsValue(a.settings.OpenCode)
	if _, _, ok := a.findOpenCodeModel(openCodeModelSelection(settings.ProviderID, settings.ModelID)); !ok {
		a.openCode.panelStatus = "Choose and save an available OpenCode model in Settings before creating a session."
		a.refresh()
		return nil
	}
	if err := a.ensureOpenCodeStore(); err != nil {
		a.openCode.panelStatus = "Local session history is unavailable; a session cannot be saved."
		a.refresh()
		return nil
	}
	directory, err := canonicalWorkspaceRoot(a.ws.Root)
	if err != nil {
		a.openCode.panelStatus = "Could not resolve the selected workspace for a local session."
		a.refresh()
		return nil
	}
	sessionID, err := newOpenCodeLocalSessionID()
	if err != nil {
		a.openCode.panelStatus = "Could not create a local OpenCode session ID."
		a.refresh()
		return nil
	}
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationCreateSession)
	store, result := a.openCode.store, a.openCode.results
	a.openCode.reviewable = false
	a.openCode.proposals = nil
	a.openCode.messages = nil
	a.openCode.selectedSessionID = ""
	a.openCode.panelStatus = "Saving a local session to SQLite…"
	a.refresh()
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, 3*time.Second)
		defer cancel()
		local, err := store.CreateSession(ctx, agentstore.Session{
			ID:                sessionID,
			WorkspaceRoot:     directory,
			ServerID:          openCodeConsoleBackendID,
			OpenCodeSessionID: sessionID,
			Title:             "New OpenCode session",
			Agent:             "opencode",
			Model:             settings.ProviderID + "/" + settings.ModelID,
			Status:            agentstore.SessionQueued,
		})
		var sessions []opencode.Session
		status := "Local session saved to SQLite. Prompting remains paused until the safe agent loop is implemented."
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
	var worktreeManager *openCodeWorktreeManager
	var worktreeManagerErr error
	worktreeManager, worktreeManagerErr = a.openCodeWorktreeManager()
	a.openCode.selectedSessionID = sessionID
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
		var messages []agentstore.Message
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
			messages, err = store.ListMessages(ctx, local.ID)
		}
		projected := make([]openCodeTranscriptMessage, 0, len(messages))
		for _, message := range messages {
			projected = append(projected, openCodeTranscriptMessage{
				ID: message.OpenCodeMessageID, Role: message.Role, Text: message.Body,
				Ordinal: message.Ordinal, CreatedAt: message.CreatedAt,
			})
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
			messages: projected, panelStatus: status, err: err,
		})
	}()
	return nil
}

func (a *App) submitOpenCodePrompt(_ string) error {
	a.openCode.panelStatus = "Prompts are paused until safe tool access and proposal acceptance are ready."
	a.refresh()
	return nil
}

func (a *App) reviewOpenCodeProposal() error {
	if a.ws == nil || a.ws.Sample || !a.openCode.reviewable || a.openCode.selectedSessionID == "" {
		a.openCode.panelStatus = "Select an isolated session with a verified proposal baseline first."
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

func (a *App) interruptOpenCodeSession() error {
	a.openCode.panelStatus = "There is no active local agent execution to stop."
	a.refresh()
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
	if reply.operation == openCodeOperationReview {
		a.openCode.reviewable = reply.reviewable
	}
	if reply.operation == openCodeOperationLoadMessages || reply.operation == openCodeOperationCreateSession {
		a.openCode.reviewable = reply.reviewable
		a.openCode.proposals = nil
	}
	if reply.proposals != nil {
		a.openCode.proposals = reply.proposals
	}
	if reply.err != nil && !reply.inProgress {
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
	}
	if reply.messages != nil {
		a.openCode.messages = reply.messages
	}
	if reply.panelStatus != "" {
		a.openCode.panelStatus = reply.panelStatus
	}
	if !reply.inProgress {
		a.openCode.operation = openCodeOperationNone
		a.cancelOpenCodeOperation()
	}
}
