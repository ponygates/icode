package tui

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/executil"
	"golang.org/x/term"
)

// ── Raw mode (full screen) ───────────────────────────────────────

// escFollowTimeout is how long handleKey waits after a lone ESC for a possible
// follow-up byte (arrow keys, Alt+key, CSI) before treating it as a standalone
// Esc. Terminals write an escape sequence as one burst, so the follow-up byte
// lands on keyCh within microseconds; 10ms keeps lone-Esc latency imperceptible
// while never mistaking a real sequence for a lone Esc.
const escFollowTimeout = 10 * time.Millisecond

// keyRuneReader adapts the key-pump channel to io.RuneReader so the shared
// escape-sequence parsers (handleMouse / readPaste) can read follow-up bytes
// from the same single-owner stream instead of grabbing t.reader directly.
type keyRuneReader struct{ ch <-chan rune }

func (k keyRuneReader) ReadRune() (rune, int, error) {
	r, ok := <-k.ch
	if !ok {
		return 0, 0, io.EOF
	}
	return r, 1, nil
}

// keyPump is the ONLY goroutine that reads from t.reader while the raw-mode
// TUI is running. It forwards every rune to keyCh (normal keys) or permKeyCh
// (decision keys while a permission prompt is pending). Because all consumers
// — the main loop, drainStream and PromptPermission — read from these
// channels, no two goroutines ever touch the terminal reader concurrently:
// keys cannot be stolen and bufio.Reader stays race-free.
func (t *TUI) keyPump() {
	br, ok := t.reader.(*bufio.Reader)
	if !ok {
		close(t.keyCh)
		close(t.keyReaderDone)
		return
	}
	defer close(t.keyReaderDone)
	defer close(t.keyCh)
	acc := make([]byte, 0, 4)
	for {
		b, err := br.ReadByte()
		if err != nil {
			return // EOF or read error — session input is gone
		}

		// ── ConHost extended keys ────────────────────────────────────────
		// Windows consoles deliver arrows/Home/End/Delete/Insert/PgUp/PgDn
		// as a 0xE0 (or legacy 0x00) prefix byte + a scan-code letter. The
		// prefix is INVALID UTF-8 — a rune-based reader misreads it and the
		// scan-code letter then leaks into the input as garbage ("S" for
		// Delete, "G" for Home, …). Translate the pair into the VT sequence
		// the key parser expects. A 0xE0 followed by a UTF-8 continuation
		// byte (0x80-0xBF) is a legit multibyte character lead, not a prefix.
		if b == 0x00 || b == 0xE0 {
			nb, nerr := br.ReadByte()
			if nerr != nil {
				return
			}
			if b == 0xE0 && nb >= 0x80 && nb <= 0xBF {
				acc = append(acc[:0], b, nb)
				continue
			}
			var seq string
			switch nb {
			case 'H':
				seq = "[A" // ↑
			case 'P':
				seq = "[B" // ↓
			case 'K':
				seq = "[D" // ←
			case 'M':
				seq = "[C" // →
			case 'G':
				seq = "[H" // Home
			case 'O':
				seq = "[F" // End
			case 'S':
				seq = "[3~" // Delete
			case 'R':
				seq = "[2~" // Insert
			case 'I':
				seq = "[5~" // PgUp
			case 'Q':
				seq = "[6~" // PgDn
			default:
				continue // unknown extended key — drop silently
			}
			for _, c := range seq {
				select {
				case t.keyCh <- c:
				case <-t.keyStop:
					return
				}
			}
			continue
		}

		// ── Incremental UTF-8 decoding ───────────────────────────────────
		// Byte-level accumulation handles IME commit bursts that can split a
		// multibyte character across reads — a rune-based reader misreads
		// such splits and half characters surfaced as "¿" garbage. ASCII
		// fast-paths through with zero overhead.
		if b < 0x80 {
			acc = acc[:0]
			r := rune(b)
			t.mu.Lock()
			pending := t.permPending
			t.mu.Unlock()
			if pending {
				select {
				case t.permKeyCh <- r:
				case <-t.keyStop:
					return
				}
			} else {
				select {
				case t.keyCh <- r:
				case <-t.keyStop:
					return
				}
			}
			continue
		}

		acc = append(acc, b)
		need := 1
		switch {
		case acc[0] >= 0xF0:
			need = 4
		case acc[0] >= 0xE0:
			need = 3
		case acc[0] >= 0xC0:
			need = 2
		}
		if len(acc) < need {
			continue // wait for the rest of the character
		}
		r, size := utf8.DecodeRune(acc)
		if r == utf8.RuneError || size != need {
			// Invalid sequence — drop it entirely instead of leaking
			// replacement characters into the input.
			acc = acc[:0]
			continue
		}
		acc = acc[:0]

		t.mu.Lock()
		pending := t.permPending
		t.mu.Unlock()
		if pending {
			select {
			case t.permKeyCh <- r:
			case <-t.keyStop:
				return
			}
		} else {
			select {
			case t.keyCh <- r:
			case <-t.keyStop:
				return
			}
		}
	}
}
func (t *TUI) nextKey() (rune, bool) {
	r, ok := <-t.keyCh
	return r, ok
}

