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
	ID          string       `json:"id"`
	Role        Role         `json:"role"`
	Content     string       `json:"content"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`
	ToolID      string       `json:"tool_id,omitempty"`
	Timestamp   time.Time    `json:"timestamp"`
	Metadata    MessageMeta  `json:"metadata,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment represents inline content (images, PDFs, etc.) attached to
// a message. Currently only image attachments are supported.
type Attachment struct {
	Type     string `json:"type"`               // "image", "pdf"
	MIMEType string `json:"mime"`               // "image/png", "image/jpeg", …
	Data     string `json:"data"`               // base64-encoded content
	AltText  string `json:"alt_text,omitempty"` // optional description
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
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	Name      string      `json:"name"`
	Arguments string      `json:"arguments"`
	Result    *ToolResult `json:"result,omitempty"`
}

// ============================================================================
// ToolResult — the outcome of executing a tool
// ============================================================================

type ToolResult struct {
	Success bool   `json:"success"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`

	// Attachments carries inline multimodal output (e.g. an image produced by
	// the image_gen tool) back into the conversation so vision-capable models
	// can see the generated artifact in subsequent turns. Only image
	// attachments are consumed by providers today; other types are ignored.
	Attachments []Attachment `json:"attachments,omitempty"`
}

// ============================================================================
// Tool Definition — describes a tool the agent can use
// ============================================================================

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"input_schema"`

	// ServerName is the owning MCP server for tools discovered over MCP, and
	// empty for built-ins. Callers must not re-derive it from Name: the
	// namespace slug is sanitised and may be uniquified on collision.
	ServerName string `json:"server_name,omitempty"`
}

// Tool is the executable contract every tool must fulfill.
type Tool interface {
	// Def returns the tool definition sent to the LLM.
	Def() ToolDef

	// Execute runs the tool with raw JSON arguments and returns a result.
	Execute(ctx context.Context, args string) (*ToolResult, error)
}

// ToolProgressFunc receives incremental output chunks while a long-running
// tool (bash) executes. The chunk is raw stdout/stderr text. Tools that
// stream progress MUST NOT call fn after Execute returns (the engine's
// callback is turn-scoped and unregistered once the tool completes). The
// callback may be called from the tool's own goroutine.
type ToolProgressFunc func(chunk string)

// ============================================================================
// ChatCompletion — request + streaming response
// ============================================================================

type ChatRequest struct {
	SessionID    string    `json:"session_id"`
	Messages     []Message `json:"messages"`
	Model        string    `json:"model"`
	ProviderName string    `json:"provider"`
	MaxTokens    int       `json:"max_tokens,omitempty"`
	// Temperature is a pointer so "not configured" (nil, the field is omitted
	// and the provider's own default applies) stays distinguishable from an
	// explicit 0 — a user choosing deterministic sampling means 0, and the old
	// `> 0` guard silently dropped exactly that request.
	Temperature *float64 `json:"temperature,omitempty"`
	// TopP is nucleus sampling. Zero means "not configured" and the field is
	// omitted (top_p 0 is not a meaningful sampling setting).
	TopP  float64   `json:"top_p,omitempty"`
	Tools []ToolDef `json:"tools,omitempty"`

	// SystemPrompt is injected at the head of each request (immutable prefix).
	SystemPrompt string `json:"system_prompt,omitempty"`

	// User override for provider-specific system fencing.
	User string `json:"user,omitempty"`

	// Cache hints for prefix-cache aware providers (DeepSeek, Anthropic, etc.).
	CacheBreakpoints []int `json:"cache_breakpoints,omitempty"`

	// Thinking, when non-nil, enables provider extended thinking (Anthropic
	// Claude). Ignored by providers that do not support it.
	Thinking *ThinkingConfig `json:"thinking,omitempty"`

	// CacheTTL overrides the default ephemeral cache breakpoint TTL for
	// providers that honor cache_control ttl (Anthropic). Values like "5m",
	// "1h" mirror Claude Code's promptCacheTtl setting. Empty = provider
	// default.
	CacheTTL string `json:"cache_ttl,omitempty"`
}

// Temp returns a pointer to v, for ChatRequest.Temperature. Callers that
// genuinely want a value (including 0) should use this; leaving the field nil
// means "not configured — let the provider decide".
func Temp(v float64) *float64 { return &v }

// ThinkingConfig enables extended thinking (Anthropic Messages API
// "thinking" block). BudgetTokens must be > 0 and less than MaxTokens.
type ThinkingConfig struct {
	BudgetTokens int `json:"budget_tokens,omitempty"`
}

// StreamEvent is pushed to the caller as the LLM responds.
type StreamEvent struct {
	Type       StreamEventType `json:"type"`
	Content    string          `json:"content"`
	ToolCall   *LiveToolCall   `json:"tool_call,omitempty"`
	Meta       StreamMeta      `json:"meta,omitempty"`
	Permission *PermissionReq  `json:"permission,omitempty"`
}

type StreamEventType string

