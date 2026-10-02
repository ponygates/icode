package slashui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/hooks"
	"github.com/ponygates/icode/internal/core/plugins"
	"github.com/ponygates/icode/internal/core/skills"
	"github.com/ponygates/icode/internal/core/todo"
	"github.com/ponygates/icode/internal/core/tool"
)

// cmdHooks manages the engine's lifecycle hooks (PreToolUse/Stop/…), the
// ones actually loaded from config.yaml → hooks:. The old behaviour showed
// (and generated) ~/.icode/hooks.yaml — a permission-gate template that the
// hooks engine never reads, which silently misled users. This rewrite lists
// live rules and supports add/rm/reload with instant hot-reload.
func cmdHooks(b *Backend, args []string) Result {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "", "list", "ls":
		cfg, err := config.Load()
		if err != nil {
			return errf("读取配置失败: %v", err)
		}
		return ok(hooks.FormatRules(hooks.RulesFromConfig(cfg.Hooks)))

	case "events":
		return ok(hooks.EventsHelp())

	case "add":
		ev, rule, err := hooks.ParseAddArgs(args[1:])
		if err != nil {
			return errf("%v", err)
		}
		cfg, err := config.Load()
		if err != nil {
			return errf("读取配置失败: %v", err)
		}
		if cfg.Hooks == nil {
			cfg.Hooks = map[string][]config.HookRule{}
		}
		cfg.Hooks[string(ev)] = append(cfg.Hooks[string(ev)], config.HookRule{
			Matcher: rule.Matcher, Command: rule.Command, Timeout: rule.Timeout,
		})
		if err := cfg.Save(config.DefaultPath()); err != nil {
			return errf("保存配置失败: %v", err)
		}
		reloadHooks(b, cfg)
		detail := ""
		if rule.Matcher != "" {
			detail = "（匹配 " + rule.Matcher + "）"
		}
		return ok(fmt.Sprintf("✓ 已添加钩子 %s%s，已即时生效。\n%s", ev, detail, hooks.FormatRules(hooks.RulesFromConfig(cfg.Hooks))))

	case "rm", "remove", "del":
		if len(args) < 3 {
			return errf("用法: /hooks rm <事件> <序号>（序号见 /hooks 列表）")
		}
		ev := hooks.NormalizeEvent(args[1])
		if ev == "" {
			return errf("未知事件 %q（/hooks events 查看全部事件）", args[1])
		}
		n, err := strconv.Atoi(args[2])
		if err != nil || n <= 0 {
			return errf("序号必须是正整数，收到 %q", args[2])
		}
		cfg, err := config.Load()
		if err != nil {
			return errf("读取配置失败: %v", err)
		}
		list := cfg.Hooks[string(ev)]
		if n > len(list) {
			return errf("序号越界：%s 共 %d 条钩子", ev, len(list))
		}
		removed := list[n-1]
		cfg.Hooks[string(ev)] = append(list[:n-1], list[n:]...)
		if len(cfg.Hooks[string(ev)]) == 0 {
			delete(cfg.Hooks, string(ev))
		}
		if err := cfg.Save(config.DefaultPath()); err != nil {
			return errf("保存配置失败: %v", err)
		}
		reloadHooks(b, cfg)
		return ok(fmt.Sprintf("✓ 已删除 %s 的第 %d 条钩子: %s", ev, n, removed.Command))

	case "reload":
		cfg, err := config.Load()
		if err != nil {
			return errf("读取配置失败: %v", err)
		}
		reloadHooks(b, cfg)
		total := 0
		for _, list := range cfg.Hooks {
			total += len(list)
		}
		return ok(fmt.Sprintf("✓ 已从 config.yaml 重载 %d 条钩子并注入引擎。", total))

	default:
		return errf("未知子命令 %q。用法: /hooks [list|events|add|rm|reload]", args[0])
	}
}

// reloadHooks pushes the freshly persisted config into the live engine so
// hook changes apply without a restart. A nil backend/engine (tests,
// headless runners) is fine — the config on disk is still authoritative for
// the next startup.
func reloadHooks(b *Backend, cfg *config.Config) {
	if b == nil || b.Engine == nil {
		return
	}
	wd, _ := os.Getwd()
	b.Engine.SetHooksRunner(hooks.NewRunner(hooks.RulesFromConfig(cfg.Hooks), wd))
}

func cmdInit() Result {
	cwd, err := os.Getwd()
	if err != nil {
		return errf("%v", err)
	}
	p := filepath.Join(cwd, "ICODE.md")
	if _, err := os.Stat(p); err == nil {
		return ok("ICODE.md 已存在: " + p)
	}
	if err := os.WriteFile(p, []byte("# Project Context\n\nEdit this file.\n"), 0o644); err != nil {
		return errf("创建失败: %v", err)
	}
	return ok("已创建 " + p)
}

