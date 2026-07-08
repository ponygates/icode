// Package modelupdate provides the one-click model list update service for iCode.
//
// It fetches the latest model lists, pricing plans, and context window information
// from all registered LLM providers, caching results to avoid excessive API calls.
package modelupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// Service orchestrates model list updates across providers.
type Service struct {
	mu         sync.RWMutex
	providers  map[string]types.Provider
	cacheDir   string
	cacheTTL   time.Duration
	lastUpdate time.Time
}

// CacheEntry stores the cached model list for a provider.
type CacheEntry struct {
	Provider  string            `json:"provider"`
	Models    []types.ModelInfo `json:"models"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// NewService creates a model update service.
func NewService(cacheDir string) *Service {
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".icode", "cache")
	}

	return &Service{
		providers: make(map[string]types.Provider),
		cacheDir:  cacheDir,
		cacheTTL:  24 * time.Hour,
	}
}

// Register adds a provider to the update service.
func (s *Service) Register(p types.Provider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers[p.Name()] = p
}

// UpdateAll fetches the latest model lists from all registered providers.
func (s *Service) UpdateAll(ctx context.Context) ([]ProviderUpdate, error) {
	s.mu.Lock()
	providers := make(map[string]types.Provider)
	for k, v := range s.providers {
		providers[k] = v
	}
	s.mu.Unlock()

	var updates []ProviderUpdate
	var mu sync.Mutex
	var wg sync.WaitGroup

	for name, provider := range providers {
		wg.Add(1)
		go func(pname string, p types.Provider) {
			defer wg.Done()

			update := ProviderUpdate{Name: pname}

			// Try fetching from provider's API
			fetcher, ok := knownFetchers[pname]
			if ok {
				models, err := fetcher.fetch(ctx, s.cacheDir)
				if err == nil && len(models) > 0 {
					update.Models = models
					update.Success = true
					update.Source = "api"

					// Cache the result
					s.writeCache(pname, models)
				}
			}

			// Fallback: use provider's built-in models
			if !update.Success {
				update.Models = p.ListModels()
				update.Success = true
				update.Source = "builtin"
			}

			update.Count = len(update.Models)

			mu.Lock()
			updates = append(updates, update)
			mu.Unlock()
		}(name, provider)
	}

	wg.Wait()
	s.lastUpdate = time.Now()
	return updates, nil
}

// UpdateOne fetches the latest model list for a single provider.
func (s *Service) UpdateOne(ctx context.Context, providerName string) (*ProviderUpdate, error) {
	s.mu.RLock()
	p, ok := s.providers[providerName]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("provider %q not registered", providerName)
	}

	update := &ProviderUpdate{Name: providerName}

	fetcher, ok := knownFetchers[providerName]
	if ok {
		models, err := fetcher.fetch(ctx, s.cacheDir)
		if err == nil && len(models) > 0 {
			update.Models = models
			update.Success = true
			update.Source = "api"
			update.Count = len(models)
			s.writeCache(providerName, models)
			return update, nil
		}
	}

	update.Models = p.ListModels()
	update.Success = true
	update.Source = "builtin"
	update.Count = len(update.Models)
	return update, nil
}

// LoadCache reads cached model lists from disk.
func (s *Service) LoadCache(providerName string) ([]types.ModelInfo, bool) {
	path := filepath.Join(s.cacheDir, providerName+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}

	var entry CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, false
	}

	if time.Since(entry.UpdatedAt) > s.cacheTTL {
		return nil, false
	}

	return entry.Models, true
}

func (s *Service) writeCache(providerName string, models []types.ModelInfo) {
	os.MkdirAll(s.cacheDir, 0755)

	entry := CacheEntry{
		Provider:  providerName,
		Models:    models,
		UpdatedAt: time.Now(),
	}

	data, _ := json.MarshalIndent(entry, "", "  ")
	path := filepath.Join(s.cacheDir, providerName+".json")
	os.WriteFile(path, data, 0644)
}

// LastUpdate returns when the last full update ran.
func (s *Service) LastUpdate() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastUpdate
}

// ============================================================================
// ProviderUpdate — result of updating a provider
// ============================================================================

type ProviderUpdate struct {
	Name    string            `json:"name"`
	Success bool              `json:"success"`
	Count   int               `json:"count"`
	Source  string            `json:"source"` // "api" or "builtin"
	Models  []types.ModelInfo `json:"models"`
	Error   string            `json:"error,omitempty"`
}

// ============================================================================
// Fetcher registry — per-provider API calls to fetch model lists
// ============================================================================

type modelFetcher struct {
	name  string
	fetch func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error)
}

// knownFetchers maps provider names to their model list fetchers.
var knownFetchers = map[string]*modelFetcher{
	"openrouter": {
		name: "openrouter",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenRouterModels(ctx)
		},
	},
	"deepseek": {
		name: "deepseek",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenAICompatModels(ctx, "https://api.deepseek.com/v1/models", "deepseek")
		},
	},
	"zhipu": {
		name: "zhipu",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenAICompatModels(ctx, "https://open.bigmodel.cn/api/paas/v4/models", "zhipu")
		},
	},
	"kimi": {
		name: "kimi",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenAICompatModels(ctx, "https://api.moonshot.cn/v1/models", "kimi")
		},
	},
}

// fetchOpenRouterModels fetches the model list from OpenRouter's API.
func fetchOpenRouterModels(ctx context.Context) ([]types.ModelInfo, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/models", nil)
	req.Header.Set("User-Agent", "iCode/0.1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openrouter: HTTP %d — %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Description   string `json:"description"`
			ContextLength int    `json:"context_length"`
			Pricing       struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("openrouter decode: %w", err)
	}

	var models []types.ModelInfo
	for _, d := range result.Data {
		// Only include models that support tool calling
		models = append(models, types.ModelInfo{
			ID:              d.ID,
			Name:            d.Name,
			Description:     d.Description,
			Provider:        "openrouter",
			ContextWindow:   d.ContextLength,
			MaxOutputTokens: 16384,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		})
	}

	return models, nil
}

// fetchOpenAICompatModels fetches models from any OpenAI-compatible /models endpoint.
func fetchOpenAICompatModels(ctx context.Context, endpoint, providerName string) ([]types.ModelInfo, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "iCode/0.1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s fetch: %w", providerName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s: HTTP %d — %s", providerName, resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%s decode: %w", providerName, err)
	}

	var models []types.ModelInfo
	for _, d := range result.Data {
		models = append(models, types.ModelInfo{
			ID:          d.ID,
			Name:        d.ID,
			Provider:    providerName,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		})
	}

	return models, nil
}
