package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"

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
	writeJSON(w, http.StatusOK, res)
}
