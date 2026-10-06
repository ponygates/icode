package conversation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	projectcontext "github.com/ponygates/icode/internal/core/context"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/skills"
	"github.com/ponygates/icode/internal/types"
)

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

# WORKFLOW — follow this every time
1. EXPLORE first: read the relevant files (read_file / grep / glob / ls) before changing anything. Never guess the code you cannot see.
2. PLAN: for multi-step tasks, call todo_write to lay out the steps, then work through them one by one, updating status as you go.
3. EXECUTE: make the smallest change that works. Prefer edit / write_file over rewriting whole files.
4. VERIFY: after changing code, run the project's test or lint command (go test / npm test / pytest / cargo test / tsc / ruff ...) to confirm it works.
5. FIX ITERATIVELY: if a command fails, READ THE FULL ERROR, fix the root cause, and re-run — up to 3 attempts. Do not repeat the same broken command; change your approach when it fails twice.
6. REPORT: summarize what you changed and the verification result, concisely.

# WORKED EXAMPLE (few-shot)
User: "修复 src/main.go 里的并发 bug"
Good flow: read_file src/main.go → 定位竞态 → edit 最小修复 → bash "go test ./..." → 汇报「改了哪几行 + 测试通过」
Bad flow: 直接 write_file 重写整个文件；不验证就声称修复完成；只描述方案不动手

# TOOL RULES
- ALWAYS use tools — never just describe what you would do.
- bash: run any shell command (add "cwd" param for a specific directory).
- read_file / write_file / edit / grep / glob / ls: file operations.
- task: delegate independent sub-problems to sub-agents for parallel work.
- If one approach fails, try a different one before asking the user.

# CODE QUALITY
- Match the project's existing style and conventions; keep changes focused, avoid unrelated refactors.
- When fixing a bug, preserve existing behavior elsewhere.

# SAFETY
- Never run destructive commands (rm -rf, git push --force, DELETE ...) without user approval.
- Report what you actually did and real results — never what you "would" do.

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

