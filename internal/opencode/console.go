package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	consoleModelsURL       = "https://opencode.ai/console/api/v1/models"
	consoleRequestTimeout  = 12 * time.Second
	consoleMaxResponseSize = 8 << 20
)

// ConsoleClient calls the OpenCode Console API directly. It does not connect
// to an OpenCode Server process or create remote sessions.
type ConsoleClient struct {
	apiKey string
	http   *http.Client
}

// ConsoleModel is one model available to the API key's workspace.
type ConsoleModel struct {
	ProviderID     string
	ProviderName   string
	ID             string
	Name           string
	Type           string
	Description    string
	ContextLimit   int
	OutputLimit    int
	ToolsSupported bool
	ToolsKnown     bool
	APIs           map[string]ConsoleModelAPI
}

// ConsoleModelAPI describes one inference protocol exposed for a model.
type ConsoleModelAPI struct {
	BaseURL string
}

// SupportsAgentRuntime reports whether the catalog explicitly advertises a
// supported tool-capable chat protocol for this model.
func (m ConsoleModel) SupportsAgentRuntime() bool {
	if !strings.EqualFold(strings.TrimSpace(m.Type), "chat") || !m.ToolsKnown || !m.ToolsSupported {
		return false
	}
	for protocol, api := range m.APIs {
		if protocol != "chat-completions" && protocol != "responses" {
			continue
		}
		if strings.TrimSpace(api.BaseURL) != "" {
			return true
		}
	}
	return false
}

// ConsoleHTTPError reports only the response status, never the request headers.
type ConsoleHTTPError struct {
	StatusCode int
}

func (e *ConsoleHTTPError) Error() string {
	return fmt.Sprintf("OpenCode Console returned HTTP %d", e.StatusCode)
}

// NewConsoleClient creates a Console API client from a workspace-bound API key.
func NewConsoleClient(apiKey string) (*ConsoleClient, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("OpenCode Console API key is required")
	}
	client := &http.Client{
		Timeout: consoleRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &ConsoleClient{apiKey: apiKey, http: client}, nil
}

// String returns a representation that never includes the Console API key.
func (c *ConsoleClient) String() string { return "OpenCodeConsoleClient{key:[redacted]}" }

// GoString returns a redacted representation for %#v formatting.
func (c *ConsoleClient) GoString() string { return c.String() }

// Models fetches the models available to this key's workspace.
func (c *ConsoleClient) Models(ctx context.Context) ([]ConsoleModel, error) {
	if c == nil || c.http == nil || c.apiKey == "" {
		return nil, errors.New("OpenCode Console client is not configured")
	}
	if ctx == nil {
		return nil, errors.New("OpenCode Console request context is nil")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, consoleModelsURL, nil)
	if err != nil {
		return nil, errors.New("could not create OpenCode Console model request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.apiKey)

	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("OpenCode Console model request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &ConsoleHTTPError{StatusCode: response.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, consoleMaxResponseSize+1))
	if err != nil {
		return nil, errors.New("could not read OpenCode Console model response")
	}
	if len(body) > consoleMaxResponseSize {
		return nil, errors.New("OpenCode Console model response exceeds the size limit")
	}
	var providers map[string]consoleProvider
	if err := json.Unmarshal(body, &providers); err != nil {
		return nil, fmt.Errorf("could not decode OpenCode Console model response: %w", err)
	}

	models := make([]ConsoleModel, 0)
	for providerKey, provider := range providers {
		providerID := strings.TrimSpace(provider.ID)
		if providerID == "" {
			providerID = providerKey
		}
		for modelKey, model := range provider.Models {
			id := strings.TrimSpace(model.ID)
			if id == "" {
				id = modelKey
			}
			if id == "" {
				continue
			}
			apis := make(map[string]ConsoleModelAPI, len(model.API))
			for protocol, api := range model.API {
				apis[protocol] = ConsoleModelAPI{BaseURL: strings.TrimSpace(api.BaseURL)}
			}
			toolsKnown := model.Capabilities.Tools.Supported != nil
			toolsSupported := toolsKnown && *model.Capabilities.Tools.Supported
			models = append(models, ConsoleModel{
				ProviderID:     providerID,
				ProviderName:   strings.TrimSpace(provider.Name),
				ID:             id,
				Name:           strings.TrimSpace(model.Name),
				Type:           strings.TrimSpace(model.Type),
				Description:    strings.TrimSpace(model.Description),
				ContextLimit:   model.Limit.Context,
				OutputLimit:    model.Limit.Output,
				ToolsSupported: toolsSupported,
				ToolsKnown:     toolsKnown,
				APIs:           apis,
			})
		}
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].ProviderID != models[j].ProviderID {
			return models[i].ProviderID < models[j].ProviderID
		}
		leftName := strings.ToLower(models[i].Name)
		rightName := strings.ToLower(models[j].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		return models[i].ID < models[j].ID
	})
	return models, nil
}

type consoleProvider struct {
	ID     string                  `json:"id"`
	Name   string                  `json:"name"`
	Models map[string]consoleModel `json:"models"`
}

type consoleModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Limit       struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Capabilities struct {
		Tools struct {
			Supported *bool `json:"supported"`
		} `json:"tools"`
	} `json:"capabilities"`
	API map[string]struct {
		BaseURL string `json:"base_url"`
	} `json:"api"`
}
