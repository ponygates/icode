//go:build !windows

package cmd

// fixConsoleCodepage is a no-op on non-Windows platforms, which already use
// UTF-8 locales by default.
func fixConsoleCodepage() {}

// setupConsoleIO is a no-op on non-Windows platforms: they use console-
// subsystem binaries with inherited stdio, so a terminal is always available.
// Returns true to keep the CLI dispatch path in Execute.
func setupConsoleIO() bool { return true }

// allocConsole is a no-op on non-Windows platforms (setupConsoleIO always
// returns true there, so the double-click branch in Execute is never taken).
func allocConsole() bool { return false }

// showCLIMessage is a no-op on non-Windows.
func showCLIMessage() {}
