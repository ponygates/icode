package provider

import (
	"context"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function Function `json:"function"`
}

type Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}

type CompletionRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	Tools     []ToolDef `json:"tools,omitempty"`
	Stream    bool      `json:"stream"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

type CompletionResponse struct {
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     Usage      `json:"usage"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheHits    int `json:"cache_hits"`
}

type StreamChunk struct {
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Done      bool       `json:"done"`
	Usage     *Usage     `json:"usage,omitempty"`
}

type ToolDef struct {
	Type       string     `json:"type"`
	Function   ToolFunc   `json:"function"`
}

type ToolFunc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type CacheInfo struct {
	Provider string `json:"provider"`
	PrefixID string `json:"prefix_id,omitempty"`
	Strategy string `json:"strategy"`
}

type Provider interface {
	Name() string
	Models() []string
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
	Stream(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error)
	CacheInfo() CacheInfo
	Cost(req CompletionRequest, usage Usage) float64
}

type Registry struct {
	providers map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

func (r *Registry) Register(p Provider) {
	r.providers[p.Name()] = p
}

func (r *Registry) Get(name string) Provider {
	return r.providers[name]
}

func (r *Registry) List() []string {
	var names []string
	for n := range r.providers {
		names = append(names, n)
	}
	return names
}

func (r *Registry) Default(name string, fallback Provider) Provider {
	if p := r.providers[name]; p != nil {
		return p
	}
	return fallback
}
