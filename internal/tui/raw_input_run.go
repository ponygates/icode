package tui

import (
	"bufio"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"golang.org/x/term"
)

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

	// Terminal title (Claude Code parity): OSC 0 sets both tab and window
	// title. Works on Windows Terminal, conhost (VT enabled), and every
	// xterm-family terminal; unknown sequences are ignored harmlessly.
	// The title then flips dynamically per turn state (see setTermTitle):
	// ⏳ generating / ⚠ waiting for approval / idle.
	if cwd, err := os.Getwd(); err == nil {
		t.titleDir = shortDir(cwd)
	}
	t.setTermTitle("")
	defer t.setTermTitle("")

	// Legacy conhost (cmd.exe / PowerShell windows) positions ANSI cursor
	// moves against the SCREEN BUFFER, whose default height (~9001 lines) is
	// far taller than the viewport — absolute rows would land deep in the
	// scrollback, painting the frame (and the echo/IME cursor) off-screen:
	// the "typed text appears above the input box" bug. Collapsing the
	// buffer to the viewport height makes row 1 the top of what the user
	// actually sees. No-op on Windows Terminal / Unix, where positioning is
	// already viewport-relative.
	compactConsoleBuffer()

	// Turn on SGR mouse tracking + bracketed paste while the full-screen TUI
	// is active so clicks/drags/wheel/large pastes work; restore on exit.
	// mouseOn came from config (New) — /mouse may have flipped it since.
	t.setMouseTracking(t.mouseOn)
	defer t.disableMouse()

	// Immediately re-measure the terminal size before the first render.
	// The measurement in Run() can be stale on Windows where GetConsoleScreen-
	// BufferInfo may return cached values from before the alternate-screen
	// switch. Using termSize() (stdin+stdout) gives the most reliable result.
	if w, h, ok := t.termSize(); ok {
		t.width, t.height = w, h
	}
	// Also record the initial dimensions as last-rendered so the first render
	// doesn't spuriously trigger a full clear.
	t.lastRenderW, t.lastRenderH = t.width, t.height
	t.render()
	go t.watchResize()
	t.reader = bufio.NewReader(t.reader)
	// rawBytePump is the ONLY goroutine that reads from t.reader; it feeds
	// raw bytes to keyPump over byteCh, which reassembles escape sequences
	// (pumpEscape) and dispatches decoded keys over keyCh.
	go t.rawBytePump()
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
				traceKeySite("main", rr)
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
		// Re-assert raw input flags every cycle: legacy conhost partially
		// resets the console mode on buffer/window mutations and some IME
		// wrappers re-enable ECHO behind our back — once ECHO is live again,
		// conhost echoes every keystroke at the physical cursor and typed
		// text lands outside the input box. On revival detection, force a
		// full repaint so anything conhost already echoed into the
		// conversation area gets washed off. No-op on non-Windows.
		if hardenConsoleInput() {
			t.fullRepaintSoon()
		}
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

// suspendRaw leaves the alternate screen and restores cooked mode so an
// external program (e.g. $EDITOR) can use the terminal normally.
func (t *TUI) suspendRaw() {
	if !t.rawMode || t.rawState == nil {
		return
	}
	fmt.Fprint(t.writer, "\x1b[?25h\x1b[?1049l")
	_ = term.Restore(int(os.Stdin.Fd()), t.rawState)
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
	// $EDITOR may have left ECHO on; re-assert raw flags before repainting.
	hardenConsoleInput()
	fmt.Fprint(t.writer, "\x1b[?1049h\x1b[2J\x1b[H")
	t.render()
}
