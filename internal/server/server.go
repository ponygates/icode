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
	"io/fs"
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
	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/todo"
	"github.com/ponygates/icode/internal/db"
	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/mcp"
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

	mcpPool      *mcp.Pool
	mcpToolNames map[string]bool // tool names currently registered into the engine
	mcpMu        sync.Mutex

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
	mux.HandleFunc("/api/search", s.handleSearch)

	// Chat
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/chat/stop", s.handleChatStop)

	// Config
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/config/reset", s.handleConfigReset)
	mux.HandleFunc("/api/config/lang", s.handleSetLanguage)
	mux.HandleFunc("/api/config/keys", s.handleListKeys)
	mux.HandleFunc("/api/config/key", s.handleSetKey)
	mux.HandleFunc("/api/config/models", s.handleListCustomModels)
	mux.HandleFunc("/api/config/model", s.handleConfigModel)
	mux.HandleFunc("/api/config/provider", s.handleConfigProvider)

	// Permission
	mux.HandleFunc("/api/permission/mode", s.handleSetPermissionMode)
	mux.HandleFunc("/api/permission/rules", s.handleToolRules)
	mux.HandleFunc("/api/permission/allow-tool", s.handleSessionToolAllow)
	mux.HandleFunc("/api/permission/respond", s.handlePermissionRespond)
	mux.HandleFunc("/api/permission/session-allow", s.handleSessionAllow)

	// Checkpoints — Reasonix-style rewind history
	mux.HandleFunc("/api/checkpoints/", s.handleCheckpoints)
	mux.HandleFunc("/api/checkpoints/rewind", s.handleRewind)

	// Memory
	mux.HandleFunc("/api/memory/icode", s.handleMemory)

	// MCP (Model Context Protocol) server management — Reasonix-style tool integration
	mux.HandleFunc("/api/mcp", s.handleMCP)
	mux.HandleFunc("/api/mcp/test", s.handleMCPTest)
	mux.HandleFunc("/api/mcp/tools", s.handleMCPTools)

	// Todo list (session-scoped scratchpad backing the TodoWrite tool)
	mux.HandleFunc("/api/todos/", s.handleTodos)

	// Analytics: token/cache/cost stats for a session
	mux.HandleFunc("/api/analytics/", s.handleAnalytics)

	// Static frontend — serve the desktop UI at /
	mux.HandleFunc("/", s.handleFrontend)

	// CORS middleware
	handler := s.corsMiddleware(mux)

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

	// Register any user-defined (custom) models that were persisted in the
	// config file so the engine can resolve them at chat time.
	for _, m := range s.cfg.Models {
		if m.Custom {
			s.registerCustomModel(m)
		}
	}

	// Initialise the MCP pool: connect every enabled MCP server configured in
	// the user config and surface its tools to the conversation engine.
	s.mcpPool = mcp.NewPool()
	s.mcpToolNames = make(map[string]bool)
	for _, mc := range s.cfg.MCP {
		if mc.Enabled {
			if err := s.mcpPool.Add(context.Background(), toMCPServerConfig(mc)); err != nil {
				log.Printf("[iCode MCP] failed to connect %q: %v", mc.Name, err)
			}
		}
	}
	s.refreshMCPTools()

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
		if s.isProviderDisabled(m.Provider) {
			continue
		}
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
			Name      string `json:"name"`
			APIBase   string `json:"api_base"`
			APIKey    string `json:"api_key"`
			TimeoutSec int   `json:"timeout_sec"`
			Disabled  bool   `json:"disabled"`
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
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query().Get("q")
	if query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "query parameter q is required"})
		return
	}
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := fmt.Sscanf(l, "%d", &limit); err != nil || n != 1 {
			limit = 20
		}
	}
	results, err := s.store.SearchMessages(query, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "query": query})
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
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&sess); err != nil {
			_ = r.Body.Close()
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

	case http.MethodPut:
		// Rename / retitle a session (Reasonix-style session management).
		var body struct {
			Title    string `json:"title"`
			ModelID  string `json:"model_id"`
			Provider string `json:"provider"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			_ = r.Body.Close()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		sess, err := s.store.Get(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		if body.Title != "" {
			sess.Title = body.Title
		}
		if body.ModelID != "" {
			sess.ModelID = body.ModelID
		}
		if body.Provider != "" {
			sess.ProviderName = body.Provider
		}
		if err := s.store.Update(sess); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
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
		SessionID   string              `json:"session_id"`
		Content     string              `json:"content"`
		Model       string              `json:"model"`
		Provider    string              `json:"provider"`
		Attachments []types.Attachment  `json:"attachments,omitempty"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not available"})
		return
	}

	log.Printf("[server] chat: session=%s model=%s provider=%s", req.SessionID, req.Model, req.Provider)

	// Ensure a session exists for this conversation.
	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("web-%d", time.Now().UnixNano())
	}
	sess, err := s.store.Get(req.SessionID)
	if err != nil {
		sess = &types.Session{
			ID:           req.SessionID,
			ModelID:      orDefault(req.Model, "openrouter/free"),
			ProviderName: orDefault(req.Provider, "openrouter"),
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

	// Pre-check: resolve the model to verify Provider + credentials exist
	prov, _, resolveErr := s.reg.ResolveModel(sess.ModelID)
	if resolveErr != nil {
		log.Printf("[server] chat: resolve model %q failed: %v", sess.ModelID, resolveErr)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		errData, _ := json.Marshal(types.StreamEvent{
			Type:    types.EventError,
			Content: fmt.Sprintf("模型 %q 未找到，请检查设置中的模型配置", sess.ModelID),
		})
		fmt.Fprintf(w, "data: %s\n\n", errData)
		flusher.Flush()
		return
	}
	// Quick health check to verify provider connectivity
	if healthErr := prov.Health(r.Context()); healthErr != nil {
		log.Printf("[server] chat: provider %s health check failed: %v", sess.ProviderName, healthErr)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		errData, _ := json.Marshal(types.StreamEvent{
			Type:    types.EventError,
			Content: fmt.Sprintf("无法连接 %s: %v。请检查 API Key 和网络设置（Ctrl+, 打开设置）", sess.ProviderName, healthErr),
		})
		fmt.Fprintf(w, "data: %s\n\n", errData)
		flusher.Flush()
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Use a timeout context so a hung provider doesn't block the stream
	// indefinitely. 120s matches the server WriteTimeout.
	chatCtx, cancel := context.WithTimeout(r.Context(), 115*time.Second)
	defer cancel()

	eventCh, err := s.engine.Send(chatCtx, req.SessionID, req.Content, req.Attachments)
	if err != nil {
		log.Printf("[server] chat: engine.Send failed session=%s model=%s err=%v", req.SessionID, sess.ModelID, err)
		errData, _ := json.Marshal(types.StreamEvent{
			Type:    types.EventError,
			Content: err.Error(),
		})
		fmt.Fprintf(w, "data: %s\n\n", errData)
		flusher.Flush()
		return
	}

	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

			if event.Type == types.EventDone || event.Type == types.EventError {
				return
			}
		case <-r.Context().Done():
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
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

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
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			_ = r.Body.Close()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
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
		// Push generation parameters into the live engine.
		s.engine.SetGenerationParams(s.cfg.Defaults.Temperature, s.cfg.Defaults.MaxTokens)
		s.engine.SetSystemPrompt(s.cfg.Defaults.SystemPrompt)
	s.engine.SetFallbackModels(s.cfg.Defaults.FallbackModels)
		// Push tool sandbox settings into the live permission gate so the
		// desktop "工具与权限" settings take effect without a restart.
		if s.gate != nil {
			s.gate.SetAllowedPaths(cfg.Tools.AllowedPaths)
			s.gate.SetDeniedCommands(cfg.Tools.DeniedCommands)
		}
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
		Timeout int    `json:"timeout_sec"`
		Disabled bool  `json:"disabled"`
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

// ============================================================================
// MCP (Model Context Protocol) — server management & tool integration
// ============================================================================

// mcpToolAdapter wraps a discovered MCP tool definition so it satisfies the
// types.Tool interface and routes execution through the shared MCP pool.
type mcpToolAdapter struct {
	def  types.ToolDef
	pool *mcp.Pool
}

func (a *mcpToolAdapter) Def() types.ToolDef { return a.def }

func (a *mcpToolAdapter) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		// Fall back to an empty arg map so the MCP server still receives a call.
		m = map[string]any{}
	}
	return a.pool.Execute(ctx, a.def.Name, m)
}

// toMCPServerConfig converts a persisted config entry to the live mcp.ServerConfig.
func toMCPServerConfig(c config.MCPServerCfg) mcp.ServerConfig {
	return mcp.ServerConfig{
		Name:    c.Name,
		Type:    mcp.Transport(c.Type),
		Command: c.Command,
		Args:    c.Args,
		Env:     c.Env,
		URL:     c.URL,
		Headers: c.Headers,
		Enabled: c.Enabled,
	}
}

// refreshMCPTools re-registers every discovered MCP tool into the conversation
// engine, replacing any previously-registered MCP tools. Tools from disabled
// or disconnected servers are dropped.
func (s *Server) refreshMCPTools() {
	if s.engine == nil {
		return
	}
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()

	// Remove stale MCP tools registered in a previous refresh.
	for name := range s.mcpToolNames {
		s.engine.UnregisterTool(name)
	}
	s.mcpToolNames = make(map[string]bool)

	for _, def := range s.mcpPool.AllTools() {
		s.engine.RegisterTool(&mcpToolAdapter{def: def, pool: s.mcpPool})
		s.mcpToolNames[def.Name] = true
	}
}

// handleMCP lists, adds/updates, or removes MCP servers.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list := make([]map[string]any, 0, len(s.cfg.MCP))
		for _, mc := range s.cfg.MCP {
			connected := false
			toolCount := 0
			if s.mcpPool != nil {
				if s.mcpPool.Has(mc.Name) {
					connected = true
					for _, t := range s.mcpPool.AllTools() {
						if strings.HasPrefix(t.Name, "mcp_"+mc.Name+"_") {
							toolCount++
						}
					}
				}
			}
			list = append(list, map[string]any{
				"name":      mc.Name,
				"type":      mc.Type,
				"command":   mc.Command,
				"args":      mc.Args,
				"url":       mc.URL,
				"enabled":   mc.Enabled,
				"connected": connected,
				"tools":     toolCount,
			})
		}
		writeJSON(w, http.StatusOK, list)

	case http.MethodPut:
		var req config.MCPServerCfg
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
		if req.Command != "" && containsDangerousChars(req.Command) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "command contains dangerous characters"})
			return
		}
		if req.Type == "" {
			req.Type = "stdio"
		}
		// Upsert into config.
		s.cfg.UpsertMCP(req)
		if err := s.cfg.Save(config.DefaultPath()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		// Apply connection changes to the live pool.
		if s.mcpPool != nil {
			s.mcpPool.Remove(req.Name)
			if req.Enabled {
				if err := s.mcpPool.Add(context.Background(), toMCPServerConfig(req)); err != nil {
					writeJSON(w, http.StatusOK, map[string]any{"ok": true, "warning": "saved but connect failed: " + err.Error()})
					return
				}
			}
			s.refreshMCPTools()
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case http.MethodDelete:
		var req struct {
			Name string `json:"name"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			_ = r.Body.Close()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
			return
		}
		s.cfg.RemoveMCP(req.Name)
		if err := s.cfg.Save(config.DefaultPath()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if s.mcpPool != nil {
			s.mcpPool.Remove(req.Name)
			s.refreshMCPTools()
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleMCPTest connects to a server definition (without persisting it) and
// returns the discovered tool list, so the UI can validate a configuration.
func (s *Server) handleMCPTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req config.MCPServerCfg
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
	if req.Command != "" && containsDangerousChars(req.Command) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "command contains dangerous characters"})
		return
	}
	if req.Type == "" {
		req.Type = "stdio"
	}
	client := mcp.NewClient(toMCPServerConfig(req))
	if err := client.Connect(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer client.Close()
	tools, err := client.DiscoverTools(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tools": names})
}

// handleMCPTools returns all tools currently discovered across connected MCP
// servers (used by the UI to show the effective tool set).
func (s *Server) handleMCPTools(w http.ResponseWriter, r *http.Request) {
	if s.mcpPool == nil {
		writeJSON(w, http.StatusOK, []types.ToolDef{})
		return
	}
	writeJSON(w, http.StatusOK, s.mcpPool.AllTools())
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// isProviderDisabled reports whether a provider has been turned off in the
// config. Disabled providers are hidden from the model/key lists and
// deregistered from the live registry so they cannot be used for chat.
func (s *Server) isProviderDisabled(name string) bool {
	if pc, ok := s.cfg.Providers[name]; ok {
		return pc.Disabled
	}
	return false
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

// registerCustomModel registers a user-defined model in the live registry so
// the engine can resolve it at chat time. It is registered under its canonical
// id (provider/model_id) and also under its raw provider model id, because the
// desktop UI sometimes sends one and sometimes the other.
func (s *Server) registerCustomModel(m config.ModelCfg) {
	if m.ID == "" {
		m.ID = config.ModelKey(m.Provider, m.ModelID)
	}
	info := types.ModelInfo{
		ID:              m.ID,
		Name:            m.Name,
		Provider:        m.Provider,
		ContextWindow:   m.ContextWindow,
		MaxOutputTokens: m.MaxOutput,
	}
	s.reg.RegisterCustomModel(info, m.ModelID)
}

// containsDangerousChars reports whether s contains shell metacharacters or
// path traversal sequences that could be used for command injection.
func containsDangerousChars(s string) bool {
	dangerous := []string{";", "&", "|", "`", "$", "(", ")", "{", "}", "<", ">", "!", "#", "*", "?", "[", "]", "~", "\n", "\r", ".."}
	for _, ch := range dangerous {
		if strings.Contains(s, ch) {
			return true
		}
	}
	return false
}

var embeddedFrontend fs.FS

// SetEmbeddedFrontend sets the embedded frontend filesystem (from go:embed)
// so handleFrontend can serve the UI without relying on disk files.
func SetEmbeddedFrontend(f fs.FS) {
	embeddedFrontend = f
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

	// Strategy 1: Embedded frontend (from go:embed, always available in
	// the self-contained binary). This is the primary path for the
	// desktop launcher — no disk files needed.
	if embeddedFrontend != nil {
		path := r.URL.Path
		if path == "/" || path == "" {
			path = "/index.html"
		}
		path = strings.TrimPrefix(path, "/")
		data, err := fs.ReadFile(embeddedFrontend, path)
		if err == nil {
			// Set content type based on extension
			if strings.HasSuffix(path, ".css") {
				w.Header().Set("Content-Type", "text/css")
			} else if strings.HasSuffix(path, ".js") {
				w.Header().Set("Content-Type", "application/javascript")
			} else if strings.HasSuffix(path, ".html") {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
			} else if strings.HasSuffix(path, ".svg") {
				w.Header().Set("Content-Type", "image/svg+xml")
			}
			w.Write(data)
			return
		}
	}

	// Strategy 2: Disk files (for development without rebuild).
	candidates := []string{
		filepath.Join("desktop", "dist", "index.html"),
		filepath.Join("desktop", "index.html"),
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

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowedOrigin := ""
		if origin != "" {
			// Only allow localhost and 127.0.0.1 origins.
			if strings.HasPrefix(origin, "http://localhost:") ||
				strings.HasPrefix(origin, "http://127.0.0.1:") ||
				strings.HasPrefix(origin, "https://localhost:") ||
				strings.HasPrefix(origin, "https://127.0.0.1:") {
				allowedOrigin = origin
			}
		} else {
			allowedOrigin = fmt.Sprintf("http://localhost:%d", s.port)
		}
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleTodos serves the session-scoped todo list at /api/todos/{sessionID}.
//   GET  → { items: [...], counts: {pending, in_progress, completed, total} }
//   POST → replaces the list (used by the desktop for manual edits; the
//          model-driven flow goes through the todo_write tool instead)
func (s *Server) handleTodos(w http.ResponseWriter, r *http.Request) {
	// URL shape: /api/todos/<sessionID>
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/todos/")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		http.Error(w, "sessionID required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		items := todo.Default.Get(sessionID)
		p, a, d, t := todo.Default.Counts(sessionID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": items,
			"counts": map[string]int{
				"pending":     p,
				"in_progress": a,
				"completed":   d,
				"total":       t,
			},
		})
	case http.MethodPost:
		var body struct {
			Items []todo.TodoItem `json:"items"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			_ = r.Body.Close()
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		saved := todo.Default.Replace(sessionID, body.Items)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": saved})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/analytics/")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		http.Error(w, "sessionID required", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.engine == nil {
		writeJSON(w, 503, map[string]any{"error": "engine not available"})
		return
	}
	stats := s.engine.SessionStats(sessionID)
	if stats == nil {
		writeJSON(w, 404, map[string]any{"error": "no data for session"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// ── Checkpoints ──

func (s *Server) handleCheckpoints(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/checkpoints/")
	if sessionID == "" || sessionID == "rewind" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		store, err := checkpoint.GetOrOpen(sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		entries, err := store.List(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRewind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
		Steps     int    `json:"steps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.SessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}
	if body.Steps <= 0 {
		body.Steps = 1
	}
	store, err := checkpoint.GetOrOpen(body.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	files, err := store.Rewind(r.Context(), body.Steps)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "steps": body.Steps, "files": files,
	})
}

// ── Memory ──

func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	// Find ICODE.md in current directory or parent
	wd, _ := os.Getwd()
	icodePath := findICODEPath(wd)
	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(icodePath)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"content": ""})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"content": string(data)})
	case http.MethodPut:
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if err := os.WriteFile(icodePath, []byte(body.Content), 0644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func findICODEPath(wd string) string {
	for _, name := range []string{"ICODE.md", "icode.md", "CLAUDE.md", "claude.md"} {
		p := filepath.Join(wd, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(wd, "ICODE.md")
}
