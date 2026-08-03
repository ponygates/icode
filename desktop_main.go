//go:build windows && desktop_only

// Desktop-only build: launches iCode desktop mode directly.
// Build with: go build -ldflags="-s -w -H windowsgui" -o icode-desktop.exe -tags desktop_only .
package main

import (
	"fmt"
	"os"

	"github.com/ponygates/icode/cmd"
)

func main() {
	if err := cmd.ExecuteDesktop(Version, BuildTime, GitCommit); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