const (
	EventText       StreamEventType = "text"
	EventToolUse    StreamEventType = "tool_use"
	EventThinking   StreamEventType = "thinking"
	EventDone       StreamEventType = "done"
	EventError      StreamEventType = "error"
	EventPermission StreamEventType = "permission"
	// EventToolProgress is emitted while a long-running tool (bash) streams
	// its stdout/stderr incrementally, so the UI can show live output instead
	// of a single result dump at the end. Never persisted into the session.
	EventToolProgress StreamEventType = "tool_progress"
	// EventSystem is an engine-originated notice (e.g. budget guard kicking
	// in) shown to the user as a system message — never folded into the
	// assistant reply or persisted into the conversation.
	EventSystem StreamEventType = "system"
	// EventPlanProposal is emitted when a plan-mode turn finishes without tool
	// calls — the reply is a plan awaiting the user's go/no-go confirmation.
	EventPlanProposal StreamEventType = "plan_proposal"
)

// PermissionReq is emitted when the engine needs interactive approval for a
// tool call (agent mode). The desktop client renders a dialog and POSTs the
// decision back via /api/permission/respond; the CLI resolves it in-process.
// Strikes/Threshold surface the graded-auth escalation progress (Claude Code
// parity): the dialog shows "已拦截 N/阈值 次" and the manual-mode fallback.
type PermissionReq struct {
	RequestID string `json:"request_id,omitempty"`
	Tool      string `json:"tool"`
	Prompt    string `json:"prompt"`
	// Severity rates the risk of this ask ("high" | "medium" | "low", from
	// permission.RiskSeverity) so approval UIs can colour-code: high =
	// destructive commands (rm -rf, git push --force) render red.
	Severity  string `json:"severity,omitempty"`
	Strikes   int    `json:"strikes,omitempty"`   // consecutive ask/deny count (incl. this one)
	Threshold int    `json:"threshold,omitempty"` // escalation threshold (0 = disabled)
}

type LiveToolCall struct {
	Index     int    `json:"index"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type StreamMeta struct {
	Usage        TokenUsage `json:"usage,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	Model        string     `json:"model"`
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
	Currency    string  `json:"currency,omitempty"` // CNY, USD — defaults to USD

	// FreeTier indicates whether this plan has a free daily quota.
	FreeTier *FreeTier `json:"free_tier,omitempty"`
}

type FreeTier struct {
	DailyTokens   int `json:"daily_tokens"`
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

// CredentialedProvider is an OPTIONAL capability implemented by providers whose
// API credentials (key / base URL) can be updated at runtime. The server uses
// it to push keys configured via the desktop UI into the live provider without
// requiring a restart.
type CredentialedProvider interface {
	// SetCredentials updates the API key and base URL. Empty values are ignored
	// so callers can update only one field.
	SetCredentials(apiKey, apiBase string)
}

// TokenRefresher is the hot-path credential hook a provider calls at most
// once per request after the vendor rejects it with 401. It receives the
// credential the failed request actually carried and returns the credential
// to retry with plus its kind. Returning the same credential of the same
// kind means "nothing changed" — the provider must surface the original 401
// instead of retrying, which is what keeps plain API-key users (whose
// credential never rotates) on their exact pre-existing request path.
type TokenRefresher func(ctx context.Context, sentToken string) (token string, bearer bool, err error)

// OAuthCredentialProvider is an OPTIONAL capability implemented by providers
// that can carry either a vendor API key or an OAuth subscription token in
// the same key field. The credential kind changes how it travels — e.g.
// Anthropic sends API keys in the x-api-key header but accepts subscription
// tokens only as `Authorization: Bearer` — so internal/core/auth configures
// every live provider it builds with both methods below.
type OAuthCredentialProvider interface {
	// SetSubscription records whether the key field currently holds an OAuth
	// bearer token (true) or a plain API key (false).
	SetSubscription(on bool)
	// SetTokenRefresher installs the 401 renewal path (nil disables retries).
	SetTokenRefresher(fn TokenRefresher)
}

// TimeoutSetter is an OPTIONAL capability implemented by providers whose HTTP
// client timeout can be updated at runtime. The server uses it to apply a
// per-provider timeout configured via the desktop UI without a restart.
type TimeoutSetter interface {
	SetTimeout(sec int)
}

// ModelSetter is an OPTIONAL capability implemented by providers whose model
// list can be updated at runtime (e.g. after a live catalog refresh).
type ModelSetter interface {
	SetModels(models []ModelInfo)
}

// ModelFetcher is an OPTIONAL capability implemented by providers that can
// enumerate the models the configured credentials actually have access to, by
// querying the vendor's own /models endpoint live.
//
// This is the only way to discover models a vendor released after this binary
// was built: the built-in catalogue is a snapshot, but the vendor's endpoint
// reflects the account's real entitlements (which vary by plan and region).
// The desktop settings UI drives it from the per-provider "fetch models"
// action.
type ModelFetcher interface {
	// FetchModels lists the models the configured credentials can use, in the
	// order the vendor returned them. It returns an error — rather than an
	// empty slice — when the vendor could not be reached or rejected the key,
	// so callers can tell "the account has no models" apart from "the fetch
	// itself failed".
	FetchModels(ctx context.Context) ([]ModelInfo, error)
}

// ============================================================================
// Model Info
// ============================================================================

type ModelInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Provider    string `json:"provider"`

	// APIModelID is the model string actually sent on the wire to the
	// provider's API. It differs from ID for user-defined custom models:
	// ID is the canonical "provider/model_id" registry key, while the
	// provider expects the bare model_id (e.g. ID "agnes/agnes-3.0-flash"
	// → APIModelID "agnes-3.0-flash"). Empty means ID is already the wire
	// name (true for all provider-built-in models, whose IDs are the raw
	// API names, including slash-style ones like "openrouter/free").
	APIModelID string `json:"api_model_id,omitempty"`

	ContextWindow   int `json:"context_window"`
	MaxOutputTokens int `json:"max_output_tokens"`

	// Plans lists available pricing plans (coding plan, token plan, etc.).
	Plans []TokenPlan `json:"plans"`

	// Capabilities — what this model can do.
	Capabilities ModelCap `json:"capabilities"`

	// Multimodal support hint.
	SupportsVision bool `json:"supports_vision"`

	// Deprecated marks a model that is no longer available via the provider's
	// /v1/models API. Deprecated models are kept in the list (greyed out in UI)
	// so existing sessions referencing them still work, but they show a ⚠ icon
	// and "已下架" label. A model is only marked deprecated after it is absent
	// from the API response for two consecutive refreshes.
	Deprecated bool `json:"deprecated,omitempty"`

	// DeprecatedCount tracks how many consecutive refreshes a model was absent
	// from the provider's API. Once this reaches 2, Deprecated is set to true.
	DeprecatedCount int `json:"deprecated_count,omitempty"`

	// Last update of this model record.
	UpdatedAt time.Time `json:"updated_at"`
}

