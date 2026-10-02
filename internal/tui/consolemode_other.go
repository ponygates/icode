//go:build !windows

package tui

// hardenConsoleInput is a no-op on non-Windows: term.MakeRaw fully covers the
// echo/line flags on POSIX terminals. Always reports "no revival".
func hardenConsoleInput() bool { return false }

// diagInputMode is a no-op on non-Windows (no Win32 console modes to dump).
func diagInputMode(string) {}

// vtProcessingActive is always true on non-Windows: POSIX terminals parse
// ANSI sequences natively, so the full-screen renderer is always safe.
func vtProcessingActive() bool { return true }
