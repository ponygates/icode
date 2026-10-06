package tui

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

// frameRowRe matches the absolute-positioning row writes of a render frame:
// ESC[row;1H followed by ESC[K, optional leading SGR color codes, then the
// row's visible text.
var frameRowRe = regexp.MustCompile(`\x1b\[(\d+);1H\x1b\[K(?:\x1b\[[0-9;]*m)*([^\x1b]*)`)

// TestFrameNoOverwideRows guards the wire-level width clip in render(): every
// painted row must fit the terminal width, because a single over-wide row
// triggers the terminal's autowrap (DECAWM), pushes the whole layout down and
// desynchronises every later cursor move — the "text and garbage crawl into
// the conversation area" corruption. The fixture deliberately includes the
// historically over-wide producers: a many-column Markdown table, a long MCP
// tool name + 60-char arg excerpt, and an ultra-long unbroken word.
func TestFrameNoOverwideRows(t *testing.T) {
	for _, w := range []int{20, 40, 60, 80, 120} {
		tui := &TUI{
			mode:          ModeAgent,
			model:         "deepseek-v4-flash",
			provider:      "deepseek",
			lang:          "zh-CN",
			theme:         "dark",
			rawMode:       true,
			color:         true,
			width:         w,
			height:        30,
			streamDone:    make(chan struct{}, 1),
			statusVisible: true,
		}
		tui.writer = &bytes.Buffer{}
		table := strings.Repeat("| 很长的表头列一 | 长表头列二 | header3 | val |", 4) + "\n" +
			"|---|---|---|---|\n" +
			"| 单元格内容比较长一 | b | c | d |\n" +
			"| 第二行数据 | 数据一 | 数据二 | 数据三 |"
		tui.messages = []Message{
			{Role: RoleUser, Content: table},
			{Role: RoleAssistant, Content: "好"},
			{Role: RoleTool, Tool: "mcp_workbuddy_bridge_search_files",
				ToolArgs: "query=超长的查询参数字符串超长的查询参数字符串超长的查询参数字符串",
				Content:  "ok"},
			{Role: RoleAssistant, Content: "超长单词: " + strings.Repeat("x", 300)},
		}
		tui.render()

		frame := tui.writer.(*bytes.Buffer).String()
		if frame == "" {
			t.Fatalf("width=%d: empty frame", w)
		}
		// Walk every "ESC[row;1H ESC[K [sgr] <text>" segment and verify the
		// visible width of the text (any trailing SGR runs are excluded by
		// the capture group already).
		for _, loc := range frameRowRe.FindAllStringSubmatchIndex(frame, -1) {
			rowStart := loc[0]
			text := frame[loc[4]:loc[5]]
			if vw := visibleWidth(text); vw > w {
				t.Errorf("width=%d: row %s is %d columns wide (max %d): %q",
					w, frame[rowStart:rowStart+12], vw, w, truncForLog(text))
			}
		}
	}
}

