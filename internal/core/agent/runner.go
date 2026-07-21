// Package agent — sub-agent runner with independent Optimizer isolation.
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/llm/tokenopt"
	"github.com/ponygates/icode/internal/types"
)

// Runner executes sub-agents in isolated Optimizer contexts. Each sub-agent
// call spins up a fresh Optimizer with its own system prompt, message log,
// and tool subset — nothing flows into the main conversation's context.
type Runner struct {
	providerReg types.ProviderRegistry
	toolReg     *tool.Registry
	mu          sync.Mutex

	// sessionID is passed through to the sub-agent's Execute path so
	// session-scoped tools (TodoWrite, checkpoint hooks) still work.
	sessionID string
}

// NewRunner creates a sub-agent runner bound to a provider registry and tool
// registry (both typically shared with the main Engine).
func NewRunner(reg types.ProviderRegistry, tr *tool.Registry) *Runner {
	return &Runner{
		providerReg: reg,
		toolReg:     tr,
	}
}

// SetSessionID records the current session ID so it is injected into the
// context for every tool call the sub-agent makes. Called by the Engine
// before dispatching a Task tool invocation.
func (r *Runner) SetSessionID(sessionID string) {
	r.mu.Lock()
	r.sessionID = sessionID
	r.mu.Unlock()
}

// Run executes a sub-agent in its own Optimizer with the given agent
// definition and input prompt. Returns the agent's final response text and
// the total tokens used (for cost tracking).
func (r *Runner) Run(ctx context.Context, def *AgentDef, input string) (string, int, error) {
	// Resolve model
	provider, modelInfo, err := r.providerReg.ResolveModel(def.Model)
	if err != nil {
		return "", 0, fmt.Errorf("resolve model for sub-agent %q: %w", def.Name, err)
	}

	// Build a dedicated Optimizer for this sub-agent run.
	optCfg := tokenopt.DefaultConfig(modelInfo)
	optCfg.SystemPrompt = def.SystemPrompt
	optCfg.ProviderName = modelInfo.Provider
	opt := tokenopt.New(optCfg)

	// Filter tool definitions to the allowed subset.
	toolDefs := r.toolReg.ListDefs()
	if len(def.Tools) > 0 {
		allowed := make(map[string]bool, len(def.Tools))
		for _, t := range def.Tools {
			allowed[strings.ToLower(t)] = true
		}
		filtered := make([]types.ToolDef, 0, len(allowed))
		for _, td := range toolDefs {
			if allowed[td.Name] {
				filtered = append(filtered, td)
			}
		}
		toolDefs = filtered
	}
	opt.SetTools(toolDefs)

	// One-shot prompt: just add the user message and go.
	opt.AddMessage(types.Message{
		Role:      types.RoleUser,
		Content:   input,
		Timestamp: time.Now(),
	})

	// Agentic loop with its own context and token budget.
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	startTime := time.Now()
	var finalText strings.Builder
	const maxRounds = 12
	depth := def.MaxRounds
	if depth <= 0 || depth > maxRounds {
		depth = maxRounds
	}

	for round := 0; round < depth; round++ {
		select {
		case <-subCtx.Done():
			return finalText.String(), 0, subCtx.Err()
		default:
		}

		messages := opt.CompactRequest("")
		eventCh, err := provider.ChatStream(subCtx, types.ChatRequest{
			SessionID:    r.sessionID,
			Messages:     messages,
			Model:        modelInfo.ID,
			ProviderName: modelInfo.Provider,
			SystemPrompt: opt.BuildPrefix(),
			Tools:        toolDefs,
			MaxTokens:    def.MaxTokens,
			Temperature:  0.1,
		})
		if err != nil {
			if finalText.Len() == 0 {
				return "", 0, fmt.Errorf("sub-agent %q chat stream: %w", def.Name, err)
			}
			return finalText.String(), 0, nil
		}

		var assistantMsg types.Message
		assistantMsg.Role = types.RoleAssistant
		assistantMsg.Timestamp = time.Now()
		var toolCalls []types.ToolCall

		for event := range eventCh {
			switch event.Type {
			case types.EventText:
				assistantMsg.Content += event.Content

			case types.EventToolUse:
				tc := types.ToolCall{
					ID:        event.ToolCall.ID,
					Name:      event.ToolCall.Name,
					Arguments: event.ToolCall.Arguments,
				}
				toolCalls = append(toolCalls, tc)

			case types.EventDone:
				if len(toolCalls) == 0 {
					finalText.WriteString(assistantMsg.Content)
					opt.AddMessage(assistantMsg)
					opt.RecordUsage(event.Meta.Usage, 0, startTime)
					stats := opt.Stats()
					return finalText.String(), stats.TotalTokens, nil
				}

				// Record assistant message with tool calls
				assistantMsg.ToolCalls = toolCalls
				opt.AddMessage(assistantMsg)

				// Execute tool calls
				for i, tc := range toolCalls {
					tc.Result = r.executeTool(subCtx, tc)
					toolCalls[i] = tc
					if tc.Result != nil {
						opt.AddMessage(types.Message{
							Role:      types.RoleTool,
							Content:   tc.Result.Content,
							ToolID:    tc.ID,
							Timestamp: time.Now(),
						})
					}
				}
				toolCalls = nil

			case types.EventError:
				if finalText.Len() == 0 {
					return "", 0, fmt.Errorf("sub-agent %q error: %s", def.Name, event.Content)
				}
				return finalText.String(), 0, nil
			}
		}
	}

	return finalText.String(), 0, nil
}

// executeTool runs a single tool call for the sub-agent.
func (r *Runner) executeTool(ctx context.Context, tc types.ToolCall) *types.ToolResult {
	toolCtx := tool.WithSessionID(ctx, r.sessionID)
	res, err := r.toolReg.Execute(toolCtx, tc.Name, tc.Arguments)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}
	}
	return res
}
