package tui

import (
	"strings"
)

// handleCSISequence dispatches the letter/first digit after ESC [ or ESC O. The
// cases are disjoint, so this table is the same decision as the old inline switch;
// every branch consumes the rest of the sequence (that is what keeps stray bytes
// out of the input buffer).
func (t *TUI) handleCSISequence(c1 rune) {
	switch c1 {
	case 'A': // ↑ multi-line: cursor up a line; first line → history prev
		t.csiArrowUp()
	case 'B': // ↓ multi-line: cursor down a line; last line → history next
		t.csiArrowDown()
	case 'C': // → cursor right
		t.mu.Lock()
		runes := []rune(t.inputBuf)
		if t.cursor < len(runes) {
			t.cursor++
		}
		t.mu.Unlock()
	case 'D': // ← cursor left
		t.mu.Lock()
		if t.cursor > 0 {
			t.cursor--
		}
		t.mu.Unlock()
	case 'H':
		t.moveCursor(0)
	case 'F':
		t.moveCursor(len([]rune(t.inputBuf)))
	case '5': // PgUp — modifier variants ("5;5~") swallow whole
		t.consumeSeqTail()
		t.scrollPgUp()
	case '6': // PgDn
		t.consumeSeqTail()
		t.scrollPgDn()
	case '3': // Delete — remove the rune under the cursor
		// The tail must be consumed as a whole: a Ctrl+Delete arrives
		// as "3;5~", and reading exactly one byte (the old ";") left
		// the trailing "5~" to be typed into the input box as
		// garbage — the "pressing Delete prints junk" bug.
		t.consumeSeqTail()
		t.deleteUnderCursor()
		t.updateSuggestions()
	case '1', '4': // Home ("1~") / End ("4~") — and the leading digit
		// of modifier arrow reports ("1;5A" = Ctrl+Up). Consume the
		// whole tail first; only the plain "~"-terminated form is a
		// real Home/End press, the letter-terminated ones are
		// modified arrows we deliberately ignore.
		t.csiHomeEnd(c1)
	case 'Z': // Shift+Tab → cycle agent mode
		t.cycleMode()
	case 'P', 'Q', 'R', 'S': // SS3 F1–F4 — unbound, swallow silently
		// Without this case they fell into the CSI default and its
		// drain loop, which waited for a '~'/'m'/'M' that never
		// comes — eating every keystroke the user typed next.
	case '<': // SGR mouse report
		// handleMouse always PARSES the report (consuming the bytes —
		// skipping here would leak the payload onto keyCh); the
		// /mouse toggle gates the ACTIONS inside handleMouse.
		t.handleMouse(keyRuneReader{ch: t.keyCh, site: ""})
	case 'M': // X10 mouse report — 3 raw coordinate bytes follow.
		t.csiX10Mouse()
	case '2':
		// 2-prefixed sequences: bracketed paste "200~", F12 "24~",
		// Insert "2~", and anything else. Collect the tail with a
		// timeout so a truncated sequence can never start
		// swallowing the user's next keystrokes.
		t.csiExtendedTail()
	default:
		t.drainUnknownCSI()
	}
}

// csiArrowUp handles ↑: whichever overlay owns the key wins, then the multi-line
// cursor, then history.
func (t *TUI) csiArrowUp() {
	if t.modelPickerOpen {
		t.movePicker(-1)
		return
	}
	if t.resumePickerOpen {
		t.moveResumePicker(-1)
		return
	}
	if t.replayOpen {
		t.moveReplay(-1)
		return
	}
	if t.diffBoxOpen {
		t.moveDiff(-1)
		return
	}
	if t.acOpen && len(t.acItems) > 0 {
		// Wrap around: up from the first item lands on the last.
		t.acIdx = (t.acIdx - 1 + len(t.acItems)) % len(t.acItems)
		return
	}
	li, col := inputCursorPos(t.inputBuf, t.cursor)
	if li > 0 {
		t.moveCursor(inputAbsCursor(t.inputBuf, li-1, col))
		return
	}
	t.historyPrev()
}

