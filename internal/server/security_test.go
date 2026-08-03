package server

import (
	"context"
	"encoding/json"
	"fmt"
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
	return srv, fmt.Sprintf("http://127.0.0.1:%d", port)
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
