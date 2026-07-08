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
	return nil, types.ModelInfo{}, fmt.Errorf("model %q not found in any provider", modelID)
}
