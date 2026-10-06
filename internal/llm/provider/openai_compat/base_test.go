package openai_compat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ponygates/icode/internal/types"
)

func TestNewProvider(t *testing.T) {
	models := []types.ModelInfo{
		{ID: "test-model", ContextWindow: 128000},
	}
	p := New(Config{
		Name:       "test-provider",
		APIBase:    "https://api.example.com",
		APIKey:     "sk-test",
		TimeoutSec: 60,
		Models:     models,
	})

	if p.Name() != "test-provider" {
		t.Errorf("expected name 'test-provider', got %q", p.Name())
	}
	if len(p.ListModels()) != 1 {
		t.Errorf("expected 1 model, got %d", len(p.ListModels()))
	}
	if p.ListModels()[0].ID != "test-model" {
		t.Errorf("expected model ID 'test-model', got %q", p.ListModels()[0].ID)
	}
}

func TestNewProvider_Defaults(t *testing.T) {
	p := New(Config{
		Name:   "defaults-test",
		APIKey: "sk-test",
	})

	if p.Name() != "defaults-test" {
		t.Errorf("expected name 'defaults-test', got %q", p.Name())
	}
	// Timeout model (see New): no whole-request client timeout — a long
	// streaming body must not be killed on a timer. Instead the default
	// 120s applies as the Transport's first-response (headers) timeout,
	// and Chat() bounds itself through the request context.
	if p.httpClient.Timeout != 0 {
		t.Errorf("client whole-request timeout = %v, want 0 (streaming-safe)", p.httpClient.Timeout)
	}
	tr, ok := p.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", p.httpClient.Transport)
	}
	if tr.ResponseHeaderTimeout != 120*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 120s", tr.ResponseHeaderTimeout)
	}
	if p.timeout != 120*time.Second {
		t.Errorf("provider timeout field = %v, want 120s (Chat ctx deadline)", p.timeout)
	}
}

func TestSetCredentials(t *testing.T) {
	p := New(Config{
		Name:    "cred-test",
		APIKey:  "old-key",
		APIBase: "https://old.example.com",
	})

	// Update just API key
	p.SetCredentials("new-key", "")
	if p.apiKey != "new-key" {
		t.Errorf("expected apiKey 'new-key', got %q", p.apiKey)
	}
	if p.apiBase != "https://old.example.com" {
		t.Errorf("apiBase should not have changed, got %q", p.apiBase)
	}

	// Update just base URL
	p.SetCredentials("", "https://new.example.com")
	if p.apiBase != "https://new.example.com" {
		t.Errorf("expected apiBase 'https://new.example.com', got %q", p.apiBase)
	}
}

func TestSetTimeout(t *testing.T) {
	p := New(Config{Name: "timeout-test", APIKey: "sk-test"})

	p.SetTimeout(30)
	if p.httpClient.Timeout != 30*time.Second {
		t.Errorf("expected timeout 30s, got %v", p.httpClient.Timeout)
	}

	// Zero should reset to 120s
	p.SetTimeout(0)
	if p.httpClient.Timeout != 120*time.Second {
		t.Errorf("expected timeout reset to 120s, got %v", p.httpClient.Timeout)
	}
}

func TestSupportsCache(t *testing.T) {
	p1 := New(Config{Name: "cache-on", APIKey: "sk-test", CacheSupport: true})
	if !p1.SupportsCache() {
		t.Error("expected cache support enabled")
	}

	p2 := New(Config{Name: "cache-off", APIKey: "sk-test", CacheSupport: false})
	if p2.SupportsCache() {
		t.Error("expected cache support disabled")
	}
}

func TestChatEndpoint(t *testing.T) {
	p := New(Config{
		Name:    "endpoint-test",
		APIKey:  "sk-test",
		APIBase: "https://api.example.com/v1",
	})
	endpoint := p.chatEndpoint()
	if endpoint != "https://api.example.com/v1/chat/completions" {
		t.Errorf("unexpected endpoint: %s", endpoint)
	}

	// Test without trailing /v1
	p2 := New(Config{
		Name:    "endpoint-test-2",
		APIKey:  "sk-test",
		APIBase: "https://api.example.com",
	})
	endpoint2 := p2.chatEndpoint()
	if endpoint2 != "https://api.example.com/chat/completions" {
		t.Errorf("unexpected endpoint: %s", endpoint2)
	}
}

