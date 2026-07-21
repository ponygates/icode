// Package tokenopt — Budget Enforcement + Cache-Aware Compression.
//
// Budget Enforcement: 对工具输出设置硬性大小限制，防止 LLM 上下文被撑爆。
// Claude Code 规格：全局 50K/Bash 30K/Grep 20K，总上限 200K 字符。
//
// Cache-Aware Compression: 只在缓存断点之后进行压缩，不破坏前缀缓存。
// Reasonix 核心原则：不修改已缓存的字节。
//
// 分层架构（5层压缩管道的 Level 4：预算强制）：
//   Level 0: Snip        — 零成本过滤空白轮次
//   Level 1: Dedup       — 工具输出去重缓存
//   Level 2: Microcompact — 折叠旧工具结果为占位符
//   Level 3: Context Fold — LLM 摘要多轮次
//   Level 4: Budget      — 硬性大小限制（本文件）

package tokenopt

import (
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// BudgetConfig defines per-tool and global output size limits.
type BudgetConfig struct {
	// GlobalMax is the maximum total characters for all tool outputs combined.
	GlobalMax int

	// BashMax is the maximum characters for a single bash command output.
	BashMax int

	// ReadMax is the maximum characters for a single read_file output.
	ReadMax int

	// GrepMax is the maximum characters for a single grep output.
	GrepMax int

	// DefaultMax is the maximum for any other tool output.
	DefaultMax int
}

// DefaultBudgetConfig returns the recommended budget limits.
func DefaultBudgetConfig() BudgetConfig {
	return BudgetConfig{
		GlobalMax:  200_000, // 200K chars total
		BashMax:    30_000,  // 30K for bash
		ReadMax:    50_000,  // 50K for file reads
		GrepMax:    20_000,  // 20K for grep results
		DefaultMax: 50_000,  // 50K for other tools
	}
}

// BudgetEnforcer applies size limits to tool outputs.
type BudgetEnforcer struct {
	config BudgetConfig
	total  int // running total across all tools in this turn
}

// NewBudgetEnforcer creates a budget enforcer.
func NewBudgetEnforcer(cfg BudgetConfig) *BudgetEnforcer {
	return &BudgetEnforcer{config: cfg}
}

// Enforce applies the budget limit to a tool result.
// Returns the trimmed content and whether it was truncated.
func (b *BudgetEnforcer) Enforce(toolName, content string) (trimmed string, truncated bool) {
	if content == "" {
		return "", false
	}

	// Determine per-tool limit
	maxChars := b.config.DefaultMax
	switch toolName {
	case "bash", "run_command", "cmd":
		maxChars = b.config.BashMax
	case "read_file":
		maxChars = b.config.ReadMax
	case "grep", "search_files", "search_content":
		maxChars = b.config.GrepMax
	default:
		maxChars = b.config.DefaultMax
	}

	// Apply per-tool limit
	if len(content) > maxChars {
		headLen := maxChars * 7 / 10
		tailLen := maxChars - headLen - 100
		head := content[:headLen]
		tail := content[len(content)-tailLen:]

		// Try to cut at newlines for readability
		if idx := strings.LastIndex(head, "\n"); idx > headLen/2 {
			head = content[:idx]
		}
		if idx := strings.Index(tail, "\n"); idx >= 0 {
			tail = tail[idx+1:]
		}

		omitted := len(content) - len(head) - len(tail)
		content = fmt.Sprintf("%s\n\n[... %d chars truncated by budget limit (%s max: %d) ...]\n\n%s",
			head, omitted, toolName, maxChars, tail)
		truncated = true
	}

	// Apply global budget
	remaining := b.config.GlobalMax - b.total
	if remaining <= 0 {
		return "[Global budget exceeded. Further tool output suppressed.]", true
	}
	if len(content) > remaining {
		content = content[:remaining]
		content += "\n\n[Global budget limit reached. Output truncated.]"
		truncated = true
	}

	b.total += len(content)
	return content, truncated
}

// Reset resets the running total for a new turn.
func (b *BudgetEnforcer) Reset() {
	b.total = 0
}

// ============================================================================
// Cache-Aware Microcompact
// ============================================================================

// CacheAwareCompactor selectively compresses messages that are outside the
// cache prefix zone. It never modifies bytes that are before the cache
// breakpoint — this preserves the provider's KV cache.
type CacheAwareCompactor struct {
	// CacheRegionSize is the estimated size of the cached prefix region.
	// Messages whose cumulative size is within this region are NOT compressed.
	CacheRegionSize int
}

// DefaultCacheAwareCompactor returns a compactor with sensible defaults.
func DefaultCacheAwareCompactor() *CacheAwareCompactor {
	return &CacheAwareCompactor{
		CacheRegionSize: 4096, // ~4K tokens of system prompt + tools is typically cached
	}
}

// ShouldCompact checks if a message at the given index can be safely
// compacted without breaking the cache prefix. Messages within the cache
// region are left untouched.
//
// The cache region typically contains:
//   - System prompt (immutable prefix)
//   - Tool definitions
//   - The first few conversation rounds
//
// Messages beyond the cache region are fair game for compression.
func (c *CacheAwareCompactor) ShouldCompact(messages []types.Message, msgIndex int) bool {
	if msgIndex < 0 || msgIndex >= len(messages) {
		return false
	}

	// Calculate cumulative size up to this message
	cumulativeSize := 0
	for i := 0; i <= msgIndex; i++ {
		cumulativeSize += len(messages[i].Content)
		for _, tc := range messages[i].ToolCalls {
			cumulativeSize += len(tc.Arguments)
			if tc.Result != nil {
				cumulativeSize += len(tc.Result.Content)
			}
		}
	}

	// If within cache region, don't compact
	return cumulativeSize > c.CacheRegionSize
}

// CompactionPlan returns indices of messages that can be safely compacted.
func (c *CacheAwareCompactor) CompactionPlan(messages []types.Message) []int {
	var plan []int
	for i := range messages {
		if c.ShouldCompact(messages, i) {
			plan = append(plan, i)
		}
	}
	return plan
}
