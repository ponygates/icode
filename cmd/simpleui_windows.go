//go:build windows && !nogui

package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"github.com/ponygates/icode/internal/core/knowledge"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/core/slashui"
	"github.com/ponygates/icode/internal/core/tool"
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

	// lastStatsAt is the wall-clock of the last throttled stats push while
	// streaming (at most one per second). Keeps the bottom status bar live
	// during generation without a WebView JS round-trip per token.
	lastStatsAt time.Time
	// liveCompletion is an ESTIMATE of completion tokens for the in-flight
	// turn (~4 chars/token), so the status bar counter moves while generating;
	// the engine's real usage overwrites it on the final pushStats.
	liveCompletion int

	// activeStreams tracks sessions with an in-flight engine generation
	// (sessionID → true). C7 multi-session parallelism: switching sessions
	// does NOT cancel the background turn — its events are consumed silently
	// and the final transcript persists to the store, so switching back shows
	// the completed result (see runPrompt / OpenSession).
	activeStreams map[string]bool
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
	provider := b.provider
	sid := b.sessionID
	b.mu.Unlock()
	// Re-point the live session at the new model. The engine reads
	// ModelID/ProviderName from the session on every Send, so without this the
	// dropdown looks like it switched but the next turn still uses the model
	// the session was created with — making the selector appear "stuck".
	if sid != "" && b.app != nil && b.app.SessStore != nil {
		if sess, err := b.app.SessStore.Get(sid); err == nil && sess != nil {
			sess.ModelID = id
			sess.ProviderName = provider
			_ = b.app.SessStore.Update(sess)
		}
	}
	b.push(fmt.Sprintf("uiStatus('model', %s)", jsStr(id)))
}

// AddCustomModel persists and live-registers a user-defined model. Returns an
// error message, or "" on success. Bound to JS for the "添加模型" dialog.
func (b *simpleUIBridge) AddCustomModel(provider, modelID, name string) string {
	if b.app == nil || b.app.Reg == nil {
		return "引擎未初始化。"
	}
	if provider == "" || modelID == "" {
		return "provider 与 model_id 不能为空。"
	}
	if name == "" {
		name = modelID
	}
	cfg, err := config.Load()
	if err != nil {
		return "读取配置失败: " + err.Error()
	}
	m := config.ModelCfg{
		Provider: provider,
		ModelID:  modelID,
		Name:     name,
		Custom:   true,
	}
	m.ID = config.ModelKey(provider, modelID)
	cfg.UpsertModel(m)
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return "保存配置失败: " + err.Error()
	}
	// Live-register so the model works immediately, and refresh the dropdown.
	b.app.RegisterCustomModel(m)
	b.refreshModelList()
	return ""
}

// RemoveCustomModel removes a user-defined model. Returns an error message,
// or "" on success.
func (b *simpleUIBridge) RemoveCustomModel(id string) string {
	if b.app == nil || b.app.Reg == nil {
		return "引擎未初始化。"
	}
	if id == "" {
		return "id 不能为空（形如 provider/model_id）。"
	}
	cfg, err := config.Load()
	if err != nil {
		return "读取配置失败: " + err.Error()
	}
	if !cfg.DeleteModel(id) {
		return fmt.Sprintf("模型 %q 未找到。", id)
	}
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return "保存配置失败: " + err.Error()
	}
	b.app.RemoveCustomModel(id)
	b.refreshModelList()
	return ""
}

// refreshModelList re-pushes the current model catalogue to the UI so newly
// added/removed custom models appear in the <select> immediately.
func (b *simpleUIBridge) refreshModelList() {
	if b.w == nil {
		return
	}
	if all := b.app.Reg.ListAllModels(); len(all) > 0 {
		ids := make([]string, 0, len(all))
		for _, m := range all {
			if m.Deprecated {
				ids = append(ids, "⚠ "+m.ID)
			} else {
				ids = append(ids, m.ID)
			}
		}
		quoted := make([]string, 0, len(ids))
		for _, id := range ids {
			quoted = append(quoted, jsStr(id))
		}
		b.push(fmt.Sprintf("fillModelList([%s], null)", strings.Join(quoted, ", ")))
	}
}

// SetMode switches the permission mode (Tab/Shift+Tab cycle) and persists it
// to the config file so it survives restarts.
func (b *simpleUIBridge) SetMode(m string) {
	if m == "" {
		return
	}
	if b.app != nil && b.app.Gate != nil {
		b.app.Gate.SetMode(permission.Mode(m))
	}
	cfg, err := config.Load()
	if err != nil {
		return
	}
	cfg.Defaults.Mode = m
	_ = cfg.Save(config.DefaultPath())
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
	expanded, atts := b.expandFileRefsUI(text)
	b.runPrompt(expanded, atts)
}

// SendWithAttachments sends a prompt together with inline multimodal
// attachments (e.g. images pasted from the clipboard or picked via the file
// chooser). The UI passes attachments as a JSON array of
// {mime, data(base64)}; we map them onto types.Attachment for the engine,
// which already streams image_url / base64 content to OpenAI-compatible and
// Anthropic providers (mirrors the TUI's Ctrl+V multimodal path). Any "@path"
// references in the text are also expanded first (expandFileRefsUI).
func (b *simpleUIBridge) SendWithAttachments(text, attsJSON string) {
	text = strings.TrimSpace(text)
	if text == "" && strings.TrimSpace(attsJSON) == "" {
		return
	}
	if !b.ensureSession() {
		return
	}
	expanded, atts := b.expandFileRefsUI(text)
	if strings.TrimSpace(attsJSON) != "" {
		var raw []struct {
			MIME string `json:"mime"`
			Data string `json:"data"` // base64, with or without a "data:<mime>;base64," prefix
		}
		if err := json.Unmarshal([]byte(attsJSON), &raw); err != nil {
			b.sys("附件解析失败: " + err.Error())
			return
		}
		for _, r := range raw {
			if r.MIME == "" || r.Data == "" {
				continue
			}
			data := r.Data
			// Strip a leading "data:<mime>;base64," prefix if the UI sent a full data URL.
			if i := strings.Index(data, ","); i > 0 && strings.HasPrefix(data, "data:") {
				data = data[i+1:]
			}
			atts = append(atts, types.Attachment{Type: "image", MIMEType: r.MIME, Data: data})
		}
	}
	if strings.TrimSpace(expanded) == "" && len(atts) == 0 {
		return
	}
	b.runPrompt(expanded, atts)
}

// expandFileRefsUI expands "@path" references in the input text exactly like
// the CLI's expandFileRefs: images become inline multimodal attachments, text
// files are inlined verbatim, and other binaries are reduced to a path note so
// raw bytes never pollute the prompt. Returns the rewritten text plus any
// attachments. The WebView2 front end inserts "@<absolute path> " for files
// dragged in or picked with the 📎 button, so this is the single entry point.
func (b *simpleUIBridge) expandFileRefsUI(text string) (string, []types.Attachment) {
	var result strings.Builder
	var atts []types.Attachment
	remaining := text
	for {
		idx := strings.Index(remaining, "@")
		if idx < 0 {
			result.WriteString(remaining)
			break
		}
		result.WriteString(remaining[:idx])
		rest := remaining[idx+1:]
		end := len(rest)
		for i, r := range rest {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
				end = i
				break
			}
		}
		path := strings.TrimSpace(rest[:end])
		remaining = rest[end:]
		if path == "" {
			result.WriteString("@")
			continue
		}
		full := path
		if !filepath.IsAbs(full) {
			if cwd, err := os.Getwd(); err == nil {
				full = filepath.Join(cwd, path)
			}
		}
		data, err := os.ReadFile(full)
		if err != nil {
			// Unreadable — keep the reference visible so the user notices.
			result.WriteString("@" + path)
			continue
		}
		if mime, ok := sniffImage(data); ok {
			atts = append(atts, types.Attachment{Type: "image", MIMEType: mime, Data: base64.StdEncoding.EncodeToString(data)})
			result.WriteString("[📎 图片: " + filepath.Base(path) + "]")
		} else if !bytes.Contains(data, []byte{0}) {
			result.WriteString(fmt.Sprintf("[file: %s]\n%s\n", path, strings.TrimSpace(string(data))))
		} else {
			result.WriteString(fmt.Sprintf("[file: %s] (二进制文件，未内联内容)\n", path))
		}
	}
	return strings.TrimSpace(result.String()), atts
}

// sniffImage detects common raster image formats from their magic bytes.
func sniffImage(data []byte) (string, bool) {
	if len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png", true
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg", true
	}
	if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
		return "image/gif", true
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp", true
	}
	return "", false
}

// Regenerate re-runs the most recent user message as a fresh turn (opencode /
// desktop-style "重新生成" on an assistant message). It reads the last user
// message from the bridge transcript so the UI button needs no extra state.
func (b *simpleUIBridge) Regenerate() {
	b.mu.Lock()
	var lastUser string
	for i := len(b.messages) - 1; i >= 0; i-- {
		if b.messages[i].Role == "user" {
			lastUser = b.messages[i].Content
			break
		}
	}
	b.mu.Unlock()
	if strings.TrimSpace(lastUser) == "" {
		return
	}
	b.Send(lastUser)
}

