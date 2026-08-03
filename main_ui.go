//go:build simpleui && !desktop_only

// Simple-UI build: launches the WebView2 lightweight chat window — the
// "CLI 的 UI 版" (double-click-friendly, native scrollbar + mouse wheel).
// Build with:
//
//	go build -ldflags="-s -w -H windowsgui" -tags simpleui -o icode-cli.exe .
//
// The plain CLI (TUI, icode.exe) and the desktop build (icode-desktop.exe)
// are separate binaries — see main.go and desktop_main.go.
package main

import (
	"fmt"
	"os"

	"github.com/ponygates/icode/cmd"
)

func main() {
	if err := cmd.ExecuteSimpleUI(Version, BuildTime, GitCommit); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
