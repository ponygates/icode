// Package conversation implements the core conversation loop for iCode.
package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/config"
	projectcontext "github.com/ponygates/icode/internal/core/context"
	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/privacy"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/llm/tokenopt"
	"github.com/ponygates/icode/internal/types"
)

// PermissionHandler resolves an interactive "ask" decision (agent mode) and
// returns the user's final choice.
type PermissionHandler func(sessionID string, req *types.PermissionReq, res permission.CheckResult) permission.Decision

// Engine drives the agentic conversation loop.
type Engine struct {
	providerReg types.ProviderRegistry
	toolReg     *tool.Registry
	sessionSt   types.SessionStore
	gate        *permission.Gate
	permHandler PermissionHandler

	mu          sync.Mutex
	optimizers  map[string]*tokenopt.Optimizer
	stopFns     map[string]context.CancelFunc

	permMu       sync.Mutex
	permRespChans map[string]chan permission.Decision
	permSeq      uint64

	temperature    float64
	maxTokens      int
	systemPrompt   string   // user-configured system prompt override
	fallbackModels []string // model IDs to try if the primary fails

	// Sub-agent runner — dispatches Task tool calls to isolated Optimizer
	// contexts. Created on first use so the tool registry is ready.
	agentRunner    *agent.Runner
	agentRegistry  *agent.Registry
	loadAgentsOnce sync.Once

	// Doom-loop detector prevents the model from repeating the same tool
	// call more than N consecutive times (OpenCode parity).
	doomLoop *DoomLoopDetector
}

// NewEngine creates a conversation engine.
func NewEngine(
	providerReg types.ProviderRegistry,
	sessionSt types.SessionStore,
	gate *permission.Gate,
) *Engine {
	e := &Engine{
		providerReg:    providerReg,
		toolReg:        tool.NewRegistry(),
		sessionSt:      sessionSt,
		gate:           gate,
		optimizers:     make(map[string]*tokenopt.Optimizer),
		stopFns:        make(map[string]context.CancelFunc),
		permRespChans:  make(map[string]chan permission.Decision),
		doomLoop:       NewDoomLoopDetector(),
	}
	// Register the Task tool (sub-agent dispatcher).
	e.toolReg.Register(tool.NewTaskTool(e))
	return e
}

func (e *Engine) SetPermissionHandler(fn PermissionHandler) {
	e.permHandler = fn
}

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

func (e *Engine) SetGenerationParams(temperature float64, maxTokens int) {
	e.temperature = temperature
	e.maxTokens = maxTokens
}

// SetSystemPrompt configures a user-defined system prompt override.
// When set, this replaces the hardcoded base system prompt.
// Pass an empty string to restore the default.
func (e *Engine) SetSystemPrompt(prompt string) {
	e.systemPrompt = prompt
}

// SetFallbackModels configures model IDs to try if the primary model fails.
// Each entry should be a valid model ID (e.g. "deepseek-v4-flash").
func (e *Engine) SetFallbackModels(models []string) {
	e.fallbackModels = models
}

func (e *Engine) RegisterTool(t types.Tool) {
	e.toolReg.Register(t)
}

func (e *Engine) UnregisterTool(name string) {
	e.toolReg.Unregister(name)
}

// WireTaskRunner injects the engine as the sub-agent runner into the
// tool registry so the Task tool can delegate to sub-agents.
func (e *Engine) WireTaskRunner() {
	e.toolReg.SetTaskRunner(e)
}

// ExecuteTool runs a tool directly without going through the permission
// gate. Used by CLI commands (cleanup, etc.) and model-free operations.
func (e *Engine) ExecuteTool(name string, args string) *types.ToolResult {
	ctx := context.Background()
	tc := types.ToolCall{Name: name, Arguments: args}
	return e.runTool(ctx, tc)
}

// GetToolRules returns persistent per-tool permission rules from the gate.
func (e *Engine) GetToolRules() map[string]string {
	if e.gate == nil {
		return nil
	}
	return e.gate.GetToolRules()
}

// RunSubAgent implements tool.SubAgentRunner. It delegates to a sub-agent
// that runs in an isolated Optimizer context — the main conversation never
// sees the intermediate tool results, only the final answer.
func (e *Engine) RunSubAgent(ctx context.Context, name, prompt string) (string, int, error) {
	e.mu.Lock()
	runner := e.getAgentRunner()
	reg := e.agentRegistry
	e.mu.Unlock()

	def, ok := reg.Get(name)
	if !ok {
		// If the requested agent isn't defined, fall back to a reasonable
		// default based on the name pattern.
		known := make([]string, 0, len(reg.List()))
		for _, d := range reg.List() {
			known = append(known, d.Name)
		}
		return "", 0, fmt.Errorf("unknown sub-agent %q (available: %v)", name, known)
	}
	return runner.Run(ctx, def, prompt)
}

