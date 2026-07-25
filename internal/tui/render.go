package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/permission"
)

// writeCliLog appends a diagnostic message to ~/.icode/cli.log so a crash is
// never silent — the user (or a helper) can inspect it after a "flash close".
func writeCliLog(s string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		fmt.Fprint(os.Stderr, s)
		return
	}
	dir := filepath.Join(home, ".icode")
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "cli.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprint(os.Stderr, s)
		return
	}
	defer f.Close()
	f.WriteString(s)
}

// ── Rendering (raw mode) ─────────────────────────────────────────

func (t *TUI) render() {
	// A render must never crash the whole process. Renders are also triggered
	// from background goroutines (watchResize, the streaming animation ticker,
	// streaming callbacks) that have no caller-level recover, and an uncaught
	// panic in ANY goroutine kills the entire Go process — the classic silent
	// "flash close" (闪退) on launch. Restoring the terminal to a usable state
	// and logging the stack turns that into a diagnosable, recoverable event.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprint(os.Stdout, "\x1b[?25h\x1b[?1049l")
			writeCliLog(fmt.Sprintf("[tui] render panic: %v\n%s", r, debug.Stack()))
		}
	}()

	// Snapshot all state under the data mutex, then write under the render
	// mutex. This keeps render() safe to call from the streaming goroutine
	// (which also appends streamed text and triggers renders) without
	// re-locking t.mu. Width/height are also snapshotted here so a concurrent
	// resize (watchResize) cannot race the render.
	t.mu.Lock()
	msgs := append([]Message{}, t.messages...)
	streaming := t.streaming
	streamContent := t.streamBuf.String()
	inputBuf := t.inputBuf
	cursor := t.cursor
	rawMode := t.rawMode
	status := t.statusLine()
	permPending := t.permPending
	permPrompt := t.permPrompt
	welcomeVisible := t.welcomeVisible
	W := t.width
	H := t.height
	t.mu.Unlock()

	// Clear one-shot flash notice after rendering
	if t.statusNotice != "" {
		t.mu.Lock()
		t.statusNotice = ""
		t.mu.Unlock()
	}

	if !rawMode {
		return // line mode renders incrementally, not full-screen
	}

	// Re-measure terminal dimensions on every render so window resizes are
	// always picked up, even when watchResize's polling misses the change.
	// Update t.width/t.height so other goroutines see the latest values too.
	// Try both stdin and stdout handles — on Windows, GetConsoleScreenBufferInfo
	// may require an output handle on some system configurations.
	if w, h, ok := t.termSize(); ok {
		W, H = w, h
		t.mu.Lock()
		t.width, t.height = w, h
		t.mu.Unlock()
	}

	if W < 20 {
		W = 20
	}
	if H < 10 {
		H = 10
	}

	// ── Layout ───────────────────────────────────────────────────
	// The input owns the bottom 3 rows: 1 prompt line + 1 hint line + 1 status
	// line. Everything else (header, conversation, status bar) lives above.
	const inputRows = 3
	contentRows := H - inputRows
	if contentRows < 4 {
		contentRows = 4
	}

	// Bottom status bar: a single compact line (model · tokens · context% ·
	// cost · cache), truncated to the terminal width so it stays a thin strip.
	statusLine := status
	if visibleWidth(statusLine) > W {
		statusLine = truncVisible(statusLine, W)
	}
	statusW := []string{statusLine}

	// Overlays drawn above the input box.
	acLines := t.autocompleteLines()
	permLines := []string{}
	if permPending {
		// Claude Code-style bordered permission box
		title := "? " + t.tstr("perm.title")
		boxW := min(visibleWidth(title)+4, W-4)
		if boxW < 40 {
			boxW = 40
		}
		if boxW > W-4 {
			boxW = W - 4
		}
		// Truncate the prompt to the inner width by *visible* columns so a
		// long CJK command can't push the right │ out of line.
		prompt := truncVisible(permPrompt, boxW-2)
		opts := "[1] 允许   [2] 全部允许   [3] 拒绝"
		permLines = append(permLines,
			t.paint("yellow", "  ╭"+repeat("─", boxW)+"╮"),
			t.paint("yellow", "  │ ")+t.paint("bold", title)+padVisible("", boxW-visibleWidth(title)-2)+t.paint("yellow", " │"),
			t.paint("dim", "  │ ")+prompt+padVisible("", boxW-visibleWidth(prompt)-2)+t.paint("dim", " │"),
			t.paint("yellow", "  │ ")+t.paint("dim", opts)+padVisible("", boxW-visibleWidth(opts)-2)+t.paint("yellow", " │"),
			t.paint("yellow", "  ╰"+repeat("─", boxW)+"╯"),
		)
	}

	// Body height = rows between the top header/hrule and the bottom (status
	// separator + status). Claude Code is a single column, so the conversation
	// takes the full terminal width.
	bodyH := contentRows - 1 /*header*/ - 2 /*hrule×2*/ - len(statusW) - len(permLines) - len(acLines)
	if bodyH < 3 {
		bodyH = 3
	}

	// Scrollbar: decide availability from the full-width conversation, then
	// render the conversation one column narrower (contentW) so the scrollbar
	// gets a clean gutter column. Help/welcome/permission/autocomplete overlays
	// suppress the scrollbar to avoid visual overlap.
	isWelcome := welcomeVisible && len(msgs) == 0 && !streaming
	convFull := t.conversationLines(msgs, streaming, streamContent, W)
	sbActive := !t.helpVisible && !isWelcome && !permPending && !t.acOpen && W >= 24 && len(convFull) > bodyH

	contentW := W
	if sbActive {
		contentW = W - 1
	}

	conv := convFull
	convTotal := len(conv)
	if sbActive {
		conv = t.conversationLines(msgs, streaming, streamContent, contentW)
		convTotal = len(conv)
	}

	if t.helpVisible {
		conv = t.helpBox(contentW, bodyH)
		t.scrollOffset = 0
		sbActive = false
	} else if isWelcome {
		// Start the welcome at row 1 (right after the hrule). No extra
		// topMargin — that was pushing the logo partially off-screen
		// on terminals whose initial height measurement was inaccurate.
		conv = t.welcomeLines(contentW, bodyH)
		// Welcome mode always shows the latest — reset scroll.
		t.scrollOffset = 0
		sbActive = false
	} else if convTotal > bodyH && t.scrollOffset > 0 {
		// User has scrolled up: show N lines above the bottom.
		total := convTotal
		maxOff := total - bodyH
		if t.scrollOffset > maxOff {
			t.scrollOffset = maxOff
		}
		start := total - bodyH - t.scrollOffset
		conv = conv[start : start+bodyH]
		// Prepend a scroll indicator.
		indicator := t.paint("yellow", fmt.Sprintf("  ↑ %d more lines — PgDn/End to follow", t.scrollOffset))
		conv = append([]string{""}, conv...)        // blank line
		conv = append([]string{indicator}, conv...) // indicator
		if len(conv) > bodyH {
			conv = conv[:bodyH]
		}
	} else if convTotal > bodyH {
		// Auto-follow: always show the latest content.
		t.scrollOffset = 0
		conv = conv[convTotal-bodyH:]
	}
	if !sbActive {
		contentW = W
	}

	// Assemble the full screen (everything except the final input box).
	convStart := 2 // header(index 0) + hrule(index 1) precede the conversation
	convEnd := convStart + len(conv) - 1
	var out []string
	out = append(out, t.headerLine())
	out = append(out, t.hrule(contentW))
	out = append(out, conv...)
	out = append(out, t.hrule(contentW))
	for _, sl := range statusW {
		out = append(out, sl)
	}
	for _, pl := range permLines {
		out = append(out, pl)
	}
	for _, al := range acLines {
		out = append(out, al)
	}

	// Track the conversation's row range (after any top-trim) so the scrollbar
	// only overlays the conversation body, not the header/hrule/status.
	trimOff := 0
	if len(out) > contentRows {
		trimOff = len(out) - contentRows
		out = out[trimOff:]
	}
	bodyTop := convStart - trimOff + 1
	bodyBottom := convEnd - trimOff + 1
	if bodyTop < 1 {
		bodyTop = 1
	}
	if bodyBottom > contentRows {
		bodyBottom = contentRows
	}
	if bodyBottom < bodyTop {
		bodyBottom = bodyTop
	}

	// Scrollbar thumb position derived from the cached scroll offset.
	thumbRow := bodyBottom
	if sbActive {
		maxOff := 0
		if convTotal > bodyH {
			maxOff = convTotal - bodyH
		}
		if maxOff > 0 {
			// frac: 1 at the top of the track (oldest, max offset), 0 at the
			// bottom (newest, offset 0).
			frac := float64(maxOff-t.scrollOffset) / float64(maxOff)
			thumbRow = bodyTop + int(frac*float64(bodyBottom-bodyTop))
			if thumbRow < bodyTop {
				thumbRow = bodyTop
			}
			if thumbRow > bodyBottom {
				thumbRow = bodyBottom
			}
		}
		// Cache geometry for mouse handlers (click/drag on the scrollbar).
		t.mu.Lock()
		t.sbMaxOff = maxOff
		t.sbTop = bodyTop
		t.sbBottom = bodyBottom
		t.mu.Unlock()
	}

	// ── Write frame (absolute rows, in-place clear) ──────────────
	t.renderMu.Lock()
	defer t.renderMu.Unlock()

	// Full clear when dimensions change, so orphaned text from the previous
	// frame at a different size never bleeds into the new layout.
	if W != t.lastRenderW || H != t.lastRenderH {
		fmt.Fprint(t.writer, "\x1b[2J\x1b[H")
		t.lastRenderW, t.lastRenderH = W, H
	}

	var buf strings.Builder
	buf.WriteString("\x1b[?25l") // hide cursor while repainting
	for i, ln := range out {
		row := i + 1
		if row > contentRows {
			break
		}
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
		buf.WriteString(ln)
		// Overlay the scrollbar gutter over the conversation body rows.
		if sbActive && row >= bodyTop && row <= bodyBottom {
			var glyph string
			if row == thumbRow {
				glyph = t.paint("cyan", "█")
			} else {
				glyph = t.paint("dim", "│")
			}
			buf.WriteString(fmt.Sprintf("\x1b[%d;%dH", row, W))
			buf.WriteString(glyph)
		}
	}
	// Clear any rows left between the content block and the input box so old
	// text from a taller previous frame never lingers.
	for row := len(out) + 1; row <= contentRows; row++ {
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
	}
	fmt.Fprint(t.writer, buf.String())
	t.drawInputBox(contentW, H, inputBuf, cursor, streaming)
}

