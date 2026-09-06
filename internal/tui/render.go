package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/skills"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/xgo"
)

// cliLogRotator is a package-level rotating writer so cli.log (panic stack
// traces from the TUI) can't grow unbounded across many crashes. 1 MB cap —
// crash dumps are infrequent but can be large; keep one backup.
var (
	cliLogOnce    sync.Once
	cliLogRotator *xgo.RotatingWriter
)

func cliLogWriter() *xgo.RotatingWriter {
	cliLogOnce.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return
		}
		path := filepath.Join(home, ".icode", "cli.log")
		if rw, err := xgo.NewRotatingWriter(path, 1024*1024); err == nil {
			cliLogRotator = rw
		}
	})
	return cliLogRotator
}

// writeCliLog appends a diagnostic message to ~/.icode/cli.log so a crash is
// never silent — the user (or a helper) can inspect it after a "flash close".
func writeCliLog(s string) {
	if rw := cliLogWriter(); rw != nil {
		rw.Write([]byte(s))
		return
	}
	// Fallback (home dir unavailable or rotator init failed): stderr.
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
	searchMode := t.searchMode
	searchBuf := t.searchBuf
	var searchCur string
	if t.searchMode && t.searchIdx >= 0 && t.searchIdx < len(t.searchMatches) {
		searchCur = t.searchMatches[t.searchIdx]
	}
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
	// Bottom chrome is the multi-line prompt box (`❯ input`, grows with
	// content up to a cap and scrolls internally) plus a single compact status
	// bar underneath it (hidden entirely when /statusline is toggled off).
	// Everything else (header, conversation, overlays) lives above those rows.
	inputRows := t.inputVisibleLines(H)
	if n := t.inputLineCount(); n < inputRows {
		inputRows = n
	}
	// Claude Code-style context bar sits directly above the input box, so it
	// needs its own reserved row whenever context usage is known.
	if t.contextWindow > 0 && t.contextTokens >= 0 {
		inputRows++
	}
	// Streaming-time queue indicator ("⏳ queued / typing") above the box.
	inputRows += t.queueRows()
	contentRows := H - inputRows
	if contentRows < 4 {
		contentRows = 4
	}

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
		// buildPrompt may return a multi-line summary (e.g. write_file shows
		// the content excerpt, edit shows old→new). Render each line inside the
		// box, wrapping long lines so the argument preview stays readable.
		// The prompt is plain text (colours are applied at render time), so
		// wrapping by runes is safe and never splits a UTF-8 sequence.
		innerW := boxW - 2
		var wrapped []string
		for _, pl := range strings.Split(permPrompt, "\n") {
			runes := []rune(pl)
			for len(runes) > 0 {
				w := 0
				n := 0
				for n < len(runes) {
					cw := runeWidth(runes[n])
					if w+cw > innerW {
						break
					}
					w += cw
					n++
				}
				if n == 0 {
					n = 1 // guard: a single rune wider than the box
				}
				wrapped = append(wrapped, string(runes[:n]))
				runes = runes[n:]
			}
		}
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		opts := "[1] 允许   [2] 全部允许   [3] 拒绝   [Tab] 加说明"
		permLines = append(permLines,
			t.paint("yellow", "  ╭"+repeat("─", boxW)+"╮"),
			t.paint("yellow", "  │ ")+t.paint("bold", title)+padVisible("", boxW-visibleWidth(title)-2)+t.paint("yellow", " │"),
		)
		for _, pl := range wrapped {
			permLines = append(permLines,
				t.paint("dim", "  │ ")+pl+padVisible("", boxW-visibleWidth(pl)-2)+t.paint("dim", " │"),
			)
		}
		// Tab opens an inline note field (Claude Code parity): the typed
		// reason is handed to the agent with the decision, so it knows WHY an
		// action was rejected instead of guessing.
		if t.permNoteOpen {
			noteLabel := "说明: "
			shown := t.permNoteBuf
			if visibleWidth(noteLabel+shown) > innerW {
				shown = truncVisible(shown, innerW-visibleWidth(noteLabel)-1)
			}
			noteLine := noteLabel + shown + "▌"
			permLines = append(permLines,
				t.paint("yellow", "  │ ")+t.paint("cyan", "├"+repeat("─", boxW-2)+"┤"),
				t.paint("yellow", "  │ ")+noteLine+padVisible("", boxW-visibleWidth(noteLine)-2)+t.paint("yellow", " │"),
				t.paint("yellow", "  │ ")+t.paint("dim", "Enter 提交「拒绝并说明」· Esc 取消说明")+padVisible("", boxW-visibleWidth("Enter 提交「拒绝并说明」· Esc 取消说明")-2)+t.paint("yellow", " │"),
			)
		}
		permLines = append(permLines,
			t.paint("yellow", "  │ ")+t.paint("dim", opts)+padVisible("", boxW-visibleWidth(opts)-2)+t.paint("yellow", " │"),
			t.paint("yellow", "  ╰"+repeat("─", boxW)+"╯"),
		)
	}

	// Body height = rows between the header rule and the bottom prompt block.
	// opencode chrome above the conversation: header (1) + rule (1). The status
	// bar is part of the prompt block (drawn by drawInputBox), not a separate
	// strip.
	bodyH := contentRows - 1 /*header*/ - 1 /*header rule*/ - len(permLines) - len(acLines)
	if bodyH < 3 {
		bodyH = 3
	}

	// Scrollbar: decide availability from the full-width conversation, then
	// render the conversation one column narrower (contentW) so the scrollbar
	// gets a clean gutter column. Help/welcome/permission/autocomplete overlays
	// suppress the scrollbar to avoid visual overlap.
	isWelcome := welcomeVisible && len(msgs) == 0 && !streaming
	convFull, headsFull := t.conversationLines(msgs, streaming, streamContent, W)
	sbActive := !t.helpVisible && !isWelcome && !permPending && !t.acOpen && W >= 24 && len(convFull) > bodyH

	contentW := W
	if sbActive {
		contentW = W - 1
	}

	conv, heads := convFull, headsFull
	convTotal := len(conv)
	if sbActive {
		conv, heads = t.conversationLines(msgs, streaming, streamContent, contentW)
		convTotal = len(conv)
	}

	// convBase tracks the index (into the full conversationLines result) of the
	// first line of the FINAL conv slice, so tool card header rows can be mapped
	// back to screen rows for click-to-fold (see toolHeadRows below).
	convBase := 0

	if t.resumePickerOpen {
		conv = t.resumePickerOverlay(W, bodyH)
		t.scrollOffset = 0
		sbActive = false
		heads = nil
	} else if t.modelPickerOpen {
		// Fixed overlay: always fully visible. The panel scrolls its own
		// internal window (modelPickerTop) to keep the highlighted row on
		// screen, independent of the conversation scroll position. It takes
		// precedence over the welcome banner so /model works even on a fresh
		// session (before the first message dismisses the banner).
		conv = t.modelPickerOverlay(W, bodyH)
		t.scrollOffset = 0
		sbActive = false
		heads = nil
	} else if t.askPendingVisible() {
		// Interactive multiple-choice question (Claude Code AskUserQuestion
		// parity): render the question + numbered options centered in the
		// body until the user picks (1-9/Enter/Esc).
		conv = t.drawAskOverlay(contentW, bodyH)
		t.scrollOffset = 0
		sbActive = false
		heads = nil
	} else if t.askFormActive() {
		// Multi-question wizard (opencode AskQuestion parity): render the
		// whole form (progress + current question + options) centered.
		conv = t.drawAskFormOverlay(contentW, bodyH)
		t.scrollOffset = 0
		sbActive = false
		heads = nil
	} else if t.diffBoxOpen {
		conv = t.diffBoxOverlay(contentW, bodyH)
		t.scrollOffset = 0
		sbActive = false
		heads = nil
	} else if t.helpVisible {
		conv = t.helpBox(contentW, bodyH)
		t.scrollOffset = 0
		sbActive = false
		heads = nil
	} else if isWelcome {
		// Start the welcome at row 1 (right after the hrule). No extra
		// topMargin — that was pushing the logo partially off-screen
		// on terminals whose initial height measurement was inaccurate.
		conv = t.welcomeLines(contentW, bodyH)
		// Welcome mode always shows the latest — reset scroll.
		t.scrollOffset = 0
		sbActive = false
		heads = nil
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
		// The indicator+blank pushed the window by 2 rows; convBase must track
		// the FULL index of the first content line so head-row mapping stays
		// correct for click-to-fold while scrolled.
		convBase = start - 2
	} else if convTotal > bodyH {
		// Auto-follow: always show the latest content.
		t.scrollOffset = 0
		conv = conv[convTotal-bodyH:]
		convBase = convTotal - bodyH
	}
	if !sbActive {
		contentW = W
	}

	// Assemble the full screen (everything except the final input box).
	// opencode layout: the header is followed by a thin dim rule, then the
	// conversation flows beneath it all the way down to the prompt block.
	convStart := 2 // header(index 0) + rule(index 1) precede the conversation
	convEnd := convStart + len(conv) - 1
	var out []string
	out = append(out, t.headerLine(W))
	out = append(out, t.hrule(contentW))
	out = append(out, conv...)
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

	// Map tool-card header rows to their messages for click-to-fold. heads is
	// keyed by index into the FULL conversationLines result; convBase is the
	// index of the first line of the final conv window, so the screen row is
	// convStart + (fullIdx - convBase), minus whatever trimOff dropped from the
	// top. Rows scrolled out of view are simply not mapped.
	t.mu.Lock()
	t.toolHeadRows = make(map[int]int, len(heads))
	for fullIdx, msgIdx := range heads {
		outIdx := convStart + (fullIdx - convBase) - trimOff
		if outIdx >= 0 && outIdx < len(out) {
			t.toolHeadRows[outIdx] = msgIdx
		}
	}
	t.mu.Unlock()

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

	// ── Write frame (incremental: only changed rows, absolute positioning) ──
	t.renderMu.Lock()
	defer t.renderMu.Unlock()

	// Full clear when dimensions change, so orphaned text from the previous
	// frame at a different size never bleeds into the new layout.
	if W != t.lastRenderW || H != t.lastRenderH {
		fmt.Fprint(t.writer, "\x1b[2J\x1b[H")
		t.lastRenderW, t.lastRenderH = W, H
		t.lastFrame = nil // force a full repaint at the new size
	}
	if len(t.lastFrame) != contentRows {
		t.lastFrame = make([]string, contentRows)
	}

	var buf strings.Builder
	buf.WriteString("\x1b[?25l") // hide cursor while repainting
	n := len(out)
	if n > contentRows {
		n = contentRows
	}
	for i := 0; i < n; i++ {
		row := i + 1
		ln := out[i]
		changed := ln != t.lastFrame[i]
		var glyph string
		if sbActive && row >= bodyTop && row <= bodyBottom {
			if row == thumbRow {
				glyph = t.paint("cyan", "█")
			} else {
				glyph = t.paint("dim", "│")
			}
		}
		if changed {
			t.lastFrame[i] = ln
			buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
			buf.WriteString(ln)
		}
		// Overlay the scrollbar gutter over the conversation body rows.
		if glyph != "" {
			buf.WriteString(fmt.Sprintf("\x1b[%d;%dH", row, W))
			buf.WriteString(glyph)
		}
	}
	// Clear rows that shrank out of the previous frame (taller → shorter).
	for i := n; i < contentRows; i++ {
		if t.lastFrame[i] != "" {
			t.lastFrame[i] = ""
			buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", i+1))
		}
	}
	// Single-flush frame: the main paint and the input/overlay tail go out in
	// ONE write, so the physical cursor never rests mid-screen between the two.
	// (A cursor left in the log area makes IME composition / typed text land
	// above the input box — the "text appears in the blank space" bug.)
	var tail string
	if t.settingsOpen {
		tail = t.renderSettingsPanel()
	} else if searchMode {
		tail = t.drawSearchBox(contentW, H, searchBuf, searchCur, streaming)
	} else {
		tail = t.drawInputBox(contentW, H, inputBuf, cursor, streaming, status)
	}
	fmt.Fprint(t.writer, buf.String()+tail)
}

