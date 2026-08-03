//go:build !windows || nogui

package cmd

// runSimpleUI is unavailable outside Windows (or in the nogui build). The
// double-click branch in Execute() is only taken on Windows, so this stub is
// only here to keep the package compiling; it falls back to the desktop app.
func runSimpleUI() error {
	return runDesktop()
}
