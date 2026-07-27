package tui

import (
	"bufio"
	"strconv"
	"strings"
	"testing"
)

func TestParseSGRMouse(t *testing.T) {
	cases := []struct {
		seq      string
		button   int
		x, y     int
		released bool
	}{
		{"0;12;5M", 0, 12, 5, false},
		{"64;10;8M", 64, 10, 8, false},
		{"65;10;8M", 65, 10, 8, false},
		{"0;12;5m", 0, 12, 5, true},
		{"32;3;4M", 32, 3, 4, false},
	}
	for _, c := range cases {
		b, x, y, rel, ok := parseSGRMouse(bufio.NewReader(strings.NewReader(c.seq)))
		if !ok {
			t.Fatalf("seq %q: expected ok", c.seq)
		}
		if b != c.button || x != c.x || y != c.y || rel != c.released {
			t.Errorf("seq %q -> got (%d,%d,%d,%v) want (%d,%d,%d,%v)",
				c.seq, b, x, y, rel, c.button, c.x, c.y, c.released)
		}
	}
}

func TestCursorForCol(t *testing.T) {
	cases := []struct {
		input string
		col   int
		want  int
	}{
		{"hello", 0, 0},
		{"hello", 3, 3},
		{"hello", 5, 5},
		{"hello", 99, 5},
		{"你好", 0, 0},
		{"你好", 1, 0},
		{"你好", 2, 1},
		{"你好", 3, 1},
		{"你好", 4, 2},
	}
	for _, c := range cases {
		got := (&TUI{}).cursorForCol(c.input, c.col)
		if got != c.want {
			t.Errorf("cursorForCol(%q,%d) = %d, want %d", c.input, c.col, got, c.want)
		}
	}
}

func TestDeleteWordBackward(t *testing.T) {
	tu := &TUI{inputBuf: "foo bar baz", cursor: 11}
	tu.deleteWordBackward()
	if got, want := tu.inputBuf, "foo bar "; got != want {
		t.Errorf("after 1st delete: %q want %q", got, want)
	}
	if tu.cursor != 8 {
		t.Errorf("cursor after 1st delete = %d want 8", tu.cursor)
	}
	tu.deleteWordBackward()
	if got, want := tu.inputBuf, "foo "; got != want {
		t.Errorf("after 2nd delete: %q want %q", got, want)
	}
	tu.deleteWordBackward()
	if got, want := tu.inputBuf, ""; got != want {
		t.Errorf("after 3rd delete: %q want %q", got, want)
	}
	if tu.cursor != 0 {
		t.Errorf("cursor after 3rd delete = %d want 0", tu.cursor)
	}
}

func TestDeleteToLineStart(t *testing.T) {
	tu := &TUI{inputBuf: "abc def", cursor: 4}
	tu.deleteToLineStart()
	if got, want := tu.inputBuf, "def"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if tu.cursor != 0 {
		t.Errorf("cursor = %d want 0", tu.cursor)
	}
}

func TestCycleMode(t *testing.T) {
	tu := &TUI{} // rawMode false → scheduleRender is a no-op
	tu.mode = ModeAuto
	tu.cycleMode()
	if tu.mode != ModePlan {
		t.Errorf("auto -> %q want plan", tu.mode)
	}
	tu.cycleMode()
	if tu.mode != ModeAgent {
		t.Errorf("plan -> %q want agent", tu.mode)
	}
	tu.cycleMode()
	if tu.mode != ModeYOLO {
		t.Errorf("agent -> %q want yolo", tu.mode)
	}
	tu.cycleMode()
	if tu.mode != ModeAuto {
		t.Errorf("yolo -> %q want auto", tu.mode)
	}
}

func TestScrollToRow(t *testing.T) {
	tu := &TUI{}
	tu.sbTop, tu.sbBottom, tu.sbMaxOff = 3, 12, 100
	// Top of track → max offset (oldest content).
	tu.scrollToRow(3)
	if tu.scrollOffset != 100 {
		t.Errorf("top: offset = %d want 100", tu.scrollOffset)
	}
	// Bottom of track → offset 0 (newest).
	tu.scrollToRow(12)
	if tu.scrollOffset != 0 {
		t.Errorf("bottom: offset = %d want 0", tu.scrollOffset)
	}
	// Middle row 7 → frac (7-3)/9 = 4/9 → off = (1-4/9)*100 ≈ 55.
	tu.scrollToRow(7)
	if tu.scrollOffset != 55 {
		t.Errorf("mid: offset = %d want 55", tu.scrollOffset)
	}
	// Out-of-range clamp.
	tu.scrollToRow(1)
	if tu.scrollOffset != 100 {
		t.Errorf("clamp low: offset = %d want 100", tu.scrollOffset)
	}
	tu.scrollToRow(99)
	if tu.scrollOffset != 0 {
		t.Errorf("clamp high: offset = %d want 0", tu.scrollOffset)
	}
}

