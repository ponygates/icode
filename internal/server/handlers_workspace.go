package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ponygates/icode/internal/core/checkpoint"
	projectcontext "github.com/ponygates/icode/internal/core/context"
	"github.com/ponygates/icode/internal/core/todo"
	"github.com/ponygates/icode/internal/db"
)

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
	// /api/analytics/global → cross-session aggregate dashboard.
	if sessionID == "global" {
		s.handleAnalyticsGlobal(w, r)
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
	// Persist a snapshot so the global dashboard aggregates across sessions
	// and survives restarts. Best-effort: ignore errors when no store is wired.
	if s.db != nil {
		_ = s.db.UpsertSessionStats(db.SessionStatSnapshot{
			SessionID:          sessionID,
			TokensSaved:        stats.TokensSaved,
			CacheHitTokens:     stats.CacheHitTokens,
			EstimatedCost:      stats.EstimatedCost,
			EstimatedSavedCost: stats.EstimatedSavedCost,
			CacheHitRate:       stats.CacheHitRate,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// handleAnalyticsGlobal returns the cross-session aggregate plus a per-day
// savings trend. Built from the persisted session_stats table so it works
// across restarts, not just for the currently-live in-memory sessions.

func (s *Server) handleAnalyticsGlobal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		writeJSON(w, 503, map[string]any{"error": "store not available"})
		return
	}
	agg, err := s.db.GlobalStats()
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agg)
}

// ── Workspaces ──

// handleWorkspaces lists (GET) or creates (POST) workspaces.

func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, 503, map[string]any{"error": "store not available"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.db.ListWorkspaces()
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"workspaces": list})
	case http.MethodPost:
		var body struct {
			Name       string   `json:"name"`
			Path       string   `json:"path"`
			SessionIDs []string `json:"session_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			_ = r.Body.Close()
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ws := db.Workspace{Name: body.Name, Path: body.Path, SessionIDs: body.SessionIDs}
		if ws.Name == "" {
			ws.Name = "工作区"
		}
		if err := s.db.CreateWorkspace(ws); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		created, _ := s.db.GetWorkspace(ws.ID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleWorkspaceByID handles /api/workspaces/{id} and
// /api/workspaces/{id}/sessions (POST to set the ordered session membership).

func (s *Server) handleWorkspaceByID(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, 503, map[string]any{"error": "store not available"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	// Split into id and optional sub-path.
	id := rest
	sub := ""
	if idx := strings.Index(rest, "/"); idx >= 0 {
		id = rest[:idx]
		sub = rest[idx+1:]
	}
	if id == "" {
		http.Error(w, "workspace id required", http.StatusBadRequest)
		return
	}

	switch {
	case sub == "sessions" && r.Method == http.MethodPost:
		var body struct {
			// Append a single session (deep binding) when session_id is set.
			SessionID string `json:"session_id"`
			// Replace the whole ordered membership when session_ids is set.
			SessionIDs []string `json:"session_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			_ = r.Body.Close()
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.SessionID != "" {
			if err := s.db.AddSessionToWorkspace(id, body.SessionID); err != nil {
				writeJSON(w, 500, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "mode": "append"})
			return
		}
		if err := s.db.SetWorkspaceSessions(id, body.SessionIDs); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "mode": "replace"})
	case r.Method == http.MethodGet:
		ws, err := s.db.GetWorkspace(id)
		if err != nil {
			writeJSON(w, 404, map[string]any{"error": "workspace not found"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ws)
	case r.Method == http.MethodPut:
		var body struct {
			Name       string   `json:"name"`
			Path       string   `json:"path"`
			SessionIDs []string `json:"session_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			_ = r.Body.Close()
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ws, err := s.db.GetWorkspace(id)
		if err != nil {
			writeJSON(w, 404, map[string]any{"error": "workspace not found"})
			return
		}
		if body.Name != "" {
			ws.Name = body.Name
		}
		if body.Path != "" {
			ws.Path = body.Path
		}
		if body.SessionIDs != nil {
			ws.SessionIDs = body.SessionIDs
		}
		if err := s.db.UpdateWorkspace(*ws); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "workspace": ws})
	case r.Method == http.MethodDelete:
		if err := s.db.DeleteWorkspace(id); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ── Checkpoints ──

func (s *Server) handleCheckpoints(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/checkpoints/")
	if sessionID == "" || sessionID == "rewind" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	// /api/checkpoints/{sessionID}/diff → the diff handler (the trailing
	// /diff suffix is handled inline here rather than as a registered route,
	// so there is no bare /api/checkpoints/diff endpoint).
	if strings.HasSuffix(sessionID, "/diff") {
		s.handleCheckpointDiff(w, r)
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

// handleCheckpointDiff returns the unified diff of the last N checkpoints
// (GET /api/checkpoints/{sessionID}/diff?steps=N) — the data behind the
// desktop diff viewer.
func (s *Server) handleCheckpointDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/checkpoints/")
	rest = strings.TrimSuffix(rest, "/diff")
	sessionID := strings.TrimSuffix(rest, "/")
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	steps := 1
	if v := r.URL.Query().Get("steps"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			steps = n
		}
	}
	store, err := checkpoint.GetOrOpen(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	diff, err := store.Diff(r.Context(), steps)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"diff":  diff,
		"steps": steps,
	})
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
	// scope=project (default) targets ./ICODE.md; scope=user targets the
	// cross-project memory file at ~/.icode/ (Claude Code parity).
	wd, _ := os.Getwd()
	icodePath := findICODEPath(wd)
	if r.URL.Query().Get("scope") == "user" {
		if p, err := projectcontext.UserMemoryPath(); err == nil {
			icodePath = p
		}
	}
	switch r.Method {
	case http.MethodPost:
		// Quick-append a memory note (the `#` shortcut). Body: {"text": "..."}
		var body struct {
			Text  string `json:"text"`
			Scope string `json:"scope"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		scope := body.Scope
		if scope == "" {
			scope = r.URL.Query().Get("scope")
		}
		var err error
		if scope == "user" {
			err = projectcontext.AppendUserMemory(body.Text)
		} else {
			_, err = projectcontext.AppendProjectMemory(body.Text)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
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
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
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
