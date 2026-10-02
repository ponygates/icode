package tui

import (
	"fmt"
	"strings"
	"time"
)

// deleteUnderCursor removes the rune AT the cursor (forward delete — the
// Delete key, as opposed to Backspace which removes the one before it).
func (t *TUI) deleteUnderCursor() {
	t.mu.Lock()
	runes := []rune(t.inputBuf)
	if t.cursor >= len(runes) {
		t.mu.Unlock()
		return
	}
	t.pushUndo()
	t.inputBuf = string(runes[:t.cursor]) + string(runes[t.cursor+1:])
	t.mu.Unlock()
}

func (t *TUI) deleteAtCursor() {
	t.mu.Lock()
	runes := []rune(t.inputBuf)
	if t.cursor == 0 || len(runes) == 0 {
		t.mu.Unlock()
		return
	}
	t.pushUndo()
	runes = append(runes[:t.cursor-1], runes[t.cursor:]...)
	t.inputBuf = string(runes)
	t.cursor--
	t.mu.Unlock()
}

// handleVimKey implements the vi-style normal-mode key bindings. A compact but
// real subset: hjkl / 0 / $ motion, i a I A to insert, x delete-char, dd
// delete-line, u undo, Esc back to insert.
func (t *TUI) handleVimKey(r rune) bool {
	t.mu.Lock()
	switch r {
	case 'h':
		if t.cursor > 0 {
			t.cursor--
		}
	case 'l':
		if t.cursor < len([]rune(t.inputBuf)) {
			t.cursor++
		}
	case '0':
		t.cursor = 0
	case '$':
		t.cursor = len([]rune(t.inputBuf))
	case 'i':
		t.vimInsert = true
	case 'a':
		if t.cursor < len([]rune(t.inputBuf)) {
			t.cursor++
		}
		t.vimInsert = true
	case 'I':
		t.cursor = 0
		t.vimInsert = true
	case 'A':
		t.cursor = len([]rune(t.inputBuf))
		t.vimInsert = true
	case 'x':
		t.saveVimUndo()
		runes := []rune(t.inputBuf)
		if t.cursor < len(runes) {
			runes = append(runes[:t.cursor], runes[t.cursor+1:]...)
			t.inputBuf = string(runes)
		}
	case 'd':
		// dd — delete the whole line (vi operator simplified to `d` = clear).
		t.saveVimUndo()
		t.inputBuf = ""
		t.cursor = 0
	case 'u':
		t.restoreVimUndoLocked()
	default:
		// swallow unknown normal-mode keys so they never leak into the buffer
	}
	t.mu.Unlock()
	t.updateSuggestions()
	return true
}

// saveVimUndo snapshots the buffer before a destructive normal-mode edit so `u`
// can restore it. Multiple edits keep the most recent snapshot.
func (t *TUI) saveVimUndo() {
	t.vimUndo = t.inputBuf
	t.vimUndoValid = true
}

// restoreVimUndo applies the snapshot recorded by the last destructive edit.
func (t *TUI) restoreVimUndo() {
	if !t.vimUndoValid {
		return
	}
	t.setInput(t.vimUndo, len([]rune(t.vimUndo)))
	t.vimUndoValid = false
}

// restoreVimUndoLocked is restoreVimUndo for callers already holding t.mu
// (handleVimKey wraps its whole switch in one critical section).
func (t *TUI) restoreVimUndoLocked() {
	if !t.vimUndoValid {
		return
	}
	t.inputBuf = t.vimUndo
	t.cursor = len([]rune(t.inputBuf))
	t.vimUndoValid = false
}

func (t *TUI) historyPrev() {
	if len(t.history) == 0 {
		return
	}
	if t.histIdx == -1 {
		t.histIdx = len(t.history) - 1
	} else if t.histIdx > 0 {
		t.histIdx--
	}
	buf := t.history[t.histIdx]
	t.setInput(buf, len([]rune(buf)))
}

func (t *TUI) historyNext() {
	if len(t.history) == 0 || t.histIdx == -1 {
		return
	}
	if t.histIdx < len(t.history)-1 {
		t.histIdx++
		buf := t.history[t.histIdx]
		t.setInput(buf, len([]rune(buf)))
	} else {
		t.histIdx = -1
		t.setInput("", 0)
	}
}

func (t *TUI) pushHistory(s string) {
	if len(t.history) == 0 || t.history[len(t.history)-1] != s {
		t.history = append(t.history, s)
	}
	t.histIdx = -1
	// Persist on every add: loadInputHistory runs at startup, so without this
	// the ↑-recall across sessions the file format promises never worked.
	saveInputHistory(t.history)
}

