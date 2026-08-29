package voice

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── Pure helpers ────────────────────────────────────────────────

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("short", 10); got != "short" {
		t.Errorf("truncateStr(short) = %q, want %q", got, "short")
	}
	got := truncateStr("123456789012345", 10)
	if got != "1234567890..." {
		t.Errorf("truncateStr(long) = %q, want %q", got, "1234567890...")
	}
	if !strings.HasPrefix(GenerateCUID(), "icode-") {
		t.Errorf("GenerateCUID() = %q, want icode- prefix", GenerateCUID())
	}
}

// TestIFlytekSign verifies the HMAC-SHA1 signing against an independent
// implementation of the documented algorithm (app_id + ts signed with the
// API secret). A wrong signature here would fail EVERY iFlytek call, so this
// is a cheap regression net for the whole provider.
func TestIFlytekSign(t *testing.T) {
	want := func(secret, ts, signaStr string) string {
		mac := hmac.New(sha1.New, []byte(secret))
		mac.Write([]byte(signaStr))
		return base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	cases := []struct{ secret, ts, signa string }{
		{"sec1", "1700000000", "app1" + "1700000000"},
		{"", "1", "app1" + "1"},
		{"abc", "99", "app-xyz" + "99"},
	}
	for _, c := range cases {
		got := iFlytekSign(c.secret, c.ts, c.signa)
		if w := want(c.secret, c.ts, c.signa); got != w {
			t.Errorf("iFlytekSign(%q,%q,%q) = %q, want %q", c.secret, c.ts, c.signa, got, w)
		}
	}
}

// ── Input validation (no network) ───────────────────────────────

func TestTranscribeValidation(t *testing.T) {
	ctx := context.Background()
	audio := []byte{0x52, 0x49, 0x46, 0x46} // fake RIFF head

	// Zhipu: missing key / empty audio / oversized.
	if _, err := TranscribeZhipu(ctx, "", audio, "voice.wav"); err == nil {
		t.Error("TranscribeZhipu: empty key should error")
	}
	if _, err := TranscribeZhipu(ctx, "k", nil, "voice.wav"); err == nil {
		t.Error("TranscribeZhipu: empty audio should error")
	}
	if _, err := TranscribeZhipu(ctx, "k", make([]byte, MaxAudioBytes+1), "voice.wav"); err == nil {
		t.Error("TranscribeZhipu: oversized audio should error")
	}

	// Baidu: missing keys / empty audio / oversized.
	if _, err := TranscribeBaidu(ctx, "", "s", audio); err == nil {
		t.Error("TranscribeBaidu: empty apiKey should error")
	}
	if _, err := TranscribeBaidu(ctx, "k", "", audio); err == nil {
		t.Error("TranscribeBaidu: empty secretKey should error")
	}
	if _, err := TranscribeBaidu(ctx, "k", "s", nil); err == nil {
		t.Error("TranscribeBaidu: empty audio should error")
	}
	if _, err := TranscribeBaidu(ctx, "k", "s", make([]byte, MaxAudioBytes+1)); err == nil {
		t.Error("TranscribeBaidu: oversized audio should error")
	}

	// iFlytek: missing credentials / empty audio / oversized.
	if _, err := TranscribeiFlytek(ctx, "", "k", "s", audio); err == nil {
		t.Error("TranscribeiFlytek: empty appID should error")
	}
	if _, err := TranscribeiFlytek(ctx, "a", "k", "s", nil); err == nil {
		t.Error("TranscribeiFlytek: empty audio should error")
	}
	if _, err := TranscribeiFlytek(ctx, "a", "k", "s", make([]byte, MaxAudioBytes+1)); err == nil {
		t.Error("TranscribeiFlytek: oversized audio should error")
	}
}

// ── HTTP transport swap helpers ─────────────────────────────────

// roundTripFunc adapts a function into an http.RoundTripper for tests.
type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResp(v any) *http.Response {
	body, _ := json.Marshal(v)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
	}
}

