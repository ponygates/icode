package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/searchreplace"
	"github.com/ponygates/icode/internal/executil"
)

// reviewCommand runs a code review over a path or the working-tree diff
// (Claude Code's /review). When staged SEARCH/REPLACE edits are pending it
// opens the interactive diff overlay instead; otherwise the gathered content
// is fed to the model as a normal user turn so the assistant streams back
// the analysis.
func (t *TUI) reviewCommand(args []string) {
	var target string
	if len(args) > 0 {
		path := args[0]
		if data, err := os.ReadFile(path); err == nil {
			target = fmt.Sprintf("请审查文件 %s：\n\n%s", path, string(data))
		} else {
			t.add(RoleError, "读取文件失败: "+err.Error())
			return
		}
	} else {
		// With pending staged edits, show the interactive review panel.
		if len(t.stage().List()) > 0 {
			t.openDiffBox()
			return
		}
		cmd := executil.Command("git", "diff")
		out, err := cmd.CombinedOutput()
		if err != nil && len(out) == 0 {
			t.add(RoleError, "git diff 失败: "+err.Error())
			return
		}
		if len(out) == 0 {
			t.add(RoleSystem, "没有未提交的改动可审查。可指定路径：/review <file>")
			return
		}
		target = "请审查以下 git 工作区差异，指出质量问题、安全隐患与改进建议：\n\n```diff\n" + string(out) + "\n```"
	}
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleUser, Content: target})
	t.streaming = true
	t.streamBuf.Reset()
	t.turnStart = time.Now()
	t.mu.Unlock()
	if t.callback != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.add(RoleError, fmt.Sprintf("内部错误: %v", r))
				}
			}()
			t.callback.OnSend(target, nil)
		}()
	}
	t.ensureAnim()
	t.drainStream()
}

func (t *TUI) stage() *searchreplace.StagingArea {
	var sid string
	if t.callback != nil {
		sid = t.callback.SessionID()
	}
	return searchreplace.StageForSessionOrProcess(sid)
}

func (t *TUI) applyStagedEdits() {
	edits := t.stage().List()
	if len(edits) == 0 {
		t.add(RoleSystem, "没有可应用的暂存编辑。")
		return
	}
	snapshottedFiles := make(map[string]bool)
	for _, ed := range edits {
		if ed.Valid && ed.FilePath != "" && !snapshottedFiles[ed.FilePath] {
			if checkpoint.DefaultUndo != nil {
				_, _ = checkpoint.DefaultUndo.SnapshotFile(context.Background(), ed.FilePath)
			}
			snapshottedFiles[ed.FilePath] = true
		}
	}
	results := t.stage().ApplyValid()
	for _, r := range results {
		t.add(RoleSystem, r)
	}
}

func (t *TUI) rejectStagedEdits() {
	stage := t.stage()
	n := stage.Count()
	if n == 0 {
		t.add(RoleSystem, "没有可丢弃的暂存编辑。")
		return
	}
	stage.Clear()
	t.add(RoleSystem, fmt.Sprintf("已丢弃 %d 条暂存编辑。", n))
}

// openDiffBox enters the staged-edits review overlay (Claude Code parity):
// it shows one unified diff per staged SEARCH/REPLACE edit, lets the user
// walk through them with ↑/↓, then Enter to apply all, Ctrl+Z to reject all,
// or Esc/any printable key to dismiss leaving the edits staged.
func (t *TUI) openDiffBox() {
	if !t.rawMode {
		t.add(RoleSystem, "已暂存修改：/review 查看，/apply 全部应用，/reject 全部拒绝")
		return
	}
	edits := t.stage().List()
	if len(edits) == 0 {
		t.add(RoleSystem, "没有可审阅的暂存编辑。")
		return
	}
	t.mu.Lock()
	t.diffBoxOpen = true
	t.diffEdits = edits
	t.diffIdx = 0
	t.mu.Unlock()
	t.render()
}

// closeDiffBox dismisses the review overlay, leaving edits staged.
func (t *TUI) closeDiffBox() {
	t.mu.Lock()
	t.diffBoxOpen = false
	t.diffEdits = nil
	t.diffIdx = 0
	t.mu.Unlock()
	t.render()
}

// diffBoxOverlay renders the staged-edit review panel. Each row is a
// file-path header followed by its unified diff, indented and dimmed.
func (t *TUI) diffBoxOverlay(W, bodyH int) []string {
	title := "已暂存的修改（↑/↓ 查看，Enter 全部应用，Ctrl+Z 全部拒绝，Esc 关闭）："
	var lines []string
	lines = append(lines, t.paint("bold", title))
	lines = append(lines, t.hrule(W))
	n := len(t.diffEdits)
	if n == 0 {
		lines = append(lines, t.paint("dim", "  （无暂存修改）"))
		return lines
	}
	// Walk down each edit; the highlighted one (diffIdx) is marked with ▶.
	for i, ed := range t.diffEdits {
		if len(lines) >= bodyH-1 {
			break
		}
		marker := "    "
		if i == t.diffIdx {
			marker = t.paint("green", "▶ ")
		}
		fileRow := marker + t.paint("yellow", ed.FilePath)
		if !ed.Valid {
			fileRow += t.paint("red", "  (无效: "+ed.Reason+")")
		}
		lines = append(lines, fileRow)
		diff := ed.Diff
		if diff == "" {
			lines = append(lines, t.paint("dim", "  （无差异预览）"))
			continue
		}
		for _, dl := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
			if len(lines) >= bodyH-1 {
				break
			}
			col := t.diffColor(dl)
			lines = append(lines, t.paint(col, "    "+dl))
		}
	}
	return lines
}

// diffColor picks the ANSI colour for one unified-diff line.
func (t *TUI) diffColor(line string) string {
	switch {
	case strings.HasPrefix(line, "+"):
		return "green"
	case strings.HasPrefix(line, "-"):
		return "red"
	case strings.HasPrefix(line, "@@"):
		return "cyan"
	default:
		return "dim"
	}
}

// moveDiff shifts the review highlight and refreshes the panel.
func (t *TUI) moveDiff(delta int) {
	t.mu.Lock()
	t.diffIdx += delta
	if t.diffIdx < 0 {
		t.diffIdx = 0
	}
	if t.diffIdx >= len(t.diffEdits) {
		t.diffIdx = len(t.diffEdits) - 1
	}
	t.mu.Unlock()
	if t.diffBoxOpen && t.rawMode {
		t.render()
	}
}