// csiArrowDown is the ↓ counterpart of csiArrowUp.
func (t *TUI) csiArrowDown() {
	if t.modelPickerOpen {
		t.movePicker(1)
		return
	}
	if t.resumePickerOpen {
		t.moveResumePicker(1)
		return
	}
	if t.replayOpen {
		t.moveReplay(1)
		return
	}
	if t.diffBoxOpen {
		t.moveDiff(1)
		return
	}
	if t.acOpen && len(t.acItems) > 0 {
		// Wrap around: down from the last item lands on the first.
		t.acIdx = (t.acIdx + 1) % len(t.acItems)
		return
	}
	lines := strings.Split(t.inputBuf, "\n")
	li, col := inputCursorPos(t.inputBuf, t.cursor)
	if li < len(lines)-1 {
		t.moveCursor(inputAbsCursor(t.inputBuf, li+1, col))
		return
	}
	t.historyNext()
}

// csiHomeEnd handles the 1~/4~ forms; a letter-terminated tail is a modified
// arrow report we deliberately ignore.
func (t *TUI) csiHomeEnd(c1 rune) {
	if f, ok := t.consumeSeqFinal(); ok && f == '~' {
		if c1 == '1' {
			t.moveCursor(0)
		} else {
			t.moveCursor(len([]rune(t.inputBuf)))
		}
	}
}

// csiX10Mouse consumes the three raw coordinate bytes of an X10 mouse report and
// acts on the wheel / legacy right-click paste.
func (t *TUI) csiX10Mouse() {
	// keyPump's reassembly (pumpEscape) reads the three raw
	// bytes past the UTF-8 decoder and dispatches them in the
	// same burst, so they are already queued on keyCh by the
	// time we get here — including coordinates > 0x7F on wide
	// terminals (the old ">95 columns wheel dies" limit). The
	// timeouts stay purely as a truncated-burst fallback.
	b1, ok1 := t.nextKeyTimeout(escSwallowWait)
	if !ok1 {
		t.dumpInputTrace("x10-short")
		return
	}
	b2, ok2 := t.nextKeyTimeout(escSwallowWait)
	b3, ok3 := t.nextKeyTimeout(escSwallowWait)
	if !ok2 || !ok3 {
		t.dumpInputTrace("x10-short")
		return
	}
	_ = b2
	_ = b3
	switch btn := int(b1) - 32; {
	case btn == 64:
		t.scrollUpSmall()
	case btn == 65:
		t.scrollDownSmall()
	case btn&3 == 2:
		t.pasteFromClipboard() // legacy terminals: right-click press
	}
}

// csiExtendedTail collects the tail of a 2-prefixed sequence and dispatches the
// few forms we understand (bracketed paste, F12 screen dump).
func (t *TUI) csiExtendedTail() {
	var tail strings.Builder
	for {
		rr, ok := t.nextKeyTimeout(escFollowTimeout)
		if !ok {
			t.dumpInputTrace("csi2-drain-timeout")
			break
		}
		tail.WriteRune(rr)
		if rr == '~' || rr == 'm' || rr == 'M' {
			break
		}
	}
	switch tail.String() {
	case "00~": // bracketed paste
		pasted := t.readPaste(keyRuneReader{ch: t.keyCh, site: "paste"})
		t.dismissWelcome()
		t.insertPasted(pasted)
		t.updateSuggestions()
	case "4~": // F12 — snapshot the live screen buffer to
		// ~/.icode/screen-dump.txt — the definitive diagnostic
		// for "text appears in the wrong place" reports. The
		// state dump that follows adds the RAW buffers (input,
		// queue, stream, messages) plus a screen-vs-lastFrame
		// diff that names any row written by something OTHER
		// than our renderer.
		scr := dumpScreen()
		t.dumpState(scr)
	}
}

// drainUnknownCSI throws away an unrecognised sequence up to its terminator (or a
// timeout) and ignores it. Without the timeout, a sequence ending in neither
// ~ m M (a CPR/DA reply, an OSC fragment) swallowed every subsequent keystroke
// until one of those arrived by chance.
func (t *TUI) drainUnknownCSI() {
	for {
		rr, ok := t.nextKeyTimeout(escFollowTimeout)
		if !ok {
			t.dumpInputTrace("csi-drain-timeout")
			break
		}
		if rr == '~' || rr == 'm' || rr == 'M' {
			break
		}
	}
}
