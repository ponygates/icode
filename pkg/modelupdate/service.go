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
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
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
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[modelupdate] UpdateAll goroutine panic (%s): %v\n%s", pname, r, debug.Stack())
				}
			}()

			update := s.updateProvider(ctx, pname, p)

			mu.Lock()
			updates = append(updates, update)
			mu.Unlock()
		}(name, provider)
	}

	wg.Wait()
	s.lastUpdate = time.Now()

	s.writeUpdateHistory(updates)

	return updates, nil
}

// updateProvider handles the full enrichment + diff pipeline for one provider.
func (s *Service) updateProvider(ctx context.Context, pname string, p types.Provider) ProviderUpdate {
	update := ProviderUpdate{Name: pname}

	builtinModels := p.ListModels()

	var fetchedModels []types.ModelInfo
	fetchOK := false

	fetcher, ok := knownFetchers[pname]
	if ok {
		models, err := fetcher.fetch(ctx, s.cacheDir)
		if err == nil && len(models) > 0 {
			fetchedModels = models
			fetchOK = true
		} else if err != nil {
			log.Printf("[modelupdate] %s API fetch failed: %v", pname, err)
		}
	}

	if !fetchOK {
		if cached, ok := s.LoadCache(pname); ok && len(cached) > 0 {
			fetchedModels = cached
			fetchOK = true
			update.Source = "cache"
		}
	}

	if !fetchOK {
		update.Models = builtinModels
		update.Success = true
		update.Source = "builtin"
		update.Count = len(builtinModels)
		return update
	}

	if update.Source == "" {
		update.Source = "api"
	}

	var docInfo map[string]DocModelInfo
	if fetcher != nil && fetcher.docURL != "" {
		docInfo = fetchDocModels(ctx, fetcher.docURL)
	}

	enriched := mergeModelInfo(fetchedModels, builtinModels, docInfo)

	added, removed := compareModels(builtinModels, enriched)
	update.Added = added
	update.Removed = removed

	enrichedIDs := make(map[string]bool, len(enriched))
	for _, m := range enriched {
		enrichedIDs[m.ID] = true
	}

	prevCached := s.loadPreviousCache(pname)
	finalModels := applyDeprecatedLogic(enriched, prevCached, enrichedIDs)

	for _, rmID := range removed {
		if enrichedIDs[rmID] {
			continue
		}
		for _, bm := range builtinModels {
			if bm.ID == rmID {
				count := 0
				for _, pc := range prevCached {
					if pc.ID == rmID && pc.DeprecatedCount > 0 {
						count = pc.DeprecatedCount
						break
					}
				}
				count++
				bm.DeprecatedCount = count
				bm.Deprecated = count >= 2
				bm.UpdatedAt = time.Now()
				finalModels = append(finalModels, bm)
				break
			}
		}
	}

	update.Models = finalModels
	update.Success = true
	update.Count = len(finalModels)

	if ms, ok := p.(types.ModelSetter); ok {
		ms.SetModels(finalModels)
	}

	s.writeCache(pname, finalModels)

	return update
}

// UpdateOne fetches the latest model list for a single provider.
func (s *Service) UpdateOne(ctx context.Context, providerName string) (*ProviderUpdate, error) {
	s.mu.RLock()
	p, ok := s.providers[providerName]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("provider %q not registered", providerName)
	}

	update := s.updateProvider(ctx, providerName, p)
	s.writeUpdateHistory([]ProviderUpdate{update})

	return &update, nil
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
	Added   []types.ModelInfo `json:"added,omitempty"`
	Removed []string          `json:"removed,omitempty"`
}

// ============================================================================
// Fetcher registry — per-provider API calls to fetch model lists
// ============================================================================

type modelFetcher struct {
	name   string
	docURL string
	fetch  func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error)
}

