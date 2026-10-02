package permission

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Check evaluates a tool action against the current permission mode, then
// stamps every ask decision with a risk severity (RiskSeverity) so all
// approval surfaces — TUI box colour, desktop dialog, ACP clients — can
// render risk level instead of one uniform yellow. The stamping lives here
// rather than in each ask branch so no future decision path can forget it.
func (g *Gate) Check(sessionID string, action Action) CheckResult {
	res := g.checkCore(sessionID, action)
	if res.Decision == DecisionAsk && res.Severity == "" {
		res.Severity = RiskSeverity(action)
	}
	return res
}

// RiskSeverity rates how dangerous an action is when it reaches a human
// approval prompt. Pure function of the action (CheckBashCommand is pure),
// safe to call outside the gate lock.
func RiskSeverity(action Action) string {
	if action.Tool == "bash" {
		for _, v := range CheckBashCommand(action.Command) {
			if v.Severity == SeverityAsk {
				return SeverityHigh
			}
		}
		return SeverityMedium
	}
	switch action.Tool {
	case "write_file", "edit":
		return SeverityMedium
	}
	return SeverityLow
}

// checkCore is the original Check logic (strike-counter escalation included).
func (g *Gate) checkCore(sessionID string, action Action) CheckResult {
	// A session already forced into manual mode: require confirmation for
	// everything, regardless of mode. Escalated stays false here so the UI
	// only shows the "已退回手动" notice once (on the triggering decision).
	g.mu.RLock()
	esc := g.escalated[sessionID]
	g.mu.RUnlock()
	if esc {
		return CheckResult{
			Decision: DecisionAsk,
			Reason:   "手动模式：连续拦截后已退回人工确认，本会话所有操作需显式批准",
			Prompt:   g.buildPrompt(action),
		}
	}

	// Parameter-level rules: first match wins and overrides every other
	// decision path (mode defaults, tool rules, session allow-all, even the
	// Claude settings allow list) so destructive patterns can be hard-blocked.
	g.mu.RLock()
	rules := append([]ParamRule(nil), g.paramRules...)
	g.mu.RUnlock()
	if result := g.evalParamRules(rules, action); result != nil {
		g.recordStrike(sessionID, result)
		return *result
	}

	// MCP trust policy (P1 hardening): a server configured with trust_mode
	// "ask" must ALWAYS prompt — the auto classifier must never silently
	// approve its tools; "readonly" auto-approves only read-verb tool names
	// and asks for anything else. Servers with "all" (the default) are
	// unrestricted, preserving pre-policy behavior.
	if mode := g.mcpTrustMode(action.Tool); mode != "" && mode != "all" {
		if mode == "ask" || !mcpReadVerbTool(action.Tool) {
			res := CheckResult{
				Decision: DecisionAsk,
				Reason:   "MCP 服务器信任级别为 " + mode + "：" + action.Tool + " 需人工确认",
				Prompt:   g.buildPrompt(action),
			}
			g.recordStrike(sessionID, &res)
			return res
		}
		// readonly + read-verb tool → fall through to the normal flow.
	}

	result := g.check(sessionID, action)
	// Auto-mode classifier (Claude Code parity): when the rule-based flow
	// would ask the user, consult the cheap classifier model; if it judges
	// the call safe, auto-approve instead — far less interruption. Runs
	// outside the gate lock (classifier does a network call).
	if result.Decision == DecisionAsk && g.mode == ModeAuto && g.classifier != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		allow, reason, cerr := g.classifier.Classify(ctx, action.Tool, action.Arguments)
		cancel()
		if cerr == nil && allow {
			reason = strings.TrimSpace(reason)
			if reason == "" {
				reason = "分类器判定安全"
			}
			result = CheckResult{Decision: DecisionAllow, Reason: "Auto classifier: " + reason, Prompt: result.Prompt}
		} else if cerr == nil && !allow {
			// Classifier says risky — surface its reason, still ask.
			if r := strings.TrimSpace(reason); r != "" {
				result.Reason = "Auto classifier 提示风险: " + r
			}
		}
		// On classifier error we stay with the original ask (fail safe).
	}
	g.recordStrike(sessionID, &result)
	return result
}

// evalParamRules matches action against the ordered rule list (first match
// wins). Split from checkParamRules so Check can snapshot the rules under a
// short read lock without holding it across prompt building.
func (g *Gate) evalParamRules(rules []ParamRule, action Action) *CheckResult {
	for _, r := range rules {
		if !claudeAllowMatch(action, r.Pattern) {
			continue
		}
		return &CheckResult{
			Decision: r.Decision,
			Reason:   fmt.Sprintf("参数级规则命中 %s", r.Pattern),
			Prompt:   g.buildPrompt(action),
		}
	}
	return nil
}

