package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/opencode"
	"github.com/chinmay-sawant/own-code/internal/secretstore"
)

const openCodeRequestTimeout = 12 * time.Second

type openCodeOperation uint8

const (
	openCodeOperationNone openCodeOperation = iota
	openCodeOperationCheck
	openCodeOperationConnectProvider
	openCodeOperationSessions
	openCodeOperationCreateSession
	openCodeOperationLoadMessages
	openCodeOperationPrompt
	openCodeOperationInterrupt
	openCodeOperationReview
)

type openCodeRuntime struct {
	client            *opencode.Client
	process           *opencode.ServerProcess
	store             *agentstore.Store
	worktrees         *openCodeWorktreeManager
	status            string
	connected         bool
	serverID          string
	integrations      []opencode.Integration
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
}

type openCodeIntegrationRow struct {
	ID        string
	Name      string
	KeyAuth   bool
	Connected bool
}

type openCodeReply struct {
	token             uint64
	operation         openCodeOperation
	client            *opencode.Client
	process           *opencode.ServerProcess
	serverURL         string
	serverInfo        opencode.ServerInfo
	integrations      []opencode.Integration
	safeStatus        string
	serverReachable   bool
	err               error
	integrationErr    error
	passwordSaveErr   error
	sessions          []opencode.Session
	selectedSessionID string
	localSessionID    string
	messages          []openCodeTranscriptMessage
	panelStatus       string
	inProgress        bool
	reviewable        bool
	proposals         []openCodeProposalFile
}

func newOpenCodeRuntime() openCodeRuntime {
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	return openCodeRuntime{
		lifeCtx:    lifeCtx,
		lifeCancel: lifeCancel,
		results:    make(chan openCodeReply, 16),
	}
}

func (a *App) openCodeServerID(serverURL string) string {
	digest := sha256.Sum256([]byte(serverURL))
	return fmt.Sprintf("server-%x", digest[:12])
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
	process := a.openCode.process
	a.openCode.process = nil
	a.openCode.client = nil
	a.openCode.connected = false
	a.openCode.integrations = nil
	a.openCode.sessions = nil
	a.openCode.selectedSessionID = ""
	a.openCode.messages = nil
	a.openCode.reviewable = false
	a.openCode.proposals = nil
	if process != nil {
		go func() { _ = process.Close() }()
	}
}

func (a *App) checkOpenCodeConnection(client *opencode.Client, password string, savePassword bool) {
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationCheck)
	a.openCode.client = client
	a.openCode.connected = false
	a.openCode.status = "Checking the OpenCode server…"
	a.refresh()

	result := a.openCode.results
	directory := a.ws.Root
	serverURL := openCodeSettingsValue(a.settings.OpenCode).ServerURL
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, openCodeRequestTimeout)
		defer cancel()
		reply := openCodeReply{token: token, operation: openCodeOperationCheck, client: client}
		reply.serverInfo, reply.err = client.GetInfo(ctx)
		reply.serverReachable = reply.err == nil
		if reply.err == nil {
			reply.integrations, reply.integrationErr = client.ListIntegrations(ctx, directory)
		}
		if reply.err == nil && savePassword {
			reply.passwordSaveErr = saveOpenCodeServerPassword(password)
		}
		select {
		case result <- reply:
		default:
		}
		a.page.WantFrameIn(0)
	}()
	a.openCode.serverID = a.openCodeServerID(serverURL)
}

func (a *App) connectOpenCodeProvider(client *opencode.Client, integrationID, key string, answer json.RawMessage, password string, savePassword bool) {
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationConnectProvider)
	a.openCode.client = client
	a.openCode.status = "Connecting the provider to OpenCode…"
	a.refresh()

	result := a.openCode.results
	directory := a.ws.Root
	go func() {
		defer clear(answer)
		ctx, cancel := context.WithTimeout(baseCtx, openCodeRequestTimeout)
		defer cancel()
		reply := openCodeReply{token: token, operation: openCodeOperationConnectProvider, client: client}
		_, reply.err = client.GetInfo(ctx)
		reply.serverReachable = reply.err == nil
		if reply.err == nil {
			reply.integrations, reply.integrationErr = client.ListIntegrations(ctx, directory)
			if reply.integrationErr != nil {
				reply.err = reply.integrationErr
			} else if !integrationSupportsKey(reply.integrations, integrationID) {
				reply.safeStatus = "The selected integration does not advertise key authentication. Choose an integration with a key method."
				reply.err = errors.New("selected integration does not support key authentication")
			}
			if reply.err == nil {
				reply.err = client.ConnectKey(ctx, integrationID, opencode.ConnectKeyInput{Key: key, Answer: answer}, directory)
			}
		}
		if reply.err == nil {
			reply.integrations, reply.integrationErr = client.ListIntegrations(ctx, directory)
		}
		if reply.err == nil && savePassword {
			reply.passwordSaveErr = saveOpenCodeServerPassword(password)
		}
		a.sendOpenCodeReply(baseCtx, result, reply)
	}()
}

