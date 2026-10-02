package tui

import (
	"strings"
	"testing"
)

// Paste is the only unguarded channel for terminal-control bytes into the
// input buffer (keyboard input is already filtered at handleKey), and the
// input box paints its buffer verbatim — so clipboard text carrying ANSI
// escapes would be re-executed by the terminal on every repaint. These
// tests pin the sanitize gate on both layers: the paste entry point and
// the render output.

func TestSanitizeInputStripsControlBytes(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain text untouched", "你好 world", "你好 world"},
		{"newline and tab kept", "a\nb\tc", "a\nb\tc"},
		{"ANSI colour escape stripped", "\x1b[31mred\x1b[0m", "red"},
		{"CSI cursor jump stripped", "x\x1b[1;5Ay", "xy"},
		{"carriage return stripped", "a\rb", "ab"},
		{"C1 range stripped", "a\u0085b\u0090c", "abc"},
		{"DEL stripped", "a\x7fb", "ab"},
		{"NUL stripped", "\x00a", "a"},
	}
	for _, c := range cases {
		if got := sanitizeInput(c.in); got != c.want {
			t.Errorf("%s: sanitizeInput(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestSanitizeInputFastPath: clean input must come back as the identical
// string (no allocation churn on the common typing path).
func TestSanitizeInputFastPath(t *testing.T) {
	s := strings.Repeat("普通的输入内容 normal input ", 10)
	if got := sanitizeInput(s); got != s {
		t.Fatalf("fast path altered clean input")
	}
}

// TestInsertPastedSanitizesANSI: clipboard text copied from a terminal
// (colour codes + cursor moves) must land in the buffer with the poison
// already removed — this is the "paste garbage, then typing re-executes it
// on every repaint" bug.
func TestInsertPastedSanitizesANSI(t *testing.T) {
	tu := newTestTUI()
	tu.insertPasted("\x1b[32mOK\x1b[0m done")
	if tu.inputBuf != "OK done" {
		t.Fatalf("insertPasted left control bytes: inputBuf=%q want %q", tu.inputBuf, "OK done")
	}
}

// TestInsertPastedCRLFNormalized: Windows clipboard text uses CRLF; it must
// normalize to LF before folding counts lines.
func TestInsertPastedCRLFNormalized(t *testing.T) {
	tu := newTestTUI()
	tu.insertPasted("a\r\nb\r\nc\r\nd") // 4 lines → folds to placeholder
	if !strings.Contains(tu.inputBuf, "粘贴 4 行") {
		t.Fatalf("CRLF paste miscounted/folded wrong: inputBuf=%q", tu.inputBuf)
	}
	if got, ok := tu.pasteBlocks[strings.TrimSpace(tu.inputBuf)]; !ok || got != "a\nb\nc\nd" {
		t.Fatalf("folded content not CRLF-normalized: %q", got)
	}
}

// TestMessagePipelineSanitizesToolOutput: tool stdout/stderr is the main
// ANSI carrier (colour-coded go/git/npm output). Every entry point into the
// transcript must strip it — a poisoned row is re-executed by the terminal
// on every repaint of that row (Delete/typing resize the input box, which
// reshuffles and repaints the whole transcript area).
func TestMessagePipelineSanitizesToolOutput(t *testing.T) {
	tu := newTestTUI()
	tu.AddToolMessage("bash", `{"cmd":"ls"}`, "\x1b[32mok\x1b[0m\nfile1")
	tu.AppendToolResult("\x1b[31merr\x1b[0m tail")
	tu.AppendToolProgress("\x1b[2Jwiping")
	n := len(tu.messages)
	if n == 0 {
		t.Fatal("no tool message recorded")
	}
	m := tu.messages[n-1]
	if m.Content != "ok\nfile1\nerr tail" {
		t.Fatalf("tool Content not sanitized: %q", m.Content)
	}
	if m.LiveTail != "wiping" {
		t.Fatalf("LiveTail not sanitized: %q", m.LiveTail)
	}
}

// TestAddMessageSanitizesSystem: AddMessage is the shared entry for
// user/system/error/assistant/thinking content — one gate covers all roles.
func TestAddMessageSanitizesSystem(t *testing.T) {
	tu := newTestTUI()
	tu.AddMessage(RoleSystem, "before\x1b[1;5Aafter")
	m := tu.messages[len(tu.messages)-1]
	if m.Content != "beforeafter" {
		t.Fatalf("system message not sanitized: %q", m.Content)
	}
}

// TestDrawInputBoxNeverPaintsControlBytes: render-level belt-and-braces —
// even if a control byte reaches the buffer through some future path, the
// frame bytes written to the terminal must not contain it.
func TestDrawInputBoxNeverPaintsControlBytes(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.width, tu.height = 80, 24
	tu.inputBuf = "before\x1b[2Jafter" // a full-screen-clear escape!
	frame := tu.drawInputBox(80, 24, tu.inputBuf, 3, false, "status")
	if strings.Contains(frame, "\x1b[2J") {
		t.Fatalf("drawInputBox painted a control escape into the frame:\n%q", frame)
	}
	if !strings.Contains(frame, "before") || !strings.Contains(frame, "after") {
		t.Fatalf("visible text lost by sanitize:\n%q", frame)
	}
}