// swapTransport replaces the default client transport for the duration of the
// test and restores it afterwards.
func swapTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	old := http.DefaultClient.Transport
	http.DefaultClient.Transport = rt
	t.Cleanup(func() { http.DefaultClient.Transport = old })
}

// resetBaiduCache clears the global token cache between tests.
func resetBaiduCache() {
	baiduCache.mu.Lock()
	baiduCache.token = ""
	baiduCache.expires = time.Time{}
	baiduCache.mu.Unlock()
}

// ── Baidu token cache ───────────────────────────────────────────

// TestBaiduGetTokenCached verifies the fast path: a valid cached token is
// returned WITHOUT any HTTP round-trip (the cache lock must not block on the
// network — regression for the B3 lock restructure).
func TestBaiduGetTokenCached(t *testing.T) {
	resetBaiduCache()
	defer resetBaiduCache()

	calls := 0
	swapTransport(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return jsonResp(map[string]any{"access_token": "fresh", "expires_in": 3600}), nil
	}))

	// Seed the cache with a token valid far into the future.
	baiduCache.mu.Lock()
	baiduCache.token = "cached-token"
	baiduCache.expires = time.Now().Add(time.Hour)
	baiduCache.mu.Unlock()

	tok, err := baiduGetToken(context.Background(), "k", "s")
	if err != nil {
		t.Fatalf("baiduGetToken(cached) error: %v", err)
	}
	if tok != "cached-token" {
		t.Errorf("baiduGetToken(cached) = %q, want cached-token", tok)
	}
	if calls != 0 {
		t.Errorf("cached token must not hit the network (calls=%d)", calls)
	}
}

// TestBaiduGetTokenRefresh verifies the slow path: an expired/missing cache
// triggers exactly one token request outside the lock, and the fresh token is
// written back for the next call.
func TestBaiduGetTokenRefresh(t *testing.T) {
	resetBaiduCache()
	defer resetBaiduCache()

	calls := 0
	swapTransport(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return jsonResp(map[string]any{"access_token": "fresh-token", "expires_in": 3600}), nil
	}))

	tok, err := baiduGetToken(context.Background(), "k", "s")
	if err != nil {
		t.Fatalf("baiduGetToken(refresh) error: %v", err)
	}
	if tok != "fresh-token" {
		t.Errorf("baiduGetToken(refresh) = %q, want fresh-token", tok)
	}
	if calls != 1 {
		t.Errorf("refresh must hit the network exactly once (calls=%d)", calls)
	}

	// Second call is now cached — no further network.
	if _, err := baiduGetToken(context.Background(), "k", "s"); err != nil {
		t.Fatalf("second baiduGetToken error: %v", err)
	}
	if calls != 1 {
		t.Errorf("second call should be served from cache (calls=%d)", calls)
	}
}

// ── Baidu ASR payload format ────────────────────────────────────

// TestTranscribeBaiduFormatWav is the regression test for B4: the recorder
// produces a 44-byte RIFF/WAVE buffer (BuildWAV), so the ASR payload must
// declare format "wav" — declaring "pcm" makes Baidu parse the WAV header as
// audio and fail. We capture the outgoing JSON and assert the fields.
func TestTranscribeBaiduFormatWav(t *testing.T) {
	resetBaiduCache()
	defer resetBaiduCache()

	var captured map[string]any
	swapTransport(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(req.URL.String(), "/oauth/2.0/token"):
			return jsonResp(map[string]any{"access_token": "tok", "expires_in": 3600}), nil
		case strings.Contains(req.URL.String(), "/server_api"):
			raw, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(raw, &captured)
			return jsonResp(map[string]any{"err_no": 0, "result": []string{"你好"}}), nil
		default:
			return jsonResp(map[string]any{"err_no": 1, "err_msg": "unexpected endpoint " + req.URL.String()}), nil
		}
	}))

	// A minimal WAV-looking blob (BuildWAV output shape: 44-byte header + data).
	audio := make([]byte, 44+256)
	copy(audio, "RIFF")
	text, err := TranscribeBaidu(context.Background(), "k", "s", audio)
	if err != nil {
		t.Fatalf("TranscribeBaidu error: %v", err)
	}
	if text != "你好" {
		t.Errorf("TranscribeBaidu text = %q, want 你好", text)
	}
	if captured == nil {
		t.Fatal("ASR request body was not captured")
	}
	if fmt, _ := captured["format"].(string); fmt != "wav" {
		t.Errorf("payload format = %q, want \"wav\" (B4 regression: recorder emits WAV)", fmt)
	}
	if rate, _ := captured["rate"].(float64); int(rate) != 16000 {
		t.Errorf("payload rate = %v, want 16000", rate)
	}
	if ch, _ := captured["channel"].(float64); int(ch) != 1 {
		t.Errorf("payload channel = %v, want 1", ch)
	}
	if tok, _ := captured["token"].(string); tok != "tok" {
		t.Errorf("payload token = %q, want tok", tok)
	}
}

