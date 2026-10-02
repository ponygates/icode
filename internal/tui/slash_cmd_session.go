package tui

import (
	"fmt"
	"strings"
)

// handleSlashSession dispatches the conversation/session-scoped commands.
// It reports whether the command was claimed.
func (t *TUI) handleSlashSession(cmd string, args []string) bool {
	switch cmd {
	case "/exit", "/quit":
		t.slashExit()
	case "/session", "/sessions":
		t.slashSessions()
	case "/new", "/newsession":
		t.slashNewSession()
	case "/resume":
		t.slashResume(args)
	case "/fork", "/branch":
		t.slashFork(cmd, args)
	case "/restore":
		t.slashRestore(args)
	case "/goal":
		t.slashGoal(args)
	case "/budget":
		t.slashBudget(args)
	case "/clear":
		t.slashClear()
	case "/wipe":
		t.slashWipe()
	case "/search":
		t.slashSearch(args)
	case "/summarize":
		t.slashSummarize()
	case "/cd":
		t.changeDir(args)
	case "/rename":
		t.renameSession(args)
	case "/export":
		t.exportMarkdown(args)
	case "/copy":
		t.copyLastAssistant(args)
	case "/share":
		// Claude Code /share parity: render the conversation as ONE
		// self-contained HTML file (embedded CSS + inline syntax
		// highlighting) under ~/.icode/shares/ — local-only, no upload.
		// Plain-Markdown export stays on /export.
		t.shareCommand(args)
	default:
		return false
	}
	return true
}

// slashExit archives a zero-token session summary, then ends the run loop.
func (t *TUI) slashExit() {
	// Summary-on-exit: archive a zero-token session summary so a later
	// /resume can rebuild context without re-reading the full transcript.
	if t.callback != nil {
		t.callback.OnSlashCommand("/summarize", nil)
	}
	t.add(RoleSystem, "再见！👋")
	t.running = false
}

func (t *TUI) slashSessions() {
	if t.callback != nil {
		t.add(RoleSystem, t.callback.OnListSessions())
	}
}

func (t *TUI) slashNewSession() {
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
}

func (t *TUI) slashResume(args []string) {
	if len(args) > 0 && t.callback != nil {
		t.callback.OnSlashCommand("/resume", args)
	} else if t.callback != nil {
		// No session ID → interactive picker (raw mode) or a static
		// numbered list (line mode).
		t.openResumePicker()
	}
}

func (t *TUI) slashFork(cmd string, args []string) {
	cmdName := cmd
	if len(args) > 0 && t.callback != nil {
		t.callback.OnSlashCommand("/fork", args)
	} else {
		t.add(RoleSystem, "用法: "+cmdName+" <session-id>[@<n>] — 从历史会话分支出一个独立会话")
	}
}

// slashRestore un-soft-deletes a session (/clear marks it deleted). Forwarded
// to the backend store so the flag and the message list are repaired together.
func (t *TUI) slashRestore(args []string) {
	if t.callback != nil {
		t.callback.OnSlashCommand("/restore", args)
	} else {
		t.add(RoleSystem, "用法: /restore <session-id> — 恢复被 /clear 软删除的会话")
	}
}

func (t *TUI) slashGoal(args []string) {
	if t.callback != nil {
		t.callback.OnSlashCommand("/goal", args)
	} else {
		t.add(RoleSystem, "用法: /goal set <目标> | /goal show | /goal clear")
	}
}

func (t *TUI) slashBudget(args []string) {
	if t.callback != nil {
		t.callback.OnSlashCommand("/budget", args)
	} else {
		t.add(RoleSystem, "用法: /budget set <上限token数> | /budget show | /budget clear")
	}
}

// slashClear archives the current session through the callback first (so it
// stays recoverable with /restore), then wipes the local message list.
func (t *TUI) slashClear() {
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
}

func (t *TUI) slashWipe() {
	t.mu.Lock()
	t.messages = nil
	t.promptTokens = 0
	t.completionTokens = 0
	t.cost = ""
	t.cacheHitRate = 0
	t.scrollOffset = 0
	t.mu.Unlock()
	t.add(RoleSystem, "对话已清空并重置。")
}

func (t *TUI) slashSearch(args []string) {
	query := strings.Join(args, " ")
	if query == "" {
		t.add(RoleSystem, "用法: /search <关键词> — 搜索历史对话")
		return
	}
	if t.callback != nil {
		t.callback.OnSlashCommand("/search", args)
	} else {
		t.add(RoleSystem, "搜索需要会话存储支持。")
	}
}

// slashSummarize prints a local recap of the transcript and archives a
// zero-token summary for a later /resume.
func (t *TUI) slashSummarize() {
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
}
