package tui

import (
	"strings"
)

// handleEnterKey owns the plain Enter/Return key (Alt+Enter is handled inside the
// Esc branch, Ctrl+J inserts a newline). An open overlay takes the key first, then
// multi-line mode, then the trailing-backslash continuation, and only then is the
// draft submitted.
func (t *TUI) handleEnterKey() bool {
	if t.enterOverlayAction() {
		return true
	}
	if t.multiline {
		// In multi-line mode, Enter inserts a newline. Submit with Alt+Enter.
		t.mu.Lock()
		t.inputBuf += "\n"
		t.cursor++
		t.mu.Unlock()
		return true
	}
	// Claude Code parity: a trailing backslash on the caret's logical line
	// continues the input on a new line instead of submitting — handy for
	// long prompts on any terminal, no Ctrl+J reachability needed.
	if li, ci := inputCursorPos(t.inputBuf, t.cursor); ci > 0 {
		logical := []rune(strings.Split(t.inputBuf, "\n")[li])
		if ci == len(logical) && len(logical) > 0 && logical[len(logical)-1] == '\\' {
			abs := t.cursor - 1 // drop the backslash, insert a newline
			runes := []rune(t.inputBuf)
			t.setInput(string(runes[:abs])+"\n"+string(runes[t.cursor:]), abs+1)
			return true
		}
	}
	t.submitEnterDraft()
	return true
}

// enterOverlayAction reports that Enter acted on an open overlay instead of the
// input line (review box, model /resume picker, replay timeline).
func (t *TUI) enterOverlayAction() bool {
	if t.diffBoxOpen {
		t.applyStagedEdits()
		t.closeDiffBox()
		return true
	}
	if t.modelPickerOpen {
		t.selectModelAt(t.modelPickerIdx)
		return true
	}
	if t.resumePickerOpen {
		t.resumeSessionAt(t.resumePickerIdx)
		return true
	}
	if t.replayOpen {
		t.replayViewStep(t.replayIdx)
		return true
	}
	return false
}

// submitEnterDraft clears the prompt and sends it, honouring the autocomplete and
// slash-command-prefix conveniences Claude Code uses on Enter.
func (t *TUI) submitEnterDraft() {
	text := strings.TrimSpace(t.inputBuf)
	t.setInput("", 0)
	if text == "" {
		// An empty Enter does nothing but dismiss the welcome banner, if any.
		t.dismissWelcome()
		return
	}
	// Claude Code parity: Enter accepts the HIGHLIGHTED autocomplete row —
	// except when the highlighted item is exactly what was typed (then it
	// just submits, preserving muscle memory for exact commands).
	if t.acOpen && len(t.acItems) > 0 && t.acIdx < len(t.acItems) {
		if item := t.acItems[t.acIdx].Name; item != text {
			t.acceptSuggestion()
			return
		}
	}
	// Auto-complete an incomplete slash-command prefix on Enter: "/c"
	// runs the first matching command (e.g. /compact), a bare "/" never
	// dispatches an empty command.
	if full, ok := t.completeSlashCommand(text); ok {
		text = full
	}
	t.pushHistory(text)
	t.submit(text)
}
