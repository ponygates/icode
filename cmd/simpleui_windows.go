//go:build windows && !nogui

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jchv/go-webview2"
	"github.com/ponygates/icode/internal/app"
	"github.com/ponygates/icode/internal/config"
	projectcontext "github.com/ponygates/icode/internal/core/context"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/slashui"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/executil"
	"github.com/ponygates/icode/internal/types"
	"github.com/ponygates/icode/internal/xgo"
)

// simpleUIBridge drives the iCode engine and pushes rendered events into the
// WebView2 chat window. It is bound to JS as a handful of global functions
// (window.send / window.models / window.setModel / window.clear / window.runCommand).
type simpleUIBridge struct {
	mu        sync.Mutex
	app       *app.App
	w         webview2.WebView
	model     string
	provider  string
	sessionID string
	lastTool  string
	// thinkingBuf accumulates reasoning deltas (DeepSeek R1 etc.) so they can
	// be folded into a collapsible box instead of being dropped. Truncated to
	// keep the transcript light — shown in the UI, never re-sent to the model.
	thinkingBuf string

	// messages accumulates the visible conversation so commands like
	// /export, /compact, /review, /clear can operate on it (mirrors the CLI).
	messages []simpleMsg
	// curAssistant is the index of the in-progress assistant message in
	// messages, or -1 when no assistant turn is active.
	curAssistant int
}

// simpleMsg is a lightweight conversation record used by the simple UI.
type simpleMsg struct {
	Role    string
	Content string
}

// Models returns the list of selectable model IDs for the <select> control.
func (b *simpleUIBridge) Models() []string {
	if b.app == nil || b.app.Reg == nil {
		return nil
	}
	all := b.app.Reg.ListAllModels()
	ids := make([]string, 0, len(all))
	for _, m := range all {
		if m.Deprecated {
			ids = append(ids, "⚠ "+m.ID)
		} else {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

// SetModel switches the active model (provider is derived from the "p/m" id).
func (b *simpleUIBridge) SetModel(id string) {
	id = strings.TrimPrefix(id, "⚠ ")
	b.mu.Lock()
	b.model = id
	if i := strings.Index(id, "/"); i > 0 {
		b.provider = id[:i]
	}
	b.mu.Unlock()
	b.push(fmt.Sprintf("uiStatus('model', %s)", jsStr(id)))
}

// Send submits a user message and streams the assistant response into the UI.
func (b *simpleUIBridge) Send(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if !b.ensureSession() {
		return
	}
	b.runPrompt(text)
}

// RunCommand is the single input entry point (mirrors the CLI submit prefix
// scan). It dispatches:
//   - "/cmd ..."      → shared slashui command set
//   - "# <content>"   → append to project memory (ICODE.md)
//   - "# user: ..."   → append to user-level memory (~/.icode/)
//   - "! <shell>"     → run a shell command and show the output
//   - anything else   → normal chat message
func (b *simpleUIBridge) RunCommand(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if strings.HasPrefix(text, "/") {
		b.runSlash(text)
		return
	}
	if strings.HasPrefix(text, "!") {
		b.execShell(strings.TrimSpace(strings.TrimPrefix(text, "!")))
		return
	}
	if strings.HasPrefix(text, "#") {
		b.appendMemory(strings.TrimSpace(strings.TrimPrefix(text, "#")))
		return
	}
	b.Send(text)
}

// appendMemory writes a quick memory note, mirroring the CLI's `#` shortcut.
// Plain `# note` targets the project memory file (./ICODE.md); `# user: note`
// targets the cross-project user memory (~/.icode/).
func (b *simpleUIBridge) appendMemory(text string) {
	if text == "" {
		b.sys("用法: # <记到项目 ICODE.md>  |  # user: <记到用户级 ~/.icode>")
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
			b.sys("追加 memory 失败: " + err.Error())
			return
		}
		path, _ := projectcontext.UserMemoryPath()
		b.sys("✓ 已记录到用户级 " + path)
		return
	}
	path, err := projectcontext.AppendProjectMemory(text)
	if err != nil {
		b.sys("追加 memory 失败: " + err.Error())
		return
	}
	b.sys("✓ 已记录到项目 " + path)
}

// execShell runs a shell command and surfaces the output as a tool block,
// mirroring the CLI's `!` shortcut (60s timeout).
func (b *simpleUIBridge) execShell(cmdStr string) {
	if cmdStr == "" {
		return
	}
	b.push(fmt.Sprintf("uiTool(%s, %s)", jsStr("shell"), jsStr(cmdStr)))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = executil.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = executil.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		b.push(fmt.Sprintf("uiAppend('error', %s)", jsStr("shell: "+err.Error())))
		return
	}
	if len(output) == 0 {
		b.push(fmt.Sprintf("uiToolResult(%s)", jsStr("(无输出)")))
		return
	}
	b.push(fmt.Sprintf("uiToolResult(%s)", jsStr(strings.TrimRight(string(output), "\n"))))
}

// Stop cancels the in-flight generation for the active session.
func (b *simpleUIBridge) Stop() {
	b.mu.Lock()
	sid := b.sessionID
	b.mu.Unlock()
	if sid != "" && b.app != nil && b.app.Engine != nil {
		b.app.Engine.Stop(sid)
		b.push("uiBusy(false)")
	}
}

// RefreshModelsUI triggers a live model-catalog refresh from all configured
// providers and returns a short status string for the UI.
func (b *simpleUIBridge) RefreshModelsUI() string {
	if b.app == nil || b.app.Updater == nil {
		return "刷新失败：引擎未初始化"
	}
	results, err := b.app.RefreshModels(context.Background())
	if err != nil {
		return "刷新失败: " + err.Error()
	}
	var sb strings.Builder
	okN, failN := 0, 0
	totalAdded, totalRemoved := 0, 0
	for _, r := range results {
		if r.Success {
			okN++
			sb.WriteString(fmt.Sprintf("  ✓ %s: %d 个模型 (%s)\n", r.Name, r.Count, r.Source))
			if len(r.Added) > 0 || len(r.Removed) > 0 {
				if len(r.Added) > 0 {
					totalAdded += len(r.Added)
					names := make([]string, 0, len(r.Added))
					for _, m := range r.Added {
						if len(names) < 3 {
							names = append(names, m.Name)
						}
					}
					extra := ""
					if len(r.Added) > 3 {
						extra = fmt.Sprintf(" 等%d个", len(r.Added))
					}
					sb.WriteString(fmt.Sprintf("    ➕ 新增: %s%s\n", strings.Join(names, ", "), extra))
				}
				if len(r.Removed) > 0 {
					totalRemoved += len(r.Removed)
					sb.WriteString(fmt.Sprintf("    ⚠️ 下架: %s\n", strings.Join(r.Removed, ", ")))
				}
			}
		} else {
			failN++
			sb.WriteString(fmt.Sprintf("  ✗ %s: %s\n", r.Name, r.Error))
		}
	}
	summary := fmt.Sprintf("模型刷新完成: %d 成功, %d 失败\n", okN, failN)
	if totalAdded > 0 || totalRemoved > 0 {
		summary += fmt.Sprintf("📋 变更: ➕%d 新增, ⚠️%d 下架\n", totalAdded, totalRemoved)
	}
	return summary + sb.String()
}

// ensureSession creates a session if one isn't active yet. Returns false if
// the session store is unavailable.
func (b *simpleUIBridge) ensureSession() bool {
	b.mu.Lock()
	if b.sessionID != "" {
		b.mu.Unlock()
		return true
	}
	model, provider := b.model, b.provider
	b.mu.Unlock()

	if b.app == nil || b.app.SessStore == nil {
		b.push(fmt.Sprintf("uiAppend('system', %s)", jsStr("会话存储不可用。")))
		return false
	}
	sess := &types.Session{
		ID:           fmt.Sprintf("%x", time.Now().UnixNano()),
		ModelID:      model,
		ProviderName: provider,
		Title:        "简易聊天",
	}
	if err := b.app.SessStore.Create(sess); err != nil {
		b.push(fmt.Sprintf("uiAppend('error', %s)", jsStr(fmt.Sprintf("会话错误: %v", err))))
		return false
	}
	b.mu.Lock()
	b.sessionID = sess.ID
	b.mu.Unlock()
	// Refresh the session dropdown so the new session appears, then select it.
	b.push("refreshSessions()")
	b.push(fmt.Sprintf("uiSetSession(%s)", jsStr(sess.ID)))
	return true
}

// appendMsg records a message in the bridge's local transcript.
func (b *simpleUIBridge) appendMsg(role, content string) {
	b.mu.Lock()
	b.messages = append(b.messages, simpleMsg{Role: role, Content: content})
	if role == "assistant" {
		b.curAssistant = len(b.messages) - 1
	} else {
		b.curAssistant = -1
	}
	b.mu.Unlock()
}

// appendAssistantText appends streamed text to the current assistant message,
// creating one if needed.
func (b *simpleUIBridge) appendAssistantText(text string) {
	b.mu.Lock()
	if b.curAssistant < 0 || b.curAssistant >= len(b.messages) || b.messages[b.curAssistant].Role != "assistant" {
		b.messages = append(b.messages, simpleMsg{Role: "assistant", Content: ""})
		b.curAssistant = len(b.messages) - 1
	}
	b.messages[b.curAssistant].Content += text
	b.mu.Unlock()
}

// runPrompt streams a prompt through the engine into the UI and the local
// transcript. Shared by Send and slash commands that need a model turn.
func (b *simpleUIBridge) runPrompt(prompt string) {
	b.appendMsg("user", prompt)
	b.push(fmt.Sprintf("uiAppend('user', %s)", jsStr(prompt)))

	if b.app == nil || b.app.Engine == nil {
		b.push(fmt.Sprintf("uiAppend('system', %s)", jsStr("引擎未初始化，请先配置 API Key。")))
		return
	}

	ctx := context.Background()
	eventCh, err := b.app.Engine.Send(ctx, b.sessionID, prompt)
	if err != nil {
		b.push(fmt.Sprintf("uiAppend('error', %s)", jsStr(fmt.Sprintf("引擎错误: %v", err))))
		b.push("uiBusy(false)")
		return
	}
	b.push("uiBusy(true)")

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.push(fmt.Sprintf("uiAppend('error', %s)", jsStr(fmt.Sprintf("内部错误: %v", r))))
				b.push("uiBusy(false)")
			}
		}()
		for event := range eventCh {
			switch event.Type {
			case types.EventThinking:
				b.mu.Lock()
				b.thinkingBuf += event.Content
				if len(b.thinkingBuf) > 2000 {
					b.thinkingBuf = b.thinkingBuf[:2000]
				}
				b.mu.Unlock()
			case types.EventText:
				b.mu.Lock()
				lt := b.lastTool
				th := b.thinkingBuf
				b.mu.Unlock()
				if th != "" {
					b.push(fmt.Sprintf("uiThinking(%s)", jsStr(th)))
					b.mu.Lock()
					b.thinkingBuf = ""
					b.mu.Unlock()
				}
				if lt != "" {
					b.push(fmt.Sprintf("uiToolResult(%s)", jsStr(strings.TrimSpace(event.Content))))
					b.mu.Lock()
					b.lastTool = ""
					b.mu.Unlock()
				} else {
					b.appendAssistantText(event.Content)
					b.push(fmt.Sprintf("uiDelta(%s)", jsStr(event.Content)))
				}
			case types.EventToolUse:
				b.mu.Lock()
				b.lastTool = event.ToolCall.Name
				b.mu.Unlock()
				args := event.ToolCall.Arguments
				if strings.TrimSpace(args) == "{}" {
					args = ""
				}
				b.push(fmt.Sprintf("uiTool(%s, %s)", jsStr(event.ToolCall.Name), jsStr(args)))
			case types.EventDone:
				b.mu.Lock()
				th := b.thinkingBuf
				b.thinkingBuf = ""
				b.mu.Unlock()
				if th != "" {
					b.push(fmt.Sprintf("uiThinking(%s)", jsStr(th)))
				}
				b.push("uiDone()")
				b.push("uiBusy(false)")
				b.pushStats()
				return
			case types.EventError:
				b.mu.Lock()
				th := b.thinkingBuf
				b.thinkingBuf = ""
				b.mu.Unlock()
				if th != "" {
					b.push(fmt.Sprintf("uiThinking(%s)", jsStr(th)))
				}
				b.push(fmt.Sprintf("uiAppend('error', %s)", jsStr(event.Content)))
				b.push("uiDone()")
				b.push("uiBusy(false)")
				b.pushStats()
				return
			}
		}
	}()
}

