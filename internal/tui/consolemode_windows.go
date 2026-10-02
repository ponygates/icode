//go:build windows

package tui

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// Console input mode flags (Win32).
const (
	ciEnableEchoInput      = 0x0004
	ciEnableLineInput      = 0x0002
	ciEnableProcessedInput = 0x0001
)

// lastEchoDiag rate-limits the "ECHO revived" diagnostic line so a runaway
// revival loop can't flood console-diag.log.
var lastEchoDiag time.Time

// hardenConsoleInput re-applies the raw-input flags that term.MakeRaw should
// have set, directly via SetConsoleMode. Some Windows console configurations
// (ConHost-backed sessions, IME wrappers) have been observed to keep or
// revive ECHO/LINE bits — ConHost then echoes every keypress at the
// PHYSICAL cursor position, so characters land wherever the last paint left
// the cursor (the "typed text appears in the log area" bug).
//
// Returns true when a revival was detected and corrected, so callers can
// force a full repaint to wash off any characters conhost already echoed
// into the conversation area.
func hardenConsoleInput() bool {
	var mode uint32
	h := windows.Handle(os.Stdin.Fd())
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	revived := mode&(ciEnableEchoInput|ciEnableLineInput) != 0
	if revived && time.Since(lastEchoDiag) > 2*time.Second {
		lastEchoDiag = time.Now()
		diagf("ECHO/LINE revived: stdin mode=%#04x — re-clearing", mode)
	}
	clean := mode &^ (ciEnableEchoInput | ciEnableLineInput | ciEnableProcessedInput)
	_ = windows.SetConsoleMode(h, clean)
	return revived
}

// diagInputMode snapshots the stdin console mode into console-diag.log —
// one line per session start (and around suspend/resume) to prove whether
// ECHO was actually off when the TUI began.
func diagInputMode(tag string) {
	var mode uint32
	h := windows.Handle(os.Stdin.Fd())
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		diagf("inputmode[%s]: GetConsoleMode err=%v", tag, err)
		return
	}
	echo := 0
	if mode&ciEnableEchoInput != 0 {
		echo = 1
	}
	line := 0
	if mode&ciEnableLineInput != 0 {
		line = 1
	}
	diagf("inputmode[%s]: mode=%#04x echo=%d line=%d", tag, mode, echo, line)
}

// coEnableVirtualTerminalProcessing is the output-handle console mode flag
// that makes conhost parse ANSI/VT escape sequences (Win10 1511+).
const coEnableVirtualTerminalProcessing = 0x0004

// vtProcessingActive reports whether the output side of the terminal
// actually parses ANSI/VT sequences. cmd's fixConsoleCodepage enables the
// bit at startup, but on legacy consoles that predates VT support — or when
// the SetConsoleMode call silently failed — the bit stays off, and a
// full-screen TUI would then stream its frames as literal text. The caller
// uses this to decide between the full-screen renderer and line mode.
func vtProcessingActive() bool {
	var mode uint32
	h := windows.Handle(os.Stdout.Fd())
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		// Not a console handle at all (redirected?) — fall back to the
		// caller's normal terminal detection; treating VT as available
		// keeps behaviour unchanged for non-conhost terminals.
		return true
	}
	return mode&coEnableVirtualTerminalProcessing != 0
}