func (t *TUI) runRaw() error {
	t.writer = os.Stdout
	// Enter the alternate screen buffer, clear it, and home the cursor before
	// the first paint. This is essential: without it, anything printed before
	// the TUI started (startup logs, the CLI banner) remains in the scrollback,
	// and on consoles that address rows relative to the screen *buffer* rather
	// than the visible window (notably legacy Windows conhost) the absolute
	// row moves in render() land above the viewport — so the header and the top
	// of the ASCII logo get painted off-screen ("top half of the banner
	// missing"). The alternate screen gives us a clean, window-sized canvas
	// where row 1 is always the top of what the user sees. We restore the
	// original screen (and cursor) on exit.
	// Enter the alternate screen buffer and *force the visible window to the
	// top of a clean canvas* before the first paint. This is critical: a
	// centred banner computed for a height larger than the real visible window
	// (or any pre-TUI output still in the scrollback) would leave the top rows
	// of the ASCII logo painted above the viewport — the "top half of the
	// banner missing" symptom.
	//
	// We combine three measures so it works whether or not the terminal honours
	// the alternate screen:
	//   • \x1b[?1049h  enter alt screen (clean, window-sized canvas)
	//   • \x1b[3J       erase the scrollback history (xterm) so the window
	//                  can never stay scrolled down to old content
	//   • \x1b[2J\x1b[H clear the screen and home the cursor to (1,1)
	//   • \x1b[?25l     hide the cursor while painting
	// On terminals that ignore ?1049h the 3J/2J/H trio still scroll the window
	// to the top and clear it, so the banner's top is always visible.
	fmt.Fprint(t.writer, "\x1b[?1049h\x1b[3J\x1b[2J\x1b[H\x1b[?25l")
	defer fmt.Fprint(t.writer, "\x1b[?25h\x1b[?1049l")

	// Turn on SGR mouse tracking + bracketed paste while the full-screen TUI
	// is active so clicks/drags/wheel/large pastes work; restore on exit.
	t.enableMouse()
	defer t.disableMouse()

	// Attempt to resize the terminal window to a comfortable size for the TUI.
	// Uses ANSI escape \x1b[8;H;Wt supported by Windows Terminal, xterm, etc.
	// Does nothing (silently fails) on terminals that don't support it.
	t.resizeTerminal()

	// Immediately re-measure the terminal size before the first render.
	// The measurement in Run() can be stale on Windows where GetConsoleScreen-
	// BufferInfo may return cached values from before the alternate-screen
	// switch. Using termSize() (stdin+stdout) gives the most reliable result.
	// This also captures the result of the resizeTerminal() call above.
	if w, h, ok := t.termSize(); ok {
		t.width, t.height = w, h
	}
	// Also record the initial dimensions as last-rendered so the first render
	// doesn't spuriously trigger a full clear.
	t.lastRenderW, t.lastRenderH = t.width, t.height
	t.render()
	go t.watchResize()
	t.reader = bufio.NewReader(t.reader)
	// The key pump is the ONLY goroutine that reads from t.reader while the
	// TUI is live; the main loop below consumes runes from keyCh instead.
	go t.keyPump()
	defer close(t.keyStop)

	for t.running {
		// A single bad keystroke must never crash the whole session. Recover
		// here so a panic in handleKey/render-context is logged to cli.log and
		// the TUI keeps running instead of "flash closing" (闪退).
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprint(t.writer, "\x1b[?25h")
					writeCliLog(fmt.Sprintf("[tui] raw loop panic: %v\n%s", r, debug.Stack()))
				}
			}()
			t.render()
			// A backgrounded (Ctrl+B) turn finished: flush the queued message
			// here on the main loop — safe, unlike calling submit() from the
			// engine goroutine that produced the completion.
			t.mu.Lock()
			flush := t.pendingQueueFlush && !t.streaming
			t.mu.Unlock()
			if flush {
				t.mu.Lock()
				t.pendingQueueFlush = false
				t.mu.Unlock()
				if next := t.popQueue(); next != "" {
					t.notice("发送排队消息")
					t.submit(next)
					return // submit() already drained the stream; next iteration
				}
			}
			select {
			case rr, ok := <-t.keyCh:
				if !ok {
					// Key pump exited (stdin EOF) — end the session.
					t.running = false
					return
				}
				t.mu.Lock()
				t.lastActivity = time.Now()
				t.mu.Unlock()
				if !t.handleKey(rr) {
					t.running = false
				}
			case <-time.After(1 * time.Second):
				// Idle tick: lets the "auto-recap after 3 min away" feature
				// fire even when no key is pressed (the pump would otherwise
				// block forever on keyCh). render() at the top of the loop
				// repaints so any auto-recap message shows up immediately.
				t.checkAutoRecap()
			}
		}()
	}
	return nil
}

// watchResize polls the terminal size and reapplies it whenever the window
// changes. We poll (instead of relying on SIGWINCH) because SIGWINCH is not
// available on Windows, where a large share of users run iCode. The 150ms
// cadence is imperceptible and only triggers a repaint on an actual change, so
// it costs nothing while the size is stable.
func (t *TUI) watchResize() {
	defer func() {
		if r := recover(); r != nil {
			writeCliLog(fmt.Sprintf("[tui] watchResize panic: %v\n%s", r, debug.Stack()))
		}
	}()
	lastW, lastH := 0, 0
	t.mu.Lock()
	lastW, lastH = t.width, t.height
	t.mu.Unlock()
	for t.running {
		time.Sleep(150 * time.Millisecond)
		if w, h, ok := t.termSize(); ok {
			t.mu.Lock()
			changed := w != lastW || h != lastH
			if changed {
				lastW, lastH = w, h
				t.width, t.height = w, h
			}
			t.mu.Unlock()
			if changed && t.rawMode {
				// Clear the whole screen once on a size change so a shrink can
				// never leave orphaned rows from the taller previous frame,
				// then repaint from a clean canvas at the new dimensions.
				t.renderMu.Lock()
				fmt.Fprint(t.writer, "\x1b[2J\x1b[H")
				t.renderMu.Unlock()
				t.render()
			}
		}
	}
}

// dismissWelcome hides the startup banner if it is currently showing and the
// input line is empty (so an in-progress command is never discarded). It
// returns true when it closed the banner.
func (t *TUI) dismissWelcome() bool {
	t.mu.Lock()
	visible := t.welcomeVisible && t.inputBuf == ""
	if visible {
		t.welcomeVisible = false
	}
	t.mu.Unlock()
	if visible {
		t.render()
	}
	return visible
}

