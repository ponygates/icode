// Package openai_compat provides a base Provider implementation for any
// OpenAI-compatible API (Chat Completions). Most Chinese and international
// providers follow this protocol, making it the foundation for the multi-model system.
package openai_compat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// BaseProvider implements types.Provider using the OpenAI Chat Completions API.
// Concrete providers (DeepSeek, Zhipu, Kimi, etc.) embed or extend this type.
type BaseProvider struct {
	name         string
	apiBase      string
	apiKey       string
	httpClient   *http.Client
	models       []types.ModelInfo
	mu           sync.RWMutex
	cacheSupport bool
}

// Config configures a BaseProvider.
type Config struct {
	Name         string
	APIBase      string
	APIKey       string
	TimeoutSec   int
	Models       []types.ModelInfo
	CacheSupport bool
}

// FactoryConfig holds per-provider static configuration used by NewProvider.
type FactoryConfig struct {
	Name         string
	DefaultBase  string
	TimeoutSec   int
	CacheSupport bool
}

// New creates a new OpenAI-compatible provider.
func New(cfg Config) *BaseProvider {
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 120
	}

	return &BaseProvider{
		name:         cfg.Name,
		apiBase:      strings.TrimRight(cfg.APIBase, "/"),
		apiKey:       cfg.APIKey,
		models:       cfg.Models,
		cacheSupport: cfg.CacheSupport,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.TimeoutSec) * time.Second,
		},
	}
}

// NewProvider creates a provider using factory config + runtime credentials.
// DefaultBase is used when apiBase is empty, eliminating repetitive if-statements
// in each concrete provider's New().
func NewProvider(fc FactoryConfig, apiKey, apiBase string, models []types.ModelInfo) *BaseProvider {
	if apiBase == "" {
		apiBase = fc.DefaultBase
	}
	return New(Config{
		Name:         fc.Name,
		APIBase:      apiBase,
		APIKey:       apiKey,
		TimeoutSec:   fc.TimeoutSec,
		CacheSupport: fc.CacheSupport,
		Models:       models,
	})
}

// Name returns the provider identifier.
func (p *BaseProvider) Name() string {
	return p.name
}

// ListModels returns cached model info.
func (p *BaseProvider) ListModels() []types.ModelInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.models
}

// SetModels updates the model list (called by auto-update).
func (p *BaseProvider) SetModels(models []types.ModelInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.models = models
}

// SupportsCache reports prefix-cache compatibility.
func (p *BaseProvider) SupportsCache() bool {
	return p.cacheSupport
}

// SetCredentials updates the API key and base URL at runtime. Empty values are
// left unchanged so callers can update only one field. This lets the server
// push keys configured via the desktop UI into the live provider without a
// restart.
func (p *BaseProvider) SetCredentials(apiKey, apiBase string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if apiKey != "" {
		p.apiKey = apiKey
	}
	if apiBase != "" {
		p.apiBase = strings.TrimRight(apiBase, "/")
	}
}

// SetTimeout updates the HTTP client timeout at runtime (used when the desktop
// UI changes a provider's per-provider timeout). Values <= 0 reset to 120s.
func (p *BaseProvider) SetTimeout(sec int) {
	if sec <= 0 {
		sec = 120
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.httpClient.Timeout = time.Duration(sec) * time.Second
}

// Health performs a connectivity check.
func (p *BaseProvider) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiBase+"/models", nil)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	p.setAuth(req)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("health check: HTTP %d — %s", resp.StatusCode, string(body))
	}
	return nil
}

// ============================================================================
// Retry / backoff for transient API failures
// ============================================================================

// retryableStatus returns true for HTTP status codes that warrant a retry.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, // 429
		http.StatusServiceUnavailable,          // 503
		http.StatusBadGateway,                  // 502
		http.StatusGatewayTimeout:              // 504
		return true
	}
	return false
}