// headerLine renders the compact top bar: app name (orange "iCode" wordmark),
// working directory, and mode — the signature Claude Code-style header.
func (t *TUI) headerLine() string {
	cwd, _ := os.Getwd()
	short := shortDir(cwd)
	modeLabel := t.mode
	if modeLabel == "" {
		modeLabel = ModeAuto
	}
	modeColor := "dim"
	if modeLabel == "yolo" {
		modeColor = "yellow"
	} else if modeLabel == "plan" {
		modeColor = "cyan"
	}
	return t.paint("orange", "iCode") + " " + t.paint("dim", appVersionStr()) +
		t.paint("dim", "  ·  ") + t.paint("dim", short) +
		t.paint("dim", "  ·  ") + t.paint(modeColor, modeLabel)
}

// welcomeLines renders the startup screen: an ASCII LOGO (plum blossom +
// block-letter ICODE wordmark, deliberately WITHOUT a surrounding box so it can
// never be mis-aligned) on top, followed by the two startup panels — the LEFT
// panel merges the live session info (model / provider / mode / cwd / context /
// cache / quick commands) with the "Welcome back!" greeting, and the RIGHT panel
// shows tips & what's new. The two panels sit side by side when they fit, and
// stack vertically on narrow terminals.
func (t *TUI) welcomeLines(width, maxH int) []string {
	if maxH < 1 || width < 30 {
		return nil
	}
	logo := t.logoLines(width)
	boxes := t.welcomeBoxes(width)
	if boxes == nil {
		if logo != nil {
			return logo
		}
		return []string{"  " + t.paint("orange", "*") + "  " + t.paint("bold", "Welcome to iCode")}
	}
	// Centre the panel block (side-by-side or stacked) within the terminal so
	// it shares the LOGO's centre axis — the whole welcome stays cohesive.
	boxesW := 0
	for _, l := range boxes {
		if vw := visibleWidth(l); vw > boxesW {
			boxesW = vw
		}
	}
	if indent := (width - boxesW) / 2; indent > 0 {
		pad := strings.Repeat(" ", indent)
		for i := range boxes {
			boxes[i] = pad + boxes[i]
		}
	}
	combined := append(append([]string{}, logo...), append([]string{""}, boxes...)...)
	if len(combined) <= maxH {
		return combined
	}
	// Too tall for the full logo + panels: drop the logo, keep the panels.
	if len(boxes) <= maxH {
		return boxes
	}
	// Still too tall: show the left panel alone (it carries the greeting).
	left := t.welcomeInfoBox(width)
	if left != nil && len(left) <= maxH {
		return left
	}
	return []string{"  " + t.paint("orange", "*") + "  " + t.paint("bold", "Welcome to iCode")}
}