// handleKey processes a single input rune in raw mode.
// Returns false to signal the loop should exit.
func (t *TUI) handleKey(r rune) bool {
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
		return true
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
		return true
	}
	// Settings panel keyboard navigation
	if t.settingsOpen {
		return t.handleSettingsKey(r)
	}
	// While the reverse-history-search overlay (Ctrl+R) is active, every key
	// is routed to the search handler — mirroring Claude Code's isearch.
	if t.searchMode {
		return t.handleSearchKey(r)
	}
	// Any key other than Esc disarms an armed double-Esc rewind so the
	// rollback shortcut can never fire by accident later.
	if r != 0x1b {
		t.rewindArmed = false
	}
	switch r {
	case 0x03: // Ctrl+C
		if t.streaming {
			if t.callback != nil {
				t.callback.OnInterrupt()
			}
			return true
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
			return false
		}
		t.inputBuf = ""
		t.cursor = 0
		return true
	case 0x04: // Ctrl+D
		if t.inputBuf == "" && !t.streaming {
			t.running = false
			return false
		}
		return true
	case 0x0c: // Ctrl+L — clear & redraw
		fmt.Fprint(t.writer, "\x1b[2J\x1b[H")
		return true
	case 0x0b: // Ctrl+K — clear input buffer
		t.inputBuf = ""
		t.cursor = 0
		return true
	case 0x0f: // Ctrl+O — dismiss welcome, else toggle transcript detail
		if t.welcomeVisible {
			t.dismissWelcome()
			return true
		}
		return t.toggleTranscript()
	case 0x01: // Ctrl+A — start of current logical line (readline per-line)
		li, _ := inputCursorPos(t.inputBuf, t.cursor)
		t.cursor = inputAbsCursor(t.inputBuf, li, 0)
		return true
	case 0x05: // Ctrl+E — end of current logical line
		li, _ := inputCursorPos(t.inputBuf, t.cursor)
		lines := strings.Split(t.inputBuf, "\n")
		endCol := len([]rune(lines[li]))
		t.cursor = inputAbsCursor(t.inputBuf, li, endCol)
		return true
	case 0x0a: // Ctrl+J — insert a newline (multi-line input, any terminal)
		t.pushUndo()
		runes := []rune(t.inputBuf)
		if t.cursor > len(runes) {
			t.cursor = len(runes)
		}
		t.inputBuf = string(runes[:t.cursor]) + "\n" + string(runes[t.cursor:])
		t.cursor++
		return true
	case 0x13: // Ctrl+S — stash / restore the current prompt (Claude Code)
		return t.stashPrompt()
	case 0x07: // Ctrl+G — open the current prompt in $EDITOR (Claude Code)
		return t.editInExternalEditor()
	case 0x1f: // Ctrl+_ (also Ctrl+Shift+-) — undo the last input edit
		if !t.undoInput() {
			t.notice("没有可撤销的输入编辑")
		} else {
			t.notice("已撤销上一步输入编辑")
		}
		t.updateSuggestions()
		return true
	case 0x02: // Ctrl+B — move the running turn to a background task
		return t.backgroundCurrentTurn()
	case 0x17: // Ctrl+W — delete word backward
		t.deleteWordBackward()
		t.updateSuggestions()
		return true
	case 0x15: // Ctrl+U — delete to line start
		t.deleteToLineStart()
		t.updateSuggestions()
		return true
	case 0x10: // Ctrl+P — history prev OR move suggestion cursor up
		// (readline convention; settings live on Ctrl+,)
		if t.acOpen && len(t.acItems) > 0 {
			if t.acIdx > 0 {
				t.acIdx--
			}
			return true
		}
		t.historyPrev()
		return true
	case 0x0e: // Ctrl+N — history next OR move suggestion cursor down
		if t.acOpen && len(t.acItems) > 0 {
			if t.acIdx < len(t.acItems)-1 {
				t.acIdx++
			}
			return true
		}
		t.historyNext()
		return true
	case 0x12: // Ctrl+R — reverse history search (Claude Code style)
		t.startSearch()
		return true
	case 0x19: // Ctrl+Y — copy last assistant reply to clipboard
		t.copyLastReply()
		return true
	case 0x09: // Tab — accept suggestion OR cycle model
		if t.acOpen && len(t.acItems) > 0 {
			t.acceptSuggestion()
			return true
		}
		// No menu open — cycle the permission mode (plan/agent/yolo/auto);
		// model switching lives in /model, the picker and Alt+P.
		if !t.streaming {
			t.cycleMode()
		}
		return true
	case 0x2c: // Ctrl+, — open settings panel (same as Ctrl+P)
		t.openSettings()
		return true
	case 0x1b:
		// Escape sequences: arrow keys, mouse reports, bracketed paste, and
		// the Shift+Tab mode cycle. The first byte after ESC decides which.
		// Without buffering these (reading each as a separate rune) stray "["
		// / "A" characters would leak into the input buffer — the classic
		// "garbled text on up/down" bug.
		//
		// Lone Esc (no follow-up byte yet): the terminal delivers a plain ESC
		// press as a single byte, while sequences arrive as one burst. The key
		// pump forwards runes one at a time, so we cannot peek the reader;
		// instead we wait escFollowTimeout for a follow-up byte. A blocking
		// read here would hang forever on a lone Esc — the classic "ESC key
		// does nothing" bug.
		var ur rune
		select {
		case u, ok := <-t.keyCh:
			if !ok {
				return true
			}
			ur = u
		case <-time.After(escFollowTimeout):
			// Lone Esc — interrupt streaming first, then dismiss whatever
			// overlay is open (plan bar, diff box, pickers, autocomplete,
			// help panel, welcome, vim mode).
			if t.streaming {
				if t.callback != nil {
					t.callback.OnInterrupt()
				}
				return true
			}
			if t.acOpen {
				t.acOpen = false
				t.acItems = nil
				return true
			}
			if t.helpVisible {
				t.helpVisible = false
				return true
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
			return true
		}
		// Alt+Enter (or Alt+Return): submit current input.
		if ur == '\r' || ur == '\n' {
			text := strings.TrimSpace(t.inputBuf)
			// Unfold any pasted-block placeholders before history + submit so
			// ↑-recalled history and the model both receive the real content.
			text = t.expandPasteBlocks(text)
			t.inputBuf = ""
			t.cursor = 0
			if text != "" {
				t.pushHistory(text)
				t.submit(text)
			}
			return true
		}
		// Plain Esc (or any non-CSI key) cancels the model /resume pickers.
		if t.modelPickerOpen && ur != '[' {
			t.closeModelPicker()
			return true
		}
		if t.resumePickerOpen && ur != '[' {
			t.closeResumePicker()
			return true
		}
		if ur == '[' || ur == 'O' {
			// CSI (ESC[…) or SS3 (ESC O…) sequence — application-cursor-mode
			// terminals send arrows as SS3; both share the letter alphabet.
			c1, ok := t.nextKey()
			if !ok {
				return true
			}
			switch c1 {
			case 'A': // ↑ multi-line: cursor up a line; first line → history prev
				if t.modelPickerOpen {
					t.movePicker(-1)
					return true
				}
				if t.resumePickerOpen {
					t.moveResumePicker(-1)
					return true
				}
				if t.diffBoxOpen {
					t.moveDiff(-1)
					return true
				}
				if t.acOpen && len(t.acItems) > 0 {
					// Wrap around: up from the first item lands on the last.
					t.acIdx = (t.acIdx - 1 + len(t.acItems)) % len(t.acItems)
					return true
				}
				li, col := inputCursorPos(t.inputBuf, t.cursor)
				if li > 0 {
					t.cursor = inputAbsCursor(t.inputBuf, li-1, col)
					return true
				}
				t.historyPrev()
				return true
			case 'B': // ↓ multi-line: cursor down a line; last line → history next
				if t.modelPickerOpen {
					t.movePicker(1)
					return true
				}
				if t.resumePickerOpen {
					t.moveResumePicker(1)
					return true
				}
				if t.diffBoxOpen {
					t.moveDiff(1)
					return true
				}
				if t.acOpen && len(t.acItems) > 0 {
					// Wrap around: down from the last item lands on the first.
					t.acIdx = (t.acIdx + 1) % len(t.acItems)
					return true
				}
				lines := strings.Split(t.inputBuf, "\n")
				li, col := inputCursorPos(t.inputBuf, t.cursor)
				if li < len(lines)-1 {
					t.cursor = inputAbsCursor(t.inputBuf, li+1, col)
					return true
				}
				t.historyNext()
				return true
			case 'C': // → cursor right
				runes := []rune(t.inputBuf)
				if t.cursor < len(runes) {
					t.cursor++
				}
				return true
			case 'D': // ← cursor left
				if t.cursor > 0 {
					t.cursor--
				}
				return true
			case 'H':
				t.cursor = 0
				return true
			case 'F':
				t.cursor = len([]rune(t.inputBuf))
				return true
			case '5': // PgUp
				t.nextKey() // consume trailing '~'
				t.scrollPgUp()
				return true
			case '6': // PgDn
				t.nextKey() // consume trailing '~'
				t.scrollPgDn()
				return true
			case '3': // Delete — remove the rune under the cursor
				t.nextKey() // consume the trailing '~'
				t.deleteUnderCursor()
				t.updateSuggestions()
				return true
			case 'Z': // Shift+Tab → cycle agent mode
				t.cycleMode()
				return true
			case '<': // SGR mouse report
				t.handleMouse(keyRuneReader{ch: t.keyCh})
				return true
			case 'M': // X10 mouse report — 3 raw coordinate bytes follow
				b1, ok1 := t.nextKey()
				b2, ok2 := t.nextKey()
				b3, ok3 := t.nextKey()
				if !ok1 || !ok2 || !ok3 {
					return true
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
				return true
			case '2':
				// Bracketed paste begins with "200~"; otherwise it's an
				// unknown CSI we consume and ignore.
				c2, ok2 := t.nextKey()
				if ok2 && c2 == '0' {
					c3, ok3 := t.nextKey()
					if ok3 && c3 == '0' {
						c4, ok4 := t.nextKey()
						if ok4 && c4 == '~' {
							pasted := t.readPaste(keyRuneReader{ch: t.keyCh})
							t.dismissWelcome()
							t.insertPasted(pasted)
							t.updateSuggestions()
							return true
						}
					}
				}
				for {
					rr, ok := t.nextKey()
					if !ok || rr == '~' || rr == 'm' || rr == 'M' {
						break
					}
				}
				return true
			default:
				// Unknown CSI — drain the terminator and ignore.
				for {
					rr, ok := t.nextKey()
					if !ok || rr == '~' || rr == 'm' || rr == 'M' {
						break
					}
				}
				return true
			}
		}
		// Alt+P — switch model without clearing the prompt (Claude Code).
		if ur == 'p' || ur == 'P' {
			t.showModelPicker()
			return true
		}
		// Alt+V — image paste alias (Claude Code parity; Ctrl+V also works).
		if ur == 'v' || ur == 'V' {
			t.pasteClipboardImage()
			return true
		}
		// Alt+T — toggle extended thinking in place (Claude Code).
		if ur == 't' || ur == 'T' {
			t.thinkingOn = !t.thinkingOn
			if t.thinkingOn {
				t.handleSlash("/thinking on")
			} else {
				t.handleSlash("/thinking off")
			}
			return true
		}
		// Alt+<key>: treat the key as a printable insertion (e.g. Alt+b).
		if ur >= 0x20 && ur != 0x7f {
			t.dismissWelcome()
			t.insertAtCursor(string(ur))
			t.updateSuggestions()
			return true
		}
		return true
	case '\r':
		// Note: plain \n (Ctrl+J) inserts a newline above — see case 0x0a.
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
		if t.multiline {
			// In multi-line mode, Enter inserts a newline. Submit with Alt+Enter.
			t.inputBuf += "\n"
			t.cursor++
			return true
		}
		text := strings.TrimSpace(t.inputBuf)
		t.inputBuf = ""
		t.cursor = 0
		if text == "" {
			if t.dismissWelcome() {
				return true
			}
			return true
		}
		// Claude Code parity: Enter accepts the HIGHLIGHTED autocomplete row —
		// except when the highlighted item is exactly what was typed (then it
		// just submits, preserving muscle memory for exact commands).
		if t.acOpen && len(t.acItems) > 0 && t.acIdx < len(t.acItems) {
			if item := t.acItems[t.acIdx].Name; item != text {
				t.acceptSuggestion()
				return true
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
		return true
	case 0x1a: // Ctrl+Z — reject all staged edits in the review overlay
		if t.diffBoxOpen {
			t.rejectStagedEdits()
			t.closeDiffBox()
			return true
		}
		return true
	case 0x7f, 0x08: // Backspace / DEL
		t.deleteAtCursor()
		t.updateSuggestions()
		return true
	}

	if r == 0x16 { // Ctrl+V — paste from clipboard (image best-effort)
		t.pasteClipboardImage()
		return true
	}
	if r < 0x20 {
		// Ignore other control characters.
		return true
	}

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

	// Vim normal mode: printable runes are vi commands until the user returns
	// to insert mode (i/a/I/A or Esc). The dispatcher swallows unknown keys.
	if t.vimMode && !t.vimInsert {
		return t.handleVimKey(r)
	}

	// Printable rune (incl. Chinese) — insert at cursor.
	// The first keystroke also clears the welcome banner so typing feels
	// immediate (Claude Code does the same).
	if t.dismissWelcome() {
		// banner dismissed; fall through to insert the rune into a clean prompt
	}
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
	t.updateSuggestions()
	return true
}

// deleteUnderCursor removes the rune AT the cursor (forward delete — the
// Delete key, as opposed to Backspace which removes the one before it).
func (t *TUI) deleteUnderCursor() {
	runes := []rune(t.inputBuf)
	if t.cursor >= len(runes) {
		return
	}
	t.pushUndo()
	t.inputBuf = string(runes[:t.cursor]) + string(runes[t.cursor+1:])
}

func (t *TUI) deleteAtCursor() {
	runes := []rune(t.inputBuf)
	if t.cursor == 0 || len(runes) == 0 {
		return
	}
	t.pushUndo()
	runes = append(runes[:t.cursor-1], runes[t.cursor:]...)
	t.inputBuf = string(runes)
	t.cursor--
}

// handleVimKey implements the vi-style normal-mode key bindings. A compact but
// real subset: hjkl / 0 / $ motion, i a I A to insert, x delete-char, dd
// delete-line, u undo, Esc back to insert.
func (t *TUI) handleVimKey(r rune) bool {
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
		t.restoreVimUndo()
	default:
		// swallow unknown normal-mode keys so they never leak into the buffer
	}
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
	t.inputBuf = t.history[t.histIdx]
	t.cursor = len([]rune(t.inputBuf))
}

func (t *TUI) historyNext() {
	if len(t.history) == 0 || t.histIdx == -1 {
		return
	}
	if t.histIdx < len(t.history)-1 {
		t.histIdx++
		t.inputBuf = t.history[t.histIdx]
	} else {
		t.histIdx = -1
		t.inputBuf = ""
	}
	t.cursor = len([]rune(t.inputBuf))
}

func (t *TUI) pushHistory(s string) {
	if len(t.history) == 0 || t.history[len(t.history)-1] != s {
		t.history = append(t.history, s)
	}
	t.histIdx = -1
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

// toggleTranscript implements Ctrl+O (Claude Code's transcript viewer): expand
// every tool block to show full arguments and output, or fold them all back to
// their one-line summaries. Cheap, reversible, and keeps long sessions
// readable during review.
func (t *TUI) toggleTranscript() bool {
	t.mu.Lock()
	t.transcriptVerbose = !t.transcriptVerbose
	verbose := t.transcriptVerbose
	toolCount := 0
	for i := range t.messages {
		if t.messages[i].Tool != "" {
			t.messages[i].Folded = !verbose
			toolCount++
		}
	}
	t.mu.Unlock()
	if verbose {
		t.notice(fmt.Sprintf("🔍 已展开全部工具详情（%d 条）· Ctrl+O 折叠", toolCount))
	} else {
		t.notice(fmt.Sprintf("已折叠工具详情（%d 条）· Ctrl+O 展开", toolCount))
	}
	t.render()
	return true
}

// stashPrompt implements Ctrl+S (Claude Code parity): with text in the box it
// stashes the prompt and clears the input; with an empty box it restores the
// stashed text back into the input.
func (t *TUI) stashPrompt() bool {
	t.mu.Lock()
	cur := t.inputBuf
	t.mu.Unlock()
	if strings.TrimSpace(cur) != "" {
		t.stashBuf = cur
		t.mu.Lock()
		t.inputBuf = ""
		t.cursor = 0
		t.mu.Unlock()
		t.notice("已暂存提示词（空输入时按 Ctrl+S 恢复）")
		t.render()
		return true
	}
	if t.stashBuf != "" {
		t.mu.Lock()
		t.inputBuf = t.stashBuf
		t.cursor = len([]rune(t.stashBuf))
		t.stashBuf = ""
		t.mu.Unlock()
		t.notice("已恢复暂存的提示词")
		t.render()
		return true
	}
	t.notice("没有暂存的提示词")
	return true
}

// editInExternalEditor implements Ctrl+G (Claude Code parity): hand the
// current prompt to $VISUAL/$EDITOR, then read the result back into the input
// box. Raw mode and the alternate screen are suspended for the duration so the
// editor renders normally.
func (t *TUI) editInExternalEditor() bool {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		t.notice("未设置 $EDITOR / $VISUAL，无法用外部编辑器编辑提示词")
		return true
	}

	f, err := os.CreateTemp("", "icode-prompt-*.md")
	if err != nil {
		t.notice("创建临时文件失败: " + err.Error())
		return true
	}
	path := f.Name()
	t.mu.Lock()
	_, _ = f.WriteString(t.inputBuf)
	t.mu.Unlock()
	f.Close()
	defer os.Remove(path)

	// Split the editor value so `code -w` / `vim -u NONE` style values work.
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	t.suspendRaw()
	runErr := cmd.Run()
	t.resumeRaw()

	if runErr != nil {
		t.notice("编辑器退出异常: " + runErr.Error())
		return true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.notice("读取编辑结果失败: " + err.Error())
		return true
	}
	edited := strings.TrimRight(string(data), "\n")
	t.mu.Lock()
	t.inputBuf = edited
	t.cursor = len([]rune(edited))
	t.mu.Unlock()
	t.notice("已载入编辑器内容（Enter 发送）")
	t.render()
	return true
}

// suspendRaw leaves the alternate screen and restores cooked mode so an
// external program (e.g. $EDITOR) can use the terminal normally.
func (t *TUI) suspendRaw() {
	if !t.rawMode || t.rawState == nil {
		return
	}
	fmt.Fprint(t.writer, "\x1b[?25h\x1b[?1049l")
	term.Restore(int(os.Stdin.Fd()), t.rawState)
}

// resumeRaw re-enters raw mode and the alternate screen after suspendRaw, and
// repaints so the UI is consistent again.
func (t *TUI) resumeRaw() {
	if !t.rawMode {
		return
	}
	fd := int(os.Stdin.Fd())
	if state, err := term.MakeRaw(fd); err == nil {
		t.rawState = state
	}
	fmt.Fprint(t.writer, "\x1b[?1049h\x1b[2J\x1b[H")
	t.render()
}

// backgroundCurrentTurn implements Ctrl+B outside of streaming (nothing is
// running in that case) so the shortcut always gives feedback instead of
// silently doing nothing.
func (t *TUI) backgroundCurrentTurn() bool {
	if !t.streaming {
		t.notice("当前没有正在运行的任务（任务运行中按 Ctrl+B 可转入后台）")
		return true
	}
	// Streaming case is handled inside drainStream (it must break out of the
	// wait loop); reaching here means the turn just ended.
	t.notice("当前任务已接近结束，无需后台化")
	return true
}

// runShellMode implements the "! cmd" shell mode (main-loop goroutine, same
// pattern as drainStream): runs the command asynchronously, waits with
// Ctrl+C abort, prints the captured output, then hands "command + output" to
// the agent as a user turn so it can react (Claude Code parity).
func (t *TUI) runShellMode(cmdStr string) {
	t.add(RoleSystem, "$ "+cmdStr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan string, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Sprintf("（命令执行异常: %v）", r)
			}
		}()
		done <- t.execShellCapture(ctx, cmdStr)
	}()

	var composed string
	for {
		select {
		case composed = <-done:
			// Output captured — show it and continue below.
		case r, ok := <-t.keyCh:
			if !ok {
				composed = <-done
			} else if r == 0x03 { // Ctrl+C — kill the process, keep the transcript note
				cancel()
				t.add(RoleSystem, "⏹ shell 命令已中断")
				return
			} else {
				continue // other keys ignored while the command runs
			}
		}
		break
	}

	out := strings.TrimRight(composed, "\n")
	if out == "" {
		out = "（无输出）"
	}
	if len(out) > 4000 {
		out = out[:4000] + "\n…（输出已截断，完整输出可让 agent 用 bash 查看）"
	}
	t.add(RoleSystem, out)

	// Command + output enter the conversation as a user turn; the agent sees
	// the result and responds — no extra round-trip needed.
	composedMsg := fmt.Sprintf("[! shell] $ %s\n命令输出：\n%s\n\n请根据以上命令输出给出你的响应或下一步。", cmdStr, out)
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleUser, Content: composedMsg})
	t.scrollOffset = 0
	t.streaming = true
	t.streamBuf.Reset()
	t.turnStart = time.Now()
	t.mu.Unlock()
	if t.callback != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.add(RoleError, fmt.Sprintf("内部错误: %v", r))
				}
			}()
			t.callback.OnSend(composedMsg, nil)
		}()
		t.ensureAnim()
		t.drainStream()
	}
}

