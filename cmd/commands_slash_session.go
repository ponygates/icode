package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/tui"
	"github.com/ponygates/icode/internal/types"
)

// slashSession handles every session-store scoped command: listing, resume,
// restore, export/import, fork, goal, budget, clear, summarize, search.
func (c *chatCallback) slashSession(cmd string, args []string) bool {
	switch cmd {
	case "/session":
		if c.sessionID != "" {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Active session: %s", c.sessionID))
		} else {
			c.tui.AddMessage(tui.RoleSystem, "No active session. Start typing to create one.")
		}
		return true

	case "/resume":
		c.slashResume(args)
		return true

	case "/restore":
		c.slashRestore(args)
		return true

	case "/export":
		c.slashExport(args)
		return true

	case "/import":
		c.slashImport(args)
		return true

	case "/fork":
		c.slashFork(args)
		return true

	case "/goal":
		c.slashGoal(args)
		return true

	case "/budget":
		c.slashBudget(args)
		return true

	case "/clear":
		c.slashClear()
		return true

	case "/summarize":
		c.slashSummarize()
		return true

	case "/search":
		c.slashSearch(args)
		return true
	}
	return false
}

// withActiveSession loads the current session and hands it to fn. It prints
// the same diagnostics the goal/budget commands have always printed when
// there is no session to work on.
func (c *chatCallback) withActiveSession(fn func(*types.Session)) {
	if c.sessionID == "" || c.app == nil || c.app.SessStore == nil {
		c.tui.AddMessage(tui.RoleSystem, "没有活跃会话。")
		return
	}
	sess, err := c.app.SessStore.Get(c.sessionID)
	if err != nil {
		c.tui.AddMessage(tui.RoleSystem, "读取会话失败: "+err.Error())
		return
	}
	fn(sess)
}

// slashResume implements /resume <id> [--lite[=<n>] | --compact[=<n>]].
func (c *chatCallback) slashResume(args []string) {
	if len(args) == 0 || c.app == nil || c.app.SessStore == nil {
		c.tui.AddMessage(tui.RoleSystem, "Usage: /resume <session-id> [--lite[=<n>] | --compact[=<n>]]")
		return
	}
	id := args[0]
	lite := 0
	compact := false
	bad := false
	if len(args) > 1 {
		switch a := strings.TrimSpace(args[1]); {
		case a == "--lite":
			lite = 4
		case strings.HasPrefix(a, "--lite="):
			fmt.Sscanf(strings.TrimPrefix(a, "--lite="), "%d", &lite)
			if lite <= 0 {
				c.tui.AddMessage(tui.RoleSystem, "用法: /resume <session-id> --lite=<n>（应为正整数）")
				bad = true
			}
		case a == "--compact":
			compact = true
			lite = 4
		case strings.HasPrefix(a, "--compact="):
			compact = true
			fmt.Sscanf(strings.TrimPrefix(a, "--compact="), "%d", &lite)
			if lite <= 0 {
				c.tui.AddMessage(tui.RoleSystem, "用法: /resume <session-id> --compact=<n>（应为正整数）")
				bad = true
			}
		default:
			c.tui.AddMessage(tui.RoleSystem, "用法: /resume <session-id> --lite=<n> 或 --compact[=<n>]")
			bad = true
		}
	}
	if !bad && lite > 0 {
		sess, err := c.app.SessStore.Get(id)
		if err != nil {
			c.tui.AddMessage(tui.RoleSystem, "会话不存在: "+id)
			return
		}
		if compact && !sessionum.IsSemantic(sess) && c.app.Engine != nil {
			// Resume compaction (Claude Code parity): generate a semantic
			// summary on the spot and cache it, so the resumed session
			// feeds the model only summary + recent turns. A cached
			// semantic summary is reused without a second model call.
			c.tui.AddMessage(tui.RoleSystem, "⏳ 正在生成会话语义摘要…（模型压缩，约 10–60 秒）")
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			sum, serr := c.app.Engine.SummarizeConversation(ctx, id, "")
			cancel()
			switch {
			case serr != nil:
				c.tui.AddMessage(tui.RoleSystem, "⚠ 模型摘要生成失败（"+clipStr(serr.Error(), 140)+"），已回退为本地存档摘要。")
			case sum == "":
				c.tui.AddMessage(tui.RoleSystem, "该会话轮次太少，无需模型摘要；如已有本地摘要则继续压缩恢复。")
			default:
				_ = sessionum.Save(c.app.SessStore, sess, sum)
				_ = sessionum.MarkSemantic(c.app.SessStore, sess)
			}
		}
		if sessionum.Get(sess) == "" {
			c.tui.AddMessage(tui.RoleSystem, "该会话还没有存档摘要，无法压缩恢复。先 /summarize、/compact 或退出时自动存档后再试。")
			return
		}
		if err := sessionum.SetLite(c.app.SessStore, sess, lite); err != nil {
			c.tui.AddMessage(tui.RoleSystem, "设置 lite 模式失败: "+err.Error())
			return
		}
		if compact {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("✓ 已启用紧凑恢复（语义摘要 + 最近 %d 条消息送入模型，后续轮次自动生效）", lite))
		}
	}
	if !bad {
		c.tui.AddMessage(tui.RoleSystem, c.OnResume(id))
	}
}

