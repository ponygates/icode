// Package server provides the HTTP API server that bridges the Electron
// desktop frontend with the iCode Go backend.
//
// The server exposes a JSON-RPC-style API over HTTP endpoints, with
// server-sent events (SSE) for streaming chat responses.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/conversation"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/db"
	"github.com/ponygates/icode/internal/mcp"
	"github.com/ponygates/icode/internal/types"
	"github.com/ponygates/icode/internal/update"
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
	version string

	mcpPool      *mcp.Pool
	mcpToolNames map[string]bool // tool names currently registered into the engine
	mcpMu        sync.Mutex

	httpSrv  *http.Server
	listener net.Listener
	port     int
	mu       sync.Mutex
	apiToken string
}

// Store exposes the session store, used by handlers and external consumers
// (e.g. tests) that need to seed or inspect sessions directly.
func (s *Server) Store() types.SessionStore { return s.store }

// Config configures the API server.
type ServerConfig struct {
	Config   *config.Config
	Registry types.ProviderRegistry
	Store    types.SessionStore
	DB       *db.Store
	Engine   *conversation.Engine
	Gate     *permission.Gate
	Updater  *modelupdate.Service
	Version  string // app version
	Port     int    // 0 = auto-assign
}

// New creates a new API server.
func New(cfg ServerConfig) *Server {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		// crypto/rand virtually never fails; fall back to a SHA-256 of high-entropy
		// process material rather than a guessable timestamp+pid.
		h := sha256.Sum256([]byte(fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())))
		tokenBytes = h[:]
	}
	return &Server{
		cfg:      cfg.Config,
		reg:      cfg.Registry,
		store:    cfg.Store,
		db:       cfg.DB,
		engine:   cfg.Engine,
		gate:     cfg.Gate,
		updater:  cfg.Updater,
		version:  cfg.Version,
		port:     cfg.Port,
		apiToken: hex.EncodeToString(tokenBytes),
	}
}

