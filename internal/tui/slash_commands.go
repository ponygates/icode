package tui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	i18n "github.com/ponygates/icode/internal/config/i18n"
	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/checkpoint"
	projectcontext "github.com/ponygates/icode/internal/core/context"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/searchreplace"
	"github.com/ponygates/icode/internal/core/skills"
	"github.com/ponygates/icode/internal/core/slashcmd"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/executil"
)

// ── Slash commands ───────────────────────────────────────────────

// setMode switches the TUI mode AND the backend gate's mode so the displayed
// mode always matches what the permission layer enforces (/mode and
// Shift+Tab both funnel through here).
func (t *TUI) setMode(mode string) {
	t.mode = mode
	if t.callback != nil {
		if msg := t.callback.OnSetMode(mode); msg != "" {
			t.notice(msg)
		}
	}
}

func (t *TUI) handleSlash(text string) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}
	cmd := strings.ToLower(parts[0])
	args := parts[1:]
	t.noteRecentCmd(cmd)

	switch cmd {
	case "/help":
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
		b.WriteString("  ! <shell>         运行 shell 命令\n")
		b.WriteString("\n" + t.tstr("sc.title") + ":\n")
		b.WriteString("  Ctrl+C           " + t.tstr("sc.ctrlc") + "\n")
		b.WriteString("  Ctrl+L           " + t.tstr("sc.ctrll") + "\n")
		b.WriteString("  Ctrl+P / Ctrl+N  " + t.tstr("sc.history") + "\n")
		b.WriteString("  Ctrl+Y           复制最近助手回复到剪贴板\n")
		b.WriteString("  " + t.tstr("cmd.ac"))
		t.add(RoleSystem, b.String())

	case "/exit", "/quit":
		// Summary-on-exit: archive a zero-token session summary so a later
		// /resume can rebuild context without re-reading the full transcript.
		if t.callback != nil {
			t.callback.OnSlashCommand("/summarize", nil)
		}
		t.add(RoleSystem, "再见！👋")
		t.running = false

	case "/expand":
		t.toolFolded = !t.toolFolded
		if t.toolFolded {
			t.add(RoleSystem, "工具输出已折叠（只显示前 8 行），再运行 /expand 展开全部")
		} else {
			t.add(RoleSystem, "工具输出已全部展开")
		}

	case "/model":
		if len(args) == 0 {
			// No argument → open the interactive model picker (Claude Code
			// style): ↑/↓ move, Enter confirms, Esc cancels, a digit jumps.
			t.openModelPicker()
			return
		}
		// Accept /model <n> (1-based index into the picker) or /model <id>.
		if n, err := strconv.Atoi(args[0]); err == nil && n >= 1 && n <= len(t.models) {
			t.model = t.models[n-1]
		} else {
			id := args[0]
			idx := indexOfString(t.models, id)
			if idx >= 0 {
				t.model = id
				t.modelIdx = idx
			} else {
				// Accept the ID as-is but warn if it isn't in the known list.
				t.model = id
				t.modelIdx = -1
				t.add(RoleSystem, "⚠️ 模型 “"+id+"” 不在可用列表中（仍需手动确认）")
				return
			}
		}
		t.notice("Model -> " + t.model)
		t.add(RoleSystem, t.tstr("mode.set")+" -> "+t.model)

	case "/mode":
		if len(args) > 0 {
			want := strings.ToLower(args[0])
			valid := map[string]bool{"agent": true, "plan": true, "yolo": true, "auto": true, "ask": true}
			if valid[want] {
				t.setMode(want)
				t.add(RoleSystem, "Mode -> "+want)
			} else {
				t.add(RoleError, "无效模式: "+want+"（可选 agent/plan/yolo/auto/ask）")
			}
		} else {
			t.add(RoleSystem, "当前模式: "+t.mode+"\n用法: /mode <agent|plan|yolo|auto|ask>")
		}

	case "/plan", "/ask", "/debug":
		want := strings.TrimPrefix(cmd, "/")
		if want == "debug" {
			want = "agent"
		}
		t.setMode(want)
		t.add(RoleSystem, "Mode -> "+want)

	case "/cd":
		t.changeDir(args)

	case "/rename":
		t.renameSession(args)

	case "/session", "/sessions":
		if t.callback != nil {
			t.add(RoleSystem, t.callback.OnListSessions())
		}

	case "/new", "/newsession":
		if t.callback != nil {
			t.callback.OnSlashCommand("/clear", nil)
		}
		t.mu.Lock()
		t.messages = nil
		t.promptTokens = 0
		t.completionTokens = 0
		t.cost = ""
		t.cacheHitRate = 0
		t.mu.Unlock()
		t.add(RoleSystem, "新会话已创建。")

	case "/resume":
		if len(args) > 0 && t.callback != nil {
			t.callback.OnSlashCommand("/resume", args)
		} else if t.callback != nil {
			// No session ID → interactive picker (raw mode) or a static
			// numbered list (line mode).
			t.openResumePicker()
		}

	case "/fork", "/branch":
		cmdName := cmd
		if len(args) > 0 && t.callback != nil {
			t.callback.OnSlashCommand("/fork", args)
		} else {
			t.add(RoleSystem, "用法: "+cmdName+" <session-id>[@<n>] — 从历史会话分支出一个独立会话")
		}

	case "/restore":
		// Restore a soft-deleted session (/clear marks sessions as deleted;
		// /restore undoes that). Forwarded to the backend store so both the
		// soft-delete flag and the message list are repaired atomically.
		if t.callback != nil {
			t.callback.OnSlashCommand("/restore", args)
		} else {
			t.add(RoleSystem, "用法: /restore <session-id> — 恢复被 /clear 软删除的会话")
		}

	case "/goal":
		if t.callback != nil {
			t.callback.OnSlashCommand("/goal", args)
		} else {
			t.add(RoleSystem, "用法: /goal set <目标> | /goal show | /goal clear")
		}

	case "/budget":
		if t.callback != nil {
			t.callback.OnSlashCommand("/budget", args)
		} else {
			t.add(RoleSystem, "用法: /budget set <上限token数> | /budget show | /budget clear")
		}

	case "/clear":
		// Archive the current session (soft-delete) via the callback first so
		// the CLI/slashui behaviour matches: the session can later be recovered
		// with /restore. Then wipe the local message list.
		if t.callback != nil {
			t.callback.OnSlashCommand("/clear", nil)
		}
		t.mu.Lock()
		t.messages = nil
		t.promptTokens = 0
		t.completionTokens = 0
		t.cost = ""
		t.cacheHitRate = 0
		t.mu.Unlock()
		t.add(RoleSystem, "对话已清空（可从 /sessions 恢复）")

	case "/search":
		query := strings.Join(args, " ")
		if query == "" {
			t.add(RoleSystem, "用法: /search <关键词> — 搜索历史对话")
			break
		}
		if t.callback != nil {
			t.callback.OnSlashCommand("/search", args)
		} else {
			t.add(RoleSystem, "搜索需要会话存储支持。")
		}

	case "/compact":
		t.compactCommand(args)

	case "/export":
		t.exportMarkdown(args)

	case "/copy":
		t.copyLastAssistant(args)

	case "/share":
		// Export the conversation to a timestamped Markdown file and print the
		// absolute path, so it can be pasted into docs / sent to others.
		name := fmt.Sprintf("icode-share-%s.md", time.Now().Format("20060102-150405"))
		t.exportMarkdown([]string{name})
		if abs, err := filepath.Abs(name); err == nil {
			t.add(RoleSystem, "📤 可分享副本: "+abs+"\n（该 Markdown 文件可直接发送或粘贴到支持 Markdown 的工具）")
		}

	case "/diff":
		t.showGitDiff(args)

	case "/lsp":
		sub := "status"
		if len(args) > 0 {
			sub = args[0]
			args = args[1:]
		}
		t.add(RoleSystem, t.callback.LSPQuery(sub, args))

	case "/kb", "/knowledge":
		t.add(RoleSystem, t.callback.KnowledgeQuery(strings.Join(args, " ")))

	case "/idle":
		if len(args) < 2 {
			t.add(RoleSystem, "用法: /idle <名称> <任务描述>（闲时窗口内自动执行）")
			break
		}
		t.add(RoleSystem, t.callback.CreateIdleTask(args[0], strings.Join(args[1:], " ")))

	case "/output-style":
		if len(args) == 0 {
			cur := "normal"
			if c, err := config.Load(); err == nil && c.Defaults.OutputStyle != "" {
				cur = c.Defaults.OutputStyle
			}
			t.add(RoleSystem, "当前输出风格: "+cur+"\n用法: /output-style <concise|normal|verbose>")
			break
		}
		style := strings.ToLower(args[0])
		if style != "concise" && style != "normal" && style != "verbose" {
			t.add(RoleError, "无效风格: "+args[0]+"（可选 concise|normal|verbose）")
			break
		}
		if t.callback != nil {
			t.add(RoleSystem, t.callback.OnOutputStyle(style))
		} else {
			t.persistSetting(func(c *config.Config) { c.Defaults.OutputStyle = style })
			t.add(RoleSystem, "输出风格已设为 "+style+"（已持久化，重启会话后生效）")
		}

	case "/update":
		if t.callback != nil {
			t.add(RoleSystem, t.callback.OnUpdateModels())
		} else {
			t.add(RoleSystem, "引擎未初始化。")
		}

	case "/add-dir":
		if len(args) == 0 {
			var list []string
			if c, err := config.Load(); err == nil {
				list = c.Defaults.ExtraDirs
			}
			if len(list) == 0 {
				t.add(RoleSystem, "没有额外工作目录。\n用法: /add-dir <路径>")
			} else {
				t.add(RoleSystem, "额外工作目录：\n  "+strings.Join(list, "\n  ")+"\n\n添加: /add-dir <路径>")
			}
			break
		}
		if t.callback != nil {
			t.add(RoleSystem, t.callback.OnAddDir(args[0]))
		} else {
			t.add(RoleSystem, "引擎未初始化。")
		}

	case "/rewind", "/checkpoint":
		n := 1
		if len(args) > 0 {
			fmt.Sscanf(args[0], "%d", &n)
		}
		if n <= 0 {
			t.add(RoleSystem, "用法: /rewind [N]  — 回滚前 N 步工具调用")
			break
		}
		t.rewindSteps(n)

	case "/undo":
		t.rewindSteps(1)

	case "/apply":
		if t.callback != nil {
			t.callback.OnSlashCommand("/apply", args)
		} else {
			t.applyStagedEdits()
		}

	case "/reject":
		if t.callback != nil {
			t.callback.OnSlashCommand("/reject", args)
		} else {
			t.rejectStagedEdits()
		}

	case "/admin":
		if len(args) == 0 || (len(args) > 0 && strings.ToLower(args[0]) != "off") {
			if t.callback != nil {
				t.callback.OnSlashCommand("/mode", []string{"yolo"})
			}
			t.mode = "yolo"
			t.add(RoleSystem, "管理员模式开启（yolo 模式，不再逐一询问工具权限）")
		} else {
			if t.callback != nil {
				t.callback.OnSlashCommand("/mode", []string{"ask"})
			}
			t.mode = "ask"
			t.add(RoleSystem, "管理员模式已关闭，恢复 ask 模式")
		}

	case "/todo":
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

	case "/init":
		cwd, err := os.Getwd()
		if err != nil {
			t.add(RoleError, err.Error())
			break
		}
		if _, err := os.Stat(filepath.Join(cwd, "ICODE.md")); err == nil {
			t.add(RoleSystem, "ICODE.md 已存在")
			break
		}
		_ = os.WriteFile(filepath.Join(cwd, "ICODE.md"), []byte("# Project Context\n\nEdit this file.\n"), 0o644)
		t.add(RoleSystem, "[x] ICODE.md 已生成")

	case "/agents":
		t.agentsCommand()

	case "/skills":
		reg := skills.Load(skills.DefaultDirs()...)
		var a strings.Builder
		a.WriteString("可用技能 (SKILL.md):\n")
		if list := reg.List(); len(list) == 0 {
			a.WriteString("  （无。在 ~/.icode/skills/ 或 .icode/skills/ 下放置 SKILL.md 即可启用）\n")
		} else {
			for _, s := range list {
				trig := ""
				if len(s.Triggers) > 0 {
					trig = " 触发: " + strings.Join(s.Triggers, ", ")
				}
				a.WriteString(fmt.Sprintf("  %s — %s%s\n", s.Name, s.Description, trig))
			}
		}
		t.add(RoleSystem, a.String())

	case "/teams":
		var a strings.Builder
		a.WriteString("多智能体团队:\n")
		list := agent.LoadTeams(agent.TeamDefaultDirs()...)
		if len(list) == 0 {
			list = agent.DefaultTeamDefs()
		}
		for _, td := range list {
			members := make([]string, 0, len(td.Members))
			for _, m := range td.Members {
				members = append(members, m.Name)
			}
			a.WriteString(fmt.Sprintf("  %s — %s [成员: %s]\n", td.Name, td.Description, strings.Join(members, ", ")))
		}
		t.add(RoleSystem, a.String())

	case "/mcp":
		t.mcpCommand(args)

	case "/hooks":
		home, _ := os.UserHomeDir()
		path := filepath.Join(home, ".icode", "hooks.yaml")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			permission.GenerateHooks(path)
			t.add(RoleSystem, "[x] "+path)
		} else {
			data, _ := os.ReadFile(path)
			t.add(RoleSystem, fmt.Sprintf("📄 %s\n\n```yaml\n%s\n```", path, string(data)))
		}

	case "/status":
		if t.callback != nil {
			t.add(RoleSystem, t.callback.OnStatus())
		} else {
			t.add(RoleSystem, "引擎未初始化。")
		}

	case "/cost", "/usage", "/stats":
		// Prefer the full Token-savings report (with per-round detail and
		// Cache-First Loop stats) when the engine is available; fall back to
		// the simple cost panel. This mirrors slashui where /cost and /token
		// are identical aliases.
		if t.callback != nil {
			t.add(RoleSystem, t.callback.OnTokenStats())
		} else {
			t.costPanel()
		}

	case "/provider":
		if len(args) > 0 {
			t.provider = args[0]
			t.add(RoleSystem, "Provider -> "+args[0])
		} else {
			t.add(RoleSystem, "当前 Provider: "+t.provider+"\n用法: /provider <name>")
		}

	case "/token":
		if t.callback != nil {
			t.add(RoleSystem, t.callback.OnTokenStats())
		} else {
			t.add(RoleSystem, "引擎未初始化。")
		}
	case "/multiline":
		t.multiline = !t.multiline
		if t.multiline {
			t.add(RoleSystem, "[Multiline ON] Enter=newline, Alt+Enter=send")
		} else {
			t.add(RoleSystem, "[Multiline OFF]")
		}

	case "/keys":
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

	case "/models":
		t.modelsCommand(args)

	case "/config":
		t.configCommand(args)

	case "/history":
		if len(t.history) == 0 {
			t.add(RoleSystem, "无历史记录。")
			return
		}
		var b strings.Builder
		for i, h := range t.history {
			b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, h))
		}
		t.add(RoleSystem, b.String())

	case "/theme":
		if len(args) > 0 {
			switch strings.ToLower(args[0]) {
			case "auto", "dark", "light":
				t.theme = strings.ToLower(args[0])
				t.persistSetting(func(c *config.Config) { c.TUI.Theme = t.theme })
				t.add(RoleSystem, fmt.Sprintf(t.tstr("theme.set"), t.theme))
			default:
				t.add(RoleSystem, t.tstr("theme.usage"))
			}
		} else {
			t.add(RoleSystem, t.tstr("theme.usage"))
		}

	case "/lang":
		if len(args) > 0 {
			switch args[0] {
			case "zh-CN", "zh-TW", "en":
				t.lang = args[0]
				// Sync the global translator so cmd-layer i18n.Tr strings
				// (prompts, CLI banners) follow the same locale.
				i18n.T.SetLanguage(i18n.Lang(args[0]))
				t.persistSetting(func(c *config.Config) { c.Language = t.lang })
				t.add(RoleSystem, fmt.Sprintf(t.tstr("lang.set"), t.lang)+
					"\n"+t.tstr("lang.modelNote"))
			default:
				t.add(RoleSystem, t.tstr("lang.usage"))
			}
		} else {
			cur := t.lang
			if cur == "" {
				cur = "zh-CN"
			}
			t.add(RoleSystem, t.tstr("lang.usage")+"\n"+fmt.Sprintf(t.tstr("lang.current"), cur))
		}

	case "/summarize":
		if len(t.messages) == 0 {
			t.add(RoleSystem, t.tstr("cmd.summarize")+": 没有对话内容可总结。")
			return
		}
		var b strings.Builder
		b.WriteString("## 对话总结\n\n")
		msgCount := 0
		for _, m := range t.messages {
			if m.Role == RoleUser || m.Role == RoleAssistant {
				msgCount++
			}
		}
		b.WriteString(fmt.Sprintf("共 %d 条消息，模型: %s，提供商: %s，模式: %s\n\n", msgCount, t.model, t.provider, t.mode))
		if t.promptTokens > 0 || t.completionTokens > 0 {
			b.WriteString(fmt.Sprintf("Token: %d 输入 + %d 输出 = %d 总计", t.promptTokens, t.completionTokens, t.promptTokens+t.completionTokens))
			if t.cost != "" {
				b.WriteString(" · 费用: " + t.cost)
			}
			if t.cacheHitRate > 0 {
				b.WriteString(fmt.Sprintf(" · 缓存: %.0f%%", t.cacheHitRate*100))
			}
			b.WriteString("\n\n")
		}

		// List key topics from user messages
		b.WriteString("### 用户提问\n\n")
		for _, m := range t.messages {
			if m.Role == RoleUser {
				trunc := m.Content
				if len([]rune(trunc)) > 120 {
					trunc = string([]rune(trunc)[:120]) + "…"
				}
				b.WriteString(fmt.Sprintf("- %s\n", trunc))
			}
		}
		t.add(RoleSystem, b.String())
		// Also archive a zero-token summary to the session store so a later
		// /resume can rebuild context without re-reading the transcript.
		if t.callback != nil {
			t.callback.OnSlashCommand("/summarize", nil)
		}

	case "/review":
		t.reviewCommand(args)

	case "/security":
		if len(args) == 0 {
			t.add(RoleSystem, fmt.Sprintf(t.tstr("security.usage"),
				permission.SecurityLabel(config.SecurityLevel(t.securityLevel))))
			return
		}
		newLevel := strings.ToLower(args[0])
		valid := map[string]config.SecurityLevel{
			"local":        config.SecLocal,
			"desensitize":  config.SecDesensitize,
			"local-llm":    config.SecLocalLLM,
			"foreign-llm":  config.SecForeignLLM,
			"unrestricted": config.SecUnrestricted,
		}
		level, ok := valid[newLevel]
		if !ok {
			t.add(RoleSystem, fmt.Sprintf(t.tstr("security.usage"),
				permission.SecurityLabel(config.SecurityLevel(t.securityLevel))))
			return
		}
		t.securityLevel = newLevel
		t.persistSetting(func(c *config.Config) { c.SecurityLevel = level })
		t.add(RoleSystem, fmt.Sprintf(t.tstr("security.set"),
			permission.SecurityLabel(level)))

	case "/welcome":
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

	case "/doctor":
		t.doctor()

	case "/whoami":
		cwd, _ := os.Getwd()
		t.add(RoleSystem, fmt.Sprintf("iCode %s\n  Model:     %s\n  Provider:  %s\n  Security:  %s\n  CWD:       %s",
			t.version, t.model, t.provider,
			permission.SecurityLabel(config.SecurityLevel(t.securityLevel)), shortDir(cwd)))

	case "/context":
		if t.contextWindow > 0 {
			pct := float64(t.contextTokens) * 100 / float64(t.contextWindow)
			t.add(RoleSystem, fmt.Sprintf("上下文窗口: %d / %d tokens (%.0f%%)", t.contextTokens, t.contextWindow, pct))
		} else {
			t.add(RoleSystem, fmt.Sprintf("上下文用量: %d tokens（窗口大小未知）", t.contextTokens))
		}

	case "/permissions":
		t.permissionsCommand(args)

	case "/verbose":
		t.verbose = !t.verbose
		if t.verbose {
			t.add(RoleSystem, "[x] 详细输出已开启（完整工具参数 / 原始 diff）")
		} else {
			t.add(RoleSystem, "[ ] 详细输出已关闭")
		}

	case "/vim":
		t.vimMode = !t.vimMode
		t.persistSetting(func(c *config.Config) { c.TUI.Vim = t.vimMode })
		if t.vimMode {
			t.vimInsert = true
			t.add(RoleSystem, "[x] Vim 模式已开启（Esc 进入普通模式: h/l/0/$ 移动, x 删字符, dd 删整行, u 撤销, i/a/I/A 插入）")
		} else {
			t.add(RoleSystem, "[ ] Vim 模式已关闭")
		}

	case "/statusline":
		t.statusVisible = !t.statusVisible
		t.persistSetting(func(c *config.Config) {
			v := t.statusVisible
			c.TUI.ShowStatusLine = &v
		})
		if t.statusVisible {
			t.add(RoleSystem, "[x] 底部状态栏已显示")
		} else {
			t.add(RoleSystem, "[ ] 底部状态栏已隐藏")
		}
		t.render()

	case "/pr_comments":
		t.prCommentsCommand(args)

	case "/release-notes":
		t.releaseNotesCommand()

	case "/bug":
		t.bugCommand()

	case "/memory":
		t.memoryCommand(args)

	case "/tasks":
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

	case "/feedback":
		t.add(RoleSystem, "反馈渠道:\n  · GitHub Issues: https://github.com/ponygates/icode/issues\n  · 对话中输入 `# <建议>` 可写入记忆文件")

	case "/wipe":
		t.mu.Lock()
		t.messages = nil
		t.promptTokens = 0
		t.completionTokens = 0
		t.cost = ""
		t.cacheHitRate = 0
		t.scrollOffset = 0
		t.mu.Unlock()
		t.add(RoleSystem, "对话已清空并重置。")

	case "/login":
		t.add(RoleSystem, "配置 API 凭据:\n  · CLI: icode config key <provider> <apikey>\n  · 桌面端: 设置 → 提供商 → 填入 Key\n配置完成后用 /doctor 验证连通性。")

	case "/logout":
		t.add(RoleSystem, "清除凭据:\n  · CLI: icode config key <provider> \"\"\n  · 桌面端: 设置 → 提供商 → 删除 Key\n（CLI 不在此命令中直接清除凭据，避免误删。）")

	default:
		// User-defined slash command? Look it up in .icode/commands/*.md
		// (user + project scope) and expand it into a normal chat message.
		if custom := t.tryCustomSlash(cmd, strings.Join(args, " ")); custom {
			return
		}
		if t.callback != nil {
			t.callback.OnSlashCommand(cmd, args)
		}
	}
}

