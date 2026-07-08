// Package types defines the core domain types and interfaces for iCode.
// These abstractions form the foundation for all Provider, Tool, and Session implementations.
package types

import (
	"context"
	"io"
	"time"
)

// ============================================================================
// Role — the speaker in a conversation turn
// ============================================================================

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ============================================================================
// Message — a single turn in a conversation
// ============================================================================

type Message struct {
	ID        string        `json:"id"`
	Role      Role          `json:"role"`
	Content   string        `json:"content"`
	ToolCalls []ToolCall    `json:"tool_calls,omitempty"`
	ToolID    string        `json:"tool_id,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
	Metadata  MessageMeta   `json:"metadata,omitempty"`
}

type MessageMeta struct {
	TokenCount   int            `json:"token_count,omitempty"`
	CacheHit     bool           `json:"cache_hit,omitempty"`
	Model        string         `json:"model,omitempty"`
	FinishReason string         `json:"finish_reason,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
}

// ============================================================================
// ToolCall — an LLM-requested tool invocation
// ============================================================================

type ToolCall struct {
	ID         string       `json:"id"`
	Type       string       `json:"type"`
	Name       string       `json:"name"`
	Arguments  string       `json:"arguments"`
	Result     *ToolResult  `json:"result,omitempty"`
}

// ============================================================================
// ToolResult — the outcome of executing a tool
// ============================================================================

type ToolResult struct {
	Success bool   `json:"success"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

// ============================================================================
// Tool Definition — describes a tool the agent can use
// ============================================================================

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"input_schema"`
}

// Tool is the executable contract every tool must fulfill.
type Tool interface {
	// Def returns the tool definition sent to the LLM.
	Def() ToolDef

	// Execute runs the tool with raw JSON arguments and returns a result.
	Execute(ctx context.Context, args string) (*ToolResult, error)
}

// ============================================================================
// ChatCompletion — request + streaming response
// ============================================================================

type ChatRequest struct {
	SessionID    string    `json:"session_id"`
	Messages     []Message `json:"messages"`
	Model        string    `json:"model"`
	ProviderName string    `json:"provider"`
	MaxTokens    int       `json:"max_tokens,omitempty"`
	Temperature  float64   `json:"temperature,omitempty"`
	Tools        []ToolDef `json:"tools,omitempty"`

	// SystemPrompt is injected at the head of each request (immutable prefix).
	SystemPrompt string `json:"system_prompt,omitempty"`

	// User override for provider-specific system fencing.
	User string `json:"user,omitempty"`

	// Cache hints for prefix-cache aware providers (DeepSeek, Anthropic, etc.).
	CacheBreakpoints []int `json:"cache_breakpoints,omitempty"`
}

// StreamEvent is pushed to the caller as the LLM responds.
type StreamEvent struct {
	Type    StreamEventType `json:"type"`
	Content string          `json:"content"`
	ToolCall *LiveToolCall  `json:"tool_call,omitempty"`
	Meta    StreamMeta      `json:"meta,omitempty"`
}

type StreamEventType string

const (
	EventText    StreamEventType = "text"
	EventToolUse StreamEventType = "tool_use"
	EventDone    StreamEventType = "done"
	EventError   StreamEventType = "error"
)

type LiveToolCall struct {
	Index     int    `json:"index"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type StreamMeta struct {
	Usage       TokenUsage `json:"usage,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	Model       string     `json:"model"`
}

// ============================================================================
// Token Tracking
// ============================================================================

type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheHitTokens   int `json:"cache_hit_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

type TokenPlan struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	InputPrice  float64 `json:"input_price_per_mtok"`
	OutputPrice float64 `json:"output_price_per_mtok"`
	CachePrice  float64 `json:"cache_price_per_mtok"`

	// FreeTier indicates whether this plan has a free daily quota.
	FreeTier *FreeTier `json:"free_tier,omitempty"`
}

type FreeTier struct {
	DailyTokens  int  `json:"daily_tokens"`
	DailyRequests int `json:"daily_requests"`
}

// ============================================================================
// Provider Interface — what every LLM backend must implement
// ============================================================================

type Provider interface {
	// Name returns the canonical provider identifier (e.g. "deepseek", "zhipu").
	Name() string

	// ListModels returns every model + its coding/token plan this provider offers.
	ListModels() []ModelInfo

	// ChatStream performs a streaming chat completion.
	ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error)

	// Chat performs a non-streaming chat completion.
	Chat(ctx context.Context, req ChatRequest) (*Message, error)

	// Health performs a lightweight connectivity check (e.g. list models).
	Health(ctx context.Context) error

	// SupportsCache reports whether this provider supports prefix-cache hints.
	SupportsCache() bool
}

// ============================================================================
// Model Info
// ============================================================================

type ModelInfo struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Provider    string    `json:"provider"`

	ContextWindow  int       `json:"context_window"`
	MaxOutputTokens int      `json:"max_output_tokens"`

	// Plans lists available pricing plans (coding plan, token plan, etc.).
	Plans []TokenPlan `json:"plans"`

	// Capabilities — what this model can do.
	Capabilities ModelCap `json:"capabilities"`

	// Multimodal support hint.
	SupportsVision bool `json:"supports_vision"`

	// Last update of this model record.
	UpdatedAt time.Time `json:"updated_at"`
}

type ModelCap struct {
	Tools      bool `json:"tools"`
	Streaming  bool `json:"streaming"`
	JSONMode   bool `json:"json_mode"`
	Reasoning  bool `json:"reasoning"`
}

// ============================================================================
// Provider Registry — central model catalogue
// ============================================================================

type ProviderRegistry interface {
	// Register adds or updates a provider implementation.
	Register(p Provider) error

	// Get returns a provider by name.
	Get(name string) (Provider, error)

	// List returns all registered provider names.
	List() []string

	// ListAllModels returns every model across all providers.
	ListAllModels() []ModelInfo

	// RefreshAll triggers every provider to refresh its model list.
	RefreshAll(ctx context.Context) []error

	// ResolveModel finds the provider that owns a given model ID.
	ResolveModel(modelID string) (Provider, ModelInfo, error)
}

// ============================================================================
// Session
// ============================================================================

type Session struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	ModelID     string    `json:"model_id"`
	ProviderName string   `json:"provider"`
	Messages    []Message `json:"messages"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Metadata    map[string]any `json:"metadata,omitempty"`

	// Total tokens consumed in this session.
	TotalTokens TokenUsage `json:"total_tokens"`
}

type SessionStore interface {
	Create(s *Session) error
	Get(id string) (*Session, error)
	List(limit, offset int) ([]Session, error)
	Update(s *Session) error
	Delete(id string) error
	AppendMessage(sessionID string, msg Message) error
}

// ============================================================================
// Conversation Engine
// ============================================================================

type ConversationEngine interface {
	// Send sends a user message and returns a stream of assistant responses.
	Send(ctx context.Context, sessionID string, content string) (<-chan StreamEvent, error)

	// Stop cancels the current in-flight request for a session.
	Stop(sessionID string)
}

// ============================================================================
// Helper types
// ============================================================================

// ReadCloser wraps an io.ReadCloser with a name for MIME detection.
// Useful for passing file content to multimodal providers.
type NamedReadCloser struct {
	Name   string
	Reader io.ReadCloser
}
