//go:build !windows

package tui

// hardenConsoleInput is a no-op on non-Windows: term.MakeRaw fully covers the
// echo/line flags on POSIX terminals.
func hardenConsoleInput() {}