// getAgentRunner lazily initialises the sub-agent runner and loads agent
// definitions from disk (with built-in defaults as fallback).
func (e *Engine) getAgentRunner() *agent.Runner {
	e.loadAgentsOnce.Do(func() {
		reg := agent.Load(agent.AgentDefaultDirs()...)
		reg.RegisterDefaults()
		e.agentRegistry = reg
		e.agentRunner = agent.NewRunner(e.providerReg, e.toolReg)
	})
	return e.agentRunner
}

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
	case "task":
		a.Command = getStr("name")
		a.Pattern = getStr("prompt")
	}
	return a
}

// parseToolArgs extracts tool arguments from a JSON string.
// Returns nil on parse failure.
func parseToolArgs(args string) map[string]interface{} {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return nil
	}
	return m
}

func (e *Engine) executeTool(
	ctx context.Context,
	sessionID string,
	tc types.ToolCall,
	out chan types.StreamEvent,
) *types.ToolResult {
	ctx = tool.WithSessionID(ctx, sessionID)

	// Doom loop detection: if the same tool+args appears 3+ consecutive
	// times, emit a warning and return a failure to break the loop.
	if e.doomLoop.RecordCall(tc.Name, tc.Arguments) {
		status := e.doomLoop.DoomLoopStatus()
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("检测到 Doom Loop — AI 连续重复调用同一工具。\n%s\n请重新描述你的需求以改变策略。", status),
		}
	}

	action := buildAction(tc.Name, tc.Arguments)

	if e.gate == nil {
		return e.runTool(ctx, tc)
	}

	res := e.gate.Check(sessionID, action)
	switch res.Decision {
	case permission.DecisionAllow:
		return e.runTool(ctx, tc)
	case permission.DecisionDeny:
		// Track tool rejection for strategy-change forcing
		if e.doomLoop.RecordRejection(tc.Name) {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("「%s」已经被拒绝多次。AI 应更换方案，不要再调用此工具。", tc.Name),
			}
		}
		return &types.ToolResult{Success: false, Error: "Permission denied: " + res.Reason}
	case permission.DecisionAsk:
		if e.permHandler != nil {
			req := &types.PermissionReq{Tool: tc.Name, Prompt: res.Prompt}
			return e.applyDecision(ctx, sessionID, tc, e.permHandler(sessionID, req, res))
		}
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
	e.snapshotBeforeTool(ctx, tc)
	res, err := e.toolReg.Execute(ctx, tc.Name, tc.Arguments)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}
	}
	// Tool output dedup: if the same (tool + args) produced the same
	// content before, replace the result with a short placeholder to keep
	// the context lean. The model already saw this data on the previous
	// invocation — it only needs the confirmation that the result is
	// identical, not the full output again.
	if res.Success && res.Content != "" {
		replacement, dup := tokenopt.DefaultOutputCache.Lookup(tc.Name, tc.Arguments, res.Content)
		if dup {
			res.Content = replacement
		}
	}
	return res
}

// snapshotBeforeTool creates a checkpoint + file-level undo before mutating
// tool calls. The checkpoint stores session-level rollback data; the undo
// snapshot stores actual file content for the /undo command.
func (e *Engine) snapshotBeforeTool(ctx context.Context, tc types.ToolCall) {
	mutating := map[string]bool{"write_file": true, "edit": true, "bash": true}
	if !mutating[tc.Name] {
		return
	}
	sessionID := tool.SessionIDFromContext(ctx)
	if sessionID == "" {
		return
	}
	// File-level undo snapshot (OpenCode parity)
	var filePath string
	if m := parseToolArgs(tc.Arguments); m != nil {
		if p, ok := m["path"]; ok {
			filePath, _ = p.(string)
		} else if p, ok := m["file_path"]; ok {
			filePath, _ = p.(string)
		}
	}
	checkpoint.BeforeTool(ctx, tc.Name, filePath)
	store, err := checkpoint.GetOrOpen(sessionID)
	if err != nil {
		return // silently skip — checkpoints are best-effort
	}
	_, _ = store.Snapshot(ctx, "before "+tc.Name)
}

func (e *Engine) genPermID() string {
	e.permMu.Lock()
	e.permSeq++
	id := fmt.Sprintf("perm-%d", e.permSeq)
	e.permMu.Unlock()
	return id
}

func orMaxTokens(cfg, modelDefault int) int {
	if cfg > 0 {
		return cfg
	}
	return modelDefault
}