// projectMemoryPath returns the project-level memory file (ICODE.md) path.
func (t *TUI) projectMemoryPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "ICODE.md"
	}
	return filepath.Join(cwd, "ICODE.md")
}

// doctor runs a quick, non-blocking system diagnostic (Claude Code's /doctor).
// It reports config sanity, configured providers, and memory file paths. No
// network calls are made so it can never hang on a restricted network.
func (t *TUI) doctor() {
	var b strings.Builder
	b.WriteString("iCode 诊断:\n")
	b.WriteString(fmt.Sprintf("  版本:       %s\n", t.version))
	b.WriteString(fmt.Sprintf("  模型:       %s\n", t.model))
	b.WriteString(fmt.Sprintf("  提供商:     %s\n", t.provider))
	b.WriteString(fmt.Sprintf("  安全等级:   %s\n",
		permission.SecurityLabel(config.SecurityLevel(t.securityLevel))))
	cfg, err := config.Load()
	if err != nil {
		b.WriteString("  配置:       读取失败 " + err.Error() + "\n")
	} else {
		configured := 0
		for _, pc := range cfg.Providers {
			if pc.APIKey != "" {
				configured++
			}
		}
		b.WriteString(fmt.Sprintf("  已配置 Key: %d 个提供商\n", configured))
	}
	if proj := t.projectMemoryPath(); proj != "" {
		b.WriteString("  项目记忆:   " + proj + "\n")
	}
	if user, err := projectcontext.UserMemoryPath(); err == nil {
		b.WriteString("  用户记忆:   " + user + "\n")
	}
	t.add(RoleSystem, b.String())
}

