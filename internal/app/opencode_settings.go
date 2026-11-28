package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/opencode"
	"github.com/chinmay-sawant/own-code/internal/secretstore"
)

const (
	openCodeModeOwned        = "owned"
	openCodeModeAttach       = "attach"
	openCodeDefaultURL       = "http://127.0.0.1:4096"
	openCodeMaxIntegrationID = 100
)

var (
	errInvalidOpenCodeServerURL = errors.New("Enter a valid OpenCode server address without credentials or a path prefix.")
	errOpenCodeRemoteNeedsHTTPS = errors.New("Use HTTPS for remote OpenCode servers; HTTP is allowed only on localhost or a loopback IP.")
)

// opencodeSettings contains only non-secret connection metadata. Credentials
// are entered into native form controls and are read through FormValue only
// when a connection action is submitted.
type opencodeSettings struct {
	Mode          string `json:"mode,omitempty"`
	ServerURL     string `json:"serverUrl,omitempty"`
	IntegrationID string `json:"integrationId,omitempty"`
}

func defaultOpenCodeSettings() opencodeSettings {
	return opencodeSettings{
		Mode:      openCodeModeAttach,
		ServerURL: openCodeDefaultURL,
	}
}

func (s *opencodeSettings) sanitize() *opencodeSettings {
	if s == nil {
		return nil
	}
	out := *s
	if out.Mode != openCodeModeOwned && out.Mode != openCodeModeAttach {
		out.Mode = openCodeModeAttach
	}
	if normalized, err := normalizeOpenCodeServerURL(out.ServerURL); err == nil {
		out.ServerURL = normalized
	} else {
		out.ServerURL = openCodeDefaultURL
	}
	out.IntegrationID = sanitizeOpenCodeIntegrationID(out.IntegrationID)
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

func openCodeSetupStatus() string {
	return "Not connected. Enter the OpenCode server address and check the connection."
}

// normalizeOpenCodeServerURL rejects embedded credentials and cleartext
// remote endpoints. A path is also rejected because the OpenCode server API
// routes are rooted at /api and are not mounted under a user path prefix.
func normalizeOpenCodeServerURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > maxFieldLen {
		return "", errInvalidOpenCodeServerURL
	}
	u, err := url.Parse(value)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errInvalidOpenCodeServerURL
	}
	if u.Path != "" && u.Path != "/" {
		return "", errInvalidOpenCodeServerURL
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	switch strings.ToLower(u.Scheme) {
	case "http":
		if !openCodeLoopbackHost(host) {
			return "", errOpenCodeRemoteNeedsHTTPS
		}
	case "https":
	default:
		return "", errInvalidOpenCodeServerURL
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	u.ForceQuery = false
	return strings.TrimRight(u.String(), "/"), nil
}

func openCodeLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sanitizeOpenCodeIntegrationID(raw string) string {
	value := strings.TrimSpace(raw)
	if len(value) > openCodeMaxIntegrationID {
		return ""
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return ""
		}
	}
	return value
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
	a.page.Form().SetFormValue("opencode-server-password", "")
	a.page.Form().SetFormValue("opencode-api-key", "")
	cleared := make(map[string]struct{})
	for _, integration := range a.openCode.integrations {
		for _, method := range integration.Methods {
			if !strings.EqualFold(method.Type, "key") {
				continue
			}
			fields, err := ParseOpenCodeProviderKeyFields(method)
			if err != nil {
				continue
			}
			for _, field := range fields {
				if _, ok := cleared[field.Key]; ok {
					continue
				}
				a.page.Form().SetFormValue("opencode-provider-"+field.Key, "")
				cleared[field.Key] = struct{}{}
			}
		}
	}
}

func (a *App) openCodeProviderAnswer(integrationID string) (json.RawMessage, error) {
	method, ok := openCodeKeyMethod(a.openCode.integrations, integrationID)
	if !ok {
		return nil, errors.New("OpenCode did not advertise a key authentication method for the selected provider")
	}
	fields, err := ParseOpenCodeProviderKeyFields(method)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(fields))
	for _, field := range fields {
		if field.Hidden {
			continue
		}
		values[field.Key] = a.opencodeCredentialInput("opencode-provider-" + field.Key)
	}
	answer, err := BuildOpenCodeProviderAnswer(method, values)
	if err != nil {
		return nil, err
	}
	return answer.JSON()
}