// TestInputBoxBelowContentArea guards the inputRows accounting in render():
// the prompt block's top border must start strictly BELOW the content area.
// The old formula forgot the status row and the streaming thinking row, so
// the conversation's last lines were painted over by the box chrome — and
// during generation the spinner landed directly on live content.
func TestInputBoxBelowContentArea(t *testing.T) {
	cases := []struct {
		name      string
		streaming bool
		status    bool
		hasCtx    bool
	}{
		{"plain", false, true, false},
		{"with-ctx", false, true, true},
		{"no-statusline", false, false, false},
		{"streaming", true, true, false},
		{"streaming-ctx", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tui := &TUI{
				mode:          ModeAgent,
				model:         "m",
				provider:      "p",
				lang:          "zh-CN",
				theme:         "dark",
				rawMode:       true,
				color:         true,
				width:         80,
				height:        30,
				streamDone:    make(chan struct{}, 1),
				statusVisible: tc.status,
				streaming:     tc.streaming,
			}
			if tc.hasCtx {
				tui.contextWindow = 100000
				tui.contextTokens = 500
			}
			tui.writer = &bytes.Buffer{}
			tui.messages = []Message{
				{Role: RoleUser, Content: "hi"},
				{Role: RoleAssistant, Content: "hello"},
			}
			tui.streamBuf.WriteString("生成中…")
			tui.render()

			frame := tui.writer.(*bytes.Buffer).String()
			// Find the input box top border row: "ESC[row;1H ESC[K" then a
			// top border beginning with ╭ (leading color codes are skipped
			// by the capture pattern).
			topRow := -1
			lastContentRow := 0
			for _, loc := range frameRowRe.FindAllStringSubmatchIndex(frame, -1) {
				row := atoi10(frame[loc[2]:loc[3]])
				text := frame[loc[4]:loc[5]]
				if topRow < 0 && strings.HasPrefix(text, "╭") {
					topRow = row
				}
				if len(text) > 0 && !strings.HasPrefix(text, "▓") &&
					!strings.HasPrefix(text, "⠋") && topRow < 0 {
					if row > lastContentRow {
						lastContentRow = row
					}
				}
			}
			if topRow < 0 {
				t.Fatalf("case %s: input box top border not found in frame", tc.name)
			}
			if topRow <= lastContentRow {
				t.Errorf("case %s: input box top border (row %d) overlaps content area (last row %d)",
					tc.name, topRow, lastContentRow)
			}
		})
	}
}

func truncForLog(s string) string {
	r := []rune(s)
	if len(r) > 50 {
		return string(r[:50]) + "…"
	}
	return s
}

func atoi10(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// TestAutocompleteRowsClamped verifies autocompleteLines clamps every row to
// the terminal width (long @file descriptions used to overflow and wrap).
func TestAutocompleteRowsClamped(t *testing.T) {
	tui := &TUI{
		lang:    "zh-CN",
		theme:   "dark",
		rawMode: true,
		color:   true,
		acOpen:  true,
		acItems: []acItem{
			{Name: "file-with-a-very-long-name.go", Desc: "超长的文件描述" + strings.Repeat("述", 40)},
		},
		acIdx: 0,
	}
	for _, w := range []int{20, 40, 80} {
		lines := tui.autocompleteLines(w)
		if len(lines) == 0 {
			t.Fatalf("width=%d: no autocomplete lines", w)
		}
		for i, ln := range lines {
			if vw := visibleWidth(ln); vw > w {
				t.Errorf("width=%d: autocomplete row %d is %d cols (max %d): %q",
					w, i, vw, w, truncForLog(ln))
			}
		}
	}
}

// TestMarkdownTableFitsWidth verifies the table column budget: a wide
// multi-column table must shrink its columns to fit the rendered width.
func TestMarkdownTableFitsWidth(t *testing.T) {
	for _, w := range []int{30, 50, 80} {
		tui := &TUI{lang: "zh-CN", theme: "dark", color: true}
		rows := []string{
			"| 列一 | 列二 | 列三 | 列四 | 列五 |",
			"|---|---|---|---|---|",
			"| 数据数据数据数据 | 数据数据 | 数据 | d | e |",
		}
		var out []string
		renderTable(tui, &out, "  ", "  ", w, rows)
		if len(out) == 0 {
			t.Fatalf("width=%d: table not rendered", w)
		}
		for i, ln := range out {
			if vw := visibleWidth(ln); vw > w {
				t.Errorf("width=%d: table row %d is %d cols (max %d): %q",
					w, i, vw, w, truncForLog(ln))
			}
		}
	}
}

// newLayoutTUI builds a TUI wired for full render() calls in layout tests:
// bytes.Buffer writer, raw mode, status line visible, one-line input.
func newLayoutTUI(w, h int) *TUI {
	tui := &TUI{
		mode:          ModeAgent,
		model:         "test-model",
		provider:      "p",
		lang:          "zh-CN",
		theme:         "dark",
		rawMode:       true,
		color:         true,
		width:         w,
		height:        h,
		streamDone:    make(chan struct{}, 1),
		statusVisible: true,
	}
	tui.writer = &bytes.Buffer{}
	tui.messages = []Message{
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "hello"},
	}
	return tui
}

