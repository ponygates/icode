package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/auth"
	"github.com/ponygates/icode/internal/core/knowledge"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/todo"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/mcp"
	"github.com/ponygates/icode/internal/tui"
	"github.com/ponygates/icode/internal/types"
)

func (c *chatCallback) OnPermissionResponse(decision string) {
	c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Permission: %s", decision))
}

// TodoCounts implements tui.Callback — surfaces the current session's todo
// tally in the status bar. Missing session / empty list → all zeros.
func (c *chatCallback) TodoCounts() (pending, active, done, total int) {
	if c.sessionID == "" {
		return
	}
	return todo.Default.Counts(c.sessionID)
}

// TodoActiveText implements tui.Callback — the in-progress item's text.
func (c *chatCallback) TodoActiveText() string {
	if c.sessionID == "" {
		return ""
	}
	return todo.Default.ActiveText(c.sessionID)
}

func (c *chatCallback) SessionID() string { return c.sessionID }

// OnRenameSession implements tui.Callback — retitles the active session in the
// backend store so the sidebar / resume list reflect it immediately.
func (c *chatCallback) OnRenameSession(title string) string {
	if c.app == nil || c.app.SessStore == nil || c.sessionID == "" {
		return "无会话存储可用。"
	}
	sess, err := c.app.SessStore.Get(c.sessionID)
	if err != nil {
		return "会话不存在: " + c.sessionID
	}
	sess.Title = title
	if err := c.app.SessStore.Update(sess); err != nil {
		return "保存标题失败: " + err.Error()
	}
	return ""
}

// OnSetAskUser implements tui.Callback — wires the TUI's interactive
// multiple-choice asker into the engine so the ask_user_question tool can
// render options and read the user's choice (Claude Code AskUserQuestion
// parity). Headless / desktop leave the engine's AskUser nil and
// the tool degrades gracefully.
func (c *chatCallback) OnSetAskUser(fn func(question string, options []string) (int, error)) {
	if c.app == nil || c.app.Engine == nil {
		return
	}
	c.app.Engine.AskUser = tool.AskUserFunc(fn)
}

// OnSetAskUserForm implements tui.Callback — wires the multi-question wizard
// asker into the engine (opencode AskQuestion parity).
func (c *chatCallback) OnSetAskUserForm(fn func(questions []tool.FormQuestion) ([]tool.FormAnswer, error)) {
	if c.app == nil || c.app.Engine == nil {
		return
	}
	c.app.Engine.AskUserForm = tool.AskUserFormFunc(fn)
}

// OnSetMode implements tui.Callback — switches the permission gate so the
// TUI's displayed mode and the enforced mode can never drift apart.
func (c *chatCallback) OnSetMode(mode string) string {
	if c.app == nil || c.app.Gate == nil {
		return ""
	}
	c.app.Gate.SetMode(permission.Mode(mode))
	return ""
}

func (c *chatCallback) OnInterrupt() {
	if c.app != nil && c.app.Engine != nil && c.sessionID != "" {
		c.app.Engine.Stop(c.sessionID)
	}
}

func (c *chatCallback) OnStatus() string {
	if c.app == nil {
		return "引擎未初始化。配置 API Key 后重试。"
	}
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
			// Mask the key
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
	if c.sessionID != "" {
		b.WriteString(c.sessionID[:8] + "...")
	} else {
		b.WriteString("无")
	}

	return b.String()
}

