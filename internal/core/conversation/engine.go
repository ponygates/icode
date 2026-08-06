// Package conversation implements the core conversation loop for iCode.
package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/checkpoint"
	projectcontext "github.com/ponygates/icode/internal/core/context"
	"github.com/ponygates/icode/internal/core/hooks"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/prefmem"
	"github.com/ponygates/icode/internal/core/privacy"
	"github.com/ponygates/icode/internal/core/router"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/core/skills"
	"github.com/ponygates/icode/internal/core/slashcmd"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/llm/tokenopt"
	"github.com/ponygates/icode/internal/lsp"
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

	mu         sync.Mutex
	optimizers map[string]*tokenopt.Optimizer
	stopFns    map[string]context.CancelFunc

	permMu        sync.Mutex
	permRespChans map[string]chan permission.Decision
	permSeq       uint64

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

	// Budget enforcer (tokenopt Level 4) caps tool-output size per turn so a
	// single huge read/grep/bash never blows the context budget. Activated
	// here so the Cache-First Loop keeps saving tokens even on large repos.
	budgetEnforcer *tokenopt.BudgetEnforcer

	// Smart model router — selects the most cost-effective model based on
	// query complexity. When enabled, simple queries use cheap models and
	// complex tasks use powerful models. Set via SetRouter.
	modelRouter *router.Router

	// LSP manager for code intelligence and diagnostics. When set, the
	// engine checks for compilation errors after tool execution and
	// automatically injects fix hints to the model.
	lspManager *lsp.Manager

	// diagCache remembers the last LSP diagnostics text injected per file so
	// unchanged compile errors are not re-injected on every tool turn
	// (they would bloat the context and alert the model to errors it already
	// saw). Keyed by file path.
	diagCache map[string]string

	// Multi-agent team registry — teams defined via TeamDef are registered
	// here and dispatched by the task tool, just like single agents.
	teamRegistry map[string]*agent.TeamDef

	// Skill registry — SKILL.md files loaded from .icode/skills (user + project).
	// When set, the engine injects available skill definitions into the system
	// prompt so the model can follow them on demand (Claude Code parity).
	skillReg *skills.Registry

	// Lifecycle hooks runner — fires PreToolUse/PostToolUse/Stop external
	// commands (Claude Code parity). PreToolUse hooks can block a tool call.
	hooksRunner *hooks.Runner

	// truncDet detects responses cut off at max_tokens (finish_reason="length"
	// or a clearly mid-sentence stop) so the engine can retry with a bigger
	// output budget and a continuation hint (Claude Code parity).
	truncDet *TruncationDetector

	// prefMem remembers USER PREFERENCES (never code) across turns, injecting
	// them into the system prompt so the model respects how the user likes to
	// work. Stale entries age out automatically (prefmem.TTL). Book-inspired:
	// "remember preferences, never code."
	prefMem *prefmem.Store

	// prefSavePath is where prefMem is persisted. When set, learnPreferences
	// schedules a debounced auto-save so preferences survive a crash even if
	// App.Close() is never reached.
	prefSavePath string
	prefSaveTimer *time.Timer
	prefSaveMu    sync.Mutex
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
		diagCache:      make(map[string]string),
		permRespChans:  make(map[string]chan permission.Decision),
		doomLoop:       NewDoomLoopDetector(),
		teamRegistry:   make(map[string]*agent.TeamDef),
		budgetEnforcer: tokenopt.NewBudgetEnforcer(tokenopt.DefaultBudgetConfig()),
		truncDet:       NewTruncationDetector(DefaultTruncationRecoveryConfig()),
		prefMem:        prefmem.New(prefmem.Options{}),
	}
	if gate != nil {
		slashcmd.SetShellGate(&shellGateAdapter{gate: gate})
	}
	e.toolReg.Register(tool.NewTaskTool(e))
	return e
}

func (e *Engine) SetPermissionHandler(fn PermissionHandler) {
	e.permHandler = fn
}

// SetPreferenceMemory replaces the engine's preference memory Store, e.g.
// with one that has been Restore()d from disk. Pass nil to disable memory.
func (e *Engine) SetPreferenceMemory(s *prefmem.Store) {
	if s == nil {
		s = prefmem.New(prefmem.Options{})
	}
	e.prefMem = s
}

// SetPreferenceSavePath enables debounced auto-persistence of preference
// memory to path. Called after SetPreferenceMemory so a crash mid-session
// does not lose learned preferences.
func (e *Engine) SetPreferenceSavePath(path string) {
	e.prefSaveMu.Lock()
	defer e.prefSaveMu.Unlock()
	e.prefSavePath = path
}

// schedulePrefSave debounces preference persistence: at most one timer is
// armed, and it fires `delay` after the most recent learn so rapid turns
// coalesce into a single disk write.
func (e *Engine) schedulePrefSave(delay time.Duration) {
	if e.prefSavePath == "" || e.prefMem == nil {
		return
	}
	e.prefSaveMu.Lock()
	if e.prefSaveTimer != nil {
		e.prefSaveTimer.Stop()
	}
	e.prefSaveTimer = time.AfterFunc(delay, func() {
		if e.prefSavePath != "" && e.prefMem != nil {
			_ = e.prefMem.SaveFile(e.prefSavePath)
		}
	})
	e.prefSaveMu.Unlock()
}

// FlushPreferenceSave performs an immediate synchronous save, cancelling any
// pending debounce timer. Called on shutdown paths (App.Close) and available
// for tests.
func (e *Engine) FlushPreferenceSave() {
	e.prefSaveMu.Lock()
	if e.prefSaveTimer != nil {
		e.prefSaveTimer.Stop()
		e.prefSaveTimer = nil
	}
	path := e.prefSavePath
	e.prefSaveMu.Unlock()
	if path != "" && e.prefMem != nil {
		_ = e.prefMem.SaveFile(path)
	}
}

// PreferenceMemory exposes the engine's preference memory Store so the caller
// can persist (Snapshot) or clear (Purge) it independently of the session.
func (e *Engine) PreferenceMemory() *prefmem.Store {
	return e.prefMem
}