// welcomeBoxes returns the two startup panels as a single block: side by side
// when they fit horizontally, stacked vertically otherwise. Returns nil if
// neither panel can be built.
func (t *TUI) welcomeBoxes(width int) []string {
	leftLines := t.welcomeInfoLines()
	rightLines := t.welcomeTipsLines()

	// Equalise heights so the two panels can be placed side by side with their
	// top and bottom borders perfectly aligned. Keep content top-aligned so the
	// first real line of each panel (Welcome back! / Tips for getting started)
	// sits on the same row, making the two boxes feel horizontally aligned.
	maxLen := len(leftLines)
	if len(rightLines) > maxLen {
		maxLen = len(rightLines)
	}
	leftLines = padSliceBottom(leftLines, maxLen)
	rightLines = padSliceBottom(rightLines, maxLen)

	// Use the same inner width for both boxes so their outer borders line up
	// vertically and the whole block looks like one aligned composition.
	innerW := maxVisibleWidth(leftLines)
	if w := maxVisibleWidth(rightLines); w > innerW {
		innerW = w
	}
	if innerW < 16 {
		innerW = 16
	}
	if innerW+4 > width {
		innerW = width - 4
	}
	if innerW < 6 {
		return nil
	}

	left := t.buildBoxWithInner("cyan", leftLines, innerW, width)
	right := t.buildBoxWithInner("orange", rightLines, innerW, width)
	if left == nil && right == nil {
		return nil
	}
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	gap := 3
	if boxWidth(left[0])+gap+boxWidth(right[0]) <= width {
		return joinSideBySide(left, right, gap)
	}
	// Not enough horizontal room — stack them instead.
	return append(append([]string{}, left...), append([]string{""}, right...)...)
}

