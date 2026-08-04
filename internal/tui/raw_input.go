package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

// ── Raw mode (full screen) ───────────────────────────────────────

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
			rr, _, err := t.reader.(*bufio.Reader).ReadRune()
			if err != nil {
				if err == io.EOF {
					t.running = false
					return
				}
				time.Sleep(100 * time.Millisecond)
				return
			}
			if !t.handleKey(rr) {
				t.running = false
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
	// While the reverse-history-search overlay (Ctrl+R) is active, every key
	// is routed to the search handler — mirroring Claude Code's isearch.
	if t.searchMode {
		return t.handleSearchKey(r)
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
			fmt.Fprint(t.writer, "\r\n")
			t.running = false
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
	case 0x0f: // Ctrl+O — dismiss welcome
		if t.welcomeVisible {
			t.dismissWelcome()
			return true
		}
	case 0x01: // Ctrl+A — home
		t.cursor = 0
		return true
	case 0x05: // Ctrl+E — end
		t.cursor = len([]rune(t.inputBuf))
		return true
	case 0x17: // Ctrl+W — delete word backward
		t.deleteWordBackward()
		t.updateSuggestions()
		return true
	case 0x15: // Ctrl+U — delete to line start
		t.deleteToLineStart()
		t.updateSuggestions()
		return true
	case 0x10: // Ctrl+P — history prev OR move suggestion cursor up
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
		if len(t.models) > 1 && !t.streaming {
			// Cycle to next model
			t.modelIdx = (t.modelIdx + 1) % len(t.models)
			t.model = t.models[t.modelIdx]
			t.add(RoleSystem, "Tab -> "+t.model)
			return true
		}
		return true
	case 0x1b:
		// Escape sequences: arrow keys, mouse reports, bracketed paste, and
		// the Shift+Tab mode cycle. The first byte after ESC decides which.
		// Without buffering these (reading each as a separate rune) stray "["
		// / "A" characters would leak into the input buffer — the classic
		// "garbled text on up/down" bug.
		if br, ok := t.reader.(*bufio.Reader); ok {
			ur, _, err := br.ReadRune()
			if err != nil {
				// Lone Esc (no follow-up byte): cancel the model picker if
				// open, otherwise switch back from vim normal mode.
				if t.modelPickerOpen {
					t.closeModelPicker()
				} else if t.vimMode && !t.vimInsert {
					t.vimInsert = true
					t.render()
				}
				return true
			}
			// Alt+Enter (or Alt+Return): submit current input.
			if ur == '\r' || ur == '\n' {
				text := strings.TrimSpace(t.inputBuf)
				t.inputBuf = ""
				t.cursor = 0
				if text != "" {
					t.pushHistory(text)
					t.submit(text)
				}
				return true
			}
			// Plain Esc (or any non-CSI key) cancels the model picker.
			if t.modelPickerOpen && ur != '[' {
				t.closeModelPicker()
				return true
			}
			if ur == '[' {
				// CSI sequence: read the parameter/command byte.
				c1, _, e2 := br.ReadRune()
				if e2 != nil {
					return true
				}
				switch c1 {
				case 'A': // ↑ history prev OR move suggestion cursor up (Claude Code)
					if t.modelPickerOpen {
						t.movePicker(-1)
						return true
					}
					if t.acOpen && len(t.acItems) > 0 {
						if t.acIdx > 0 {
							t.acIdx--
						}
						return true
					}
					t.historyPrev()
					return true
				case 'B': // ↓ history next OR move suggestion cursor down (Claude Code)
					if t.modelPickerOpen {
						t.movePicker(1)
						return true
					}
					if t.acOpen && len(t.acItems) > 0 {
						if t.acIdx < len(t.acItems)-1 {
							t.acIdx++
						}
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
					br.ReadRune() // consume trailing '~'
					t.scrollPgUp()
					return true
				case '6': // PgDn
					br.ReadRune() // consume trailing '~'
					t.scrollPgDn()
					return true
				case 'Z': // Shift+Tab → cycle agent mode
					t.cycleMode()
					return true
				case '<': // SGR mouse report
					t.handleMouse(br)
					return true
				case '2':
					// Bracketed paste begins with "200~"; otherwise it's an
					// unknown CSI we consume and ignore.
					c2, _, e3 := br.ReadRune()
					if e3 == nil && c2 == '0' {
						c3, _, e4 := br.ReadRune()
						if e4 == nil && c3 == '0' {
							c4, _, e5 := br.ReadRune()
							if e5 == nil && c4 == '~' {
								pasted := t.readPaste(br)
								t.dismissWelcome()
								t.insertAtCursor(pasted)
								t.updateSuggestions()
								return true
							}
						}
					}
					for {
						rr, _, ee := br.ReadRune()
						if ee != nil || rr == '~' || rr == 'm' || rr == 'M' {
							break
						}
					}
					return true
				default:
					// Unknown CSI — drain the terminator and ignore.
					for {
						rr, _, ee := br.ReadRune()
						if ee != nil || rr == '~' || rr == 'm' || rr == 'M' {
							break
						}
					}
					return true
				}
			}
			// Alt+<key>: treat the key as a printable insertion (e.g. Alt+b).
			if ur >= 0x20 && ur != 0x7f {
				t.dismissWelcome()
				t.insertAtCursor(string(ur))
				t.updateSuggestions()
				return true
			}
			return true
		}
		// Plain Esc — stop streaming, dismiss panels, or welcome screen.
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
		if t.dismissWelcome() {
			return true
		}
		return true
	case '\r', '\n':
		if t.modelPickerOpen {
			t.selectModelAt(t.modelPickerIdx)
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
		// Auto-complete an incomplete slash-command prefix on Enter: "/c"
		// runs the first matching command (e.g. /compact), a bare "/" never
		// dispatches an empty command.
		if full, ok := t.completeSlashCommand(text); ok {
			text = full
		}
		t.pushHistory(text)
		t.submit(text)
		return true
	case 0x7f, 0x08: // Backspace / DEL
		t.deleteAtCursor()
		t.updateSuggestions()
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

func (t *TUI) deleteAtCursor() {
	runes := []rune(t.inputBuf)
	if t.cursor == 0 || len(runes) == 0 {
		return
	}
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
	// Shell mode (! prefix)
	if strings.HasPrefix(text, "!") {
		t.execShell(text[1:])
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

	// User message — expand @file references first.
	expanded := t.expandFileRefs(text)
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleUser, Content: expanded})
	t.scrollOffset = 0 // auto-follow on new turn
	t.mu.Unlock()

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
			t.callback.OnSend(expanded)
		}()
		t.ensureAnim()
		t.drainStream()
	}
}

func (t *TUI) drainStream() {
	<-t.streamDone
	t.streaming = false
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
	matches := make([]string, 0, len(t.history))
	for i := len(t.history) - 1; i >= 0; i-- {
		if q == "" || strings.Contains(strings.ToLower(t.history[i]), q) {
			matches = append(matches, t.history[i])
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
