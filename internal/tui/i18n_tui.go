// Package tui — localized UI strings and slash-command definitions.
package tui

const (
	langZhCN = "zh-CN"
	langZhTW = "zh-TW"
	langEn   = "en"
)

var tuiStrings = map[string]map[string]string{
	langZhCN: {
		"ac.title":       "命令提示",
		"ac.hint":        "Ctrl+P/N 选择 · Tab 补全 · Esc 关闭",
		"banner.hint":    "输入你的需求，或 /help 查看命令。按 Ctrl+C 退出。",
		"welcome.hint":   "输入你的需求开始对话，或 /help 查看全部命令 · Tab 补全 · ↑↓ 历史",
		"welcome.tagline": "你的 AI 编程伙伴",
		"welcome.close":  "按 Esc 或回车关闭欢迎屏 · 输入即开始",
		"welcome.reopen": "欢迎屏仅在空对话时显示，先 /clear 再试",
		"input.hint":     "manual mode on · ? for shortcuts · Enter for agents",
		"input.hint.streaming": "esc 中断生成",
		"cmd.welcome":    "显示/隐藏启动欢迎屏",
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
		"cmd.todo":       "显示会话待办列表",
		"cmd.rewind":     "回滚前 N 步工具调用",
		"cmd.init":       "生成项目 ICODE.md 骨架",
		"cmd.agents":     "列出子 agent",
		"cmd.mcp":        "显示 MCP 服务器状态",
		"cmd.hooks":      "编辑 hooks.yaml 权限规则",
		"cmd.status":     "显示系统状态诊断",
		"cmd.cost":       "显示 Token 与成本明细",
		"cmd.theme":      "设置主题",
		"cmd.lang":       "设置语言",
		"cmd.exit":       "退出 iCode",
		"cmd.ac":         "(输入 / 时) Tab 补全命令 · Ctrl+P/N 选择 · Esc 关闭",
		"cmd.summarize":  "总结当前对话内容",
		"cmd.review":     "审查代码变更，提出改进建议",
		"cmd.security":   "显示/切换安全等级（本地处理 · 脱敏处理 · 本地大模型 · 国外大模型 · 无限制）",
		"cmd.expand":     "展开/折叠工具输出详情",
		"security.usage": "用法: /security [local|desensitize|local-llm|foreign-llm|unrestricted]\n当前等级: %s\n\n等级说明:\n  local          [L] 所有数据仅在本地处理，不调用外部 API\n  desensitize    [D] 发送前脱敏（隐藏身份证/手机号/密钥等）\n  local-llm      [M] 仅允许本地模型（Ollama/Llama.cpp 等）\n  foreign-llm    [G] 允许国外大模型 API\n  unrestricted   [!] 无安全限制\n",
		"security.set":   "安全等级已设为: %s",
		"security.desc":  "安全等级 — 控制数据隐私边界",
	},
	langZhTW: {
		"ac.title":       "指令提示",
		"ac.hint":        "Ctrl+P/N 選擇 · Tab 補全 · Esc 關閉",
		"banner.hint":    "輸入你的需求，或 /help 檢視指令。按 Ctrl+C 退出。",
		"welcome.hint":   "輸入你的需求開始對話，或 /help 檢視全部指令 · Tab 補全 · ↑↓ 歷史",
		"welcome.tagline": "你的 AI 程式設計夥伴",
		"welcome.close":  "按 Esc 或 Enter 關閉歡迎屏 · 輸入即開始",
		"welcome.reopen": "歡迎屏僅在空對話時顯示，先 /clear 再試",
		"input.hint":     "manual mode on · ? for shortcuts · Enter for agents",
		"input.hint.streaming": "esc 中斷生成",
		"cmd.welcome":    "顯示/隱藏啟動歡迎屏",
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
		"cmd.todo":       "顯示工作階段待辦清單",
		"cmd.rewind":     "回滾前 N 步工具呼叫",
		"cmd.init":       "產生專案 ICODE.md 骨架",
		"cmd.agents":     "列出子 agent",
		"cmd.mcp":        "顯示 MCP 伺服器狀態",
		"cmd.hooks":      "編輯 hooks.yaml 權限規則",
		"cmd.status":     "顯示系統狀態診斷",
		"cmd.cost":       "顯示 Token 與費用明細",
		"cmd.theme":      "設定主題",
		"cmd.lang":       "設定語言",
		"cmd.exit":       "退出 iCode",
		"cmd.ac":         "(輸入 / 時) Tab 補全指令 · Ctrl+P/N 選擇 · Esc 關閉",
		"cmd.summarize":  "總結目前對話內容",
		"cmd.review":     "審查程式碼變更，提出改進建議",
		"cmd.security":   "顯示/切換安全等級（本地處理 · 脫敏處理 · 本地大模型 · 國外大模型 · 無限制）",
		"cmd.expand":     "展開/折疊工具輸出詳情",
		"security.usage": "用法: /security [local|desensitize|local-llm|foreign-llm|unrestricted]\n目前等級: %s\n\n等級說明:\n  local          [L] 所有資料僅在本機處理，不呼叫外部 API\n  desensitize    [D] 傳送前脫敏（隱藏身分證/手機號/金鑰等）\n  local-llm      [M] 僅允許本機模型（Ollama/Llama.cpp 等）\n  foreign-llm    [G] 允許國外大模型 API\n  unrestricted   [!] 無安全限制\n",
		"security.set":   "安全等級已設為: %s",
		"security.desc":  "安全等級 — 控制資料隱私邊界",
	},
	langEn: {
		"ac.title":       "Commands",
		"ac.hint":        "Ctrl+P/N to move · Tab to complete · Esc to close",
		"banner.hint":    "Type your request, or /help for commands. Ctrl+C to exit.",
		"welcome.hint":   "Type your request to start, or /help for all commands · Tab to complete · ↑↓ history",
		"welcome.tagline": "your AI coding partner",
		"welcome.close":  "Press Esc or Enter to close · just start typing",
		"welcome.reopen": "Welcome shows only on an empty chat — run /clear first",
		"input.hint":     "manual mode on · ? for shortcuts · Enter for agents",
		"input.hint.streaming": "esc to interrupt",
		"cmd.welcome":    "Show/hide the startup welcome screen",
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
		"cmd.todo":       "Show session todo list",
		"cmd.rewind":     "Rewind N tool steps",
		"cmd.init":       "Generate ICODE.md project skeleton",
		"cmd.agents":     "List sub-agents",
		"cmd.mcp":        "Show MCP server status",
		"cmd.hooks":      "Edit hooks.yaml permission rules",
		"cmd.status":     "Show system diagnostics",
		"cmd.cost":       "Show token & cost breakdown",
		"cmd.theme":      "Set theme",
		"cmd.lang":       "Set language",
		"cmd.exit":       "Exit iCode",
		"cmd.ac":         "(typing / ) Tab to complete · Ctrl+P/N to choose · Esc to close",
		"cmd.summarize":  "Summarize the current conversation",
		"cmd.review":     "Review code changes and suggest improvements",
		"cmd.security":   "Show/switch security level (local · desensitize · local-llm · foreign-llm · unrestricted)",
		"cmd.expand":     "Expand/collapse tool output details",
		"security.usage": "Usage: /security [local|desensitize|local-llm|foreign-llm|unrestricted]\nCurrent level: %s\n\nLevels:\n  local          [L] All data stays local, no external API calls\n  desensitize    [D] PII redacted before sending (IDs/phones/keys)\n  local-llm      [M] Only local models allowed (Ollama/Llama.cpp etc.)\n  foreign-llm    [G] Foreign LLM APIs allowed\n  unrestricted   [!] No security restrictions\n",
		"security.set":   "Security level set to: %s",
		"security.desc":  "Security level — controls data privacy boundary",
	},
}

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
	{"/todo", "cmd.todo"},
	{"/rewind", "cmd.rewind"},
	{"/init", "cmd.init"},
	{"/agents", "cmd.agents"},
	{"/mcp", "cmd.mcp"},
	{"/hooks", "cmd.hooks"},
	{"/status", "cmd.status"},
	{"/cost", "cmd.cost"},
	{"/theme", "cmd.theme"},
	{"/lang", "cmd.lang"},
	{"/welcome", "cmd.welcome"},
	{"/security", "cmd.security"},
	{"/summarize", "cmd.summarize"},
	{"/review", "cmd.review"},
	{"/exit", "cmd.exit"},
	{"/expand", "cmd.expand"},
}