// helpBox renders the keyboard-shortcut help overlay (opened with `?` on an
// empty input). It mirrors the bordered-box style used by the permission
// prompt and is capped to the available body height.
func (t *TUI) helpBox(W, bodyH int) []string {
	type row struct{ k, d string }
	rows := []row{
		{"Enter", "发送消息"},
		{"Shift+Tab", "切换模式 auto → plan → agent → yolo"},
		{"↑ / ↓", "历史记录上 / 下"},
		{"← / →", "光标左右移动"},
		{"Home / End", "行首 / 行尾（或 Ctrl+A / Ctrl+E）"},
		{"Ctrl+W / Ctrl+U", "删除前一个词 / 删除到行首"},
		{"Ctrl+L", "清屏并重绘"},
		{"Ctrl+K", "清空输入"},
		{"Ctrl+C / Ctrl+D", "中断 / 退出"},
		{"PgUp / PgDn", "会话上 / 下翻页"},
		{"鼠标滚轮", "滚动会话"},
		{"点击输入行", "移动编辑光标"},
		{"点击 / 拖动滚动条", "跳转滚动位置"},
		{"Tab", "补全 / 切换模型"},
		{"/ 命令", "slash 命令（输入 / 查看）"},
		{"@ 文件", "文件引用补全"},
		{"? ", "显示 / 隐藏本帮助"},
		{"Esc", "取消 / 关闭面板"},
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

// welcomeInfoBox returns the LEFT panel (greeting + live session info) as a
// bordered box.
func (t *TUI) welcomeInfoBox(width int) []string {
	return t.buildBox("cyan", t.welcomeInfoLines(), width)
}

// welcomeTipsBox returns the RIGHT panel (tips & what's new) as a bordered box.
func (t *TUI) welcomeTipsBox(width int) []string {
	return t.buildBox("orange", t.welcomeTipsLines(), width)
}

// welcomeInfoLines builds the LEFT panel content: greeting + live session info.
// This is the content the user asked to pull out of the old (mis-aligned) top
// banner and merge into the new left panel.
func (t *TUI) welcomeInfoLines() []string {
	cwd, _ := os.Getwd()
	short := shortDir(cwd)
	var meter string
	if t.contextWindow > 0 && t.contextTokens >= 0 {
		pct := t.contextTokens * 100 / t.contextWindow
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		cells := 10
		filled := pct * cells / 100
		bar := repeat("█", filled) + repeat("░", cells-filled)
		meter = "Context:  " + fmt.Sprintf("%dK / %dK", t.contextTokens/1000, t.contextWindow/1000) +
			" [" + bar + "] " + fmt.Sprintf("%d%%", pct)
	} else {
		meter = "Context:  —"
	}
	cache := "Cache:    "
	if t.cacheHitRate > 0 {
		cache += fmt.Sprintf("%.0f%%", t.cacheHitRate*100)
	} else {
		cache += "—"
	}
	return []string{
		t.paint("bold", "Welcome back!"),
		"",
		"Model:    " + t.model,
		"Provider: " + t.provider,
		"Mode:     " + t.mode,
		"CWD:      " + short,
		"",
		meter,
		cache,
		"",
		"/help  /model  /provider  /mode  /clear  /exit",
	}
}

// welcomeTipsLines builds the RIGHT panel content: tips & what's new.
func (t *TUI) welcomeTipsLines() []string {
	return []string{
		t.paint("orange", "Tips for getting started"),
		t.paint("orange", "─────────────────────"),
		"  Run /init to create a ICODE.md file with",
		"  instructions for iCode",
		"",
		t.paint("orange", "What's new"),
		t.paint("orange", "──────────"),
		"  Check the iCode changelog for updates",
	}
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

// maxVisibleWidth returns the largest visible width among the supplied lines.
func maxVisibleWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		if vw := visibleWidth(l); vw > w {
			w = vw
		}
	}
	return w
}

// padSliceBottom pads a slice with empty strings at the bottom so it reaches n
// items. Side-by-side boxes keep their first content line on the same row,
// which makes the two panels look horizontally aligned.
func padSliceBottom(items []string, n int) []string {
	if len(items) >= n {
		return items
	}
	out := make([]string, n)
	copy(out, items)
	for i := len(items); i < n; i++ {
		out[i] = ""
	}
	return out
}

// joinSideBySide zips two equal-height box blocks row by row, separated by gap
// spaces. Both boxes must already have the same number of rows (the caller
// equalises content via welcomeBoxes).
func joinSideBySide(a, b []string, gap int) []string {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	gapStr := strings.Repeat(" ", gap)
	out := make([]string, n)
	for i := 0; i < n; i++ {
		x, y := "", ""
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		out[i] = x + gapStr + y
	}
	return out
}

// boxWidth returns the visible width of a box's top (or bottom) border line —
// i.e. the full rendered width of that box.
func boxWidth(topLine string) int {
	return visibleWidth(topLine)
}

func (t *TUI) messageLinesW(m Message, width int) []string {
	switch m.Role {
	case RoleThinking:
		return thinkingLines(m.Content, width)
	case RoleUser:
		return wrapPrefixed(t.paint("cyan", "❯ ")+"  ", "    ", m.Content, width)
	case RoleAssistant:
		if t.rawMode {
			return t.renderMarkdown(m.Content, "", "  ", width)
		}
		return wrapPrefixed("  ", "  ", m.Content, width)
	case RoleSystem:
		if t.rawMode {
			return t.renderMarkdown(m.Content, "", "  ", width)
		}
		return wrapPrefixed("  ", "  ", m.Content, width)
	case RoleError:
		return wrapPrefixed(t.paint("red", "× ")+"  ", "    ", m.Content, width)
	case RoleTool:
		var out []string
		pre := t.paint("cyan", "⏺ ") + " "
		head := pre + m.Tool
		// Hide empty/no-op parameter objects like "{}" so the tool line
		// shows "* git_status" instead of "* git_status {}".
		args := strings.TrimSpace(m.ToolArgs)
		if args == "{}" || args == "" {
			args = ""
		}
		if args != "" {
			head += " " + truncate(args, 60)
		}
		out = append(out, t.paint("cyan", head))
		if m.Content != "" {
			toolOutput := m.Content
			// Claude Code-style: fold long tool output with a summary line
			const maxLines = 8
			if !t.toolFolded {
				fold := strings.Split(toolOutput, "\n")
				if len(fold) > maxLines {
					toolOutput = strings.Join(fold[:maxLines], "\n") + "\n" +
						t.paint("dim", fmt.Sprintf("    ⎿  ... %d more lines (use /expand to show all)", len(fold)-maxLines))
				}
			}
			for _, l := range wrapPrefixed("    ⎿ ", "      ", toolOutput, width) {
				out = append(out, t.paint("dim", l))
			}
		}
		return out
	}
	return wrapPrefixed("  ", "  ", m.Content, width)
}

// conversationLines builds the full-width conversation: every message (+ the
// in-flight stream). Claude Code-style thin dim separators divide turns.
// While the model is "thinking" (stream started but no tokens yet) a prominent
// animated thinking box is shown with the rotating spinner and sliding gradient bar.
func (t *TUI) conversationLines(msgs []Message, streaming bool, streamContent string, width int) []string {
	sep := t.paint("dim", "  "+strings.Repeat("─", min(width-2, 80)))
	var lines []string
	all := append([]Message{}, msgs...)
	if streaming {
		all = append(all, Message{Role: RoleAssistant, Content: streamContent})
	}
	for i, m := range all {
		// Replace the empty in-flight assistant message with the thinking box.
		if streaming && i == len(all)-1 && strings.TrimSpace(m.Content) == "" {
			continue
		}
		// Insert thin separator between turns, except before the first message
		// or before a tool message (which is part of the same assistant turn).
		if i > 0 && m.Role != RoleTool && all[i-1].Role != RoleAssistant {
			lines = append(lines, "")
			lines = append(lines, sep)
			lines = append(lines, "")
		} else if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, t.messageLinesW(m, width)...)
	}
	if streaming && strings.TrimSpace(streamContent) == "" {
		lines = append(lines, "")
		lines = append(lines, t.thinkingBox(width)...)
	}
	return lines
}

