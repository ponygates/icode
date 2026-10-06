package skills

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateHome redirects the user home (and thus ~/.icode/skills) into a
// temp dir so install tests never touch the real user directory.
func isolateHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// os.UserHomeDir reads USERPROFILE on Windows, HOME elsewhere.
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

// stubGitHub spins up a fake api.github.com + raw.githubusercontent.com pair
// and points the package's base URLs at it. The canned repo "octo/skills"
// (default branch "main") serves:
//
//	skills/
//	  code-review/SKILL.md          (valid frontmatter)
//	  code-review/refs/notes.md     (companion file)
//	  doc-gen/SKILL.md              (valid frontmatter)
//	.claude-plugin/marketplace.json (only for the "with-market" repo)
type stubGitHub struct {
	srv *httptest.Server
}

const stubSKILLMD = `---
name: code-review
description: Reviews code for bugs
category: coding
triggers:
  - review
---
# Code Review
Do the review.
`

const stubNotesMD = `# Notes
companion file body
`

func newStubGitHub(t *testing.T) *stubGitHub {
	t.Helper()
	mux := http.NewServeMux()

	// ── API endpoints ──────────────────────────────────────────
	mux.HandleFunc("/repos/octo/skills", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"default_branch":"main"}`))
	})
	mux.HandleFunc("/repos/octo/skills/contents/.claude-plugin/marketplace.json", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/repos/octo/skills/contents/skills", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"name":"code-review","path":"skills/code-review","type":"dir","download_url":null},
			{"name":"doc-gen","path":"skills/doc-gen","type":"dir","download_url":null}
		]`))
	})
	mux.HandleFunc("/repos/octo/skills/contents/skills/code-review", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"name":"SKILL.md","path":"skills/code-review/SKILL.md","type":"file","download_url":"http://unused/raw/octo/skills/main/skills/code-review/SKILL.md"},
			{"name":"refs","path":"skills/code-review/refs","type":"dir","download_url":null}
		]`))
	})
	mux.HandleFunc("/repos/octo/skills/contents/skills/code-review/refs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"name":"notes.md","path":"skills/code-review/refs/notes.md","type":"file","download_url":null}
		]`))
	})
	mux.HandleFunc("/repos/octo/skills/contents/skills/doc-gen", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"name":"SKILL.md","path":"skills/doc-gen/SKILL.md","type":"file","download_url":null}
		]`))
	})

	// ── raw endpoints ──────────────────────────────────────────
	mux.HandleFunc("/raw/octo/skills/main/skills/code-review/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(stubSKILLMD))
	})
	mux.HandleFunc("/raw/octo/skills/main/skills/code-review/refs/notes.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(stubNotesMD))
	})
	mux.HandleFunc("/raw/octo/skills/main/skills/doc-gen/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("---\nname: doc-gen\ndescription: Generates docs\ncategory: coding\n---\n# Doc Gen\n"))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	oldAPI, oldRaw := ghAPIBase, ghRawBase
	ghAPIBase = srv.URL
	ghRawBase = srv.URL + "/raw"
	t.Cleanup(func() { ghAPIBase, ghRawBase = oldAPI, oldRaw })

	return &stubGitHub{srv: srv}
}

func TestParseSource(t *testing.T) {
	cases := []struct {
		in      string
		owner   string
		repo    string
		branch  string
		subDir  string
		fileURL string
		wantErr bool
	}{
		{in: "octo/skills", owner: "octo", repo: "skills"},
		{in: " https://github.com/octo/skills ", owner: "octo", repo: "skills"},
		{in: "https://github.com/octo/skills/tree/dev/sub/dir", owner: "octo", repo: "skills", branch: "dev", subDir: "sub/dir"},
		{in: "https://example.com/x/SKILL.md", fileURL: "https://example.com/x/SKILL.md"},
		{in: "", wantErr: true},
		{in: "http://insecure.example/x", wantErr: true},
		{in: "a/b/c", wantErr: true},
		{in: "https://github.com/onlyowner", wantErr: true},
	}
	for _, c := range cases {
		gh, fileURL, err := parseSource(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseSource(%q): expected error, got gh=%+v fileURL=%q", c.in, gh, fileURL)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSource(%q): unexpected error: %v", c.in, err)
			continue
		}
		if c.fileURL != "" {
			if fileURL != c.fileURL {
				t.Errorf("parseSource(%q): fileURL=%q, want %q", c.in, fileURL, c.fileURL)
			}
			continue
		}
		if gh == nil || gh.owner != c.owner || gh.repo != c.repo || gh.branch != c.branch || gh.subDir != c.subDir {
			t.Errorf("parseSource(%q): got %+v, want owner=%s repo=%s branch=%s subDir=%s",
				c.in, gh, c.owner, c.repo, c.branch, c.subDir)
		}
	}
}

func TestListFromSource_SkillsDir(t *testing.T) {
	isolateHome(t)
	newStubGitHub(t)

	list, err := ListFromSource("octo/skills")
	if err != nil {
		t.Fatalf("ListFromSource: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 skills, got %d: %+v", len(list), list)
	}
	byName := map[string]RemoteSkill{}
	for _, s := range list {
		byName[s.Name] = s
	}
	cr, ok := byName["code-review"]
	if !ok {
		t.Fatalf("code-review missing from list: %+v", list)
	}
	if cr.Description != "Reviews code for bugs" {
		t.Errorf("description = %q, want %q", cr.Description, "Reviews code for bugs")
	}
	if cr.Category != "coding" {
		t.Errorf("category = %q, want coding", cr.Category)
	}
	if cr.Path != "skills/code-review" {
		t.Errorf("path = %q, want skills/code-review", cr.Path)
	}
	if cr.Installed {
		t.Error("code-review should not be installed yet")
	}
	if _, ok := byName["doc-gen"]; !ok {
		t.Errorf("doc-gen missing from list: %+v", list)
	}
}

func TestInstallFromSource_Repo(t *testing.T) {
	home := isolateHome(t)
	newStubGitHub(t)

	name, err := InstallFromSource("octo/skills", "skills/code-review")
	if err != nil {
		t.Fatalf("InstallFromSource: %v", err)
	}
	if name != "code-review" {
		t.Fatalf("installed name = %q, want code-review", name)
	}
	// SKILL.md must exist with the downloaded content…
	skillPath := filepath.Join(home, ".icode", "skills", "code-review", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("SKILL.md not installed: %v", err)
	}
	if !strings.Contains(string(data), "# Code Review") {
		t.Errorf("SKILL.md content mismatch: %q", string(data))
	}
	// …and the companion file must come along.
	notes, err := os.ReadFile(filepath.Join(home, ".icode", "skills", "code-review", "refs", "notes.md"))
	if err != nil {
		t.Fatalf("companion file not installed: %v", err)
	}
	if !strings.Contains(string(notes), "companion file body") {
		t.Errorf("notes.md content mismatch: %q", string(notes))
	}
	if !IsInstalled("code-review") {
		t.Error("IsInstalled(code-review) = false after install")
	}
	// Re-listing should now mark it installed.
	list, err := ListFromSource("octo/skills")
	if err != nil {
		t.Fatalf("re-list: %v", err)
	}
	for _, s := range list {
		if s.Name == "code-review" && !s.Installed {
			t.Error("code-review not marked installed after install")
		}
	}
}

// stubTLS serves a single SKILL.md over https (parseSource rejects plain
// http URLs, so direct-URL tests must use NewTLSServer) and swaps the
// package client for one that trusts its self-signed cert.
func stubTLS(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	oldClient := remoteClient
	remoteClient = srv.Client()
	t.Cleanup(func() { remoteClient = oldClient })
	return srv.URL + "/x/SKILL.md"
}

func TestInstallFromURL(t *testing.T) {
	home := isolateHome(t)
	url := stubTLS(t, "---\nname: url-skill\ndescription: From a URL\n---\n# URL Skill\n")

	name, err := InstallFromSource(url, "")
	if err != nil {
		t.Fatalf("InstallFromSource(URL): %v", err)
	}
	if name != "url-skill" {
		t.Fatalf("installed name = %q, want url-skill", name)
	}
	data, err := os.ReadFile(filepath.Join(home, ".icode", "skills", "url-skill", "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md not installed: %v", err)
	}
	if !strings.Contains(string(data), "# URL Skill") {
		t.Errorf("SKILL.md content mismatch: %q", string(data))
	}
}

func TestInstallFromURL_RejectsNonSkill(t *testing.T) {
	isolateHome(t)
	url := stubTLS(t, "just some markdown, no frontmatter name")

	if _, err := InstallFromSource(url, ""); err == nil {
		t.Fatal("expected error for non-skill URL, got nil")
	}
}

func TestSafeJoin(t *testing.T) {
	if _, err := safeJoin(`C:\dest`, "../evil"); err == nil {
		t.Error("safeJoin must reject ../ traversal")
	}
	if _, err := safeJoin(`C:\dest`, "a/../../evil"); err == nil {
		t.Error("safeJoin must reject nested traversal")
	}
	if _, err := safeJoin(`C:\dest`, ""); err == nil {
		t.Error("safeJoin must reject empty rel")
	}
	got, err := safeJoin(`C:\dest`, "refs/notes.md")
	if err != nil {
		t.Fatalf("safeJoin(normal) failed: %v", err)
	}
	want := filepath.Join(`C:\dest`, "refs", "notes.md")
	if got != want {
		t.Errorf("safeJoin = %q, want %q", got, want)
	}
	// Backslash variants must not smuggle a traversal through.
	if _, err := safeJoin(`C:\dest`, `..\evil`); err == nil {
		t.Error("safeJoin must reject backslash traversal")
	}
}
