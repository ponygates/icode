package tui

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/executil"
)

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
// recapCommand implements /recap (Claude Code parity): generate a short,
// non-destructive session recap — what's done, where it stands, what's next.
// Unlike /compact it never rewrites the conversation history.
func (t *TUI) recapCommand() {
	t.autoRecap("⏳ 正在生成会话回顾…")
}

// autoRecap runs a non-destructive session recap. caption is shown while the
// model summarises; reused by both the /recap command and the idle auto-recap.
// Returns false (and reports the too-short reason only when caption is set, i.e.
// a manual /recap) when the conversation has fewer than 3 turns.
func (t *TUI) autoRecap(caption string) bool {
	t.mu.Lock()
	turns := 0
	for _, m := range t.messages {
		if m.Role == RoleUser || m.Role == RoleAssistant {
			turns++
		}
	}
	t.mu.Unlock()
	if turns < 3 {
		if caption != "" {
			t.add(RoleSystem, "对话还太短，暂无可回顾的内容（至少 3 轮）。")
		}
		return false
	}
	t.add(RoleSystem, caption)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.add(RoleError, fmt.Sprintf("生成回顾失败: %v", r))
			}
		}()
		sum := ""
		if t.callback != nil {
			sum = t.callback.OnCompactSummarize("生成简短的会话回顾：已完成什么、当前进展、下一步建议")
		}
		if strings.TrimSpace(sum) == "" {
			t.add(RoleSystem, "（模型不可用，无法生成回顾）")
			return
		}
		// Cap at 400 characters, same as Claude Code's recap limit.
		runes := []rune(strings.TrimSpace(sum))
		if len(runes) > 400 {
			sum = string(runes[:400]) + "…"
		}
		t.add(RoleSystem, "📋 会话回顾：\n"+sum)
	}()
	return true
}

// checkAutoRecap fires a one-shot session recap when the user has been idle for
// ≥3 minutes with no streaming in flight (Claude Code parity: "auto-recap after
// you walk away"). It is cooled down (10 min) so it never spams, and is skipped
// entirely while a turn is generating. Called from the main loop's idle tick.
func (t *TUI) checkAutoRecap() {
	t.mu.Lock()
	streaming := t.streaming
	idle := time.Since(t.lastActivity)
	last := t.lastAutoRecap
	t.mu.Unlock()
	if streaming || idle < 3*time.Minute {
		return
	}
	if !last.IsZero() && time.Since(last) < 10*time.Minute {
		return
	}
	t.mu.Lock()
	t.lastAutoRecap = time.Now()
	t.mu.Unlock()
	t.autoRecap("💤 你已离开 3 分钟，自动为你回顾一下当前会话…")
}

func (t *TUI) compactCommand(args []string) {
	instruction := strings.Join(args, " ")
	t.mu.Lock()
	if len(t.messages) < 4 {
		t.mu.Unlock()
		t.add(RoleSystem, "消息太少，无需压缩（至少 4 条）。")
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

// redoStep re-applies the most recent /undo or /rewind via the checkpoint
// store (opencode /redo parity).
func (t *TUI) redoStep() {
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
	files, err := store.Redo(context.Background())
	if err != nil {
		t.add(RoleError, "重做失败: "+err.Error())
		return
	}
	msg := "✓ 已重做。影响文件:\n"
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
		t.add(RoleError, "导出失败: "+err.Error())
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
		t.add(RoleError, "git diff 失败: "+err.Error())
		return
	}
	if len(output) == 0 {
		t.add(RoleSystem, "没有未暂存的改动。")
		return
	}
	t.AddToolMessage("git_diff", "", t.colorizeDiffStr(strings.TrimRight(string(output), "\n")))
}
