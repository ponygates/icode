// Package conversation implements the core conversation loop for iCode.
package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	projectcontext "github.com/ponygates/icode/internal/core/context"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/llm/tokenopt"
	"github.com/ponygates/icode/internal/types"
)

// PermissionHandler resolves an interactive "ask" decision (agent mode) and
// returns the user's final choice. The CLI wires this to the TUI prompt; the
// server leaves it nil and instead streams a permission_request event for the
// desktop client to answer via /api/permission/respond.
type PermissionHandler func(sessionID string, req *types.PermissionReq, res permission.CheckResult) permission.Decision

// Engine drives the agentic conversation loop.
type Engine struct {
	providerReg types.ProviderRegistry
	toolReg     *tool.Registry
	sessionSt   types.SessionStore
	gate        *permission.Gate

	// permHandler is invoked for interactive "ask" decisions (CLI path).
	permHandler PermissionHandler

	mu          sync.Mutex
	optimizers  map[string]*tokenopt.Optimizer // sessionID → optimizer
	stopFns     map[string]context.CancelFunc

	// permMu / permRespChans handle the server (SSE) permission flow.
	permMu       sync.Mutex
	permRespChans map[string]chan permission.Decision
	permSeq      uint64
}

// NewEngine creates a conversation engine.
func NewEngine(
	providerReg types.ProviderRegistry,
	sessionSt types.SessionStore,
	gate *permission.Gate,
) *Engine {
	return &Engine{
		providerReg:   providerReg,
		toolReg:       tool.NewRegistry(),
		sessionSt:     sessionSt,
		gate:          gate,
		optimizers:    make(map[string]*tokenopt.Optimizer),
		stopFns:       make(map[string]context.CancelFunc),
		permRespChans: make(map[string]chan permission.Decision),
	}
}

// SetPermissionHandler wires an interactive permission resolver (CLI/TUI).
func (e *Engine) SetPermissionHandler(fn PermissionHandler) {
	e.permHandler = fn
}

// SetPermissionResponse delivers the user's decision for a permission request
// raised via the SSE flow (desktop client). It is called by the server's
// /api/permission/respond endpoint.
func (e *Engine) SetPermissionResponse(requestID string, decision permission.Decision) {
	e.permMu.Lock()
	ch, ok := e.permRespChans[requestID]
	delete(e.permRespChans, requestID)
	e.permMu.Unlock()
	if ok {
		select {
		case ch <- decision:
		default:
		}
	}
}

// buildAction constructs a permission.Action from a tool call's JSON arguments,
// extracting the relevant fields (command/path/pattern/url) the gate uses.
func buildAction(toolName, arguments string) permission.Action {
	a := permission.Action{Tool: toolName, Arguments: arguments}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &m); err != nil {
		return a
	}
	getStr := func(key string) string {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	switch toolName {
	case "bash":
		a.Command = getStr("command")
	case "read_file", "write_file":
		a.Path = getStr("path")
	case "edit":
		a.Path = getStr("file_path")
	case "grep":
		a.Pattern = getStr("pattern")
		a.Path = getStr("path")
	case "glob":
		a.Pattern = getStr("pattern")
	case "git_commit":
		a.Command = getStr("message")
	case "fetch":
		a.URL = getStr("url")
	}
	return a
}