func openCodeKeyMethod(integrations []opencode.Integration, integrationID string) (opencode.IntegrationMethod, bool) {
	for _, integration := range integrations {
		if integration.ID != integrationID {
			continue
		}
		for _, method := range integration.Methods {
			if strings.EqualFold(method.Type, "key") {
				return method, true
			}
		}
		return opencode.IntegrationMethod{}, false
	}
	return opencode.IntegrationMethod{}, false
}

func openCodeProviderFields(integrations []opencode.Integration, integrationID string) []OpenCodeProviderField {
	method, ok := openCodeKeyMethod(integrations, integrationID)
	if !ok {
		return nil
	}
	fields, err := ParseOpenCodeProviderKeyFields(method)
	if err != nil {
		return nil
	}
	return fields
}

func (a *App) receiveOpenCode(ctx context.Context) bool {
	changed := false
	for {
		select {
		case reply := <-a.openCode.results:
			if reply.token != a.openCode.token {
				if reply.process != nil {
					_ = reply.process.Close()
				}
				continue
			}
			if reply.operation >= openCodeOperationSessions {
				a.receiveOpenCodePanelReply(reply)
				a.refresh()
				changed = true
				continue
			}
			if reply.err != nil {
				a.openCode.connected = reply.operation == openCodeOperationConnectProvider && reply.serverReachable
				a.openCode.status = reply.safeStatus
				if a.openCode.status == "" {
					if reply.operation == openCodeOperationConnectProvider && reply.serverReachable {
						a.openCode.status = "OpenCode could not connect the provider key. Check the selected integration and try again."
					} else {
						a.openCode.status = openCodeFailureStatus(reply.err)
					}
				}
				if reply.process != nil {
					_ = reply.process.Close()
					if a.openCode.process == reply.process {
						a.openCode.process = nil
					}
				}
			} else {
				a.openCode.client = reply.client
				if reply.process != nil {
					a.openCode.process = reply.process
				}
				if reply.serverURL != "" && a.settings.OpenCode != nil && a.settings.OpenCode.Mode == openCodeModeOwned {
					settings := openCodeSettingsValue(a.settings.OpenCode)
					settings.ServerURL = reply.serverURL
					a.settings.OpenCode = &settings
					a.openCode.serverID = a.openCodeServerID(reply.serverURL)
					a.persistSettings()
				}
				a.openCode.connected = true
				a.openCode.integrations = reply.integrations
				a.openCode.status = "Connected to OpenCode " + strings.TrimSpace(reply.serverInfo.Version)
				if reply.process != nil {
					a.openCode.status = "Local OpenCode server is running."
				}
				if reply.operation == openCodeOperationConnectProvider {
					a.openCode.status = "Provider credential connected by OpenCode."
				}
				if reply.integrationErr != nil {
					a.openCode.status = "Connected, but provider integrations could not be loaded."
				}
				if reply.passwordSaveErr != nil {
					a.openCode.status += " Server password could not be saved to the user config directory."
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
				if err := a.page.Redraw(ctx); err != nil {
					return false
				}
			}
			return changed
		}
	}
}

func (a *App) startOwnedOpenCode(clientURL string, password string, savePassword bool) error {
	parsed, err := url.Parse(clientURL)
	if err != nil || parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && !openCodeLoopbackHost(parsed.Hostname()) {
		return errors.New("Starting an owned OpenCode server requires a loopback address.")
	}
	if a.openCode.process != nil {
		select {
		case <-a.openCode.process.Done():
			_ = a.openCode.process.Close()
			a.openCode.process = nil
		default:
			if a.openCode.process.URL() == clientURL {
				a.checkOpenCodeConnection(a.openCode.process.Client(), "", false)
				return nil
			}
			return errors.New("An own-code managed OpenCode server is already running at a different address.")
		}
	}
	if password == "" {
		password, err = generateOpenCodePassword()
		if err != nil {
			return errors.New("Could not generate a password for the local OpenCode server.")
		}
		savePassword = true
	}

	port := 0
	if parsed.Port() != "" {
		var rawPort string
		_, rawPort, err = net.SplitHostPort(parsed.Host)
		if err != nil {
			return errors.New("Enter a valid loopback address with a port for the local OpenCode server.")
		}
		port, err = strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return errors.New("Enter a valid loopback address with a port for the local OpenCode server.")
		}
	}
	token, _ := a.beginOpenCodeOperation(openCodeOperationCheck)
	a.openCode.status = "Starting a local OpenCode server…"
	a.openCode.connected = false
	a.refresh()

	procCtx, procCancel := context.WithCancel(a.openCode.lifeCtx)
	result := a.openCode.results
	directory := a.ws.Root
	go func() {
		process, err := opencode.StartServer(procCtx, opencode.ServerOptions{
			Directory: directory,
			Password:  password,
			Port:      port,
		})
		reply := openCodeReply{token: token, operation: openCodeOperationCheck, err: err}
		if err == nil {
			reply.process = process
			reply.client = process.Client()
			reply.serverURL = process.URL()
			ctx, cancel := context.WithTimeout(procCtx, openCodeRequestTimeout)
			reply.serverInfo, reply.err = process.Client().GetInfo(ctx)
			reply.serverReachable = reply.err == nil
			if reply.err == nil {
				reply.integrations, reply.integrationErr = process.Client().ListIntegrations(ctx, directory)
			}
			cancel()
			if reply.err == nil && savePassword {
				reply.passwordSaveErr = saveOpenCodeServerPassword(password)
			}
		}
		if reply.err != nil && process == nil {
			procCancel()
		}
		select {
		case result <- reply:
		default:
			if process != nil {
				_ = process.Close()
			}
		}
		a.page.WantFrameIn(0)
	}()
	return nil
}

