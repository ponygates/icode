package slashui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/checkpoint"
	projectcontext "github.com/ponygates/icode/internal/core/context"
	"github.com/ponygates/icode/internal/core/conversation"
	"github.com/ponygates/icode/internal/core/knowledge"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/plugins"
	"github.com/ponygates/icode/internal/core/prefmem"
	"github.com/ponygates/icode/internal/core/searchreplace"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/core/skills"
	"github.com/ponygates/icode/internal/core/slashcmd"
	"github.com/ponygates/icode/internal/core/todo"
	"github.com/ponygates/icode/internal/executil"
	"github.com/ponygates/icode/internal/scheduler"
	"github.com/ponygates/icode/internal/types"
	"github.com/ponygates/icode/pkg/modelupdate"
)

// Backend exposes the pieces of the iCode backend that slash commands need.
// Both the in-process simple UI and the HTTP server build this from their own
// app/server state, so the command set behaves identically everywhere.
type Backend struct {
	Engine        *conversation.Engine
	SessStore     types.SessionStore
	Gate          *permission.Gate
	RefreshModels func(ctx context.Context) ([]modelupdate.ProviderUpdate, error)
	// Scheduler is optional — when set, /idle can create off-peak tasks.
	Scheduler *scheduler.Scheduler
	// RegisterCustomModel persists + live-registers a user-defined model
	// (/models add). It returns an error string, or "" on success. Optional —
	// when nil, /models add reports "not available".
	RegisterCustomModel func(provider, modelID, name string) string
	// RemoveCustomModel removes a user-defined model (/models rm). Optional.
	RemoveCustomModel func(id string) string
}

// State carries the caller's runtime state for the current turn. The caller
// owns these values and adopts any updates returned in Result.
type State struct {
	SessionID string
	Model     string
	Provider  string
	Mode      string
	Security  string
	CWD       string
	Version   string

	// NoPersistCWD, when true, makes /cd skip writing the new working
	// directory to the user config (used by tests and headless runners).
	NoPersistCWD bool
}

// Result describes the outcome of a slash command. When Chat is true the
// caller should feed Content to the engine as a normal user turn.
type Result struct {
	Output  string `json:"output"`
	IsError bool   `json:"is_error,omitempty"`

	// Chat=true → send Content to the engine as a user message.
	Chat    bool   `json:"chat,omitempty"`
	Content string `json:"content,omitempty"`

	// State updates the caller should adopt.
	Model        string `json:"model,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Mode         string `json:"mode,omitempty"`
	Security     string `json:"security,omitempty"`
	ClearSession bool   `json:"clear_session,omitempty"`
	NewSession   bool   `json:"new_session,omitempty"`

	// CWD is the resolved working directory after /cd. The caller should
	// os.Chdir(res.CWD) so every later tool (bash, file read/write) runs
	// relative to the new directory, then persist the change for new turns.
	CWD string `json:"cwd,omitempty"`

	// SessionID tells the caller which session became active (e.g. after
	// /resume or /fork) so it can reload and display that session's messages.
	SessionID string `json:"session_id,omitempty"`
}

func ok(msg string) Result { return Result{Output: msg} }
func errf(f string, a ...any) Result {
	return Result{Output: fmt.Sprintf(f, a...), IsError: true}
}
func chat(content string) Result { return Result{Output: content, Chat: true, Content: content} }

// Execute dispatches a slash command to the shared implementation. It returns
// a Result that the caller renders (as a system message and/or a model turn).
func Execute(ctx context.Context, b *Backend, st *State, text string) Result {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return ok("")
	}
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "/help":
		return cmdHelp(args)
	case "/whoami":
		return cmdWhoami(st)
	case "/status":
		return cmdStatus(st)
	case "/config":
		return cmdConfig(args)
	case "/models":
		return cmdModels(b, args)
	case "/model":
		return cmdModel(b, st, args)
	case "/provider":
		return cmdProvider(st, args)
	case "/mode":
		return cmdMode(b, st, args)
	case "/thinking":
		return cmdThinking(b, args)
	case "/copy":
		return cmdCopy(b, st, args)
	case "/session", "/sessions":
		return cmdSessions(b, st, args)
	case "/resume":
		return cmdResume(b, st, args)
	case "/fork":
		return cmdFork(b, st, args)
	case "/branch":
		return cmdFork(b, st, args)
	case "/rename":
		return cmdRename(b, st, args)
	case "/cd", "/cds":
		return cmdCD(st, args)
	case "/goal":
		return cmdGoal(b, st, args)
	case "/budget":
		return cmdBudget(b, st, args)
	case "/new", "/newsession":
		archiveSession(b, st)
		return ok("新会话已创建。").withNewSession()
	case "/search":
		return cmdSearch(b, args)
	case "/clear", "/wipe":
		return cmdClear(b, st)
	case "/restore":
		return cmdRestore(b, args)
	case "/undo", "/rewind", "/checkpoint":
		return cmdUndo(st, args)
	case "/diff":
		return cmdDiff(args)
	case "/lsp":
		return cmdLsp(b, st, args)
	case "/kb", "/knowledge":
		return cmdKnowledge(b, args)
	case "/idle":
		return cmdIdle(b, args)
	case "/export":
		return cmdExport(b, st, args)
	case "/share":
		return cmdShare(b, st, args)
	case "/review":
		return cmdReview(args)
	case "/apply":
		return cmdApply(st)
	case "/reject":
		return cmdReject(st)
	case "/keys":
		return cmdKeys()
	case "/mcp":
		return cmdMCP(args)
	case "/memory":
		return cmdMemory(b, args)
	case "/hooks":
		return cmdHooks()
	case "/init":
		return cmdInit()
	case "/agents":
		return cmdAgents()
	case "/skills":
		return cmdSkills()
	case "/skill-eval":
		return cmdSkillEval(args)
	case "/plugin":
		return cmdPlugin(args)
	case "/teams":
		return cmdTeams()
	case "/todo":
		return cmdTodo(st)
	case "/token", "/cost", "/usage", "/stats":
		return cmdToken(b, st)
	case "/plan", "/ask", "/debug":
		return cmdModeShortcut(b, st, cmd)
	case "/context":
		return cmdContext(b, st)
	case "/summarize":
		return cmdSummarize(b, st)
	case "/compact":
		return cmdCompact(b, st, args)
	case "/output-style":
		return cmdOutputStyle(b, args)
	case "/security":
		return cmdSecurity(b, st, args)
	case "/permissions":
		return cmdPermissions(b, st)
	case "/add-dir":
		return cmdAddDir(b, args)
	case "/update":
		return cmdUpdate(b)
	case "/doctor":
		return cmdDoctor(b, st)
	case "/lang":
		return cmdLang(args, b)
	case "/theme":
		return ok("该命令是 CLI 终端专用（主题切换）。请使用应用内的主题/深浅色设置。")
	case "/login":
		return ok("配置 API 凭证:\n  · 桌面端/UI 版: 设置 → 提供商 → 填写 API Key\n  · CLI: icode config key <provider> <apikey>\n配置完成后用 /doctor 验证连通性。")
	case "/logout":
		return ok("清除 API 凭证:\n  · 桌面端/UI 版: 设置 → 提供商 → 删除 Key\n  · CLI: icode config key <provider> \"\"\n（CLI 不存储本地明文令牌，仅配置文件中保存 Key）")
	case "/release-notes", "/changelog":
		data, err := os.ReadFile("CHANGELOG.md")
		if err != nil {
			return ok("版本更新日志: https://github.com/ponygates/icode/releases\n（当前项目目录下未找到 CHANGELOG.md）")
		}
		lines := strings.Split(string(data), "\n")
		limit := 40
		if len(lines) < limit {
			limit = len(lines)
		}
		return ok("最近的更新日志:\n" + strings.Join(lines[:limit], "\n") + "\n\n完整日志: https://github.com/ponygates/icode/releases")
	case "/bug", "/feedback":
		return ok("反馈问题:\n  · GitHub Issues: https://github.com/ponygates/icode/issues\n  · 或对话中 `# <内容>` 写入记忆文件")
	case "/pr", "/pr_comments":
		return ok("PR 评论功能需配合 GitHub 相关工具使用；当前桌面端/UI 版未内置该命令。")
	case "/exit", "/quit":
		return ok("该命令是 CLI 终端专用。应用内请使用窗口关闭按钮退出。")
	case "/vim", "/statusline", "/verbose", "/welcome", "/expand", "/multiline", "/history":
		return ok("该命令是 CLI 终端专用（" + cmd + "）。应用内无需该选项。")
	case "/admin":
		return cmdAdmin(b, args)
	default:
		// User-defined slash commands (.icode/commands/*.md)
		if custom := tryCustomSlash(parts); custom.Ok {
			return custom.Result
		}
		return errf("未知命令: %s（输入 /help 查看可用命令）", cmd)
	}
}

