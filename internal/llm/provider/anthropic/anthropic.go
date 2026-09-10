// Package anthropic implements the Anthropic Claude Provider using the native Messages API.
// Unlike OpenAI-compatible providers, Anthropic uses a different request/response format
// with explicit cache_control breakpoints for prompt caching.
package anthropic

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

const (
	ProviderName     = "anthropic"
	DefaultBase      = "https://api.anthropic.com/v1"
	AnthropicVersion = "2023-06-01"
)

// Provider implements types.Provider for Anthropic Claude.
type Provider struct {
	mu         sync.RWMutex
	apiBase    string
	apiKey     string
	httpClient *http.Client
	models     []types.ModelInfo
}

// New creates an Anthropic provider.
func New(apiKey, apiBase string) *Provider {
	if apiBase == "" {
		apiBase = DefaultBase
	}

	return &Provider{
		apiBase: apiBase,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		models: DefaultModels(),
	}
}

func (p *Provider) Name() string                  { return ProviderName }
func (p *Provider) ListModels() []types.ModelInfo { return p.models }
func (p *Provider) SupportsCache() bool           { return true }

// SetModels updates the model list (called after a live catalog refresh).
func (p *Provider) SetModels(models []types.ModelInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.models = models
}

// Defaults for a model discovered live but absent from the built-in catalogue.
// Anthropic's window is 200K, so that is the better guess here; the user can
// still edit it in the settings UI.
const (
	defaultFetchedContextWindow = 200000
	defaultFetchedMaxOutput     = 8192
)

