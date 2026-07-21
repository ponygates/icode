//go:build windows

package tui

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleScreenBufferSize = kernel32.NewProc("SetConsoleScreenBufferSize")
	procSetConsoleWindowInfo       = kernel32.NewProc("SetConsoleWindowInfo")
	procSetConsoleTitle            = kernel32.NewProc("SetConsoleTitleW")
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

// resizeTerminalWindows resizes the console window via Win32 API.
// Called by resizeTerminal() as a fallback after the ANSI escape.
func resizeTerminalWindows(cols, rows int) {
	handle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil || handle == syscall.InvalidHandle {
		return
	}

	// Get current buffer info.
	var csbi _CONSOLE_SCREEN_BUFFER_INFO
	ret, _, _ := procGetConsoleScreenBufferInfo.Call(
		uintptr(handle), uintptr(unsafe.Pointer(&csbi)),
	)
	if ret == 0 {
		return
	}

	// Expand buffer if too small (must be ≥ window size).
	bufX := int16(cols)
	bufY := int16(rows + 200) // extra scrollback
	if bufX < csbi.Size.X {
		bufX = csbi.Size.X
	}
	if bufY < csbi.Size.Y {
		bufY = csbi.Size.Y
	}
	// COORD is passed as a 32-bit value: LOWORD=X, HIWORD=Y.
	bufSize := uint32(uint16(bufX)) | (uint32(uint16(bufY)) << 16)
	procSetConsoleScreenBufferSize.Call(uintptr(handle), uintptr(bufSize))

	// Set window (visible viewport).
	rect := _SMALL_RECT{
		Left:   0,
		Top:    0,
		Right:  int16(cols) - 1,
		Bottom: int16(rows) - 1,
	}
	procSetConsoleWindowInfo.Call(
		uintptr(handle), 1, // TRUE = absolute coordinates
		uintptr(unsafe.Pointer(&rect)),
	)

	// Update window title.
	title, _ := syscall.UTF16PtrFromString("iCode")
	if title != nil {
		procSetConsoleTitle.Call(uintptr(unsafe.Pointer(title)))
	}
}