// slashRestore implements /restore <session-id> (undo a /clear soft delete).
func (c *chatCallback) slashRestore(args []string) {
	if len(args) == 0 || c.app == nil || c.app.SessStore == nil {
		c.tui.AddMessage(tui.RoleSystem, "Usage: /restore <session-id> — 恢复被 /clear 软删除的会话")
		return
	}
	if sess, err := c.app.SessStore.Get(args[0]); err != nil {
		c.tui.AddMessage(tui.RoleSystem, "会话不存在: "+args[0])
	} else if !sessionum.IsDeleted(sess) {
		c.tui.AddMessage(tui.RoleSystem, "该会话未被删除，无需恢复。")
	} else if err := sessionum.Restore(c.app.SessStore, sess); err != nil {
		c.tui.AddMessage(tui.RoleSystem, "恢复失败: "+err.Error())
	} else {
		c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已恢复会话 %s — %d 条消息", sess.ID, len(sess.Messages)))
	}
}

// slashExport implements /export <session-id> [output.json].
func (c *chatCallback) slashExport(args []string) {
	if len(args) == 0 || c.app == nil || c.app.SessStore == nil {
		c.tui.AddMessage(tui.RoleSystem, "Usage: /export <session-id> [output.json]")
		return
	}
	sess, err := c.app.SessStore.Get(args[0])
	if err != nil {
		c.tui.AddMessage(tui.RoleSystem, "会话不存在: "+args[0])
		return
	}
	data, err := sessionum.Export(sess)
	if err != nil {
		c.tui.AddMessage(tui.RoleSystem, "导出失败: "+err.Error())
		return
	}
	path := fmt.Sprintf("icode-%s.json", sess.ID)
	if len(args) > 1 {
		path = args[1]
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		c.tui.AddMessage(tui.RoleSystem, "写入失败: "+err.Error())
		return
	}
	c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已导出会话 %s（%d 条消息）到 %s", sess.ID, len(sess.Messages), path))
}

// slashImport implements /import <session.json>.
func (c *chatCallback) slashImport(args []string) {
	if len(args) == 0 || c.app == nil || c.app.SessStore == nil {
		c.tui.AddMessage(tui.RoleSystem, "Usage: /import <session.json>")
		return
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		c.tui.AddMessage(tui.RoleSystem, "读取失败: "+err.Error())
		return
	}
	sess, imported, err := sessionum.Import(c.app.SessStore, data)
	if err != nil {
		c.tui.AddMessage(tui.RoleSystem, "导入失败: "+err.Error())
		return
	}
	c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已导入会话 %s（%d 条消息），正在切换…", sess.ID, imported))
	c.tui.AddMessage(tui.RoleSystem, c.OnResume(sess.ID))
}

// slashFork implements /fork <session-id>[@<n>].
func (c *chatCallback) slashFork(args []string) {
	if len(args) > 0 && c.app != nil && c.app.SessStore != nil {
		spec := args[0]
		srcID := spec
		n := 0
		if at := strings.LastIndex(spec, "@"); at > 0 {
			srcID = spec[:at]
			fmt.Sscanf(spec[at+1:], "%d", &n)
		}
		forked, err := sessionum.Fork(c.app.SessStore, srcID, n)
		if err != nil {
			c.tui.AddMessage(tui.RoleSystem, "分叉失败: "+err.Error())
		} else {
			c.tui.AddMessage(tui.RoleSystem, c.OnResume(forked.ID))
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已从 %s 分叉出独立会话 %s — %d 条消息", srcID, forked.ID, len(forked.Messages)))
		}
		return
	}
	c.tui.AddMessage(tui.RoleSystem, "Usage: /fork <session-id>[@<n>]")
}

// slashGoal implements /goal set|show|clear (long-goal mode).
func (c *chatCallback) slashGoal(args []string) {
	sub := "show"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "set":
		c.goalSet(args)
	case "show":
		c.goalShow()
	case "clear", "unset":
		c.goalClear()
	default:
		c.tui.AddMessage(tui.RoleSystem, "用法: /goal set <目标> [--verify <验收命令>] | /goal show | /goal clear")
	}
}

