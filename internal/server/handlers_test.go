package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ponygates/icode/internal/db"
	"github.com/ponygates/icode/internal/types"
)

// testTokens maps each test server's base URL to its per-launch API token so
// the shared httpDo helper can present the Bearer token required by the
// privileged mutating endpoints (shell/config/permission/update). Registered
// by the test server factories; read on every httpDo call.
var testTokens sync.Map // string base URL -> string token

// tokenForURL returns the registered token for a test server base URL, if any.
func tokenForURL(url string) (string, bool) {
	var tok string
	testTokens.Range(func(k, v any) bool {
		if strings.HasPrefix(url, k.(string)) {
			tok, _ = v.(string)
			return false
		}
		return true
	})
	return tok, tok != ""
}

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
	if tok, ok := tokenForURL(url); ok {
		req.Header.Set("Authorization", "Bearer "+tok)
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
	// TMP/TEMP too: os.TempDir() prefers them over USERPROFILE, and
	// Server.Start() publishes the discovery port file (port + Bearer token)
	// into os.TempDir()/icode/. Without this redirect every test that boots
	// a server overwrites the REAL %TEMP%\icode\port that production
	// desktop / VS Code clients read — a flaky "VS Code can't find the
	// backend" failure mode that is nearly impossible to reproduce.
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	return tmp
}

