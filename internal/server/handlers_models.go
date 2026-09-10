package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	type providerInfo struct {
		Name      string `json:"name"`
		Models    int    `json:"models"`
		CacheSupp bool   `json:"cache_support"`
		// Filtered reports whether the user has curated this vendor's model
		// list via the "fetch models" flow; Enabled is how many models
		// survived that filter.
		Filtered bool `json:"filtered"`
		Enabled  int  `json:"enabled"`
	}

	var result []providerInfo
	for _, name := range s.reg.List() {
		if s.isProviderDisabled(name) {
			continue
		}
		p, err := s.reg.Get(name)
		if err != nil {
			continue
		}
		// Count over the catalogue *plus* models registered on the fly for this
		// vendor (a live-discovered model the build never knew about). Both the
		// numerator and the denominator use the same set, so the UI badge can
		// never render the nonsense "3/2" when a user enables a model that is
		// not in the built-in catalogue.
		enabled, total := 0, 0
		seen := make(map[string]bool)
		countOne := func(modelID string) {
			key := config.VendorModelID(name, modelID)
			if key == "" || seen[key] {
				return
			}
			seen[key] = true
			total++
			if s.cfg.IsModelEnabled(name, key) {
				enabled++
			}
		}
		for _, m := range p.ListModels() {
			countOne(m.ID)
		}
		for _, cm := range s.cfg.Models {
			if cm.Custom && cm.Provider == name {
				countOne(cm.ModelID)
			}
		}
		result = append(result, providerInfo{
			Name:      name,
			Models:    total,
			CacheSupp: p.SupportsCache(),
			Filtered:  len(s.cfg.Providers[name].EnabledModels) > 0,
			Enabled:   enabled,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	models := s.reg.ListAllModels()

	// Convert to frontend-friendly format
	type modelDTO struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Provider   string `json:"provider"`
		ModelID    string `json:"model_id"`
		Plan       string `json:"plan"`
		ContextWin int    `json:"context_window"`
		MaxOut     int    `json:"max_output_tokens"`
		// The desktop renderer reads camelCase (model.contextWindow /
		// model.maxOutputTokens) while these DTOs only ever emitted snake_case,
		// so those fields were silently always undefined: ChatPage's context
		// gauge fell back to 200000 and ModelsPage's "ctx" chip never rendered.
		// Emitting both spellings fixes the renderer without breaking any
		// existing consumer.
		ContextWindow   int  `json:"contextWindow"`
		MaxOutputTokens int  `json:"maxOutputTokens"`
		FreeTier        bool `json:"free_tier"`
		Custom          bool `json:"custom"`
		// Per-model generation overrides, echoed so the settings modal can show
		// what is currently configured instead of resetting to its defaults.
		// Nil means "not overridden".
		Temperature *float64 `json:"temperature,omitempty"`
		TopP        *float64 `json:"top_p,omitempty"`
	}
	result := make([]modelDTO, 0, len(models)+len(s.cfg.Models))
	seen := make(map[string]bool, len(models)+len(s.cfg.Models))
	for _, m := range models {
		if s.isProviderDisabled(m.Provider) {
			continue
		}
		// Respect the vendor's curated model list: a user who fetched the
		// live catalogue and ticked a subset expects only those to be
		// selectable. An unset filter enables everything (see IsModelEnabled).
		if !s.cfg.IsModelEnabled(m.Provider, m.ID) {
			continue
		}
		// Custom models are appended below (marked custom:true) so they are not
		// listed twice — ListAllModels already includes registered custom models.
		if m.Provider != "" && s.isCustomModelID(m.ID) {
			continue
		}
		seen[m.ID] = true
		plan := "Coding Plan"
		free := false
		if len(m.Plans) > 0 {
			plan = m.Plans[0].Name
			free = m.Plans[0].FreeTier != nil
		}
		// A stored override wins over the built-in figures, so an edit made in
		// the per-model settings modal is reflected in the list rather than
		// being stored and ignored.
		name := m.Name
		ctxWin, maxOut := m.ContextWindow, m.MaxOutputTokens
		var ovTemp, ovTopP *float64
		if ov, ok := s.cfg.ModelOverride(m.Provider, m.ID); ok {
			if ov.Name != "" {
				name = ov.Name
			}
			if ov.ContextWindow > 0 {
				ctxWin = ov.ContextWindow
			}
			if ov.MaxOutput > 0 {
				maxOut = ov.MaxOutput
			}
			ovTemp, ovTopP = ov.Temperature, ov.TopP
		}
		result = append(result, modelDTO{
			ID:              m.ID,
			Name:            name,
			Provider:        m.Provider,
			ModelID:         m.ID,
			Plan:            plan,
			ContextWin:      ctxWin,
			MaxOut:          maxOut,
			ContextWindow:   ctxWin,
			MaxOutputTokens: maxOut,
			FreeTier:        free,
			Custom:          false,
			Temperature:     ovTemp,
			TopP:            ovTopP,
		})
	}

	// Append user-defined custom models (new ids not in the registry).
	for _, cm := range s.cfg.Models {
		if !cm.Custom {
			continue
		}
		if s.isProviderDisabled(cm.Provider) {
			continue
		}
		if !s.cfg.IsModelEnabled(cm.Provider, cm.ModelID) {
			continue
		}
		if seen[cm.ID] {
			continue
		}
		result = append(result, modelDTO{
			ID:              cm.ID,
			Name:            cm.Name,
			Provider:        cm.Provider,
			ModelID:         cm.ModelID,
			ContextWin:      cm.ContextWindow,
			MaxOut:          cm.MaxOutput,
			ContextWindow:   cm.ContextWindow,
			MaxOutputTokens: cm.MaxOutput,
			FreeTier:        cm.FreeTier,
			Custom:          true,
			Temperature:     cm.Temperature,
			TopP:            cm.TopP,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

// handleListCustomModels returns the user-defined model entries stored in the
// config file (used by the desktop "模型" settings section for editing).

func (s *Server) handleListCustomModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": s.cfg.Models})
}

// handleConfigModel dispatches PUT (upsert) and DELETE (remove) for
// user-defined model entries.

func (s *Server) handleConfigModel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		var m config.ModelCfg
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			_ = r.Body.Close()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		m.Provider = strings.TrimSpace(m.Provider)
		m.ModelID = strings.TrimSpace(m.ModelID)
		if m.Provider == "" || m.ModelID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "provider and model_id are required"})
			return
		}
		if strings.ContainsAny(m.ModelID, " \t\n") {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "model_id must not contain whitespace"})
			return
		}
		// Respect an explicit custom flag from the client. New user-added models
		// send custom:true; editing a built-in model sends custom:false so it is
		// stored as a display-name/parameter override rather than duplicating
		// the built-in entry in the model list.
		//
		// Normalise to the bare vendor id before building the key: callers
		// reach this with either convention (a built-in registry entry may be
		// keyed "openrouter/openai/gpt-4o" while the vendor expects
		// "openai/gpt-4o"), and an un-normalised key would store the override
		// under "openrouter/openrouter/…" where nothing would ever read it.
		m.ModelID = config.VendorModelID(m.Provider, m.ModelID)
		m.ID = config.ModelKey(m.Provider, m.ModelID)

		// A model the vendor's own catalogue already contains must not be
		// duplicated as a custom entry: the blank copy would shadow the
		// described one, losing its plan, pricing and context window.
		//
		// Refusing outright is no better, though. The usual reason to land here
		// is that the vendor's /models omitted this model — so it was missing
		// from the fetched checklist and adding it by hand was the only route
		// left. A flat 409 dead-ends precisely the user who needs help. Enable
		// the built-in instead, and only fail when something real is in the
		// way.
		if m.Custom && s.cataloguedByProvider(m.Provider, m.ModelID) {
			s.ensureModelEnabled(m.Provider, m.ModelID)
			if err := s.cfg.Save(config.DefaultPath()); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			if s.isProviderDisabled(m.Provider) {
				writeJSON(w, http.StatusConflict, map[string]any{
					"error": fmt.Sprintf("%s 是 %s 的内置模型，但该厂商已停用；请先在厂商列表中启用该厂商",
						m.ModelID, m.Provider),
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true, "id": m.ID, "builtin": true, "enabled": true,
			})
			return
		}

		s.cfg.UpsertModel(m)
		// A hand-added model must survive the vendor's curated filter. Without
		// this the model is stored but hidden — the classic "I clicked add and
		// nothing appeared" bug.
		if m.Custom {
			s.ensureModelEnabled(m.Provider, m.ModelID)
		}
		if err := s.cfg.Save(config.DefaultPath()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		// Register the custom model so the engine can resolve it at chat time.
		s.registerCustomModel(m)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": m.ID})

	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id is required"})
			return
		}
		// Read the entry before it disappears — the vendor filter needs cleaning
		// up so a dangling id cannot keep the vendor restricted to a model that
		// no longer exists.
		prev, had := s.cfg.FindModel(id)
		if !s.cfg.DeleteModel(id) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "model not found"})
			return
		}
		s.reg.RemoveCustomModel(id)
		if had && prev.Custom {
			s.forgetModelEnabled(prev.Provider, prev.ModelID)
		}
		if err := s.cfg.Save(config.DefaultPath()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// cataloguedByProvider reports whether the vendor's built-in catalogue already
// contains this model id, tolerating either id convention (bare for deepseek,
// "ollama/llama3"-style for others).
func (s *Server) cataloguedByProvider(provider, modelID string) bool {
	p, err := s.reg.Get(provider)
	if err != nil {
		return false
	}
	for _, m := range p.ListModels() {
		if m.ID == modelID || config.VendorModelID(provider, m.ID) == modelID {
			return true
		}
	}
	return false
}

// ensureModelEnabled appends a newly added model to the vendor's curated list.
//
// Only meaningful when a filter exists: an empty filter restricts nothing, so
// every model is already enabled and there is nothing to join. Joining is what
// makes "add a model" observable when the user has already ticked a subset.
func (s *Server) ensureModelEnabled(provider, modelID string) {
	cur := s.cfg.Providers[provider].EnabledModels
	if len(cur) == 0 {
		return
	}
	vendorID := config.VendorModelID(provider, modelID)
	for _, id := range cur {
		if id == vendorID {
			return
		}
	}
	next := make([]string, 0, len(cur)+1)
	next = append(next, cur...)
	s.cfg.SetEnabledModels(provider, append(next, vendorID))
}

// forgetModelEnabled drops a deleted model from the vendor's curated list.
//
// If that empties the list the vendor goes back to "no restriction", which is
// the consistent outcome: an empty filter means nothing is filtered out.
func (s *Server) forgetModelEnabled(provider, modelID string) {
	cur := s.cfg.Providers[provider].EnabledModels
	if len(cur) == 0 {
		return
	}
	vendorID := config.VendorModelID(provider, modelID)
	next := make([]string, 0, len(cur))
	for _, id := range cur {
		if id != vendorID && id != modelID {
			next = append(next, id)
		}
	}
	if len(next) == len(cur) {
		return // nothing referenced it
	}
	s.cfg.SetEnabledModels(provider, next)
}

// handleConfigProvider dispatches PUT (add/update a vendor's base URL & key)
// and DELETE (remove a vendor together with its custom models) so the desktop
// settings UI can manage providers directly.

func (s *Server) handleConfigProvider(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		var req struct {
			Name       string `json:"name"`
			APIBase    string `json:"api_base"`
			APIKey     string `json:"api_key"`
			TimeoutSec int    `json:"timeout_sec"`
			Disabled   bool   `json:"disabled"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			_ = r.Body.Close()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
			return
		}
		pc := s.cfg.Providers[req.Name]
		if req.APIBase != "" {
			pc.APIBase = req.APIBase
		}
		if req.APIKey != "" {
			pc.APIKey = req.APIKey
		}
		if req.TimeoutSec > 0 {
			pc.Timeout = req.TimeoutSec
		}
		pc.Disabled = req.Disabled
		s.cfg.Providers[req.Name] = pc
		if err := s.cfg.Save(config.DefaultPath()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		// Push the new credentials into the live provider so the change takes
		// effect immediately. If this is a brand-new (custom) vendor that has
		// no live provider yet, register a generic OpenAI-compatible provider.
		if !s.reg.SetCredentials(req.Name, pc.APIKey, pc.APIBase) {
			if _, gerr := s.reg.Get(req.Name); gerr != nil {
				np := openai_compat.New(openai_compat.Config{
					Name:         req.Name,
					APIKey:       pc.APIKey,
					APIBase:      pc.APIBase,
					TimeoutSec:   pc.Timeout,
					CacheSupport: true,
				})
				_ = s.reg.Register(np)
				// Register any custom models that already belong to this vendor.
				for _, cm := range s.cfg.Models {
					if cm.Custom && cm.Provider == req.Name {
						s.registerCustomModel(cm)
					}
				}
			}
		}
		// Apply a per-provider timeout to the live provider client.
		if req.TimeoutSec > 0 {
			s.reg.SetTimeout(req.Name, req.TimeoutSec)
		}
		// Note: a disabled provider is hidden from the model list (so it cannot
		// be selected for chat) but is NOT deregistered — that would drop a
		// built-in provider's own model catalogue. Re-enabling simply removes
		// the filter, which is safer and reversible.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "disabled": req.Disabled})

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
			return
		}
		delete(s.cfg.Providers, name)
		// Remove custom models that belong to this provider.
		kept := s.cfg.Models[:0]
		for _, m := range s.cfg.Models {
			if m.Provider != name {
				kept = append(kept, m)
			} else {
				s.reg.RemoveCustomModel(config.ModelKey(m.Provider, m.ModelID))
			}
		}
		s.cfg.Models = kept
		s.reg.Deregister(name)
		if err := s.cfg.Save(config.DefaultPath()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRefreshModels(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "updater not available"})
		return
	}

	ctx := r.Context()
	updates, err := s.updater.UpdateAll(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"updated": len(updates),
		"results": updates,
	})
}

// ============================================================================
// Live model discovery + per-vendor model selection
// ============================================================================

// handleFetchModels queries the vendor's own /models endpoint using the stored
// key and returns what that key can actually use.
//
// This backs the "获取模型" action in the desktop settings. The built-in
// catalogue is a build-time snapshot, whereas the vendor is the authority on
// what this account is entitled to (it varies by plan and region) and on
// models released since this binary was built.
func (s *Server) handleFetchModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "provider is required"})
		return
	}
	p, err := s.reg.Get(provider)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "provider not found: " + provider})
		return
	}
	fetcher, ok := p.(types.ModelFetcher)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]any{
			"error": fmt.Sprintf("%s 暂不支持实时获取模型（该厂商未实现 ModelFetcher）", provider),
		})
		return
	}

	// A vendor round-trip can be slow — OpenRouter's catalogue is megabytes —
	// so bound it well below a typical browser request timeout.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	fetched, err := fetcher.FetchModels(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	type fetchedModel struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Known   bool   `json:"known"`
		Enabled bool   `json:"enabled"`
		// BuiltinOnly marks a model that lives in the built-in catalogue but
		// which the vendor's own /models did not return. Vendors omit models
		// for all sorts of reasons — account entitlement, region, or simply an
		// incomplete listing — yet iCode can still call them. Surfacing them
		// is what keeps the checklist from contradicting the add endpoint
		// ("the list doesn't have it" vs "adding it says it already exists"),
		// and it protects them from being silently dropped by a select-all
		// save, which would then filter them out of the model list for good.
		BuiltinOnly bool `json:"builtin_only,omitempty"`
		Context     int  `json:"context_window,omitempty"`
		MaxOut      int  `json:"max_output_tokens,omitempty"`
	}

	// `known` must mean "present in the built-in catalogue", so the UI can
	// steer the user to the models that are genuinely new to this build.
	// Reporting a constant true — as this once did — carries no information.
	catalogue := make(map[string]types.ModelInfo)
	for _, m := range p.ListModels() {
		if id := config.VendorModelID(provider, m.ID); id != "" {
			catalogue[id] = m
		}
	}

	out := make([]fetchedModel, 0, len(fetched)+len(catalogue))
	seen := make(map[string]bool, len(fetched)+len(catalogue))
	for _, m := range fetched {
		vendorID := config.VendorModelID(provider, m.ID)
		if vendorID == "" || seen[vendorID] {
			continue
		}
		seen[vendorID] = true
		entry := fetchedModel{
			ID:      vendorID,
			Name:    m.Name,
			Enabled: s.cfg.IsModelEnabled(provider, vendorID),
			Context: m.ContextWindow,
			MaxOut:  m.MaxOutputTokens,
		}
		// For a built-in model the panel must show the figures iCode will
		// actually use. /api/models serves the curated registry entry for a
		// built-in, so echoing the vendor's numbers here would advertise a
		// context window the rest of the app ignores.
		if bi, ok := catalogue[vendorID]; ok {
			entry.Known = true
			if bi.Name != "" {
				entry.Name = bi.Name
			}
			if bi.ContextWindow > 0 {
				entry.Context = bi.ContextWindow
			}
			if bi.MaxOutputTokens > 0 {
				entry.MaxOut = bi.MaxOutputTokens
			}
		}
		out = append(out, entry)
	}

	// Fold in user-added custom models for this vendor so the checklist is the
	// complete truth for the provider. Without this, ticking "select all" would
	// silently drop a model the user had added by hand.
	custom := make([]fetchedModel, 0, 4)
	for _, cm := range s.cfg.Models {
		if cm.Provider != provider || !cm.Custom {
			continue
		}
		vendorID := config.VendorModelID(provider, cm.ModelID)
		if vendorID == "" || seen[vendorID] {
			continue
		}
		seen[vendorID] = true
		custom = append(custom, fetchedModel{
			ID:      vendorID,
			Name:    cm.Name,
			Enabled: s.cfg.IsModelEnabled(provider, vendorID),
			Context: cm.ContextWindow,
			MaxOut:  cm.MaxOutput,
		})
	}

	// Then the built-in models the vendor chose not to report. They go last so
	// the vendor's own list — what this account can demonstrably call — stays
	// at the top.
	omitted := 0
	for _, m := range p.ListModels() {
		vendorID := config.VendorModelID(provider, m.ID)
		if vendorID == "" || seen[vendorID] {
			continue
		}
		seen[vendorID] = true
		omitted++
		out = append(out, fetchedModel{
			ID:          vendorID,
			Name:        m.Name,
			Known:       true,
			BuiltinOnly: true,
			Enabled:     s.cfg.IsModelEnabled(provider, vendorID),
			Context:     m.ContextWindow,
			MaxOut:      m.MaxOutputTokens,
		})
	}
	out = append(out, custom...)

	writeJSON(w, http.StatusOK, map[string]any{
		"provider": provider,
		"count":    len(out),
		"filtered": len(s.cfg.Providers[provider].EnabledModels) > 0,
		// Let the UI explain why the list is longer than the vendor reported.
		"builtin_only": omitted,
		"models":       out,
	})
}

// handleSaveModelSelection persists the models a user ticked for one vendor.
//
// Models absent from the built-in catalogue are stored as custom entries and
// registered, so a model the vendor shipped after this build becomes
// selectable and resolvable without waiting for a release.
func (s *Server) handleSaveModelSelection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Provider string   `json:"provider"`
		Models   []string `json:"models"`
		// Meta carries the vendor-reported parameters for the ticked models,
		// keyed by model id. Without it an auto-registered model would be stored
		// with a zero context window, so the UI would fall back to a guess even
		// though the vendor told us the real figure moments earlier.
		Meta map[string]struct {
			ContextWindow   int `json:"context_window"`
			MaxOutputTokens int `json:"max_output_tokens"`
		} `json:"meta"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if req.Provider == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "provider is required"})
		return
	}
	p, err := s.reg.Get(req.Provider)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "provider not found: " + req.Provider})
		return
	}

	// Index the built-in catalogue under both conventions a provider might use.
	builtin := make(map[string]bool)
	for _, m := range p.ListModels() {
		builtin[m.ID] = true
		builtin[config.VendorModelID(req.Provider, m.ID)] = true
	}

	registered := 0
	clean := make([]string, 0, len(req.Models))
	for _, id := range req.Models {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		clean = append(clean, id)
		if builtin[id] {
			continue // already in the catalogue — nothing to register
		}
		key := config.ModelKey(req.Provider, id)
		if _, exists := s.cfg.FindModel(key); exists {
			continue
		}
		m := config.ModelCfg{
			ID:       key,
			Provider: req.Provider,
			ModelID:  id,
			Name:     id,
			Custom:   true,
		}
		// Carry the vendor's own figures across, so the new model's context
		// window is the real one rather than a blank/degenerate default.
		if meta, ok := req.Meta[id]; ok {
			m.ContextWindow = meta.ContextWindow
			m.MaxOutput = meta.MaxOutputTokens
		}
		s.cfg.UpsertModel(m)
		s.registerCustomModel(m)
		registered++
	}

	// An empty selection is rejected rather than stored: "no models ticked"
	// would be stored as an empty filter, which means "no restriction" and so
	// would enable the entire catalogue — the exact opposite of the intent.
	// Disabling a whole vendor is what the provider-level switch is for.
	if len(clean) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "至少勾选一个模型；若要停用整个厂商，请使用厂商开关",
		})
		return
	}

	s.cfg.SetEnabledModels(req.Provider, clean)
	if err := s.cfg.Save(config.DefaultPath()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"enabled":    len(clean),
		"registered": registered,
	})
}