// tryCustomSlash resolves `cmd` (e.g. "/changelog") against the user- and
// project-scoped command registry loaded from .icode/commands/*.md. When a
// match is found, its template is expanded and the result is submitted as a
// regular user message. Returns true if a custom command handled the input.
func (t *TUI) tryCustomSlash(cmd, argStr string) bool {
	reg := slashcmd.CachedLoad(slashcmd.DefaultDirs()...)
	c, ok := reg.Get(cmd)
	if !ok {
		return false
	}
	expanded, err := c.Expand(context.Background(), argStr)
	if err != nil {
		t.add(RoleError, fmt.Sprintf("展开 %s 失败: %v", cmd, err))
		return true
	}
	if strings.TrimSpace(expanded) == "" {
		t.add(RoleSystem, fmt.Sprintf("命令 %s 展开为空", cmd))
		return true
	}

	// Feed the expanded text into the normal submit flow so it is displayed
	// as a user message and streamed through the LLM. Bypass the leading
	// prefix scan (! # /) that the raw submit() runs — the expanded body
	// might legitimately start with any of those characters.
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleUser, Content: fmt.Sprintf("(%s) %s", cmd, argStr)})
	t.streaming = true
	t.streamBuf.Reset()
	t.turnStart = time.Now()
	t.mu.Unlock()
	if t.callback != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.add(RoleError, fmt.Sprintf("内部错误: %v", r))
				}
			}()
			t.callback.OnSend(expanded)
		}()
	}
	t.ensureAnim()
	t.drainStream()
	return true
}

// ── Claude Code-parity command helpers ──────────────────────────

