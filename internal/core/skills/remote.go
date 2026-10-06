// Package skills — remote skill sources (marketplace v1).
//
// InstallFromSource / ListFromSource let the market UI install skills from a
// GitHub repository or a direct https URL, compatible with the Claude Code
// ecosystem conventions so community skills work out of the box:
//
//   - owner/repo or https://github.com/owner/repo
//   - https://github.com/owner/repo/tree/<branch>/<subdir>
//   - https://anywhere.example/skill/SKILL.md (direct file URL)
//
// Repo discovery order (first hit wins):
//
//   1. .claude-plugin/marketplace.json — the Claude plugin-market manifest;
//      plugins whose source points inside the repo are listed.
//   2. skills/ directory — one sub-directory per skill (the most common
//      community layout, mirrors iCode's own ~/.icode/skills layout).
//   3. repository root — any top-level <dir>/SKILL.md.
//
// Downloads go through the GitHub contents API for listings and the returned
// download_url (raw.githubusercontent.com) for file bodies. Defensive limits
// (file count, per-file and total size) keep a huge or malicious repo from
// filling the user's disk.
package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// GitHub endpoints — variables (not consts) so tests can point them at a
// local httptest server instead of the real API.
var (
	ghAPIBase = "https://api.github.com"
	ghRawBase = "https://raw.githubusercontent.com"
)

// Remote download guard rails.
const (
	remoteHTTPTimeout   = 15 * time.Second
	remoteMaxFiles      = 50       // per skill directory
	remoteMaxFileBytes  = 1 << 20  // 1 MiB per file
	remoteMaxTotalBytes = 10 << 20 // 10 MiB per skill
)

// RemoteSkill describes one installable skill inside a remote source.
type RemoteSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Path        string `json:"path"`      // location inside the repo ("" for direct URLs)
	Installed   bool   `json:"installed"` // already present in ~/.icode/skills
}

// ghSource is a normalized GitHub repository reference.
type ghSource struct {
	owner  string
	repo   string
	branch string // default branch once resolved ("" until then)
	subDir string // optional skill root inside the repo
}

// claudeMarketplace is the subset of .claude-plugin/marketplace.json we read.
type claudeMarketplace struct {
	Plugins []struct {
		Name        string `json:"name"`
		Source      string `json:"source"` // "./skills/foo" — repo-relative
		Description string `json:"description"`
	} `json:"plugins"`
}

// ghEntry is one entry of a GitHub contents-API directory listing.
type ghEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"` // "file" | "dir"
	DownloadURL string `json:"download_url"`
}

var remoteClient = &http.Client{Timeout: remoteHTTPTimeout}

// ListFromSource probes a remote source and returns the installable skills
// it advertises, annotated with local install state.
func ListFromSource(source string) ([]RemoteSkill, error) {
	gh, fileURL, err := parseSource(source)
	if err != nil {
		return nil, err
	}
	if fileURL != "" {
		return listFromURL(fileURL)
	}
	if err := gh.resolveBranch(); err != nil {
		return nil, err
	}
	return gh.listSkills()
}

// InstallFromSource downloads a skill from a remote source into the user's
// skills directory. For GitHub sources, skillPath is the repo-internal path
// returned by ListFromSource; for direct file URLs skillPath is ignored.
func InstallFromSource(source, skillPath string) (string, error) {
	gh, fileURL, err := parseSource(source)
	if err != nil {
		return "", err
	}
	if fileURL != "" {
		return installFromURL(fileURL)
	}
	if err := gh.resolveBranch(); err != nil {
		return "", err
	}
	if skillPath == "" {
		return "", errors.New("skill path required for repo sources (call list first)")
	}
	return gh.installSkill(skillPath)
}

// ── source parsing ─────────────────────────────────────────────

