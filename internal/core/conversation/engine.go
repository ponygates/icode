// Package conversation implements the core conversation loop for iCode.
package conversation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/llm/tokenopt"
	"github.com/ponygates/icode/internal/types"
)

// Engine drives the agentic conversation loop.
type Engine struct {
	providerReg types.ProviderRegistry
	toolReg     *tool.Registry
	sessionSt   types.SessionStore

	mu          sync.Mutex
	optimizers  map[string]*tokenopt.Optimizer // sessionID → optimizer
	stopFns     map[string]context.CancelFunc
}

// NewEngine creates a conversation engine.
func NewEngine(
	providerReg types.ProviderRegistry,
	sessionSt types.SessionStore,
) *Engine {
	return &Engine{
		providerReg: providerReg,
		toolReg:     tool.NewRegistry(),
		sessionSt:   sessionSt,
		optimizers:  make(map[string]*tokenopt.Optimizer),
		stopFns:     make(map[string]context.CancelFunc),
	}
}

// Send starts a conversation turn and returns a stream of events.
func (e *Engine) Send(ctx context.Context, sessionID, content string) (<-chan types.StreamEvent, error) {
	sess, err := e.sessionSt.Get(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	provider, modelInfo, err := e.providerReg.ResolveModel(sess.ModelID)
	if err != nil {
		return nil, fmt.Errorf("resolve model: %w", err)
	}

	// Create or reuse token optimizer for this session
	opt := e.getOrCreateOptimizer(sessionID, modelInfo)

	// Record user message
	userMsg := types.Message{
		Role:      types.RoleUser,
		Content:   content,
		Timestamp: time.Now(),
	}
	opt.AddMessage(userMsg)
	e.sessionSt.AppendMessage(sessionID, userMsg)

	// Build request
	messages := opt.CompactRequest("") // input already added in AddMessage

	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.stopFns[sessionID] = cancel
	e.mu.Unlock()

	startTime := time.Now()

	eventCh, err := provider.ChatStream(ctx, types.ChatRequest{
		SessionID:     sessionID,
		Messages:      messages,
		Model:         sess.ModelID,
		ProviderName:  sess.ProviderName,
		SystemPrompt:  opt.BuildPrefix(),
		Tools:         e.toolReg.ListDefs(),
		MaxTokens:     modelInfo.MaxOutputTokens,
		Temperature:   0.0,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("chat stream: %w", err)
	}

	// Wrap the stream to accumulate usage and record assistant messages
	out := make(chan types.StreamEvent, 64)
	go func() {
		defer close(out)
		defer cancel()

		var assistantMsg types.Message
		assistantMsg.Role = types.RoleAssistant
		assistantMsg.Timestamp = time.Now()

		var toolCalls []types.ToolCall

		for event := range eventCh {
			switch event.Type {
			case types.EventText:
				assistantMsg.Content += event.Content
				out <- event

			case types.EventToolUse:
				tc := types.ToolCall{
					ID:        event.ToolCall.ID,
					Name:      event.ToolCall.Name,
					Arguments: event.ToolCall.Arguments,
				}
				toolCalls = append(toolCalls, tc)
				out <- event

				// Execute the tool immediately
				result, execErr := e.toolReg.Execute(ctx, tc.Name, tc.Arguments)
				if execErr != nil {
					out <- types.StreamEvent{
						Type:    types.EventError,
						Content: execErr.Error(),
					}
					return
				}

				// Store tool call + result
				tc.Result = result
				out <- types.StreamEvent{
					Type:    types.EventText,
					Content: fmt.Sprintf("\n[Tool: %s] %s\n", tc.Name, result.Content),
				}

			case types.EventDone:
				if len(toolCalls) > 0 {
					// Agent had tool calls — record and continue
					assistantMsg.ToolCalls = toolCalls
					opt.AddMessage(assistantMsg)
					e.sessionSt.AppendMessage(sessionID, assistantMsg)

					// Record tool results
					for _, tc := range toolCalls {
						if tc.Result != nil {
							toolMsg := types.Message{
								Role:      types.RoleTool,
								Content:   tc.Result.Content,
								ToolID:    tc.ID,
								Timestamp: time.Now(),
							}
							opt.AddMessage(toolMsg)
							e.sessionSt.AppendMessage(sessionID, toolMsg)
						}
					}

					// Send follow-up: "continue with the tool results"
					out <- types.StreamEvent{
						Type:    types.EventText,
						Content: "\n[Continuing with tool results...]\n\n",
					}

					// Continue the agent loop with tool results
					e.continueAgentLoop(ctx, sessionID, provider, opt, modelInfo, out, 0)

				} else {
					// Simple text response — done
					opt.RecordUsage(event.Meta.Usage, calculateCost(event.Meta.Usage, modelInfo), startTime)
					opt.AddMessage(assistantMsg)
					e.sessionSt.AppendMessage(sessionID, assistantMsg)
					out <- event
				}

				out <- types.StreamEvent{
					Type: types.EventDone,
					Meta: types.StreamMeta{
						Model: modelInfo.ID,
					},
				}
				return

			case types.EventError:
				out <- event
				return
			}
		}
	}()

	return out, nil
}

// continueAgentLoop recursively continues the agent loop after tool results.
func (e *Engine) continueAgentLoop(
	ctx context.Context,
	sessionID string,
	provider types.Provider,
	opt *tokenopt.Optimizer,
	modelInfo types.ModelInfo,
	out chan types.StreamEvent,
	depth int,
) {
	const maxToolRounds = 10
	if depth >= maxToolRounds {
		out <- types.StreamEvent{
			Type:    types.EventText,
			Content: fmt.Sprintf("\n[Max tool rounds (%d) reached. Stopping.]\n", maxToolRounds),
		}
		return
	}

	select {
	case <-ctx.Done():
		return
	default:
	}

	messages := opt.CompactRequest("")
	startTime := time.Now()

	eventCh, err := provider.ChatStream(ctx, types.ChatRequest{
		SessionID:    sessionID,
		Messages:     messages,
		Model:        modelInfo.ID,
		ProviderName: modelInfo.Provider,
		SystemPrompt: opt.BuildPrefix(),
		Tools:        e.toolReg.ListDefs(),
		MaxTokens:    modelInfo.MaxOutputTokens,
		Temperature:  0.0,
	})
	if err != nil {
		out <- types.StreamEvent{Type: types.EventError, Content: err.Error()}
		return
	}

	var assistantMsg types.Message
	assistantMsg.Role = types.RoleAssistant
	assistantMsg.Timestamp = time.Now()
	var toolCalls []types.ToolCall

	for event := range eventCh {
		switch event.Type {
		case types.EventText:
			assistantMsg.Content += event.Content
			out <- event
		case types.EventToolUse:
			tc := types.ToolCall{
				ID:        event.ToolCall.ID,
				Name:      event.ToolCall.Name,
				Arguments: event.ToolCall.Arguments,
			}
			toolCalls = append(toolCalls, tc)
			out <- event

			result, execErr := e.toolReg.Execute(ctx, tc.Name, tc.Arguments)
			if execErr != nil {
				out <- types.StreamEvent{Type: types.EventError, Content: execErr.Error()}
				return
			}
			tc.Result = result
			out <- types.StreamEvent{
				Type:    types.EventText,
				Content: fmt.Sprintf("\n[Tool: %s] %s\n", tc.Name, result.Content),
			}

		case types.EventDone:
			if len(toolCalls) > 0 {
				assistantMsg.ToolCalls = toolCalls
				opt.AddMessage(assistantMsg)
				e.sessionSt.AppendMessage(sessionID, assistantMsg)

				for _, tc := range toolCalls {
					if tc.Result != nil {
						opt.AddMessage(types.Message{
							Role: types.RoleTool, Content: tc.Result.Content,
							ToolID: tc.ID, Timestamp: time.Now(),
						})
					}
				}

				e.continueAgentLoop(ctx, sessionID, provider, opt, modelInfo, out, depth+1)
			} else {
				opt.RecordUsage(event.Meta.Usage, calculateCost(event.Meta.Usage, modelInfo), startTime)
				opt.AddMessage(assistantMsg)
				e.sessionSt.AppendMessage(sessionID, assistantMsg)
			}
			return
		case types.EventError:
			out <- event
			return
		}
	}
}

// Stop cancels the current in-flight request for a session.
func (e *Engine) Stop(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cancel, ok := e.stopFns[sessionID]; ok {
		cancel()
		delete(e.stopFns, sessionID)
	}
}

func (e *Engine) getOrCreateOptimizer(sessionID string, modelInfo types.ModelInfo) *tokenopt.Optimizer {
	e.mu.Lock()
	defer e.mu.Unlock()

	if opt, ok := e.optimizers[sessionID]; ok {
		return opt
	}

	systemPrompt := fmt.Sprintf(`You are iCode, a powerful AI coding agent. Your task is to help the user with software development tasks.

Instructions:
- Read files, write code, run commands, and search the codebase as needed.
- Use the provided tools to gather information and make changes.
- Be concise and direct. Show the user what you found and what you changed.
- If a command fails, explain why and suggest alternatives.
- Always explain your reasoning before making changes.

Current session ID: %s`, sessionID)

	opt := tokenopt.New(tokenopt.Config{
		ModelInfo:    modelInfo,
		SystemPrompt: systemPrompt,
		ProviderName: modelInfo.Provider,
	})
	opt.SetTools(e.toolReg.ListDefs())
	e.optimizers[sessionID] = opt
	return opt
}

func calculateCost(usage types.TokenUsage, model types.ModelInfo) float64 {
	if len(model.Plans) == 0 {
		return 0
	}
	plan := model.Plans[0]

	inputCost := float64(usage.PromptTokens-usage.CacheHitTokens) * plan.InputPrice / 1_000_000
	outputCost := float64(usage.CompletionTokens) * plan.OutputPrice / 1_000_000
	cacheCost := float64(usage.CacheHitTokens) * plan.CachePrice / 1_000_000

	return inputCost + outputCost + cacheCost
}
