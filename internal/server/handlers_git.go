package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/executil"
)

// Git workbench endpoints (Claude Code desktop / Reasonix desktop parity):
// the desktop renders a "review diffs → stage → commit" panel on top of plain
// git plumbing. All commands run in the server process cwd, which /cd keeps
// pointed at the active project — the same directory the agent's bash tool
// sees. Trust model matches /api/shell (local, same-origin).

// gitFile is one entry of the porcelain status.
type gitFile struct {
	Path        string `json:"path"`
	Index       string `json:"index"`     // staged status letter (M/A/D/R/C), "" when none
	Worktree    string `json:"worktree"`  // unstaged status letter (M/D), "" when none
	Untracked   bool   `json:"untracked"` // "??"
	RenamedFrom string `json:"renamed_from,omitempty"`
}

type gitStatus struct {
	Repo   bool      `json:"repo"`
	Branch string    `json:"branch,omitempty"`
	Ahead  int       `json:"ahead"`
	Behind int       `json:"behind"`
	Files  []gitFile `json:"files"`
}

func gitRun(ctx context.Context, args ...string) (string, error) {
	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := executil.CommandContext(ctx2, "git", args...)
	out, err := cmd.CombinedOutput()
	// git diff --no-index exits 1 when files differ — that's success for us.
	if err != nil && !(len(args) > 1 && args[0] == "diff" && isExitCode(err, 1)) {
		return string(out), fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

func isExitCode(err error, code int) bool {
	ee, ok := err.(interface{ ExitCode() int })
	return ok && ee.ExitCode() == code
}

// handleGitStatus serves GET /api/git/status — porcelain status parsed into
// structured data (branch, ahead/behind, per-file staged/unstaged letters).
func (s *Server) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	out, err := gitRun(r.Context(), "status", "--porcelain=v1", "-z", "-b")
	if err != nil {
		// Not a repo (or git missing) — the panel shows its empty state.
		writeJSON(w, http.StatusOK, gitStatus{Repo: false})
		return
	}
	st := parseGitPorcelain(out)
	st.Repo = true
	writeJSON(w, http.StatusOK, st)
}

var aheadBehindRe = regexp.MustCompile(`\[ahead (\d+)(?:, behind (\d+))?\]|\[behind (\d+)\]`)

// parseGitPorcelain parses `git status --porcelain=v1 -z -b` output.
func parseGitPorcelain(out string) gitStatus {
	st := gitStatus{Files: []gitFile{}}
	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if rec == "" {
			continue
		}
		if strings.HasPrefix(rec, "## ") {
			header := strings.TrimPrefix(rec, "## ")
			switch {
			case strings.HasPrefix(header, "No commits yet on "):
				st.Branch = strings.TrimPrefix(header, "No commits yet on ")
			case strings.HasPrefix(header, "HEAD "): // detached HEAD
				st.Branch = "HEAD (detached)"
			case strings.Contains(header, "..."):
				st.Branch = header[:strings.Index(header, "...")]
			case strings.Contains(header, " "):
				st.Branch = header[:strings.Index(header, " ")]
			default:
				st.Branch = header
			}
			if m := aheadBehindRe.FindStringSubmatch(header); m != nil {
				fmt.Sscanf(m[1], "%d", &st.Ahead)
				if m[2] != "" {
					fmt.Sscanf(m[2], "%d", &st.Behind)
				}
				if m[3] != "" {
					fmt.Sscanf(m[3], "%d", &st.Behind)
				}
			}
			continue
		}
		if len(rec) < 4 {
			continue
		}
		x, y, path := rec[0], rec[1], rec[3:]
		f := gitFile{Path: path, Untracked: x == '?'}
		if x != ' ' && x != '?' {
			f.Index = string(x)
		}
		if y != ' ' && y != '?' {
			f.Worktree = string(y)
		}
		// Renames emit the new path first, the original path as the next record.
		if x == 'R' || x == 'C' {
			if i+1 < len(recs) {
				f.RenamedFrom = recs[i+1]
				i++
			}
		}
		st.Files = append(st.Files, f)
	}
	return st
}

// handleGitDiff serves GET /api/git/diff?file=<path>&staged=1.
func (s *Server) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	file := strings.TrimSpace(r.URL.Query().Get("file"))
	staged := r.URL.Query().Get("staged") == "1"

	args := []string{"diff"}
	if staged {
		args = append(args, "--cached")
	}
	if file != "" {
		// Untracked files have no diff hunks yet — synthesize one against
		// /dev/null so the panel shows the full new-file content.
		if !staged && fileIsUntracked(r.Context(), file) {
			out, err := gitRun(r.Context(), "diff", "--no-index", "--", "/dev/null", file)
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]any{"diff": "", "untracked": true})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"diff": out, "untracked": true})
			return
		}
		args = append(args, "--", file)
	}
	out, err := gitRun(r.Context(), args...)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"diff": out})
}