// cmdAgents renders the live agent panel — kept in sync with the TUI's
// agentsCommand: capability flags (fork/memory/isolation), teams, and
// background sub-agent runs.
func cmdAgents() Result {
	var b strings.Builder
	b.WriteString("子 agent 注册表:\n")
	v := agent.Load(agent.AgentDefaultDirs()...)
	v.RegisterDefaults()
	list := v.List()
	if len(list) == 0 {
		b.WriteString("  （无）\n")
	}
	for _, d := range list {
		flags := ""
		if d.Fork {
			flags += " fork"
		}
		if d.Memory != "" {
			flags += " memory:" + d.Memory
		}
		if d.Isolation != "" {
			flags += " isolation:" + d.Isolation
		}
		if flags != "" {
			flags = "  [" + flags + "]"
		}
		fmt.Fprintf(&b, "  %s — %s%s\n", d.Name, d.Description, flags)
	}

	if teams := agent.LoadTeams(agent.TeamDefaultDirs()...); len(teams) > 0 {
		b.WriteString("\n团队:\n")
		for _, tm := range teams {
			names := make([]string, 0, len(tm.Members))
			for _, m := range tm.Members {
				names = append(names, m.Name)
			}
			fmt.Fprintf(&b, "  %s (%d 成员: %s)\n", tm.Name, len(tm.Members), strings.Join(names, ", "))
		}
	}

	if lines := tool.ListAgentTaskLines(); len(lines) > 0 {
		b.WriteString("\n后台运行中:\n")
		for _, l := range lines {
			b.WriteString("  " + l + "\n")
		}
	}
	return ok(b.String())
}

// cmdTasks surfaces background jobs (detached sub-agents + shell commands)
// on the server/desktop surface — parity with the TUI /tasks panel.
func cmdTasks() Result {
	agentLines := tool.ListAgentTaskLines()
	shellLines := tool.ListShellTaskLines()
	if len(agentLines) == 0 && len(shellLines) == 0 {
		return ok("当前没有后台任务。\n后台子代理：task 工具传 background=true；\n后台命令：bash 工具传 run_in_background=true。")
	}
	var b strings.Builder
	if len(agentLines) > 0 {
		b.WriteString("后台子代理 (agt-N):\n")
		for _, l := range agentLines {
			b.WriteString("  " + l + "\n")
		}
	}
	if len(shellLines) > 0 {
		if len(agentLines) > 0 {
			b.WriteString("\n")
		}
		b.WriteString("后台命令 (bg-N):\n")
		for _, l := range shellLines {
			b.WriteString("  " + l + "\n")
		}
	}
	b.WriteString("\n查询输出：task_output(task_id=...)；实时跟踪：monitor(task_id=...)。")
	return ok(b.String())
}

func cmdSkills(args []string) Result {
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "market", "browse", "list-market":
			return marketList()
		case "install":
			if len(args) < 2 {
				return errf("用法: /skills install <名称>（先 /skills market 查看可安装项）")
			}
			if err := skills.Install(args[1]); err != nil {
				return errf("安装失败: %s", err.Error())
			}
			return ok("✓ 已安装技能「" + args[1] + "」。在对话中提及它的描述或触发词即可使用。")
		}
	}
	reg := skills.Load(skills.DefaultDirs()...)
	var b strings.Builder
	b.WriteString("已安装技能 (SKILL.md):\n")
	if list := reg.List(); len(list) == 0 {
		b.WriteString("  无。用 /skills market 浏览内置市场，或直接在 ~/.icode/skills/ 或 .icode/skills/ 放置 SKILL.md。\n")
	} else {
		for _, s := range list {
			trig := ""
			if len(s.Triggers) > 0 {
				trig = " 触发词: " + strings.Join(s.Triggers, ", ")
			}
			b.WriteString(fmt.Sprintf("  %s — %s%s\n", s.Name, s.Description, trig))
		}
	}
	return ok(b.String())
}

// marketList renders the built-in skill market (WorkBuddy SkillHub parity,
// offline-first): bundled skills annotated with install state.
func marketList() Result {
	cats := skills.ListCatalog()
	if len(cats) == 0 {
		return ok("技能市场当前为空。")
	}
	var b strings.Builder
	b.WriteString("技能市场（内置精选 · 离线可用）:\n")
	for _, c := range cats {
		mark := "  "
		if c.Installed {
			mark = "✓ "
		}
		b.WriteString(fmt.Sprintf(" %s%s — %s\n", mark, c.Name, c.Description))
	}
	b.WriteString("\n安装: /skills install <名称>")
	return ok(b.String())
}