// headerLine renders the opencode-style top bar: an orange model dot (●) with
// the bold model name on the left, and the working-directory basename in dim on
// the right. No version, no mode label, no wordmark — the least chromed line
// that still tells you what's driving the session, exactly like opencode.
func (t *TUI) headerLine(W int) string {
	cwd, _ := os.Getwd()
	model := t.model
	left := t.paint("orange", "●") + " " + t.paint("bold", model)
	if model == "" {
		left = t.paint("orange", "●") + " " + t.paint("bold", "iCode")
	}
	right := t.paint("dim", filepath.Base(cwd))

	if visibleWidth(left)+2+visibleWidth(right) < W {
		pad := W - visibleWidth(left) - visibleWidth(right) - 2
		return left + strings.Repeat(" ", pad) + right
	}
	return truncVisible(left, W)
}

// welcomeLines renders the startup screen: an ASCII LOGO (the enlarged "iCode"
// wordmark — yellow-dot i + dim Code, in opencode's minimal style and
// deliberately WITHOUT a surrounding box so it can never be mis-aligned) on
// top, followed by the two startup panels — the LEFT
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
		meter = "Context:  -"
	}
	cache := "Cache:    "
	if t.cacheHitRate > 0 {
		cache += fmt.Sprintf("%.0f%%", t.cacheHitRate*100)
	} else {
		cache += "-"
	}
	// Label→value column: pad every label to a fixed visible width so the
	// values start at the same column no matter how long the label is.
	// Hand-padded literals were the source of the drifting value column.
	kv := func(label, val string) string {
		return label + strings.Repeat(" ", 10-visibleWidth(label)) + val
	}
	return []string{
		t.paint("bold", "Welcome back!"),
		"",
		kv("Model:", t.model),
		kv("Provider:", t.provider),
		kv("Mode:", t.mode),
		kv("CWD:", short),
		"",
		meter,
		cache,
		"",
		"/help  /model  /provider  /mode  /clear  /exit",
	}
}