// CircuitBreakerStatus returns a snapshot of every tool's circuit breaker for
// the UI layer (diagnostics panel, /status, server API).
func (e *Engine) CircuitBreakerStatus() []CircuitStatus {
	return e.doomLoop.CircuitStatus()
}

// learnPreferences scans a user message for explicit preference statements
// and records them in memory (book: prefer a repeated, explicit statement;
// ignore everything else, especially code).
func (e *Engine) learnPreferences(content string) {
	if e.prefMem == nil {
		return
	}
	learned := false
	for _, pref := range prefmem.Extract(content) {
		e.prefMem.Remember(pref)
		learned = true
	}
	if learned {
		// Debounced persistence: crash-safe without spamming disk per turn.
		e.schedulePrefSave(2 * time.Second)
	}
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

// SetRouter enables smart model routing. When set, the engine will
// automatically select the most cost-effective model based on query
// complexity instead of always using the session's configured model.
func (e *Engine) SetRouter(r *router.Router) {
	e.modelRouter = r
}

// SetLSPManager attaches an LSP manager for code intelligence. When set,
// the engine checks for diagnostics after tool execution and injects
// auto-fix hints to the model on compilation errors.
func (e *Engine) SetLSPManager(m *lsp.Manager) {
	e.lspManager = m
}

// RegisterTeam registers a multi-agent team definition. Once registered,
// the team can be dispatched via the task tool just like a single agent.
// The team name is prefixed with "team:" to distinguish it from single agents.
func (e *Engine) RegisterTeam(def *agent.TeamDef) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.teamRegistry == nil {
		e.teamRegistry = make(map[string]*agent.TeamDef)
	}
	e.teamRegistry[def.Name] = def
}

// SetSkillsRegistry attaches a skill registry (SKILL.md loader). When set,
// the engine injects the available skill definitions into the system prompt
// so the model can discover and follow them. Call from app bootstrap.
func (e *Engine) SetSkillsRegistry(r *skills.Registry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.skillReg = r
}

// SetHooksRunner attaches a lifecycle hooks runner (PreToolUse/PostToolUse/
// Stop). Call from app bootstrap after loading config.
func (e *Engine) SetHooksRunner(r *hooks.Runner) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hooksRunner = r
}

// SetSkillLoader wires the on-demand skill resolver into the use_skill tool so
// the model can fetch a SKILL.md body without it ever entering the cached
// system prefix. Call from app bootstrap after SetSkillsRegistry.
func (e *Engine) SetSkillLoader(fn tool.SkillLoader) {
	e.toolReg.SetSkillsLoader(fn)
}

// SkillBody resolves a skill name to its full body + description for the
// use_skill tool. Returns ok=false when the skill is unknown.
func (e *Engine) SkillBody(name string) (body, description string, ok bool) {
	e.mu.Lock()
	reg := e.skillReg
	e.mu.Unlock()
	if reg == nil {
		return "", "", false
	}
	s, found := reg.Get(name)
	if !found {
		return "", "", false
	}
	return s.Body, s.Description, true
}

// SetMultimodalOptions injects the image_gen / video_gen backend config.
// Call from app bootstrap after loading config.
func (e *Engine) SetMultimodalOptions(opts tool.MultimodalOptions) {
	e.toolReg.SetMultimodalOptions(opts)
}

func (e *Engine) getHooksRunner() *hooks.Runner {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.hooksRunner
}

// ListSkills returns the skills currently registered with the engine
// (empty slice when no registry is attached). Used by the TUI /api surface.
func (e *Engine) ListSkills() []skills.Skill {
	e.mu.Lock()
	reg := e.skillReg
	e.mu.Unlock()
	if reg == nil {
		return nil
	}
	return reg.List()
}

// EnableSkill turns a skill's user-managed on/off switch on and persists it.
func (e *Engine) EnableSkill(name string) error {
	e.mu.Lock()
	reg := e.skillReg
	e.mu.Unlock()
	if reg == nil {
		return fmt.Errorf("no skill registry attached")
	}
	return reg.SetEnabled(name, true)
}

// DisableSkill turns a skill's user-managed on/off switch off and persists it.
func (e *Engine) DisableSkill(name string) error {
	e.mu.Lock()
	reg := e.skillReg
	e.mu.Unlock()
	if reg == nil {
		return fmt.Errorf("no skill registry attached")
	}
	return reg.SetEnabled(name, false)
}

// ListTeams returns the multi-agent teams registered with the engine.
func (e *Engine) ListTeams() []agent.TeamDef {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]agent.TeamDef, 0, len(e.teamRegistry))
	for _, def := range e.teamRegistry {
		out = append(out, *def)
	}
	return out
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
	// Bound execution so a hung tool (network read, stuck subprocess) cannot
	// block the caller indefinitely. 2 minutes matches the default tool budget.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
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
	// Check if the requested name is a multi-agent team
	teamDef, isTeam := e.teamRegistry[name]
	e.mu.Unlock()

	if isTeam && teamDef != nil {
		// Dispatch to team runner
		teamRunner := agent.NewTeamRunner(runner)
		result, err := teamRunner.Run(ctx, teamDef, prompt)
		if err != nil {
			return "", 0, fmt.Errorf("team %q failed: %w", name, err)
		}
		// Format the team result
		output := result.LeaderOutput
		if len(result.MemberOutputs) > 0 {
			output += "\n\n### Team Member Contributions\n"
			for member, out := range result.MemberOutputs {
				output += fmt.Sprintf("\n**%s**:\n%s\n", member, out)
			}
		}
		if len(result.Errors) > 0 {
			output += "\n\n### Errors\n"
			for _, err := range result.Errors {
				output += fmt.Sprintf("- %s\n", err)
			}
		}
		return output, result.TotalTokens, nil
	}

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
		e.agentRunner = agent.NewRunnerWithGate(e.providerReg, e.toolReg, e.gate)
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
	case "read_file", "write_file", "ls":
		a.Path = getStr("path")
	case "edit":
		a.Path = getStr("file_path")
	case "search_replace":
		a.Path = getStr("file_path")
	case "grep":
		a.Pattern = getStr("pattern")
		a.Path = getStr("path")
	case "glob":
		a.Pattern = getStr("pattern")
		if p := getStr("path"); p != "" {
			a.Path = p
		}
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

