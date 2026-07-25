//go:build nogui

// When built with `-tags nogui`, the real desktop implementation
// (desktop_windows.go / desktop_posix.go / tray_*.go / desktop_common.go) is
// excluded so the binary can be cross-compiled as a pure-Go headless CLI
// without CGO (systray / WebView2 / global hotkey). We still provide the
// symbols root.go references so the main package compiles, but they are
// no-ops: the `desktop` subcommand is hidden and tells the user it is absent.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// desktopCmd is a hidden placeholder so `rootCmd.AddCommand(desktopCmd)` keeps
// working; the real command only exists in non-nogui builds.
var desktopCmd = &cobra.Command{
	Use:    "desktop",
	Short:  "Launch the desktop app (not available in this build)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("desktop mode is not compiled into this build (built with -tags nogui)")
		return nil
	},
}

// runDesktop is the stub referenced by ExecuteDesktop under the nogui build.
func runDesktop() error {
	fmt.Println("desktop mode is not compiled into this build (built with -tags nogui)")
	return nil
}
