package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ponygates/icode/internal/executil"
)

// codeblockRequest is the payload for POST /api/codeblock/open — the desktop
// counterpart of the TUI's "open code block in editor" affordance. The backend
// writes the snippet to a temp file and opens it with the OS default editor,
// so the WebView2 desktop needs no native file bridge for this action.
type codeblockRequest struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

type codeblockResponse struct {
	OK       bool   `json:"ok"`
	Path     string `json:"path,omitempty"`
	Error    string `json:"error,omitempty"`
}

// handleCodeBlockOpen saves a code-block snippet to a temp file and opens it
// with the platform's default editor (Claude Code desktop parity for the
// "open in editor" code-block toolbar action). The filename is sanitised to a
// bare base name to prevent path traversal — content always lands in the
// system temp dir.
func (s *Server) handleCodeBlockOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req codeblockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "content is required"})
		return
	}

	// Sanitise the filename: strip directory components so a hostile snippet
	// can never write outside the temp dir, and fall back to a generic name.
	name := filepath.Base(strings.TrimSpace(req.Filename))
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "icode-snippet.txt"
	}
	if !strings.HasSuffix(name, ".txt") && strings.Contains(name, ".") == false {
		name += ".txt"
	}

	path := filepath.Join(os.TempDir(), name)
	if err := os.WriteFile(path, []byte(req.Content), 0o600); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "write temp file: " + err.Error()})
		return
	}

	if err := openWithDefaultApp(path); err != nil {
		writeJSON(w, http.StatusInternalServerError, codeblockResponse{OK: false, Path: path, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, codeblockResponse{OK: true, Path: path})
}

// openWithDefaultApp launches the OS default handler for a file without
// blocking the server. Windows uses `start`, macOS `open`, Linux `xdg-open`.
func openWithDefaultApp(path string) error {
	switch runtime.GOOS {
	case "windows":
		cmd := executil.Command("cmd", "/C", "start", "", path)
		return cmd.Start()
	case "darwin":
		cmd := executil.Command("open", path)
		return cmd.Start()
	default:
		cmd := executil.Command("xdg-open", path)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("xdg-open: %w", err)
		}
		return nil
	}
}