func fileIsUntracked(ctx context.Context, file string) bool {
	out, err := gitRun(ctx, "status", "--porcelain=v1", "--", file)
	if err != nil {
		return false
	}
	line := strings.TrimSpace(out)
	return strings.HasPrefix(line, "??")
}

// handleGitStage serves POST /api/git/stage {files: []} (empty = stage all).
func (s *Server) handleGitStage(w http.ResponseWriter, r *http.Request) {
	s.gitMutating(w, r, func(files []string) (string, error) {
		args := []string{"add", "-A", "--"}
		if len(files) > 0 {
			args = append([]string{"add", "--"}, files...)
		}
		return gitRun(r.Context(), args...)
	})
}

// handleGitUnstage serves POST /api/git/unstage {files: []} (empty = reset all).
func (s *Server) handleGitUnstage(w http.ResponseWriter, r *http.Request) {
	s.gitMutating(w, r, func(files []string) (string, error) {
		args := []string{"reset", "--"}
		if len(files) > 0 {
			args = append(args, files...)
		}
		return gitRun(r.Context(), args...)
	})
}

// handleGitCommit serves POST /api/git/commit {message, files?}. With files it
// stages exactly those paths first (selective commit); without them it commits
// what is already staged — matching the panel's checkbox semantics.
func (s *Server) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var req struct {
		Message string   `json:"message"`
		Files   []string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "message is required"})
		return
	}
	if len(req.Files) > 0 {
		if _, err := gitRun(r.Context(), append([]string{"add", "--"}, req.Files...)...); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	out, err := gitRun(r.Context(), "commit", "-m", msg)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": strings.TrimSpace(out)})
}

// handleGitDiscard serves POST /api/git/discard {files} — reverts unstaged
// changes (checkout --) or removes untracked files (clean -f). The frontend
// double-confirms; the backend cannot undo this.
func (s *Server) handleGitDiscard(w http.ResponseWriter, r *http.Request) {
	s.gitMutating(w, r, func(files []string) (string, error) {
		if len(files) == 0 {
			return "", fmt.Errorf("discard needs explicit files")
		}
		var tracked, untracked []string
		for _, f := range files {
			if fileIsUntracked(r.Context(), f) {
				untracked = append(untracked, f)
			} else {
				tracked = append(tracked, f)
			}
		}
		var out string
		if len(tracked) > 0 {
			o, err := gitRun(r.Context(), append([]string{"checkout", "HEAD", "--"}, tracked...)...)
			if err != nil {
				return o, err
			}
			out += o
		}
		if len(untracked) > 0 {
			o, err := gitRun(r.Context(), append([]string{"clean", "-f", "--"}, untracked...)...)
			if err != nil {
				return out + o, err
			}
			out += o
		}
		return out, nil
	})
}

// gitMutating decodes {files: []} and runs fn, returning {ok} or {error}.
func (s *Server) gitMutating(w http.ResponseWriter, r *http.Request, fn func([]string) (string, error)) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var req struct {
		Files []string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	out, err := fn(req.Files)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": strings.TrimSpace(out)})
}

// handleGitBranches serves GET /api/git/branches — local branches + current.
func (s *Server) handleGitBranches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	out, err := gitRun(r.Context(), "branch", "--format=%(HEAD)%00%(refname:short)")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"repo": false, "branches": []any{}})
		return
	}
	type branch struct {
		Name    string `json:"name"`
		Current bool   `json:"current"`
	}
	branches := []branch{}
	current := ""
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		parts := strings.SplitN(line, "\x00", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue
		}
		b := branch{Name: parts[1], Current: parts[0] == "*"}
		if b.Current {
			current = b.Name
		}
		branches = append(branches, b)
	}
	writeJSON(w, http.StatusOK, map[string]any{"repo": true, "current": current, "branches": branches})
}

// handleGitCheckout serves POST /api/git/checkout {branch} or {new, from}.
func (s *Server) handleGitCheckout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var req struct {
		Branch string `json:"branch"`
		New    string `json:"new"`
		From   string `json:"from"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	var out string
	var err error
	switch {
	case strings.TrimSpace(req.New) != "":
		args := []string{"checkout", "-b", strings.TrimSpace(req.New)}
		if strings.TrimSpace(req.From) != "" {
			args = append(args, strings.TrimSpace(req.From))
		}
		out, err = gitRun(r.Context(), args...)
	case strings.TrimSpace(req.Branch) != "":
		out, err = gitRun(r.Context(), "checkout", strings.TrimSpace(req.Branch))
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "branch or new is required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": strings.TrimSpace(out)})
}

// handleGitCommitMessage serves POST /api/git/commit-message {diff} — drafts a
// commit message with the configured cheap model (classifier_model).
func (s *Server) handleGitCommitMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var req struct {
		Diff string `json:"diff"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	msg, err := s.engine.GenerateCommitMessage(ctx, req.Diff)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": msg})
}