// OnTokenStats implements tui.Callback — surfaces iCode's token-saving
// metrics so the Cache-First Loop is visible, not invisible.
func (c *chatCallback) OnTokenStats() string {
	if c.app == nil || c.app.Engine == nil {
		return "引擎未初始化。"
	}
	if c.sessionID == "" {
		return "没有活跃会话，先发一条消息再查看统计。"
	}
	stats := c.app.Engine.SessionStats(c.sessionID)
	if stats == nil {
		return "暂无统计数据。"
	}
	var b strings.Builder
	b.WriteString("🪙 iCode Token 节省报告\n\n")
	b.WriteString(fmt.Sprintf("已节省 Token:   %s\n", formatInt(stats.TokensSaved)))
	b.WriteString(fmt.Sprintf("缓存命中率:     %.1f%%\n", stats.CacheHitRate*100))
	b.WriteString(fmt.Sprintf("累计压缩次数:   %d\n", stats.CompactionsDone))
	b.WriteString(fmt.Sprintf("Prompt Token:   %s\n", formatInt(stats.PromptTokens)))
	b.WriteString(fmt.Sprintf("Completion:     %s\n", formatInt(stats.CompletionTokens)))
	b.WriteString(fmt.Sprintf("总 Token:       %s\n", formatInt(stats.TotalTokens)))
	if stats.CacheHitTokens > 0 {
		b.WriteString(fmt.Sprintf("缓存命中 Token: %s\n", formatInt(stats.CacheHitTokens)))
	}
	if stats.EstimatedCost > 0 {
		b.WriteString(fmt.Sprintf("预估费用:       ¥%.4f\n", stats.EstimatedCost))
	}
	if stats.EstimatedSavedCost > 0 {
		b.WriteString(fmt.Sprintf("预估节省:       ¥%.4f\n", stats.EstimatedSavedCost))
	}
	if len(stats.Rounds) > 0 {
		b.WriteString("\n每轮明细（缓存命中即可见节省）:\n")
		for _, r := range stats.Rounds {
			hit := "   miss"
			if r.CacheHit > 0 {
				hit = fmt.Sprintf("hit %8s", formatInt(r.CacheHit))
			}
			bar := cliSpark(r.Prompt)
			b.WriteString(fmt.Sprintf("  #%-2d  prompt %-9s comp %-7s  cache %s  ¥%.4f  %s\n",
				r.Turn, formatInt(r.Prompt), formatInt(r.Completion), hit, r.Cost, bar))
		}
	}
	b.WriteString("\n机制: Cache-First Loop（不可变前缀 + 追加日志 + 易失暂存）\n")
	b.WriteString("5 层压缩: Snip → 去重 → 折叠 → 摘要 → 预算上限")
	return b.String()
}

// cliSpark renders a tiny ASCII sparkline for prompt-size growth across turns
// (each block ≈ 2k tokens).
func cliSpark(prompt int) string {
	blocks := prompt / 2000
	if blocks > 12 {
		blocks = 12
	}
	return strings.Repeat("█", blocks) + strings.Repeat("░", 12-blocks)
}

// OnOutputStyle implements tui.Callback — applies an answer style live and
// persists it to config (takes effect from the next model turn).
func (c *chatCallback) OnOutputStyle(style string) string {
	style = strings.ToLower(strings.TrimSpace(style))
	if style != "concise" && style != "normal" && style != "verbose" {
		return "无效风格: " + style + "（可选 concise|normal|verbose）"
	}
	cfg, err := config.Load()
	if err != nil {
		return "读取配置失败: " + err.Error()
	}
	cfg.Defaults.OutputStyle = style
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return "保存配置失败: " + err.Error()
	}
	if c.app != nil && c.app.Engine != nil {
		c.app.Engine.SetSystemPrompt(config.EffectiveSystemPrompt(cfg))
		return "输出风格已设为 " + style + "（已即时生效并持久化）"
	}
	return "输出风格已设为 " + style + "（已持久化，重启会话后生效）"
}

// OnAddDir implements tui.Callback — registers an extra working directory and
// re-applies the composed system prompt so the model can reference it.
func (c *chatCallback) OnAddDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "解析路径失败: " + err.Error()
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "目录不存在或不是文件夹: " + abs
	}
	cfg, err := config.Load()
	if err != nil {
		return "读取配置失败: " + err.Error()
	}
	for _, d := range cfg.Defaults.ExtraDirs {
		if d == abs {
			return "该目录已在工作目录列表中: " + abs
		}
	}
	cfg.Defaults.ExtraDirs = append(cfg.Defaults.ExtraDirs, abs)
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return "保存配置失败: " + err.Error()
	}
	if c.app != nil && c.app.Engine != nil {
		c.app.Engine.SetSystemPrompt(config.EffectiveSystemPrompt(cfg))
	}
	return fmt.Sprintf("✓ 已添加工作目录: %s（共 %d 个，已注入上下文）", abs, len(cfg.Defaults.ExtraDirs))
}

