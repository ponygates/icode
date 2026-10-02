//go:build !windows

package tui

// dumpScreen is a Windows-only diagnostic; nothing to snapshot elsewhere.
// Returns nil (no rows read) so callers fall back to state-only dumping.
func dumpScreen() []string { return nil }