// mcpCommand manages MCP servers (Claude Code's /mcp has list/add/get/remove/
// restart subcommands). Configuration is persisted to the user config file.
func (t *TUI) mcpCommand(args []string) {
	cfg, err := config.Load()
	if err != nil {
		t.add(RoleSystem, "无法读取配置: "+err.Error())
		return
	}
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "", "list":
		var b strings.Builder
		b.WriteString("MCP 服务器:\n")
		if len(cfg.MCP) == 0 {
			b.WriteString("  （无。用 `/mcp add <name> <stdio|sse> <command> [args...]` 添加，\n   或编辑 ~/.icode/config.yaml 的 mcp 段）\n")
		}
		for _, s := range cfg.MCP {
			en := "✓"
			if !s.Enabled {
				en = "·"
			}
			line := fmt.Sprintf("  %s %s [%s] %s", en, s.Name, s.Type, s.Command)
			if s.URL != "" {
				line += " " + s.URL
			}
			b.WriteString(line + "\n")
		}
		t.add(RoleSystem, b.String())
	case "add":
		if len(args) < 4 {
			t.add(RoleSystem, "用法: /mcp add <name> <stdio|sse> <command> [args...]")
			return
		}
		name := args[1]
		typ := strings.ToLower(args[2])
		if typ != "stdio" && typ != "sse" {
			t.add(RoleSystem, "类型只能是 stdio 或 sse")
			return
		}
		mc := config.MCPServerCfg{Name: name, Type: typ, Command: args[3], Enabled: true}
		if len(args) > 4 {
			mc.Args = args[4:]
		}
		filtered := make([]config.MCPServerCfg, 0, len(cfg.MCP))
		for _, s := range cfg.MCP {
			if s.Name != name {
				filtered = append(filtered, s)
			}
		}
		cfg.MCP = append(filtered, mc)
		if err := cfg.Save(config.DefaultPath()); err != nil {
			t.add(RoleError, "保存失败: "+err.Error())
			return
		}
		t.add(RoleSystem, fmt.Sprintf("[x] 已添加 MCP 服务器 %s（%s）。重启 iCode 后生效。", name, typ))
	case "remove":
		if len(args) < 2 {
			t.add(RoleSystem, "用法: /mcp remove <name>")
			return
		}
		name := args[1]
		filtered := make([]config.MCPServerCfg, 0, len(cfg.MCP))
		found := false
		for _, s := range cfg.MCP {
			if s.Name != name {
				filtered = append(filtered, s)
			} else {
				found = true
			}
		}
		if !found {
			t.add(RoleSystem, "未找到 MCP 服务器: "+name)
			return
		}
		cfg.MCP = filtered
		if err := cfg.Save(config.DefaultPath()); err != nil {
			t.add(RoleError, "保存失败: "+err.Error())
			return
		}
		t.add(RoleSystem, fmt.Sprintf("[x] 已移除 MCP 服务器 %s。重启 iCode 后生效。", name))
	case "get":
		if len(args) < 2 {
			t.add(RoleSystem, "用法: /mcp get <name>")
			return
		}
		name := args[1]
		for _, s := range cfg.MCP {
			if s.Name == name {
				var b strings.Builder
				fmt.Fprintf(&b, "MCP 服务器 %s:\n", name)
				fmt.Fprintf(&b, "  类型: %s\n", s.Type)
				fmt.Fprintf(&b, "  命令: %s\n", s.Command)
				if len(s.Args) > 0 {
					fmt.Fprintf(&b, "  参数: %s\n", strings.Join(s.Args, " "))
				}
				if s.URL != "" {
					fmt.Fprintf(&b, "  地址: %s\n", s.URL)
				}
				fmt.Fprintf(&b, "  启用: %v\n", s.Enabled)
				if s.TrustMode != "" {
					fmt.Fprintf(&b, "  信任模式: %s\n", s.TrustMode)
				}
				t.add(RoleSystem, b.String())
				return
			}
		}
		t.add(RoleSystem, "未找到 MCP 服务器: "+name)
	case "restart":
		if len(args) < 2 {
			t.add(RoleSystem, "用法: /mcp restart <name>")
			return
		}
		t.add(RoleSystem, fmt.Sprintf("已请求重启 %s。MCP 连接于启动时建立，请重启 iCode 使新配置生效。", args[1]))
	default:
		t.add(RoleSystem, "用法: /mcp [list] | add <name> <stdio|sse> <command> [args...] | remove <name> | get <name> | restart <name>")
	}
}

// memoryCommand shows (or edits) the memory files (Claude Code's /memory).
func (t *TUI) memoryCommand(args []string) {
	proj := t.projectMemoryPath()
	user, _ := projectcontext.UserMemoryPath()
	if len(args) > 0 && strings.ToLower(args[0]) == "edit" {
		editor := os.Getenv("EDITOR")
		if editor == "" {
			if runtime.GOOS == "windows" {
				editor = "notepad"
			} else {
				editor = "vi"
			}
		}
		cmd := exec.Command(editor, proj)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.add(RoleError, "打开编辑器失败: "+err.Error())
		} else {
			t.add(RoleSystem, "[x] 已用 "+editor+" 打开项目记忆 "+proj)
		}
		return
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("记忆文件:\n  项目级: %s\n  用户级: %s\n\n", proj, user))
	if data, err := os.ReadFile(proj); err == nil && len(data) > 0 {
		b.WriteString("── 项目记忆 (ICODE.md) ──\n" + string(data) + "\n")
	} else {
		b.WriteString("（项目记忆为空，用 `# <内容>` 追加，或 `/memory edit` 编辑）\n")
	}
	t.add(RoleSystem, b.String())
}

// permissionsCommand shows the current permission/security configuration.
func (t *TUI) permissionsCommand(args []string) {
	cfg, _ := config.Load()
	var b strings.Builder
	b.WriteString("权限 / 安全:\n")
	b.WriteString(fmt.Sprintf("  当前安全等级: %s\n", permission.SecurityLabel(config.SecurityLevel(t.securityLevel))))
	b.WriteString(fmt.Sprintf("  配置值:       %s\n", t.securityLevel))
	if cfg != nil {
		if len(cfg.Hooks) > 0 {
			b.WriteString("  生命周期钩子:\n")
			for ev, rules := range cfg.Hooks {
				for _, r := range rules {
					b.WriteString(fmt.Sprintf("    %-12s %s\n", ev, r.Command))
				}
			}
		}
	}
	b.WriteString("\n切换安全等级: /security [local|desensitize|local-llm|foreign-llm|unrestricted]\n")
	b.WriteString("工具级允许/拒绝请在桌面端 设置 → 工具权限 中配置。")
	t.add(RoleSystem, b.String())
}

// reviewCommand runs a code review over a path or the working-tree diff
// (Claude Code's /review). When staged SEARCH/REPLACE edits are pending it
// opens the interactive diff overlay instead; otherwise the gathered content
// is fed to the model as a normal user turn so the assistant streams back
// the analysis.
func (t *TUI) reviewCommand(args []string) {
	var target string
	if len(args) > 0 {
		path := args[0]
		if data, err := os.ReadFile(path); err == nil {
			target = fmt.Sprintf("请审查文件 %s：\n\n%s", path, string(data))
		} else {
			t.add(RoleError, "读取文件失败: "+err.Error())
			return
		}
	} else {
		// With pending staged edits, show the interactive review panel.
		if len(t.stage().List()) > 0 {
			t.openDiffBox()
			return
		}
		cmd := executil.Command("git", "diff")
		out, err := cmd.CombinedOutput()
		if err != nil && len(out) == 0 {
			t.add(RoleError, "git diff 失败: "+err.Error())
			return
		}
		if len(out) == 0 {
			t.add(RoleSystem, "没有未提交的改动可审查。可指定路径：/review <file>")
			return
		}
		target = "请审查以下 git 工作区差异，指出质量问题、安全隐患与改进建议：\n\n```diff\n" + string(out) + "\n```"
	}
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleUser, Content: target})
	t.streaming = true
	t.streamBuf.Reset()
	t.turnStart = time.Now()
	t.mu.Unlock()
	if t.callback != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.add(RoleError, fmt.Sprintf("内部错误: %v", r))
				}
			}()
			t.callback.OnSend(target)
		}()
	}
	t.ensureAnim()
	t.drainStream()
}

// configCommand shows the current configuration, or sets a key when given
// `set <key> <value>` (Claude Code's /config opens an interactive menu; here
// we expose the most useful keys directly).
// modelsCommand implements /models with optional add/rm subcommands so the TUI
// can manage user-defined models without dropping to the CLI:
//
//	/models                     → list custom models
//	/models add <p> <id> [name] → persist + live-register a custom model
//	/models rm <id>             → remove a custom model (id = provider/model_id)
func (t *TUI) modelsCommand(args []string) {
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "add":
			if len(args) < 3 {
				t.add(RoleSystem, "用法: /models add <provider> <model_id> [name]")
				return
			}
			name := args[2]
			if len(args) >= 4 {
				name = strings.Join(args[3:], " ")
			}
			msg := t.callback.OnAddCustomModel(args[1], args[2], name)
			t.add(RoleSystem, msg)
			return
		case "rm", "remove", "del", "delete":
			if len(args) < 2 {
				t.add(RoleSystem, "用法: /models rm <id>（id 形如 provider/model_id）")
				return
			}
			t.add(RoleSystem, t.callback.OnRemoveCustomModel(args[1]))
			return
		}
	}

	cfg, err := config.Load()
	if err != nil || len(cfg.Models) == 0 {
		t.add(RoleSystem, "暂无自定义模型。\n用 `/models add <provider> <model_id> [name]` 或 `icode config model add <provider> <model_id> [name]` 新增。")
		return
	}
	var b strings.Builder
	b.WriteString("自定义模型：\n")
	for _, m := range cfg.Models {
		name := m.Name
		if name == "" {
			name = m.ModelID
		}
		b.WriteString(fmt.Sprintf("  %-26s %s / %s\n", m.ID, m.Provider, name))
	}
	t.add(RoleSystem, b.String())
}

