package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/slashui"
	"github.com/ponygates/icode/pkg/modelupdate"
)

var errUpdaterUnavailable = errors.New("model catalog updater not available")

// slashRequest is the payload for POST /api/slash.
type slashRequest struct {
	Text      string `json:"text"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	Mode      string `json:"mode"`
	Security  string `json:"security"`
}

// handleSlash dispatches a CLI-parity slash command and returns the shared
// Result. State-changing commands return updates (model/provider/mode/…)
// that the frontend adopts for subsequent requests.
func (s *Server) handleSlash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req slashRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body: " + err.Error()})
		return
	}
	if req.Text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "text is required"})
		return
	}

	cwd, _ := os.Getwd()
	backend := &slashui.Backend{
		Engine:    s.engine,
		SessStore: s.store,
		Gate:      s.gate,
		RefreshModels: func(ctx context.Context) ([]modelupdate.ProviderUpdate, error) {
			if s.updater == nil {
				return nil, errUpdaterUnavailable
			}
			return s.updater.UpdateAll(ctx)
		},
		RegisterCustomModel: func(provider, modelID, name string) string {
			if name == "" {
				name = modelID
			}
			m := config.ModelCfg{
				Provider: provider,
				ModelID:  modelID,
				Name:     name,
				Custom:   true,
			}
			m.ID = config.ModelKey(provider, modelID)
			s.cfg.UpsertModel(m)
			if err := s.cfg.Save(config.DefaultPath()); err != nil {
				return err.Error()
			}
			s.registerCustomModel(m)
			return ""
		},
		RemoveCustomModel: func(id string) string {
			if !s.cfg.DeleteModel(id) {
				return fmt.Sprintf("model %q not found", id)
			}
			s.reg.RemoveCustomModel(id)
			if err := s.cfg.Save(config.DefaultPath()); err != nil {
				return err.Error()
			}
			return ""
		},
	}
	state := &slashui.State{
		SessionID: req.SessionID,
		Model:     req.Model,
		Provider:  req.Provider,
		Mode:      req.Mode,
		Security:  req.Security,
		CWD:       cwd,
		Version:   s.version,
	}

	res := slashui.Execute(r.Context(), backend, state, req.Text)

	// /cd relocated the session's working directory: chdir the process so
	// every later tool (bash, file read/write) resolves from the new path,
	// and update the owning workspace's bound Path so the desktop file tree
	// and the workspace list stay in sync.
	if res.CWD != "" {
		if err := os.Chdir(res.CWD); err == nil {
			cwd = res.CWD
			s.syncWorkspacePath(req.SessionID, res.CWD)
		}
	}

	writeJSON(w, http.StatusOK, res)
}

// syncWorkspacePath finds the workspace that owns sessionID and updates its
// bound Path to the new working directory so the desktop file tree and the
// workspace list stay in sync. Best-effort — failures are logged, not fatal.
// Sessions not yet bound to any workspace are left untouched (they'll be
// bound when a new session is created under a workspace).
func (s *Server) syncWorkspacePath(sessionID, newPath string) {
	if s.db == nil || sessionID == "" {
		return
	}
	list, err := s.db.ListWorkspaces()
	if err != nil {
		return
	}
	for _, ws := range list {
		for _, sid := range ws.SessionIDs {
			if sid == sessionID {
				if ws.Path == newPath {
					return
				}
				ws.Path = newPath
				_ = s.db.UpdateWorkspace(ws)
				return
			}
		}
	}
}