// handleSearch searches message content across all sessions.

func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type keyInfo struct {
		Name     string `json:"name"`
		KeySet   bool   `json:"key_set"`
		APIBase  string `json:"api_base,omitempty"`
		Timeout  int    `json:"timeout_sec"`
		Disabled bool   `json:"disabled"`
	}
	seen := map[string]bool{}
	var result []keyInfo
	// Built-in providers registered in the engine. Disabled providers are kept
	// in this list (with Disabled=true) so the desktop settings UI can still
	// show and re-enable them — only handleListModels hides their models.
	for _, name := range s.reg.List() {
		pc := s.cfg.Providers[name]
		result = append(result, keyInfo{Name: name, KeySet: pc.APIKey != "", APIBase: pc.APIBase, Timeout: pc.Timeout, Disabled: pc.Disabled})
		seen[name] = true
	}
	// Custom providers that only exist in the config file.
	for name, pc := range s.cfg.Providers {
		if seen[name] {
			continue
		}
		result = append(result, keyInfo{Name: name, KeySet: pc.APIKey != "", APIBase: pc.APIBase, Timeout: pc.Timeout, Disabled: pc.Disabled})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": result})
}

// handleSetKey saves (or updates) the API key and optional base URL for a
// provider, persisting to disk. The secret is never echoed back in responses.