// parallelSafeTools are read-only tools with no side effects — safe to run
// concurrently within one model turn (Claude Code parallel tool use parity).
var parallelSafeTools = map[string]bool{
	"read_file": true, "ls": true, "grep": true, "glob": true,
	"git_diff": true, "git_status": true, "fetch": true,
	"disk_usage": true, "code_search": true, "web_search": true,
	"use_skill": true,
}

// executeToolBatch runs one model turn's tool calls: read-only tools execute
// concurrently (bounded at 4), mutating tools execute sequentially in their
// original order afterwards so permission prompts and writes never interleave.
// Results are written back into toolCalls and progress events are emitted in
// the original call order.
func (e *Engine) executeToolBatch(
	ctx context.Context,
	sessionID string,
	toolCalls []types.ToolCall,
	out chan types.StreamEvent,
) {
	finish := func(i int, result *types.ToolResult) {
		if result == nil {
			result = &types.ToolResult{Success: false, Error: "tool produced no result"}
		}
		toolCalls[i].Result = result
	}

	// Reset the per-turn tool-output budget before this model turn's tools
	// run. The budget caps the combined size of tool outputs so a single
	// oversized result can't exhaust the context window.
	e.budgetEnforcer.Reset()

	// Phase 1: read-only tools in parallel (only worth it for 2+).
	var parallel []int
	for i := range toolCalls {
		if parallelSafeTools[toolCalls[i].Name] {
			parallel = append(parallel, i)
		}
	}
	if len(parallel) >= 2 {
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for _, i := range parallel {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				// A panic in a parallel tool must not leave wg.Wait blocked
				// (which would hang the whole conversation loop). Recover,
				// mark the tool as failed, and keep going.
				defer func() {
					if r := recover(); r != nil {
						fmt.Fprintf(os.Stderr, "[engine] parallel tool panic: %v\n%s\n", r, debug.Stack())
						finish(i, &types.ToolResult{Success: false, Error: fmt.Sprintf("工具执行 panic（已恢复）: %v", r)})
					}
				}()
				sem <- struct{}{}
				defer func() { <-sem }()
				finish(i, e.executeTool(ctx, sessionID, toolCalls[i], out))
			}(i)
		}
		wg.Wait()
	}

	// Phase 2: everything not yet executed, sequentially, in original order.
	for i := range toolCalls {
		if toolCalls[i].Result == nil {
			finish(i, e.executeTool(ctx, sessionID, toolCalls[i], out))
		}
	}

	// Emit progress lines in original order for a stable transcript.
	for i := range toolCalls {
		res := toolCalls[i].Result
		summary := res.Error
		if summary == "" {
			summary = firstN(res.Content, 200)
		}
		out <- types.StreamEvent{
			Type:    types.EventText,
			Content: fmt.Sprintf("\n[Tool: %s] %s\n", toolCalls[i].Name, summary),
		}
	}
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

	// PreToolUse lifecycle hooks (Claude Code parity): an external command
	// exiting with code 2 blocks the tool call; its stderr is fed back to
	// the model so it can adjust course.
	if hr := e.getHooksRunner(); hr.HasHooks(hooks.PreToolUse) {
		res := hr.Fire(ctx, hooks.PreToolUse, hooks.Input{
			ToolName:  tc.Name,
			ToolInput: json.RawMessage(tc.Arguments),
			SessionID: sessionID,
		})
		if res.Block {
			return &types.ToolResult{
				Success: false,
				Error:   "PreToolUse hook blocked this call: " + res.Message,
			}
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
		e.rememberConnectDomain(tc)
		return e.runTool(ctx, tc)
	case permission.DecisionAllowAll:
		if e.gate != nil {
			e.gate.SetSessionAllow(sessionID, true)
		}
		e.rememberConnectDomain(tc)
		return e.runTool(ctx, tc)
	default:
		return &types.ToolResult{Success: false, Error: "Permission denied by user"}
	}
}

// rememberConnectDomain adds an approved Connect-tier destination (fetch, etc.)
// to the gate's silent whitelist so Auto mode won't re-prompt for it later.
// Called only after the user explicitly approves the call.
func (e *Engine) rememberConnectDomain(tc types.ToolCall) {
	if e.gate == nil || permission.AccessLevelOf(tc.Name) != permission.AccessConnect {
		return
	}
	a := buildAction(tc.Name, tc.Arguments)
	if a.URL == "" {
		return
	}
	e.gate.TrustDomain(a.URL)
}