// check is the core permission evaluator (the original Check logic).
func (g *Gate) check(sessionID string, action Action) CheckResult {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Build prompt for display
	prompt := g.buildPrompt(action)

	switch g.mode {
	case ModeAuto:
		// Four-tier permission (本书 ch.22): Read is auto-approved; Write,
		// Execute, and Connect all require confirmation — Connect tools
		// (fetch/web_search) touch external networks, so even though they
		// don't mutate local state they are NOT silently auto-approved.
		level := AccessLevelOf(action.Tool)
		if level == AccessRead && g.isReadOnly(action) {
			return CheckResult{Decision: DecisionAllow, Reason: "Auto mode: Read-tier operation", Prompt: prompt}
		}
		if g.isDenied(action) {
			return CheckResult{Decision: DecisionDeny, Reason: "Auto mode deny list: " + g.explainDeny(action), Prompt: prompt}
		}
		// Check runtime per-tool rules (persistent across sessions)
		if g.ToolRules[action.Tool] == "deny" {
			return CheckResult{Decision: DecisionDeny, Reason: "Tool denied by user rule", Prompt: prompt}
		}
		if g.ToolRules[action.Tool] == "allow" {
			return CheckResult{Decision: DecisionAllow, Reason: "Tool allowed by user rule", Prompt: prompt}
		}
		// Check session-level per-tool allow
		if g.sessionToolAllows[sessionID] != nil && g.sessionToolAllows[sessionID][action.Tool] {
			return CheckResult{Decision: DecisionAllow, Reason: "Tool allowed for this session", Prompt: prompt}
		}
		// Connect-tier silent whitelist (本书 ch.22 静默白名单): a destination
		// the user approved once is auto-allowed on subsequent visits without a
		// re-prompt. Requires the URL field (web_search-by-query never matches).
		if level == AccessConnect && g.connectDomainTrusted(action) {
			return CheckResult{Decision: DecisionAllow, Reason: "Connect tier: domain already approved by user", Prompt: prompt}
		}
		return CheckResult{
			Decision: DecisionAsk,
			Prompt:   prompt,
			Reason:   fmt.Sprintf("Auto mode: %s-tier operation needs confirmation", level),
		}

	case ModePlan:
		if g.isReadOnly(action) {
			return CheckResult{Decision: DecisionAllow, Reason: "Plan mode: read-only operation", Prompt: prompt}
		}
		return CheckResult{
			Decision: DecisionDeny,
			Reason:   "Plan mode: mutating operations are not allowed. Switch to Agent or YOLO mode.",
			Prompt:   prompt,
		}

	case ModeYOLO:
		// Check if command is in denied list
		if g.isDenied(action) {
			return CheckResult{
				Decision: DecisionDeny,
				Reason:   fmt.Sprintf("YOLO deny list (%q): %s", action.Command, g.explainDeny(action)),
				Prompt:   prompt,
			}
		}
		return CheckResult{Decision: DecisionAllow, Reason: "YOLO mode: auto-approved", Prompt: prompt}

	case ModeAgent:
		// Check session-level allow-all
		if g.sessionAllows[sessionID] {
			return CheckResult{Decision: DecisionAllow, Reason: "Session allow-all active", Prompt: prompt}
		}

		// Check Claude Code settings.json rules (deny wins over allow)
		if decision := g.checkClaudeSettings(action); decision != nil {
			return *decision
		}

		// Check hooks
		if decision := g.checkHooks(action); decision != nil {
			return *decision
		}

		// Check persistent per-tool rules
		if g.ToolRules[action.Tool] == "deny" {
			return CheckResult{Decision: DecisionDeny, Reason: "Tool denied by user rule", Prompt: prompt}
		}
		if g.ToolRules[action.Tool] == "allow" {
			return CheckResult{Decision: DecisionAllow, Reason: "Tool allowed by user rule", Prompt: prompt}
		}
		// Check session-level per-tool allow
		if g.sessionToolAllows[sessionID] != nil && g.sessionToolAllows[sessionID][action.Tool] {
			return CheckResult{Decision: DecisionAllow, Reason: "Tool allowed for this session", Prompt: prompt}
		}

		// Check if denied
		if g.isDenied(action) {
			return CheckResult{
				Decision: DecisionDeny,
				Reason:   "Agent mode deny list: " + g.explainDeny(action),
				Prompt:   prompt,
			}
		}

		// Otherwise, ask the user
		return CheckResult{Decision: DecisionAsk, Prompt: prompt}

	default:
		return CheckResult{Decision: DecisionAsk, Prompt: prompt}
	}
}

// ============================================================================
// Helpers
// ============================================================================

