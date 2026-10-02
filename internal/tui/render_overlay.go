package tui

import (
	"fmt"
	"strings"
)

// helpBox renders the keyboard-shortcut help overlay (opened with `?` on an
// empty input). It mirrors the bordered-box style used by the permission
// prompt and is capped to the available body height.
// drawAskOverlay renders the interactive multiple-choice question box
// (Claude Code AskUserQuestion parity): question + numbered options + key
// hint, centered in the body area. Returns content lines (no positioning).
func (t *TUI) drawAskOverlay(W, H int) []string {
	t.mu.Lock()
	ask := t.askPending
	t.mu.Unlock()
	if ask == nil {
		return nil
	}
	inner := []string{
		t.paint("yellow", " 🤔 ") + ask.Question,
		"",
	}
	for i, opt := range ask.Options {
		inner = append(inner, fmt.Sprintf("   %d. %s", i+1, opt))
	}
	inner = append(inner, "", t.paint("dim", "   1-9 选择 · Enter 选第一项 · Esc 取消"))

	// Horizontal centering.
	lines := make([]string, 0, len(inner)+2)
	for _, ln := range inner {
		pad := (W - visibleWidth(ln)) / 2
		if pad < 0 {
			pad = 0
		}
		lines = append(lines, strings.Repeat(" ", pad)+ln)
	}
	// Vertical centering within the body height.
	top := (H - len(lines)) / 2
	if top < 0 {
		top = 0
	}
	padded := make([]string, 0, top+len(lines))
	for i := 0; i < top; i++ {
		padded = append(padded, "")
	}
	return append(padded, lines...)
}

// drawAskFormOverlay renders the multi-question wizard (opencode AskQuestion
// parity): progress line + current question + options (multi-select shows ✓
// picks) + key hints, centered in the body area.
func (t *TUI) drawAskFormOverlay(W, H int) []string {
	t.mu.Lock()
	fs := t.askForm
	t.mu.Unlock()
	if fs == nil || fs.Idx >= len(fs.Questions) {
		return nil
	}
	q := fs.Questions[fs.Idx]
	total := len(fs.Questions)
	inner := []string{
		t.paint("yellow", " 🤔 "+fmt.Sprintf("问题 %d/%d", fs.Idx+1, total)) + q.Question,
		"",
	}
	if q.Text {
		inner = append(inner, t.paint("dim", "   请输入回答（Enter 确认 · Tab 跳过）"))
	} else {
		for i, opt := range q.Options {
			mark := " "
			if q.Multi && i < len(fs.Picked) && fs.Picked[i] {
				mark = "✓"
			}
			prefix := fmt.Sprintf(" %s %d.", mark, i+1)
			if q.Multi {
				inner = append(inner, prefix+" "+opt)
			} else {
				inner = append(inner, "    "+fmt.Sprintf("%d.", i+1)+" "+opt)
			}
		}
		inner = append(inner, "")
		if q.Multi {
			inner = append(inner, t.paint("dim", "   1-9 切换选择 · Enter 确认本题 · Tab 下一题 · Esc 取消"))
		} else {
			inner = append(inner, t.paint("dim", "   1-9 选择 · Tab 下一题 · Esc 取消"))
		}
	}

	// Horizontal centering.
	lines := make([]string, 0, len(inner)+2)
	for _, ln := range inner {
		pad := (W - visibleWidth(ln)) / 2
		if pad < 0 {
			pad = 0
		}
		lines = append(lines, strings.Repeat(" ", pad)+ln)
	}
	// Vertical centering within the body height.
	top := (H - len(lines)) / 2
	if top < 0 {
		top = 0
	}
	padded := make([]string, 0, top+len(lines))
	for i := 0; i < top; i++ {
		padded = append(padded, "")
	}
	return append(padded, lines...)
}