// parseSource normalizes user input into a GitHub repo reference or a direct
// file URL. Supported forms:
//
//	owner/repo
//	https://github.com/owner/repo
//	https://github.com/owner/repo/tree/<branch>/<subdir>
//	https://host/.../SKILL.md
func parseSource(source string) (gh *ghSource, fileURL string, err error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, "", errors.New("source required")
	}
	if !strings.Contains(source, "://") {
		parts := strings.Split(strings.Trim(source, "/"), "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return &ghSource{owner: parts[0], repo: parts[1]}, "", nil
		}
		return nil, "", fmt.Errorf("unrecognized source %q: use owner/repo or an https URL", source)
	}
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, "", errors.New("source must be an https URL or owner/repo")
	}
	if u.Host == "github.com" || u.Host == "www.github.com" {
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) < 2 || segs[0] == "" || segs[1] == "" {
			return nil, "", fmt.Errorf("unrecognized GitHub URL %q", source)
		}
		src := &ghSource{owner: segs[0], repo: strings.TrimSuffix(segs[1], ".git")}
		if len(segs) >= 4 && segs[2] == "tree" {
			src.branch = segs[3]
			src.subDir = strings.Join(segs[4:], "/")
		}
		return src, "", nil
	}
	// Anything else is treated as a direct file URL.
	return nil, source, nil
}

// ── GitHub API helpers ─────────────────────────────────────────

// apiBase returns the contents-API root for this repo.
func (g *ghSource) apiBase() string {
	return ghAPIBase + "/repos/" + g.owner + "/" + g.repo
}

// resolveBranch fills in the default branch (unless the URL already pinned
// one) so listings can use a stable ref.
func (g *ghSource) resolveBranch() error {
	if g.branch != "" {
		return nil
	}
	body, status, err := remoteGetJSON(g.apiBase())
	if err != nil {
		return fmt.Errorf("GitHub API unreachable: %w", err)
	}
	if status == http.StatusNotFound {
		return fmt.Errorf("repo %s/%s not found", g.owner, g.repo)
	}
	if status != http.StatusOK {
		return fmt.Errorf("GitHub API error (status %d)", status)
	}
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(body, &repo); err != nil || repo.DefaultBranch == "" {
		return errors.New("cannot determine default branch")
	}
	g.branch = repo.DefaultBranch
	return nil
}

// listDir returns the contents-API listing of dirPath ("" = repo root).
// A 404 yields (nil, nil) so callers can fall through to the next layout.
func (g *ghSource) listDir(dirPath string) ([]ghEntry, error) {
	u := g.apiBase() + "/contents/" + dirPath + "?ref=" + url.PathEscape(g.branch)
	body, status, err := remoteGetJSON(u)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusNotFound:
		return nil, nil
	case status != http.StatusOK:
		return nil, fmt.Errorf("GitHub API error listing %q (status %d)", dirPath, status)
	}
	var entries []ghEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("bad listing for %q: %w", dirPath, err)
	}
	return entries, nil
}

// listSkills discovers installable skills in the repo:
// marketplace.json first, then skills/, then root-level <dir>/SKILL.md.
func (g *ghSource) listSkills() ([]RemoteSkill, error) {
	if skills, ok, err := g.listFromMarketplace(); err == nil && ok {
		return skills, nil
	}
	if skills, ok, err := g.listFromDir("skills"); err == nil && ok {
		return skills, nil
	}
	if g.subDir != "" {
		// A tree URL pins the skill root — that directory is the skill.
		skills, ok, err := g.listFromDir(g.subDir)
		if err == nil && ok {
			return skills, nil
		}
	}
	// Fallback: top-level directories containing SKILL.md.
	skills, ok, err := g.listFromDir("")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no skills found in %s/%s (no marketplace.json, no skills/ dir, no root-level SKILL.md)", g.owner, g.repo)
	}
	return skills, nil
}

