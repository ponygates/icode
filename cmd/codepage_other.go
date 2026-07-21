//go:build !windows

package cmd

// fixConsoleCodepage is a no-op on non-Windows platforms, which already use
// UTF-8 locales by default.
func fixConsoleCodepage() {}

// isDoubleClicked is always false on non-Windows (no Explorer double-click).
func isDoubleClicked() bool { return false }

// showCLIMessage is a no-op on non-Windows.
func showCLIMessage() {}
