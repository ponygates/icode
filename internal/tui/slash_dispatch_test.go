package tui

import (
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

