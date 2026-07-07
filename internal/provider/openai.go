package provider

import (
	"context"
	"encoding/json"
)

type OpenAIProvider struct {
	name      string
	models    []string
	client    *HTTPClient
	cacheInfo CacheInfo
}

type openAIReqMsg struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string     `json:"id"`
	Type     string     `json:"type"`
	Function openAIFunc `json:"function"`
}

type openAIFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIReqTool struct {
	Type     string       `json:"type"`
	Function openAIToolFn `json:"function"`
}

type openAIToolFn struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type openAIReq struct {
	Model       string          `json:"model"`
	Messages    []openAIReqMsg  `json:"messages"`
	Tools       []openAIReqTool `json:"tools,omitempty"`
	Stream      bool            `json:"stream"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
}

type openAIResp struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
}

type openAIChoice struct {
	Index        int        `json:"index"`
	Message      openAIMsg  `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type openAIMsg struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type OpenAIProviderConfig struct {
	Name    string
	BaseURL string
	APIKey  string
	Models  []string
	Headers map[string]string
}

func NewOpenAIProvider(cfg OpenAIProviderConfig) *OpenAIProvider {
	opts := []func(*HTTPClient){}
	for k, v := range cfg.Headers {
		opts = append(opts, WithHeader(k, v))
	}
	return &OpenAIProvider{
		name:   cfg.Name,
		models: cfg.Models,
		client: NewHTTPClient(cfg.BaseURL, cfg.APIKey, opts...),
		cacheInfo: CacheInfo{
			Provider: cfg.Name,
			Strategy: "append-only",
		},
	}
}

func (p *OpenAIProvider) Name() string             { return p.name }
func (p *OpenAIProvider) Models() []string          { return p.models }
func (p *OpenAIProvider) CacheInfo() CacheInfo      { return p.cacheInfo }

func (p *OpenAIProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	oaiReq := p.toOpenAIReq(req)
	oaiReq.Stream = false

	var oaiResp openAIResp
	if err := p.client.PostJSON("/chat/completions", oaiReq, &oaiResp); err != nil {
		return nil, err
	}

	return p.fromOpenAIResp(&oaiResp), nil
}

func (p *OpenAIProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
	oaiReq := p.toOpenAIReq(req)
	oaiReq.Stream = true

	rawCh, err := p.client.PostSSE("/chat/completions", oaiReq)
	if err != nil {
		return nil, err
	}

	out := make(chan StreamChunk, 64)
	go func() {
		defer close(out)

		var toolCallAccumulators map[int]*accumulatedToolCall
		contentBuf := ""

		for raw := range rawCh {
			chunk := p.parseStreamChunk(raw)
			if chunk == nil {
				continue
			}

			contentBuf += chunk.content
			if chunk.content != "" {
				out <- StreamChunk{Content: chunk.content}
			}

			for _, tc := range chunk.toolCalls {
				if toolCallAccumulators == nil {
					toolCallAccumulators = make(map[int]*accumulatedToolCall)
				}
				acc, ok := toolCallAccumulators[tc.index]
				if !ok {
					acc = &accumulatedToolCall{}
					toolCallAccumulators[tc.index] = acc
				}
				if tc.id != "" {
					acc.id = tc.id
				}
				if tc.fnName != "" {
					acc.fnName = tc.fnName
				}
				acc.args += tc.args
			}

			if chunk.done {
				if len(toolCallAccumulators) > 0 {
					var tcs []ToolCall
					for _, acc := range toolCallAccumulators {
						tcs = append(tcs, ToolCall{
							ID:   acc.id,
							Type: "function",
							Function: Function{
								Name:      acc.fnName,
								Arguments: acc.args,
							},
						})
					}
					if len(tcs) > 0 {
						out <- StreamChunk{ToolCalls: tcs}
					}
				}
				out <- StreamChunk{Done: true}
				return
			}
		}
		out <- StreamChunk{Done: true}
	}()

	return out, nil
}

func (p *OpenAIProvider) Cost(req CompletionRequest, usage Usage) float64 {
	return 0
}

type accumulatedToolCall struct {
	id     string
	fnName string
	args   string
}

type parsedStreamContent struct {
	content   string
	toolCalls []parsedToolCall
	done      bool
}

type parsedToolCall struct {
	index int
	id    string
	fnName string
	args   string
}

func (p *OpenAIProvider) parseStreamChunk(raw string) *parsedStreamContent {
	var oaiChunk struct {
		Choices []struct {
			Delta struct {
				Content   string           `json:"content"`
				ToolCalls []openAIToolCall `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(raw), &oaiChunk); err != nil {
		return nil
	}
	if len(oaiChunk.Choices) == 0 {
		return nil
	}
	c := oaiChunk.Choices[0]
	out := &parsedStreamContent{
		content: c.Delta.Content,
	}
	for i, tc := range c.Delta.ToolCalls {
		out.toolCalls = append(out.toolCalls, parsedToolCall{
			index:  i,
			id:     tc.ID,
			fnName: tc.Function.Name,
			args:   tc.Function.Arguments,
		})
	}
	if c.FinishReason != nil && *c.FinishReason != "" {
		out.done = true
	}
	return out
}

func (p *OpenAIProvider) toOpenAIReq(req CompletionRequest) openAIReq {
	msgs := make([]openAIReqMsg, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = openAIReqMsg{Role: m.Role, Content: m.Content}
	}
	tools := make([]openAIReqTool, len(req.Tools))
	for i, t := range req.Tools {
		tools[i] = openAIReqTool{
			Type: "function",
			Function: openAIToolFn{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		}
	}
	return openAIReq{
		Model:       req.Model,
		Messages:    msgs,
		Tools:       tools,
		Stream:      req.Stream,
		MaxTokens:   req.MaxTokens,
		Temperature: 0.7,
	}
}

func (p *OpenAIProvider) fromOpenAIResp(resp *openAIResp) *CompletionResponse {
	out := &CompletionResponse{}
	if len(resp.Choices) == 0 {
		return out
	}
	c := resp.Choices[0]
	out.Content = c.Message.Content
	for _, tc := range c.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: Function{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	if resp.Usage != nil {
		out.Usage = Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		}
	}
	return out
}
