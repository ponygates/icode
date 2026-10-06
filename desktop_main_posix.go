//go:build !windows && desktop_only

// Desktop-only build for macOS/Linux: launches iCode desktop mode
// directly, mirroring desktop_main.go — which is Windows-only purely
// because that build also passes -H windowsgui to hide the console
// (see the Desktop matrix in .github/workflows/build.yml; the CI build
// step already skips the windowsgui flag off Windows).
// Build with: go build -ldflags="-s -w" -o icode-desktop -tags desktop_only .
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
