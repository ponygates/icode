package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/slashcmd"
	"github.com/ponygates/icode/internal/core/tool"
)

// handleSlashInfo dispatches the read-only / informational commands.
// It reports whether the command was claimed.
func (t *TUI) handleSlashInfo(cmd string, args []string) bool {
	switch cmd {
	case "/help":
		t.slashHelp()
	case "/expand":
		t.slashExpand()
	case "/status":
		t.slashStatus()
	case "/cost", "/usage", "/stats":
		t.slashCost()
	case "/token":
		t.slashToken()
	case "/keys":
		t.slashKeys()
	case "/history":
		t.slashHistory()
	case "/tasks":
		t.slashTasks()
	case "/todo":
		t.slashTodo()
	case "/welcome":
		t.slashWelcome()
	case "/whoami":
		t.slashWhoami()
	case "/context":
		t.slashContext()
	case "/feedback":
		t.slashFeedback()
	case "/release-notes":
		t.releaseNotesCommand()
	case "/bug":
		t.bugCommand()
	case "/doctor":
		t.doctor()
	default:
		return false
	}
	return true
}

func (t *TUI) slashHelp() {
	var b strings.Builder
	b.WriteString(t.tstr("cmd.help") + ":\n")
	for _, d := range slashDefs {
		b.WriteString(fmt.Sprintf("  %-14s %s\n", d.Name, t.tstr(d.Key)))
	}

	// Append user-defined slash commands (.icode/commands/*.md) so
	// `/help` reflects everything the current session will accept.
	if custom := slashcmd.CachedLoad(slashcmd.DefaultDirs()...).List(); len(custom) > 0 {
		b.WriteString("\n自定义命令:\n")
		for _, c := range custom {
			hint := c.ArgumentHint
			if hint != "" {
				hint = " " + hint
			}
			b.WriteString(fmt.Sprintf("  %-14s %s\n", c.Name+hint, c.Description))
		}
	}

	b.WriteString("\n特殊语法:\n")
	b.WriteString("  # <内容>          追加到 ~/.icode/CLAUDE.md\n")
	b.WriteString("  ! <shell>         运行 shell 命令（输出进上下文，AI 自动响应）\n")
	b.WriteString("\n" + t.tstr("sc.title") + ":\n")
	b.WriteString("  Ctrl+C           " + t.tstr("sc.ctrlc") + "\n")
	b.WriteString("  Ctrl+L           " + t.tstr("sc.ctrll") + "\n")
	b.WriteString("  Ctrl+P / Ctrl+N  " + t.tstr("sc.history") + "\n")
	b.WriteString("  Ctrl+Y           复制最近助手回复到剪贴板\n")
	b.WriteString("  Ctrl+J           多行输入换行（Enter 发送）\n")
	b.WriteString("  Ctrl+S           暂存提示词 / 空输入时恢复\n")
	b.WriteString("  Ctrl+G           用 $EDITOR 编辑当前提示词\n")
	b.WriteString("  Ctrl+_           撤销上一步输入编辑\n")
	b.WriteString("  Ctrl+B           当前任务转入后台（可继续输入，消息排队）\n")
	b.WriteString("  Ctrl+R           反向历史搜索\n")
	b.WriteString("  Ctrl+O           展开/折叠全部工具执行详情（transcript）\n")
	b.WriteString("  Alt+P            切换模型（不清空输入）\n")
	b.WriteString("  Alt+T            切换扩展思考（extended thinking）\n")
	b.WriteString("  Tab              接受补全建议 / 权限框内加「拒绝说明」\n")
	b.WriteString("  Esc              中断生成 · 双 Esc 清空草稿或回溯\n")
	b.WriteString("  " + t.tstr("cmd.ac"))
	t.add(RoleSystem, b.String())
}

func (t *TUI) slashExpand() {
	t.toolFolded = !t.toolFolded
	// Global fold preference now drives every existing tool card too, so
	// /expand still works as the all-or-nothing switch while individual
	// cards keep their own state after a click.
	t.mu.Lock()
	for i := range t.messages {
		if t.messages[i].Role == RoleTool {
			t.messages[i].Folded = !t.toolFolded
		}
	}
	t.mu.Unlock()
	if t.toolFolded {
		t.add(RoleSystem, "工具输出已全部展开（点击卡片头 ▾ 可单独折叠）")
	} else {
		t.add(RoleSystem, "工具输出已折叠（只显示前 8 行），点击卡片头 ▸ 展开单个")
	}
}