// thinkingBox renders the framed "thinking" indicator — a bordered box
// containing the spinning glyph and sliding gradient bar, styled to match
// Claude Code's streaming thinking state. Returns individual lines.
func (t *TUI) thinkingBox(width int) []string {
	// Build a headline line with the spinner and slider
	headline := "  " + t.tstr("status.gen") + "  " + t.thinkingBar()

	boxW := width - 2
	if boxW < 24 {
		boxW = 24
	}
	inner := t.paint("dim", "│") + " " + padVisible(headline, boxW-4) + " " + t.paint("dim", "│")
	top := t.paint("dim", "┌"+strings.Repeat("─", boxW-2)+"┐")
	bot := t.paint("dim", "└"+strings.Repeat("─", boxW-2)+"┘")

	return []string{top, inner, bot}
}

// padVisible pads s with spaces to reach the given display width, accounting
// for embedded ANSI escape sequences that take zero visible columns.
func padVisible(s string, w int) string {
	vw := visibleWidth(s)
	if vw >= w {
		return s
	}
	return s + strings.Repeat(" ", w-vw)
}

// contextBar renders a "NN% ▓▓░░" usage meter of width w (display columns).
// Colour-coded: green (<50%), yellow (50–80%), red (>80%) — Claude Code style.
func (t *TUI) contextBar(w int) string {
	if w < 6 {
		return strings.Repeat("░", w)
	}
	pct := 0
	if t.contextWindow > 0 && t.contextTokens > 0 {
		pct = t.contextTokens * 100 / t.contextWindow
	}
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	barW := w - 4
	if barW < 1 {
		barW = 1
	}
	filled := barW * pct / 100

	// Colour-coded threshold
	colour := t.c("green") // < 50%
	if pct > 80 {
		colour = t.c("red")
	} else if pct > 50 {
		colour = t.c("yellow")
	}
	pctStr := fmt.Sprintf("%2d%%", pct)
	if pct > 80 {
		pctStr = t.paint("bold", pctStr)
	}
	reset := "\x1b[0m"
	if !t.color {
		colour, reset = "", ""
		pctStr = fmt.Sprintf("%2d%%", pct)
	}
	return colour + pctStr + " " + strings.Repeat("▓", filled) + strings.Repeat("░", barW-filled) + reset
}