// TestArrowNavigatesHistory mirrors Claude Code: ↑/↓ move through input
// history (not the conversation scrollbar). A tall, scrollable conversation is
// used to prove the arrow keys do NOT scroll it — they drive history instead.
func TestArrowNavigatesHistory(t *testing.T) {
	// Build a tall conversation so it overflows and canScroll() is true.
	tu := &TUI{width: 80, height: 40}
	for i := 0; i < 200; i++ {
		tu.messages = append(tu.messages, Message{
			Role:    RoleUser,
			Content: "line " + strconv.Itoa(i) + " " + strings.Repeat("x", 60),
		})
	}
	if !tu.canScroll() {
		t.Fatalf("expected canScroll() == true for a tall conversation")
	}

	// Seed history so ↑/↓ have something to navigate.
	tu.pushHistory("first command")
	tu.pushHistory("second command")

	// sendArrow feeds a full CSI arrow sequence (ESC [ A/B) into handleKey,
	// mimicking what the terminal delivers on each key press.
	sendArrow := func(seq string) {
		rd := bufio.NewReader(strings.NewReader(seq))
		tu.reader = rd
		r, _, _ := rd.ReadRune() // leading ESC
		if !tu.handleKey(r) {
			t.Fatalf("handleKey(%q) returned false", seq)
		}
	}

	// ↑ should load the most recent history entry, NOT scroll the conversation.
	sendArrow("\x1b[A")
	if tu.inputBuf != "second command" {
		t.Errorf("after ↑: inputBuf = %q, want %q (history prev)", tu.inputBuf, "second command")
	}
	if tu.scrollOffset != 0 {
		t.Errorf("after ↑: scrollOffset = %d, want 0 (arrows never scroll)", tu.scrollOffset)
	}

	// A second ↑ loads the older entry.
	sendArrow("\x1b[A")
	if tu.inputBuf != "first command" {
		t.Errorf("after ↑ #2: inputBuf = %q, want %q", tu.inputBuf, "first command")
	}

	// ↓ returns toward the newest entry, then to an empty input at the end.
	sendArrow("\x1b[B")
	if tu.inputBuf != "second command" {
		t.Errorf("after ↓: inputBuf = %q, want %q", tu.inputBuf, "second command")
	}
	sendArrow("\x1b[B")
	if tu.inputBuf != "" {
		t.Errorf("after ↓ at end: inputBuf = %q, want empty", tu.inputBuf)
	}
}

// TestCtrlRReverseSearch exercises the Claude Code-style Ctrl+R isearch:
// open with Ctrl+R, type a query, accept the match into the input line.
func TestCtrlRReverseSearch(t *testing.T) {
	tu := &TUI{width: 80, height: 40}
	tu.pushHistory("build the project")
	tu.pushHistory("fix the parser bug")
	tu.pushHistory("write tests for parser")

	// Ctrl+R opens the search overlay.
	if !tu.handleKey(0x12) {
		t.Fatalf("handleKey(Ctrl+R) returned false")
	}
	if !tu.searchMode {
		t.Fatalf("searchMode expected true after Ctrl+R")
	}

	// Type "parser" — should match 2 entries (most recent first).
	for _, ch := range "parser" {
		if !tu.handleKey(ch) {
			t.Fatalf("handleKey(%q) returned false", ch)
		}
	}
	if len(tu.searchMatches) != 2 {
		t.Fatalf("searchMatches = %d, want 2", len(tu.searchMatches))
	}
	// Most recent match is "write tests for parser".
	if tu.searchMatches[0] != "write tests for parser" {
		t.Errorf("match[0] = %q, want %q", tu.searchMatches[0], "write tests for parser")
	}

	// Enter accepts the highlighted match into the input buffer.
	if !tu.handleKey('\r') {
		t.Fatalf("handleKey(Enter) returned false")
	}
	if tu.searchMode {
		t.Errorf("searchMode should be false after accepting")
	}
	if tu.inputBuf != "write tests for parser" {
		t.Errorf("inputBuf = %q, want %q", tu.inputBuf, "write tests for parser")
	}
}

// TestCtrlREscCancel confirms Esc/Ctrl+G cancels the search and restores the
// prior input buffer.
func TestCtrlREscCancel(t *testing.T) {
	tu := &TUI{width: 80, height: 40}
	tu.pushHistory("alpha command")
	tu.inputBuf = "draft text"
	tu.cursor = len([]rune(tu.inputBuf))

	tu.handleKey(0x12) // open search
	if !tu.searchMode {
		t.Fatalf("searchMode expected true")
	}
	tu.handleKey(0x1b) // Esc cancels
	if tu.searchMode {
		t.Errorf("searchMode should be false after Esc")
	}
	if tu.inputBuf != "draft text" {
		t.Errorf("inputBuf = %q, want restored %q", tu.inputBuf, "draft text")
	}
}

// TestMouseWheelScrollsConversation confirms that the SGR mouse wheel report
// (button 64 = wheel up, 65 = wheel down) drives the conversation's side
// scrollbar the same way the arrow keys do — letting the user scroll through
// output before/after the current view with the wheel. This is what the user
// asked for ("CLI 版鼠标滚轮可以查看当前对话前后的对话输出内容").
func TestMouseWheelScrollsConversation(t *testing.T) {
	tu := &TUI{width: 80, height: 40, scrollOffset: 0}
	for i := 0; i < 200; i++ {
		tu.messages = append(tu.messages, Message{
			Role:    RoleUser,
			Content: "line " + strconv.Itoa(i) + " " + strings.Repeat("x", 60),
		})
	}
	if !tu.canScroll() {
		t.Fatalf("expected canScroll() == true for a tall conversation")
	}

	// Wheel up (button 64) → scroll toward older content (offset increases).
	tu.handleMouse(bufio.NewReader(strings.NewReader("64;40;12M")))
	if tu.scrollOffset <= 0 {
		t.Errorf("wheel up: scrollOffset = %d, want > 0", tu.scrollOffset)
	}

	// Wheel down (button 65) → back toward newest (offset decreases to 0).
	tu.handleMouse(bufio.NewReader(strings.NewReader("65;40;12M")))
	if tu.scrollOffset != 0 {
		t.Errorf("wheel down: scrollOffset = %d, want 0", tu.scrollOffset)
	}
}