type customOutcome struct {
	Ok     bool
	Result Result
}

func tryCustomSlash(parts []string) customOutcome {
	if len(parts) == 0 {
		return customOutcome{}
	}
	reg := slashcmd.CachedLoad(slashcmd.DefaultDirs()...)
	c, found := reg.Get(parts[0])
	if !found {
		return customOutcome{}
	}
	args := ""
	if len(parts) > 1 {
		args = strings.Join(parts[1:], " ")
	}
	expanded, err := c.Expand(context.Background(), args)
	if err != nil {
		return customOutcome{Ok: true, Result: errf("展开 %s 失败: %v", parts[0], err)}
	}
	if strings.TrimSpace(expanded) == "" {
		return customOutcome{Ok: true, Result: ok(fmt.Sprintf("命令 %s 展开为空", parts[0]))}
	}
	return customOutcome{Ok: true, Result: chat(fmt.Sprintf("(%s) %s", parts[0], args) + "\n\n" + expanded)}
}

// ── Command implementations ───────────────────────────────────────

func cmdHelp(_ []string) Result {
	var b strings.Builder
	b.WriteString("可用命令:\n")
	for _, d := range helpDefs() {
		b.WriteString(fmt.Sprintf("  %-18s %s\n", d.Name, d.Hint))
	}
	if custom := slashcmd.Load(slashcmd.DefaultDirs()...).List(); len(custom) > 0 {
		b.WriteString("\n自定义命令 (.icode/commands/*.md):\n")
		for _, c := range custom {
			usage := c.Name
			if c.ArgumentHint != "" {
				usage += " " + c.ArgumentHint
			}
			b.WriteString(fmt.Sprintf("  %-18s %s\n", usage, c.Description))
		}
	}
	b.WriteString("\n特殊语法:\n")
	b.WriteString("  # <内容>   追加到项目记忆 (ICODE.md)\n")
	b.WriteString("  ! <命令>   执行 shell 命令\n")
	return ok(b.String())
}

type helpItem struct{ Name, Hint string }

func helpDefs() []helpItem {
	return []helpItem{
		{"/cd <path>", "移动会话工作目录"}, {"/model [id]", "切换模型"}, {"/provider [名]", "切换提供商"},
		{"/mode [agent|plan|yolo|auto|ask]", "切换模式"}, {"/plan|/ask|/debug", "/mode 快捷方式"}, {"/models", "列出自定义模型"},
		{"/session", "显示当前会话"}, {"/sessions", "列出已保存会话"},
		{"/resume <id>", "载入历史会话"}, {"/fork <id>[@n]", "从历史会话分支出独立会话"}, {"/rename <标题>", "重命名当前会话"}, {"/goal [set|show|clear]", "长目标模式"}, {"/budget [set|show|clear]", "Token 预算护栏"}, {"/new", "开启新会话"},
		{"/clear", "清空当前会话"}, {"/wipe", "清空会话上下文"},
		{"/undo [N]", "回滚 N 步文件更改"}, {"/rewind [N]", "同上（检查点回滚）"},
		{"/diff", "显示 git 工作区差异"}, {"/review [file]", "审查 diff 或指定文件"},
		{"/apply", "应用已暂存编辑"}, {"/reject", "丢弃已暂存编辑"},
		{"/search <query>", "搜索历史会话"}, {"/export [file]", "导出会话为 Markdown"},
		{"/share", "导出会话为带时间戳 Markdown"}, {"/copy [file]", "复制最后输出到剪贴板"},
		{"/token", "Token 节省报告"}, {"/usage", "同上（费用别名）"}, {"/cost", "本次会话费用"}, {"/context", "上下文用量"},
		{"/summarize", "会话摘要"}, {"/compact", "压缩会话（自动五层压缩）"},
		{"/status", "系统状态"}, {"/whoami", "显示当前配置"},
		{"/doctor", "运行诊断"}, {"/keys", "API Key 状态"},
		{"/config", "查看配置"}, {"/output-style [style]", "设置输出风格"},
		{"/security [level]", "设置安全等级"}, {"/permissions", "查看权限/安全配置"},
		{"/mcp", "管理 MCP 服务器"}, {"/memory", "查看记忆文件"},
		{"/hooks", "查看/生成 hooks.yaml"}, {"/init", "创建 ICODE.md"},
		{"/agents", "列出 agent"}, {"/skills", "列出已安装技能"},
		{"/skill-eval [名称] [--scaffold]", "技能触发自测"},
		{"/plugin [list|install|remove]", "插件管理"},
		{"/teams", "列出团队"}, {"/todo", "查看任务"},
		{"/tasks", "后台任务面板（子代理 + 命令）"},
		{"/add-dir <dir>", "添加工作目录"}, {"/update", "刷新模型目录"},
		{"/lang [zh-CN|zh-TW|en]", "切换语言"}, {"/login", "配置 API Key"},
		{"/logout", "清除 API Key"}, {"/feedback", "反馈问题"},
		{"/help", "显示帮助"},
	}
}

func (r Result) withNewSession() Result {
	r.NewSession = true
	r.ClearSession = true
	return r
}

func cmdWhoami(st *State) Result {
	cwd := st.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	sec := st.Security
	if sec == "" {
		if c, err := config.Load(); err == nil && c.SecurityLevel != "" {
			sec = string(c.SecurityLevel)
		} else {
			sec = "local"
		}
	}
	return ok(fmt.Sprintf("iCode %s\n  Model:     %s\n  Provider:  %s\n  Security:  %s\n  CWD:       %s",
		shortStr(st.Version, "dev"), shortStr(st.Model, "未设置"), shortStr(st.Provider, "未设置"),
		permission.SecurityLabel(config.SecurityLevel(sec)), shortDir(cwd)))
}

func cmdStatus(st *State) Result {
	var b strings.Builder
	b.WriteString("iCode 系统状态\n\n")
	cfg, _ := config.Load()
	if cfg != nil {
		b.WriteString(fmt.Sprintf("语言: %s\n", cfg.Language))
		b.WriteString(fmt.Sprintf("默认模型: %s\n", cfg.Defaults.Model))
		b.WriteString(fmt.Sprintf("默认 Provider: %s\n", cfg.Defaults.Provider))
		b.WriteString(fmt.Sprintf("权限模式: %s\n\n", cfg.Defaults.Mode))
		b.WriteString("API Key 状态:\n")
		for name, pc := range cfg.Providers {
			status := "✓ 已配置"
			if pc.APIKey == "" {
				status = "✗ 未配置"
			}
			key := pc.APIKey
			if len(key) > 8 {
				key = key[:4] + "..." + key[len(key)-4:]
			} else if key != "" {
				key = "****"
			}
			b.WriteString(fmt.Sprintf("  %-14s %-10s %s\n", name, status, key))
		}
	}
	b.WriteString("\n活跃会话: ")
	if st.SessionID != "" {
		sid := st.SessionID
		if len(sid) > 8 {
			sid = sid[:8] + "..."
		}
		b.WriteString(sid)
	} else {
		b.WriteString("无")
	}
	return ok(b.String())
}