func (e *Engine) runTool(ctx context.Context, tc types.ToolCall) *types.ToolResult {
	// Circuit breaker check BEFORE executing (本书 ch.23 三态熔断): an open
	// breaker blocks the tool until its cooldown elapses, then admits exactly
	// one probe. This prevents the model from hammering a broken tool every
	// turn while still auto-healing after the failure storm passes.
	if allowed, retryIn := e.doomLoop.CheckBreaker(tc.Name); !allowed {
		msg := fmt.Sprintf("工具「%s」正处于熔断状态，请更换方案（换工具/换参数），不要再调用它。", tc.Name)
		if retryIn > 0 {
			msg = fmt.Sprintf("工具「%s」已熔断，约 %s 后可重试一次。请先检查失败原因或更换方案。", tc.Name, retryIn.Round(time.Second))
		}
		return &types.ToolResult{Success: false, Error: msg}
	}

	e.snapshotBeforeTool(ctx, tc)
	res, err := e.toolReg.Execute(ctx, tc.Name, tc.Arguments)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}
	}

	// Circuit breaker POST-execution (本书 ch.23 熔断): a tool that keeps
	// failing — bash build errors, fetch timeouts, read errors — trips after
	// maxFailuresPerTool consecutive failures so the model is forced to
	// change strategy instead of retrying the same broken call forever. A
	// success closes the breaker (and, if it was half-open, heals it) so a
	// flaky tool that recovers is not kept tripped. Doom-loop signature
	// detection already covers the "same call repeated" case; this covers
	// "different calls, same tool, all failing".
	if !res.Success {
		if e.doomLoop.RecordFailure(tc.Name) {
			return &types.ToolResult{
				Success: false,
				Error: fmt.Sprintf(
					"工具「%s」已连续失败，触发熔断。请立即更换方案（换个参数、换工具、或先检查原因），不要再调用此工具。最近错误：%s",
					tc.Name, firstN(res.Error, 160),
				),
			}
		}
	} else {
		e.doomLoop.ResetToolFailures(tc.Name)
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
	// Level 4 budget enforcement: cap oversized tool outputs (read_file 50K,
	// bash 30K, grep 20K, global 200K) so the context stays within budget.
	// Runs after dedup so the dedup placeholder is never truncated.
	if res.Success && res.Content != "" {
		if trimmed, truncated := e.budgetEnforcer.Enforce(tc.Name, res.Content); truncated {
			res.Content = trimmed
		}
	}
	// PostToolUse lifecycle hooks: feedback from the hook (stderr) is
	// appended to the tool result so the model sees it on the next turn.
	if hr := e.getHooksRunner(); hr.HasHooks(hooks.PostToolUse) {
		hres := hr.Fire(ctx, hooks.PostToolUse, hooks.Input{
			ToolName:   tc.Name,
			ToolInput:  json.RawMessage(tc.Arguments),
			ToolOutput: truncateForHook(res.Content),
			SessionID:  tool.SessionIDFromContext(ctx),
		})
		if hres.Message != "" {
			res.Content += "\n\n[PostToolUse hook feedback]\n" + hres.Message
		}
	}
	return res
}

// truncateForHook caps tool output passed to hook processes at 32KB so huge
// outputs don't blow up the hook's stdin pipe.
func truncateForHook(s string) string {
	const max = 32 * 1024
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…(truncated)"
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

// stashIfPivot inspects the session before a new user message and, if the
// user is continuing a DIFFERENT task (a pivot) rather than answering the
// current one, snapshots a checkpoint and refreshes the archived summary so
// the interrupted task stays recoverable. Best-effort: failures are ignored.
func (e *Engine) stashIfPivot(ctx context.Context, sess *types.Session, newContent string) {
	if e.sessionSt == nil || sess == nil {
		return
	}

	// "In progress" = we have at least one prior user turn AND some tool
	// activity after it. Without both there is nothing worth stashing.
	lastUser := ""
	hasToolActivity := false
	for _, m := range sess.Messages {
		switch m.Role {
		case types.RoleUser:
			if t := strings.TrimSpace(m.Content); t != "" {
				lastUser = t
			}
		case types.RoleTool:
			hasToolActivity = true
		}
	}
	newContent = strings.TrimSpace(newContent)
	if lastUser == "" || newContent == "" || !hasToolActivity {
		return
	}
	// Is this actually a pivot? Two complementary signals:
	//  1. an explicit new-goal opener ("另外…", "顺便…", "新任务:"), or
	//  2. a topic switch — the new request shares almost no vocabulary with
	//     the last user request, so it is very likely a different task rather
	//     than a continuation ("帮我修个 bug" right after "写个报告").
	// Both are cheap and deterministic; no LLM call.
	if !isNewTaskPrompt(newContent) && !diverges(newContent, lastUser) {
		return
	}

	// Snapshot a checkpoint labeled with the *interrupted* task so /rewind can
	// return here. Best-effort: an empty shadow repo yields no commit, which
	// is fine — the summary note below is the authoritative stash record.
	store, err := checkpoint.GetOrOpen(sess.ID)
	if err == nil {
		_, _ = store.Snapshot(ctx, "stash before: "+truncStashMsg(lastUser))
	}
	// Refresh the archived summary so the resume layer sees the interrupted
	// work at a glance.
	summary := sessionum.Get(sess)
	if g := sessionum.Generate(sess, sess.ModelID, sess.ProviderName, ""); g != "" {
		summary = g
	}
	if strings.TrimSpace(summary) != "" {
		summary += fmt.Sprintf("\n\n(stash) 上个任务未完成: %s — 检查点已保存，可 /rewind 恢复。", truncStashMsg(lastUser))
		_ = sessionum.Save(e.sessionSt, sess, summary)
	}
}

// diverges reports whether newMsg is a different task than lastMsg by the
// share of significant tokens they have in common. Short follow-ups like
// "继续", "还有", "另外补一句" share little vocabulary but are continuations,
// so we require the new message to be long enough to be a real request and
// to share essentially no content words.
func diverges(newMsg, lastMsg string) bool {
	na := tokenizeForPivot(newMsg)
	la := tokenizeForPivot(lastMsg)
	if len(na) < 4 || len(la) < 4 {
		// Too short to judge by vocabulary — fall back to opener detection.
		return false
	}
	laSet := make(map[string]bool, len(la))
	for _, w := range la {
		laSet[w] = true
	}
	shared := 0
	for _, w := range na {
		if laSet[w] {
			shared++
		}
	}
	// <30% shared content vocabulary = a different topic.
	return float64(shared)/float64(len(na)) < 0.3
}

// tokenizeForPivot yields content tokens: whole ASCII words plus individual
// CJK characters (Chinese has no spaces, so character-level is the honest
// granularity here). Pure connectors/particles and single-char filler are
// dropped so overlap reflects real topic words.
func tokenizeForPivot(s string) []string {
	lower := strings.ToLower(s)
	stop := map[rune]bool{
		'的': true, '了': true, '是': true, '我': true, '你': true, '他': true, '她': true, '它': true,
		'们': true, '在': true, '和': true, '与': true, '或': true, '就': true, '都': true, '也': true,
		'还': true, '又': true, '要': true, '会': true, '能': true, '很': true, '更': true, '最': true,
		'吧': true, '吗': true, '呢': true, '啊': true, '嗯': true, '这': true, '那': true, '个': true,
		'有': true, '做': true, '用': true, '给': true, '让': true, '把': true, '被': true, '帮': true,
		'请': true, '下': true, '一': true, '并': true, '么': true, '些': true, '里': true,
		'来': true, '去': true, '到': true, '从': true, '对': true, '为': true, '上': true, '中': true,
		'等': true, '以': true, '可': true, '好': true, '大': true, '小': true, '新': true, '旧': true,
	}
	stopWord := map[string]bool{
		"the": true, "a": true, "an": true, "to": true, "of": true, "in": true, "on": true,
		"and": true, "or": true, "for": true, "with": true, "at": true, "it": true, "is": true,
		"are": true, "was": true, "i": true, "you": true, "me": true, "my": true, "please": true,
		"help": true, "do": true,
	}
	var out []string
	var word []rune
	flush := func() {
		if len(word) == 0 {
			return
		}
		w := string(word)
		if len(word) >= 2 && !stopWord[w] {
			out = append(out, w)
		}
		word = word[:0]
	}
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			word = append(word, r)
		case r >= 0x4e00 && r <= 0x9fff:
			flush()
			if !stop[r] {
				out = append(out, string(r))
			}
		default:
			flush()
		}
	}
	flush()
	return out
}

