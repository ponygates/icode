//go:build !windows

package cmd

// fixConsoleCodepage is a no-op on non-Windows platforms, which already use
// UTF-8 locales by default.
func fixConsoleCodepage() {}
