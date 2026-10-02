package cmd

import (
	"fmt"
	"runtime"

	"github.com/ponygates/icode/internal/config"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "显示版本信息",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("icode v%s (%s/%s)\n", appVersion, runtime.GOOS, runtime.GOARCH)
		if appCommit != "" && appCommit != "unknown" {
			fmt.Printf("commit: %s\n", appCommit)
		}
		if appBuild != "" && appBuild != "unknown" {
			fmt.Printf("build:  %s\n", appBuild)
		}
		fmt.Printf("config: %s\n", config.DefaultPath())
	},
}
