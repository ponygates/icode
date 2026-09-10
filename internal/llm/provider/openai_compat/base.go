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

	"github.com/ponygates/icode/internal/llm/modelmeta"
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
			Transport: &http.Transport{
				// Honor HTTP_PROXY / HTTPS_PROXY / NO_PROXY so users behind a
				// proxy (e.g. reaching OpenRouter from restricted networks) work.
				// When no proxy env is set, ProxyFromEnvironment returns nil -> direct.
				Proxy: http.ProxyFromEnvironment,
			},
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
	p.mu.RLock()
	hasKey := p.apiKey != ""
	p.mu.RUnlock()

	// Skip health check when no API key is configured — the provider may
	// still work for free-tier models once a key is added.
	if !hasKey {
		return nil
	}

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
		http.StatusServiceUnavailable, // 503
		http.StatusBadGateway,         // 502
		http.StatusGatewayTimeout:     // 504
		return true
	}
	return false
}

// maxProviderWait bounds how long a single in-provider retry may sleep. A
// provider that asks for longer than this is handed straight back (see
// doRequestWithRetry) so the engine — which owns the user-facing retry policy
// and reports progress — performs the wait instead of us burning an attempt.
const maxProviderWait = 30 * time.Second

// doRequestWithRetry executes an HTTP request with exponential backoff.
// It retries on transient network errors and retryable HTTP status codes
// (429, 502, 503, 504) with up to maxRetries attempts.
//
// For rate limits the provider's own Retry-After hint wins over the default
// 100ms-doubling schedule: that schedule is far too eager for a real 429 and
// re-failing early both wastes an attempt and deepens the limit.
func (p *BaseProvider) doRequestWithRetry(ctx context.Context, httpReq *http.Request, maxRetries int) (*http.Response, error) {
	backoff := 100 * time.Millisecond

	// Snapshot the body once, for bodies http.NewRequest cannot rewind on its
	// own (GetBody is only installed for *bytes.Buffer, *bytes.Reader and
	// *strings.Reader).
	var bodyBytes []byte
	if httpReq.Body != nil && httpReq.GetBody == nil {
		b, err := io.ReadAll(httpReq.Body)
		if err != nil {
			return nil, fmt.Errorf("snapshot request body: %w", err)
		}
		httpReq.Body.Close()
		bodyBytes = b
		httpReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// The transport closes the request body after every attempt, so a
		// retry must be handed a *fresh* one. The previous implementation
		// re-read httpReq.Body at the top of each iteration instead, which
		// yielded an empty body on retry while ContentLength still said 80 —
		// the request then failed locally with "ContentLength=80 with Body
		// length 0", so the retry never actually reached the provider. This
		// silently disabled retries for every body-carrying POST, which is to
		// say every chat and stream request.
		if attempt > 0 {
			if httpReq.GetBody != nil {
				nb, err := httpReq.GetBody()
				if err != nil {
					return nil, fmt.Errorf("rewind request body for retry: %w", err)
				}
				httpReq.Body = nb
			} else if bodyBytes != nil {
				httpReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}
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
			// Let the provider's own Retry-After hint override our schedule.
			if hint := types.RetryAfterFromHeaders(resp.Header.Get); hint > 0 {
				if hint > maxProviderWait {
					// Too long to absorb here — hand the response back so
					// ChatStream surfaces a types.RateLimitError carrying the
					// hint, and the engine performs the wait (it can report
					// progress to the user; we cannot).
					return resp, nil
				}
				if hint > backoff {
					backoff = hint
				}
			}
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

// maxErrBody caps how much of a provider error body we echo into an error.
const maxErrBody = 400

// errBodyText trims and truncates a provider error body for error messages.
func errBodyText(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > maxErrBody {
		return s[:maxErrBody] + "…"
	}
	return s
}

// httpStatusError converts a non-2xx response into the most specific error we
// can produce: a typed types.RateLimitError (carrying the server's own
// Retry-After hint when present) for 429, a plain descriptive error otherwise.
// Carrying the hint is what lets the engine honour the provider's schedule
// instead of guessing.
func (p *BaseProvider) httpStatusError(prefix string, resp *http.Response, errBody []byte) error {
	if resp.StatusCode == http.StatusTooManyRequests {
		return &types.RateLimitError{
			Provider:   p.name,
			StatusCode: resp.StatusCode,
			RetryAfter: types.RetryAfterFromHeaders(resp.Header.Get),
			Body:       errBodyText(errBody),
		}
	}
	return fmt.Errorf("%s: HTTP %d — %s", prefix, resp.StatusCode, errBodyText(errBody))
}

// ============================================================================
// Chat — non-streaming completion
// ============================================================================

func (p *BaseProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.Message, error) {
	p.mu.RLock()
	hasKey := p.apiKey != ""
	p.mu.RUnlock()
	if !hasKey {
		return nil, fmt.Errorf("API key not configured for %s — go to Settings (Ctrl+,) to add your API key", p.name)
	}

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
		return nil, p.httpStatusError("chat", resp, errBody)
	}

	return p.parseChatResponse(resp.Body)
}

// ============================================================================
// ChatStream — streaming completion
// ============================================================================

func (p *BaseProvider) ChatStream(ctx context.Context, req types.ChatRequest) (<-chan types.StreamEvent, error) {
	p.mu.RLock()
	hasKey := p.apiKey != ""
	p.mu.RUnlock()
	if !hasKey {
		return nil, fmt.Errorf("API key not configured for %s — go to Settings (Ctrl+,) to add your API key", p.name)
	}

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
		return nil, p.httpStatusError("stream", resp, errBody)
	}

	ch := make(chan types.StreamEvent, 64)
	go p.readStream(ctx, resp.Body, ch)
	return ch, nil
}

// ============================================================================
// FetchModels — live model discovery from the vendor
// ============================================================================

// maxModelsBody caps how much of a /models response we read. OpenRouter's
// catalogue is multi-megabyte, so the limit is generous but finite.
const maxModelsBody = 8 << 20

// Defaults for a model discovered live but absent from the built-in
// catalogue. Deliberately conservative — the user can edit them in the
// settings UI once the real limits are known.
const (
	defaultFetchedContextWindow = 128000
	defaultFetchedMaxOutput     = 8192
)

// FetchModels queries the vendor's /models endpoint with this provider's
// credentials and returns the models the key can actually use.
//
// The built-in catalogue is a snapshot baked in at build time; the vendor's
// endpoint is the authority on what this account is entitled to, which varies
// by plan and region and changes whenever the vendor ships a model. The
// settings UI drives this from the per-provider "fetch models" action.
func (p *BaseProvider) FetchModels(ctx context.Context) ([]types.ModelInfo, error) {
	p.mu.RLock()
	hasKey := p.apiKey != ""
	apiBase := p.apiBase
	name := p.name
	known := p.models
	p.mu.RUnlock()

	if !hasKey {
		return nil, fmt.Errorf("%s 未配置 API Key —— 请先在设置里填写 Key，再获取模型", name)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("构造模型列表请求失败: %w", err)
	}
	p.setAuth(req)
	p.setHeaders(req)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取模型列表失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxModelsBody))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("获取模型列表失败: HTTP %d — %s", resp.StatusCode, errBodyText(body))
	}

	entries := modelmeta.FilterChatModels(modelmeta.ParseVendorList(body))
	if len(entries) == 0 {
		return nil, fmt.Errorf("厂商未返回任何模型（响应格式无法识别）")
	}

	// Reuse the built-in catalogue's metadata for models we already know so the
	// UI keeps showing real context windows and prices. For models the vendor
	// reports but this build has never heard of, prefer the vendor's own
	// context window / max output; fall back to the conservative defaults only
	// when the vendor stays silent.
	byID := make(map[string]types.ModelInfo, len(known)*2)
	for _, m := range known {
		byID[m.ID] = m
		byID[strings.TrimPrefix(m.ID, name+"/")] = m
	}

	now := time.Now()
	out := make([]types.ModelInfo, 0, len(entries))
	for _, e := range entries {
		if m, ok := byID[e.ID]; ok {
			out = append(out, m)
			continue
		}
		ctxWindow := e.ContextWindow
		if ctxWindow <= 0 {
			ctxWindow = defaultFetchedContextWindow
		}
		maxOut := e.MaxOutputTokens
		if maxOut <= 0 {
			maxOut = defaultFetchedMaxOutput
		}
		out = append(out, types.ModelInfo{
			ID:              e.ID,
			Name:            e.ID,
			Provider:        name,
			ContextWindow:   ctxWindow,
			MaxOutputTokens: maxOut,
			Plans: []types.TokenPlan{{
				Name:        "default",
				Description: "由厂商 /models 实时获取；定价请以厂商为准",
			}},
			Capabilities: types.ModelCap{Tools: true, Streaming: true, JSONMode: true},
			UpdatedAt:    now,
		})
	}
	return out, nil
}

