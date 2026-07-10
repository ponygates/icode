// Package server provides the HTTP API server that bridges the Electron
// desktop frontend with the iCode Go backend.
//
// The server exposes a JSON-RPC-style API over HTTP endpoints, with
// server-sent events (SSE) for streaming chat responses.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/conversation"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/db"
	"github.com/ponygates/icode/internal/types"
	"github.com/ponygates/icode/pkg/modelupdate"
)

// Server is the iCode backend HTTP API server.
type Server struct {
	cfg     *config.Config
	reg     types.ProviderRegistry
	store   types.SessionStore
	db      *db.Store
	engine  *conversation.Engine
	gate    *permission.Gate
	updater *modelupdate.Service

	httpSrv  *http.Server
	listener net.Listener
	port     int
	mu       sync.Mutex
}

// Config configures the API server.
type ServerConfig struct {
	Config    *config.Config
	Registry  types.ProviderRegistry
	Store     types.SessionStore
	DB        *db.Store
	Engine    *conversation.Engine
	Gate      *permission.Gate
	Updater   *modelupdate.Service
	Port      int // 0 = auto-assign
}

// New creates a new API server.
func New(cfg ServerConfig) *Server {
	return &Server{
		cfg:     cfg.Config,
		reg:     cfg.Registry,
		store:   cfg.Store,
		db:      cfg.DB,
		engine:  cfg.Engine,
		gate:    cfg.Gate,
		updater: cfg.Updater,
		port:    cfg.Port,
	}
}

// Start begins listening and serving API requests.
// Returns the actual port the server is listening on (useful for port=0).
func (s *Server) Start(ctx context.Context) (int, error) {
	mux := http.NewServeMux()

	// Health & status
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/status", s.handleStatus)

	// Provider & models
	mux.HandleFunc("/api/providers", s.handleListProviders)
	mux.HandleFunc("/api/models", s.handleListModels)
	mux.HandleFunc("/api/models/refresh", s.handleRefreshModels)

	// Sessions
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/", s.handleSessionByID)

	// Chat
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/chat/stop", s.handleChatStop)

	// Config
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/config/lang", s.handleSetLanguage)
	mux.HandleFunc("/api/config/keys", s.handleListKeys)
	mux.HandleFunc("/api/config/key", s.handleSetKey)
	mux.HandleFunc("/api/config/models", s.handleListCustomModels)
	mux.HandleFunc("/api/config/model", s.handleConfigModel)
	mux.HandleFunc("/api/config/provider", s.handleConfigProvider)

	// Permission
	mux.HandleFunc("/api/permission/mode", s.handleSetPermissionMode)
	mux.HandleFunc("/api/permission/respond", s.handlePermissionRespond)

	// Static frontend — serve the desktop UI at /
	mux.HandleFunc("/", s.handleFrontend)

	// CORS middleware
	handler := corsMiddleware(mux)

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("listen: %w", err)
	}
	s.listener = listener
	s.port = listener.Addr().(*net.TCPAddr).Port

	s.httpSrv = &http.Server{
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Write port to a temp file so the Electron app can discover it
	s.writePortFile(s.port)

	go func() {
		log.Printf("[iCode Server] Listening on http://%s", listener.Addr().String())
		if err := s.httpSrv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("[iCode Server] Error: %v", err)
		}
	}()

	return s.port, nil
}

// Port returns the actual port.
func (s *Server) Port() int { return s.port }

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.httpSrv != nil {
		if err := s.httpSrv.Shutdown(ctx); err != nil {
			return err
		}
	}
	s.cleanupPortFile()
	return nil
}