func (t *TUI) configCommand(args []string) {
	if len(args) > 0 && strings.ToLower(args[0]) == "set" {
		if len(args) < 3 {
			t.add(RoleSystem, "用法: /config set <key> <value>\n可配置: theme|lang|security|model|provider")
			return
		}
		key := strings.ToLower(args[1])
		val := strings.Join(args[2:], " ")
		switch key {
		case "theme":
			if val != "auto" && val != "dark" && val != "light" {
				t.add(RoleSystem, "theme 仅支持 auto|dark|light")
				return
			}
			t.theme = val
			t.persistSetting(func(c *config.Config) { c.TUI.Theme = val })
			t.add(RoleSystem, "Theme -> "+val)
		case "lang":
			if val != "zh-CN" && val != "zh-TW" && val != "en" {
				t.add(RoleSystem, "lang 仅支持 zh-CN|zh-TW|en")
				return
			}
			t.lang = val
			t.persistSetting(func(c *config.Config) { c.Language = val })
			t.add(RoleSystem, "Language -> "+val)
		case "security":
			t.securityLevel = val
			lvl := config.ParseSecurityLevel(val)
			t.persistSetting(func(c *config.Config) { c.SecurityLevel = lvl })
			t.add(RoleSystem, "Security -> "+permission.SecurityLabel(lvl))
		case "model":
			t.model = val
			t.add(RoleSystem, "Model -> "+val)
		case "provider":
			t.provider = val
			t.add(RoleSystem, "Provider -> "+val)
		default:
			t.add(RoleSystem, "未知配置项: "+key)
			return
		}
		return
	}
	cfg, _ := config.Load()
	lang := "zh-CN"
	theme := "auto"
	diff := "unified"
	syntax := "on"
	if cfg != nil {
		lang = cfg.Language
		theme = cfg.TUI.Theme
		diff = cfg.TUI.DiffMode
		if !cfg.TUI.SyntaxHL {
			syntax = "off"
		}
	}
	t.add(RoleSystem, fmt.Sprintf("当前设置：\n  Model:     %s\n  Provider:  %s\n  Mode:      %s\n  Language:  %s\n  Theme:     %s\n  Diff:      %s\n  Security:  %s\n  Syntax:    %s\n\n用 `/config set <key> <value>` 或 `/lang` `/theme` `/security` 即时切换。",
		t.model, t.provider, t.mode, lang, theme, diff,
		permission.SecurityLabel(config.SecurityLevel(t.securityLevel)), syntax))
}

// prCommentsCommand shows pull-request comments via the GitHub CLI (Claude
// Code's /pr_comments). Requires `gh` to be installed and authenticated.
func (t *TUI) prCommentsCommand(args []string) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.add(RoleSystem, "未检测到 GitHub CLI (gh)。请先安装并登录：https://cli.github.com")
		return
	}
	pr := ""
	if len(args) > 0 {
		pr = args[0]
	}
	var cmd *exec.Cmd
	if pr != "" {
		cmd = exec.Command("gh", "pr", "view", pr, "--comments", "--json", "title,comments")
	} else {
		cmd = exec.Command("gh", "pr", "view", "--comments", "--json", "title,comments")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.add(RoleError, "获取 PR 评论失败: "+string(out))
		return
	}
	t.add(RoleSystem, "PR 评论:\n"+string(out))
}

// releaseNotesCommand prints the latest release notes from CHANGELOG.md
// (Claude Code's /release-notes).
func (t *TUI) releaseNotesCommand() {
	candidates := []string{"CHANGELOG.md", filepath.Join(".icode", "CHANGELOG.md")}
	var data []byte
	for _, c := range candidates {
		if d, err := os.ReadFile(c); err == nil {
			data = d
			break
		}
	}
	if len(data) == 0 {
		t.add(RoleSystem, "未找到 CHANGELOG.md。")
		return
	}
	text := string(data)
	if i := strings.Index(text, "\n## "); i > 0 {
		rest := text[i+1:]
		if j := strings.Index(rest, "\n## "); j > 0 {
			text = text[:i+1+j]
		}
	}
	const max = 2000
	if len(text) > max {
		text = text[:max] + "\n…"
	}
	t.add(RoleSystem, "发布说明:\n"+text)
}

// bugCommand opens a pre-filled GitHub issue for bug reports (Claude Code's
// /bug).
func (t *TUI) bugCommand() {
	body := fmt.Sprintf("**环境**: iCode %s / %s / %s\n**复现步骤**:\n1. \n\n**预期**: \n**实际**: ",
		t.version, t.provider, t.model)
	u := "https://github.com/ponygates/icode/issues/new?title=%5Bbug%5D&body=" + url.QueryEscape(body)
	t.add(RoleSystem, "请在此提交 Bug 报告：\n"+u)
	openURL(u)
}

// openURL opens a URL in the default browser (cross-platform).
func openURL(u string) {
	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	case "darwin":
		_ = exec.Command("open", u).Start()
	default:
		_ = exec.Command("xdg-open", u).Start()
	}
}

// add appends a message and refreshes the screen.
func (t *TUI) add(role Role, content string) {
	t.AddMessage(role, content)
}

// ── Shell mode ───────────────────────────────────────────────────

// appendMemory writes a quick memory note, mirroring Claude Code's `#`
// shortcut. Plain `# note` targets the project memory file (./ICODE.md);
// `# user: note` targets the cross-project user memory (~/.icode/).
// The same rule applies in the desktop app so both surfaces stay consistent.
func (t *TUI) appendMemory(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		t.add(RoleSystem, "用法: # <记到项目 ICODE.md>  |  # user: <记到用户级 ~/.icode>")
		return
	}
	lower := strings.ToLower(text)
	var userPrefix string
	if strings.HasPrefix(lower, "user:") {
		userPrefix = "user:"
	} else if strings.HasPrefix(lower, "user：") {
		userPrefix = "user："
	}
	if userPrefix != "" {
		note := strings.TrimSpace(text[len(userPrefix):])
		if err := projectcontext.AppendUserMemory(note); err != nil {
			t.add(RoleError, "追加 memory 失败: "+err.Error())
			return
		}
		path, _ := projectcontext.UserMemoryPath()
		t.add(RoleSystem, fmt.Sprintf("[x] 已记录到用户级 %s", path))
		return
	}
	path, err := projectcontext.AppendProjectMemory(text)
	if err != nil {
		t.add(RoleError, "追加 memory 失败: "+err.Error())
		return
	}
	t.add(RoleSystem, fmt.Sprintf("[x] 已记录到项目 %s", path))
}

func (t *TUI) execShell(cmdStr string) {
	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return
	}
	t.add(RoleTool, "bash "+cmdStr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") {
		cmd = executil.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = executil.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.add(RoleError, err.Error())
	}
	if len(output) > 0 {
		t.AppendToolResult(strings.TrimRight(string(output), "\n"))
	}
}

// ── Compact ──────────────────────────────────────────────────────