func TestReadStream_BasicText(t *testing.T) {
	p := New(Config{Name: "stream-test", APIKey: "sk-test"})

	sseData := `data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}

data: {"choices":[{"delta":{"content":" World"},"index":0}]}

data: {"choices":[{"delta":{},"index":0,"finish_reason":"stop"}]}

data: [DONE]
`

	ch := make(chan types.StreamEvent, 64)
	go p.readStream(context.Background(), sseBody(sseData), ch)

	var events []types.StreamEvent
	for e := range ch {
		events = append(events, e)
	}

	if len(events) < 2 {
		t.Fatalf("expected at least 2 events (text + done), got %d", len(events))
	}

	// Check first text event
	if events[0].Type != types.EventText {
		t.Errorf("expected EventText, got %v", events[0].Type)
	}
	if events[0].Content != "Hello" {
		t.Errorf("expected content 'Hello', got %q", events[0].Content)
	}

	// Second text event
	if events[1].Type != types.EventText || events[1].Content != " World" {
		t.Errorf("expected ' World' text event, got %v", events[1].Type)
	}

	// Last event should be done
	last := events[len(events)-1]
	if last.Type != types.EventDone {
		t.Errorf("expected final EventDone, got %v", last.Type)
	}
}

func TestReadStream_ToolCall(t *testing.T) {
	p := New(Config{Name: "stream-tool-test", APIKey: "sk-test"})

	sseData := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_file","arguments":""}}]},"index":0}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\": \"main.go\"}"}}]},"index":0}]}

data: {"choices":[{"delta":{},"index":0,"finish_reason":"tool_calls"}]}

data: [DONE]
`

	ch := make(chan types.StreamEvent, 64)
	go p.readStream(context.Background(), sseBody(sseData), ch)

	var events []types.StreamEvent
	for e := range ch {
		events = append(events, e)
	}

	// Should have a tool use event and a done event
	foundTool := false
	foundDone := false
	for _, e := range events {
		if e.Type == types.EventToolUse && e.ToolCall != nil {
			foundTool = true
			if e.ToolCall.Name != "read_file" {
				t.Errorf("expected tool name 'read_file', got %q", e.ToolCall.Name)
			}
			// Arguments should be accumulated from both deltas
			expected := `{"path": "main.go"}`
			if e.ToolCall.Arguments != expected {
				t.Errorf("expected arguments %q, got %q", expected, e.ToolCall.Arguments)
			}
		}
		if e.Type == types.EventDone {
			foundDone = true
			if e.Meta.FinishReason != "tool_calls" {
				t.Errorf("expected finish_reason 'tool_calls', got %q", e.Meta.FinishReason)
			}
		}
	}

	if !foundTool {
		t.Error("expected a tool use event")
	}
	if !foundDone {
		t.Error("expected a done event")
	}
}

func TestReadStream_WithUsage(t *testing.T) {
	p := New(Config{Name: "stream-usage-test", APIKey: "sk-test"})

	sseData := `data: {"choices":[{"delta":{"content":"response"},"index":0}]}

