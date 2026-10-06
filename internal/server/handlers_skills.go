package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
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

// handleSkillSourceList probes a remote skill source (GitHub repo, GitHub
// tree URL or a direct SKILL.md URL) and returns the installable skills it
// advertises. POST /api/skills/source/list {source}.

func (s *Server) handleSkillSourceList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Source == "" {
		_ = r.Body.Close()
		http.Error(w, "source required", http.StatusBadRequest)
		return
	}
	list, err := skills.ListFromSource(body.Source)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "skills": list})
}

// handleSkillSourceInstall downloads and installs a skill from a remote
// source. POST /api/skills/source/install {source, path} — path comes from a
// prior list call (repo-internal path); direct file URLs ignore it.

func (s *Server) handleSkillSourceInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Source string `json:"source"`
		Path   string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Source == "" {
		_ = r.Body.Close()
		http.Error(w, "source required", http.StatusBadRequest)
		return
	}
	name, err := skills.InstallFromSource(body.Source, body.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "installed": name})
}

// handleSkillSources manages the user's saved remote sources (A3):
// GET    /api/skills/sources        → list
// POST   /api/skills/sources        → add {source, alias?}
// DELETE /api/skills/sources/{src}  → remove (src may contain slashes)

func (s *Server) handleSkillSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sources": skills.ListSources()})
	case http.MethodPost:
		var body struct {
			Source string `json:"source"`
			Alias  string `json:"alias"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Source == "" {
			_ = r.Body.Close()
			http.Error(w, "source required", http.StatusBadRequest)
			return
		}
		added, err := skills.AddSource(body.Source, body.Alias)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, os.ErrExist) {
				status = http.StatusConflict
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "added": added})
	case http.MethodDelete:
		// The source string itself may contain slashes (owner/repo, URLs),
		// so the whole remaining path — URL-decoded by net/http — is the key.
		src := strings.TrimPrefix(r.URL.Path, "/api/skills/sources/")
		if src == "" {
			http.Error(w, "source required", http.StatusBadRequest)
			return
		}
		if err := skills.RemoveSource(src); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, os.ErrNotExist) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": src})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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