// opencodeAction validates connection metadata and passes one-time credentials
// to the OpenCode runtime without copying them into View or persisted settings.
func (a *App) opencodeAction(ctx context.Context, action string) error {
	switch action {
	case "opencode-mode:owned", "opencode-mode:attach":
		settings := openCodeSettingsValue(a.settings.OpenCode)
		mode := strings.TrimPrefix(action, "opencode-mode:")
		if settings.Mode != mode {
			a.retireOpenCodeConnection()
			a.openCode.status = "OpenCode mode changed. Check the server connection to continue."
		}
		settings.Mode = mode
		a.settings.OpenCode = &settings
		a.settingsNote = "OpenCode mode saved."
		a.persistSettings()
		return a.redrawSettings(ctx)
	case "opencode-provider-fields":
		a.clearOpenCodeCredentialInputs()
		settings := openCodeSettingsValue(a.settings.OpenCode)
		integrationID := sanitizeOpenCodeIntegrationID(a.view.OpenCodeIntegrationID)
		if strings.TrimSpace(a.view.OpenCodeIntegrationID) != "" && integrationID == "" {
			a.settingsNote = "Provider integration ID may contain only letters, numbers, dots, underscores, and hyphens."
			return a.redrawSettings(ctx)
		}
		settings.IntegrationID = integrationID
		a.settings.OpenCode = &settings
		a.persistSettings()
		a.settingsNote = "Provider fields loaded from the selected OpenCode integration."
		return a.redrawSettings(ctx)
	case "opencode-save":
		a.clearOpenCodeCredentialInputs()
		settings := openCodeSettingsValue(a.settings.OpenCode)
		oldServerURL := settings.ServerURL
		serverURL, err := normalizeOpenCodeServerURL(a.view.OpenCodeServerURL)
		if err != nil {
			a.settingsNote = err.Error()
			return a.redrawSettings(ctx)
		}
		integrationID := sanitizeOpenCodeIntegrationID(a.view.OpenCodeIntegrationID)
		if strings.TrimSpace(a.view.OpenCodeIntegrationID) != "" && integrationID == "" {
			a.settingsNote = "Provider integration ID may contain only letters, numbers, dots, underscores, and hyphens."
			return a.redrawSettings(ctx)
		}
		settings.ServerURL = serverURL
		settings.IntegrationID = integrationID
		a.settings.OpenCode = &settings
		if oldServerURL != serverURL {
			a.retireOpenCodeConnection()
			a.openCode.status = "Connection settings changed. Check the new server address."
		}
		a.settingsNote = "OpenCode connection settings saved. Credentials are not stored in settings.json."
		a.persistSettings()
		return a.redrawSettings(ctx)
	case "opencode-check-connection", "opencode-connect-provider":
		passwordInput := a.opencodeCredentialInput("opencode-server-password")
		key := ""
		var answer json.RawMessage
		var answerErr error
		if action == "opencode-connect-provider" {
			key = a.opencodeCredentialInput("opencode-api-key")
			answer, answerErr = a.openCodeProviderAnswer(a.view.OpenCodeIntegrationID)
		}
		a.clearOpenCodeCredentialInputs()
		settings := openCodeSettingsValue(a.settings.OpenCode)
		serverURL, err := normalizeOpenCodeServerURL(a.view.OpenCodeServerURL)
		if err != nil {
			a.openCode.status = err.Error()
			a.openCode.connected = false
			return a.redrawSettings(ctx)
		}
		settings.ServerURL = serverURL
		integrationID := sanitizeOpenCodeIntegrationID(a.view.OpenCodeIntegrationID)
		if strings.TrimSpace(a.view.OpenCodeIntegrationID) != "" && integrationID == "" {
			a.openCode.status = "Provider integration ID may contain only letters, numbers, dots, underscores, and hyphens."
			return a.redrawSettings(ctx)
		}
		settings.IntegrationID = integrationID
		a.settings.OpenCode = &settings
		a.persistSettings()

		if action == "opencode-check-connection" {
			password, provided, err := resolveOpenCodePassword(passwordInput)
			if err != nil && !errors.Is(err, secretstore.ErrNotFound) {
				a.openCode.status = "Could not read the saved OpenCode server password."
				return a.redrawSettings(ctx)
			}
			if settings.Mode == openCodeModeOwned {
				if err := a.startOwnedOpenCode(settings.ServerURL, password, provided); err != nil {
					a.openCode.status = err.Error()
					return a.redrawSettings(ctx)
				}
				return a.redrawSettings(ctx)
			}
			client, err := opencode.NewClient(settings.ServerURL, password)
			if err != nil {
				a.openCode.status = err.Error()
				a.openCode.connected = false
				return a.redrawSettings(ctx)
			}
			a.openCode.serverID = a.openCodeServerID(settings.ServerURL)
			a.checkOpenCodeConnection(client, password, provided)
			return a.redrawSettings(ctx)
		}
		if answerErr != nil {
			a.openCode.status = "Provider details are incomplete or no longer match OpenCode's advertised form. Load the selected provider fields and try again."
			return a.redrawSettings(ctx)
		}

		password, provided, passwordErr := resolveOpenCodePassword(passwordInput)
		if key == "" {
			a.openCode.status = "Enter a provider API key before connecting it."
			return a.redrawSettings(ctx)
		}
		if integrationID == "" {
			a.openCode.status = "Choose an OpenCode provider integration first."
			return a.redrawSettings(ctx)
		}
		if passwordErr != nil && !errors.Is(passwordErr, secretstore.ErrNotFound) {
			a.openCode.status = "Could not read the saved OpenCode server password."
			return a.redrawSettings(ctx)
		}
		client := a.openCode.client
		serverID := a.openCodeServerID(settings.ServerURL)
		if client == nil || a.openCode.serverID != serverID || provided {
			if settings.Mode == openCodeModeOwned {
				if a.openCode.process == nil || a.openCode.process.URL() != settings.ServerURL {
					a.openCode.status = "Start or check the owned local OpenCode server before connecting a provider key."
					return a.redrawSettings(ctx)
				}
				client = a.openCode.process.Client()
				provided = false
			} else {
				client, err = opencode.NewClient(settings.ServerURL, password)
				if err != nil {
					a.openCode.status = err.Error()
					return a.redrawSettings(ctx)
				}
			}
		}
		a.openCode.serverID = serverID
		a.connectOpenCodeProvider(client, integrationID, key, answer, password, provided)
		return a.redrawSettings(ctx)
	default:
		return nil
	}
}