// execShellCapture runs cmdStr through the platform shell and returns its
// combined output. It honours ctx cancellation (Ctrl+C kills the process).
func (t *TUI) execShellCapture(ctx context.Context, cmdStr string) string {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = executil.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = executil.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	out, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() == nil {
		// Include the exit-status hint; the output itself stays first.
		return string(out) + fmt.Sprintf("\n（退出码: %v）", err)
	}
	return string(out)
}

func (t *TUI) drainStream() {
	// Non-raw (line / piped) mode: there is no key pump and no raw-mode key
	// handling, so just wait for the stream to finish. The old code did a
	// `t.reader.(*bufio.Reader)` assertion here, which panicked on the
	// *os.File reader used by runLine — this branch never touches the reader.
	if !t.rawMode {
		<-t.streamDone
		t.streaming = false
		return
	}
	// Raw mode: while waiting for the stream to finish, keep reading keys so
	// Esc can interrupt. All keys come from the key pump's keyCh — the pump is
	// the ONLY reader of the terminal, and permission-prompt keys are routed
	// to permKeyCh while a prompt is pending, so this loop can never steal the
	// decision keys from PromptPermission.
drainLoop:
	for {
		select {
		case <-t.streamDone:
			// Stream finished; any keys still in keyCh are picked up on the
			// next main-loop iteration. Break out so the queued-message
			// auto-send below can run.
			t.streaming = false
			break drainLoop
		case r, ok := <-t.keyCh:
			if !ok {
				// Key pump exited (stdin EOF) — wait for the stream to end.
				<-t.streamDone
				t.streaming = false
				break drainLoop
			}
			// Process the key while still waiting for the stream.
			// Only a lone Esc and Ctrl+C work during streaming. Escape
			// SEQUENCES (arrow keys = ESC [ A, Home = ESC [ H, ...) start
			// with the same 0x1b byte, so wait escFollowTimeout for a
			// follow-up before treating it as a lone Esc — otherwise arrow
			// presses would interrupt the stream (the "方向键打断生成" bug).
			if r == 0x1b {
				select {
				case u, ok := <-t.keyCh:
					if !ok {
						<-t.streamDone
						t.streaming = false
						return
					}
					// Escape sequence follow-up (arrow/Home/End/...) — not
					// an interrupt. Arrow-up recalls the oldest queued
					// message back into the typing buffer for editing
					// (Claude Code parity); other sequences are swallowed.
					if u == '[' {
						select {
						case c2, ok2 := <-t.keyCh:
							if ok2 && c2 == 'A' {
								t.mu.Lock()
								hasQueue := len(t.queue) > 0
								if hasQueue {
									recalled := t.queue[0]
									t.queue = t.queue[1:]
									if t.inputBuf != "" {
										recalled = t.inputBuf + " " + recalled
									}
									t.inputBuf = recalled
									t.cursor = len([]rune(t.inputBuf))
								}
								t.mu.Unlock()
								if hasQueue {
									t.updateSuggestions()
									t.render()
								}
							}
						case <-time.After(escFollowTimeout):
						}
					}
					_ = u
					continue
				case <-time.After(escFollowTimeout):
					// Lone Esc — interrupt.
					if t.callback != nil {
						t.callback.OnInterrupt()
					}
					// Immediate visual feedback: don't wait for the engine's
					// stream goroutine to unwind — the user must see the key
					// register the instant it's pressed.
					t.add(RoleSystem, "⏹ 正在中断…")
				}
			} else if r == 0x03 { // Ctrl+C
				if t.callback != nil {
					t.callback.OnInterrupt()
				}
				t.add(RoleSystem, "⏹ 正在中断…")
			} else if r == 0x02 { // Ctrl+B — background the running turn
				// The engine keeps working; only the UI stops blocking, so the
				// user can keep typing (messages queue up) while it runs.
				t.mu.Lock()
				t.backgrounded = true
				t.mu.Unlock()
				t.add(RoleSystem, "⏭ 已转入后台运行：可继续输入（消息将排队），完成后提示")
				break drainLoop
			} else {
				// Claude Code-style message queueing: everything the user
				// types while the agent works goes into the queue buffer
				// instead of being dropped. Enter enqueues, ↑ recalls the
				// oldest queued entry back into the buffer for editing.
				t.handleQueueKey(r)
			}
		}
	}
	// Turn finished — auto-send the oldest queued message as the next turn
	// (Claude Code parity: queue drains across turn boundaries). When the turn
	// was backgrounded with Ctrl+B the engine is still running, so the queue
	// stays put and EndStream flushes it on real completion.
	t.mu.Lock()
	bged := t.backgrounded
	t.mu.Unlock()
	if bged {
		return
	}
	if next := t.popQueue(); next != "" {
		t.notice("发送排队消息")
		t.submit(next)
	}
}

