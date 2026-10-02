package tui

import (
	"fmt"
	"math/rand"
	"strings"
)

// hScrollLine windows a too-long input line so the caret always stays
// visible while typing (Claude Code horizontal scrolling). The cursor line
// keeps a small lookahead margin to the right of the caret; other lines are
// tail-anchored so the most recent text stays on screen. Returns the shown
// text plus the caret's visible column inside it.
func hScrollLine(ln string, cursorVis, innerW int, cursorLine bool) (string, int) {
	if innerW < 6 {
		innerW = 6
	}
	total := visibleWidth(ln)
	if total <= innerW {
		return ln, cursorVis
	}
	if !cursorLine {
		return "…" + visTail(ln, innerW-1), 0
	}
	margin := 4 // cols of fresh space kept to the right of the caret
	if innerW-8 < margin {
		margin = innerW - 8
	}
	if margin < 1 {
		margin = 1
	}
	start := 0
	if cursorVis > innerW-1-margin {
		start = cursorVis - (innerW - 1 - margin)
	}
	if maxStart := total - innerW + 1; start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	shown := "…"
	cur := cursorVis - start + 1
	if start == 0 {
		shown = ""
		cur = cursorVis
	}
	shown += visSlice(ln, start, innerW-visibleWidth(shown))
	return shown, cur
}

// inputPlaceholderHints rotates Claude Code-style "Try …" suggestions; one
// is picked per process so the box never flickers between hints mid-session.
var inputPlaceholderHints = []string{
	`Try "解释这个项目的架构"`,
	`Try "修复这个失败的测试"`,
	`Try "为这个函数写单元测试"`,
	`Try "重构这段代码并说明理由"`,
	`Try "/compact 压缩上下文"`,
	`Try "/model 切换模型"`,
	`Try "/token 查看节省了多少"`,
	"输入 / 查看全部命令 · Tab 补全 · Ctrl+R 搜历史",
}