// FetchModels queries Anthropic's /v1/models with the configured key and
// returns the models this account can actually use.
//
// The endpoint reports display_name, which labels far better than the raw id
// ("Claude Sonnet 4.5" vs "claude-sonnet-4-5-20250929"), so we keep it.
func (p *Provider) FetchModels(ctx context.Context) ([]types.ModelInfo, error) {
	p.mu.RLock()
	hasKey := p.apiKey != ""
	base := p.apiBase
	known := p.models
	p.mu.RUnlock()

	if !hasKey {
		return nil, fmt.Errorf("anthropic 未配置 API Key —— 请先在设置里填写 Key，再获取模型")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models?limit=1000", nil)
	if err != nil {
		return nil, fmt.Errorf("构造模型列表请求失败: %w", err)
	}
	p.setHeaders(req)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取模型列表失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("获取模型列表失败: HTTP %d — %s", resp.StatusCode, errBodyText(body))
	}

	var parsed struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("解析模型列表失败: %w", err)
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("anthropic 未返回任何模型")
	}

	// Reuse built-in metadata for models we already know about.
	byID := make(map[string]types.ModelInfo, len(known))
	for _, m := range known {
		byID[m.ID] = m
	}

	now := time.Now()
	out := make([]types.ModelInfo, 0, len(parsed.Data))
	for _, e := range parsed.Data {
		if e.ID == "" {
			continue
		}
		if m, ok := byID[e.ID]; ok {
			out = append(out, m)
			continue
		}
		name := e.DisplayName
		if name == "" {
			name = e.ID
		}
		out = append(out, types.ModelInfo{
			ID:              e.ID,
			Name:            name,
			Provider:        ProviderName,
			ContextWindow:   defaultFetchedContextWindow,
			MaxOutputTokens: defaultFetchedMaxOutput,
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

// SetCredentials updates the API key and base URL at runtime. Empty values are
// left unchanged. See openai_compat.BaseProvider for rationale.
func (p *Provider) SetCredentials(apiKey, apiBase string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if apiKey != "" {
		p.apiKey = apiKey
	}
	if apiBase != "" {
		p.apiBase = apiBase
	}
}

// SetTimeout updates the HTTP client timeout at runtime (used when the desktop
// UI changes a provider's per-provider timeout). Values <= 0 reset to 120s.
func (p *Provider) SetTimeout(sec int) {
	if sec <= 0 {
		sec = 120
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.httpClient.Timeout = time.Duration(sec) * time.Second
}

func (p *Provider) Health(ctx context.Context) error {
	p.mu.RLock()
	base := p.apiBase
	p.mu.RUnlock()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	p.setHeaders(req)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("health: HTTP %d — %s", resp.StatusCode, string(body))
	}
	return nil
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

// statusError converts a non-2xx response into the most specific error we can
// produce. For 429 it returns a typed types.RateLimitError carrying the
// server's own Retry-After hint — Anthropic advertises the reset via
// Retry-After and the anthropic-ratelimit-*-reset headers, and honouring that
// beats guessing a backoff.
func (p *Provider) statusError(prefix string, resp *http.Response, errBody []byte) error {
	if resp.StatusCode == http.StatusTooManyRequests {
		return &types.RateLimitError{
			Provider:   ProviderName,
			StatusCode: resp.StatusCode,
			RetryAfter: types.RetryAfterFromHeaders(resp.Header.Get),
			Body:       errBodyText(errBody),
		}
	}
	return fmt.Errorf("%s: HTTP %d — %s", prefix, resp.StatusCode, errBodyText(errBody))
}

// ============================================================================
// Chat — non-streaming Messages API
// ============================================================================

func (p *Provider) Chat(ctx context.Context, req types.ChatRequest) (*types.Message, error) {
	body, err := p.buildMessagesBody(req, false)
	if err != nil {
		return nil, err
	}

	p.mu.RLock()
	base := p.apiBase
	p.mu.RUnlock()
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/messages", body)
	p.setHeaders(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, p.statusError("anthropic", resp, errBody)
	}

	return p.parseMessagesResponse(resp.Body)
}

// ============================================================================
// ChatStream — streaming Messages API with SSE
// ============================================================================

func (p *Provider) ChatStream(ctx context.Context, req types.ChatRequest) (<-chan types.StreamEvent, error) {
	body, err := p.buildMessagesBody(req, true)
	if err != nil {
		return nil, err
	}

	p.mu.RLock()
	base := p.apiBase
	p.mu.RUnlock()
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/messages", body)
	p.setHeaders(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic stream: %w", err)
	}

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, p.statusError("anthropic stream", resp, errBody)
	}

	ch := make(chan types.StreamEvent, 64)
	go p.readSSEStream(resp.Body, ch)
	return ch, nil
}

func (p *Provider) readSSEStream(body io.ReadCloser, ch chan types.StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 128*1024), 1024*1024)

	var (
		currentBlock   string // current text block being accumulated
		toolName       string
		toolArgs       strings.Builder
		toolID         string
		currentToolIdx int = -1
		usage          *anthropicUsage
	)

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "event: "):
			eventType := strings.TrimPrefix(line, "event: ")

			switch eventType {
			case "content_block_start":
				// Next data line will have content block info
			case "content_block_delta":
				// Next data line has the delta
			case "content_block_stop":
				// Block complete
				if currentToolIdx >= 0 {
					// Tool use block finished — emit with complete args
					ch <- types.StreamEvent{
						Type: types.EventToolUse,
						ToolCall: &types.LiveToolCall{
							Index:     currentToolIdx,
							ID:        toolID,
							Name:      toolName,
							Arguments: toolArgs.String(),
						},
					}
					currentToolIdx = -1
					toolID = ""
					toolName = ""
					toolArgs.Reset()
				}
				if currentBlock != "" {
					ch <- types.StreamEvent{
						Type:    types.EventText,
						Content: currentBlock,
					}
					currentBlock = ""
				}
			case "message_delta":
				// Message-level metadata
			case "message_stop":
				meta := types.StreamMeta{
					FinishReason: "end_turn",
					Model:        "claude",
				}
				if usage != nil {
					meta.Usage = types.TokenUsage{
						PromptTokens:     usage.InputTokens,
						CompletionTokens: usage.OutputTokens,
						TotalTokens:      usage.InputTokens + usage.OutputTokens,
						CacheHitTokens:   usage.CacheReadTokens,
						CacheWriteTokens: usage.CacheCreationTokens,
					}
				}
				ch <- types.StreamEvent{Type: types.EventDone, Meta: meta}
				return
			case "error":
				ch <- types.StreamEvent{Type: types.EventError, Content: "Anthropic stream error"}
				return
			}

		case strings.HasPrefix(line, "data: "):
			data := strings.TrimPrefix(line, "data: ")
			var event map[string]any
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			eventType, _ := event["type"].(string)

			switch eventType {
			case "content_block_start":
				if cb, ok := event["content_block"].(map[string]any); ok {
					cbType, _ := cb["type"].(string)
					if cbType == "tool_use" {
						currentToolIdx++
						if id, ok := cb["id"].(string); ok {
							toolID = id
						}
						if name, ok := cb["name"].(string); ok {
							toolName = name
						}
						toolArgs.Reset()

						ch <- types.StreamEvent{
							Type: types.EventToolUse,
							ToolCall: &types.LiveToolCall{
								Index: currentToolIdx,
								ID:    toolID,
								Name:  toolName,
							},
						}
					}
				}

			case "content_block_delta":
				if delta, ok := event["delta"].(map[string]any); ok {
					deltaType, _ := delta["type"].(string)
					switch deltaType {
					case "text_delta":
						if text, ok := delta["text"].(string); ok {
							currentBlock += text
							ch <- types.StreamEvent{
								Type:    types.EventText,
								Content: text,
							}
						}
					case "input_json_delta":
						if partial, ok := delta["partial_json"].(string); ok {
							toolArgs.WriteString(partial)
						}
					}
				}

			case "message_start":
				if msg, ok := event["message"].(map[string]any); ok {
					if u, ok := msg["usage"].(map[string]any); ok {
						usage = parseUsage(u)
					}
				}

			case "message_delta":
				if u, ok := event["usage"].(map[string]any); ok {
					if usage == nil {
						usage = &anthropicUsage{}
					}
					if v, ok := u["output_tokens"].(float64); ok {
						usage.OutputTokens = int(v)
					}
				}

			case "ping":
				// Keep-alive, ignore
			}
		}
	}

}

