package openai_compat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// newRateLimitServer returns a test server that answers 429 with the given
// Retry-After header (empty string = omit it), plus a counter of hits.
func newRateLimitServer(retryAfter string) (*httptest.Server, *int32) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limit reached","type":"rate_limit_error"}}`)
	}))
	return srv, &hits
}

// A hint longer than maxProviderWait is not absorbed inside the provider: the
// response is handed straight back so the engine performs the (long) wait. The
// hint must survive the trip as a typed error.
func TestChatStream_RateLimitCarriesLongRetryAfter(t *testing.T) {
	srv, hits := newRateLimitServer("60")
	defer srv.Close()

	p := New(Config{Name: "rl-long", APIKey: "sk-test", APIBase: srv.URL})
	_, err := p.ChatStream(context.Background(), types.ChatRequest{
		Model:    "test-model",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error from a 429 response")
	}

	var rle *types.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("want *types.RateLimitError, got %T: %v", err, err)
	}
	if rle.Provider != "rl-long" {
		t.Errorf("Provider = %q, want rl-long", rle.Provider)
	}
	if rle.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want 429", rle.StatusCode)
	}
	if rle.RetryAfter != 60*time.Second {
		t.Errorf("RetryAfter = %v, want 60s (parsed from the header)", rle.RetryAfter)
	}
	// The long hint must short-circuit the in-provider retry loop: exactly one
	// request, no wasteful retries that would fail again anyway.
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("server hits = %d, want 1 (long hint should not be retried in-provider)", got)
	}
}

// A 429 without any hint still yields the typed error, with RetryAfter == 0 so
// the engine falls back to its own backoff schedule.
func TestChatStream_RateLimitWithoutHint(t *testing.T) {
	srv, _ := newRateLimitServer("")
	defer srv.Close()

	p := New(Config{Name: "rl-nohint", APIKey: "sk-test", APIBase: srv.URL})
	_, err := p.ChatStream(context.Background(), types.ChatRequest{
		Model:    "test-model",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	})

	var rle *types.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("want *types.RateLimitError, got %T: %v", err, err)
	}
	if rle.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0 when no header was sent", rle.RetryAfter)
	}
}

// A short hint is honoured in-provider: after waiting Retry-After the retry
// succeeds, so the caller never sees an error.
func TestChatStream_HonoursShortRetryAfter(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"slow down"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"index\":0}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := New(Config{Name: "rl-short", APIKey: "sk-test", APIBase: srv.URL})

	start := time.Now()
	ch, err := p.ChatStream(context.Background(), types.ChatRequest{
		Model:    "test-model",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("retry should have succeeded, got %v", err)
	}
	if ch == nil {
		t.Fatal("expected a stream channel")
	}
	// The retry must have waited roughly the advertised second, not the
	// default 100ms first step.
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %v, want >= ~1s (the Retry-After value, not the 100ms default)", elapsed)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("server hits = %d, want 2 (one 429 then one success)", got)
	}
}