func (t *TUI) slashStatus() {
	if t.callback != nil {
		t.add(RoleSystem, t.callback.OnStatus())
	} else {
		t.add(RoleSystem, "引擎未初始化。")
	}
}

// slashCost prefers the full token-savings report when the engine is
// available; falls back to the simple cost panel. Mirrors slashui where /cost
// and /token are identical aliases.
func (t *TUI) slashCost() {
	if t.callback != nil {
		t.add(RoleSystem, t.callback.OnTokenStats())
	} else {
		t.costPanel()
	}
}

func (t *TUI) slashToken() {
	if t.callback != nil {
		t.add(RoleSystem, t.callback.OnTokenStats())
	} else {
		t.add(RoleSystem, "引擎未初始化。")
	}
}

func (t *TUI) slashKeys() {
	cfg, err := config.Load()
	if err != nil {
		t.add(RoleSystem, "无法读取配置: "+err.Error())
		return
	}
	var b strings.Builder
	b.WriteString("API 密钥状态：\n")
	for name, pc := range cfg.Providers {
		st := "未配置"
		if pc.APIKey != "" {
			st = "已配置"
		}
		b.WriteString(fmt.Sprintf("  %-14s %s\n", name, st))
	}
	b.WriteString("\n用 `icode config key <provider> <key>` 或桌面端设置配置。")
	t.add(RoleSystem, b.String())
}

func (t *TUI) slashHistory() {
	if len(t.history) == 0 {
		t.add(RoleSystem, "无历史记录。")
		return
	}
	var b strings.Builder
	for i, h := range t.history {
		b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, h))
	}
	t.add(RoleSystem, b.String())
}

func (t *TUI) slashTasks() {
	var b strings.Builder
	agentLines := tool.ListAgentTaskLines()
	shellLines := tool.ListShellTaskLines()
	if len(agentLines) == 0 && len(shellLines) == 0 {
		b.WriteString("当前没有后台任务。\n后台子代理：task 工具传 background=true；\n后台命令：bash 工具传 run_in_background=true。")
	} else {
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
		b.WriteString("\n查询输出：让模型调用 task_output(task_id=...)，或直接问「bg-1 输出是什么」。")
	}
	t.add(RoleSystem, b.String())
}

func (t *TUI) slashTodo() {
	var b strings.Builder
	if t.callback != nil {
		if p, a, d, total := t.callback.TodoCounts(); total > 0 {
			fmt.Fprintf(&b, "📋 待办 (%d 待处理 · %d 进行中 · %d 完成)\n\n", p, a, d)
			b.WriteString("（详情可通过 `todo_write` 工具查看 — 当前只在状态栏显示计数）\n")
		} else {
			b.WriteString("当前会话没有待办事项。让模型执行任务时会自动创建。\n")
		}
	}
	t.add(RoleSystem, b.String())
}

func (t *TUI) slashWelcome() {
	t.mu.Lock()
	empty := len(t.messages) == 0
	t.welcomeVisible = !t.welcomeVisible
	vis := t.welcomeVisible
	t.mu.Unlock()
	if vis && empty {
		t.render() // conversation is empty → banner will show
	} else if vis && !empty {
		t.add(RoleSystem, t.tstr("welcome.reopen"))
	} else {
		t.render() // hidden
	}
}

func (t *TUI) slashWhoami() {
	cwd, _ := os.Getwd()
	t.add(RoleSystem, fmt.Sprintf("iCode %s\n  Model:     %s\n  Provider:  %s\n  Security:  %s\n  CWD:       %s",
		t.version, t.model, t.provider,
		permission.SecurityLabel(config.SecurityLevel(t.securityLevel)), shortDir(cwd)))
}

func (t *TUI) slashContext() {
	if t.contextWindow > 0 {
		pct := float64(t.contextTokens) * 100 / float64(t.contextWindow)
		t.add(RoleSystem, fmt.Sprintf("上下文窗口: %d / %d tokens (%.0f%%)", t.contextTokens, t.contextWindow, pct))
	} else {
		t.add(RoleSystem, fmt.Sprintf("上下文用量: %d tokens（窗口大小未知）", t.contextTokens))
	}
}

func (t *TUI) slashFeedback() {
	t.add(RoleSystem, "反馈渠道:\n  · GitHub Issues: https://github.com/ponygates/icode/issues\n  · 对话中输入 `# <建议>` 可写入记忆文件")
}