// welcomeTipsLines builds the RIGHT panel content: tips & what's new.
func (t *TUI) welcomeTipsLines() []string {
	// Underline = title visible width, so the orange rule always matches the
	// heading above it (a fixed dash count drifted when titles changed).
	rule := func(title string) string {
		return repeat("─", visibleWidth(title))
	}
	return []string{
		t.paint("orange", "Tips for getting started"),
		t.paint("orange", rule("Tips for getting started")),
		"  Run /init to create a ICODE.md file with",
		"  instructions for iCode",
		"",
		t.paint("orange", "What's new"),
		t.paint("orange", rule("What's new")),
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

// maxToolLines caps the excerpt shown for a folded tool card.
const maxToolLines = 8

func (t *TUI) messageLinesW(m Message, width int) []string {
	switch m.Role {
	case RoleThinking:
		return thinkingLines(m.Content, width)
	case RoleUser:
		// Claude Code parity: the user's own prompt renders Markdown (code
		// blocks, inline code, lists) the same way replies do.
		if t.rawMode {
			return t.renderMarkdown(m.Content, t.paint("orange", "❯ "), "    ", width)
		}
		return wrapPrefixed(t.paint("orange", "❯ ")+"  ", "    ", m.Content, width)
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
		// opencode-style fold indicator: ▸ collapsed / ▾ expanded. Clicking the
		// header row toggles this block (see handleMouse → toolHeadRows).
		marker := t.paint("yellow", "▸")
		if !m.Folded {
			marker = t.paint("yellow", "▾")
		}
		head := marker + " " + t.paint("cyan", "⏺ "+m.Tool)
		// Hide empty/no-op parameter objects like "{}" so the tool line
		// shows "* git_status" instead of "* git_status {}".
		args := strings.TrimSpace(m.ToolArgs)
		if args == "{}" || args == "" {
			args = ""
		}
		if args != "" {
			head += " " + truncate(args, 60)
		}
		out = append(out, head)
		if m.Content != "" {
			// Per-block folding (Claude Code / opencode style): a folded card
			// shows the header plus a short excerpt; expanded shows everything.
			toolOutput := m.Content
			if t.zenMode {
				// Focus view (Claude Code parity): in zen/focus mode show only
				// the tool header — no output excerpt — so the transcript reads
				// as pure conversation with a one-line activity marker.
				out = append(out, t.paint("dim", "    ⎿ … (工具输出已折叠，/zen 展开)"))
				return out
			}
			if m.Folded {
				fold := strings.Split(toolOutput, "\n")
				if len(fold) > maxToolLines {
					toolOutput = strings.Join(fold[:maxToolLines], "\n") + "\n" +
						t.paint("dim", fmt.Sprintf("    ⎿  ... %d more lines (click ▸ to expand)", len(fold)-maxToolLines))
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
// in-flight stream). Turns are separated by a thin dim rule — the opencode
// message divider — so each new turn is visible at a glance while the chrome
// stays quiet. Tool messages belong to the assistant turn that invoked them,
// so they get no rule above. While the model is "thinking" (stream started but
// no tokens yet) a single animated spinner + gradient bar is shown.
//
// The returned map records, for every tool card, the display-line index of its
// header row → index into msgs. render() uses it to map a mouse click on a card
// header back to the message for per-block fold toggling.
func (t *TUI) conversationLines(msgs []Message, streaming bool, streamContent string, width int) ([]string, map[int]int) {
	var lines []string
	heads := map[int]int{}
	all := append([]Message{}, msgs...)
	if streaming {
		all = append(all, Message{Role: RoleAssistant, Content: streamContent})
	}
	for i, m := range all {
		// Replace the empty in-flight assistant message with the thinking line.
		if streaming && i == len(all)-1 && strings.TrimSpace(m.Content) == "" {
			continue
		}
		// A thin dim rule separates turns; tool messages belong to the
		// assistant turn that invoked them, so they get no rule above.
		if i > 0 && m.Role != RoleTool {
			lines = append(lines, t.paint("dim", repeat("─", width)))
		}
		if m.Role == RoleTool {
			heads[len(lines)] = i // header row of this tool card
		}
		lines = append(lines, t.messageLinesW(m, width)...)
	}
	if streaming && strings.TrimSpace(streamContent) == "" {
		lines = append(lines, "")
		// The thinking indicator lives at the bottom (drawInputBox), not
		// here — keeps the message area from repainting every frame.
	}
	return lines, heads
}

// thinkingBox renders the streaming "thinking" indicator — a single bare line
// (spinner + status text + elapsed, opencode style), deliberately box-less.
func (t *TUI) thinkingBox(width int) []string {
	return []string{"  " + t.thinkingBar()}
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

// thinkingBar is the animated "thinking" indicator in opencode's exact style:
// a braille spinner + dim status text + elapsed clock (+ context % when
// known). No wide ▓▒░ slider — opencode keeps the chrome quiet, so the frame
// has far less changing surface (also eliminates the flicker the old 14-cell
// slider caused on Win10 conhost).
//
// Visual:  ⠋ 正在生成… 32% 12s
func (t *TUI) thinkingBar() string {
	elapsed := time.Since(t.turnStart)
	frame := int(elapsed.Milliseconds() / 100)
	if frame < 0 {
		frame = 0
	}

	// ── Spinner ── braille cycle (opencode's default "dots" spinner).
	spinners := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinner := spinners[frame%len(spinners)]

	// Context percentage when known.
	var pctStr string
	if t.contextWindow > 0 && t.contextTokens > 0 {
		pct := t.contextTokens * 100 / t.contextWindow
		if pct > 100 {
			pct = 100
		}
		pctStr = fmt.Sprintf(" %d%%", pct)
	}
	secs := int(elapsed.Seconds())
	if secs < 0 {
		secs = 0
	}

	// Lead with the active model (dim, truncated) so it is obvious which
	// model is generating — especially when switching models mid-session.
	modelTag := ""
	if m := strings.TrimSpace(t.model); m != "" {
		modelTag = t.paint("dim", truncate(m, 18)+" ")
	}

	// Claude Code parity: token count + live token rate + "esc 中断" hint.
	rateStr := ""
	if t.completionTokens > 0 && secs >= 2 {
		rateStr = t.paint("dim", fmt.Sprintf(" · %d tok/s", t.completionTokens/secs))
	}
	tokStr := ""
	if t.completionTokens > 0 {
		tokStr = t.paint("dim", " ↑"+formatTokens(t.completionTokens))
	}
	return modelTag + t.paint("cyan", spinner) + " " +
		t.paint("dim", t.tstr("status.gen")) +
		tokStr + rateStr +
		t.paint("dim", pctStr) + t.paint("dim", fmt.Sprintf(" %ds", secs)) +
		t.paint("dim", " · esc 中断")
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
			time.Sleep(50 * time.Millisecond)
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

// statusLine renders the Claude Code / opencode-style bottom task bar:
// mode badge, model dot, git branch, token counts, a visual context bar,
// cache, cost, todo counts, current-tool activity, and background-task
// count — joined by dim `·`. While streaming the elapsed time trails.
func (t *TUI) statusLine() string {
	d := func(s string) string { return t.paint("dim", s) }
	var parts []string
	// Session title — set by autoTitle (first message) or /rename, so the
	// status line always says which conversation is active.
	if st := strings.TrimSpace(t.sessionTitle); st != "" {
		parts = append(parts, d("📎 "+truncate(st, 18)))
	}
	// Mode badge — always visible so the user knows what approvals to
	// expect (Claude Code parity: plan/agent/auto/yolo are first-class).
	if t.mode != "" {
		parts = append(parts, t.paint(modeColor(t.mode), "["+string(t.mode)+"]"))
	}
	parts = append(parts, t.paint("green", "●")+" "+t.model)
	// Installed skill count — cached lazily, never hit the disk per frame.
	if !t.skillCountLoaded {
		t.skillCountLoaded = true
		if reg := skills.Load(skills.DefaultDirs()...); reg != nil {
			t.skillCount = len(reg.List())
		}
	}
	if t.skillCount > 0 {
		parts = append(parts, d(fmt.Sprintf("🧩%d", t.skillCount)))
	}
	// Git branch — lazily refreshed, never on the render hot path.
	if b := t.branchSegment(); b != "" {
		parts = append(parts, d("⎇ ")+b)
	}
	// PR status badge — lazily refreshed (gh pr view), never on the render hot
	// path. Coloured by mergeable state (OPEN/MERGED green; CLOSED/DRAFT red).
	if pr := t.prSegment(); pr != "" {
		color := "green"
		if strings.Contains(pr, "CLOSED") || strings.Contains(pr, "DRAFT") {
			color = "red"
		}
		parts = append(parts, t.paint(color, "PR "+pr))
	}
	// Security level badge — always visible so the user knows their privacy
	// boundary. Unlike Claude Code, no hidden telemetry or phone-home.
	if t.securityLevel != "" && t.securityLevel != "local" {
		label := permission.SecurityLabel(config.SecurityLevel(t.securityLevel))
		parts = append(parts, label)
	}
	if t.promptTokens > 0 || t.completionTokens > 0 {
		parts = append(parts,
			d("▸"+formatTokens(t.promptTokens)+" ▸"+formatTokens(t.completionTokens)))
	}
	if t.contextWindow > 0 && t.contextTokens > 0 {
		// Claude Code shows context REMAINING (how much is left), not used.
		parts = append(parts, d(fmt.Sprintf("ctx %d%%", 100-t.contextTokens*100/t.contextWindow)))
	}
	if t.cacheHitRate > 0 {
		parts = append(parts, d(fmt.Sprintf("%.0f%% cache", t.cacheHitRate*100)))
	}
	if t.cost != "" {
		parts = append(parts, d(t.cost))
	}
	// Todo counter — shown when the current session has an active todo list.
	// Zero-list sessions render nothing.
	if t.callback != nil {
		if text := t.callback.TodoActiveText(); text != "" {
			// Claude Code parity: show WHAT is being worked on right now.
			parts = append(parts, d("◑ "+text))
		}
		if pending, active, done, total := t.callback.TodoCounts(); total > 0 {
			seg := fmt.Sprintf("✓%d", done)
			if pending > 0 || active > 0 {
				seg = fmt.Sprintf("%d…%d→%d", pending, active, done)
			}
			if active > 0 {
				seg = t.paint("yellow", seg)
			} else {
				seg = d(seg)
			}
			parts = append(parts, seg)
		}
	}
	// Current work indicator: the tool executing right now ("正在做的工作").
	// NOTE: statusLine runs under t.mu (render snapshot phase) — never lock
	// inside; read fields directly.
	if t.streaming && t.curTool != "" && t.curTool != "…" {
		parts = append(parts, t.paint("yellow", "⚙ "+t.curTool))
	}
	// Background tasks (sub-agents + shell) running detached.
	if n := t.runningBgCount(); n > 0 {
		parts = append(parts, t.paint("cyan", fmt.Sprintf("⚡%d bg", n)))
	}
	if t.streaming && !t.turnStart.IsZero() {
		parts = append(parts, d("⏱ "+formatDuration(time.Since(t.turnStart))))
	}
	line := strings.Join(parts, d(" · "))
	// Flash notice (slash command feedback)
	if t.statusNotice != "" {
		line += "  " + t.statusNotice
	}
	return line
}

// contextBar renders a mini progress bar for context usage with threshold
// colouring: green <60%, yellow <85%, red at/above. Example: ctx ▓▓▓░░░░░ 42%
func (t *TUI) contextBar(tokens, window int) string {
	if window <= 0 {
		return ""
	}
	pct := tokens * 100 / window
	if pct > 100 {
		pct = 100
	}
	const cells = 8
	filled := pct * cells / 100
	if pct > 0 && filled == 0 {
		filled = 1 // always show a sliver once context is in use
	}
	bar := strings.Repeat("▓", filled) + strings.Repeat("░", cells-filled)
	color := "green"
	switch {
	case pct >= 85:
		color = "red"
	case pct >= 60:
		color = "yellow"
	}
	// Label shows context REMAINING (Claude Code convention) — the bar still
	// fills with usage so "how full" reads at a glance.
	return t.paint(color, bar) + t.paint("dim", fmt.Sprintf(" %d%% left", 100-pct))
}

// branchSegment returns the cached git branch for the status bar. Runs under
// t.mu (render snapshot phase) — reads fields directly and only spawns the
// background refresh goroutine (which takes the lock itself on write-back).
func (t *TUI) branchSegment() string {
	if time.Since(t.branchCheck) >= 30*time.Second {
		t.branchCheck = time.Now()
		xgo.GoSafe("tui.branch", func() {
			out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
			name := ""
			if err == nil {
				name = strings.TrimSpace(string(out))
				if name == "HEAD" { // detached — show short sha instead
					if sha, e := exec.Command("git", "rev-parse", "--short", "HEAD").Output(); e == nil {
						name = "@" + strings.TrimSpace(string(sha))
					}
				}
			}
			t.mu.Lock()
			t.gitBranch = name
			t.mu.Unlock()
		})
	}
	return t.gitBranch
}

// runningBgCount reports how many background jobs (detached sub-agents plus
// background shell commands) exist, for the ⚡N status segment.
func (t *TUI) runningBgCount() int {
	return tool.RunningAgentTaskCount() + tool.RunningShellTaskCount()
}

// prSegment returns the cached GitHub PR status badge for the current branch,
// e.g. "#123 OPEN" or "#456 MERGED". Mirrors branchSegment: refreshed lazily
// (every 60s) via `gh pr view`, never on the render hot path. Empty when there
// is no PR for the branch, `gh` is missing, or the call fails. The JSON result
// carries the review/merge state so the badge can be coloured meaningfully.
func (t *TUI) prSegment() string {
	if time.Since(t.prCheck) >= 60*time.Second {
		t.prCheck = time.Now()
		branch := t.gitBranch
		xgo.GoSafe("tui.pr", func() {
			if branch == "" {
				t.mu.Lock()
				t.prBadge = ""
				t.mu.Unlock()
				return
			}
			if _, err := exec.LookPath("gh"); err != nil {
				return // gh not installed — leave the (empty) badge as-is
			}
			out, err := exec.Command("gh", "pr", "view", "--json",
				"number,state,title,url").Output()
			if err != nil {
				t.mu.Lock()
				t.prBadge = ""
				t.mu.Unlock()
				return
			}
			var pr struct {
				Number int    `json:"number"`
				State  string `json:"state"`
				Title  string `json:"title"`
				URL    string `json:"url"`
			}
			if e := json.Unmarshal(out, &pr); e != nil || pr.Number == 0 {
				t.mu.Lock()
				t.prBadge = ""
				t.mu.Unlock()
				return
			}
			badge := fmt.Sprintf("#%d %s", pr.Number, strings.ToUpper(pr.State))
			t.mu.Lock()
			t.prBadge = badge
			t.mu.Unlock()
		})
	}
	return t.prBadge
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

// drawInputBox renders the minimal opencode-style prompt: a single `❯ <input>`
// row followed by one compact status bar row underneath it (the /statusline
// toggle hides that second row). The old hint row and duplicated effort/context
// rows are gone — all live info now lives on the single status line passed in.
//
//	❯ <input>                              row topRow
//	ctx ▓▓▓░░░░░ 42%                        row topRow-1 (when context known)
//	● model · ▸1.2k ▸3.4k · 42%             row topRow+1 (when /statusline on)
func (t *TUI) drawInputBox(W, H int, inputBuf string, cursor int, streaming bool, status string) string {
	statusRows := 0
	if t.statusVisible {
		statusRows = 1
	}
	// Claude Code-style context bar sits directly above the input box. render()
	// reserves the row via inputRows; mirror that here so topRow accounts for it.
	t.mu.Lock()
	ctxTokens, ctxWindow := t.contextTokens, t.contextWindow
	t.mu.Unlock()
	hasCtx := ctxWindow > 0 && ctxTokens >= 0
	ctxRows := 0
	if hasCtx {
		ctxRows = 1
	}
	// Streaming-time queue line (typing buffer / queued count).
	qRows := t.queueRows()
	qText := ""
	if qRows > 0 {
		qText = t.queueLine()
	}
	// Thinking indicator row (opencode-style spinner) shown above the context
	// bar while generating — the "思考进度" lives at the bottom, next to the
	// input, not inside the message area.
	thinkRows := 0
	if streaming {
		thinkRows = 1
	}

	// Multi-line prompt: "❯ <line0>", continuation lines indented to align
	// with the first content column. indent == promptW + 1: the first content
	// column is 3 (❯ at 1, space at 2, content at 3) and continuation lines
	// use 2 spaces so their content starts at the same column. (promptW + 2
	// used to misalign the cursor by one column — caret floated with a gap
	// before the typed text.)
	maxVis := t.inputVisibleLines(H)
	prompt := t.paint(modeColor(t.mode), "❯")
	promptW := visibleWidth(prompt)
	indent := promptW + 1 // continuation indent == first content column - 1
	innerW := W - indent - 1
	if innerW < 4 {
		innerW = 4
	}

	lines := strings.Split(inputBuf, "\n")
	lineIdx, colIdx := inputCursorPos(inputBuf, cursor)

	// Visible window: tail-anchored, but pulled back so the cursor line stays
	// visible while navigating upwards inside a long input.
	visStart := 0
	if len(lines) > maxVis {
		visStart = len(lines) - maxVis
		if lineIdx-2 < visStart {
			visStart = lineIdx - 2
			if visStart < 0 {
				visStart = 0
			}
		}
	}
	visEnd := visStart + maxVis
	if visEnd > len(lines) {
		visEnd = len(lines)
	}
	visLines := lines[visStart:visEnd]

	topRow := H - statusRows - ctxRows - qRows - thinkRows - len(visLines) + 1
	if topRow < 1 {
		topRow = 1
	}

	var b strings.Builder
	// Thinking indicator (opencode-style spinner + elapsed) directly above
	// the context bar while generating.
	if thinkRows > 0 && topRow-thinkRows-ctxRows-qRows >= 1 {
		tb := t.thinkingBar()
		if visibleWidth(tb) > W {
			tb = truncVisible(tb, W)
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow-thinkRows-ctxRows-qRows))
		b.WriteString(tb)
	}
	if hasCtx && topRow-thinkRows-ctxRows-qRows-1 >= 1 {
		cb := t.contextBar(ctxTokens, ctxWindow)
		if visibleWidth(cb) > W {
			cb = truncVisible(cb, W)
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow-thinkRows-ctxRows-qRows-1))
		b.WriteString(cb)
	}
	// Queue indicator line, directly above the first input line.
	if qRows > 0 && topRow-1 >= 1 {
		if visibleWidth(qText) > W {
			qText = truncVisible(qText, W)
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow-1))
		b.WriteString(qText)
	}

	// Cursor position within the visible window.
	curVisRow := -1
	curVisCol := 0
	if lineIdx >= visStart && lineIdx < visEnd {
		curVisRow = lineIdx - visStart
		curVisCol = colIdx
	}

	for i, ln := range visLines {
		row := topRow + i
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
		var line string
		if i == 0 {
			line = prompt + " " + ln
			if t.multiline {
				// Persistent multi-line indicator so Enter=newline never
				// surprises the user (Alt+Enter submits).
				line = prompt + " " + t.paint("yellow", "[MULTI]") + " " +
					t.paint("dim", "Enter=换行·Alt+Enter=发送 ") + ln
			} else if ln == "" {
				if streaming {
					// Claude Code-style "esc to interrupt" hint while generating.
					line = prompt + " " + t.paint("dim", t.tstr("input.hint.streaming"))
				} else {
					// Empty input: quiet Claude Code-style placeholder hint.
					line = prompt + " " + t.paint("dim", "/ 查看命令 · Tab 补全 · Ctrl+R 历史 · Alt+P 模型")
				}
			}
		} else {
			line = strings.Repeat(" ", indent) + ln
		}
		if visibleWidth(line) > W {
			line = truncVisible(line, W)
		}
		b.WriteString(line)
	}

	if t.statusVisible {
		st := status
		if visibleWidth(st) > W {
			st = truncVisible(st, W)
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow+len(visLines)))
		b.WriteString(st)
	} else {
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", H))
	}

	// Position the cursor: prompt + space (line 0) or indent (later lines),
	// plus content up to the cursor column. Use visibleWidth so CJK width
	// handling stays correct (see the single-line version's history).
	if curVisRow < 0 {
		// Cursor line scrolled out of the visible window — park at the end of
		// the last visible line so the terminal cursor (and IME composition
		// windows, which open at the physical cursor) never lands mid-log.
		curVisRow = len(visLines) - 1
		curVisCol = len([]rune(lines[visStart+curVisRow]))
	}
	if curVisRow >= 0 {
		vw := visibleWidth(string([]rune(lines[visStart+curVisRow])[:curVisCol]))
		col := indent + vw + 1 // 1-based ANSI column
		// The first line can carry extra prefixes ([MULTI] marker + hint) that
		// push the content right — the cursor must track them or it floats
		// with a visible gap between caret and typed text.
		if curVisRow == 0 && t.multiline {
			col += visibleWidth("[MULTI] Enter=换行·Alt+Enter=发送 ")
		}
		if col > W {
			col = W
		}
		if col < 1 {
			col = 1
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH", topRow+curVisRow, col))
	}
	// Hide the caret while streaming, show it otherwise (single-line version's
	// behaviour preserved for the multi-line box).
	if streaming {
		b.WriteString("\x1b[?25l")
	} else {
		b.WriteString("\x1b[?25h")
	}

	return b.String()
}

// drawSearchBox renders the Claude Code-style reverse-history-search overlay
// shown while Ctrl+R is active. It replaces the normal input prompt.
func (t *TUI) drawSearchBox(W, H int, searchBuf, current string, streaming bool) string {
	bottomRows := 1
	if t.statusVisible {
		bottomRows = 2
	}
	// Mirror the context-bar row reservation from drawInputBox so the search
	// overlay lands at the same height as the normal input box.
	t.mu.Lock()
	ctxTokens, ctxWindow := t.contextTokens, t.contextWindow
	t.mu.Unlock()
	hasCtx := ctxWindow > 0 && ctxTokens >= 0
	if hasCtx {
		bottomRows++
	}
	topRow := H - bottomRows + 1
	if topRow < 1 {
		topRow = 1
	}
	innerW := W - 4
	if innerW < 4 {
		innerW = 4
	}

	prompt := t.paint("yellow", "(reverse-i-search)")
	display := current
	if visibleWidth(display) > innerW-30 {
		display = truncVisible(display, innerW-30)
	}
	line := prompt + "`" + t.paint("cyan", display) + "'"
	if visibleWidth(line) > W {
		line = truncVisible(line, W)
	}

	query := t.paint("dim", "  i-search: "+searchBuf)

	var b strings.Builder
	if hasCtx && topRow-1 >= 1 {
		cb := t.contextBar(ctxTokens, ctxWindow)
		if visibleWidth(cb) > W {
			cb = truncVisible(cb, W)
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow-1))
		b.WriteString(cb)
	}
	b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow))
	b.WriteString(line)
	if t.statusVisible {
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow+1))
		b.WriteString(query)
	}
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", topRow, 2))
	b.WriteString("\x1b[?25h")
	return b.String()
}

// ── Scrolling support ───────────────────────────────────────────

// convHeight returns the number of rows available for conversation content.
func (t *TUI) convHeight() int {
	h := t.height - 4 // header(1) + footer hrule(1) + prompt(1) + status(1)
	// The Claude Code-style context bar above the input box claims one row.
	if t.contextWindow > 0 && t.contextTokens >= 0 {
		h--
	}
	return h
}

// totalConvLines counts all display lines for the current conversation.
func (t *TUI) totalConvLines(msgs []Message, streaming bool, streamContent string, width int) int {
	lines, _ := t.conversationLines(msgs, streaming, streamContent, width)
	return len(lines)
}

func (t *TUI) scrollPgUp() {
	t.mu.Lock()
	bodyH := t.convHeight()
	if bodyH < 1 {
		bodyH = 10
	}
	t.scrollOffset += bodyH
	if t.welcomeVisible {
		t.welcomeVisible = false
	}
	t.mu.Unlock()
	t.scheduleRender()
}

func (t *TUI) scrollPgDn() {
	t.mu.Lock()
	bodyH := t.convHeight()
	if bodyH < 1 {
		bodyH = 10
	}
	t.scrollOffset -= bodyH
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	t.mu.Unlock()
	t.scheduleRender()
}

func (t *TUI) scrollToTop() {
	t.mu.Lock()
	msgs := append([]Message{}, t.messages...)
	lines, _ := t.conversationLines(msgs, t.streaming, t.streamBuf.String(), t.width)
	total := len(lines)
	bodyH := t.convHeight()
	if total > bodyH {
		t.scrollOffset = total - bodyH
	}
	t.mu.Unlock()
	t.scheduleRender()
}

// scrollToBottom resumes auto-follow (scroll to latest content).
func (t *TUI) scrollToBottom() {
	t.mu.Lock()
	t.scrollOffset = 0
	t.mu.Unlock()
	t.scheduleRender()
}

func (t *TUI) scrollUpSmall() {
	t.mu.Lock()
	t.scrollOffset += 3
	t.mu.Unlock()
	t.scheduleRender()
}

func (t *TUI) scrollDownSmall() {
	t.mu.Lock()
	t.scrollOffset -= 3
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	t.mu.Unlock()
	t.scheduleRender()
}

// canScroll reports whether the conversation has more display lines than the
// visible body, i.e. the viewport can be scrolled. It snapshots the needed
// state under the lock so callers (key handler) don't race the stream.
func (t *TUI) canScroll() bool {
	t.mu.Lock()
	streaming := t.streaming
	msgs := append([]Message{}, t.messages...)
	streamContent := t.streamBuf.String()
	W := t.width
	H := t.height
	t.mu.Unlock()
	bodyH := H - 4
	if bodyH < 3 {
		bodyH = 3
	}
	total := t.totalConvLines(msgs, streaming, streamContent, W)
	return total > bodyH
}

// scrollUp moves the conversation viewport up by n display lines (towards
// older content). render() clamps scrollOffset to the maximum, so over-
// scrolling is harmless. It also dismisses the welcome banner if present.
func (t *TUI) scrollUp(n int) {
	if n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.welcomeVisible {
		t.welcomeVisible = false
	}
	t.scrollOffset += n
	t.scheduleRender()
}

// scrollDown moves the conversation viewport down by n display lines (towards
// newer content), never past the bottom (auto-follow at offset 0).
func (t *TUI) scrollDown(n int) {
	if n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scrollOffset -= n
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	t.scheduleRender()
}

// settingItem is one row of the settings overlay panel.
type settingItem struct {
	label string
	value string
}

// settingsPanelItems builds the settings-panel rows. Labels go through the
// TUI i18n table (t.tstr) so /lang applies to the overlay; the row COUNT is
// derived from this single list, and handleSettingsKey clamps navigation to
// len(settingsPanelItems(...)) so the drawn rows and the keyboard bounds can
// never drift apart.
func (t *TUI) settingsPanelItems(cfg *config.Config) []settingItem {
	return []settingItem{
		{label: t.tstr("settings.model"), value: cfg.Defaults.Model},
		{label: t.tstr("settings.provider"), value: cfg.Defaults.Provider},
		{label: t.tstr("settings.mode"), value: cfg.Defaults.Mode},
		{label: t.tstr("settings.lang"), value: cfg.Language},
		{label: t.tstr("settings.theme"), value: cfg.TUI.Theme},
		{label: t.tstr("settings.voiceProv"), value: cfg.Voice.Provider},
		{label: t.tstr("settings.baiduKey"), value: maskStringTUI(cfg.Voice.BaiduAPIKey)},
		{label: t.tstr("settings.xfyunAppID"), value: cfg.Voice.IFlytekAppID},
	}
}

// renderSettingsPanel draws the settings overlay panel.
func (t *TUI) renderSettingsPanel() string {
	t.mu.Lock()
	W := t.width
	H := t.height
	t.mu.Unlock()

	if W < 40 || H < 15 {
		return ""
	}

	// Use the snapshot loaded when the panel opened (see the Ctrl+, handler);
	// fall back to a one-off load so render() itself never hits the disk.
	t.mu.Lock()
	cfg := t.settingsCfg
	t.mu.Unlock()
	if cfg == nil {
		var err error
		cfg, err = config.Load()
		if err != nil {
			cfg = config.Default()
		}
	}

	// Panel dimensions
	panelW := W - 8
	if panelW > 70 {
		panelW = 70
	}
	panelH := H - 6
	if panelH > 30 {
		panelH = 30
	}
	startX := (W - panelW) / 2
	startY := (H - panelH) / 2

	// Build settings items (labels via the TUI i18n table, so /lang applies
	// to the overlay too). settingsPanelItems keeps the row list in ONE place:
	// renderSettingsPanel draws it and handleSettingsKey clamps navigation to
	// its length, so the two can never drift apart.
	items := t.settingsPanelItems(cfg)

	var b strings.Builder
	// Move cursor to panel position
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", startY+1, startX+1))

	// Draw panel border
	borderH := panelH
	borderW := panelW

	// Top border
	b.WriteString("\x1b(0") // Enter line drawing mode
	b.WriteString("l")
	b.WriteString(strings.Repeat("q", borderW-2))
	b.WriteString("k")
	b.WriteString("\x1b(B") // Exit line drawing mode

	// Title
	title := t.tstr("settings.title")
	titlePad := (borderW - 2 - len(title)) / 2
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", startY+1, startX+1))
	b.WriteString("\x1b(0l")
	b.WriteString(strings.Repeat("q", titlePad))
	b.WriteString("\x1b(B")
	b.WriteString("\x1b[1m" + title + "\x1b[0m")
	b.WriteString("\x1b(0")
	b.WriteString(strings.Repeat("q", borderW-2-titlePad-len(title)))
	b.WriteString("k")
	b.WriteString("\x1b(B")

	// Content rows
	for i, item := range items {
		rowY := startY + 2 + i
		if rowY >= startY+borderH-1 {
			break
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH", rowY, startX+1))
		b.WriteString("\x1b(0x\x1b(B") // Left border

		// Highlight current row
		if i == t.settingsCursor {
			b.WriteString("\x1b[7m") // Reverse video
		}

		// Format: label + padding + value
		label := item.label + ": "
		padding := borderW - 2 - len(label) - len(item.value)
		if padding < 0 {
			padding = 0
		}
		b.WriteString(" " + label + strings.Repeat(" ", padding) + item.value)

		if i == t.settingsCursor {
			b.WriteString("\x1b[0m") // Reset
		}

		// Pad remaining width and right border
		remaining := borderW - 2 - len(" "+label+strings.Repeat(" ", padding)+item.value)
		if remaining > 0 {
			b.WriteString(strings.Repeat(" ", remaining))
		}
		b.WriteString("\x1b(0\x1b(B") // Right border
	}

	// Empty rows
	for i := len(items); i < panelH-2; i++ {
		rowY := startY + 2 + i
		if rowY >= startY+borderH-1 {
			break
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH", rowY, startX+1))
		b.WriteString("\x1b(0x")
		b.WriteString(strings.Repeat(" ", borderW-2))
		b.WriteString("x\x1b(B")
	}

	// Bottom border
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", startY+borderH-1, startX+1))
	b.WriteString("\x1b(0m")
	b.WriteString(strings.Repeat("q", borderW-2))
	b.WriteString("j")
	b.WriteString("\x1b(B")

	// Hint line
	hintY := startY + borderH
	hint := "↑↓ 移动 | Enter 修改 | Esc 关闭"
	hintX := startX + (panelW-len(hint))/2
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", hintY, hintX, hint))

	return b.String()
}

func maskStringTUI(s string) string {
	if s == "" {
		return "(未设置)"
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