// OnCredentialsSaved implements the optional tui credPusher contract: it
// injects a key saved by /login (or the cleared one from /logout) into the
// live provider, so the very next message uses it instead of the stale one
// captured at startup.
func (c *chatCallback) OnCredentialsSaved(provider, apiKey string) {
	if c.app == nil || c.app.Reg == nil {
		return
	}
	base := ""
	if cfg, err := config.Load(); err == nil {
		base = cfg.Providers[provider].APIBase
	}
	if c.app.Reg.SetCredentials(provider, apiKey, base) {
		return
	}
	if apiKey == "" {
		return
	}
	// A vendor the session never registered (first-ever key for a custom
	// gateway): build it from the just-saved config and add it.
	cfg, err := config.Load()
	if err != nil {
		return
	}
	pc := cfg.Providers[provider]
	pc.APIKey = apiKey
	if err := c.app.Reg.Register(auth.Provider(provider, pc)); err != nil {
		c.tui.AddMessage(tui.RoleSystem, "已保存，但注册到运行中的会话失败: "+err.Error())
	}
}

// OnUpdateModels implements tui.Callback — refreshes provider model catalogs
// and the TUI model picker list.
func (c *chatCallback) OnUpdateModels() string {
	if c.app == nil {
		return "引擎未初始化。"
	}
	updates, err := c.app.RefreshModels(context.Background())
	if err != nil && len(updates) == 0 {
		return "刷新失败: " + err.Error()
	}
	var b strings.Builder
	b.WriteString("模型目录刷新结果：\n")
	ok, fail := 0, 0
	for _, u := range updates {
		if u.Success {
			ok++
			b.WriteString(fmt.Sprintf("  ✓ %-14s %d 个模型（%s）\n", u.Name, u.Count, u.Source))
		} else {
			fail++
			msg := u.Error
			if msg == "" {
				msg = "未知错误"
			}
			b.WriteString(fmt.Sprintf("  ✗ %-14s %s\n", u.Name, msg))
		}
	}
	b.WriteString(fmt.Sprintf("成功 %d · 失败 %d", ok, fail))
	// Refresh the TUI's model list so /model and Tab switching see updates.
	if c.app.Reg != nil && c.tui != nil {
		if all := c.app.Reg.ListAllModels(); len(all) > 0 {
			ids := make([]string, 0, len(all))
			for _, m := range all {
				ids = append(ids, m.ID)
			}
			c.tui.SetModels(ids)
		}
	}
	return b.String()
}

