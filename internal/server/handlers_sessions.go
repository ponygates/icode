package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/types"
)

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
		kept := sessions[:0]
		for _, sess := range sessions {
			if sessionum.IsDeleted(&sess) {
				continue
			}
			kept = append(kept, sess)
		}
		writeJSON(w, http.StatusOK, kept)

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