func cmdConfig(args []string) Result {
	if len(args) >= 2 && strings.ToLower(args[0]) == "set" {
		return cmdConfigSet(args[1:])
	}
	cfg, err := config.LoadOrCreate()
	if err != nil {
		return errf("读取配置失败: %v", err)
	}
	var b strings.Builder
	b.WriteString("iCode 配置\n\n")
	b.WriteString(fmt.Sprintf("语言:          %s\n", cfg.Language))
	b.WriteString(fmt.Sprintf("默认模型:      %s\n", cfg.Defaults.Model))
	b.WriteString(fmt.Sprintf("默认 Provider: %s\n", cfg.Defaults.Provider))
	b.WriteString(fmt.Sprintf("权限模式:      %s\n", cfg.Defaults.Mode))
	b.WriteString(fmt.Sprintf("安全等级:      %s\n", cfg.SecurityLevel))
	if cfg.Defaults.OutputStyle != "" {
		b.WriteString(fmt.Sprintf("输出风格:      %s\n", cfg.Defaults.OutputStyle))
	}
	if len(cfg.Defaults.ExtraDirs) > 0 {
		b.WriteString(fmt.Sprintf("额外工作目录:  %s\n", strings.Join(cfg.Defaults.ExtraDirs, ", ")))
	}
	if len(cfg.Providers) > 0 {
		b.WriteString("\n提供商:\n")
		for name, pc := range cfg.Providers {
			key := ""
			if pc.APIKey != "" {
				key = "（已配置 Key）"
			}
			b.WriteString(fmt.Sprintf("  %s %s\n", name, key))
		}
	}
	b.WriteString("\n用法: /config set <key> <value>")
	b.WriteString("\n可设置: model, provider, mode, security, lang, output-style, theme")
	b.WriteString("\n配置文件: " + config.DefaultPath())
	return ok(b.String())
}

func cmdConfigSet(args []string) Result {
	if len(args) < 2 {
		return errf("用法: /config set <key> <value>")
	}
	key := strings.ToLower(args[0])
	value := strings.Join(args[1:], " ")
	cfg, err := config.LoadOrCreate()
	if err != nil {
		return errf("读取配置失败: %v", err)
	}
	switch key {
	case "model":
		cfg.Defaults.Model = value
	case "provider":
		cfg.Defaults.Provider = value
	case "mode":
		valid := map[string]bool{"plan": true, "agent": true, "ask": true, "auto": true, "yolo": true}
		if !valid[strings.ToLower(value)] {
			return errf("无效模式: %s（可选 plan/agent/ask/auto/yolo）", value)
		}
		cfg.Defaults.Mode = strings.ToLower(value)
	case "security":
		cfg.SecurityLevel = config.SecurityLevel(value)
	case "lang", "language":
		valid := map[string]bool{"zh-CN": true, "zh-TW": true, "en": true}
		if !valid[value] {
			return errf("无效语言: %s（可选 zh-CN/zh-TW/en）", value)
		}
		cfg.Language = value
	case "output-style", "outputstyle":
		valid := map[string]bool{"concise": true, "normal": true, "verbose": true}
		if !valid[value] {
			return errf("无效风格: %s（可选 concise/normal/verbose）", value)
		}
		cfg.Defaults.OutputStyle = value
	case "theme":
		valid := map[string]bool{"auto": true, "dark": true, "light": true}
		if !valid[value] {
			return errf("无效主题: %s（可选 auto/dark/light）", value)
		}
		cfg.TUI.Theme = value
	default:
		return errf("未知配置项: %s（可设置: model, provider, mode, security, lang, output-style, theme）", key)
	}
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return errf("保存失败: %v", err)
	}
	return ok(fmt.Sprintf("已设置 %s = %s", key, value))
}

func cmdModels(b *Backend, args []string) Result {
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "add":
			if b == nil || b.RegisterCustomModel == nil {
				return errf("当前界面不支持添加模型。请用 `icode config model add <provider> <model_id> [name]` 或桌面端设置。")
			}
			if len(args) < 3 {
				return errf("用法: /models add <provider> <model_id> [name]")
			}
			name := args[2]
			if len(args) >= 4 {
				name = strings.Join(args[3:], " ")
			}
			if msg := b.RegisterCustomModel(args[1], args[2], name); msg != "" {
				return errf("%s", msg)
			}
			return ok(fmt.Sprintf("✓ 已添加自定义模型 %s（%s / %s）", config.ModelKey(args[1], args[2]), args[1], name))
		case "rm", "remove", "del", "delete":
			if b == nil || b.RemoveCustomModel == nil {
				return errf("当前界面不支持删除模型。请用 `icode config model rm <id>` 或桌面端设置。")
			}
			if len(args) < 2 {
				return errf("用法: /models rm <id>（id 形如 provider/model_id）")
			}
			if msg := b.RemoveCustomModel(args[1]); msg != "" {
				return errf("%s", msg)
			}
			return ok(fmt.Sprintf("✓ 已移除自定义模型 %s", args[1]))
		}
	}

	cfg, err := config.Load()
	if err != nil || len(cfg.Models) == 0 {
		return ok("暂无自定义模型。\n用 `/models add <provider> <model_id> [name]` 新增。")
	}
	var out strings.Builder
	out.WriteString("自定义模型:\n")
	for _, m := range cfg.Models {
		name := m.Name
		if name == "" {
			name = m.ModelID
		}
		out.WriteString(fmt.Sprintf("  %-26s %s / %s\n", m.ID, m.Provider, name))
	}
	return ok(out.String())
}

func cmdModel(b *Backend, st *State, args []string) Result {
	if len(args) == 0 {
		return ok("当前模型: " + shortStr(st.Model, "未设置") + "\n用法: /model <模型ID>（如 /model gpt-4o）")
	}
	id := args[0]
	_ = b
	persistSetting(func(c *config.Config) { c.Defaults.Model = id })
	return Result{Output: "Model -> " + id, Model: id}
}

func cmdProvider(st *State, args []string) Result {
	if len(args) == 0 {
		return ok("当前 Provider: " + shortStr(st.Provider, "未设置") + "\n用法: /provider <名称>")
	}
	persistSetting(func(c *config.Config) { c.Defaults.Provider = args[0] })
	return Result{Output: "Provider -> " + args[0], Provider: args[0]}
}

func cmdMode(b *Backend, st *State, args []string) Result {
	if len(args) == 0 {
		return ok("当前模式: " + shortStr(st.Mode, "agent") + "\n用法: /mode <agent|plan|yolo|auto|ask>")
	}
	want := strings.ToLower(args[0])
	valid := map[string]bool{"agent": true, "plan": true, "yolo": true, "auto": true, "ask": true}
	if !valid[want] {
		return errf("无效模式: %s（可选 agent/plan/yolo/auto/ask）", args[0])
	}
	if b != nil && b.Gate != nil {
		b.Gate.SetMode(permission.Mode(want))
	}
	persistSetting(func(c *config.Config) { c.Defaults.Mode = want })
	return Result{Output: "Mode -> " + want, Mode: want}
}

// cmdThinking toggles Anthropic extended thinking at runtime and persists it
// to config (Defaults.ThinkingTokens), matching the CLI /thinking command.
func cmdThinking(b *Backend, args []string) Result {
	if b == nil || b.Engine == nil {
		return ok("引擎未初始化。")
	}
	if len(args) == 0 {
		if bg := b.Engine.ThinkingBudget(); bg > 0 {
			return ok(fmt.Sprintf("当前 extended thinking 已开启，预算 %d tokens。用法: /thinking <on|off|<tokens>>", bg))
		}
		return ok("当前 extended thinking 已关闭。用法: /thinking <on|off|<tokens>>（如 /thinking 4096，对 Anthropic 模型生效）")
	}
	budget := 0
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "on":
		budget = 4096
	case "off", "0":
		budget = 0
	default:
		v, err := strconv.Atoi(strings.TrimSpace(args[0]))
		if err != nil || v < 1024 {
			return errf("无效预算（至少 1024 tokens）。用法: /thinking <on|off|<tokens>>")
		}
		budget = v
	}
	b.Engine.SetThinking(budget)
	persistSetting(func(c *config.Config) { c.Defaults.ThinkingTokens = budget })
	if budget > 0 {
		return ok(fmt.Sprintf("✓ extended thinking 已开启（预算 %d tokens，对 Anthropic 模型生效，已持久化）", budget))
	}
	return ok("✓ extended thinking 已关闭（已持久化）。")
}

