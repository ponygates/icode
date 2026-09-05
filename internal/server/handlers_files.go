package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleFiles returns the project file tree as a flat list with relative
// paths and file-type hints. Used by the desktop FileTree sidebar panel.
//
//	Query:   ?path=<root-dir>   (defaults to cwd)
//	Response: {"files": [{"path":"...","type":"go","isDir":false,"depth":1}, ...]}
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	root := r.URL.Query().Get("path")
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
	}
	// Normalise to absolute so Walk starts from a concrete directory.
	if !filepath.IsAbs(root) {
		abs, err := filepath.Abs(root)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		root = abs
	}

	// P1 hardening: the tree is only ever rendered for a workspace bound to
	// this server — an arbitrary absolute path would let any loopback page
	// enumerate the whole filesystem. Clamp to the process working directory
	// (kept in sync with /cd and the active workspace), allowing only
	// subdirectories of it.
	if cwd, err := os.Getwd(); err == nil {
		if !pathsWithin(cwd, root) {
			writeJSON(w, 400, map[string]any{"error": "path is outside the active workspace"})
			return
		}
	}

	// Skip these directories when scanning.
	skip := map[string]bool{
		"node_modules": true, ".git": true, ".idea": true, ".vscode": true,
		"dist": true, "build": true, ".next": true, "vendor": true,
		"__pycache__": true, ".cache": true, "target": true,
		".icode": true, ".mimocode": true,
	}

	var files []map[string]any
	// Walk with a depth cap of 5 to keep response size bounded.
	const maxDepth = 5
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		// Depth is counted by path separators.
		depth := strings.Count(rel, string(filepath.Separator)) + 1
		if depth > maxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if skip[name] {
				return filepath.SkipDir
			}
			// Show the directory itself (files inside will be listed separately).
			files = append(files, map[string]any{
				"path":  rel,
				"type":  "dir",
				"isDir": true,
				"depth": depth,
			})
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		typeName := extToType(ext)
		files = append(files, map[string]any{
			"path":  rel,
			"type":  typeName,
			"isDir": false,
			"depth": depth,
		})
		return nil
	})
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"files": files})
}

// pathsWithin reports whether child equals parent or lives underneath it
// (lexically, after cleaning — symlink resolution is handled separately by
// the gate sandbox for tool actions).
func pathsWithin(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// extToType maps a file extension to a short human-readable label used by the
// frontend to pick an icon colour.
func extToType(ext string) string {
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx", ".jsx", ".js":
		return "ts"
	case ".py":
		return "py"
	case ".md":
		return "md"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".html":
		return "html"
	case ".css", ".scss", ".less":
		return "css"
	case ".sh":
		return "sh"
	case ".sql":
		return "sql"
	case ".proto":
		return "proto"
	case ".svg":
		return "svg"
	case ".png", ".jpg", ".jpeg", ".gif":
		return "image"
	default:
		if ext == "" {
			return "file"
		}
		return "other"
	}
}