// frameRows extracts "row -> visible text" pairs from a render frame.
func frameRows(frame string) map[int]string {
	rows := map[int]string{}
	for _, loc := range frameRowRe.FindAllStringSubmatchIndex(frame, -1) {
		row := atoi10(frame[loc[2]:loc[3]])
		text := frame[loc[4]:loc[5]]
		// Keep the LAST write per row — later paints win, mirroring the
		// terminal.
		rows[row] = text
	}
	return rows
}

// TestStatusBarAnchoredToBottom guards the bottom-anchored prompt block: the
// status bar must sit on row H no matter which aux rows (context bar,
// thinking indicator) are present. The old topRow formula subtracted the aux
// rows too, so every aux row lifted the status bar one row off the bottom —
// it jumped between H and H-N as aux rows came and went, leaving stale
// copies on screen (the "status bar shows up twice" bug) and wasting the
// bottom row(s) when aux rows were visible.
func TestStatusBarAnchoredToBottom(t *testing.T) {
	cases := []struct {
		name      string
		streaming bool
		hasCtx    bool
	}{
		{"plain", false, false},
		{"with-ctx", false, true},
		{"streaming", true, false},
		{"streaming-ctx", true, true},
	}
	const W, H = 80, 30
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tui := newLayoutTUI(W, H)
			tui.streaming = tc.streaming
			if tc.hasCtx {
				tui.contextWindow = 100000
				tui.contextTokens = 45000 // 45% — nonzero so it is findable
			}
			tui.render()
			frame := tui.writer.(*bytes.Buffer).String()
			rows := frameRows(frame)

			statusRow, bottomRow := -1, -1
			for row, text := range rows {
				// The status row's first visible segment is the colored
				// mode badge (the regex capture skips leading SGR codes).
				// The badge label is localized: [智能体] for agent mode.
				if strings.HasPrefix(text, "[") && strings.Contains(text, "智能体") {
					statusRow = row
				}
				if strings.HasPrefix(text, "╰") {
					bottomRow = row
				}
			}
			if statusRow != H {
				t.Errorf("case %s: status bar on row %d, want bottom row %d", tc.name, statusRow, H)
			}
			if bottomRow != H-1 {
				t.Errorf("case %s: input box bottom border on row %d, want %d", tc.name, bottomRow, H-1)
			}
			// When context is known, the usage percent rides in the
			// status line's right zone (the standalone contextBar row is
			// gone). Assert against the raw frame: frameRowRe captures
			// only each row's first SGR segment, but the percent sits
			// mid-line behind its own colour code.
			if tc.hasCtx && !strings.Contains(frame, "45%") {
				t.Errorf("case %s: context percent missing from status line", tc.name)
			}
		})
	}
}