// handleQueueKey maintains the streaming-time input buffer + queue.
func (t *TUI) handleQueueKey(r rune) {
	switch r {
	case '\r':
		t.mu.Lock()
		txt := strings.TrimSpace(t.inputBuf)
		if txt != "" {
			t.queue = append(t.queue, txt)
			t.inputBuf = ""
			t.cursor = 0
		}
		t.mu.Unlock()
		t.updateSuggestions()
		t.render()
	case 0x7f, 0x08: // Backspace
		t.deleteAtCursor()
		t.updateSuggestions()
		t.render()
	default:
		if r >= 0x20 && r != 0x7f {
			t.mu.Lock()
			runes := []rune(t.inputBuf)
			if t.cursor > len(runes) {
				t.cursor = len(runes)
			}
			rest := append([]rune{r}, runes[t.cursor:]...)
			runes = append(runes[:t.cursor], rest...)
			t.inputBuf = string(runes)
			t.cursor++
			t.mu.Unlock()
			t.updateSuggestions()
			t.render()
		}
	}
}

// popQueue removes and returns the oldest queued message, if any.
func (t *TUI) popQueue() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.queue) == 0 {
		return ""
	}
	next := t.queue[0]
	t.queue = t.queue[1:]
	return next
}

// pasteClipboardImage implements Ctrl+V (Claude Code parity): when the system
// clipboard holds an image, save it to a temp PNG and insert an @path reference
// into the input. On send the path is read, base64-encoded, and inlined as a
// true multimodal attachment (see consumePendingImages), so vision-capable
// models can actually see the picture. Falls back gracefully when the clipboard
// has no image or the platform's clipboard tool is unavailable (text paste is
// already handled by the terminal's bracketed-paste mode, so we never
// double-paste text). The downloaded image is left in the OS temp dir.
func (t *TUI) pasteClipboardImage() {
	var path string
	switch runtime.GOOS {
	case "windows":
		ps := `Add-Type -AssemblyName System.Windows.Forms; ` +
			`Add-Type -AssemblyName System.Drawing; ` +
			`if ([System.Windows.Forms.Clipboard]::ContainsImage()) { ` +
			`$img=[System.Windows.Forms.Clipboard]::GetImage(); ` +
			`$p=Join-Path $env:TEMP ('icode-clip-'+[datetime]::Now.ToString('yyyyMMddHHmmssffff')+'.png'); ` +
			`$img.Save($p,[System.Drawing.Imaging.ImageFormat]::Png); $p }`
		out, err := exec.Command("powershell", "-NoProfile", "-Command", ps).Output()
		if err == nil {
			path = strings.TrimSpace(string(out))
		}
	case "darwin":
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("icode-clip-%d.png", time.Now().UnixNano()))
		cmd := exec.Command("bash", "-c",
			fmt.Sprintf("command -v pngpaste >/dev/null 2>&1 && pngpaste %q >/dev/null 2>&1 && echo %q", tmp, tmp))
		if out, err := cmd.Output(); err == nil {
			if p := strings.TrimSpace(string(out)); p == tmp {
				path = p
			}
		}
	default: // linux / other
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("icode-clip-%d.png", time.Now().UnixNano()))
		cmd := exec.Command("bash", "-c",
			fmt.Sprintf("command -v xclip >/dev/null 2>&1 && xclip -selection clipboard -t image/png -o >%q 2>/dev/null && echo %q", tmp, tmp))
		if out, err := cmd.Output(); err == nil {
			if p := strings.TrimSpace(string(out)); p == tmp {
				path = p
			}
		}
	}
	if path == "" {
		t.notice("剪贴板中没有图片（或当前环境无法读取剪贴板图片）")
		return
	}
	t.dismissWelcome()
	t.insertAtCursor("@" + path + " ")
	t.updateSuggestions()
	t.notice("📎 已粘贴剪贴板图片: " + filepath.Base(path) + "（发送时作为多模态附件）")
}

