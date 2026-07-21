//go:build !windows

package tui

// resizeTerminalWindows is a no-op on non-Windows platforms.
// The Windows implementation is in resize_windows.go.
func resizeTerminalWindows(cols, rows int) {}
