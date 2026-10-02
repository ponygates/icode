package tui

import (
	"io"
	"strings"
)

// setInput replaces the input buffer and cursor as one unit, synchronised
// against concurrent render() snapshots taken on timer/streaming goroutines.
// Callers must NOT hold t.mu (single short critical section, no reentrancy).
//
// Why this matters: t.inputBuf is a Go string (header + pointer). Assigning it
// from the main goroutine while a timer-driven render() reads it under t.mu is
// a data race — a torn read pairs a new pointer with a stale length and the
// frame paints garbage. Every write to inputBuf/cursor outside t.mu must go
// through setInput/moveCursor.
func (t *TUI) setInput(buf string, cur int) {
	t.mu.Lock()
	t.inputBuf = buf
	t.cursor = cur
	t.mu.Unlock()
}

// moveCursor repositions only the edit cursor, synchronised like setInput.
func (t *TUI) moveCursor(cur int) {
	t.mu.Lock()
	t.cursor = cur
	t.mu.Unlock()
}

// insertAtCursor inserts s at the current cursor position. Used by bracketed
// paste and Alt+key insertion.
// pushUndo snapshots the current input state so Ctrl+_ (readline-style undo)
// can restore it. The stack is capped to keep long sessions bounded.
func (t *TUI) pushUndo() {
	t.undoStack = append(t.undoStack, struct {
		buf    string
		cursor int
	}{t.inputBuf, t.cursor})
	if len(t.undoStack) > 200 {
		t.undoStack = t.undoStack[len(t.undoStack)-200:]
	}
}

// undoInput restores the most recent pre-edit snapshot (Ctrl+_ / Ctrl+Shift+-).
func (t *TUI) undoInput() bool {
	if len(t.undoStack) == 0 {
		return false
	}
	last := t.undoStack[len(t.undoStack)-1]
	t.undoStack = t.undoStack[:len(t.undoStack)-1]
	t.setInput(last.buf, last.cursor)
	return true
}

func (t *TUI) insertAtCursor(s string) {
	if s == "" {
		return
	}
	t.mu.Lock()
	t.pushUndo()
	runes := []rune(t.inputBuf)
	if t.cursor >= len(runes) {
		t.inputBuf += s
	} else {
		t.inputBuf = string(runes[:t.cursor]) + s + string(runes[t.cursor:])
	}
	t.cursor += len([]rune(s))
	t.mu.Unlock()
}

// deleteWordBackward removes the word before the cursor (Ctrl+W): it skips any
// trailing spaces, then deletes back to the previous space boundary.
func (t *TUI) deleteWordBackward() {
	t.mu.Lock()
	runes := []rune(t.inputBuf)
	if t.cursor == 0 {
		t.mu.Unlock()
		return
	}
	t.pushUndo()
	i := t.cursor - 1
	for i >= 0 && runes[i] == ' ' {
		i--
	}
	for i >= 0 && runes[i] != ' ' {
		i--
	}
	newCursor := i + 1
	runes = append(runes[:newCursor], runes[t.cursor:]...)
	t.inputBuf = string(runes)
	t.cursor = newCursor
	t.mu.Unlock()
}

// deleteToLineStart removes everything before the cursor (Ctrl+U).
func (t *TUI) deleteToLineStart() {
	t.mu.Lock()
	runes := []rune(t.inputBuf)
	if t.cursor == 0 {
		t.mu.Unlock()
		return
	}
	t.pushUndo()
	t.inputBuf = string(runes[t.cursor:])
	t.cursor = 0
	t.mu.Unlock()
}

// cycleMode rotates the agent mode: auto → plan → agent → yolo → auto
// (Shift+Tab), mirroring the mode switcher in Claude Code. The backend gate
// is switched through the callback so display and enforcement stay in sync.
func (t *TUI) cycleMode() {
	t.mu.Lock()
	switch t.mode {
	case ModePlan:
		t.mode = ModeAgent
	case ModeAgent:
		t.mode = ModeYOLO
	case ModeYOLO:
		t.mode = ModeAuto
	default:
		t.mode = ModePlan
	}
	mode := t.mode
	t.mu.Unlock()
	if t.callback != nil {
		if msg := t.callback.OnSetMode(mode); msg != "" {
			t.notice(msg)
		}
	}
	t.notice("模式: " + t.modeLabel(mode))
	t.scheduleRender()
}

// readPaste reads the body of a bracketed paste (everything up to the
// terminating "ESC[201~"). It is resilient to stray ESC bytes so a paste that
// itself contains escape sequences is not truncated.
func (t *TUI) readPaste(r io.RuneReader) string {
	var buf strings.Builder
	for {
		rr, _, err := r.ReadRune()
		if err != nil {
			return buf.String()
		}
		if rr == 0x1b {
			// Possible terminator: ESC [ 2 0 1 ~
			n1, _, e1 := r.ReadRune()
			if e1 != nil || n1 != '[' {
				if n1 != 0 {
					buf.WriteRune(0x1b)
					buf.WriteRune(n1)
				}
				continue
			}
			n2, _, e2 := r.ReadRune()
			if e2 != nil || n2 != '2' {
				buf.WriteRune(0x1b)
				buf.WriteRune('[')
				if n2 != 0 {
					buf.WriteRune(n2)
				}
				continue
			}
			n3, _, e3 := r.ReadRune()
			if e3 != nil || n3 != '0' {
				buf.WriteRune(0x1b)
				buf.WriteRune('[')
				buf.WriteRune('2')
				if n3 != 0 {
					buf.WriteRune(n3)
				}
				continue
			}
			n4, _, e4 := r.ReadRune()
			if e4 != nil || n4 != '1' {
				buf.WriteRune(0x1b)
				buf.WriteRune('[')
				buf.WriteRune('2')
				buf.WriteRune('0')
				if n4 != 0 {
					buf.WriteRune(n4)
				}
				continue
			}
			n5, _, e5 := r.ReadRune()
			if e5 == nil && n5 == '~' {
				return buf.String() // paste complete
			}
			buf.WriteRune(0x1b)
			buf.WriteRune('[')
			buf.WriteRune('2')
			buf.WriteRune('0')
			buf.WriteRune('1')
			if n5 != 0 {
				buf.WriteRune(n5)
			}
			continue
		}
		buf.WriteRune(rr)
	}
}