func (c *chatCallback) OnAddCustomModel(provider, modelID, name string) string {
	if c.app == nil || c.app.Reg == nil {
		return "引擎未初始化。"
	}
	if provider == "" || modelID == "" {
		return "用法: /models add <provider> <model_id> [name]"
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
	// Live-register so the model works immediately (no restart needed).
	c.app.RegisterCustomModel(m)
	if c.tui != nil {
		if all := c.app.Reg.ListAllModels(); len(all) > 0 {
			ids := make([]string, 0, len(all))
			for _, mm := range all {
				ids = append(ids, mm.ID)
			}
			c.tui.SetModels(ids)
		}
	}
	return fmt.Sprintf("✓ 已添加自定义模型 %s（%s / %s）", m.ID, provider, name)
}

func (c *chatCallback) OnRemoveCustomModel(id string) string {
	if c.app == nil || c.app.Reg == nil {
		return "引擎未初始化。"
	}
	if id == "" {
		return "用法: /models rm <id>（id 形如 provider/model_id）"
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
	c.app.RemoveCustomModel(id)
	if c.tui != nil {
		if all := c.app.Reg.ListAllModels(); len(all) > 0 {
			ids := make([]string, 0, len(all))
			for _, mm := range all {
				ids = append(ids, mm.ID)
			}
			c.tui.SetModels(ids)
		}
	}
	return fmt.Sprintf("✓ 已移除自定义模型 %s", id)
}

// LSPQuery implements tui.Callback — runs an on-demand LSP code-intelligence
// query for /lsp. Delegates to the shared lsp.Manager.QueryReport so the TUI
// behaves identically to the HTTP slash layer.
func (c *chatCallback) LSPQuery(sub string, args []string) string {
	if c.app == nil || c.app.LSPManager == nil {
		return "LSP 未启用。请在配置中开启 lsp.enabled（config.toml 或 /config）。"
	}
	out, err := c.app.LSPManager.QueryReport(sub, args)
	if err != nil {
		return "⚠ " + err.Error()
	}
	return out
}

// KnowledgeQuery implements tui.Callback — searches the local document
// knowledge base for /kb.
func (c *chatCallback) KnowledgeQuery(query string) string {
	if c.app == nil || c.app.Knowledge == nil {
		return "知识库未配置。请在 config.yaml 的 knowledge.dirs 中指定文档目录。"
	}
	if strings.TrimSpace(query) == "" {
		return fmt.Sprintf("知识库状态：已索引 %d 个片段。\n用法: /kb <查询>", c.app.Knowledge.ChunkCount())
	}
	results := c.app.Knowledge.Search(query, 5)
	return knowledge.Format(results)
}

// CreateIdleTask implements tui.Callback — creates an off-peak task for /idle.
func (c *chatCallback) CreateIdleTask(name, prompt string) string {
	if c.app == nil || c.app.Scheduler == nil {
		return "调度器不可用（需持久化后端）。"
	}
	t, err := c.app.Scheduler.Create(name, prompt, "idle")
	if err != nil {
		return "创建闲时任务失败: " + err.Error()
	}
	return fmt.Sprintf("✓ 已创建闲时任务「%s」（ID: %s）\n将在闲时窗口（低峰时段）自动执行，完成后通知你。", t.Name, t.ID)
}

// OnPermissionNote implements tui.Callback — records the reason the user gave
// when rejecting a tool call (Tab note on the permission prompt), so the agent
// sees it on the next turn instead of retrying blindly.
func (c *chatCallback) OnPermissionNote(toolPrompt, note string) {
	if c.app == nil || c.app.SessStore == nil || c.sessionID == "" {
		return
	}
	sess, err := c.app.SessStore.Get(c.sessionID)
	if err != nil {
		return
	}
	sess.Messages = append(sess.Messages, types.Message{
		Role:      types.RoleSystem,
		Content:   fmt.Sprintf("用户拒绝了这次操作，原因：%s", note),
		Timestamp: time.Now(),
	})
	_ = c.app.SessStore.Update(sess)
	c.tui.AddMessage(tui.RoleSystem, "已拒绝并说明："+note)
}

// OnMCPApply implements tui.Callback — applies an MCP server change to the
// live shared pool (connect/disconnect) and refreshes the engine's tool
// registry, so /mcp add|remove take effect immediately without a restart
// (Claude Code parity). The config file is already persisted by the TUI;
// this only handles the live connection.
func (c *chatCallback) OnMCPApply(action string, name string, cfg config.MCPServerCfg) string {
	a := c.app
	if a == nil || a.MCPPool == nil {
		return "⚠ MCP 池未初始化（改动已保存，重启后生效）"
	}
	switch action {
	case "add":
		// Idempotent reconnect: drop any previous connection for this name.
		a.MCPPool.Remove(name)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := a.MCPPool.Add(ctx, mcp.ServerConfigFrom(cfg)); err != nil {
			return fmt.Sprintf("⚠ 已保存 %s，但连接失败: %s（重启后重试）", name, err.Error())
		}
	case "remove":
		a.MCPPool.Remove(name)
	default:
		return "未知操作: " + action
	}
	a.RefreshMCPTools()
	if action == "add" {
		return fmt.Sprintf("✓ MCP 服务器 %s 已连接，注册 %d 个工具（立即生效）", name, len(a.MCPPool.ToolsByServer(name)))
	}
	return fmt.Sprintf("✓ MCP 服务器 %s 已断开并注销其工具（立即生效）", name)
}

// formatInt renders an integer with thousands separators.
func formatInt(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// estimateCost mirrors core/conversation.calculateCost for the CLI status bar.
func estimateCost(u types.TokenUsage, mi types.ModelInfo) float64 {
	if len(mi.Plans) == 0 {
		return 0
	}
	plan := mi.Plans[0]
	cacheHit := u.CacheHitTokens
	if cacheHit > u.PromptTokens {
		cacheHit = u.PromptTokens
	}
	inputCost := float64(u.PromptTokens-cacheHit) * plan.InputPrice / 1e6
	outputCost := float64(u.CompletionTokens) * plan.OutputPrice / 1e6
	cacheCost := float64(cacheHit) * plan.CachePrice / 1e6
	return inputCost + outputCost + cacheCost
}

func primaryCurrency(mi types.ModelInfo) string {
	if len(mi.Plans) > 0 && mi.Plans[0].Currency != "" {
		return mi.Plans[0].Currency
	}
	return "USD"
}

func formatCost(v float64, cur string) string {
	sym := "$"
	if cur == "CNY" {
		sym = "¥"
	}
	if v <= 0 {
		return sym + "0.0000"
	}
	return fmt.Sprintf("%s%.4f", sym, v)
}

func countApplied(results []string) int {
	n := 0
	for _, r := range results {
		if strings.HasPrefix(r, "APPLIED") {
			n++
		}
	}
	return n
}
