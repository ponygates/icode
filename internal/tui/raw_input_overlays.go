package tui

// routeKeyToOverlay is the front of handleKey's decision chain: while one of the
// modal overlays is open every key belongs to it, so the overlay is asked first
// and in exactly this order (form wizard → ask-user pending → /login prompt →
// settings panel → Ctrl+R search). claimed reports that an overlay owned the
// key; keepGoing is the value handleKey must return (false ends the run loop).
func (t *TUI) routeKeyToOverlay(r rune) (claimed, keepGoing bool) {
	// Interactive form wizard (opencode AskQuestion parity): digits pick the
	// current question's option, Enter confirms multi-select/advances, Tab
	// jumps to the next question, Esc cancels the whole form.
	if t.askFormActive() {
		switch {
		case r >= '1' && r <= '9':
			t.formPick(int(r - '1'))
		case r == '\r' || r == '\n':
			t.formConfirm()
		case r == 0x09: // Tab — next question
			t.formAdvance()
		case r == 0x1b: // Esc — cancel
			t.mu.Lock()
			fs := t.askForm
			t.mu.Unlock()
			if fs != nil {
				t.resolveAskForm(fs.Answers)
			}
		}
		return true, true
	}
	// Interactive ask mode (Claude Code AskUserQuestion parity): while the
	// engine's ask_user_question tool waits, digits 1-9 pick an option,
	// Enter picks the first, Esc cancels. Everything else is swallowed so it
	// can't corrupt the input buffer mid-question.
	if t.askPendingVisible() {
		switch {
		case r >= '1' && r <= '9':
			t.mu.Lock()
			ask := t.askPending
			t.mu.Unlock()
			if ask != nil && int(r-'1') < len(ask.Options) {
				t.resolveAsk(int(r - '1'))
			}
		case r == '\r' || r == '\n':
			t.resolveAsk(0)
		case r == 0x1b: // Esc — cancel
			t.resolveAsk(-1)
		}
		return true, true
	}
	// The /login and /logout modal prompt is the most specific overlay: while
	// it waits for input every key belongs to it (a stray keystroke must never
	// land a secret in the normal input line).
	if t.promptActive() {
		return true, t.handlePromptKey(r)
	}
	// Settings panel keyboard navigation
	if t.settingsOpen {
		return true, t.handleSettingsKey(r)
	}
	// While the reverse-history-search overlay (Ctrl+R) is active, every key
	// is routed to the search handler — mirroring Claude Code's isearch.
	if t.searchMode {
		return true, t.handleSearchKey(r)
	}
	return false, true
}
