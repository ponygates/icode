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

// TestArrowScrollsConversation verifies that ↑/↓ drive the conversation's side
// scrollbar (one display line per press) while Ctrl+P/Ctrl+N keep history
// navigation. This is the behavior the user asked for ("CLI 版侧边可以上下滚动").
func TestArrowScrollsConversation(t *testing.T) {
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

	// ↑ should scroll the viewport up by one display line.
	rd := bufio.NewReader(strings.NewReader("\x1b[A"))
	tu.reader = rd
	r, _, _ := rd.ReadRune() // consume the leading ESC
	if !tu.handleKey(r) {
		t.Fatalf("handleKey(↑) returned false")
	}
	if tu.scrollOffset != 1 {
		t.Errorf("after ↑: scrollOffset = %d, want 1", tu.scrollOffset)
	}
	if tu.inputBuf != "" {
		t.Errorf("↑ changed inputBuf = %q, want empty", tu.inputBuf)
	}

	// ↓ while scrolled up should scroll back down one line (to 0), not touch
	// history (history is empty, so inputBuf must stay empty).
	rd2 := bufio.NewReader(strings.NewReader("\x1b[B"))
	tu.reader = rd2
	r2, _, _ := rd2.ReadRune()
	if !tu.handleKey(r2) {
		t.Fatalf("handleKey(↓) returned false")
	}
	if tu.scrollOffset != 0 {
		t.Errorf("after ↓: scrollOffset = %d, want 0", tu.scrollOffset)
	}
	if tu.inputBuf != "" {
		t.Errorf("↓ at bottom changed inputBuf = %q, want empty (history)", tu.inputBuf)
	}

	// At the very bottom with no overflow left, ↓ must fall through to history
	// navigation without panicking.
	if !tu.handleKey(r2) {
		t.Fatalf("handleKey(↓) at bottom returned false")
	}
}

// TestArrowFallsBackToHistoryWhenNotScrollable confirms that on a short
// conversation (nothing to scroll) ↑ keeps its original job: history prev.
func TestArrowFallsBackToHistoryWhenNotScrollable(t *testing.T) {
	tu := &TUI{width: 120, height: 60}
	tu.messages = append(tu.messages, Message{Role: RoleUser, Content: "hi"})
	if tu.canScroll() {
		t.Fatalf("short conversation should NOT be scrollable")
	}
	rd := bufio.NewReader(strings.NewReader("\x1b[A"))
	tu.reader = rd
	r, _, _ := rd.ReadRune()
	if !tu.handleKey(r) {
		t.Fatalf("handleKey(↑) returned false")
	}
	if tu.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d, want 0 (no scroll on short conv)", tu.scrollOffset)
	}
}
