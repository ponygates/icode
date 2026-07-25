package tui

import (
	"bufio"
	"strings"
)

// insertAtCursor inserts s at the current cursor position. Used by bracketed
// paste and Alt+key insertion.
func (t *TUI) insertAtCursor(s string) {
	if s == "" {
		return
	}
	runes := []rune(t.inputBuf)
	if t.cursor >= len(runes) {
		t.inputBuf += s
	} else {
		t.inputBuf = string(runes[:t.cursor]) + s + string(runes[t.cursor:])
	}
	t.cursor += len([]rune(s))
}

// deleteWordBackward removes the word before the cursor (Ctrl+W): it skips any
// trailing spaces, then deletes back to the previous space boundary.
func (t *TUI) deleteWordBackward() {
	runes := []rune(t.inputBuf)
	if t.cursor == 0 {
		return
	}
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
}

// deleteToLineStart removes everything before the cursor (Ctrl+U).
func (t *TUI) deleteToLineStart() {
	runes := []rune(t.inputBuf)
	if t.cursor == 0 {
		return
	}
	t.inputBuf = string(runes[t.cursor:])
	t.cursor = 0
}

// cycleMode rotates the agent mode: auto → plan → agent → yolo → auto
// (Shift+Tab), mirroring the mode switcher in Claude Code.
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
	t.notice("Mode: " + mode)
	t.scheduleRender()
}

// readPaste reads the body of a bracketed paste (everything up to the
// terminating "ESC[201~"). It is resilient to stray ESC bytes so a paste that
// itself contains escape sequences is not truncated.
func (t *TUI) readPaste(br *bufio.Reader) string {
	var buf strings.Builder
	for {
		r, _, err := br.ReadRune()
		if err != nil {
			return buf.String()
		}
		if r == 0x1b {
			// Possible terminator: ESC [ 2 0 1 ~
			n1, _, e1 := br.ReadRune()
			if e1 != nil || n1 != '[' {
				if n1 != 0 {
					buf.WriteRune(0x1b)
					buf.WriteRune(n1)
				}
				continue
			}
			n2, _, e2 := br.ReadRune()
			if e2 != nil || n2 != '2' {
				buf.WriteRune(0x1b)
				buf.WriteRune('[')
				if n2 != 0 {
					buf.WriteRune(n2)
				}
				continue
			}
			n3, _, e3 := br.ReadRune()
			if e3 != nil || n3 != '0' {
				buf.WriteRune(0x1b)
				buf.WriteRune('[')
				buf.WriteRune('2')
				if n3 != 0 {
					buf.WriteRune(n3)
				}
				continue
			}
			n4, _, e4 := br.ReadRune()
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
			n5, _, e5 := br.ReadRune()
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
		buf.WriteRune(r)
	}
}
