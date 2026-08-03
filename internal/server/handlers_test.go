package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// httpDo performs a request and decodes the response body into out (when
// non-nil), asserting the expected status code.
func httpDo(t *testing.T, method, url, body string, wantStatus int, out any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, url, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s status = %d (want %d): %s", method, url, resp.StatusCode, wantStatus, string(raw))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s %s: %v", method, url, err)
		}
	}
	return resp
}

// isolateHome redirects config.DefaultPath() into a temp dir so handler tests
// that persist config never touch the real ~/.icode/config.yaml. Returns the
// isolated home so the test can assert the written file location.
func isolateHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp) // Windows
	t.Setenv("HOME", tmp)        // POSIX
	return tmp
}

func TestHealthEndpoint(t *testing.T) {
	_, base := newSecurityTestServer(t)
	var body map[string]any
	httpDo(t, http.MethodGet, base+"/api/health", "", http.StatusOK, &body)
	if body["version"] != "test" {
		t.Fatalf("health version = %v, want %q", body["version"], "test")
	}
}

func TestStatusEndpoint(t *testing.T) {
	_, base := newSecurityTestServer(t)
	httpDo(t, http.MethodGet, base+"/api/status", "", http.StatusOK, nil)
}

func TestModelsEndpoint(t *testing.T) {
	_, base := newSecurityTestServer(t)
	var models []map[string]any
	httpDo(t, http.MethodGet, base+"/api/models", "", http.StatusOK, &models)
	found := false
	for _, m := range models {
		if m["id"] == "openrouter/free" {
			found = true
		}
	}
	if !found {
		t.Fatalf("GET /api/models missing openrouter/free, got %d entries", len(models))
	}
}

func TestListProviders(t *testing.T) {
	_, base := newSecurityTestServer(t)
	var providers []map[string]any
	httpDo(t, http.MethodGet, base+"/api/providers", "", http.StatusOK, &providers)
	if len(providers) != 1 || providers[0]["name"] != "openrouter" {
		t.Fatalf("providers = %v, want [{name: openrouter}]", providers)
	}
}

func TestSessionCRUD(t *testing.T) {
	_, base := newSecurityTestServer(t)

	// Create.
	var created map[string]any
	httpDo(t, http.MethodPost, base+"/api/sessions",
		`{"id":"crud-1","title":"CRUD","model_id":"openrouter/free","provider_name":"openrouter"}`, http.StatusCreated, &created)
	if created["id"] != "crud-1" {
		t.Fatalf("created session id = %v, want crud-1", created["id"])
	}

	// List contains it.
	var list []map[string]any
	httpDo(t, http.MethodGet, base+"/api/sessions", "", http.StatusOK, &list)
	if len(list) != 1 || list[0]["id"] != "crud-1" {
		t.Fatalf("session list = %v, want [crud-1]", list)
	}

	// Rename via PUT.
	var renamed map[string]any
	httpDo(t, http.MethodPut, base+"/api/sessions/crud-1",
		`{"title":"Renamed"}`, http.StatusOK, &renamed)
	if renamed["title"] != "Renamed" {
		t.Fatalf("renamed title = %v, want Renamed", renamed["title"])
	}

	// Delete.
	httpDo(t, http.MethodDelete, base+"/api/sessions/crud-1", "", http.StatusOK, nil)
	var after []map[string]any
	httpDo(t, http.MethodGet, base+"/api/sessions", "", http.StatusOK, &after)
	if len(after) != 0 {
		t.Fatalf("session list after delete = %v, want empty", after)
	}
}

func TestSessionByIDNotFound(t *testing.T) {
	_, base := newSecurityTestServer(t)
	httpDo(t, http.MethodGet, base+"/api/sessions/nope", "", http.StatusNotFound, nil)
}

