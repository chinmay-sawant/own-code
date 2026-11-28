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
			v.Status = "Session is read-only; agent actions are disabled."
		} else if !v.Connected {
			v.Status = "Connect OpenCode in Settings to start or resume a session."
		} else {
			v.Status = "Choose a session or create one for this workspace."
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
		status := "OpenCode session"
		if !v.Connected {
			status = "Cached local history"
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
		a.openCode.panelStatus = "Local session history is unavailable; sessions can still be read from OpenCode."
	}
	if a.openCode.client != nil && a.openCode.connected {
		if err := a.loadOpenCodeSessions(); err != nil {
			return err
		}
	} else {
		return a.loadOpenCodeCachedSessions()
	}
	return nil
}

func (a *App) loadOpenCodeCachedSessions() error {
	if a.ws == nil || a.ws.Sample {
		a.openCode.panelStatus = "Open a project folder to use OpenCode sessions."
		a.refresh()
		return nil
	}
	if a.openCode.store == nil {
		a.openCode.panelStatus = "OpenCode is disconnected and local session history is unavailable."
		a.refresh()
		return nil
	}
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationSessions)
	store, directory := a.openCode.store, a.ws.Root
	serverID := a.openCodeServerID(openCodeSettingsValue(a.settings.OpenCode).ServerURL)
	result := a.openCode.results
	a.openCode.panelStatus = "Loading saved OpenCode history…"
	a.refresh()
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, 3*time.Second)
		defer cancel()
		history, err := newOpenCodeSessionHistory(store, directory, serverID)
		var cached []agentstore.Session
		if err == nil {
			cached, err = history.ListSessions(ctx)
		}
		sessions := make([]opencode.Session, 0, len(cached))
		for _, local := range cached {
			sessions = append(sessions, opencode.Session{
				ID: local.OpenCodeSessionID, Title: local.Title,
				Time:     opencode.SessionTime{Created: float64(local.CreatedAt.UnixMilli()), Updated: float64(local.UpdatedAt.UnixMilli())},
				Location: opencode.LocationRef{Directory: directory},
			})
		}
		status := "OpenCode is disconnected. Showing saved local history."
		if err != nil {
			status = "OpenCode is disconnected and local session history could not be loaded."
		} else if len(sessions) == 0 {
			status = "OpenCode is disconnected. There is no saved history for this workspace."
		}
		a.sendOpenCodeReply(baseCtx, result, openCodeReply{
			token: token, operation: openCodeOperationSessions, sessions: sessions,
			panelStatus: status, err: err,
		})
	}()
	return nil
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
	if a.openCode.client == nil || !a.openCode.connected {
		a.openCode.panelStatus = "Connect to OpenCode in Settings before loading sessions."
		a.refresh()
		return nil
	}
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationSessions)
	client, directory, serverID, store := a.openCode.client, a.ws.Root, a.openCode.serverID, a.openCode.store
	serverURL := openCodeSettingsValue(a.settings.OpenCode).ServerURL
	manager, managerErr := a.openCodeWorktreeManager()
	if managerErr != nil {
		manager = nil
	}
	result := a.openCode.results
	a.openCode.panelStatus = "Loading workspace sessions…"
	a.refresh()
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, openCodeRequestTimeout)
		defer cancel()
		sessions, status, err := listOpenCodeWorkspaceSessions(ctx, client, directory, serverID, serverURL, store, manager)
		if managerErr != nil && err == nil {
			status = "Sessions loaded; isolated worktree sessions are unavailable because the private worktree directory could not be opened."
		}
		a.sendOpenCodeReply(baseCtx, result, openCodeReply{
			token: token, operation: openCodeOperationSessions, client: client,
			sessions: append([]opencode.Session{}, sessions...), panelStatus: status, err: err,
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
	if a.openCode.client == nil || !a.openCode.connected {
		a.openCode.panelStatus = "Connect to OpenCode in Settings before creating a session."
		a.refresh()
		return nil
	}
	serverURL := openCodeSettingsValue(a.settings.OpenCode).ServerURL
	if !isOpenCodeLoopbackServerURL(serverURL) {
		a.openCode.panelStatus = "Isolated OpenCode sessions require a server running on this device. Remote servers cannot access this private worktree."
		a.refresh()
		return nil
	}
	if a.openCode.store == nil {
		a.openCode.panelStatus = "Local session history is unavailable; an isolated session cannot be tracked safely."
		a.refresh()
		return nil
	}
	manager, managerErr := a.openCodeWorktreeManager()
	if managerErr != nil {
		a.openCode.panelStatus = "The private OpenCode worktree directory could not be opened."
		a.refresh()
		return nil
	}
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationCreateSession)
	a.openCode.reviewable = false
	a.openCode.proposals = nil
	client, directory, serverID, store := a.openCode.client, a.ws.Root, a.openCode.serverID, a.openCode.store
	result := a.openCode.results
	a.openCode.panelStatus = "Creating an OpenCode session…"
	a.refresh()
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, 45*time.Second)
		defer cancel()
		worktree, err := manager.Create(ctx, directory)
		var session opencode.Session
		if err == nil {
			session, err = client.CreateSession(ctx, opencode.CreateSessionInput{
				Title:       "New isolated own-code session",
				Location:    &opencode.LocationRef{Directory: worktree.WorkspaceRoot()},
				Permissions: openCodeReadOnlyPermissions(),
			})
		}
		var local agentstore.Session
		if err == nil {
			if !sameOpenCodeWorkspace(session.Location.Directory, worktree.WorkspaceRoot()) {
				err = errors.New("OpenCode created the session outside its private worktree")
			} else if history, historyErr := newOpenCodeSessionHistory(store, directory, serverID); historyErr != nil {
				err = historyErr
			} else {
				local, err = history.PersistIsolatedSession(ctx, session, worktree, agentstore.SessionQueued)
			}
		}
		var sessions []opencode.Session
		status := "Isolated session created. Agent permissions remain blocked until proposal review is ready."
		if err == nil {
			sessions, status, err = listOpenCodeWorkspaceSessions(ctx, client, directory, serverID, serverURL, store, manager)
			if err == nil {
				found := false
				for _, listed := range sessions {
					if listed.ID == session.ID {
						found = true
						break
					}
				}
				if !found {
					session.Location.Directory = directory
					sessions = append(sessions, session)
				}
			}
		}
		if err != nil {
			status = "OpenCode could not create or record the isolated session. Any created worktree was left in place for safety."
		}
		a.sendOpenCodeReply(baseCtx, result, openCodeReply{
			token: token, operation: openCodeOperationCreateSession, client: client,
			sessions: append([]opencode.Session{}, sessions...), selectedSessionID: session.ID,
			localSessionID: local.ID, reviewable: local.BaselineCommit != "", panelStatus: status, err: err,
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
	connected := a.openCode.connected && a.openCode.client != nil
	if !connected && a.openCode.store == nil {
		a.openCode.panelStatus = "OpenCode is disconnected and local session history is unavailable."
		a.refresh()
		return nil
	}
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationLoadMessages)
	client, directory, serverID, store := a.openCode.client, a.ws.Root, a.openCode.serverID, a.openCode.store
	var worktreeManager *openCodeWorktreeManager
	var worktreeManagerErr error
	if store != nil {
		worktreeManager, worktreeManagerErr = a.openCodeWorktreeManager()
	}
	if !connected || serverID == "" {
		serverID = a.openCodeServerID(openCodeSettingsValue(a.settings.OpenCode).ServerURL)
	}
	a.openCode.selectedSessionID = sessionID
	a.openCode.messages = nil
	a.openCode.reviewable = false
	a.openCode.proposals = nil
	a.openCode.panelStatus = "Loading session transcript…"
	a.refresh()
	result := a.openCode.results
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, openCodeRequestTimeout)
		defer cancel()
		var history *openCodeSessionHistory
		var err error
		var local agentstore.Session
		var messages []agentstore.Message
		reviewStatus := ""
		reviewable := false
		if !connected {
			if store == nil {
				err = errors.New("local OpenCode session history is unavailable")
			} else if history, historyErr := newOpenCodeSessionHistory(store, directory, serverID); historyErr != nil {
				err = historyErr
			} else {
				localID := stableHistoryID("session", serverID, sessionID)
				local, err = history.FindSession(ctx, sessionID)
				if err == nil {
					reviewable, reviewStatus = verifyOpenCodeSessionWorktree(ctx, worktreeManager, directory, local)
					messages, err = history.LoadMessages(ctx, localID)
				}
			}
		} else if store != nil {
			history, err = newOpenCodeSessionHistory(store, directory, serverID)
			if err == nil {
				local, err = history.FindSession(ctx, selected.ID)
				if errors.Is(err, agentstore.ErrNotFound) {
					local, err = history.PersistSession(ctx, selected, agentstore.SessionCompleted)
				}
			}
			if err == nil && local.WorktreeRoot != "" {
				reviewable, reviewStatus = verifyOpenCodeSessionWorktree(ctx, worktreeManager, directory, local)
				if worktreeManagerErr != nil {
					reviewable = false
					reviewStatus = "Transcript loaded, but the isolated worktree could not be verified for proposal review."
				}
			}
			if err == nil {
				messages, err = history.SyncMessages(ctx, client, local.ID)
			}
		} else {
			page, listErr := client.ListMessages(ctx, sessionID, opencode.MessageListOptions{Limit: 100, Order: "desc"})
			err = listErr
			if err == nil {
				for _, message := range projectOpenCodeMessages(page.Data) {
					messages = append(messages, agentstore.Message{
						OpenCodeMessageID: message.ID, Role: message.Role, Body: message.Text,
						Ordinal: message.Ordinal, CreatedAt: message.CreatedAt,
					})
				}
			}
		}
		projected := make([]openCodeTranscriptMessage, 0, len(messages))
		for _, message := range messages {
			projected = append(projected, openCodeTranscriptMessage{
				ID: message.OpenCodeMessageID, Role: message.Role, Text: message.Body,
				Ordinal: message.Ordinal, CreatedAt: message.CreatedAt,
			})
		}
		status := "Session ready."
		if !connected {
			status = "Saved transcript loaded offline."
		}
		if err != nil {
			status = "OpenCode could not load this session transcript."
		} else if reviewStatus != "" {
			status = reviewStatus
		}
		a.sendOpenCodeReply(baseCtx, result, openCodeReply{
			token: token, operation: openCodeOperationLoadMessages, client: client,
			selectedSessionID: sessionID, localSessionID: local.ID, reviewable: reviewable,
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
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationReview)
	client, store, directory := a.openCode.client, a.openCode.store, a.ws.Root
	serverID := a.openCode.serverID
	if serverID == "" {
		serverID = a.openCodeServerID(openCodeSettingsValue(a.settings.OpenCode).ServerURL)
	}
	sessionID, result := a.openCode.selectedSessionID, a.openCode.results
	a.openCode.panelStatus = "Loading isolated proposal changes…"
	a.openCode.proposals = nil
	a.refresh()
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, 30*time.Second)
		defer cancel()
		history, err := newOpenCodeSessionHistory(store, directory, serverID)
		var proposals []openCodeProposalFile
		if err == nil {
			var local agentstore.Session
			local, err = history.FindSession(ctx, sessionID)
			if err == nil && (local.WorktreeRoot == "" || !validOpenCodeCommitID(local.BaselineCommit)) {
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
			token: token, operation: openCodeOperationReview, client: client,
			selectedSessionID: sessionID, reviewable: err == nil,
			proposals: proposals, panelStatus: status, err: err,
		})
	}()
	return nil
}