// doRequestWithRetry executes an HTTP request with exponential backoff.
// It retries on transient network errors and retryable HTTP status codes
// (429, 502, 503, 504) with up to maxRetries attempts.
func (p *BaseProvider) doRequestWithRetry(ctx context.Context, httpReq *http.Request, maxRetries int) (*http.Response, error) {
	backoff := 100 * time.Millisecond

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Clone the request body for each retry (body can only be read once).
		var bodyReader io.Reader
		if httpReq.Body != nil {
			bodyBytes, _ := io.ReadAll(httpReq.Body)
			httpReq.Body.Close()
			bodyReader = bytes.NewReader(bodyBytes)
			httpReq.Body = io.NopCloser(bodyReader)
		}

		resp, err := p.httpClient.Do(httpReq)
		if err != nil {
			// Transient network error — retry if we have attempts left.
			if attempt < maxRetries {
				select {
				case <-time.After(backoff):
					backoff *= 2
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, err
		}

		// Retry on rate-limit / server-error status codes.
		if resp.StatusCode >= 400 && retryableStatus(resp.StatusCode) && attempt < maxRetries {
			resp.Body.Close()
			select {
			case <-time.After(backoff):
				backoff *= 2
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		return resp, nil
	}

	return nil, fmt.Errorf("max retries exceeded")
}

// ============================================================================
// Chat — non-streaming completion
// ============================================================================

func (p *BaseProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.Message, error) {
	body, err := p.buildRequestBody(req, false)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.chatEndpoint(), body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setAuth(httpReq)
	p.setHeaders(httpReq)

	resp, err := p.doRequestWithRetry(ctx, httpReq, 3)
	if err != nil {
		return nil, fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("chat: HTTP %d — %s", resp.StatusCode, string(errBody))
	}

	return p.parseChatResponse(resp.Body)
}

// ============================================================================
// ChatStream — streaming completion
// ============================================================================

func (p *BaseProvider) ChatStream(ctx context.Context, req types.ChatRequest) (<-chan types.StreamEvent, error) {
	body, err := p.buildRequestBody(req, true)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.chatEndpoint(), body)
	if err != nil {
		return nil, fmt.Errorf("create stream request: %w", err)
	}
	p.setAuth(httpReq)
	p.setHeaders(httpReq)

	resp, err := p.doRequestWithRetry(ctx, httpReq, 3)
	if err != nil {
		return nil, fmt.Errorf("stream request: %w", err)
	}

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("stream: HTTP %d — %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan types.StreamEvent, 64)
	go p.readStream(resp.Body, ch)
	return ch, nil
}

func (p *BaseProvider) readStream(body io.ReadCloser, ch chan types.StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	toolCalls := make(map[int]*types.LiveToolCall)
	eventsProduced := false

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			eventsProduced = true
			ch <- types.StreamEvent{
				Type: types.EventDone,
				Meta: types.StreamMeta{FinishReason: "stop"},
			}
			return
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// Handle usage-only chunk with no choices (e.g. some providers
		// send usage in a final chunk separately from the last choice).
		if len(chunk.Choices) == 0 && chunk.Usage != nil {
			eventsProduced = true
			ch <- types.StreamEvent{
				Type: types.EventDone,
				Meta: types.StreamMeta{
					Usage: types.TokenUsage{
						PromptTokens:     chunk.Usage.PromptTokens,
						CompletionTokens: chunk.Usage.CompletionTokens,
						TotalTokens:      chunk.Usage.TotalTokens,
					},
					FinishReason: "",
					Model:        "",
				},
			}
			continue
		}

		// Handle usage in final chunk
		if chunk.Usage != nil {
			eventsProduced = true
			ch <- types.StreamEvent{
				Type: types.EventDone,
				Meta: types.StreamMeta{
					Usage: types.TokenUsage{
						PromptTokens:     chunk.Usage.PromptTokens,
						CompletionTokens: chunk.Usage.CompletionTokens,
						TotalTokens:      chunk.Usage.TotalTokens,
					},
					FinishReason: chunk.Choices[0].FinishReason,
					Model:        chunk.Model,
				},
			}
			return
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		// Text delta
		if choice.Delta.Content != "" {
			ch <- types.StreamEvent{
				Type:    types.EventText,
				Content: choice.Delta.Content,
			}
		}

		// Tool call delta
		for _, tc := range choice.Delta.ToolCalls {
			idx := tc.Index
			if existing, ok := toolCalls[idx]; ok {
				if tc.Function.Name != "" {
					existing.Name = tc.Function.Name
				}
				existing.Arguments += tc.Function.Arguments
			} else {
				ltc := &types.LiveToolCall{
					Index:     idx,
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				}
				toolCalls[idx] = ltc
				ch <- types.StreamEvent{
					Type:     types.EventToolUse,
					ToolCall: ltc,
				}
			}
		}

		if choice.FinishReason != "" {
			eventsProduced = true
			ch <- types.StreamEvent{
				Type: types.EventDone,
				Meta: types.StreamMeta{
					FinishReason: choice.FinishReason,
					Model:        chunk.Model,
				},
			}
			return
		}
	}

	// No events were produced — the provider returned a successful HTTP
	// response but NO valid SSE data. This typically means the API key is
	// missing or invalid, and the provider returned a non-streaming body.
	if !eventsProduced {
		ch <- types.StreamEvent{
			Type:    types.EventError,
			Content: "No response from provider — please check your API key in Settings → Models",
		}
	}
}

// ============================================================================
// Request building
// ============================================================================

func (p *BaseProvider) buildRequestBody(req types.ChatRequest, stream bool) (io.Reader, error) {
	messages := make([]map[string]any, 0, len(req.Messages)+1)

	// Immutable prefix: system message at position 0 for cache stability
	if req.SystemPrompt != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": req.SystemPrompt,
		})
	}

	for _, msg := range req.Messages {
		m := map[string]any{
			"role":    string(msg.Role),
			"content": msg.Content,
		}

		if len(msg.ToolCalls) > 0 {
			var tcList []map[string]any
			for _, tc := range msg.ToolCalls {
				item := map[string]any{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]any{
						"name":      tc.Name,
						"arguments": tc.Arguments,
					},
				}
				tcList = append(tcList, item)
			}
			m["tool_calls"] = tcList
		}

		if msg.ToolID != "" {
			m["tool_call_id"] = msg.ToolID
		}

		messages = append(messages, m)
	}

	body := map[string]any{
		"model":    req.Model,
		"messages": messages,
		"stream":   stream,
	}

	if len(req.CacheBreakpoints) > 0 {
		body["cache_breakpoints"] = req.CacheBreakpoints
	}

	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			})
		}
		body["tools"] = tools
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	return bytes.NewReader(data), nil
}