// thinkingBar is an animated "thinking" indicator inspired by Claude Code's
// glimmer bar. It combines a rotating spinner on the left with a growing
// gradient bar on the right that sweeps back and forth. The combined effect
// gives smooth, continuous motion feedback while the model works.
//
// Visual:  ◌ [▓▓▓▓▓░░░░░░░░░]  32%  ⏱ 12s
func (t *TUI) thinkingBar() string {
	const trackLen = 16
	elapsed := time.Since(t.turnStart)
	frame := int(elapsed.Milliseconds() / 100)
	if frame < 0 {
		frame = 0
	}

	// ── Spinner ──
	spinners := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinner := spinners[frame%len(spinners)]

	// ── Context progress bar ──
	// Use actual context usage when available, otherwise animate.
	var filled int
	if t.contextWindow > 0 && t.contextTokens > 0 {
		filled = t.contextTokens * trackLen / t.contextWindow
		if filled > trackLen {
			filled = trackLen
		}
	} else {
		// Animated growing bar during stream
		filled = (frame % (trackLen + 1))
	}

	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < trackLen; i++ {
		if i < filled {
			if i < filled-1 {
				b.WriteString("▓")
			} else {
				b.WriteString("▒")
			}
		} else {
			b.WriteString("░")
		}
	}
	b.WriteString("]")

	// Context percentage
	var pctStr string
	if t.contextWindow > 0 && t.contextTokens > 0 {
		pct := t.contextTokens * 100 / t.contextWindow
		if pct > 100 {
			pct = 100
		}
		pctStr = fmt.Sprintf(" %d%%", pct)
	}

	return t.paint("cyan", spinner) + " " + b.String() + t.paint("dim", pctStr)
}

// fit returns s padded (or truncated with an ellipsis) to exactly w *visible*
// columns, so split panes line up under the vertical frame line. ANSI escape
// sequences are measured as zero width so colored lines still align.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if visibleWidth(s) > w {
		return truncVisible(s, w)
	}
	return s + strings.Repeat(" ", w-visibleWidth(s))
}

// fitVis pins s to EXACTLY w visible columns: it hard-truncates (no overflow,
// even for CJK runes that would otherwise push truncVisible one cell past w)
// and pads with spaces. ANSI escape sequences are measured as zero width and
// preserved, so colored box content still aligns. It is used to build framed
// boxes whose borders must line up on every row regardless of content width.
func fitVis(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var b strings.Builder
	cur := 0
	inEsc := false
	for _, ch := range s {
		if inEsc {
			b.WriteRune(ch)
			if ch == 'm' {
				inEsc = false
			}
			continue
		}
		if ch == '\x1b' {
			inEsc = true
			b.WriteRune(ch)
			continue
		}
		cw := runeWidth(ch)
		if cur+cw > w {
			break
		}
		b.WriteRune(ch)
		cur += cw
	}
	vw := visibleWidth(b.String())
	if vw < w {
		b.WriteString(strings.Repeat(" ", w-vw))
	}
	return b.String()
}

// visibleWidth returns the display width of s, ignoring ANSI escape sequences.
func visibleWidth(s string) int {
	w := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		w += runeWidth(r)
	}
	return w
}

// truncVisible truncates s to w visible columns, preserving ANSI sequences and
// appending an ellipsis if anything was cut. A reset code is appended when a
// colored run is cut, so color never bleeds past the frame line.
func truncVisible(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var b strings.Builder
	cur := 0
	inEsc := false
	cut := false
	for _, ch := range s {
		if inEsc {
			b.WriteRune(ch)
			if ch == 'm' {
				inEsc = false
			}
			continue
		}
		if ch == '\x1b' {
			inEsc = true
			b.WriteRune(ch)
			continue
		}
		if cur >= w-1 {
			cut = true
			break
		}
		b.WriteRune(ch)
		cur += runeWidth(ch)
	}
	if cut {
		if inEsc {
			b.WriteString("\x1b[0m") // close any open color before the ellipsis
		}
		b.WriteString("…")
	}
	return b.String()
}

