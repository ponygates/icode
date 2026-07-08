package main

import (
	"fmt"
	"os"

	"github.com/ponygates/icode/cmd"
)

func main() {
	if err := cmd.Execute(Version, BuildTime, GitCommit); err != nil {
		fmt.Fprintf(os.Stderr, "icode error: %v\n", err)
		os.Exit(1)
	}
}
