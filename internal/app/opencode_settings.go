package app

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/agent"
	"github.com/chinmay-sawant/own-code/internal/opencode"
	"github.com/chinmay-sawant/own-code/internal/secretstore"
)

// opencodeSettings contains selected non-secret provider/model identifiers.
// The Console API key is stored separately from application settings.
type opencodeSettings struct {
	ProviderID string `json:"providerId,omitempty"`
	ModelID    string `json:"modelId,omitempty"`
	// DockWidth is the agent dock's base width in px (0 = default).
	DockWidth int `json:"dockWidth,omitempty"`
}

func defaultOpenCodeSettings() opencodeSettings { return opencodeSettings{} }

func (s *opencodeSettings) sanitize() *opencodeSettings {
	if s == nil {
		return nil
	}
	out := *s
	out.ProviderID = strings.TrimSpace(out.ProviderID)
	out.ModelID = strings.TrimSpace(out.ModelID)
	if len(out.ProviderID) > maxFieldLen {
		out.ProviderID = ""
	}
	if len(out.ModelID) > maxFieldLen {
		out.ModelID = ""
	}
	if out.DockWidth < openCodeDockMin || out.DockWidth > openCodeDockSavedMax {
		out.DockWidth = 0
	}
	return &out
}

func openCodeSettingsValue(s *opencodeSettings) opencodeSettings {
	if s == nil {
		return defaultOpenCodeSettings()
	}
	clean := s.sanitize()
	if clean == nil {
		return defaultOpenCodeSettings()
	}
	return *clean
}

func (a *App) openCodeSettingsView() opencodeSettings {
	if a == nil {
		return defaultOpenCodeSettings()
	}
	return openCodeSettingsValue(a.settings.OpenCode)
}

func (a *App) findOpenCodeModel(selection string) (providerID, modelID string, ok bool) {
	providerID, modelID, found := strings.Cut(strings.TrimSpace(selection), "/")
	if !found || providerID == "" || modelID == "" {
		return "", "", false
	}
	for _, model := range agent.FilterEligible(a.openCode.consoleModels) {
		if model.ProviderID == providerID && model.ID == modelID {
			return providerID, modelID, true
		}
	}
	return "", "", false
}

func openCodeModelSelection(providerID, modelID string) string {
	if strings.TrimSpace(providerID) == "" || strings.TrimSpace(modelID) == "" {
		return ""
	}
	return providerID + "/" + modelID
}

func openCodeSetupStatus() string {
	return "Not connected. Enter a Console API key or use OPENCODE_API_KEY."
}

// opencodeCredentialInput reads a secret directly from the native form
// state. Neither credential is copied to a View field or settings snapshot.
func (a *App) opencodeCredentialInput(id string) string {
	if a == nil || a.page == nil {
		return ""
	}
	return a.page.Form().FormValue(id)
}

func (a *App) clearOpenCodeCredentialInputs() {
	if a == nil || a.page == nil {
		return
	}
	a.page.Form().SetFormValue("opencode-api-key", "")
}

// opencodeAction connects to the OpenCode Console directly. API keys stay in
// native form state until the catalog request succeeds and are never copied to
// View or settings.json.
func (a *App) opencodeAction(ctx context.Context, action string) error {
	switch action {
	case "opencode-model-save":
		settings := openCodeSettingsValue(a.settings.OpenCode)
		providerID, modelID, ok := a.findOpenCodeModel(a.view.OpenCodeModelSelection)
		if !ok {
			a.settingsNote = "Choose a chat model from the connected OpenCode Console catalog first."
			return a.redrawSettings(ctx)
		}
		settings.ProviderID = providerID
		settings.ModelID = modelID
		a.settings.OpenCode = &settings
		a.settingsNote = "OpenCode model saved."
		a.persistSettings()
		return a.redrawSettings(ctx)
	case "opencode-connect":
		key := strings.TrimSpace(a.opencodeCredentialInput("opencode-api-key"))
		a.clearOpenCodeCredentialInputs()
		provided := key != ""
		if !provided {
			var err error
			key, err = a.loadOpenCodeKey()
			if err != nil {
				if errors.Is(err, secretstore.ErrNotFound) {
					a.openCode.status = "Enter a workspace API key, save one first, or set OPENCODE_API_KEY in .env."
				} else {
					a.openCode.status = "Could not read the saved OpenCode Console API key."
				}
				return a.redrawSettings(ctx)
			}
		}
		if err := a.startOpenCodeConnect(key, provided); err != nil {
			a.openCode.status = "The OpenCode Console API key is invalid."
			return a.redrawSettings(ctx)
		}
		return a.redrawSettings(ctx)
	case "opencode-key-clear":
		if err := a.deleteOpenCodeKey(); err != nil {
			a.openCode.status = "Could not clear the saved OpenCode Console API key."
			return a.redrawSettings(ctx)
		}
		a.retireOpenCodeConnection()
		a.openCode.status = "Saved OpenCode Console API key cleared."
		return a.redrawSettings(ctx)
	default:
		return nil
	}
}

// AutoConnectOpenCode checks for a persisted Console API key (.env, user
// secret store, or process environment) and connects in the background so the
// user does not have to re-enter the key on every launch.
func (a *App) AutoConnectOpenCode() {
	if a == nil || a.openCode.connected || a.openCode.operation != openCodeOperationNone {
		return
	}
	key, err := a.loadOpenCodeKey()
	if err != nil || strings.TrimSpace(key) == "" {
		return
	}
	_ = a.startOpenCodeConnect(key, false)
}

func (a *App) startOpenCodeConnect(key string, save bool) error {
	client, err := opencode.NewConsoleClient(key)
	if err != nil {
		return err
	}
	a.retireOpenCodeConnection()
	token, baseCtx := a.beginOpenCodeOperation(openCodeOperationCheck)
	a.openCode.connected = false
	a.openCode.status = "Loading the OpenCode Console model catalog…"
	a.refresh()
	result := a.openCode.results
	go func() {
		ctx, cancel := context.WithTimeout(baseCtx, openCodeRequestTimeout)
		defer cancel()
		reply := openCodeReply{token: token, operation: openCodeOperationCheck, consoleClient: client}
		reply.consoleModels, reply.err = client.Models(ctx)
		client.Forget() // the app keeps the client only as a connected marker
		if reply.err == nil && save {
			reply.apiKeySaveErr = a.saveOpenCodeKey(key)
		}
		if reply.err == nil {
			reply.safeStatus = "Connected to OpenCode Console. Loaded " + strconv.Itoa(len(reply.consoleModels)) + " models."
		}
		a.sendOpenCodeReply(baseCtx, result, reply)
	}()
	return nil
}