// cmdModeShortcut maps the mode-shortcut commands to /mode values:
// /plan → plan, /ask → ask, /debug → agent (the closest "hands-on debugging"
// mode in iCode's agent/plan/yolo/auto/ask vocabulary).
func cmdModeShortcut(b *Backend, st *State, cmd string) Result {
	want := strings.TrimPrefix(cmd, "/")
	if want == "debug" {
		want = "agent"
	}
	return cmdMode(b, st, []string{want})
}

// cmdCD moves the session's working directory (Claude Code / Qwen Code
// parity). Unlike /add-dir (which only grants extra access), /cd relocates
// the whole session: every later tool resolves relative paths from here.
// No args prints the current directory.
func cmdCD(st *State, args []string) Result {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	if len(args) == 0 {
		return ok("当前工作目录: " + cwd + "\n用法: /cd <path> — 移动会话工作目录（相对路径基于当前目录解析）")
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
		return errf("解析路径失败: %v", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return errf("目录不存在或不是文件夹: %s", abs)
	}
	if err := os.Chdir(abs); err != nil {
		return errf("切换工作目录失败: %v", err)
	}
	// Refresh engine context so tools see the new directory immediately.
	if ncwd, err := os.Getwd(); err == nil {
		abs = ncwd
	}
	// Persist the directory so the CLI/TUI/server start there next launch
	// (the launch directory is otherwise lost when the process exits).
	if st == nil || !st.NoPersistCWD {
		persistSetting(func(c *config.Config) { c.Defaults.WorkingDir = abs })
	}
	return Result{Output: "工作目录已切换到: " + abs, CWD: abs}
}

// cmdRename retitles the active session (Cursor / Codex / Claude parity).
// With no title it shows the current one.
func cmdRename(b *Backend, st *State, args []string) Result {
	if b == nil || b.SessStore == nil {
		return ok("无会话存储可用。")
	}
	if st.SessionID == "" {
		return ok("没有活跃会话。先开始对话再重命名。")
	}
	sess, err := b.SessStore.Get(st.SessionID)
	if err != nil {
		return errf("会话不存在: %s", st.SessionID)
	}
	if len(args) == 0 {
		title := sess.Title
		if title == "" {
			title = "(untitled)"
		}
		return ok(fmt.Sprintf("当前会话标题: %s\n用法: /rename <新标题>", title))
	}
	title := strings.Join(args, " ")
	sess.Title = title
	if err := b.SessStore.Update(sess); err != nil {
		return errf("保存标题失败: %v", err)
	}
	return ok(fmt.Sprintf("会话已重命名为: %s", title))
}

// cmdCopy copies the last assistant output (or an explicit target file) to
// the Windows/Unix clipboard — Cursor / Codex / Gemini parity. It reads the
// session's most recent assistant message when no arg is given.
func cmdCopy(b *Backend, st *State, args []string) Result {
	if len(args) > 0 {
		path := args[0]
		data, err := os.ReadFile(path)
		if err != nil {
			return errf("读取文件失败: %v", err)
		}
		if err := copyToClipboard(string(data)); err != nil {
			return errf("复制失败: %v", err)
		}
		return ok("已复制文件内容到剪贴板: " + path)
	}
	if b == nil || b.SessStore == nil || st.SessionID == "" {
		return ok("没有可复制的输出（需要活跃会话）.\n用法: /copy [文件路径]")
	}
	sess, err := b.SessStore.Get(st.SessionID)
	if err != nil {
		return errf("会话不存在: %s", st.SessionID)
	}
	text := ""
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Role == types.RoleAssistant {
			text = sess.Messages[i].Content
			break
		}
	}
	if text == "" {
		return ok("没有可复制的助手输出。")
	}
	if err := copyToClipboard(text); err != nil {
		return errf("复制失败: %v", err)
	}
	return ok(fmt.Sprintf("已复制最后一段助手输出（%d 字符）到剪贴板。", len(text)))
}

func cmdSessions(b *Backend, st *State, _ []string) Result {
	if b == nil || b.SessStore == nil {
		return ok("无会话存储可用。")
	}
	if st.SessionID != "" {
		return ok("活跃会话: " + st.SessionID)
	}
	sessions, err := sessionum.ListNonDeleted(b.SessStore, 20)
	if err != nil || len(sessions) == 0 {
		return ok("暂无已保存会话。开始对话后自动创建。")
	}
	var sb strings.Builder
	kept := 0
	sb.WriteString(fmt.Sprintf("已保存会话 (%d):\n", len(sessions)))
	for _, s := range sessions {
		kept++
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		line := fmt.Sprintf("  %s  %s  [%s]", s.ID, title, s.ModelID)
		if sum := sessionum.Get(&s); sum != "" {
			first := sum
			if idx := strings.Index(first, "\n"); idx > 0 {
				first = first[:idx]
			}
			if r := []rune(first); len(r) > 60 {
				first = string(r[:60]) + "…"
			}
			line += fmt.Sprintf("\n      ↳ %s", first)
		}
		sb.WriteString(line + "\n")
	}
	if kept == 0 {
		sb.WriteString("  (无可用会话 — 全部已软删除，可用 /restore <id> 恢复)\n")
	}
	sb.WriteString("\n/resume <session_id> 载入某个会话")
	return ok(sb.String())
}

// defaultLiteN is how many recent messages /resume --lite replays alongside
// the archived summary (the rest is dropped from the model context).
const defaultLiteN = 4

func cmdResume(b *Backend, st *State, args []string) Result {
	if len(args) == 0 {
		return ok("用法: /resume <session-id> [--lite[=<最近消息数>] | --compact[=<最近消息数>]]\n  --lite 只把摘要+最近 N 条消息送给模型，省 token（需要该会话已有存档摘要）\n  --compact 同 --lite，但摘要不足时先用模型生成语义摘要并缓存")
	}
	if b == nil || b.SessStore == nil {
		return ok("无会话存储可用。")
	}
	id := args[0]
	lite := 0
	compact := false
	if len(args) > 1 {
		switch a := strings.TrimSpace(args[1]); {
		case a == "--lite":
			lite = defaultLiteN
		case strings.HasPrefix(a, "--lite="):
			if v, err := strconv.Atoi(strings.TrimPrefix(a, "--lite=")); err == nil && v > 0 {
				lite = v
			} else {
				return errf("用法: /resume <session-id> --lite=<最近消息数>（应为正整数）")
			}
		case a == "--compact":
			compact = true
			lite = defaultLiteN
		case strings.HasPrefix(a, "--compact="):
			compact = true
			if v, err := strconv.Atoi(strings.TrimPrefix(a, "--compact=")); err == nil && v > 0 {
				lite = v
			} else {
				return errf("用法: /resume <session-id> --compact=<最近消息数>（应为正整数）")
			}
		default:
			return errf("未知参数: %s（支持 --lite、--lite=<n>、--compact、--compact=<n>）", a)
		}
	}
	if st.SessionID != "" && st.SessionID != id {
		archiveSession(b, st)
	}
	sess, err := b.SessStore.Get(id)
	if err != nil {
		return errf("会话不存在: %s", id)
	}
	if sessionum.IsDeleted(sess) {
		return errf("该会话已被软删除，先用 /restore %s 恢复。", id)
	}
	if lite > 0 {
		if compact && !sessionum.IsSemantic(sess) && b.Engine != nil {
			// Resume compaction (Claude Code parity): generate a semantic
			// summary on the spot and cache it, so the resumed session feeds
			// the model only summary + recent turns. A cached semantic summary
			// is reused without a second model call.
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			sum, serr := b.Engine.SummarizeConversation(ctx, id, "")
			cancel()
			switch {
			case serr != nil:
				return errf("⚠ 模型摘要生成失败（%s），已回退为本地存档摘要。", clip(serr.Error(), 140))
			case sum == "":
				return errf("该会话轮次太少，无需模型摘要；如已有本地摘要可改用 /resume %s --lite。", id)
			default:
				_ = sessionum.Save(b.SessStore, sess, sum)
				_ = sessionum.MarkSemantic(b.SessStore, sess)
			}
		}
		if sessionum.Get(sess) == "" {
			return errf("该会话还没有存档摘要，无法压缩恢复。先 /summarize、/compact 或退出时自动存档后再试。")
		}
		if err := sessionum.SetLite(b.SessStore, sess, lite); err != nil {
			return errf("设置 lite 模式失败: %v", err)
		}
	}
	st.SessionID = sess.ID
	out := fmt.Sprintf("已载入会话 %s — %d 条消息", sess.ID, len(sess.Messages))
	if lite > 0 {
		if compact {
			out = fmt.Sprintf("已载入会话 %s（紧凑恢复）— 语义摘要 + 最近 %d 条消息（共 %d 条）送入模型，后续轮次自动生效", sess.ID, lite, len(sess.Messages))
		} else {
			out = fmt.Sprintf("已载入会话 %s（lite 模式）— 摘要 + 最近 %d 条消息（共 %d 条）送入模型", sess.ID, lite, len(sess.Messages))
		}
	}
	return Result{
		Output:    out,
		Model:     sess.ModelID,
		Provider:  sess.ProviderName,
		SessionID: sess.ID,
	}
}

