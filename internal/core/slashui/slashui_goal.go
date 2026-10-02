package slashui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/types"
)

// cmdGoal manages the session's long-goal mode: /goal set <text> injects the
// goal into every turn's system prompt; /goal show / clear inspect or remove
// it. The goal itself costs no tokens beyond its own text — it replaces the
// need to re-state intent in every message.
func cmdGoal(b *Backend, st *State, args []string) Result {
	if b == nil || b.SessStore == nil || st.SessionID == "" {
		return ok("没有活跃会话（先发一条消息）。")
	}
	load := func() (*types.Session, Result) {
		sess, err := b.SessStore.Get(st.SessionID)
		if err != nil {
			return nil, errf("读取会话失败: %v", err)
		}
		return sess, Result{}
	}
	if len(args) == 0 {
		sess, r := load()
		if sess == nil {
			return r
		}
		if g := sessionum.GetGoal(sess); g != "" {
			return ok("当前目标（长目标模式生效中）：\n" + g)
		}
		return ok("当前没有目标。\n用法: /goal set <目标> — 开启长目标模式（每轮自动携带）\n      /goal show — 查看\n      /goal clear — 退出")
	}
	switch strings.ToLower(args[0]) {
	case "set":
		if len(args) < 2 {
			return ok("用法: /goal set <目标文本> [--verify <验收命令>]")
		}
		// Parse optional --verify <acceptance command> and --budget <tokens>
		// (ZCode Goal parity + Reasonix goal_token_budget parity).
		goal := ""
		verify := ""
		budget := 0
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--verify" || rest[i] == "-v" {
				if i+1 < len(rest) {
					verify = strings.TrimSpace(strings.Join(rest[i+1:], " "))
				}
				break
			}
			if rest[i] == "--budget" || rest[i] == "-b" {
				if i+1 < len(rest) {
					if n, err := strconv.Atoi(rest[i+1]); err == nil {
						budget = n
					}
					i++
				}
				continue
			}
			if goal != "" {
				goal += " "
			}
			goal += rest[i]
		}
		goal = strings.TrimSpace(goal)
		if goal == "" {
			return ok("目标不能为空。用法: /goal set <目标文本> [--verify <验收命令>] [--budget <token 上限>]")
		}
		sess, r := load()
		if sess == nil {
			return r
		}
		if err := sessionum.SetGoal(b.SessStore, sess, goal); err != nil {
			return errf("保存目标失败: %v", err)
		}
		if budget > 0 {
			if err := sessionum.SetGoalTokenBudget(b.SessStore, sess, budget); err != nil {
				return errf("保存预算失败: %v", err)
			}
		}
		if verify != "" {
			if err := sessionum.SetGoalVerify(b.SessStore, sess, verify); err != nil {
				return errf("保存验收命令失败: %v", err)
			}
			msg := "已设置可验收目标（每轮自动迭代直到验收命令通过）：\n目标：" + goal + "\n验收命令：" + verify
			if budget > 0 {
				msg += fmt.Sprintf("\nToken 预算：%d（用尽后自动收尾）", budget)
			}
			return ok(msg)
		}
		msg := "已设置长目标（后续每轮对话都会自动携带）：\n" + goal
		if budget > 0 {
			msg += fmt.Sprintf("\nToken 预算：%d（用尽后自动收尾）", budget)
		}
		return ok(msg)
	case "show":
		sess, r := load()
		if sess == nil {
			return r
		}
		if g := sessionum.GetGoal(sess); g != "" {
			msg := "当前目标（长目标模式生效中）：\n" + g
			if v := sessionum.GetGoalVerify(sess); v != "" {
				msg += "\n验收命令：" + v
			}
			return ok(msg)
		}
		return ok("当前没有目标。")
	case "clear", "unset":
		sess, r := load()
		if sess == nil {
			return r
		}
		if err := sessionum.SetGoal(b.SessStore, sess, ""); err != nil {
			return errf("清除目标失败: %v", err)
		}
		return ok("已清除目标，退出长目标模式。")
	default:
		return errf("未知子命令: %s（支持 set / show / clear）", args[0])
	}
}

// cmdBudget manages the session's hard token budget: /budget set <n> makes the
// engine shrink the context to fit (archived summary + recent messages) on any
// turn whose estimated size would exceed it. Pure local math — no model call.
func pctOf(used, total int) int {
	if total <= 0 {
		return 0
	}
	return used * 100 / total
}