data: {"choices":[{"delta":{},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}

data: [DONE]
`

	ch := make(chan types.StreamEvent, 64)
	go p.readStream(context.Background(), sseBody(sseData), ch)

	var events []types.StreamEvent
	for e := range ch {
		events = append(events, e)
	}

	foundUsage := false
	for _, e := range events {
		if e.Type == types.EventDone && e.Meta.Usage.TotalTokens > 0 {
			foundUsage = true
			if e.Meta.Usage.PromptTokens != 10 {
				t.Errorf("expected prompt_tokens=10, got %d", e.Meta.Usage.PromptTokens)
			}
			if e.Meta.Usage.CompletionTokens != 5 {
				t.Errorf("expected completion_tokens=5, got %d", e.Meta.Usage.CompletionTokens)
			}
		}
	}
	if !foundUsage {
		t.Error("expected usage info in done event")
	}
}

func TestReadStream_EmptyResponse(t *testing.T) {
	p := New(Config{Name: "stream-empty-test", APIKey: "sk-test"})

	// No valid SSE data
	sseData := "just some random text\nno data prefix\n"

	ch := make(chan types.StreamEvent, 64)
	go p.readStream(context.Background(), sseBody(sseData), ch)

	var events []types.StreamEvent
	for e := range ch {
		events = append(events, e)
	}

	if len(events) < 1 {
		t.Fatal("expected at least one event (error or done)")
	}

	// Should produce an error event about missing response
	last := events[len(events)-1]
	if last.Type != types.EventError {
		t.Errorf("expected EventError for empty response, got %v", last.Type)
	}
}

func TestReadStream_MultipleToolCalls(t *testing.T) {
	p := New(Config{Name: "stream-multi-tool", APIKey: "sk-test"})

	// Two tool calls in parallel
	sseData := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_file","arguments":""}}]},"index":0}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","function":{"name":"grep","arguments":""}}]},"index":0}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\": \"main.go\"}"}}]},"index":0}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"pattern\": \"test\"}"}}]},"index":0}]}

data: {"choices":[{"delta":{},"index":0,"finish_reason":"tool_calls"}]}

data: [DONE]
`

	ch := make(chan types.StreamEvent, 64)
	go p.readStream(context.Background(), sseBody(sseData), ch)

	events := make(map[int]*types.LiveToolCall)
	for e := range ch {
		if e.Type == types.EventToolUse && e.ToolCall != nil {
			events[e.ToolCall.Index] = e.ToolCall
		}
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 distinct tool calls, got %d", len(events))
	}

	if events[0].Name != "read_file" {
		t.Errorf("expected tool 0 'read_file', got %q", events[0].Name)
	}
	if events[1].Name != "grep" {
		t.Errorf("expected tool 1 'grep', got %q", events[1].Name)
	}
	if events[0].Arguments != `{"path": "main.go"}` {
		t.Errorf("tool 0 unexpected arguments: %q", events[0].Arguments)
	}
}

// TestChatStream_WithMockServer tests the full ChatStream flow using a mock
// HTTP server that returns valid SSE data.
func TestChatStream_WithMockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		data := `data: {"choices":[{"delta":{"content":"Hello from mock"},"index":0}]}

data: {"choices":[{"delta":{},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}

data: [DONE]
`
		w.Write([]byte(data))
	}))
	defer server.Close()

	p := New(Config{
		Name:    "mock-test",
		APIKey:  "sk-test",
		APIBase: server.URL,
		Models: []types.ModelInfo{
			{ID: "mock-model", ContextWindow: 128000},
		},
	})

	ctx := context.Background()
	ch, err := p.ChatStream(ctx, types.ChatRequest{
		Model: "mock-model",
		Messages: []types.Message{
			{Role: types.RoleUser, Content: "test"},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}

	var events []types.StreamEvent
	for e := range ch {
		events = append(events, e)
	}

	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}

	if events[0].Type != types.EventText || events[0].Content != "Hello from mock" {
		t.Errorf("expected text 'Hello from mock', got %v", events[0])
	}

	last := events[len(events)-1]
	if last.Type != types.EventDone {
		t.Errorf("expected EventDone, got %v", last.Type)
	}
}

// TestChatStream_HTTPError tests that ChatStream returns an error on non-200.
func TestChatStream_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer server.Close()

	p := New(Config{
		Name:    "error-test",
		APIKey:  "bad-key",
		APIBase: server.URL,
	})

	_, err := p.ChatStream(context.Background(), types.ChatRequest{
		Model:    "test-model",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for HTTP 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention HTTP status, got: %v", err)
	}
}

// ============================================================================
// Helpers
// ============================================================================

func sseBody(data string) *ioReadCloser {
	return newIORC(strings.NewReader(data))
}

// ioReadCloser wraps a strings.Reader to implement io.ReadCloser.
type ioReadCloser struct {
	*strings.Reader
}

func newIORC(r *strings.Reader) *ioReadCloser {
	return &ioReadCloser{r}
}

func (r *ioReadCloser) Close() error { return nil }

