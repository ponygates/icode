package tui

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// enableMouse turns on SGR mouse tracking (clicks + wheel, encoding 1006) and
// bracketed paste mode (?2004). With these on, the TUI can react to clicks,
// wheel scrolling, and pasted blocks without the terminal echoing raw sequences.
//
// We deliberately use button-event tracking (?1000h) rather than the drag/motion
// modes (?1002h/?1003h): motion tracking forces the terminal into "app captures
// the mouse" state, which disables the user's native click-drag text selection
// (and therefore Ctrl+Shift+C copy) in Windows Terminal and most emulators.
// Mode 1000 only reports button presses, releases and wheel scrolls — the app
// still reacts to clicks, the scrollbar, wheel, and right-click paste, while the
// terminal keeps its normal text-selection behaviour for dragging.
func (t *TUI) enableMouse() {
	fmt.Fprint(t.writer, "\x1b[?2004h\x1b[?1000h\x1b[?1006h")
}

// disableMouse turns off the mouse + paste modes enabled by enableMouse.
func (t *TUI) disableMouse() {
	fmt.Fprint(t.writer, "\x1b[?1006l\x1b[?1000l\x1b[?2004l")
}

// handleMouse parses and acts on one SGR mouse report whose leading "ESC[<"
// has already been consumed. br is positioned at the first byte after '<'.
func (t *TUI) handleMouse(br *bufio.Reader) {
	button, x, y, released, ok := parseSGRMouse(br)
	if !ok {
		return
	}

	// SGR coords are 1-based; convert to 0-based column/row.
	col := x - 1
	row := y - 1

	// Wheel reports (button encodes the direction; no press/release pair).
	if button&0x40 != 0 || button == 64 || button == 65 || button == 96 || button == 97 {
		switch {
		case button == 64 || button == 96:
			t.scrollUpSmall()
		case button == 65 || button == 97:
			t.scrollDownSmall()
		}
		return
	}

	// Right-click → paste the clipboard into the input line. SGR mouse capture
	// otherwise intercepts the terminal's native right-click paste, so we must
	// do it ourselves (the terminal sends the press, not a paste).
	if button&3 == 2 && !released {
		t.pasteFromClipboard()
		return
	}

	// Rightmost column → scrollbar: click or drag jumps the viewport.
	if col == t.width-1 {
		t.scrollToRow(row)
		return
	}

	// Bottom 3 rows → input box: move the editable cursor to the clicked column.
	inputTop := t.height - 3 + 1 // first input row (1-based): H-2
	if row >= inputTop {
		targetCol := col - visibleWidth("❯ ") // prompt occupies "❯ " (1-2 + space cells)
		if targetCol < 0 {
			targetCol = 0
		}
		t.mu.Lock()
		t.cursor = t.cursorForCol(t.inputBuf, targetCol)
		t.mu.Unlock()
		t.dismissWelcome()
		t.scheduleRender()
		return
	}

	// Anywhere else in the body → click/drag to jump the scroll position.
	t.scrollToRow(row)
}

// parseSGRMouse reads a single SGR-encoded mouse report from br (the bytes
// after "ESC[<") and decodes it into button code, 1-based x/y, and whether it
// was a release ('m') rather than a press/move ('M'). It is pure and exported
// so it can be unit-tested without a real terminal.
func parseSGRMouse(br *bufio.Reader) (button, x, y int, released, ok bool) {
	var seq strings.Builder
	var end byte
	for {
		r, _, err := br.ReadRune()
		if err != nil {
			return 0, 0, 0, false, false
		}
		if r == 'M' || r == 'm' {
			end = byte(r)
			break
		}
		seq.WriteRune(r)
	}
	parts := strings.Split(seq.String(), ";")
	if len(parts) != 3 {
		return 0, 0, 0, false, false
	}
	b, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	xx, e2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	yy, e3 := strconv.Atoi(strings.TrimSpace(parts[2]))
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, 0, 0, false, false
	}
	return b, xx, yy, end == 'm', true
}

// scrollToRow maps a 0-based terminal row to a scroll offset and jumps the
// viewport there. It uses the scrollbar geometry cached by the last render so
// the mapping stays in sync with what the user sees. row values outside the
// scrollbar track are clamped to the nearest end.
func (t *TUI) scrollToRow(row int) {
	t.mu.Lock()
	top, bottom, maxOff := t.sbTop, t.sbBottom, t.sbMaxOff
	t.mu.Unlock()

	if bottom <= top || maxOff <= 0 {
		return
	}
	if row < top {
		row = top
	}
	if row > bottom {
		row = bottom
	}
	// frac 0 at the top of the track (oldest content, max offset), 1 at the
	// bottom (newest content, offset 0).
	frac := float64(row-top) / float64(bottom-top)
	off := int((1 - frac) * float64(maxOff))
	if off < 0 {
		off = 0
	}
	if off > maxOff {
		off = maxOff
	}
	t.mu.Lock()
	t.scrollOffset = off
	t.mu.Unlock()
	t.scheduleRender()
}

// pasteFromClipboard reads the system clipboard and inserts it at the input
// cursor. Used by right-click (see handleMouse). No-op when the clipboard is
// empty or unreadable — the user can always fall back to Ctrl+Shift+V.
func (t *TUI) pasteFromClipboard() {
	text, err := readClipboard()
	if err != nil || strings.TrimSpace(text) == "" {
		return
	}
	t.dismissWelcome()
	t.insertAtCursor(text)
	t.updateSuggestions()
	t.scheduleRender()
}

// cursorForCol returns the rune index within input that best matches the given
// 0-based display column, accounting for wide (CJK) characters that occupy two
// columns. Used when the user clicks the input line to reposition the cursor.
func (t *TUI) cursorForCol(input string, col int) int {
	runes := []rune(input)
	w := 0
	for i, r := range runes {
		cw := runeWidth(r)
		if w+cw > col {
			return i
		}
		w += cw
		if w >= col {
			return i + 1
		}
	}
	return len(runes)
}