// ── iFlytek cancellation ────────────────────────────────────────

// TestTranscribeiFlytekCancel verifies the A7 regression: the getProgress
// polling loop must observe ctx cancellation and bail out with the dedicated
// message instead of polling for up to 30s.
func TestTranscribeiFlytekCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var mu sync.Mutex
	progressCalls := 0
	swapTransport(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		u := req.URL.String()
		switch {
		case strings.Contains(u, "/prepare"):
			return jsonResp(map[string]any{"ok": 0, "data": "task-1"}), nil
		case strings.Contains(u, "/upload"):
			return jsonResp(map[string]any{"ok": 0, "data": ""}), nil
		case strings.Contains(u, "/merge"):
			return jsonResp(map[string]any{"ok": 0, "data": ""}), nil
		case strings.Contains(u, "/getProgress"):
			mu.Lock()
			progressCalls++
			mu.Unlock()
			// Never completes; cancel the caller so the poll loop must stop.
			cancel()
			return jsonResp(map[string]any{"ok": 0, "data": `{"status":1}`}), nil
		default:
			return jsonResp(map[string]any{"ok": 1, "failed": "unexpected " + u}), nil
		}
	}))

	_, err := TranscribeiFlytek(ctx, "app", "key", "secret", make([]byte, 44+128))
	if err == nil {
		t.Fatal("TranscribeiFlytek should error when cancelled")
	}
	if !strings.Contains(err.Error(), "讯飞识别被取消") {
		t.Errorf("error = %q, want cancellation message", err.Error())
	}
	mu.Lock()
	defer mu.Unlock()
	if progressCalls == 0 {
		t.Error("expected at least one getProgress call before cancellation")
	}
}

// TestTranscribeiFlytekResult verifies the happy path end-to-end with a
// completed transcription.
func TestTranscribeiFlytekResult(t *testing.T) {
	ctx := context.Background()
	swapTransport(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		u := req.URL.String()
		switch {
		case strings.Contains(u, "/prepare"):
			return jsonResp(map[string]any{"ok": 0, "data": "task-ok"}), nil
		case strings.Contains(u, "/upload"):
			return jsonResp(map[string]any{"ok": 0, "data": ""}), nil
		case strings.Contains(u, "/merge"):
			return jsonResp(map[string]any{"ok": 0, "data": ""}), nil
		case strings.Contains(u, "/getProgress"):
			return jsonResp(map[string]any{"ok": 0, "data": `{"status":4}`}), nil
		case strings.Contains(u, "/getResult"):
			return jsonResp(map[string]any{"ok": 0, "data": `[{"onebest":"讯飞转写成功"}]`}), nil
		default:
			return jsonResp(map[string]any{"ok": 1, "failed": "unexpected " + u}), nil
		}
	}))

	text, err := TranscribeiFlytek(ctx, "app", "key", "secret", make([]byte, 44+128))
	if err != nil {
		t.Fatalf("TranscribeiFlytek happy path error: %v", err)
	}
	if text != "讯飞转写成功" {
		t.Errorf("TranscribeiFlytek text = %q, want 讯飞转写成功", text)
	}
}
