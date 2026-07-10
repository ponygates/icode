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
	ProviderName = "anthropic"
	DefaultBase  = "https://api.anthropic.com/v1"
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

func (p *Provider) Name() string                             { return ProviderName }
func (p *Provider) ListModels() []types.ModelInfo            { return p.models }
func (p *Provider) SupportsCache() bool                      { return true }

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
		return nil, fmt.Errorf("anthropic: HTTP %d — %s", resp.StatusCode, string(errBody))
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
		return nil, fmt.Errorf("anthropic stream: HTTP %d — %s", resp.StatusCode, string(errBody))
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
		currentBlock   string   // current text block being accumulated
		toolName       string
		toolArgs       strings.Builder
		toolID         string
		toolIndex      int
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
				if currentBlock != "" && currentToolIdx < 0 {
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

	_ = toolID
	_ = toolArgs
	_ = toolIndex
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
			m["content"] = msg.Content
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
				sc[len(sc)-1]["cache_control"] = map[string]string{"type": "ephemeral"}
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
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Role       string            `json:"role"`
	Model      string            `json:"model"`
	Content    []contentBlock    `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      anthropicUsage    `json:"usage"`
}

type contentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text,omitempty"`
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
}

type anthropicUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	CacheReadTokens    int `json:"cache_read_input_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
}

// ============================================================================
// Default models
// ============================================================================

func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:              "claude-sonnet-4-20250514",
			Name:            "Claude Sonnet 4",
			Description:     "Anthropic latest coding model, excellent at code generation and reasoning",
			Provider:        ProviderName,
			ContextWindow:   200000,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "Standard coding plan with prompt caching",
					InputPrice:  3.0,
					OutputPrice: 15.0,
					CachePrice:  0.30,
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
			ID:              "claude-haiku-4-20250514",
			Name:            "Claude Haiku 4",
			Description:     "Fast and cost-effective Claude model",
			Provider:        ProviderName,
			ContextWindow:   200000,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{
					Name:        "token-plan",
					Description: "Cost-optimized plan",
					InputPrice:  0.80,
					OutputPrice: 4.0,
					CachePrice:  0.08,
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
	}
}
