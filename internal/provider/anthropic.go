package provider

import (
	"context"
	"encoding/json"
	"strings"
)

type AnthropicProvider struct {
	name      string
	models    []string
	client    *HTTPClient
	cacheInfo CacheInfo
	apiKey    string
}

type anthropicReq struct {
	Model         string             `json:"model"`
	MaxTokens     int                `json:"max_tokens,omitempty"`
	System        string             `json:"system,omitempty"`
	Messages      []anthropicMsg     `json:"messages"`
	Tools         []anthropicTool    `json:"tools,omitempty"`
	Stream        bool               `json:"stream"`
}

type anthropicMsg struct {
	Role    string              `json:"role"`
	Content []anthropicContent  `json:"content"`
}

type anthropicContent struct {
	Type   string            `json:"type"`
	Text   string            `json:"text,omitempty"`
	ID     string            `json:"id,omitempty"`
	Name   string            `json:"name,omitempty"`
	Input  json.RawMessage   `json:"input,omitempty"`
}

type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

type anthropicResp struct {
	ID      string            `json:"id"`
	Type    string            `json:"type"`
	Role    string            `json:"role"`
	Content []anthropicContent `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage   *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func NewAnthropicProvider(apiKey string, models []string) *AnthropicProvider {
	return &AnthropicProvider{
		name:   "anthropic",
		models: models,
		client: NewHTTPClient("https://api.anthropic.com/v1", "", WithHeader("x-api-key", apiKey), WithHeader("anthropic-version", "2023-06-01")),
		apiKey: apiKey,
		cacheInfo: CacheInfo{
			Provider: "anthropic",
			Strategy: "append-only",
		},
	}
}

func (p *AnthropicProvider) Name() string             { return p.name }
func (p *AnthropicProvider) Models() []string          { return p.models }
func (p *AnthropicProvider) CacheInfo() CacheInfo      { return p.cacheInfo }

func (p *AnthropicProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	anthropicReq := p.toAnthropicReq(req)
	anthropicReq.Stream = false

	var resp anthropicResp
	if err := p.client.PostJSON("/messages", anthropicReq, &resp); err != nil {
		return nil, err
	}

	return p.fromAnthropicResp(&resp), nil
}

func (p *AnthropicProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
	anthropicReq := p.toAnthropicReq(req)
	anthropicReq.Stream = true

	rawCh, err := p.client.PostSSE("/messages", anthropicReq)
	if err != nil {
		return nil, err
	}

	out := make(chan StreamChunk, 64)
	go func() {
		defer close(out)
		for raw := range rawCh {
			chunk := p.parseSSE(raw)
			if chunk != nil {
				out <- *chunk
				if chunk.Done {
					return
				}
			}
		}
		out <- StreamChunk{Done: true}
	}()

	return out, nil
}

func (p *AnthropicProvider) Cost(req CompletionRequest, usage Usage) float64 {
	return 0
}

func (p *AnthropicProvider) toAnthropicReq(req CompletionRequest) anthropicReq {
	var system string
	var msgs []anthropicMsg

	for _, m := range req.Messages {
		if m.Role == "system" {
			system += m.Content + "\n"
			continue
		}
		content := []anthropicContent{{Type: "text", Text: m.Content}}
		msgs = append(msgs, anthropicMsg{Role: m.Role, Content: content})
	}

	tools := make([]anthropicTool, len(req.Tools))
	for i, t := range req.Tools {
		tools[i] = anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		}
	}

	return anthropicReq{
		Model:     req.Model,
		Messages:  msgs,
		System:    strings.TrimSpace(system),
		Tools:     tools,
		Stream:    req.Stream,
		MaxTokens: req.MaxTokens,
	}
}

func (p *AnthropicProvider) fromAnthropicResp(resp *anthropicResp) *CompletionResponse {
	out := &CompletionResponse{}
	for _, c := range resp.Content {
		switch c.Type {
		case "text":
			out.Content += c.Text
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:   c.ID,
				Type: "function",
				Function: Function{
					Name:      c.Name,
					Arguments: string(c.Input),
				},
			})
		}
	}
	if resp.Usage != nil {
		out.Usage = Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		}
	}
	return out
}

func (p *AnthropicProvider) parseSSE(raw string) *StreamChunk {
	var event struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return nil
	}

	switch event.Type {
	case "content_block_delta":
		var delta struct {
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(raw), &delta); err != nil {
			return nil
		}
		return &StreamChunk{Content: delta.Delta.Text}

	case "content_block_start":
		var block struct {
			ContentBlock struct {
				Type string          `json:"type"`
				Name string          `json:"name"`
				ID   string          `json:"id"`
				Input json.RawMessage `json:"input"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(raw), &block); err != nil {
			return nil
		}
		if block.ContentBlock.Type == "tool_use" {
			return &StreamChunk{
				ToolCalls: []ToolCall{{
					ID:   block.ContentBlock.ID,
					Type: "function",
					Function: Function{
						Name:      block.ContentBlock.Name,
						Arguments: string(block.ContentBlock.Input),
					},
				}},
			}
		}

	case "message_delta":
		var delta struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(raw), &delta); err != nil {
			return nil
		}
		return &StreamChunk{Done: true}

	case "message_stop":
		return &StreamChunk{Done: true}
	}

	return nil
}