// ensureAnim starts a lightweight ticker that repaints the screen while a
// generation is in flight, so the thinking bar keeps sliding even between
// token bursts. It is idempotent.
func (t *TUI) ensureAnim() {
	t.mu.Lock()
	if t.animRunning {
		t.mu.Unlock()
		return
	}
	t.animRunning = true
	t.mu.Unlock()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				writeCliLog(fmt.Sprintf("[tui] anim ticker panic: %v\n%s", r, debug.Stack()))
			}
		}()
		for t.streaming || t.running {
			t.mu.Lock()
			if !t.streaming {
				t.animRunning = false
				t.mu.Unlock()
				return
			}
			t.mu.Unlock()
			t.scheduleRender()
			time.Sleep(110 * time.Millisecond)
		}
		t.mu.Lock()
		t.streaming = false
		t.animRunning = false
		t.mu.Unlock()
	}()

}

// SetContext records the latest request's prompt-token count and the model's
// context window so the status bar / explorer can show live context usage.
func (t *TUI) SetContext(tokens, window int) {
	t.mu.Lock()
	t.contextTokens = tokens
	t.contextWindow = window
	t.mu.Unlock()
}

// listCwd returns the top-level entries of the current working directory
// (dotfiles and hidden entries skipped), used by the explorer pane.
func listCwd() []string {
	dir, err := os.Getwd()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 14 {
		names = names[:14]
	}
	return names
}

// statusLine renders the Claude Code-style bottom status bar: a colored model
// dot, provider, token usage, context %, cache hit rate, and running cost.
// All coloring is applied here (the renderer appends the line verbatim, so no
// nested ANSI wrapping occurs). During generation it shows the elapsed time
// and the sliding thinking bar.
func (t *TUI) statusLine() string {
	d := func(s string) string { return t.paint("dim", s) }
	var parts []string
	parts = append(parts, t.paint("green", "*")+" "+t.model)
	if t.provider != "" {
		parts = append(parts, d(t.provider))
	}
	// Security level badge — always visible so the user knows their privacy
	// boundary. Unlike Claude Code, no hidden telemetry or phone-home.
	if t.securityLevel != "" && t.securityLevel != "local" {
		label := permission.SecurityLabel(config.SecurityLevel(t.securityLevel))
		parts = append(parts, label)
	}
	if t.promptTokens > 0 || t.completionTokens > 0 {
		parts = append(parts,
			d("↑"+formatTokens(t.promptTokens)+" ↓"+formatTokens(t.completionTokens)))
	}
	if t.contextWindow > 0 && t.contextTokens > 0 {
		pct := t.contextTokens * 100 / t.contextWindow
		if pct > 100 {
			pct = 100
		}
		parts = append(parts, d(fmt.Sprintf("%d%% ctx", pct)))
		// Visual context progress bar
		barW := 10
		filled := pct * barW / 100
		if filled > barW {
			filled = barW
		}
		bar := "["
		for i := 0; i < barW; i++ {
			if i < filled {
				bar += "▓"
			} else {
				bar += "░"
			}
		}
		bar += "]"
		parts = append(parts, d(bar))
	}
	if t.cacheHitRate > 0 {
		parts = append(parts, d(fmt.Sprintf("%.0f%% cache", t.cacheHitRate*100)))
	}
	if t.cost != "" {
		parts = append(parts, d(t.cost))
	}
	// Todo counters — shown when the current session has an active todo
	// list. Pending items appear in dim, in-progress in yellow, completed
	// dim. Zero-list sessions render nothing.
	if t.callback != nil {
		if pending, active, done, total := t.callback.TodoCounts(); total > 0 {
			seg := fmt.Sprintf("[ ]%d >%d [x]%d", pending, active, done)
			if active > 0 {
				seg = t.paint("yellow", seg)
			} else {
				seg = d(seg)
			}
			parts = append(parts, seg)
		}
	}
	if t.streaming {
		if !t.turnStart.IsZero() {
			parts = append(parts, d("T "+formatDuration(time.Since(t.turnStart))))
		}
		// Thinking bar is already shown in the conversation area (thinkingBox),
		// so don't duplicate it here — Claude Code shows the spinner only once.
	}
	// Flash notice (slash command feedback)
	if t.statusNotice != "" {
		parts = append(parts, t.statusNotice)
	}
	return strings.Join(parts, d(" · "))
}

// formatDuration renders a duration compactly: "3.2s" or "1m04s".
func formatDuration(d time.Duration) string {
	d = d.Round(time.Millisecond)
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d / time.Minute)
	s := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", m, s)
}

// modeColor returns the ANSI color name for the current mode indicator,
// matching Claude Code's mode coloring (plan=blue, yolo=red, …).
func modeColor(m Mode) string {
	switch m {
	case ModePlan:
		return "blue"
	case ModeAuto:
		return "green"
	case ModeYOLO:
		return "red"
	default:
		// Agent default — Claude Code's signature orange/red
		return "orange"
	}
}