// listFromMarketplace reads .claude-plugin/marketplace.json and lists the
// plugins that point inside this repo. ok=false when there is no manifest or
// no in-repo plugins (fall through to directory scanning).
func (g *ghSource) listFromMarketplace() (out []RemoteSkill, ok bool, err error) {
	u := g.apiBase() + "/contents/.claude-plugin/marketplace.json?ref=" + url.PathEscape(g.branch)
	body, status, err := remoteGetJSON(u)
	if err != nil || status != http.StatusOK {
		return nil, false, err
	}
	var mp claudeMarketplace
	if err := json.Unmarshal(body, &mp); err != nil {
		return nil, false, nil // unreadable manifest → fall through
	}
	for _, p := range mp.Plugins {
		pth := strings.TrimPrefix(strings.TrimSpace(p.Source), "./")
		if pth == "" || strings.Contains(pth, "://") || strings.Contains(pth, ":") {
			continue // external sources not supported in v1
		}
		// Only list plugins whose directory actually exists in this repo.
		entries, err := g.listDir(pth)
		if err != nil || !hasSkillMD(entries) {
			continue
		}
		out = append(out, RemoteSkill{
			Name:        p.Name,
			Description: p.Description,
			Path:        pth,
			Installed:   IsInstalled(p.Name),
		})
	}
	return out, len(out) > 0, nil
}

// listFromDir treats dirPath as a skills directory: every sub-directory with
// a SKILL.md becomes one RemoteSkill. With dirPath == "" the repo root is
// scanned (fallback layout).
func (g *ghSource) listFromDir(dirPath string) (out []RemoteSkill, ok bool, err error) {
	entries, err := g.listDir(dirPath)
	if err != nil {
		return nil, false, err
	}
	for _, ent := range entries {
		if ent.Type != "dir" || !isSkillDirName(ent.Name) {
			continue
		}
		skillDir := ent.Path
		if dirPath == "" {
			skillDir = ent.Name // root listing paths already include the dir
		}
		sub, err := g.listDir(skillDir)
		if err != nil || !hasSkillMD(sub) {
			continue
		}
		name := ent.Name
		desc, cat := g.peekSkillMeta(skillDir)
		out = append(out, RemoteSkill{
			Name:        name,
			Description: desc,
			Category:    cat,
			Path:        skillDir,
			Installed:   IsInstalled(name),
		})
	}
	return out, len(out) > 0, nil
}

// peekSkillMeta downloads <skillDir>/SKILL.md and extracts description +
// category for list rendering. Failures yield empty strings, not errors.
func (g *ghSource) peekSkillMeta(skillDir string) (desc, cat string) {
	data, err := g.fetchRaw(path.Join(skillDir, "SKILL.md"))
	if err != nil {
		return "", ""
	}
	s := parseSkill(path.Base(skillDir), skillDir+"/SKILL.md", string(data))
	if s == nil {
		return "", ""
	}
	return s.Description, s.Category
}

// hasSkillMD reports whether a directory listing contains SKILL.md.
func hasSkillMD(entries []ghEntry) bool {
	for _, e := range entries {
		if e.Type == "file" && e.Name == "SKILL.md" {
			return true
		}
	}
	return false
}

// isSkillDirName filters obvious non-skill directories when scanning.
func isSkillDirName(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".github", "docs", "doc", "examples", "test", "tests", "node_modules", "vendor", ".claude-plugin":
		return false
	}
	return true
}

// ── install (GitHub) ───────────────────────────────────────────

