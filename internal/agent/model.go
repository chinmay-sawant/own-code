package agent

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/opencode"
)

// ResolveModel returns the routing for a catalog model, failing closed when
// the model is not a chat model with explicit tool support and a supported
// protocol with a base_url. Preference order: chat-completions, responses,
// messages (Anthropic).
func ResolveModel(m opencode.ConsoleModel) (ModelTarget, error) {
	if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.ProviderID) == "" {
		return ModelTarget{}, errors.New("model is missing provider or model ID")
	}
	if !strings.EqualFold(strings.TrimSpace(m.Type), "chat") {
		return ModelTarget{}, errors.New("model is not a chat model")
	}
	if !m.ToolsKnown || !m.ToolsSupported {
		return ModelTarget{}, errors.New("model does not advertise tool support")
	}
	for _, protocol := range []string{ProtocolChatCompletions, ProtocolResponses, ProtocolMessages} {
		api, ok := m.APIs[protocol]
		if !ok || strings.TrimSpace(api.BaseURL) == "" {
			continue
		}
		target := ModelTarget{
			ProviderID: m.ProviderID, ModelID: m.ID, Protocol: protocol,
			BaseURL: strings.TrimSpace(api.BaseURL), ToolsSupported: true,
		}
		if err := target.Validate(); err != nil {
			return ModelTarget{}, err
		}
		return target, nil
	}
	return ModelTarget{}, errors.New("model advertises no supported protocol (chat-completions, responses, or messages) with a base_url")
}

// Eligible reports whether the model may be selected for agent runs.
func Eligible(m opencode.ConsoleModel) bool {
	_, err := ResolveModel(m)
	return err == nil
}

// Validate rechecks the routing metadata; adapters call it for every request.
func (t ModelTarget) Validate() error {
	if strings.TrimSpace(t.ProviderID) == "" || strings.TrimSpace(t.ModelID) == "" {
		return errors.New("provider and model IDs are required")
	}
	if !t.ToolsSupported {
		return errors.New("model does not advertise tool support")
	}
	if t.Protocol != ProtocolChatCompletions && t.Protocol != ProtocolResponses && t.Protocol != ProtocolMessages {
		return fmt.Errorf("unsupported inference protocol %q", t.Protocol)
	}
	_, err := t.endpoint()
	return err
}

// endpoint builds the request URL. https is required except for loopback hosts.
func (t ModelTarget) endpoint() (string, error) {
	base := strings.TrimSpace(t.BaseURL)
	if base == "" {
		return "", errors.New("model base_url is required")
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil {
		return "", errors.New("model base_url is invalid")
	}
	host := u.Hostname()
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", errors.New("model base_url must use https")
	}
	u.RawQuery, u.Fragment = "", ""
	suffix := "/chat/completions"
	switch t.Protocol {
	case ProtocolResponses:
		suffix = "/responses"
	case ProtocolMessages:
		suffix = "/messages"
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, suffix) {
		u.Path += suffix
	}
	return u.String(), nil
}

// FilterEligible returns, in order, the models that may be selected for agent
// runs (chat models with advertised tool support and a supported protocol).
// The input slice is not modified.
func FilterEligible(models []opencode.ConsoleModel) []opencode.ConsoleModel {
	out := make([]opencode.ConsoleModel, 0, len(models))
	for _, m := range models {
		if Eligible(m) {
			out = append(out, m)
		}
	}
	return out
}
