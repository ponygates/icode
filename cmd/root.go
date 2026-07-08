// Package cmd provides the Cobra CLI entry points for iCode.
package cmd

import (
	"fmt"

	"github.com/ponygates/icode/internal/config/i18n"
	"github.com/spf13/cobra"
)

var (
	appVersion string
	appBuild   string
	appCommit  string
)

// Execute is the main entry point for the CLI.
func Execute(version, build, commit string) error {
	appVersion = version
	appBuild = build
	appCommit = commit
	return rootCmd.Execute()
}

var rootCmd = &cobra.Command{
	Use:   "icode",
	Short: i18n.Tr("app.tagline"),
	Long: fmt.Sprintf(`%s — %s

A multi-model AI coding agent that supports:
  • 50+ LLM providers across China & worldwide
  • Cache-first token optimization (up to 94% savings)
  • Native zh-CN / zh-TW / en interface
  • CLI (TUI) + Electron desktop dual experience

Type 'icode chat' to start an interactive session.
`, i18n.Tr("app.name"), i18n.Tr("app.tagline")),
	Version: appVersion,
}

func init() {
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(modelCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(serverCmd)

	// Persistent flags
	rootCmd.PersistentFlags().StringP("lang", "l", "zh-CN", "Language (zh-CN, zh-TW, en)")
	rootCmd.PersistentFlags().StringP("provider", "p", "", "Default LLM provider")
	rootCmd.PersistentFlags().StringP("model", "m", "", "Default model ID")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose logging")
}