// drawInputBox renders the Claude Code-style single-line prompt — NO box, just
// `> <input>` on a single row. The hint bar (manual mode · shortcuts · agents)
// and the status bar (max · /effort) sit on the rows below. This is the exact
// layout from the Claude Code v2.x screenshots.
//
//	> <input>                                   row topRow
//	  manual mode on · ? for shortcuts · ↵ for agents   row topRow+1
//	                                          ⊙max · /effort   row topRow+2
func (t *TUI) drawInputBox(W, H int, inputBuf string, cursor int, streaming bool) {
	const inputRows = 3
	topRow := H - inputRows + 1
	if topRow < 1 {
		topRow = 1
	}
	hintRow := topRow + 1
	statusRow := topRow + 2

	// Prompt line: "❯ <input>" (Claude Code style)
	prompt := t.paint(modeColor(t.mode), "❯")
	innerW := W - 4
	if innerW < 4 {
		innerW = 4
	}
	content := inputBuf
	if visibleWidth(content) > innerW {
		content = truncVisible(content, innerW)
	}
	line := prompt + " " + content
	if visibleWidth(line) > W {
		line = truncVisible(line, W)
	}

	// Hint row (left side)
	hint := t.paint("dim", "  "+t.tstr("input.hint"))
	if streaming {
		hint = t.paint("dim", "  "+t.tstr("input.hint.streaming"))
	}

	// Status row (right side, model-style)
	effort := t.paint("dim", "*") + "max"
	if t.mode == ModeYOLO {
		effort = t.paint("yellow", "*") + "yolo"
	}
	rightStatus := effort + t.paint("dim", " · /effort")
	// Right-align
	pad := W - visibleWidth(rightStatus) - 2
	if pad < 0 {
		pad = 0
	}
	statusPadded := strings.Repeat(" ", pad) + rightStatus

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow))
	b.WriteString(line)
	b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", hintRow))
	b.WriteString(hint)
	b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", statusRow))
	b.WriteString(statusPadded)

	// Position the cursor on the input line, just after the typed prefix.
	runes := []rune(inputBuf)
	if cursor > len(runes) {
		cursor = len(runes)
	}
	vw := visibleWidth(string(runes[:cursor]))
	if vw > innerW {
		vw = innerW
	}
	col := 2 + vw // "❯ "(2) → content starts at column 2
	if col > W {
		col = W
	}
	if col < 1 {
		col = 1
	}
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", topRow, col))
	if streaming {
		b.WriteString("\x1b[?25l")
	} else {
		b.WriteString("\x1b[?25h")
	}
	fmt.Fprint(t.writer, b.String())
}

// ── Scrolling support ───────────────────────────────────────────

// convHeight returns the number of rows available for conversation content.
func (t *TUI) convHeight() int {
	return t.height - 7 // header(1) + hrule(1) + hrule(1) + status(1) + perm(2) + input(4) — rough min
}

// totalConvLines counts all display lines for the current conversation.
func (t *TUI) totalConvLines(msgs []Message, streaming bool, streamContent string, width int) int {
	return len(t.conversationLines(msgs, streaming, streamContent, width))
}

// scrollPgUp scrolls one page up (or to top).
func (t *TUI) scrollPgUp() {
	t.mu.Lock()
	defer t.mu.Unlock()
	bodyH := t.convHeight()
	if bodyH < 1 {
		bodyH = 10
	}
	t.scrollOffset += bodyH
	if t.welcomeVisible {
		t.welcomeVisible = false
	}
	t.scheduleRender()
}

// scrollPgDn scrolls one page down (or to bottom / auto-follow).
func (t *TUI) scrollPgDn() {
	t.mu.Lock()
	defer t.mu.Unlock()
	bodyH := t.convHeight()
	if bodyH < 1 {
		bodyH = 10
	}
	t.scrollOffset -= bodyH
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	t.scheduleRender()
}

// scrollToTop jumps to the oldest conversation lines.
func (t *TUI) scrollToTop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	msgs := append([]Message{}, t.messages...)
	total := len(t.conversationLines(msgs, t.streaming, t.streamBuf.String(), t.width))
	bodyH := t.convHeight()
	if total > bodyH {
		t.scrollOffset = total - bodyH
	}
	t.scheduleRender()
}

// scrollToBottom resumes auto-follow (scroll to latest content).
func (t *TUI) scrollToBottom() {
	t.mu.Lock()
	t.scrollOffset = 0
	t.mu.Unlock()
	t.scheduleRender()
}

// scrollUpSmall moves up a few lines.
func (t *TUI) scrollUpSmall() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scrollOffset += 3
	t.scheduleRender()
}

// scrollDownSmall moves down a few lines.
func (t *TUI) scrollDownSmall() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scrollOffset -= 3
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	t.scheduleRender()
}