// cmdFork branches a new independent session from a source session (or a
// prefix of it): /fork <session-id>[@<n>]. The fork shares history up to
// message n (or the whole session when n is omitted) but diverges from there.
func cmdFork(b *Backend, st *State, args []string) Result {
	if len(args) == 0 {
		return ok("用法: /fork <session-id>[@<消息数>] — 从历史会话分支出一个独立会话")
	}
	if b == nil || b.SessStore == nil {
		return ok("无会话存储可用。")
	}
	spec := args[0]
	srcID := spec
	n := 0
	if at := strings.LastIndex(spec, "@"); at > 0 {
		srcID = spec[:at]
		if v, err := strconv.Atoi(spec[at+1:]); err == nil {
			n = v
		} else {
			return errf("用法: /fork <session-id>[@<消息数>]（消息数应为数字）")
		}
	}
	if st.SessionID != "" && st.SessionID != srcID {
		archiveSession(b, st)
	}
	forked, err := sessionum.Fork(b.SessStore, srcID, n)
	if err != nil {
		return errf("分叉失败: %v", err)
	}
	st.SessionID = forked.ID
	return Result{
		Output:    fmt.Sprintf("已从 %s 分叉出独立会话 %s — %d 条消息（后续互不影响）", srcID, forked.ID, len(forked.Messages)),
		Model:     forked.ModelID,
		Provider:  forked.ProviderName,
		SessionID: forked.ID,
	}
}

// archiveSession best-effort writes the current session's local summary into
// its Metadata before the user leaves it (/new, /resume). Failures are
// swallowed — archiving must never block navigation.
func archiveSession(b *Backend, st *State) {
	if b == nil || b.SessStore == nil || st == nil || st.SessionID == "" {
		return
	}
	sess, err := b.SessStore.Get(st.SessionID)
	if err != nil {
		return
	}
	_ = sessionum.Save(b.SessStore, sess, sessionum.Generate(sess, st.Model, st.Provider, st.Mode))
}

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
		// Parse optional --verify <acceptance command> (ZCode Goal parity).
		goal := ""
		verify := ""
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
			return ok("目标不能为空。用法: /goal set <目标文本> [--verify <验收命令>]")
		}
		sess, r := load()
		if sess == nil {
			return r
		}
		if err := sessionum.SetGoal(b.SessStore, sess, goal); err != nil {
			return errf("保存目标失败: %v", err)
		}
		if verify != "" {
			if err := sessionum.SetGoalVerify(b.SessStore, sess, verify); err != nil {
				return errf("保存验收命令失败: %v", err)
			}
			return ok("已设置可验收目标（每轮自动迭代直到验收命令通过）：\n目标：" + goal + "\n验收命令：" + verify)
		}
		return ok("已设置长目标（后续每轮对话都会自动携带）：\n" + goal)
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