// readImageFile reads an image file and returns its base64 payload plus a MIME
// type, or ok=false when the file is unreadable, too large, or not a supported
// image format.
func readImageFile(path string) (b64, mime string, ok bool) {
	const maxImageBytes = 25 << 20 // 25 MB cap to keep memory / context bounded
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 || len(b) > maxImageBytes {
		return "", "", false
	}
	mime = detectImageMIME(b, path)
	if mime == "" {
		return "", "", false
	}
	return base64.StdEncoding.EncodeToString(b), mime, true
}

// detectImageMIME identifies common image formats by magic bytes, falling back
// to the file extension.
func detectImageMIME(b []byte, path string) string {
	switch {
	case len(b) >= 8 && b[0] == 0x89 && string(b[1:4]) == "PNG":
		return "image/png"
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) >= 6 && (string(b[0:6]) == "GIF89a" || string(b[0:6]) == "GIF87a"):
		return "image/gif"
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return ""
}

// ── Reverse history search (Ctrl+R, Claude Code style) ───────────

// handleSearchKey routes every key while the reverse-search overlay is open.
func (t *TUI) handleSearchKey(r rune) bool {
	switch r {
	case 0x03, 0x07, 0x1b: // Ctrl+C / Ctrl+G / Esc — cancel search
		t.cancelSearch()
		return true
	case 0x0c: // Ctrl+L — clear screen and cancel search
		t.cancelSearch()
		fmt.Fprint(t.writer, "\x1b[2J\x1b[H")
		return true
	case 0x12: // Ctrl+R again — cycle to the next match (bash isearch)
		t.cycleSearch()
		return true
	case '\r', '\n', 0x09: // Enter / Tab — accept the current match into input
		t.acceptSearch()
		return true
	case 0x7f, 0x08: // Backspace / DEL — delete last query char
		if len([]rune(t.searchBuf)) > 0 {
			t.searchBuf = string([]rune(t.searchBuf)[:len([]rune(t.searchBuf))-1])
			t.updateSearchMatches()
			t.searchIdx = 0
		} else {
			t.cancelSearch()
			return true
		}
		t.render()
		return true
	}
	if r < 0x20 {
		return true // ignore other control characters
	}
	// Printable rune — append to the search query and re-filter.
	t.searchBuf += string(r)
	t.updateSearchMatches()
	t.searchIdx = 0
	t.render()
	return true
}

