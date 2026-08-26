package server

// Mesh intake endpoint — receives messages forwarded by peer iCode machines.

import (
	"net/http"
	"os"
	"strings"

	"github.com/ponygates/icode/internal/mesh"
)

func (s *Server) handleMeshMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// The concrete store must implement the messaging surface (the SQLite
	// store does); a bare SessionStore is rejected with a clear error.
	msgStore, ok := interface{}(s.store).(interface {
		SendAgentMessage(fromID, toID, body string) error
	})
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "message store unavailable"})
		return
	}
	sess, err := mesh.ReceiveHandler(msgStore, r)
	if err != nil {
		code := http.StatusBadRequest
		if err.Error() == "invalid mesh token" {
			code = http.StatusUnauthorized
		}
		writeJSON(w, code, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "delivered_to": sess})
}

// handleMeshPing answers peer connectivity probes (X-Mesh-Token gated).
func (s *Server) handleMeshPing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !mesh.VerifyToken(r.Header.Get("X-Mesh-Token")) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid mesh token"})
		return
	}
	host, _ := osHostname()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"host":    host,
		"version": strings.TrimPrefix(s.version, "v"),
	})
}

// osHostname is a tiny indirection for testability.
var osHostname = hostName

func hostName() (string, error) { return os.Hostname() }