// ============================================================================
// Handlers
// ============================================================================

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": "0.1.0",
		"uptime":  time.Now().Format(time.RFC3339),
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	providers := s.reg.List()
	totalModels := len(s.reg.ListAllModels())
	sessions, _ := s.store.List(100, 0)
	cwd, _ := os.Getwd()

	writeJSON(w, http.StatusOK, map[string]any{
		"providers":    providers,
		"total_models": totalModels,
		"sessions":     len(sessions),
		"port":         s.port,
		"db_active":    s.db != nil,
		"cwd":          cwd,
	})
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	type providerInfo struct {
		Name      string `json:"name"`
		Models    int    `json:"models"`
		CacheSupp bool   `json:"cache_support"`
	}

	var result []providerInfo
	for _, name := range s.reg.List() {
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
		ID          string `json:"id"`
		Name        string `json:"name"`
		Provider    string `json:"provider"`
		ModelID     string `json:"model_id"`
		Plan        string `json:"plan"`
		ContextWin  int    `json:"context_window"`
		MaxOut      int    `json:"max_output_tokens"`
		FreeTier    bool   `json:"free_tier"`
		Custom      bool   `json:"custom"`
	}
	result := make([]modelDTO, 0, len(models)+len(s.cfg.Models))
	for _, m := range models {
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
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if m.Provider == "" || m.ModelID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "provider and model_id are required"})
			return
		}
		m.Custom = true
		s.cfg.UpsertModel(m)
		if err := s.cfg.Save(config.DefaultPath()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
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
			Name    string `json:"name"`
			APIBase string `json:"api_base"`
			APIKey  string `json:"api_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
		s.cfg.Providers[req.Name] = pc
		if err := s.cfg.Save(config.DefaultPath()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

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
			}
		}
		s.cfg.Models = kept
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

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sessions, err := s.store.List(100, 0)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, sessions)

	case http.MethodPost:
		var sess types.Session
		if err := json.NewDecoder(r.Body).Decode(&sess); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := s.store.Create(&sess); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, sess)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from path: /api/sessions/{id}
	id := filepath.Base(r.URL.Path)
	if id == "" || id == "sessions" {
		http.Error(w, "missing session ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		sess, err := s.store.Get(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, sess)

	case http.MethodDelete:
		if err := s.store.Delete(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		Content   string `json:"content"`
		Model     string `json:"model"`
		Provider  string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not available"})
		return
	}

	// Ensure a session exists for this conversation.
	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("web-%d", time.Now().UnixNano())
	}
	sess, err := s.store.Get(req.SessionID)
	if err != nil {
		sess = &types.Session{
			ID:           req.SessionID,
			ModelID:      orDefault(req.Model, "deepseek-v4-flash"),
			ProviderName: orDefault(req.Provider, "deepseek"),
			Title:        firstLine(req.Content, 40),
		}
		_ = s.store.Create(sess)
	} else {
		// Reflect any model/provider change from the client.
		changed := false
		if req.Model != "" && sess.ModelID != req.Model {
			sess.ModelID = req.Model
			changed = true
		}
		if req.Provider != "" && sess.ProviderName != req.Provider {
			sess.ProviderName = req.Provider
			changed = true
		}
		if changed {
			_ = s.store.Update(sess)
		}
	}

	// SSE streaming
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	eventCh, err := s.engine.Send(r.Context(), req.SessionID, req.Content)
	if err != nil {
		fmt.Fprintf(w, "data: {\"type\":\"error\",\"content\":%q}\n\n", err.Error())
		flusher.Flush()
		return
	}

	for event := range eventCh {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		if event.Type == types.EventDone || event.Type == types.EventError {
			return
		}
	}
}

func (s *Server) handleChatStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if s.engine != nil {
		s.engine.Stop(req.SessionID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.cfg)
	case http.MethodPut:
		var cfg config.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		// Update fields without copying the mutex.
		s.cfg.Language = cfg.Language
		s.cfg.Defaults.Model = cfg.Defaults.Model
		s.cfg.Defaults.Provider = cfg.Defaults.Provider
		s.cfg.Defaults.Mode = cfg.Defaults.Mode
		s.cfg.TUI.Theme = cfg.TUI.Theme
		s.cfg.TUI.DiffMode = cfg.TUI.DiffMode
		// Persist to disk so settings survive restarts.
		if err := s.cfg.Save(config.DefaultPath()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListKeys returns provider key configuration status WITHOUT exposing
// the secret itself (APIKey carries json:"-"). Used by the desktop settings UI
// to show which providers are configured and to manage custom providers.
func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type keyInfo struct {
		Name    string `json:"name"`
		KeySet  bool   `json:"key_set"`
		APIBase string `json:"api_base,omitempty"`
	}
	seen := map[string]bool{}
	var result []keyInfo
	// Built-in providers registered in the engine.
	for _, name := range s.reg.List() {
		pc := s.cfg.Providers[name]
		result = append(result, keyInfo{Name: name, KeySet: pc.APIKey != "", APIBase: pc.APIBase})
		seen[name] = true
	}
	// Custom providers that only exist in the config file.
	for name, pc := range s.cfg.Providers {
		if seen[name] {
			continue
		}
		result = append(result, keyInfo{Name: name, KeySet: pc.APIKey != "", APIBase: pc.APIBase})
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if req.Provider == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "provider is required"})
		return
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSetLanguage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	s.cfg.Language = req.Language
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "language": req.Language})
}

func (s *Server) handleSetPermissionMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if s.gate != nil {
		s.gate.SetMode(permission.Mode(req.Mode))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": req.Mode})
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

// ============================================================================
// Port file discovery
// ============================================================================

func (s *Server) writePortFile(port int) {
	dir := filepath.Join(os.TempDir(), "icode")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "port"), []byte(fmt.Sprintf("%d", port)), 0644)
}

func (s *Server) cleanupPortFile() {
	dir := filepath.Join(os.TempDir(), "icode")
	os.Remove(filepath.Join(dir, "port"))
}

// ============================================================================
// Helpers
// ============================================================================

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// firstLine returns the first non-empty line of s, truncated to max runes.
func firstLine(s string, max int) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			r := []rune(line)
			if len(r) > max {
				return string(r[:max]) + "…"
			}
			return line
		}
	}
	return ""
}

// handleFrontend serves the desktop UI.
// The standalone desktop/index.html is a self-contained build that talks to
// the real backend; we prefer it, then fall back to the Vite dist output.
func (s *Server) handleFrontend(w http.ResponseWriter, r *http.Request) {
	// API routes are handled by other handlers
	if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
		http.NotFound(w, r)
		return
	}

	candidates := []string{
		filepath.Join("desktop", "index.html"),
		filepath.Join("desktop", "dist", "index.html"),
		filepath.Join("..", "desktop", "dist", "index.html"),
	}

	for _, indexPath := range candidates {
		if _, err := os.Stat(indexPath); err == nil {
			if r.URL.Path == "/" || r.URL.Path == "" {
				http.ServeFile(w, r, indexPath)
				return
			}
			// Serve static assets (CSS/JS) relative to the chosen dir.
			dir := filepath.Dir(indexPath)
			fs := http.FileServer(http.Dir(dir))
			http.StripPrefix("/", fs).ServeHTTP(w, r)
			return
		}
	}

	// No UI found, show API info
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<html><body style="font-family:system-ui;padding:40px;background:#1e1e2e;color:#cdd6f4">
<h1>iCode API Server</h1><p>Desktop UI not found. Expected <code>desktop/index.html</code>.</p>
<p>API available at <a href="/api/health" style="color:#89b4fa">/api/health</a></p></body></html>`)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