func TestSessionByIDBadMethod(t *testing.T) {
	_, base := newSecurityTestServer(t)
	resp, err := http.Post(base+"/api/sessions/crud-1", "application/json", nil)
	if err != nil {
		t.Fatalf("POST session by id: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("unexpected 200 for POST /api/sessions/{id}")
	}
}

func TestWorkspaceList(t *testing.T) {
	_, base := newSecurityTestServer(t)
	var body map[string]any
	httpDo(t, http.MethodGet, base+"/api/workspaces", "", http.StatusOK, &body)
	if _, ok := body["workspaces"]; !ok {
		t.Fatalf("GET /api/workspaces missing workspaces field: %v", body)
	}
}

func TestConfigPUTPersists(t *testing.T) {
	home := isolateHome(t)
	srv, base := newSecurityTestServer(t)

	httpDo(t, http.MethodPut, base+"/api/config",
		`{
			"language":"en",
			"security_level":"local",
			"defaults":{
				"model":"openrouter/free",
				"provider":"openrouter",
				"mode":"plan",
				"temperature":0.3,
				"max_tokens":512,
				"cache":true
			}
		}`, http.StatusOK, nil)

	// In-memory config updated.
	if srv.cfg.Language != "en" || srv.cfg.Defaults.Mode != "plan" || srv.cfg.Defaults.Temperature != 0.3 {
		t.Fatalf("cfg not updated: lang=%q mode=%q temp=%v", srv.cfg.Language, srv.cfg.Defaults.Mode, srv.cfg.Defaults.Temperature)
	}

	// GET reflects it.
	var got map[string]any
	httpDo(t, http.MethodGet, base+"/api/config", "", http.StatusOK, &got)
	if got["language"] != "en" {
		t.Fatalf("GET /api/config language = %v, want en", got["language"])
	}
	defaults, _ := got["defaults"].(map[string]any)
	if defaults == nil || defaults["mode"] != "plan" {
		t.Fatalf("GET /api/config defaults.mode = %v, want plan", defaults)
	}

	// Persisted to the (isolated) home config file.
	cfgPath := filepath.Join(home, ".icode", "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("expected isolated config file written: %v", err)
	}
	if !bytes.Contains(data, []byte("language: en")) {
		t.Fatalf("persisted config missing language: en:\n%s", string(data))
	}
}

func TestConfigModelLifecycle(t *testing.T) {
	home := isolateHome(t)
	_, base := newSecurityTestServer(t)

	// handleConfigModel upserts on PUT (not POST) and deletes via ?id=.
	httpDo(t, http.MethodPut, base+"/api/config/model",
		`{"provider":"acme","model_id":"m1","name":"Acme M1","custom":true}`, http.StatusOK, nil)

	var listed map[string]any
	httpDo(t, http.MethodGet, base+"/api/config/models", "", http.StatusOK, &listed)
	models, _ := listed["models"].([]any)
	found := false
	for _, raw := range models {
		m, _ := raw.(map[string]any)
		if m["provider"] == "acme" && m["model_id"] == "m1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("custom model not listed: %v", listed)
	}

	httpDo(t, http.MethodDelete, base+"/api/config/model?id=acme/m1", "", http.StatusOK, nil)

	// Deleting a missing model returns 404.
	httpDo(t, http.MethodDelete, base+"/api/config/model?id=acme/m2", "", http.StatusNotFound, nil)

	// The isolated config file was written by the upsert.
	if _, err := os.Stat(filepath.Join(home, ".icode", "config.yaml")); err != nil {
		t.Fatalf("expected isolated config file after model upsert: %v", err)
	}
}

func TestSetPermissionMode(t *testing.T) {
	_, base := newSecurityTestServer(t)
	var body map[string]any
	httpDo(t, http.MethodPost, base+"/api/permission/mode", `{"mode":"yolo"}`, http.StatusOK, &body)
	if body["mode"] != "yolo" {
		t.Fatalf("mode response = %v, want yolo", body["mode"])
	}
}

func TestMCPListEndpoint(t *testing.T) {
	_, base := newSecurityTestServer(t)
	var servers []map[string]any
	httpDo(t, http.MethodGet, base+"/api/mcp", "", http.StatusOK, &servers)
	if servers == nil {
		t.Fatal("GET /api/mcp returned null, want []")
	}
}

func TestSkillsListEndpoint(t *testing.T) {
	_, base := newSecurityTestServer(t)
	var body map[string]any
	httpDo(t, http.MethodGet, base+"/api/skills", "", http.StatusOK, &body)
	if _, ok := body["skills"]; !ok {
		t.Fatalf("GET /api/skills missing skills field: %v", body)
	}
}