// parseVendorModelIDs extracts model identifiers from a /models response,
// tolerating the shapes seen in the wild:
//
//	{"data":[{"id":"gpt-4o"}], "object":"list"}   OpenAI, DeepSeek, Moonshot, …
//	{"models":[{"name":"llama3:latest"}]}         Ollama's native /api/tags
//	["gpt-4o","gpt-4o-mini"]                      a bare array of ids
//
// Vendor order is preserved and duplicates are dropped. Metadata parsing lives
// in modelmeta.ParseVendorList so both ingestion paths share one parser; this
// thin wrapper remains for the id-only callers and their tests.
func parseVendorModelIDs(body []byte) []string {
	entries := modelmeta.ParseVendorList(body)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

// readStream pumps SSE lines from the response body into ch until EOF, an
// error, or ctx cancellation. The context matters: when the user interrupts
// (Esc / stop button) the engine cancels it, and the HTTP transport closes
// the body — but the bufio.Scanner may still be parked on a read, so we
// also select on ctx.Done() and abort the scan early instead of blocking
// until the provider notices.
func (p *BaseProvider) readStream(ctx context.Context, body io.ReadCloser, ch chan types.StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	toolCalls := make(map[int]*types.LiveToolCall)
	eventsProduced := false

	// Drain the scanner on a dedicated goroutine; the main loop selects on
	// ctx.Done() so an interrupt never waits for the provider's SSE to end.
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				// Scanner finished (EOF or error). Surface a scan error if the
				// context is still alive; otherwise emit the no-response error
				// for a stream that produced nothing.
				if err := scanner.Err(); err != nil && !eventsProduced {
					ch <- types.StreamEvent{Type: types.EventError, Content: err.Error()}
				} else if !eventsProduced {
					ch <- types.StreamEvent{
						Type:    types.EventError,
						Content: "No response from provider — please check your API key in Settings → Models",
					}
				}
				return
			}
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

			// Reasoning / thinking delta (DeepSeek R1, Qwen, etc.)
			if choice.Delta.ReasoningContent != "" {
				ch <- types.StreamEvent{
					Type:    types.EventThinking,
					Content: choice.Delta.ReasoningContent,
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
	}
}

// ============================================================================
// Request building
// ============================================================================

func (p *BaseProvider) buildRequestBody(req types.ChatRequest, stream bool) (io.Reader, error) {
	messages := make([]map[string]any, 0, len(req.Messages)+1)

	// Immutable prefix: system message at position 0 for cache stability
	hadSystem := req.SystemPrompt != ""
	if hadSystem {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": req.SystemPrompt,
		})
	}

	for _, msg := range req.Messages {
		m := map[string]any{
			"role": string(msg.Role),
		}
		if content := openAIContent(msg); content != nil {
			m["content"] = content
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

	// cache_breakpoints are message-log indices produced by the optimizer, where
	// 0 marks the immutable prefix boundary. buildRequestBody inserts the system
	// prompt at index 0 when present, so any breakpoint past the prefix must be
	// shifted +1 to still point at the intended conversation message on the
	// wire. The prefix marker (0) already coincides with the system message and
	// needs no shift.
	if hadSystem && len(req.CacheBreakpoints) > 0 {
		shifted := make([]int, 0, len(req.CacheBreakpoints))
		for _, bp := range req.CacheBreakpoints {
			if bp == 0 {
				shifted = append(shifted, 0)
			} else {
				shifted = append(shifted, bp+1)
			}
		}
		body["cache_breakpoints"] = shifted
	} else if !hadSystem && len(req.CacheBreakpoints) > 0 {
		body["cache_breakpoints"] = req.CacheBreakpoints
	}

	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	// nil = not configured (let the provider default stand). A pointer means a
	// value was deliberately chosen, and 0 is a valid one.
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP > 0 {
		body["top_p"] = req.TopP
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
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
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

// openAIContent renders a message's content for the OpenAI Chat Completions
// format. When a message carries image attachments and is a normal (non-tool)
// message, the content becomes a content-part array: the text (if present)
// followed by image_url parts. Tool and tool-result messages, and any
// non-image attachment types, are left as plain text so existing text
// conversations are never disturbed.
func openAIContent(msg types.Message) any {
	if msg.Role == types.RoleTool || len(msg.ToolCalls) > 0 || len(msg.Attachments) == 0 {
		return msg.Content
	}
	var parts []map[string]any
	if strings.TrimSpace(msg.Content) != "" {
		parts = append(parts, map[string]any{"type": "text", "text": msg.Content})
	}
	for _, att := range msg.Attachments {
		if att.Type == "image" && att.Data != "" {
			mime := att.MIMEType
			if mime == "" {
				mime = "image/png"
			}
			parts = append(parts, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url": fmt.Sprintf("data:%s;base64,%s", mime, att.Data),
				},
			})
		}
	}
	if len(parts) > 0 {
		return parts
	}
	return msg.Content
}