// cmdSkillEval runs trigger-accuracy evals (desktop/server surface of the
// TUI /skill-eval command).
func cmdSkillEval(args []string) Result {
	reg := skills.Load(skills.DefaultDirs()...)
	scaffold := false
	name := ""
	for _, a := range args {
		if strings.EqualFold(a, "--scaffold") {
			scaffold = true
		} else {
			name = strings.TrimSpace(a)
		}
	}
	if name == "" {
		reports := skills.RunAllEvals(reg)
		if len(reports) == 0 {
			return ok("没有技能携带 evals.yaml。用 /skill-eval <名称> --scaffold 生成触发测试模板。")
		}
		var b strings.Builder
		b.WriteString("技能触发自测:\n")
		total, passed := 0, 0
		for _, rep := range reports {
			fmt.Fprintf(&b, "  %-16s %d/%d 通过\n", rep.SkillName, rep.Passed, rep.Total)
			total += rep.Total
			passed += rep.Passed
			for _, c := range rep.Cases {
				if !c.Pass {
					want := "漏触发"
					if !c.WantFire {
						want = "误触发"
					}
					fmt.Fprintf(&b, "      ✗ [%s] %q\n", want, c.Prompt)
				}
			}
		}
		pct := 100.0
		if total > 0 {
			pct = float64(passed) * 100 / float64(total)
		}
		fmt.Fprintf(&b, "\n总计: %d/%d (%.0f%%)", passed, total, pct)
		return ok(b.String())
	}

	s, found := reg.Get(name)
	if !found {
		return errf("未找到技能 %q（/skills 查看列表）", name)
	}
	if scaffold {
		if err := skills.ScaffoldEval(s); err != nil {
			return errf("脚手架失败: %v", err)
		}
		return ok("已创建 " + s.EvalPath())
	}
	suite, exists := skills.LoadEval(s)
	if !exists || len(suite.Cases) == 0 {
		return ok(fmt.Sprintf("技能 %q 还没有 evals.yaml。/skill-eval %s --scaffold 生成模板。", name, name))
	}
	rep := skills.RunEval(s, suite)
	var b strings.Builder
	fmt.Fprintf(&b, "技能 %q: %d/%d 通过 (%.0f%%)\n", name, rep.Passed, rep.Total, rep.PassRate()*100)
	for _, c := range rep.Cases {
		mark := "✓"
		if !c.Pass {
			mark = "✗"
		}
		fmt.Fprintf(&b, "  %s %q\n", mark, c.Prompt)
	}
	return ok(b.String())
}

// cmdPlugin manages bundled plugins (list/install/remove).
func cmdPlugin(args []string) Result {
	if len(args) == 0 || args[0] == "list" {
		list := plugins.List()
		if len(list) == 0 {
			return ok("没有已安装插件。安装：/plugin install <目录或.zip>")
		}
		var b strings.Builder
		b.WriteString("已安装插件:\n")
		for _, m := range list {
			v := m.Version
			if v == "" {
				v = "-"
			}
			fmt.Fprintf(&b, "  %s  v%s  %s\n", m.Name, v, m.Description)
		}
		return ok(b.String())
	}
	switch strings.ToLower(args[0]) {
	case "install":
		if len(args) < 2 {
			return errf("用法: /plugin install <目录|.zip> [--force]")
		}
		force := false
		for _, a := range args[2:] {
			if strings.EqualFold(a, "--force") {
				force = true
			}
		}
		m, err := plugins.Install(args[1], force)
		if err != nil {
			return errf("安装失败: %v", err)
		}
		return ok(fmt.Sprintf("✓ 插件 %q 已安装，其 skills/commands/agents 立即可用。", m.Name))
	case "remove", "uninstall":
		if len(args) < 2 {
			return errf("用法: /plugin remove <名称>")
		}
		if err := plugins.Remove(args[1]); err != nil {
			return errf("%v", err)
		}
		return ok("✓ 插件 " + args[1] + " 已卸载。")
	default:
		return errf("用法: /plugin [list | install <路径> | remove <名称>]")
	}
}

func cmdTeams() Result {
	var b strings.Builder
	b.WriteString("可用团队:\n")
	list := agent.LoadTeams(agent.TeamDefaultDirs()...)
	if len(list) == 0 {
		list = agent.DefaultTeamDefs()
	}
	for _, td := range list {
		members := make([]string, 0, len(td.Members))
		for _, m := range td.Members {
			members = append(members, m.Name)
		}
		b.WriteString(fmt.Sprintf("  %s — %s [成员: %s]\n", td.Name, td.Description, strings.Join(members, ", ")))
	}
	return ok(b.String())
}

func cmdTodo(st *State) Result {
	if st.SessionID == "" {
		return ok("没有活跃会话，无法查看任务。")
	}
	p, a, d, total := todo.Default.Counts(st.SessionID)
	if total == 0 {
		return ok("当前会话没有待办任务。模型执行任务时可自动创建。")
	}
	return ok(fmt.Sprintf("待办任务 (%d 待办 · %d 进行中 · %d 已完成):\n\n可通过 `todo_write` 工具查看/更新。", p, a, d))
}
