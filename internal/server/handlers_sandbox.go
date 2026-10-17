package server

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/sandbox"
)

// handleSandbox reports and updates the operating-system confinement applied to
// model-issued shell commands.
//
// GET returns the runtime status next to the persisted configuration. The two
// can disagree: mode=auto on a platform without a backend is stored as auto but
// runs unconfined, and mode was changed but the process has not been restarted.
// Reporting only the configuration would tell users they have a boundary they
// do not have.
//
// PUT persists new settings. The backend is built once at startup — an
// AppContainer profile, DACL grants and private temp directories cannot be
// swapped underneath running tasks — so the response sets restart_required
// instead of pretending the change is live.
func (s *Server) handleSandbox(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.sandboxState())
	case http.MethodPut:
		var body struct {
			Mode           *string  `json:"mode"`
			Network        *string  `json:"network"`
			WritablePaths  []string `json:"writable_paths"`
			GrantWorkspace *bool    `json:"grant_workspace"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			_ = r.Body.Close()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if body.Mode != nil && !validSandboxMode(*body.Mode) {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": "mode must be one of auto, on, off",
			})
			return
		}
		if err := s.cfg.WithLock(func() error {
			if body.Mode != nil {
				s.cfg.Sandbox.Mode = strings.ToLower(strings.TrimSpace(*body.Mode))
			}
			if body.Network != nil {
				net := strings.ToLower(strings.TrimSpace(*body.Network))
				if net != string(sandbox.NetworkAllow) {
					net = string(sandbox.NetworkDeny)
				}
				s.cfg.Sandbox.Network = net
			}
			if body.WritablePaths != nil {
				s.cfg.Sandbox.WritablePaths = body.WritablePaths
			}
			if body.GrantWorkspace != nil {
				v := *body.GrantWorkspace
				s.cfg.Sandbox.GrantWorkspace = &v
			}
			return s.cfg.SaveLocked(config.DefaultPath())
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		state := s.sandboxState()
		state["ok"] = true
		state["restart_required"] = true
		writeJSON(w, http.StatusOK, state)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSandboxGrants lists and revokes the directory access entries the
// sandbox backend wrote on the user's behalf.
//
// Confining a command means adding an inheritable ACE to its working directory,
// which is a lasting change to the filesystem made by a background process.
// Listing it is what makes the change visible; DELETE is what makes it
// reversible. Without both, "the sandbox touched my project directory" is a
// claim the user can neither confirm nor undo.
func (s *Server) handleSandboxGrants(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		grants, err := sandbox.ListGrants()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"grants": grants,
			// The UI disables revocation rather than offering a button that
			// can only fail. Windows is the only backend that writes ACEs.
			"revocable": runtime.GOOS == "windows",
		})
	case http.MethodDelete:
		var body struct {
			Path string `json:"path"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			_ = r.Body.Close()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if strings.TrimSpace(body.Path) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "path is required"})
			return
		}
		revoked, err := sandbox.Revoke(body.Path)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		grants, _ := sandbox.ListGrants()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"revoked": revoked,
			"grants":  grants,
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// sandboxState merges runtime status with the persisted configuration.
func (s *Server) sandboxState() map[string]any {
	st := sandbox.Status()
	grant := true
	var writable []string
	var mode, network string
	s.cfg.WithRLock(func() {
		mode = s.cfg.Sandbox.Mode
		network = s.cfg.Sandbox.Network
		writable = append([]string(nil), s.cfg.Sandbox.WritablePaths...)
		if s.cfg.Sandbox.GrantWorkspace != nil {
			grant = *s.cfg.Sandbox.GrantWorkspace
		}
	})
	return map[string]any{
		"runtime": st,
		"config": map[string]any{
			"mode":            mode,
			"network":         network,
			"writable_paths":  writable,
			"grant_workspace": grant,
		},
		"restart_required": false,
	}
}

func validSandboxMode(m string) bool {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case string(sandbox.ModeAuto), string(sandbox.ModeOn), string(sandbox.ModeOff):
		return true
	}
	return false
}
