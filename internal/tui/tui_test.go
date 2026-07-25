package tui

import (
	"bufio"
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
