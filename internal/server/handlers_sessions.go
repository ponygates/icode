package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"

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
		trash := r.URL.Query().Get("trash") == "1"
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
				limit = n
			}
		}
		var sessions []types.Session
		var err error
		if trash {
			sessions, err = sessionum.ListDeleted(s.store, limit)
		} else {
			sessions, err = sessionum.ListNonDeleted(s.store, limit)
		}
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

// handleSessionImport creates a new session from an exported JSON document
// (POST /api/sessions/import). Import logic lives in sessionum so the CLI can
// share it. A malformed payload returns 400; storage failures return 500.
func (s *Server) handleSessionImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	data, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "read body: " + err.Error()})
		return
	}

	sess, imported, err := sessionum.Import(s.store, data)
	if err != nil {
		if errors.Is(err, sessionum.ErrParse) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": sess, "imported": imported})
}

// handleSessionRestore revives a soft-deleted session (POST /api/sessions/restore).
// It is registered with a longer path than /api/sessions/ so ServeMux's
// longest-pattern-wins rule routes it here instead of handleSessionByID.
func (s *Server) handleSessionRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.SessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing session_id"})
		return
	}
	sess, err := s.store.Get(body.SessionID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	if !sessionum.IsDeleted(sess) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restored": false})
		return
	}
	if err := sessionum.Restore(s.store, sess); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restored": true})
}

// handleSessionTrash soft-deletes a session (POST /api/sessions/trash): the
// transcript is archived with a summary and hidden from lists, recoverable via
// restore. Registered as a longer path than /api/sessions/ like restore.
func (s *Server) handleSessionTrash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.SessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing session_id"})
		return
	}
	sess, err := s.store.Get(body.SessionID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	if sessionum.IsDeleted(sess) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trashed": false})
		return
	}
	if err := sessionum.MarkDeleted(s.store, sess); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trashed": true})
}

// handleTrashPurge permanently deletes every soft-deleted session
// (POST /api/sessions/trash/purge). Irreversible.
func (s *Server) handleTrashPurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	removed, err := sessionum.PurgeAllTrash(s.store)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": removed})
}
