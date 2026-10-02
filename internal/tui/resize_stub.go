//go:build !windows

package tui

// compactConsoleBuffer is a no-op on non-Windows platforms: Unix terminals
// have no separate screen-buffer/viewport split, so ANSI positioning is
// always viewport-relative. The Windows implementation is in
// resize_windows.go.
func compactConsoleBuffer() {}