// TestRenderLayoutShiftWipesScreen guards the full-screen wipe when the
// prompt block grows or shrinks: rows that USED to belong to the prompt
// block become conversation rows, and a blank conversation line at such a
// row never repaints through the incremental diff ("" == ""), so stale
// prompt chrome (an old context bar / status line) would stay on screen
// forever. A layout shift must emit ESC[2J and force a full repaint.
func TestRenderLayoutShiftWipesScreen(t *testing.T) {
	const W, H = 80, 30
	tui := newLayoutTUI(W, H)
	buf := tui.writer.(*bytes.Buffer)

	// Frame 1: first ever render — lastFrame is nil, so it always wipes and
	// paints in full. Not asserted beyond "produces a frame".
	tui.render()
	if buf.Len() == 0 {
		t.Fatalf("frame 1: empty frame")
	}

	// Frame 2: same layout — pure incremental, no wipe allowed.
	buf.Reset()
	tui.render()
	if f2 := buf.String(); strings.Contains(f2, "\x1b[2J") {
		t.Fatalf("frame 2 (no layout change) should be incremental, got ESC[2J")
	}

	// Frame 3: streaming starts — the thinking row joins the prompt block,
	// contentRows shrinks, the layout shifts — full wipe + repaint.
	tui.streaming = true
	tui.turnStart = time.Now()
	buf.Reset()
	tui.render()
	if f3 := buf.String(); !strings.Contains(f3, "\x1b[2J\x1b[H") {
		t.Fatalf("frame 3 (layout shift) must wipe the screen, got no ESC[2J")
	}

	// Frame 4: same layout again — back to incremental.
	buf.Reset()
	tui.render()
	if f4 := buf.String(); strings.Contains(f4, "\x1b[2J") {
		t.Fatalf("frame 4 (no layout change) should be incremental, got ESC[2J")
	}
}

// TestDrawInputBoxClearsBlockRows verifies drawInputBox wipes the ENTIRE
// prompt-block region (aux rows + box + status row) before painting: any
// row vacated by a layout shift would otherwise keep its previous frame's
// content — nothing else clears it.
func TestDrawInputBoxClearsBlockRows(t *testing.T) {
	const W, H = 80, 10
	tui := newLayoutTUI(W, H)
	tui.streaming = true

	frame := tui.drawInputBox(W, H, "x", 1, true, "● m · $0.01")
	// Bottom-anchored layout with the thinking row on: think 6, top border =
	// H-status-vis-2+1 = 7, input 8, bottom border 9, status 10. Every row
	// from clearTop (topRow-think) through H must carry an erase sequence.
	topRow := H - 1 - 1 - 2 + 1 // status=1, vis=1
	clearTop := topRow - 1      // thinkRows=1
	for row := clearTop; row <= H; row++ {
		want := fmt.Sprintf("\x1b[%d;1H\x1b[K", row)
		if !strings.Contains(frame, want) {
			t.Errorf("prompt block row %d not cleared (missing %q)", row, want)
		}
	}
	// And the status bar must be painted on row H itself (bottom anchor).
	if !strings.Contains(frame, fmt.Sprintf("\x1b[%d;1H\x1b[K", H)) ||
		!strings.Contains(frame, "● m · $0.01") {
		t.Errorf("status bar not painted on bottom row %d", H)
	}
}

// TestLayoutStatusLine covers the two-zone status line: left (identity)
// and right (usage) padded to the edges on wide terminals, tail segments
// dropped first on narrow ones — model/cost heads survive, the flash
// notice goes first.
func TestLayoutStatusLine(t *testing.T) {
	tui := &TUI{lang: "zh-CN", theme: "dark", color: true}

	left := []string{"[agent]", "📎 会话标题", "⎇ main", "PR 123 OPEN", "🧩5"}
	right := []string{"● test-model", "$0.0123", "▸1.2k ▸3.4k", "85% cache"}

	// Wide terminal: both zones visible, row padded to exactly W columns,
	// left zone at the left edge, right zone at the right edge.
	line := tui.layoutStatusLine(100, left, right)
	if vw := visibleWidth(line); vw != 100 {
		t.Errorf("wide: visible width %d, want 100", vw)
	}
	if !strings.Contains(line, "[agent]") || !strings.Contains(line, "PR 123 OPEN") {
		t.Errorf("wide: left zone truncated unexpectedly: %q", truncForLog(line))
	}
	if !strings.Contains(line, "85% cache") {
		t.Errorf("wide: right zone truncated unexpectedly: %q", truncForLog(line))
	}

	// Medium: tails start dropping (PR, skills, cache), heads stay.
	line = tui.layoutStatusLine(50, left, right)
	if strings.Contains(line, "PR 123") {
		t.Errorf("medium: PR badge should drop before heads, got %q", truncForLog(line))
	}
	if !strings.Contains(line, "● test-model") || !strings.Contains(line, "$0.0123") {
		t.Errorf("medium: model/cost heads must survive, got %q", truncForLog(line))
	}

	// Narrow: everything optional is gone; model head remains.
	line = tui.layoutStatusLine(20, left, right)
	if vw := visibleWidth(line); vw > 20 {
		t.Errorf("narrow: visible width %d exceeds 20: %q", vw, truncForLog(line))
	}
	if !strings.Contains(line, "● test-model") && !strings.Contains(line, "[agent]") {
		t.Errorf("narrow: lost every head segment: %q", truncForLog(line))
	}

	// Empty zones: blank row, no panic.
	line = tui.layoutStatusLine(40, nil, nil)
	if vw := visibleWidth(line); vw > 40 {
		t.Errorf("empty: visible width %d exceeds 40", vw)
	}
}

