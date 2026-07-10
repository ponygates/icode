// Package tui — localized UI strings and slash-command definitions.
//
// The TUI keeps its own translation map (independent of the global i18n
// package) so that the interactive terminal UI can render banners, help,
// permission prompts and autocomplete hints in the user's chosen language
// without pulling the whole app/i18n machinery into the raw terminal loop.
package tui

// Supported UI locales for the TUI.
const (
	langZhCN = "zh-CN"
	langZhTW = "zh-TW"
	langEn   = "en"
)

// tuiStrings holds user-facing UI text, keyed by locale.
// zh-CN is the fallback when a key is missing in another locale.
var tuiStrings = map[string]map[string]string{
	langZhCN: {
		"ac.title":       "命令提示",
		"ac.hint":        "Ctrl+P/N 选择 · Tab 补全 · Esc 关闭",
		"banner.hint":    "输入你的需求，或 /help 查看命令。按 Ctrl+C 退出。",
		"perm.title":     "需要授权",
		"perm.allow":     "允许",
		"perm.all":       "本次会话全部允许",
		"perm.deny":      "拒绝",
		"status.gen":     "生成中…",
		"lang.usage":     "用法: /lang <zh-CN|zh-TW|en>",
		"lang.set":       "语言已设为 %s",
		"theme.usage":    "用法: /theme <auto|dark|light>",
		"theme.set":      "主题已设为 %s（已持久化）",
		"sc.title":       "快捷键",
		"sc.shell":       "运行 shell 命令",
		"sc.ctrlc":       "中断 / 退出",
		"sc.ctrll":       "清屏",
		"sc.history":     "历史记录",
		"cmd.help":       "显示本帮助",
		"cmd.model":      "切换模型",
		"cmd.provider":   "切换提供商",
		"cmd.mode":       "切换权限模式",
		"cmd.keys":       "显示各提供商 API Key 状态",
		"cmd.models":     "显示自定义模型",
		"cmd.config":     "显示当前设置",
		"cmd.history":    "显示输入历史",
		"cmd.sessions":   "列出已保存会话",
		"cmd.resume":     "恢复指定会话",
		"cmd.compact":    "压缩上下文（摘要 + 保留近期）",
		"cmd.clear":      "清空当前对话",
		"cmd.export":     "导出对话为 Markdown",
		"cmd.diff":       "显示 git diff",
		"cmd.cost":       "显示 Token 与成本明细",
		"cmd.theme":      "设置主题",
		"cmd.lang":       "设置语言",
		"cmd.exit":       "退出 iCode",
		"cmd.ac":         "(输入 / 时) Tab 补全命令 · Ctrl+P/N 选择 · Esc 关闭",
	},
	langZhTW: {
		"ac.title":       "指令提示",
		"ac.hint":        "Ctrl+P/N 選擇 · Tab 補全 · Esc 關閉",
		"banner.hint":    "輸入你的需求，或 /help 檢視指令。按 Ctrl+C 退出。",
		"perm.title":     "需要授權",
		"perm.allow":     "允許",
		"perm.all":       "本次工作階段全部允許",
		"perm.deny":      "拒絕",
		"status.gen":     "生成中…",
		"lang.usage":     "用法: /lang <zh-CN|zh-TW|en>",
		"lang.set":       "語言已設為 %s",
		"theme.usage":    "用法: /theme <auto|dark|light>",
		"theme.set":      "主題已設為 %s（已儲存）",
		"sc.title":       "快捷鍵",
		"sc.shell":       "執行 shell 指令",
		"sc.ctrlc":       "中斷 / 退出",
		"sc.ctrll":       "清螢幕",
		"sc.history":     "歷史紀錄",
		"cmd.help":       "顯示說明",
		"cmd.model":      "切換模型",
		"cmd.provider":   "切換提供商",
		"cmd.mode":       "切換權限模式",
		"cmd.keys":       "顯示各提供商 API 金鑰狀態",
		"cmd.models":     "顯示自訂模型",
		"cmd.config":     "顯示目前設定",
		"cmd.history":    "顯示輸入歷史",
		"cmd.sessions":   "列出已儲存工作階段",
		"cmd.resume":     "恢復指定工作階段",
		"cmd.compact":    "壓縮上下文（摘要 + 保留近期）",
		"cmd.clear":      "清空目前對話",
		"cmd.export":     "匯出對話為 Markdown",
		"cmd.diff":       "顯示 git diff",
		"cmd.cost":       "顯示 Token 與費用明細",
		"cmd.theme":      "設定主題",
		"cmd.lang":       "設定語言",
		"cmd.exit":       "退出 iCode",
		"cmd.ac":         "(輸入 / 時) Tab 補全指令 · Ctrl+P/N 選擇 · Esc 關閉",
	},
	langEn: {
		"ac.title":       "Commands",
		"ac.hint":        "Ctrl+P/N to move · Tab to complete · Esc to close",
		"banner.hint":    "Type your request, or /help for commands. Ctrl+C to exit.",
		"perm.title":     "Needs approval",
		"perm.allow":     "allow",
		"perm.all":       "allow all this session",
		"perm.deny":      "deny",
		"status.gen":     "generating…",
		"lang.usage":     "usage: /lang <zh-CN|zh-TW|en>",
		"lang.set":       "Language set to %s",
		"theme.usage":    "usage: /theme <auto|dark|light>",
		"theme.set":      "Theme set to %s (saved)",
		"sc.title":       "Shortcuts",
		"sc.shell":       "run a shell command",
		"sc.ctrlc":       "interrupt / exit",
		"sc.ctrll":       "clear screen",
		"sc.history":     "history",
		"cmd.help":       "Show this help",
		"cmd.model":      "Switch model",
		"cmd.provider":   "Switch provider",
		"cmd.mode":       "Switch permission mode",
		"cmd.keys":       "Show API key status per provider",
		"cmd.models":     "Show custom models",
		"cmd.config":     "Show current settings",
		"cmd.history":    "Show input history",
		"cmd.sessions":   "List saved sessions",
		"cmd.resume":     "Resume a session",
		"cmd.compact":    "Compact context (summarize & keep recent)",
		"cmd.clear":      "Clear conversation",
		"cmd.export":     "Export conversation to Markdown",
		"cmd.diff":       "Show git diff",
		"cmd.cost":       "Show token & cost breakdown",
		"cmd.theme":      "Set theme",
		"cmd.lang":       "Set language",
		"cmd.exit":       "Exit iCode",
		"cmd.ac":         "(typing / ) Tab to complete · Ctrl+P/N to choose · Esc to close",
	},
}

