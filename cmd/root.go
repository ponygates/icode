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

	// When launched by double-clicking in Explorer (not from a terminal),
	// automatically start desktop mode on Windows, or show a message on
	// other platforms. This makes the binary feel like a proper desktop app.
	if isDoubleClicked() {
		return runDesktop()
	}

	// Ensure the Windows console uses UTF-8 so Unicode UI glyphs and Chinese
	// input render correctly instead of as mojibake.
	fixConsoleCodepage()
	return rootCmd.Execute()
}

var rootCmd = &cobra.Command{
	Use:   "icode",
	Short: i18n.Tr("app.tagline"),
	Long: fmt.Sprintf(`%s — %s

A multi-model AI coding agent that supports:
  • 50+ LLM providers across China & worldwide
  • Cache-first token optimization (up to 94%% savings)
  • Native zh-CN / zh-TW / en interface
  • CLI (TUI) + Electron desktop dual experience

Just run 'icode' (or double-click the executable) to start an interactive
chat session. Use 'icode chat' for the same, or 'icode --help' for all
commands.
`, i18n.Tr("app.name"), i18n.Tr("app.tagline")),
	Version: appVersion,
	// When launched with no subcommand — e.g. by double-clicking the
	// executable — drop straight into the chat so the app actually runs
	// instead of printing help and immediately closing the window.
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, _ := cmd.Flags().GetString("provider")
		model, _ := cmd.Flags().GetString("model")
		return startChat(provider, model, "")
	},
}

func init() {
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(desktopCmd)
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