// isNewTaskPrompt is a conservative heuristic: does this user message OPEN a
// new/parallel task rather than continue the current one? It matches short
// command-like openers ("另外…", "顺便…", "新任务:", "同时…"). Plain
// continuation sentences are NOT treated as pivots.
func isNewTaskPrompt(s string) bool {
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	for _, m := range []string{
		"另外", "顺便", "与此同时", "同时", "接着", "然后", "新任务", "换个", "另外帮我",
		"紧接着", "另外，", "还有", "以及", "aside", "by the way", "also, ", "next: ",
	} {
		if strings.HasPrefix(lower, m) {
			return true
		}
	}
	return false
}

// truncStashMsg shortens a task reference used in stash labels/notes.
func truncStashMsg(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 60 {
		return s
	}
	return s[:57] + "..."
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

// runToolTurn persists the assistant message with its tool calls, executes the
// batch (read-only tools concurrently, mutating tools sequentially), feeds any
// generated images and LSP diagnostics back in, and resumes the agent loop.
func (e *Engine) runToolTurn(
	ctx context.Context,
	sessionID string,
	provider types.Provider,
	opt *tokenopt.Optimizer,
	modelInfo types.ModelInfo,
	assistantMsg types.Message,
	toolCalls []types.ToolCall,
	out chan types.StreamEvent,
	depth int,
) {
	assistantMsg.ToolCalls = toolCalls
	opt.AddMessage(assistantMsg)
	e.sessionSt.AppendMessage(sessionID, assistantMsg)

	e.executeToolBatch(ctx, sessionID, toolCalls, out)
	for _, tc := range toolCalls {
		if tc.Result != nil {
			opt.AddMessage(types.Message{
				Role: types.RoleTool, Content: tc.Result.Content,
				ToolID: tc.ID, Timestamp: time.Now(),
			})
		}
	}
	// Feed any generated images back into the conversation so vision-capable
	// models can see them on the next turn.
	e.ingestToolAttachments(sessionID, toolCalls, opt)
	// LSP diagnostics: inject compile errors as auto-fix hints so the model
	// can correct them in the continuation.
	if e.lspManager != nil {
		if diagnosticsMsg := e.collectDiagnostics(toolCalls); diagnosticsMsg != "" {
			opt.AddMessage(types.Message{
				Role: types.RoleSystem, Content: diagnosticsMsg, Timestamp: time.Now(),
			})
			out <- types.StreamEvent{Type: types.EventText, Content: diagnosticsMsg}
		}
	}
	out <- types.StreamEvent{
		Type:    types.EventText,
		Content: "\n[Continuing with tool results...]\n\n",
	}
	e.continueAgentLoop(ctx, sessionID, provider, opt, modelInfo, out, depth+1)
}

// finishTextTurn closes out a turn that produced no tool calls. If the model
// was cut off at max_tokens it transparently retries with a bigger budget and
// a continuation hint (truncation recovery) before persisting the reply.
func (e *Engine) finishTextTurn(
	ctx context.Context,
	sessionID string,
	provider types.Provider,
	opt *tokenopt.Optimizer,
	modelInfo types.ModelInfo,
	assistantMsg types.Message,
	done types.StreamEvent,
	startTime time.Time,
	out chan types.StreamEvent,
	depth int,
) {
	opt.RecordUsage(done.Meta.Usage, calculateCost(done.Meta.Usage, modelInfo), startTime)
	if e.truncDet != nil && e.truncDet.IsTruncated(done.Meta.FinishReason, assistantMsg.Content) && ctx.Err() == nil {
		if recovered := e.recoverTruncation(ctx, sessionID, provider, opt, modelInfo, &assistantMsg, out); recovered {
			if len(assistantMsg.ToolCalls) > 0 {
				e.runToolTurn(ctx, sessionID, provider, opt, modelInfo, assistantMsg, assistantMsg.ToolCalls, out, depth)
				return
			}
		}
	}
	opt.AddMessage(assistantMsg)
	e.sessionSt.AppendMessage(sessionID, assistantMsg)
	// Plan mode: the finished reply is a proposal, so surface it as a pending
	// plan the UI can offer to confirm (Enter) or discard (Esc). Confirmation
	// switches the gate out of read-only plan and continues execution.
	if e.gate != nil && e.gate.Mode() == permission.ModePlan {
		out <- types.StreamEvent{Type: types.EventPlanProposal}
	}
	out <- done
}

// recoverTruncation retries a truncated assistant reply with a larger
// max_tokens and a continuation hint (Claude Code parity). The continuation
// text is appended to msg; if the model instead emits tool calls they are
// returned on msg.ToolCalls for normal batch execution. Escalates through
// 8K→16K→32K→64K up to MaxRetries.
func (e *Engine) recoverTruncation(
	ctx context.Context,
	sessionID string,
	provider types.Provider,
	opt *tokenopt.Optimizer,
	modelInfo types.ModelInfo,
	msg *types.Message,
	out chan types.StreamEvent,
) bool {
	tr := e.truncDet
	if tr == nil {
		return false
	}
	maxTokens := orMaxTokens(e.maxTokens, modelInfo.MaxOutputTokens)
	recovered := false
	for attempt := 0; attempt < tr.config.MaxRetries && ctx.Err() == nil; attempt++ {
		nextMax := tr.config.NextTokens(maxTokens)
		if nextMax <= maxTokens {
			return recovered // already at the ceiling, nothing more to escalate
		}
		maxTokens = nextMax

		// Continue from the cut-off tail — never apologise or restart.
		opt.AddMessage(types.Message{
			Role:      types.RoleUser,
			Content:   tr.config.BuildRetryPrompt(firstN(msg.Content, 1500)),
			Timestamp: time.Now(),
		})

		ch, err := provider.ChatStream(ctx, types.ChatRequest{
			SessionID:        sessionID,
			Messages:         opt.CompactRequest(""),
			Model:            modelInfo.ID,
			ProviderName:     modelInfo.Provider,
			SystemPrompt:     opt.BuildPrefix(),
			Tools:            e.toolReg.ListDefs(),
			MaxTokens:        maxTokens,
			Temperature:      e.temperature,
			CacheBreakpoints: opt.BuildCacheBreakpoints(),
		})
		if err != nil {
			out <- types.StreamEvent{Type: types.EventError, Content: err.Error()}
			return recovered
		}
		recovered = true

		var contCalls []types.ToolCall
		stillTruncated := false
		for ev := range ch {
			switch ev.Type {
			case types.EventText:
				msg.Content += ev.Content
				out <- ev
			case types.EventThinking:
				out <- ev
			case types.EventToolUse:
				contCalls = append(contCalls, types.ToolCall{
					ID: ev.ToolCall.ID, Name: ev.ToolCall.Name, Arguments: ev.ToolCall.Arguments,
				})
				out <- ev
			case types.EventError:
				out <- ev
				stillTruncated = false
			case types.EventDone:
				stillTruncated = tr.IsTruncated(ev.Meta.FinishReason, msg.Content)
			}
		}
		if len(contCalls) > 0 {
			msg.ToolCalls = contCalls
			return recovered
		}
		if !stillTruncated {
			return recovered
		}
	}
	return recovered
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

	// Stash-and-continue audit: if the user pivots to a (different) task while
	// in-progress work exists in this session, snapshot a checkpoint + refresh
	// the archived summary so the old task remains recoverable. This is
	// best-effort — it must never block the conversation.
	e.stashIfPivot(ctx, sess, content)

	// Smart model routing: only engage when the user has NOT explicitly
	// chosen a model (session model empty or "auto"). An explicit selection
	// made in the UI/config (e.g. openrouter/free) is always respected, so
	// the router can never hijack it with its own default (which may point
	// to a provider that has no API key configured).
	modelID := sess.ModelID
	if e.modelRouter != nil && (sess.ModelID == "" || sess.ModelID == "auto") {
		route := e.modelRouter.RouteQuery(content, len(sess.Messages))
		if route.ModelID != "" {
			modelID = route.ModelID
		}
	}

	provider, modelInfo, err := e.providerReg.ResolveModel(modelID)
	if err != nil {
		// Fallback to the session's configured model
		provider, modelInfo, err = e.providerReg.ResolveModel(sess.ModelID)
		if err != nil {
			return nil, fmt.Errorf("resolve model: %w", err)
		}
	}

	// Security level enforcement: block or sanitize based on the user's
	// configured privacy boundary.
	if e.gate != nil {
		if err := e.gate.CheckProviderAccess(sess.ProviderName); err != nil {
			return nil, err
		}
	}

	// Lite-resume mode: when the session was resumed with /resume --lite, feed
	// the model only the archived summary + the most recent n messages instead
	// of the whole transcript. The summary is injected as a preset prefix (see
	// getOrCreateOptimizer), so the model keeps the gist without the cost of
	// replaying every old turn.
	//
	// Token budget guard (/budget): when a hard budget is set, shrink the
	// context to fit it (summary + recent messages) instead of growing
	// unbounded. The trim is re-computed every turn so it tracks the budget
	// as the conversation grows.
	msgs := sess.Messages
	budget := sessionum.BudgetMax(sess)
	trimmed := false
	var warnMsg string
	if budget > 0 {
		if sessionum.Get(sess) == "" {
			mode := ""
			if e.gate != nil {
				mode = string(e.gate.Mode())
			}
			_ = sessionum.Save(e.sessionSt, sess, sessionum.Generate(sess, modelID, sess.ProviderName, mode))
		}
		msgs, trimmed = sessionum.TrimToBudget(msgs, budget)
		if trimmed {
			_ = sessionum.RecordTrim(e.sessionSt, sess)
		}
		if warn, used, b := sessionum.BudgetWarning(e.sessionSt, sess); warn {
			_ = sessionum.RecordWarn(e.sessionSt, sess)
			warnMsg = fmt.Sprintf("ⓘ [预算护栏] 已用约 %d/%d tokens（%d%%），接近上限，即将自动压缩。", used, b, used*100/b)
		}
	} else if n := sessionum.LiteN(sess); n > 0 && n < len(msgs) {
		msgs = msgs[len(msgs)-n:]
	}

	opt := e.getOrCreateOptimizer(sessionID, modelInfo, msgs, sessionum.Get(sess))
	// A hard /budget trim computed above must be reflected in the cached
	// optimizer's log; getOrCreateOptimizer only seeds a fresh optimizer, so
	// on trimmed turns we replace the log with the trimmed subset to make the
	// budget actually bind for in-progress sessions.
	if trimmed {
		opt.ReplaceMessages(msgs)
	}

	// Long-goal mode: when the session has a goal, re-inject it into the
	// system prompt every turn so the model keeps working toward it. The
	// optimizer's SetSystemPrompt is a no-op when unchanged, keeping the
	// provider cache prefix stable.
	if goal := sessionum.GetGoal(sess); goal != "" {
		opt.SetSystemPrompt(e.buildSystemPrompt(sessionID) + "\n\nCURRENT GOAL (keep working toward this until done):\n" + goal)
	}

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

	// Preference memory: learn from what the user just said (only explicit
	// preference statements — never code). The block is injected into the
	// system prompt on subsequent turns.
	e.learnPreferences(sendContent)

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
			SessionID:        sessionID,
			Messages:         messages,
			Model:            mt.modelID,
			ProviderName:     mt.providerName,
			SystemPrompt:     opt.BuildPrefix(),
			Tools:            e.toolReg.ListDefs(),
			MaxTokens:        orMaxTokens(e.maxTokens, mt.modelInfo.MaxOutputTokens),
			Temperature:      e.temperature,
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
	if trimmed {
		out <- types.StreamEvent{Type: types.EventSystem, Content: "ⓘ [预算护栏] 会话上下文超出预算，已自动压缩为摘要 + 最近消息（≤ 预算）。"}
	} else if warnMsg != "" {
		out <- types.StreamEvent{Type: types.EventSystem, Content: warnMsg}
	}
	go func() {
		defer close(out)
		defer cancel()
		// A panic in streaming/tool execution must never kill the whole CLI
		// process (the classic silent "flash close" / 闪退). Recover here, log
		// the stack, and surface a user-visible error event so the session
		// survives.
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "[engine] stream goroutine panic: %v\n%s\n", r, debug.Stack())
				select {
				case out <- types.StreamEvent{Type: types.EventError, Content: fmt.Sprintf("引擎内部错误（已自动恢复）: %v", r)}:
				default:
				}
			}
		}()

		var assistantMsg types.Message
		assistantMsg.Role = types.RoleAssistant
		assistantMsg.Timestamp = time.Now()
		var toolCalls []types.ToolCall

		for event := range eventCh {
			switch event.Type {
			case types.EventText:
				assistantMsg.Content += event.Content
				out <- event
			case types.EventThinking:
				// Pass through thinking events to the UI for display
				out <- event
			case types.EventToolUse:
				tc := types.ToolCall{
					ID:        event.ToolCall.ID,
					Name:      event.ToolCall.Name,
					Arguments: event.ToolCall.Arguments,
				}
				toolCalls = append(toolCalls, tc)
				out <- event
				// NOTE: execution deferred to EventDone so the whole turn's
				// tool calls can run through executeToolBatch (read-only tools
				// in parallel — Claude Code parallel tool use parity).
			case types.EventDone:
				if len(toolCalls) > 0 {
					e.runToolTurn(ctx, sessionID, provider, opt, modelInfo, assistantMsg, toolCalls, out, 0)
				} else {
					e.finishTextTurn(ctx, sessionID, provider, opt, modelInfo, assistantMsg, event, startTime, out, 0)
				}
				// Stop lifecycle hook — the agent has finished responding.
				if hr := e.getHooksRunner(); hr.HasHooks(hooks.Stop) {
					hr.Fire(context.Background(), hooks.Stop, hooks.Input{SessionID: sessionID})
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
		SessionID:        sessionID,
		Messages:         messages,
		Model:            modelInfo.ID,
		ProviderName:     modelInfo.Provider,
		SystemPrompt:     opt.BuildPrefix(),
		Tools:            e.toolReg.ListDefs(),
		MaxTokens:        orMaxTokens(e.maxTokens, modelInfo.MaxOutputTokens),
		Temperature:      e.temperature,
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
		case types.EventThinking:
			// Pass through thinking events to the UI for display
			out <- event
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
				e.runToolTurn(ctx, sessionID, provider, opt, modelInfo, assistantMsg, toolCalls, out, depth)
			} else {
				e.finishTextTurn(ctx, sessionID, provider, opt, modelInfo, assistantMsg, event, startTime, out, depth)
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

// checkDiagnosticsAfterTool queries the LSP manager for diagnostics on
// the given file path. If compilation errors are found, returns a formatted
// string suitable for injection into the model's context for auto-fix.
func (e *Engine) checkDiagnosticsAfterTool(filePath string) string {
	if e.lspManager == nil || filePath == "" {
		return ""
	}
	lang := lsp.DetectLanguage(filePath)
	if lang == "" {
		return ""
	}
	// Lazily start the language server for this language on first use.
	// StartLanguageServer is idempotent (no-ops if already running) and
	// returns an error when the binary is absent, so this stays safe.
	// Cap with a timeout so an unresponsive server initialize cannot hang
	// the first tool call that triggers it.
	lspCtx, lspCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer lspCancel()
	if err := e.lspManager.StartLanguageServer(lspCtx, lang); err != nil {
		return ""
	}
	client := e.lspManager.GetClient(lang)
	if client == nil {
		return ""
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return ""
	}
	uri := "file://" + filepath.ToSlash(absPath)
	diags, err := client.Diagnostics(uri)
	if err != nil || len(diags) == 0 {
		return ""
	}
	// Filter to errors only (severity 1 = error)
	var errors []string
	for _, d := range diags {
		if d.Severity == 1 {
			line := d.Range.Start.Line + 1
			msg := strings.TrimRight(d.Message, "\n")
			errors = append(errors, fmt.Sprintf("  L%d: %s", line, msg))
		}
	}
	if len(errors) == 0 {
		return ""
	}
	return fmt.Sprintf("\n⚠️ LSP diagnostics for %s:\n%s\n",
		filePath, strings.Join(errors, "\n"))
}

// collectDiagnostics checks all tool calls for file modifications and
// queries LSP diagnostics for each modified file. Returns a combined
// diagnostics message for all files, or "" if no errors found. A file whose
// diagnostics are unchanged since the last injection is skipped (G1) so the
// model is only alerted to new or changed compile errors.
func (e *Engine) collectDiagnostics(toolCalls []types.ToolCall) string {
	checked := make(map[string]bool)
	var parts []string
	for _, tc := range toolCalls {
		// Extract file path from common editing tools
		filePath := extractFilePath(tc.Name, tc.Arguments)
		if filePath == "" || checked[filePath] {
			continue
		}
		checked[filePath] = true
		msg := e.checkDiagnosticsAfterTool(filePath)
		if msg == "" {
			continue
		}
		if e.rememberDiagnostics(filePath, msg) {
			parts = append(parts, msg)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "🔧 以下文件存在编译错误，请修复:\n" + strings.Join(parts, "")
}

// rememberDiagnostics records the diagnostics text for a file and reports
// whether it is new (should be injected). Identical re-runs are suppressed to
// avoid re-alerting the model every turn.
func (e *Engine) rememberDiagnostics(filePath, msg string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if msg == "" {
		delete(e.diagCache, filePath)
		return false
	}
	if e.diagCache[filePath] == msg {
		return false
	}
	e.diagCache[filePath] = msg
	return true
}

// extractFilePath extracts the file path from a tool call's arguments.
func extractFilePath(toolName, args string) string {
	// Try to parse JSON arguments
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return ""
	}
	switch toolName {
	case "write_file", "edit", "search_replace", "read_file":
		if fp, ok := parsed["file_path"].(string); ok {
			return fp
		}
		if fp, ok := parsed["filePath"].(string); ok {
			return fp
		}
	}
	if fp, ok := parsed["file_path"].(string); ok {
		return fp
	}
	return ""
}

func (e *Engine) Stop(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cancel, ok := e.stopFns[sessionID]; ok {
		cancel()
		delete(e.stopFns, sessionID)
	}
}

func (e *Engine) getOrCreateOptimizer(sessionID string, modelInfo types.ModelInfo, existingMessages []types.Message, presetSummary string) *tokenopt.Optimizer {
	e.mu.Lock()
	defer e.mu.Unlock()
	if opt, ok := e.optimizers[sessionID]; ok {
		return opt
	}
	opt := tokenopt.New(tokenopt.Config{
		ModelInfo:     modelInfo,
		SystemPrompt:  e.buildSystemPrompt(sessionID),
		ProviderName:  modelInfo.Provider,
		PresetSummary: presetSummary,
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

// ingestToolAttachments collects inline multimodal output (e.g. images produced
// by the image_gen tool) from this round's tool results and appends a single
// user message carrying them, so vision-capable models can reference the
// generated artifact on the next turn. Tool messages themselves stay
// text-only because most providers reject image content inside tool messages.
func (e *Engine) ingestToolAttachments(sessionID string, toolCalls []types.ToolCall, opt *tokenopt.Optimizer) {
	var imgs []types.Attachment
	for _, tc := range toolCalls {
		if tc.Result != nil {
			imgs = append(imgs, tc.Result.Attachments...)
		}
	}
	if len(imgs) == 0 {
		return
	}
	msg := types.Message{
		Role:        types.RoleUser,
		Content:     "（上一步工具生成的可视化结果，见附件）",
		Attachments: imgs,
		Timestamp:   time.Now(),
	}
	opt.AddMessage(msg)
	e.sessionSt.AppendMessage(sessionID, msg)
}

func (e *Engine) buildSystemPrompt(sessionID string) string {
	// User-configured system prompt takes precedence (from config.Defaults.SystemPrompt).
	if e.systemPrompt != "" {
		projectContext := projectcontext.LoadProjectContext()
		projectAnalysis := projectcontext.LoadProjectAnalysis()
		result := ""
		if projectAnalysis != "" {
			result = projectAnalysis + "\n\n"
		}
		if strings.TrimSpace(projectContext) != "" {
			result += projectContext + "\n\n---\n\n"
		}
		result += e.systemPrompt
		return result
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
	projectAnalysis := projectcontext.LoadProjectAnalysis()
	result := ""
	if projectAnalysis != "" {
		result = projectAnalysis + "\n\n"
	}
	if strings.TrimSpace(projectContext) != "" {
		result += projectContext + "\n\n---\n\n"
	}
	result += base

	// Inject a COMPACT skill index (name + one-line description) into the
	// immutable prefix. The full SKILL.md body is NOT embedded here — that
	// would bloat the prefix and invalidate the provider's KV cache the
	// moment a skill is added or its body changes. Instead the model loads a
	// skill on demand via the use_skill tool, keeping the prefix tiny and
	// cache-stable no matter how many skills are installed. This is the
	// cornerstone of iCode's token-saving mechanism.
	if e.skillReg != nil {
		if all := e.skillReg.List(); len(all) > 0 {
			ptrs := make([]*skills.Skill, 0, len(all))
			for i := range all {
				ptrs = append(ptrs, &all[i])
			}
			if skillBlock := skills.FormatIndex(ptrs); skillBlock != "" {
				result += skillBlock
			}
		}
	}

	// Remembered user preferences (prefmem): a short list of the user's
	// durable work-style facts, e.g. "用简体中文回答" or "优先用 Go 写后台
	// 服务". Injected last so they are near the model's focus; empty when
	// nothing is remembered so the cache prefix stays stable.
	if e.prefMem != nil {
		if prefs := e.prefMem.Render(); prefs != "" {
			result += prefs
		}
	}
	return result
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

type shellGateAdapter struct {
	gate *permission.Gate
}

func (a *shellGateAdapter) CheckShellCommand(cmd string) (bool, string) {
	action := permission.Action{Tool: "bash", Command: cmd, Arguments: fmt.Sprintf(`{"command":%q}`, cmd)}
	res := a.gate.Check("", action)
	if res.Decision == permission.DecisionDeny {
		return false, res.Reason
	}
	return true, ""
}