// Start begins listening and serving API requests.
// Returns the actual port the server is listening on (useful for port=0).
func (s *Server) Start(ctx context.Context) (int, error) {
	// Apply the configured proxy to the process environment before any
	// provider request can fire (harmless no-op when unset).
	s.cfg.WithRLock(func() { applyProxyEnv(s.cfg.Proxy) })
	// Purge soft-deleted sessions past their restore window so the trash
	// never grows unbounded across restarts.
	if removed, err := sessionum.PurgeExpiredTrash(s.store, 0); err == nil && removed > 0 {
		log.Printf("[iCode] purged %d expired trash session(s)", removed)
	}
	mux := http.NewServeMux()

	// Health & status
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/status", s.handleStatus)

	// App update check
	mux.HandleFunc("/api/update/check", s.handleUpdateCheck)

	// Provider & models
	mux.HandleFunc("/api/providers", s.handleListProviders)
	mux.HandleFunc("/api/models", s.handleListModels)
	mux.HandleFunc("/api/models/refresh", s.handleRefreshModels)

	// Sessions
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/import", s.handleSessionImport)
	mux.HandleFunc("/api/sessions/restore", s.handleSessionRestore)
	mux.HandleFunc("/api/sessions/trash", s.handleSessionTrash)
	mux.HandleFunc("/api/sessions/trash/purge", s.handleTrashPurge)
	mux.HandleFunc("/api/sessions/", s.handleSessionByID)
	mux.HandleFunc("/api/sessions/{id}/messages", s.handleSessionMessages)
	mux.HandleFunc("/api/sessions/{id}/messages/{msgID}", s.handleSessionMessages)
	mux.HandleFunc("/api/sessions/{id}/clear", s.handleSessionClear)
	mux.HandleFunc("/api/search", s.handleSearch)

	// Chat
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/chat/stop", s.handleChatStop)
	mux.HandleFunc("/api/slash", s.handleSlash)
	mux.HandleFunc("/api/shell", s.handleShell)

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
	mux.HandleFunc("/api/memory", s.handleMemory) // alias used by the desktop `#` command

	// Skills & Teams (Claude-Code-parity surfaces)
	mux.HandleFunc("/api/skills", s.handleSkills)
	mux.HandleFunc("/api/skills/", s.handleSkillEnable)            // POST/DELETE /api/skills/{name}/enable
	mux.HandleFunc("/api/skills/market", s.handleSkillMarket)      // GET built-in market/catalog
	mux.HandleFunc("/api/skills/market/", s.handleSkillMarketItem) // POST install / DELETE {name}
	mux.HandleFunc("/api/skills/import", s.handleSkillImport)      // POST import local path
	mux.HandleFunc("/api/teams", s.handleTeams)

	// MCP (Model Context Protocol) server management — Reasonix-style tool integration
	mux.HandleFunc("/api/mcp", s.handleMCP)
	mux.HandleFunc("/api/mcp/test", s.handleMCPTest)
	mux.HandleFunc("/api/mcp/tools", s.handleMCPTools)
	mux.HandleFunc("/api/mcp/trust", s.handleMCPTrust) // PUT {name, trust_mode}

	// Todo list (session-scoped scratchpad backing the TodoWrite tool)
	mux.HandleFunc("/api/todos/", s.handleTodos)

	// Analytics: token/cache/cost stats for a session
	mux.HandleFunc("/api/analytics/", s.handleAnalytics)

	// Workspaces — desktop project containers that group sessions
	mux.HandleFunc("/api/workspaces", s.handleWorkspaces)
	mux.HandleFunc("/api/workspaces/", s.handleWorkspaceByID)

	// Static frontend — serve the desktop UI at /
	mux.HandleFunc("/", s.handleFrontend)

	// Recover middleware first: a handler panic becomes a clean 500 + stack
	// trace in desktop.log instead of crashing the whole desktop process.
	handler := s.corsMiddleware(s.authMiddleware(sameOriginGuard(s.serverOrigin, recoverMiddleware(mux))))

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("listen: %w", err)
	}
	s.listener = listener
	s.port = listener.Addr().(*net.TCPAddr).Port

	s.httpSrv = &http.Server{
		Handler:           handler,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB — bound the request header size
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
	//
	// IMPORTANT: an enabled-but-unreachable MCP server (or a stdio subprocess
	// that never responds) can block its connect for up to the client's own
	// 30s call timeout. Doing this synchronously on the boot path used to block
	// bootDesktopBackend *before the WebView2 window even opened* — the classic
	// "桌面启动卡死". Connect in the background with a bounded context so the
	// server (and the desktop window) come up immediately; MCP tools simply
	// populate when the connections succeed.
	s.mcpPool = mcp.NewPool()
	s.mcpToolNames = make(map[string]bool)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[iCode MCP] init panic: %v", r)
			}
		}()
		mcpCtx, mcpCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer mcpCancel()

		mcpList := s.cfg.MCP
		// WorkBuddy bridge: auto-import connectors from ~/.workbuddy/mcp.json so
		// servers configured in WorkBuddy are usable here without re-configuring.
		if s.cfg.ImportWorkBuddyEnabled() {
			if imported, err := config.LoadWorkBuddyMCP(config.WorkBuddyMCPPath(), mcpList); err != nil {
				log.Printf("[iCode MCP] workbuddy import skipped: %v", err)
			} else if len(imported) > 0 {
				log.Printf("[iCode MCP] imported %d server(s) from WorkBuddy mcp.json", len(imported))
				mcpList = append(mcpList, imported...)
			}
		}
		// Connect every enabled server CONCURRENTLY (each bounded by mcpCtx) so
		// a slow/unreachable server can never serialise the others or the boot
		// path. The shared 20s deadline caps total wall time even with many
		// connectors imported from WorkBuddy.
		var wg sync.WaitGroup
		for _, mc := range mcpList {
			if !mc.Enabled {
				continue
			}
			wg.Add(1)
			go func(mc config.MCPServerCfg) {
				defer wg.Done()
				// A panic connecting one server must not hang wg.Wait.
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[iCode MCP] connect %q panic recovered: %v", mc.Name, r)
					}
				}()
				if err := s.mcpPool.Add(mcpCtx, toMCPServerConfig(mc)); err != nil {
					log.Printf("[iCode MCP] failed to connect %q: %v", mc.Name, err)
				}
			}(mc)
		}
		wg.Wait()
		log.Printf("[iCode MCP] background connect finished")
		s.refreshMCPTools()
	}()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[iCode Server] Serve panic recovered: %v", r)
			}
		}()
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
		"version": s.version,
		"uptime":  time.Now().Format(time.RFC3339),
	})
}

