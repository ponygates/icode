package provider

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type ZhipuProvider struct {
	name      string
	models    []string
	client    *HTTPClient
	cacheInfo CacheInfo
	apiKey    string
}

type zhipuReq struct {
	Model       string          `json:"model"`
	Messages    []openAIReqMsg  `json:"messages"`
	Tools       []openAIReqTool `json:"tools,omitempty"`
	Stream      bool            `json:"stream"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
}

type zhipuResp struct {
	ID      string         `json:"id"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
}

func NewZhipuProvider(apiKey string, models []string) *ZhipuProvider {
	p := &ZhipuProvider{
		name:   "zhipu",
		models: models,
		apiKey: apiKey,
		cacheInfo: CacheInfo{
			Provider: "zhipu",
			Strategy: "append-only",
		},
	}
	p.client = NewHTTPClient("https://open.bigmodel.cn/api/paas/v4", "",
		WithTokenGen(func() string { return p.generateToken() }),
	)
	return p
}

func (p *ZhipuProvider) Name() string             { return p.name }
func (p *ZhipuProvider) Models() []string          { return p.models }
func (p *ZhipuProvider) CacheInfo() CacheInfo      { return p.cacheInfo }

func (p *ZhipuProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	zReq := p.toZhipuReq(req)
	zReq.Stream = false

	var resp zhipuResp
	if err := p.client.PostJSON("/chat/completions", zReq, &resp); err != nil {
		return nil, err
	}

	return p.fromZhipuResp(&resp), nil
}

func (p *ZhipuProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
	zReq := p.toZhipuReq(req)
	zReq.Stream = true

	out := make(chan StreamChunk, 64)

	go func() {
		defer close(out)

		zReq.Stream = false
		resp, err := p.Complete(ctx, req)
		if err != nil {
			return
		}

		if resp.Content != "" {
			out <- StreamChunk{Content: resp.Content}
		}
		if len(resp.ToolCalls) > 0 {
			out <- StreamChunk{ToolCalls: resp.ToolCalls}
		}
		out <- StreamChunk{Done: true}
	}()

	return out, nil
}

func (p *ZhipuProvider) Cost(req CompletionRequest, usage Usage) float64 {
	return 0
}

func (p *ZhipuProvider) generateToken() string {
	parts := splitAPIKey(p.apiKey)
	if len(parts) != 2 {
		return p.apiKey
	}
	id, secret := parts[0], parts[1]

	header := map[string]any{
		"alg": "HS256",
		"sign_type": "SIGN",
	}
	headerJSON, _ := json.Marshal(header)
	payload := map[string]any{
		"api_key":   id,
		"exp":       fmt.Sprintf("%d", time.Now().Add(30*time.Minute).UnixMilli()),
		"timestamp": fmt.Sprintf("%d", time.Now().UnixMilli()),
	}
	payloadJSON, _ := json.Marshal(payload)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(headerB64 + "." + payloadB64))
	sign := hex.EncodeToString(mac.Sum(nil))

	return headerB64 + "." + payloadB64 + "." + sign
}

func splitAPIKey(apiKey string) []string {
	parts := make([]string, 0, 2)
	current := make([]byte, 0)
	for i := 0; i < len(apiKey); i++ {
		if apiKey[i] == '.' {
			parts = append(parts, string(current))
			current = current[:0]
		} else {
			current = append(current, apiKey[i])
		}
	}
	if len(current) > 0 {
		parts = append(parts, string(current))
	}
	return parts
}

func (p *ZhipuProvider) toZhipuReq(req CompletionRequest) zhipuReq {
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
	return zhipuReq{
		Model:       req.Model,
		Messages:    msgs,
		Tools:       tools,
		Stream:      req.Stream,
		MaxTokens:   req.MaxTokens,
		Temperature: 0.7,
	}
}

func (p *ZhipuProvider) fromZhipuResp(resp *zhipuResp) *CompletionResponse {
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