// executeTool runs a tool after consulting the permission gate. The resulting
// ToolResult (which carries a denial message when blocked) is always returned
// so the conversation loop can feed it back to the model.
func (e *Engine) executeTool(
	ctx context.Context,
	sessionID string,
	tc types.ToolCall,
	out chan types.StreamEvent,
) *types.ToolResult {
	action := buildAction(tc.Name, tc.Arguments)

	// No gate configured → execute freely.
	if e.gate == nil {
		return e.runTool(ctx, tc)
	}

	res := e.gate.Check(sessionID, action)
	switch res.Decision {
	case permission.DecisionAllow:
		return e.runTool(ctx, tc)

	case permission.DecisionDeny:
		return &types.ToolResult{
			Success: false,
			Error:   "Permission denied: " + res.Reason,
		}

	case permission.DecisionAsk:
		if e.permHandler != nil {
			// CLI path: resolve in-process via the TUI prompt.
			req := &types.PermissionReq{Tool: tc.Name, Prompt: res.Prompt}
			return e.applyDecision(ctx, sessionID, tc, e.permHandler(sessionID, req, res))
		}
		// Server path: announce via the stream and wait for the SSE response.
		reqID := e.genPermID()
		req := &types.PermissionReq{RequestID: reqID, Tool: tc.Name, Prompt: res.Prompt}
		out <- types.StreamEvent{Type: types.EventPermission, Permission: req}
		ch := make(chan permission.Decision, 1)
		e.permMu.Lock()
		e.permRespChans[reqID] = ch
		e.permMu.Unlock()
		select {
		case decision := <-ch:
			return e.applyDecision(ctx, sessionID, tc, decision)
		case <-ctx.Done():
			return &types.ToolResult{Success: false, Error: "Permission request cancelled"}
		}

	default:
		return e.runTool(ctx, tc)
	}
}

func (e *Engine) applyDecision(ctx context.Context, sessionID string, tc types.ToolCall, decision permission.Decision) *types.ToolResult {
	switch decision {
	case permission.DecisionAllow:
		return e.runTool(ctx, tc)
	case permission.DecisionAllowAll:
		if e.gate != nil {
			e.gate.SetSessionAllow(sessionID, true)
		}
		return e.runTool(ctx, tc)
	default:
		return &types.ToolResult{Success: false, Error: "Permission denied by user"}
	}
}

func (e *Engine) runTool(ctx context.Context, tc types.ToolCall) *types.ToolResult {
	res, err := e.toolReg.Execute(ctx, tc.Name, tc.Arguments)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}
	}
	return res
}

func (e *Engine) genPermID() string {
	e.permMu.Lock()
	e.permSeq++
	id := fmt.Sprintf("perm-%d", e.permSeq)
	e.permMu.Unlock()
	return id
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

			// Execute the tool (after consulting the permission gate).
			result := e.executeTool(ctx, sessionID, tc, out)
			if result == nil {
				result = &types.ToolResult{Success: false, Error: "tool produced no result"}
			}

			// Store tool call + result
			tc.Result = result
			toolContent := result.Content
			if toolContent == "" && result.Error != "" {
				toolContent = result.Error
			}
			out <- types.StreamEvent{
				Type:    types.EventText,
				Content: fmt.Sprintf("\n[Tool: %s] %s\n", tc.Name, toolContent),
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

		result := e.executeTool(ctx, sessionID, tc, out)
		if result == nil {
			result = &types.ToolResult{Success: false, Error: "tool produced no result"}
		}
		tc.Result = result
		toolContent := result.Content
		if toolContent == "" && result.Error != "" {
			toolContent = result.Error
		}
		out <- types.StreamEvent{
			Type:    types.EventText,
			Content: fmt.Sprintf("\n[Tool: %s] %s\n", tc.Name, toolContent),
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

	systemPrompt := buildSystemPrompt(sessionID)

	opt := tokenopt.New(tokenopt.Config{
		ModelInfo:    modelInfo,
		SystemPrompt: systemPrompt,
		ProviderName: modelInfo.Provider,
	})
	opt.SetTools(e.toolReg.ListDefs())
	e.optimizers[sessionID] = opt
	return opt
}

// buildSystemPrompt constructs the system prompt for a session, prepending any
// ICODE.md project context found in the current or parent directories.
func buildSystemPrompt(sessionID string) string {
	base := fmt.Sprintf(`You are iCode, a powerful AI coding agent. Your task is to help the user with software development tasks.

Instructions:
- Read files, write code, run commands, and search the codebase as needed.
- Use the provided tools to gather information and make changes.
- Be concise and direct. Show the user what you found and what you changed.
- If a command fails, explain why and suggest alternatives.
- Always explain your reasoning before making changes.

Current session ID: %s`, sessionID)

	projectContext := projectcontext.LoadProjectContext()
	if strings.TrimSpace(projectContext) == "" {
		return base
	}

	return projectContext + "\n\n---\n\n" + base
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