// handleUpdateCheck reports whether a newer release is available on GitHub.
// Failures (offline, rate limit) return 200 with available=false so the UI
// never shows a scary error for a background check.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	info, err := update.Check(s.version)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false, "current": strings.TrimPrefix(s.version, "v"), "error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, info)
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

// handleFrontend serves the embedded desktop UI (or falls back to disk files
// during development). API routes are handled by the dedicated handlers.
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

// recoverMiddleware turns any panic in a handler (or a goroutine it spawns
// that panics before returning) into a clean HTTP 500 and logs the full stack
// to desktop.log. Without this, a single bad request could crash the whole
// desktop process. net/http already recovers per-request, but this also
// captures the stack for diagnosis instead of losing it to a console-less
// (-H windowsgui) build.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rv := recover(); rv != nil {
				log.Printf("[server] PANIC recovered %s %s: %v\n%s", r.Method, r.URL.Path, rv, debug.Stack())
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" || strings.HasPrefix(r.URL.Path, "/") && !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if isLocalRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		authHeader := r.Header.Get("Authorization")
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token != "" && token == s.apiToken {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

func (s *Server) APIToken() string {
	return s.apiToken
}

// serverOrigin returns the canonical http origin that this server presents.
// The desktop renderer is served from this same origin, so every legitimate
// browser-side request is same-origin; CORS is only shared with that exact
// origin rather than reflecting any localhost page.
func (s *Server) serverOrigin() string {
	host := "127.0.0.1"
	if s.listener != nil {
		if a := s.listener.Addr(); a != nil {
			host, _, _ = net.SplitHostPort(a.String())
		}
	}
	return fmt.Sprintf("http://%s:%d", host, s.port)
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowedOrigin := ""
		// Only ever reflect the exact origin this server itself serves. Any other
		// origin (e.g. a malicious page loaded from a different localhost port)
		// gets no CORS headers, so cross-site reads fail.
		if origin != "" {
			if origin == s.serverOrigin() {
				allowedOrigin = origin
			}
		}
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if !strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Security-Policy",
				"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOriginGuard rejects state-changing cross-origin requests (CSRF). The
// browser attaches an Origin header to every POST/PUT/DELETE; a cross-site page
// (or a malicious localhost page from a different port) carries a foreign
// Origin and is refused. Requests with no Origin at all (CLI, Node/VS Code
// extension, curl) are trusted — they are not subject to browser CSRF.
//
// serverOrigin is evaluated per-request because the actual port is only known
// after the listener is bound.
func sameOriginGuard(serverOrigin func() string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && strings.HasPrefix(r.URL.Path, "/api/") {
			if origin := r.Header.Get("Origin"); origin != "" && origin != serverOrigin() {
				http.Error(w, "cross-origin request denied", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// handleTodos serves the session-scoped todo list at /api/todos/{sessionID}.
//
//	GET  → { items: [...], counts: {pending, in_progress, completed, total} }
//	POST → replaces the list (used by the desktop for manual edits; the
//	       model-driven flow goes through the todo_write tool instead)
