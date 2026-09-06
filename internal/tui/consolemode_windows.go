//go:build windows

package tui

import (
	"golang.org/x/sys/windows"
	"os"
)

// Console input mode flags (Win32).
const (
	ciEnableEchoInput      = 0x0004
	ciEnableLineInput      = 0x0002
	ciEnableProcessedInput = 0x0001
)

// hardenConsoleInput re-applies the raw-input flags that term.MakeRaw should
// have set, directly via SetConsoleMode. Some Windows console configurations
// (ConHost-backed Windows Terminal sessions, IME wrappers) have been observed
// to keep ECHO/LINE bits alive — ConHost then echoes every keypress at the
// PHYSICAL cursor position, so characters land wherever the last paint left
// the cursor (the "typed text appears in the log area" bug).
func hardenConsoleInput() {
	var mode uint32
	h := windows.Handle(os.Stdin.Fd())
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return
	}
	clean := mode &^ (ciEnableEchoInput | ciEnableLineInput | ciEnableProcessedInput)
	_ = windows.SetConsoleMode(h, clean)
}
