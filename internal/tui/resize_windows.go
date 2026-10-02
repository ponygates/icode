//go:build windows

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/ponygates/icode/internal/core/privacy"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleScreenBufferSize = kernel32.NewProc("SetConsoleScreenBufferSize")
	procSetConsoleCursorPosition   = kernel32.NewProc("SetConsoleCursorPosition")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

type _COORD struct {
	X, Y int16
}

type _SMALL_RECT struct {
	Left, Top, Right, Bottom int16
}

type _CONSOLE_SCREEN_BUFFER_INFO struct {
	Size              _COORD
	CursorPosition    _COORD
	Attributes        uint16
	Window            _SMALL_RECT
	MaximumWindowSize _COORD
}

// diagf appends one timestamped line to ~/.icode/console-diag.log so the
// conhost "typed text lands above the input box" bug can be diagnosed from a
// single reproduction. Kept tiny (no rotation): 256 KB cap, then rewrite.
func diagf(format string, args ...interface{}) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, ".icode", "console-diag.log")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if fi, e := os.Stat(path); e == nil && fi.Size() > 256*1024 {
		_ = os.Remove(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	// console-diag.log captures raw key events among other diagnostics; a
	// pasted secret in a keystroke dump must not survive to disk in the clear.
	_, _ = fmt.Fprintf(privacy.NewRedactingWriter(f), "[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

// compactConsoleBuffer shrinks the console screen buffer to the visible
// viewport height.
//
// Legacy conhost addresses ANSI cursor-position sequences against the SCREEN
// BUFFER, not the viewport. The default buffer is thousands of lines tall
// (9001 on a stock Windows profile), while the visible window is ~30 rows —
// so an absolute move to row 1 lands at the top of the scrollback, far above
// what the user sees, and every full-screen frame paints off-viewport. The
// IME/echo cursor follows the same physical position, which is exactly the
// "typed text lands in the blank area above the input box" bug on cmd.exe /
// PowerShell windows.
//
// Collapsing the buffer to the viewport height makes buffer coordinates and
// viewport coordinates identical, which is the standard fix recommended by
// Microsoft's "classic terminal apps" guidance (and what Claude Code /
// bubbletea-class TUIs effectively get from Windows Terminal, which keeps no
// scroll buffer at all).
//
// Called right after entering the 1049 alternate screen, so on conhost builds
// that support it the shrink applies to the alt buffer and the user's main
// scrollback survives the session. On terminals without a scroll buffer
// (Windows Terminal, VS Code, etc.) the call is a no-op because
// Size.Y already equals the viewport height.
func compactConsoleBuffer() {
	handle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil || handle == syscall.InvalidHandle {
		diagf("compact: GetStdHandle failed err=%v", err)
		return
	}

	var csbi _CONSOLE_SCREEN_BUFFER_INFO
	ret, _, _ := procGetConsoleScreenBufferInfo.Call(
		uintptr(handle), uintptr(unsafe.Pointer(&csbi)),
	)
	if ret == 0 {
		diagf("compact: GetConsoleScreenBufferInfo failed")
		return
	}

	winW := int(csbi.Window.Right - csbi.Window.Left + 1)
	winH := int(csbi.Window.Bottom - csbi.Window.Top + 1)
	// Environment snapshot + buffer geometry, one line per session start —
	// enough to tell conhost (9001-line buffer) from Windows Terminal
	// (viewport-sized) and to see whether the shrink actually worked.
	diagf("env: wt_session=%q term=%q vt=%d buffer=%dx%d viewport=%dx%d cursor=(%d,%d) maxwin=%dx%d",
		os.Getenv("WT_SESSION"), os.Getenv("TERM"),
		func() int {
			var m uint32
			if e := windows.GetConsoleMode(windows.Handle(handle), &m); e != nil {
				return -1
			}
			if m&coEnableVirtualTerminalProcessing != 0 {
				return 1
			}
			return 0
		}(),
		csbi.Size.X, csbi.Size.Y, winW, winH,
		csbi.CursorPosition.X, csbi.CursorPosition.Y,
		csbi.MaximumWindowSize.X, csbi.MaximumWindowSize.Y)

	if winH <= 0 || int(csbi.Size.Y) <= winH {
		diagf("compact: buffer already viewport-sized, skip")
		return // buffer already viewport-sized (Windows Terminal etc.)
	}

	// The cursor must sit inside the target buffer or SetConsoleScreenBuffer
	// Size fails; park it at the origin first. The render that follows
	// repositions it inside the input box anyway.
	origin := uint32(uint16(0)) | (uint32(uint16(0)) << 16) // COORD{0,0}
	procSetConsoleCursorPosition.Call(uintptr(handle), uintptr(origin))

	// COORD is passed as a 32-bit value: LOWORD=X, HIWORD=Y.
	bufSize := uint32(uint16(csbi.Size.X)) | (uint32(uint16(winH)) << 16)
	ret2, _, _ := procSetConsoleScreenBufferSize.Call(uintptr(handle), uintptr(bufSize))
	if ret2 == 0 {
		diagf("compact: SetConsoleScreenBufferSize(%d,%d) FAILED", csbi.Size.X, winH)
	} else {
		diagf("compact: SetConsoleScreenBufferSize(%d,%d) ok", csbi.Size.X, winH)
	}
}