// goalSet stores the long-goal text plus an optional --verify command.
func (c *chatCallback) goalSet(args []string) {
	if len(args) < 2 {
		c.tui.AddMessage(tui.RoleSystem, "用法: /goal set <目标文本> [--verify <验收命令>]")
		return
	}
	goal, verify := "", ""
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--verify" || rest[i] == "-v" {
			if i+1 < len(rest) {
				verify = strings.TrimSpace(strings.Join(rest[i+1:], " "))
			}
			break
		}
		if goal != "" {
			goal += " "
		}
		goal += rest[i]
	}
	goal = strings.TrimSpace(goal)
	if goal == "" {
		c.tui.AddMessage(tui.RoleSystem, "目标不能为空。用法: /goal set <目标文本> [--verify <验收命令>]")
		return
	}
	finalGoal, finalVerify := goal, verify
	c.withActiveSession(func(sess *types.Session) {
		if err := sessionum.SetGoal(c.app.SessStore, sess, finalGoal); err != nil {
			c.tui.AddMessage(tui.RoleSystem, "保存目标失败: "+err.Error())
			return
		}
		if finalVerify != "" {
			if err := sessionum.SetGoalVerify(c.app.SessStore, sess, finalVerify); err != nil {
				c.tui.AddMessage(tui.RoleSystem, "保存验收命令失败: "+err.Error())
				return
			}
			c.tui.AddMessage(tui.RoleSystem, "已设置可验收目标（每轮自动迭代直到验收命令通过）：\n目标："+finalGoal+"\n验收命令："+finalVerify)
			return
		}
		c.tui.AddMessage(tui.RoleSystem, "已设置长目标（后续每轮对话都会自动携带）：\n"+finalGoal)
	})
}

// goalShow prints the stored goal (and its verify command, if any).
func (c *chatCallback) goalShow() {
	c.withActiveSession(func(sess *types.Session) {
		if g := sessionum.GetGoal(sess); g != "" {
			msg := "当前目标（长目标模式生效中）：\n" + g
			if v := sessionum.GetGoalVerify(sess); v != "" {
				msg += "\n验收命令：" + v
			}
			c.tui.AddMessage(tui.RoleSystem, msg)
		} else {
			c.tui.AddMessage(tui.RoleSystem, "当前没有目标。\n用法: /goal set <目标> [--verify <验收命令>] | /goal show | /goal clear")
		}
	})
}

// goalClear drops both the goal text and its verify command.
func (c *chatCallback) goalClear() {
	c.withActiveSession(func(sess *types.Session) {
		if err := sessionum.SetGoal(c.app.SessStore, sess, ""); err != nil {
			c.tui.AddMessage(tui.RoleSystem, "清除目标失败: "+err.Error())
		} else {
			_ = sessionum.SetGoalVerify(c.app.SessStore, sess, "")
			c.tui.AddMessage(tui.RoleSystem, "已清除目标，退出长目标模式。")
		}
	})
}

// slashBudget implements /budget set|show|warn|clear (token guardrail).
func (c *chatCallback) slashBudget(args []string) {
	sub := "show"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "set":
		c.budgetSet(args)
	case "show":
		c.budgetShow()
	case "warn":
		c.budgetWarn(args)
	case "clear":
		c.budgetClear()
	default:
		c.tui.AddMessage(tui.RoleSystem, "用法: /budget set <上限> | /budget show | /budget warn <50-95> | /budget clear")
	}
}

// budgetSet stores the per-request token ceiling.
func (c *chatCallback) budgetSet(args []string) {
	if len(args) < 2 {
		c.tui.AddMessage(tui.RoleSystem, "用法: /budget set <上限token数>（如 /budget set 16000）")
		return
	}
	n := 0
	fmt.Sscanf(args[1], "%d", &n)
	if n < 1000 {
		c.tui.AddMessage(tui.RoleSystem, "预算应为 ≥1000 的 token 数。")
		return
	}
	c.withActiveSession(func(sess *types.Session) {
		if err := sessionum.SetBudget(c.app.SessStore, sess, n); err != nil {
			c.tui.AddMessage(tui.RoleSystem, "保存预算失败: "+err.Error())
		} else {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已设置 Token 预算: %d\n每次请求估算超限会自动压缩为摘要 + 最近消息。", n))
		}
	})
}

