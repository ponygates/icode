package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/searchreplace"
	"github.com/ponygates/icode/internal/tui"
)

// slashEdits handles the staged search_replace edit workflow: review, undo,
// apply, reject.
func (c *chatCallback) slashEdits(cmd string, args []string) bool {
	switch cmd {
	case "/review":
		c.slashReview()
		return true
	case "/undo":
		c.slashUndo(args)
		return true
	case "/apply":
		c.slashApply()
		return true
	case "/reject":
		c.slashReject()
		return true
	}
	return false
}

// slashReview prints the staged edits with a coloured diff per valid edit.
func (c *chatCallback) slashReview() {
	edits := searchreplace.StageForSessionOrProcess(c.sessionID).List()
	if len(edits) == 0 {
		c.tui.AddMessage(tui.RoleSystem, "No staged edits. Use the search_replace tool to propose changes first.")
		return
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("📋 Staged edits (%d):\n", len(edits)))
	for i, ed := range edits {
		status := "✓ valid"
		if !ed.Valid {
			status = "✗ invalid"
		}
		b.WriteString(fmt.Sprintf("\n── #%d %s [%s] ──\n", i, ed.FilePath, status))
		b.WriteString(fmt.Sprintf("   Reason: %s\n", ed.Reason))
		if ed.Valid && ed.Diff != "" {
			// Show diff with colored markers
			for _, line := range strings.Split(ed.Diff, "\n") {
				if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "@@") {
					b.WriteString(fmt.Sprintf("  %s\n", line))
				} else if strings.HasPrefix(line, "-") {
					b.WriteString(fmt.Sprintf("  \033[31m%s\033[0m\n", line))
				} else if strings.HasPrefix(line, "+") {
					b.WriteString(fmt.Sprintf("  \033[32m%s\033[0m\n", line))
				} else {
					b.WriteString(fmt.Sprintf("  %s\n", line))
				}
			}
		} else if !ed.Valid {
			b.WriteString(fmt.Sprintf("  (search text not found — cannot generate diff)\n"))
		}
	}
	b.WriteString("\n/apply   — apply all valid staged edits")
	b.WriteString("\n/reject  — discard staged edits")
	c.tui.AddMessage(tui.RoleSystem, b.String())
}

// slashUndo implements /undo [N] via the checkpoint store.
func (c *chatCallback) slashUndo(args []string) {
	steps := 1
	if len(args) > 0 {
		fmt.Sscanf(args[0], "%d", &steps)
	}
	if checkpoint.DefaultUndo != nil {
		files, err := checkpoint.DefaultUndo.Undo(context.Background(), steps)
		if err != nil {
			c.tui.AddMessage(tui.RoleSystem, "撤销失败: "+err.Error())
		} else if len(files) == 0 {
			c.tui.AddMessage(tui.RoleSystem, "没有可撤销的更改")
		} else {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已撤销 %d 步，还原了 %d 个文件:", steps, len(files)))
			for _, f := range files {
				c.tui.AddMessage(tui.RoleSystem, "  - "+f)
			}
		}
		return
	}
	c.tui.AddMessage(tui.RoleSystem, "撤销系统未初始化")
}

// slashApply snapshots then applies every valid staged edit, rolling back
// automatically when at least one edit succeeded and another failed.
func (c *chatCallback) slashApply() {
	stage := searchreplace.StageForSessionOrProcess(c.sessionID)
	n := stage.Count()
	if n == 0 {
		c.tui.AddMessage(tui.RoleSystem, "No staged edits to apply.")
		return
	}

	// Snapshot files before applying (for atomic rollback)
	edits := stage.List()
	snapshottedFiles := make(map[string]bool)
	for _, ed := range edits {
		if ed.Valid && ed.FilePath != "" && !snapshottedFiles[ed.FilePath] {
			if checkpoint.DefaultUndo != nil {
				_, _ = checkpoint.DefaultUndo.SnapshotFile(context.Background(), ed.FilePath)
			}
			snapshottedFiles[ed.FilePath] = true
		}
	}

	results := stage.ApplyValid()

	// Check for failures — auto-rollback on any failure
	hasFailures := false
	for _, r := range results {
		c.tui.AddMessage(tui.RoleSystem, r)
		if strings.HasPrefix(r, "FAILED") {
			hasFailures = true
		}
	}

	applied := countApplied(results)
	if hasFailures && applied > 0 && checkpoint.DefaultUndo != nil {
		c.tui.AddMessage(tui.RoleSystem, "⚠️ 编辑失败，自动回滚中...")
		if restored, err := checkpoint.DefaultUndo.Undo(context.Background(), 1); err != nil {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("回滚失败: %v — 请手动 /undo 1", err))
		} else {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("✅ 已回滚 %d 个文件到编辑前的状态", len(restored)))
		}
	}

	c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Applied %d/%d staged edits. Remaining: %d",
		applied, n, stage.Count()))
}

// slashReject discards the staged edits.
func (c *chatCallback) slashReject() {
	stage := searchreplace.StageForSessionOrProcess(c.sessionID)
	n := stage.Count()
	if n == 0 {
		c.tui.AddMessage(tui.RoleSystem, "No staged edits to reject.")
		return
	}
	stage.Clear()
	c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Rejected %d staged edits.", n))
}