func (p *BaseProvider) chatEndpoint() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.apiBase + "/chat/completions"
}

func (p *BaseProvider) setAuth(req *http.Request) {
	p.mu.RLock()
	k := p.apiKey
	p.mu.RUnlock()
	if k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
}

func (p *BaseProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "iCode/0.1.0 (github.com/ponygates/icode)")
}

// ============================================================================
// Response parsing
// ============================================================================

func (p *BaseProvider) parseChatResponse(body io.Reader) (*types.Message, error) {
	var resp chatResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := resp.Choices[0]
	msg := &types.Message{
		Role:      types.RoleAssistant,
		Content:   choice.Message.Content,
		Timestamp: time.Now(),
		Metadata: types.MessageMeta{
			Model:        resp.Model,
			FinishReason: choice.FinishReason,
		},
	}

	if resp.Usage != nil {
		msg.Metadata.TokenCount = resp.Usage.TotalTokens
	}

	// Parse tool calls
	for _, tc := range choice.Message.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, types.ToolCall{
			ID:        tc.ID,
			Type:      tc.Type,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	return msg, nil
}

// ============================================================================
// JSON wire types
// ============================================================================

type chatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   *usageInfo   `json:"usage,omitempty"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message,omitempty"`
	Delta        chatDelta   `json:"delta,omitempty"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

type chatMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
}

type chatDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
}

type toolCall struct {
	Index    int              `json:"index"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function toolCallFunction `json:"function"`
}

type toolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type streamChunk struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   *usageInfo   `json:"usage,omitempty"`
}

type usageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