// budgetShow prints the budget, warning line and guardrail counters.
func (c *chatCallback) budgetShow() {
	c.withActiveSession(func(sess *types.Session) {
		if bg := sessionum.BudgetMax(sess); bg > 0 {
			line := fmt.Sprintf("当前 Token 预算: %d\n预警阈值: %d%%（/budget warn <50-95> 调整）", bg, sessionum.BudgetWarnPct(sess))
			if cnt, last, wc := sessionum.TrimStats(sess); cnt > 0 || wc > 0 {
				line += fmt.Sprintf("\n护栏记录: 自动压缩 %d 次", cnt)
				if last != "" {
					line += fmt.Sprintf("（最近 %s）", last)
				}
				if wc > 0 {
					line += fmt.Sprintf("，提前预警 %d 次", wc)
				}
			}
			c.tui.AddMessage(tui.RoleSystem, line)
		} else {
			c.tui.AddMessage(tui.RoleSystem, "当前没有 Token 预算。\n用法: /budget set <上限> | /budget show | /budget warn <50-95> | /budget clear")
		}
	})
}

// budgetWarn reads or adjusts the early-warning percentage.
func (c *chatCallback) budgetWarn(args []string) {
	if len(args) < 2 {
		c.withActiveSession(func(sess *types.Session) {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("当前预警阈值: %d%%\n用法: /budget warn <50-95> — 调整提前预警线", sessionum.BudgetWarnPct(sess)))
		})
		return
	}
	n := 0
	fmt.Sscanf(args[1], "%d", &n)
	c.withActiveSession(func(sess *types.Session) {
		if err := sessionum.SetWarnPct(c.app.SessStore, sess, n); err != nil {
			c.tui.AddMessage(tui.RoleSystem, "设置预警阈值失败: "+err.Error())
		} else {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已设置预算预警阈值: %d%%", n))
		}
	})
}

// budgetClear removes the token ceiling.
func (c *chatCallback) budgetClear() {
	c.withActiveSession(func(sess *types.Session) {
		if err := sessionum.SetBudget(c.app.SessStore, sess, 0); err != nil {
			c.tui.AddMessage(tui.RoleSystem, "关闭预算失败: "+err.Error())
		} else {
			c.tui.AddMessage(tui.RoleSystem, "已关闭 Token 预算，恢复完整上下文。")
		}
	})
}

// slashClear soft-deletes (archives) the active session and resets the view.
func (c *chatCallback) slashClear() {
	if c.sessionID != "" && c.app != nil && c.app.SessStore != nil {
		if sess, err := c.app.SessStore.Get(c.sessionID); err == nil {
			if err := sessionum.MarkDeleted(c.app.SessStore, sess); err != nil {
				c.tui.AddMessage(tui.RoleSystem, "归档失败（会话未删除）: "+err.Error())
			}
		}
	}
	c.sessionID = ""
	c.tui.LoadSession(nil)
	c.tui.AddMessage(tui.RoleSystem, "Session cleared (soft). /restore <id> 可恢复。")
}

// slashSummarize archives a zero-token session summary for a later /resume.
func (c *chatCallback) slashSummarize() {
	if c.sessionID != "" && c.app != nil && c.app.SessStore != nil {
		if sess, err := c.app.SessStore.Get(c.sessionID); err == nil {
			mode := ""
			if c.app.Gate != nil {
				mode = string(c.app.Gate.Mode())
			}
			sum := sessionum.Generate(sess, c.tui.CurrentModel(), c.tui.CurrentProvider(), mode)
			if sum != "" && sessionum.Save(c.app.SessStore, sess, sum) == nil {
				c.tui.AddMessage(tui.RoleSystem, "✓ 会话摘要已存档（零 token，退出后可恢复）。")
			}
		}
	}
}

// slashSearch implements /search <query> over stored conversations.
func (c *chatCallback) slashSearch(args []string) {
	query := strings.Join(args, " ")
	if query == "" {
		c.tui.AddMessage(tui.RoleSystem, "Usage: /search <query> — search past conversations")
		return
	}
	if c.app == nil || c.app.SessStore == nil {
		c.tui.AddMessage(tui.RoleSystem, "No session store available.")
		return
	}
	results, err := c.app.SessStore.SearchMessages(query, 20)
	if err != nil {
		c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Search error: %v", err))
		return
	}
	if len(results) == 0 {
		c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("No results for %q.", query))
		return
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🔍 Found %d results for %q:\n", len(results), query))
	for _, r := range results {
		title := r.SessionTitle
		if title == "" {
			title = "Untitled"
		}
		// Truncate content for display
		content := strings.ReplaceAll(r.Content, "\n", " ")
		if len(content) > 120 {
			content = content[:120] + "..."
		}
		b.WriteString(fmt.Sprintf("\n  [%s] %s\n", title, r.Role))
		b.WriteString(fmt.Sprintf("    %s\n", content))
	}
	b.WriteString(fmt.Sprintf("\n/resume <session_id> to load a session"))
	c.tui.AddMessage(tui.RoleSystem, b.String())
}
