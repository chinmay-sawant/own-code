package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agent"
	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

const openCodeRequestTimeout = 12 * time.Second

type openCodeOperation uint8

const (
	openCodeOperationNone openCodeOperation = iota
	openCodeOperationCheck
	openCodeOperationSessions
	openCodeOperationCreateSession
	openCodeOperationLoadMessages
	openCodeOperationPrompt
	openCodeOperationInterrupt
	openCodeOperationReview
)

type openCodeRuntime struct {
	consoleClient     *opencode.ConsoleClient
	consoleModels     []opencode.ConsoleModel
	store             *agentstore.Store
	worktrees         *openCodeWorktreeManager
	status            string
	connected         bool
	token             uint64
	operation         openCodeOperation
	cancel            context.CancelFunc
	lifeCtx           context.Context
	lifeCancel        context.CancelFunc
	results           chan openCodeReply
	panelOpen         bool
	panelStatus       string
	sessions          []opencode.Session
	selectedSessionID string
	messages          []openCodeTranscriptMessage
	includeSelection  bool
	reviewable        bool
	proposals         []openCodeProposalFile
	// agentProposals holds pending write_file/edit_file proposals per session.
	agentProposals map[string][]*openCodeAgentProposal
	// runs holds one run context per local session, keyed by session ID.
	// Cancellation and event routing never cross sessions.
	runs         map[string]*openCodeRun
	retryText    string
	recoveryNote string
	testProvider agent.Provider
}

type openCodeReply struct {
	run               *openCodeRunEvent
	token             uint64
	operation         openCodeOperation
	consoleClient     *opencode.ConsoleClient
	consoleModels     []opencode.ConsoleModel
	safeStatus        string
	err               error
	apiKeySaveErr     error
	sessions          []opencode.Session
	selectedSessionID string
	messages          []openCodeTranscriptMessage
	panelStatus       string
	inProgress        bool
	reviewable        bool
	proposals         []openCodeProposalFile
	retryText         string
}

type openCodeModelRow struct {
	Value   string
	Label   string
	Details string
}

func openCodeModelRows(models []opencode.ConsoleModel) []openCodeModelRow {
	rows := make([]openCodeModelRow, 0, len(models))
	for _, model := range agent.FilterEligible(models) {
		providerID := strings.TrimSpace(model.ProviderID)
		modelID := strings.TrimSpace(model.ID)
		if providerID == "" || modelID == "" {
			continue
		}
		providerName := strings.TrimSpace(model.ProviderName)
		if providerName == "" {
			providerName = providerID
		}
		modelName := strings.TrimSpace(model.Name)
		if modelName == "" {
			modelName = modelID
		}
		details := model.Type
		if model.ContextLimit > 0 {
			details += " · " + strconv.Itoa(model.ContextLimit) + " context"
		}
		if model.OutputLimit > 0 {
			details += " · " + strconv.Itoa(model.OutputLimit) + " output"
		}
		details += " · tools"
		rows = append(rows, openCodeModelRow{
			Value:   providerID + "/" + modelID,
			Label:   providerName + " · " + modelName,
			Details: strings.TrimPrefix(details, " · "),
		})
	}
	return rows
}

func newOpenCodeRuntime() openCodeRuntime {
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	return openCodeRuntime{
		lifeCtx:    lifeCtx,
		lifeCancel: lifeCancel,
		results:    make(chan openCodeReply, 16),
	}
}

func (a *App) beginOpenCodeOperation(operation openCodeOperation) (uint64, context.Context) {
	a.cancelOpenCodeOperation()
	a.openCode.token++
	a.openCode.operation = operation
	ctx, cancel := context.WithCancel(a.openCode.lifeCtx)
	a.openCode.cancel = cancel
	return a.openCode.token, ctx
}

func (a *App) cancelOpenCodeOperation() {
	if a.openCode.cancel != nil {
		a.openCode.cancel()
		a.openCode.cancel = nil
	}
}

func (a *App) retireOpenCodeConnection() {
	a.cancelOpenCodeOperation()
	a.openCode.token++
	a.openCode.operation = openCodeOperationNone
	a.openCode.consoleClient = nil
	a.openCode.consoleModels = nil
	a.openCode.connected = false
}

func (a *App) receiveOpenCode(ctx context.Context) bool {
	changed := false
	panelOnly := true
	prevPanel := a.view.OpenCodePanel
	for {
		select {
		case reply := <-a.openCode.results:
			if reply.run != nil {
				if a.handleOpenCodeRunEvent(reply.run) {
					changed = true
				}
				continue
			}
			if reply.token != a.openCode.token {
				continue
			}
			if reply.operation >= openCodeOperationSessions {
				a.receiveOpenCodePanelReply(reply)
				changed = true
				continue
			}
			panelOnly = false
			if reply.err != nil {
				a.openCode.connected = false
				a.openCode.consoleClient = nil
				a.openCode.consoleModels = nil
				a.openCode.status = reply.safeStatus
				if a.openCode.status == "" {
					a.openCode.status = openCodeFailureStatus(reply.err)
				}
			} else {
				a.openCode.consoleClient = reply.consoleClient
				a.openCode.consoleModels = append([]opencode.ConsoleModel(nil), reply.consoleModels...)
				a.openCode.connected = reply.consoleClient != nil
				a.openCode.status = reply.safeStatus
				if a.openCode.status == "" {
					if a.openCode.connected {
						a.openCode.status = "Connected to OpenCode Console."
					} else {
						a.openCode.status = "OpenCode Console client is unavailable."
					}
				}
				if reply.apiKeySaveErr != nil {
					a.openCode.status += " The key was not saved; enter it again next time."
				}
			}
			a.openCode.operation = openCodeOperationNone
			a.cancelOpenCodeOperation()
			a.refresh()
			if a.openCode.panelOpen && a.openCode.connected {
				_ = a.loadOpenCodeSessions()
			}
			changed = true
		default:
			if changed {
				if panelOnly {
					nextPanel := a.openCodePanelView()
					a.view.OpenCodePanel = nextPanel
					if a.patchOpenCodePanel(ctx, prevPanel, nextPanel) {
						return true
					}
					a.refresh()
				}
				if err := a.page.Redraw(ctx); err != nil {
					return false
				}
			}
			return changed
		}
	}
}

func (a *App) closeOpenCodeRuntime() {
	if a == nil {
		return
	}
	a.cancelOpenCodeOperation()
	a.cancelAllOpenCodeRuns()
	if a.openCode.lifeCancel != nil {
		a.openCode.lifeCancel()
		a.openCode.lifeCancel = nil
	}
	if a.openCode.store != nil {
		_ = a.openCode.store.Close()
		a.openCode.store = nil
	}
}

func openCodeFailureStatus(err error) string {
	var consoleErr *opencode.ConsoleHTTPError
	if errors.As(err, &consoleErr) {
		switch consoleErr.StatusCode {
		case http.StatusUnauthorized:
			return "OpenCode rejected the Console API key. Check that it is active."
		case http.StatusForbidden:
			return "This API key cannot read the workspace model catalog. Check its Models permission."
		default:
			return fmt.Sprintf("OpenCode Console returned HTTP %d.", consoleErr.StatusCode)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "OpenCode Console did not respond before the connection timed out."
	}
	return "Could not reach OpenCode Console. Check the network connection and try again."
}
