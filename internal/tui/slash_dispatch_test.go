package tui

import (
	"bufio"
	"strings"
	"testing"
)

func newTestTUI() *TUI {
	return New(Config{
		Mode:     ModeAuto,
		Model:    "openrouter/free",
		Provider: "openrouter",
		Version:  "test",
		Callback: nil,
	})
}

// countSystem returns how many system messages are currently rendered.
func countSystem(t *TUI) int {
	n := 0
	for _, m := range t.messages {
		if m.Role == RoleSystem {
			n++
		}
	}
	return n
}

func TestSubmitSlashHelp(t *testing.T) {
	tu := newTestTUI()
	tu.submit("/help")
	if countSystem(tu) == 0 {
		t.Fatalf("/help produced no system message; messages=%d", len(tu.messages))
	}
	// The help text should mention at least one command like /model.
	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "/model") {
		t.Errorf("/help output missing expected command list:\n%s", joined)
	}
}

func TestSubmitSlashModel(t *testing.T) {
	tu := newTestTUI()
	tu.submit("/model claude-3-opus")
	if tu.model != "claude-3-opus" {
		t.Errorf("model not switched: got %q", tu.model)
	}
	if countSystem(tu) == 0 {
		t.Fatalf("/model produced no system message")
	}
}

func TestSubmitSlashUnknown(t *testing.T) {
	tu := newTestTUI()
	// Unknown command with nil callback should not panic and should not
	// echo the literal text as a user message.
	tu.submit("/thisdoesnotexist")
	for _, m := range tu.messages {
		if m.Role == RoleUser {
			t.Errorf("unknown slash command echoed as user message: %q", m.Content)
		}
	}
}

func TestSubmitUserMessage(t *testing.T) {
	tu := newTestTUI()
	tu.submit("hello world")
	found := false
	for _, m := range tu.messages {
		if m.Role == RoleUser && m.Content == "hello world" {
			found = true
		}
	}
	if !found {
		t.Errorf("user message not recorded; messages=%d", len(tu.messages))
	}
}

// messagesText flattens all messages to their content for substring checks.
func messagesText(t *TUI) []string {
	out := make([]string, 0, len(t.messages))
	for _, m := range t.messages {
		out = append(out, m.Content)
	}
	return out
}

// TestRawKeyInputSlash simulates the real raw-mode path: a fresh session
// starts with the welcome banner visible; the user types "/help" one rune at
// a time (which also dismisses the welcome screen) and presses Enter. This
// exercises welcome dismissal + autocomplete + Enter routing — the layers the
// direct submit() test skips.
func TestRawKeyInputSlash(t *testing.T) {
	tu := newTestTUI()
	if !tu.welcomeVisible {
		t.Fatalf("expected welcomeVisible=true on fresh TUI")
	}
	for _, r := range "/help" {
		if !tu.handleKey(r) {
			t.Fatalf("handleKey(%q) signalled exit", r)
		}
	}
	if tu.welcomeVisible {
		t.Errorf("welcome banner should have been dismissed by first keystroke")
	}
	if tu.inputBuf != "/help" {
		t.Fatalf("inputBuf after typing = %q, want /help", tu.inputBuf)
	}
	// Press Enter.
	if !tu.handleKey('\r') {
		t.Fatalf("handleKey(Enter) signalled exit")
	}
	if countSystem(tu) == 0 {
		t.Fatalf("typing /help + Enter produced no system message; messages=%d", len(tu.messages))
	}
}

// TestModelPicker verifies /model with no argument shows the picker and that
// /model <n> (index) and /model <id> both switch the active model. This is
// the regression test for the "typed /model but no switcher appeared" report.
func TestModelPicker(t *testing.T) {
	tu := newTestTUI()
	tu.SetModels([]string{"openrouter/free", "claude-3-opus", "gpt-4"})
	tu.model = "openrouter/free"
	tu.modelIdx = 0

	tu.submit("/model")
	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "openrouter/free") || !strings.Contains(joined, "gpt-4") {
		t.Errorf("/model picker missing models:\n%s", joined)
	}
	if !strings.Contains(joined, "▶") {
		t.Errorf("/model picker missing current-model marker:\n%s", joined)
	}

	tu.submit("/model 2")
	if tu.model != "claude-3-opus" {
		t.Errorf("/model 2 did not switch to 2nd model: got %q", tu.model)
	}

	tu.submit("/model gpt-4")
	if tu.model != "gpt-4" {
		t.Errorf("/model gpt-4 did not switch by id: got %q", tu.model)
	}
}