func cmdSearch(b *Backend, args []string) Result {
	query := strings.Join(args, " ")
	if query == "" {
		return ok("用法: /search <关键词> — 搜索历史会话")
	}
	if b == nil || b.SessStore == nil {
		return ok("无会话存储可用。")
	}
	results, err := b.SessStore.SearchMessages(query, 20)
	if err != nil {
		return errf("搜索失败: %v", err)
	}
	if len(results) == 0 {
		return ok(fmt.Sprintf("未找到 %q 相关结果。", query))
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 条结果:\n", len(results)))
	for _, r := range results {
		title := r.SessionTitle
		if title == "" {
			title = "Untitled"
		}
		content := strings.ReplaceAll(r.Content, "\n", " ")
		if len(content) > 120 {
			content = content[:120] + "..."
		}
		sb.WriteString(fmt.Sprintf("\n  [%s] %s\n    %s\n", title, r.Role, content))
	}
	sb.WriteString("\n/resume <session_id> 载入某个会话")
	return ok(sb.String())
}

func cmdClear(b *Backend, st *State) Result {
	if b != nil && b.SessStore != nil && st.SessionID != "" {
		if sess, err := b.SessStore.Get(st.SessionID); err == nil {
			if err := sessionum.MarkDeleted(b.SessStore, sess); err != nil {
				return errf("归档失败（会话未删除）: %v", err)
			}
		}
	}
	return Result{Output: "会话已归档并标记删除，可从列表移除（/restore <id> 可恢复）。", ClearSession: true}
}

func cmdRestore(b *Backend, args []string) Result {
	if len(args) == 0 {
		return ok("用法: /restore <session-id> — 恢复被 /clear 软删除的会话")
	}
	if b == nil || b.SessStore == nil {
		return ok("无会话存储可用。")
	}
	sess, err := b.SessStore.Get(args[0])
	if err != nil {
		return errf("会话不存在: %s", args[0])
	}
	if !sessionum.IsDeleted(sess) {
		return ok("该会话未被删除，无需恢复。")
	}
	if err := sessionum.Restore(b.SessStore, sess); err != nil {
		return errf("恢复失败: %v", err)
	}
	return ok(fmt.Sprintf("已恢复会话 %s — %d 条消息", sess.ID, len(sess.Messages)))
}

func cmdUndo(st *State, args []string) Result {
	if st.SessionID == "" {
		return ok("没有活跃会话。")
	}
	n := 1
	if len(args) > 0 {
		if v, err := strconv.Atoi(args[0]); err == nil {
			n = v
		}
	}
	if n <= 0 {
		return ok("用法: /undo [N] — 回滚前 N 步文件更改")
	}
	store, err := checkpoint.GetOrOpen(st.SessionID)
	if err != nil {
		return errf("打开检查点失败: %v", err)
	}
	files, err := store.Rewind(context.Background(), n)
	if err != nil {
		return errf("回滚失败: %v", err)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("已回滚 %d 步。影响文件:\n", n))
	for _, f := range files {
		b.WriteString("  " + f + "\n")
	}
	if len(files) == 0 {
		b.WriteString("  （无检查点文件）\n")
	}
	return ok(b.String())
}

func cmdDiff(args []string) Result {
	extra := ""
	if len(args) > 0 {
		extra = args[0]
	}
	cmdArgs := []string{"diff", extra}
	output, err := executil.Command("git", cmdArgs...).CombinedOutput()
	if err != nil && len(output) == 0 {
		return errf("git diff: %v", err)
	}
	if len(output) == 0 {
		return ok("没有未提交的改动。")
	}
	return ok("```diff\n" + strings.TrimRight(string(output), "\n") + "\n```")
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

// cmdKnowledge searches the local document knowledge base (/kb), 对标
// WorkBuddy 的"资料库"能力（本地 RAG，零 API 零 token）。
func cmdKnowledge(b *Backend, args []string) Result {
	if b == nil || b.Engine == nil {
		return errf("知识库不可用（引擎未就绪）。")
	}
	mgr := b.Engine.KnowledgeManager()
	if mgr == nil {
		return errf("知识库未配置。请在 config.yaml 的 knowledge.dirs 中指定文档目录，或在 /config 中设置。")
	}
	if len(args) == 0 {
		return ok(fmt.Sprintf("知识库状态：已索引 %d 个片段。\n用法: /kb <查询>（如 /kb 保险犹豫期退保规则）", mgr.ChunkCount()))
	}
	query := strings.Join(args, " ")
	if mgr.ChunkCount() == 0 {
		if _, err := mgr.Index(context.Background()); err != nil {
			return errf("索引失败: %v", err)
		}
	}
	results := mgr.Search(query, 5)
	return ok(knowledge.Format(results))
}
func cmdLsp(b *Backend, st *State, args []string) Result {
	if b == nil || b.Engine == nil {
		return errf("LSP 不可用（引擎未就绪）。")
	}
	mgr := b.Engine.LSPManager()
	if mgr == nil {
		return errf("LSP 未启用。请在配置中开启 lsp.enabled（config.toml 或 /config）。")
	}
	sub := "status"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
		args = args[1:]
	}
	out, err := mgr.QueryReport(sub, args)
	if err != nil {
		return errf("%v", err)
	}
	return ok(out)
}

func cmdExport(b *Backend, st *State, args []string) Result {
	filename := "icode-export.md"
	if len(args) > 0 {
		filename = args[0]
	}
	if b == nil || b.SessStore == nil || st.SessionID == "" {
		return ok("没有可导出的会话（先发一条消息）。")
	}
	sess, err := b.SessStore.Get(st.SessionID)
	if err != nil {
		return errf("读取会话失败: %v", err)
	}
	var sb strings.Builder
	sb.WriteString("# iCode Conversation Export\n\n")
	sb.WriteString(fmt.Sprintf("**Model:** %s  \n", st.Model))
	sb.WriteString(fmt.Sprintf("**Mode:** %s  \n\n", st.Mode))
	sb.WriteString("---\n\n")
	for _, m := range sess.Messages {
		switch m.Role {
		case "user":
			sb.WriteString("## User\n\n")
		case "assistant":
			sb.WriteString("## Assistant\n\n")
		case "system":
			sb.WriteString("> ")
		case "tool":
			if len(m.ToolCalls) > 0 {
				sb.WriteString("### Tool: " + m.ToolCalls[0].Name + "\n\n")
			} else {
				sb.WriteString("### Tool\n\n")
			}
		}
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}
	if err := os.WriteFile(filename, []byte(sb.String()), 0o644); err != nil {
		return errf("导出失败: %v", err)
	}
	return ok(fmt.Sprintf("已导出到 %s（%d 条消息）", filename, len(sess.Messages)))
}

func cmdShare(b *Backend, st *State, args []string) Result {
	name := fmt.Sprintf("icode-share-%s.md", time.Now().Format("20060102-150405"))
	if len(args) > 0 {
		name = args[0]
	}
	res := cmdExport(b, st, []string{name})
	if res.IsError {
		return res
	}
	if abs, err := filepath.Abs(name); err == nil {
		return ok(res.Output + "\n分享文件: " + abs + "\n（可直接发送或粘贴到支持 Markdown 的工具）")
	}
	return res
}

func cmdReview(args []string) Result {
	var target string
	if len(args) > 0 {
		path := args[0]
		if data, err := os.ReadFile(path); err == nil {
			target = fmt.Sprintf("请审查文件 %s：\n\n%s", path, string(data))
		} else {
			return errf("读取文件失败: %v", err)
		}
	} else {
		output, err := executil.Command("git", "diff").CombinedOutput()
		if err != nil && len(output) == 0 {
			return errf("git diff 失败: %v", err)
		}
		if len(output) == 0 {
			return ok("没有未提交的改动可审查。可指定路径：/review <file>")
		}
		target = "请审查以下 git 工作区差异，指出质量问题、安全隐患与改进建议：\n\n```diff\n" + string(output) + "\n```"
	}
	return chat(target)
}

func cmdApply(st *State) Result {
	stage := searchreplace.StageForSession(st.SessionID)
	n := stage.Count()
	if n == 0 {
		return ok("没有可应用的暂存编辑。")
	}
	results := stage.ApplyValid()
	var b strings.Builder
	applied := 0
	for _, r := range results {
		b.WriteString(r + "\n")
		if !strings.HasPrefix(r, "FAILED") {
			applied++
		}
	}
	b.WriteString(fmt.Sprintf("Applied %d/%d staged edits. Remaining: %d", applied, n, stage.Count()))
	return ok(b.String())
}

func cmdReject(st *State) Result {
	stage := searchreplace.StageForSession(st.SessionID)
	n := stage.Count()
	if n == 0 {
		return ok("没有可丢弃的暂存编辑。")
	}
	stage.Clear()
	return ok(fmt.Sprintf("已丢弃 %d 条暂存编辑。", n))
}

func cmdKeys() Result {
	cfg, err := config.Load()
	if err != nil {
		return errf("读取配置失败: %v", err)
	}
	var b strings.Builder
	b.WriteString("API 密钥状态:\n")
	for name, pc := range cfg.Providers {
		st := "未配置"
		if pc.APIKey != "" {
			st = "已配置"
		}
		b.WriteString(fmt.Sprintf("  %-14s %s\n", name, st))
	}
	b.WriteString("\n用 `icode config key <provider> <key>` 或 设置 → 提供商 配置。")
	return ok(b.String())
}

func cmdMCP(args []string) Result {
	cfg, err := config.Load()
	if err != nil {
		return errf("读取配置失败: %v", err)
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
			b.WriteString("  （无。用 `/mcp add <name> <stdio|sse> <command> [args...]` 添加）\n")
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
		return ok(b.String())
	case "add":
		if len(args) < 4 {
			return ok("用法: /mcp add <name> <stdio|sse> <command> [args...]")
		}
		typ := strings.ToLower(args[2])
		if typ != "stdio" && typ != "sse" {
			return errf("类型只能是 stdio 或 sse，收到: %s", args[2])
		}
		mc := config.MCPServerCfg{Name: args[1], Type: typ, Command: args[3], Enabled: true}
		if len(args) > 4 {
			mc.Args = args[4:]
		}
		filtered := make([]config.MCPServerCfg, 0, len(cfg.MCP))
		for _, s := range cfg.MCP {
			if s.Name != args[1] {
				filtered = append(filtered, s)
			}
		}
		cfg.MCP = append(filtered, mc)
		if err := cfg.Save(config.DefaultPath()); err != nil {
			return errf("保存失败: %v", err)
		}
		return ok(fmt.Sprintf("已添加 MCP 服务器 %s（%s）。重启 iCode 后生效。", args[1], typ))
	case "remove":
		if len(args) < 2 {
			return ok("用法: /mcp remove <name>")
		}
		filtered := make([]config.MCPServerCfg, 0, len(cfg.MCP))
		found := false
		for _, s := range cfg.MCP {
			if s.Name != args[1] {
				filtered = append(filtered, s)
			} else {
				found = true
			}
		}
		if !found {
			return errf("未找到 MCP 服务器: %s", args[1])
		}
		cfg.MCP = filtered
		if err := cfg.Save(config.DefaultPath()); err != nil {
			return errf("保存失败: %v", err)
		}
		return ok(fmt.Sprintf("已移除 MCP 服务器 %s。重启 iCode 后生效。", args[1]))
	case "get":
		if len(args) < 2 {
			return ok("用法: /mcp get <name>")
		}
		for _, s := range cfg.MCP {
			if s.Name == args[1] {
				var b strings.Builder
				fmt.Fprintf(&b, "MCP 服务器 %s:\n", args[1])
				fmt.Fprintf(&b, "  类型: %s\n", s.Type)
				fmt.Fprintf(&b, "  命令: %s\n", s.Command)
				if len(s.Args) > 0 {
					fmt.Fprintf(&b, "  参数: %s\n", strings.Join(s.Args, " "))
				}
				if s.URL != "" {
					fmt.Fprintf(&b, "  地址: %s\n", s.URL)
				}
				fmt.Fprintf(&b, "  启用: %v\n", s.Enabled)
				return ok(b.String())
			}
		}
		return errf("未找到 MCP 服务器: %s", args[1])
	case "restart":
		if len(args) < 2 {
			return ok("用法: /mcp restart <name>")
		}
		return ok(fmt.Sprintf("已请求重启 %s。MCP 连接于启动时建立，请重启 iCode 使新配置生效。", args[1]))
	default:
		return ok("用法: /mcp [list] | add <name> <stdio|sse> <command> [args...] | remove <name> | get <name> | restart <name>")
	}
}

func cmdMemory(b *Backend, args []string) Result {
	proj := projectMemoryPath()
	user, _ := projectcontext.UserMemoryPath()
	if len(args) == 0 {
		return memoryOverview(b, proj, user)
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "edit":
		editor := os.Getenv("EDITOR")
		if editor == "" {
			if runtime.GOOS == "windows" {
				editor = "notepad"
			} else {
				editor = "vi"
			}
		}
		cmd := executil.Command(editor, proj)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return errf("打开编辑器失败: %v", err)
		}
		return ok(fmt.Sprintf("已用 %s 打开项目记忆 %s", editor, proj))
	case "prefs", "preference", "preferences":
		return memoryPrefs(b, args[1:])
	case "list":
		return memoryPrefs(b, args[1:])
	case "forget", "rm":
		return memoryForget(b, args[1:])
	case "clear":
		return memoryClear(b, args[1:])
	default:
		// Unknown subcommand — treat as a raw append to the project memory file
		// (legacy behavior) so `# <content>`-style calls keep working.
		return memoryOverview(b, proj, user)
	}
}