// slashDefs is the single source of truth for slash commands. It drives both
// the /help listing and the input autocomplete panel (with localized hints).
var slashDefs = []struct {
	Name string
	Key  string
}{
	{"/help", "cmd.help"},
	{"/model", "cmd.model"},
	{"/provider", "cmd.provider"},
	{"/mode", "cmd.mode"},
	{"/keys", "cmd.keys"},
	{"/models", "cmd.models"},
	{"/config", "cmd.config"},
	{"/history", "cmd.history"},
	{"/sessions", "cmd.sessions"},
	{"/resume", "cmd.resume"},
	{"/compact", "cmd.compact"},
	{"/clear", "cmd.clear"},
	{"/export", "cmd.export"},
	{"/diff", "cmd.diff"},
	{"/cost", "cmd.cost"},
	{"/theme", "cmd.theme"},
	{"/lang", "cmd.lang"},
	{"/exit", "cmd.exit"},
}

// acItem is a single autocomplete entry shown above the input line.
type acItem struct {
	Name string
	Desc string
}

// palettes maps theme name → ANSI color codes for TUI accents. "auto" falls
// back to "dark" (we cannot reliably detect the terminal background in raw
// mode). The light palette uses darker accents readable on light backgrounds.
var palettes = map[string]map[string]string{
	"dark": {
		"dim":    "\x1b[90m",
		"red":    "\x1b[31m",
		"green":  "\x1b[32m",
		"yellow": "\x1b[33m",
		"blue":   "\x1b[34m",
		"cyan":   "\x1b[36m",
		"white":  "\x1b[37m",
	},
	"light": {
		"dim":    "\x1b[90m",
		"red":    "\x1b[31m",
		"green":  "\x1b[38;5;28m",
		"yellow": "\x1b[38;5;136m",
		"blue":   "\x1b[34m",
		"cyan":   "\x1b[38;5;30m",
		"white":  "\x1b[30m",
	},
}

// tstr returns the localized string for the active TUI language, falling back
// to zh-CN and finally to the key itself.
func (t *TUI) tstr(key string) string {
	lang := t.lang
	if lang == "" {
		lang = langZhCN
	}
	if m, ok := tuiStrings[lang]; ok {
		if v, ok := m[key]; ok && v != "" {
			return v
		}
	}
	if m, ok := tuiStrings[langZhCN]; ok {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return key
}

// activeTheme resolves the effective palette theme ("light" only when the
// user explicitly chose it; everything else renders as dark).
func (t *TUI) activeTheme() string {
	if t.theme == "light" {
		return "light"
	}
	return "dark"
}

// c returns the ANSI code for a named accent color in the active theme.
func (t *TUI) c(name string) string {
	if !t.color {
		return ""
	}
	if p, ok := palettes[t.activeTheme()]; ok {
		if code, ok := p[name]; ok {
			return code
		}
	}
	if p, ok := palettes["dark"]; ok {
		if code, ok := p[name]; ok {
			return code
		}
	}
	return ""
}

// paint wraps text in the named accent color (no-op when color is off).
func (t *TUI) paint(name, text string) string {
	if !t.color {
		return text
	}
	return t.c(name) + text + "\x1b[0m"
}
