package slashui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/conversation"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/slashcmd"
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
	// SetCredentials injects a freshly saved (or cleared) API key into the
	// live providers so /login and /logout take effect without a restart.
	// Optional — when nil the key still persists and applies next startup.
	// It returns an error string, or "" on success.
	SetCredentials func(provider, apiKey string) string
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
	case "/preset":
		return cmdPreset(b, st, args)
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
	case "/replay":
		return cmdReplay(st, args)
	case "/loop":
		return cmdLoop(b, args)
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
		return cmdHooks(b, args)
	case "/init":
		return cmdInit()
	case "/agents":
		return cmdAgents()
	case "/tasks":
		return cmdTasks()
	case "/skills":
		return cmdSkills(args)
	case "/skill-eval":
		return cmdSkillEval(args)
	case "/plugin":
		return cmdPlugin(args)
	case "/mesh":
		return cmdMesh(args, ctx)
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
		return cmdPermissions(b, st, args)
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
		return cmdLogin(b, args, ctx)
	case "/logout":
		return cmdLogout(b, args, st)
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
		if len(args) > 0 && strings.EqualFold(args[0], "zip") {
			return cmdBugZip(st)
		}
		return ok("反馈问题:\n  · /bug zip — 生成脱敏诊断包（版本/日志/配置打码，附 issue 直接上传）\n  · GitHub Issues: https://github.com/ponygates/icode/issues\n  · 或对话中 `# <内容>` 写入记忆文件")
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
		{"/preset [light|balanced|deliver]", "执行设定（轻量/均衡/交付）"},
		{"/session", "显示当前会话"}, {"/sessions", "列出已保存会话"},
		{"/resume <id>", "载入历史会话"}, {"/fork <id>[@n]", "从历史会话分支出独立会话"}, {"/rename <标题>", "重命名当前会话"}, {"/goal [set|show|clear]", "长目标模式"}, {"/budget [set|show|clear]", "Token 预算护栏"}, {"/new", "开启新会话"},
		{"/clear", "清空当前会话"}, {"/wipe", "清空会话上下文"},
		{"/undo [N]", "回滚 N 步文件更改"}, {"/rewind [N]", "同上（检查点回滚）"},
		{"/diff", "显示 git 工作区差异"}, {"/review [file]", "审查 diff 或指定文件"},
		{"/replay [a] [b]", "会话检查点时间轴 / 对比两点差异（如 /replay 1 3）"},
		{"/apply", "应用已暂存编辑"}, {"/reject", "丢弃已暂存编辑"},
		{"/search <query>", "搜索历史会话"}, {"/export [file]", "导出会话为 Markdown"},
		{"/share [html] [file]", "导出会话为 Markdown 或单文件网页（/share html）"}, {"/copy [file]", "复制最后输出到剪贴板"},
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
		{"/lang [zh-CN|zh-TW|en]", "切换语言"}, {"/login <provider> <key>", "保存并验证 API Key"},
		{"/login oauth <provider>", "订阅账号登录（OAuth）"},
		{"/logout", "清除 API Key"}, {"/feedback", "反馈问题"},
		{"/help", "显示帮助"},
	}
}

func (r Result) withNewSession() Result {
	r.NewSession = true
	r.ClearSession = true
	return r
}

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

// sanitizeConfig masks values of secret-looking keys.
func sanitizeConfig(cfg string) string {
	re := regexp.MustCompile(`(?i)^(\s*(?:key|api_key|apikey|token|secret|password)\s*:\s*).+$`)
	return re.ReplaceAllString(cfg, "$1\"***MASKED***\"")
}

// tailLines keeps the last n lines of s.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