// TestHistoryRecall verifies the up/down arrow history navigation logic used
// by raw mode (and now also recorded in line mode). Regression for the
// "arrow keys don't show history" report.
func TestHistoryRecall(t *testing.T) {
	tu := newTestTUI()
	tu.pushHistory("first")
	tu.pushHistory("second")
	tu.pushHistory("third")
	if len(tu.history) != 3 {
		t.Fatalf("history len = %d, want 3", len(tu.history))
	}
	tu.historyPrev()
	if tu.inputBuf != "third" {
		t.Errorf("historyPrev#1 = %q, want third", tu.inputBuf)
	}
	tu.historyPrev()
	if tu.inputBuf != "second" {
		t.Errorf("historyPrev#2 = %q, want second", tu.inputBuf)
	}
	tu.historyNext()
	if tu.inputBuf != "third" {
		t.Errorf("historyNext = %q, want third", tu.inputBuf)
	}
	tu.historyNext()
	if tu.inputBuf != "" {
		t.Errorf("historyNext past end = %q, want empty", tu.inputBuf)
	}
}

// TestModelPickerInteractive drives the arrow-key / Enter selection path that
// raw mode uses, including the ESC[ A/B CSI sequences. This is the regression
// test for "can't use up/down to select and switch" — the picker must respond
// to navigation keys, not just the /model <n> syntax.
func TestModelPickerInteractive(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.SetModels([]string{"a", "b", "c", "d"})
	tu.modelIdx = 0

	// /model with no arg opens the interactive picker.
	tu.submit("/model")
	if !tu.modelPickerOpen {
		t.Fatalf("picker did not open on /model")
	}
	if tu.modelPickerMsgIdx < 0 {
		t.Fatalf("picker panel message index not recorded")
	}

	// Down, Down -> highlight index 2.
	feedEsc(t, tu, "\x1b[B")
	feedEsc(t, tu, "\x1b[B")
	if tu.modelPickerIdx != 2 {
		t.Fatalf("after two downs modelPickerIdx=%d, want 2", tu.modelPickerIdx)
	}
	// Up -> highlight index 1.
	feedEsc(t, tu, "\x1b[A")
	if tu.modelPickerIdx != 1 {
		t.Fatalf("after up modelPickerIdx=%d, want 1", tu.modelPickerIdx)
	}
	// Clamp: many downs stay at last index.
	for i := 0; i < 10; i++ {
		feedEsc(t, tu, "\x1b[B")
	}
	if tu.modelPickerIdx != 3 {
		t.Fatalf("down should clamp at last index, got %d", tu.modelPickerIdx)
	}
	// Enter confirms the highlighted model.
	if !tu.handleKey('\r') {
		t.Fatalf("Enter signalled exit")
	}
	if tu.model != "d" {
		t.Errorf("Enter did not select highlighted model: got %q, want d", tu.model)
	}
	if tu.modelPickerOpen {
		t.Errorf("picker should be closed after Enter")
	}
	if tu.modelPickerMsgIdx != -1 {
		t.Errorf("pickerMsgIdx should reset to -1, got %d", tu.modelPickerMsgIdx)
	}
}

// TestModelPickerEscCancel verifies Esc cancels the picker without changing
// the active model.
func TestModelPickerEscCancel(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.SetModels([]string{"a", "b", "c"})
	tu.model = "a"
	tu.modelIdx = 0
	tu.submit("/model")
	// Move highlight, then Esc cancels.
	feedEsc(t, tu, "\x1b[B")
	if !tu.modelPickerOpen {
		t.Fatalf("picker should be open before Esc")
	}
	feedEsc(t, tu, "\x1b") // plain Esc (no CSI) cancels
	if tu.modelPickerOpen {
		t.Errorf("Esc did not cancel the picker")
	}
	if tu.model != "a" {
		t.Errorf("Esc should not change model, got %q", tu.model)
	}
}

// feedEsc sets the reader to the remainder of an ESC sequence (after the
// leading 0x1b) and dispatches the ESC key through handleKey, exactly as the
// raw-mode main loop does.
func feedEsc(t *testing.T, tu *TUI, seq string) {
	if len(seq) == 0 || seq[0] != 0x1b {
		t.Fatalf("feedEsc expects a sequence starting with ESC, got %q", seq)
	}
	tu.reader = bufio.NewReader(strings.NewReader(seq[1:]))
	if !tu.handleKey(0x1b) {
		t.Fatalf("handleKey(ESC %q) signalled exit", seq)
	}
}

