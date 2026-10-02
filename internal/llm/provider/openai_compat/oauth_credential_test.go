package openai_compat

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

func writeCompletion(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":     "1",
		"object": "chat.completion",
		"model":  "test-model",
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role": "assistant", "content": "ok",
			},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
}

func compatChat() types.ChatRequest {
	return types.ChatRequest{
		Model:    "test-model",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
}

// A subscription token on an OpenAI-compatible gateway travels in the one
// Authorization header this transport already uses — it must never also emit
// a second credential header.
func TestSubscriptionTokenSingleAuthHeader(t *testing.T) {
	var authVals []string
	var sawKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authVals = r.Header.Values("Authorization")
		_, sawKey = r.Header["X-Api-Key"]
		writeCompletion(w)
	}))
	defer srv.Close()

	p := New(Config{Name: "gateway", APIKey: "oat-subscription", APIBase: srv.URL + "/v1"})
	p.SetSubscription(true)
	if _, err := p.Chat(context.Background(), compatChat()); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if len(authVals) != 1 || authVals[0] != "Bearer oat-subscription" {
		t.Errorf("expected exactly one bearer header, got %v", authVals)
	}
	if sawKey {
		t.Error("openai-compatible transport must not send x-api-key")
	}
}

// 401 with a rotating refresher: replay exactly once, then a second 401 is
// an error — never a retry loop.
func TestChat401RefreshRetryIsBounded(t *testing.T) {
	var chatHits, refreshCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&chatHits, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid token"}}`))
	}))
	defer srv.Close()

	p := New(Config{Name: "gateway", APIKey: "stale", APIBase: srv.URL + "/v1"})
	p.SetSubscription(true)
	p.SetTokenRefresher(func(_ context.Context, _ string) (string, bool, error) {
		atomic.AddInt32(&refreshCalls, 1)
		return "rotated", true, nil
	})
	_, err := p.Chat(context.Background(), compatChat())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected a 401 after the failed replay, got %v", err)
	}
	if n := atomic.LoadInt32(&chatHits); n != 2 {
		t.Errorf("expected 1 original + 1 replay, got %d", n)
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Errorf("one replay means one refresh, got %d", n)
	}
}

// An API-key-only credential (refresher hands back the same key) must stay a
// single request, preserving the pre-existing failure behavior.
func TestChat401WithoutChangeDoesNotRetry(t *testing.T) {
	var chatHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&chatHits, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := New(Config{Name: "gateway", APIKey: "sk-plain", APIBase: srv.URL + "/v1"})
	p.SetTokenRefresher(func(_ context.Context, sent string) (string, bool, error) {
		return sent, false, nil
	})
	if _, err := p.Chat(context.Background(), compatChat()); err == nil {
		t.Fatal("expected the 401 to surface")
	}
	if n := atomic.LoadInt32(&chatHits); n != 1 {
		t.Errorf("a plain API key must not be replayed, got %d hits", n)
	}
}

// The streaming path renews and replays once, before any SSE bytes are read.
func TestChatStream401RefreshRetriesOnce(t *testing.T) {
	var chatHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&chatHits, 1)
		if r.Header.Get("Authorization") != "Bearer rotated" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	p := New(Config{Name: "gateway", APIKey: "stale", APIBase: srv.URL + "/v1"})
	p.SetTokenRefresher(func(_ context.Context, _ string) (string, bool, error) {
		return "rotated", true, nil
	})
	ch, err := p.ChatStream(context.Background(), compatChat())
	if err != nil {
		t.Fatalf("stream after refresh: %v", err)
	}
	for ev := range ch {
		if ev.Type == types.EventError {
			t.Fatalf("unexpected stream error: %s", ev.Content)
		}
	}
	if n := atomic.LoadInt32(&chatHits); n != 2 {
		t.Errorf("expected 1 original + 1 replay, got %d", n)
	}
}
