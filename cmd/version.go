package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the iCode version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("icode %s\n", appVersion)
		if appBuild != "" && appBuild != "unknown" {
			fmt.Printf("build:  %s\n", appBuild)
		}
		if appCommit != "" && appCommit != "unknown" {
			fmt.Printf("commit: %s\n", appCommit)
		}
	},
}
