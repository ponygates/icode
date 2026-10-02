package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ponygates/icode/internal/config"
)

// openResumePicker shows the saved-session selector. Raw mode gets an
// interactive overlay (↑/↓ + Enter); line mode falls back to a numbered list.
func (t *TUI) openResumePicker() {
	if t.callback == nil {
		t.add(RoleSystem, "用法: /resume <session-id> [--lite[=<n>] | --compact[=<n>]]")
		return
	}
	t.resumeSessions = t.callback.OnListSessionsStructured(20)
	if len(t.resumeSessions) == 0 {
		t.add(RoleSystem, "没有可恢复的历史会话。开始对话后会自动创建。")
		return
	}
	if !t.rawMode {
		var b strings.Builder
		b.WriteString("历史会话（/resume <session-id> 恢复，--compact 语义摘要压缩）:\n")
		for i, s := range t.resumeSessions {
			b.WriteString(fmt.Sprintf("  %-3d %s  %s  [%s]\n", i+1, s.ID, s.Title, s.Model))
		}
		t.add(RoleSystem, b.String())
		return
	}
	t.mu.Lock()
	t.resumePickerOpen = true
	t.resumePickerIdx = 0
	t.resumePickerTop = 0
	t.mu.Unlock()
	t.render()
}

// resumePickerOverlay renders the /resume selector as a fixed overlay.
func (t *TUI) resumePickerOverlay(W, bodyH int) []string {
	title := "选择要恢复的会话（↑/↓ 移动，Enter 恢复，Esc 取消）："
	const titleRows = 1
	maxRows := bodyH - titleRows
	if maxRows < 1 {
		maxRows = 1
	}
	n := len(t.resumeSessions)
	if n == 0 {
		return []string{title, t.paint("dim", "  （暂无历史会话）")}
	}
	if n > maxRows {
		maxRows = bodyH - titleRows - 1
		if maxRows < 1 {
			maxRows = 1
		}
	}
	if t.resumePickerIdx < t.resumePickerTop {
		t.resumePickerTop = t.resumePickerIdx
	}
	if t.resumePickerIdx >= t.resumePickerTop+maxRows {
		t.resumePickerTop = t.resumePickerIdx - maxRows + 1
	}
	if t.resumePickerTop > n-maxRows {
		t.resumePickerTop = n - maxRows
	}
	if t.resumePickerTop < 0 {
		t.resumePickerTop = 0
	}
	var lines []string
	lines = append(lines, t.paint("bold", title))
	for i := t.resumePickerTop; i < t.resumePickerTop+maxRows && i < n; i++ {
		s := t.resumeSessions[i]
		num := fmt.Sprintf("%-3d", i+1)
		title := truncate(s.Title, 40)
		meta := s.ID
		if len(meta) > 24 {
			meta = meta[:24] + "…"
		}
		row := "  " + num + title + t.paint("dim", "  "+meta+"  "+s.Updated+"  ["+s.Model+"]")
		if i == t.resumePickerIdx {
			row = "  " + t.paint("green", "▶ ") + num + t.paint("green", title) + t.paint("dim", "  "+meta+"  "+s.Updated+"  ["+s.Model+"]")
		}
		lines = append(lines, row)
	}
	if n > maxRows {
		above := t.resumePickerTop
		below := n - (t.resumePickerTop + maxRows)
		lines = append(lines, t.paint("dim",
			fmt.Sprintf("  ↑ %d 更多  ·  ↓ %d 更多  (共 %d)", above, below, n)))
	}
	return lines
}

// moveResumePicker shifts the /resume highlight.
func (t *TUI) moveResumePicker(delta int) {
	n := len(t.resumeSessions)
	if n == 0 {
		return
	}
	t.resumePickerIdx += delta
	if t.resumePickerIdx < 0 {
		t.resumePickerIdx = 0
	}
	if t.resumePickerIdx >= n {
		t.resumePickerIdx = n - 1
	}
	t.render()
}

// resumeSessionAt resumes the session at index i and closes the picker.
func (t *TUI) resumeSessionAt(i int) {
	if i < 0 || i >= len(t.resumeSessions) {
		t.closeResumePicker()
		return
	}
	id := t.resumeSessions[i].ID
	t.closeResumePicker()
	if t.callback != nil {
		msg := t.callback.OnResume(id)
		if msg != "" {
			t.add(RoleSystem, msg)
		}
	}
}

// closeResumePicker exits the /resume selector.
func (t *TUI) closeResumePicker() {
	if !t.resumePickerOpen {
		return
	}
	t.resumePickerOpen = false
	t.resumeSessions = nil
	t.resumePickerTop = 0
	if t.rawMode {
		t.render()
	}
}

func indexOfString(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// changeDir implements /cd: moves the TUI's working directory and refreshes
// the explorer pane. Mirrors slashui.cmdCD so both ends behave identically.
func (t *TUI) changeDir(args []string) {
	cwd, _ := os.Getwd()
	if len(args) == 0 {
		t.add(RoleSystem, "当前工作目录: "+cwd+"\n用法: /cd <path> — 移动会话工作目录（相对路径基于当前目录解析）")
		return
	}
	target := args[0]
	if target == "~" || target == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			target = home
		}
	} else if strings.HasPrefix(target, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			target = filepath.Join(home, strings.TrimPrefix(target, "~/"))
		}
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		t.add(RoleError, "解析路径失败: "+err.Error())
		return
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		t.add(RoleError, "目录不存在或不是文件夹: "+abs)
		return
	}
	if err := os.Chdir(abs); err != nil {
		t.add(RoleError, "切换工作目录失败: "+err.Error())
		return
	}
	if ncwd, err := os.Getwd(); err == nil {
		abs = ncwd
	}
	// Persist the directory so the CLI/TUI/server start there next launch.
	persistSetting(func(c *config.Config) { c.Defaults.WorkingDir = abs })
	// Refresh the explorer pane listing and the prompt-line dir badge.
	t.mu.Lock()
	t.dirEntries = listCwd()
	t.mu.Unlock()
	// Re-notify the engine so tools (bash, file read/write) resolve relative
	// paths from the new directory on the next turn.
	t.notice("工作目录已切换到: " + abs)
	t.add(RoleSystem, "✓ 工作目录已切换到: "+abs)
}

// renameSession implements /rename: retitles the active session via the
// callback (backend store) so the sidebar / resume list reflect it.
func (t *TUI) renameSession(args []string) {
	if len(args) == 0 {
		t.add(RoleSystem, "用法: /rename <新标题> — 重命名当前会话")
		return
	}
	title := strings.Join(args, " ")
	if t.callback != nil {
		msg := t.callback.OnRenameSession(title)
		if msg != "" {
			t.add(RoleError, msg)
			return
		}
		t.mu.Lock()
		t.sessionTitle = title
		t.mu.Unlock()
		t.add(RoleSystem, "✓ 会话已重命名为: "+title)
		t.scheduleRender()
		return
	}
	t.add(RoleSystem, "引擎未初始化，无法重命名。")
}
