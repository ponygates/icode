package tui

import (
	"fmt"
	"runtime/debug"
	"strings"
	"time"
)

// thinkingBox renders the streaming "thinking" indicator — a single bare line
// (spinner + status text + elapsed, opencode style), deliberately box-less.
func (t *TUI) thinkingBox(width int) []string {
	return []string{"  " + t.thinkingBar()}
}

// thinkingSlider renders the opencode-style "thinking" scanner for the
// bottom status line: a bright ■ head sweeping back and forth across an
// 8-cell track of dim ⬝ dots, trailing a 6-step amber→gold gradient behind
// it, with the toggle-pause at both ends — 9 frames parked at the right edge,
// 30 at the left, 40 ms per frame. Same bidirectional cycle as opencode's
// Knight Rider scanner (packages/tui/src/ui/spinner.ts), which shows
// whenever a generation is in flight. The slot width is FIXED (8 cells)
// so the status-line layout never shifts frame to frame — only the head's
// position and the trail colours animate. In no-colour mode the ■/⬝ glyph
// contrast alone still reads as a moving block.
// Runs under t.mu (render snapshot phase, same as statusParts).
//
// Visual:  ⬝⬝⬝⬝■⬝⬝⬝   ← the head sweeps right↔left with an amber→gold
//
//	fade-trail, pausing at each end while streaming.
func (t *TUI) thinkingSlider() string {
	const (
		trackW     = 8  // track width in cells (opencode default)
		trailSteps = 6  // cyan gradient steps behind the head
		holdEnd    = 9  // frames parked at the right edge
		holdStart  = 30 // frames parked at the left edge
		stepMs     = 40 // opencode spinner interval
	)
	// Bidirectional cycle: forward → hold end → backward → hold start.
	cycle := trackW + holdEnd + (trackW - 1) + holdStart // 8+9+7+30 = 54
	frame := 0
	if !t.turnStart.IsZero() {
		frame = int(time.Since(t.turnStart).Milliseconds() / stepMs)
		if frame < 0 {
			frame = 0
		}
	}
	f := frame % cycle

	// Head position + sweep phase (mirrors getScannerState bidirectional).
	pos, hold, forward := 0, false, true
	var holdProgress int
	switch {
	case f < trackW: // sweep right: head 0 → trackW-1
		pos = f
	case f < trackW+holdEnd: // parked at the right edge
		pos = trackW - 1
		hold = true
		holdProgress = f - trackW
	case f < trackW+holdEnd+(trackW-1): // sweep left: head trackW-2 → 0
		pos = trackW - 2 - (f - trackW - holdEnd)
		forward = false
	default: // parked at the left edge
		pos = 0
		hold = true
		holdProgress = f - trackW - holdEnd - (trackW - 1)
	}

	var b strings.Builder
	reset := "\x1b[0m"
	if !t.color {
		reset = "" // knightColor already returns "" when colour is off
	}
	for ch := 0; ch < trackW; ch++ {
		// Trail sits BEHIND the direction of travel (calculateColorIndex).
		dist := pos - ch
		if !forward {
			dist = ch - pos
		}
		idx := -1
		if hold {
			// Head (and whole trail) fades out while parked.
			idx = dist + holdProgress
		} else if dist > 0 && dist < trailSteps {
			idx = dist
		} else if dist == 0 {
			idx = 0
		}
		if idx >= 0 && idx < trailSteps {
			b.WriteString(t.knightColor(idx))
			b.WriteString("■")
		} else {
			b.WriteString(t.knightColor(-1))
			b.WriteString("⬝")
		}
		b.WriteString(reset)
	}
	return b.String()
}

// scannerStop is one rung of the thinking-scanner colour ladder: the exact
// 24-bit value for truecolor terminals and the nearest xterm-256 code for
// 8/16-bit ones. Both stay in the warm amber band so the terminal scanner and
// the yellow thinking-budget slider in the desktop settings page read as one
// accent.
type scannerStop struct {
	rgb     [3]int
	palette string
}

// scannerStops walks index 0 = bright head → 5 = dimmest trail step. In
// non-truecolor mode every rung is a distinct xterm-256 cube level, so the
// fade keeps six visible shades (the old fallback painted trail steps 2..5
// with one #38;5;30 and the gradient collapsed into a two-colour block).
var scannerStops = []scannerStop{
	{rgb: [3]int{254, 243, 199}, palette: "\x1b[38;5;230m"}, // 0 head — pale cream
	{rgb: [3]int{252, 211, 77}, palette: "\x1b[38;5;220m"},  // 1        gold
	{rgb: [3]int{245, 158, 11}, palette: "\x1b[38;5;214m"},  // 2        amber
	{rgb: [3]int{217, 119, 6}, palette: "\x1b[38;5;208m"},   // 3        orange
	{rgb: [3]int{180, 83, 9}, palette: "\x1b[38;5;166m"},    // 4        deep orange
	{rgb: [3]int{146, 64, 14}, palette: "\x1b[38;5;130m"},   // 5        dark amber
}

// scannerStopTrack is the dormant dot: a muted dark amber that sits clearly
// below the dimmest trail step on both terminal classes.
var scannerStopTrack = scannerStop{rgb: [3]int{90, 61, 30}, palette: "\x1b[38;5;94m"}

// knightColor returns the ANSI SGR code (no terminator — the caller appends
// \x1b[0m) for one cell of the thinking scanner: idx 0 is the bright head,
// 1..5 walk the amber→gold fade trail, idx < 0 is the dim track dot.
func (t *TUI) knightColor(idx int) string {
	if !t.color {
		return ""
	}
	stop := scannerStopTrack
	if idx >= 0 && idx < len(scannerStops) {
		stop = scannerStops[idx]
	}
	if trueColorTerm() {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", stop.rgb[0], stop.rgb[1], stop.rgb[2])
	}
	return stop.palette
}

// thinkingBar renders the per-turn thinking indicator above the input box:
// model tag, braille spinner, token count, live rate and the esc hint. It
// keeps the chrome quiet (opencode style) — one bare line, no box.
//
// Visual:  ⠋ 正在生成… 12s
func (t *TUI) thinkingBar() string {
	elapsed := time.Since(t.turnStart)
	frame := int(elapsed.Milliseconds() / 100)
	if frame < 0 {
		frame = 0
	}

	// ── Spinner ── braille cycle (opencode's default "dots" spinner).
	spinners := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinner := spinners[frame%len(spinners)]

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
		rateStr = t.paint("dim", fmt.Sprintf(" · "+t.tstr("status.tokRate"), t.completionTokens/secs))
	}
	tokStr := ""
	if t.completionTokens > 0 {
		tokStr = t.paint("dim", " ↑"+formatTokens(t.completionTokens))
	}
	return modelTag + t.paint("cyan", spinner) + " " +
		t.paint("dim", t.tstr("status.gen")) +
		tokStr + rateStr +
		t.paint("dim", fmt.Sprintf(t.tstr("status.elapsed"), secs)) +
		t.paint("dim", " · esc 中断")
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
