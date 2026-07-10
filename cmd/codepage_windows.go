//go:build windows

package cmd

import (
	"syscall"
	"unsafe"
)

const (
	enableVirtualTerminalProcessing = 0x0004
	stdOutputHandle                 = ^uintptr(0) - 10 // -11
)

// fixConsoleCodepage configures the Windows console for iCode:
//  1. Sets input/output code pages to UTF-8 (65001) so Unicode UI glyphs
//     (◆ ▸ » ● ×) and Chinese user input render correctly instead of as
//     mojibake in the default GBK (CP936) console.
//  2. Enables virtual-terminal processing so ANSI escape sequences (colors,
//     cursor movement) used by the full-screen TUI render correctly.
//
// Both changes are per-process and revert when the program exits, leaving the
// user's shell untouched.
func fixConsoleCodepage() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	setCP := kernel32.NewProc("SetConsoleCP")
	setOutCP := kernel32.NewProc("SetConsoleOutputCP")
	if setCP.Find() == nil {
		setCP.Call(65001)
	}
	if setOutCP.Find() == nil {
		setOutCP.Call(65001)
	}

	getStdHandle := kernel32.NewProc("GetStdHandle")
	getConsoleMode := kernel32.NewProc("GetConsoleMode")
	setConsoleMode := kernel32.NewProc("SetConsoleMode")

	hOut, _, _ := getStdHandle.Call(stdOutputHandle)
	if hOut == 0 || hOut == ^uintptr(0) {
		return
	}
	var mode uint32
	r, _, _ := getConsoleMode.Call(hOut, uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return
	}
	mode |= enableVirtualTerminalProcessing
	setConsoleMode.Call(hOut, uintptr(mode))
}