// ArchiveCurrent best-effort saves a zero-token summary of the current
// session before the user leaves it (new/open/close), so a later resume has
// context without re-reading the transcript. Failures are swallowed.
func (b *simpleUIBridge) ArchiveCurrent() {
	b.mu.Lock()
	sid := b.sessionID
	b.mu.Unlock()
	if sid == "" || b.app == nil || b.app.SessStore == nil {
		return
	}
	sess, err := b.app.SessStore.Get(sid)
	if err != nil {
		return
	}
	_ = sessionum.Save(b.app.SessStore, sess, sessionum.Generate(sess, b.model, b.provider, ""))
}

// Clear starts a fresh session.
func (b *simpleUIBridge) Clear() {
	b.mu.Lock()
	if b.sessionID != "" && b.app != nil && b.app.SessStore != nil {
		b.app.SessStore.Delete(b.sessionID)
	}
	b.sessionID = ""
	b.messages = nil
	b.curAssistant = -1
	b.mu.Unlock()
	b.push("uiClear()")
	b.pushStats()
}

// SessionEntry is a lightweight session descriptor for the dropdown.
type SessionEntry struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Sessions lists saved sessions (id + title) for the UI dropdown.
func (b *simpleUIBridge) Sessions() []SessionEntry {
	if b.app == nil || b.app.SessStore == nil {
		return nil
	}
	list, err := b.app.SessStore.List(50, 0)
	if err != nil {
		return nil
	}
	out := make([]SessionEntry, 0, len(list))
	for _, s := range list {
		title := s.Title
		if title == "" {
			title = s.ID
		}
		out = append(out, SessionEntry{ID: s.ID, Title: title})
	}
	return out
}

// NewSession detaches from the current session (kept in the store) and starts
// a fresh one — unlike Clear, it does NOT delete the old session.
func (b *simpleUIBridge) NewSession() {
	b.ArchiveCurrent()
	b.mu.Lock()
	b.sessionID = ""
	b.messages = nil
	b.curAssistant = -1
	b.mu.Unlock()
	b.push("uiClear()")
	b.sys("✓ 已开启新会话（旧会话已保存在下拉列表中）。")
	b.pushStats()
}

