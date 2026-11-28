package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/chinmay-sawant/own-code/internal/opencode"
)

// DefaultProviderID is the backend registered by NewProviderRegistry. It is
// also the value stored in a session's agent column.
const DefaultProviderID = "opencode"

// ProviderDescriptor describes one inference backend. Models carry the
// catalog's own provider IDs (for example "anthropic") inside a backend; the
// descriptor ID names the backend that serves them, so the UI never needs to
// hardcode a vendor.
type ProviderDescriptor struct {
	ID          string
	DisplayName string
	// LoadCatalog returns the models the credential can use. Callers pass the
	// result through FilterEligible before offering it for selection.
	LoadCatalog func(ctx context.Context, apiKey string) ([]opencode.ConsoleModel, error)
	// NewRequestProvider builds the request sender for a credential.
	NewRequestProvider func(apiKey string) (Provider, error)
	// Resolve maps a catalog model to its routing, failing closed.
	Resolve func(opencode.ConsoleModel) (ModelTarget, error)
}

func (d ProviderDescriptor) validate() error {
	if strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.DisplayName) == "" {
		return errors.New("provider descriptor needs an ID and display name")
	}
	if d.LoadCatalog == nil || d.NewRequestProvider == nil || d.Resolve == nil {
		return fmt.Errorf("provider %q needs a catalog loader, request builder, and resolver", d.ID)
	}
	return nil
}

// ProviderRegistry holds the registered backends.
type ProviderRegistry struct {
	mu    sync.RWMutex
	items map[string]ProviderDescriptor
}

// NewProviderRegistry returns a registry with the OpenCode Console backend, the
// only provider today.
func NewProviderRegistry() *ProviderRegistry {
	r := &ProviderRegistry{items: map[string]ProviderDescriptor{}}
	_ = r.Register(OpenCodeConsoleDescriptor())
	return r
}

// Register adds a backend. Duplicate IDs are rejected.
func (r *ProviderRegistry) Register(d ProviderDescriptor) error {
	if err := d.validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.items[d.ID]; dup {
		return fmt.Errorf("provider %q is already registered", d.ID)
	}
	r.items[d.ID] = d
	return nil
}

// Get returns a backend by ID.
func (r *ProviderRegistry) Get(id string) (ProviderDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.items[id]
	return d, ok
}

// List returns every backend ordered by ID.
func (r *ProviderRegistry) List() []ProviderDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProviderDescriptor, 0, len(r.items))
	for _, d := range r.items {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Default is the backend used when a session names none.
func (r *ProviderRegistry) Default() (ProviderDescriptor, bool) { return r.Get(DefaultProviderID) }

// OpenCodeConsoleDescriptor is the OpenCode Console backend.
func OpenCodeConsoleDescriptor() ProviderDescriptor {
	return ProviderDescriptor{
		ID:          DefaultProviderID,
		DisplayName: "OpenCode Console",
		LoadCatalog: func(ctx context.Context, apiKey string) ([]opencode.ConsoleModel, error) {
			client, err := opencode.NewConsoleClient(apiKey)
			if err != nil {
				return nil, err
			}
			return client.Models(ctx)
		},
		NewRequestProvider: func(apiKey string) (Provider, error) {
			return NewClient(apiKey, Options{})
		},
		Resolve: ResolveModel,
	}
}

// ModelChoice is one selectable model row. BackendID is the registered
// provider that serves it; ProviderID/ProviderName are the catalog's grouping.
type ModelChoice struct {
	BackendID    string
	ProviderID   string
	ProviderName string
	ModelID      string
	Name         string
}

// Key is the stable "provider/model" selection value.
func (c ModelChoice) Key() string { return c.ProviderID + "/" + c.ModelID }

// ModelGroup is one catalog provider's models.
type ModelGroup struct {
	ProviderID   string
	ProviderName string
	Models       []ModelChoice
}

// GroupEligible filters a catalog and groups it by provider, preserving the
// catalog order of first appearance.
func GroupEligible(backendID string, models []opencode.ConsoleModel) []ModelGroup {
	var groups []ModelGroup
	index := map[string]int{}
	for _, m := range FilterEligible(models) {
		i, ok := index[m.ProviderID]
		if !ok {
			name := strings.TrimSpace(m.ProviderName)
			if name == "" {
				name = m.ProviderID
			}
			groups = append(groups, ModelGroup{ProviderID: m.ProviderID, ProviderName: name})
			i = len(groups) - 1
			index[m.ProviderID] = i
		}
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = m.ID
		}
		groups[i].Models = append(groups[i].Models, ModelChoice{
			BackendID: backendID, ProviderID: m.ProviderID, ProviderName: groups[i].ProviderName,
			ModelID: m.ID, Name: name,
		})
	}
	return groups
}
