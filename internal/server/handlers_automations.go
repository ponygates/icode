package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ponygates/icode/internal/scheduler"
)

// handleAutomations lists (GET) or creates (POST) scheduled automations.
func (s *Server) handleAutomations(w http.ResponseWriter, r *http.Request) {
	if s.sch == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "tasks": []any{}})
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": true,
			"tasks":   s.sch.List(),
		})
	case http.MethodPost:
		var req struct {
			Name     string `json:"name"`
			Prompt   string `json:"prompt"`
			Schedule string `json:"schedule"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		t, err := s.sch.Create(req.Name, req.Prompt, req.Schedule)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, t)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

// handleAutomationByID handles /api/automations/{id} and sub-paths:
//   - GET    {id}          — task detail
//   - PUT    {id}          — update task
//   - DELETE {id}          — remove task
//   - POST   {id}/run      — run immediately
//   - GET    {id}/history  — recent run records
func (s *Server) handleAutomationByID(w http.ResponseWriter, r *http.Request) {
	if s.sch == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/automations/")
	parts := strings.Split(path, "/")
	id := parts[0]
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id required"})
		return
	}
	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}

	switch {
	case sub == "run" && r.Method == http.MethodPost:
		rec, err := s.sch.RunNow(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, rec)

	case sub == "history" && r.Method == http.MethodGet:
		limit := 20
		if v := r.URL.Query().Get("limit"); v != "" {
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
				limit = n
			}
		}
		runs, err := s.sch.History(id, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"runs": runs})

	case r.Method == http.MethodGet:
		t, ok := s.sch.Get(id)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "task not found"})
			return
		}
		writeJSON(w, http.StatusOK, t)

	case r.Method == http.MethodPut:
		var patch scheduler.Task
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		t, err := s.sch.Update(id, patch)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, t)

	case r.Method == http.MethodDelete:
		if err := s.sch.Delete(id); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}
