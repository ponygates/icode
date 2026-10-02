package tui

import (
	"strings"
	"time"
)

// escFollow describes what followed an Esc byte, which is how handleKey
// distinguishes a lone Esc press from the start of an escape sequence.
type escFollow int

const (
	escFollowClosed escFollow = iota // the key reader hit EOF — no follow-up can arrive
	escFollowLone                    // nothing within escFollowTimeout — a plain Esc press
	escFollowSeq                     // a follow-up byte arrived — this is a sequence
)

// handleEscapeKey owns the whole Esc branch. Escape sequences: arrow keys, mouse
// reports, bracketed paste, and the Shift+Tab mode cycle. The first byte after ESC
// decides which. Without buffering these (reading each as a separate rune) stray
// "[" / "A" characters would leak into the input buffer — the classic "garbled
// text on up/down" bug.
//
// Lone Esc (no follow-up byte yet): the terminal delivers a plain ESC press as a
// single byte, while sequences arrive as one burst. The key pump forwards runes one
// at a time, so we cannot peek the reader; instead we wait escFollowTimeout for a
// follow-up byte. A blocking read here would hang forever on a lone Esc — the
// classic "ESC key does nothing" bug.
func (t *TUI) handleEscapeKey() bool {
	ur, kind := t.awaitEscFollow()
	switch kind {
	case escFollowClosed:
		return true
	case escFollowLone:
		t.dismissOverlaysOnEsc()
		return true
	}
	t.consumeEscSequence(ur)
	return true
}

// awaitEscFollow waits (bounded by escFollowTimeout) for the byte that decides
// whether this Esc was a lone press or the head of a sequence.
func (t *TUI) awaitEscFollow() (rune, escFollow) {
	select {
	case u, ok := <-t.keyCh:
		if !ok {
			return 0, escFollowClosed
		}
		return u, escFollowSeq
	case <-time.After(escFollowTimeout):
		return 0, escFollowLone
	}
}

// dismissOverlaysOnEsc is the lone-Esc behaviour: interrupt a running generation
// first, otherwise dismiss whichever overlay is open. With nothing to dismiss the
// key becomes a double-Esc rewind candidate.
func (t *TUI) dismissOverlaysOnEsc() {
	if t.streaming {
		if t.callback != nil {
			t.callback.OnInterrupt()
		}
		return
	}
	if t.acOpen {
		t.acOpen = false
		t.acItems = nil
		return
	}
	if t.helpVisible {
		t.helpVisible = false
		return
	}
	t.mu.Lock()
	planPending := t.planPending
	t.mu.Unlock()
	if planPending {
		t.SetPlanPending(false)
		t.render()
	} else if t.diffBoxOpen {
		t.closeDiffBox()
	} else if t.modelPickerOpen {
		t.closeModelPicker()
	} else if t.resumePickerOpen {
		t.closeResumePicker()
	} else if t.replayOpen {
		t.closeReplay()
	} else if t.vimMode && !t.vimInsert {
		t.vimInsert = true
		t.render()
	} else if t.dismissWelcome() {
		// Welcome banner closed.
	} else {
		// No overlay to dismiss — this Esc is a "double-Esc" candidate
		// (Claude Code parity): clear the draft, or arm/execute rewind.
		t.handleLoneEsc()
	}
}

// consumeEscSequence handles the byte that followed Esc: Alt+Enter, a plain key
// that cancels a picker, a CSI/SS3 sequence, or an Alt+<letter> binding.
func (t *TUI) consumeEscSequence(ur rune) {
	// Alt+Enter (or Alt+Return): submit current input.
	if ur == '\r' || ur == '\n' {
		t.altEnterSubmit()
		return
	}
	// Plain Esc (or any non-CSI key) cancels the model /resume pickers.
	if t.closePickersOnPlainEsc(ur) {
		return
	}
	if ur == '[' || ur == 'O' {
		// CSI (ESC[…) or SS3 (ESC O…) sequence — application-cursor-mode
		// terminals send arrows as SS3; both share the letter alphabet.
		c1, ok := t.nextKey()
		if !ok {
			return
		}
		t.handleCSISequence(c1)
		return
	}
	t.handleAltKey(ur)
}

// altEnterSubmit submits the current draft without leaving raw mode.
func (t *TUI) altEnterSubmit() {
	text := strings.TrimSpace(t.inputBuf)
	// Unfold any pasted-block placeholders before history + submit so
	// ↑-recalled history and the model both receive the real content.
	text = t.expandPasteBlocks(text)
	t.setInput("", 0)
	if text != "" {
		t.pushHistory(text)
		t.submit(text)
	}
}

// closePickersOnPlainEsc reports whether a non-CSI byte after Esc closed one of
// the transcript pickers.
func (t *TUI) closePickersOnPlainEsc(ur rune) bool {
	if t.modelPickerOpen && ur != '[' {
		t.closeModelPicker()
		return true
	}
	if t.resumePickerOpen && ur != '[' {
		t.closeResumePicker()
		return true
	}
	if t.replayOpen && ur != '[' {
		t.closeReplay()
		return true
	}
	return false
}

// handleAltKey owns the Alt+<letter> bindings; an unbound Alt combo is treated as
// a printable insertion (e.g. Alt+b), matching the historical behaviour.
func (t *TUI) handleAltKey(ur rune) {
	// Alt+P — switch model without clearing the prompt (Claude Code).
	if ur == 'p' || ur == 'P' {
		t.showModelPicker()
		return
	}
	// Alt+V — image paste alias (Claude Code parity; Ctrl+V also works).
	if ur == 'v' || ur == 'V' {
		t.pasteClipboardImage()
		return
	}
	// Alt+T — toggle extended thinking in place (Claude Code).
	if ur == 't' || ur == 'T' {
		t.thinkingOn = !t.thinkingOn
		if t.thinkingOn {
			t.handleSlash("/thinking on")
		} else {
			t.handleSlash("/thinking off")
		}
		return
	}
	// Alt+<key>: treat the key as a printable insertion (e.g. Alt+b).
	if ur >= 0x20 && ur != 0x7f {
		t.dismissWelcome()
		t.insertAtCursor(string(ur))
		t.updateSuggestions()
		return
	}
}
