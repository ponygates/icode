//go:build !desktop_only

package main

import (
	"fmt"
	"os"

	"github.com/ponygates/icode/cmd"
)

// main is the CLI build (icode.exe). The desktop build (icode-desktop.exe)
// is a separate binary — see desktop_main.go (-tags desktop_only).
func main() {
	if err := cmd.Execute(Version, BuildTime, GitCommit); err != nil {
		fmt.Fprintf(os.Stderr, "icode error: %v\n", err)
		os.Exit(1)
	}
}