func cmdBudget(b *Backend, st *State, args []string) Result {
	if b == nil || b.SessStore == nil || st.SessionID == "" {
		return ok("没有活跃会话（先发一条消息）。")
	}
	load := func() (*types.Session, Result) {
		sess, err := b.SessStore.Get(st.SessionID)
		if err != nil {
			return nil, errf("读取会话失败: %v", err)
		}
		return sess, Result{}
	}
	if len(args) == 0 {
		sess, r := load()
		if sess == nil {
			return r
		}
		if bg := sessionum.BudgetMax(sess); bg > 0 {
			used := sessionum.EstimatedUsage(sess)
			return ok(fmt.Sprintf("当前 Token 预算: %d\n当前估算用量: %d（%d%%）\n每次请求估算超限会自动压缩为摘要 + 最近消息。\n/budget clear 关闭。", bg, used, pctOf(used, bg)))
		}
		return ok("当前没有 Token 预算。\n用法: /budget set <上限token数> — 超限自动压缩\n      /budget show — 查看\n      /budget clear — 关闭")
	}
	switch strings.ToLower(args[0]) {
	case "set":
		if len(args) < 2 {
			return ok("用法: /budget set <上限token数>（如 /budget set 16000）")
		}
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 1000 {
			return errf("预算应为 ≥1000 的 token 数。")
		}
		sess, r := load()
		if sess == nil {
			return r
		}
		if err := sessionum.SetBudget(b.SessStore, sess, n); err != nil {
			return errf("保存预算失败: %v", err)
		}
		return ok(fmt.Sprintf("已设置 Token 预算: %d\n每次请求估算超限会自动压缩为摘要 + 最近消息，不会超预算。", n))
	case "show":
		sess, r := load()
		if sess == nil {
			return r
		}
		if bg := sessionum.BudgetMax(sess); bg > 0 {
			used := sessionum.EstimatedUsage(sess)
			line := fmt.Sprintf("当前 Token 预算: %d\n当前估算用量: %d（%d%%）\n预警阈值: %d%%（/budget warn <50-95> 调整）", bg, used, pctOf(used, bg), sessionum.BudgetWarnPct(sess))
			if cnt, last, wc := sessionum.TrimStats(sess); cnt > 0 || wc > 0 {
				line += fmt.Sprintf("\n护栏记录: 自动压缩 %d 次", cnt)
				if last != "" {
					line += fmt.Sprintf("（最近 %s）", last)
				}
				if wc > 0 {
					line += fmt.Sprintf("，提前预警 %d 次", wc)
				}
			}
			return ok(line)
		}
		return ok("当前没有 Token 预算。")
	case "warn":
		if len(args) < 2 {
			if sess, r := load(); sess != nil {
				return ok(fmt.Sprintf("当前预警阈值: %d%%\n用法: /budget warn <50-95> — 调整提前预警线", sessionum.BudgetWarnPct(sess)))
			} else if r.Output != "" {
				return r
			}
			return ok("当前预警阈值: 80%\n用法: /budget warn <50-95> — 调整提前预警线")
		}
		sess, r := load()
		if sess == nil {
			return r
		}
		n, err := strconv.Atoi(args[1])
		if err != nil {
			return errf("阈值应为 50-95 的百分比数。")
		}
		if err := sessionum.SetWarnPct(b.SessStore, sess, n); err != nil {
			return errf("%v", err)
		}
		return ok(fmt.Sprintf("已设置预算预警阈值: %d%%", n))
	case "clear":
		sess, r := load()
		if sess == nil {
			return r
		}
		if err := sessionum.SetBudget(b.SessStore, sess, 0); err != nil {
			return errf("关闭预算失败: %v", err)
		}
		return ok("已关闭 Token 预算，恢复完整上下文。")
	default:
		return errf("未知子命令: %s（支持 set / show / warn / clear）", args[0])
	}
}

// cmdIdle creates an off-peak task (/idle), 对齐智谱 ZCode 的"闲时任务"：
// 把非紧急重活排到低峰窗口（默认 00:00–06:00）自动执行。
func cmdIdle(b *Backend, args []string) Result {
	if b == nil || b.Scheduler == nil {
		return errf("调度器不可用（需持久化后端）。")
	}
	if len(args) < 2 {
		return errf("用法: /idle <名称> <任务描述>（如 /idle 全仓库重构 扫描 internal/ 下所有 Go 文件并修复明显问题）")
	}
	name := args[0]
	prompt := strings.Join(args[1:], " ")
	t, err := b.Scheduler.Create(name, prompt, "idle")
	if err != nil {
		return errf("创建闲时任务失败: %v", err)
	}
	return ok(fmt.Sprintf("✓ 已创建闲时任务「%s」（ID: %s）\n将在闲时窗口（低峰时段）自动执行，完成后通知你。", t.Name, t.ID))
}