// compactCommand mirrors Claude Code's /compact [instructions]: it summarises
// the older turns of the conversation into a single system note so the model
// keeps the context while freeing up the token budget. An optional instruction
// string is folded into the summary so the user can steer what is preserved.
//
// Since v0.37.5 the summary is model-generated (semantic): the backend asks
// the configured model to compress the older turns into a structured summary
// (目标/已完成/关键决策/文件改动/待办/下一步), cached into the session metadata
// so a later /resume --compact reuses it without a second model call. When the
// model is unavailable or fails, it falls back to the free local line dump.
func (t *TUI) compactCommand(args []string) {
	instruction := strings.Join(args, " ")
	t.mu.Lock()
	if len(t.messages) < 4 {
		t.mu.Unlock()
		t.add(RoleSystem, "Not enough messages to compact.")
		return
	}
	t.mu.Unlock()

	// Async so the TUI keeps rendering while the model works.
	t.add(RoleSystem, "⏳ 正在生成语义摘要…（模型压缩，约 10–60 秒）")
	go func() {
		sum := ""
		if t.callback != nil {
			sum = t.callback.OnCompactSummarize(instruction)
		}
		if sum != "" {
			// Semantic path: keep system notes + the last 4 turns, replace the
			// rest with a [Compacted] summary note (Claude Code style).
			t.mu.Lock()
			var keep []Message
			for _, m := range t.messages {
				if m.Role == RoleSystem {
					keep = append(keep, m)
				}
			}
			var recent []Message
			for i := len(t.messages) - 1; i >= 0 && len(recent) < 4; i-- {
				m := t.messages[i]
				if m.Role == RoleUser || m.Role == RoleAssistant {
					recent = append([]Message{m}, recent...)
				}
			}
			keep = append(keep, Message{Role: RoleSystem, Content: "[Compacted] 已由模型压缩的早期对话摘要:\n\n" + sum})
			keep = append(keep, recent...)
			t.messages = keep
			t.mu.Unlock()
			t.render()
			t.add(RoleSystem, "✓ 已用模型语义摘要压缩较早对话（已缓存，/resume --compact 可复用）。")
			return
		}

		// Fallback: free local line dump (existing behavior).
		t.mu.Lock()
		var keep []Message
		var summary strings.Builder
		summary.WriteString("[Compacted] Summary of earlier turns")
		if instruction != "" {
			summary.WriteString(" (focus: " + instruction + ")")
		}
		summary.WriteString(":\n")
		count := 0
		for _, m := range t.messages {
			if m.Role == RoleSystem || count >= len(t.messages)-4 {
				keep = append(keep, m)
			} else {
				summary.WriteString(fmt.Sprintf("  %s: %s\n", m.Role, truncate(m.Content, 80)))
				count++
			}
		}
		t.messages = keep
		t.messages = append(t.messages, Message{Role: RoleSystem, Content: summary.String()})
		t.mu.Unlock()
		t.render()
		t.add(RoleSystem, "✓ 已压缩较早的对话上下文。（模型摘要不可用，已用本地摘要）")
	}()
}

// ── Rewind / Cost panel ──────────────────────────────────────────

// rewindSteps rolls back the last n tool-call steps via the checkpoint store.
// Shared by /rewind and /undo. Before rolling back it surfaces a diff preview
// of the changes about to be reverted (Claude Code parity).
func (t *TUI) rewindSteps(n int) {
	sessionID := ""
	if t.callback != nil {
		sessionID = t.callback.SessionID()
	}
	if sessionID == "" {
		t.add(RoleSystem, "没有活跃会话。")
		return
	}
	store, err := checkpoint.GetOrOpen(sessionID)
	if err != nil {
		t.add(RoleError, "打开检查点失败: "+err.Error())
		return
	}
	// Preview the diff that the rewind will revert.
	if preview, perr := store.Diff(context.Background(), n); perr == nil && strings.TrimSpace(preview) != "" {
		t.add(RoleSystem, fmt.Sprintf("⏪ 即将回滚 %d 步，以下改动将被撤销:\n```diff\n%s\n```", n, strings.TrimRight(preview, "\n")))
	}
	files, err := store.Rewind(context.Background(), n)
	if err != nil {
		t.add(RoleError, "回滚失败: "+err.Error())
		return
	}
	msg := fmt.Sprintf("✓ 已回滚 %d 步。影响文件:\n", n)
	for _, f := range files {
		msg += "  " + f + "\n"
	}
	t.add(RoleSystem, msg)
}

// miniBar renders a small progress bar (used by the cache-hit meter).
func miniBar(frac float64, width int) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	fill := int(frac * float64(width))
	return repeat("█", fill) + repeat("░", width-fill)
}

// costPanel shows the current session's token/cost breakdown with a cache-hit
// meter, then points to /token for the full Cache-First Loop savings report.
func (t *TUI) costPanel() {
	var b strings.Builder
	b.WriteString("💰 费用（本次会话）\n")
	b.WriteString(fmt.Sprintf("  Prompt:     %s\n", formatTokens(t.promptTokens)))
	b.WriteString(fmt.Sprintf("  Completion: %s\n", formatTokens(t.completionTokens)))
	b.WriteString(fmt.Sprintf("  合计:       %s\n", formatTokens(t.promptTokens+t.completionTokens)))
	if t.cacheHitRate > 0 {
		b.WriteString(fmt.Sprintf("  缓存命中率:  %.0f%% %s\n", t.cacheHitRate*100, miniBar(t.cacheHitRate, 10)))
	}
	if t.cost != "" {
		b.WriteString(fmt.Sprintf("  估算费用:    %s（本轮）\n", t.cost))
	}
	b.WriteString("\n提示: /token 查看完整 Token 节省报告（Cache-First Loop 五层压缩）")
	t.add(RoleSystem, b.String())
}

// ── Export ───────────────────────────────────────────────────────

func (t *TUI) exportMarkdown(args []string) {
	filename := "icode-export.md"
	if len(args) > 0 {
		filename = args[0]
	}
	t.mu.Lock()
	msgs := append([]Message{}, t.messages...)
	t.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("# iCode Conversation Export\n\n")
	sb.WriteString(fmt.Sprintf("**Model:** %s  \n", t.model))
	sb.WriteString(fmt.Sprintf("**Mode:** %s  \n\n", t.mode))
	sb.WriteString("---\n\n")
	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			sb.WriteString("## User\n\n")
		case RoleAssistant:
			sb.WriteString("## Assistant\n\n")
		case RoleSystem:
			sb.WriteString("> ")
		case RoleTool:
			sb.WriteString("### Tool: " + m.Tool + "\n\n")
		case RoleError:
			sb.WriteString("### Error\n\n")
		}
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}
	// Windows: prepend UTF-8 BOM so Notepad recognizes the file as UTF-8.
	content := sb.String()
	if runtime.GOOS == "windows" {
		content = "\xef\xbb\xbf" + content
	}
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		t.add(RoleError, "Export failed: "+err.Error())
		return
	}
	t.add(RoleSystem, fmt.Sprintf("已导出到 %s（%d 条消息）", filename, len(msgs)))
}

// ── Git diff ─────────────────────────────────────────────────────

func (t *TUI) showGitDiff(args []string) {
	gitArgs := []string{"diff"}
	gitArgs = append(gitArgs, args...)
	cmd := executil.Command("git", gitArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		t.add(RoleError, "git diff: "+err.Error())
		return
	}
	if len(output) == 0 {
		t.add(RoleSystem, "没有未暂存的改动。")
		return
	}
	t.AddToolMessage("git_diff", "", t.colorizeDiffStr(strings.TrimRight(string(output), "\n")))
}

// showModelPicker lists the available models with a 1-based index and marks
// the active one, so the user can switch via `/model <n>` or `/model <id>`.
// It works in both raw and line mode (it just appends a system message).
func (t *TUI) showModelPicker() {
	if len(t.models) == 0 {
		t.add(RoleSystem, "暂无可用模型列表。\n  请先配置 API Key：icode auth set --provider <provider> --key <YOUR_KEY>\n  或直接切换：/model <模型ID>（如 /model openrouter/free）")
		return
	}
	t.openModelPicker()
}

// buildModelPickerList renders a static /model list (used in line mode, where
// the interactive overlay is unavailable). The row at highlightIdx is marked
// with ▶ (pass -1 for no highlight). The current model is tagged "(当前)".
func (t *TUI) buildModelPickerList(highlightIdx int) string {
	var b strings.Builder
	b.WriteString("选择模型（↑/↓ 移动，Enter 确认，Esc 取消；也可直接输入编号）：\n")
	for i, m := range t.models {
		mark := "  "
		if i == highlightIdx {
			mark = "▶ "
		} else if m == t.model {
			mark = "  " // current but not highlighted
		}
		tag := ""
		if m == t.model {
			tag = "  (当前)"
		}
		b.WriteString(fmt.Sprintf("  %s%-3d %s%s\n", mark, i+1, m, tag))
	}
	if highlightIdx >= 0 && highlightIdx < len(t.models) {
		b.WriteString(fmt.Sprintf("\n  当前高亮：%s\n", t.models[highlightIdx]))
	}
	return b.String()
}