// memoryOverview shows both the ICODE.md memory files and the remembered
// preference store.
func memoryOverview(b *Backend, proj, user string) Result {
	var bld strings.Builder
	bld.WriteString(fmt.Sprintf("记忆文件:\n  项目级: %s\n  用户级: %s\n\n", proj, user))
	if data, err := os.ReadFile(proj); err == nil && len(data) > 0 {
		bld.WriteString("── 项目记忆 (ICODE.md) ──\n" + string(data) + "\n")
	} else {
		bld.WriteString("（项目记忆为空，用 `# <内容>` 追加，或 `/memory edit` 编辑）\n")
	}
	prefs := memoryPrefs(b, nil)
	if strings.TrimSpace(prefs.Output) != "" {
		bld.WriteString("\n" + prefs.Output)
	}
	return ok(bld.String())
}

// memoryPrefs lists the remembered USER PREFERENCES (prefmem). Unlike the
// ICODE.md files these are learned automatically and only ever hold short
// preference statements — never code.
func memoryPrefs(b *Backend, _ []string) Result {
	if b == nil || b.Engine == nil || b.Engine.PreferenceMemory() == nil {
		return ok("偏好记忆未启用。")
	}
	entries := b.Engine.PreferenceMemory().Snapshot()
	if len(entries) == 0 {
		return ok("（暂无已记忆的用户偏好。说“以后都用…/优先用…”会自动记住。）")
	}
	var bld strings.Builder
	bld.WriteString("已记忆的用户偏好 (prefmem):\n")
	for _, e := range entries {
		seen := ""
		if e.Seen > 1 {
			seen = fmt.Sprintf("  (提到 %d 次)", e.Seen)
		}
		bld.WriteString(fmt.Sprintf("  · %s%s\n", e.Text, seen))
	}
	bld.WriteString("\n管理: /memory forget <内容>  清除单条；/memory clear 清空全部；/memory list 查看。")
	return ok(bld.String())
}

// memoryForget removes a single remembered preference matching the argument
// text (substring match).
func memoryForget(b *Backend, args []string) Result {
	if b == nil || b.Engine == nil || b.Engine.PreferenceMemory() == nil {
		return ok("偏好记忆未启用。")
	}
	if len(args) == 0 {
		return errf("用法: /memory forget <偏好内容片段>")
	}
	needle := strings.Join(args, " ")
	store := b.Engine.PreferenceMemory()
	removed := false
	for _, e := range store.Snapshot() {
		if strings.Contains(e.Text, needle) {
			store.Forget(e.Text)
			removed = true
		}
	}
	if removed {
		_ = store.SaveFile(prefmem.DefaultPath())
		return ok(fmt.Sprintf("已遗忘 %d 条与 “%s” 相关的偏好。", 1, needle))
	}
	return ok(fmt.Sprintf("没有找到与 “%s” 匹配的偏好。用 /memory list 查看。", needle))
}

// memoryClear wipes all remembered preferences (both memory and the persisted
// file). Pass "yes" to skip the confirmation prompt.
func memoryClear(b *Backend, args []string) Result {
	if b == nil || b.Engine == nil || b.Engine.PreferenceMemory() == nil {
		return ok("偏好记忆未启用。")
	}
	confirm := ""
	if len(args) > 0 {
		confirm = strings.ToLower(args[0])
	}
	if confirm != "yes" && confirm != "y" {
		return ok("确认清空全部偏好记忆？执行 /memory clear yes")
	}
	n := b.Engine.PreferenceMemory().Purge()
	_ = b.Engine.PreferenceMemory().SaveFile(prefmem.DefaultPath())
	return ok(fmt.Sprintf("已清空 %d 条偏好记忆。", n))
}

func cmdHooks() Result {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".icode", "hooks.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if gerr := permission.GenerateHooks(path); gerr != nil {
			return errf("生成 hooks.yaml 失败: %v", gerr)
		}
		return ok("[✓] 已生成 " + path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errf("读取 hooks.yaml 失败: %v", err)
	}
	return ok(fmt.Sprintf("hooks.yaml: %s\n\n```yaml\n%s\n```", path, string(data)))
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

func cmdAgents() Result {
	v := agent.Load(agent.AgentDefaultDirs()...)
	v.RegisterDefaults()
	var b strings.Builder
	b.WriteString("可用 agent:\n")
	for _, d := range v.List() {
		b.WriteString(fmt.Sprintf("  %s — %s\n", d.Name, d.Description))
	}
	return ok(b.String())
}

