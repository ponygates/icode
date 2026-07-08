package main

import (
	"fmt"
	"os"

	"github.com/ponygates/icode/cmd"
)

var (
	Version   = "0.1.0-dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

func main() {
	if err := cmd.Execute(Version, BuildTime, GitCommit); err != nil {
		fmt.Fprintf(os.Stderr, "icode error: %v\n", err)
		os.Exit(1)
	}
}