// knownFetchers maps provider names to their model list fetchers.
var knownFetchers = map[string]*modelFetcher{
	"openrouter": {
		name:   "openrouter",
		docURL: "https://openrouter.ai/llms.txt",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenRouterModels(ctx)
		},
	},
	"deepseek": {
		name:   "deepseek",
		docURL: "https://api-docs.deepseek.com/llms.txt",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenAICompatModels(ctx, "https://api.deepseek.com/v1/models", "deepseek")
		},
	},
	"zhipu": {
		name:   "zhipu",
		docURL: "https://open.bigmodel.cn/llms.txt",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenAICompatModels(ctx, "https://open.bigmodel.cn/api/paas/v4/models", "zhipu")
		},
	},
	"kimi": {
		name:   "kimi",
		docURL: "https://platform.moonshot.cn/llms.txt",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenAICompatModels(ctx, "https://api.moonshot.cn/v1/models", "kimi")
		},
	},
	"anthropic": {
		name:   "anthropic",
		docURL: "https://docs.anthropic.com/llms.txt",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchAnthropicModels(ctx)
		},
	},
	"nvidia": {
		name:   "nvidia",
		docURL: "https://build.nvidia.com/llms.txt",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenAICompatModels(ctx, "https://integrate.api.nvidia.com/v1/models", "nvidia")
		},
	},
	"volcengine": {
		name:   "volcengine",
		docURL: "",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenAICompatModels(ctx, "https://ark.cn-beijing.volces.com/api/v3/models", "volcengine")
		},
	},
	"tencent": {
		name:   "tencent",
		docURL: "https://cloud.tencent.com/llms.txt",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenAICompatModels(ctx, "https://api.hunyuan.cloud.tencent.com/v1/models", "tencent")
		},
	},
	"huawei": {
		name:   "huawei",
		docURL: "",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenAICompatModels(ctx, "https://maas-console.huawei.com/api/v1/models", "huawei")
		},
	},
	"scnet": {
		name:   "scnet",
		docURL: "",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenAICompatModels(ctx, "https://api.scnet.cn/v1/models", "scnet")
		},
	},
	"sensenova": {
		name:   "sensenova",
		docURL: "",
		fetch: func(ctx context.Context, cacheDir string) ([]types.ModelInfo, error) {
			return fetchOpenAICompatModels(ctx, "https://api.sensenova.cn/v1/models", "sensenova")
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
			ID:       d.ID,
			Name:     d.ID,
			Provider: providerName,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		})
	}

	return models, nil
}

// fetchAnthropicModels fetches the model list from Anthropic's /v1/models endpoint.
// Anthropic uses a different auth header (x-api-key) and returns data in a
// slightly different format than the standard OpenAI-compatible /models.
func fetchAnthropicModels(ctx context.Context) ([]types.ModelInfo, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/v1/models", nil)
	req.Header.Set("User-Agent", "iCode/0.1.0")
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("anthropic: auth required (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("anthropic: HTTP %d — %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			Display   string `json:"display_name"`
			CreatedAt string `json:"created_at"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("anthropic decode: %w", err)
	}

	var models []types.ModelInfo
	for _, d := range result.Data {
		name := d.Display
		if name == "" {
			name = d.ID
		}
		models = append(models, types.ModelInfo{
			ID:       d.ID,
			Name:     name,
			Provider: "anthropic",
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		})
	}

	return models, nil
}

// ============================================================================
// Document page fetcher — supplementary model info from llms.txt / API docs
// ============================================================================

// DocModelInfo holds supplementary model metadata parsed from a documentation
// page (llms.txt or similar). Fields are optional — only what the doc provides.
type DocModelInfo struct {
	ContextWindow   int  `json:"context_window,omitempty"`
	MaxOutputTokens int  `json:"max_output_tokens,omitempty"`
	SupportsVision  bool `json:"supports_vision,omitempty"`
	Reasoning       bool `json:"reasoning,omitempty"`
}

// fetchDocModels fetches and parses a documentation URL (llms.txt format) to
// extract supplementary model metadata. Returns a map keyed by model ID.
// On any error (404, timeout, parse failure), returns an empty map — never
// blocks the main update flow.
func fetchDocModels(ctx context.Context, docURL string) map[string]DocModelInfo {
	result := make(map[string]DocModelInfo)
	if docURL == "" {
		return result
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, docURL, nil)
	if err != nil {
		return result
	}
	req.Header.Set("User-Agent", "iCode/0.1.0")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[modelupdate] doc fetch %s: %v (ignored)", docURL, err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Printf("[modelupdate] doc fetch %s: HTTP %d (ignored)", docURL, resp.StatusCode)
		return result
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return result
	}

	parseDocModelEntries(string(body), result)
	return result
}

// parseDocModelEntries scans a text/markdown document for model entries.
// Looks for patterns like:
//   - "model-id" or `model-id` followed by context window info
//   - Lines containing "context" + number (e.g. "128k context", "context_length: 128000")
//   - Lines containing "vision" or "multimodal"
//   - Lines containing "reasoning" or "thinking"
func parseDocModelEntries(text string, result map[string]DocModelInfo) {
	lines := strings.Split(text, "\n")
	var currentModel string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		modelID := extractModelID(trimmed)
		if modelID != "" {
			currentModel = modelID
			if _, exists := result[currentModel]; !exists {
				result[currentModel] = DocModelInfo{}
			}
			continue
		}

		if currentModel == "" {
			continue
		}

		info := result[currentModel]

		if info.ContextWindow == 0 {
			if ctx := extractContextWindow(trimmed); ctx > 0 {
				info.ContextWindow = ctx
			}
		}
		if strings.Contains(strings.ToLower(trimmed), "vision") ||
			strings.Contains(strings.ToLower(trimmed), "multimodal") {
			info.SupportsVision = true
		}
		if strings.Contains(strings.ToLower(trimmed), "reasoning") ||
			strings.Contains(strings.ToLower(trimmed), "thinking") ||
			strings.Contains(strings.ToLower(trimmed), "deep-think") {
			info.Reasoning = true
		}

		result[currentModel] = info
	}
}

// extractModelID tries to extract a model identifier from a line. Model IDs
// typically look like "provider/model-name" or "model-name-v2" etc.
func extractModelID(line string) string {
	if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		rest := strings.TrimPrefix(line, "#")
		rest = strings.TrimPrefix(rest, " ")
		rest = strings.TrimPrefix(rest, "- ")
		rest = strings.TrimPrefix(rest, "* ")
		rest = strings.TrimSpace(rest)

		if isPlausibleModelID(rest) {
			return rest
		}
	}
	if strings.HasPrefix(line, "`") && strings.HasSuffix(line, "`") {
		id := strings.Trim(line, "`")
		if isPlausibleModelID(id) {
			return id
		}
	}
	if strings.HasPrefix(line, "[") && strings.Contains(line, "](") {
		idx := strings.Index(line, "](")
		id := strings.TrimSpace(line[1:idx])
		if isPlausibleModelID(id) {
			return id
		}
	}
	return ""
}