// startSearch opens the reverse-history-search overlay.
func (t *TUI) startSearch() {
	if len(t.history) == 0 {
		return
	}
	t.mu.Lock()
	t.searchMode = true
	t.searchBuf = ""
	t.restoreInput = t.inputBuf
	t.inputBuf = ""
	t.cursor = 0
	t.acOpen = false
	t.acItems = nil
	t.searchIdx = 0
	t.mu.Unlock()
	t.updateSearchMatches()
	t.render()
}

// updateSearchMatches recomputes matches (most-recent-first) filtered by the
// current query. Safe to call from any goroutine that already holds t.mu is
// NOT assumed — it locks internally.
func (t *TUI) updateSearchMatches() {
	t.mu.Lock()
	defer t.mu.Unlock()
	q := strings.ToLower(t.searchBuf)
	// Transcript search (Claude Code Ctrl+R parity): match the conversation
	// log — user AND assistant messages — newest first, then fall back to the
	// typed-input history. Dedup keeps repeated prompts from stacking.
	matches := make([]string, 0, len(t.messages)+len(t.history))
	seen := map[string]bool{}
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		matches = append(matches, s)
	}
	for i := len(t.messages) - 1; i >= 0; i-- {
		m := t.messages[i]
		switch m.Role {
		case RoleUser, RoleAssistant, RoleSystem:
			if q == "" || strings.Contains(strings.ToLower(m.Content), q) {
				add(m.Content)
			}
		}
	}
	for i := len(t.history) - 1; i >= 0; i-- {
		if q == "" || strings.Contains(strings.ToLower(t.history[i]), q) {
			add(t.history[i])
		}
	}
	t.searchMatches = matches
	if t.searchIdx >= len(matches) {
		t.searchIdx = len(matches) - 1
	}
	if t.searchIdx < 0 {
		t.searchIdx = 0
	}
}