func integrationSupportsKey(integrations []opencode.Integration, integrationID string) bool {
	for _, integration := range integrations {
		if integration.ID != integrationID {
			continue
		}
		for _, method := range integration.Methods {
			if strings.EqualFold(method.Type, "key") {
				return true
			}
		}
		return false
	}
	return false
}

func openCodeIntegrationRows(integrations []opencode.Integration) []openCodeIntegrationRow {
	rows := make([]openCodeIntegrationRow, 0, len(integrations))
	for _, integration := range integrations {
		if sanitizeOpenCodeIntegrationID(integration.ID) == "" {
			continue
		}
		rows = append(rows, openCodeIntegrationRow{
			ID:        integration.ID,
			Name:      integration.Name,
			KeyAuth:   integrationSupportsKey([]opencode.Integration{integration}, integration.ID),
			Connected: len(integration.Connections) > 0,
		})
	}
	return rows
}

func generateOpenCodePassword() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func (a *App) closeOpenCodeRuntime() {
	if a == nil {
		return
	}
	a.cancelOpenCodeOperation()
	if a.openCode.lifeCancel != nil {
		a.openCode.lifeCancel()
		a.openCode.lifeCancel = nil
	}
	if a.openCode.process != nil {
		_ = a.openCode.process.Close()
		a.openCode.process = nil
	}
	if a.openCode.store != nil {
		_ = a.openCode.store.Close()
		a.openCode.store = nil
	}
}

func openCodeFailureStatus(err error) string {
	var httpErr *opencode.HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusUnauthorized:
			return "OpenCode rejected the server password."
		case http.StatusNotFound:
			return "This server does not expose the required OpenCode V2 API."
		default:
			return fmt.Sprintf("OpenCode returned HTTP %d.", httpErr.StatusCode)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "OpenCode did not respond before the connection timed out."
	}
	return "Could not reach the OpenCode server. Check the address and try again."
}

func resolveOpenCodePassword(input string) (password string, provided bool, err error) {
	password = input
	if strings.TrimSpace(password) != "" {
		return password, true, nil
	}
	password, err = loadOpenCodeServerPassword()
	if errors.Is(err, secretstore.ErrNotFound) {
		return "", false, err
	}
	return password, false, err
}
