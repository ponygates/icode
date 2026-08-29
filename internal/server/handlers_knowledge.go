package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ponygates/icode/internal/core/knowledge"
)

// knowledgeStatus is the payload for GET /api/knowledge — the desktop
// counterpart of the CLI's /kb status. It reports whether a knowledge base is
// configured and how many passages are currently indexed.
type knowledgeStatus struct {
	Configured bool     `json:"configured"`
	Dirs       []string `json:"dirs,omitempty"`
	Chunks     int      `json:"chunks"`
}

// knowledgeImportRequest is the payload for POST /api/knowledge/import.
// Exactly one of Path (import a file from disk) or Name+Content (paste text)
// should be set.
type knowledgeImportRequest struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// knowledgeImportResponse reports the outcome of an import.
type knowledgeImportResponse struct {
	OK     bool   `json:"ok"`
	Chunks int    `json:"chunks,omitempty"`
	Error  string `json:"error,omitempty"`
}

// handleKnowledge serves GET /api/knowledge — status of the local document
// knowledge base (WorkBuddy 资料库 parity surface).
func (s *Server) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	km := s.engine.KnowledgeManager()
	if km == nil {
		writeJSON(w, http.StatusOK, knowledgeStatus{Configured: false})
		return
	}
	writeJSON(w, http.StatusOK, knowledgeStatus{
		Configured: true,
		Chunks:     km.ChunkCount(),
	})
}

// handleKnowledgeImport serves POST /api/knowledge/import — imports a single
// document into the knowledge base either from a local file path or from
// pasted text, then re-indexes (IDF re-weighted over the enlarged corpus).
func (s *Server) handleKnowledgeImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req knowledgeImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body: " + err.Error()})
		return
	}
	km := s.engine.KnowledgeManager()
	if km == nil {
		writeJSON(w, http.StatusBadRequest, knowledgeImportResponse{OK: false, Error: "知识库未配置，请在 config.yaml 的 knowledge.dirs 中指定文档目录"})
		return
	}

	if strings.TrimSpace(req.Path) != "" {
		n, err := km.ImportFile(strings.TrimSpace(req.Path))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, knowledgeImportResponse{OK: false, Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, knowledgeImportResponse{OK: true, Chunks: n})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "pasted.md"
	}
	n, err := km.AddContent(name, req.Content)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, knowledgeImportResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, knowledgeImportResponse{OK: true, Chunks: n})
}

// ensure knowledge import is always referenced so the package stays imported
// even if future refactors drop the direct calls.
var _ = knowledge.New
