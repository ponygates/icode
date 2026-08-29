// Package cmd provides the Cobra CLI entry points for iCode.
package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/ponygates/icode/internal/config/i18n"
	"github.com/ponygates/icode/internal/update"
	"github.com/spf13/cobra"
)

var (
	appVersion string
	appBuild   string
	appCommit  string

	// weAllocatedConsole is true when Execute() created a fresh console for a
	// double-clicked GUI-subsystem binary. It lets us keep that console window
	// open (pause on exit) so the user can read any error instead of the window
	// vanishing the instant the process ends.
	weAllocatedConsole bool
)

// ExecuteDesktop launches the desktop-only build.
// version/build/commit come from the main package (set via ldflags) so the
// desktop binary reports the same version as the CLI instead of a stale
// hardcoded constant.
func ExecuteDesktop(version, build, commit string) error {
	appVersion = version
	appBuild = build
	appCommit = commit
	return runDesktop()
}

// ExecuteSimpleUI launches the lightweight WebView2 chat window (the "CLI 的
// UI 版"). It is built as a separate binary with -tags simpleui (see
// main_ui.go), so the CLI binary never auto-opens a GUI window.
func ExecuteSimpleUI(version, build, commit string) error {
	appVersion = version
	appBuild = build
	appCommit = commit
	return runSimpleUI()
}

// Execute is the main entry point for the CLI.
func Execute(version, build, commit string) (err error) {
	appVersion = version
	appBuild = build
	appCommit = commit

	// Keep a fresh double-click console window open on a fatal error/panic so
	// the user can read what went wrong instead of the window flashing away.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("icode 内部错误: %v", r)
			fmt.Fprintf(os.Stderr, "\n⚠ 发生了内部错误: %v\n", r)
		}
		if err != nil && weAllocatedConsole {
			fmt.Fprintln(os.Stderr, "\n按 Enter 键退出...")
			bufio.NewReader(os.Stdin).ReadBytes('\n')
		}
	}()

	// The binary is linked as a GUI-subsystem app on Windows so double-clicking
	// it never flashes a console window. setupConsoleIO restores CLI stdio when
	// we were actually launched from a terminal (attaching to the parent
	// console) and reports whether a console is available.
	consoleReady := setupConsoleIO()

	// No console AND no CLI arguments means a genuine Explorer double-click.
	// Allocate a fresh console and run the enhanced TUI (CLI). The desktop app
	// stays reachable via `icode desktop` / `icode-desktop.exe` (the
	// windows+desktop_only build). Only if allocation fails do we fall back to
	// the desktop app.
	if !consoleReady && len(os.Args) <= 1 {
		if allocConsole() {
			weAllocatedConsole = true
			fixConsoleCodepage()
			return rootCmd.Execute()
		}
		return runDesktop()
	}

	// Console available: run the TUI. (The WebView2 simple-UI and the desktop
	// are now separate binaries — see -tags simpleui / desktop_only — so this
	// CLI binary never auto-launches a GUI window on double-click.)
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
		// Non-interactive print mode (Claude Code `-p` parity): run a single
		// prompt to completion, print the result, exit. Scripts/CI friendly.
		prompt, _ := cmd.Flags().GetString("print")
		if strings.TrimSpace(prompt) != "" {
			outFmt, _ := cmd.Flags().GetString("output-format")
			cont, _ := cmd.Flags().GetBool("continue")
			resumeID, _ := cmd.Flags().GetString("resume")
			mode, _ := cmd.Flags().GetString("mode")
			return runPrintMode(prompt, outFmt, cont, resumeID, provider, model, mode)
		}
		return startChat(provider, model, "")
	},
}

func init() {
	// Clean up the .old binary left by a previous `icode upgrade`.
	update.CleanupOldBinary()

	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(desktopCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(modelCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(upgradeCmd)

	// Persistent flags
	rootCmd.PersistentFlags().StringP("lang", "l", "zh-CN", "Language (zh-CN, zh-TW, en)")
	rootCmd.PersistentFlags().StringP("provider", "p", "", "Default LLM provider")
	rootCmd.PersistentFlags().StringP("model", "m", "", "Default model ID")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose logging")
	// Print (non-interactive) mode — Claude Code `-p` parity. Note: -p is
	// already bound to --provider, so print uses -P / --print to avoid
	// breaking existing scripts.
	rootCmd.PersistentFlags().StringP("print", "P", "", "Print mode: run one prompt non-interactively, print the result and exit")
	rootCmd.PersistentFlags().String("output-format", "text", "Output format in print mode: text | json | stream-json")
	rootCmd.PersistentFlags().BoolP("continue", "c", false, "In print mode, continue the most recent session")
	rootCmd.PersistentFlags().String("resume", "", "In print mode, resume the given session ID")
	rootCmd.PersistentFlags().String("mode", "", "Permission mode: plan | agent | auto | yolo")
}
