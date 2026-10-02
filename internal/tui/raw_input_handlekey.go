package tui

// handleKey processes a single input rune in raw mode.
// Returns false to signal the loop should exit.
//
// The order below is the historical decision order and is load-bearing:
//
//  1. a modal overlay owns the key outright (routeKeyToOverlay);
//  2. any key other than Esc disarms the armed double-Esc rewind;
//  3. Esc (the sequence/overlay router) and Enter get their own handlers;
//  4. the bound control runes go through handleControlKeys;
//  5. whatever is left is the printable tail (Ctrl+V, unbound control
//     characters, picker hotkeys, the help overlay, vim, text insertion).
//
// Every helper keeps its own decision order inside; the control-key groups are
// disjoint, so running them in sequence matches the original single switch.
func (t *TUI) handleKey(r rune) bool {
	if claimed, keepGoing := t.routeKeyToOverlay(r); claimed {
		return keepGoing
	}
	// Any key other than Esc disarms an armed double-Esc rewind so the
	// rollback shortcut can never fire by accident later.
	if r != 0x1b {
		t.rewindArmed = false
	}
	switch r {
	case 0x1b:
		return t.handleEscapeKey()
	case '\r':
		// Note: plain \n (Ctrl+J) inserts a newline — see handleControlKeys.
		return t.handleEnterKey()
	}
	if keepGoing, handled := t.handleControlKeys(r); handled {
		return keepGoing
	}
	return t.handlePrintableKey(r)
}
