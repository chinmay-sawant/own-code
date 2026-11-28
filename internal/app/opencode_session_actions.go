package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

// openCodeSessionAdmin is the store surface for rename and archive. It is an
// interface so the panel degrades to a visible message if a store build lacks
// it.
type openCodeSessionAdmin interface {
	RenameSession(ctx context.Context, sessionID, title string, at time.Time) error
	ArchiveSession(ctx context.Context, sessionID string, archived bool, at time.Time) error
}

const openCodeTitleLimit = 120

// openCodeRequestNotice explains why a new inference request cannot be sent.
// It is empty when a request could be made. Cached history is unaffected.
func (a *App) openCodeRequestNotice() string {
	if a.ws == nil || a.ws.Sample {
		return ""
	}
	if !a.openCode.connected {
		return "Offline: showing cached history. A new request needs a Console credential and a selected model."
	}
	st := openCodeSettingsValue(a.settings.OpenCode)
	if _, _, ok := a.findOpenCodeModel(openCodeModelSelection(st.ProviderID, st.ModelID)); !ok {
		return "A new request needs an available Console model. Choose one from the model menu."
	}
	return ""
}

// openCodeSessionContinue says whether a session can send a new turn with the
// current key and catalog. History stays readable either way; only new
// requests are gated. The session's own model wins over the default.
func (a *App) openCodeSessionContinue(sessionID string) string {
	if !a.openCode.connected {
		return "Cannot continue: no API key connected. History stays readable."
	}
	providerID, modelID := a.openCodeSessionModel(sessionID)
	if providerID == "" || modelID == "" {
		return "Cannot continue yet: choose a model."
	}
	if _, _, ok := a.findOpenCodeModel(openCodeModelSelection(providerID, modelID)); !ok {
		return "Cannot continue: model " + providerID + "/" + modelID + " is unavailable. Pick another."
	}
	return "Can continue with the current key and model."
}

// openCodeSessionState is the lifecycle label from the local run lifecycle.
func (a *App) openCodeSessionState(sessionID string, connected bool) string {
	if run := a.openCodeRunFor(sessionID); run != nil {
		switch {
		case run.approval != nil:
			return "Waiting for approval"
		case run.stopping:
			return "Stopping"
		}
		return "Running"
	}
	for _, s := range a.openCode.allSessions {
		if s.ID != sessionID {
			continue
		}
		switch s.Status {
		case agentstore.SessionCompleted:
			return "Completed"
		case agentstore.SessionFailed:
			return "Failed"
		case agentstore.SessionInterrupted, agentstore.SessionRunning:
			// "running" without a live run is a stale row from a closed
			// process: it is shown as interrupted, never as active.
			return "Interrupted"
		}
	}
	if !connected {
		return "Disconnected"
	}
	return "Ready"
}

// openCodeOtherWorkspaceSessions lists saved Console sessions that belong to
// other workspaces. Selecting one asks before switching.
func (a *App) openCodeOtherWorkspaceSessions() []openCodePanelOtherRow {
	if a.ws == nil || a.ws.Sample {
		return nil
	}
	var rows []openCodePanelOtherRow
	for _, s := range a.openCode.allSessions {
		if s.Legacy || s.Archived || strings.TrimSpace(s.WorkspaceRoot) == "" ||
			sameOpenCodeWorkspace(s.WorkspaceRoot, a.ws.Root) {
			continue
		}
		title := strings.TrimSpace(s.Title)
		if title == "" {
			title = "Untitled session"
		}
		rows = append(rows, openCodePanelOtherRow{ID: s.ID, Title: title, Root: s.WorkspaceRoot})
		if len(rows) == 8 {
			break
		}
	}
	return rows
}

// openCodeArchivedSessions lists archived sessions of the current workspace so
// they can be restored.
func (a *App) openCodeArchivedSessions() []openCodePanelSessionRow {
	if a.ws == nil || a.ws.Sample {
		return nil
	}
	var rows []openCodePanelSessionRow
	for _, s := range a.openCode.allSessions {
		if !s.Archived || s.Legacy || !sameOpenCodeWorkspace(s.WorkspaceRoot, a.ws.Root) {
			continue
		}
		title := strings.TrimSpace(s.Title)
		if title == "" {
			title = "Untitled session"
		}
		rows = append(rows, openCodePanelSessionRow{ID: s.ID, Title: title, Status: "Archived", State: "Archived"})
	}
	return rows
}

func (a *App) unarchiveOpenCodeSession(sessionID string) error {
	return a.mutateOpenCodeSession("Session restored.", func(ctx context.Context, admin openCodeSessionAdmin) error {
		return admin.ArchiveSession(ctx, sessionID, false, time.Now().UTC())
	})
}

