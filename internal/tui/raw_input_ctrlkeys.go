package tui

import (
	"fmt"
	"strings"
)

// handleControlKeys dispatches the bound control runes (Esc and Enter are routed
// earlier, in handleKey). claimed=false means "r is not a bound control key" and
// the caller falls through to the printable tail; the keepGoing value is then
// meaningless. The three groups below have disjoint rune sets, so trying them in
// sequence is equivalent to the single switch this replaced.
func (t *TUI) handleControlKeys(r rune) (keepGoing, claimed bool) {
	if kg, ok := t.handleControlSessionKeys(r); ok {
		return kg, true
	}
	if kg, ok := t.handleControlEditKeys(r); ok {
		return kg, true
	}
	return t.handleControlNavKeys(r)
}

// handleControlSessionKeys covers the run-loop keys: interrupt/exit, redraw,
// transcript detail, backgrounding, prompt stash, external editor, clipboard copy
// and the settings shortcut.
func (t *TUI) handleControlSessionKeys(r rune) (keepGoing, claimed bool) {
	switch r {
	case 0x03: // Ctrl+C
		return t.handleCtrlC()
	case 0x04: // Ctrl+D
		if t.inputBuf == "" && !t.streaming {
			t.running = false
			return false, true
		}
		return true, true
	case 0x0c: // Ctrl+L — clear & redraw
		fmt.Fprint(t.writer, "\x1b[2J\x1b[H")
		return true, true
	case 0x0f: // Ctrl+O — dismiss welcome, else toggle transcript detail
		if t.welcomeVisible {
			t.dismissWelcome()
			return true, true
		}
		return t.toggleTranscript(), true
	case 0x02: // Ctrl+B — move the running turn to a background task
		return t.backgroundCurrentTurn(), true
	case 0x13: // Ctrl+S — stash / restore the current prompt (Claude Code)
		return t.stashPrompt(), true
	case 0x07: // Ctrl+G — open the current prompt in $EDITOR (Claude Code)
		return t.editInExternalEditor(), true
	case 0x19: // Ctrl+Y — copy last assistant reply to clipboard
		t.copyLastReply()
		return true, true
	case 0x2c: // Ctrl+, — open settings panel (same as Ctrl+P)
		t.openSettings()
		return true, true
	}
	return false, false
}

// handleCtrlC is Ctrl+C: interrupt a running turn, clear a typed draft, or — on an
// empty line — ask for a second Ctrl+C before actually quitting.
func (t *TUI) handleCtrlC() (keepGoing, claimed bool) {
	if t.streaming {
		if t.callback != nil {
			t.callback.OnInterrupt()
		}
		return true, true
	}
	if t.inputBuf == "" {
		// First Ctrl+C on an empty line confirms exit (Claude Code parity)
		// so a stray keypress never accidentally quits with an unsaved
		// conversation. A second Ctrl+C (or Ctrl+D) exits immediately.
		fmt.Fprint(t.writer, "\r\n")
		if t.callback != nil {
			t.callback.OnSlashCommand("/summarize", nil)
		}
		t.add(RoleSystem, "按任意键退出（再次 Ctrl+C 直接退出）")
		t.render()
		// Wait for any key via the key pump (the sole reader of stdin),
		// or for the pump to exit on EOF.
		select {
		case <-t.keyCh:
		case <-t.keyReaderDone:
		}
		t.running = false
		t.add(RoleSystem, "再见！👋")
		return false, true
	}
	t.setInput("", 0)
	return true, true
}

// handleControlEditKeys covers the readline-style editing keys on the input line.
func (t *TUI) handleControlEditKeys(r rune) (keepGoing, claimed bool) {
	switch r {
	case 0x0b: // Ctrl+K — clear input buffer
		t.setInput("", 0)
		return true, true
	case 0x01: // Ctrl+A — start of current logical line (readline per-line)
		li, _ := inputCursorPos(t.inputBuf, t.cursor)
		t.moveCursor(inputAbsCursor(t.inputBuf, li, 0))
		return true, true
	case 0x05: // Ctrl+E — end of current logical line
		li, _ := inputCursorPos(t.inputBuf, t.cursor)
		lines := strings.Split(t.inputBuf, "\n")
		endCol := len([]rune(lines[li]))
		t.moveCursor(inputAbsCursor(t.inputBuf, li, endCol))
		return true, true
	case 0x0a: // Ctrl+J — insert a newline (multi-line input, any terminal)
		t.insertNewlineAtCursor()
		return true, true
	case 0x1f: // Ctrl+_ (also Ctrl+Shift+-) — undo the last input edit
		if !t.undoInput() {
			t.notice("没有可撤销的输入编辑")
		} else {
			t.notice("已撤销上一步输入编辑")
		}
		t.updateSuggestions()
		return true, true
	case 0x17: // Ctrl+W — delete word backward
		t.deleteWordBackward()
		t.updateSuggestions()
		return true, true
	case 0x15: // Ctrl+U — delete to line start
		t.deleteToLineStart()
		t.updateSuggestions()
		return true, true
	case 0x1a: // Ctrl+Z — reject all staged edits in the review overlay
		if t.diffBoxOpen {
			t.rejectStagedEdits()
			t.closeDiffBox()
			return true, true
		}
		return true, true
	case 0x7f, 0x08: // Backspace / DEL
		t.deleteAtCursor()
		t.updateSuggestions()
		return true, true
	}
	return false, false
}

// insertNewlineAtCursor splices a newline in at the caret, remembering the edit so
// Ctrl+_ can undo it.
func (t *TUI) insertNewlineAtCursor() {
	t.mu.Lock()
	t.pushUndo()
	runes := []rune(t.inputBuf)
	if t.cursor > len(runes) {
		t.cursor = len(runes)
	}
	t.inputBuf = string(runes[:t.cursor]) + "\n" + string(runes[t.cursor:])
	t.cursor++
	t.mu.Unlock()
}

// handleControlNavKeys covers history recall and the autocomplete menu, which
// share Ctrl+P / Ctrl+N / Tab.
func (t *TUI) handleControlNavKeys(r rune) (keepGoing, claimed bool) {
	switch r {
	case 0x10: // Ctrl+P — history prev OR move suggestion cursor up
		// (readline convention; settings live on Ctrl+,)
		if t.acOpen && len(t.acItems) > 0 {
			if t.acIdx > 0 {
				t.acIdx--
			}
			return true, true
		}
		t.historyPrev()
		return true, true
	case 0x0e: // Ctrl+N — history next OR move suggestion cursor down
		if t.acOpen && len(t.acItems) > 0 {
			if t.acIdx < len(t.acItems)-1 {
				t.acIdx++
			}
			return true, true
		}
		t.historyNext()
		return true, true
	case 0x12: // Ctrl+R — reverse history search (Claude Code style)
		t.startSearch()
		return true, true
	case 0x09: // Tab — accept suggestion OR cycle model
		if t.acOpen && len(t.acItems) > 0 {
			t.acceptSuggestion()
			return true, true
		}
		// No menu open — cycle the permission mode (plan/agent/yolo/auto);
		// model switching lives in /model, the picker and Alt+P.
		if !t.streaming {
			t.cycleMode()
		}
		return true, true
	}
	return false, false
}