// Ensure streamChunk marshaling works properly
func TestStreamChunk_Unmarshal(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "text delta",
			input: `{"choices":[{"delta":{"content":"hello"},"index":0}]}`,
			want:  "hello",
		},
		{
			name:  "empty delta",
			input: `{"choices":[{"delta":{},"index":0}]}`,
			want:  "",
		},
		{
			name:  "with usage",
			input: `{"choices":[{"delta":{},"index":0}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var chunk streamChunk
			if err := json.Unmarshal([]byte(tt.input), &chunk); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if len(chunk.Choices) == 0 && tt.want != "" {
				t.Errorf("expected choices, got none")
				return
			}
			if len(chunk.Choices) > 0 {
				got := chunk.Choices[0].Delta.Content
				if got != tt.want {
					t.Errorf("expected content %q, got %q", tt.want, got)
				}
			}
		})
	}
}

func TestBuildRequestBody_CacheBreakpointsShift(t *testing.T) {
	cases := []struct {
		name   string
		sys    string
		bps    []int
		want   []int
		hasKey bool
	}{
		{name: "system prefix shifts non-zero breakpoints", sys: "SYS", bps: []int{0, 20, 40}, want: []int{0, 21, 41}, hasKey: true},
		{name: "no system prefix keeps indices", sys: "", bps: []int{0, 20}, want: []int{0, 20}, hasKey: true},
		{name: "no breakpoints omits key", sys: "SYS", bps: nil, want: nil, hasKey: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := New(Config{Name: "cache", APIKey: "sk-test", CacheSupport: true})
			req := types.ChatRequest{
				SystemPrompt:     tc.sys,
				Messages:         []types.Message{{Role: types.RoleUser, Content: "hi"}},
				CacheBreakpoints: tc.bps,
			}
			body, err := p.buildRequestBody(req, false)
			if err != nil {
				t.Fatalf("buildRequestBody: %v", err)
			}
			var m map[string]any
			if err := json.NewDecoder(body).Decode(&m); err != nil {
				t.Fatalf("decode: %v", err)
			}
			raw, has := m["cache_breakpoints"]
			if has != tc.hasKey {
				t.Fatalf("has cache_breakpoints = %v, want %v", has, tc.hasKey)
			}
			if !has {
				return
			}
			arr := raw.([]any)
			if len(arr) != len(tc.want) {
				t.Fatalf("len = %d, want %d (%v)", len(arr), len(tc.want), arr)
			}
			for i, v := range arr {
				if int(v.(float64)) != tc.want[i] {
					t.Fatalf("idx %d = %v, want %d", i, v, tc.want[i])
				}
			}
		})
	}
}
// blockingBody: first Read returns an SSE line, further Reads park until
// Close (what a stalled connection looks like to the scanner).
type blockingBody struct {
	first *bool
	mu    *sync.Mutex
	park  *stuckBody
}

func (b *blockingBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	f := *b.first
	*b.first = false
	b.mu.Unlock()
	if f {
		line := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"
		return copy(p, line), nil
	}
	<-b.park.closed
	return 0, io.EOF
}

func (b *blockingBody) Close() error { return b.park.Close() }

// stuckBody parks Reads on a channel released by Close.
type stuckBody struct {
	once   sync.Once
	closed chan struct{}
}

func newStuckBody() *stuckBody { return &stuckBody{closed: make(chan struct{})} }

func (b *stuckBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

// TestReadStream_IdleWatchdog verifies the idle watchdog: a stalled stream
// gets a friendly EventError after the idle window and the channel closes
// without a duplicate raw scan error.
func TestReadStream_IdleWatchdog(t *testing.T) {
	old := streamIdleTimeout
	streamIdleTimeout = 150 * time.Millisecond
	defer func() { streamIdleTimeout = old }()

	p := New(Config{Name: "idle-test", APIKey: "sk-test"})
	ch := make(chan types.StreamEvent, 64)
	first := true
	var mu sync.Mutex
	body := &blockingBody{first: &first, mu: &mu, park: newStuckBody()}
	go p.readStream(context.Background(), body, ch)

	// First event: the "hi" text.
	select {
	case ev := <-ch:
		if ev.Type != types.EventText || ev.Content != "hi" {
			t.Fatalf("first event = %+v, want text 'hi'", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no first event")
	}
	// Then the watchdog fires with the friendly idle-timeout error.
	select {
	case ev := <-ch:
		if ev.Type != types.EventError || !strings.Contains(ev.Content, "空闲超时") {
			t.Fatalf("second event = %+v, want idle-timeout EventError", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watchdog never fired")
	}
	// And the channel closes (no duplicate raw error from the broken scan).
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel closed after idle error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("channel never closed")
	}
}

// midDropBody emits one good SSE line, then returns a raw network error —
// simulating a connection reset after partial output.
type midDropBody struct{ read bool }

func (b *midDropBody) Read(p []byte) (int, error) {
	if !b.read {
		b.read = true
		line := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"
		return copy(p, line), nil
	}
	return 0, fmt.Errorf("read tcp 1.2.3.4:443: connection reset by peer")
}

func (b *midDropBody) Close() error { return nil }

// TestReadStream_MidStreamErrorSurfaced guards the regression where a scan
// error after partial output was swallowed (no error event), silently
// discarding the user's half reply.
func TestReadStream_MidStreamErrorSurfaced(t *testing.T) {
	old := streamIdleTimeout
	streamIdleTimeout = 10 * time.Second // effectively disabled for this test
	defer func() { streamIdleTimeout = old }()

	p := New(Config{Name: "drop-test", APIKey: "sk-test"})
	ch := make(chan types.StreamEvent, 64)
	go p.readStream(context.Background(), &midDropBody{}, ch)

	var sawText, sawErr bool
	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			switch ev.Type {
			case types.EventText:
				if ev.Content != "hello" {
					t.Fatalf("text = %q", ev.Content)
				}
				sawText = true
			case types.EventError:
				if !strings.Contains(ev.Content, "connection reset") {
					t.Fatalf("error = %q, want connection reset", ev.Content)
				}
				sawErr = true
			}
		case <-time.After(3 * time.Second):
			t.Fatal("stream stalled")
		}
	}
	if !sawText || !sawErr {
		t.Fatalf("sawText=%v sawErr=%v, want both", sawText, sawErr)
	}
	// Channel closes after the error.
	if _, ok := <-ch; ok {
		t.Fatal("expected channel closed")
	}
}
