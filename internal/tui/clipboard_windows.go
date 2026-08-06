//go:build windows

package tui

import (
	"syscall"
	"unsafe"
)

// Windows Win32 clipboard functions. x/sys/windows does not vendor these, so we
// bind them directly via LazyDLL. The handle returned by GetClipboardData must
// NOT be freed by us (it belongs to the clipboard) — only the global lock is
// released.
var (
	clipUser32          = syscall.NewLazyDLL("user32.dll")
	clipKernel32        = syscall.NewLazyDLL("kernel32.dll")
	procOpenClipboard    = clipUser32.NewProc("OpenClipboard")
	procCloseClipboard   = clipUser32.NewProc("CloseClipboard")
	procGetClipboardData = clipUser32.NewProc("GetClipboardData")
	procGlobalLock       = clipKernel32.NewProc("GlobalLock")
	procGlobalUnlock     = clipKernel32.NewProc("GlobalUnlock")
	procGlobalSize       = clipKernel32.NewProc("GlobalSize")
)

const cfUnicodeText = 13 // CF_UNICODETEXT

// readClipboard returns the current clipboard text via the Win32 clipboard API
// (CF_UNICODETEXT). Returns ("", nil) when the clipboard is empty, holds a non
// text format, or cannot be locked — never blocks the TUI on a modal clipboard
// owner for long.
func readClipboard() (string, error) {
	r1, _, _ := procOpenClipboard.Call(0)
	if r1 == 0 {
		// Clipboard already open by another window — skip the paste.
		return "", nil
	}
	defer procCloseClipboard.Call()

	handle, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if handle == 0 {
		return "", nil
	}
	size, _, _ := procGlobalSize.Call(handle)
	if size == 0 {
		return "", nil
	}
	ptr, _, _ := procGlobalLock.Call(handle)
	if ptr == 0 {
		return "", nil
	}
	defer procGlobalUnlock.Call(handle)

	// CF_UNICODETEXT data is NUL-terminated UTF-16; slice the locked memory and
	// decode it. The global handle remains locked until GlobalUnlock.
	u16 := (*[1 << 30]uint16)(unsafe.Pointer(ptr))[:int(size)/2]
	return syscall.UTF16ToString(u16), nil
}