func parseUsage(u map[string]any) *anthropicUsage {
	usage := &anthropicUsage{}
	if v, ok := u["input_tokens"].(float64); ok {
		usage.InputTokens = int(v)
	}
	if v, ok := u["output_tokens"].(float64); ok {
		usage.OutputTokens = int(v)
	}
	if v, ok := u["cache_read_input_tokens"].(float64); ok {
		usage.CacheReadTokens = int(v)
	}
	if v, ok := u["cache_creation_input_tokens"].(float64); ok {
		usage.CacheCreationTokens = int(v)
	}
	return usage
}

// ============================================================================
// Request building — Anthropic Messages format
// ============================================================================

func (p *Provider) buildMessagesBody(req types.ChatRequest, stream bool) (io.Reader, error) {
	// Convert OpenAI-format messages to Anthropic format
	var messages []map[string]any
	for _, msg := range req.Messages {
		m := map[string]any{
			"role": string(msg.Role),
		}

		// Handle different content formats
		if len(msg.ToolCalls) > 0 {
			// Assistant message with tool_use blocks
			var content []map[string]any
			for _, tc := range msg.ToolCalls {
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": json.RawMessage(tc.Arguments),
				})
			}
			if msg.Content != "" {
				content = append(content, map[string]any{
					"type": "text",
					"text": msg.Content,
				})
			}
			m["content"] = content
		} else if msg.ToolID != "" {
			// Tool result message
			m["content"] = []map[string]any{
				{
					"type":        "tool_result",
					"tool_use_id": msg.ToolID,
					"content":     msg.Content,
				},
			}
		} else {
			m["content"] = anthropicContent(msg)
		}
		messages = append(messages, m)
	}

	// System prompt
	var systemContent []map[string]any
	if req.SystemPrompt != "" {
		systemContent = append(systemContent, map[string]any{
			"type": "text",
			"text": req.SystemPrompt,
		})
	}

	body := map[string]any{
		"model":    req.Model,
		"messages": messages,
		"stream":   stream,
	}

	if len(systemContent) > 0 {
		body["system"] = systemContent

		// Add cache_control to system prompt for prompt caching
		if p.SupportsCache() {
			if sc, ok := body["system"].([]map[string]any); ok && len(sc) > 0 {
				cc := map[string]string{"type": "ephemeral"}
				if req.CacheTTL != "" {
					cc["ttl"] = req.CacheTTL
				}
				sc[len(sc)-1]["cache_control"] = cc
			}
		}
	}

	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	} else {
		body["max_tokens"] = 8192
	}

	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}

	// Extended thinking (Claude Messages API "thinking" block). When enabled,
	// Anthropic requires temperature to stay at its default (1) — suppress any
	// configured temperature so the API never rejects the call. budget_tokens
	// must be < max_tokens; clamp it to max_tokens/2 (>= 1024) as a safety net
	// so a misconfigured budget can never 400 the request.
	if req.Thinking != nil && req.Thinking.BudgetTokens > 0 {
		budget := req.Thinking.BudgetTokens
		maxTok := req.MaxTokens
		if maxTok <= 0 {
			maxTok = 8192
		}
		if budget >= maxTok {
			budget = maxTok / 2
		}
		if budget < 1024 {
			budget = 1024
		}
		body["thinking"] = map[string]any{
			"type":          "enabled",
			"budget_tokens": budget,
		}
		delete(body, "temperature")
	}

	// Tool definitions in Anthropic format
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.Parameters,
			})
		}
		body["tools"] = tools

		// Add cache_control to last tool for prompt caching
		if p.SupportsCache() && len(tools) > 0 {
			tools[len(tools)-1]["cache_control"] = map[string]string{"type": "ephemeral"}
		}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	return bytes.NewReader(data), nil
}

