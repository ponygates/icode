package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/privacy"
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
			// cli.log is the file we ask users to attach to bug reports, so it
			// must never carry a live key: a provider error echoing the Authorization
			// header would otherwise land on disk in the clear.
			rw.SetFilter(privacy.RedactSecretsBytes)
			cliLogRotator = rw
		}
	})
	return cliLogRotator
}

// cliLogSuppress disables writeCliLog entirely. Set by the package's
// TestMain so tests that exercise diagnostic paths (input-trace dumps on
// drain timeouts, render-panic recovery) never append test noise to the
// user's real ~/.icode/cli.log — the very file we ask users to submit as
// forensic evidence.
var cliLogSuppress bool

// writeCliLog appends a diagnostic message to ~/.icode/cli.log so a crash is
// never silent — the user (or a helper) can inspect it after a "flash close".
func writeCliLog(s string) {
	if cliLogSuppress {
		return
	}
	if rw := cliLogWriter(); rw != nil {
		_, _ = rw.Write([]byte(s))
		return
	}
	// Fallback (home dir unavailable or rotator init failed): stderr.
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		fmt.Fprint(privacy.NewRedactingWriter(os.Stderr), s)
		return
	}
	dir := filepath.Join(home, ".icode")
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "cli.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprint(privacy.NewRedactingWriter(os.Stderr), s)
		return
	}
	defer f.Close()
	// Same redaction as the rotating path: this fallback appends to the very
	// cli.log we ask users to attach to bug reports.
	_, _ = privacy.NewRedactingWriter(f).Write([]byte(s))
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
	stLeft, stRight := t.statusParts()
	permPending := t.permPending
	permPrompt := t.permPrompt
	permSeverity := t.permSeverity
	welcomeVisible := t.welcomeVisible
	searchMode := t.searchMode
	searchBuf := t.searchBuf
	promptActive := t.prompt != nil
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
	// Bottom chrome is the Claude Code-style rounded input box: a bordered
	// multi-line frame (top rule + content rows + bottom rule) that grows
	// with content up to a cap and scrolls internally, plus a single compact
	// status bar underneath it (hidden entirely when /statusline is toggled
	// off). Everything else (header, conversation, overlays) lives above.
	inputRows := t.inputVisibleLines(H)
	if n := t.inputLineCount(); n < inputRows {
		inputRows = n
	}
	inputRows += 2 // rounded box top + bottom border rows
	// Streaming-time queue indicator ("⏳ queued / typing") above the box.
	inputRows += t.queueRows()
	// The status bar (under the bottom border) and the thinking indicator
	// (above the box) are both part of the prompt block drawn by
	// drawInputBox, whose topRow math subtracts statusRows and thinkRows.
	// inputRows MUST count them too, or contentRows extends into the box
	// chrome and the conversation's last lines get painted over — and worse,
	// during streaming the thinking row lands ON TOP of live content.
	if t.statusVisible {
		inputRows++
	}
	if streaming {
		inputRows++
	}
	contentRows := H - inputRows
	if contentRows < 4 {
		contentRows = 4
	}

	// Overlays drawn above the input box.
	acLines := t.autocompleteLines(W)
	permLines := []string{}
	if permPending {
		// Claude Code-style bordered permission box, coloured by risk:
		// red warns before destructive commands (rm -rf, git push --force),
		// cyan keeps read-tier confirms calm, yellow stays the default.
		title := "? " + t.tstr("perm.title")
		borderColor := "yellow"
		switch permSeverity {
		case permission.SeverityHigh:
			borderColor = "red"
			title = "⚠ " + t.tstr("perm.titleHigh")
		case permission.SeverityLow:
			borderColor = "cyan"
		}
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
			t.paint(borderColor, "  ╭"+repeat("─", boxW)+"╮"),
			t.paint(borderColor, "  │ ")+t.paint("bold", title)+padVisible("", boxW-visibleWidth(title)-2)+t.paint(borderColor, " │"),
		)
		for _, pl := range wrapped {
			// Diff-style colouring inside the approval box: removed lines
			// ("- ", from gate_hooks.go's edit preview) render red, added
			// lines ("+ ") green — reading like git diff instead of a wall
			// of dim text.
			lineColor := "dim"
			switch {
			case strings.HasPrefix(pl, "+ "):
				lineColor = "green"
			case strings.HasPrefix(pl, "- "):
				lineColor = "red"
			}
			permLines = append(permLines,
				t.paint(lineColor, "  │ ")+pl+padVisible("", boxW-visibleWidth(pl)-2)+t.paint(lineColor, " │"),
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
				t.paint(borderColor, "  │ ")+t.paint("cyan", "├"+repeat("─", boxW-2)+"┤"),
				t.paint(borderColor, "  │ ")+noteLine+padVisible("", boxW-visibleWidth(noteLine)-2)+t.paint(borderColor, " │"),
				t.paint(borderColor, "  │ ")+t.paint("dim", "Enter 提交「拒绝并说明」· Esc 取消说明")+padVisible("", boxW-visibleWidth("Enter 提交「拒绝并说明」· Esc 取消说明")-2)+t.paint(borderColor, " │"),
			)
		}
		permLines = append(permLines,
			t.paint(borderColor, "  │ ")+t.paint("dim", opts)+padVisible("", boxW-visibleWidth(opts)-2)+t.paint(borderColor, " │"),
			t.paint(borderColor, "  ╰"+repeat("─", boxW)+"╯"),
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
	} else if t.replayOpen {
		// Checkpoint timeline (Claude Code /replay parity): newest-first list
		// with per-step diff on Enter and confirm-to-rewind on r.
		conv = t.replayOverlay(W, bodyH)
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
	// Full clear on demand: conhost ECHO revival echoed stray keys into the
	// conversation area where incremental repaint would leave them forever.
	t.mu.Lock()
	repaint := t.fullRepaintPending
	t.fullRepaintPending = false
	t.mu.Unlock()
	if repaint {
		fmt.Fprint(t.writer, "\x1b[2J\x1b[H")
		t.lastFrame = nil
	}
	// Anti-poison: every antiPoisonInterval'th render, drop lastFrame so the
	// diff loop below rewrites EVERY row (single flush, same content — the
	// user sees nothing). Incremental repaint skips "unchanged" rows, so a
	// row painted into the console buffer by an outsider (revived ECHO, an
	// IME writing its composition directly, a zombie instance sharing the
	// buffer) would otherwise stay pinned on screen indefinitely. A periodic
	// full repaint bounds any stray row's lifetime to a couple of seconds.
	t.renderCount++
	if t.renderCount >= antiPoisonInterval {
		t.renderCount = 0
		t.lastFrame = nil
	}
	layoutShift := len(t.lastFrame) != contentRows
	if layoutShift {
		t.lastFrame = make([]string, contentRows)
	}

	var buf strings.Builder
	buf.WriteString("\x1b[?25l") // hide cursor while repainting
	if layoutShift {
		// The prompt block grew or shrank (aux rows appearing/disappearing,
		// input lines changing), so rows that USED to belong to the prompt
		// block are now conversation rows — and a BLANK conversation line at
		// such a row never repaints (the diff sees "" == ""), leaving stale
		// prompt chrome (an old context bar, an old status line — the
		// "status bar shows up twice" bug) pinned on screen forever. Wipe
		// the screen so the full repaint below rewrites every row, prompt
		// chrome included (drawInputBox clears its whole block anyway).
		buf.WriteString("\x1b[2J\x1b[H")
	}
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
			// Hard width clip (belt-and-braces): a row wider than the
			// terminal triggers the terminal's autowrap (DECAWM), pushing
			// every following row down and desynchronising ALL subsequent
			// absolute cursor moves in this frame — the classic "text and
			// garbage crawl into the conversation area" corruption.
			// Upstream renderers are supposed to bound rows to W, but a
			// single missed path (wide table, long autocomplete desc, MCP
			// tool name) is enough to wreck the layout, so enforce the
			// terminal width here at the wire.
			if visibleWidth(ln) > W {
				ln = fitVis(ln, W)
			}
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
	} else if promptActive {
		tail = t.drawPromptBox(contentW, H)
	} else if searchMode {
		tail = t.drawSearchBox(contentW, H, searchBuf, searchCur, streaming)
	} else {
		status := t.layoutStatusLine(contentW, stLeft, stRight)
		tail = t.drawInputBox(contentW, H, inputBuf, cursor, streaming, status)
	}
	frame := buf.String() + tail
	t.traceFrame(frame)
	fmt.Fprint(t.writer, frame)
}