// modelPickerOverlay renders the interactive /model panel as a FIXED overlay
// (like helpBox / permLines). It is always fully on screen regardless of the
// conversation scroll position or the number of models. When the list is taller
// than the viewport, an internal top-index (modelPickerTop) scrolls the window
// so the highlighted row (modelPickerIdx) is always visible — this is what
// fixes the "highlight scrolls out of view" symptom that the old
// message-appended panel had.
func (t *TUI) modelPickerOverlay(W, bodyH int) []string {
	title := "选择模型（↑/↓ 移动，Enter 确认，Esc 取消；也可直接输入编号）："
	const titleRows = 1
	maxRows := bodyH - titleRows
	if maxRows < 1 {
		maxRows = 1
	}
	n := len(t.models)
	if n == 0 {
		return []string{title, t.paint("dim", "  （暂无可用模型）")}
	}
	// When the list is taller than the viewport we also need a scroll-hint
	// line, so reserve one row for it to keep the whole overlay within bodyH.
	if n > maxRows {
		maxRows = bodyH - titleRows - 1
		if maxRows < 1 {
			maxRows = 1
		}
	}
	// Keep modelPickerIdx inside the visible window [top, top+maxRows).
	if t.modelPickerTop < 0 {
		t.modelPickerTop = 0
	}
	if t.modelPickerIdx < t.modelPickerTop {
		t.modelPickerTop = t.modelPickerIdx
	}
	if t.modelPickerIdx >= t.modelPickerTop+maxRows {
		t.modelPickerTop = t.modelPickerIdx - maxRows + 1
	}
	if t.modelPickerTop > n-maxRows {
		t.modelPickerTop = n - maxRows
	}
	if t.modelPickerTop < 0 {
		t.modelPickerTop = 0
	}
	var lines []string
	lines = append(lines, t.paint("bold", title))
	for i := t.modelPickerTop; i < t.modelPickerTop+maxRows && i < n; i++ {
		num := fmt.Sprintf("%-3d", i+1)
		var row string
		if i == t.modelPickerIdx {
			row = "  " + t.paint("green", "▶ ") + " " + num + t.paint("green", t.models[i])
		} else {
			row = "  " + "  " + num + t.models[i]
		}
		if t.models[i] == t.model {
			row += t.paint("dim", "  (当前)")
		}
		lines = append(lines, row)
	}
	if n > maxRows {
		above := t.modelPickerTop
		below := n - (t.modelPickerTop + maxRows)
		lines = append(lines, t.paint("dim",
			fmt.Sprintf("  ↑ %d 更多  ·  ↓ %d 更多  (共 %d)", above, below, n)))
	}
	return lines
}

// openModelPicker enters selection mode. In raw mode it opens the interactive
// overlay; in line mode it prints a static list (no overlay available).
func (t *TUI) openModelPicker() {
	if len(t.models) == 0 {
		t.add(RoleSystem, "暂无可用模型列表。\n  请先配置 API Key：icode auth set --provider <provider> --key <YOUR_KEY>\n  或直接切换：/model <模型ID>（如 /model openrouter/free）")
		return
	}
	if !t.rawMode {
		t.add(RoleSystem, t.buildModelPickerList(-1))
		return
	}
	t.modelPickerOpen = true
	if t.modelIdx < 0 || t.modelIdx >= len(t.models) {
		t.modelIdx = 0
	}
	t.modelPickerIdx = t.modelIdx
	t.modelPickerTop = 0
	t.render()
}

// updateModelPicker refreshes the overlay after navigation.
func (t *TUI) updateModelPicker() {
	if t.modelPickerOpen && t.rawMode {
		t.render()
	}
}

// movePicker shifts the highlight by delta and refreshes the panel.
func (t *TUI) movePicker(delta int) {
	if len(t.models) == 0 {
		return
	}
	t.modelPickerIdx += delta
	if t.modelPickerIdx < 0 {
		t.modelPickerIdx = 0
	}
	if t.modelPickerIdx >= len(t.models) {
		t.modelPickerIdx = len(t.models) - 1
	}
	t.updateModelPicker()
}

// selectModelAt confirms the model at index i and closes the picker.
func (t *TUI) selectModelAt(i int) {
	if i < 0 || i >= len(t.models) {
		t.closeModelPicker()
		return
	}
	t.model = t.models[i]
	t.modelIdx = i
	t.closeModelPicker()
	t.add(RoleSystem, t.tstr("mode.set")+" -> "+t.model)
}

// closeModelPicker exits selection mode and removes the overlay.
func (t *TUI) closeModelPicker() {
	if !t.modelPickerOpen {
		return
	}
	t.modelPickerOpen = false
	t.modelPickerTop = 0
	if t.rawMode {
		t.render()
	}
}

// ── /resume interactive session picker ───────────────────────────

// openResumePicker shows the saved-session selector. Raw mode gets an
// interactive overlay (↑/↓ + Enter); line mode falls back to a numbered list.
func (t *TUI) openResumePicker() {
	if t.callback == nil {
		t.add(RoleSystem, "Usage: /resume <session-id> [--lite[=<n>] | --compact[=<n>]]")
		return
	}
	t.resumeSessions = t.callback.OnListSessionsStructured(20)
	if len(t.resumeSessions) == 0 {
		t.add(RoleSystem, "没有可恢复的历史会话。开始对话后会自动创建。")
		return
	}
	if !t.rawMode {
		var b strings.Builder
		b.WriteString("历史会话（/resume <session-id> 恢复，--compact 语义摘要压缩）:\n")
		for i, s := range t.resumeSessions {
			b.WriteString(fmt.Sprintf("  %-3d %s  %s  [%s]\n", i+1, s.ID, s.Title, s.Model))
		}
		t.add(RoleSystem, b.String())
		return
	}
	t.mu.Lock()
	t.resumePickerOpen = true
	t.resumePickerIdx = 0
	t.resumePickerTop = 0
	t.mu.Unlock()
	t.render()
}

// resumePickerOverlay renders the /resume selector as a fixed overlay.
func (t *TUI) resumePickerOverlay(W, bodyH int) []string {
	title := "选择要恢复的会话（↑/↓ 移动，Enter 恢复，Esc 取消）："
	const titleRows = 1
	maxRows := bodyH - titleRows
	if maxRows < 1 {
		maxRows = 1
	}
	n := len(t.resumeSessions)
	if n == 0 {
		return []string{title, t.paint("dim", "  （暂无历史会话）")}
	}
	if n > maxRows {
		maxRows = bodyH - titleRows - 1
		if maxRows < 1 {
			maxRows = 1
		}
	}
	if t.resumePickerIdx < t.resumePickerTop {
		t.resumePickerTop = t.resumePickerIdx
	}
	if t.resumePickerIdx >= t.resumePickerTop+maxRows {
		t.resumePickerTop = t.resumePickerIdx - maxRows + 1
	}
	if t.resumePickerTop > n-maxRows {
		t.resumePickerTop = n - maxRows
	}
	if t.resumePickerTop < 0 {
		t.resumePickerTop = 0
	}
	var lines []string
	lines = append(lines, t.paint("bold", title))
	for i := t.resumePickerTop; i < t.resumePickerTop+maxRows && i < n; i++ {
		s := t.resumeSessions[i]
		num := fmt.Sprintf("%-3d", i+1)
		title := truncate(s.Title, 40)
		meta := s.ID
		if len(meta) > 24 {
			meta = meta[:24] + "…"
		}
		row := "  " + num + title + t.paint("dim", "  "+meta+"  "+s.Updated+"  ["+s.Model+"]")
		if i == t.resumePickerIdx {
			row = "  " + t.paint("green", "▶ ") + num + t.paint("green", title) + t.paint("dim", "  "+meta+"  "+s.Updated+"  ["+s.Model+"]")
		}
		lines = append(lines, row)
	}
	if n > maxRows {
		above := t.resumePickerTop
		below := n - (t.resumePickerTop + maxRows)
		lines = append(lines, t.paint("dim",
			fmt.Sprintf("  ↑ %d 更多  ·  ↓ %d 更多  (共 %d)", above, below, n)))
	}
	return lines
}

// moveResumePicker shifts the /resume highlight.
func (t *TUI) moveResumePicker(delta int) {
	n := len(t.resumeSessions)
	if n == 0 {
		return
	}
	t.resumePickerIdx += delta
	if t.resumePickerIdx < 0 {
		t.resumePickerIdx = 0
	}
	if t.resumePickerIdx >= n {
		t.resumePickerIdx = n - 1
	}
	t.render()
}

// resumeSessionAt resumes the session at index i and closes the picker.
func (t *TUI) resumeSessionAt(i int) {
	if i < 0 || i >= len(t.resumeSessions) {
		t.closeResumePicker()
		return
	}
	id := t.resumeSessions[i].ID
	t.closeResumePicker()
	if t.callback != nil {
		msg := t.callback.OnResume(id)
		if msg != "" {
			t.add(RoleSystem, msg)
		}
	}
}

// closeResumePicker exits the /resume selector.
func (t *TUI) closeResumePicker() {
	if !t.resumePickerOpen {
		return
	}
	t.resumePickerOpen = false
	t.resumeSessions = nil
	t.resumePickerTop = 0
	if t.rawMode {
		t.render()
	}
}