// requestOpenCodeWorkspaceSwitch records a visible confirmation; the current
// workspace is not changed until the user confirms.
func (a *App) requestOpenCodeWorkspaceSwitch(sessionID string) error {
	for _, s := range a.openCode.allSessions {
		if s.ID == sessionID {
			if a.ws != nil && sameOpenCodeWorkspace(s.WorkspaceRoot, a.ws.Root) {
				return a.selectOpenCodeSession(sessionID)
			}
			a.openCode.pendingSwitch = &openCodePendingSwitch{SessionID: s.ID, Title: s.Title, Root: s.WorkspaceRoot}
			a.openCode.panelStatus = "This session belongs to another workspace. Confirm to switch."
			a.refresh()
			return nil
		}
	}
	return nil
}

func (a *App) confirmOpenCodeWorkspaceSwitch(ctx context.Context) error {
	sw := a.openCode.pendingSwitch
	a.openCode.pendingSwitch = nil
	if sw == nil {
		return nil
	}
	if a.openCodeRunFor(a.openCode.selectedSessionID) != nil {
		a.openCode.panelStatus = "Stop the running turn before switching workspace."
		a.refresh()
		return nil
	}
	if err := a.switchProject(ctx, sw.Root); err != nil {
		return err
	}
	a.openCode.pendingSelect = sw.SessionID
	a.openCode.selectedSessionID = ""
	a.openCode.messages = nil
	return a.openOpenCodePanel(ctx)
}

func (a *App) cancelOpenCodeWorkspaceSwitch() {
	a.openCode.pendingSwitch = nil
	a.openCode.panelStatus = "Workspace unchanged."
	a.refresh()
}

func (a *App) startOpenCodeRename(sessionID string) {
	a.openCode.renamingID = sessionID
	a.refresh()
}

func (a *App) saveOpenCodeRename(sessionID string) error {
	title := strings.Join(strings.Fields(a.opencodeCredentialInput("opencode-rename")), " ")
	if title == "" || len(title) > openCodeTitleLimit {
		a.openCode.panelStatus = "Enter a title up to 120 characters."
		a.refresh()
		return nil
	}
	a.openCode.renamingID = ""
	return a.mutateOpenCodeSession("Session renamed.", func(ctx context.Context, admin openCodeSessionAdmin) error {
		return admin.RenameSession(ctx, sessionID, title, time.Now().UTC())
	})
}

func (a *App) archiveOpenCodeSession(sessionID string) error {
	if a.openCodeRunFor(sessionID) != nil {
		a.openCode.panelStatus = "Stop the running turn before archiving this session."
		a.refresh()
		return nil
	}
	return a.mutateOpenCodeSession("Session archived. It is kept in local history.", func(ctx context.Context, admin openCodeSessionAdmin) error {
		return admin.ArchiveSession(ctx, sessionID, true, time.Now().UTC())
	})
}

// mutateOpenCodeSession runs a store mutation off the UI thread, then reloads
// the session list through the normal reply path.
func (a *App) mutateOpenCodeSession(okStatus string, fn func(context.Context, openCodeSessionAdmin) error) error {
	if a.ws == nil || a.ws.Sample {
		return nil
	}
	if err := a.ensureOpenCodeStore(); err != nil {
		a.openCode.panelStatus = "Local session history is unavailable."
		a.refresh()
		return nil
	}
	admin, ok := any(a.openCode.store).(openCodeSessionAdmin)
	if !ok {
		a.openCode.panelStatus = "This build of local history cannot rename or archive sessions."
		a.refresh()
		return nil
	}
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationSessions)
	store, directory, result := a.openCode.store, a.ws.Root, a.openCode.results
	a.openCode.panelStatus = "Updating session…"
	a.refresh()
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, 3*time.Second)
		defer cancel()
		err := fn(ctx, admin)
		status := okStatus
		if errors.Is(err, agentstore.ErrNotFound) {
			status = "That session no longer exists."
		} else if err != nil {
			status = "The session could not be updated."
		}
		sessions, all, lerr := fetchOpenCodePanelSessions(ctx, store, directory)
		reply := openCodeReply{token: token, operation: openCodeOperationSessions, sessions: sessions,
			allSessions: all, setAllSessions: lerr == nil, panelStatus: status}
		if lerr != nil {
			reply.panelStatus, reply.err = "Local session history could not be loaded.", lerr
		}
		a.sendOpenCodeReply(baseCtx, result, reply)
	}()
	return nil
}

// fetchOpenCodePanelSessions returns the workspace list plus all stored rows.
func fetchOpenCodePanelSessions(ctx context.Context, store *agentstore.Store, directory string) ([]opencode.Session, []agentstore.Session, error) {
	sessions, err := listLocalOpenCodeSessions(ctx, store, directory)
	if err != nil {
		return nil, nil, err
	}
	all, err := store.ListSessions(ctx, "", agentstore.IncludeArchived())
	if err != nil {
		return nil, nil, err
	}
	return sessions, all, nil
}