// calculateCostWithPricing is calculateCost with per-model contracted-price
// overrides (Claude Code modelPricing parity). Any zero override field falls
// back to the model's built-in plan price.
func calculateCostWithPricing(usage types.TokenUsage, model types.ModelInfo, overrides map[string]config.PricingOverride) float64 {
	if len(model.Plans) == 0 {
		return 0
	}
	plan := model.Plans[0]
	inPrice, outPrice, cachePrice := plan.InputPrice, plan.OutputPrice, plan.CachePrice
	if o, ok := overrides[model.ID]; ok {
		if o.InputPrice > 0 {
			inPrice = o.InputPrice
		}
		if o.OutputPrice > 0 {
			outPrice = o.OutputPrice
		}
		if o.CachePrice > 0 {
			cachePrice = o.CachePrice
		}
	}
	inputCost := float64(usage.PromptTokens-usage.CacheHitTokens) * inPrice / 1_000_000
	outputCost := float64(usage.CompletionTokens) * outPrice / 1_000_000
	cacheCost := float64(usage.CacheHitTokens) * cachePrice / 1_000_000
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

// humanizeDeny produces a user-understandable refusal: a deterministic plain
// language mapping first (Claude Code parity — "拒绝至少要告诉用户原因"),
// optionally polished by ONE temperature-0 LLM call so the explanation reads
// naturally. The LLM pass is skipped in local security mode (no hidden
// network calls, iCode ethos) and on any error falls back to the mapping.
func (e *Engine) humanizeDeny(ctx context.Context, sessionID string, tc types.ToolCall, reason string) string {
	a := buildAction(tc.Name, tc.Arguments)
	out := permission.HumanizeDeny(a, reason)
	if e.gate != nil && e.gate.SecurityLevel() == config.SecLocal {
		return out // 隐私边界内不做任何额外网络调用
	}
	if !e.humanizeLLMPolish {
		return out // 默认纯本地模板：拒绝理由不再外发 LLM（隐私 + 确定性）
	}
	if polished := e.polishDenyWithLLM(ctx, sessionID, tc.Name, reason); polished != "" {
		return polished
	}
	return out
}

// polishDenyWithLLM rewrites a denial reason with the session model at
// temperature 0 (deterministic per 源码解析 ch.5), tiny budget, hard timeout.
// Best-effort: empty string on any failure.
func (e *Engine) polishDenyWithLLM(ctx context.Context, sessionID, toolName, reason string) string {
	if e.sessionSt == nil || sessionID == "" {
		return ""
	}
	sess, err := e.sessionSt.Get(sessionID)
	if err != nil || sess == nil || sess.ModelID == "" {
		return ""
	}
	provider, mi, err := e.providerReg.ResolveModel(sess.ModelID)
	if err != nil || provider == nil {
		return ""
	}

	pctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	prompt := "你是权限系统解释器。把下面的工具拒绝原因改写成一句普通用户能看懂的中文（不超过60字），再给一句「怎么办」建议。只输出这两行，不要任何前后缀。\n工具：" + toolName + "\n原因：" + reason

	ch, err := provider.ChatStream(pctx, types.ChatRequest{
		SessionID:    sessionID,
		Messages:     []types.Message{{Role: types.RoleUser, Content: prompt, Timestamp: time.Now()}},
		Model:        mi.WireModel(),
		ProviderName: mi.Provider,
		SystemPrompt: "你只输出两行中文：第一行原因，第二行以「建议：」开头。",
		MaxTokens:    150,
		// nil = 沿用 provider 默认。此前写 0 也会被 `> 0` 判断省略掉，
		// 所以行为完全不变；真要固定 0 得用 types.Temp(0)。
		Temperature: nil,
	})
	if err != nil {
		return ""
	}
	var sb strings.Builder
	for ev := range ch {
		switch ev.Type {
		case types.EventText:
			sb.WriteString(ev.Content)
		case types.EventDone, types.EventError:
			return strings.TrimSpace(sb.String())
		}
	}
	return strings.TrimSpace(sb.String())
}

// uiShapedGoal heuristically decides whether a goal involves a visual UI, so
// the acceptance loop should include a screen_read verification step.
func uiShapedGoal(text string) bool {
	t := strings.ToLower(text)
	keywords := []string{
		"ui", "页面", "界面", "前端", "浏览器", "网页", "web", "localhost",
		"渲染", "样式", "布局", "弹窗", "按钮", "表单", "dashboard", "预览",
		"preview", "chrome", "app", "可视化",
	}
	for _, k := range keywords {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
}

// ── Computer-use runaway guard ────────────────────────────────────
// CU tools drive the real desktop; a model stuck in a click-loop can do real
// damage (or just burn turns). Track consecutive CU operations per session:
// exceeding the cap blocks further CU input until a non-CU action signals
// progress (file edit, command, observation) or a fresh user turn arrives.

const maxConsecutiveCUOps = 12

var cuTools = map[string]bool{
	"mouse_move": true, "mouse_click": true, "mouse_scroll": true,
	"type_text": true, "key_press": true,
}

func isCUTool(name string) bool { return cuTools[name] }

// bumpCUGuard records one CU operation and reports whether the per-session
// cap is exceeded. Non-CU calls reset the streak. Safe on a zero Engine.
func (e *Engine) bumpCUGuard(sessionID, toolName string) (blocked bool, count int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cuStreaks == nil {
		e.cuStreaks = map[string]int{}
	}
	if !isCUTool(toolName) {
		delete(e.cuStreaks, sessionID)
		return false, 0
	}
	e.cuStreaks[sessionID]++
	return e.cuStreaks[sessionID] > maxConsecutiveCUOps, e.cuStreaks[sessionID]
}

// resetCUStreak clears the session's CU streak (new user turn).
func (e *Engine) resetCUStreak(sessionID string) {
	e.mu.Lock()
	delete(e.cuStreaks, sessionID)
	e.mu.Unlock()
}

// currentSessionID returns the tracked active session ("" when unknown).
func (e *Engine) currentSessionID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.curSessionID
}