func (g *Gate) isReadOnly(action Action) bool {
	readOnlyTools := map[string]bool{
		"read_file":  true,
		"read_image": true,
		"ls":         true,
		"grep":       true,
		"glob":       true,
		"git_diff":   true,
		"git_status": true,
		"fetch":      true,
		"disk_usage": true,
		// code_search only reads the in-memory symbol index.
		"code_search": true,
		// task_output only inspects the user's own background tasks
		// (kill included — the task was started via an approved bash call).
		"task_output": true,
		// todo_write only mutates in-process session state, never the
		// filesystem or external systems — safe to auto-approve.
		"todo_write": true,
	}
	return readOnlyTools[action.Tool]
}

func (g *Gate) isDenied(action Action) bool {
	// Sandbox containment applies to all file tools, not just bash.
	if g.outsideAllowedPaths(action) {
		return true
	}

	if action.Tool != "bash" {
		return false
	}

	cmd := strings.ToLower(strings.TrimSpace(action.Command))

	// Check classic deny list (backward compatible)
	for _, denied := range g.DeniedCommands {
		if strings.Contains(cmd, strings.ToLower(denied)) {
			return true
		}
	}

	// Check 23-rule Bash security engine
	if violation := IsDeniedBashCommand(action.Command); violation != nil {
		return true
	}

	// Check if command has any SeverityBlock violations
	violations := CheckBashCommand(action.Command)
	for _, v := range violations {
		if v.Severity == SeverityBlock {
			return true
		}
	}

	return false
}

// explainDeny pinpoints WHY an action was denied, so the humanised refusal
// can name the actual cause instead of a generic "in deny list" message.
// Called only when isDenied already returned true.
func (g *Gate) explainDeny(action Action) string {
	if g.outsideAllowedPaths(action) {
		return fmt.Sprintf("目标路径 %q 在工作区沙箱之外（AllowedPaths 白名单未包含）", action.Path)
	}
	if action.Tool == "bash" {
		cmd := strings.ToLower(strings.TrimSpace(action.Command))
		for _, d := range g.DeniedCommands {
			if strings.Contains(cmd, strings.ToLower(d)) {
				return fmt.Sprintf("命令命中危险模式 %q", d)
			}
		}
		if v := IsDeniedBashCommand(action.Command); v != nil {
			return "命令命中 Bash 安全引擎的硬性规则"
		}
		violations := CheckBashCommand(action.Command)
		for _, v := range violations {
			if v.Severity == SeverityBlock {
				return "命令命中 Bash 安全引擎的硬性规则"
			}
		}
	}
	return "命中危险操作清单"
}

// HumanizeDeny translates a technical denial reason into plain language the
// user can act on — Claude Code parity: a refusal must explain itself in
// natural language ("如果你要拒绝用户的操作，至少要告诉他们原因"), never a raw
// error code. Deterministic and free; the engine may additionally polish the
// output with one temperature-0 LLM call.
func HumanizeDeny(action Action, reason string) string {
	switch {
	case strings.HasPrefix(reason, "参数级规则命中"):
		pattern := strings.TrimPrefix(reason, "参数级规则命中 ")
		return fmt.Sprintf("⛔ 已拦截：%s 调用被你配置的硬性规则（%s）拦下。\n💡 如需放行：调整 config.toml 的 [permission.rules]，或让用户手动执行这一步。", action.Tool, pattern)

	case strings.Contains(reason, ".claude/settings.json"):
		return fmt.Sprintf("⛔ 已拦截：项目权限规则（.claude/settings.json 的 deny 列表）明确禁止 %s 操作。\n💡 如需放行：请用户编辑该文件的 permissions.deny，或由用户手动执行。", action.Tool)

	case strings.Contains(reason, "hooks rule"):
		return fmt.Sprintf("⛔ 已拦截：%s 调用命中 hooks.yaml 规则。\n💡 如需放行：请用户编辑 hooks.yaml 中对应工具的 deny/allow 列表。", action.Tool)

	case strings.Contains(reason, "deny list") || strings.Contains(reason, "危险"):
		return fmt.Sprintf("⛔ 已拦截：%s 命中危险操作清单。\n💡 建议：把任务拆解成更安全的步骤（例如用文件工具代替删除命令），或请用户手动执行高风险部分。", action.Tool)

	case strings.Contains(reason, "Tool denied by user rule"):
		return fmt.Sprintf("⛔ 已拦截：%s 之前被你设为「总是拒绝」（/permissions 可改）。\n💡 如需放行：运行 /permissions 调整该工具的持久规则。", action.Tool)

	case strings.Contains(reason, "手动模式"):
		return "⛔ 已拦截：" + reason + "\n💡 本会话所有操作都需要你逐条确认，这是连续多次拦截后的保护行为。"

	default:
		return "⛔ 已拦截：" + reason
	}
}