// installSkill downloads the skill rooted at skillPath (a directory, or a
// SKILL.md file) into ~/.icode/skills/<name>/.
func (g *ghSource) installSkill(skillPath string) (string, error) {
	// skillPath may point at the SKILL.md itself.
	dir := skillPath
	if strings.EqualFold(path.Base(skillPath), "SKILL.md") {
		dir = path.Dir(skillPath)
	}
	// collectFiles returns paths RELATIVE to dir, so the root SKILL.md is
	// plain "SKILL.md" and joining can never duplicate the directory prefix.
	files, err := g.collectFiles(dir)
	if err != nil {
		return "", err
	}
	var hasRootSkillMD bool
	for _, rel := range files {
		if rel == "SKILL.md" {
			hasRootSkillMD = true
			break
		}
	}
	if !hasRootSkillMD {
		return "", fmt.Errorf("no SKILL.md under %q", skillPath)
	}
	// Validate the skill and resolve its name from frontmatter.
	mdData, err := g.fetchRaw(path.Join(dir, "SKILL.md"))
	if err != nil {
		return "", err
	}
	base := path.Base(dir)
	if base == "." || base == "/" {
		base = g.repo
	}
	s := parseSkill(base, dir+"/SKILL.md", string(mdData))
	if s == nil || s.Name == "" {
		return "", errors.New("invalid skill: missing name in frontmatter")
	}
	dest := userSkillsDir()
	if dest == "" {
		return "", errors.New("cannot resolve user skills dir")
	}
	destRoot := filepath.Join(dest, s.Name)
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return "", err
	}
	total := 0
	for _, rel := range files {
		clean, err := safeJoin(destRoot, rel)
		if err != nil {
			return "", err
		}
		if rel == "SKILL.md" {
			if err := os.WriteFile(clean, mdData, 0o644); err != nil {
				return "", err
			}
			total += len(mdData)
			continue
		}
		data, err := g.fetchRaw(path.Join(dir, rel))
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(clean, data, 0o644); err != nil {
			return "", err
		}
		total += len(data)
		if total > remoteMaxTotalBytes {
			return "", fmt.Errorf("skill exceeds %d MiB download budget", remoteMaxTotalBytes>>20)
		}
	}
	return s.Name, nil
}