// cmdLoop creates a recurring loop task (/loop), 对标 Claude Code 的 /loop：
// 每 <间隔>（支持秒级，如 30s / 5m / 2h）自动执行一次任务，直到手动停掉。
func cmdLoop(b *Backend, args []string) Result {
	if b == nil || b.Scheduler == nil {
		return errf("调度器不可用（需持久化后端）。")
	}
	if len(args) < 3 {
		return errf("用法: /loop <间隔> <名称> <任务描述>（如 /loop 30s 舆情监控 每半分钟抓一次页面并总结变化；间隔支持 30s/5m/2h）")
	}
	interval := args[0]
	name := args[1]
	prompt := strings.Join(args[2:], " ")
	var sched string
	// Normalize an interval like "30s" into a scheduler schedule: seconds →
	// RRULE (second-level precision, WorkBuddy parity), minutes/hours/days →
	// the native every: form.
	iv := strings.ToLower(strings.TrimSpace(interval))
	if n, unit, ok := splitDuration(iv); ok {
		switch unit {
		case "s":
			sched = fmt.Sprintf("rrule:FREQ=SECONDLY;INTERVAL=%d", n)
		case "m":
			sched = fmt.Sprintf("every:%dm", n)
		case "h":
			sched = fmt.Sprintf("every:%dh", n)
		case "d":
			sched = fmt.Sprintf("every:%dd", n)
		}
	}
	if sched == "" {
		return errf("无法解析间隔 %q（用 30s/5m/2h/1d）", interval)
	}
	t, err := b.Scheduler.Create(name, prompt, sched)
	if err != nil {
		return errf("创建循环任务失败: %v", err)
	}
	return ok(fmt.Sprintf("✓ 已创建循环任务「%s」（ID: %s，每 %s）\n将按间隔自动执行，可在任务面板管理/停用。", t.Name, t.ID, interval))
}

// splitDuration parses "<n><unit>" (e.g. "30s", "5m", "2h", "1d").
func splitDuration(s string) (int, string, bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(s) {
		return 0, "", false
	}
	n := 0
	for _, c := range s[:i] {
		n = n*10 + int(c-'0')
	}
	u := s[i:]
	switch u {
	case "s", "m", "h", "d":
		return n, u, true
	}
	return 0, "", false
}

func cmdToken(b *Backend, st *State) Result {
	if b == nil || b.Engine == nil {
		return ok("引擎未初始化。")
	}
	if st.SessionID == "" {
		return ok("没有活跃会话，先发一条消息再查看统计。")
	}
	stats := b.Engine.SessionStats(st.SessionID)
	if stats == nil {
		return ok("暂无统计数据。")
	}
	var sb strings.Builder
	sb.WriteString("iCode Token 节省报告\n\n")
	sb.WriteString(fmt.Sprintf("已节省 Token:   %s\n", formatInt(stats.TokensSaved)))
	sb.WriteString(fmt.Sprintf("缓存命中率:     %.1f%%\n", stats.CacheHitRate*100))
	sb.WriteString(fmt.Sprintf("累计压缩次数:   %d\n", stats.CompactionsDone))
	sb.WriteString(fmt.Sprintf("Prompt Token:   %s\n", formatInt(stats.PromptTokens)))
	sb.WriteString(fmt.Sprintf("Completion:     %s\n", formatInt(stats.CompletionTokens)))
	sb.WriteString(fmt.Sprintf("总 Token:       %s\n", formatInt(stats.TotalTokens)))
	if stats.CacheHitTokens > 0 {
		sb.WriteString(fmt.Sprintf("缓存命中 Token: %s\n", formatInt(stats.CacheHitTokens)))
	}
	if stats.EstimatedCost > 0 {
		sb.WriteString(fmt.Sprintf("预估费用:       ¥%.4f\n", stats.EstimatedCost))
	}
	if stats.EstimatedSavedCost > 0 {
		sb.WriteString(fmt.Sprintf("预估节省:       ¥%.4f\n", stats.EstimatedSavedCost))
	}
	if len(stats.Rounds) > 0 {
		sb.WriteString("\n每轮明细（缓存命中即可见节省）:\n")
		for _, r := range stats.Rounds {
			hit := "   miss"
			if r.CacheHit > 0 {
				hit = fmt.Sprintf("hit %8s", formatInt(r.CacheHit))
			}
			bar := sparkTokens(r.Prompt)
			sb.WriteString(fmt.Sprintf("  #%-2d  prompt %-9s comp %-7s  cache %s  ¥%.4f  %s\n",
				r.Turn, formatInt(r.Prompt), formatInt(r.Completion), hit, r.Cost, bar))
		}
	}
	// Loops breakdown (Claude Code /usage parity): per-loop run count, total
	// tokens, tokens-per-run and last run so runaway /loop tasks stand out.
	if b.Scheduler != nil {
		if loops := b.Scheduler.LoopStats(); len(loops) > 0 {
			sb.WriteString("\nLoops 分解（自动化/循环任务）:\n")
			for _, l := range loops {
				last := "—"
				if !l.LastRun.IsZero() {
					last = l.LastRun.Format("01-02 15:04")
				}
				state := "停用"
				if l.Enabled {
					state = "启用"
				}
				sb.WriteString(fmt.Sprintf("  %-20s  runs %-3d  %s tok  ~%s/run  last %s  %s\n",
					truncateIcode(l.Name, 20), l.Runs, formatInt(l.Tokens),
					formatInt(l.TokensPerRun()), last, state))
			}
		}
	}
	sb.WriteString("\n机制: Cache-First Loop（不可变前缀 + 追加日志 + 易失暂存）\n5 层压缩: Snip → 去重 → 折叠 → 摘要 → 预算上限")
	return ok(sb.String())
}