func (s *Server) handleSetKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
		APIBase  string `json:"api_base"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if req.Provider == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "provider is required"})
		return
	}

	// When user adds an API key for an external provider, auto-elevate the
	// security level from "local" to "foreign-llm" so the chat actually works.
	// Local-only providers (ollama, llama, lmstudio) don't trigger this.
	localProviders := map[string]bool{"ollama": true, "llama": true, "local": true, "lmstudio": true}
	if req.APIKey != "" && !localProviders[req.Provider] {
		if s.cfg.SecurityLevel == config.SecLocal {
			s.cfg.SecurityLevel = config.SecForeignLLM
			_ = s.cfg.Save(config.DefaultPath())
		}
		if s.gate != nil {
			s.gate.SetSecurityLevel(s.cfg.SecurityLevel)
		}
	}

	pc := s.cfg.Providers[req.Provider]
	if req.APIKey != "" {
		pc.APIKey = req.APIKey
	}
	if req.APIBase != "" {
		pc.APIBase = req.APIBase
	}
	s.cfg.Providers[req.Provider] = pc
	if err := s.cfg.Save(config.DefaultPath()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	// Push the new credentials into the live provider so the change takes
	// effect immediately (no server restart required).
	s.reg.SetCredentials(req.Provider, pc.APIKey, pc.APIBase)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
