// Package tokenopt implements the Cache-First Loop — iCode's core token-saving mechanism.
//
// Design (inspired by Reasonix + extended for multi-provider support):
//
//  1. Immutable Prefix — system prompt + tool definitions placed at position 0,
//     kept stable across turns so providers can reuse KV cache entries.
//
//  2. Append-Only Log — conversation messages accumulate in strict order;
//     no edits or deletions that would invalidate cached prefixes.
//
//  3. Volatile Scratch — tool call results for the current turn are ephemeral;
//     they are appended after the stable prefix and discarded after the turn.
//
//  4. Smart Compaction — when context exceeds the model's window, older messages
//     are summarized and folded into the system prefix, preserving cache stability.
//
//  5. Provider Strategies — per-provider cache hint placement and compaction policies.
package tokenopt

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ponygates/icode/internal/types"
)

// CompactionStrategy defines how context overflow is handled.
type CompactionStrategy string

const (
	// StrategySummarize compresses old messages into a summary injected into the prefix.
	StrategySummarize CompactionStrategy = "summarize"

	// StrategyDrop drops the oldest messages, keeping recent context.
	StrategyDrop CompactionStrategy = "drop"

	// StrategyNone disables compaction (will hit context limit errors).
	StrategyNone CompactionStrategy = "none"
)

// CacheStrategy defines per-provider cache hint behavior.
type CacheStrategy struct {
	// MarkSystem marks the system prompt for caching.
	MarkSystem bool

	// MarkTools marks the last tool definition for caching (Anthropic style).
	MarkTools bool

	// StablePrefix indicates the provider has a stable prefix-cache (DeepSeek).
	StablePrefix bool

	// MaxCacheTokens is the maximum number of tokens that can be cached.
	MaxCacheTokens int
}

// ProviderCacheStrategies maps provider names to their cache strategies.
var ProviderCacheStrategies = map[string]CacheStrategy{
	"deepseek": {
		MarkSystem:    true,
		StablePrefix:  true,
		MaxCacheTokens: 65536,
	},
	"anthropic": {
		MarkSystem:    true,
		MarkTools:     true,
		MaxCacheTokens: 131072,
	},
	"openrouter": {
		MarkSystem: true,
	},
}

// Stats tracks token usage over a session.
type Stats struct {
	PromptTokens      int     `json:"prompt_tokens"`
	CompletionTokens  int     `json:"completion_tokens"`
	TotalTokens       int     `json:"total_tokens"`
	CacheHitTokens    int     `json:"cache_hit_tokens"`
	CacheWriteTokens  int     `json:"cache_write_tokens"`
	CacheHitRate      float64 `json:"cache_hit_rate"`
	EstimatedCost     float64 `json:"estimated_cost"`
	CompactionsDone   int     `json:"compactions_done"`
	TokensSaved       int     `json:"tokens_saved"`
	Rounds            []RoundStat `json:"rounds,omitempty"`
}

type RoundStat struct {
	Turn       int           `json:"turn"`
	Prompt     int           `json:"prompt"`
	Completion int           `json:"completion"`
	CacheHit   int           `json:"cache_hit"`
	Cost       float64       `json:"cost"`
	Duration   time.Duration `json:"duration"`
}

// CompactionRecord tracks what was compacted.
type CompactionRecord struct {
	OriginalMsgCount int    `json:"original_msg_count"`
	KeptMsgCount     int    `json:"kept_msg_count"`
	OriginalTokens   int    `json:"original_tokens"`
	CompressedTokens int    `json:"compressed_tokens"`
	Summary          string `json:"summary,omitempty"`
}

// Optimizer is the central token optimization engine.
type Optimizer struct {
	mu sync.Mutex

	systemPrompt string
	providerName string
	toolSchemas  []types.ToolDef
	messageLog   []types.Message
	modelInfo    types.ModelInfo

	compactThreshold float64
	strategy         CompactionStrategy

	// The last compaction summary — injected into prefix for cache stability.
	compactionSummary string

	// Running statistics.
	stats Stats

	// Cache strategy for this provider.
	cacheStrategy CacheStrategy

	// turnStartIdx tracks the index in messageLog where the current user
	// turn began. When a new user message arrives, tool results from the
	// previous turn (between turnStartIdx and len(messageLog)) are folded
	// into short placeholders to keep the context lean.
	turnStartIdx int

	// Snip filter — zero-cost removal of useless turns before any
	// higher-level compression is applied. Part of the 5-layer pipeline.
	snipFilter *SnipFilter
	snipConfig SnipConfig
}

