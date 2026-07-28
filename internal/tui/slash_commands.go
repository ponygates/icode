package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	projectcontext "github.com/ponygates/icode/internal/core/context"
	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/skills"
	"github.com/ponygates/icode/internal/core/slashcmd"
	"github.com/ponygates/icode/internal/executil"
)
// ── Slash commands ───────────────────────────────────────────────

func (t *TUI) handleSlash(text string) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "/help":
		var b strings.Builder
		b.WriteString(t.tstr("cmd.help") + ":\n")
		for _, d := range slashDefs {
			b.WriteString(fmt.Sprintf("  %-14s %s\n", d.Name, t.tstr(d.Key)))
		}

		// Append user-defined slash commands (.icode/commands/*.md) so
		// `/help` reflects everything the current session will accept.
		if custom := slashcmd.Load(slashcmd.DefaultDirs()...).List(); len(custom) > 0 {
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
		b.WriteString("  " + t.tstr("cmd.ac"))
		t.add(RoleSystem, b.String())

	case "/exit", "/quit":
		t.add(RoleSystem, "Goodbye!")
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
			t.model = args[0]
		}
		t.modelIdx = indexOfString(t.models, t.model)
		t.notice("Model -> " + t.model)
		t.add(RoleSystem, t.tstr("mode.set")+" -> "+t.model)

	case "/mode":
		if len(args) > 0 {
			t.mode = args[0]
			t.add(RoleSystem, "Mode -> "+args[0])
		}

	case "/session", "/sessions":
		if t.callback != nil {
			t.add(RoleSystem, t.callback.OnListSessions())
		}

	case "/resume":
		if len(args) > 0 && t.callback != nil {
			t.add(RoleSystem, t.callback.OnResume(args[0]))
		} else {
			t.add(RoleSystem, "Usage: /resume <session-id>")
		}

	case "/clear":
		t.mu.Lock()
		t.messages = nil
		t.promptTokens = 0
		t.completionTokens = 0
		t.cost = ""
		t.cacheHitRate = 0
		t.mu.Unlock()
		t.add(RoleSystem, "Conversation cleared.")

	case "/compact":
		t.compact()

	case "/export":
		t.exportMarkdown(args)

	case "/diff":
		t.showGitDiff()

	case "/rewind":
		n := 1
		if len(args) > 0 {
			fmt.Sscanf(args[0], "%d", &n)
		}
		if n <= 0 {
			t.add(RoleSystem, "用法: /rewind [N]  — 回滚前 N 步工具调用")
			break
		}
		sessionID := ""
		if t.callback != nil {
			sessionID = t.callback.SessionID()
		}
		if sessionID == "" {
			t.add(RoleSystem, "没有活跃会话。")
			break
		}
		store, err := checkpoint.GetOrOpen(sessionID)
		if err != nil {
			t.add(RoleError, "打开检查点失败: "+err.Error())
			break
		}
		files, err := store.Rewind(context.Background(), n)
		if err != nil {
			t.add(RoleError, "回滚失败: "+err.Error())
			break
		}
		msg := fmt.Sprintf("⏪ 已回滚 %d 步。影响文件:\n", n)
		for _, f := range files {
			msg += "  " + f + "\n"
		}
		t.add(RoleSystem, msg)

	case "/todo":
		var b strings.Builder
		sessionID := ""
		if t.callback != nil {
			if p, a, d, total := t.callback.TodoCounts(); total > 0 {
				fmt.Fprintf(&b, "📋 待办 (%d 待处理 · %d 进行中 · %d 完成)\n\n", p, a, d)
				b.WriteString("（详情可通过 `todo_write` 工具查看 — 当前只在状态栏显示计数）\n")
			} else {
				b.WriteString("当前会话没有待办事项。让模型执行任务时会自动创建。\n")
			}
		}
		_ = sessionID
		t.add(RoleSystem, b.String())

	case "/init":
		cwd, err := os.Getwd()
		if err != nil { t.add(RoleError, err.Error()); break }
		if _, err := os.Stat(filepath.Join(cwd, "ICODE.md")); err == nil {
			t.add(RoleSystem, "ICODE.md 已存在")
			break
		}
		_ = os.WriteFile(filepath.Join(cwd, "ICODE.md"), []byte("# Project Context\n\nEdit this file.\n"), 0o644)
		t.add(RoleSystem, "[x] ICODE.md 已生成")

	case "/agents":
		v := agent.Load(agent.AgentDefaultDirs()...)
		v.RegisterDefaults()
		var a strings.Builder
		a.WriteString("子 agent:\n")
		for _, d := range v.List() { a.WriteString(fmt.Sprintf("  %s — %s\n", d.Name, d.Description)) }
		t.add(RoleSystem, a.String())

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
		cfg, err := config.Load()
		if err != nil { t.add(RoleSystem, err.Error()); break }
		var a strings.Builder
		a.WriteString("MCP 服务器:\n")
		for _, s := range cfg.MCP { a.WriteString(fmt.Sprintf("  %s: %s\n", s.Name, s.Command)) }
		if a.Len() < 12 { a.WriteString("  未配置\n在 ~/.icode/mcp.json 中添加。\n") }
		t.add(RoleSystem, a.String())

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

	case "/cost":
		info := fmt.Sprintf("Tokens: %d prompt + %d completion = %d total",
			t.promptTokens, t.completionTokens, t.promptTokens+t.completionTokens)
		if t.cost != "" {
			info += " · Cost: " + t.cost
		}
		if t.cacheHitRate > 0 {
			info += fmt.Sprintf(" · Cache: %.0f%%", t.cacheHitRate*100)
		}
		t.add(RoleSystem, info)

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
		cfg, err := config.Load()
		if err != nil || len(cfg.Models) == 0 {
			t.add(RoleSystem, "暂无自定义模型。\n用 `icode config model add <provider> <model_id> [name]` 新增。")
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

	case "/config":
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
		t.add(RoleSystem, fmt.Sprintf("当前设置：\n  Model:     %s\n  Provider:  %s\n  Mode:      %s\n  Language:  %s\n  Theme:     %s\n  Diff:      %s\n  Security:  %s\n  Syntax:    %s\n\n用 `icode config <key> <value>` 修改，或 `/lang` `/theme`  `/security` 即时切换。",
			t.model, t.provider, t.mode, lang, theme, diff,
			permission.SecurityLabel(config.SecurityLevel(t.securityLevel)), syntax))

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
				t.persistSetting(func(c *config.Config) { c.Language = t.lang })
				t.add(RoleSystem, fmt.Sprintf(t.tstr("lang.set"), t.lang))
			default:
				t.add(RoleSystem, t.tstr("lang.usage"))
			}
		} else {
			t.add(RoleSystem, t.tstr("lang.usage"))
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

	case "/review":
		t.add(RoleSystem, "🔍 审查模式已启用。请描述你想审查的代码或文件路径，我将分析代码质量、安全性和潜在问题。")

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
		t.add(RoleSystem, fmt.Sprintf("当前权限/安全等级: %s",
			permission.SecurityLabel(config.SecurityLevel(t.securityLevel))))

	case "/verbose":
		t.verbose = !t.verbose
		if t.verbose {
			t.add(RoleSystem, "[x] 详细输出已开启（完整工具参数 / 原始 diff）")
		} else {
			t.add(RoleSystem, "[ ] 详细输出已关闭")
		}

	case "/memory":
		proj := t.projectMemoryPath()
		user, _ := projectcontext.UserMemoryPath()
		t.add(RoleSystem, fmt.Sprintf("记忆文件:\n  项目级: %s\n  用户级: %s", proj, user))

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
	reg := slashcmd.Load(slashcmd.DefaultDirs()...)
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

func (t *TUI) compact() {
	t.mu.Lock()
	if len(t.messages) < 4 {
		t.messages = append(t.messages, Message{Role: RoleSystem, Content: "Not enough messages to compact."})
		t.mu.Unlock()
		t.render()
		return
	}
	var keep []Message
	var summary strings.Builder
	summary.WriteString("[Compacted] Summary of earlier turns:\n")
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
	if err := os.WriteFile(filename, []byte(sb.String()), 0644); err != nil {
		t.add(RoleError, "Export failed: "+err.Error())
		return
	}
	t.add(RoleSystem, fmt.Sprintf("Exported to %s (%d messages)", filename, len(msgs)))
}

// ── Git diff ─────────────────────────────────────────────────────

func (t *TUI) showGitDiff() {
	cmd := executil.Command("git", "diff")
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		t.add(RoleError, "git diff: "+err.Error())
		return
	}
	if len(output) == 0 {
		t.add(RoleSystem, "No unstaged changes.")
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

// buildModelPicker renders the interactive /model panel. The row at
// t.modelPickerIdx is marked with ▶; a hint line explains the keys.
func (t *TUI) buildModelPicker() string {
	var b strings.Builder
	b.WriteString("选择模型（↑/↓ 移动，Enter 确认，Esc 取消；也可直接输入编号）：\n")
	for i, m := range t.models {
		mark := "  "
		if i == t.modelPickerIdx {
			mark = "▶ "
		} else if m == t.model {
			mark = "  " // current but not highlighted
		}
		b.WriteString(fmt.Sprintf("  %s%-3d %s\n", mark, i+1, m))
	}
	if t.modelPickerIdx >= 0 && t.modelPickerIdx < len(t.models) {
		b.WriteString(fmt.Sprintf("\n  当前高亮：%s\n", t.models[t.modelPickerIdx]))
	}
	return b.String()
}

// openModelPicker appends a live picker panel and enters selection mode.
func (t *TUI) openModelPicker() {
	if len(t.models) == 0 {
		t.add(RoleSystem, "暂无可用模型列表。\n  请先配置 API Key：icode auth set --provider <provider> --key <YOUR_KEY>\n  或直接切换：/model <模型ID>（如 /model openrouter/free）")
		return
	}
	t.modelPickerOpen = true
	if t.modelIdx < 0 || t.modelIdx >= len(t.models) {
		t.modelIdx = 0
	}
	t.modelPickerIdx = t.modelIdx
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleSystem, Content: t.buildModelPicker()})
	t.modelPickerMsgIdx = len(t.messages) - 1
	t.mu.Unlock()
	if t.rawMode {
		t.render()
	}
}

// updateModelPicker rewrites the live picker panel in place (navigation).
func (t *TUI) updateModelPicker() {
	if t.modelPickerMsgIdx < 0 {
		return
	}
	content := t.buildModelPicker()
	t.mu.Lock()
	if t.modelPickerMsgIdx < len(t.messages) {
		t.messages[t.modelPickerMsgIdx] = Message{Role: RoleSystem, Content: content}
	}
	t.mu.Unlock()
	if t.rawMode {
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

// closeModelPicker exits selection mode and removes the live panel message.
func (t *TUI) closeModelPicker() {
	wasOpen := t.modelPickerOpen
	t.modelPickerOpen = false
	if t.modelPickerMsgIdx >= 0 {
		t.mu.Lock()
		if t.modelPickerMsgIdx < len(t.messages) {
			t.messages = append(t.messages[:t.modelPickerMsgIdx], t.messages[t.modelPickerMsgIdx+1:]...)
		}
		t.mu.Unlock()
		t.modelPickerMsgIdx = -1
	}
	if wasOpen && t.rawMode {
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