// kbResult is the trimmed JSON shape served to the knowledge panel.
type kbResult struct {
	File    string  `json:"file"`
	Section string  `json:"section"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

// knowledgeManager returns the engine's knowledge manager (local RAG).
func (b *simpleUIBridge) knowledgeManager() *knowledge.Manager {
	if b.app == nil || b.app.Engine == nil {
		return nil
	}
	return b.app.Engine.KnowledgeManager()
}

// KnowledgeStatus reports the knowledge base index size as JSON
// ({"chunks":N}, chunks=-1 when not configured / engine missing).
func (b *simpleUIBridge) KnowledgeStatus() string {
	mgr := b.knowledgeManager()
	if mgr == nil {
		return `{"chunks":-1}`
	}
	return fmt.Sprintf(`{"chunks":%d}`, mgr.ChunkCount())
}

// KnowledgeSearch queries the local knowledge base (zero-API RAG, mirrors
// /kb) and returns the top results as a JSON array for the UI panel.
func (b *simpleUIBridge) KnowledgeSearch(query string) string {
	mgr := b.knowledgeManager()
	if mgr == nil {
		return `[]`
	}
	if mgr.ChunkCount() == 0 {
		if _, err := mgr.Index(context.Background()); err != nil {
			return `[]`
		}
	}
	res := mgr.Search(query, 6)
	out := make([]kbResult, 0, len(res))
	for _, c := range res {
		runes := []rune(c.Text)
		snippet := string(runes)
		if len(runes) > 240 {
			snippet = string(runes[:240]) + "…"
		}
		out = append(out, kbResult{File: c.File, Section: c.Section, Snippet: snippet, Score: c.Score})
	}
	data, _ := json.Marshal(out)
	return string(data)
}

// taskItem is the trimmed JSON shape for the automation panel.
type taskItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Enabled  bool   `json:"enabled"`
	NextRun  string `json:"next_run"`
}

// TasksList returns the persisted automations as JSON for the panel (mirrors
// /tasks, zero-API local).
func (b *simpleUIBridge) TasksList() string {
	if b.app == nil || b.app.Scheduler == nil {
		return `[]`
	}
	ts := b.app.Scheduler.List()
	out := make([]taskItem, 0, len(ts))
	for _, t := range ts {
		next := ""
		if !t.NextRun.IsZero() {
			next = t.NextRun.Format("01-02 15:04")
		}
		out = append(out, taskItem{ID: t.ID, Name: t.Name, Schedule: t.Schedule, Enabled: t.Enabled, NextRun: next})
	}
	data, _ := json.Marshal(out)
	return string(data)
}

// TaskCreate registers a new automation (schedule: every:30m | daily:09:00 | idle).
func (b *simpleUIBridge) TaskCreate(name, prompt, schedule string) string {
	if b.app == nil || b.app.Scheduler == nil {
		return "调度器不可用（需在配置中启用 scheduler）"
	}
	name = strings.TrimSpace(name)
	prompt = strings.TrimSpace(prompt)
	if name == "" || prompt == "" {
		return "任务名与内容不能为空"
	}
	if schedule == "" {
		schedule = "idle"
	}
	if _, err := b.app.Scheduler.Create(name, prompt, schedule); err != nil {
		return "创建失败: " + err.Error()
	}
	return "✓ 已创建自动化任务"
}

// TaskDelete removes an automation.
func (b *simpleUIBridge) TaskDelete(id string) string {
	if b.app == nil || b.app.Scheduler == nil {
		return "调度器不可用"
	}
	if err := b.app.Scheduler.Delete(id); err != nil {
		return "删除失败: " + err.Error()
	}
	return "✓ 已删除任务"
}

// TaskRunNow triggers an automation immediately in the background.
func (b *simpleUIBridge) TaskRunNow(id string) string {
	if b.app == nil || b.app.Scheduler == nil {
		return "调度器不可用"
	}
	if _, err := b.app.Scheduler.RunNow(id); err != nil {
		return "触发失败: " + err.Error()
	}
	return "✓ 已触发立即执行（后台）"
}

// EscalationState exposes the graded-auth strike state for the permission bar
// (Claude Code parity: "连续 N 次拦截后自动退回手动").
func (b *simpleUIBridge) EscalationState() string {
	if b.app == nil || b.app.Gate == nil {
		return `{"escalated":false,"strikes":0,"threshold":0}`
	}
	esc, strikes := b.app.Gate.EscalationState(b.sessionID)
	th := b.app.Gate.StrikeThreshold()
	return fmt.Sprintf(`{"escalated":%v,"strikes":%d,"threshold":%d}`, esc, strikes, th)
}

// lspReady reports whether the LSP manager is available.
func (b *simpleUIBridge) lspReady() bool {
	return b.app != nil && b.app.Engine != nil && b.app.Engine.LSPManager() != nil
}

// LspStatus returns the /lsp status report text for the diagnostics panel.
func (b *simpleUIBridge) LspStatus() string {
	if !b.lspReady() {
		return "LSP 未启用（需在配置中开启 lsp.enabled）"
	}
	out, err := b.app.Engine.LSPManager().QueryReport("status", nil)
	if err != nil {
		return "LSP 状态查询失败: " + err.Error()
	}
	return out
}

// LspDiag returns the diagnostic report for a file (/lsp diag <file>).
func (b *simpleUIBridge) LspDiag(file string) string {
	if !b.lspReady() {
		return "LSP 未启用（需在配置中开启 lsp.enabled）"
	}
	file = strings.TrimSpace(file)
	if file == "" {
		return "请输入文件路径，如：internal/core/conversation/engine.go"
	}
	out, err := b.app.Engine.LSPManager().QueryReport("diag", []string{file})
	if err != nil {
		return "诊断失败: " + err.Error()
	}
	return out
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
		// An interrupted turn may leave the permission bar hanging — the
		// engine's pending request is cancelled with the context.
		b.push("var pb=document.getElementById('permBar'); if(pb) pb.style.display='none'; permRequestId=null;")
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
// atts carries inline multimodal attachments (images) when present.
func (b *simpleUIBridge) runPrompt(prompt string, atts []types.Attachment) {
	b.appendMsg("user", prompt)
	b.push(fmt.Sprintf("uiAppend('user', %s)", jsStr(prompt)))

	// Fresh turn: reset the live token estimate so statsJSON never mixes the
	// previous turn's in-flight approximation into this one.
	b.mu.Lock()
	b.liveCompletion = 0
	b.lastStatsAt = time.Time{}
	b.mu.Unlock()

	if b.app == nil || b.app.Engine == nil {
		b.push(fmt.Sprintf("uiAppend('system', %s)", jsStr("引擎未初始化，请先配置 API Key。")))
		return
	}

	// C7 multi-session parallelism: capture the session this turn belongs to.
	// The user may switch to another session while it streams — the events
	// keep being consumed (engine persists the transcript to the store) but
	// are only pushed to the UI while that session is still active.
	b.mu.Lock()
	sid := b.sessionID
	b.mu.Unlock()

	ctx := context.Background()
	eventCh, err := b.app.Engine.Send(ctx, sid, prompt, atts)
	if err != nil {
		b.push(fmt.Sprintf("uiAppend('error', %s)", jsStr(fmt.Sprintf("引擎错误: %v", err))))
		b.push("uiBusy(false)")
		return
	}
	b.mu.Lock()
	b.activeStreams[sid] = true
	b.mu.Unlock()
	b.push("uiBusy(true)")

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.push(fmt.Sprintf("uiAppend('error', %s)", jsStr(fmt.Sprintf("内部错误: %v", r))))
				b.push("uiBusy(false)")
			}
			// The turn ended (done/error/cancel) — drop the background marker
			// regardless of which session is now active.
			b.mu.Lock()
			delete(b.activeStreams, sid)
			b.mu.Unlock()
		}()
		for event := range eventCh {
			// Session switched away mid-turn: swallow the remaining events so
			// another conversation's stream never bleeds into the visible UI.
			// The transcript is still saved by the engine; switching back
			// (OpenSession) reloads it from the store.
			b.mu.Lock()
			active := b.sessionID == sid
			b.mu.Unlock()
			if !active {
				continue
			}
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
				// Live token estimate for the status bar (~4 chars/token).
				b.liveCompletion += len([]rune(event.Content)) / 4
				b.mu.Unlock()
				if th != "" {
					b.push(fmt.Sprintf("uiThinking(%s)", jsStr(th)))
					b.mu.Lock()
					b.thinkingBuf = ""
					b.mu.Unlock()
				}
				b.push("uiThinkingLive('')")
				if lt != "" {
					b.push(fmt.Sprintf("uiToolResult(%s)", jsStr(strings.TrimSpace(event.Content))))
					b.mu.Lock()
					b.lastTool = ""
					b.mu.Unlock()
				} else {
					b.appendAssistantText(event.Content)
					b.push(fmt.Sprintf("uiDelta(%s)", jsStr(event.Content)))
				}
				// Throttled stats refresh (≤1/s) so the bottom bar's token
				// counter moves while generating instead of freezing until
				// the turn completes.
				b.pushStatsThrottled()
			case types.EventSystem:
				b.push(fmt.Sprintf("uiAppend('system', %s)", jsStr(strings.TrimSpace(event.Content))))
			case types.EventPlanProposal:
				// Plan-mode turn finished — show the confirmation bar so the
				// user can accept (switch to auto + continue) or discard.
				b.push("uiPlanProposal()")
			case types.EventToolUse:
				b.mu.Lock()
				b.lastTool = event.ToolCall.Name
				b.mu.Unlock()
				args := event.ToolCall.Arguments
				if strings.TrimSpace(args) == "{}" {
					args = ""
				}
				b.push(fmt.Sprintf("uiTool(%s, %s)", jsStr(event.ToolCall.Name), jsStr(args)))
			case types.EventPermission:
				// Engine paused the tool pending user approval — surface the
				// in-window approval bar (allow once / session allow / deny).
				req := event.Permission
				if req == nil {
					break
				}
				b.push(fmt.Sprintf("uiPermission(%s, %s, %s)",
					jsStr(req.RequestID), jsStr(req.Tool), jsStr(req.Prompt)))
			case types.EventToolProgress:
				// Live bash output — surface as tool-result updates so the
				// SimpleUI transcript grows while the command runs.
				b.mu.Lock()
				lt := b.lastTool
				b.mu.Unlock()
				if lt != "" {
					b.push(fmt.Sprintf("uiToolResult(%s)", jsStr(strings.TrimSpace(event.Content))))
				}
			case types.EventDone:
				b.mu.Lock()
				th := b.thinkingBuf
				b.thinkingBuf = ""
				// Drop the live estimate — the final pushStats carries the
				// engine's exact usage for the completed turn.
				b.liveCompletion = 0
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
				b.liveCompletion = 0
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

// Clear archives a summary (if missing) then soft-deletes the session — the
// transcript is kept and restorable via /restore.
func (b *simpleUIBridge) Clear() {
	b.mu.Lock()
	if b.sessionID != "" && b.app != nil && b.app.SessStore != nil {
		if sess, err := b.app.SessStore.Get(b.sessionID); err == nil {
			_ = sessionum.MarkDeleted(b.app.SessStore, sess)
		}
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
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt int64  `json:"updated_at"` // Unix milliseconds, for timeline grouping
}

// Sessions lists saved sessions (id + title) for the UI dropdown.
func (b *simpleUIBridge) Sessions() []SessionEntry {
	if b.app == nil || b.app.SessStore == nil {
		return nil
	}
	list, err := sessionum.ListNonDeleted(b.app.SessStore, 50)
	if err != nil {
		return nil
	}
	out := make([]SessionEntry, 0, len(list))
	for _, s := range list {
		title := s.Title
		if title == "" {
			title = s.ID
		}
		upd := s.UpdatedAt.UnixMilli()
		if upd == 0 {
			upd = s.CreatedAt.UnixMilli()
		}
		out = append(out, SessionEntry{ID: s.ID, Title: title, UpdatedAt: upd})
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
	// If the session we just opened still has an in-flight background turn
	// (C7), restore the busy/stop state so the UI matches reality.
	b.mu.Lock()
	streaming := b.activeStreams[sess.ID]
	b.mu.Unlock()
	if streaming {
		b.push("uiBusy(true)")
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
		case strings.EqualFold(parts[0], "/voice"):
			b.sys(b.handleVoiceCommand(parts[1:]))
			return
		}
	}

	backend := &slashui.Backend{}
	if b.app != nil {
		backend.Engine = b.app.Engine
		backend.SessStore = b.app.SessStore
		backend.Gate = b.app.Gate
		backend.RefreshModels = b.app.RefreshModels
		backend.RegisterCustomModel = func(provider, modelID, name string) string {
			return b.AddCustomModel(provider, modelID, name)
		}
		backend.RemoveCustomModel = func(id string) string {
			return b.RemoveCustomModel(id)
		}
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
	} else if res.SessionID != "" {
		b.mu.Lock()
		cur := b.sessionID
		b.mu.Unlock()
		if cur != res.SessionID {
			b.OpenSession(res.SessionID)
		}
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
	if res.CWD != "" {
		if err := os.Chdir(res.CWD); err == nil {
			b.sys("✓ 工作目录已切换到: " + res.CWD)
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

// handleVoiceCommand processes /voice subcommands for voice settings.
func (b *simpleUIBridge) handleVoiceCommand(args []string) string {
	if len(args) == 0 {
		cfg, err := config.Load()
		if err != nil {
			return "读取配置失败: " + err.Error()
		}
		mask := func(s string) string {
			if s == "" {
				return "(未设置)"
			}
			if len(s) <= 8 {
				return "****"
			}
			return s[:4] + "****" + s[len(s)-4:]
		}
		return fmt.Sprintf(
			"语音输入设置:\n"+
				"  提供商: %s\n"+
				"  百度 API Key: %s\n"+
				"  百度 Secret: %s\n"+
				"  讯飞 App ID: %s\n"+
				"  讯飞 API Key: %s\n"+
				"  讯飞 Secret: %s\n\n"+
				"用法:\n"+
				"  /voice provider <name>     设置提供商 (zhipu|baidu|xfyun)\n"+
				"  /voice baidu <key> <secret> 设置百度语音 API 凭证\n"+
				"  /voice xfyun <appid> <key> <secret> 设置讯飞语音 API 凭证",
			cfg.Voice.Provider,
			mask(cfg.Voice.BaiduAPIKey),
			mask(cfg.Voice.BaiduSecretKey),
			cfg.Voice.IFlytekAppID,
			mask(cfg.Voice.IFlytekAPIKey),
			mask(cfg.Voice.IFlytekAPISecret),
		)
	}

	subcmd := strings.ToLower(args[0])
	cfg, err := config.Load()
	if err != nil {
		return "读取配置失败: " + err.Error()
	}

	switch subcmd {
	case "provider":
		if len(args) < 2 {
			return "用法: /voice provider <name> (zhipu|baidu|xfyun)"
		}
		provider := strings.ToLower(args[1])
		switch provider {
		case "zhipu", "baidu", "xfyun":
			cfg.Voice.Provider = provider
		default:
			return "提供商必须是: zhipu, baidu, xfyun"
		}
	case "baidu":
		if len(args) < 3 {
			return "用法: /voice baidu <api_key> <secret_key>"
		}
		cfg.Voice.BaiduAPIKey = args[1]
		cfg.Voice.BaiduSecretKey = args[2]
	case "xfyun":
		if len(args) < 4 {
			return "用法: /voice xfyun <app_id> <api_key> <api_secret>"
		}
		cfg.Voice.IFlytekAppID = args[1]
		cfg.Voice.IFlytekAPIKey = args[2]
		cfg.Voice.IFlytekAPISecret = args[3]
	default:
		return "未知命令: " + subcmd + " (可用: provider, baidu, xfyun)"
	}

	if err := cfg.Save(config.DefaultPath()); err != nil {
		return "保存配置失败: " + err.Error()
	}
	return "语音设置已保存"
}

// chatTurn runs a prompt that needs a model response.
func (b *simpleUIBridge) chatTurn(prompt string) {
	if !b.ensureSession() {
		return
	}
	b.runPrompt(prompt, nil)
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
		live := b.liveCompletion
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
		// While streaming, the engine's real usage hasn't landed yet — surface
		// the live estimate so the status bar counter moves (~4 chars/token).
		// The final pushStats() after the turn carries exact numbers.
		if live > p.Completion {
			p.Completion = live
			p.Total = p.PromptTokens + p.Completion
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

// pushStatsThrottled refreshes the status bar at most once per second — the
// live token counter during generation. The final pushStats() (unthrottled)
// after the turn always carries the engine's exact usage.
func (b *simpleUIBridge) pushStatsThrottled() {
	b.mu.Lock()
	if !b.lastStatsAt.IsZero() && time.Since(b.lastStatsAt) < time.Second {
		b.mu.Unlock()
		return
	}
	b.lastStatsAt = time.Now()
	b.mu.Unlock()
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

	// Watchdog: if Bootstrap itself ever hangs (seen in the field — some runs
	// stall before "config loaded"), log it instead of freezing silently.
	bootDone := make(chan struct{})
	go func() {
		select {
		case <-bootDone:
		case <-time.After(20 * time.Second):
			log.Printf("[simpleui] WARNING: app.Bootstrap still in progress after 20s")
		}
	}()

	var a *app.App
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[simpleui] Bootstrap panic: %v", r)
				showDesktopError("iCode", fmt.Sprintf("启动异常: %v", r))
			}
		}()
		a, err = app.Bootstrap()
	}()
	close(bootDone)
	if err != nil {
		log.Printf("[simpleui] Bootstrap failed: %v", err)
		showDesktopError("iCode", "启动失败: "+err.Error())
		return err
	}
	if a == nil {
		return fmt.Errorf("bootstrap returned nil app")
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
	// Permission approval: the SimpleUI now surfaces the engine's
	// EventPermission pause as an in-window approval bar (allow-once /
	// session-allow / deny), replacing the old blanket auto-approve that made
	// this surface the only one without a permission gate.

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

	b := &simpleUIBridge{app: a, w: w, model: model, provider: provider, curAssistant: -1, activeStreams: make(map[string]bool)}

	// Notify in the simple UI when a background task completes.
	tool.SetCompleteHook(func(id, errMsg string) {
		status := "✓ 后台任务完成: " + id
		if errMsg != "" {
			status = "⚠ 后台任务失败: " + id + " — " + errMsg
		}
		b.sys(status)
	})

	w.Bind("send", func(text string) { b.Send(text) })
	w.Bind("sendWithAttachments", func(text, attsJSON string) { b.SendWithAttachments(text, attsJSON) })
	w.Bind("regenerate", func() { b.Regenerate() })
	w.Bind("models", func() []string { return b.Models() })
	w.Bind("setModel", func(id string) { b.SetModel(id) })
	w.Bind("addCustomModel", func(provider, modelID, name string) string {
		return b.AddCustomModel(provider, modelID, name)
	})
	w.Bind("removeCustomModel", func(id string) string {
		return b.RemoveCustomModel(id)
	})
	w.Bind("setMode", func(m string) { b.SetMode(m) })
	// Tab / Shift+Tab mode cycling — canonical order matches the TUI's
	// cycleMode exactly (plan → agent → yolo → auto) so muscle memory
	// transfers across all three surfaces.
	w.Bind("cycleMode", func(dir string) {
		if b.app == nil || b.app.Gate == nil {
			return
		}
		modes := []permission.Mode{permission.ModePlan, permission.ModeAgent, permission.ModeYOLO, permission.ModeAuto}
		cur := b.app.Gate.Mode()
		idx := 0
		for i, m := range modes {
			if m == cur {
				idx = i
				break
			}
		}
		step := 1
		if dir == "-1" || dir == "prev" {
			step = -1
		}
		next := modes[(idx+step+len(modes))%len(modes)]
		b.app.Gate.SetMode(next)
		b.pushStats()
		b.sys("模式: " + string(next))
	})
	w.Bind("clear", func() { b.Clear() })
	w.Bind("runCommand", func(text string) { b.RunCommand(text) })
	w.Bind("stop", func() { b.Stop() })
	// Permission bar: answer the engine's pending permission request and
	// optionally register the session-scoped tool allow first.
	w.Bind("respondPermission", func(requestID, decision string) {
		if b.app == nil || b.app.Engine == nil {
			return
		}
		switch strings.ToLower(decision) {
		case "allow":
			b.app.Engine.SetPermissionResponse(requestID, permission.DecisionAllow)
		case "deny":
			b.app.Engine.SetPermissionResponse(requestID, permission.DecisionDeny)
		default:
			b.app.Engine.SetPermissionResponse(requestID, permission.DecisionDeny)
		}
	})
	w.Bind("allowToolForSession", func(toolName string) {
		if b.app == nil || b.app.Gate == nil {
			return
		}
		b.mu.Lock()
		sid := b.sessionID
		b.mu.Unlock()
		if sid != "" {
			b.app.Gate.SetSessionToolAllow(sid, toolName)
		}
	})
	w.Bind("stats", func() string { return b.statsJSON() })
	w.Bind("refreshModels", func() string { return b.RefreshModelsUI() })
	w.Bind("sessions", func() []SessionEntry { return b.Sessions() })
	w.Bind("kbStatus", func() string { return b.KnowledgeStatus() })
	w.Bind("kbSearch", func(query string) string { return b.KnowledgeSearch(query) })
	w.Bind("tasksList", func() string { return b.TasksList() })
	w.Bind("taskCreate", func(name, prompt, schedule string) string { return b.TaskCreate(name, prompt, schedule) })
	w.Bind("taskDelete", func(id string) string { return b.TaskDelete(id) })
	w.Bind("taskRunNow", func(id string) string { return b.TaskRunNow(id) })
	w.Bind("escState", func() string { return b.EscalationState() })
	w.Bind("lspStatus", func() string { return b.LspStatus() })
	w.Bind("lspDiag", func(file string) string { return b.LspDiag(file) })
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
  :root {
    color-scheme: dark;
    /* Shared design tokens — aligned with desktop (global.css) & TUI palette */
    --bg-app: #0b0e14;
    --bg-panel: #161926;
    --bg-elev: #10141d;
    --border: #2a3140;
    --border-strong: #3c4a6b;
    --text: #e6e6e6;
    --text-muted: #6b7484;
    --accent: #ff7a45;
    --accent-2: #ff5f6d;
    --r-sm: 8px;
    --r-md: 10px;
    --r-lg: 12px;
    --shadow-glow: 0 2px 8px rgba(255, 122, 69, .45);
  }
  * { box-sizing: border-box; }
  html, body { margin: 0; height: 100%; }
  body {
    font: 14px/1.6 -apple-system, "Segoe UI", "Microsoft YaHei", system-ui, sans-serif;
    background: linear-gradient(160deg, #0b0e14 0%, #0f1521 45%, #101a2b 100%);
    color: var(--text); display: flex; flex-direction: row; height: 100vh; overflow: hidden;
  }
  #main { display: flex; flex-direction: column; flex: 1 1 auto; min-width: 0; height: 100%; }
  #bar {
    display: flex; align-items: center; gap: 10px; padding: 9px 14px;
    background: rgba(22, 25, 34, 0.82); backdrop-filter: blur(10px);
    border-bottom: 1px solid var(--border); flex: 0 0 auto;
  }
  #bar .logo {
    color: #fff; font-weight: 800; font-size: 15px; letter-spacing: .5px;
    display: flex; align-items: center; gap: 8px;
  }
  #bar .logo::before {
    content: ""; width: 22px; height: 22px; border-radius: 7px;
    background: linear-gradient(135deg, var(--accent), var(--accent-2));
    display: inline-block; box-shadow: var(--shadow-glow);
  }
  #bar button { flex: 0 0 auto; white-space: nowrap; }
  #bar select, #bar input.listbar {
    background: var(--bg-elev); color: var(--text); border: 1px solid #2f3850;
    border-radius: var(--r-sm); padding: 5px 10px; max-width: 200px; min-width: 70px;
    outline: none; transition: border-color .15s;
  }
  #bar select:focus, #bar input.listbar:focus { border-color: var(--accent); }
  #bar button {
    background: #1c2332; color: var(--text); border: 1px solid #2f3850;
    border-radius: var(--r-sm); padding: 5px 12px; cursor: pointer;
    transition: background .15s, transform .1s, border-color .15s;
  }
  #bar button:hover { background: #28304a; border-color: var(--border-strong); }
  #bar button:active { transform: translateY(1px); }
  #bar .spacer { flex: 1; }
  #log {
    flex: 1 1 auto; overflow-y: auto; padding: 16px 18px; scrollbar-width: thin;
    scrollbar-color: #3a4151 transparent;
  }
  #log::-webkit-scrollbar { width: 12px; }
  #log::-webkit-scrollbar-track { background: transparent; }
  #log::-webkit-scrollbar-thumb { background: #39425c; border-radius: 8px; border: 3px solid transparent; background-clip: content-box; }
  #log::-webkit-scrollbar-thumb:hover { background: #4a546e; background-clip: content-box; }
  .msg {
    position: relative; margin: 0 0 14px; padding: 11px 14px; border-radius: 12px;
    white-space: pre-wrap; word-break: break-word; max-width: 92%;
    animation: msgIn .18s ease-out;
    box-shadow: 0 1px 3px rgba(0,0,0,.35);
  }
  .msg-copy, .msg-resend { position: absolute; top: 6px; right: 6px; background: #232b38; border: 1px solid #313846; color: #9aa7b8; border-radius: 5px; padding: 2px 8px; font-size: 11px; cursor: pointer; opacity: 0; transition: opacity .15s; }
  .msg:hover .msg-copy, .msg:hover .msg-resend { opacity: 1; }
  .msg-resend { right: auto; left: 6px; }
  .msg-copy:hover, .msg-resend:hover { color: #fff; border-color: #ff7a45; }
  @keyframes msgIn { from { opacity: 0; transform: translateY(4px); } to { opacity: 1; transform: none; } }
  .user { background: linear-gradient(135deg, #1c3a63, #1d3a5f); margin-left: auto; }
  .assistant { background: #161b26; border: 1px solid #262d3e; }
  .system { background: #1a1f2c; color: #98a3b5; font-size: 13px; }
  .error { background: #3a1d1d; color: #ffb4b4; border: 1px solid #5a2a2a; }
  .tool { background: #131e2b; border: 1px solid #223140; color: #9ecbff; font-size: 13px; }
  .tool .tool-head { display: flex; align-items: center; gap: 7px; }
  .tool .tool-icon { font-size: 12px; }
  .tool .name { font-weight: 700; color: #7fd1ff; flex: 1; }
  .tool .tool-status { font-size: 10px; padding: 1px 8px; border-radius: 10px; font-weight: 600; }
  .tool .tool-status.running { background: rgba(255,170,60,.15); color: #ffb84d; }
  .tool .tool-status.ok { background: rgba(46,160,67,.15); color: #4cd26a; }
  .tool details.tool-args { margin-top: 8px; }
  .tool details.tool-args summary { cursor: pointer; font-size: 11px; color: #6f8db0; user-select: none; }
  .tool details.tool-args summary:hover { color: #9ecbff; }
  .tool pre { margin: 6px 0 0; white-space: pre-wrap; word-break: break-word; color: #c7d2e0; font-size: 12px; max-height: 240px; overflow-y: auto; }
  .thinking { background: #1a1f2c; border: 1px dashed #2f3a52; color: #98a3b5; font-size: 12px; }
  .thinking summary { cursor: pointer; color: #7fd1ff; font-weight: 600; }
  .thinking pre { margin: 6px 0 0; white-space: pre-wrap; word-break: break-word; color: #8a93a3; }
  .role { font-size: 11px; color: #6b7484; margin-bottom: 3px; }
  #inputbar { display: flex; gap: 8px; padding: 12px 14px; border-top: 1px solid var(--border); background: rgba(22, 25, 34, 0.82); backdrop-filter: blur(10px); flex: 0 0 auto; }
  #inp {
    flex: 1; resize: none; height: 44px; background: var(--bg-elev); color: var(--text);
    border: 1px solid #2f3850; border-radius: var(--r-md); padding: 11px 14px; font: inherit;
    outline: none; transition: border-color .15s, box-shadow .15s;
  }
  #inp:focus { border-color: var(--accent); box-shadow: 0 0 0 3px rgba(255, 122, 69, .15); }
  #inp::placeholder { color: #55607a; }
  #send {
    background: linear-gradient(135deg, var(--accent), var(--accent-2)); color: #fff;
    border: none; border-radius: var(--r-md); padding: 0 20px; font-weight: 700; cursor: pointer;
    box-shadow: 0 3px 10px rgba(255, 95, 109, .35); transition: transform .1s, box-shadow .15s, filter .15s;
  }
  #send:hover { filter: brightness(1.08); box-shadow: 0 4px 14px rgba(255, 95, 109, .45); }
  #send:active { transform: translateY(1px); }
  #stopBtn { background: #b53a3a; color: #fff; border: none; border-radius: 10px; padding: 0 16px; font-weight: 700; cursor: pointer; transition: filter .15s; }
  #stopBtn:hover { filter: brightness(1.15); }
  /* Bottom status bar: model/provider/mode/security/tokens/cache/cost */
  #status {
    flex: 0 0 auto; padding: 6px 14px; font-size: 12px; color: var(--text-muted);
    background: rgba(12, 15, 22, .7); border-top: 1px solid #232a3a; white-space: nowrap;
    overflow-x: auto; scrollbar-width: none;
  }
  #status::-webkit-scrollbar { display: none; }
  /* Right command panel + draggable slider */
  #grip {
    flex: 0 0 6px; cursor: col-resize; background: #262b36;
    transition: background .15s;
  }
  #grip:hover, #grip.dragging { background: var(--accent); }
  #side {
    flex: 0 0 280px; width: 280px; min-width: 180px; max-width: 60%;
    background: rgba(16, 19, 27, .9); border-left: 1px solid #232a3a; display: flex; flex-direction: column;
    height: 100%;
  }
  #side .side-head { padding: 11px 14px; font-weight: 700; color: var(--accent); border-bottom: 1px solid #232a3a; flex: 0 0 auto; display: flex; align-items: center; justify-content: space-between; }
  .side-tabs { display: flex; gap: 4px; margin-right: auto; }
  #cmdFilter {
    margin: 8px; padding: 6px 9px; font: inherit; font-size: 12px;
    border: 1px solid #2f3850; border-radius: var(--r-sm);
    background: var(--bg-elev); color: var(--text); outline: none;
  }
  #cmdFilter:focus { border-color: var(--accent); }
  .cmd-desc { display: block; color: #6b7690; font-size: 10.5px; margin-top: 1px; }
  .side-tab { background: transparent; border: none; color: #8a93a6; padding: 4px 12px; border-radius: 6px; cursor: pointer; font-size: 12px; }
  .side-tab.active { background: #2d5a88; color: #fff; }
  .side-kb { padding: 10px 12px; display: flex; flex-direction: column; gap: 8px; overflow: hidden; flex: 1; min-height: 0; }
  .kb-search-row { display: flex; gap: 6px; flex: 0 0 auto; }
  .kb-search-row input { flex: 1; padding: 7px 9px; border-radius: 6px; border: 1px solid #2a2e3a; background: #0f1115; color: #e6e6e6; font-size: 12px; }
  .kb-search-row button { padding: 7px 12px; border-radius: 6px; border: 1px solid #2d5a88; background: #2d5a88; color: #fff; cursor: pointer; font-size: 12px; }
  #kbStats { color: #8a93a6; font-size: 11px; flex: 0 0 auto; }
  #kbResults { flex: 1; overflow: auto; min-height: 0; }
  .kb-item { background: #0f1115; border: 1px solid #2a2e3a; border-radius: 8px; padding: 8px 10px; cursor: pointer; margin-bottom: 6px; }
  .kb-item:hover { border-color: #4c9adf; }
  .kb-item-head { color: #7fc3ff; font-size: 12px; font-weight: 600; margin-bottom: 4px; word-break: break-all; }
  .kb-item-body { color: #c9c9c9; font-size: 12px; line-height: 1.5; max-height: 140px; overflow: auto; white-space: pre-wrap; word-break: break-all; }
  .task-create { display: flex; flex-direction: column; gap: 6px; flex: 0 0 auto; }
  .task-create input, .task-create select { padding: 6px 9px; border-radius: 6px; border: 1px solid #2a2e3a; background: #0f1115; color: #e6e6e6; font-size: 12px; }
  .task-create button { padding: 7px 12px; border-radius: 6px; border: 1px solid #2d5a88; background: #2d5a88; color: #fff; cursor: pointer; font-size: 12px; }
  #taskList { flex: 1; overflow: auto; min-height: 0; }
  .task-item { background: #0f1115; border: 1px solid #2a2e3a; border-radius: 8px; padding: 8px 10px; margin-bottom: 6px; }
  .task-item-head { display: flex; align-items: center; gap: 6px; color: #e6e6e6; font-size: 12px; font-weight: 600; }
  .task-item-head .dot { width: 8px; height: 8px; border-radius: 50%; background: #4caf50; flex: 0 0 auto; }
  .task-item-head .dot.off { background: #666; }
  .task-item-meta { color: #8a93a6; font-size: 11px; margin: 3px 0 6px; }
  .task-item-actions { display: flex; gap: 6px; }
  .task-item-actions button { padding: 3px 10px; border-radius: 5px; border: 1px solid #2a2e3a; background: #1a1f2a; color: #c9c9c9; cursor: pointer; font-size: 11px; }
  .task-item-actions button:hover { border-color: #4c9adf; }
  #side .side-list { overflow-y: auto; padding: 8px; scrollbar-width: thin; scrollbar-color: #39425c transparent; flex: 1 1 auto; }
  #side .side-list::-webkit-scrollbar { width: 10px; }
  #side .side-list::-webkit-scrollbar-thumb { background: #39425c; border-radius: 8px; border: 2px solid transparent; background-clip: content-box; }
  #side .grp { color: #6b7484; font-size: 12px; margin: 12px 6px 5px; letter-spacing: .3px; }
  #side .cmd {
    padding: 6px 10px; margin: 2px 0; border-radius: 7px; cursor: pointer; color: #cdd6e2;
    font-family: ui-monospace, "Cascadia Code", Consolas, monospace; font-size: 13px;
    transition: background .12s, color .12s;
  }
  #side .cmd:hover { background: #232c40; color: #fff; }
  #side .collapse { background: #232c40; color: #e6e6e6; border: 1px solid #2f3850; border-radius: 6px; padding: 2px 10px; cursor: pointer; }
  #side .collapse:hover { background: #2b3650; }
  /* Markdown rendering inside chat bubbles */
  .content { line-height: 1.55; }
  .md-h { display: block; font-weight: 700; margin: 6px 0 2px; color: #ffd9c2; }
  .md-h1 { font-size: 1.15em; } .md-h2 { font-size: 1.08em; } .md-h3, .md-h4, .md-h5, .md-h6 { font-size: 1em; }
  /* Heading hierarchy: colour + spacing so structure survives scanning */
  .md-h { display: block; font-weight: 600; margin: 10px 0 4px; line-height: 1.35; }
  .md-h:first-child { margin-top: 0; }
  .md-h1 { color: #7ec8ff; border-bottom: 1px solid #2a3140; padding-bottom: 4px; }
  .md-h2 { color: #eab308; }
  .md-h3, .md-h4, .md-h5, .md-h6 { color: var(--text-primary); opacity: 0.92; }
  html.light .md-h1 { color: #0b62c4; border-bottom-color: #d8dde6; }
  html.light .md-h2 { color: #a16207; }
  /* Syntax highlight tokens for fenced code blocks */
  .hl-k { color: #c678dd; }
  .hl-s { color: #98c379; }
  .hl-c { color: #6b7280; font-style: italic; }
  .hl-n { color: #d19a66; }
  /* Unified-diff line colouring (parity with the desktop colorizeDiffLines) */
  .hl-diff-add { color: #4cd26a; }
  .hl-diff-del { color: #ff6b6b; }
  .hl-diff-hunk { color: #5ec8ff; }
  .hl-diff-head { font-weight: 700; }
  html.light .hl-k { color: #a626a4; }
  html.light .hl-s { color: #40782f; }
  html.light .hl-c { color: #9a9aa5; }
  html.light .hl-n { color: #b76b01; }
  html.light .hl-diff-add { color: #1a7f37; }
  html.light .hl-diff-del { color: #cf222e; }
  html.light .hl-diff-hunk { color: #0b62c4; }
  html.light .hl-diff-head { font-weight: 700; }
  /* Nested list indentation */
  .md-ul .md-ul, .md-ol .md-ul, .md-ul .md-ol, .md-ol .md-ol { margin: 2px 0; }
  li > .md-ul, li > .md-ol { padding-left: 18px; }
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
  html.light, html.light body { background: linear-gradient(160deg, #f2f3f7 0%, #f8f8fb 55%, #eef2f9 100%); color: #1a1a1f; }
  html.light #bar { background: rgba(255, 255, 255, .85); backdrop-filter: blur(10px); border-color: #e0e0e5; }
  html.light #bar .logo { color: #1a1a1f; }
  html.light #bar select, html.light #bar input.listbar, html.light #bar button { background: #f4f4f7; color: #1a1a1f; border-color: #d0d0da; }
  html.light #log { scrollbar-color: #c5c5ce transparent; }
  html.light #log::-webkit-scrollbar-thumb { background: #c5c5ce; background-clip: content-box; }
  html.light .msg { border-radius: 12px; box-shadow: 0 1px 3px rgba(0,0,0,.06); }
  html.light .user { background: linear-gradient(135deg, #d5e4fb, #dae8fc); }
  html.light .assistant { background: #fff; border-color: #e0e0e5; }
  html.light .system { background: #eeeef2; color: #6b6b7a; }
  html.light .error { background: #fce0e0; color: #b52424; border-color: #f0c0c0; }
  html.light .tool { background: #eff6ff; border-color: #d0daf0; color: #2a4a7a; }
  html.light .tool .name { color: #1a5acc; }
  html.light .tool pre { color: #3a3a4a; }
  html.light .thinking { background: #f4f6f8; border-color: #cfd6e2; color: #5a6270; }
  html.light .thinking summary { color: #1a5acc; }
  html.light .thinking pre { color: #5a6270; }
  html.light #inputbar { background: rgba(255, 255, 255, .85); backdrop-filter: blur(10px); border-color: #e0e0e5; }
  html.light #inp { background: #f7f7f9; color: #1a1a1f; border-color: #d0d0da; }
  html.light #send { background: linear-gradient(135deg, #ff7a45, #ff5f6d); color: #fff; }
  html.light #stopBtn { background: #c0392b; }
  html.light #status { background: rgba(244, 244, 247, .8); border-color: #e0e0e5; color: #888; }
  html.light #side { background: rgba(244, 244, 247, .92); border-color: #e0e0e5; }
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
  /* Custom click-to-open dropdowns — reliable in WebView2 where native
     <datalist> popups don't appear on click. */
  .dd { position: relative; display: inline-flex; align-items: center; }
  .dd-arrow { cursor: pointer; color: #888; padding: 0 6px; font-size: 11px; user-select: none; }
  .dd-arrow:hover { color: #ccc; }
  .dd-panel {
    position: absolute; top: 100%; left: 0; margin-top: 4px; z-index: 500;
    background: #171a21; border: 1px solid #2a2e3a; border-radius: 8px;
    box-shadow: 0 8px 24px rgba(0,0,0,0.5); min-width: 240px; max-width: 360px;
    overflow: hidden;
  }
  .dd-search {
    width: 100%; box-sizing: border-box; padding: 7px 9px; border: none;
    border-bottom: 1px solid #2a2e3a; background: #0f1115; color: #e6e6e6;
    font-size: 13px; outline: none;
  }
  .dd-list { max-height: 280px; overflow-y: auto; padding: 4px; }
  .dd-option {
    padding: 7px 10px; border-radius: 6px; cursor: pointer; font-size: 13px;
    color: #e6e6e6; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .dd-option:hover { background: #243049; }
  .dd-option.sel { background: #1d3a5f; }
  .dd-empty { padding: 10px; color: #777; font-size: 12px; text-align: center; }
  html.light .dd-panel { background: #fff; border-color: #d0d0da; }
  html.light .dd-search { background: #f5f5f8; border-color: #d0d0da; color: #222; }
  html.light .dd-option:hover { background: #eef1fb; }
  html.light .dd-option.sel { background: #dbe7ff; }
</style></head>
<body>
  <div id="main">
    <div id="bar">
      <span class="logo">iCODE</span>
      <div class="dd">
        <input id="session" class="listbar" autocomplete="off" title="搜索/切换会话" placeholder="会话…" />
        <span class="dd-arrow" id="sessionArrow" title="展开会话列表">▾</span>
        <div class="dd-panel" id="sessionPanel" style="display:none;">
          <input id="sessionSearch" class="dd-search" placeholder="搜索会话…" autocomplete="off" />
          <div class="dd-list" id="sessionOptions"></div>
        </div>
      </div>
      <button id="newBtn" title="开启新会话（旧会话保留在下拉列表中）">新会话</button>
      <div class="dd">
        <input id="model" class="listbar" autocomplete="off" title="搜索/切换模型" placeholder="模型…" />
        <span class="dd-arrow" id="modelArrow" title="展开模型列表">▾</span>
        <div class="dd-panel" id="modelPanel" style="display:none;">
          <input id="modelSearch" class="dd-search" placeholder="搜索模型…" autocomplete="off" />
          <div class="dd-list" id="modelOptions"></div>
        </div>
      </div>
      <button id="addModelBtn" title="添加自定义模型" class="theme-btn" style="font-size:13px;">＋模型</button>
      <button id="updateBtn" title="一键刷新所有提供商模型列表" class="theme-btn" style="font-size:13px;">↻</button>
      <span class="spacer"></span>
      <button id="themeBtn" title="切换主题" class="theme-btn">☀</button>
      <button id="panelBtn" title="显示/隐藏命令面板" class="theme-btn" style="font-size:13px;">☰</button>
      <button id="clearBtn" title="清空并删除当前会话">清空</button>
    </div>
    <div id="log"></div>
    <div id="status" title="模型 / 提供商 / 模式 / 安全等级 / Token / 缓存 / 费用"></div>
    <div id="planBar" style="display:none; align-items:center; gap:10px; padding:6px 12px; margin:0 12px 6px; background:#1d2433; border:1px solid #4caf50; border-radius:8px; font-size:13px;">
      <span style="flex:1; color:#e6e6e6;">📋 计划已生成 — 接受后开始执行</span>
      <button id="planAccept" style="background:#4caf50; border:none; color:#fff; padding:4px 12px; border-radius:6px; cursor:pointer;">接受并执行</button>
      <button id="planDiscard" style="background:transparent; border:1px solid #555; color:#aaa; padding:4px 10px; border-radius:6px; cursor:pointer;">放弃</button>
    </div>
    <div id="permBar" style="display:none; align-items:flex-start; gap:8px; flex-direction:column; padding:8px 12px; margin:0 12px 6px; background:#2a2113; border:1px solid #eab308; border-radius:8px; font-size:13px;">
      <div style="width:100%; display:flex; align-items:center; gap:8px;">
        <span style="color:#eab308; font-weight:600;">⚠ 需要授权</span>
        <span id="permTool" style="color:#fff; font-weight:600;"></span>
        <span style="flex:1;"></span>
        <button id="permAllowOnce" style="background:#4caf50; border:none; color:#fff; padding:4px 12px; border-radius:6px; cursor:pointer;">允许一次</button>
        <button id="permAlways" style="background:#2d5a88; border:none; color:#fff; padding:4px 12px; border-radius:6px; cursor:pointer;">本会话总是允许</button>
        <button id="permDeny" style="background:#b0413e; border:none; color:#fff; padding:4px 12px; border-radius:6px; cursor:pointer;">拒绝</button>
      </div>
      <div id="permPrompt" style="width:100%; color:#c9c9c9; font-size:12px; word-break:break-all; max-height:72px; overflow:auto;"></div>
      <div id="escHint" style="width:100%; color:#eab308; font-size:11px;"></div>
    </div>
    <div id="inputbar">
      <div id="attZone" style="display:none; flex-wrap:wrap; gap:6px; margin-bottom:6px;"></div>
      <div style="display:flex; align-items:flex-end; gap:8px; flex:1; min-width:0;">
        <button id="attachBtn" title="附加文件（图片走多模态，其他走 @路径 引用；也可直接拖拽文件进来）" style="flex:0 0 auto; padding:8px 10px; border-radius:8px; border:1px solid #2a2e3a; background:#0f1115; color:#e6e6e6; cursor:pointer;">📎</button>
        <textarea id="inp" placeholder="输入消息或 /命令，Enter 发送，Shift+Enter 换行…（可 Ctrl+V 粘贴图片、拖拽文件）"></textarea>
        <button id="stopBtn" title="停止生成" style="display:none;">■ 停止</button>
        <button id="send">发送</button>
      </div>
      <input type="file" id="fileInput" multiple style="display:none" />
    </div>
  </div>
  <div id="grip" title="拖动调整命令面板宽度"></div>
  <div id="side">
    <div class="side-head">
      <span class="side-tabs">
        <button id="tabCmd" class="side-tab active" title="命令列表">命令</button>
        <button id="tabKb" class="side-tab" title="本地知识库检索（/kb）">知识库</button>
        <button id="tabTasks" class="side-tab" title="自动化任务（/tasks）">任务</button>
        <button id="tabLsp" class="side-tab" title="LSP 代码诊断（/lsp）">诊断</button>
      </span>
      <button class="collapse" id="collapseBtn" title="折叠/展开">⟨</button>
    </div>
    <div class="side-list" id="sideList"></div>
    <div class="side-kb" id="sideKb" style="display:none;">
      <div class="kb-search-row">
        <input id="kbInput" placeholder="搜索知识库…（如：犹豫期退保）" autocomplete="off" />
        <button id="kbGo">检索</button>
      </div>
      <div id="kbStats"></div>
      <div id="kbResults"></div>
    </div>
    <div class="side-kb" id="sideTasks" style="display:none;">
      <div id="taskMsg" style="color:#8a93a6;font-size:11px;min-height:14px;"></div>
      <div class="task-create">
        <input id="taskName" placeholder="任务名（如：深夜知识库体检）" autocomplete="off" />
        <input id="taskPrompt" placeholder="要执行的内容…" autocomplete="off" />
        <select id="taskSched">
          <option value="idle">闲时（0 点后自动跑）</option>
          <option value="daily:02:00">每天 02:00</option>
          <option value="daily:00:30">每天 00:30</option>
          <option value="every:30m">每 30 分钟</option>
          <option value="every:1h">每小时</option>
        </select>
        <button id="taskCreateBtn">创建任务</button>
      </div>
      <div id="taskList"></div>
    </div>
    <div class="side-kb" id="sideLsp" style="display:none;">
      <div style="display:flex; gap:6px; flex:0 0 auto;">
        <button id="lspStatusBtn" style="flex:0 0 auto; padding:6px 10px; border-radius:6px; border:1px solid #2d5a88; background:#2d5a88; color:#fff; cursor:pointer; font-size:12px;">LSP 状态</button>
        <input id="lspFile" placeholder="文件路径，如 internal/…/engine.go" autocomplete="off" style="flex:1; padding:6px 9px; border-radius:6px; border:1px solid #2a2e3a; background:#0f1115; color:#e6e6e6; font-size:12px;" />
        <button id="lspGo" style="flex:0 0 auto; padding:6px 10px; border-radius:6px; border:1px solid #2d5a88; background:#2d5a88; color:#fff; cursor:pointer; font-size:12px;">诊断</button>
      </div>
      <pre id="lspOut" style="flex:1; overflow:auto; min-height:0; margin:0; padding:8px; background:#0f1115; border:1px solid #2a2e3a; border-radius:8px; color:#c9c9c9; font-size:12px; line-height:1.5; white-space:pre-wrap; word-break:break-all;"></pre>
    </div>
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
  <div id="modelModal" style="display:none; position:fixed; inset:0; background:rgba(0,0,0,0.5); z-index:999; align-items:center; justify-content:center;">
    <div style="background:#171a21; border:1px solid #2a2e3a; border-radius:10px; padding:18px; width:380px;">
      <div style="font-weight:600; margin-bottom:4px;">添加自定义模型</div>
      <div style="font-size:12px; color:#888; margin-bottom:10px;">为任意 OpenAI 兼容端点添加模型（如自建网关 / 中转站）。保存后立即可用。</div>
      <input id="modelProvider" placeholder="提供商名称，如 mygate" style="width:100%; box-sizing:border-box; padding:7px 9px; border-radius:6px; border:1px solid #2a2e3a; background:#0f1115; color:#e6e6e6; margin-bottom:8px;"/>
      <input id="modelID" placeholder="模型 ID，如 gpt-4o" style="width:100%; box-sizing:border-box; padding:7px 9px; border-radius:6px; border:1px solid #2a2e3a; background:#0f1115; color:#e6e6e6; margin-bottom:8px;"/>
      <input id="modelName" placeholder="显示名称（可选，默认同模型 ID）" style="width:100%; box-sizing:border-box; padding:7px 9px; border-radius:6px; border:1px solid #2a2e3a; background:#0f1115; color:#e6e6e6; margin-bottom:12px;"/>
      <div style="display:flex; gap:8px; justify-content:flex-end;">
        <button onclick="document.getElementById('modelModal').style.display='none';" style="padding:6px 14px; border-radius:6px; border:1px solid #2a2e3a; background:transparent; color:#aaa; cursor:pointer;">取消</button>
        <button onclick="modelSave()" style="padding:6px 14px; border-radius:6px; border:none; background:#4f6ef7; color:#fff; cursor:pointer;">保存</button>
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
    s = s.replace(/!\[([^\]]*)\]\((https?:\/\/[^)\s]+)\)/g, '<span class="md-img"><span>🖼️</span><a href="$2" target="_blank" rel="noopener noreferrer">$1</a></span>');
    s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
    s = s.replace(/(^|[^"=\/])(https?:\/\/[^\s<>"{}|]+)/g, function(m, pre, url) {
      return pre + '<a href="' + url + '" target="_blank" rel="noopener noreferrer">' + url + '</a>';
    });
    return s;
  }
  // Lightweight syntax highlighter for fenced code blocks (client-side, no
  // deps): comments / strings / numbers / keywords per language family.
  // Input is already HTML-escaped upstream; we only wrap tokens in spans.
  var HL_KW = {
    go: 'func|var|const|type|struct|interface|map|chan|defer|if|else|for|range|switch|case|default|break|continue|return|package|import|select',
    js: 'function|var|let|const|class|extends|new|this|async|await|if|else|for|while|do|switch|case|default|break|continue|return|typeof|instanceof|import|export|from|try|catch|finally|throw|yield',
    py: 'def|class|if|elif|else|for|while|return|import|from|as|with|try|except|finally|lambda|yield|pass|raise|in|not|and|or|is|global|assert|del',
    sh: 'if|then|else|elif|fi|for|while|do|done|case|esac|function|export|source|local|return',
    sql: 'select|from|where|insert|into|values|update|set|delete|join|left|right|inner|outer|on|group|by|order|having|limit|create|table|index|drop|alter'
  };
  function hlFamily(lang) {
    var l = (lang || '').toLowerCase();
    if (/^(js|jsx|javascript|ts|tsx|typescript)$/.test(l)) return 'js';
    if (/^(py|python)$/.test(l)) return 'py';
    if (/^(bash|sh|shell|zsh|console)$/.test(l)) return 'sh';
    if (/^sql$/.test(l)) return 'sql';
    if (/^(go|golang)$/.test(l)) return 'go';
    return 'js';
  }
  function hlCode(lang, code) {
    // Unified-diff blocks get line-level colouring (parity with the desktop
    // colorizeDiffLines): +++/--- headers bold, + green, - red, @@ hunk cyan.
    if ((lang || '').toLowerCase() === 'diff') {
      return code.split('\n').map(function (ln) {
        if (ln.indexOf('+++') === 0 || ln.indexOf('---') === 0) return '<span class="hl-diff-head">' + ln + '</span>';
        if (ln.indexOf('+') === 0) return '<span class="hl-diff-add">' + ln + '</span>';
        if (ln.indexOf('-') === 0) return '<span class="hl-diff-del">' + ln + '</span>';
        if (ln.indexOf('@@') === 0) return '<span class="hl-diff-hunk">' + ln + '</span>';
        return ln;
      }).join('\n');
    }
    var fam = hlFamily(lang);
    var kw = HL_KW[fam] || '';
    var hashComment = (fam === 'py' || fam === 'sh');
    // Single-pass alternation: comment → string → number → keyword.
    var cRe = hashComment ? '#[^\\n]*' : '//[^\\n]*|/\\*[\\s\\S]*?\\*/';
    var re = new RegExp(
      '(' + cRe + ')' +
      '|("(?:[^"\\\\\\n]|\\\\.)*"|\'(?:[^\'\\\\\\n]|\\\\.)*\')' +
      '|\\b(\\d+(?:\\.\\d+)?)\\b' +
      (kw ? '|\\b(' + kw + ')\\b' : ''),
      'g');
    return code.replace(re, function (m, c, s, n, k) {
      if (c) return '<span class="hl-c">' + m + '</span>';
      if (s) return '<span class="hl-s">' + m + '</span>';
      if (n) return '<span class="hl-n">' + m + '</span>';
      if (k) return '<span class="hl-k">' + m + '</span>';
      return m;
    });
  }

  // Lightweight Markdown → HTML for the chat bubbles. Input is first escaped,
  // so model output can never inject scripts. Block elements rely on the
  // .msg white-space:pre-wrap to preserve paragraph line breaks.
  function renderMarkdownHTML(src) {
    var esc = escapeHtml(src);
    var blocks = [];
    esc = esc.replace(/\u0060\u0060\u0060(\w*)\n?([\s\S]*?)\u0060\u0060\u0060/g, function (_, lang, code) {
      var i = blocks.length;
      var body = hlCode(lang, code.replace(/\n$/, ''));
      blocks.push('<div class="md-codewrap"><button class="md-copy" type="button">复制</button><pre class="md-code"><code>' + body + '</code></pre></div>');
      return ' CB' + i + ' ';
    });
    var lines = esc.split('\n');
    var out = [];
    // List stack for nesting: each entry is 'ul' or 'ol' at its indent level
    // (2 spaces per level; tabs count as 2).
    var listStack = [];
    function listLevel(s) {
      var m = /^([ \t]*)/.exec(s)[1];
      var n = 0;
      for (var q = 0; q < m.length; q++) n += (m.charAt(q) === '\t' ? 2 : 1);
      return Math.floor(n / 2);
    }
    function closeToList(level, type) {
      while (listStack.length > level) out.push('</' + listStack.pop() + '>');
      if (type && listStack.length === level && listStack[listStack.length - 1] !== type) {
        out.push('</' + listStack.pop() + '>');
      }
      while (type && listStack.length < level) { out.push('<ul class="md-ul">'); listStack.push('ul'); }
      if (type && listStack.length === level) { out.push('<' + type + ' class="md-' + type + '">'); listStack.push(type); }
    }
    function closeAllLists() { closeToList(0, null); }
    for (var i = 0; i < lines.length; i++) {
      var ln = lines[i];
      var cb = /^ CB(\d+) $/.exec(ln);
      if (cb) { closeAllLists(); out.push(blocks[+cb[1]]); continue; }
      var h = /^(#{1,6})\s+(.*)$/.exec(ln);
      if (h) { closeAllLists(); out.push('<span class="md-h md-h' + h[1].length + '">' + inlineMd(h[2]) + '</span>'); continue; }
      if (/^(---|\*\*\*|___)\s*$/.test(ln)) { closeAllLists(); out.push('<hr class="md-hr">'); continue; }
      if (/^&gt;\s?/.test(ln)) { closeAllLists(); out.push('<div class="md-quote">' + inlineMd(ln.replace(/^&gt;\s?/, '')) + '</div>'); continue; }
      // List items (nested by 2-space indent levels). Tasks render as list
      // items so they participate in nesting like any other bullet.
      var lvl = listLevel(ln);
      var trimmed = ln.trim();
      var taskM = /^[-*+]\s+\[([ xX])\]\s+(.*)$/.exec(trimmed);
      var ulm = /^[-*+]\s+(.*)$/.exec(trimmed);
      var olm = /^(\d+)\.\s+(.*)$/.exec(trimmed);
      if (taskM || ulm || olm) {
        var ltype = 'ul', liInner = '';
        if (taskM) {
          var chk = taskM[1] !== ' ';
          liInner = '<span class="md-chk' + (chk ? ' checked' : '') + '">' + (chk ? '✓' : '') + '</span><span class="md-task-text' + (chk ? ' done' : '') + '">' + inlineMd(taskM[2]) + '</span>';
        } else if (olm) {
          ltype = 'ol'; liInner = inlineMd(olm[2]);
        } else {
          liInner = inlineMd(ulm[1]);
        }
        closeToList(lvl, ltype);
        out.push('<li>' + liInner + '</li>');
        continue;
      }
      if (/^\|.+\|$/.test(ln.trim())) {
        closeAllLists();
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
      if (ln.trim() === '') { closeAllLists(); continue; }
      // Standalone image line: ![alt](url) with nothing else on the line
      var img = /^\!\[([^\]]*)\]\((https?:\/\/[^)\s]+)\)\s*$/.exec(ln.trim());
      if (img) {
        closeAllLists();
        var alt = img[1] || '';
        var src = img[2];
        out.push('<div class="md-img-block"><a href="' + src + '" target="_blank" rel="noopener noreferrer"><span>🖼️</span> <span>' + inlineMd(alt || src) + '</span></a></div>');
        continue;
      }
      closeAllLists();
      out.push('<div class="md-p">' + inlineMd(ln) + '</div>');
    }
    closeAllLists();
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
      // Regenerate: re-run the last user message as a fresh turn (opencode /
      // desktop parity). The Go side reads the transcript, so no state here.
      var rg = document.createElement('button'); rg.className = 'msg-resend'; rg.type = 'button'; rg.textContent = '↻';
      rg.title = '重新生成';
      rg.addEventListener('click', function() {
        if (window.regenerate) window.regenerate();
      });
      d.appendChild(rg);
    }
    if (role === 'user') {
      var r = document.createElement('div'); r.className = 'role'; r.textContent = '你';
      d.appendChild(r);
      // Resend: put the raw message text back into the input box (opencode-
      // style message resend, without editing history).
      var re = document.createElement('button'); re.className = 'msg-resend'; re.type = 'button'; re.textContent = '↻';
      re.title = '重新发送';
      re.addEventListener('click', function() {
        var inp = document.getElementById('inp');
        inp.value = (d.__raw || text || '').trim();
        inp.focus();
      });
      d.appendChild(re);
      d.__raw = text;
    }
    var c = document.createElement('div'); c.className = 'content';
    c.innerHTML = useMd ? renderMarkdownHTML(text) : escapeHtml(text || '');
    d.appendChild(c);
    log.appendChild(d); stick(); return d;
  }

  function uiAppend(role, text) { current = null; addBlock(role, text, role === 'user' || role === 'system' || role === 'assistant'); }
  function uiDelta(text) {
    if (!current) { current = addBlock('assistant', '', false); current.__raw = ''; }
    current.__raw += text;
    // Stream with live Markdown rendering (previously rendered as plain text).
    // Try/catch prevents a transient parse error from dropping the whole output.
    try {
      current.querySelector('.content').innerHTML = renderMarkdownHTML(current.__raw);
    } catch(e) {
      current.querySelector('.content').textContent = current.__raw;
    }
    stick();
  }
  function uiThinking(text) {
    current = null;
    var d = document.createElement('div'); d.className = 'msg thinking';
    // NOTE: <summary> MUST stay the first child of <details> per the HTML spec
    // (a wrapping div breaks the collapse toggle), so the copy button is
    // appended to the .msg block instead — the same absolute-positioned
    // top-right placement the assistant messages use.
    var s = document.createElement('summary'); s.textContent = '🧠 推理过程';
    var p = document.createElement('pre'); p.textContent = text;
    var details = document.createElement('details'); details.appendChild(s); details.appendChild(p);
    d.appendChild(details);
    var cp = document.createElement('button'); cp.className = 'msg-copy'; cp.type = 'button'; cp.textContent = '复制';
    cp.addEventListener('click', function() {
      navigator.clipboard.writeText(text).then(function() { cp.textContent = '✓ 已复制'; setTimeout(function() { cp.textContent = '复制'; }, 1500); }).catch(function(){});
    });
    d.appendChild(cp);
    log.appendChild(d); stick();
  }
  function uiTool(name, args) {
    current = null;
    var d = document.createElement('div'); d.className = 'msg tool';
    var head = document.createElement('div'); head.className = 'tool-head';
    var icon = document.createElement('span'); icon.className = 'tool-icon'; icon.textContent = '🔧';
    var n = document.createElement('span'); n.className = 'name'; n.textContent = name;
    var status = document.createElement('span'); status.className = 'tool-status running'; status.textContent = '运行中';
    head.appendChild(icon); head.appendChild(n); head.appendChild(status);
    d.appendChild(head);
    if (args) {
      var det = document.createElement('details'); det.className = 'tool-args';
      var s = document.createElement('summary'); s.textContent = '参数';
      var p = document.createElement('pre'); p.textContent = args;
      det.appendChild(s); det.appendChild(p); d.appendChild(det);
    }
    d.__status = status;
    log.appendChild(d); stick();
  }
  function uiToolResult(text) {
    var blocks = log.querySelectorAll('.msg.tool');
    var last = blocks[blocks.length - 1];
    if (last) {
      if (last.__status && last.__status.className.indexOf('running') >= 0) {
        last.__status.textContent = '✓';
        last.__status.className = 'tool-status ok';
      }
      var p = document.createElement('pre'); p.textContent = text; last.appendChild(p);
    }
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
  var currentMode = 'auto';
  function fmtTok(n) { n = n || 0; return n >= 1000 ? (n/1000).toFixed(1) + 'k' : '' + n; }
  function uiStats(s) {
    var el = document.getElementById('status'); if (!el) return;
    var parts = [];
    if (s.model) parts.push('模型 ' + s.model);
    if (s.provider) parts.push('提供商 ' + s.provider);
    if (s.mode) { parts.push('模式 ' + s.mode); currentMode = s.mode; }
    if (s.security) parts.push('安全 ' + s.security);
    if (s.total !== undefined && s.total > 0) parts.push('↑' + fmtTok(s.prompt_tokens) + ' ↓' + fmtTok(s.completion_tokens) + ' = ' + fmtTok(s.total));
    if (s.cache_hit_rate > 0) parts.push('缓存 ' + Math.round(s.cache_hit_rate * 100) + '%');
    if (s.cost > 0) parts.push('¥' + s.cost.toFixed(4));
    el.textContent = parts.join('  ·  ');
  }

  // Multimodal image attachments — mirrors the CLI's Ctrl+V path. Images are
  // held client-side as base64 until send; nothing is uploaded anywhere else.
  var pendingAttachments = []; // [{mime, data(base64)}]
  var attZone = document.getElementById('attZone');
  var fileInput = document.getElementById('fileInput');

  function renderAtts() {
    if (!attZone) return;
    if (!pendingAttachments.length) { attZone.style.display = 'none'; attZone.innerHTML = ''; return; }
    attZone.style.display = 'flex';
    attZone.innerHTML = '';
    pendingAttachments.forEach(function(a, i){
      var chip = document.createElement('div');
      chip.style.cssText = 'position:relative;width:56px;height:56px;border-radius:8px;overflow:hidden;border:1px solid #2a2e3a;';
      var img = document.createElement('img');
      img.src = 'data:' + a.mime + ';base64,' + a.data;
      img.style.cssText = 'width:100%;height:100%;object-fit:cover;';
      var x = document.createElement('span');
      x.textContent = '✕'; x.title = '移除';
      x.style.cssText = 'position:absolute;top:1px;right:2px;cursor:pointer;color:#fff;background:rgba(0,0,0,.55);border-radius:50%;width:16px;height:16px;line-height:16px;text-align:center;font-size:11px;';
      x.addEventListener('click', function(){ pendingAttachments.splice(i,1); renderAtts(); });
      chip.appendChild(img); chip.appendChild(x);
      attZone.appendChild(chip);
    });
  }

  function addImageFile(file) {
    if (!file || !file.type || !file.type.startsWith('image/')) return;
    var reader = new FileReader();
    reader.onload = function(e){
      var dataURL = e.target.result; // data:<mime>;base64,xxxxx
      var idx = dataURL.indexOf(',');
      if (idx < 0) return;
      pendingAttachments.push({ mime: file.type, data: dataURL.substring(idx + 1) });
      renderAtts();
    };
    reader.readAsDataURL(file);
  }

  // Paste screenshots straight from the clipboard into attachments.
  inp.addEventListener('paste', function(e){
    var dt = e.clipboardData || window.clipboardData;
    if (!dt || !dt.items) return;
    var had = false;
    for (var i = 0; i < dt.items.length; i++) {
      if (dt.items[i].kind === 'file' && dt.items[i].type && dt.items[i].type.indexOf('image/') === 0) {
        var f = dt.items[i].getAsFile();
        if (f) { addImageFile(f); had = true; }
      }
    }
    if (had) e.preventDefault();
  });

  // addFileRef handles any dropped / picked file the way the CLI does:
  //   - images          → inline multimodal attachment (paste path)
  //   - anything with a local path (WebView2 exposes File.path) → "@<path> "
  //     reference, expanded server-side by expandFileRefsUI
  //   - text without a path → inlined directly into the input
  function addFileRef(f) {
    if (!f) return;
    if (f.type && f.type.indexOf('image/') === 0) { addImageFile(f); return; }
    if (f.path) {
      if (inp.value.length && !/\s$/.test(inp.value)) inp.value += ' ';
      inp.value += '@' + f.path + ' ';
      inp.focus();
      return;
    }
    var r = new FileReader();
    r.onload = function(ev){
      var t = String(ev.target.result || '');
      if (inp.value.length && !/\s$/.test(inp.value)) inp.value += ' ';
      inp.value += t + '\n';
      inp.focus();
    };
    r.readAsText(f);
  }

  var attachBtn = document.getElementById('attachBtn');
  if (attachBtn) attachBtn.addEventListener('click', function(){ if (fileInput) fileInput.click(); });
  if (fileInput) fileInput.addEventListener('change', function(e){
    Array.prototype.forEach.call(e.target.files || [], addFileRef);
    fileInput.value = '';
    inp.focus();
  });

  // Drag & drop files straight into the input bar.
  window.addEventListener('dragover', function(e){ e.preventDefault(); if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy'; });
  window.addEventListener('drop', function(e){
    e.preventDefault();
    var files = e.dataTransfer && e.dataTransfer.files;
    if (!files || !files.length) return;
    var before = inp.value;
    Array.prototype.forEach.call(files, addFileRef);
    if (inp.value !== before) inp.focus();
  });

  function doSend() {
    var t = inp.value.trim();
    if (!t && !pendingAttachments.length) return;
    inp.value = '';
    if (pendingAttachments.length) {
      var atts = pendingAttachments.slice();
      pendingAttachments = [];
      renderAtts();
      if (window.sendWithAttachments) window.sendWithAttachments(t, JSON.stringify(atts));
    } else {
      if (window.runCommand) window.runCommand(t);
    }
  }

  // Plan-mode confirmation bar: accept switches to auto mode and starts
  // executing the plan; discard just hides the bar.
  function uiPlanProposal() {
    var bar = document.getElementById('planBar');
    if (bar) bar.style.display = 'flex';
  }
  // Permission approval bar — the SimpleUI surface of the engine's
  // EventPermission pause. The engine blocks the tool until respondPermission
  // answers, exactly like the desktop modal and the TUI's [1]/[2]/[3] keys.
  var permRequestId = null;
  // Graded-auth escalation hint (Claude Code parity): shows strike progress /
  // "已退回手动" in the permission bar.
  function refreshEsc() {
    var el = document.getElementById('escHint');
    if (!el) return;
    if (!window.escState) { el.textContent = ''; return; }
    try {
      var s = JSON.parse(window.escState() || '{}');
      if (s.escalated) {
        el.textContent = '⚠ 已连续 ' + s.strikes + ' 次拦截，本会话已退回手动模式（需逐次确认）';
      } else if (s.strikes > 0) {
        el.textContent = '已拦截 ' + s.strikes + '/' + (s.threshold || 3) + ' 次，连续 ' + (s.threshold || 3) + ' 次将退回手动';
      } else {
        el.textContent = '';
      }
    } catch(e) {}
  }
  function uiPermission(id, tool, prompt) {
    permRequestId = id;
    var bar = document.getElementById('permBar');
    if (!bar) return;
    document.getElementById('permTool').textContent = tool;
    document.getElementById('permPrompt').textContent = prompt || '';
    bar.style.display = 'flex';
    refreshEsc();
  }
  function answerPermission(decision) {
    var bar = document.getElementById('permBar');
    if (bar) bar.style.display = 'none';
    if (permRequestId && window.respondPermission) {
      window.respondPermission(permRequestId, decision);
    }
    permRequestId = null;
  }
  document.getElementById('planAccept').addEventListener('click', function(){
    document.getElementById('planBar').style.display = 'none';
    if (window.setMode) window.setMode('auto');
    if (window.runCommand) window.runCommand('计划已确认。请按上述计划立即开始执行，不要再重复或重新规划，直接动手。');
  });
  document.getElementById('planDiscard').addEventListener('click', function(){
    document.getElementById('planBar').style.display = 'none';
  });
  document.getElementById('permAllowOnce').addEventListener('click', function(){ answerPermission('allow'); });
  document.getElementById('permAlways').addEventListener('click', function(){
    // Session-scoped tool allow (same as the desktop "总是允许"): register the
    // per-session allow first, then answer the pending request with allow.
    var tool = document.getElementById('permTool').textContent;
    if (window.allowToolForSession) window.allowToolForSession(tool);
    answerPermission('allow');
  });
  document.getElementById('permDeny').addEventListener('click', function(){ answerPermission('deny'); });

  document.getElementById('send').addEventListener('click', doSend);
  document.getElementById('clearBtn').addEventListener('click', function(){ if (window.clear) window.clear(); });
  stopBtn.addEventListener('click', function(){ if (window.stop) window.stop(); });
  inp.addEventListener('keydown', function(e){
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); doSend(); return; }
    // Esc — interrupt an in-flight generation (mirrors the TUI and the
    // desktop app; the stop button is hidden while idle, so this is the
    // keyboard path for the same action).
    if (e.key === 'Escape') {
      if (stopBtn.style.display !== 'none') { if (window.stop) window.stop(); }
      e.preventDefault();
      return;
    }
    // Tab / Shift+Tab cycle the permission mode (Claude Code style): Tab
    // moves forward, Shift+Tab backwards. The Go bridge computes the next
    // mode from the gate's authoritative state using the SAME canonical
    // order as the TUI (plan → agent → yolo → auto), then pushStats()
    // refreshes this status bar — no local drift possible.
    if (e.key === 'Tab' && !e.altKey) {
      e.preventDefault();
      if (window.cycleMode) window.cycleMode(e.shiftKey ? '-1' : '1');
    }
  });

  // Command catalog — rendered into the right panel. Only lists commands that
  // actually work in this UI (CLI-terminal-only commands like /vim /history
  // are excluded).
  var CATALOG = [
    {g:'会话', c:['/clear','/new','/sessions','/resume','/fork','/branch','/rename','/goal','/budget','/compact','/export','/share','/diff','/review','/summarize','/undo','/rewind','/checkpoint','/restore','/search','/wipe','/apply','/reject']},
    {g:'模型', c:['/model','/provider','/models','/keys','/update','/thinking']},
    {g:'配置', c:['/config','/theme','/lang','/security','/permissions','/mcp','/output-style','/mode','/plan','/ask','/debug']},
    {g:'工具', c:['/init','/add-dir','/agents','/skills','/skill-eval','/plugin','/teams','/hooks','/todo','/lsp','/kb','/idle','/tasks','/mesh','/admin']},
    {g:'信息', c:['/help','/whoami','/status','/cost','/token','/usage','/stats','/doctor','/memory','/context','/feedback','/copy']},
    {g:'系统', c:['/login','/logout','/release-notes','/bug']}
  ];
  var sideList = document.getElementById('sideList');
  var DESC = {'/clear':'清空当前会话','/new':'新建会话','/sessions':'会话列表','/resume':'恢复会话','/fork':'分支会话','/branch':'分支管理','/rename':'重命名会话','/goal':'目标模式','/budget':'预算控制','/compact':'压缩上下文','/export':'导出 Markdown','/share':'分享导出','/diff':'查看 git diff','/review':'审查改动','/summarize':'总结会话','/undo':'撤销改动','/rewind':'回滚到检查点','/checkpoint':'检查点管理','/restore':'恢复检查点','/search':'搜索会话','/wipe':'清除全部数据','/apply':'应用暂存修改','/reject':'拒绝暂存修改','/model':'切换模型','/provider':'切换供应商','/models':'模型列表','/keys':'API 密钥管理','/update':'检查更新','/thinking':'深度思考开关','/config':'配置面板','/theme':'主题切换','/lang':'界面语言','/security':'安全等级','/permissions':'权限规则','/mcp':'MCP 服务器','/output-style':'输出风格','/mode':'权限模式','/plan':'计划模式','/ask':'询问模式','/debug':'调试模式','/init':'初始化 ICODE.md','/add-dir':'添加工作目录','/agents':'子代理列表','/skills':'技能列表','/skill-eval':'技能评测','/plugin':'插件管理','/teams':'团队协作','/hooks':'自动化钩子','/todo':'待办统计','/lsp':'LSP 状态','/kb':'知识库','/idle':'闲时代理','/tasks':'后台任务','/mesh':'多机协同','/admin':'管理面板','/help':'帮助','/whoami':'当前身份','/status':'系统状态','/cost':'费用统计','/token':'Token 统计','/usage':'用量报告','/stats':'使用统计','/doctor':'自检诊断','/memory':'记忆文件','/context':'上下文占用','/feedback':'反馈','/copy':'复制回复','/login':'登录','/logout':'登出','/release-notes':'更新日志','/bug':'反馈问题（/bug zip 生成诊断包）'};
  // Filter box on top of the command list — CLI-grade discoverability.
  var filter = document.createElement('input');
  filter.id = 'cmdFilter'; filter.placeholder = '搜索命令…';
  filter.autocomplete = 'off';
  sideList.parentNode.insertBefore(filter, sideList);
  function renderCommands(q) {
    q = (q || '').trim().toLowerCase();
    sideList.innerHTML = '';
    CATALOG.forEach(function(group){
      var items = group.c.filter(function(name){
        if (!q) return true;
        return name.indexOf(q) >= 0 || (DESC[name] || '').indexOf(q) >= 0;
      });
      if (items.length === 0) return;
      var h = document.createElement('div'); h.className = 'grp'; h.textContent = group.g;
      sideList.appendChild(h);
      items.forEach(function(name){
        var d = document.createElement('div'); d.className = 'cmd'; d.title = DESC[name] || ('执行 ' + name);
        d.innerHTML = name + '<span class="cmd-desc">' + (DESC[name] || '') + '</span>';
        d.addEventListener('click', function(){ if (window.runCommand) window.runCommand(name); });
        sideList.appendChild(d);
      });
    });
  }
  renderCommands('');
  filter.addEventListener('input', function(){ renderCommands(filter.value); });

  // ── Side panel tabs: 命令 / 知识库 ──────────────────────────────
  var tabCmd = document.getElementById('tabCmd');
  var tabKb = document.getElementById('tabKb');
  var sideKb = document.getElementById('sideKb');
  var kbInput = document.getElementById('kbInput');
  var kbGo = document.getElementById('kbGo');
  var kbStats = document.getElementById('kbStats');
  var kbResults = document.getElementById('kbResults');

  function showSideTab(which) {
    var kb = which === 'kb', tk = which === 'tasks', lp = which === 'lsp';
    var non = (kb || tk || lp);
    sideList.style.display = non ? 'none' : '';
    if (sideKb) sideKb.style.display = kb ? 'flex' : 'none';
    if (sideTasks) sideTasks.style.display = tk ? 'flex' : 'none';
    if (sideLsp) sideLsp.style.display = lp ? 'flex' : 'none';
    if (tabCmd) tabCmd.className = 'side-tab' + (non ? '' : ' active');
    if (tabKb) tabKb.className = 'side-tab' + (kb ? ' active' : '');
    if (tabTasks) tabTasks.className = 'side-tab' + (tk ? ' active' : '');
    if (tabLsp) tabLsp.className = 'side-tab' + (lp ? ' active' : '');
    if (kb && kbInput) { kbInput.focus(); refreshKbStats(); }
    if (tk) refreshTasks();
    if (lp) lspShowStatus();
  }
  if (tabCmd) tabCmd.addEventListener('click', function(){ showSideTab('cmd'); });
  if (tabKb) tabKb.addEventListener('click', function(){ showSideTab('kb'); });

  // ── 任务（自动化 / 闲时调度）面板 ─────────────────────────────
  var tabTasks = document.getElementById('tabTasks');
  var sideTasks = document.getElementById('sideTasks');
  var taskName = document.getElementById('taskName');
  var taskPrompt = document.getElementById('taskPrompt');
  var taskSched = document.getElementById('taskSched');
  var taskCreateBtn = document.getElementById('taskCreateBtn');
  var taskList = document.getElementById('taskList');
  var taskMsg = document.getElementById('taskMsg');

  function alertMsg(s) {
    if (!taskMsg || !s) return;
    taskMsg.textContent = s;
    clearTimeout(alertMsg._t);
    alertMsg._t = setTimeout(function(){ taskMsg.textContent = ''; }, 4000);
  }

  function refreshTasks() {
    if (!taskList) return;
    if (!window.tasksList) { taskList.innerHTML = '<div style="color:#8a93a6;font-size:12px;">调度器不可用</div>'; return; }
    var items = [];
    try { items = JSON.parse(window.tasksList() || '[]'); } catch(e) {}
    taskList.innerHTML = '';
    if (!items.length) {
      var empty = document.createElement('div');
      empty.style.cssText = 'color:#8a93a6;font-size:12px;line-height:1.6;';
      empty.textContent = '暂无自动化任务。把重活（批量处理、知识库索引、跑测试）挂到闲时窗口自动执行。';
      taskList.appendChild(empty);
      return;
    }
    items.forEach(function(t){
      var d = document.createElement('div'); d.className = 'task-item';
      var h = document.createElement('div'); h.className = 'task-item-head';
      var dot = document.createElement('span'); dot.className = 'dot' + (t.enabled ? '' : ' off');
      var nm = document.createElement('span'); nm.textContent = t.name;
      h.appendChild(dot); h.appendChild(nm);
      var m = document.createElement('div'); m.className = 'task-item-meta';
      m.textContent = (t.schedule || '') + (t.next_run ? ' · 下次 ' + t.next_run : '');
      var act = document.createElement('div'); act.className = 'task-item-actions';
      var run = document.createElement('button'); run.textContent = '立即运行';
      run.addEventListener('click', function(){
        if (window.taskRunNow) { var r = window.taskRunNow(t.id); if (r) alertMsg(r); refreshTasks(); }
      });
      var del = document.createElement('button'); del.textContent = '删除';
      del.addEventListener('click', function(){
        if (window.taskDelete) { var r = window.taskDelete(t.id); if (r) alertMsg(r); refreshTasks(); }
      });
      act.appendChild(run); act.appendChild(del);
      d.appendChild(h); d.appendChild(m); d.appendChild(act);
      taskList.appendChild(d);
    });
  }

  if (tabTasks) tabTasks.addEventListener('click', function(){ showSideTab('tasks'); });
  if (taskCreateBtn) taskCreateBtn.addEventListener('click', function(){
    if (!window.taskCreate) return;
    var n = (taskName.value || '').trim(), p = (taskPrompt.value || '').trim();
    if (!n || !p) { alertMsg('任务名与内容不能为空'); return; }
    var r = window.taskCreate(n, p, (taskSched && taskSched.value) || 'idle');
    alertMsg(r);
    if (r && r.indexOf('✓') === 0) { taskName.value = ''; taskPrompt.value = ''; refreshTasks(); }
  });

  // ── LSP 诊断面板（/lsp 可视化） ──────────────────────────────
  var tabLsp = document.getElementById('tabLsp');
  var sideLsp = document.getElementById('sideLsp');
  var lspFile = document.getElementById('lspFile');
  var lspOut = document.getElementById('lspOut');

  function lspShowStatus() {
    if (!lspOut) return;
    if (!window.lspStatus) { lspOut.textContent = 'LSP 不可用'; return; }
    lspOut.textContent = window.lspStatus();
  }
  function lspRunDiag() {
    if (!lspOut || !window.lspDiag) return;
    lspOut.textContent = window.lspDiag((lspFile && lspFile.value) || '');
  }
  if (tabLsp) tabLsp.addEventListener('click', function(){ showSideTab('lsp'); });
  var lspStatusBtn = document.getElementById('lspStatusBtn');
  var lspGo = document.getElementById('lspGo');
  if (lspStatusBtn) lspStatusBtn.addEventListener('click', lspShowStatus);
  if (lspGo) lspGo.addEventListener('click', lspRunDiag);
  if (lspFile) lspFile.addEventListener('keydown', function(e){
    if (e.key === 'Enter') { e.preventDefault(); lspRunDiag(); }
  });

  function refreshKbStats() {
    if (!kbStats) return;
    if (!window.kbStatus) { kbStats.textContent = '知识库不可用'; return; }
    try {
      var o = JSON.parse(window.kbStatus() || '{}');
      kbStats.textContent = o.chunks >= 0 ? ('已索引 ' + o.chunks + ' 个片段，输入关键词检索（本地 RAG，零 token）') : '知识库未配置（config.yaml knowledge.dirs）';
    } catch(e) { kbStats.textContent = ''; }
  }

  function doKbSearch() {
    if (!kbInput || !kbResults || !window.kbSearch) return;
    var q = kbInput.value.trim();
    if (!q) { kbResults.innerHTML = ''; refreshKbStats(); return; }
    if (kbStats) kbStats.textContent = '检索中…';
    var list = [];
    try { list = JSON.parse(window.kbSearch(q) || '[]'); } catch(e) { list = []; }
    kbResults.innerHTML = '';
    if (!list.length) { if (kbStats) kbStats.textContent = '未命中，换个关键词试试（先确认知识库已配置并索引）'; return; }
    if (kbStats) kbStats.textContent = '命中 ' + list.length + ' 条 · 点击插入输入框';
    list.forEach(function(r){
      var d = document.createElement('div'); d.className = 'kb-item';
      d.title = r.file;
      var h = document.createElement('div'); h.className = 'kb-item-head';
      h.textContent = (r.section || r.file || '片段') + '  ·  ' + Math.round((r.score || 0) * 100) + '%';
      var b = document.createElement('div'); b.className = 'kb-item-body'; b.textContent = r.snippet || '';
      d.appendChild(h); d.appendChild(b);
      d.addEventListener('click', function(){
        // Insert the full snippet with its source note so the model can read it.
        var block = '[知识库资料: ' + (r.file || '') + (r.section ? ' / ' + r.section : '') + ']\n' + (r.snippet || '') + '\n';
        if (inp.value.length && !/\s$/.test(inp.value)) inp.value += '\n';
        inp.value += block;
        inp.focus();
      });
      kbResults.appendChild(d);
    });
  }
  if (kbGo) kbGo.addEventListener('click', doKbSearch);
  if (kbInput) kbInput.addEventListener('keydown', function(e){
    if (e.key === 'Enter') { e.preventDefault(); doKbSearch(); }
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

  // Populate the model dropdown. A custom panel (#modelOptions) is used
  // instead of a native <datalist> because WebView2 does not reliably show
  // datalist popups on click — the user reported the model list "won't open".
  function fillModelList(list, cur) {
    var box = document.getElementById('modelOptions');
    box.innerHTML = '';
    (list || []).forEach(function(id){
      var o = document.createElement('div');
      o.className = 'dd-option' + (id === cur ? ' sel' : '');
      o.textContent = id;
      o.addEventListener('click', function(){
        document.getElementById('model').value = id;
        if (window.setModel) window.setModel(id);
        closeAllDD();
      });
      box.appendChild(o);
    });
    if (!box.querySelector('.dd-option')) {
      var e = document.createElement('div'); e.className = 'dd-empty'; e.textContent = '无可用的模型'; box.appendChild(e);
    }
    if (cur !== undefined && cur !== null) document.getElementById('model').value = cur;
  }
  if (window.models) {
    window.models().then(function(list){
      fillModelList(list, ` + jsStr(model) + `);
    }).catch(function(){});
  }
  document.getElementById('model').addEventListener('change', function(e){
    var v = e.target.value;
    if (v && window.setModel) window.setModel(v);
  });
  document.getElementById('model').addEventListener('keydown', function(e){
    if (e.key === 'Enter' && e.target.value && window.setModel) {
      e.preventDefault(); window.setModel(e.target.value);
    }
  });

  // ── Custom click-to-open dropdown helpers (model + session) ──
  function closeAllDD() {
    document.getElementById('modelPanel').style.display = 'none';
    document.getElementById('sessionPanel').style.display = 'none';
  }
  function openDD(panelId, searchId) {
    var wasOpen = document.getElementById(panelId).style.display === 'block';
    closeAllDD();
    if (wasOpen) return; // toggle off when already open
    var p = document.getElementById(panelId);
    p.style.display = 'block';
    var s = document.getElementById(searchId);
    if (s) { s.value = ''; filterDD(panelId); s.focus(); }
  }
  function filterDD(panelId) {
    var p = document.getElementById(panelId);
    var s = p.querySelector('.dd-search');
    var q = (s ? s.value : '').toLowerCase();
    var shown = 0;
    p.querySelectorAll('.dd-option').forEach(function(o){
      var hit = o.textContent.toLowerCase().indexOf(q) >= 0;
      o.style.display = hit ? '' : 'none';
      if (hit) shown++;
    });
    var empty = p.querySelector('.dd-empty');
    if (empty) empty.style.display = shown ? 'none' : '';
  }
  // Close any open panel when clicking outside a .dd widget.
  document.addEventListener('click', function(e){
    if (!e.target.closest || !e.target.closest('.dd')) closeAllDD();
  });
  document.getElementById('modelArrow').addEventListener('click', function(e){ e.stopPropagation(); openDD('modelPanel','modelSearch'); });
  document.getElementById('model').addEventListener('click', function(e){ e.stopPropagation(); openDD('modelPanel','modelSearch'); });
  document.getElementById('modelSearch').addEventListener('input', function(){ filterDD('modelPanel'); });
  document.getElementById('sessionArrow').addEventListener('click', function(e){ e.stopPropagation(); openDD('sessionPanel','sessionSearch'); });
  document.getElementById('session').addEventListener('click', function(e){ e.stopPropagation(); openDD('sessionPanel','sessionSearch'); });
  document.getElementById('sessionSearch').addEventListener('input', function(){ filterDD('sessionPanel'); });

  // Refresh all provider model catalogs.
  document.getElementById('updateBtn').addEventListener('click', function(){
    var btn = this;
    btn.disabled = true; btn.textContent = '…';
      if (window.refreshModels) {
      window.refreshModels().then(function(result){
        addBlock('system', result, true);
        // Re-populate model input
        if (window.models) {
          window.models().then(function(list){
            fillModelList(list, document.getElementById('model').value);
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

  // Populate the session input; selecting an entry opens that session.
  function timeGroup(ts) {
    if (!ts) return '';
    var days = Math.floor((Date.now() - ts) / 86400000);
    if (days <= 0) return '今天';
    if (days === 1) return '昨天';
    if (days < 7) return '近7天';
    return '更早';
  }
  function fillSessions(list) {
    var box = document.getElementById('sessionOptions');
    var cur = document.getElementById('session').value;
    box.innerHTML = '';
    (list || []).forEach(function(e2){
      var o = document.createElement('div');
      o.className = 'dd-option';
      var g = e2.updated_at ? timeGroup(e2.updated_at) : '';
      o.textContent = (g ? (g + ' · ') : '') + (e2.title || e2.id);
      o.addEventListener('click', function(){
        document.getElementById('session').value = e2.id;
        if (window.openSession) window.openSession(e2.id);
        closeAllDD();
      });
      box.appendChild(o);
    });
    if (!box.querySelector('.dd-option')) {
      var em = document.createElement('div'); em.className = 'dd-empty'; em.textContent = '暂无会话'; box.appendChild(em);
    }
    if (cur) document.getElementById('session').value = cur;
  }
  function refreshSessions() {
    if (window.sessions) window.sessions().then(function(list){
      fillSessions(list);
      // Cross-UI history sync: on a fresh load nothing is selected yet, so
      // resume the most recently updated session (the backend list is
      // newest-first) — the same behaviour as desktop/CLI. When the user has
      // explicitly switched sessions or pressed "新会话" the dropdown already
      // holds a value, so we never override their choice.
      var v = document.getElementById('session');
      if (!v.value && list && list.length > 0) {
        v.value = list[0].id;
        if (window.openSession) window.openSession(list[0].id);
      }
    }).catch(function(){});
  }
  refreshSessions();
  document.getElementById('session').addEventListener('change', function(e){
    if (e.target.value && window.openSession) window.openSession(e.target.value);
  });
  document.getElementById('session').addEventListener('keydown', function(e){
    if (e.key === 'Enter' && e.target.value && window.openSession) {
      e.preventDefault(); window.openSession(e.target.value);
    }
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

  // Dark / light theme toggle. localStorage on an about:blank WebView2 page
  // can throw a SecurityError, which would abort the whole inline script and
  // leave every later handler unbound — so all storage access goes through
  // safe wrappers.
  function loadTheme() { try { return localStorage.getItem('icode.simpleui.theme') || 'dark'; } catch (e) { return 'dark'; } }
  function saveTheme(t) { try { localStorage.setItem('icode.simpleui.theme', t); } catch (e) {} }
  var themeBtn = document.getElementById('themeBtn');
  var curTheme = loadTheme();
  function applyTheme(t) {
    document.documentElement.className = t;
    themeBtn.textContent = t === 'dark' ? '☀' : '☾';
    saveTheme(t);
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

  // 添加自定义模型对话框。
  document.getElementById('addModelBtn').addEventListener('click', function(){
    document.getElementById('modelProvider').value = '';
    document.getElementById('modelID').value = '';
    document.getElementById('modelName').value = '';
    document.getElementById('modelModal').style.display = 'flex';
    document.getElementById('modelProvider').focus();
  });
  function modelSave() {
    var p = document.getElementById('modelProvider').value.trim();
    var id = document.getElementById('modelID').value.trim();
    var n = document.getElementById('modelName').value.trim();
    document.getElementById('modelModal').style.display = 'none';
    if (!p || !id) { addBlock('system', '请填写提供商名称与模型 ID', true); return; }
    if (window.addCustomModel) {
      window.addCustomModel(p, id, n).then(function(msg){
        if (msg) { addBlock('system', '添加失败: ' + msg, true); return; }
        addBlock('system', '✓ 已添加自定义模型 ' + p + '/' + id, true);
      }).catch(function(e){ addBlock('system', '添加失败: ' + e, true); });
    }
  }
</script>
</body></html>`
}