// redirectTemp points TMP/TEMP at a per-test scratch dir without touching
// HOME — used by the test server factories so Server.Start() cannot
// overwrite the production %TEMP%\icode\port discovery file (os.TempDir()
// prefers TMP/TEMP over USERPROFILE). Separate from isolateHome because
// tests that assert HOME-derived config paths call isolateHome themselves;
// a factory must not clobber their HOME redirect, so the two helpers are
// designed to compose in either order.
func redirectTemp(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
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

// TestWorkspaceCreateReturnsTheStoredRow pins the created workspace's identity:
// the response must carry the generated id, not null, so the client can address
// the workspace it just made.
func TestWorkspaceCreateReturnsTheStoredRow(t *testing.T) {
	_, base := newSecurityTestServer(t)
	var created db.Workspace
	httpDo(t, http.MethodPost, base+"/api/workspaces",
		`{"name":"结算区","path":"/tmp/ws-test"}`, http.StatusCreated, &created)
	if created.ID == "" {
		t.Fatalf("POST /api/workspaces returned no id: %+v", created)
	}
	if created.Name != "结算区" || created.Path != "/tmp/ws-test" {
		t.Errorf("created = %+v, want the submitted name/path", created)
	}
	if created.SessionIDs == nil {
		t.Error("created.SessionIDs = nil, want an empty slice in the response")
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Errorf("created timestamps missing: %+v", created)
	}

	var list map[string]any
	httpDo(t, http.MethodGet, base+"/api/workspaces", "", http.StatusOK, &list)
	rows, _ := list["workspaces"].([]any)
	for _, raw := range rows {
		if m, _ := raw.(map[string]any); m != nil && m["id"] == created.ID {
			return
		}
	}
	t.Fatalf("created workspace %q not present in GET listing: %v", created.ID, list)
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

// TestSessionImportRoundTrip validates that a session exported as JSON via
// GET /api/sessions/{id} can be re-imported with a fresh identity and its
// messages restored intact.
func TestSessionImportRoundTrip(t *testing.T) {
	srv, base := newSecurityTestServer(t)

	// Seed a session with a couple of messages directly through the store.
	sess := &types.Session{Title: "seed", ModelID: "m1", ProviderName: "openrouter"}
	if err := srv.Store().Create(sess); err != nil {
		t.Fatalf("create seed session: %v", err)
	}
	msgs := []types.Message{
		{ID: "m1", Role: "user", Content: "hello", Timestamp: time.Now()},
		{ID: "m2", Role: "assistant", Content: "hi there", Timestamp: time.Now()},
	}
	for _, m := range msgs {
		if err := srv.Store().AppendMessage(sess.ID, m); err != nil {
			t.Fatalf("append message: %v", err)
		}
	}

	// Export it.
	var exported types.Session
	httpDo(t, http.MethodGet, base+"/api/sessions/"+sess.ID, "", http.StatusOK, &exported)
	if len(exported.Messages) != 2 {
		t.Fatalf("exported messages = %d, want 2", len(exported.Messages))
	}

	// Reset state so the imported copy must stand alone (no shared identity).
	exported.ID = ""

	// Import it back.
	raw, err := json.Marshal(exported)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	var imp map[string]any
	httpDo(t, http.MethodPost, base+"/api/sessions/import", string(raw), http.StatusOK, &imp)
	impSess, ok := imp["session"].(map[string]any)
	if !ok {
		t.Fatalf("import response missing session: %v", imp)
	}
	newID, _ := impSess["id"].(string)
	if newID == "" || newID == sess.ID {
		t.Fatalf("imported session id = %q (want fresh)", newID)
	}

	// Verify the imported copy round-trips the messages.
	var got types.Session
	httpDo(t, http.MethodGet, base+"/api/sessions/"+newID, "", http.StatusOK, &got)
	if len(got.Messages) != 2 {
		t.Fatalf("re-imported messages = %d, want 2", len(got.Messages))
	}
	if got.Messages[1].Content != "hi there" {
		t.Fatalf("re-imported message content = %q", got.Messages[1].Content)
	}
}

func TestSessionImportRejectsBadJSON(t *testing.T) {
	_, base := newSecurityTestServer(t)
	httpDo(t, http.MethodPost, base+"/api/sessions/import", `{not json`, http.StatusBadRequest, nil)
}

// TestSessionMessageEndpoints verifies the append/update/delete/clear message
// endpoints that let desktop-local helpers share the same persisted history.
func TestSessionMessageEndpoints(t *testing.T) {
	srv, base := newSecurityTestServer(t)
	sess := &types.Session{Title: "msg", ModelID: "m1", ProviderName: "p"}
	if err := srv.Store().Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Append two messages.
	var post struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}
	httpDo(t, http.MethodPost, base+"/api/sessions/"+sess.ID+"/messages",
		`{"id":"a1","role":"user","content":"hello"}`, http.StatusOK, &post)
	if post.ID != "a1" {
		t.Fatalf("appended id = %q, want a1", post.ID)
	}
	httpDo(t, http.MethodPost, base+"/api/sessions/"+sess.ID+"/messages",
		`{"id":"a2","role":"assistant","content":"hi"}`, http.StatusOK, nil)

	var got types.Session
	httpDo(t, http.MethodGet, base+"/api/sessions/"+sess.ID, "", http.StatusOK, &got)
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(got.Messages))
	}

	// Update a message in place (shell result arrives after the placeholder).
	httpDo(t, http.MethodPut, base+"/api/sessions/"+sess.ID+"/messages/a1",
		`{"role":"user","content":"hello updated"}`, http.StatusOK, nil)
	httpDo(t, http.MethodGet, base+"/api/sessions/"+sess.ID, "", http.StatusOK, &got)
	if got.Messages[0].Content != "hello updated" {
		t.Fatalf("updated content = %q", got.Messages[0].Content)
	}

	// Delete one message.
	httpDo(t, http.MethodDelete, base+"/api/sessions/"+sess.ID+"/messages/a2", "", http.StatusOK, nil)
	httpDo(t, http.MethodGet, base+"/api/sessions/"+sess.ID, "", http.StatusOK, &got)
	if len(got.Messages) != 1 {
		t.Fatalf("after delete messages = %d, want 1", len(got.Messages))
	}

	// Clear all messages, keeping the session shell.
	httpDo(t, http.MethodPost, base+"/api/sessions/"+sess.ID+"/clear", "", http.StatusOK, nil)
	httpDo(t, http.MethodGet, base+"/api/sessions/"+sess.ID, "", http.StatusOK, &got)
	if len(got.Messages) != 0 {
		t.Fatalf("after clear messages = %d, want 0", len(got.Messages))
	}
}

func TestSessionMessageAppendRejectsInvalid(t *testing.T) {
	_, base := newSecurityTestServer(t)
	httpDo(t, http.MethodPost, base+"/api/sessions/x/messages", `{}`, http.StatusBadRequest, nil)
}
