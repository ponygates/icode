package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/types"
)

// TestHotPathAutoRefreshesExpiredGrant builds a provider through Provider()
// the same way the bootstrap does, lets its grant expire, and fires concurrent
// chats at a gateway that rejects the stale token. Every chat must succeed via
// the 401→refresh→replay path, and the rotating per-provider lock must collapse
// the racers into a single token-endpoint round-trip.
func TestHotPathAutoRefreshesExpiredGrant(t *testing.T) {
	isolate(t)

	var refreshPosts int32
	tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshPosts, 1)
		// Slow enough that racing goroutines would each fire without the lock.
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh-access", "refresh_token": "rotated-refresh", "expires_in": 3600,
		})
	}))
	defer tokSrv.Close()

	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh-access" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"token expired"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
		})
	}))
	defer chatSrv.Close()

	setOAuth(t, "vendorx", &config.OAuthCfg{TokenURL: tokSrv.URL + "/t", ClientID: "c"}, &config.OAuthGrant{
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().UTC().Add(-time.Minute).Unix(),
	}, "stale-access")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	pc := cfg.Providers["vendorx"]
	pc.APIBase = chatSrv.URL + "/v1"
	cfg.Providers["vendorx"] = pc
	if err := cfg.Save(config.DefaultPath()); err != nil {
		t.Fatalf("save base: %v", err)
	}

	p := Provider("vendorx", cfg.Providers["vendorx"])

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = p.Chat(context.Background(), types.ChatRequest{
				Model:    "test-model",
				Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("chat %d: %v (an expired subscription token must renew transparently)", i, err)
		}
	}
	if n := atomic.LoadInt32(&refreshPosts); n != 1 {
		t.Fatalf("concurrent expiry must trigger exactly one refresh, saw %d", n)
	}

	// The rotated token must be persisted through the encrypted grant path,
	// so the next process start does not re-login.
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	vx := cfg.Providers["vendorx"]
	if vx.APIKey != "fresh-access" || vx.Grant == nil || vx.Grant.RefreshToken != "rotated-refresh" {
		t.Fatalf("rotated tokens not persisted: apikey=%q grant=%+v", vx.APIKey, vx.Grant)
	}
	if vx.Grant.ExpiresAt <= time.Now().Unix() {
		t.Fatalf("persisted expiry should be in the future: %d", vx.Grant.ExpiresAt)
	}
}

// A provider built for an Anthropic-style vendor with a subscription grant
// must go out as Bearer from the first request (wired at construction from
// the config's Subscription state) — and a plain API key must stay x-api-key.
func TestProviderWiringCredentialKind(t *testing.T) {
	isolate(t)

	var gotAuth, gotKey string
	var sawAuth, sawKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, sawAuth = r.Header["Authorization"]
		gotKey = r.Header.Get("x-api-key")
		_, sawKey = r.Header["X-Api-Key"]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "m", "content": []map[string]any{{"type": "text", "text": "ok"}}, "stop_reason": "end_turn",
		})
	}))
	defer srv.Close()

	chat := func(p types.Provider) {
		t.Helper()
		if _, err := p.Chat(context.Background(), types.ChatRequest{
			Model:    "claude-sonnet-5",
			Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
		}); err != nil {
			t.Fatalf("chat: %v", err)
		}
	}

	// Subscription grant → bearer.
	setOAuth(t, "anthropic", &config.OAuthCfg{TokenURL: "http://127.0.0.1:1/t"}, &config.OAuthGrant{
		RefreshToken: "r",
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Unix(),
	}, "oat-live")
	cfg, _ := config.Load()
	pc := cfg.Providers["anthropic"]
	pc.APIBase = srv.URL
	cfg.Providers["anthropic"] = pc
	if err := cfg.Save(config.DefaultPath()); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg, _ = config.Load()
	chat(Provider("anthropic", cfg.Providers["anthropic"]))
	if !sawAuth || gotAuth != "Bearer oat-live" || sawKey {
		t.Fatalf("subscription grant must send Bearer only: auth=%q key=%q", gotAuth, gotKey)
	}

	// Plain API key → x-api-key, no Authorization (unchanged for the majority path).
	if _, err := Save("anthropic", "sk-ant-api03-live"); err != nil {
		t.Fatalf("save key: %v", err)
	}
	gotAuth, gotKey, sawAuth, sawKey = "", "", false, false
	cfg, _ = config.Load()
	chat(Provider("anthropic", cfg.Providers["anthropic"]))
	if !sawKey || gotKey != "sk-ant-api03-live" || sawAuth {
		t.Fatalf("API key must keep using x-api-key: auth=%q key=%q", gotAuth, gotKey)
	}
}

// The pure API-key hot path must be byte-for-byte the old behavior: a 401 is
// surfaced immediately, with no refresh round-trip and no replay.
func TestHotPathAPIKeyOnly401Unchanged(t *testing.T) {
	isolate(t)

	var chatHits, refreshHits int32
	tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshHits, 1)
	}))
	defer tokSrv.Close()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&chatHits, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer chatSrv.Close()

	if _, err := Save("vendorx", "sk-plain-key"); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg, _ := config.Load()
	pc := cfg.Providers["vendorx"]
	pc.APIBase = chatSrv.URL + "/v1"
	pc.OAuth = &config.OAuthCfg{TokenURL: tokSrv.URL + "/t"} // configured, but no grant
	cfg.Providers["vendorx"] = pc
	if err := cfg.Save(config.DefaultPath()); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg, _ = config.Load()

	p := Provider("vendorx", cfg.Providers["vendorx"])
	if _, err := p.Chat(context.Background(), types.ChatRequest{
		Model:    "test-model",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}); err == nil {
		t.Fatal("expected the 401 to surface for a bad API key")
	}
	if n := atomic.LoadInt32(&chatHits); n != 1 {
		t.Errorf("API-key requests must not be replayed, got %d", n)
	}
	if n := atomic.LoadInt32(&refreshHits); n != 0 {
		t.Errorf("a provider without a grant must never hit the token endpoint, got %d", n)
	}
}