// OpenSession loads a saved session's messages into the bridge and the UI.
func (b *simpleUIBridge) OpenSession(id string) {
	if b.app == nil || b.app.SessStore == nil {
		return
	}
	b.mu.Lock()
	cur := b.sessionID
	b.mu.Unlock()
	if cur != "" && cur != id {
		b.ArchiveCurrent()
	}
	sess, err := b.app.SessStore.Get(id)
	if err != nil {
		b.sys("打开会话失败: " + err.Error())
		return
	}
	b.mu.Lock()
	b.sessionID = sess.ID
	b.messages = nil
	b.curAssistant = -1
	if sess.ModelID != "" {
		b.model = sess.ModelID
	}
	if sess.ProviderName != "" {
		b.provider = sess.ProviderName
	}
	b.mu.Unlock()
	b.push("uiClear()")
	for _, m := range sess.Messages {
		role := "system"
		switch m.Role {
		case types.RoleUser:
			role = "user"
		case types.RoleAssistant:
			role = "assistant"
		}
		b.appendMsg(role, m.Content)
		b.push(fmt.Sprintf("uiAppend(%s, %s)", jsStr(role), jsStr(m.Content)))
	}
	if sess.ModelID != "" {
		b.push(fmt.Sprintf("uiStatus('model', %s)", jsStr(sess.ModelID)))
	}
	b.push("refreshSessions()")
	b.push(fmt.Sprintf("uiSetSession(%s)", jsStr(sess.ID)))
	b.pushStats()
}

// push schedules a JS snippet to run on the WebView UI thread.
func (b *simpleUIBridge) push(js string) {
	if b.w == nil {
		return
	}
	w := b.w
	w.Dispatch(func() {
		w.Eval(js)
	})
}

// jsStr returns s as a JSON string literal safe to embed in a JS expression.
func jsStr(s string) string {
	v, _ := json.Marshal(s)
	return string(v)
}

// runSlash interprets a CLI-style slash command. The full command vocabulary
// is implemented once in the shared slashui package, so the lightweight UI
// exposes the complete CLI feature set through a single code path.
func (b *simpleUIBridge) runSlash(text string) {
	// /theme is a real UI concept here (the window has a light/dark toggle),
	// so bridge it to the page instead of the CLI-only stub in slashui.
	if parts := strings.Fields(text); len(parts) > 0 && strings.EqualFold(parts[0], "/theme") {
		t := "toggle"
		if len(parts) > 1 {
			t = strings.ToLower(parts[1])
		}
		b.push(fmt.Sprintf("uiTheme(%s)", jsStr(t)))
		return
	}
	// /login & /logout are the key-management entry points (this window has no
	// settings page). With args they persist directly; bare /login opens the
	// in-page key dialog.
	if parts := strings.Fields(text); len(parts) > 0 {
		switch {
		case strings.EqualFold(parts[0], "/login"):
			if len(parts) >= 3 {
				b.sys(b.setKey(parts[1], strings.Join(parts[2:], " ")))
			} else {
				b.push("uiKeyPrompt()")
			}
			return
		case strings.EqualFold(parts[0], "/logout"):
			if len(parts) >= 2 {
				b.sys(b.setKey(parts[1], ""))
			} else {
				b.sys("用法: /logout <provider>（清除该提供商的 API Key）")
			}
			return
		}
	}

	backend := &slashui.Backend{}
	if b.app != nil {
		backend.Engine = b.app.Engine
		backend.SessStore = b.app.SessStore
		backend.Gate = b.app.Gate
		backend.RefreshModels = b.app.RefreshModels
	}

	sec := "local"
	mode := "agent"
	if cfg, err := config.Load(); err == nil {
		if cfg.SecurityLevel != "" {
			sec = string(cfg.SecurityLevel)
		}
		if cfg.Defaults.Mode != "" {
			mode = cfg.Defaults.Mode
		}
	}
	cwd, _ := os.Getwd()
	state := &slashui.State{
		SessionID: b.sessionID,
		Model:     b.model,
		Provider:  b.provider,
		Mode:      mode,
		Security:  sec,
		CWD:       cwd,
		Version:   runningVersion(),
	}

	res := slashui.Execute(context.Background(), backend, state, text)

	if res.NewSession {
		b.NewSession()
	} else if res.ClearSession {
		b.Clear()
	}
	if res.Model != "" && res.Model != b.model {
		b.SetModel(res.Model)
	}
	if res.Provider != "" {
		b.mu.Lock()
		b.provider = res.Provider
		b.mu.Unlock()
	}
	if res.Mode != "" && b.app != nil && b.app.Gate != nil {
		b.app.Gate.SetMode(permission.Mode(res.Mode))
	}
	if res.Security != "" {
		if cfg, err := config.Load(); err == nil {
			cfg.SecurityLevel = config.SecurityLevel(res.Security)
			_ = cfg.Save(config.DefaultPath())
		}
	}

	if res.Chat {
		b.chatTurn(res.Content)
	} else if res.Output != "" {
		b.sys(res.Output)
	}
}

// setKey persists an API key for a provider — the simple-ui key entry point,
// since the lightweight window has no settings page. An empty key clears it.
// Mirrors `icode config key <provider> <apikey>` from the CLI.
func (b *simpleUIBridge) setKey(provider, key string) string {
	provider = strings.TrimSpace(provider)
	key = strings.TrimSpace(key)
	if provider == "" {
		return "用法: /login <provider> <api key>（如 /login deepseek sk-xxx）"
	}
	cfg, err := config.Load()
	if err != nil {
		return "读取配置失败: " + err.Error()
	}
	pc, _ := cfg.Provider(provider)
	pc.APIKey = key
	cfg.SetProvider(provider, pc)
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return "保存配置失败: " + err.Error()
	}
	if b.app != nil && b.app.Reg != nil {
		b.app.Reg.SetCredentials(provider, key, pc.APIBase)
	}
	if key == "" {
		return "已清除 " + provider + " 的 API Key"
	}
	return "已设置 " + provider + " 的 API Key，可用 /doctor 验证连通性。"
}

// chatTurn runs a prompt that needs a model response.
func (b *simpleUIBridge) chatTurn(prompt string) {
	if !b.ensureSession() {
		return
	}
	b.runPrompt(prompt)
}

func (b *simpleUIBridge) sys(s string) {
	b.appendMsg("system", s)
	b.push(fmt.Sprintf("uiAppend('system', %s)", jsStr(s)))
}

// uiStatsPayload is the status-bar payload pushed after each turn.
type uiStatsPayload struct {
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	Mode         string  `json:"mode"`
	Security     string  `json:"security"`
	PromptTokens int     `json:"prompt_tokens"`
	Completion   int     `json:"completion_tokens"`
	Total        int     `json:"total_tokens"`
	CacheHitRate float64 `json:"cache_hit_rate"`
	Cost         float64 `json:"cost"`
}

// statsJSON renders the current session/token/mode/security summary as JSON.
func (b *simpleUIBridge) statsJSON() string {
	var p uiStatsPayload
	b.mu.Lock()
	p.Model, p.Provider = b.model, b.provider
	b.mu.Unlock()
	if b.app != nil && b.app.Gate != nil {
		p.Mode = string(b.app.Gate.Mode())
		p.Security = permission.SecurityLabel(b.app.Gate.SecurityLevel())
	}
	if b.app != nil && b.app.Engine != nil {
		b.mu.Lock()
		sid := b.sessionID
		b.mu.Unlock()
		if sid != "" {
			if s := b.app.Engine.SessionStats(sid); s != nil {
				p.PromptTokens = s.PromptTokens
				p.Completion = s.CompletionTokens
				p.Total = s.TotalTokens
				p.CacheHitRate = s.CacheHitRate
				p.Cost = s.EstimatedCost
			}
		}
	}
	v, err := json.Marshal(&p)
	if err != nil {
		return "{}"
	}
	return string(v)
}

// pushStats refreshes the bottom status bar after a turn completes.
func (b *simpleUIBridge) pushStats() {
	b.push("uiStats(" + b.statsJSON() + ")")
}

// runningVersion returns the running version string.
func runningVersion() string {
	if appVersion == "" {
		return "dev"
	}
	return appVersion
}

