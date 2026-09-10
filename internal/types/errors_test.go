package types

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// httpDate renders t in the RFC 9110 IMF-fixdate form the parser accepts.
func httpDate(t time.Time) string {
	return t.UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
}

// headerGetter builds a header lookup over a plain map, mirroring
// http.Header.Get for the purposes of these tests.
func headerGetter(h map[string]string) func(string) string {
	return func(k string) string { return h[k] }
}

func TestRetryAfterFromHeaders_StandardSeconds(t *testing.T) {
	got := RetryAfterFromHeaders(headerGetter(map[string]string{"Retry-After": "30"}))
	if got != 30*time.Second {
		t.Fatalf("Retry-After: 30 → want 30s, got %v", got)
	}
}

func TestRetryAfterFromHeaders_FractionalSeconds(t *testing.T) {
	// Not RFC-legal but several vendors emit it; accepting it is harmless.
	got := RetryAfterFromHeaders(headerGetter(map[string]string{"Retry-After": "1.5"}))
	if got != 1500*time.Millisecond {
		t.Fatalf("Retry-After: 1.5 → want 1.5s, got %v", got)
	}
}

func TestRetryAfterFromHeaders_FutureHTTPDate(t *testing.T) {
	got := RetryAfterFromHeaders(headerGetter(map[string]string{
		"Retry-After": httpDate(time.Now().Add(45 * time.Second)),
	}))
	if got < 40*time.Second || got > 50*time.Second {
		t.Fatalf("future HTTP-date → want ~45s, got %v", got)
	}
}

func TestRetryAfterFromHeaders_PastHTTPDateClampsToImmediate(t *testing.T) {
	got := RetryAfterFromHeaders(headerGetter(map[string]string{
		"Retry-After": httpDate(time.Now().Add(-5 * time.Minute)),
	}))
	// A past instant means the window already reset: retry immediately, but
	// still report *a* hint (0 would push callers onto a blind sleep).
	if got != time.Millisecond {
		t.Fatalf("past HTTP-date → want 1ms, got %v", got)
	}
}

func TestRetryAfterFromHeaders_MillisecondVariant(t *testing.T) {
	got := RetryAfterFromHeaders(headerGetter(map[string]string{"retry-after-ms": "1500"}))
	if got != 1500*time.Millisecond {
		t.Fatalf("retry-after-ms: 1500 → want 1.5s, got %v", got)
	}
}

func TestRetryAfterFromHeaders_ResetAfterVariant(t *testing.T) {
	got := RetryAfterFromHeaders(headerGetter(map[string]string{"x-ratelimit-reset-after": "5"}))
	if got != 5*time.Second {
		t.Fatalf("x-ratelimit-reset-after: 5 → want 5s, got %v", got)
	}
}

func TestRetryAfterFromHeaders_AnthropicResetDate(t *testing.T) {
	got := RetryAfterFromHeaders(headerGetter(map[string]string{
		"anthropic-ratelimit-requests-reset": httpDate(time.Now().Add(20 * time.Second)),
	}))
	if got < 15*time.Second || got > 25*time.Second {
		t.Fatalf("anthropic reset date → want ~20s, got %v", got)
	}
}

func TestRetryAfterFromHeaders_EpochReset(t *testing.T) {
	epoch := time.Now().Add(30 * time.Second).Unix()
	got := RetryAfterFromHeaders(headerGetter(map[string]string{
		"x-ratelimit-reset": strconv.FormatInt(epoch, 10),
	}))
	if got < 25*time.Second || got > 35*time.Second {
		t.Fatalf("epoch reset → want ~30s, got %v", got)
	}
}

func TestRetryAfterFromHeaders_Precedence(t *testing.T) {
	// The standard header must win over the vendor variants.
	got := RetryAfterFromHeaders(headerGetter(map[string]string{
		"Retry-After":                      "7",
		"retry-after-ms":                   "99999",
		"x-ratelimit-reset-after":          "42",
		"anthropic-ratelimit-tokens-reset": httpDate(time.Now().Add(time.Hour)),
	}))
	if got != 7*time.Second {
		t.Fatalf("Retry-After should take precedence → want 7s, got %v", got)
	}
}

func TestRetryAfterFromHeaders_NoHint(t *testing.T) {
	cases := []struct {
		name string
		get  func(string) string
	}{
		{"nil getter", nil},
		{"empty headers", headerGetter(map[string]string{})},
		{"garbage value", headerGetter(map[string]string{"Retry-After": "soon"})},
		{"negative seconds", headerGetter(map[string]string{"Retry-After": "-5"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RetryAfterFromHeaders(tc.get); got != 0 {
				t.Fatalf("want 0 (no usable hint), got %v", got)
			}
		})
	}
}

func TestRateLimitError_ErrorString(t *testing.T) {
	err := &RateLimitError{Provider: "deepseek", StatusCode: 429, RetryAfter: 30 * time.Second}
	msg := err.Error()

	// friendlyModelError classifies by substring and isRateLimitError matches
	// "429" — the typed message must keep both working.
	if !strings.Contains(msg, "429") {
		t.Errorf("error text must carry the status code so message-based classification still works: %q", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "rate limit") {
		t.Errorf("error text must mention the rate limit: %q", msg)
	}
	if !strings.Contains(msg, "deepseek") {
		t.Errorf("error text should name the provider: %q", msg)
	}
	if !strings.Contains(msg, "30s") {
		t.Errorf("error text should surface the wait hint: %q", msg)
	}
}

func TestRateLimitError_NilSafety(t *testing.T) {
	var err *RateLimitError
	if got := err.Error(); got == "" {
		t.Fatal("nil RateLimitError must still return a message rather than panic")
	}
}

func TestRateLimitError_ErrorsAs(t *testing.T) {
	var base error = &RateLimitError{Provider: "kimi", StatusCode: 429}
	var target *RateLimitError
	if !errors.As(base, &target) {
		t.Fatal("errors.As must recover the typed rate-limit error")
	}
	if target.Provider != "kimi" {
		t.Fatalf("recovered wrong error: %+v", target)
	}
}
