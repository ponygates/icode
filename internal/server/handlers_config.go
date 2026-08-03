package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/desktop"
)

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Serialise under the read lock so concurrent writers (chat, MCP
		// connection, key updates) never race with the JSON marshal.
		var body []byte
		s.cfg.WithRLock(func() {
			body, _ = json.Marshal(s.cfg)
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	case http.MethodPut:
		var cfg config.Config
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			_ = r.Body.Close()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		// Update fields under the write lock and persist atomically, so a
		// concurrent reader never observes a partially-updated config.
		// Mutations use direct field assignment (no re-entrant locks).
		if err := s.cfg.WithLock(func() error {
			// Update fields without copying the mutex.
			s.cfg.Language = cfg.Language
			s.cfg.Defaults.Model = cfg.Defaults.Model
			s.cfg.Defaults.Provider = cfg.Defaults.Provider
			s.cfg.Defaults.Mode = cfg.Defaults.Mode
			s.cfg.Defaults.Temperature = cfg.Defaults.Temperature
			s.cfg.Defaults.MaxTokens = cfg.Defaults.MaxTokens
			s.cfg.Defaults.Cache = cfg.Defaults.Cache
			s.cfg.Defaults.SystemPrompt = cfg.Defaults.SystemPrompt
			s.cfg.Defaults.FallbackModels = cfg.Defaults.FallbackModels
			s.cfg.TUI.Theme = cfg.TUI.Theme
			s.cfg.TUI.DiffMode = cfg.TUI.DiffMode
			s.cfg.TUI.SyntaxHL = cfg.TUI.SyntaxHL
			s.cfg.Tools.BashTimeout = cfg.Tools.BashTimeout
			s.cfg.Tools.AllowedPaths = cfg.Tools.AllowedPaths
			s.cfg.Tools.DeniedCommands = cfg.Tools.DeniedCommands
			s.cfg.Update.AutoUpdate = cfg.Update.AutoUpdate
			s.cfg.Update.Channel = cfg.Update.Channel
			s.cfg.Update.IntervalH = cfg.Update.IntervalH
			// Desktop settings: launch-on-login + fixed backend port.
			s.cfg.Autostart = cfg.Autostart
			s.cfg.Server.Port = cfg.Server.Port
			return s.cfg.SaveLocked(config.DefaultPath())
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		// Apply the launch-on-login preference immediately (best-effort;
		// platform failures are logged, never fatal to the request).
		if err := desktop.ApplyAutostart(cfg.Autostart); err != nil {
			log.Printf("[desktop] autostart apply failed: %v", err)
		}
		// Push generation parameters into the live engine.
		s.engine.SetGenerationParams(cfg.Defaults.Temperature, cfg.Defaults.MaxTokens)
		s.engine.SetSystemPrompt(cfg.Defaults.SystemPrompt)
		s.engine.SetFallbackModels(cfg.Defaults.FallbackModels)
		// Push tool sandbox settings into the live permission gate so the
		// desktop "工具与权限" settings take effect without a restart.
		if s.gate != nil {
			s.gate.SetAllowedPaths(cfg.Tools.AllowedPaths)
			s.gate.SetDeniedCommands(cfg.Tools.DeniedCommands)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListKeys returns provider key configuration status WITHOUT exposing
// the secret itself (APIKey carries json:"-"). Used by the desktop settings UI
// to show which providers are configured and to manage custom providers.

func (s *Server) handleSetLanguage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Language string `json:"language"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	s.cfg.Language = req.Language
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "language": req.Language})
}

// handleConfigReset restores the behaviour/UI settings to their defaults while
// PRESERVING the user's API credentials (providers) and custom models, which
// would be dangerous to wipe. It then pushes the reset values into the live
// engine and permission gate.

func (s *Server) handleConfigReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	def := config.Default()
	// Keep providers (keys) and models; reset everything else.
	def.Providers = s.cfg.Providers
	def.Models = s.cfg.Models
	s.cfg.Language = def.Language
	s.cfg.Defaults = def.Defaults
	s.cfg.TUI = def.TUI
	s.cfg.Tools = def.Tools
	s.cfg.Update = def.Update
	if err := s.cfg.Save(config.DefaultPath()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if s.engine != nil {
		s.engine.SetGenerationParams(s.cfg.Defaults.Temperature, s.cfg.Defaults.MaxTokens)
	}
	if s.gate != nil {
		s.gate.SetMode(permission.Mode(s.cfg.Defaults.Mode))
		s.gate.SetAllowedPaths(s.cfg.Tools.AllowedPaths)
		s.gate.SetDeniedCommands(s.cfg.Tools.DeniedCommands)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSetPermissionMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Mode string `json:"mode"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if s.gate != nil {
		// Normalise the desktop mode vocabulary (ask/auto/plan/yolo) onto the
		// backend's internal modes. "ask" maps to agent (prompt per call);
		// "auto" maps to the new auto mode (read-only auto, mutating asks).
		switch req.Mode {
		case "ask":
			s.gate.SetMode(permission.ModeAgent)
		case "auto":
			s.gate.SetMode(permission.ModeAuto)
		case "plan":
			s.gate.SetMode(permission.ModePlan)
		case "yolo":
			s.gate.SetMode(permission.ModeYOLO)
		default:
			s.gate.SetMode(permission.Mode(req.Mode))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": req.Mode})
}

// handleSessionAllow toggles session-scoped auto-approval for a single
// conversation (the desktop "本会话全部自动放行" button). It calls the live
// gate so the change takes effect immediately without a restart.

func (s *Server) handleSessionAllow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		Allow     bool   `json:"allow"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if s.gate != nil {
		s.gate.SetSessionAllow(req.SessionID, req.Allow)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "allow": req.Allow})
}

// handlePermissionRespond delivers the desktop client's answer to an
// interactive permission request raised via the SSE chat stream.

func (s *Server) handlePermissionRespond(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		RequestID string `json:"request_id"`
		Decision  string `json:"decision"` // "allow" | "deny" | "allow_all"
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	var decision permission.Decision
	switch req.Decision {
	case "allow":
		decision = permission.DecisionAllow
	case "allow_all", "allowall":
		decision = permission.DecisionAllowAll
	default:
		decision = permission.DecisionDeny
	}

	if s.engine != nil {
		s.engine.SetPermissionResponse(req.RequestID, decision)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleToolRules returns the persistent tool rules and allows updating them.

func (s *Server) handleToolRules(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil || s.gate == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "not available"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		rules := s.gate.GetToolRules()
		writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
	case http.MethodPost:
		var body struct {
			Tool string `json:"tool"`
			Rule string `json:"rule"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			_ = r.Body.Close()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		s.gate.SetToolRule(body.Tool, body.Rule)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSessionToolAllow records a per-tool allow for a session.

func (s *Server) handleSessionToolAllow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
		Tool      string `json:"tool"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if s.gate == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "gate not available"})
		return
	}
	s.gate.SetSessionToolAllow(body.SessionID, body.Tool)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ============================================================================
// Port file discovery
// ============================================================================
