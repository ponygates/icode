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
//     are summarized and folded into the system prompt, preserving cache stability.
package tokenopt

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// Stats tracks token usage over a session.
type Stats struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CacheHitTokens   int
	CacheWriteTokens int

	// Cache hit rate = CacheHitTokens / (CacheHitTokens + CacheWriteTokens + PromptTokens)
	CacheHitRate float64

	// Estimated cost in USD (or provider-native currency).
	EstimatedCost float64

	// Round-by-round history for dashboard display.
	Rounds []RoundStat
}

type RoundStat struct {
	Turn      int
	Prompt    int
	Completion int
	CacheHit  int
	Cost      float64
	Duration  time.Duration
}

// Optimizer is the central token optimization engine.
type Optimizer struct {
	mu sync.Mutex

	// Immutable prefix — system prompt + tool schemas.
	systemPrompt string
	toolSchemas  []types.ToolDef

	// Append-only message log (all turns, never mutated in-place).
	messageLog []types.Message

	// Current model info.
	modelInfo types.ModelInfo

	// Compression threshold (percentage of context window).
	compactThreshold float64

	// Running statistics.
	stats Stats
}

// New creates an optimizer for a given model.
func New(modelInfo types.ModelInfo, systemPrompt string) *Optimizer {
	return &Optimizer{
		systemPrompt:     systemPrompt,
		modelInfo:        modelInfo,
		compactThreshold: 0.85, // 85% of context window triggers compaction
		stats:            Stats{},
	}
}

// SetTools records the tool schemas (part of the immutable prefix).
func (o *Optimizer) SetTools(tools []types.ToolDef) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.toolSchemas = tools
}

// AddMessage appends a message to the log — never mutates existing entries.
func (o *Optimizer) AddMessage(msg types.Message) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.messageLog = append(o.messageLog, msg)
}

// RecordUsage updates the running token stats and cache hit rate.
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

	round := len(o.stats.Rounds) + 1
	o.stats.Rounds = append(o.stats.Rounds, RoundStat{
		Turn:       round,
		Prompt:     usage.PromptTokens,
		Completion: usage.CompletionTokens,
		CacheHit:   usage.CacheHitTokens,
		Cost:       cost,
		Duration:   time.Since(startTime),
	})
}

// BuildPrefix constructs the immutable prefix for a provider request.
// This must remain byte-stable across turns to maximize cache reuse.
func (o *Optimizer) BuildPrefix() string {
	o.mu.Lock()
	defer o.mu.Unlock()

	var sb strings.Builder
	sb.WriteString(o.systemPrompt)

	if len(o.toolSchemas) > 0 {
		sb.WriteString("\n\n## Available Tools\n\n")
		for _, t := range o.toolSchemas {
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", t.Name, t.Description))
		}
	}

	return sb.String()
}

// ShouldCompact returns true if the estimated token count exceeds the threshold.
func (o *Optimizer) ShouldCompact() bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	estimated := o.estimateTokens()
	limit := o.modelInfo.ContextWindow
	threshold := int(float64(limit) * o.compactThreshold)

	return estimated >= threshold
}

// CompactRequest builds a chat request with optimal cache hints.
// It decides which messages to include and where to place cache breakpoints.
func (o *Optimizer) CompactRequest(input string) []types.Message {
	o.mu.Lock()
	defer o.mu.Unlock()

	// For now, return all messages + new user message.
	// Phase 3 will add intelligent summarization when context overflows.
	messages := make([]types.Message, len(o.messageLog))
	copy(messages, o.messageLog)

	if input != "" {
		messages = append(messages, types.Message{
			Role:      types.RoleUser,
			Content:   input,
			Timestamp: time.Now(),
		})
	}

	return messages
}

// Stats returns a snapshot of the current token statistics.
func (o *Optimizer) Stats() Stats {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stats
}

// EstimateTokens provides a rough character-based token count.
func (o *Optimizer) estimateTokens() int {
	total := len(o.systemPrompt)

	for _, t := range o.toolSchemas {
		total += len(t.Name) + len(t.Description)
	}

	for _, m := range o.messageLog {
		total += len(m.Content)
		for _, tc := range m.ToolCalls {
			total += len(tc.Arguments) + len(tc.Result.Content)
		}
	}

	// Rough estimate: ~4 chars per token for Chinese, ~3.5 for English.
	return total / 4
}