// collectFiles recursively lists repo files under dirPath (skipping .git and
// friends), returned as paths RELATIVE to dirPath. The file-count guard rail
// applies to the relative names, so the caller can join them onto the local
// destination without re-deriving prefixes.
func (g *ghSource) collectFiles(dirPath string) ([]string, error) {
	var out []string
	prefix := ""
	if dirPath = strings.Trim(dirPath, "/"); dirPath != "" {
		prefix = dirPath + "/"
	}
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := g.listDir(dir)
		if err != nil {
			return err
		}
		if entries == nil {
			return nil // 404 → empty
		}
		for _, e := range entries {
			switch {
			case e.Type == "file":
				out = append(out, strings.TrimPrefix(e.Path, prefix))
				if len(out) > remoteMaxFiles {
					return fmt.Errorf("skill has more than %d files", remoteMaxFiles)
				}
			case e.Type == "dir" && isSkillDirName(e.Name):
				if err := walk(e.Path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(dirPath); err != nil {
		return nil, err
	}
	return out, nil
}

// fetchRaw downloads one repo file via raw.githubusercontent.com. Path
// segments are escaped individually — a whole-path PathEscape would also
// escape the "/" separators and 404 every file.
func (g *ghSource) fetchRaw(repoPath string) ([]byte, error) {
	segs := strings.Split(repoPath, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	u := fmt.Sprintf("%s/%s/%s/%s/%s",
		ghRawBase, g.owner, g.repo, g.branch, strings.Join(segs, "/"))
	data, status, err := remoteGetBytes(u)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("download %q failed (status %d)", repoPath, status)
	}
	if len(data) > remoteMaxFileBytes {
		return nil, fmt.Errorf("file %q exceeds %d KiB limit", repoPath, remoteMaxFileBytes>>10)
	}
	return data, nil
}

// ── direct URL mode ────────────────────────────────────────────

// parseRemoteSkill is parseSkill without the name fallback: remote content
// must carry a real frontmatter name, otherwise any random web page would
// install as a "skill". Passing "" as the fallback name makes that check a
// plain emptiness test.
func parseRemoteSkill(content string) *Skill {
	if !strings.HasPrefix(content, "---") {
		return nil
	}
	s := parseSkill("", "", content)
	if s == nil || s.Name == "" {
		return nil
	}
	return s
}

// listFromURL probes a direct file URL and returns a single-entry list.
func listFromURL(fileURL string) ([]RemoteSkill, error) {
	data, status, err := remoteGetBytes(fileURL)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("download failed (status %d)", status)
	}
	s := parseRemoteSkill(string(data))
	if s == nil {
		return nil, errors.New("URL does not point to a valid SKILL.md (missing frontmatter name)")
	}
	return []RemoteSkill{{
		Name:        s.Name,
		Description: s.Description,
		Category:    s.Category,
		Installed:   IsInstalled(s.Name),
	}}, nil
}

// installFromURL downloads a single SKILL.md and installs it (one download).
func installFromURL(fileURL string) (string, error) {
	data, status, err := remoteGetBytes(fileURL)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("download failed (status %d)", status)
	}
	s := parseRemoteSkill(string(data))
	if s == nil {
		return "", errors.New("URL does not point to a valid SKILL.md (missing frontmatter name)")
	}
	dest := userSkillsDir()
	if dest == "" {
		return "", errors.New("cannot resolve user skills dir")
	}
	target := filepath.Join(dest, s.Name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", err
	}
	return s.Name, nil
}

// ── shared HTTP helpers ────────────────────────────────────────

// remoteGetBytes performs a GET with the market User-Agent and returns the
// body plus status code. Per-file size is enforced here so a hostile URL
// cannot stream an unbounded body into memory.
func remoteGetBytes(u string) ([]byte, int, error) {
	resp, err := remoteClient.Do(remoteReq(u))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, remoteMaxFileBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(data) > remoteMaxFileBytes {
		return nil, resp.StatusCode, fmt.Errorf("response exceeds %d KiB limit", remoteMaxFileBytes>>10)
	}
	return data, resp.StatusCode, nil
}

// remoteGetJSON is remoteGetBytes for API endpoints.
func remoteGetJSON(u string) ([]byte, int, error) {
	return remoteGetBytes(u)
}

// remoteReq builds the outbound request with the required UA header. When
// GITHUB_TOKEN (or GH_TOKEN) is set it is attached to GitHub requests only —
// the unauthenticated API budget is 60 req/h, which a single repo probe with
// a few skills already eats into; a token lifts it to 5000/h.
func remoteReq(u string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "icode-skill-market")
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := githubToken(); tok != "" && isGitHubURL(u) {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req
}

// githubToken reads the optional personal access token from the environment.
func githubToken() string {
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		return tok
	}
	return strings.TrimSpace(os.Getenv("GH_TOKEN"))
}

// isGitHubURL reports whether u targets one of the two GitHub endpoints
// (which accept the token). Third-party URLs must never receive the user's
// GitHub credential. Matching against the (overridable) base variables keeps
// tests — which repoint the bases at a local server — consistent.
func isGitHubURL(u string) bool {
	return strings.HasPrefix(u, ghAPIBase+"/") || strings.HasPrefix(u, ghRawBase+"/")
}

// safeJoin joins a repo-relative path under root, rejecting traversal.
// A ".." segment anywhere in rel is a hard error: silently re-anchoring it
// inside root (what path.Clean("/"+rel) would do) is technically safe but
// hides intent — a hostile path should fail loudly, not install into a
// neighbouring skill's directory.
func safeJoin(root, rel string) (string, error) {
	norm := strings.ReplaceAll(rel, "\\", "/")
	for _, seg := range strings.Split(norm, "/") {
		if seg == ".." {
			return "", fmt.Errorf("unsafe path %q", rel)
		}
	}
	clean := path.Clean(norm)
	if clean == "" || clean == "." || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("unsafe path %q", rel)
	}
	return filepath.Join(root, filepath.FromSlash(clean)), nil
}