// TestStatusPartsZonesAndCtxPct verifies statusParts' two-zone split and
// that the context-usage percent rides in the RIGHT zone — the standalone
// gradient contextBar row is gone; a bare percent is the whole display.
func TestStatusPartsZonesAndCtxPct(t *testing.T) {
	tui := newLayoutTUI(80, 24)
	tui.contextWindow = 100000
	tui.contextTokens = 50000
	tui.cost = "$0.05"

	tui.mu.Lock()
	left, right := tui.statusParts()
	tui.mu.Unlock()

	// Zone split: mode badge on the left, model + cost on the right.
	// (Compare plain text — segments carry SGR colour codes internally.)
	// The badge label is localized: [智能体] for agent mode.
	joined := strings.Join(left, "|") + strings.Join(right, "|")
	if !strings.Contains(joined, "[智能体]") {
		t.Errorf("mode badge missing from left zone: %q", joined)
	}
	if !strings.Contains(joined, "test-model") || !strings.Contains(joined, "$0.05") {
		t.Errorf("model/cost missing from right zone: %q", joined)
	}
	// The context percent MUST appear in the right zone (50000/100000 = 50%,
	// yellow band) — it is the single place context state is displayed.
	if !strings.Contains(joined, "50%") {
		t.Errorf("context percent missing from right zone: %q", joined)
	}
}

// TestThinkingSlider guards the opencode Knight Rider scanner: an 8-cell
// track of dim ⬝ dots with a bright ■ head sweeping right→left, a 6-step
// cyan trail behind it, 40ms per frame and the toggle-pause at both ends
// (9 frames at the right, 30 at the left — the bidirectional cycle of
// packages/tui/src/ui/spinner.ts). No-colour mode keeps the ■/⬝ glyph
// contrast.
func TestThinkingSlider(t *testing.T) {
	tui := newLayoutTUI(80, 24)

	// Frame at 0ms — head at the left edge, no trail yet.
	tui.turnStart = time.Now()
	s0 := tui.thinkingSlider()
	if vw := visibleWidth(s0); vw != 8 {
		t.Errorf("slider visible width %d, want fixed 8", vw)
	}
	if got := stripANSI(s0); got != "■⬝⬝⬝⬝⬝⬝⬝" {
		t.Errorf("frame 0: head should sit at the left edge: %q", got)
	}

	// Frame at 40ms — head advanced one cell, one trail step behind.
	tui.turnStart = time.Now().Add(-40 * time.Millisecond)
	if got := stripANSI(tui.thinkingSlider()); got != "■■⬝⬝⬝⬝⬝⬝" {
		t.Errorf("frame 1: head + trail should fill cells 0..1: %q", got)
	}

	// Frame at 7×40ms — head parked at the right edge with a full trail.
	tui.turnStart = time.Now().Add(-7 * 40 * time.Millisecond)
	if got := stripANSI(tui.thinkingSlider()); got != "⬝⬝■■■■■■" {
		t.Errorf("frame 7: head should sit at the right edge: %q", got)
	}
	// Frame 8: first hold frame at the right edge — same cells.
	tui.turnStart = time.Now().Add(-8 * 40 * time.Millisecond)
	if got := stripANSI(tui.thinkingSlider()); got != "⬝⬝■■■■■■" {
		t.Errorf("frame 8: right-edge hold must keep the head in place: %q", got)
	}

	// Frame 17: sweep back left — head at position 6, trail to its right.
	tui.turnStart = time.Now().Add(-17 * 40 * time.Millisecond)
	if got := stripANSI(tui.thinkingSlider()); got != "⬝⬝⬝⬝⬝⬝■■" {
		t.Errorf("frame 17: head should bounce back to position 6: %q", got)
	}

	// Frame 24: parked back at the left edge.
	tui.turnStart = time.Now().Add(-24 * 40 * time.Millisecond)
	if got := stripANSI(tui.thinkingSlider()); got != "■⬝⬝⬝⬝⬝⬝⬝" {
		t.Errorf("frame 24: left-edge hold must reset the head: %q", got)
	}

	// No-colour mode: same geometry, no SGR codes.
	tui.color = false
	plain := tui.thinkingSlider()
	if strings.Contains(plain, "\x1b") {
		t.Errorf("no-colour slider must not emit SGR codes: %q", plain)
	}
	if vw := visibleWidth(plain); vw != 8 {
		t.Errorf("no-colour slider width %d, want 8", vw)
	}
	if got := stripANSI(plain); got != "■⬝⬝⬝⬝⬝⬝⬝" {
		t.Errorf("no-colour frame 24 must still show the head: %q", got)
	}
}