// traceFrame dumps every absolute cursor move + the text that follows it to
// ~/.icode/tui-trace.log when ICODE_TRACE=1 — the definitive evidence for
// "text renders in the wrong place" reports.
func (t *TUI) traceFrame(frame string) {
	if os.Getenv("ICODE_TRACE") == "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(home, ".icode", "tui-trace.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	var row, col int
	for _, seg := range strings.Split(frame, "\x1b[") {
		if n, _ := fmt.Sscanf(seg, "%d;%dH", &row, &col); n == 2 {
			rest := seg
			if i := strings.IndexByte(seg, 'H'); i >= 0 && i+1 < len(seg) {
				rest = seg[i+1:]
			}
			// Strip remaining escapes for a readable preview.
			var b strings.Builder
			inEsc := false
			for _, r := range rest {
				if r == 0x1b {
					inEsc = true
					continue
				}
				if inEsc {
					if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
						inEsc = false
					}
					continue
				}
				b.WriteRune(r)
			}
			prev := strings.TrimSpace(b.String())
			if len([]rune(prev)) > 40 {
				prev = string([]rune(prev)[:40]) + "…"
			}
			fmt.Fprintf(f, "%s row=%d col=%d %q\n", time.Now().Format("15:04:05.000"), row, col,
				privacy.RedactSecrets(prev))
		}
	}
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