// runSimpleUI opens the lightweight WebView2 chat window (the "简易 UI" that a
// double-clicked CLI binary launches). It reuses the same engine as the TUI and
// the desktop app, so the chat behaves identically — just in a native-scrollbar
// window with mouse-wheel scrolling instead of a console. A right-side command
// panel (with a draggable resize slider) exposes the full CLI slash-command set.
func runSimpleUI() error {
	// Redirect stderr (and thus log output) to a file before hiding the
	// console, since FreeConsole() will detach from the console and any
	// subsequent log.Println goes to /dev/null. The WebView2 HTML bridge
	// can also push diagnostic messages to the window, but only AFTER the
	// window is fully initialized — so a file-based log is essential for
	// diagnosing boot-phase hangs ("启动就卡死").
	homeDir, _ := os.UserHomeDir()
	logDir := filepath.Join(homeDir, ".icode")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "simpleui.log")
	// Cap at 2 MB (simpleui is a lighter session) with one backup.
	rw, logErr := xgo.NewRotatingWriter(logPath, 2*1024*1024)
	if logErr == nil {
		log.SetOutput(rw)
		log.Printf("[simpleui] === iCode simple UI starting ===")
	}
	hideConsoleWindow()
	log.Printf("[simpleui] stage: console hidden")

	// go-webview2 requires WebView2 creation AND the message pump (w.Run) to
	// run on ONE OS thread (same thread-affinity issue as the desktop path —
	// splitting them freezes the window and the ~60s Chromium watchdog kills
	// the process). Lock this goroutine so creation + pump stay on one thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Single-instance guard: the simple UI and the desktop app share the same
	// WebView2 user-data dir — two windowed instances at once would contend
	// for its lock and hang startup. The second instance exits with a message.
	release, err := acquireSingleInstance()
	if err != nil {
		showDesktopError("iCode", "iCode 已在运行。\n\n本机已有一个 iCode 窗口，请勿重复启动（避免 WebView2 数据目录被占用导致卡死）。")
		return nil
	}
	defer release()

	a, err := app.Bootstrap()
	if err != nil {
		log.Printf("[simpleui] Bootstrap failed: %v", err)
		showDesktopError("iCode", "启动失败: "+err.Error())
		return err
	}
	defer a.Close()
	log.Printf("[simpleui] stage: Bootstrap done")

	cfg, _ := config.Load()
	model := "openrouter/free"
	provider := "openrouter"
	if cfg != nil {
		if cfg.Defaults.Model != "" {
			model = cfg.Defaults.Model
		}
		if cfg.Defaults.Provider != "" {
			provider = cfg.Defaults.Provider
		}
	}
	// Auto-approve tool calls so the simple UI never blocks on a permission
	// prompt (it has no terminal to show one). Mirrors the desktop agent flow.
	if a.Engine != nil {
		a.Engine.SetPermissionHandler(func(sessionID string, req *types.PermissionReq, res permission.CheckResult) permission.Decision {
			return permission.DecisionAllow
		})
	}

	cache, _ := os.UserCacheDir()
	dataPath := filepath.Join(cache, "icode", "webview")

	// Same zombie-lock guard as the desktop path: kill orphaned WebView2
	// processes before they deadlock runtime creation on the data-dir lock,
	// then heal an oversized/unreadable dir so creation never hangs on it.
	killStaleWebViewProcesses(dataPath)
	healWebViewDataDir(dataPath)
	log.Printf("[simpleui] stage: zombie cleanup done, creating window")

	// Watchdog: creation is synchronous on this locked thread (WebView2 is
	// thread-affine, so it can't be moved into a timeout goroutine). The heal
	// above makes hangs unlikely; if one still happens, this logs where boot
	// stalled without freezing anything else.
	initDone := make(chan struct{})
	go func() {
		select {
		case <-initDone:
		case <-time.After(15 * time.Second):
			log.Printf("[simpleui] WARNING: WebView2 creation still in progress after 15s")
		}
	}()

	w := tryInitWebView(dataPath, "", 1100, 680)
	if w == nil {
		log.Printf("[simpleui] WebView2 failed on primary, wiping and retrying %s", dataPath)
		resetWebViewDataDir(dataPath)
		w = tryInitWebView(dataPath, "", 1100, 680)
	}
	if w == nil {
		fallback := dataPath + "-R"
		_ = os.RemoveAll(fallback)
		killStaleWebViewProcesses(fallback)
		w = tryInitWebView(fallback, "", 1100, 680)
	}
	if w == nil {
		log.Printf("[simpleui] WebView2 failed on fallback, retrying with ephemeral profile")
		w = tryInitWebView("", "", 1100, 680)
	}
	close(initDone)
	if w == nil {
		showDesktopError("iCode",
			"无法初始化原生窗口（WebView2 运行时未安装）。\n\n"+
				"iCode 简易界面使用 Windows 原生 WebView2 控件渲染。\n"+
				"请安装 Microsoft Edge WebView2 运行时后重试\n"+
				"（Windows 10/11 通常已内置）：\n\n"+
				"https://developer.microsoft.com/zh-cn/microsoft-edge/webview2/")
		return fmt.Errorf("webview2 init failed")
	}

	b := &simpleUIBridge{app: a, w: w, model: model, provider: provider, curAssistant: -1}
	w.Bind("send", func(text string) { b.Send(text) })
	w.Bind("models", func() []string { return b.Models() })
	w.Bind("setModel", func(id string) { b.SetModel(id) })
	w.Bind("clear", func() { b.Clear() })
	w.Bind("runCommand", func(text string) { b.RunCommand(text) })
	w.Bind("stop", func() { b.Stop() })
	w.Bind("stats", func() string { return b.statsJSON() })
	w.Bind("refreshModels", func() string { return b.RefreshModelsUI() })
	w.Bind("sessions", func() []SessionEntry { return b.Sessions() })
	w.Bind("openSession", func(id string) { b.OpenSession(id) })
	w.Bind("newSession", func() { b.NewSession() })
	w.Bind("setKey", b.setKey)
	w.SetHtml(simpleUIHTML(model, provider))
	log.Printf("[simpleui] stage: bridge wired, HTML loaded, entering message pump")
	w.Run()
	log.Printf("[simpleui] stage: window closed")
	b.ArchiveCurrent()
	a.Close()
	os.Exit(0)
	return nil
}

