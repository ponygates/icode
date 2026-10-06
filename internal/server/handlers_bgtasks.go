package server

// Desktop Agents view endpoints: structured background-task listings and
// per-task cancel. Backed by the tool package's global task managers, so
// the list covers both shell commands (bg-N) and sub-agents (agt-N) started
// from any session in this process.

import (
	"net/http"
	"strings"

	"github.com/ponygates/icode/internal/core/tool"
)

// handleBgTasks serves GET /api/bgtasks — every background task as a typed
// snapshot (id, kind, status, elapsed, label, tail). Read-only, so it keeps
// the pre-existing loopback trust like the other GETs.
func (s *Server) handleBgTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks": tool.ListBgTaskInfos(),
	})
}

// handleBgTaskCancel serves POST /api/bgtasks/{id}/cancel. Mutating, so it
// sits behind requireAPITokenForMutating like the other privileged writes.
func (s *Server) handleBgTaskCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Path shape: /api/bgtasks/{id}/cancel
	rest := strings.TrimPrefix(r.URL.Path, "/api/bgtasks/")
	id, action, ok := strings.Cut(rest, "/")
	if !ok || action != "cancel" || id == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var cancelled bool
	switch {
	case strings.HasPrefix(id, "agt-"):
		cancelled = tool.CancelAgentTask(id)
	case strings.HasPrefix(id, "bg-"):
		cancelled = tool.CancelShellTask(id)
	default:
		http.Error(w, "bad task id", http.StatusBadRequest)
		return
	}
	if !cancelled {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "no such running task"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