func (a *App) interruptOpenCodeSession() error {
	if !a.openCode.connected || a.openCode.client == nil || a.openCode.selectedSessionID == "" || a.ws == nil || a.ws.Sample {
		return nil
	}
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationInterrupt)
	client, sessionID := a.openCode.client, a.openCode.selectedSessionID
	directory, serverID, store := a.ws.Root, a.openCode.serverID, a.openCode.store
	session := sessionForID(a.openCode.sessions, sessionID)
	result := a.openCode.results
	a.openCode.panelStatus = "Stopping the OpenCode agent…"
	a.refresh()
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, openCodeRequestTimeout)
		interrupted, err := client.Interrupt(ctx, sessionID)
		var messages []openCodeTranscriptMessage
		if err == nil {
			page, listErr := client.ListMessages(ctx, sessionID, opencode.MessageListOptions{Limit: 100, Order: "desc"})
			if listErr == nil {
				messages = projectOpenCodeMessages(page.Data)
				if store != nil {
					if history, historyErr := newOpenCodeSessionHistory(store, directory, serverID); historyErr == nil {
						status := agentstore.SessionCompleted
						if interrupted {
							status = agentstore.SessionInterrupted
						}
						if local, persistErr := history.PersistSession(ctx, session, status); persistErr == nil {
							if persisted, syncErr := history.SyncMessages(ctx, client, local.ID); syncErr == nil {
								messages = make([]openCodeTranscriptMessage, 0, len(persisted))
								for _, message := range persisted {
									messages = append(messages, openCodeTranscriptMessage{ID: message.OpenCodeMessageID, Role: message.Role, Text: message.Body, Ordinal: message.Ordinal, CreatedAt: message.CreatedAt})
								}
							}
						}
					}
				}
			}
		}
		cancel()
		status := "No active OpenCode execution was running."
		if interrupted {
			status = "OpenCode execution stopped."
		}
		if err != nil {
			status = "OpenCode could not stop this session."
		}
		a.sendOpenCodeReply(baseCtx, result, openCodeReply{token: token, operation: openCodeOperationInterrupt, client: client, messages: messages, panelStatus: status, err: err})
	}()
	return nil
}

func sessionForID(sessions []opencode.Session, id string) opencode.Session {
	for _, session := range sessions {
		if session.ID == id {
			return session
		}
	}
	return opencode.Session{}
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