// sparkTokens renders a tiny ASCII sparkline so prompt-size growth across turns
// is visible at a glance (each block ≈ 2k tokens).
func sparkTokens(prompt int) string {
	blocks := prompt / 2000
	if blocks > 12 {
		blocks = 12
	}
	return strings.Repeat("█", blocks) + strings.Repeat("░", 12-blocks)
}

func cmdContext(b *Backend, st *State) Result {
	if b == nil || b.Engine == nil {
		return ok("引擎未初始化。")
	}
	if st.SessionID == "" {
		return ok("没有活跃会话。")
	}
	stats := b.Engine.SessionStats(st.SessionID)
	if stats == nil {
		return ok("暂无上下文统计。")
	}
	return ok(fmt.Sprintf("上下文用量: %s tokens（Prompt），含 %s 缓存命中。\n窗口大小未知，受模型上下文上限约束。",
		formatInt(stats.PromptTokens), formatInt(stats.CacheHitTokens)))
}

func cmdSummarize(b *Backend, st *State) Result {
	if b == nil || b.SessStore == nil || st.SessionID == "" {
		return ok("没有可总结的会话（先发一条消息）。")
	}
	sess, err := b.SessStore.Get(st.SessionID)
	if err != nil {
		return errf("读取会话失败: %v", err)
	}
	summary := sessionum.Generate(sess, st.Model, st.Provider, st.Mode)
	if summary == "" {
		return ok("没有可总结的对话内容。")
	}
	if err := sessionum.Save(b.SessStore, sess, summary); err != nil {
		return errf("存档摘要失败: %v", err)
	}
	return ok(summary + "\n\n（摘要已存档，之后 /resume 会作为上下文前缀注入）")
}

func cmdCompact(b *Backend, st *State, args []string) Result {
	if b == nil || b.Engine == nil {
		return ok("引擎未初始化。")
	}
	if st.SessionID == "" {
		return ok("没有活跃会话。")
	}
	instruction := strings.Join(args, " ")
	stats := b.Engine.SessionStats(st.SessionID)

	// Claude Code parity: /compact now generates a model semantic summary of
	// the older turns and caches it (summary + recent turns from now on). The
	// Cache-First Loop stats are reported alongside.
	sess, err := b.SessStore.Get(st.SessionID)
	if err == nil && sess != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		sum, serr := b.Engine.SummarizeConversation(ctx, st.SessionID, instruction)
		cancel()
		if serr != nil {
			return errf("⚠ 模型语义摘要生成失败（%s）。Cache-First Loop 自动压缩不受影响，仍按原逻辑运行。", clip(serr.Error(), 140))
		}
		if sum == "" {
			return ok("会话轮次太少，无需模型压缩；Cache-First Loop 自动压缩会按需处理。")
		}
		_ = sessionum.Save(b.SessStore, sess, sum)
		_ = sessionum.MarkSemantic(b.SessStore, sess)
		out := "✓ 已用模型语义摘要压缩较早对话（已缓存，后续轮次按「摘要+最近消息」喂给模型）。\n"
		if stats != nil {
			out += fmt.Sprintf("Cache-First Loop 已自动压缩 %d 次，共节省 %s tokens。当前 Prompt: %s tokens。",
				stats.CompactionsDone, formatInt(stats.TokensSaved), formatInt(stats.PromptTokens))
		}
		return ok(out)
	}
	if stats == nil {
		return ok("会话采用 Cache-First Loop 自动压缩，无需手动操作。")
	}
	return ok(fmt.Sprintf("Cache-First Loop 自动压缩已运行 %d 次，已节省 %s tokens。\n当前 Prompt: %s tokens。",
		stats.CompactionsDone, formatInt(stats.TokensSaved), formatInt(stats.PromptTokens)))
}