func (t *TUI) submit(text string) {
	// Pending plan confirmation: Enter accepts the plan and starts execution
	// instead of sending whatever is in the input box (Claude Code plan mode).
	t.mu.Lock()
	planPending := t.planPending
	t.mu.Unlock()
	if planPending {
		t.SetPlanPending(false)
		t.notice("计划已确认，开始执行")
		if t.callback != nil {
			t.callback.OnPlanConfirm()
		}
		return
	}

	// Shell mode ("! cmd", Claude Code parity). Raw (full-screen) mode uses the
	// richer runner: Ctrl+C aborts the process, long output is truncated, and
	// "command + output" is handed to the agent as a user turn so it responds.
	// Line mode (non-TTY) keeps the simple synchronous runner.
	if strings.HasPrefix(text, "!") {
		cmdStr := strings.TrimSpace(strings.TrimPrefix(text, "!"))
		if cmdStr != "" {
			t.pushHistory(text)
			if t.rawMode {
				t.runShellMode(cmdStr)
			} else {
				t.execShell(cmdStr)
			}
		}
		return
	}
	// Quick memory append (# prefix) — matches Claude Code's `#` shortcut.
	// The text after the `#` is written to the user memory file (~/.icode/
	// CLAUDE.md) and NOT sent to the LLM. This lets users capture a
	// preference in-line without leaving the chat.
	if strings.HasPrefix(text, "#") {
		t.appendMemory(strings.TrimSpace(text[1:]))
		return
	}
	// Slash command
	if strings.HasPrefix(text, "/") {
		t.handleSlash(text)
		return
	}

	// A turn is running in the background (Ctrl+B): don't block the UI on it,
	// queue the message and let it auto-send on completion.
	t.mu.Lock()
	bged := t.backgrounded && t.streaming
	t.mu.Unlock()
	if bged {
		t.mu.Lock()
		t.queue = append(t.queue, text)
		t.mu.Unlock()
		t.notice("已排队（后台任务运行中，完成后自动发送）")
		return
	}

	// User message — first unfold any pasted blocks, then expand @file
	// references; images (@photo.png or Ctrl+V pastes) are lifted into inline
	// multimodal attachments here.
	text = t.expandPasteBlocks(text)
	expanded, atts := t.expandFileRefs(text)
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleUser, Content: expanded})
	t.scrollOffset = 0 // auto-follow on new turn
	t.mu.Unlock()

	// Fresh session → auto-title it from the first message (Claude Code
	// parity). Silently skipped for resumed sessions and manual /rename.
	t.autoTitle(text)

	if len(atts) > 0 {
		t.notice(fmt.Sprintf("📎 已附带 %d 张图片发送给模型", len(atts)))
	}

	if t.callback != nil {
		t.mu.Lock()
		t.streaming = true
		t.streamBuf.Reset()
		t.turnStart = time.Now()
		t.mu.Unlock()
		t.setTermTitle("⏳")
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.add(RoleError, fmt.Sprintf("内部错误: %v", r))
				}
			}()
			t.callback.OnSend(expanded, atts)
		}()
		t.ensureAnim()
		t.drainStream()
	}
}

// handleLoneEsc implements the double-Esc shortcut (Claude Code parity):
//   - within 600ms with a draft: clear the draft, keep it in history (↑ recalls)
//   - within 600ms with an empty box: arm rewind; a second double-Esc rolls
//     back the last tool call (with the existing diff preview + confirm text)
func (t *TUI) handleLoneEsc() {
	now := time.Now()
	prev := t.lastEscAt
	t.lastEscAt = now
	if prev.IsZero() || now.Sub(prev) > 600*time.Millisecond {
		return // first Esc of a potential pair
	}

	t.mu.Lock()
	draft := t.inputBuf
	t.mu.Unlock()

	if draft != "" {
		// Draft → history so ↑ can recall it, exactly like Claude Code.
		t.pushHistory(draft)
		t.mu.Lock()
		t.inputBuf = ""
		t.cursor = 0
		t.mu.Unlock()
		t.add(RoleSystem, "🗑 草稿已清空并存入历史（按 ↑ 召回）")
		t.render()
		return
	}

	if !t.rewindArmed {
		t.rewindArmed = true
		t.add(RoleSystem, "⏪ 再按一次双 Esc 将回滚最近 1 步工具调用（/rewind N 可指定步数）")
		t.render()
		return
	}
	t.rewindArmed = false
	t.lastEscAt = time.Time{}
	t.notice("⏪ 回溯最近 1 步…")
	t.handleSlash("/rewind")
}