type ModelCap struct {
	Tools     bool `json:"tools"`
	Streaming bool `json:"streaming"`
	JSONMode  bool `json:"json_mode"`
	Reasoning bool `json:"reasoning"`
}

// WireModel returns the model string to send on the API wire for m. Custom
// models carry their bare provider-side id in APIModelID; provider-built-in
// models (and any custom entry without one) use ID directly. Every ChatRequest
// construction site must use this instead of m.ID, or custom models with a
// "provider/model" composite ID will send the composite string to the API and
// get a 503 model_not_found from gateways that only know the bare name.
func (m ModelInfo) WireModel() string {
	if m.APIModelID != "" {
		return m.APIModelID
	}
	return m.ID
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

	// SetCredentials pushes updated API credentials into a registered provider
	// if it supports runtime updates. Returns true if a matching provider was
	// found and updated.
	SetCredentials(name, apiKey, apiBase string) bool

	// SetTimeout updates a provider's HTTP client timeout at runtime if it
	// supports runtime updates. Returns true if a matching provider was found
	// and updated.
	SetTimeout(name string, sec int) bool

	// RegisterCustomModel adds or updates a user-defined model mapping so it can
	// be resolved by ResolveModel. alias is an optional alternate ID (e.g. the
	// canonical "provider/model" id) that should also resolve to the same model.
	RegisterCustomModel(m ModelInfo, alias string)

	// RemoveCustomModel removes a user-defined model by its canonical id.
	RemoveCustomModel(canonicalID string)

	// Deregister removes a provider by name (used when a custom vendor is deleted).
	Deregister(name string)
}

// ============================================================================
// Session
// ============================================================================

type Session struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	ModelID      string         `json:"model_id"`
	ProviderName string         `json:"provider"`
	Messages     []Message      `json:"messages"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	Metadata     map[string]any `json:"metadata,omitempty"`

	// Total tokens consumed in this session.
	TotalTokens TokenUsage `json:"total_tokens"`
}

// SearchResult represents a message found by SearchMessages.
type SearchResult struct {
	SessionID    string    `json:"session_id"`
	SessionTitle string    `json:"session_title"`
	MessageID    string    `json:"message_id"`
	Role         Role      `json:"role"`
	Content      string    `json:"content"`
	Timestamp    time.Time `json:"timestamp"`
	MatchPos     int       `json:"match_pos"` // character position of first match
}

// AgentMessage is one cross-session message (Claude Code SendMessage parity).
// Sessions address each other by session ID; the model discovers peers via
// the list_agents tool and reads its inbox with the inbox tool.
type AgentMessage struct {
	ID        int64     `json:"id"`
	FromID    string    `json:"from"`
	ToID      string    `json:"to"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	ReadAt    time.Time `json:"read_at,omitempty"` // zero while unread
}

type SessionStore interface {
	Create(s *Session) error
	Get(id string) (*Session, error)
	List(limit, offset int) ([]Session, error)
	Update(s *Session) error
	Delete(id string) error
	AppendMessage(sessionID string, msg Message) error
	UpdateMessage(sessionID string, msg Message) error
	DeleteMessage(sessionID, msgID string) error
	ClearMessages(sessionID string) error
	SearchMessages(query string, limit int) ([]SearchResult, error)
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
