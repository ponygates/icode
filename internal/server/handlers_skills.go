package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ponygates/icode/internal/core/skills"
)

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "skills": s.engine.ListSkills()})
}

// handleSkillEnable toggles a skill's enabled state: POST = enable, DELETE = disable.
// Path: /api/skills/{name}/enable

func (s *Server) handleSkillEnable(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/skills/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || parts[1] != "enable" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	name := parts[0]
	if name == "" {
		http.Error(w, "skill name required", http.StatusBadRequest)
		return
	}
	var err error
	switch r.Method {
	case http.MethodPost:
		err = s.engine.EnableSkill(name)
	case http.MethodDelete:
		err = s.engine.DisableSkill(name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": r.Method == http.MethodPost})
}

// handleSkillMarket lists the built-in skill market (catalog) with install
// state, so the desktop UI can render a browsable, one-click-install store.

func (s *Server) handleSkillMarket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "market": skills.ListCatalog()})
}

// handleSkillMarketItem handles install (POST .../market/install) and
// uninstall (DELETE .../market/{name}).

func (s *Server) handleSkillMarketItem(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimPrefix(r.URL.Path, "/api/skills/market/")
	sub = strings.Trim(sub, "/")
	switch r.Method {
	case http.MethodPost:
		if sub != "install" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			_ = r.Body.Close()
			http.Error(w, "skill name required", http.StatusBadRequest)
			return
		}
		if err := skills.Install(body.Name); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "installed": body.Name})
	case http.MethodDelete:
		if sub == "" {
			http.Error(w, "skill name required", http.StatusBadRequest)
			return
		}
		if err := skills.Uninstall(sub); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "uninstalled": sub})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSkillImport installs a skill from a local SKILL.md file or directory
// (e.g. exported from another machine or downloaded from a community repo).

func (s *Server) handleSkillImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		_ = r.Body.Close()
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	if err := skills.Import(body.Path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "imported": body.Path})
}

// ── Teams ──

func (s *Server) handleTeams(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "teams": s.engine.ListTeams()})
}