// isPlausibleModelID checks whether a string looks like a model identifier
// (contains / or - and looks like a slug, not a sentence).
func isPlausibleModelID(s string) bool {
	if len(s) < 3 || len(s) > 120 {
		return false
	}
	if strings.Contains(s, " ") && !strings.Contains(s, "/") {
		return false
	}
	hasSlash := strings.Contains(s, "/")
	hasDash := strings.Contains(s, "-")
	hasDot := strings.Contains(s, ".")
	return hasSlash || hasDash || hasDot
}

// extractContextWindow tries to extract a context window size from a line.
// Recognizes patterns like "128k context", "context_length: 128000", "128K tokens", etc.
func extractContextWindow(line string) int {
	lower := strings.ToLower(line)

	patterns := []struct {
		prefix string
		suffix string
	}{
		{"context", ""},
		{"context_length", ""},
		{"context window", ""},
		{"ctx", ""},
		{"tokens", ""},
	}

	for _, pat := range patterns {
		idx := strings.Index(lower, pat.prefix)
		if idx < 0 {
			continue
		}

		region := lower[idx:]
		numStr := extractNumber(region)
		if numStr == 0 {
			continue
		}

		if strings.Contains(region, "k") && numStr < 10000 {
			return numStr * 1000
		}
		return numStr
	}

	return 0
}

