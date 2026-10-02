package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/core/checkpoint"
)

// handleSlashWorkspace dispatches repository / tooling commands: diffs, LSP
// and knowledge queries, checkpoints (rewind/replay/undo/redo) and the staged
// edit review flow. It reports whether the command was claimed.
func (t *TUI) handleSlashWorkspace(cmd string, args []string) bool {
	switch cmd {
	case "/diff":
		t.showGitDiff(args)
	case "/review":
		t.reviewCommand(args)
	case "/compact":
		t.compactCommand(args)
	case "/recap":
		t.recapCommand()
	case "/lsp":
		t.slashLSP(args)
	case "/kb", "/knowledge":
		t.add(RoleSystem, t.callback.KnowledgeQuery(strings.Join(args, " ")))
	case "/idle":
		t.slashIdle(args)
	case "/rewind", "/checkpoint":
		t.slashRewind(args)
	case "/undo":
		t.rewindSteps(1)
	case "/redo":
		t.redoStep()
	case "/replay":
		t.slashReplay()
	case "/apply":
		if t.callback != nil {
			t.callback.OnSlashCommand("/apply", args)
		} else {
			t.applyStagedEdits()
		}
	case "/reject":
		if t.callback != nil {
			t.callback.OnSlashCommand("/reject", args)
		} else {
			t.rejectStagedEdits()
		}
	default:
		return false
	}
	return true
}

func (t *TUI) slashLSP(args []string) {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	t.add(RoleSystem, t.callback.LSPQuery(sub, args))
}

func (t *TUI) slashIdle(args []string) {
	if len(args) < 2 {
		t.add(RoleSystem, "用法: /idle <名称> <任务描述>（闲时窗口内自动执行）")
		return
	}
	t.add(RoleSystem, t.callback.CreateIdleTask(args[0], strings.Join(args[1:], " ")))
}

func (t *TUI) slashRewind(args []string) {
	n := 1
	if len(args) > 0 {
		fmt.Sscanf(args[0], "%d", &n)
	}
	if n <= 0 {
		t.add(RoleSystem, "用法: /rewind [N]  — 回滚前 N 步工具调用")
		return
	}
	t.rewindSteps(n)
}

// slashReplay shows the checkpoint timeline: interactive overlay in raw mode,
// plain listing in line mode.
func (t *TUI) slashReplay() {
	// Checkpoint timeline (Claude Code /replay parity): interactive
	// overlay in raw mode; falls back to a plain listing in line mode.
	if t.rawMode {
		t.openReplay()
		return
	}
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
	entries, err := store.List(context.Background())
	if err != nil || len(entries) == 0 {
		t.add(RoleSystem, "本会话还没有检查点。模型修改文件后会自动创建。")
		return
	}
	var b strings.Builder
	b.WriteString("⏪ 检查点时间轴（新 → 旧）:\n")
	for i, e := range entries {
		fmt.Fprintf(&b, "  #%-4d %s  %s\n", len(entries)-1-i, e.When.Format("01-02 15:04:05"), truncRunes(e.Message, 50))
	}
	b.WriteString("\n（交互模式 /replay 可逐步查看 diff；/rewind N 回滚）")
	t.add(RoleSystem, b.String())
}
