//go:build !windows || nogui

package cmd

// killStaleWebViewProcesses is a no-op on non-Windows platforms and nogui
// builds — the WebView2 zombie-lock problem is Windows-only.
func killStaleWebViewProcesses(dataPath string) {}