// Send starts a conversation turn and returns a stream of events.
// Security level is checked here to enforce the user's privacy boundary.
// Unlike Claude Code, iCode NEVER sends data externally without the user
// knowing exactly what level is active — shown in the TUI status bar.
func (e *Engine) Send(ctx context.Context, sessionID, content string, attachments ...[]types.Attachment) (<-chan types.StreamEvent, error) {
	// New user input resets the doom loop detector
	e.doomLoop.Reset()

	sess, err := e.sessionSt.Get(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	provider, modelInfo, err := e.providerReg.ResolveModel(sess.ModelID)
	if err != nil {
		return nil, fmt.Errorf("resolve model: %w", err)
	}

	// Security level enforcement: block or sanitize based on the user's
	// configured privacy boundary.
	if e.gate != nil {
		if err := e.gate.CheckProviderAccess(sess.ProviderName); err != nil {
			return nil, err
		}
	}

	opt := e.getOrCreateOptimizer(sessionID, modelInfo, sess.Messages)

	// Redact content if security level is "desensitize"
	sendContent := content
	if e.gate != nil && e.gate.SecurityLevel() == config.SecDesensitize {
		sendContent = privacy.Redact(content)
		// Also redact any past attachments in the session
	}

	userMsg := types.Message{
		Role:      types.RoleUser,
		Content:   sendContent,
		Timestamp: time.Now(),
	}
	if len(attachments) > 0 && len(attachments[0]) > 0 {
		userMsg.Attachments = attachments[0]
	}
	opt.AddMessage(userMsg)
	e.sessionSt.AppendMessage(sessionID, userMsg)

	messages := opt.CompactRequest("")

	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.stopFns[sessionID] = cancel
	e.mu.Unlock()

	startTime := time.Now()

	// Build fallback chain: primary + configured fallback models.
	type modelTry struct {
		modelID      string
		providerName string
		modelInfo    types.ModelInfo
		provider     types.Provider // nil for primary
		isFallback   bool
	}
	modelsToTry := []modelTry{
		{modelID: sess.ModelID, providerName: sess.ProviderName, modelInfo: modelInfo},
	}
	for _, fb := range e.fallbackModels {
		if fb == sess.ModelID {
			continue
		}
		p, mi, err := e.providerReg.ResolveModel(fb)
		if err == nil {
			modelsToTry = append(modelsToTry, modelTry{
				modelID: fb, providerName: mi.Provider, modelInfo: mi,
				provider: p, isFallback: true,
			})
		}
	}

	var eventCh <-chan types.StreamEvent
	var lastErr error
	for _, mt := range modelsToTry {
		if mt.isFallback && lastErr != nil {
			fmt.Printf("[iCode] fallback to model %s (previous: %v)\n", mt.modelID, lastErr)
		}
		p := mt.provider
		if p == nil {
			p = provider
		}
		eventCh, err = p.ChatStream(ctx, types.ChatRequest{
			SessionID:     sessionID,
			Messages:      messages,
			Model:         mt.modelID,
			ProviderName:  mt.providerName,
			SystemPrompt:  opt.BuildPrefix(),
			Tools:         e.toolReg.ListDefs(),
			MaxTokens:     orMaxTokens(e.maxTokens, mt.modelInfo.MaxOutputTokens),
			Temperature:     e.temperature,
			CacheBreakpoints: opt.BuildCacheBreakpoints(),
		})
		if err == nil {
			break
		}
		lastErr = err
	}
	if err != nil {
		cancel()
		if lastErr != nil {
			return nil, fmt.Errorf("all models failed, last: %w", lastErr)
		}
		return nil, fmt.Errorf("chat stream: %w", err)
	}

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
					out <- types.StreamEvent{
						Type:    types.EventText,
						Content: "\n[Continuing with tool results...]\n\n",
					}
					e.continueAgentLoop(ctx, sessionID, provider, opt, modelInfo, out, 0)
				} else {
					opt.RecordUsage(event.Meta.Usage, calculateCost(event.Meta.Usage, modelInfo), startTime)
					opt.AddMessage(assistantMsg)
					e.sessionSt.AppendMessage(sessionID, assistantMsg)
					out <- event
				}
				out <- types.StreamEvent{
					Type: types.EventDone,
					Meta: types.StreamMeta{Model: modelInfo.ID},
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
		MaxTokens:    orMaxTokens(e.maxTokens, modelInfo.MaxOutputTokens),
		Temperature:    e.temperature,
		CacheBreakpoints: opt.BuildCacheBreakpoints(),
	})
	if err != nil {
		out <- types.StreamEvent{Type: types.EventError, Content: err.Error()}
		return
	}

	var assistantMsg types.Message
	assistantMsg.Role = types.RoleAssistant
	assistantMsg.Timestamp = time.Now()
	var toolCalls []types.ToolCall
	acc := &textAccumulator{}

	for event := range eventCh {
		switch event.Type {
		case types.EventText:
			full, delta := acc.feed(event.Content)
			assistantMsg.Content = full
			if delta != "" {
				out <- types.StreamEvent{Type: types.EventText, Content: delta}
			}
		case types.EventToolUse:
			tc := types.ToolCall{
				ID:        event.ToolCall.ID,
				Name:      event.ToolCall.Name,
				Arguments: event.ToolCall.Arguments,
			}
			toolCalls = append(toolCalls, tc)
			out <- event
			// NOTE: tool execution deferred — see parallel execution below
		case types.EventDone:
			if len(toolCalls) > 0 {
				assistantMsg.ToolCalls = toolCalls
				opt.AddMessage(assistantMsg)
				e.sessionSt.AppendMessage(sessionID, assistantMsg)

				// Execute tools sequentially so permission requests don't deadlock.
				// Claude Code semantics: all tool calls from one model turn are
				// independent, but we execute them one at a time for safety.
				for i := range toolCalls {
					result := e.executeTool(ctx, sessionID, toolCalls[i], out)
					if result == nil {
						result = &types.ToolResult{Success: false, Error: "tool produced no result"}
					}
					toolCalls[i].Result = result
					var summary string
					if result.Error != "" {
						summary = result.Error
					} else {
						summary = firstN(result.Content, 200)
					}
					out <- types.StreamEvent{
						Type:    types.EventText,
						Content: fmt.Sprintf("\n[Tool: %s] %s\n", toolCalls[i].Name, summary),
					}
				}
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

// SessionStats returns token and cache statistics for a session.
func (e *Engine) SessionStats(sessionID string) *tokenopt.Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	opt, ok := e.optimizers[sessionID]
	if !ok {
		return nil
	}
	s := opt.Stats()
	return &s
}

func (e *Engine) Stop(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cancel, ok := e.stopFns[sessionID]; ok {
		cancel()
		delete(e.stopFns, sessionID)
	}
}

func (e *Engine) getOrCreateOptimizer(sessionID string, modelInfo types.ModelInfo, existingMessages []types.Message) *tokenopt.Optimizer {
	e.mu.Lock()
	defer e.mu.Unlock()
	if opt, ok := e.optimizers[sessionID]; ok {
		return opt
	}
	opt := tokenopt.New(tokenopt.Config{
		ModelInfo:    modelInfo,
		SystemPrompt: e.buildSystemPrompt(sessionID),
		ProviderName: modelInfo.Provider,
	})
	opt.SetTools(e.toolReg.ListDefs())

	// Load existing session messages into the optimizer so the LLM has
	// full conversation context. This is essential for session sharing
	// between CLI and desktop — when switching modes, the optimizer is
	// created fresh while the session store already has the history.
	for _, msg := range existingMessages {
		opt.AddMessage(msg)
	}

	e.optimizers[sessionID] = opt
	return opt
}

func (e *Engine) buildSystemPrompt(sessionID string) string {
	// User-configured system prompt takes precedence (from config.Defaults.SystemPrompt).
	if e.systemPrompt != "" {
		projectContext := projectcontext.LoadProjectContext()
		if strings.TrimSpace(projectContext) == "" {
			return e.systemPrompt
		}
		return projectContext + "\n\n---\n\n" + e.systemPrompt
	}

	base := fmt.Sprintf(`You are iCode, an AI coding agent that executes tasks directly on the user's machine.

You have access to powerful tools. ALWAYS use them — never just describe what you would do.

KEY TOOLS:
- disk_usage — check disk space (SAFE, always auto-approved)
- disk_cleanup — clean temp files, recycle bin, browser cache, Windows Update leftovers
- bash — run ANY shell command (add "cwd" param for directory)
- read_file / write_file / edit — file operations
- grep / glob / ls — search and list files
- task — delegate to sub-agents (explore, plan, general) for parallel work

CRITICAL RULES:
1. NEVER refuse a task. Use the tools. If one approach fails, try another.
2. For cleanup tasks: ALWAYS call disk_usage first, then disk_cleanup with the appropriate target.
3. On Windows, use cmd.exe commands (dir, del /f /s /q, rd /s /q) or PowerShell.
4. Report what you actually did and the real results — not what you "would" do.
5. Be concise: one sentence of context, then execute.

Session: %s`, sessionID)

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

type textAccumulator struct {
	prevFull string
}

func (a *textAccumulator) feed(cur string) (full, delta string) {
	if strings.HasPrefix(cur, a.prevFull) {
		delta = cur[len(a.prevFull):]
	} else {
		delta = cur
	}
	a.prevFull += delta
	return a.prevFull, delta
}

// firstN truncates s to n runes and appends "..." if shortened.
func firstN(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