// simpleUIHTML returns the self-contained page for the lightweight chat UI:
// a scrollable transcript (native scrollbar + mouse wheel), a model selector,
// a clear button, an input box, and a collapsible right-side command panel with
// a draggable resize slider.
func simpleUIHTML(model, provider string) string {
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>iCode — 简易聊天</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  html, body { margin: 0; height: 100%; }
  body {
    font: 14px/1.6 -apple-system, "Segoe UI", "Microsoft YaHei", system-ui, sans-serif;
    background: #0f1115; color: #e6e6e6; display: flex; flex-direction: row; height: 100vh; overflow: hidden;
  }
  #main { display: flex; flex-direction: column; flex: 1 1 auto; min-width: 0; height: 100%; }
  #bar {
    display: flex; align-items: center; gap: 10px; padding: 8px 12px;
    background: #161922; border-bottom: 1px solid #262b36; flex: 0 0 auto;
  }
  #bar .logo { color: #ff7a45; font-weight: 700; }
  #bar select {
    background: #0f1115; color: #e6e6e6; border: 1px solid #313846;
    border-radius: 6px; padding: 4px 8px; max-width: 320px;
  }
  #bar button {
    background: #1f2530; color: #e6e6e6; border: 1px solid #313846;
    border-radius: 6px; padding: 4px 10px; cursor: pointer;
  }
  #bar button:hover { background: #2a3140; }
  #bar .spacer { flex: 1; }
  #log {
    flex: 1 1 auto; overflow-y: auto; padding: 14px 16px; scrollbar-width: thin;
    scrollbar-color: #3a4151 #0f1115;
  }
  #log::-webkit-scrollbar { width: 12px; }
  #log::-webkit-scrollbar-track { background: #0f1115; }
  #log::-webkit-scrollbar-thumb { background: #3a4151; border-radius: 8px; border: 3px solid #0f1115; }
  #log::-webkit-scrollbar-thumb:hover { background: #4a5366; }
  .msg { position: relative; margin: 0 0 12px; padding: 10px 12px; border-radius: 10px; white-space: pre-wrap; word-break: break-word; max-width: 92%; }
  .user { background: #1d3a5f; margin-left: auto; }
  .assistant { background: #1a1f29; border: 1px solid #262b36; }
  .system { background: #20242e; color: #9aa4b2; font-size: 13px; }
  .error { background: #3a1d1d; color: #ffb4b4; border: 1px solid #5a2a2a; }
  .tool { background: #16202a; border: 1px solid #223; color: #9ecbff; font-size: 13px; }
  .tool .name { font-weight: 700; color: #7fd1ff; }
  .tool pre { margin: 6px 0 0; white-space: pre-wrap; word-break: break-word; color: #c7d2e0; }
  .thinking { background: #1c2026; border: 1px dashed #334; color: #9aa4b2; font-size: 12px; }
  .thinking summary { cursor: pointer; color: #9ecbff; font-weight: 600; }
  .thinking pre { margin: 6px 0 0; white-space: pre-wrap; word-break: break-word; color: #8a93a3; }
  .role { font-size: 11px; color: #6b7484; margin-bottom: 3px; }
  #inputbar { display: flex; gap: 8px; padding: 10px 12px; border-top: 1px solid #262b36; background: #161922; flex: 0 0 auto; }
  #inp {
    flex: 1; resize: none; height: 42px; background: #0f1115; color: #e6e6e6;
    border: 1px solid #313846; border-radius: 8px; padding: 10px 12px; font: inherit;
  }
  #send { background: #ff7a45; color: #1a1205; border: none; border-radius: 8px; padding: 0 18px; font-weight: 700; cursor: pointer; }
  #send:hover { background: #ff9166; }
  #stopBtn { background: #b53a3a; color: #fff; border: none; border-radius: 8px; padding: 0 16px; font-weight: 700; cursor: pointer; }
  #stopBtn:hover { background: #cf4a4a; }
  /* Bottom status bar: model/provider/mode/security/tokens/cache/cost */
  #status {
    flex: 0 0 auto; padding: 5px 12px; font-size: 12px; color: #6b7484;
    background: #12151c; border-top: 1px solid #262b36; white-space: nowrap;
    overflow-x: auto; scrollbar-width: none;
  }
  #status::-webkit-scrollbar { display: none; }
  /* Right command panel + draggable slider */
  #grip {
    flex: 0 0 6px; cursor: col-resize; background: #262b36;
    transition: background .15s;
  }
  #grip:hover, #grip.dragging { background: #ff7a45; }
  #side {
    flex: 0 0 280px; width: 280px; min-width: 180px; max-width: 60%;
    background: #12151c; border-left: 1px solid #262b36; display: flex; flex-direction: column;
    height: 100%;
  }
  #side .side-head { padding: 10px 12px; font-weight: 700; color: #ff7a45; border-bottom: 1px solid #262b36; flex: 0 0 auto; }
  #side .side-list { overflow-y: auto; padding: 8px; scrollbar-width: thin; scrollbar-color: #3a4151 #12151c; flex: 1 1 auto; }
  #side .side-list::-webkit-scrollbar { width: 10px; }
  #side .side-list::-webkit-scrollbar-thumb { background: #3a4151; border-radius: 8px; border: 2px solid #12151c; }
  #side .grp { color: #6b7484; font-size: 12px; margin: 10px 4px 4px; }
  #side .cmd {
    padding: 5px 8px; margin: 2px 0; border-radius: 6px; cursor: pointer; color: #cdd6e2;
    font-family: ui-monospace, "Cascadia Code", Consolas, monospace; font-size: 13px;
  }
  #side .cmd:hover { background: #1f2530; color: #fff; }
  #side .collapse { background: #1f2530; color: #e6e6e6; border: 1px solid #313846; border-radius: 6px; padding: 2px 8px; cursor: pointer; }
  /* Markdown rendering inside chat bubbles */
  .content { line-height: 1.55; }
  .md-h { display: block; font-weight: 700; margin: 6px 0 2px; color: #ffd9c2; }
  .md-h1 { font-size: 1.15em; } .md-h2 { font-size: 1.08em; } .md-h3, .md-h4, .md-h5, .md-h6 { font-size: 1em; }
  .md-quote { border-left: 3px solid #3a4151; padding: 2px 10px; color: #b9c2d0; margin: 4px 0; }
  .md-hr { border: none; border-top: 1px solid #2a3140; margin: 8px 0; }
  .md-ul, .md-ol { margin: 4px 0; padding-left: 22px; }
  .md-p { margin: 2px 0; }
  .md-codewrap { position: relative; margin: 6px 0; }
  .md-copy { position: absolute; top: 6px; right: 6px; background: #1f2530; color: #cdd6e2; border: 1px solid #313846; border-radius: 4px; font-size: 11px; padding: 2px 8px; cursor: pointer; }
  .md-copy:hover { background: #2a3140; color: #fff; }
  .md-code { background: #0b0d12; border: 1px solid #262b36; border-radius: 6px; padding: 8px 10px; overflow-x: auto; margin: 0; }
  .md-code code { font-family: ui-monospace, "Cascadia Code", Consolas, monospace; font-size: 12.5px; color: #c7d2e0; white-space: pre; }
  .md-icode { background: #0b0d12; border: 1px solid #262b36; border-radius: 4px; padding: 0 4px; font-family: ui-monospace, Consolas, monospace; font-size: 12.5px; color: #9ee7c7; }
  .theme-btn { background: transparent; border: 1px solid #313846; border-radius: 6px; padding: 4px 8px; cursor: pointer; font-size: 15px; line-height: 1; }
  .theme-btn:hover { background: #2a3140; }
  .content a { color: #ff9d6e; }
  .content del { text-decoration: line-through; opacity: 0.6; }
  .md-task { display: flex; align-items: baseline; gap: 6px; padding: 1px 0; }
  .md-chk { display: inline-flex; align-items: center; justify-content: center; width: 16px; height: 16px; border-radius: 4px; border: 1px solid #3a4151; font-size: 10px; flex-shrink: 0; color: transparent; }
  .md-chk.checked { border-color: #4caf50; background: #4caf50; color: #fff; }
  .md-task-text.done { text-decoration: line-through; color: #6b7484; }
  .md-table-wrap { overflow-x: auto; margin: 6px 0; }
  .md-table { border-collapse: collapse; width: 100%; font-size: 12.5px; }
  .md-table th { padding: 6px 10px; text-align: left; border-bottom: 2px solid #3a4151; color: #ffd9c2; font-weight: 600; }
  .md-table td { padding: 4px 10px; border-bottom: 1px solid #262b36; color: #b9c2d0; }
  .msg-copy { position: absolute; top: 6px; right: 6px; background: #1f2530; color: #cdd6e2; border: 1px solid #313846; border-radius: 4px; font-size: 11px; padding: 2px 8px; cursor: pointer; opacity: 0; transition: opacity 0.15s; }
  .msg:hover .msg-copy { opacity: 1; }
  .msg-copy:hover { background: #2a3140; color: #fff; }
  /* Light theme */
  html.light, html.light body { background: #f7f7f9; color: #1a1a1f; }
  html.light #bar { background: #fff; border-color: #e0e0e5; }
  html.light #bar select, html.light #bar button { background: #f4f4f7; color: #1a1a1f; border-color: #d0d0da; }
  html.light #log { background: #f7f7f9; scrollbar-color: #c5c5ce #f7f7f9; }
  html.light #log::-webkit-scrollbar-thumb { background: #c5c5ce; }
  html.light .msg { border-radius: 10px; }
  html.light .user { background: #dae8fc; }
  html.light .assistant { background: #fff; border-color: #e0e0e5; }
  html.light .system { background: #eeeef2; color: #6b6b7a; }
  html.light .error { background: #fce0e0; color: #b52424; border-color: #f0c0c0; }
  html.light .tool { background: #eff6ff; border-color: #d0daf0; color: #2a4a7a; }
  html.light .tool .name { color: #1a5acc; }
  html.light .tool pre { color: #3a3a4a; }
  html.light .thinking { background: #f4f6f8; border-color: #cfd6e2; color: #5a6270; }
  html.light .thinking summary { color: #1a5acc; }
  html.light .thinking pre { color: #5a6270; }
  html.light #inputbar { background: #fff; border-color: #e0e0e5; }
  html.light #inp { background: #f7f7f9; color: #1a1a1f; border-color: #d0d0da; }
  html.light #send { background: #ff7a45; color: #fff; }
  html.light #stopBtn { background: #c0392b; }
  html.light #status { background: #f4f4f7; border-color: #e0e0e5; color: #888; }
  html.light #side { background: #f4f4f7; border-color: #e0e0e5; }
  html.light #grip { background: #d0d0da; }
  html.light #grip:hover { background: #ff7a45; }
  html.light #side .cmd { color: #3a3a4a; }
  html.light #side .cmd:hover { background: #e8e8ee; color: #000; }
  html.light .theme-btn { border-color: #d0d0da; color: #333; }
  html.light .theme-btn:hover { background: #e8e8ee; }
  html.light #panelBtn { border-color: #d0d0da; color: #333; }
  html.light #panelBtn:hover { background: #e8e8ee; }
  html.light .md-h { color: #c45a00; }
  html.light .md-quote { border-color: #d0d0da; color: #6b6b7a; }
  html.light .md-hr { border-color: #d0d0da; }
  html.light .md-code { background: #f0f0f4; border-color: #d0d0da; }
  html.light .md-code code { color: #3a3a4a; }
  html.light .md-icode { background: #f0f0f4; border-color: #d0d0da; color: #1a5acc; }
  html.light .content a { color: #1a5acc; }
  html.light .content del { color: #999; }
  html.light .md-chk { border-color: #d0d0da; }
  html.light .md-chk.checked { border-color: #4caf50; background: #4caf50; }
  html.light .md-task-text.done { color: #aaa; }
  html.light .md-table th { border-color: #d0d0da; color: #c45a00; }
  html.light .md-table td { border-color: #e0e0e5; color: #3a3a4a; }
  html.light .msg-copy { background: #eee; border-color: #d0d0da; color: #666; }
  html.light .msg-copy:hover { background: #e0e0e5; }
  html.light .md-copy { background: #eee; border-color: #d0d0da; color: #666; }
  html.light .md-copy:hover { background: #e0e0e5; }
</style></head>
<body>
  <div id="main">
    <div id="bar">
      <span class="logo">iCode</span>
      <select id="session" title="切换会话"></select>
      <button id="newBtn" title="开启新会话（旧会话保留在下拉列表中）">新会话</button>
      <select id="model" title="模型"></select>
      <button id="updateBtn" title="一键刷新所有提供商模型列表" class="theme-btn" style="font-size:13px;">↻</button>
      <span class="spacer"></span>
      <button id="themeBtn" title="切换主题" class="theme-btn">☀</button>
      <button id="panelBtn" title="显示/隐藏命令面板" class="theme-btn" style="font-size:13px;">☰</button>
      <button id="clearBtn" title="清空并删除当前会话">清空</button>
    </div>
    <div id="log"></div>
    <div id="status" title="模型 / 提供商 / 模式 / 安全等级 / Token / 缓存 / 费用"></div>
    <div id="inputbar">
      <textarea id="inp" placeholder="输入消息或 /命令，Enter 发送，Shift+Enter 换行…"></textarea>
      <button id="stopBtn" title="停止生成" style="display:none;">■ 停止</button>
      <button id="send">发送</button>
    </div>
  </div>
  <div id="grip" title="拖动调整命令面板宽度"></div>
  <div id="side">
    <div class="side-head">命令面板 <button class="collapse" id="collapseBtn" title="折叠/展开">⟨</button></div>
    <div class="side-list" id="sideList"></div>
  </div>
  <div id="keyModal" style="display:none; position:fixed; inset:0; background:rgba(0,0,0,0.5); z-index:999; align-items:center; justify-content:center;">
    <div style="background:#171a21; border:1px solid #2a2e3a; border-radius:10px; padding:18px; width:360px;">
      <div style="font-weight:600; margin-bottom:4px;">配置 API Key</div>
      <div style="font-size:12px; color:#888; margin-bottom:10px;">简易界面没有设置页，在这里粘贴提供商密钥（如 deepseek / openrouter / zhipu）</div>
      <input id="keyProvider" placeholder="提供商，如 deepseek" style="width:100%; box-sizing:border-box; padding:7px 9px; border-radius:6px; border:1px solid #2a2e3a; background:#0f1115; color:#e6e6e6; margin-bottom:8px;"/>
      <input id="keyValue" type="password" placeholder="API Key" style="width:100%; box-sizing:border-box; padding:7px 9px; border-radius:6px; border:1px solid #2a2e3a; background:#0f1115; color:#e6e6e6; margin-bottom:12px;"/>
      <div style="display:flex; gap:8px; justify-content:flex-end;">
        <button onclick="document.getElementById('keyModal').style.display='none';" style="padding:6px 14px; border-radius:6px; border:1px solid #2a2e3a; background:transparent; color:#aaa; cursor:pointer;">取消</button>
        <button onclick="keySave()" style="padding:6px 14px; border-radius:6px; border:none; background:#4f6ef7; color:#fff; cursor:pointer;">保存</button>
      </div>
    </div>
  </div>
<script>
  var log = document.getElementById('log');
  var inp = document.getElementById('inp');
  var current = null;
  // Smart auto-scroll: only follow the stream to the bottom when the user is
  // already there. If they scroll up to read history, don't yank them back.
  var atBottom = true;
  log.addEventListener('scroll', function () {
    atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 40;
  });
  function stick() { if (atBottom) log.scrollTop = log.scrollHeight; }
  function scrollDown() { stick(); }

  function escapeHtml(s) {
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }
  function inlineMd(s) {
    s = s.replace(/\u0060([^\u0060]+)\u0060/g, '<code class="md-icode">$1</code>');
    s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
    s = s.replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>');
    s = s.replace(/~~([^~]+)~~/g, '<del>$1</del>');
    s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
    s = s.replace(/(^|[^"=\/])(https?:\/\/[^\s<>"{}|]+)/g, function(m, pre, url) {
      return pre + '<a href="' + url + '" target="_blank" rel="noopener noreferrer">' + url + '</a>';
    });
    return s;
  }
  // Lightweight Markdown → HTML for the chat bubbles. Input is first escaped,
  // so model output can never inject scripts. Block elements rely on the
  // .msg white-space:pre-wrap to preserve paragraph line breaks.
  function renderMarkdownHTML(src) {
    var esc = escapeHtml(src);
    var blocks = [];
    esc = esc.replace(/\u0060\u0060\u0060(\w*)\n?([\s\S]*?)\u0060\u0060\u0060/g, function (_, lang, code) {
      var i = blocks.length;
      blocks.push('<div class="md-codewrap"><button class="md-copy" type="button">复制</button><pre class="md-code"><code>' + code.replace(/\n$/, '') + '</code></pre></div>');
      return ' CB' + i + ' ';
    });
    var lines = esc.split('\n');
    var out = [];
    var listType = null;
    function closeList() { if (listType) { out.push('</' + listType + '>'); listType = null; } }
    for (var i = 0; i < lines.length; i++) {
      var ln = lines[i];
      var cb = /^ CB(\d+) $/.exec(ln);
      if (cb) { closeList(); out.push(blocks[+cb[1]]); continue; }
      var h = /^(#{1,6})\s+(.*)$/.exec(ln);
      if (h) { closeList(); out.push('<span class="md-h md-h' + h[1].length + '">' + inlineMd(h[2]) + '</span>'); continue; }
      if (/^(---|\*\*\*|___)\s*$/.test(ln)) { closeList(); out.push('<hr class="md-hr">'); continue; }
      if (/^&gt;\s?/.test(ln)) { closeList(); out.push('<div class="md-quote">' + inlineMd(ln.replace(/^&gt;\s?/, '')) + '</div>'); continue; }
      var task = /^[-*+]\s+\[([ xX])\]\s+(.*)$/.exec(ln);
      if (task) { closeList(); var chk = task[1] !== ' '; out.push('<div class="md-task"><span class="md-chk' + (chk ? ' checked' : '') + '">' + (chk ? '✓' : '') + '</span><span class="md-task-text' + (chk ? ' done' : '') + '">' + inlineMd(task[2]) + '</span></div>'); continue; }
      var ul = /^[-*+]\s+(.*)$/.exec(ln);
      if (ul) { if (listType !== 'ul') { closeList(); out.push('<ul class="md-ul">'); listType = 'ul'; } out.push('<li>' + inlineMd(ul[1]) + '</li>'); continue; }
      var ol = /^(\d+)\.\s+(.*)$/.exec(ln);
      if (ol) { if (listType !== 'ol') { closeList(); out.push('<ol class="md-ol">'); listType = 'ol'; } out.push('<li>' + inlineMd(ol[2]) + '</li>'); continue; }
      if (/^\|.+\|$/.test(ln.trim())) {
        closeList();
        var trows = [];
        while (i < lines.length && /^\|.+\|$/.test(lines[i].trim())) { trows.push(lines[i]); i++; }
        i--;
        if (trows.length >= 2) {
          var th = trows[0].replace(/^\|/, '').replace(/\|$/, '').split('|').map(function(c){return c.trim();});
          var sepI = 1;
          if (trows.length > 1 && trows[1].replace(/^\|/, '').replace(/\|$/, '').split('|').every(function(c){return /^:?-+:?$/.test(c.trim());})) sepI = 2;
          out.push('<div class="md-table-wrap"><table class="md-table"><thead><tr>');
          th.forEach(function(c){ out.push('<th>' + inlineMd(c) + '</th>'); });
          out.push('</tr></thead><tbody>');
          for (var ri = sepI; ri < trows.length; ri++) {
            var cells = trows[ri].replace(/^\|/, '').replace(/\|$/, '').split('|').map(function(c){return c.trim();});
            out.push('<tr>'); cells.forEach(function(c){ out.push('<td>' + inlineMd(c) + '</td>'); }); out.push('</tr>');
          }
          out.push('</tbody></table></div>');
        }
        continue;
      }
      if (ln.trim() === '') { closeList(); continue; }
      closeList();
      out.push('<div class="md-p">' + inlineMd(ln) + '</div>');
    }
    closeList();
    return out.join('');
  }

  function addBlock(role, text, useMd) {
    var d = document.createElement('div');
    d.className = 'msg ' + role;
    if (role === 'assistant') {
      var cp = document.createElement('button'); cp.className = 'msg-copy'; cp.type = 'button'; cp.textContent = '复制';
      cp.addEventListener('click', function() {
        var raw = d.__raw || text || '';
        navigator.clipboard.writeText(raw).then(function() { cp.textContent = '✓ 已复制'; setTimeout(function() { cp.textContent = '复制'; }, 1500); }).catch(function(){});
      });
      d.appendChild(cp);
    }
    if (role === 'user') {
      var r = document.createElement('div'); r.className = 'role'; r.textContent = '你';
      d.appendChild(r);
    }
    var c = document.createElement('div'); c.className = 'content';
    c.innerHTML = useMd ? renderMarkdownHTML(text) : escapeHtml(text || '');
    d.appendChild(c);
    log.appendChild(d); stick(); return d;
  }

  function uiAppend(role, text) { current = null; addBlock(role, text, role === 'user' || role === 'system'); }
  function uiDelta(text) {
    if (!current) { current = addBlock('assistant', '', false); current.__raw = ''; }
    current.__raw += text;
    current.querySelector('.content').textContent = current.__raw;
    stick();
  }
  function uiThinking(text) {
    current = null;
    var d = document.createElement('div'); d.className = 'msg thinking';
    var s = document.createElement('summary'); s.textContent = '🧠 推理过程';
    var p = document.createElement('pre'); p.textContent = text;
    var details = document.createElement('details'); details.appendChild(s); details.appendChild(p);
    d.appendChild(details);
    log.appendChild(d); stick();
  }
  function uiTool(name, args) {
    current = null;
    var d = document.createElement('div'); d.className = 'msg tool';
    var n = document.createElement('div'); n.className = 'name'; n.textContent = '⏺ ' + name;
    d.appendChild(n);
    if (args) { var p = document.createElement('pre'); p.textContent = args; d.appendChild(p); }
    log.appendChild(d); stick();
  }
  function uiToolResult(text) {
    var blocks = log.querySelectorAll('.msg.tool');
    var last = blocks[blocks.length - 1];
    if (last) { var p = document.createElement('pre'); p.textContent = text; last.appendChild(p); }
    stick();
  }
  function uiDone() {
    if (current && current.__raw !== undefined) {
      current.querySelector('.content').innerHTML = renderMarkdownHTML(current.__raw);
    }
    current = null;
  }
  function uiClear() { log.innerHTML = ''; current = null; atBottom = true; }
  function uiStatus(kind, val) { if (kind === 'model') { var s = document.getElementById('model'); if (s) s.value = val; } }
  function uiSetSession(id) { var s = document.getElementById('session'); if (s) s.value = id; }

  // Busy state toggles the stop button in place of the send button.
  var sendBtn = document.getElementById('send');
  var stopBtn = document.getElementById('stopBtn');
  function uiBusy(b) {
    if (b) { stopBtn.style.display = ''; sendBtn.style.display = 'none'; }
    else { stopBtn.style.display = 'none'; sendBtn.style.display = ''; }
  }

  // Status bar rendering (model/provider/mode/security/tokens/cache/cost).
  function fmtTok(n) { n = n || 0; return n >= 1000 ? (n/1000).toFixed(1) + 'k' : '' + n; }
  function uiStats(s) {
    var el = document.getElementById('status'); if (!el) return;
    var parts = [];
    if (s.model) parts.push('模型 ' + s.model);
    if (s.provider) parts.push('提供商 ' + s.provider);
    if (s.mode) parts.push('模式 ' + s.mode);
    if (s.security) parts.push('安全 ' + s.security);
    if (s.total !== undefined && s.total > 0) parts.push('↑' + fmtTok(s.prompt_tokens) + ' ↓' + fmtTok(s.completion_tokens) + ' = ' + fmtTok(s.total));
    if (s.cache_hit_rate > 0) parts.push('缓存 ' + Math.round(s.cache_hit_rate * 100) + '%');
    if (s.cost > 0) parts.push('¥' + s.cost.toFixed(4));
    el.textContent = parts.join('  ·  ');
  }

  function doSend() {
    var t = inp.value.trim(); if (!t) return;
    inp.value = '';
    if (window.runCommand) window.runCommand(t);
  }

  document.getElementById('send').addEventListener('click', doSend);
  document.getElementById('clearBtn').addEventListener('click', function(){ if (window.clear) window.clear(); });
  stopBtn.addEventListener('click', function(){ if (window.stop) window.stop(); });
  inp.addEventListener('keydown', function(e){
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); doSend(); }
  });

  // Command catalog — rendered into the right panel. Only lists commands that
  // actually work in this UI (CLI-terminal-only commands like /vim /history
  // are excluded).
  var CATALOG = [
    {g:'会话', c:['/clear','/new','/sessions','/resume','/compact','/export','/share','/diff','/review','/summarize','/undo']},
    {g:'模型', c:['/model','/provider','/models','/keys','/update']},
    {g:'配置', c:['/config','/theme','/lang','/security','/permissions','/mcp','/output-style']},
    {g:'工具', c:['/init','/add-dir','/agents','/skills','/teams','/hooks','/todo']},
    {g:'信息', c:['/help','/whoami','/status','/cost','/doctor','/memory','/context','/feedback']},
    {g:'系统', c:['/login','/logout','/release-notes','/bug']}
  ];
  var sideList = document.getElementById('sideList');
  CATALOG.forEach(function(group){
    var h = document.createElement('div'); h.className = 'grp'; h.textContent = group.g;
    sideList.appendChild(h);
    group.c.forEach(function(name){
      var d = document.createElement('div'); d.className = 'cmd'; d.textContent = name;
      d.title = '执行 ' + name;
      d.addEventListener('click', function(){ if (window.runCommand) window.runCommand(name); });
      sideList.appendChild(d);
    });
  });

  // Draggable resize slider for the command panel.
  var grip = document.getElementById('grip');
  var side = document.getElementById('side');
  var dragging = false;
  grip.addEventListener('mousedown', function(e){ dragging = true; grip.classList.add('dragging'); e.preventDefault(); });
  document.addEventListener('mouseup', function(){ dragging = false; grip.classList.remove('dragging'); });
  document.addEventListener('mousemove', function(e){
    if (!dragging) return;
    var total = document.body.clientWidth;
    var w = total - e.clientX;
    if (w < 180) w = 180;
    if (w > total * 0.6) w = total * 0.6;
    side.style.flex = '0 0 ' + w + 'px';
    side.style.width = w + 'px';
  });
  // Collapse / expand the panel.
  var collapsed = false;
  function togglePanel() {
    collapsed = !collapsed;
    if (collapsed) { side.style.display = 'none'; grip.style.display = 'none'; document.getElementById('collapseBtn').textContent = '⟩'; }
    else { side.style.display = 'flex'; grip.style.display = 'block'; document.getElementById('collapseBtn').textContent = '⟨'; }
  }
  document.getElementById('collapseBtn').addEventListener('click', togglePanel);
  document.getElementById('panelBtn').addEventListener('click', togglePanel);

  // Populate the model selector from the engine.
  if (window.models) {
    window.models().then(function(list){
      var s = document.getElementById('model');
      (list || []).forEach(function(id){
        var o = document.createElement('option'); o.value = id; o.textContent = id; s.appendChild(o);
      });
      s.value = ` + jsStr(model) + `;
    }).catch(function(){});
  }
  document.getElementById('model').addEventListener('change', function(e){
    if (window.setModel) window.setModel(e.target.value);
  });

  // Refresh all provider model catalogs.
  document.getElementById('updateBtn').addEventListener('click', function(){
    var btn = this;
    btn.disabled = true; btn.textContent = '…';
    if (window.refreshModels) {
      window.refreshModels().then(function(result){
        addBlock('system', result, true);
        // Re-populate model dropdown
        if (window.models) {
          window.models().then(function(list){
            var s = document.getElementById('model');
            var cur = s.value;
            s.innerHTML = '';
            (list || []).forEach(function(id){
              var o = document.createElement('option'); o.value = id; o.textContent = id; s.appendChild(o);
            });
            if (cur) s.value = cur;
          }).catch(function(){});
        }
        btn.disabled = false; btn.textContent = '↻';
      }).catch(function(err){
        addBlock('system', '模型刷新失败: ' + err, false);
        btn.disabled = false; btn.textContent = '↻';
      });
    } else {
      addBlock('system', '模型刷新功能不可用', false);
      btn.disabled = false; btn.textContent = '↻';
    }
  });

  // Populate the session dropdown; select → open that session.
  function fillSessions(list) {
    var s = document.getElementById('session');
    var cur = s.value;
    s.innerHTML = '';
    (list || []).forEach(function(e){
      var o = document.createElement('option'); o.value = e.id; o.textContent = e.title; s.appendChild(o);
    });
    if (cur) s.value = cur;
  }
  function refreshSessions() {
    if (window.sessions) window.sessions().then(fillSessions).catch(function(){});
  }
  refreshSessions();
  document.getElementById('session').addEventListener('change', function(e){
    if (window.openSession) window.openSession(e.target.value);
  });
  document.getElementById('newBtn').addEventListener('click', function(){
    if (window.newSession) window.newSession();
  });

  // Copy button on rendered code blocks (event delegation).
  log.addEventListener('click', function (e) {
    var btn = e.target.closest ? e.target.closest('.md-copy') : null;
    if (!btn) return;
    var pre = btn.parentElement.querySelector('pre');
    if (!pre) return;
    navigator.clipboard.writeText(pre.textContent).then(function () {
      btn.textContent = '✓ 已复制';
      setTimeout(function () { btn.textContent = '复制'; }, 1500);
    }).catch(function(){});
  });

  var hint = document.createElement('div');
  hint.className = 'msg system';
  hint.textContent = 'iCode 简易聊天 — 输入消息或 /命令开始。右侧面板可点击执行任意 CLI 命令；代码块右上角可一键复制；顶栏可切换会话 / 新建会话。特殊语法：# 记入项目记忆、# user: 记入用户记忆、! 执行 shell 命令。工具调用已自动批准。';
  log.appendChild(hint);

  // Initial status-bar fill.
  if (window.stats) window.stats().then(uiStats).catch(function(){});

  // Dark / light theme toggle.
  var themeBtn = document.getElementById('themeBtn');
  var curTheme = localStorage.getItem('icode.simpleui.theme') || 'dark';
  function applyTheme(t) {
    document.documentElement.className = t;
    themeBtn.textContent = t === 'dark' ? '☀' : '☾';
    localStorage.setItem('icode.simpleui.theme', t);
  }
  applyTheme(curTheme);
  themeBtn.addEventListener('click', function(){
    applyTheme(document.documentElement.className === 'dark' ? 'light' : 'dark');
  });
  function uiTheme(t) {
    if (t === 'dark') applyTheme('dark');
    else if (t === 'light') applyTheme('light');
    else applyTheme(document.documentElement.className === 'dark' ? 'light' : 'dark');
  }

  // API-key entry dialog — opened by bare /login (this window has no settings
  // page, so the modal is the only place to paste a provider key).
  function uiKeyPrompt() {
    document.getElementById('keyProvider').value = '';
    document.getElementById('keyValue').value = '';
    document.getElementById('keyModal').style.display = 'flex';
    document.getElementById('keyProvider').focus();
  }
  function keySave() {
    var p = document.getElementById('keyProvider').value.trim();
    var k = document.getElementById('keyValue').value.trim();
    document.getElementById('keyModal').style.display = 'none';
    if (!p) { addBlock('system', '请填写提供商名称（如 deepseek / openrouter / zhipu）', true); return; }
    if (window.setKey) {
      window.setKey(p, k).then(function(msg){ addBlock('system', msg, true); }).catch(function(e){ addBlock('system', '设置失败: ' + e, true); });
    }
  }
</script>
</body></html>`
}
