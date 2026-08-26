//go:build !windows

package tool

// foregroundWindow is unavailable off-Windows; screen_read degrades to the
// capture alone.
func foregroundWindow() (title, proc string) { return "", "" }