// anthropicContent renders a message's content for the Anthropic Messages API.
// Image attachments on a normal message are emitted as image source blocks
// alongside the text; messages without attachments stay plain text so existing
// conversations are unaffected.
func anthropicContent(msg types.Message) any {
	if len(msg.Attachments) == 0 {
		return msg.Content
	}
	var blocks []map[string]any
	if msg.Content != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": msg.Content})
	}
	for _, att := range msg.Attachments {
		if att.Type == "image" && att.Data != "" {
			mt := att.MIMEType
			if mt == "" {
				mt = "image/png"
			}
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": mt,
					"data":       att.Data,
				},
			})
		}
	}
	if len(blocks) > 0 {
		return blocks
	}
	return msg.Content
}

func (p *Provider) setHeaders(req *http.Request) {
	p.mu.RLock()
	k := p.apiKey
	p.mu.RUnlock()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", k)
	req.Header.Set("anthropic-version", AnthropicVersion)
	req.Header.Set("User-Agent", "iCode/0.1.0")
}

// ============================================================================
// Response parsing
// ============================================================================

func (p *Provider) parseMessagesResponse(body io.Reader) (*types.Message, error) {
	var resp messagesResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}

	msg := &types.Message{
		Role:      types.RoleAssistant,
		Timestamp: time.Now(),
		Metadata: types.MessageMeta{
			Model:        resp.Model,
			FinishReason: resp.StopReason,
		},
	}

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			msg.Content += block.Text
		case "tool_use":
			argsJSON, _ := json.Marshal(block.Input)
			msg.ToolCalls = append(msg.ToolCalls, types.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(argsJSON),
			})
		}
	}

	if resp.Usage.InputTokens > 0 {
		msg.Metadata.TokenCount = resp.Usage.InputTokens + resp.Usage.OutputTokens
	}

	return msg, nil
}

// ============================================================================
// Wire types
// ============================================================================

type messagesResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      anthropicUsage `json:"usage"`
}

type contentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text,omitempty"`
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
}

type anthropicUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
}

// ============================================================================
// Default models
// ============================================================================

func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:              "claude-fable-5",
			Name:            "Claude Fable 5",
			Description:     "Anthropic 最强模型，为长时运行 agent 提供下一代智能（2026-06 GA，1M 上下文）",
			Provider:        ProviderName,
			ContextWindow:   1000000,
			MaxOutputTokens: 128000,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "Standard coding plan with prompt caching",
					InputPrice:  10.0,
					OutputPrice: 50.0,
					CachePrice:  1.0,
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  false,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
		{
			ID:              "claude-opus-5",
			Name:            "Claude Opus 5",
			Description:     "复杂 agentic 编码与企业级工作负载的首选（1M 上下文）",
			Provider:        ProviderName,
			ContextWindow:   1000000,
			MaxOutputTokens: 128000,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "Standard coding plan with prompt caching",
					InputPrice:  5.0,
					OutputPrice: 25.0,
					CachePrice:  0.5,
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  false,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
		{
			ID:              "claude-sonnet-5",
			Name:            "Claude Sonnet 5",
			Description:     "速度与智能的最佳平衡，日常编码主力（1M 上下文，训练截止 2026-01）",
			Provider:        ProviderName,
			ContextWindow:   1000000,
			MaxOutputTokens: 128000,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "Standard coding plan with prompt caching",
					InputPrice:  3.0,
					OutputPrice: 15.0,
					CachePrice:  0.3,
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  false,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
		{
			ID:              "claude-haiku-4-5-20251001",
			Name:            "Claude Haiku 4.5",
			Description:     "最快、近前沿智能，成本最优（200k 上下文，训练截止 2025-02）",
			Provider:        ProviderName,
			ContextWindow:   200000,
			MaxOutputTokens: 64000,
			Plans: []types.TokenPlan{
				{
					Name:        "token-plan",
					Description: "Cost-optimized plan",
					InputPrice:  1.0,
					OutputPrice: 5.0,
					CachePrice:  0.1,
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
	}
}
