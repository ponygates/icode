package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/mcp"
	"github.com/ponygates/icode/internal/types"
)

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list := make([]map[string]any, 0, len(s.cfg.MCP))
		for _, mc := range s.cfg.MCP {
			connected := false
			toolCount := 0
			if s.mcpPool != nil {
				if s.mcpPool.Has(mc.Name) {
					connected = true
					for _, t := range s.mcpPool.AllTools() {
						if strings.HasPrefix(t.Name, "mcp_"+mc.Name+"_") {
							toolCount++
						}
					}
				}
			}
			list = append(list, map[string]any{
				"name":       mc.Name,
				"type":       mc.Type,
				"command":    mc.Command,
				"args":       mc.Args,
				"url":        mc.URL,
				"enabled":    mc.Enabled,
				"trust_mode": mc.TrustMode,
				"connected":  connected,
				"tools":      toolCount,
			})
		}
		writeJSON(w, http.StatusOK, list)

	case http.MethodPut:
		var req config.MCPServerCfg
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			_ = r.Body.Close()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
			return
		}
		if req.Command != "" && containsDangerousChars(req.Command) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "command contains dangerous characters"})
			return
		}
		if req.Type == "" {
			req.Type = "stdio"
		}
		// Upsert into config.
		s.cfg.UpsertMCP(req)
		if err := s.cfg.Save(config.DefaultPath()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		// Apply connection changes to the live pool.
		if s.mcpPool != nil {
			s.mcpPool.Remove(req.Name)
			if req.Enabled {
				if err := s.mcpPool.Add(context.Background(), toMCPServerConfig(req)); err != nil {
					writeJSON(w, http.StatusOK, map[string]any{"ok": true, "warning": "saved but connect failed: " + err.Error()})
					return
				}
			}
			s.refreshMCPTools()
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case http.MethodDelete:
		var req struct {
			Name string `json:"name"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			_ = r.Body.Close()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
			return
		}
		s.cfg.RemoveMCP(req.Name)
		if err := s.cfg.Save(config.DefaultPath()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if s.mcpPool != nil {
			s.mcpPool.Remove(req.Name)
			s.refreshMCPTools()
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleMCPTrust updates the trust mode of a single MCP server (ask | readonly | all)
// without re-establishing the connection — it is a persisted preference only.

func (s *Server) handleMCPTrust(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name      string `json:"name"`
		TrustMode string `json:"trust_mode"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
		return
	}
	if req.TrustMode != "" && req.TrustMode != "ask" && req.TrustMode != "readonly" && req.TrustMode != "all" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "trust_mode must be one of ask|readonly|all"})
		return
	}
	found := false
	for i := range s.cfg.MCP {
		if s.cfg.MCP[i].Name == req.Name {
			s.cfg.MCP[i].TrustMode = req.TrustMode
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "mcp server not found: " + req.Name})
		return
	}
	if err := s.cfg.Save(config.DefaultPath()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMCPTest connects to a server definition (without persisting it) and
// returns the discovered tool list, so the UI can validate a configuration.

func (s *Server) handleMCPTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req config.MCPServerCfg
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
		return
	}
	if req.Command != "" && containsDangerousChars(req.Command) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "command contains dangerous characters"})
		return
	}
	if req.Type == "" {
		req.Type = "stdio"
	}
	client := mcp.NewClient(toMCPServerConfig(req))
	if err := client.Connect(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer client.Close()
	tools, err := client.DiscoverTools(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tools": names})
}

// handleMCPTools returns all tools currently discovered across connected MCP
// servers (used by the UI to show the effective tool set).

func (s *Server) handleMCPTools(w http.ResponseWriter, r *http.Request) {
	if s.mcpPool == nil {
		writeJSON(w, http.StatusOK, []types.ToolDef{})
		return
	}
	writeJSON(w, http.StatusOK, s.mcpPool.AllTools())
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// containsDangerousChars reports whether s contains shell metacharacters or
// path traversal sequences that could be used for command injection.
func containsDangerousChars(s string) bool {
	dangerous := []string{";", "&", "|", "`", "$", "(", ")", "{", "}", "<", ">", "!", "#", "*", "?", "[", "]", "~", "\n", "\r", ".."}
	for _, ch := range dangerous {
		if strings.Contains(s, ch) {
			return true
		}
	}
	return false
}

var embeddedFrontend fs.FS

// SetEmbeddedFrontend sets the embedded frontend filesystem (from go:embed)
// so handleFrontend can serve the UI without relying on disk files.
func SetEmbeddedFrontend(f fs.FS) {
	embeddedFrontend = f
}
