package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/hooks"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/llm/tokenopt"
	"github.com/ponygates/icode/internal/types"
)

// ExecuteTool runs a tool directly without going through the permission
// gate. Used by CLI commands (cleanup, etc.) and model-free operations.
func (e *Engine) ExecuteTool(name string, args string) *types.ToolResult {
	// Bound execution so a hung tool (network read, stuck subprocess) cannot
	// block the caller indefinitely. 2 minutes matches the default tool budget.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tc := types.ToolCall{Name: name, Arguments: args}
	return e.runTool(ctx, tc, nil)
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
	// SubagentStart / SubagentStop lifecycle hooks (Claude Code parity).
	if hr := e.getHooksRunner(); hr != nil {
		sid := tool.SessionIDFromContext(ctx)
		fireSub := func(ev hooks.Event) {
			if hr.HasHooks(ev) {
				hr.Fire(ctx, ev, hooks.Input{SessionID: sid, ToolName: name, Prompt: truncateStr(prompt, 200)})
			}
		}
		fireSub(hooks.SubagentStart)
		defer fireSub(hooks.SubagentStop)
	}
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

// forkPrefixMaxMessages caps how much parent history a fork replays. The
// bytes must match the parent's actual messages verbatim for cache hits, so
// we take the tail verbatim instead of summarising.
const forkPrefixMaxMessages = 40

// forkPrefix extracts the replay prefix from a parent conversation: the tail
// (verbatim bytes for prompt-cache hits), with trailing orphan tool results
// dropped since they only make sense paired with their assistant tool_calls.
func forkPrefix(msgs []types.Message) []types.Message {
	if len(msgs) == 0 {
		return nil
	}
	start := len(msgs) - forkPrefixMaxMessages
	if start < 0 {
		start = 0
	}
	prefix := append([]types.Message(nil), msgs[start:]...)
	for len(prefix) > 0 && prefix[len(prefix)-1].Role == types.RoleTool {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}

// RunForkedSubAgent dispatches a sub-agent with the parent conversation's
// recent messages replayed as prefix context (fork mode). Same system prompt
// + same message bytes → provider prompt-cache hits → delegation costs only
// cache-read tokens. Falls back to a plain run when no session is loaded.
func (e *Engine) RunForkedSubAgent(ctx context.Context, name, prompt string) (string, int, error) {
	// The ctx carries this turn's sessionID (injected in executeTool via
	// tool.WithSessionID). Reading the shared curSessionID field here would
	// race: concurrent streams for different desktop tabs would make a fork
	// replay the WRONG session's message prefix. Fall back to the field only
	// for out-of-band calls that never went through a tool ctx.
	sessionID := tool.SessionIDFromContext(ctx)
	if sessionID == "" {
		sessionID = e.currentSessionID()
	}

	var prefix []types.Message
	if e.sessionSt != nil && sessionID != "" {
		if sess, err := e.sessionSt.Get(sessionID); err == nil && sess != nil {
			prefix = forkPrefix(sess.Messages)
		}
	}

	e.mu.Lock()
	runner := e.getAgentRunner()
	reg := e.agentRegistry
	if runner != nil {
		// Re-sync the runner's session ID right before use so a concurrent
		// Send() for another tab cannot stamp ours between Get and Run.
		runner.SetSessionID(sessionID)
	}
	e.mu.Unlock()

	var def *agent.AgentDef
	if d, ok := reg.Get(name); ok {
		def = d
	} else if _, isTeam := e.teamRegistry[name]; isTeam {
		return e.RunSubAgent(ctx, name, prompt)
	} else {
		return "", 0, fmt.Errorf("fork: unknown sub-agent %q", name)
	}
	return runner.RunWithPrefix(ctx, def, prompt, prefix)
}

// getAgentRunner lazily initialises the sub-agent runner and loads agent
// definitions from disk (with built-in defaults as fallback).
func (e *Engine) getAgentRunner() *agent.Runner {
	e.loadAgentsOnce.Do(func() {
		reg := agent.Load(agent.AgentDefaultDirs()...)
		reg.RegisterDefaults()
		e.agentRegistry = reg
		e.agentRunner = agent.NewRunnerWithGate(e.providerReg, e.toolReg, e.gate)
		// Sub-agents must honour the same per-model ⚙️ settings as the main
		// conversation; otherwise picking 精确 (or a custom top_p) would
		// silently apply everywhere except Task/fork calls.
		e.agentRunner.SetModelParamsResolver(agent.ParamsResolver(e.modelParams))
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
		// ToolError lifecycle hook — a tool execution failed (error text
		// rides in ToolOutput, tool name in ToolName).
		if !result.Success {
			if hr := e.getHooksRunner(); hr.HasHooks(hooks.ToolError) {
				hr.Fire(ctx, hooks.ToolError, hooks.Input{
					SessionID:  sessionID,
					ToolName:   toolCalls[i].Name,
					ToolOutput: result.Error,
				})
			}
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

	// Live-output forwarding: tools that support incremental streaming (bash)
	// emit EventToolProgress so the UI can render output in real time. The
	// UI layer throttles repaints; here we just relay chunks as-is. Progress
	// events are volatile — never persisted into the conversation.
	relay := func(chunk string) {
		select {
		case out <- types.StreamEvent{Type: types.EventToolProgress, Content: chunk}:
		case <-ctx.Done():
		default:
		}
	}
	ctx = tool.WithProgress(ctx, relay)

	// Interactive ask (Claude Code AskUserQuestion parity): the TUI injects
	// its asker via Engine.AskUser; headless leave it nil and the
	// AskUserTool degrades gracefully instead of hanging.
	if e.AskUser != nil {
		ctx = tool.WithAskUser(ctx, e.AskUser)
	}
	// Multi-question wizard asker (opencode AskQuestion parity).
	if e.AskUserForm != nil {
		ctx = tool.WithAskUserForm(ctx, e.AskUserForm)
	}

	// Doom loop detection: if the same tool+args appears 3+ consecutive
	// times, emit a warning and return a failure to break the loop.
	if dl := e.doomLoopFor(sessionID); dl.RecordCall(tc.Name, tc.Arguments) {
		status := dl.DoomLoopStatus()
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("检测到 Doom Loop — AI 连续重复调用同一工具。\n%s\n请重新描述你的需求以改变策略。", status),
		}
	}

	// PreToolUse lifecycle hooks (Claude Code parity): an external command
	// exiting with code 2 blocks the tool call; its stderr is fed back to
	// the model so it can adjust course. The stdout JSON contract adds
	// permissionDecision overrides (allow / deny / ask) so policy scripts
	// can steer the gate, plus systemMessage / timeout observability.
	var hookAskReason string
	if hr := e.getHooksRunner(); hr.HasHooks(hooks.PreToolUse) {
		hres := hr.Fire(ctx, hooks.PreToolUse, hooks.Input{
			ToolName:  tc.Name,
			ToolInput: json.RawMessage(tc.Arguments),
			SessionID: sessionID,
		})
		if hres.Block {
			return &types.ToolResult{
				Success: false,
				Error:   "PreToolUse hook blocked this call: " + hres.Message,
			}
		}
		if hres.SystemMessage != "" {
			select {
			case out <- types.StreamEvent{Type: types.EventSystem, Content: "⚠ [hook] " + hres.SystemMessage}:
			default:
			}
		}
		if hres.TimedOut {
			select {
			case out <- types.StreamEvent{Type: types.EventSystem, Content: "ⓘ [hook] PreToolUse 钩子超时，已跳过其检查。"}:
			default:
			}
		}
		// permissionDecision override: deny short-circuits exactly like a
		// gate denial; allow skips the gate entirely (headless automation:
		// the hook IS the policy); ask forces the confirmation prompt.
		switch hres.PermissionDecision {
		case "deny":
			return &types.ToolResult{
				Success: false,
				Error:   "PreToolUse hook denied this call: " + hres.Message,
			}
		case "allow":
			return e.runTool(ctx, tc, out)
		case "ask":
			hookAskReason = hres.Message
		}
	}

	action := buildAction(tc.Name, tc.Arguments)

	if e.gate == nil {
		return e.runTool(ctx, tc, out)
	}

	res := e.gate.Check(sessionID, action)
	// PreToolUse hook override: "ask" forces the confirmation prompt even
	// when the gate would have allowed (headless policy scripts can demand
	// human eyes on specific tool shapes).
	if hookAskReason != "" && res.Decision == permission.DecisionAllow {
		res.Decision = permission.DecisionAsk
		res.Prompt = "PreToolUse hook 要求人工确认此调用。"
		if hookAskReason != "" {
			res.Prompt += "\n" + hookAskReason
		}
	}
	switch res.Decision {
	case permission.DecisionAllow:
		return e.runTool(ctx, tc, out)
	case permission.DecisionDeny:
		// Track tool rejection for strategy-change forcing
		if dl := e.doomLoopFor(sessionID); dl.RecordRejection(tc.Name) {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("「%s」已经被拒绝多次。AI 应更换方案，不要再调用此工具。", tc.Name),
			}
		}
		return &types.ToolResult{Success: false, Error: e.humanizeDeny(ctx, sessionID, tc, res.Reason)}
	case permission.DecisionAsk:
		if res.Escalated {
			// Strike counter tripped: surface a one-time "已退回手动" notice.
			out <- types.StreamEvent{Type: types.EventSystem, Content: "⚠ " + res.Reason}
		}
		strikes, threshold := 0, 0
		if e.gate != nil {
			if _, s := e.gate.EscalationState(sessionID); s > 0 {
				strikes = s
			}
			threshold = e.gate.StrikeThreshold()
		}
		if e.permHandler != nil {
			req := &types.PermissionReq{Tool: tc.Name, Prompt: res.Prompt, Severity: res.Severity, Strikes: strikes, Threshold: threshold}
			return e.applyDecision(ctx, sessionID, tc, e.permHandler(sessionID, req, res), out)
		}
		reqID := e.genPermID()
		req := &types.PermissionReq{RequestID: reqID, Tool: tc.Name, Prompt: res.Prompt, Severity: res.Severity, Strikes: strikes, Threshold: threshold}
		// PermissionRequest lifecycle hook — external scripts can watch
		// every confirmation prompt (Claude Code parity).
		if hr := e.getHooksRunner(); hr.HasHooks(hooks.PermissionRequest) {
			hr.Fire(ctx, hooks.PermissionRequest, hooks.Input{SessionID: sessionID, ToolName: tc.Name, Prompt: res.Prompt})
		}
		out <- types.StreamEvent{Type: types.EventPermission, Permission: req}
		ch := make(chan permission.Decision, 1)
		e.permMu.Lock()
		e.permRespChans[reqID] = ch
		e.permMu.Unlock()
		select {
		case decision := <-ch:
			return e.applyDecision(ctx, sessionID, tc, decision, out)
		case <-ctx.Done():
			return &types.ToolResult{Success: false, Error: "Permission request cancelled"}
		}
	default:
		return e.runTool(ctx, tc, out)
	}
}

func (e *Engine) applyDecision(ctx context.Context, sessionID string, tc types.ToolCall, decision permission.Decision, out chan types.StreamEvent) *types.ToolResult {
	switch decision {
	case permission.DecisionAllow:
		e.rememberConnectDomain(tc)
		return e.runTool(ctx, tc, out)
	case permission.DecisionAllowAll:
		if e.gate != nil {
			e.gate.SetSessionAllow(sessionID, true)
		}
		e.rememberConnectDomain(tc)
		return e.runTool(ctx, tc, out)
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

func (e *Engine) runTool(ctx context.Context, tc types.ToolCall, out chan types.StreamEvent) *types.ToolResult {
	// Computer-use runaway guard: cap consecutive desktop-input operations so
	// a confused model cannot keep clicking the user's real screen forever.
	// sessionID comes from the ctx injected in executeTool (WithSessionID),
	// NOT from the shared curSessionID field — concurrent streams for different
	// desktop tabs would otherwise stamp on each other's per-session counters.
	sessionID := tool.SessionIDFromContext(ctx)
	if isCUTool(tc.Name) {
		if blocked, n := e.bumpCUGuard(sessionID, tc.Name); blocked {
			return &types.ToolResult{
				Success: false,
				Error: fmt.Sprintf("已连续执行 %d 次屏幕输入操作且无任何其他进展动作，CU 熔断触发。", n) +
					"请停止点击/输入：先用 screenshot 或 screen_read 观察当前界面状态，或改用文件工具与命令行完成任务；若确需继续桌面操作，请向用户说明原因并等待下一轮指令。",
			}
		}
	} else {
		e.resetCUStreak(sessionID)
	}

	// Circuit breaker check BEFORE executing (本书 ch.23 三态熔断): an open
	// breaker blocks the tool until its cooldown elapses, then admits exactly
	// one probe. This prevents the model from hammering a broken tool every
	// turn while still auto-healing after the failure storm passes.
	if allowed, retryIn := e.doomLoopFor(sessionID).CheckBreaker(tc.Name); !allowed {
		msg := fmt.Sprintf("工具「%s」正处于熔断状态，请更换方案（换工具/换参数），不要再调用它。", tc.Name)
		if retryIn > 0 {
			msg = fmt.Sprintf("工具「%s」已熔断，约 %s 后可重试一次。请先检查失败原因或更换方案。", tc.Name, retryIn.Round(time.Second))
		}
		return &types.ToolResult{Success: false, Error: msg}
	}

	e.snapshotBeforeTool(ctx, tc)
	// bash can modify any file — snapshot the whole project so /undo can
	// restore changes it makes (per-file snapshots can't cover that).
	if tc.Name == "bash" {
		checkpoint.BeforeBash(ctx)
	}
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
		if dl := e.doomLoopFor(sessionID); dl.RecordFailure(tc.Name) {
			return &types.ToolResult{
				Success: false,
				Error: fmt.Sprintf(
					"工具「%s」已连续失败，触发熔断。请立即更换方案（换个参数、换工具、或先检查原因），不要再调用此工具。最近错误：%s",
					tc.Name, firstN(res.Error, 160),
				),
			}
		}
	} else {
		e.doomLoopFor(sessionID).ResetToolFailures(tc.Name)
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
	// JSON contract extras (Claude Code parity): suppressOutput hides the
	// hook's feedback from the model; systemMessage warns the USER only.
	if hr := e.getHooksRunner(); hr.HasHooks(hooks.PostToolUse) {
		hres := hr.Fire(ctx, hooks.PostToolUse, hooks.Input{
			ToolName:   tc.Name,
			ToolInput:  json.RawMessage(tc.Arguments),
			ToolOutput: truncateForHook(res.Content),
			SessionID:  tool.SessionIDFromContext(ctx),
		})
		if hres.SystemMessage != "" && out != nil {
			select {
			case out <- types.StreamEvent{Type: types.EventSystem, Content: "⚠ [hook] " + hres.SystemMessage}:
			default:
			}
		}
		if hres.Message != "" && !hres.SuppressOutput {
			res.Content += "\n\n[PostToolUse hook feedback]\n" + hres.Message
		}
	}
	return res
}

// brokenToolCallIndex returns the index of the first tool call whose
// arguments JSON is malformed, or -1 when every call is well-formed.
func brokenToolCallIndex(toolCalls []types.ToolCall) int {
	for i, tc := range toolCalls {
		if !json.Valid([]byte(tc.Arguments)) {
			return i
		}
	}
	return -1
}

// maxToolRepairsPerTurn caps automatic tool-JSON repairs per user turn. One
// repair is usually enough; the cap exists so a model that repeatedly emits
// malformed arguments falls through to the tool's own "invalid args" error
// instead of spinning the conversation loop forever.
const maxToolRepairsPerTurn = 2

// resetToolRepairBudget clears the per-turn tool-JSON repair counter for a
// session (called at the start of every Send).
func (e *Engine) resetToolRepairBudget(sessionID string) {
	e.repairMu.Lock()
	delete(e.repairCounts, sessionID)
	e.repairMu.Unlock()
}

// takeToolRepair consumes one unit of the session's repair budget. Returns
// false when the budget is already spent.
func (e *Engine) takeToolRepair(sessionID string) bool {
	e.repairMu.Lock()
	defer e.repairMu.Unlock()
	if e.repairCounts[sessionID] >= maxToolRepairsPerTurn {
		return false
	}
	e.repairCounts[sessionID]++
	return true
}

// repairBrokenToolCalls implements tool-JSON auto-resend (Claude Code parity):
// when a tool call's arguments JSON is malformed, instead of executing a
// broken call (which would just fail with "invalid args" and force the model
// to restart from scratch), re-ask the model to re-emit the arguments, then
// dispatch the repaired calls.
//
// truncated tells the caller's assessment of whether the turn was cut off at
// max_tokens: truncated calls escalate the output budget (8K→16K→32K→64K,
// capped at MaxRetries attempts); non-truncated calls keep the current budget
// and rely on a clear "请重发完整参数" instruction. This means a model that
// simply emitted malformed JSON (a model bug) also gets one auto-repair
// chance instead of an immediate tool failure.
//
// Anti-loop: each user turn has a small repair budget (maxToolRepairsPerTurn,
// reset in Send) so a model that keeps emitting broken JSON cannot spin
// forever — once the budget is spent the original (broken) call executes and
// its "invalid args" error becomes the normal feedback loop.
//
// Returns true when it found a broken call AND already dispatched the repaired
// batch via runToolTurn (the caller must return immediately). Returns false
// when there is nothing to repair, the budget is spent, or repair failed —
// the caller then executes the original calls as-is.
func (e *Engine) repairBrokenToolCalls(
	ctx context.Context,
	sessionID string,
	provider types.Provider,
	opt *tokenopt.Optimizer,
	modelInfo types.ModelInfo,
	toolCalls []types.ToolCall,
	truncated bool,
	out chan types.StreamEvent,
	depth int,
) bool {
	if e.truncDet == nil || len(toolCalls) == 0 {
		return false
	}
	broken := brokenToolCallIndex(toolCalls)
	if broken < 0 {
		return false
	}
	if !e.takeToolRepair(sessionID) {
		return false // repair budget for this turn is spent — let the tool error surface
	}
	tc := toolCalls[broken]

	// Same resolution as the main turn so a repair retry does not silently
	// revert to the engine-wide defaults.
	temperature, topP, modelMaxOut := e.applyModelParams(modelInfo)
	maxTok := orMaxTokens(e.maxTokens, modelMaxOut)
	attempts := 1
	if truncated {
		attempts = e.truncDet.config.MaxRetries
	}
	for attempt := 0; attempt < attempts && ctx.Err() == nil; attempt++ {
		if truncated {
			nextMax := e.truncDet.config.NextTokens(maxTok)
			if nextMax <= maxTok {
				break // already at the ceiling, nothing more to escalate
			}
			maxTok = nextMax
		}

		if truncated {
			out <- types.StreamEvent{Type: types.EventText, Content: fmt.Sprintf(
				"\n⚠ 工具调用「%s」参数 JSON 被 max_tokens 截断，正在自动补齐重试（输出预算提升至 %d tokens）…\n",
				tc.Name, maxTok)}
		} else {
			out <- types.StreamEvent{Type: types.EventText, Content: fmt.Sprintf(
				"\n⚠ 工具调用「%s」参数 JSON 格式不合法，正在请求模型重新输出完整参数…\n", tc.Name)}
		}

		// A USER-turn hint (not an orphan assistant tool_use without a
		// tool_result) keeps every provider happy — OpenAI-compatible and
		// Anthropic native. The partial arguments are replayed as text so the
		// model can complete them faithfully.
		reason := "被 max_tokens 截断"
		if !truncated {
			reason = "格式不合法"
		}
		opt.AddMessage(types.Message{
			Role:      types.RoleUser,
			Content:   fmt.Sprintf("你上一条回复的工具调用「%s」参数 JSON %s。已生成的开头：\n\n%s\n\n请只重新输出该工具调用的完整参数 JSON（保持原意图，不要重复其他内容）。", tc.Name, reason, firstN(tc.Arguments, 2000)),
			Timestamp: time.Now(),
		})

		ch, err := provider.ChatStream(ctx, types.ChatRequest{
			SessionID:        sessionID,
			Messages:         opt.CompactRequest(""),
			Model:            modelInfo.WireModel(),
			ProviderName:     modelInfo.Provider,
			SystemPrompt:     opt.BuildPrefix(),
			Tools:            e.toolReg.ListDefs(),
			MaxTokens:        maxTok,
			Temperature:      temperature,
			TopP:             topP,
			CacheBreakpoints: opt.BuildCacheBreakpoints(),
			Thinking:         e.thinkingConfig(),
			CacheTTL:         e.cacheTTL,
		})
		if err != nil {
			out <- types.StreamEvent{Type: types.EventError, Content: friendlyModelError(err)}
			return false
		}

		var newCalls []types.ToolCall
		valid := false
		for ev := range ch {
			switch ev.Type {
			case types.EventToolUse:
				nc := types.ToolCall{ID: ev.ToolCall.ID, Name: ev.ToolCall.Name, Arguments: ev.ToolCall.Arguments}
				newCalls = append(newCalls, nc)
				if json.Valid([]byte(nc.Arguments)) {
					valid = true
				}
			case types.EventText:
				out <- ev
			case types.EventThinking:
				out <- ev
			case types.EventError:
				out <- ev
				return false // a dead stream is not going to repair anything
			}
		}
		if valid && len(newCalls) > 0 {
			out <- types.StreamEvent{Type: types.EventText, Content: "\n[已补齐工具参数，继续执行…]\n\n"}
			// The partial assistant turn was never added to the optimizer (the
			// repair hint replaced it), so history stays consistent: the model
			// sees the repair hint and now the repaired tool calls.
			e.runToolTurn(ctx, sessionID, provider, opt, modelInfo,
				types.Message{Role: types.RoleAssistant, Timestamp: time.Now()},
				newCalls, out, depth)
			return true
		}
	}
	return false
}