// TestThinkingSliderInStatusBar verifies the scanner only joins the status
// line while the agent is busy — and that it never breaks the bottom anchor.
func TestThinkingSliderInStatusBar(t *testing.T) {
	// Idle: no slider in either zone.
	idle := newLayoutTUI(80, 24)
	idle.mu.Lock()
	left, _ := idle.statusParts()
	idle.mu.Unlock()
	if joined := strings.Join(left, "|"); strings.Contains(stripANSI(joined), "■") {
		t.Errorf("idle status line must not show the thinking slider: %q", joined)
	}

	// Streaming: the slider rides in the left zone, right after the badge.
	busy := newLayoutTUI(80, 24)
	busy.streaming = true
	busy.turnStart = time.Now().Add(-3 * time.Second)
	busy.mu.Lock()
	left, _ = busy.statusParts()
	busy.mu.Unlock()
	if len(left) < 2 {
		t.Fatalf("streaming left zone too short: %q", left)
	}
	if !strings.Contains(left[0], "[智能体]") {
		t.Errorf("mode badge must stay the head segment: %q", left[0])
	}
	if !strings.Contains(stripANSI(left[1]), "■") {
		t.Errorf("slider missing from left[1] while streaming: %q", left[1])
	}

	// Tool execution without streaming: the slider still rides along
	// (opencode status()!=idle parity).
	toolbusy := newLayoutTUI(80, 24)
	toolbusy.curTool = "bash"
	toolbusy.turnStart = time.Now()
	toolbusy.mu.Lock()
	left, _ = toolbusy.statusParts()
	toolbusy.mu.Unlock()
	if len(left) < 2 || !strings.Contains(stripANSI(strings.Join(left, "|")), "■") {
		t.Errorf("slider missing while a tool runs without streaming: %q", left)
	}

	// Full frame: status bar still anchored to the bottom row with the
	// slider aboard (streaming + slider must not lift it off H).
	busy.render()
	frame := busy.writer.(*bytes.Buffer).String()
	rows := frameRows(frame)
	statusRow := -1
	for row, text := range rows {
		if strings.HasPrefix(text, "[") && strings.Contains(text, "智能体") {
			statusRow = row
		}
	}
	if statusRow != 24 {
		t.Errorf("status bar on row %d, want bottom row 24 (slider must not break the anchor)", statusRow)
	}
}