type acItem struct {
	Name string
	Desc string
}

var palettes = map[string]map[string]string{
	"dark": {
		"dim":    "\x1b[90m",
		"bold":   "\x1b[1m",
		"red":    "\x1b[31m",
		"green":  "\x1b[32m",
		"yellow": "\x1b[33m",
		"orange": "\x1b[38;5;208m",
		"blue":   "\x1b[34m",
		"magenta": "\x1b[38;5;205m",
		"purple": "\x1b[38;5;135m",
		"cyan":   "\x1b[36m",
		"white":  "\x1b[37m",
	},
	"light": {
		"dim":    "\x1b[90m",
		"bold":   "\x1b[1m",
		"red":    "\x1b[31m",
		"green":  "\x1b[38;5;28m",
		"yellow": "\x1b[38;5;136m",
		"orange": "\x1b[38;5;166m",
		"blue":   "\x1b[34m",
		"magenta": "\x1b[35m",
		"purple": "\x1b[38;5;129m",
		"cyan":   "\x1b[38;5;30m",
		"white":  "\x1b[30m",
	},
}

func (t *TUI) tstr(key string) string {
	lang := t.lang
	if lang == "" { lang = langZhCN }
	if m, ok := tuiStrings[lang]; ok {
		if v, ok := m[key]; ok && v != "" { return v }
	}
	if m, ok := tuiStrings[langZhCN]; ok {
		if v, ok := m[key]; ok { return v }
	}
	return key
}

func (t *TUI) activeTheme() string {
	if t.theme == "light" { return "light" }
	return "dark"
}

func (t *TUI) c(name string) string {
	if !t.color { return "" }
	if p, ok := palettes[t.activeTheme()]; ok {
		if code, ok := p[name]; ok { return code }
	}
	if p, ok := palettes["dark"]; ok {
		if code, ok := p[name]; ok { return code }
	}
	return ""
}

func (t *TUI) paint(name, text string) string {
	if !t.color { return text }
	return t.c(name) + text + "\x1b[0m"
}
