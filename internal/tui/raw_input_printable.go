package tui

// handlePrintableKey is the tail of handleKey: everything the bound control keys,
// Esc and Enter did not claim. Ctrl+V pastes, unbound control characters are
// ignored, an open overlay may take the hotkey, vim normal mode drives the edit,
// and any real rune is inserted at the cursor.
func (t *TUI) handlePrintableKey(r rune) bool {
	if r == 0x16 { // Ctrl+V — paste from clipboard (image best-effort)
		t.pasteClipboardImage()
		return true
	}
	if r < 0x20 {
		// Ignore other control characters.
		return true
	}
	if t.handleOverlayHotkey(r) {
		return true
	}

	// Vim normal mode: printable runes are vi commands until the user returns
	// to insert mode (i/a/I/A or Esc). The dispatcher swallows unknown keys.
	if t.vimMode && !t.vimInsert {
		return t.handleVimKey(r)
	}

	// Printable rune (incl. Chinese) — insert at cursor.
	t.insertPrintableRune(r)
	return true
}

// handleOverlayHotkey lets an open transcript overlay take a printable key: a
// digit jump in the model picker, "r" in the replay timeline, any key dismissing
// the review box, and "?" toggling the shortcut help.
func (t *TUI) handleOverlayHotkey(r rune) bool {
	// While the model picker is open, a digit jumps to that line and any
	// other printable key cancels the picker (mirrors Claude Code, where
	// typing filters/exits the panel).
	if t.modelPickerOpen {
		if r >= '1' && r <= '9' {
			n := int(r - '1')
			if n < len(t.models) {
				t.modelPickerIdx = n
				t.updateModelPicker()
			}
			return true
		}
		t.closeModelPicker()
		return true
	}

	// Replay timeline: r rewinds to the highlighted checkpoint (two-press
	// confirm), any other printable key closes the overlay.
	if t.replayOpen {
		if r == 'r' || r == 'R' {
			t.replayRewindTo(t.replayIdx)
			return true
		}
		t.closeReplay()
		return true
	}

	// Any printable key dismisses the staged-edits review overlay
	// (mirrors Claude Code's model picker behaviour).
	if t.diffBoxOpen {
		t.closeDiffBox()
		return true
	}

	// '?' on an empty prompt opens the keyboard-shortcut help overlay
	// (Claude Code-style). Any other key while it's open dismisses it.
	if r == '?' && t.inputBuf == "" && !t.streaming && !t.helpVisible {
		t.helpVisible = true
		t.render()
		return true
	}
	if t.helpVisible {
		t.helpVisible = false
		t.render()
		return true
	}
	return false
}

// insertPrintableRune splices one rune in at the caret (multi-byte aware) and
// refreshes the autocomplete menu.
func (t *TUI) insertPrintableRune(r rune) {
	// The first keystroke also clears the welcome banner so typing feels
	// immediate (Claude Code does the same) — the rune is then inserted into
	// a clean prompt.
	t.dismissWelcome()
	t.mu.Lock()
	runes := []rune(t.inputBuf)
	if t.cursor >= len(runes) {
		t.inputBuf += string(r)
	} else {
		runes = append(runes, 0)
		copy(runes[t.cursor+1:], runes[t.cursor:])
		runes[t.cursor] = r
		t.inputBuf = string(runes)
	}
	t.cursor++
	t.mu.Unlock()
	t.updateSuggestions()
}
