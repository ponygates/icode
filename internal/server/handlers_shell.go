package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/executil"
)

// shellRequest is the payload for POST /api/shell — the desktop counterpart of
// the CLI/TUI `!` shortcut. The output is surfaced to the user (zero tokens)
// and never fed to the model automatically, preserving iCode's token budget.
type shellRequest struct {
	Cmd string `json:"cmd"`
}

type shellResponse struct {
	OK       bool   `json:"ok"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// handleShell runs a shell command with a 60s timeout (mirroring simpleui's
// execShell) and returns its combined output. Local-only trust model, same as
// the CLI's `!` shortcut — the desktop backend already executes tools.
func (s *Server) handleShell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req shellRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body: " + err.Error()})
		return
	}
	cmdStr := strings.TrimSpace(req.Cmd)
	if cmdStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cmd is required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = executil.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = executil.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	output, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(interface{ ExitCode() int }); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}
	res := shellResponse{
		OK:       err == nil,
		Output:   strings.TrimRight(string(output), "\n"),
		ExitCode: exitCode,
	}
	if err != nil {
		res.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, res)
}
