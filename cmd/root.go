// Package cmd provides the Cobra CLI entry points for iCode.
package cmd

import (
	"fmt"
	"os"

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

	// The binary is linked as a GUI-subsystem app on Windows so double-clicking
	// it never flashes a console window. setupConsoleIO restores CLI stdio when
	// we were actually launched from a terminal (attaching to the parent
	// console) and reports whether a console is available.
	consoleReady := setupConsoleIO()

	// No console AND no CLI arguments means a genuine Explorer double-click:
	// start desktop mode so the binary feels like a proper desktop app. Any
	// explicit subcommand/flag (e.g. `icode desktop`, `icode chat`) falls
	// through to the normal Cobra dispatch below.
	if !consoleReady && len(os.Args) <= 1 {
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
