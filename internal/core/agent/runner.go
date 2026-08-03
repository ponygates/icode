// Package agent — sub-agent runner with independent Optimizer isolation.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/core/permission"
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
	gate        *permission.Gate
	mu          sync.Mutex

	sessionID string
}

func NewRunner(reg types.ProviderRegistry, tr *tool.Registry) *Runner {
	return &Runner{
		providerReg: reg,
		toolReg:     tr,
	}
}

func NewRunnerWithGate(reg types.ProviderRegistry, tr *tool.Registry, gate *permission.Gate) *Runner {
	return &Runner{
		providerReg: reg,
		toolReg:     tr,
		gate:        gate,
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

	doomCounter := make(map[string]int)
	const doomThreshold = 4

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

				assistantMsg.ToolCalls = toolCalls
				opt.AddMessage(assistantMsg)

				for i, tc := range toolCalls {
					doomCounter[tc.Name]++
					if doomCounter[tc.Name] >= doomThreshold {
						tc.Result = &types.ToolResult{
							Success: false,
							Error:   fmt.Sprintf("doom loop detected: tool %q called %d times consecutively, stopping", tc.Name, doomCounter[tc.Name]),
						}
						toolCalls[i] = tc
					} else {
						tc.Result = r.executeTool(subCtx, tc)
						toolCalls[i] = tc
					}
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

		allDoomed := true
		for _, c := range doomCounter {
			if c < doomThreshold {
				allDoomed = false
				break
			}
		}
		if allDoomed && len(doomCounter) > 0 {
			finalText.WriteString(fmt.Sprintf("\n[sub-agent %q stopped: doom loop detected]", def.Name))
			break
		}
	}

	return finalText.String(), 0, nil
}

// executeTool runs a single tool call for the sub-agent.
func (r *Runner) executeTool(ctx context.Context, tc types.ToolCall) *types.ToolResult {
	if r.gate != nil {
		action := permission.Action{
			Tool:      tc.Name,
			Arguments: tc.Arguments,
		}
		if cmd := extractCmdFromArgs(tc.Arguments); cmd != "" {
			action.Command = cmd
		}
		res := r.gate.Check(r.sessionID, action)
		switch res.Decision {
		case permission.DecisionDeny:
			return &types.ToolResult{Success: false, Error: "Permission denied (sub-agent): " + res.Reason}
		case permission.DecisionAsk:
			return &types.ToolResult{Success: false, Error: "Sub-agent tool calls require auto-approve or YOLO mode"}
		}
	}
	toolCtx := tool.WithSessionID(ctx, r.sessionID)
	res, err := r.toolReg.Execute(toolCtx, tc.Name, tc.Arguments)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}
	}
	return res
}

func extractCmdFromArgs(args string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return ""
	}
	if raw, ok := m["command"]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return ""
}