var inputPlaceholder = inputPlaceholderHints[rand.Intn(len(inputPlaceholderHints))]

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

	// Multi-line prompt: Claude Code-style rounded box. All content lines share
	// the same inner width (│ + space on each side); the caret column is
	// computed in visible columns so CJK input positions correctly.
	maxVis := t.inputVisibleLines(H)
	innerW := W - 4
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

	// Box top row: status sits under the bottom border, queue/thinking rows
	// stack above the top border. The aux rows live ABOVE the border, so they
	// borrow the rows inputRows already reserved for them (render() counts
	// queue/think in inputRows, which shrinks contentRows) — the border
	// itself is anchored to the BOTTOM: status row = H, bottom border = H-1,
	// input rows above that. The old formula ALSO subtracted the aux rows from
	// topRow, which lifted the whole box (status bar included) one row per aux
	// row and left the bottom row(s) of the terminal permanently blank — and
	// made the status bar jump between H and H-N whenever a thinking row
	// appeared, leaving stale copies behind ("status bar shows up twice").
	topRow := H - statusRows - len(visLines) - 2 + 1
	if topRow < 1 {
		topRow = 1
	}

	var b strings.Builder
	// Wipe the entire prompt-block region before painting it. Rows vacated
	// by a layout shift — the thinking indicator / queue line appearing or
	// disappearing, the status bar sliding between H and H-N as aux rows
	// come and go — kept their PREVIOUS frame's content on screen forever:
	// nothing else clears them (the incremental lastFrame diff only covers
	// the conversation area, and each paint below only erases the row it is
	// about to draw). That is the root cause of the "status bar shows up
	// two or three times" bug. Clearing the whole block costs a handful of
	// erase sequences per frame and kills every stale-row variant at the
	// root.
	clearTop := topRow - thinkRows - qRows
	if clearTop < 1 {
		clearTop = 1
	}
	for row := clearTop; row <= H; row++ {
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
	}
	// Thinking indicator (opencode-style spinner + elapsed) directly above
	// the box while generating. The aux rows stack tightly above the box
	// top border, top-to-bottom: thinking → queue, i.e.
	// row(topRow - thinkRows - qRows + …). Keeping the arithmetic uniform
	// here matters: the old formulas left a blank row between the aux block
	// and the border, wasting a row.
	if thinkRows > 0 && topRow-thinkRows-qRows >= 1 {
		tb := t.thinkingBar()
		if visibleWidth(tb) > W {
			tb = truncVisible(tb, W)
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow-thinkRows-qRows))
		b.WriteString(tb)
	}
	// Queue indicator line, directly above the box.
	if qRows > 0 && topRow-qRows >= 1 {
		if visibleWidth(qText) > W {
			qText = truncVisible(qText, W)
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow-qRows))
		b.WriteString(qText)
	}

	// Rounded top border. In multi-line mode the [MULTI] reminder is embedded
	// into the border itself (Claude Code box-title style) so it costs no
	// content width.
	border := t.paint("dim", "│")
	topBorder := t.paint("dim", "╭"+repeat("─", W-2)+"╮")
	if t.multiline {
		label := " [MULTI] Enter=换行 · Alt+Enter=发送 "
		lw := visibleWidth(label)
		if W-2 > lw+4 {
			padL := (W - 2 - lw) / 2
			topBorder = t.paint("dim", "╭"+repeat("─", padL)) + t.paint("yellow", label) +
				t.paint("dim", repeat("─", W-2-padL-lw)+"╮")
		}
	}
	b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow))
	b.WriteString(topBorder)

	// Cursor position within the visible window.
	curVisRow := -1
	curVisCol := 0
	if lineIdx >= visStart && lineIdx < visEnd {
		curVisRow = lineIdx - visStart
		curVisCol = colIdx
	}

	// Caret position inside the box, filled by the row loop below and
	// finalized after it.
	caretCol := 3
	caretRow := topRow + 1

	for i, ln := range visLines {
		row := topRow + 1 + i
		// What the row displays: the raw text, or a hint when the first line
		// is empty (placeholder / streaming hint). Hints are visual only —
		// the caret still tracks ln itself.
		disp := ln
		if i == 0 && ln == "" {
			if streaming {
				disp = t.paint("dim", t.tstr("input.hint.streaming"))
			} else {
				disp = t.paint("dim", inputPlaceholder)
			}
		}
		// Horizontal scroll: window the visible text around the caret so
		// typing past the right edge keeps the caret (and the fresh text)
		// on screen instead of clipping it away.
		isCur := i == curVisRow
		cursorVis := 0
		if isCur {
			curRunes := []rune(ln)
			if curVisCol > len(curRunes) {
				curVisCol = len(curRunes)
			}
			cursorVis = visibleWidth(string(curRunes[:curVisCol]))
		}
		shown, curInner := hScrollLine(disp, cursorVis, innerW, isCur)
		// Belt-and-braces: no matter how a control byte sneaked into the
		// input buffer, it must never reach the wire — painting it lets the
		// terminal execute it (cursor jumps, erased rows, "garbage above
		// the box"). Padding recomputes from the cleaned width, so the row
		// keeps its box alignment.
		shown = sanitizeInput(shown)
		padW := innerW - visibleWidth(shown)
		if padW < 0 {
			padW = 0
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
		b.WriteString(border + " " + shown + strings.Repeat(" ", padW) + " " + border)
		// Caret: remember its column on the cursor row (content starts at
		// ANSI col 3: border 1 + space 2).
		if isCur {
			caretCol = 3 + curInner
			caretRow = row
		}
	}

	// Rounded bottom border, then the status line underneath.
	b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow+1+len(visLines)))
	b.WriteString(t.paint("dim", "╰"+repeat("─", W-2)+"╯"))

	if t.statusVisible {
		st := status
		if visibleWidth(st) > W {
			st = truncVisible(st, W)
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow+2+len(visLines)))
		b.WriteString(st)
	} else {
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", H))
	}

	// Position the caret inside the box. When the caret's line scrolled out
	// of the visible window, park at the end of the last visible line so the
	// terminal cursor (and IME composition windows, which open at the
	// physical cursor) never lands mid-log.
	if curVisRow < 0 {
		last := lines[visStart+len(visLines)-1]
		_, curInner := hScrollLine(last, visibleWidth(last), innerW, true)
		caretCol = 3 + curInner
		caretRow = topRow + len(visLines)
	}
	if caretCol > W {
		caretCol = W
	}
	if caretCol < 1 {
		caretCol = 1
	}
	if caretRow < 1 {
		caretRow = 1
	}
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", caretRow, caretCol))
	// Hide the caret while streaming, show it otherwise.
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
	// The search overlay anchors to the BOTTOM edge, mirroring drawInputBox:
	// query row on H (when the status line is visible), the search prompt on
	// H-1. The old bottomRows arithmetic left row H permanently blank — the
	// same floating-box bug that made the input box's status bar jump rows.
	topRow := H
	if t.statusVisible {
		topRow = H - 1
	}
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
	// Wipe the whole overlay region first — rows vacated when the overlay
	// swaps with the input box keep stale content otherwise (same
	// stale-row bug as drawInputBox).
	clearTop := topRow
	if clearTop < 1 {
		clearTop = 1
	}
	for row := clearTop; row <= H; row++ {
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
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