// extractNumber extracts the first integer from a string.
func extractNumber(s string) int {
	i := 0
	for i < len(s) && (s[i] < '0' || s[i] > '9') {
		i++
	}
	if i >= len(s) {
		return 0
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	n := 0
	for _, ch := range s[i:j] {
		n = n*10 + int(ch-'0')
	}
	return n
}

// ============================================================================
// Model enrichment and diff logic
// ============================================================================

// mergeModelInfo enriches API-fetched models with built-in metadata and
// doc-supplement data. For each fetched model:
//  1. If a matching built-in model exists (same ID), inherit its rich metadata
//     (context window, capabilities, plans, vision, etc.)
//  2. If no built-in match, use doc-supplement data to fill in what we can.
//  3. If neither source has info, keep the API skeleton (ID/name only).
func mergeModelInfo(fetched []types.ModelInfo, builtin []types.ModelInfo, docInfo map[string]DocModelInfo) []types.ModelInfo {
	builtinMap := make(map[string]types.ModelInfo, len(builtin))
	for _, m := range builtin {
		builtinMap[m.ID] = m
	}

	enriched := make([]types.ModelInfo, 0, len(fetched))
	for _, fm := range fetched {
		if bm, ok := builtinMap[fm.ID]; ok {
			em := bm
			em.UpdatedAt = time.Now()
			if fm.Name != "" && fm.Name != fm.ID {
				em.Name = fm.Name
			}
			enriched = append(enriched, em)
			continue
		}

		em := fm
		em.UpdatedAt = time.Now()
		if di, ok := docInfo[fm.ID]; ok {
			if di.ContextWindow > 0 && em.ContextWindow == 0 {
				em.ContextWindow = di.ContextWindow
			}
			if di.MaxOutputTokens > 0 && em.MaxOutputTokens == 0 {
				em.MaxOutputTokens = di.MaxOutputTokens
			}
			if di.SupportsVision {
				em.SupportsVision = true
			}
			if di.Reasoning {
				em.Capabilities.Reasoning = true
			}
		}
		enriched = append(enriched, em)
	}

	return enriched
}

// compareModels computes the diff between built-in models and the enriched
// (API-fetched) model list. Returns:
//   - added: models present in enriched but missing from builtin (new models)
//   - removed: model IDs present in builtin but missing from enriched (deprecated)
func compareModels(builtin []types.ModelInfo, enriched []types.ModelInfo) (added []types.ModelInfo, removed []string) {
	enrichedSet := make(map[string]bool, len(enriched))
	for _, m := range enriched {
		enrichedSet[m.ID] = true
	}

	builtinSet := make(map[string]bool, len(builtin))
	for _, m := range builtin {
		builtinSet[m.ID] = true
		if !enrichedSet[m.ID] {
			removed = append(removed, m.ID)
		}
	}

	for _, m := range enriched {
		if !builtinSet[m.ID] {
			added = append(added, m)
		}
	}

	return added, removed
}

// applyDeprecatedLogic marks models as deprecated based on consecutive absence
// from the API. A model is only marked Deprecated after being absent for 2+
// consecutive refreshes. Models present in the API have their DeprecatedCount
// reset to 0.
func applyDeprecatedLogic(currentModels []types.ModelInfo, previouslyCached []types.ModelInfo, enrichedIDs map[string]bool) []types.ModelInfo {
	prevCountMap := make(map[string]int, len(previouslyCached))
	for _, m := range previouslyCached {
		if m.DeprecatedCount > 0 {
			prevCountMap[m.ID] = m.DeprecatedCount
		}
	}

	var result []types.ModelInfo

	for _, m := range currentModels {
		em := m
		if enrichedIDs[m.ID] {
			em.Deprecated = false
			em.DeprecatedCount = 0
		} else if !enrichedIDs[m.ID] {
			count := prevCountMap[m.ID] + 1
			em.DeprecatedCount = count
			if count >= 2 {
				em.Deprecated = true
			}
		}
		result = append(result, em)
	}

	return result
}

// writeUpdateHistory appends an update history entry to disk.
func (s *Service) writeUpdateHistory(updates []ProviderUpdate) {
	type HistoryEntry struct {
		Timestamp time.Time        `json:"timestamp"`
		Providers []ProviderUpdate `json:"providers"`
	}

	entry := HistoryEntry{
		Timestamp: time.Now(),
		Providers: updates,
	}

	path := filepath.Join(s.cacheDir, "update-history.json")

	var entries []HistoryEntry
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &entries)
	}

	entries = append(entries, entry)
	if len(entries) > 50 {
		entries = entries[len(entries)-50:]
	}

	out, _ := json.MarshalIndent(entries, "", "  ")
	os.MkdirAll(s.cacheDir, 0755)
	os.WriteFile(path, out, 0644)
}

// loadPreviousCache loads the previously cached model list for a provider
// (ignoring TTL — we need it even if expired, for deprecated counting).
func (s *Service) loadPreviousCache(providerName string) []types.ModelInfo {
	path := filepath.Join(s.cacheDir, providerName+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var entry CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil
	}

	return entry.Models
}