func (t *TUI) helpBox(W, bodyH int) []string {
	type row struct{ k, d string }
	rows := []row{
		{"Enter", "发送消息"},
		{"Shift+Tab", "切换模式 plan → agent → yolo → auto（三端一致）"},
		{"↑ / ↓", "历史记录上 / 下（Claude Code）"},
		{"Ctrl+R", "反向搜索历史（isearch）"},
		{"← / →", "光标左右移动"},
		{"Home / End", "行首 / 行尾（或 Ctrl+A / Ctrl+E）"},
		{"Ctrl+W / Ctrl+U", "删除前一个词 / 删除到行首"},
		{"Ctrl+L", "清屏并重绘"},
		{"Ctrl+K", "清空输入"},
		{"Ctrl+,", "打开设置面板"},
		{"Ctrl+C / Ctrl+D", "中断 / 退出"},
		{"Ctrl+P / Ctrl+N", "历史记录上 / 下"},
		{"PgUp / PgDn", "会话上 / 下翻页"},
		{"鼠标滚轮", "滚动会话"},
		{"点击输入行", "移动编辑光标"},
		{"点击 / 拖动滚动条", "跳转滚动位置"},
		{"Shift+鼠标拖动", "选择文本（终端原生，随时可用）"},
		{"/mouse", "开关鼠标交互（关 = 恢复原生拖选）"},
		{"Tab", "补全 / 切换模型"},
		{"/ 命令", "slash 命令（输入 / 查看）"},
		{"@ 文件", "文件引用补全"},
		{"? ", "显示 / 隐藏本帮助"},
		{"Esc", "中断生成 / 取消 / 关闭面板"},
	}
	title := "键盘快捷键 (Shortcuts)"
	boxW := W - 6
	if boxW < 40 {
		boxW = 40
	}
	if boxW > W-4 {
		boxW = W - 4
	}
	var lines []string
	lines = append(lines, t.paint("cyan", "  ╭"+repeat("─", boxW)+"╮"))
	lines = append(lines, "  │ "+t.paint("bold", title)+padVisible("", boxW-visibleWidth(title)-2)+t.paint("dim", " │"))
	lines = append(lines, t.paint("dim", "  ├"+repeat("─", boxW)+"┤"))
	for _, r := range rows {
		if len(lines) >= bodyH {
			break
		}
		content := r.k + "   " + r.d
		line := "  │ " + t.paint("green", r.k) + "   " + r.d +
			padVisible("", boxW-visibleWidth(content)-2) + t.paint("dim", " │")
		lines = append(lines, line)
	}
	lines = append(lines, t.paint("cyan", "  ╰"+repeat("─", boxW)+"╯"))
	return lines
}

// buildBox wraps content lines in a single bordered box (╭╮╰╯ corners) using
// fitVis() for every row, so the left/right borders line up with the corners on
// every line — including CJK content and the coloured progress meter. Empty
// content lines become blank bordered rows, which lets two equalised boxes sit
// side by side without mis-aligning their borders.
func (t *TUI) buildBox(color string, lines []string, width int) []string {
	innerW := maxVisibleWidth(lines)
	if innerW < 16 {
		innerW = 16
	}
	if innerW+4 > width {
		innerW = width - 4
	}
	if innerW < 6 {
		return nil
	}
	return t.buildBoxWithInner(color, lines, innerW, width)
}

// buildBoxWithInner is buildBox with the inner width already decided. This lets
// two boxes be built to exactly the same width so their borders line up when
// placed side by side.
func (t *TUI) buildBoxWithInner(color string, lines []string, innerW, width int) []string {
	if innerW+4 > width {
		innerW = width - 4
	}
	if innerW < 6 {
		return nil
	}
	c := t.c(color)
	reset := "\x1b[0m"
	if !t.color {
		c, reset = "", ""
	}
	bar := repeat("─", innerW+2)
	top := c + "╭" + bar + "╮" + reset
	bot := c + "╰" + bar + "╯" + reset
	out := []string{top}
	for _, l := range lines {
		if l == "" {
			out = append(out, c+"│ "+reset+strings.Repeat(" ", innerW)+c+" │"+reset)
		} else {
			out = append(out, c+"│ "+reset+fitVis(l, innerW)+c+" │"+reset)
		}
	}
	out = append(out, bot)
	return out
}
