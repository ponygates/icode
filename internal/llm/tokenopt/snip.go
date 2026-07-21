// Package tokenopt — Snip filtering layer (Claude Code Level 0 compression).
//
// Snip is a zero-cost filter that removes useless conversation turns before
// they consume context budget. It runs before any other compression layer.
//
// Three snip rules, all O(1) amortized:
//  1. Empty assistant turns — model said nothing useful
//  2. Rejected tool rounds — user denied every tool call in a turn
//  3. Blank messages — messages with zero meaningful content
//
// Unlike higher compression levels, Snip costs zero API calls — it's just
// a lightweight filter on the message log.
package tokenopt

import "github.com/ponygates/icode/internal/types"

// SnipConfig controls what the Snip filter removes.
type SnipConfig struct {
	// RemoveEmptyAssistant drops assistant messages with no content and no
	// tool calls. Default: true.
	RemoveEmptyAssistant bool

	// RemoveRejectedRounds drops assistant+tool message pairs where every
	// tool call was denied (result is empty/failed). Default: true.
	RemoveRejectedRounds bool

	// RemoveBlankMessages drops any message with zero content. Default: true.
	RemoveBlankMessages bool

	// MinContentLength is the minimum meaningful content length.
	// Messages shorter than this are treated as blank. Default: 3.
	MinContentLength int
}

// DefaultSnipConfig returns sensible defaults.
func DefaultSnipConfig() SnipConfig {
	return SnipConfig{
		RemoveEmptyAssistant:  true,
		RemoveRejectedRounds:  true,
		RemoveBlankMessages:   true,
		MinContentLength:      3,
	}
}

// SnipFilter implements zero-cost message pruning.
type SnipFilter struct {
	config SnipConfig
}

// NewSnipFilter creates a snip filter with the given config.
func NewSnipFilter(cfg SnipConfig) *SnipFilter {
	return &SnipFilter{config: cfg}
}

// Filter applies snip rules to a message slice. Returns a new slice with
// pruned messages. Always keeps system messages intact.
func (f *SnipFilter) Filter(msgs []types.Message) []types.Message {
	if !f.config.RemoveEmptyAssistant && !f.config.RemoveRejectedRounds && !f.config.RemoveBlankMessages {
		return msgs
	}

	result := make([]types.Message, 0, len(msgs))
	i := 0
	for i < len(msgs) {
		msg := msgs[i]

		// Always keep system messages
		if msg.Role == types.RoleSystem {
			result = append(result, msg)
			i++
			continue
		}

		// Rule 1: Remove empty assistant messages
		if f.config.RemoveEmptyAssistant &&
			msg.Role == types.RoleAssistant &&
			len(msg.Content) < f.config.MinContentLength &&
			len(msg.ToolCalls) == 0 {
			i++
			continue
		}

		// Rule 2: Remove rejected tool rounds
		if f.config.RemoveRejectedRounds &&
			msg.Role == types.RoleAssistant &&
			len(msg.ToolCalls) > 0 {
			// Check if ALL tool calls in this assistant message had failed results
			allRejected := true
			for _, tc := range msg.ToolCalls {
				if tc.Result != nil && tc.Result.Success {
					allRejected = false
					break
				}
			}
			if allRejected {
				// Skip this assistant message
				i++
				// Also skip any following tool result messages
				for i < len(msgs) {
					if msgs[i].Role == types.RoleTool {
						i++
					} else {
						break
					}
				}
				continue
			}
		}

		// Rule 3: Remove blank tool messages
		if f.config.RemoveBlankMessages &&
			msg.Role == types.RoleTool &&
			len(msg.Content) < f.config.MinContentLength {
			i++
			continue
		}

		result = append(result, msg)
		i++
	}

	return result
}

// isRejectedTurn checks if a contiguous block of messages represents a
// rejected tool-use round (user denied every call).
func isRejectedTurn(msgs []types.Message, start int) bool {
	if start >= len(msgs) {
		return false
	}
	msg := msgs[start]
	if msg.Role != types.RoleAssistant || len(msg.ToolCalls) == 0 {
		return false
	}
	for _, tc := range msg.ToolCalls {
		if tc.Result == nil || tc.Result.Success {
			return false
		}
	}
	return true
}
