package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

func writeOKMessages(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":          "msg_1",
		"type":        "message",
		"role":        "assistant",
		"model":       "claude-sonnet-5",
		"content":     []map[string]any{{"type": "text", "text": "ok"}},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
	})
}

func testChatRequest() types.ChatRequest {
	return types.ChatRequest{
		Model:    "claude-sonnet-5",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
}

// A subscription grant must send `Authorization: Bearer` and no x-api-key —
// Anthropic's Messages API rejects OAuth tokens carried in the key header.
func TestSubscriptionTokenUsesBearerHeader(t *testing.T) {
	var gotAuth, gotKey, gotVersion string
	var sawAuth, sawKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, sawAuth = r.Header["Authorization"]
		gotKey = r.Header.Get("x-api-key")
		_, sawKey = r.Header["X-Api-Key"]
		gotVersion = r.Header.Get("anthropic-version")
		writeOKMessages(w)
	}))
	defer srv.Close()

	p := New("oat-subscription-token", srv.URL)
	p.SetSubscription(true)
	if _, err := p.Chat(context.Background(), testChatRequest()); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if !sawAuth || gotAuth != "Bearer oat-subscription-token" {
		t.Errorf("expected bearer auth header, got %q (present=%v)", gotAuth, sawAuth)
	}
	if sawKey {
		t.Errorf("x-api-key must not be sent for a subscription token (got %q)", gotKey)
	}
	if gotVersion != AnthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", gotVersion, AnthropicVersion)
	}
}

// The plain API-key path must keep using x-api-key exactly as before.
func TestAPIKeyStillUsesXAPIKeyHeader(t *testing.T) {
	var gotAuth, gotKey string
	var sawAuth, sawKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, sawAuth = r.Header["Authorization"]
		gotKey = r.Header.Get("x-api-key")
		_, sawKey = r.Header["X-Api-Key"]
		writeOKMessages(w)
	}))
	defer srv.Close()

	p := New("sk-ant-api03-livekey", srv.URL)
	if _, err := p.Chat(context.Background(), testChatRequest()); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if !sawKey || gotKey != "sk-ant-api03-livekey" {
		t.Errorf("expected x-api-key header, got %q (present=%v)", gotKey, sawKey)
	}
	if sawAuth {
		t.Errorf("Authorization must not be sent for a plain API key (got %q)", gotAuth)
	}
}

// A provider wired before the OAuth login completed (still in API-key mode)
// must self-heal: 401 → refresh → one replay carrying the bearer token.
func TestChat401RefreshRetriesOnceWithBearer(t *testing.T) {
	var chatHits int32
	var refreshCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&chatHits, 1)
		if r.Header.Get("Authorization") == "Bearer rotated-token" {
			if _, hasKey := r.Header["X-Api-Key"]; hasKey {
				t.Error("retry must not keep the stale x-api-key header")
			}
			writeOKMessages(w)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error"}}`))
	}))
	defer srv.Close()

	p := New("stale-token", srv.URL)
	p.SetTokenRefresher(func(_ context.Context, _ string) (string, bool, error) {
		if atomic.AddInt32(&refreshCalls, 1) == 1 {
			return "rotated-token", true, nil
		}
		return "", false, nil
	})
	if _, err := p.Chat(context.Background(), testChatRequest()); err != nil {
		t.Fatalf("chat after refresh: %v", err)
	}
	if n := atomic.LoadInt32(&chatHits); n != 2 {
		t.Errorf("expected 1 original + 1 retried request, got %d", n)
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Errorf("refresh must fire exactly once, got %d", n)
	}
}

// 401 → refresh → replay → 401 again must end in an error, not a retry loop.
func TestChatDouble401StopsAfterOneRetry(t *testing.T) {
	var chatHits, refreshCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&chatHits, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error"}}`))
	}))
	defer srv.Close()

	p := New("stale-token", srv.URL)
	p.SetTokenRefresher(func(_ context.Context, _ string) (string, bool, error) {
		atomic.AddInt32(&refreshCalls, 1)
		return "yet-another-token", true, nil
	})
	_, err := p.Chat(context.Background(), testChatRequest())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected a 401 error after the failed retry, got %v", err)
	}
	if n := atomic.LoadInt32(&chatHits); n != 2 {
		t.Errorf("must stop after exactly one replay, got %d hits", n)
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Errorf("one replay means one refresh, got %d", n)
	}
}

// An API-key user's 401 must stay a single request: the refresher reports
// "nothing changed", so no replay and no vendor round-trip are wasted.
func TestChat401WithoutChangeDoesNotRetry(t *testing.T) {
	var chatHits, refreshCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&chatHits, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := New("sk-ant-api03-bad", srv.URL)
	p.SetTokenRefresher(func(_ context.Context, sent string) (string, bool, error) {
		atomic.AddInt32(&refreshCalls, 1)
		return sent, false, nil // API-key-only provider: same credential back
	})
	if _, err := p.Chat(context.Background(), testChatRequest()); err == nil {
		t.Fatal("expected the 401 to surface")
	}
	if n := atomic.LoadInt32(&chatHits); n != 1 {
		t.Errorf("a plain API key must not be retried, got %d hits", n)
	}
}

// The streaming path gets the same bounded renewal as Chat.
func TestChatStream401RefreshRetriesOnce(t *testing.T) {
	var chatHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&chatHits, 1)
		if r.Header.Get("Authorization") != "Bearer rotated-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()

	p := New("stale-token", srv.URL)
	p.SetSubscription(true)
	p.SetTokenRefresher(func(_ context.Context, _ string) (string, bool, error) {
		return "rotated-token", true, nil
	})
	ch, err := p.ChatStream(context.Background(), testChatRequest())
	if err != nil {
		t.Fatalf("stream after refresh: %v", err)
	}
	done := false
	for ev := range ch {
		if ev.Type == types.EventError {
			t.Fatalf("unexpected stream error: %s", ev.Content)
		}
		if ev.Type == types.EventDone {
			done = true
		}
	}
	if !done {
		t.Fatal("stream did not complete after the refresh retry")
	}
	if n := atomic.LoadInt32(&chatHits); n != 2 {
		t.Errorf("expected 1 original + 1 retried stream request, got %d", n)
	}
}