func cmdSkills() Result {
	reg := skills.Load(skills.DefaultDirs()...)
	var b strings.Builder
	b.WriteString("已安装技能 (SKILL.md):\n")
	if list := reg.List(); len(list) == 0 {
		b.WriteString("  无。在 ~/.icode/skills/ 或 .icode/skills/ 下放置 SKILL.md 即可启用。\n")
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

func cmdOutputStyle(b *Backend, args []string) Result {
	if len(args) == 0 {
		cur := "normal"
		if c, err := config.Load(); err == nil && c.Defaults.OutputStyle != "" {
			cur = c.Defaults.OutputStyle
		}
		return ok("当前风格: " + cur + "\n用法: /output-style <concise|normal|verbose>")
	}
	style := strings.ToLower(args[0])
	if style != "concise" && style != "normal" && style != "verbose" {
		return errf("无效风格: %s（可选 concise|normal|verbose）", args[0])
	}
	cfg, err := config.Load()
	if err != nil {
		return errf("读取配置失败: %v", err)
	}
	cfg.Defaults.OutputStyle = style
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return errf("保存配置失败: %v", err)
	}
	if b != nil && b.Engine != nil {
		b.Engine.SetSystemPrompt(config.EffectiveSystemPrompt(cfg))
		return ok("输出风格已设为 " + style + "（已即时生效并持久化）")
	}
	return ok("输出风格已设为 " + style + "（已持久化，重启会话后生效）")
}

func cmdSecurity(b *Backend, st *State, args []string) Result {
	valid := map[string]config.SecurityLevel{
		"local":        config.SecLocal,
		"desensitize":  config.SecDesensitize,
		"local-llm":    config.SecLocalLLM,
		"foreign-llm":  config.SecForeignLLM,
		"unrestricted": config.SecUnrestricted,
	}
	if len(args) == 0 {
		sec := st.Security
		if sec == "" {
			if c, err := config.Load(); err == nil && c.SecurityLevel != "" {
				sec = string(c.SecurityLevel)
			} else {
				sec = "local"
			}
		}
		return ok(fmt.Sprintf("当前安全等级: %s\n用法: /security [local|desensitize|local-llm|foreign-llm|unrestricted]",
			permission.SecurityLabel(config.SecurityLevel(sec))))
	}
	newLevel := strings.ToLower(args[0])
	level, validLevel := valid[newLevel]
	if !validLevel {
		return errf("无效安全等级: %s", args[0])
	}
	persistSetting(func(c *config.Config) { c.SecurityLevel = level })
	return Result{Output: "安全等级已设为 " + permission.SecurityLabel(level), Security: newLevel}
}

func cmdPermissions(b *Backend, st *State) Result {
	cfg, _ := config.Load()
	var sb strings.Builder
	sb.WriteString("权限 / 安全:\n")
	sec := st.Security
	if sec == "" {
		if cfg != nil && cfg.SecurityLevel != "" {
			sec = string(cfg.SecurityLevel)
		} else {
			sec = "local"
		}
	}
	sb.WriteString(fmt.Sprintf("  当前安全等级: %s\n", permission.SecurityLabel(config.SecurityLevel(sec))))
	if cfg != nil {
		if len(cfg.Hooks) > 0 {
			sb.WriteString("  生命周期钩子:\n")
			for ev, rules := range cfg.Hooks {
				for _, r := range rules {
					sb.WriteString(fmt.Sprintf("    %-12s %s\n", ev, r.Command))
				}
			}
		}
	}
	sb.WriteString("\n切换安全等级: /security [local|desensitize|local-llm|foreign-llm|unrestricted]\n工具级允许/拒绝请在 设置 → 工具权限 中配置。")
	return ok(sb.String())
}

func cmdAddDir(b *Backend, args []string) Result {
	if len(args) == 0 {
		var list []string
		if c, err := config.Load(); err == nil {
			list = c.Defaults.ExtraDirs
		}
		if len(list) == 0 {
			return ok("用法: /add-dir <目录> — 添加额外工作目录。当前无额外目录。")
		}
		return ok("当前额外工作目录:\n  " + strings.Join(list, "\n  "))
	}
	dir := args[0]
	abs, err := filepath.Abs(dir)
	if err != nil {
		return errf("解析路径失败: %v", err)
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return errf("目录不存在或不是文件夹: %s", abs)
	}
	cfg, err := config.Load()
	if err != nil {
		return errf("读取配置失败: %v", err)
	}
	for _, d := range cfg.Defaults.ExtraDirs {
		if d == abs {
			return ok("该目录已在工作目录列表中: " + abs)
		}
	}
	cfg.Defaults.ExtraDirs = append(cfg.Defaults.ExtraDirs, abs)
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return errf("保存配置失败: %v", err)
	}
	if b != nil && b.Engine != nil {
		b.Engine.SetSystemPrompt(config.EffectiveSystemPrompt(cfg))
	}
	return ok(fmt.Sprintf("已添加工作目录: %s（共 %d 个，已注入上下文）", abs, len(cfg.Defaults.ExtraDirs)))
}

func cmdUpdate(b *Backend) Result {
	if b == nil || b.RefreshModels == nil {
		return errf("模型刷新服务未初始化。")
	}
	updates, err := b.RefreshModels(context.Background())
	if err != nil && len(updates) == 0 {
		return errf("刷新失败: %v", err)
	}
	var sb strings.Builder
	sb.WriteString("模型目录刷新结果:\n")
	okN, fail := 0, 0
	for _, u := range updates {
		if u.Success {
			okN++
			sb.WriteString(fmt.Sprintf("  ✓ %-14s %d 个模型（%s）\n", u.Name, u.Count, u.Source))
		} else {
			fail++
			msg := u.Error
			if msg == "" {
				msg = "未知错误"
			}
			sb.WriteString(fmt.Sprintf("  ✗ %-14s %s\n", u.Name, msg))
		}
	}
	sb.WriteString(fmt.Sprintf("成功 %d · 失败 %d", okN, fail))
	return ok(sb.String())
}

func cmdDoctor(b *Backend, st *State) Result {
	var sb strings.Builder
	sb.WriteString("iCode 诊断:\n")
	sb.WriteString(fmt.Sprintf("  版本:       %s\n", shortStr(st.Version, "dev")))
	sb.WriteString(fmt.Sprintf("  模型:       %s\n", shortStr(st.Model, "未设置")))
	sb.WriteString(fmt.Sprintf("  提供商:     %s\n", shortStr(st.Provider, "未设置")))
	sec := st.Security
	if sec == "" {
		if c, err := config.Load(); err == nil && c.SecurityLevel != "" {
			sec = string(c.SecurityLevel)
		} else {
			sec = "local"
		}
	}
	sb.WriteString(fmt.Sprintf("  安全等级:   %s\n", permission.SecurityLabel(config.SecurityLevel(sec))))
	cfg, err := config.Load()
	if err != nil {
		sb.WriteString("  配置:       读取失败 " + err.Error() + "\n")
	} else {
		configured := 0
		for _, pc := range cfg.Providers {
			if pc.APIKey != "" {
				configured++
			}
		}
		sb.WriteString(fmt.Sprintf("  已配置 Key: %d 个提供商\n", configured))
	}
	if proj := projectMemoryPath(); proj != "" {
		sb.WriteString("  项目记忆:   " + proj + "\n")
	}
	if user, err := projectcontext.UserMemoryPath(); err == nil {
		sb.WriteString("  用户记忆:   " + user + "\n")
	}
	return ok(sb.String())
}

func cmdLang(args []string, b *Backend) Result {
	if len(args) == 0 {
		return ok("用法: /lang <zh-CN|zh-TW|en>。UI 与模型输出同步切换语言。")
	}
	switch args[0] {
	case "zh-CN", "zh-TW", "en":
		var updated *config.Config
		persistSetting(func(c *config.Config) { c.Language = args[0]; updated = c })
		// Live-refresh the engine so the language directive (replies,
		// reasoning, code comments) reaches the model without a restart.
		if b != nil && b.Engine != nil && updated != nil {
			b.Engine.SetSystemPrompt(config.EffectiveSystemPrompt(updated))
		}
		return ok("语言已设为 " + args[0] + "，模型回复/思考/注释语言已同步生效。")
	default:
		return errf("无效语言: %s（可选 zh-CN/zh-TW/en）", args[0])
	}
}

func cmdAdmin(b *Backend, args []string) Result {
	if len(args) == 0 || (len(args) > 0 && strings.ToLower(args[0]) != "off") {
		if b != nil && b.Gate != nil {
			b.Gate.SetMode(permission.Mode("yolo"))
		}
		return Result{Output: "管理员模式开启（yolo 模式，不再逐一询问工具权限）", Mode: "yolo"}
	}
	if b != nil && b.Gate != nil {
		b.Gate.SetMode(permission.Mode("ask"))
	}
	persistSetting(func(c *config.Config) { c.Defaults.Mode = "ask" })
	return Result{Output: "管理员模式已关闭，恢复 ask 模式", Mode: "ask"}
}

// ── Helpers ───────────────────────────────────────────────────────

func persistSetting(fn func(*config.Config)) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	fn(cfg)
	_ = cfg.Save(config.DefaultPath())
}

func projectMemoryPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "ICODE.md"
	}
	return filepath.Join(cwd, "ICODE.md")
}

// copyToClipboard writes text to the system clipboard using the platform's
// native pipe helper (clip / pbcopy / xclip). Mirrors the TUI's writeClipboard.
func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("clip")
	case "darwin":
		cmd = exec.Command("pbcopy")
	default:
		cmd = exec.Command("xclip", "-selection", "clipboard")
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func shortStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func shortDir(dir string) string {
	if dir == "" {
		return "."
	}
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(dir, home) {
		return "~" + strings.TrimPrefix(dir, home)
	}
	return dir
}

// clip truncates s to n runes and appends "…" when shortened (keeps error
// messages inside slash-command replies short).
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func formatInt(n int) string {
	if n <= 0 {
		return "0"
	}
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b []byte
	cnt := 0
	for i := len(s) - 1; i >= 0; i-- {
		b = append([]byte{s[i]}, b...)
		cnt++
		if cnt == 3 && i > 0 {
			b = append([]byte{','}, b...)
			cnt = 0
		}
	}
	return string(b)
}
