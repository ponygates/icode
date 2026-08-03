//go:build !simpleui && !desktop_only

package main

import (
	"fmt"
	"os"

	"github.com/ponygates/icode/cmd"
)

// main is the CLI build (icode.exe). The WebView2 simple-UI (CLI 的 UI 版,
// icode-cli.exe) and the desktop build (icode-desktop.exe) are separate
// binaries — see main_ui.go (-tags simpleui) and desktop_main.go (-tags
// desktop_only).
func main() {
	if err := cmd.Execute(Version, BuildTime, GitCommit); err != nil {
		fmt.Fprintf(os.Stderr, "icode error: %v\n", err)
		os.Exit(1)
	}
}