// Config tunes the optimizer behavior.
type Config struct {
	ModelInfo    types.ModelInfo
	SystemPrompt string
	ProviderName string
	Strategy     CompactionStrategy

	// CompactThreshold is the fraction of context window that triggers compaction.
	CompactThreshold float64

	// MinKeepMessages is the minimum number of recent messages to always keep.
	MinKeepMessages int

	// Snip controls zero-cost filtering of useless turns.
	// Zero value = use defaults (default snip config).
	Snip SnipConfig
}

// DefaultConfig returns sensible defaults.
func DefaultConfig(model types.ModelInfo) Config {
	return Config{
		ModelInfo:        model,
		CompactThreshold: 0.80,
		MinKeepMessages:  4,
		Strategy:         StrategySummarize,
	}
}

// New creates an optimizer for a given model.
func New(cfg Config) *Optimizer {
	if cfg.CompactThreshold <= 0 {
		cfg.CompactThreshold = 0.80
	}
	if cfg.MinKeepMessages <= 0 {
		cfg.MinKeepMessages = 4
	}
	if cfg.Strategy == "" {
		cfg.Strategy = StrategySummarize
	}

	cacheStrat, ok := ProviderCacheStrategies[cfg.ProviderName]
	if !ok {
		cacheStrat = CacheStrategy{}
	}

	snipCfg := cfg.Snip
	if snipCfg == (SnipConfig{}) {
		snipCfg = DefaultSnipConfig()
	}

	return &Optimizer{
		systemPrompt:     cfg.SystemPrompt,
		providerName:     cfg.ProviderName,
		modelInfo:        cfg.ModelInfo,
		compactThreshold: cfg.CompactThreshold,
		strategy:         cfg.Strategy,
		cacheStrategy:    cacheStrat,
		stats:            Stats{},
		snipConfig:       snipCfg,
		snipFilter:       NewSnipFilter(snipCfg),
	}
}

// NewWithModel is a convenience wrapper for backward compatibility.
func NewWithModel(modelInfo types.ModelInfo, systemPrompt string) *Optimizer {
	return New(Config{ModelInfo: modelInfo, SystemPrompt: systemPrompt})
}

// SetTools records the tool schemas (part of the immutable prefix).
func (o *Optimizer) SetTools(tools []types.ToolDef) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.toolSchemas = tools
}

// AddMessage appends a message — never mutates existing entries.
func (o *Optimizer) AddMessage(msg types.Message) {
	o.mu.Lock()
	defer o.mu.Unlock()

	// When a new user message arrives, fold the previous turn's tool
	// results into compact placeholders (Volatile Scratch).
	if msg.Role == types.RoleUser {
		o.foldVolatileLocked()
		o.turnStartIdx = len(o.messageLog)
	}

	// Compress large tool results to save tokens
	if msg.Role == types.RoleTool && len(msg.Content) > 4000 {
		msg.Content = compressToolResult(msg.Content, 4000)
	}

	o.messageLog = append(o.messageLog, msg)
}

// foldVolatileLocked scans the turn starting at turnStartIdx and collapses
// intermediate tool results into short [tool: name → N bytes] placeholders.
// The last assistant message of the turn is always kept intact.
func (o *Optimizer) foldVolatileLocked() {
	if o.turnStartIdx <= 0 || o.turnStartIdx >= len(o.messageLog) {
		return
	}

	// Walk backward to find the last assistant block after the last tool
	// call — that is the "terminal" assistant message whose content should
	// be kept.
	lastFinalStart := -1
	for i := len(o.messageLog) - 1; i >= o.turnStartIdx; i-- {
		m := o.messageLog[i]
		if m.Role == types.RoleAssistant && m.Content != "" && len(m.ToolCalls) == 0 {
			lastFinalStart = i
			break
		}
	}

	// If no final assistant text was found, keep everything as-is (the
	// turn may still be in progress or was just a plain text response).
	if lastFinalStart < 0 {
		return
	}

	// Fold everything between turnStartIdx and lastFinalStart:
	//   - tool_result messages → placeholders
	//   - assistant messages with tool calls → stripped (the result carries
	//     the context, not the call)
	// The final assistant message content is kept.
	for i := o.turnStartIdx; i < lastFinalStart; i++ {
		m := &o.messageLog[i]
		if m.Role == types.RoleTool {
			if len(m.Content) > 80 {
				bytes := len(m.Content)
				o.stats.TokensSaved += bytes / 4 // rough estimate
				m.Content = fmt.Sprintf("[tool result: %d bytes, folded]", bytes)
			}
		} else if m.Role == types.RoleAssistant {
			// Non-terminal assistant messages had tool calls.
			// Strip their generated text since it's been superseded
			// by later tool results and the terminal message.
			if len(m.ToolCalls) > 0 {
				m.Content = ""
			}
		}
	}
}

