package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/conversation"
	"github.com/ponygates/icode/internal/db"
)

func newSecurityTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	// Redirect TMP/TEMP: this factory boots a real server, and
	// Server.Start() would otherwise overwrite the production
	// %TEMP%\icode\port discovery file. HOME stays untouched — tests that
	// assert HOME-derived config paths do their own isolateHome.
	redirectTemp(t)

	store, err := db.New(db.Config{Path: fmt.Sprintf("file::memory:?cache=shared&_conn=%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	reg := &fakeRegistry{p: &fakeProvider{name: "openrouter"}}
	engine := conversation.NewEngine(reg, store, nil)
	cfg := config.Default()

	srv := New(ServerConfig{Config: cfg, Registry: reg, Store: store, DB: store, Engine: engine, Version: "test", Port: 0})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	port, err := srv.Start(ctx)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	// Register the per-launch token so the shared httpDo helper can present
	// it on privileged mutating endpoints (shell/config/permission/update).
	testTokens.Store(base, srv.APIToken())
	return srv, base
}

// Cross-origin state-changing requests (CSRF) must be rejected with 403.
func TestCSRFBlockedForCrossOriginMutations(t *testing.T) {
	_, base := newSecurityTestServer(t)

	req, _ := http.NewRequest("POST", base+"/api/permission/mode", strings.NewReader(`{"mode":"yolo"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example") // foreign cross-site page

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin mutation status = %d, want 403", resp.StatusCode)
	}
}

// Same-origin mutations must keep working (legit desktop renderer).
func TestSameOriginMutationAllowed(t *testing.T) {
	srv, base := newSecurityTestServer(t)

	req, _ := http.NewRequest("POST", base+"/api/permission/mode", strings.NewReader(`{"mode":"yolo"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", srv.serverOrigin())
	// The privileged mutating endpoints also require the per-launch Bearer
	// token even from loopback.
	req.Header.Set("Authorization", "Bearer "+srv.APIToken())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("same-origin mutation status = %d, want 200", resp.StatusCode)
	}
}

// CORS must not be reflected for a foreign origin.
func TestCORSNotReflectedForForeignOrigin(t *testing.T) {
	_, base := newSecurityTestServer(t)

	req, _ := http.NewRequest("GET", base+"/api/health", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO reflected for foreign origin = %q, want empty", got)
	}
}

// The multimodal API key must never be returned by GET /api/config.
func TestMultimodalKeyNotLeaked(t *testing.T) {
	srv, base := newSecurityTestServer(t)
	srv.cfg.Multimodal.APIKey = "super-secret-multimodal-key"

	resp, err := http.Get(base + "/api/config")
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "super-secret-multimodal-key") {
		t.Fatal("GET /api/config leaked the multimodal API key")
	}
}

// Privileged mutating endpoints must demand the per-launch Bearer token even
// from loopback: any local process can otherwise drive arbitrary command
// execution (/api/shell) or rewrite provider credentials / permission state
// without the user's desktop ever being involved.
func TestPrivilegedEndpointsRequireTokenOnLoopback(t *testing.T) {
	_, base := newSecurityTestServer(t)

	endpoints := []struct{ method, path, body string }{
		{"POST", "/api/shell", `{"cmd":"echo hi"}`},
		{"PUT", "/api/config", `{"language":"en"}`},
		{"POST", "/api/config/reset", `{}`},
		{"POST", "/api/config/key", `{"provider":"demo","api_key":"x"}`},
		{"PUT", "/api/config/model", `{"model_id":"m","provider":"demo"}`},
		{"PUT", "/api/config/provider", `{"name":"demo"}`},
		{"POST", "/api/permission/mode", `{"mode":"yolo"}`},
		{"POST", "/api/permission/allow-tool", `{"session_id":"s","tool":"bash"}`},
		{"POST", "/api/permission/respond", `{"request_id":"r","decision":"allow"}`},
		{"POST", "/api/permission/session-allow", `{"session_id":"s","allow":true}`},
		{"POST", "/api/update/apply", `{}`},
		{"POST", "/api/update/restart", `{}`},
	}
	for _, ep := range endpoints {
		req, err := http.NewRequest(ep.method, base+ep.path, strings.NewReader(ep.body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", ep.method, ep.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without token = %d, want 401", ep.method, ep.path, resp.StatusCode)
		}
	}
}

// With the token presented, the desktop flow keeps working: POST /api/shell
// executes and returns output (this is the `!` shortcut path).
func TestShellAllowedWithToken(t *testing.T) {
	srv, base := newSecurityTestServer(t)

	req, _ := http.NewRequest("POST", base+"/api/shell", strings.NewReader(`{"cmd":"echo icode-shell-ok"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+srv.APIToken())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("shell: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("shell with token status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		OK     bool   `json:"ok"`
		Output string `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode shell response: %v", err)
	}
	if !body.OK || !strings.Contains(body.Output, "icode-shell-ok") {
		t.Fatalf("shell output = %+v, want echo output", body)
	}
}

// Voice credentials must never be echoed by GET /api/config (VoiceCfg's
// MarshalJSON hides them); the multimodal key has the same guard above.
func TestVoiceKeysNotLeaked(t *testing.T) {
	srv, base := newSecurityTestServer(t)
	srv.cfg.Voice.BaiduAPIKey = "super-secret-baidu-key"
	srv.cfg.Voice.BaiduSecretKey = "super-secret-baidu-secret"
	srv.cfg.Voice.IFlytekAPIKey = "super-secret-xfyun-key"
	srv.cfg.Voice.IFlytekAPISecret = "super-secret-xfyun-secret"

	resp, err := http.Get(base + "/api/config")
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	for _, secret := range []string{
		"super-secret-baidu-key", "super-secret-baidu-secret",
		"super-secret-xfyun-key", "super-secret-xfyun-secret",
	} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("GET /api/config leaked voice credential %q", secret)
		}
	}
}
