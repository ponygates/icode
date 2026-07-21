package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ponygates/icode/internal/types"
)

func TestNewProvider(t *testing.T) {
	p := New("sk-ant-test", "")
	if p.Name() != ProviderName {
		t.Errorf("expected name %q, got %q", ProviderName, p.Name())
	}
	if p.apiBase != DefaultBase {
		t.Errorf("expected base %q, got %q", DefaultBase, p.apiBase)
	}
	if p.apiKey != "sk-ant-test" {
		t.Errorf("expected api key %q, got %q", "sk-ant-test", p.apiKey)
	}
}

func TestNewProvider_CustomBase(t *testing.T) {
	customBase := "https://custom.anthropic.com"
	p := New("sk-test", customBase)
	if p.apiBase != customBase {
		t.Errorf("expected custom base %q, got %q", customBase, p.apiBase)
	}
}

func TestName(t *testing.T) {
	p := New("sk-test", "")
	if p.Name() != "anthropic" {
		t.Errorf("expected 'anthropic', got %q", p.Name())
	}
}

func TestListModels(t *testing.T) {
	p := New("sk-test", "")
	models := p.ListModels()
	if len(models) == 0 {
		t.Fatal("expected at least 1 model")
	}

	// Check for the known model IDs
	modelIDs := make(map[string]bool)
	for _, m := range models {
		modelIDs[m.ID] = true
		if m.ContextWindow <= 0 {
			t.Errorf("model %q: context window should be > 0", m.ID)
		}
		if len(m.Plans) == 0 {
			t.Errorf("model %q: expected at least 1 pricing plan", m.ID)
		}
		if m.Provider != ProviderName {
			t.Errorf("model %q: expected provider %q, got %q", m.ID, ProviderName, m.Provider)
		}
	}

	if !modelIDs["claude-sonnet-4-20250514"] {
		t.Error("expected claude-sonnet-4-20250514 in model list")
	}
	if !modelIDs["claude-haiku-4-20250514"] {
		t.Error("expected claude-haiku-4-20250514 in model list")
	}
}

func TestAllModelsHaveValidConfig(t *testing.T) {
	p := New("sk-test", "")
	for _, m := range p.ListModels() {
		if m.ContextWindow <= 0 {
			t.Errorf("model %q: context window should be > 0", m.ID)
		}
		if m.MaxOutputTokens <= 0 {
			t.Errorf("model %q: max output tokens should be > 0", m.ID)
		}
		if m.Provider != ProviderName {
			t.Errorf("model %q: provider should be %q", m.ID, ProviderName)
		}
	}
}

func TestSupportsCache(t *testing.T) {
	p := New("sk-test", "")
	if !p.SupportsCache() {
		t.Error("Anthropic should support cache")
	}
}

func TestSetCredentials(t *testing.T) {
	p := New("old-key", "https://old.example.com")

	p.SetCredentials("new-key", "")
	if p.apiKey != "new-key" {
		t.Errorf("expected apiKey 'new-key', got %q", p.apiKey)
	}
	if p.apiBase != "https://old.example.com" {
		t.Errorf("apiBase should not have changed")
	}

	p.SetCredentials("", "https://new.example.com")
	if p.apiBase != "https://new.example.com" {
		t.Errorf("expected apiBase 'https://new.example.com', got %q", p.apiBase)
	}
}

func TestSetTimeout(t *testing.T) {
	p := New("sk-test", "")

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

func TestChatStream_WithMockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check auth header
		auth := r.Header.Get("x-api-key")
		if auth != "sk-ant-test" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid api key"}`))
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello from"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" Claude"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`))
	}))
	defer server.Close()

	p := New("sk-ant-test", server.URL)
	ch, err := p.ChatStream(context.Background(), types.ChatRequest{
		Model: "claude-sonnet-4-20250514",
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

	// Check text content
	foundText := false
	foundDone := false
	var fullText string
	for _, e := range events {
		if e.Type == types.EventText {
			foundText = true
			fullText += e.Content
		}
		if e.Type == types.EventDone {
			foundDone = true
		}
	}

	if !foundText {
		t.Error("expected text events")
	} else if fullText != "Hello from Claude" {
		t.Errorf("expected 'Hello from Claude', got %q", fullText)
	}

	if !foundDone {
		t.Error("expected done event")
	}
}

func TestChatStream_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer server.Close()

	p := New("bad-key", server.URL)
	_, err := p.ChatStream(context.Background(), types.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []types.Message{
			{Role: types.RoleUser, Content: "hi"},
		},
	})
	if err == nil {
		t.Fatal("expected error for HTTP 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention HTTP status, got: %v", err)
	}
}

func TestChatStream_ToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\": \"main.go\"}"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}

`))
	}))
	defer server.Close()

	p := New("sk-test", server.URL)
	ch, err := p.ChatStream(context.Background(), types.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []types.Message{
			{Role: types.RoleUser, Content: "read file"},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}

	// Anthropic emits tool calls TWICE:
	// 1. content_block_start: emits with empty Arguments
	// 2. content_block_stop: emits with accumulated Arguments
	// We track by index to find the FINAL state of each tool call.
	seenTool := make(map[int]*types.LiveToolCall)
	foundDone := false
	for e := range ch {
		if e.Type == types.EventToolUse && e.ToolCall != nil {
			seenTool[e.ToolCall.Index] = e.ToolCall
		}
		if e.Type == types.EventDone {
			foundDone = true
		}
	}

	if len(seenTool) == 0 {
		t.Fatal("expected at least 1 tool use event")
	}

	tc, ok := seenTool[0]
	if !ok {
		t.Fatal("expected tool call at index 0")
	}
	if tc.Name != "read_file" {
		t.Errorf("expected tool 'read_file', got %q", tc.Name)
	}
	if !strings.Contains(tc.Arguments, "main.go") {
		t.Errorf("expected arguments containing main.go, got %q", tc.Arguments)
	}
	if tc.ID != "toolu_1" {
		t.Errorf("expected tool ID 'toolu_1', got %q", tc.ID)
	}

	if !foundDone {
		t.Error("expected done event")
	}
}
