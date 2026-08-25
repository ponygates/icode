package server

import (
	"encoding/json"
	"net/http"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
)

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	type providerInfo struct {
		Name      string `json:"name"`
		Models    int    `json:"models"`
		CacheSupp bool   `json:"cache_support"`
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
		result = append(result, providerInfo{
			Name:      name,
			Models:    len(p.ListModels()),
			CacheSupp: p.SupportsCache(),
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
		FreeTier   bool   `json:"free_tier"`
		Custom     bool   `json:"custom"`
	}
	result := make([]modelDTO, 0, len(models)+len(s.cfg.Models))
	seen := make(map[string]bool, len(models)+len(s.cfg.Models))
	for _, m := range models {
		if s.isProviderDisabled(m.Provider) {
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
		// Apply a user-defined display-name override if present.
		name := m.Name
		if ov := s.cfg.ModelDisplayName(m.Provider, m.ID); ov != "" {
			name = ov
		}
		result = append(result, modelDTO{
			ID:         m.ID,
			Name:       name,
			Provider:   m.Provider,
			ModelID:    m.ID,
			Plan:       plan,
			ContextWin: m.ContextWindow,
			MaxOut:     m.MaxOutputTokens,
			FreeTier:   free,
			Custom:     false,
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
		if seen[cm.ID] {
			continue
		}
		result = append(result, modelDTO{
			ID:         cm.ID,
			Name:       cm.Name,
			Provider:   cm.Provider,
			ModelID:    cm.ModelID,
			ContextWin: cm.ContextWindow,
			MaxOut:     cm.MaxOutput,
			FreeTier:   cm.FreeTier,
			Custom:     true,
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
		if m.Provider == "" || m.ModelID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "provider and model_id are required"})
			return
		}
		// Respect an explicit custom flag from the client. New user-added models
		// send custom:true; editing a built-in model sends custom:false so it is
		// stored as a display-name/parameter override rather than duplicating
		// the built-in entry in the model list.
		m.ID = config.ModelKey(m.Provider, m.ModelID)
		s.cfg.UpsertModel(m)
		if err := s.cfg.Save(config.DefaultPath()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		// Register the custom model so the engine can resolve it at chat time.
		s.registerCustomModel(m)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": config.ModelKey(m.Provider, m.ModelID)})

	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id is required"})
			return
		}
		if !s.cfg.DeleteModel(id) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "model not found"})
			return
		}
		s.reg.RemoveCustomModel(id)
		if err := s.cfg.Save(config.DefaultPath()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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
