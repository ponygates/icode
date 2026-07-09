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

	// Permission
	mux.HandleFunc("/api/permission/mode", s.handleSetPermissionMode)

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

	writeJSON(w, http.StatusOK, map[string]any{
		"providers":    providers,
		"total_models": totalModels,
		"sessions":     len(sessions),
		"port":         s.port,
		"db_active":    s.db != nil,
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
		Plan        string `json:"plan"`
		ContextWin  int    `json:"context_window"`
		MaxOut      int    `json:"max_output_tokens"`
		FreeTier    bool   `json:"free_tier"`
	}
	result := make([]modelDTO, len(models))
	for i, m := range models {
		plan := "Coding Plan"
		free := false
		if len(m.Plans) > 0 {
			plan = m.Plans[0].Name
			free = m.Plans[0].FreeTier != nil
		}
		result[i] = modelDTO{
			ID:         m.ID,
			Name:       m.Name,
			Provider:   m.Provider,
			Plan:       plan,
			ContextWin: m.ContextWindow,
			MaxOut:     m.MaxOutputTokens,
			FreeTier:   free,
		}
	}
	writeJSON(w, http.StatusOK, result)
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not available"})
		return
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
		// Update fields without copying the mutex
		s.cfg.Language = cfg.Language
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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

// handleFrontend serves the desktop UI.
// It looks for the Vite build output in several locations.
func (s *Server) handleFrontend(w http.ResponseWriter, r *http.Request) {
	// API routes are handled by other handlers
	if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
		http.NotFound(w, r)
		return
	}

	// Try to serve from Vite dist directory
	distDirs := []string{
		"desktop/dist",
		"../desktop/dist",
		filepath.Join("desktop", "dist"),
	}

	for _, dir := range distDirs {
		indexPath := filepath.Join(dir, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			// If path is "/", serve index.html
			if r.URL.Path == "/" || r.URL.Path == "" {
				http.ServeFile(w, r, indexPath)
				return
			}
			// Serve static files
			fs := http.FileServer(http.Dir(dir))
			http.StripPrefix("/", fs).ServeHTTP(w, r)
			return
		}
	}

	// Fallback: redirect to the standalone HTML
	standaloneHTML := filepath.Join("desktop", "index.html")
	if _, err := os.Stat(standaloneHTML); err == nil {
		http.ServeFile(w, r, standaloneHTML)
		return
	}

	// No UI found, show API info
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<html><body style="font-family:system-ui;padding:40px;background:#1e1e2e;color:#cdd6f4">
<h1>iCode API Server</h1><p>Desktop UI not built. Run: <code>cd desktop && npm run build</code></p>
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