// RecordUsage updates running stats.
func (o *Optimizer) RecordUsage(usage types.TokenUsage, cost float64, startTime time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.stats.PromptTokens += usage.PromptTokens
	o.stats.CompletionTokens += usage.CompletionTokens
	o.stats.TotalTokens += usage.TotalTokens
	o.stats.CacheHitTokens += usage.CacheHitTokens
	o.stats.CacheWriteTokens += usage.CacheWriteTokens
	o.stats.EstimatedCost += cost

	totalWritten := o.stats.PromptTokens + o.stats.CacheWriteTokens
	if totalWritten > 0 {
		o.stats.CacheHitRate = float64(o.stats.CacheHitTokens) / float64(totalWritten)
	}

	o.stats.Rounds = append(o.stats.Rounds, RoundStat{
		Turn:       len(o.stats.Rounds) + 1,
		Prompt:     usage.PromptTokens,
		Completion: usage.CompletionTokens,
		CacheHit:   usage.CacheHitTokens,
		Cost:       cost,
		Duration:   time.Since(startTime),
	})
}

// BuildPrefix constructs the immutable prefix.
func (o *Optimizer) BuildPrefix() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buildPrefixLocked()
}

func (o *Optimizer) buildPrefixLocked() string {
	var sb strings.Builder
	sb.WriteString(o.systemPrompt)

	// NOTE: compactionSummary is deliberately NOT included here so the
	// system prefix (system prompt + tool defs) remains immutable across
	// compactions. If compactionSummary changed the prefix, every provider
	// cache strategy (DeepSeek stable-prefix, Anthropic cache_control)
	// would lose the cached prefix on every compaction — i.e. the worst
	// possible moment for a cache miss.
	//
	// Instead, the compaction summary is injected as a separate system
	// message at position 0 in CompactRequest(). This makes it part of the
	// conversation log, NOT the prefix, so the immutable prefix stays
	// cached across compactions.

	if len(o.toolSchemas) > 0 {
		sb.WriteString("\n\n## Available Tools\n\n")
		for i, t := range o.toolSchemas {
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", t.Name, t.Description))
			_ = i
		}
	}

	return sb.String()
}

// ShouldCompact returns true if estimated tokens exceed the threshold.
func (o *Optimizer) ShouldCompact() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.shouldCompactLocked()
}

func (o *Optimizer) shouldCompactLocked() bool {
	estimated := o.estimateTokensLocked()
	limit := o.modelInfo.ContextWindow
	if limit <= 0 {
		limit = 128000
	}
	threshold := int(float64(limit) * o.compactThreshold)
	return estimated >= threshold
}

// CompactRequest builds a chat request with optimal cache hints.
func (o *Optimizer) CompactRequest(input string) []types.Message {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Apply snip filter before compaction (zero-cost removal of useless turns)
	filtered := o.filterMessages(o.messageLog)

	// If context is overflowing, compact
	if o.shouldCompactLocked() {
		o.compactLocked()
	}

	// Compaction summary is injected as position 0 so it does NOT break
	// the immutable prefix (system prompt + tool defs) when it changes.
	messages := make([]types.Message, 0, len(filtered)+2)
	if o.compactionSummary != "" {
		messages = append(messages, types.Message{
			Role:    types.RoleSystem,
			Content: "## Previous Conversation Summary\n" + o.compactionSummary,
		})
	}
	messages = append(messages, filtered...)

	if input != "" {
		messages = append(messages, types.Message{
			Role:      types.RoleUser,
			Content:   input,
			Timestamp: time.Now(),
		})
	}

	return messages
}

// compactLocked performs smart compaction on the message log.
// MUST be called with mu held.
func (o *Optimizer) compactLocked() {
	if len(o.messageLog) <= 4 {
		return
	}

	originalTokens := o.estimateTokensLocked()
	originalCount := len(o.messageLog)

	switch o.strategy {
	case StrategySummarize:
		o.summarizeLocked()
	case StrategyDrop:
		o.dropOldestLocked()
	default:
		return
	}

	compressedTokens := o.estimateTokensLocked()
	saved := originalTokens - compressedTokens
	if saved > 0 {
		o.stats.TokensSaved += saved
	}
	o.stats.CompactionsDone++

	o.stats.Rounds = append(o.stats.Rounds, RoundStat{
		Turn:       -1, // compaction round
		Prompt:     originalTokens,
		Completion: compressedTokens,
	})
	_ = originalCount
}