// cycleSearch moves to the next match (Ctrl+R pressed again).
func (t *TUI) cycleSearch() {
	t.mu.Lock()
	if len(t.searchMatches) <= 1 {
		t.mu.Unlock()
		return
	}
	t.searchIdx = (t.searchIdx + 1) % len(t.searchMatches)
	t.mu.Unlock()
	t.render()
}

// acceptSearch loads the highlighted match into the input line and closes the
// overlay. The match is NOT submitted — the user can edit or press Enter.
func (t *TUI) acceptSearch() {
	t.mu.Lock()
	var chosen string
	if t.searchIdx >= 0 && t.searchIdx < len(t.searchMatches) {
		chosen = t.searchMatches[t.searchIdx]
	}
	t.searchMode = false
	t.searchBuf = ""
	t.searchMatches = nil
	t.searchIdx = 0
	t.inputBuf = chosen
	t.cursor = len([]rune(chosen))
	t.mu.Unlock()
	t.render()
}

// cancelSearch closes the overlay and restores the input buffer.
func (t *TUI) cancelSearch() {
	t.mu.Lock()
	t.searchMode = false
	t.searchBuf = ""
	t.searchMatches = nil
	t.searchIdx = 0
	t.inputBuf = t.restoreInput
	t.cursor = len([]rune(t.restoreInput))
	t.restoreInput = ""
	t.mu.Unlock()
	t.render()
}

// openSettings opens the settings overlay panel (Ctrl+P / Ctrl+,), refreshing
// its config snapshot from disk once on open so the render loop never hits
// the filesystem per frame. No-op while streaming or already open.
func (t *TUI) openSettings() {
	if t.streaming || t.settingsOpen {
		return
	}
	t.settingsOpen = true
	t.settingsCursor = 0
	t.mu.Lock()
	cfg, err := config.Load()
	if err != nil {
		cfg = config.Default()
	}
	t.settingsCfg = cfg
	t.mu.Unlock()
	t.render()
}

// handleSettingsKey routes keypresses while the settings overlay is active.
// The row bound comes from the same settingsPanelItems list the renderer
// draws, so keyboard navigation and the visible rows can never drift apart.
func (t *TUI) handleSettingsKey(r rune) bool {
	t.mu.Lock()
	cfg := t.settingsCfg
	t.mu.Unlock()
	if cfg == nil {
		cfg = config.Default()
	}
	n := len(t.settingsPanelItems(cfg))
	switch r {
	case 0x1b, 0x03: // Esc or Ctrl+C — close settings
		t.settingsOpen = false
		t.mu.Lock()
		t.settingsCfg = nil
		t.mu.Unlock()
		t.render()
		return true
	case 0x0e, 0x1b5b42: // Ctrl+N or Down arrow
		if t.settingsCursor < n-1 {
			t.settingsCursor++
			t.render()
		}
		return true
	case 0x10, 0x1b5b41: // Ctrl+P or Up arrow
		if t.settingsCursor > 0 {
			t.settingsCursor--
			t.render()
		}
		return true
	case '\r', '\n': // Enter — act on the selected row
		items := t.settingsPanelItems(cfg)
		if t.settingsCursor < 0 || t.settingsCursor >= len(items) {
			return true
		}
		t.settingsOpen = false
		t.settingsCfg = nil
		switch t.settingsCursor {
		case 0: // model → open the interactive model picker
			t.showModelPicker()
		case 2: // mode → cycle plan/agent/yolo/auto
			t.cycleMode()
		case 3: // language → cycle zh-CN/zh-TW/en
			t.handleSlash("/lang")
		case 4: // theme → toggle dark/light
			t.handleSlash("/theme")
		case 1, 5, 6, 7: // provider & voice credentials → full config editor
			t.handleSlash("/config")
		default:
			t.add(RoleSystem, t.tstr("settings.soon"))
		}
		t.render()
		return true
	}
	return true
}
