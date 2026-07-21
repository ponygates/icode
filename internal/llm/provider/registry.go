// Package registry provides the central Provider registry for iCode.
// It acts as the service-locator that resolves model IDs to concrete Provider implementations.
package registry

import (
	"context"
	"fmt"
	"sync"

	"github.com/ponygates/icode/internal/types"
)

// Registry is the default global provider registry.
var Default = NewRegistry()

// Impl is a thread-safe Provider registry.
type Impl struct {
	mu        sync.RWMutex
	providers map[string]types.Provider

	// customModels holds user-defined models keyed by their canonical id
	// (e.g. "deepseek/my-model"). customAliases maps an alternate id (e.g. the
	// raw provider model id "my-model") to the canonical id so either resolves.
	customModels  map[string]types.ModelInfo
	customAliases map[string]string
}

// NewRegistry creates an empty Provider registry.
func NewRegistry() *Impl {
	return &Impl{
		providers: make(map[string]types.Provider),
	}
}

// Register adds or replaces a provider.
func (r *Impl) Register(p types.Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if p == nil {
		return fmt.Errorf("cannot register nil provider")
	}
	name := p.Name()
	if name == "" {
		return fmt.Errorf("provider name is empty")
	}
	r.providers[name] = p
	return nil
}

// Deregister removes a provider by name.
func (r *Impl) Deregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.providers, name)
}

// RegisterCustomModel adds or updates a user-defined model mapping so it can be
// resolved by ResolveModel. alias is an optional alternate id that should also
// resolve to the same model (used when the UI stores both "provider/model" and
// the raw provider model id).
func (r *Impl) RegisterCustomModel(m types.ModelInfo, alias string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.customModels == nil {
		r.customModels = map[string]types.ModelInfo{}
		r.customAliases = map[string]string{}
	}
	if m.Provider == "" {
		// Fall back to the canonical id prefix if no provider is set.
		if idx := indexOfSlash(m.ID); idx > 0 {
			m.Provider = m.ID[:idx]
		}
	}
	r.customModels[m.ID] = m
	if alias != "" && alias != m.ID {
		r.customAliases[alias] = m.ID
	}
}

// RemoveCustomModel removes a user-defined model by its canonical id.
func (r *Impl) RemoveCustomModel(canonicalID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.customModels == nil {
		return
	}
	delete(r.customModels, canonicalID)
	for k, v := range r.customAliases {
		if v == canonicalID {
			delete(r.customAliases, k)
		}
	}
}

// indexOfSlash returns the index of the first '/' in s, or -1.
func indexOfSlash(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

// SetCredentials pushes updated API credentials into a registered provider if
// it implements the optional CredentialedProvider interface. It returns true
// if the provider was found and updated.
func (r *Impl) SetCredentials(name, apiKey, apiBase string) bool {
	r.mu.RLock()
	p, ok := r.providers[name]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	cp, ok := p.(types.CredentialedProvider)
	if !ok {
		return false
	}
	cp.SetCredentials(apiKey, apiBase)
	return true
}

// SetTimeout updates a provider's HTTP client timeout at runtime if it
// implements the optional TimeoutSetter interface. Returns true if the
// provider was found and updated.
func (r *Impl) SetTimeout(name string, sec int) bool {
	r.mu.RLock()
	p, ok := r.providers[name]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	ts, ok := p.(types.TimeoutSetter)
	if !ok {
		return false
	}
	ts.SetTimeout(sec)
	return true
}

// Get returns a provider by name.
func (r *Impl) Get(name string) (types.Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", name)
	}
	return p, nil
}

// List returns all registered provider names.
func (r *Impl) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// ListAllModels returns every model across all providers.
func (r *Impl) ListAllModels() []types.ModelInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var all []types.ModelInfo
	for _, p := range r.providers {
		all = append(all, p.ListModels()...)
	}
	return all
}

// RefreshAll triggers every provider to refresh its model list.
func (r *Impl) RefreshAll(ctx context.Context) []error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var errs []error
	for _, p := range r.providers {
		if err := p.Health(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
		}
	}
	return errs
}

// ResolveModel finds the provider that owns a given model ID.
func (r *Impl) ResolveModel(modelID string) (types.Provider, types.ModelInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.providers {
		for _, m := range p.ListModels() {
			if m.ID == modelID {
				return p, m, nil
			}
		}
	}

	// Consult user-defined models (registered via RegisterCustomModel).
	if r.customModels != nil {
		if m, ok := r.customModels[modelID]; ok {
			if p, ok := r.providers[m.Provider]; ok {
				return p, m, nil
			}
		}
		if alias, ok := r.customAliases[modelID]; ok {
			if m, ok := r.customModels[alias]; ok {
				if p, ok := r.providers[m.Provider]; ok {
					return p, m, nil
				}
			}
		}
	}
	return nil, types.ModelInfo{}, fmt.Errorf("model %q not found in any provider", modelID)
}