// summarizeLocked generates a summary of old messages and replaces them.
func (o *Optimizer) summarizeLocked() {
	keepFrom := len(o.messageLog) - 6
	if keepFrom < 2 {
		keepFrom = 2
	}

	oldMessages := o.messageLog[:keepFrom]
	recentMessages := o.messageLog[keepFrom:]

	// Build summary from old messages
	var summaryParts []string
	userRequests := 0
	toolCallsDone := 0

	for _, msg := range oldMessages {
		switch msg.Role {
		case types.RoleUser:
			userRequests++
			// Keep track of key user intents
			trimmed := strings.TrimSpace(msg.Content)
			if len(trimmed) > 200 {
				trimmed = trimmed[:200] + "..."
			}
			summaryParts = append(summaryParts, fmt.Sprintf("User asked: %s", trimmed))
		case types.RoleTool:
			toolCallsDone++
		case types.RoleAssistant:
			if len(msg.ToolCalls) > 0 {
				names := make([]string, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					names = append(names, tc.Name)
				}
				summaryParts = append(summaryParts,
					fmt.Sprintf("Assistant used tools: %s", strings.Join(names, ", ")))
			}
		}
	}

	// Build concise summary
	summary := fmt.Sprintf(
		"Previous conversation (%d user requests, %d tool executions). Key activities: %s",
		userRequests, toolCallsDone, strings.Join(summaryParts, "; "),
	)

	if len(summary) > 800 {
		summary = summary[:800] + "..."
	}

	o.compactionSummary = summary
	o.messageLog = recentMessages
}

// dropOldestLocked drops the oldest N messages, keeping the most recent.
func (o *Optimizer) dropOldestLocked() {
	keepFrom := len(o.messageLog) - 10
	if keepFrom < 2 {
		return
	}

	// Don't drop if it would break tool call/result pairing
	for keepFrom > 2 {
		msg := o.messageLog[keepFrom]
		if msg.Role == types.RoleTool || msg.Role == types.RoleAssistant {
			keepFrom-- // keep tool-related messages together
		} else {
			break
		}
	}

	o.messageLog = o.messageLog[keepFrom:]
}

// BuildCacheBreakpoints returns indices of messages where cache breakpoints should go.
func (o *Optimizer) BuildCacheBreakpoints() []int {
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.cacheStrategy.StablePrefix && !o.cacheStrategy.MarkSystem {
		return nil
	}

	var breakpoints []int

	// First breakpoint after the system prefix (index 0 represents the prefix)
	breakpoints = append(breakpoints, 0)

	// For stable prefix providers, add a breakpoint after every 20 messages
	if o.cacheStrategy.StablePrefix {
		for i := 20; i < len(o.messageLog); i += 20 {
			breakpoints = append(breakpoints, i)
		}
	}

	return breakpoints
}

// Stats returns a snapshot of current statistics.
func (o *Optimizer) Stats() Stats {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stats
}

// CompactionSummary returns the current compaction summary.
func (o *Optimizer) CompactionSummary() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.compactionSummary
}

// EstimateTokens provides a more accurate token count estimate.
func (o *Optimizer) EstimateTokens() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.estimateTokensLocked()
}

func (o *Optimizer) estimateTokensLocked() int {
	total := countTokens(o.buildPrefixLocked())

	for _, m := range o.messageLog {
		total += countTokens(m.Content)
		for _, tc := range m.ToolCalls {
			total += countTokens(tc.Arguments)
			if tc.Result != nil {
				total += countTokens(tc.Result.Content)
			}
		}
	}

	return total
}

// countTokens provides a character-based heuristic for token counting.
// Handles both CJK characters (~0.6 tokens/char) and ASCII (~0.25 tokens/char).
func countTokens(s string) int {
	ascii := 0
	cjk := 0

	for _, r := range s {
		if r <= 127 {
			ascii++
		} else if isCJK(r) {
			cjk++
		} else {
			ascii++
		}
	}

	// Token estimator: ~4 ASCII chars/token, ~1.5 CJK chars/token
	tokens := int(math.Ceil(float64(ascii)/4.0 + float64(cjk)/1.5))
	return tokens
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0x3000 && r <= 0x303F) // CJK punctuation
}

// compressToolResult truncates large tool outputs, keeping head and tail.
func compressToolResult(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}

	headLen := maxLen * 7 / 10
	tailLen := maxLen - headLen - 50

	head := content[:headLen]
	// Find last newline in head for clean cut
	if idx := strings.LastIndex(head, "\n"); idx > headLen/2 {
		head = content[:idx]
		headLen = idx
	}

	tail := content[len(content)-tailLen:]
	if idx := strings.Index(tail, "\n"); idx > 0 {
		tail = tail[idx+1:]
	}

	omitted := content[headLen : len(content)-tailLen]
	omittedChars := utf8.RuneCountInString(omitted)

	return fmt.Sprintf("%s\n\n[... %d chars omitted ...]\n\n%s", head, omittedChars, tail)
}