func indexOfString(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func (t *TUI) stage() *searchreplace.StagingArea {
	var sid string
	if t.callback != nil {
		sid = t.callback.SessionID()
	}
	return searchreplace.StageForSession(sid)
}

func (t *TUI) applyStagedEdits() {
	edits := t.stage().List()
	if len(edits) == 0 {
		t.add(RoleSystem, "没有可应用的暂存编辑。")
		return
	}
	snapshottedFiles := make(map[string]bool)
	for _, ed := range edits {
		if ed.Valid && ed.FilePath != "" && !snapshottedFiles[ed.FilePath] {
			if checkpoint.DefaultUndo != nil {
				_, _ = checkpoint.DefaultUndo.SnapshotFile(context.Background(), ed.FilePath)
			}
			snapshottedFiles[ed.FilePath] = true
		}
	}
	results := t.stage().ApplyValid()
	for _, r := range results {
		t.add(RoleSystem, r)
	}
}

func (t *TUI) rejectStagedEdits() {
	stage := t.stage()
	n := stage.Count()
	if n == 0 {
		t.add(RoleSystem, "没有可丢弃的暂存编辑。")
		return
	}
	stage.Clear()
	t.add(RoleSystem, fmt.Sprintf("已丢弃 %d 条暂存编辑。", n))
}

// openDiffBox enters the staged-edits review overlay (Claude Code parity):
// it shows one unified diff per staged SEARCH/REPLACE edit, lets the user
// walk through them with ↑/↓, then Enter to apply all, Ctrl+Z to reject all,
// or Esc/any printable key to dismiss leaving the edits staged.
func (t *TUI) openDiffBox() {
	if !t.rawMode {
		t.add(RoleSystem, "staged edits: /review to inspect, /apply or /reject to act")
		return
	}
	edits := t.stage().List()
	if len(edits) == 0 {
		t.add(RoleSystem, "没有可审阅的暂存编辑。")
		return
	}
	t.mu.Lock()
	t.diffBoxOpen = true
	t.diffEdits = edits
	t.diffIdx = 0
	t.mu.Unlock()
	t.render()
}

// closeDiffBox dismisses the review overlay, leaving edits staged.
func (t *TUI) closeDiffBox() {
	t.mu.Lock()
	t.diffBoxOpen = false
	t.diffEdits = nil
	t.diffIdx = 0
	t.mu.Unlock()
	t.render()
}

// diffBoxOverlay renders the staged-edit review panel. Each row is a
// file-path header followed by its unified diff, indented and dimmed.
func (t *TUI) diffBoxOverlay(W, bodyH int) []string {
	title := "已暂存的修改（↑/↓ 查看，Enter 全部应用，Ctrl+Z 全部拒绝，Esc 关闭）："
	var lines []string
	lines = append(lines, t.paint("bold", title))
	lines = append(lines, t.hrule(W))
	n := len(t.diffEdits)
	if n == 0 {
		lines = append(lines, t.paint("dim", "  （无暂存修改）"))
		return lines
	}
	// Walk down each edit; the highlighted one (diffIdx) is marked with ▶.
	for i, ed := range t.diffEdits {
		if len(lines) >= bodyH-1 {
			break
		}
		marker := "    "
		if i == t.diffIdx {
			marker = t.paint("green", "▶ ")
		}
		fileRow := marker + t.paint("yellow", ed.FilePath)
		if !ed.Valid {
			fileRow += t.paint("red", "  (无效: "+ed.Reason+")")
		}
		lines = append(lines, fileRow)
		diff := ed.Diff
		if diff == "" {
			lines = append(lines, t.paint("dim", "  （无差异预览）"))
			continue
		}
		for _, dl := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
			if len(lines) >= bodyH-1 {
				break
			}
			col := t.diffColor(dl)
			lines = append(lines, t.paint(col, "    "+dl))
		}
	}
	return lines
}

// diffColor picks the ANSI colour for one unified-diff line.
func (t *TUI) diffColor(line string) string {
	switch {
	case strings.HasPrefix(line, "+"):
		return "green"
	case strings.HasPrefix(line, "-"):
		return "red"
	case strings.HasPrefix(line, "@@"):
		return "cyan"
	default:
		return "dim"
	}
}

// moveDiff shifts the review highlight and refreshes the panel.
func (t *TUI) moveDiff(delta int) {
	t.mu.Lock()
	t.diffIdx += delta
	if t.diffIdx < 0 {
		t.diffIdx = 0
	}
	if t.diffIdx >= len(t.diffEdits) {
		t.diffIdx = len(t.diffEdits) - 1
	}
	t.mu.Unlock()
	if t.diffBoxOpen && t.rawMode {
		t.render()
	}
}

func (t *TUI) copyLastAssistant(args []string) {
	t.mu.Lock()
	var content string
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
			count := 0
			for i := len(t.messages) - 1; i >= 0; i-- {
				if t.messages[i].Role == RoleAssistant {
					count++
					if count == n {
						content = t.messages[i].Content
						break
					}
				}
			}
		} else {
			t.mu.Unlock()
			t.add(RoleSystem, "用法: /copy [N]  — 复制倒数第 N 条助手回复（默认 1）")
			return
		}
	} else {
		for i := len(t.messages) - 1; i >= 0; i-- {
			if t.messages[i].Role == RoleAssistant {
				content = t.messages[i].Content
				break
			}
		}
	}
	t.mu.Unlock()
	if content == "" {
		t.add(RoleSystem, "没有助手回复可复制。")
		return
	}
	if err := writeClipboard(content); err != nil {
		t.add(RoleError, "复制到剪贴板失败: "+err.Error())
		return
	}
	t.add(RoleSystem, "✓ 已复制最近一条助手回复到剪贴板。")
}

func writeClipboard(text string) error {
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("clip")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	case "darwin":
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	default:
		cmd := exec.Command("xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}
}

// copyLastReply copies the most recent assistant message to the system
// clipboard, triggered by Ctrl+Y.
func (t *TUI) copyLastReply() {
	t.mu.Lock()
	var content string
	for i := len(t.messages) - 1; i >= 0; i-- {
		if t.messages[i].Role == RoleAssistant {
			content = t.messages[i].Content
			break
		}
	}
	t.mu.Unlock()
	if content == "" {
		t.add(RoleSystem, "没有助手回复可复制。")
		return
	}
	if err := writeClipboard(content); err != nil {
		t.add(RoleError, "复制到剪贴板失败: "+err.Error())
		return
	}
	t.add(RoleSystem, "✓ 已复制最近一条助手回复到剪贴板 (Ctrl+Y)。")
}

// changeDir implements /cd: moves the TUI's working directory and refreshes
// the explorer pane. Mirrors slashui.cmdCD so both ends behave identically.
func (t *TUI) changeDir(args []string) {
	cwd, _ := os.Getwd()
	if len(args) == 0 {
		t.add(RoleSystem, "当前工作目录: "+cwd+"\n用法: /cd <path> — 移动会话工作目录（相对路径基于当前目录解析）")
		return
	}
	target := args[0]
	if target == "~" || target == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			target = home
		}
	} else if strings.HasPrefix(target, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			target = filepath.Join(home, strings.TrimPrefix(target, "~/"))
		}
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		t.add(RoleError, "解析路径失败: "+err.Error())
		return
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		t.add(RoleError, "目录不存在或不是文件夹: "+abs)
		return
	}
	if err := os.Chdir(abs); err != nil {
		t.add(RoleError, "切换工作目录失败: "+err.Error())
		return
	}
	if ncwd, err := os.Getwd(); err == nil {
		abs = ncwd
	}
	// Persist the directory so the CLI/TUI/server start there next launch.
	persistSetting(func(c *config.Config) { c.Defaults.WorkingDir = abs })
	// Refresh the explorer pane listing and the prompt-line dir badge.
	t.mu.Lock()
	t.dirEntries = listCwd()
	t.mu.Unlock()
	// Re-notify the engine so tools (bash, file read/write) resolve relative
	// paths from the new directory on the next turn.
	t.notice("工作目录已切换到: " + abs)
	t.add(RoleSystem, "✓ 工作目录已切换到: "+abs)
}

// renameSession implements /rename: retitles the active session via the
// callback (backend store) so the sidebar / resume list reflect it.
func (t *TUI) renameSession(args []string) {
	if len(args) == 0 {
		t.add(RoleSystem, "用法: /rename <新标题> — 重命名当前会话")
		return
	}
	title := strings.Join(args, " ")
	if t.callback != nil {
		msg := t.callback.OnRenameSession(title)
		if msg != "" {
			t.add(RoleError, msg)
			return
		}
		t.add(RoleSystem, "✓ 会话已重命名为: "+title)
		return
	}
	t.add(RoleSystem, "引擎未初始化，无法重命名。")
}

// persistSetting loads the user config, applies fn, and saves it back to disk.
// Best-effort: failures are ignored (logged by config.Save on error).
func persistSetting(fn func(*config.Config)) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	fn(cfg)
	_ = cfg.Save(config.DefaultPath())
}

// agentsCommand renders the live agent panel: registered sub-agents with
// their capability flags, teams, and background sub-agent runs (elapsed at
// render time). Claude Code "claude agents" parity.
func (t *TUI) agentsCommand() {
	var b strings.Builder
	b.WriteString("子 agent 注册表:\n")
	reg := agent.Load(agent.AgentDefaultDirs()...)
	reg.RegisterDefaults()
	list := reg.List()
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
			flags = "  [" + strings.TrimSpace(flags) + "]"
		}
		fmt.Fprintf(&b, "  %s — %s%s\n", d.Name, d.Description, t.paint("dim", flags))
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
	t.add(RoleSystem, b.String())
}
